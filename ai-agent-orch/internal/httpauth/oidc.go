package httpauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OIDCConfig is the minimal configuration for validating ID tokens.
type OIDCConfig struct {
	IssuerURL string // e.g., "https://accounts.google.com"
	ClientID  string // e.g., "my-app-client-id"
}

// OIDCTokenValidator validates Bearer tokens as OIDC id_tokens.
// When OIDC is not configured, it falls back to dev-token checking.
type OIDCTokenValidator struct {
	cfg      OIDCConfig
	devToken string

	mu      sync.RWMutex
	jwks    map[string]any // kid -> JWK
	jwksExp time.Time      // TTL cache expiry

	refreshMu   sync.Mutex
	refreshCh   chan struct{} // singleflight channel
	lastRefresh time.Time     // rate-limit JWKS fetches
}

// Default JWKS cache TTL.
const (
	jwksTTL         = 15 * time.Minute
	jwksMinRefresh  = 1 * time.Minute // minimum interval between JWKS fetches to prevent amplification
	clockSkewLeeway = 60 * time.Second
)

// HTTP client with timeout for OIDC discovery/JWKS requests.
var oidcHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
}

func NewOIDCTokenValidator(cfg OIDCConfig, devToken string) *OIDCTokenValidator {
	return &OIDCTokenValidator{
		cfg:      cfg,
		devToken: devToken,
		jwks:     make(map[string]any),
	}
}

// Validate checks the Authorization header.
// It accepts the exact configured dev token, or an OIDC ID token when OIDC is configured.
func (v *OIDCTokenValidator) Validate(ctx context.Context, header string) (subject string, ok bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimPrefix(header, prefix)
	if token == "" {
		return "", false
	}
	if v.devToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(v.devToken)) == 1 {
		return "local-dev", true
	}

	// If OIDC is configured, attempt JWT validation.
	if v.cfg.IssuerURL != "" && v.cfg.ClientID != "" {
		subject, err := v.validateJWT(ctx, token)
		if err == nil {
			return subject, true
		}
	}

	return "", false
}

func (v *OIDCTokenValidator) validateJWT(ctx context.Context, token string) (string, error) {
	headerB64, payloadB64, sigB64, signingInput, err := splitJWT(token)
	if err != nil {
		return "", err
	}

	// Lazy refresh if we have no keys or cache expired.
	v.mu.RLock()
	hasKeys := len(v.jwks) > 0 && time.Now().UTC().Before(v.jwksExp)
	v.mu.RUnlock()
	if !hasKeys {
		if err := v.refreshKeysSingleflight(ctx); err != nil {
			return "", fmt.Errorf("jwks refresh: %w", err)
		}
	}

	var jwtHeader map[string]any
	if err := json.Unmarshal(headerB64, &jwtHeader); err != nil {
		return "", fmt.Errorf("decode jwt header: %w", err)
	}
	kid, _ := jwtHeader["kid"].(string)
	alg, _ := jwtHeader["alg"].(string)

	v.mu.RLock()
	jwk, ok := v.jwks[kid]
	canRefresh := time.Since(v.lastRefresh) > jwksMinRefresh
	v.mu.RUnlock()
	if !ok {
		if !canRefresh {
			return "", errors.New("unknown jwt signing key (refresh rate limited)")
		}
		// Key rotation may have happened; retry once with fresh fetch.
		if err := v.refreshKeysSingleflight(ctx); err != nil {
			return "", fmt.Errorf("jwks refresh after key miss: %w", err)
		}
		v.mu.RLock()
		jwk, ok = v.jwks[kid]
		v.mu.RUnlock()
		if !ok {
			return "", errors.New("unknown jwt signing key")
		}
	}

	if err := verifySignature(alg, signingInput, sigB64, jwk); err != nil {
		return "", fmt.Errorf("signature verification failed: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payloadB64, &claims); err != nil {
		return "", fmt.Errorf("decode jwt claims: %w", err)
	}

	// Standard claim checks.
	if err := checkClaim(claims, "iss", v.cfg.IssuerURL); err != nil {
		return "", err
	}
	if err := checkAudience(claims, v.cfg.ClientID); err != nil {
		return "", err
	}
	if err := checkExp(claims); err != nil {
		return "", err
	}
	if err := checkNBF(claims); err != nil {
		return "", err
	}
	if err := checkIAT(claims); err != nil {
		return "", err
	}
	if err := checkAZP(claims, v.cfg.ClientID); err != nil {
		return "", err
	}

	subject, _ := claims["sub"].(string)
	if subject == "" {
		subject, _ = claims["email"].(string)
	}
	return subject, nil
}

func splitJWT(token string) (header, payload, sig []byte, signingInput string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, nil, nil, "", errors.New("jwt must have 3 parts")
	}
	header, err = base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("decode header: %w", err)
	}
	payload, err = base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("decode payload: %w", err)
	}
	sig, err = base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("decode signature: %w", err)
	}
	return header, payload, sig, parts[0] + "." + parts[1], nil
}

