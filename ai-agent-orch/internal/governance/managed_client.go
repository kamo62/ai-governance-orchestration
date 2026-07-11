package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

const managedClientMaxBatchEvents = 50

type ManagedClientEvidenceConfig struct {
	Credentials DeveloperCredentialStore
	Audit       AuditAppender
	Sessions    SessionStore
	Receipts    ManagedClientReceiptStore
	Now         func() time.Time
	NewID       func(prefix string) string
}

type ManagedClientEvidenceEvent struct {
	EventID         string                   `json:"event_id"`
	SchemaVersion   string                   `json:"schema_version"`
	Client          string                   `json:"client"`
	ClientSessionID string                   `json:"client_session_id"`
	EventType       string                   `json:"event_type"`
	Repo            ManagedClientRepoContext `json:"repo,omitempty"`
	Timestamp       time.Time                `json:"timestamp"`
	Content         string                   `json:"content,omitempty"`
	ContentSHA256   string                   `json:"content_sha256,omitempty"`
	Tool            ManagedClientToolEvent   `json:"tool,omitempty"`
	FileChange      ManagedClientFileChange  `json:"file_change,omitempty"`
	TokenUsage      ManagedClientTokenUsage  `json:"token_usage,omitempty"`
	Permission      ManagedClientPermission  `json:"permission_decision,omitempty"`
}

type ManagedClientRepoContext struct {
	Remote string `json:"remote,omitempty"`
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
}

type ManagedClientToolEvent struct {
	Name      string    `json:"name,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Status    string    `json:"status,omitempty"`
}

type ManagedClientFileChange struct {
	Paths      []string `json:"paths,omitempty"`
	DiffSHA256 string   `json:"diff_sha256,omitempty"`
	Diff       string   `json:"diff,omitempty"`
}

type ManagedClientTokenUsage struct {
	Model        string `json:"model,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	Source       string `json:"source,omitempty"`
}

