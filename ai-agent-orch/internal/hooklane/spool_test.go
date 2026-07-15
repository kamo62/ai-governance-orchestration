package hooklane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/governance"
)

// sequencedEvidenceServer answers the i-th POST /v1/evidence request with
// statuses[i] (repeating the last entry once exhausted; 200 if statuses is
// empty), recording each request's decoded JSON body in call order. Any
// other request gets a plain 200.
func sequencedEvidenceServer(t *testing.T, statuses []int) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/evidence" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			idx := len(bodies)
			bodies = append(bodies, body)
			status := http.StatusOK
			switch {
			case len(statuses) == 0:
			case idx < len(statuses):
				status = statuses[idx]
			default:
				status = statuses[len(statuses)-1]
			}
			if status >= 400 {
				http.Error(w, "status", status)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

// TestRunSpoolsEvidenceOnNetworkOr5xxFailure verifies a 5xx evidence POST is
// queued to the spool file instead of being lost, and that Run reports
// success (no error) with Spooled=true.
func TestRunSpoolsEvidenceOnNetworkOr5xxFailure(t *testing.T) {
	srv, bodies := sequencedEvidenceServer(t, []int{http.StatusServiceUnavailable})
	client := NewClient(srv.URL, "dev-token")
	ws := Workspace{Dir: t.TempDir()}

	result, err := Run(context.Background(), client, PostTool, Event{SessionID: "sess_x", ToolName: "Edit"}, ws)
	if err != nil {
		t.Fatalf("expected the 503 to be absorbed by spooling, got %v", err)
	}
	if !result.Spooled {
		t.Fatal("expected the current event to be reported as spooled")
	}
	if len(*bodies) != 1 {
		t.Fatalf("expected exactly 1 delivery attempt, got %d", len(*bodies))
	}

	lines, err := readSpoolLines(spoolPath(ws.Dir))
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 spooled line, got %d", len(lines))
	}
	var spooled map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &spooled); err != nil {
		t.Fatalf("spooled line is not valid JSON: %v", err)
	}
	if spooled["session_id"] != "sess_x" {
		t.Fatalf("unexpected spooled session_id: %v", spooled["session_id"])
	}
	if id, _ := spooled["client_event_id"].(string); strings.TrimSpace(id) == "" {
		t.Fatalf("expected a non-empty client_event_id in the spooled body, got %#v", spooled["client_event_id"])
	}
}

// TestRunDoesNotSpool4xxRejection verifies a 4xx evidence POST is reported
// exactly as before this change: Run returns the HTTPError, Spooled stays
// false, and nothing is written to the spool.
func TestRunDoesNotSpool4xxRejection(t *testing.T) {
	srv, bodies := sequencedEvidenceServer(t, []int{http.StatusBadRequest})
	client := NewClient(srv.URL, "dev-token")
	ws := Workspace{Dir: t.TempDir()}

	result, err := Run(context.Background(), client, PostTool, Event{SessionID: "sess_x", ToolName: "Edit"}, ws)
	if err == nil {
		t.Fatal("expected the 400 rejection to be returned as an error")
	}
	if httpErr, ok := err.(HTTPError); !ok || httpErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected an HTTPError(400), got %#v", err)
	}
	if result.Spooled {
		t.Fatal("expected a 4xx rejection to never be spooled")
	}
	if len(*bodies) != 1 {
		t.Fatalf("expected exactly 1 delivery attempt, got %d", len(*bodies))
	}
	if _, statErr := os.Stat(spoolPath(ws.Dir)); !os.IsNotExist(statErr) {
		t.Fatal("expected no spool file to be created for a 4xx rejection")
	}
}

