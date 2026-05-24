package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/dispatch"
)

func TestDispatchHandlerFailsClosedWhenAuditWriteFails(t *testing.T) {
	dispatcher := &Dispatcher{
		catalogRoot: filepath.Join("..", ".."),
		runtimes: map[string]dispatch.Runtime{
			"opencode": fakeRuntime{handle: &fakeHandle{events: []dispatch.RuntimeEvent{{Type: "done", Payload: "ok"}}}},
		},
	}
	handler := NewDispatchHandler(dispatcher, failingAuditStore{})

	req := httptest.NewRequest(http.MethodPost, "/v1/orchestrator/dispatch", bytes.NewReader([]byte(`{"agent":"test-generation","prompt":"write tests"}`)))
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

type failingAuditStore struct{}

func (failingAuditStore) Append(context.Context, audit.Event) (audit.Event, error) {
	return audit.Event{}, errors.New("disk full")
}

type fakeRuntime struct {
	handle dispatch.SessionHandle
	err    error
}

func (f fakeRuntime) StartSession(context.Context, dispatch.SessionConfig) (dispatch.SessionHandle, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.handle, nil
}

type fakeHandle struct {
	events []dispatch.RuntimeEvent
}

func (h *fakeHandle) Wait() error { return nil }

func (h *fakeHandle) Events() <-chan dispatch.RuntimeEvent {
	ch := make(chan dispatch.RuntimeEvent, len(h.events))
	for _, event := range h.events {
		ch <- event
	}
	close(ch)
	return ch
}

func (h *fakeHandle) Stop() error { return nil }
