package hooklane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/quick"
)

// recordingServer captures the method+path, headers, and decoded JSON body of
// every request, answering session-create calls with a fixed id.
type recordingServer struct {
	srv     *httptest.Server
	paths   []string
	headers []http.Header
	bodies  []map[string]any
}

func newRecordingServer(t *testing.T) *recordingServer {
	t.Helper()
	rs := &recordingServer{}
	rs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.paths = append(rs.paths, r.Method+" "+r.URL.Path)
		rs.headers = append(rs.headers, r.Header.Clone())
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rs.bodies = append(rs.bodies, body)
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sessions" {
			_ = json.NewEncoder(w).Encode(map[string]string{"session_id": "sess_new"})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(rs.srv.Close)
	return rs
}

func (rs *recordingServer) client() *Client { return NewClient(rs.srv.URL, "dev-token") }

func (rs *recordingServer) hit(path string) bool {
	for _, p := range rs.paths {
		if p == path {
			return true
		}
	}
	return false
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Feature: governed-client-integration, Property 7: any non-object/invalid JSON
// makes ParseEvent return an error.
func TestProperty7_NonObjectRejected(t *testing.T) {
	f := func(n int, s string, xs []int, b bool) bool {
		candidates := []string{
			mustJSON(n), mustJSON(s), mustJSON(xs), mustJSON(b), "null", "", "{",
		}
		for _, c := range candidates {
			if _, err := ParseEvent(strings.NewReader(c)); err == nil {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

// Feature: governed-client-integration, Property 8: any id round-trips through
// the Session_File, and a later Kiro hook (no payload id, file present) reuses
// it without opening a new session.
func TestProperty8_SessionFileRoundTripAndReuse(t *testing.T) {
	f := func(seed uint32) bool {
		id := "sess_" + sanitizeID(seed)
		if id == "sess_" {
			id = "sess_x"
		}
		dir := t.TempDir()
		if err := writeSessionFile(dir, id); err != nil {
			return false
		}
		got, err := readSessionFile(dir)
		if err != nil || got != id {
			return false
		}
		// A later Kiro hook: no payload session_id, Session_File present.
		rs := newRecordingServer(t)
		resolved, err := resolveSession(context.Background(), rs.client(), Event{}, Workspace{Dir: dir}, GitContext{})
		if err != nil || resolved != id {
			return false
		}
		// Reuse must not create a new session.
		return !rs.hit("POST /v1/sessions")
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// Feature: governed-client-integration, Property 9: a non-empty payload
// session_id is reused with no create call and no Session_File written.
func TestProperty9_PayloadSessionReusedNoPersist(t *testing.T) {
	f := func(seed uint32) bool {
		id := "claude_" + sanitizeID(seed)
		dir := t.TempDir()
		rs := newRecordingServer(t)
		resolved, err := resolveSession(context.Background(), rs.client(), Event{SessionID: id}, Workspace{Dir: dir}, GitContext{})
		if err != nil || resolved != id {
			return false
		}
		if rs.hit("POST /v1/sessions") {
			return false
		}
		// No Session_File written.
		_, statErr := os.Stat(filepath.Join(dir, ".kiro", sessionFileName))
		return os.IsNotExist(statErr)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// Feature: governed-client-integration, Property 10: for any non-empty
// Git_Context the three X-AI-Orch-* headers equal those fields.
func TestProperty10_GitHeaders(t *testing.T) {
	f := func(repo, branch, commit string) bool {
		// Constrain to non-empty, header-safe values.
		git := GitContext{
			RepoURL:   "https://example.test/" + sanitizeHeader(repo) + "x",
			Branch:    sanitizeHeader(branch) + "x",
			CommitSHA: sanitizeHeader(commit) + "x",
		}
		rs := newRecordingServer(t)
		if _, err := rs.client().doJSON(context.Background(), http.MethodPost, "/v1/evidence", map[string]any{"k": "v"}, git.headers()); err != nil {
			return false
		}
		h := rs.headers[len(rs.headers)-1]
		return h.Get("X-AI-Orch-Repo-URL") == git.RepoURL &&
			h.Get("X-AI-Orch-Branch") == git.Branch &&
			h.Get("X-AI-Orch-Commit-SHA") == git.CommitSHA
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// Feature: governed-client-integration, Property 11: the Authorization header
// is always "Bearer "+devToken.
func TestProperty11_AuthorizationHeader(t *testing.T) {
	f := func(token string) bool {
		// A dev token is never empty (an empty token fails closed at the Shell),
		// and HTTP transport trims trailing whitespace from header values, so
		// constrain to non-empty, header-safe tokens.
		token = sanitizeHeader(token) + "t"
		rs := newRecordingServer(t)
		c := NewClient(rs.srv.URL, token)
		if _, err := c.doJSON(context.Background(), http.MethodPost, "/v1/evidence", map[string]any{"k": "v"}, nil); err != nil {
			return false
		}
		h := rs.headers[len(rs.headers)-1]
		return h.Get("Authorization") == "Bearer "+token
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// hasForbiddenKey reports whether any JSON key contains a token-usage or cost term.
func hasForbiddenKey(body map[string]any) bool {
	for k := range body {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "token") || strings.Contains(lk, "usage") || strings.Contains(lk, "cost") {
			return true
		}
	}
	return false
}

// Feature: governed-client-integration, Property 12: hook bodies never contain
// token-usage or cost keys.
func TestProperty12_NoUsageOrCostKeys(t *testing.T) {
	f := func(prompt, tool, sid string) bool {
		ev := Event{Prompt: prompt, ToolName: tool, SessionID: sid}
		for _, body := range []map[string]any{
			sessionBody(ev),
			evidenceBody("sess_x", ev, PostTool),
			evidenceBody("sess_x", ev, Stop),
		} {
			if hasForbiddenKey(body) {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

// Feature: governed-client-integration, Property 13: evidence bodies are always
// self_reported/advisory and never claim a gateway-enforced level.
func TestProperty13_EvidenceSelfReportedAdvisory(t *testing.T) {
	f := func(tool, sid string) bool {
		for _, lc := range []Lifecycle{PostTool, Stop} {
			body := evidenceBody(sid, Event{ToolName: tool}, lc)
			if body["trust_level"] != "self_reported" {
				return false
			}
			if body["enforcement_mode"] != "advisory" {
				return false
			}
			// Never gateway-enforced.
			if body["trust_level"] == "gateway_enforced" || body["enforcement_mode"] == "gateway" {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

// --- Example / edge tests (task 3.9) ---

func TestParseEventDecodesUnionShapes(t *testing.T) {
	kiro := `{"prompt":"add pagination","cwd":"/repo"}`
	ev, err := ParseEvent(strings.NewReader(kiro))
	if err != nil {
		t.Fatalf("kiro parse: %v", err)
	}
	if ev.Prompt != "add pagination" || ev.SessionID != "" {
		t.Fatalf("unexpected kiro event: %#v", ev)
	}
	claude := `{"session_id":"abc123","tool_name":"Edit","tool_input":{"file_path":"src/users.go"}}`
	ev, err = ParseEvent(strings.NewReader(claude))
	if err != nil {
		t.Fatalf("claude parse: %v", err)
	}
	if ev.SessionID != "abc123" || ev.ToolName != "Edit" {
		t.Fatalf("unexpected claude event: %#v", ev)
	}
}

func TestRunLifecycleEndpointMapping(t *testing.T) {
	cases := []struct {
		name      string
		lc        Lifecycle
		ev        Event
		wantPaths []string
	}{
		{"prompt-submit creates session", PromptSubmit, Event{Prompt: "hi"}, []string{"POST /v1/sessions"}},
		{"post-tool posts evidence", PostTool, Event{SessionID: "sess_x", ToolName: "Edit"}, []string{"POST /v1/evidence"}},
		{"stop posts evidence then reads audit", Stop, Event{SessionID: "sess_x"}, []string{"POST /v1/evidence", "GET /v1/audit/sessions/sess_x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := newRecordingServer(t)
			if _, err := Run(context.Background(), rs.client(), tc.lc, tc.ev, Workspace{Dir: t.TempDir()}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			for _, want := range tc.wantPaths {
				if !rs.hit(want) {
					t.Fatalf("expected %s, got %v", want, rs.paths)
				}
			}
		})
	}
}

func TestRunNotARepoOmitsGitHeaders(t *testing.T) {
	rs := newRecordingServer(t)
	// t.TempDir() is not a git repo, so contextresolver yields empty fields.
	if _, err := Run(context.Background(), rs.client(), PromptSubmit, Event{Prompt: "hi"}, Workspace{Dir: t.TempDir()}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	h := rs.headers[0]
	for _, key := range []string{"X-AI-Orch-Repo-URL", "X-AI-Orch-Branch", "X-AI-Orch-Commit-SHA"} {
		if h.Get(key) != "" {
			t.Fatalf("expected %s omitted outside a repo, got %q", key, h.Get(key))
		}
	}
}

func TestRunNoFileNoPayloadCreatesAndPersists(t *testing.T) {
	rs := newRecordingServer(t)
	dir := t.TempDir()
	if _, err := Run(context.Background(), rs.client(), PromptSubmit, Event{Prompt: "hi"}, Workspace{Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rs.hit("POST /v1/sessions") {
		t.Fatalf("expected a session-create call, got %v", rs.paths)
	}
	got, err := readSessionFile(dir)
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	if got != "sess_new" {
		t.Fatalf("expected persisted session id sess_new, got %q", got)
	}
}

func TestClientTargetsRESTBaseOnly(t *testing.T) {
	rs := newRecordingServer(t)
	c := rs.client()
	if c.BaseURL != rs.srv.URL {
		t.Fatalf("client base url = %q, want %q", c.BaseURL, rs.srv.URL)
	}
	if _, err := Run(context.Background(), c, Stop, Event{SessionID: "sess_x"}, Workspace{Dir: t.TempDir()}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, p := range rs.paths {
		if !strings.HasPrefix(p, "POST /v1/") && !strings.HasPrefix(p, "GET /v1/") {
			t.Fatalf("unexpected non-REST path %q", p)
		}
	}
}

func TestDoJSONReturnsHTTPErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "dev")
	_, err := c.doJSON(context.Background(), http.MethodPost, "/v1/evidence", map[string]any{"k": "v"}, nil)
	httpErr, ok := err.(HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", httpErr.StatusCode)
	}
}

// Feature: governed-client-onboarding, Property 8: Claude Code events parse to
// their declared fields. A Claude payload populates SessionID/ToolName/
// ToolInput, payloads missing those fields still parse without error, and a
// non-empty payload session_id is reused without writing a Session_File.
// Validates: Requirements 6.1, 6.3
func TestProperty8_ClaudeCodeEventParsing(t *testing.T) {
	// Field-extraction property over many inputs: a Claude Code payload with
	// session_id/hook_event_name/tool_name/tool_input parses with those fields
	// populated regardless of their concrete values.
	f := func(seed uint32, toolSeed uint32) bool {
		sid := "abc_" + sanitizeID(seed)
		tool := "Tool_" + sanitizeID(toolSeed)
		payload := mustJSON(map[string]any{
			"session_id":      sid,
			"hook_event_name": "PostToolUse", // Claude-only field, must be tolerated
			"tool_name":       tool,
			"tool_input":      map[string]any{"file_path": "x"},
		})
		ev, err := ParseEvent(strings.NewReader(payload))
		if err != nil {
			return false
		}
		if ev.SessionID != sid || ev.ToolName != tool {
			return false
		}
		if len(ev.ToolInput) == 0 || !strings.Contains(string(ev.ToolInput), "file_path") {
			return false
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}

	// A concrete documented Claude payload parses as expected.
	claude := `{"session_id":"abc123","hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{"file_path":"x"}}`
	ev, err := ParseEvent(strings.NewReader(claude))
	if err != nil {
		t.Fatalf("claude parse: %v", err)
	}
	if ev.SessionID != "abc123" || ev.ToolName != "Edit" {
		t.Fatalf("unexpected claude event: %#v", ev)
	}
	if string(ev.ToolInput) != `{"file_path":"x"}` {
		t.Fatalf("unexpected tool_input: %s", ev.ToolInput)
	}

	// Events missing the declared fields still parse without error (R6.3): the
	// absent fields decode to zero values.
	for _, missing := range []string{
		`{}`,
		`{"hook_event_name":"Stop"}`,
		`{"session_id":"only-session"}`,
		`{"tool_name":"Edit"}`,
	} {
		if _, err := ParseEvent(strings.NewReader(missing)); err != nil {
			t.Fatalf("missing-field payload %q should parse, got %v", missing, err)
		}
	}

	// A non-empty payload session_id is reused without writing a Session_File (R6.2).
	dir := t.TempDir()
	rs := newRecordingServer(t)
	resolved, err := resolveSession(context.Background(), rs.client(), Event{SessionID: "abc123"}, Workspace{Dir: dir}, GitContext{})
	if err != nil {
		t.Fatalf("resolveSession: %v", err)
	}
	if resolved != "abc123" {
		t.Fatalf("expected payload session_id reused, got %q", resolved)
	}
	if rs.hit("POST /v1/sessions") {
		t.Fatal("payload session_id must not trigger a session-create call")
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".kiro", sessionFileName)); !os.IsNotExist(statErr) {
		t.Fatal("payload session_id must not write a Session_File")
	}
}

// sanitizeID maps a seed to a short alphanumeric id with no whitespace.
func sanitizeID(seed uint32) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	if seed == 0 {
		return "0"
	}
	var b strings.Builder
	for seed > 0 {
		b.WriteByte(alphabet[seed%uint32(len(alphabet))])
		seed /= uint32(len(alphabet))
	}
	return b.String()
}

// sanitizeHeader strips characters that are not safe in an HTTP header value.
func sanitizeHeader(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x21 && r < 0x7f { // visible ASCII, no spaces/controls
			b.WriteRune(r)
		}
	}
	return b.String()
}
