package modelgateway

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

func (g *Gateway) resolveSession(w http.ResponseWriter, r *http.Request, modelAlias string, body []byte, endpoint string) (string, SessionInfo, bool) {
	if sessionID := strings.TrimSpace(r.Header.Get("X-AI-Orch-Session-ID")); sessionID != "" {
		info, ok := g.sessionInfo(w, r, sessionID)
		return sessionID, info, ok
	}
	if g.autoSession == nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "X-AI-Orch-Session-ID header is required"})
		return "", SessionInfo{}, false
	}
	actor := strings.TrimSpace(r.Header.Get("X-AI-Orch-Actor-Subject"))
	if actor == "" {
		actor = strings.TrimSpace(r.Header.Get("X-AI-Orch-Local-Identity"))
	}
	if actor == "" {
		// Composite API key "<runtime-token>.<actor>" carries identity for
		// clients that cannot send custom headers.
		actor, _ = g.runtimeAuth(r)
	}
	if actor == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "actor identity is required for auto sessions: send X-AI-Orch-Actor-Subject or use the composite API key <runtime-token>.<actor>"})
		return "", SessionInfo{}, false
	}
	classification := strings.TrimSpace(r.Header.Get("X-AI-Orch-Classification"))
	if classification == "" {
		classification = "internal"
	}
	info, err := g.autoSession(r.Context(), AutoSessionRequest{
		ActorSubject:       actor,
		ModelAlias:         modelAlias,
		PromptSHA256:       sha256Hex(body),
		Client:             strings.TrimSpace(r.Header.Get("X-AI-Orch-Client")),
		Endpoint:           endpoint,
		Classification:     classification,
		RawRequestBody:     body,
		TrustedClientToken: strings.TrimSpace(r.Header.Get("X-AI-Orch-Trusted-Client-Token")),
		UseCaseID:          strings.TrimSpace(r.Header.Get("X-AI-Orch-Use-Case-ID")),
		WorkflowID:         strings.TrimSpace(r.Header.Get("X-AI-Orch-Workflow-ID")),
		WorkItemID:         strings.TrimSpace(r.Header.Get("X-AI-Orch-Work-Item-ID")),
		WorkItemType:       strings.TrimSpace(r.Header.Get("X-AI-Orch-Work-Item-Type")),
		RepoURL:            strings.TrimSpace(r.Header.Get("X-AI-Orch-Repo-URL")),
		Branch:             strings.TrimSpace(r.Header.Get("X-AI-Orch-Branch")),
		CommitSHA:          strings.TrimSpace(r.Header.Get("X-AI-Orch-Commit-SHA")),
		Intent:             strings.TrimSpace(r.Header.Get("X-AI-Orch-Intent")),
		ActorHint:          strings.TrimSpace(r.Header.Get("X-AI-Orch-Actor-Hint")),
		SourceSystem:       strings.TrimSpace(r.Header.Get("X-AI-Orch-Source-System")),
		EstimatedCostUSD:   parseOptionalFloatHeader(r.Header.Get("X-AI-Orch-Estimated-Cost-USD")),
	})
	if err != nil {
		httpx.WriteJSON(w, statusCodeFromAutoSessionError(err), map[string]any{"error": err.Error()})
		return "", SessionInfo{}, false
	}
	if info.SessionID == "" {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "auto session missing session ID"})
		return "", SessionInfo{}, false
	}
	if strings.TrimSpace(info.Classification) == "" {
		info.Classification = "internal"
	}
	if strings.TrimSpace(info.ActorSubject) == "" {
		info.ActorSubject = actor
	}
	info.AutoCreated = true
	w.Header().Set("X-AI-Orch-Session-ID", info.SessionID)
	if strings.TrimSpace(info.GatewayToken) != "" {
		w.Header().Set("X-AI-Orch-Session-Token", info.GatewayToken)
	}
	return info.SessionID, info, true
}

