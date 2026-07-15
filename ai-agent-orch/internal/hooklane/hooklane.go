// Package hooklane implements the hook→REST lane: it parses client lifecycle
// events from stdin, gathers git context, and posts directly to the Governance
// Shell REST API (not through MCP). Lifecycle evidence that did not cross a
// model gateway is reported self_reported/advisory; the Shell derives the final
// trust level server-side.
package hooklane

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/contextresolver"
)

// Lifecycle is the event kind a hook reports.
type Lifecycle string

const (
	PromptSubmit Lifecycle = "prompt-submit"
	PostTool     Lifecycle = "post-tool"
	Stop         Lifecycle = "stop"
)

// sessionFileName is the Kiro Session_File relative to the workspace .kiro dir.
const sessionFileName = ".ai-orch-session"

const (
	// spoolDirName holds one atomically-written JSON file per queued event.
	spoolDirName = ".ai-orch-spool"
	// legacySpoolFileName is imported once on startup for compatibility.
	legacySpoolFileName = ".ai-orch-spool.jsonl"
	spoolMaxFiles       = 500
)

// Workspace is the directory a hook runs against.
type Workspace struct{ Dir string }

// GitContext is the non-authoritative repository metadata sent on headers.
type GitContext struct {
	RepoURL   string
	Branch    string
	CommitSHA string
}

// headers returns the X-AI-Orch-* git headers, omitting empty fields so a
// not-a-repo workspace simply sends none (Req 9.1).
func (g GitContext) headers() map[string]string {
	h := map[string]string{}
	if g.RepoURL != "" {
		h["X-AI-Orch-Repo-URL"] = g.RepoURL
	}
	if g.Branch != "" {
		h["X-AI-Orch-Branch"] = g.Branch
	}
	if g.CommitSHA != "" {
		h["X-AI-Orch-Commit-SHA"] = g.CommitSHA
	}
	return h
}

// Event is the parsed lifecycle payload (union of Kiro and Claude Code shapes).
type Event struct {
	SessionID string          `json:"session_id"` // Claude Code supplies this; Kiro does not
	ToolName  string          `json:"tool_name"`  // post-tool
	ToolInput json.RawMessage `json:"tool_input"`
	Prompt    string          `json:"prompt"`
	Raw       map[string]any  `json:"-"` // retained for shaping, never resent wholesale
}

// ParseEvent reads a single JSON object from r. Non-object / invalid JSON
// returns an error (Req 8.2, 8.3).
func ParseEvent(r io.Reader) (Event, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Event{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return Event{}, fmt.Errorf("hook event must be a JSON object: %w", err)
	}
	if raw == nil {
		return Event{}, errors.New("hook event must be a JSON object, got null")
	}
	if dec.More() {
		return Event{}, errors.New("hook event must be a single JSON object")
	}
	var ev Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return Event{}, err
	}
	ev.Raw = raw
	return ev, nil
}

// HTTPError captures non-2xx governance responses without losing status context.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// Client is a minimal REST client over the Governance Shell, mirroring the
// doJSON/authHeaders shape from internal/mcpgateway/mcp_tools.go.
type Client struct {
	BaseURL  string
	DevToken string
	HTTP     *http.Client
}

// NewClient builds a Client targeting the Governance Shell REST base URL.
func NewClient(baseURL, devToken string) *Client {
	return &Client{
		BaseURL:  baseURL,
		DevToken: devToken,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// doJSON performs a governance REST call, attaching the dev-token bearer, the
// JSON content type on POST, and any extra (git) headers. Non-2xx → HTTPError.
func (c *Client) doJSON(ctx context.Context, method, path string, body any, headers map[string]string) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.DevToken) // Req 9.4
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, HTTPError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	return respBody, nil
}

