package httpauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOIDCTokenValidator_DevTokenFallback(t *testing.T) {
	// OIDC not configured: should fall back to dev token.
	v := NewOIDCTokenValidator(OIDCConfig{}, "my-secret")

	_, ok := v.Validate(context.Background(), "Bearer my-secret")
	if !ok {
		t.Fatal("expected dev token to be valid")
	}

	_, ok = v.Validate(context.Background(), "Bearer wrong")
	if ok {
		t.Fatal("expected wrong token to be invalid")
	}

	_, ok = v.Validate(context.Background(), "")
	if ok {
		t.Fatal("expected empty header to be invalid")
	}
}

func TestOIDCTokenValidator_NoFallbackWhenOIDCConfigured(t *testing.T) {
	// When OIDC is configured, a clearly dev-token value that is not an exact
	// match should not fall through unless the token equals the dev token.
	v := NewOIDCTokenValidator(OIDCConfig{
		IssuerURL: "https://example.com",
		ClientID:  "client",
	}, "my-secret")

	// Wrong token should fail because OIDC is configured and the token is
	// not the exact dev token.
	_, ok := v.Validate(context.Background(), "Bearer wrong")
	if ok {
		t.Fatal("expected wrong token to be invalid when oidc is configured")
	}

	// Exact dev token should still work.
	_, ok = v.Validate(context.Background(), "Bearer my-secret")
	if !ok {
		t.Fatal("expected exact dev token to remain valid")
	}
}

func TestOIDCTokenValidator_DoesNotFetchKeysForMalformedOrExactDevToken(t *testing.T) {
	var discoveryHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&discoveryHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	v := NewOIDCTokenValidator(OIDCConfig{
		IssuerURL: server.URL,
		ClientID:  "client",
	}, "my-secret")

	if _, ok := v.Validate(context.Background(), "Bearer my-secret"); !ok {
		t.Fatal("expected exact dev token to be valid without network access")
	}
	if _, ok := v.Validate(context.Background(), "Bearer not-a-jwt"); ok {
		t.Fatal("expected malformed token to be invalid")
	}
	if got := atomic.LoadInt32(&discoveryHits); got != 0 {
		t.Fatalf("expected no discovery fetches, got %d", got)
	}
}