// TestFlushSpoolDeliversInOrderAndStopsOnFirstFailure verifies flush attempts
// queued lines strictly in order and, on the first network/5xx failure,
// keeps that line and everything after it queued instead of skipping ahead.
func TestFlushSpoolDeliversInOrderAndStopsOnFirstFailure(t *testing.T) {
	ws := Workspace{Dir: t.TempDir()}
	lineA := mustJSON(map[string]any{"description": "A", "client_event_id": "cev_a"})
	lineB := mustJSON(map[string]any{"description": "B", "client_event_id": "cev_b"})
	lineC := mustJSON(map[string]any{"description": "C", "client_event_id": "cev_c"})
	if err := writeSpoolLines(spoolPath(ws.Dir), []string{lineA, lineB, lineC}); err != nil {
		t.Fatalf("seed spool: %v", err)
	}

	srv, bodies := sequencedEvidenceServer(t, []int{http.StatusOK, http.StatusServiceUnavailable})
	client := NewClient(srv.URL, "dev-token")

	notes := flushSpool(context.Background(), client, ws, GitContext{})

	if len(*bodies) != 2 {
		t.Fatalf("expected exactly 2 delivery attempts (A then B, C never reached), got %d", len(*bodies))
	}
	if (*bodies)[0]["description"] != "A" || (*bodies)[1]["description"] != "B" {
		t.Fatalf("expected A then B in order, got %v", *bodies)
	}

	remaining, err := readSpoolLines(spoolPath(ws.Dir))
	if err != nil {
		t.Fatalf("read remaining spool: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected B and C to remain queued, got %d line(s): %v", len(remaining), remaining)
	}
	if !strings.Contains(remaining[0], `"B"`) || !strings.Contains(remaining[1], `"C"`) {
		t.Fatalf("expected the remaining lines to be B then C in their original order, got %v", remaining)
	}
	if len(notes) == 0 {
		t.Fatal("expected a note describing the stopped flush")
	}
}

// TestFlushSpoolDropsRejected4xxLines verifies a rejected (4xx) spooled line
// is dropped rather than requeued, while flush continues on to later lines.
func TestFlushSpoolDropsRejected4xxLines(t *testing.T) {
	ws := Workspace{Dir: t.TempDir()}
	lineX := mustJSON(map[string]any{"description": "X", "client_event_id": "cev_x"})
	lineY := mustJSON(map[string]any{"description": "Y", "client_event_id": "cev_y"})
	if err := writeSpoolLines(spoolPath(ws.Dir), []string{lineX, lineY}); err != nil {
		t.Fatalf("seed spool: %v", err)
	}

	srv, bodies := sequencedEvidenceServer(t, []int{http.StatusBadRequest, http.StatusOK})
	client := NewClient(srv.URL, "dev-token")

	notes := flushSpool(context.Background(), client, ws, GitContext{})

	if len(*bodies) != 2 {
		t.Fatalf("expected both X (rejected) and Y (delivered) to be attempted, got %d", len(*bodies))
	}
	if (*bodies)[0]["description"] != "X" || (*bodies)[1]["description"] != "Y" {
		t.Fatalf("expected X then Y in order, got %v", *bodies)
	}

	remaining, err := readSpoolLines(spoolPath(ws.Dir))
	if err != nil {
		t.Fatalf("read remaining spool: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected the spool to be empty (X dropped, Y delivered), got %v", remaining)
	}
	foundDropNote := false
	for _, note := range notes {
		if strings.Contains(note, "rejected") {
			foundDropNote = true
		}
	}
	if !foundDropNote {
		t.Fatalf("expected a note about the dropped rejection, got %v", notes)
	}
}

