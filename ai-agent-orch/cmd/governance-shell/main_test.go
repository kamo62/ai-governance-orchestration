package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/governance"
)

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

func TestRegisterRegistryHandlersIncludesEvidenceAndCacheRoutes(t *testing.T) {
	sessionStore, err := governance.NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	defer sessionStore.Close()
	if err := sessionStore.Create(context.Background(), governance.SessionRecord{
		SessionID:      "sess_registry",
		ActorSubject:   "local-dev",
		Agent:          "test-generation",
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
	handler := governance.NewRegistryHandler(governance.NewRegistryStore(), service)
	mux := http.NewServeMux()
	registerRegistryHandlers(mux, handler)

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
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