// readSessionFile reads .kiro/.ai-orch-session, trimming surrounding space.
func readSessionFile(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, ".kiro", sessionFileName))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// writeSessionFile persists the session id to .kiro/.ai-orch-session (0600,
// single trimmed line), creating the .kiro directory if needed.
func writeSessionFile(dir, id string) error {
	kiroDir := filepath.Join(dir, ".kiro")
	if err := os.MkdirAll(kiroDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(kiroDir, sessionFileName), []byte(strings.TrimSpace(id)+"\n"), 0o600)
}

// resolveSession applies the ownership precedence (Req 12):
//  1. payload session_id wins, writes no Session_File (Claude Code, Req 12.3)
//  2. else reuse a present, non-empty Session_File (Kiro later hooks, Req 12.2)
//  3. else POST /v1/sessions and persist the returned id (Kiro, Req 12.1, 12.4)
func resolveSession(ctx context.Context, c *Client, ev Event, ws Workspace, git GitContext) (string, error) {
	if ev.SessionID != "" {
		return ev.SessionID, nil
	}
	if id, err := readSessionFile(ws.Dir); err == nil && id != "" {
		return id, nil
	}
	id, err := createSession(ctx, c, ev, git)
	if err != nil {
		return "", err
	}
	if err := writeSessionFile(ws.Dir, id); err != nil {
		return "", err
	}
	return id, nil
}

// createSession opens/continues a Governed_Session via POST /v1/sessions and
// returns the session id from the response.
func createSession(ctx context.Context, c *Client, ev Event, git GitContext) (string, error) {
	respBody, err := c.doJSON(ctx, http.MethodPost, "/v1/sessions", sessionBody(ev), git.headers())
	if err != nil {
		return "", err
	}
	var result struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse session response: %w", err)
	}
	if result.SessionID == "" {
		return "", errors.New("session response missing session_id")
	}
	return result.SessionID, nil
}

// sessionBody is the open/continue payload. It carries the prompt when present
// and never any token-usage or cost fields (Req 9.5); git context travels on
// headers instead.
func sessionBody(ev Event) map[string]any {
	body := map[string]any{}
	if ev.Prompt != "" {
		body["prompt"] = ev.Prompt
	}
	return body
}

// evidenceBody is the self_reported/advisory evidence record (Req 10.4, 11.1,
// 11.3). It never includes usage or cost fields (Req 9.5). Each call mints a
// fresh client_event_id: the Shell dedupes on (session_id, client_event_id),
// so resending this exact body from the spool never creates a second record.
func evidenceBody(sessionID string, ev Event, lc Lifecycle) map[string]any {
	desc := string(lc)
	if ev.ToolName != "" {
		desc = fmt.Sprintf("%s: %s", lc, ev.ToolName)
	}
	return map[string]any{
		"session_id":       sessionID,
		"evidence_type":    "external_tool_call",
		"description":      desc,
		"trust_level":      "self_reported", // hint only; the Shell derives the final level (Req 11.1)
		"enforcement_mode": "advisory",
		"client_event_id":  newClientEventID(),
	}
}

// newClientEventID returns a random hex identifier for evidenceBody's
// client_event_id, falling back to a timestamp if the CSPRNG is unavailable.
func newClientEventID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("cev_%d", time.Now().UnixNano())
	}
	return "cev_" + hex.EncodeToString(b[:])
}

// RunResult reports what a single hook invocation did beyond its primary
// success/failure outcome: whether the current event ended up spooled for
// retry, and any notes (spool flush progress, drops, best-effort failures)
// the caller should surface on stderr without treating them as fatal.
type RunResult struct {
	// Spooled is true when the current event's evidence POST failed with a
	// network error or 5xx and was queued locally instead of lost.
	Spooled bool
	// Notes are stderr-worthy diagnostics that never change the exit code.
	Notes []string
}

// isSpoolable reports whether a failed evidence POST should be queued for
// local retry instead of failing the hook outright: network errors and 5xx
// responses are treated as transient, while 4xx is a permanent rejection
// reported the same way as before this change.
func isSpoolable(err error) bool {
	httpErr, ok := err.(HTTPError)
	if !ok {
		return true
	}
	return httpErr.StatusCode >= 500
}

