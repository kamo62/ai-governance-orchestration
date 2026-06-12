package governance

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpauth"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

// Auth context propagation.
type authContextKey string

// AuthInfo holds the authenticated subject and method.
type AuthInfo struct {
	Subject string
	Method  string
}

// WithAuthInfo injects auth info into a context.
func WithAuthInfo(ctx context.Context, info AuthInfo) context.Context {
	return context.WithValue(ctx, authInfoKey, info)
}

// AuthInfoFromContext extracts auth info from a context.
func AuthInfoFromContext(ctx context.Context) (AuthInfo, bool) {
	v, ok := ctx.Value(authInfoKey).(AuthInfo)
	return v, ok
}

// AdminBearerSubject reports whether the Authorization header carries the
// configured admin token.
func (s *SessionService) AdminBearerSubject(header string) (string, bool) {
	if s == nil || s.adminToken == "" {
		return "", false
	}
	if !strings.HasPrefix(header, "Bearer ") {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, "Bearer ")), []byte(s.adminToken)) != 1 {
		return "", false
	}
	return AdminOperatorSubject, true
}

// actorFromContext extracts the authenticated subject from the context, falling back to "local-dev".
func actorFromContext(ctx context.Context) string {
	if info, ok := AuthInfoFromContext(ctx); ok && info.Subject != "" {
		return info.Subject
	}
	return "local-dev"
}

type requestTrustMetadata struct {
	TrustLevel      string
	EnforcementMode string
}

// trustMetadataFromRequest derives the trust level recorded on an audit event.
// Trust is server-authoritative: the X-AI-Orch-Client header is a non-authoritative
// claim of client identity, and a self-declared X-AI-Orch-Trust-Level header is never
// honored. When a trusted-client token is configured, the privileged levels
// (gateway_enforced, managed_client) are only granted to callers that prove
// possession of that shared secret, so an ordinary token holder cannot forge a
// stronger trust label on the audit trail. When no token is configured (local dev),
// the client identity header is honored on its own for backward compatibility.
func (s *SessionService) trustMetadataFromRequest(r *http.Request) requestTrustMetadata {
	selfReported := requestTrustMetadata{TrustLevel: "self_reported", EnforcementMode: "advisory"}
	if r == nil {
		return selfReported
	}
	return s.trustMetadataFromClient(r.Header.Get("X-AI-Orch-Client"), r.Header.Get("X-AI-Orch-Trusted-Client-Token"))
}

func (s *SessionService) trustMetadataFromClient(client string, presentedToken string) requestTrustMetadata {
	selfReported := requestTrustMetadata{TrustLevel: "self_reported", EnforcementMode: "advisory"}
	if s != nil && s.trustedClientToken != "" {
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(presentedToken)), []byte(s.trustedClientToken)) != 1 {
			return selfReported
		}
	}
	client = strings.ToLower(strings.TrimSpace(client))
	switch client {
	case "ai-orch-mcp":
		return requestTrustMetadata{TrustLevel: "gateway_enforced", EnforcementMode: "gateway"}
	case "ai-agent-bridge", "vscode-bridge", "ai-orch-bridge":
		return requestTrustMetadata{TrustLevel: "managed_client", EnforcementMode: "managed"}
	}
	// Unknown clients may report evidence, but they cannot upgrade its strength.
	return selfReported
}

func (s *SessionService) authorized(header string) bool {
	return authorizedBearer(header, s.devToken)
}

// RequireAuthorizedRequest validates the request and injects auth info into the request context.
// Callers must use the returned *http.Request to access the authenticated subject.
func (s *SessionService) RequireAuthorizedRequest(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	if s == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return r, false
	}
	// The auth middleware may already have established identity (including the
	// admin-operator superset); handlers re-checking must honor it.
	if info, ok := AuthInfoFromContext(r.Context()); ok && info.Subject != "" {
		return r, true
	}
	if s.authorizer != nil {
		subject, ok := s.authorizer.Validate(r.Context(), r.Header.Get("Authorization"))
		if ok {
			r = r.WithContext(WithAuthInfo(r.Context(), AuthInfo{Subject: subject, Method: "oidc"}))
			return r, true
		}
		if err := s.appendDenied(r.Context(), "invalid bearer token", nil, ""); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return r, false
		}
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return r, false
	}
	if s.devToken == "" {
		if err := s.appendDenied(r.Context(), "dev token not configured", nil, ""); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return r, false
		}
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "dev token not configured"})
		return r, false
	}
	if !s.authorized(r.Header.Get("Authorization")) {
		if err := s.appendDenied(r.Context(), "invalid dev token", nil, ""); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return r, false
		}
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return r, false
	}
	subject := "local-dev"
	// X-AI-Orch-Local-Identity is a dev-mode convenience header for local testing.
	// It allows the bridge or CLI to assert an actor label without OIDC.
	// In production, actor identity must come from OIDC claims, not client headers.
	if localIdentity := r.Header.Get("X-AI-Orch-Local-Identity"); localIdentity != "" {
		if !validActorLabel(localIdentity) {
			if err := s.appendDenied(r.Context(), "invalid local identity", nil, ""); err != nil {
				httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
				return r, false
			}
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid local identity"})
			return r, false
		}
		subject = localIdentity
	}
	r = r.WithContext(WithAuthInfo(r.Context(), AuthInfo{Subject: subject, Method: "dev"}))
	return r, true
}

// RequireAdminRequest validates the request carries the separate admin token.
// Admin endpoints (kill switch, audit retention) must use a token distinct from
// ordinary session auth. If no admin token is configured, admin endpoints are disabled.
func (s *SessionService) RequireAdminRequest(w http.ResponseWriter, r *http.Request) bool {
	if s == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return false
	}
	if s.adminToken == "" {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "admin not configured"})
		return false
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return false
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(s.adminToken)) != 1 {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "admin access required"})
		return false
	}
	return true
}

type RequestAuthorizer interface {
	Validate(ctx context.Context, header string) (subject string, ok bool)
}

// authorizedBearer delegates to httpauth.AuthorizedBearer so there is a single
// constant-time bearer comparison shared across the shell and the standalone
// services. An empty configured token always fails closed.
func authorizedBearer(header string, token string) bool {
	return httpauth.AuthorizedBearer(header, token)
}

func validActorLabel(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '@', r == ':', r == '-':
		default:
			return false
		}
	}
	return true
}