// TestFlushSpoolSkipsCorruptLines verifies a line that fails to parse as
// JSON is dropped without being posted, and flush continues to later lines.
func TestFlushSpoolSkipsCorruptLines(t *testing.T) {
	ws := Workspace{Dir: t.TempDir()}
	lineZ := mustJSON(map[string]any{"description": "Z", "client_event_id": "cev_z"})
	if err := writeSpoolLines(spoolPath(ws.Dir), []string{"not valid json{{", lineZ}); err != nil {
		t.Fatalf("seed spool: %v", err)
	}

	srv, bodies := sequencedEvidenceServer(t, []int{http.StatusOK})
	client := NewClient(srv.URL, "dev-token")

	notes := flushSpool(context.Background(), client, ws, GitContext{})

	if len(*bodies) != 1 {
		t.Fatalf("expected only the valid line Z to be posted, got %d attempt(s)", len(*bodies))
	}
	if (*bodies)[0]["description"] != "Z" {
		t.Fatalf("expected the delivered line to be Z, got %v", (*bodies)[0])
	}
	remaining, err := readSpoolLines(spoolPath(ws.Dir))
	if err != nil {
		t.Fatalf("read remaining spool: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected the spool to be empty after dropping the corrupt line and delivering Z, got %v", remaining)
	}
	foundCorruptNote := false
	for _, note := range notes {
		if strings.Contains(note, "corrupt") {
			foundCorruptNote = true
		}
	}
	if !foundCorruptNote {
		t.Fatalf("expected a note about the skipped corrupt line, got %v", notes)
	}
}

// TestAppendToSpoolDropsOldestWhenFull verifies the spool is bounded at
// spoolMaxFiles: once full, the oldest queued event is dropped to make room
// for the newest one, and a note describes the drop.
func TestAppendToSpoolDropsOldestWhenFull(t *testing.T) {
	ws := Workspace{Dir: t.TempDir()}

	var lastNote string
	for i := 0; i <= spoolMaxFiles; i++ {
		note, err := appendToSpool(ws, map[string]any{"marker": i})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		lastNote = note
	}

	lines, err := readSpoolLines(spoolPath(ws.Dir))
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	if len(lines) != spoolMaxFiles {
		t.Fatalf("expected the spool bounded at %d files, got %d", spoolMaxFiles, len(lines))
	}

	var oldest, newest map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &oldest); err != nil {
		t.Fatalf("decode oldest remaining line: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &newest); err != nil {
		t.Fatalf("decode newest line: %v", err)
	}
	// marker 0 was dropped to make room; marker 1 is now the oldest survivor,
	// and marker spoolMaxFiles (the 501st append) is the newest.
	if int(oldest["marker"].(float64)) != 1 {
		t.Fatalf("expected marker 0 to have been dropped, oldest remaining is %v", oldest["marker"])
	}
	if int(newest["marker"].(float64)) != spoolMaxFiles {
		t.Fatalf("expected the newest line to be the last append, got %v", newest["marker"])
	}
	if lastNote == "" || !strings.Contains(lastNote, "dropped") {
		t.Fatalf("expected the overflowing append to note the drop, got %q", lastNote)
	}
}

func TestAppendToSpoolConcurrentWritersPreserveBothEvents(t *testing.T) {
	ws := Workspace{Dir: t.TempDir()}
	var wg sync.WaitGroup
	for _, marker := range []string{"A", "B"} {
		marker := marker
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := appendToSpool(ws, map[string]any{"marker": marker}); err != nil {
				t.Errorf("append %s: %v", marker, err)
			}
		}()
	}
	wg.Wait()
	lines, err := readSpoolLines(spoolPath(ws.Dir))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range lines {
		var body map[string]any
		if err := json.Unmarshal([]byte(line), &body); err != nil {
			t.Fatal(err)
		}
		seen[body["marker"].(string)] = true
	}
	if len(lines) != 2 || !seen["A"] || !seen["B"] {
		t.Fatalf("concurrent append lost an event: %v", lines)
	}
	if info, err := os.Stat(spoolPath(ws.Dir)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("spool directory mode = %v, err=%v", info, err)
	}
}

