package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store is the audit persistence interface.
// Implementations must be safe for concurrent use.
type Store interface {
	Append(context.Context, Event) (Event, error)
	EventsBySession(context.Context, string) ([]Event, error)
}

type Event struct {
	EventID                  string         `json:"event_id"`
	ParentEventID            string         `json:"parent_event_id,omitempty"`
	PrevEventHash            string         `json:"prev_event_hash,omitempty"`
	EventHash                string         `json:"event_hash,omitempty"`
	SessionID                string         `json:"session_id,omitempty"`
	EventType                string         `json:"event_type"`
	Actor                    string         `json:"actor,omitempty"`
	Agent                    string         `json:"agent,omitempty"`
	Classification           string         `json:"classification,omitempty"`
	Reason                   string         `json:"reason,omitempty"`
	Findings                 []string       `json:"findings,omitempty"`
	PromptSHA256             string         `json:"prompt_sha256,omitempty"`
	EstimatedCostUSD         float64        `json:"estimated_cost_usd,omitempty"`
	CostCapUSD               float64        `json:"cost_cap_usd,omitempty"`
	ProxyCallID              string         `json:"proxy_call_id,omitempty"`
	Provider                 string         `json:"provider,omitempty"`
	ModelAlias               string         `json:"model_alias,omitempty"`
	ModelResolved            string         `json:"model_resolved,omitempty"`
	RequestedModelAlias      string         `json:"requested_model_alias,omitempty"`
	CredentialSource         string         `json:"credential_source,omitempty"`
	ReasoningEffortRequested string         `json:"reasoning_effort_requested,omitempty"`
	ReasoningEffortApplied   string         `json:"reasoning_effort_applied,omitempty"`
	ReasoningSource          string         `json:"reasoning_source,omitempty"`
	RequestSHA256            string         `json:"request_sha256,omitempty"`
	ResponseSHA256           string         `json:"response_sha256,omitempty"`
	MCPServerID              string         `json:"mcp_server_id,omitempty"`
	MCPToolName              string         `json:"mcp_tool_name,omitempty"`
	AuthMode                 string         `json:"auth_mode,omitempty"`
	PatchID                  string         `json:"patch_id,omitempty"`
	PatchBufferID            string         `json:"patch_buffer_id,omitempty"`
	PatchDecision            string         `json:"patch_decision,omitempty"`
	PatchFileCount           int            `json:"patch_file_count,omitempty"`
	Runtime                  string         `json:"runtime,omitempty"`
	RuntimeStatus            string         `json:"runtime_status,omitempty"`
	DurationMS               int64          `json:"duration_ms,omitempty"`
	EventCount               int            `json:"event_count,omitempty"`
	PatchCount               int            `json:"patch_count,omitempty"`
	ToolCallCount            int            `json:"tool_call_count,omitempty"`
	WorkspaceSHA256          string         `json:"workspace_sha256,omitempty"`
	OpencodeVersion          string         `json:"opencode_version,omitempty"`
	TokenUsage               map[string]any `json:"token_usage,omitempty"`
	GatewayBackend           string         `json:"gateway_backend,omitempty"`
	TrustLevel               string         `json:"trust_level,omitempty"`
	EnforcementMode          string         `json:"enforcement_mode,omitempty"`
	PolicyDecisionID         string         `json:"policy_decision_id,omitempty"`
	RunID                    string         `json:"run_id,omitempty"`
	PermissionMode           string         `json:"permission_mode,omitempty"`
	ApprovalMode             string         `json:"approval_mode,omitempty"`
	WorkspaceMode            string         `json:"workspace_mode,omitempty"`
	WorkItemID               string         `json:"work_item_id,omitempty"`
	WorkItemType             string         `json:"work_item_type,omitempty"`
	CommitSHA                string         `json:"commit_sha,omitempty"`
	ActorHint                string         `json:"actor_hint,omitempty"`
	SourceSystem             string         `json:"source_system,omitempty"`
	RawPromptStored          bool           `json:"raw_prompt_stored"`
	RawResponseStored        bool           `json:"raw_response_stored"`
	CorrelationSubject       string         `json:"correlation_subject,omitempty"`
	RecordedAt               time.Time      `json:"recorded_at"`
}

func (s *FileStore) EventsBySession(ctx context.Context, sessionID string) ([]Event, error) {
	if s == nil {
		return nil, errors.New("audit store is required")
	}
	if s.Path == "" {
		return nil, errors.New("audit path is required")
	}
	if sessionID == "" {
		return nil, errors.New("session_id is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	f, err := os.Open(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return []Event{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open audit file: %w", err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("parse audit event: %w", err)
		}
		if event.SessionID == sessionID {
			events = append(events, event)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan audit file: %w", err)
	}
	return events, nil
}

func (s *FileStore) AllEvents(ctx context.Context) ([]Event, error) {
	if s == nil {
		return nil, errors.New("audit store is required")
	}
	if s.Path == "" {
		return nil, errors.New("audit path is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	f, err := os.Open(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return []Event{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open audit file: %w", err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("parse audit event: %w", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan audit file: %w", err)
	}
	return events, nil
}

type FileStore struct {
	Path string
	Now  func() time.Time

	mu sync.RWMutex
}

func NewFileStore(path string) *FileStore {
	return &FileStore{
		Path: path,
		Now:  func() time.Time { return time.Now().UTC() },
	}
}

func (s *FileStore) Append(ctx context.Context, event Event) (Event, error) {
	if s == nil {
		return Event{}, errors.New("audit store is required")
	}
	if s.Path == "" {
		return Event{}, errors.New("audit path is required")
	}
	if event.EventID == "" {
		return Event{}, errors.New("audit event_id is required")
	}
	if event.EventType == "" {
		return Event{}, errors.New("audit event_type is required")
	}
	if err := ctx.Err(); err != nil {
		return Event{}, err
	}
	if event.RecordedAt.IsZero() {
		now := s.Now
		if now == nil {
			now = func() time.Time { return time.Now().UTC() }
		}
		event.RecordedAt = now().UTC()
	}

	line, err := json.Marshal(event)
	if err != nil {
		return Event{}, fmt.Errorf("encode audit event: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return Event{}, fmt.Errorf("create audit directory: %w", err)
	}
	f, err := os.OpenFile(s.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return Event{}, fmt.Errorf("open audit file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(line, '\n')); err != nil {
		return Event{}, fmt.Errorf("write audit event: %w", err)
	}
	return event, nil
}
