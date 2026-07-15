package governance

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/policyengine"
)

type policyDecisionTestEngine struct {
	decision policyengine.Decision
}

func (e policyDecisionTestEngine) Name() string { return "test" }

func (e policyDecisionTestEngine) Evaluate(context.Context, policyengine.Request) (policyengine.Decision, error) {
	return e.decision, nil
}

type policyDecisionRoundTripper func(*http.Request) (*http.Response, error)

func (f policyDecisionRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestPolicyDecisionPersistsSessionCreateAllowedAndDenied(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed bool
	}{
		{name: "allowed", allowed: true},
		{name: "denied", allowed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decisions := NewMemoryPolicyDecisionStore()
			auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
			decision := policyengine.Decision{Allowed: tc.allowed, Reason: "test decision", Engine: "test", DecisionID: "pol_create_" + tc.name, Findings: []string{"test finding"}}
			service := NewSessionService(SessionConfig{
				DevToken:        "test-token",
				Audit:           auditStore,
				PolicyEngine:    policyDecisionTestEngine{decision: decision},
				PolicyDecisions: decisions,
				NewID:           fixedIDs("sess_create_"+tc.name, "evt_create_"+tc.name),
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{"agent":"unit-tests","classification":"internal","prompt":"hello"}`))
			req.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()
			NewSessionHandler(service).ServeHTTP(rec, req)
			if want := map[bool]int{true: http.StatusCreated, false: http.StatusForbidden}[tc.allowed]; rec.Code != want {
				t.Fatalf("expected %d, got %d: %s", want, rec.Code, rec.Body.String())
			}
			assertSinglePersistedPolicyDecision(t, decisions, decision.DecisionID, tc.allowed, "session.create", tc.allowed)
			events, err := auditStore.AllEvents(context.Background())
			if err != nil || len(events) != 1 {
				t.Fatalf("audit events: events=%#v err=%v", events, err)
			}
			if events[0].PolicyDecisionID != decision.DecisionID {
				t.Fatalf("audit event missing decision ID: %#v", events[0])
			}
			if !tc.allowed && events[0].CorrelationSubject == "" {
				t.Fatalf("pre-create denial missing correlation ID: %#v", events[0])
			}
			if !tc.allowed {
				stored, found, err := decisions.GetPolicyDecision(context.Background(), decision.DecisionID)
				if err != nil || !found || events[0].CorrelationSubject != stored.CorrelationID {
					t.Fatalf("denial is not joinable to its decision: event=%#v decision=%#v err=%v", events[0], stored, err)
				}
			}
		})
	}
}

func TestPolicyDecisionPersistsAutoCreateAllowedAndDenied(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed bool
	}{
		{name: "allowed", allowed: true},
		{name: "denied", allowed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decisions := NewMemoryPolicyDecisionStore()
			auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
			decision := policyengine.Decision{Allowed: tc.allowed, Reason: "test decision", Engine: "test", DecisionID: "pol_auto_" + tc.name}
			service := NewSessionService(SessionConfig{
				Audit:           auditStore,
				Sessions:        &recordingSessionStore{},
				PolicyEngine:    policyDecisionTestEngine{decision: decision},
				PolicyDecisions: decisions,
				NewID:           fixedIDs("sess_auto_"+tc.name, "evt_auto_"+tc.name, "sgt_auto_"+tc.name),
			})
			_, err := service.CreateAutoGatewaySession(context.Background(), validAutoGatewaySessionRequest())
			if tc.allowed && err != nil {
				t.Fatalf("auto create: %v", err)
			}
			if !tc.allowed && (err == nil || !strings.Contains(err.Error(), "test decision")) {
				t.Fatalf("expected policy denial, got %v", err)
			}
			assertSinglePersistedPolicyDecision(t, decisions, decision.DecisionID, tc.allowed, "session.auto_create", tc.allowed)
			events, err := auditStore.AllEvents(context.Background())
			if err != nil || len(events) != 1 {
				t.Fatalf("audit events: events=%#v err=%v", events, err)
			}
			if events[0].PolicyDecisionID != decision.DecisionID {
				t.Fatalf("audit event missing decision ID: %#v", events[0])
			}
			if !tc.allowed && events[0].CorrelationSubject == "" {
				t.Fatalf("pre-create denial missing correlation ID: %#v", events[0])
			}
			if !tc.allowed {
				stored, found, err := decisions.GetPolicyDecision(context.Background(), decision.DecisionID)
				if err != nil || !found || events[0].CorrelationSubject != stored.CorrelationID {
					t.Fatalf("denial is not joinable to its decision: event=%#v decision=%#v err=%v", events[0], stored, err)
				}
			}
		})
	}
}

func TestPolicyDecisionPersistsDelegationAllowedAndDenied(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed bool
	}{
		{name: "allowed", allowed: true},
		{name: "denied", allowed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decisions := NewMemoryPolicyDecisionStore()
			auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
			sessions := &recordingSessionStore{}
			if err := sessions.Create(context.Background(), SessionRecord{SessionID: "sess_parent", ActorSubject: "dev@example.test", Agent: "parent", Classification: "internal", Status: "running", CreatedAt: time.Now().UTC()}); err != nil {
				t.Fatalf("seed parent session: %v", err)
			}
			decision := policyengine.Decision{Allowed: tc.allowed, Reason: "test decision", Engine: "test", DecisionID: "pol_delegate_" + tc.name}
			service := NewSessionService(SessionConfig{
				Audit:           auditStore,
				Sessions:        sessions,
				PolicyEngine:    policyDecisionTestEngine{decision: decision},
				PolicyDecisions: decisions,
				NewID:           fixedIDs("sess_child_"+tc.name, "evt_delegate_"+tc.name),
			})
			_, err := service.CreateDelegatedTaskSession(context.Background(), DelegatedTaskSessionRequest{ParentSessionID: "sess_parent", ActorSubject: "dev@example.test", Agent: "child", Description: "delegate"})
			if tc.allowed && err != nil {
				t.Fatalf("delegate: %v", err)
			}
			if !tc.allowed && (err == nil || !strings.Contains(err.Error(), "test decision")) {
				t.Fatalf("expected policy denial, got %v", err)
			}
			assertSinglePersistedPolicyDecision(t, decisions, decision.DecisionID, tc.allowed, "session.delegate", true)
			events, err := auditStore.AllEvents(context.Background())
			if err != nil || len(events) != 1 {
				t.Fatalf("audit events: events=%#v err=%v", events, err)
			}
			if events[0].PolicyDecisionID != decision.DecisionID {
				t.Fatalf("delegation audit event missing decision ID: %#v", events[0])
			}
			if !tc.allowed && events[0].SessionID != "sess_parent" {
				t.Fatalf("delegation denial must remain joined to its parent session: %#v", events[0])
			}
		})
	}
}

func TestPolicyDecisionPersistsMCPExecutionButNotCatalogPreflight(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed bool
	}{
		{name: "allowed", allowed: true},
		{name: "denied", allowed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decisions := NewMemoryPolicyDecisionStore()
			auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
			sessions := mcpSessionStore(t, "sess_mcp_policy", "documentation", "internal")
			defer func() { _ = sessions.(*SQLiteSessionStore).Close() }()
			decision := policyengine.Decision{Allowed: tc.allowed, Reason: "test decision", Engine: "test", DecisionID: "pol_mcp_" + tc.name}
			handler := NewMCPProxyHandler(MCPProxyConfig{
				ServiceToken:    "service-token",
				Audit:           auditStore,
				Sessions:        sessions,
				PolicyEngine:    policyDecisionTestEngine{decision: decision},
				PolicyDecisions: decisions,
				HTTPClient: &http.Client{Transport: policyDecisionRoundTripper(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
				})},
				Registrations: map[string]MCPProxyRegistration{
					"docs": {Endpoint: "http://mcp.invalid", AuthMode: "none", AllowedAgents: []string{"documentation"}, ToolAllow: []string{"search"}},
				},
			})
			req := httptest.NewRequest(http.MethodPost, "/internal/v1/mcp/docs/tools/search", bytes.NewReader([]byte(`{"query":"policy"}`)))
			req.Header.Set("Authorization", "Bearer service-token")
			req.Header.Set("X-AI-Orch-Session-ID", "sess_mcp_policy")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if want := map[bool]int{true: http.StatusOK, false: http.StatusForbidden}[tc.allowed]; rec.Code != want {
				t.Fatalf("expected %d, got %d: %s", want, rec.Code, rec.Body.String())
			}
			assertSinglePersistedPolicyDecision(t, decisions, decision.DecisionID, tc.allowed, "mcp.tool_call", true)
			events, err := auditStore.AllEvents(context.Background())
			if err != nil || len(events) != 1 || events[0].PolicyDecisionID != decision.DecisionID {
				t.Fatalf("mcp audit event missing decision ID: events=%#v err=%v", events, err)
			}
		})
	}

	decisions := NewMemoryPolicyDecisionStore()
	sessions := mcpSessionStore(t, "sess_catalog_policy", "documentation", "internal")
	defer func() { _ = sessions.(*SQLiteSessionStore).Close() }()
	handler := NewMCPProxyHandler(MCPProxyConfig{
		DevToken:        "dev-token",
		Audit:           audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions:        sessions,
		PolicyEngine:    policyDecisionTestEngine{decision: policyengine.Decision{Allowed: true, Reason: "allowed", Engine: "test", DecisionID: "pol_catalog"}},
		PolicyDecisions: decisions,
		Registrations: map[string]MCPProxyRegistration{
			"docs": {Endpoint: "http://mcp.invalid", AuthMode: "none", AllowedAgents: []string{"documentation"}, ToolAllow: []string{"search"}},
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/mcp/catalog", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_catalog_policy")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog: %d %s", rec.Code, rec.Body.String())
	}
	listed, err := decisions.ListPolicyDecisions(context.Background(), "")
	if err != nil || len(listed) != 0 {
		t.Fatalf("catalog preflight persisted decisions: decisions=%#v err=%v", listed, err)
	}
}

func assertSinglePersistedPolicyDecision(t *testing.T, store PolicyDecisionStore, decisionID string, allowed bool, actionType string, hasSession bool) {
	t.Helper()
	record, found, err := store.GetPolicyDecision(context.Background(), decisionID)
	if err != nil || !found {
		t.Fatalf("get persisted decision: found=%v err=%v", found, err)
	}
	if record.Allowed != allowed || record.ActionType != actionType {
		t.Fatalf("unexpected decision: %#v", record)
	}
	if hasSession && record.SessionID == "" {
		t.Fatalf("expected session-linked decision: %#v", record)
	}
	if !hasSession && record.CorrelationID == "" {
		t.Fatalf("expected pre-create correlation ID: %#v", record)
	}
}

var _ policyengine.Engine = policyDecisionTestEngine{}
var _ http.RoundTripper = policyDecisionRoundTripper(nil)