func (g *Gateway) sessionInfo(w http.ResponseWriter, r *http.Request, sessionID string) (SessionInfo, bool) {
	if g.lookupSession != nil {
		info, err := g.lookupSession(r.Context(), sessionID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
			return SessionInfo{}, false
		}
		if !sessionTokenMatches(r, info.GatewayTokenSHA256, info.RuntimeGatewayTokenSHA256) {
			httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "X-AI-Orch-Session-Token missing or invalid for this session"})
			return SessionInfo{}, false
		}
		if !sessionStatusAllowsModelCalls(info.Status) {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": "session is not active for model calls"})
			return SessionInfo{}, false
		}
		if strings.TrimSpace(info.Classification) == "" {
			info.Classification = "internal"
		}
		return info, true
	}
	if g.validateSession != nil {
		if err := g.validateSession(r.Context(), sessionID); err != nil {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
			return SessionInfo{}, false
		}
	}
	return SessionInfo{Classification: "internal"}, true
}

func sessionStatusAllowsModelCalls(status string) bool {
	switch strings.TrimSpace(status) {
	case "", "created", "awaiting_confirmation", "confirming", "confirmed", "running":
		return true
	default:
		return false
	}
}

func statusCodeFromAutoSessionError(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "kill switch"):
		return http.StatusLocked
	case strings.Contains(message, "work item") || strings.Contains(message, "feature branch") || strings.Contains(message, "protected branch"):
		return http.StatusPreconditionRequired
	case strings.Contains(message, "cost cap"):
		return http.StatusPaymentRequired
	case strings.Contains(message, "secret detected") || strings.Contains(message, "classification") || strings.Contains(message, "policy denied"):
		return http.StatusForbidden
	case strings.Contains(message, "valid actor"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func parseOptionalFloatHeader(value string) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

// sessionTokenMatches verifies the per-session gateway secret against any of
// the stored hashes (client token, dispatch-time runtime token). Sessions
// with no stored hash predate token binding and pass unchanged.
func sessionTokenMatches(r *http.Request, wantHashesHex ...string) bool {
	anyConfigured := false
	presented := strings.TrimSpace(r.Header.Get("X-AI-Orch-Session-Token"))
	var sum [sha256.Size]byte
	if presented != "" {
		sum = sha256.Sum256([]byte(presented))
	}
	for _, wantHashHex := range wantHashesHex {
		wantHashHex = strings.TrimSpace(wantHashHex)
		if wantHashHex == "" {
			continue
		}
		anyConfigured = true
		if presented == "" {
			continue
		}
		got, err := hex.DecodeString(wantHashHex)
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare(sum[:], got) == 1 {
			return true
		}
	}
	return !anyConfigured
}

// authorized delegates to httpauth.AuthorizedBearer so the constant-time bearer
// comparison has a single implementation shared across every endpoint. An empty
// runtime token always fails closed.
func (g *Gateway) authorized(r *http.Request) bool {
	_, ok := g.runtimeAuth(r)
	return ok
}

// runtimeAuth validates the runtime bearer and extracts the optional actor
// suffix from a composite key "<runtime-token>.<actor>". The composite form
// exists for OpenAI-compatible clients that cannot send custom headers (for
// example Cline), so identity can travel in the only field every client
// reliably forwards. The plain runtime token remains valid.
func (g *Gateway) runtimeAuth(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if g.runtimeToken == "" || !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimPrefix(header, prefix)
	if subtle.ConstantTimeCompare([]byte(token), []byte(g.runtimeToken)) == 1 {
		return "", true
	}
	sep := g.runtimeToken + "."
	if len(token) > len(sep) && subtle.ConstantTimeCompare([]byte(token[:len(sep)]), []byte(sep)) == 1 {
		actor := strings.TrimSpace(token[len(sep):])
		if compositeActorLabelOK(actor) {
			return actor, true
		}
	}
	return "", false
}

// compositeActorLabelOK keeps API-key-borne identities to safe label
// characters so they cannot smuggle header or log injection payloads.
func compositeActorLabelOK(actor string) bool {
	if actor == "" || len(actor) > 64 {
		return false
	}
	for _, r := range actor {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_' || r == '@':
		default:
			return false
		}
	}
	return true
}