func verifySignature(alg string, signingInput string, sig []byte, jwk any) error {
	switch alg {
	case "RS256":
		key, ok := jwk.(*rsa.PublicKey)
		if !ok {
			return errors.New("jwk is not an rsa public key")
		}
		hash := sha256.Sum256([]byte(signingInput))
		return rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], sig)
	case "RS384":
		key, ok := jwk.(*rsa.PublicKey)
		if !ok {
			return errors.New("jwk is not an rsa public key")
		}
		hash := sha512.Sum384([]byte(signingInput))
		return rsa.VerifyPKCS1v15(key, crypto.SHA384, hash[:], sig)
	case "RS512":
		key, ok := jwk.(*rsa.PublicKey)
		if !ok {
			return errors.New("jwk is not an rsa public key")
		}
		hash := sha512.Sum512([]byte(signingInput))
		return rsa.VerifyPKCS1v15(key, crypto.SHA512, hash[:], sig)
	case "ES256":
		key, ok := jwk.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("jwk is not an ec public key")
		}
		hash := sha256.Sum256([]byte(signingInput))
		return verifyECDSA(key, hash[:], sig, 32)
	case "ES384":
		key, ok := jwk.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("jwk is not an ec public key")
		}
		hash := sha512.Sum384([]byte(signingInput))
		return verifyECDSA(key, hash[:], sig, 48)
	case "ES512":
		key, ok := jwk.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("jwk is not an ec public key")
		}
		hash := sha512.Sum512([]byte(signingInput))
		return verifyECDSA(key, hash[:], sig, 66)
	default:
		return fmt.Errorf("unsupported jwt alg: %s", alg)
	}
}

func verifyECDSA(key *ecdsa.PublicKey, hash, sig []byte, coordLen int) error {
	if len(sig) != 2*coordLen {
		return errors.New("ecdsa signature length mismatch")
	}
	r := new(big.Int).SetBytes(sig[:coordLen])
	s := new(big.Int).SetBytes(sig[coordLen:])
	if !ecdsa.Verify(key, hash, r, s) {
		return errors.New("ecdsa verification failed")
	}
	return nil
}

func checkClaim(claims map[string]any, key, expected string) error {
	actual, ok := claims[key].(string)
	if !ok {
		return fmt.Errorf("claim %s missing", key)
	}
	if actual != expected {
		return fmt.Errorf("claim %s mismatch: got %s, want %s", key, actual, expected)
	}
	return nil
}

func checkAudience(claims map[string]any, clientID string) error {
	// aud can be a string or []string.
	switch aud := claims["aud"].(type) {
	case string:
		if aud != clientID {
			return errors.New("audience mismatch")
		}
	case []any:
		found := false
		for _, a := range aud {
			if s, ok := a.(string); ok && s == clientID {
				found = true
				break
			}
		}
		if !found {
			return errors.New("audience mismatch")
		}
	default:
		return errors.New("audience claim missing or invalid")
	}
	return nil
}

func checkExp(claims map[string]any) error {
	expRaw, ok := claims["exp"]
	if !ok {
		return errors.New("exp claim missing")
	}
	exp, err := parseNumericDate(expRaw)
	if err != nil {
		return err
	}
	if time.Now().UTC().Add(-clockSkewLeeway).After(time.Unix(exp, 0).UTC()) {
		return errors.New("token expired")
	}
	return nil
}

func checkNBF(claims map[string]any) error {
	nbfRaw, ok := claims["nbf"]
	if !ok {
		return nil // nbf is optional
	}
	nbf, err := parseNumericDate(nbfRaw)
	if err != nil {
		return err
	}
	if time.Now().UTC().Add(clockSkewLeeway).Before(time.Unix(nbf, 0).UTC()) {
		return errors.New("token not yet valid (nbf)")
	}
	return nil
}

func checkIAT(claims map[string]any) error {
	iatRaw, ok := claims["iat"]
	if !ok {
		return nil // iat is optional
	}
	iat, err := parseNumericDate(iatRaw)
	if err != nil {
		return err
	}
	// Reject tokens issued more than 1 hour in the future (clock skew tolerance).
	if time.Now().UTC().Add(1 * time.Hour).Before(time.Unix(iat, 0).UTC()) {
		return errors.New("token issued in the future (iat)")
	}
	return nil
}

