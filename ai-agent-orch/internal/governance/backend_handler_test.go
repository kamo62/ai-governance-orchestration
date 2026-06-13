package governance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeBackendExecutor struct {
	name string
	args []string
	dir  string
}

func (f *fakeBackendExecutor) Run(_ context.Context, name string, args []string, dir string) (string, error) {
	f.name = name
	f.args = args
	f.dir = dir
	return "ok", nil
}

func TestBackendHandlerReturnsCommands(t *testing.T) {
	h := NewBackendHandler(BackendHandlerConfig{CurrentBackend: "bifrost", GatewayOptions: []GatewayOption{{ID: "bifrost", Label: "Bifrost"}}})
	req := httptest.NewRequest(http.MethodGet, "/v1/backends", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		CurrentBackend string            `json:"current_backend"`
		Commands       map[string]string `json:"commands"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.CurrentBackend != "bifrost" || body.Commands["copilot-user"] == "" {
		t.Fatalf("unexpected body %#v", body)
	}
}

func TestBackendHandlerRunsAdminActionWhenEnabled(t *testing.T) {
	exec := &fakeBackendExecutor{}
	h := NewBackendHandler(BackendHandlerConfig{CurrentBackend: "bifrost", AdminToken: "admin", ControlEnabled: true, WorkDir: "/repo", Executor: exec})
	req := httptest.NewRequest(http.MethodPost, "/v1/backends", strings.NewReader(`{"backend":"copilot-user","action":"up"}`))
	req.Header.Set("Authorization", "Bearer admin")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if exec.name != "docker" || exec.dir != "/repo" || !strings.Contains(strings.Join(exec.args, " "), "docker-compose.copilot.yml") {
		t.Fatalf("unexpected command: %s %v dir=%s", exec.name, exec.args, exec.dir)
	}
}

func TestBackendHandlerRejectsActionWhenDisabled(t *testing.T) {
	h := NewBackendHandler(BackendHandlerConfig{AdminToken: "admin"})
	req := httptest.NewRequest(http.MethodPost, "/v1/backends", strings.NewReader(`{"backend":"bifrost","action":"up"}`))
	req.Header.Set("Authorization", "Bearer admin")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}