type ManagedClientPermission struct {
	Tool     string `json:"tool,omitempty"`
	Command  string `json:"command,omitempty"`
	Decision string `json:"decision,omitempty"`
	Decider  string `json:"decider,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type ManagedClientEvidenceResponse struct {
	SessionID string   `json:"session_id,omitempty"`
	Accepted  int      `json:"accepted"`
	Duplicate int      `json:"duplicate"`
	EventIDs  []string `json:"event_ids,omitempty"`
}

type managedClientEvidenceBatch struct {
	Events []ManagedClientEvidenceEvent `json:"events"`
}

type managedClientEvidenceHandler struct {
	credentials DeveloperCredentialStore
	audit       AuditAppender
	sessions    SessionStore
	receipts    ManagedClientReceiptStore
	now         func() time.Time
	newID       func(prefix string) string
	sessionMu   sync.Mutex
}

func NewManagedClientEvidenceHandler(cfg ManagedClientEvidenceConfig) http.Handler {
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	newID := cfg.NewID
	if newID == nil {
		newID = randomID
	}
	return &managedClientEvidenceHandler{
		credentials: cfg.Credentials,
		audit:       cfg.Audit,
		sessions:    cfg.Sessions,
		receipts:    cfg.Receipts,
		now:         now,
		newID:       newID,
	}
}

func (h *managedClientEvidenceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h == nil || h.credentials == nil || h.audit == nil || h.sessions == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "managed-client evidence unavailable"})
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	credential, ok, err := h.credentials.Validate(r.Context(), token, h.now().UTC())
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "credential validation failed"})
		return
	}
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}

	var batch managedClientEvidenceBatch
	if err := readJSON(w, r, &batch); err != nil {
		writeRequestBodyError(w, err)
		return
	}
	if len(batch.Events) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "events are required"})
		return
	}
	if len(batch.Events) > managedClientMaxBatchEvents {
		httpx.WriteJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "too many events"})
		return
	}
	for i, event := range batch.Events {
		if err := validateManagedClientEvent(event); err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("events[%d]: %s", i, err)})
			return
		}
		if i > 0 && (strings.TrimSpace(event.ClientSessionID) != strings.TrimSpace(batch.Events[0].ClientSessionID) || strings.TrimSpace(event.Client) != strings.TrimSpace(batch.Events[0].Client)) {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("events[%d]: client and client_session_id must match events[0]", i)})
			return
		}
	}

	sessionID, err := h.ensureSession(r.Context(), credential.ActorSubject, batch.Events[0])
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "session attach failed"})
		return
	}
	existing := map[string]bool{}
	if h.receipts == nil {
		var err error
		existing, err = h.existingClientEventIDs(r.Context(), sessionID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit lookup failed"})
			return
		}
	}
	response := ManagedClientEvidenceResponse{SessionID: sessionID}
	var auditEventIDs map[string]bool
	auditEventsLoaded := false
	for _, event := range batch.Events {
		auditEventID := h.newID("evt")
		auditEvent := event.toAuditEvent(sessionID, credential.ActorSubject, auditEventID)
		receipt := ManagedClientReceipt{SessionID: sessionID, ClientEventID: event.EventID, AuditEventID: auditEventID, RecordedAt: h.now().UTC()}
		if h.receipts != nil {
			// The receipt PK atomically gives one request ownership. If the audit
			// append fails, deleting only that reservation lets a later retry append.
			reserved, err := h.receipts.ReserveManagedClientReceipt(r.Context(), receipt)
			if err != nil {
				httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "receipt write failed"})
				return
			}
			if !reserved {
				existingReceipt, found, err := h.receipts.GetManagedClientReceipt(r.Context(), sessionID, event.EventID)
				if err != nil || !found {
					httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "receipt lookup failed"})
					return
				}
				if !auditEventsLoaded {
					auditEventIDs, err = h.existingAuditEventIDs(r.Context(), sessionID)
					auditEventsLoaded = true
					if err != nil {
						httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit lookup failed"})
						return
					}
				}
				if auditEventIDs[existingReceipt.AuditEventID] {
					response.Duplicate++
					continue
				}
				if err := h.receipts.ReleaseManagedClientReceipt(r.Context(), existingReceipt); err != nil {
					log.Printf("managed-client: release stale receipt: %v", err)
					httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "stale receipt cleanup failed"})
					return
				}
				reserved, err = h.receipts.ReserveManagedClientReceipt(r.Context(), receipt)
				if err != nil || !reserved {
					httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "receipt re-reservation failed"})
					return
				}
			}
		} else if existing[event.EventID] {
			response.Duplicate++
			continue
		}
		if _, err := h.audit.Append(r.Context(), auditEvent); err != nil {
			if h.receipts != nil {
				if releaseErr := h.receipts.ReleaseManagedClientReceipt(context.WithoutCancel(r.Context()), receipt); releaseErr != nil {
					log.Printf("managed-client: release receipt after audit failure: %v", releaseErr)
				}
			}
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		existing[event.EventID] = true
		if auditEventsLoaded {
			auditEventIDs[auditEvent.EventID] = true
		}
		response.Accepted++
		response.EventIDs = append(response.EventIDs, event.EventID)
		if event.EventType == "session_end" {
			_ = h.sessions.UpdateStatus(r.Context(), sessionID, "done")
		}
	}
	httpx.WriteJSON(w, http.StatusAccepted, response)
}

func (h *managedClientEvidenceHandler) existingAuditEventIDs(ctx context.Context, sessionID string) (map[string]bool, error) {
	reader, ok := h.audit.(AuditReader)
	if !ok {
		return nil, errors.New("audit reader unavailable")
	}
	events, err := reader.EventsBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		seen[event.EventID] = true
	}
	return seen, nil
}

func (h *managedClientEvidenceHandler) ensureSession(ctx context.Context, actor string, event ManagedClientEvidenceEvent) (string, error) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	clientSessionID := strings.TrimSpace(event.ClientSessionID)
	if finder, ok := h.sessions.(sessionClientFinder); ok {
		if existing, found, err := finder.FindActiveByActorClientSession(ctx, actor, clientSessionID); err != nil {
			return "", err
		} else if found {
			return existing.SessionID, nil
		}
	}
	sessionID := h.newID("sess_managed")
	eventID := h.newID("evt")
	now := h.now().UTC()
	repoURL := sanitizeRepoURL(event.Repo.Remote)
	sessionEvent := audit.Event{
		EventID:            eventID,
		SessionID:          sessionID,
		EventType:          "session.auto_created",
		Actor:              actor,
		Agent:              "managed-client",
		Classification:     "internal",
		Reason:             "managed client evidence session",
		RepoURL:            repoURL,
		Branch:             strings.TrimSpace(event.Repo.Branch),
		CommitSHA:          strings.TrimSpace(event.Repo.Commit),
		SourceSystem:       defaultString(strings.TrimSpace(event.Client), "managed-client"),
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "managed_client",
		TrustLevel:         "managed_client",
		EnforcementMode:    "advisory",
		RecordedAt:         now,
	}
	if _, err := h.audit.Append(ctx, sessionEvent); err != nil {
		return "", err
	}
	rec := SessionRecord{
		SessionID:       sessionID,
		ActorSubject:    actor,
		Agent:           "managed-client",
		Classification:  "internal",
		PromptSHA256:    "",
		Status:          "running",
		CreatedAt:       now,
		ApprovalMode:    "client_reported",
		WorkspaceMode:   "client-local",
		RepoURL:         repoURL,
		Branch:          strings.TrimSpace(event.Repo.Branch),
		CommitSHA:       strings.TrimSpace(event.Repo.Commit),
		SourceSystem:    defaultString(strings.TrimSpace(event.Client), "managed-client"),
		ClientSessionID: clientSessionID,
	}
	if err := h.sessions.Create(ctx, rec); err != nil {
		return "", err
	}
	return sessionID, nil
}

func (h *managedClientEvidenceHandler) existingClientEventIDs(ctx context.Context, sessionID string) (map[string]bool, error) {
	seen := map[string]bool{}
	reader, ok := h.audit.(AuditReader)
	if !ok {
		return seen, nil
	}
	events, err := reader.EventsBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		if event.ClientEventID != "" {
			seen[event.ClientEventID] = true
			continue
		}
		// Legacy managed-client events used the client ID as the audit ID.
		seen[event.EventID] = true
	}
	return seen, nil
}

func validateManagedClientEvent(event ManagedClientEvidenceEvent) error {
	if strings.TrimSpace(event.EventID) == "" {
		return errors.New("event_id is required")
	}
	if event.SchemaVersion != "v0" {
		return errors.New("schema_version must be v0")
	}
	if strings.TrimSpace(event.Client) == "" {
		return errors.New("client is required")
	}
	if strings.TrimSpace(event.ClientSessionID) == "" {
		return errors.New("client_session_id is required")
	}
	if event.Timestamp.IsZero() {
		return errors.New("timestamp is required")
	}
	switch event.EventType {
	case "session_start", "session_end", "prompt", "assistant_message", "tool_execution", "file_change", "token_usage", "permission_decision":
	default:
		return errors.New("unknown event_type")
	}
	if event.EventType == "token_usage" && event.TokenUsage.Source != "client_reported" {
		return errors.New(`token_usage.source must be "client_reported"`)
	}
	if event.EventType == "permission_decision" {
		name := defaultString(strings.TrimSpace(event.Permission.Tool), strings.TrimSpace(event.Permission.Command))
		if name == "" {
			return errors.New("permission_decision.tool or permission_decision.command is required")
		}
		switch event.Permission.Decision {
		case "approved", "denied":
		default:
			return errors.New("permission_decision.decision must be approved or denied")
		}
		switch event.Permission.Decider {
		case "user", "auto_policy":
		default:
			return errors.New("permission_decision.decider must be user or auto_policy")
		}
	}
	return nil
}

func (event ManagedClientEvidenceEvent) toAuditEvent(sessionID, actor, auditEventID string) audit.Event {
	auditEvent := audit.Event{
		EventID:            auditEventID,
		ClientEventID:      strings.TrimSpace(event.EventID),
		SessionID:          sessionID,
		EventType:          "managed_client." + strings.TrimSpace(event.EventType),
		Actor:              actor,
		Agent:              "managed-client",
		Classification:     "internal",
		RepoURL:            sanitizeRepoURL(event.Repo.Remote),
		Branch:             strings.TrimSpace(event.Repo.Branch),
		CommitSHA:          strings.TrimSpace(event.Repo.Commit),
		SourceSystem:       defaultString(strings.TrimSpace(event.Client), "managed-client"),
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "managed_client",
		TrustLevel:         "managed_client",
		EnforcementMode:    "advisory",
		RecordedAt:         event.Timestamp.UTC(),
	}
	switch event.EventType {
	case "prompt":
		auditEvent.PromptSHA256 = defaultString(strings.TrimSpace(event.ContentSHA256), managedClientSHA256([]byte(event.Content)))
	case "assistant_message":
		auditEvent.ResponseSHA256 = defaultString(strings.TrimSpace(event.ContentSHA256), managedClientSHA256([]byte(event.Content)))
	case "tool_execution":
		auditEvent.MCPToolName = strings.TrimSpace(event.Tool.Name)
		auditEvent.RuntimeStatus = strings.TrimSpace(event.Tool.Status)
		if !event.Tool.StartedAt.IsZero() && !event.Tool.EndedAt.IsZero() {
			auditEvent.DurationMS = event.Tool.EndedAt.Sub(event.Tool.StartedAt).Milliseconds()
		}
	case "file_change":
		diffHash := strings.TrimSpace(event.FileChange.DiffSHA256)
		if diffHash == "" {
			diffHash = managedClientSHA256([]byte(event.FileChange.Diff))
		}
		auditEvent.PatchID = diffHash
		auditEvent.RequestSHA256 = diffHash
		auditEvent.PatchFileCount = len(event.FileChange.Paths)
	case "token_usage":
		auditEvent.Provider = "client_reported"
		auditEvent.ModelAlias = strings.TrimSpace(event.TokenUsage.Model)
		auditEvent.ModelResolved = strings.TrimSpace(event.TokenUsage.Model)
		auditEvent.TokenUsage = map[string]any{
			"input_tokens":  event.TokenUsage.InputTokens,
			"output_tokens": event.TokenUsage.OutputTokens,
			"source":        "client_reported",
		}
	case "permission_decision":
		auditEvent.MCPToolName = defaultString(strings.TrimSpace(event.Permission.Tool), strings.TrimSpace(event.Permission.Command))
		auditEvent.PatchDecision = strings.TrimSpace(event.Permission.Decision)
		auditEvent.AuthMode = strings.TrimSpace(event.Permission.Decider)
		auditEvent.Reason = strings.TrimSpace(event.Permission.Reason)
	}
	return auditEvent
}

func managedClientSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
