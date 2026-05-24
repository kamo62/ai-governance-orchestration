package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxClosedSessionHistory = 256

// sseGlobalLimiter limits concurrent SSE connections to prevent DoS.
// Capacity of 500 is a reasonable default for local Phase 2.
var sseGlobalLimiter = make(chan struct{}, 500)

func acquireSSE(ctx context.Context) error {
	select {
	case sseGlobalLimiter <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseSSE() {
	select {
	case <-sseGlobalLimiter:
	default:
	}
}

// SessionEvent is a single event in a session stream.
type SessionEvent struct {
	Type      string    `json:"type"` // router, stream, patch, error, done
	Payload   string    `json:"payload"`
	Timestamp time.Time `json:"timestamp"`
}

// EventStore holds session events in memory for SSE streaming.
type EventStore struct {
	mu          sync.RWMutex
	chans       map[string][]chan SessionEvent
	closed      map[string]bool
	history     map[string][]SessionEvent
	closedOrder []string
}

func NewEventStore() *EventStore {
	return &EventStore{
		chans:   make(map[string][]chan SessionEvent),
		closed:  make(map[string]bool),
		history: make(map[string][]SessionEvent),
	}
}

// Subscribe creates a new event channel for a session. Callers must call Unsubscribe.
func (s *EventStore) Subscribe(sessionID string) <-chan SessionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	bufferSize := 64
	if len(s.history[sessionID]) > bufferSize {
		bufferSize = len(s.history[sessionID])
	}
	ch := make(chan SessionEvent, bufferSize)
	for _, event := range s.history[sessionID] {
		ch <- event
	}
	if s.closed[sessionID] {
		close(ch)
		return ch
	}
	s.chans[sessionID] = append(s.chans[sessionID], ch)
	return ch
}

// Unsubscribe removes a channel from the session.
func (s *EventStore) Unsubscribe(sessionID string, ch <-chan SessionEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.chans[sessionID]
	for i, c := range list {
		if c == ch {
			s.chans[sessionID] = append(list[:i], list[i+1:]...)
			close(c)
			break
		}
	}
}

// Publish sends an event to all subscribers of a session.
func (s *EventStore) Publish(sessionID string, event SessionEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed[sessionID] {
		return
	}
	s.appendHistoryLocked(sessionID, event)
	for _, ch := range s.chans[sessionID] {
		select {
		case ch <- event:
		default:
			// Channel full, drop event rather than block.
		}
	}
}

// Close marks a session as closed and notifies subscribers with a done event.
func (s *EventStore) Close(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed[sessionID] {
		return
	}
	s.closed[sessionID] = true
	s.closedOrder = append(s.closedOrder, sessionID)
	done := SessionEvent{Type: "done", Timestamp: time.Now().UTC()}
	sendDone := !lastEventIsDone(s.history[sessionID])
	if sendDone {
		s.appendHistoryLocked(sessionID, done)
	}
	for _, ch := range s.chans[sessionID] {
		if sendDone {
			select {
			case ch <- done:
			default:
			}
		}
		close(ch)
	}
	delete(s.chans, sessionID)
	s.evictClosedHistoryLocked()
}

func (s *EventStore) evictClosedHistoryLocked() {
	for len(s.closedOrder) > maxClosedSessionHistory {
		oldest := s.closedOrder[0]
		s.closedOrder = s.closedOrder[1:]
		if !s.closed[oldest] {
			continue
		}
		delete(s.closed, oldest)
		delete(s.history, oldest)
		delete(s.chans, oldest)
	}
}

func (s *EventStore) appendHistoryLocked(sessionID string, event SessionEvent) {
	const maxHistory = 128
	s.history[sessionID] = append(s.history[sessionID], event)
	if len(s.history[sessionID]) > maxHistory {
		s.history[sessionID] = s.history[sessionID][len(s.history[sessionID])-maxHistory:]
	}
}

func lastEventIsDone(events []SessionEvent) bool {
	return len(events) > 0 && events[len(events)-1].Type == "done"
}

// SSEWriter handles Server-Sent Events for a session.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return &SSEWriter{w: w, flusher: flusher}, nil
}

func (s *SSEWriter) WriteEvent(event SessionEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.w, "data: %s\n\n", string(data))
	if err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *SSEWriter) WriteComment(comment string) error {
	_, err := fmt.Fprintf(s.w, ": %s\n", strings.ReplaceAll(comment, "\n", " "))
	if err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// EventsHandler serves GET /v1/sessions/{id}/events as SSE.
type EventsHandler struct {
	store *EventStore
}

func NewEventsHandler(store *EventStore) http.Handler {
	return &EventsHandler{store: store}
}

func (h *EventsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h == nil || h.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "event store unavailable"})
		return
	}

	if err := acquireSSE(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "server busy"})
		return
	}
	defer releaseSSE()

	sessionID := extractSessionID(r.URL.Path, "/v1/sessions/", "/events")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}

	sse, err := NewSSEWriter(w)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming unsupported"})
		return
	}

	_ = sse.WriteComment("session " + sessionID)

	ch := h.store.Subscribe(sessionID)
	defer h.store.Unsubscribe(sessionID, ch)

	// Send a keepalive every 15 seconds to prevent timeouts.
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return
			}
			if err := sse.WriteEvent(event); err != nil {
				return
			}
			if event.Type == "done" || event.Type == "error" {
				return
			}
		case <-keepalive.C:
			if err := sse.WriteComment("keepalive"); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