func checkAZP(claims map[string]any, clientID string) error {
	azpRaw, ok := claims["azp"]
	if !ok {
		return nil // azp is optional
	}
	azp, ok := azpRaw.(string)
	if !ok {
		return errors.New("azp claim invalid type")
	}
	if azp != clientID {
		return errors.New("authorized party mismatch")
	}
	return nil
}

func parseNumericDate(v any) (int64, error) {
	switch val := v.(type) {
	case float64:
		return int64(val), nil
	case int64:
		return val, nil
	case int:
		return int64(val), nil
	default:
		return 0, errors.New("numeric date claim invalid type")
	}
}

// refreshKeysSingleflight ensures only one concurrent JWKS refresh runs.
func (v *OIDCTokenValidator) refreshKeysSingleflight(ctx context.Context) error {
	v.refreshMu.Lock()
	if v.refreshCh != nil {
		ch := v.refreshCh
		v.refreshMu.Unlock()
		<-ch
		return nil
	}
	v.refreshCh = make(chan struct{})
	v.refreshMu.Unlock()

	err := v.refreshKeys(ctx)
	if err == nil {
		v.mu.Lock()
		v.lastRefresh = time.Now().UTC()
		v.mu.Unlock()
	}

	v.refreshMu.Lock()
	close(v.refreshCh)
	v.refreshCh = nil
	v.refreshMu.Unlock()
	return err
}

// refreshKeys fetches the OIDC discovery document and JWKS.
func (v *OIDCTokenValidator) refreshKeys(ctx context.Context) error {
	wellKnown := strings.TrimSuffix(v.cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return err
	}
	resp, err := oidcHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch openid configuration: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("openid configuration status %d", resp.StatusCode)
	}

	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decode openid configuration: %w", err)
	}
	if doc.JWKSURI == "" {
		return errors.New("jwks_uri missing from openid configuration")
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, doc.JWKSURI, nil)
	if err != nil {
		return err
	}
	resp, err = oidcHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks status %d", resp.StatusCode)
	}

	var jwksDoc struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwksDoc); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}

	parsed := make(map[string]any)
	for _, key := range jwksDoc.Keys {
		kid, _ := key["kid"].(string)
		if kid == "" {
			continue
		}
		pub, err := parseJWK(key)
		if err != nil {
			continue // skip unsupported keys
		}
		parsed[kid] = pub
	}

	v.mu.Lock()
	v.jwks = parsed
	v.jwksExp = time.Now().UTC().Add(jwksTTL)
	v.mu.Unlock()
	return nil
}

func parseJWK(key map[string]any) (any, error) {
	kty, _ := key["kty"].(string)
	alg, _ := key["alg"].(string)
	switch kty {
	case "RSA":
		nB64, _ := key["n"].(string)
		eB64, _ := key["e"].(string)
		if nB64 == "" || eB64 == "" {
			return nil, errors.New("missing rsa key parameters")
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
		if err != nil {
			return nil, err
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
		if err != nil {
			return nil, err
		}
		n := new(big.Int).SetBytes(nBytes)
		e := int(new(big.Int).SetBytes(eBytes).Int64())
		return &rsa.PublicKey{N: n, E: e}, nil
	case "EC":
		crv, _ := key["crv"].(string)
		xB64, _ := key["x"].(string)
		yB64, _ := key["y"].(string)
		if xB64 == "" || yB64 == "" {
			return nil, errors.New("missing ec key parameters")
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(xB64)
		if err != nil {
			return nil, err
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(yB64)
		if err != nil {
			return nil, err
		}
		var curve elliptic.Curve
		switch crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported ec curve: %s", crv)
		}
		x := new(big.Int).SetBytes(xBytes)
		y := new(big.Int).SetBytes(yBytes)
		if !curve.IsOnCurve(x, y) {
			return nil, errors.New("ec point is not on curve")
		}
		// Verify alg matches curve.
		expectedAlg := map[string]string{"P-256": "ES256", "P-384": "ES384", "P-521": "ES512"}
		if alg != "" && alg != expectedAlg[crv] {
			return nil, fmt.Errorf("alg %s does not match curve %s", alg, crv)
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	default:
		return nil, fmt.Errorf("unsupported key type: %s", kty)
	}
}

// IsOIDCEnabled reports whether OIDC validation is active.
func (v *OIDCTokenValidator) IsOIDCEnabled() bool {
	return v.cfg.IssuerURL != "" && v.cfg.ClientID != ""
}