// spoolPath is the spool directory next to the Session_File.
func spoolPath(dir string) string {
	return filepath.Join(dir, ".kiro", spoolDirName)
}

func spoolFiles(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") && !strings.HasPrefix(entry.Name(), ".tmp-") {
			files = append(files, filepath.Join(path, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

// readSpoolLines is retained as a compact test/helper view of queued files.
func readSpoolLines(path string) ([]string, error) {
	files, err := spoolFiles(path)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		lines = append(lines, string(data))
	}
	return lines, nil
}

// writeSpoolLines seeds the directory model for tests and legacy import.
func writeSpoolLines(path string, lines []string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	for i, line := range lines {
		if err := writeSpoolFile(path, []byte(line), time.Unix(0, int64(i+1))); err != nil {
			return err
		}
	}
	return nil
}

func writeSpoolFile(dir string, data []byte, timestamp time.Time) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	name := fmt.Sprintf("%019d-%s.json", timestamp.UnixNano(), hex.EncodeToString(suffix[:]))
	return os.Rename(tmpName, filepath.Join(dir, name))
}

func appendToSpool(ws Workspace, body map[string]any) (string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	path := spoolPath(ws.Dir)
	if err := writeSpoolFile(path, data, time.Now()); err != nil {
		return "", err
	}
	dropped, err := enforceSpoolBound(path)
	if err != nil {
		return "", err
	}
	if dropped > 0 {
		return fmt.Sprintf("spool full (%d files): dropped %d oldest queued event(s)", spoolMaxFiles, dropped), nil
	}
	return "", nil
}

func enforceSpoolBound(path string) (int, error) {
	files, err := spoolFiles(path)
	if err != nil {
		return 0, err
	}
	dropped := 0
	for len(files)-dropped > spoolMaxFiles {
		if err := os.Remove(files[dropped]); err != nil && !os.IsNotExist(err) {
			return dropped, err
		}
		dropped++
	}
	return dropped, nil
}

func importLegacySpool(ws Workspace) []string {
	legacy := filepath.Join(ws.Dir, ".kiro", legacySpoolFileName)
	data, err := os.ReadFile(legacy)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return []string{fmt.Sprintf("spool import: %v", err)}
	}
	var notes []string
	base := time.Now()
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !json.Valid([]byte(line)) {
			notes = append(notes, "spool import: skipped 1 corrupt legacy line")
			continue
		}
		if err := writeSpoolFile(spoolPath(ws.Dir), []byte(line), base.Add(time.Duration(i))); err != nil {
			return append(notes, fmt.Sprintf("spool import: %v", err))
		}
	}
	if dropped, err := enforceSpoolBound(spoolPath(ws.Dir)); err != nil {
		return append(notes, fmt.Sprintf("spool import: enforce bound: %v", err))
	} else if dropped > 0 {
		notes = append(notes, fmt.Sprintf("spool full (%d files): dropped %d oldest queued event(s)", spoolMaxFiles, dropped))
	}
	if err := os.Remove(legacy); err != nil {
		notes = append(notes, fmt.Sprintf("spool import: failed to remove legacy spool: %v", err))
	}
	return notes
}

// postEvidenceOrSpool posts the current event's evidence, spooling it on a
// network/5xx failure instead of losing it. A 4xx failure (or a local error
// while trying to spool) is returned unchanged, exactly as before this
// change, so a permanent rejection still exits 1.
func postEvidenceOrSpool(ctx context.Context, c *Client, ws Workspace, sessionID string, ev Event, lc Lifecycle, git GitContext) (bool, []string, error) {
	body := evidenceBody(sessionID, ev, lc)
	if _, err := c.doJSON(ctx, http.MethodPost, "/v1/evidence", body, git.headers()); err != nil {
		if !isSpoolable(err) {
			return false, nil, err
		}
		note, spoolErr := appendToSpool(ws, body)
		if spoolErr != nil {
			// Could not spool locally either; surface the original delivery
			// failure so the caller reports it and exits non-zero as today.
			return false, nil, err
		}
		var notes []string
		if note != "" {
			notes = append(notes, note)
		}
		return true, notes, nil
	}
	return false, nil, nil
}

