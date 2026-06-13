package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/dispatch"
)

func TestDispatchHandlerFailsClosedWhenAuditWriteFails(t *testing.T) {
	dispatcher := &Dispatcher{
		catalogRoot: filepath.Join("..", ".."),
		broker:      mustToolBroker(t),
		runtimes: map[string]dispatch.Runtime{
			"opencode": fakeRuntime{handle: &fakeHandle{events: []dispatch.RuntimeEvent{{Type: "done", Payload: "ok"}}}},
		},
	}
	handler := NewDispatchHandler(dispatcher, failingAuditStore{})

	req := httptest.NewRequest(http.MethodPost, "/v1/orchestrator/dispatch", bytes.NewReader([]byte(`{"agent":"unit-tests","prompt":"write tests"}`)))
	req.Header.Set("X-AI-Orch-Session-ID", "sess_dispatch_1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "audit write failed") {
		t.Fatalf("expected audit failure response, got %s", rec.Body.String())
	}
}

func TestDispatchHandlerFailsWhenRuntimeWaitFails(t *testing.T) {
	dispatcher := &Dispatcher{
		catalogRoot: filepath.Join("..", ".."),
		broker:      mustToolBroker(t),
		runtimes: map[string]dispatch.Runtime{
			"opencode": fakeRuntime{handle: &fakeHandle{
				events: []dispatch.RuntimeEvent{{Type: "stream", Payload: "started"}},
				err:    errors.New("runtime failed"),
			}},
		},
	}
	handler := NewDispatchHandler(dispatcher, audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")))

	req := httptest.NewRequest(http.MethodPost, "/v1/orchestrator/dispatch", bytes.NewReader([]byte(`{"agent":"unit-tests","prompt":"write tests"}`)))
	req.Header.Set("X-AI-Orch-Session-ID", "sess_dispatch_1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "runtime failed") {
		t.Fatalf("expected runtime failure response, got %s", rec.Body.String())
	}
}

func TestDispatchHandlerFailsWhenRuntimeEmitsError(t *testing.T) {
	dispatcher := &Dispatcher{
		catalogRoot: filepath.Join("..", ".."),
		broker:      mustToolBroker(t),
		runtimes: map[string]dispatch.Runtime{
			"opencode": fakeRuntime{handle: &fakeHandle{
				events: []dispatch.RuntimeEvent{{Type: "error", Payload: "tool denied"}},
			}},
		},
	}
	handler := NewDispatchHandler(dispatcher, audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")))

	req := httptest.NewRequest(http.MethodPost, "/v1/orchestrator/dispatch", bytes.NewReader([]byte(`{"agent":"unit-tests","prompt":"write tests"}`)))
	req.Header.Set("X-AI-Orch-Session-ID", "sess_dispatch_1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tool denied") {
		t.Fatalf("expected runtime error response, got %s", rec.Body.String())
	}
}

func TestDispatchHandlerAuditsFailClosedRuntimeUnavailable(t *testing.T) {
	t.Setenv("AI_ORCH_BETA_SMOKE", "false")
	t.Cleanup(func() { os.Unsetenv("AI_ORCH_BETA_SMOKE") })

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	dispatcher := &Dispatcher{
		catalogRoot: filepath.Join("..", ".."),
		broker:      mustToolBroker(t),
		runtimes:    map[string]dispatch.Runtime{},
	}
	handler := NewDispatchHandler(dispatcher, audit.NewFileStore(auditPath))

	req := httptest.NewRequest(http.MethodPost, "/v1/orchestrator/dispatch", bytes.NewReader([]byte(`{"agent":"unit-tests","prompt":"write tests"}`)))
	req.Header.Set("X-AI-Orch-Session-ID", "sess_no_runtime")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "failed_closed") {
		t.Fatalf("expected fail-closed response, got %s", rec.Body.String())
	}
	auditBytes, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	auditText := string(auditBytes)
	for _, want := range []string{
		`"session_id":"sess_no_runtime"`,
		`"event_type":"specialist.dispatch_failed"`,
		`"runtime_status":"failed_closed"`,
		`"correlation_subject":"orchestrator"`,
	} {
		if !strings.Contains(auditText, want) {
			t.Fatalf("missing %s in audit event: %s", want, auditText)
		}
	}
	if strings.Contains(auditText, "write tests") {
		t.Fatalf("dispatch failure audit event must not store raw prompt: %s", auditText)
	}
}

func TestDispatchHandlerRuntimeContextSurvivesCallerCancellation(t *testing.T) {
	var runtimeCtxErr error
	dispatcher := &Dispatcher{
		catalogRoot: filepath.Join("..", ".."),
		broker:      mustToolBroker(t),
		runtimes: map[string]dispatch.Runtime{
			"opencode": fakeRuntime{
				handle: &fakeHandle{events: []dispatch.RuntimeEvent{{Type: "done", Payload: "ok"}}},
				onStart: func(ctx context.Context) {
					runtimeCtxErr = ctx.Err()
				},
			},
		},
	}
	handler := NewDispatchHandler(dispatcher, audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")))

	baseCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/orchestrator/dispatch", bytes.NewReader([]byte(`{"agent":"unit-tests","prompt":"write tests"}`))).WithContext(baseCtx)
	req.Header.Set("X-AI-Orch-Session-ID", "sess_dispatch_cancelled")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if runtimeCtxErr != nil {
		t.Fatalf("expected runtime context to survive caller cancellation, got %v", runtimeCtxErr)
	}
}

type failingAuditStore struct{}

func (failingAuditStore) Append(context.Context, audit.Event) (audit.Event, error) {
	return audit.Event{}, errors.New("disk full")
}

type fakeRuntime struct {
	handle  dispatch.SessionHandle
	err     error
	onStart func(context.Context)
}

func (f fakeRuntime) StartSession(ctx context.Context, _ dispatch.SessionConfig) (dispatch.SessionHandle, error) {
	if f.onStart != nil {
		f.onStart(ctx)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.handle, nil
}

type fakeHandle struct {
	events []dispatch.RuntimeEvent
	err    error
}

func (h *fakeHandle) Wait() error { return h.err }

func (h *fakeHandle) Events() <-chan dispatch.RuntimeEvent {
	ch := make(chan dispatch.RuntimeEvent, len(h.events))
	for _, event := range h.events {
		ch <- event
	}
	close(ch)
	return ch
}

func (h *fakeHandle) Stop() error { return nil }
