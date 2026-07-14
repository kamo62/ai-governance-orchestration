package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/governance"
)

type testHealthBackend struct {
	failuresRemaining int
	attempts          int
}

func (b *testHealthBackend) Health(context.Context) error {
	b.attempts++
	if b.failuresRemaining > 0 {
		b.failuresRemaining--
		return errors.New("not ready")
	}
	return nil
}

func TestHasSQLiteExt(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "plain db", path: "audit.db", want: true},
		{name: "sqlite", path: "audit.sqlite", want: true},
		{name: "sqlite3 uppercase", path: "AUDIT.SQLITE3", want: true},
		{name: "short non-match", path: "db", want: false},
		{name: "jsonl", path: "audit.jsonl", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasSQLiteExt(tt.path); got != tt.want {
				t.Fatalf("hasSQLiteExt(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestNewAuditStoreUsesSQLiteForDatabasePath(t *testing.T) {
	store, err := newAuditStore(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("new audit store: %v", err)
	}
	sqliteStore, ok := store.(*audit.SQLiteStore)
	if !ok {
		t.Fatalf("expected sqlite audit store, got %T", store)
	}
	defer sqliteStore.Close()
}

func TestNewAuditStoreReturnsSQLiteErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("create directory at db path: %v", err)
	}
	if _, err := newAuditStore(path); err == nil {
		t.Fatal("expected sqlite audit store error")
	}
}

func TestWaitForModelBackendHealthRetriesUntilReady(t *testing.T) {
	backend := &testHealthBackend{failuresRemaining: 2}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := waitForModelBackendHealth(ctx, backend, time.Millisecond); err != nil {
		t.Fatalf("waitForModelBackendHealth returned error: %v", err)
	}
	if backend.attempts != 3 {
		t.Fatalf("expected 3 health attempts, got %d", backend.attempts)
	}
}

func TestWaitForModelBackendHealthReturnsTimeout(t *testing.T) {
	backend := &testHealthBackend{failuresRemaining: 100}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	err := waitForModelBackendHealth(ctx, backend, time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded error, got %v", err)
	}
}

func TestAuthModeMiddlewareDeniesUnannotatedOrUnknownRoutes(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		build func() http.Handler
	}{
		{
			name: "unannotated",
			path: "/unannotated",
			build: func() http.Handler {
				mux := http.NewServeMux()
				mux.Handle("/unannotated", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				}))
				return authModeMiddleware(nil, mux, nil)
			},
		},
		{
			name: "unknown mode",
			path: "/unknown",
			build: func() http.Handler {
				routes := newAuthRouter()
				routes.Handle(authMode("unknown"), "/unknown", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				}))
				return routes.Handler(nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.build().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAuthModeMiddlewareKeepsPublicRoutesUnauthenticated(t *testing.T) {
	routes := newAuthRouter()
	for _, route := range []struct {
		mode    authMode
		pattern string
	}{
		{authPublic, "/ui"},
		{authPublic, "/ui/"},
		{authPublic, "/mcp/healthz"},
		{authPublic, "/readyz"},
		{authPublic, "/healthz"},
		{authPublic, "/metrics"},
		{authSelf, "/internal/v1/model/"},
		{authSelf, "/internal/v1/mcp/"},
		{authSelf, "/v1/mcp/"},
		{authSelf, "/v1/managed-client/evidence"},
	} {
		routes.Handle(route.mode, route.pattern, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
	}

	for _, path := range []string{
		"/ui", "/ui/demo", "/mcp/healthz", "/readyz", "/healthz", "/metrics",
		"/internal/v1/model/demo", "/internal/v1/mcp/demo", "/v1/mcp/demo", "/v1/managed-client/evidence",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			routes.Handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusNoContent {
				t.Fatalf("expected unauthenticated route to reach handler, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestManagedClientEvidenceSelfAuthRoute(t *testing.T) {
	now := time.Date(2026, 7, 11, 9, 0, 0, 0, time.UTC)
	credentials, err := governance.NewSQLiteDeveloperCredentialStore(":memory:")
	if err != nil {
		t.Fatalf("new credential store: %v", err)
	}
	defer credentials.Close()
	_, token, err := credentials.Issue(context.Background(), governance.DeveloperCredentialIssue{
		ActorSubject: "demo-managed-client",
		Client:       "demo-managed-client",
		Now:          now,
	})
	if err != nil {
		t.Fatalf("issue credential: %v", err)
	}
	sessions, err := governance.NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	defer sessions.Close()
	routes := newAuthRouter()
	routes.Handle(authSelf, "/v1/managed-client/evidence", governance.NewManagedClientEvidenceHandler(governance.ManagedClientEvidenceConfig{
		Credentials: credentials,
		Audit:       audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions:    sessions,
		Now:         func() time.Time { return now },
	}))
	body := []byte(`{"events":[{"event_id":"cev_demo_managed_client_session_start","schema_version":"v0","client":"demo-managed-client","client_session_id":"demo-managed-client-session","event_type":"session_start","timestamp":"2026-07-11T09:00:00Z"}]}`)

	for _, tc := range []struct {
		name  string
		token string
		want  int
	}{
		{name: "bad token", token: "air_bad", want: http.StatusUnauthorized},
		{name: "good token", token: token, want: http.StatusAccepted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/managed-client/evidence", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rec := httptest.NewRecorder()
			routes.Handler(nil).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRegisterRegistryHandlersIncludesEvidenceAndCacheRoutes(t *testing.T) {
	sessionStore, err := governance.NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	defer sessionStore.Close()
	if err := sessionStore.Create(context.Background(), governance.SessionRecord{
		SessionID:      "sess_registry",
		ActorSubject:   "local-dev",
		Agent:          "unit-tests",
		Classification: "internal",
		PromptSHA256:   "abc123",
		Status:         "created",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	service := governance.NewSessionService(governance.SessionConfig{
		DevToken: "test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: sessionStore,
	})
	handler := governance.NewRegistryHandlerWithMetrics(governance.NewRegistryStore(), service, nil)
	routes := newAuthRouter()
	registerRegistryHandlers(routes, handler)

	for _, tc := range []struct {
		name string
		path string
		body map[string]any
	}{
		{
			name: "cache outcomes",
			path: "/v1/cache-outcomes",
			body: map[string]any{
				"session_id":     "sess_registry",
				"cache_scope":    "session",
				"cache_key_hash": "sha256:abc",
				"hit":            true,
			},
		},
		{
			name: "evidence",
			path: "/v1/evidence",
			body: map[string]any{
				"session_id":    "sess_registry",
				"evidence_type": "test_result",
				"description":   "unit tests passed",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()
			routes.Handler(service).ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRegisterAdminRegistryHandlersIncludesEvidenceDecisionRoutes(t *testing.T) {
	routes := newAuthRouter()
	registerAdminRegistryHandlers(routes, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	service := governance.NewSessionService(governance.SessionConfig{AdminToken: "test-admin"})
	for _, path := range []string{
		"/v1/admin/evidence",
		"/v1/admin/evidence/ev_1/confirm",
		"/v1/admin/evidence/ev_1/reject",
		"/v1/admin/cache-outcomes",
		"/v1/admin/reporting/maturity-governance",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer test-admin")
		rec := httptest.NewRecorder()
		routes.Handler(service).ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("route %s was not registered: got %d", path, rec.Code)
		}
	}
}