func TestOIDCTokenValidator_ValidatesRS256IDToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(t, w, map[string]string{"jwks_uri": server.URL + "/jwks"})
		case "/jwks":
			writeTestJSON(t, w, map[string]any{
				"keys": []map[string]string{
					{
						"kty": "RSA",
						"kid": "test-key",
						"alg": "RS256",
						"use": "sig",
						"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
						"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	token := signedTestIDToken(t, key, map[string]any{
		"iss": server.URL,
		"aud": "client",
		"sub": "subject-123",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	v := NewOIDCTokenValidator(OIDCConfig{
		IssuerURL: server.URL,
		ClientID:  "client",
	}, "")

	subject, ok := v.Validate(context.Background(), "Bearer "+token)
	if !ok {
		t.Fatal("expected signed id token to be valid")
	}
	if subject != "subject-123" {
		t.Fatalf("expected subject-123, got %q", subject)
	}
}

func TestOIDCTokenValidator_RejectsTokenWithoutSubject(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(t, w, map[string]string{"jwks_uri": server.URL + "/jwks"})
		case "/jwks":
			writeTestJSON(t, w, map[string]any{
				"keys": []map[string]string{
					{
						"kty": "RSA",
						"kid": "test-key",
						"alg": "RS256",
						"use": "sig",
						"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
						"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	token := signedTestIDToken(t, key, map[string]any{
		"iss": server.URL,
		"aud": "client",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	v := NewOIDCTokenValidator(OIDCConfig{
		IssuerURL: server.URL,
		ClientID:  "client",
	}, "")

	if _, ok := v.Validate(context.Background(), "Bearer "+token); ok {
		t.Fatal("expected token without sub or email to be invalid")
	}
}

func TestOIDCTokenValidator_IsOIDCEnabled(t *testing.T) {
	v := NewOIDCTokenValidator(OIDCConfig{}, "")
	if v.IsOIDCEnabled() {
		t.Fatal("expected OIDC to be disabled when empty config")
	}

	v2 := NewOIDCTokenValidator(OIDCConfig{
		IssuerURL: "https://example.com",
		ClientID:  "client",
	}, "")
	if !v2.IsOIDCEnabled() {
		t.Fatal("expected OIDC to be enabled")
	}
}

func signedTestIDToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	headerJSON, err := json.Marshal(map[string]string{
		"alg": "RS256",
		"kid": "test-key",
		"typ": "JWT",
	})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := header + "." + payload
	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestOIDCTokenValidator_RejectExpiredToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(t, w, map[string]string{"jwks_uri": server.URL + "/jwks"})
		case "/jwks":
			writeTestJSON(t, w, map[string]any{
				"keys": []map[string]string{
					{
						"kty": "RSA",
						"kid": "test-key",
						"alg": "RS256",
						"use": "sig",
						"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
						"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	token := signedTestIDToken(t, key, map[string]any{
		"iss": server.URL,
		"aud": "client",
		"sub": "subject-123",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})
	v := NewOIDCTokenValidator(OIDCConfig{
		IssuerURL: server.URL,
		ClientID:  "client",
	}, "")

	if _, ok := v.Validate(context.Background(), "Bearer "+token); ok {
		t.Fatal("expected expired token to be invalid")
	}
}

func TestOIDCTokenValidator_RejectFutureIAT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(t, w, map[string]string{"jwks_uri": server.URL + "/jwks"})
		case "/jwks":
			writeTestJSON(t, w, map[string]any{
				"keys": []map[string]string{
					{
						"kty": "RSA",
						"kid": "test-key",
						"alg": "RS256",
						"use": "sig",
						"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
						"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	token := signedTestIDToken(t, key, map[string]any{
		"iss": server.URL,
		"aud": "client",
		"sub": "subject-123",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(2 * time.Hour).Unix(),
	})
	v := NewOIDCTokenValidator(OIDCConfig{
		IssuerURL: server.URL,
		ClientID:  "client",
	}, "")

	if _, ok := v.Validate(context.Background(), "Bearer "+token); ok {
		t.Fatal("expected future iat token to be invalid")
	}
}

func TestOIDCTokenValidator_RejectWrongAZP(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(t, w, map[string]string{"jwks_uri": server.URL + "/jwks"})
		case "/jwks":
			writeTestJSON(t, w, map[string]any{
				"keys": []map[string]string{
					{
						"kty": "RSA",
						"kid": "test-key",
						"alg": "RS256",
						"use": "sig",
						"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
						"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	token := signedTestIDToken(t, key, map[string]any{
		"iss": server.URL,
		"aud": "client",
		"sub": "subject-123",
		"exp": time.Now().Add(time.Hour).Unix(),
		"azp": "wrong-client",
	})
	v := NewOIDCTokenValidator(OIDCConfig{
		IssuerURL: server.URL,
		ClientID:  "client",
	}, "")

	if _, ok := v.Validate(context.Background(), "Bearer "+token); ok {
		t.Fatal("expected wrong azp token to be invalid")
	}
}

func TestOIDCTokenValidator_ValidatesES256IDToken(t *testing.T) {
	// Generate an ECDSA P-256 key.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ec key: %v", err)
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(t, w, map[string]string{"jwks_uri": server.URL + "/jwks"})
		case "/jwks":
			writeTestJSON(t, w, map[string]any{
				"keys": []map[string]any{
					{
						"kty": "EC",
						"kid": "ec-test-key",
						"alg": "ES256",
						"crv": "P-256",
						"x":   base64.RawURLEncoding.EncodeToString(key.PublicKey.X.Bytes()),
						"y":   base64.RawURLEncoding.EncodeToString(key.PublicKey.Y.Bytes()),
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	token := signedTestES256IDToken(t, key, map[string]any{
		"iss": server.URL,
		"aud": "client",
		"sub": "subject-ec",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	v := NewOIDCTokenValidator(OIDCConfig{
		IssuerURL: server.URL,
		ClientID:  "client",
	}, "")

	subject, ok := v.Validate(context.Background(), "Bearer "+token)
	if !ok {
		t.Fatal("expected signed ES256 id token to be valid")
	}
	if subject != "subject-ec" {
		t.Fatalf("expected subject-ec, got %q", subject)
	}
}

func signedTestES256IDToken(t *testing.T, key *ecdsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	headerJSON, err := json.Marshal(map[string]string{
		"alg": "ES256",
		"kid": "ec-test-key",
		"typ": "JWT",
	})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := header + "." + payload
	hash := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, hash[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	coordLen := 32
	sig := make([]byte, 2*coordLen)
	r.FillBytes(sig[:coordLen])
	s.FillBytes(sig[coordLen:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}