func TestFlushSpoolImportsLegacyJSONL(t *testing.T) {
	ws := Workspace{Dir: t.TempDir()}
	kiroDir := filepath.Join(ws.Dir, ".kiro")
	if err := os.MkdirAll(kiroDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(kiroDir, legacySpoolFileName)
	if err := os.WriteFile(legacy, []byte("{\"description\":\"A\"}\nnot-json\n{\"description\":\"B\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, bodies := sequencedEvidenceServer(t, nil)
	notes := flushSpool(context.Background(), NewClient(srv.URL, "dev-token"), ws, GitContext{})
	if len(*bodies) != 2 || (*bodies)[0]["description"] != "A" || (*bodies)[1]["description"] != "B" {
		t.Fatalf("legacy import order/content mismatch: %v", *bodies)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy spool was not removed: %v", err)
	}
	if len(notes) == 0 || !strings.Contains(strings.Join(notes, " "), "corrupt legacy") {
		t.Fatalf("expected corrupt legacy note, got %v", notes)
	}
}

// flakyOnceMiddleware lets the first POST /v1/evidence request actually
// reach the real handler (so the event is genuinely persisted server-side)
// but reports a simulated 503 to the client, as if the response never made
// it back. Every later request -- including the retried one, which carries
// the same client_event_id -- is served normally. This exercises Task 3's
// spool recovery and Task 2's server-side dedupe together: a client that
// never saw its first success must not end up with two stored records.
type flakyOnceMiddleware struct {
	real    http.Handler
	mu      sync.Mutex
	tripped bool
}

func (m *flakyOnceMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/v1/evidence" {
		m.mu.Lock()
		first := !m.tripped
		m.tripped = true
		m.mu.Unlock()
		if first {
			body, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			innerReq := httptest.NewRequest(r.Method, r.URL.String(), bytes.NewReader(body))
			innerReq.Header = r.Header.Clone()
			m.real.ServeHTTP(httptest.NewRecorder(), innerReq)
			http.Error(w, "simulated failure", http.StatusServiceUnavailable)
			return
		}
	}
	m.real.ServeHTTP(w, r)
}

// TestSpoolFullRecoveryEndsWithZeroLinesAndOneStoredRecord is the end-to-end
// reliability scenario: an evidence event is genuinely stored server-side
// but the client never sees the success response, so it spools the event;
// a later flush retries the exact same body (same client_event_id), the
// server-side dedupe (Task 2) recognizes the replay, and the scenario ends
// with an empty spool and exactly one stored record -- no loss, no
// duplication.
func TestSpoolFullRecoveryEndsWithZeroLinesAndOneStoredRecord(t *testing.T) {
	store := governance.NewRegistryStore()
	sessionStore, err := governance.NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	t.Cleanup(func() { _ = sessionStore.Close() })
	if err := sessionStore.Create(context.Background(), governance.SessionRecord{
		SessionID:      "sess_full",
		ActorSubject:   "local-dev",
		Agent:          "unit-tests",
		Classification: "internal",
		PromptSHA256:   "abc123",
		Status:         "created",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	svc := governance.NewSessionService(governance.SessionConfig{
		DevToken: "test",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: sessionStore,
	})
	registryHandler := governance.NewRegistryHandlerWithMetrics(store, svc, nil)

	srv := httptest.NewServer(&flakyOnceMiddleware{real: registryHandler})
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL, "test")
	ws := Workspace{Dir: t.TempDir()}
	ev := Event{SessionID: "sess_full", ToolName: "Edit"}

	result, err := Run(context.Background(), client, PostTool, ev, ws)
	if err != nil {
		t.Fatalf("expected the simulated failure to be absorbed by spooling, got %v", err)
	}
	if !result.Spooled {
		t.Fatal("expected the current event to be spooled after the simulated failure")
	}

	lines, err := readSpoolLines(spoolPath(ws.Dir))
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 spooled line, got %d", len(lines))
	}

	stored, err := store.Evidence()
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected the server to have already stored exactly 1 record despite the simulated failure, got %d", len(stored))
	}

	// A later hook invocation flushes the spool; the server now dedupes the
	// replay via its client_event_id instead of storing a second record.
	_ = flushSpool(context.Background(), client, ws, GitContext{})

	lines, err = readSpoolLines(spoolPath(ws.Dir))
	if err != nil {
		t.Fatalf("read spool after flush: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("expected zero spooled lines after recovery, got %d: %v", len(lines), lines)
	}

	stored, err = store.Evidence()
	if err != nil {
		t.Fatalf("list evidence after flush: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected exactly 1 stored record after recovery (no duplicate), got %d", len(stored))
	}
}
