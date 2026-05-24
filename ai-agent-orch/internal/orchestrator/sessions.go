package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"ai-agent-orch/internal/audit"
)

type AuditStore interface {
	Append(context.Context, audit.Event) (audit.Event, error)
}

type SessionIntakeConfig struct {
	Audit AuditStore
	NewID func(prefix string) string
}

type SessionIntake struct {
	audit AuditStore
	newID func(prefix string) string
}

type AcceptSessionRequest struct {
	Agent string `json:"agent"`
}

type AcceptSessionResponse struct {
	SessionID    string `json:"session_id"`
	Status       string `json:"status"`
	Agent        string `json:"agent"`
	AuditEventID string `json:"audit_event_id"`
}

func NewSessionIntake(cfg SessionIntakeConfig) *SessionIntake {
	newID := cfg.NewID
	if newID == nil {
		newID = randomID
	}
	return &SessionIntake{
		audit: cfg.Audit,
		newID: newID,
	}
}

func NewSessionIntakeHandler(service *SessionIntake) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orchestrator/sessions", service.acceptSession)
	return mux
}

func (s *SessionIntake) acceptSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if s == nil || s.audit == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session intake unavailable"})
		return
	}

	sessionID := r.Header.Get("X-AI-Orch-Session-ID")
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "X-AI-Orch-Session-ID header is required"})
		return
	}

	var request AcceptSessionRequest
	if err := readJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if err := request.validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	eventID := s.newID("evt")
	event, err := s.audit.Append(r.Context(), audit.Event{
		EventID:            eventID,
		SessionID:          sessionID,
		EventType:          "orchestrator.session.accepted",
		Actor:              "local-dev",
		Agent:              request.Agent,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "orchestrator",
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}

	writeJSON(w, http.StatusAccepted, AcceptSessionResponse{
		SessionID:    sessionID,
		Status:       "accepted",
		Agent:        request.Agent,
		AuditEventID: event.EventID,
	})
}

func (r AcceptSessionRequest) validate() error {
	if r.Agent == "" {
		return errors.New("agent is required")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

const maxRequestBodyBytes = 1 << 20

func readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func randomID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_fallback", prefix)
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