// flushSpool attempts every queued file, in order, before the current event
// is handled. A file that succeeds (2xx, including a duplicate marker from
// the server-side idempotency check) or is rejected (4xx) is removed; a line
// that fails to even parse as JSON is dropped as corrupt. The first
// network/5xx failure stops the flush and keeps that line plus everything
// after it queued for next time. Flush problems are only ever reported as
// notes: a broken spool must never block the current event.
func flushSpool(ctx context.Context, c *Client, ws Workspace, git GitContext) []string {
	path := spoolPath(ws.Dir)
	notes := importLegacySpool(ws)
	files, err := spoolFiles(path)
	if err != nil {
		return append(notes, fmt.Sprintf("spool flush: %v", err))
	}
	for i, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return append(notes, fmt.Sprintf("spool flush: stopped reading queue: %v", err))
		}
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			notes = append(notes, "spool flush: deleted 1 corrupt file")
			_ = os.Remove(file)
			continue
		}
		if _, err := c.doJSON(ctx, http.MethodPost, "/v1/evidence", body, git.headers()); err != nil {
			if isSpoolable(err) {
				remaining := len(files) - i
				notes = append(notes, fmt.Sprintf("spool flush: stopped after a delivery failure, %d event(s) remain queued: %v", remaining, err))
				return notes
			}
			notes = append(notes, fmt.Sprintf("spool flush: dropped a rejected event: %v", err))
		}
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			notes = append(notes, fmt.Sprintf("spool flush: failed to delete delivered event: %v", err))
		}
	}
	return notes
}

// Run resolves the session, gathers git context, flushes any spooled
// evidence, and posts per the lifecycle mapping (Req 10). It targets the
// Governance Shell REST base only (Req 9.3).
func Run(ctx context.Context, c *Client, lc Lifecycle, ev Event, ws Workspace) (RunResult, error) {
	var result RunResult
	git := gatherGit(ws)
	result.Notes = flushSpool(ctx, c, ws, git)
	sessionID, err := resolveSession(ctx, c, ev, ws, git)
	if err != nil {
		return result, err
	}
	switch lc {
	case PromptSubmit:
		// resolveSession opened/continued the Governed_Session (Req 10.1).
		return result, nil
	case PostTool:
		spooled, notes, err := postEvidenceOrSpool(ctx, c, ws, sessionID, ev, lc, git)
		result.Spooled = spooled
		result.Notes = append(result.Notes, notes...)
		return result, err
	case Stop:
		spooled, notes, err := postEvidenceOrSpool(ctx, c, ws, sessionID, ev, lc, git)
		result.Spooled = spooled
		result.Notes = append(result.Notes, notes...)
		if err != nil {
			return result, err
		}
		// The audit read-back is best-effort: it never fails the run and is
		// never spooled, since it is a lookup rather than an event to deliver.
		if _, auditErr := c.doJSON(ctx, http.MethodGet, "/v1/audit/sessions/"+sessionID, nil, git.headers()); auditErr != nil {
			result.Notes = append(result.Notes, fmt.Sprintf("stop: audit lookup failed (best-effort): %v", auditErr))
		}
		return result, nil
	default:
		return result, fmt.Errorf("unknown lifecycle: %s", lc)
	}
}

// gatherGit captures Git_Context from the workspace, failing open: outside a
// repo the three fields are empty and simply omitted (Req 9.1).
func gatherGit(ws Workspace) GitContext {
	sc := contextresolver.New(ws.Dir).Resolve()
	return GitContext{RepoURL: sc.RepoURL, Branch: sc.Branch, CommitSHA: sc.CommitSHA}
}
