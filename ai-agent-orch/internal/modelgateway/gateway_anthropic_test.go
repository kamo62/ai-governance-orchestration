package modelgateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/quick"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/catalog"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/modelbackend"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/router"
)

// anthropicGateway builds a gateway exposing a single allowed alias
// ("coding-primary") for the Anthropic adapter tests. It mirrors
// newTestGatewayWithBackend but takes an arbitrary backend so streaming and
// raw fakes can be swapped in per test.
func anthropicGateway(backend modelbackend.Backend, auditStore audit.Store) *Gateway {
	return NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
			{Alias: "coding-fast", Provider: "openrouter", ModelID: "x-ai/grok-build-0.1", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
	})
}

// chatTextBackend returns a fixed assistant text from the typed chat path.
func chatTextBackend(text string) *fakeChatClient {
	return &fakeChatClient{resp: openrouter.ChatCompletionResponse{
		ID: "chatcmpl-test",
		Choices: []struct {
			Message openrouter.Message `json:"message"`
		}{
			{Message: openrouter.Message{Role: "assistant", Content: text}},
		},
		Usage: openrouter.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
	}}
}

func anthropicRequestBody(model string, userMsg string, stream bool) []byte {
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 128,
		"stream":     stream,
		"messages": []map[string]any{
			{"role": "user", "content": userMsg},
		},
	})
	return body
}

// --- Property 14: requests yield well-formed Anthropic responses ---

func TestAnthropicMessagesResponseTranslationProperty(t *testing.T) {
	// Feature: governed-client-integration, Property 14: any valid request with
	// an allowed alias yields type:"message", role:"assistant", model==alias,
	// with text derived from the backend.
	f := func(userMsg, backendText string) bool {
		g := anthropicGateway(chatTextBackend(backendText), audit.NewFileStore(""))
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", userMsg, false)))
		req.Header.Set("Authorization", "Bearer runtime-test-token")
		req.Header.Set("X-AI-Orch-Session-ID", "sess_anthropic")
		rec := httptest.NewRecorder()
		g.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return false
		}
		var resp anthropicMessageResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			return false
		}
		if resp.Type != "message" || resp.Role != "assistant" || resp.Model != "coding-primary" {
			return false
		}
		if len(resp.Content) != 1 || resp.Content[0].Type != "text" {
			return false
		}
		return resp.Content[0].Text == backendText
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

// --- Property 15: raw provider ids / non-aliases are rejected ---

func TestAnthropicMessagesRejectsNonAliasProperty(t *testing.T) {
	// Feature: governed-client-integration, Property 15: any non-alias model
	// string is rejected with no backend call.
	f := func(model string) bool {
		if model == "coding-primary" || model == "coding-fast" {
			return true // valid aliases are out of scope for this property
		}
		backend := chatTextBackend("unused")
		g := anthropicGateway(backend, audit.NewFileStore(""))
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody(model, "hello", false)))
		req.Header.Set("Authorization", "Bearer runtime-test-token")
		req.Header.Set("X-AI-Orch-Session-ID", "sess_anthropic")
		rec := httptest.NewRecorder()
		g.Handler().ServeHTTP(rec, req)
		// Reject (never 2xx) and never touch the backend.
		return rec.Code >= 400 && backend.lastRequest.Provider == ""
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

// --- Property 16: a valid runtime token is required ---

func TestAnthropicMessagesRequiresRuntimeTokenProperty(t *testing.T) {
	// Feature: governed-client-integration, Property 16: any invalid/absent
	// token yields 401 with no backend call.
	f := func(token string) bool {
		if token == "runtime-test-token" || strings.HasPrefix(token, "runtime-test-token.") {
			return true // valid tokens (incl. composite key) are out of scope
		}
		backend := chatTextBackend("unused")
		g := anthropicGateway(backend, audit.NewFileStore(""))
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", "hello", false)))
		if strings.TrimSpace(token) != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("X-AI-Orch-Session-ID", "sess_anthropic")
		rec := httptest.NewRecorder()
		g.Handler().ServeHTTP(rec, req)
		return rec.Code == http.StatusUnauthorized && backend.lastRequest.Provider == ""
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

// --- Property 17: the adapter fails closed on unevaluable session/routing ---

func TestAnthropicMessagesFailsClosedProperty(t *testing.T) {
	// Feature: governed-client-integration, Property 17: forced session/routing
	// errors always reject and never call the backend.
	f := func(sessionID string) bool {
		if strings.TrimSpace(sessionID) == "" {
			return true // empty header drives the auto-session/400 path, out of scope
		}
		backend := chatTextBackend("unused")
		g := NewGateway(GatewayConfig{
			RuntimeToken: "runtime-test-token",
			Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
				{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
			}}, nil),
			Backend:         backend,
			Audit:           audit.NewFileStore(""),
			NewID:           func(prefix string) string { return prefix + "_test" },
			ValidateSession: func(context.Context, string) error { return errors.New("session policy cannot be evaluated") },
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", "hello", false)))
		req.Header.Set("Authorization", "Bearer runtime-test-token")
		req.Header.Set("X-AI-Orch-Session-ID", sessionID)
		rec := httptest.NewRecorder()
		g.Handler().ServeHTTP(rec, req)
		return rec.Code >= 400 && backend.lastRequest.Provider == ""
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

// --- Property 20: completed adapter audit is gateway_enforced/gateway ---

func TestAnthropicMessagesAuditGatewayEnforcedProperty(t *testing.T) {
	// Feature: governed-client-integration, Property 20: every completed adapter
	// audit event is gateway_enforced/gateway with alias + resolved provider model.
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	counter := 0
	f := func(userMsg string) bool {
		counter++
		sessionID := fmt.Sprintf("sess_audit_%d", counter)
		g := anthropicGateway(chatTextBackend("ok"), auditStore)
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", userMsg, false)))
		req.Header.Set("Authorization", "Bearer runtime-test-token")
		req.Header.Set("X-AI-Orch-Session-ID", sessionID)
		rec := httptest.NewRecorder()
		g.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return false
		}
		events, err := auditStore.EventsBySession(context.Background(), sessionID)
		if err != nil || len(events) != 1 {
			return false
		}
		e := events[0]
		return e.TrustLevel == "gateway_enforced" &&
			e.EnforcementMode == "gateway" &&
			e.ModelAlias == "coding-primary" &&
			e.ModelResolved == "anthropic/claude-opus-4.7"
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 150}); err != nil {
		t.Fatal(err)
	}
}

// --- Streaming fake backend ---

// chunkStreamBackend emits an OpenAI-compatible chat SSE stream whose assistant
// text is the configured chunks, followed by a usage frame and [DONE].
type chunkStreamBackend struct {
	chunks []string
	secret string
}

func (b *chunkStreamBackend) Name() string                            { return "chunk-stream-test" }
func (b *chunkStreamBackend) ResolvedModel(_ string, m string) string { return m }

func (b *chunkStreamBackend) ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return openrouter.ChatCompletionResponse{}, nil
}

func (b *chunkStreamBackend) ChatCompletionStream(_ context.Context, _ openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	var sb strings.Builder
	for _, c := range b.chunks {
		payload, _ := json.Marshal(map[string]any{
			"id":     "chunk",
			"object": "chat.completion.chunk",
			"model":  "m",
			"choices": []map[string]any{
				{"index": 0, "delta": map[string]any{"content": c}},
			},
		})
		sb.WriteString("data: " + string(payload) + "\n\n")
	}
	sb.WriteString(`data: {"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}` + "\n\n")
	sb.WriteString("data: [DONE]\n\n")
	return io.NopCloser(strings.NewReader(sb.String())), nil
}

// parseAnthropicSSE extracts the ordered event types and the concatenated
// content_block_delta text from an Anthropic SSE response body.
func parseAnthropicSSE(raw string) (events []string, text string) {
	for _, block := range strings.Split(raw, "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		var evType, data string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				evType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if evType == "" {
			continue
		}
		events = append(events, evType)
		if evType == "content_block_delta" {
			var ev struct {
				Delta struct {
					Text string `json:"text"`
				} `json:"delta"`
			}
			_ = json.Unmarshal([]byte(data), &ev)
			text += ev.Delta.Text
		}
	}
	return events, text
}

// --- Property 18: streaming preserves the response text ---

func TestAnthropicStreamPreservesTextProperty(t *testing.T) {
	// Feature: governed-client-integration, Property 18: for any chunking,
	// concatenated content_block_delta text equals the backend text, framed by
	// one message_start first and message_stop last.
	f := func(chunks []string) bool {
		backend := &chunkStreamBackend{chunks: chunks}
		g := anthropicGateway(backend, audit.NewFileStore(""))
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", "hello", true)))
		req.Header.Set("Authorization", "Bearer runtime-test-token")
		req.Header.Set("X-AI-Orch-Session-ID", "sess_anthropic_stream")
		rec := httptest.NewRecorder()
		g.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return false
		}
		events, text := parseAnthropicSSE(rec.Body.String())
		if len(events) < 2 {
			return false
		}
		if events[0] != "message_start" || events[len(events)-1] != "message_stop" {
			return false
		}
		if countString(events, "message_start") != 1 || countString(events, "message_stop") != 1 {
			return false
		}
		return text == strings.Join(chunks, "")
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

func countString(items []string, want string) int {
	n := 0
	for _, item := range items {
		if item == want {
			n++
		}
	}
	return n
}

// --- Property 19: no provider credential leaks ---

func TestAnthropicNoCredentialLeakProperty(t *testing.T) {
	// Feature: governed-client-integration, Property 19: no configured provider
	// credential value appears in any response body, header, or audit record.
	const secret = "sk-provider-secret-DO-NOT-LEAK-9f3c1a2b"
	f := func(userMsg string) bool {
		auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
		auditStore := audit.NewFileStore(auditPath)

		// Non-streaming path.
		nonStream := chatTextBackend("ok")
		gNon := anthropicGateway(nonStream, auditStore)
		reqNon := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", userMsg, false)))
		reqNon.Header.Set("Authorization", "Bearer runtime-test-token")
		reqNon.Header.Set("X-AI-Orch-Session-ID", "sess_leak_non")
		recNon := httptest.NewRecorder()
		gNon.Handler().ServeHTTP(recNon, reqNon)

		// Streaming path (backend holds a secret it must never emit).
		stream := &chunkStreamBackend{chunks: []string{userMsg, "tail"}, secret: secret}
		gStream := anthropicGateway(stream, auditStore)
		reqStream := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", userMsg, true)))
		reqStream.Header.Set("Authorization", "Bearer runtime-test-token")
		reqStream.Header.Set("X-AI-Orch-Session-ID", "sess_leak_stream")
		recStream := httptest.NewRecorder()
		gStream.Handler().ServeHTTP(recStream, reqStream)

		if containsSecret(recNon, secret) || containsSecret(recStream, secret) {
			return false
		}
		events, err := auditStore.AllEvents(context.Background())
		if err != nil {
			return false
		}
		for _, e := range events {
			encoded, _ := json.Marshal(e)
			if strings.Contains(string(encoded), secret) {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 150}); err != nil {
		t.Fatal(err)
	}
}

func containsSecret(rec *httptest.ResponseRecorder, secret string) bool {
	if strings.Contains(rec.Body.String(), secret) {
		return true
	}
	for _, values := range rec.Header() {
		for _, v := range values {
			if strings.Contains(v, secret) {
				return true
			}
		}
	}
	return false
}

// --- 5.8 Example / edge tests ---

func TestAnthropicMessagesReturnsAnthropicBody(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	g := anthropicGateway(chatTextBackend("Hello from backend"), auditStore)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", "hi", false)))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_example")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp anthropicMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Type != "message" || resp.Role != "assistant" {
		t.Fatalf("unexpected envelope: %#v", resp)
	}
	if resp.Model != "coding-primary" {
		t.Fatalf("expected alias in model field, got %q", resp.Model)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "Hello from backend" {
		t.Fatalf("unexpected content: %#v", resp.Content)
	}
}

func TestAnthropicStreamEventOrdering(t *testing.T) {
	g := anthropicGateway(&chunkStreamBackend{chunks: []string{"Hel", "lo", " world"}}, audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")))
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", "hi", true)))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_example_stream")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	events, text := parseAnthropicSSE(rec.Body.String())
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_delta", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected event ordering:\n got: %v\nwant: %v", events, want)
	}
	if text != "Hello world" {
		t.Fatalf("expected reconstructed text 'Hello world', got %q", text)
	}
}

func TestAnthropicStreamCompletionAudit(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	g := anthropicGateway(&chunkStreamBackend{chunks: []string{"ok"}}, auditStore)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", "hi", true)))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_stream_done")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_stream_done")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 || !strings.HasSuffix(events[0].EventType, "stream.completed") {
		t.Fatalf("expected stream completion audit, got %#v", events)
	}
	if events[0].TrustLevel != "gateway_enforced" || events[0].EnforcementMode != "gateway" {
		t.Fatalf("expected gateway-enforced stream audit, got %#v", events[0])
	}
}

// streamNoDoneBackend emits content but ends the stream before [DONE],
// exercising the mid-stream failure audit path (Req 5.4).
type streamNoDoneBackend struct{}

func (streamNoDoneBackend) Name() string                            { return "stream-nodone-test" }
func (streamNoDoneBackend) ResolvedModel(_ string, m string) string { return m }
func (streamNoDoneBackend) ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return openrouter.ChatCompletionResponse{}, nil
}
func (streamNoDoneBackend) ChatCompletionStream(context.Context, openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	data := `data: {"id":"chunk","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n"
	return io.NopCloser(strings.NewReader(data)), nil
}

func TestAnthropicStreamFailureAudit(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	g := anthropicGateway(streamNoDoneBackend{}, auditStore)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", "hi", true)))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_stream_fail")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	// The 200 header is committed before streaming begins; the failure surfaces
	// as a stream-failure audit event, not an HTTP status.
	events, err := auditStore.EventsBySession(context.Background(), "sess_stream_fail")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 || !strings.HasSuffix(events[0].EventType, "stream.failed") {
		t.Fatalf("expected stream failure audit, got %#v", events)
	}
}

func TestAnthropicMessagesRecordsUsageAndCost(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := chatTextBackend("ok")
	backend.resp.Usage = openrouter.Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18, Cost: 0.0025}
	g := anthropicGateway(backend, auditStore)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", "hi", false)))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_usage_cost")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp anthropicMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 {
		t.Fatalf("expected anthropic usage mapped from backend, got %#v", resp.Usage)
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_usage_cost")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	if got := numericAuditValue(events[0].TokenUsage["total_tokens"]); got != 18 {
		t.Fatalf("expected token usage recorded, got %#v", events[0].TokenUsage)
	}
	if got := floatAuditValue(events[0].TokenUsage["cost_usd"]); got <= 0 {
		t.Fatalf("expected cost recorded, got %#v", events[0].TokenUsage)
	}
}

func TestAnthropicMessagesRejectsDisallowedAliasOnRestrictedSession(t *testing.T) {
	backend := chatTextBackend("unused")
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"internal"}},
		}}, nil),
		Backend: backend,
		Audit:   audit.NewFileStore(""),
		NewID:   func(prefix string) string { return prefix + "_test" },
		LookupSession: func(context.Context, string) (SessionInfo, error) {
			return SessionInfo{Classification: "restricted", Status: "running"}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", "hi", false)))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_restricted")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for disallowed alias on restricted session, got %d: %s", rec.Code, rec.Body.String())
	}
	if backend.lastRequest.Provider != "" {
		t.Fatalf("expected backend untouched on rejection, got %#v", backend.lastRequest)
	}
}

func TestAnthropicMessagesSessionBoundFromToken(t *testing.T) {
	sum := sha256.Sum256([]byte("sgt_anthropic"))
	tokenHash := hex.EncodeToString(sum[:])
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: chatTextBackend("ok"),
		Audit:   audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		NewID:   func(prefix string) string { return prefix + "_test" },
		LookupSession: func(context.Context, string) (SessionInfo, error) {
			return SessionInfo{Classification: "internal", Status: "running", GatewayTokenSHA256: tokenHash}, nil
		},
	})

	// Missing per-session token → 401 (request bound to the session secret).
	missing := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", "hi", false)))
	missing.Header.Set("Authorization", "Bearer runtime-test-token")
	missing.Header.Set("X-AI-Orch-Session-ID", "sess_bound")
	missingRec := httptest.NewRecorder()
	g.Handler().ServeHTTP(missingRec, missing)
	if missingRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without session token, got %d: %s", missingRec.Code, missingRec.Body.String())
	}

	// Correct per-session token → 200.
	bound := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(anthropicRequestBody("coding-primary", "hi", false)))
	bound.Header.Set("Authorization", "Bearer runtime-test-token")
	bound.Header.Set("X-AI-Orch-Session-ID", "sess_bound")
	bound.Header.Set("X-AI-Orch-Session-Token", "sgt_anthropic")
	boundRec := httptest.NewRecorder()
	g.Handler().ServeHTTP(boundRec, bound)
	if boundRec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid session token, got %d: %s", boundRec.Code, boundRec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Area A (governed-client-onboarding) tool-translation tests
// ---------------------------------------------------------------------------

// anthropicCopilotGateway builds a gateway whose single alias resolves to a
// Copilot Responses-only model (GPT-5.x class), so anthropicBackendCall takes
// the Responses-bridge pre-check arm.
func anthropicCopilotGateway(backend modelbackend.Backend, auditStore audit.Store) *Gateway {
	return NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "github-copilot", ModelID: "gpt-5.1", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
	})
}

// anthropicModelGateway builds a gateway with an arbitrary provider/model so
// the Responses-only chat-completions fallback arm can be exercised.
func anthropicModelGateway(provider, modelID string, backend modelbackend.Backend, auditStore audit.Store) *Gateway {
	return NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: provider, ModelID: modelID, AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
	})
}

// validUTF8 keeps quick-generated strings JSON-round-trippable so content
// equality assertions are not defeated by invalid code points.
func validUTF8(s string) string { return strings.ToValidUTF8(s, "") }

func anthropicServe(g *Gateway, body []byte, sessionID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", sessionID)
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	return rec
}

// chatCompletionToolBody builds a raw OpenAI chat completion carrying a single
// tool call, as a RawChatBackend would return it.
func chatCompletionToolBody(id, name, args string) []byte {
	body, _ := json.Marshal(map[string]any{
		"id":     "chatcmpl-tool",
		"object": "chat.completion",
		"model":  "m",
		"choices": []map[string]any{
			{
				"index":         0,
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"role":    "assistant",
					"content": nil,
					"tool_calls": []map[string]any{
						{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}},
					},
				},
			},
		},
	})
	return body
}

// --- SSE chunk builders for streamed tool-call tests ---

func sseTextChunk(text string) string {
	payload, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": text}}},
	})
	return "data: " + string(payload) + "\n\n"
}

func sseToolStartChunk(index int, id, name string) string {
	payload, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{
			"tool_calls": []map[string]any{{"index": index, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": ""}}},
		}}},
	})
	return "data: " + string(payload) + "\n\n"
}

func sseToolArgChunk(index int, frag string) string {
	payload, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{
			"tool_calls": []map[string]any{{"index": index, "function": map[string]any{"arguments": frag}}},
		}}},
	})
	return "data: " + string(payload) + "\n\n"
}

func sseFinishChunk(reason string) string {
	payload, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": reason}},
	})
	return "data: " + string(payload) + "\n\n"
}

const sseDoneFrame = "data: [DONE]\n\n"

// rawChatStreamBackend emits a verbatim OpenAI chat SSE body via the typed
// streaming path, letting tests drive arbitrary tool-call delta sequences.
type rawChatStreamBackend struct{ sse string }

func (b *rawChatStreamBackend) Name() string                            { return "raw-chat-stream-test" }
func (b *rawChatStreamBackend) ResolvedModel(_ string, m string) string { return m }
func (b *rawChatStreamBackend) ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return openrouter.ChatCompletionResponse{}, nil
}
func (b *rawChatStreamBackend) ChatCompletionStream(context.Context, openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(b.sse)), nil
}

// anthropicSSEEvent is a parsed Anthropic stream event with the fields the
// tool-translation tests assert on.
type anthropicSSEEvent struct {
	Type        string
	Index       int
	BlockType   string
	ID          string
	Name        string
	PartialJSON string
	TextDelta   string
	StopReason  string
}

func parseAnthropicEvents(raw string) []anthropicSSEEvent {
	var out []anthropicSSEEvent
	for _, block := range strings.Split(raw, "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		var evType, data string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				evType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if evType == "" {
			continue
		}
		var parsed struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
		}
		_ = json.Unmarshal([]byte(data), &parsed)
		out = append(out, anthropicSSEEvent{
			Type:        evType,
			Index:       parsed.Index,
			BlockType:   parsed.ContentBlock.Type,
			ID:          parsed.ContentBlock.ID,
			Name:        parsed.ContentBlock.Name,
			PartialJSON: parsed.Delta.PartialJSON,
			TextDelta:   parsed.Delta.Text,
			StopReason:  parsed.Delta.StopReason,
		})
	}
	return out
}

// --- Property 1 (1.9): bridged non-streaming response preserves content/finish/usage ---

func TestAnthropicBridgedResponsePreservesProperty(t *testing.T) {
	// Feature: governed-client-onboarding, Property 1: a bridged non-streaming
	// response carries the same text, a stop reason mapped from the bridged
	// finish reason (always "stop"→"end_turn"), and the same token counts.
	f := func(rawText string, in, out uint8) bool {
		text := validUTF8(rawText)
		respRaw, _ := json.Marshal(map[string]any{
			"id":     "resp-1",
			"output": []map[string]any{{"type": "message", "content": []map[string]any{{"type": "output_text", "text": text}}}},
			"usage":  map[string]any{"input_tokens": int(in), "output_tokens": int(out), "total_tokens": int(in) + int(out)},
		})
		backend := &responsesFallbackBackend{responsesRaw: respRaw}
		g := anthropicCopilotGateway(backend, audit.NewFileStore(""))
		rec := anthropicServe(g, anthropicRequestBody("coding-primary", "hi", false), "sess_bridge")
		if rec.Code != http.StatusOK {
			return false
		}
		var resp anthropicMessageResponse
		if json.Unmarshal(rec.Body.Bytes(), &resp) != nil {
			return false
		}
		if backend.chatCalls != 0 || backend.responsesCalls == 0 {
			return false // pre-check must bypass the chat path
		}
		if resp.StopReason != "end_turn" {
			return false
		}
		if resp.Usage.InputTokens != int(in) || resp.Usage.OutputTokens != int(out) {
			return false
		}
		if len(resp.Content) != 1 || resp.Content[0].Type != "text" {
			return false
		}
		return resp.Content[0].Text == text
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 150}); err != nil {
		t.Fatal(err)
	}
}

// --- Property 2 (1.5): tool-free requests preserve text-only behavior ---

func TestAnthropicToolFreeTextOnlyProperty(t *testing.T) {
	// Feature: governed-client-onboarding, Property 2: with no tools and no tool
	// blocks, the backend body omits tools/tool_choice and the response is a
	// single text block.
	f := func(rawUser, rawBackend string) bool {
		userMsg := validUTF8(rawUser)
		backendText := validUTF8(rawBackend)
		req := anthropicMessageRequest{
			Model:     "coding-primary",
			MaxTokens: 64,
			Messages: []anthropicMessage{
				{Role: "user", Content: anthropicContent{Text: userMsg, Blocks: []anthropicContentItem{{Type: "text", Text: userMsg}}}},
			},
		}
		var bodyMap map[string]json.RawMessage
		if json.Unmarshal(req.toChatBody("coding-primary"), &bodyMap) != nil {
			return false
		}
		if _, ok := bodyMap["tools"]; ok {
			return false
		}
		if _, ok := bodyMap["tool_choice"]; ok {
			return false
		}
		g := anthropicGateway(chatTextBackend(backendText), audit.NewFileStore(""))
		rec := anthropicServe(g, anthropicRequestBody("coding-primary", userMsg, false), "sess_toolfree")
		if rec.Code != http.StatusOK {
			return false
		}
		var resp anthropicMessageResponse
		if json.Unmarshal(rec.Body.Bytes(), &resp) != nil {
			return false
		}
		return len(resp.Content) == 1 && resp.Content[0].Type == "text" && resp.Content[0].Text == backendText
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 150}); err != nil {
		t.Fatal(err)
	}
}

// --- Property 3 (1.3): tool definitions translate losslessly ---

func TestAnthropicToolDefinitionTranslationProperty(t *testing.T) {
	// Feature: governed-client-onboarding, Property 3: each Anthropic tool maps
	// to an OpenAI function tool preserving name/description/schema, and the
	// {type:tool,name} choice maps to {type:function,function:{name}}.
	f := func(rawName, rawDesc string) bool {
		name := validUTF8(rawName)
		desc := validUTF8(rawDesc)
		if strings.TrimSpace(name) == "" {
			return true
		}
		schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
		req := anthropicMessageRequest{
			Model:      "coding-primary",
			MaxTokens:  64,
			Messages:   []anthropicMessage{{Role: "user", Content: anthropicContent{Text: "hi", Blocks: []anthropicContentItem{{Type: "text", Text: "hi"}}}}},
			Tools:      []anthropicTool{{Name: name, Description: desc, InputSchema: schema}},
			ToolChoice: json.RawMessage(`{"type":"tool","name":` + strconv.Quote(name) + `}`),
		}
		var parsed struct {
			Tools []struct {
				Type     string `json:"type"`
				Function struct {
					Name        string          `json:"name"`
					Description string          `json:"description"`
					Parameters  json.RawMessage `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
			ToolChoice json.RawMessage `json:"tool_choice"`
		}
		if json.Unmarshal(req.toChatBody("coding-primary"), &parsed) != nil {
			return false
		}
		if len(parsed.Tools) != 1 {
			return false
		}
		tool := parsed.Tools[0]
		if tool.Type != "function" || tool.Function.Name != name {
			return false
		}
		if desc != "" && tool.Function.Description != desc {
			return false
		}
		var gotSchema, wantSchema any
		_ = json.Unmarshal(tool.Function.Parameters, &gotSchema)
		_ = json.Unmarshal(schema, &wantSchema)
		if !reflect.DeepEqual(gotSchema, wantSchema) {
			return false
		}
		var gotChoice, wantChoice any
		_ = json.Unmarshal(parsed.ToolChoice, &gotChoice)
		_ = json.Unmarshal([]byte(`{"type":"function","function":{"name":`+strconv.Quote(name)+`}}`), &wantChoice)
		return reflect.DeepEqual(gotChoice, wantChoice)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 150}); err != nil {
		t.Fatal(err)
	}
}

// --- Property 4 (1.6): tool conversation round-trips through translation ---

func TestAnthropicToolConversationRoundTripProperty(t *testing.T) {
	// Feature: governed-client-onboarding, Property 4: assistant tool_use and
	// user tool_result blocks translate to tool_calls and a {role:tool} message
	// with ids/names/arguments and text preserved.
	f := func(rawID, rawName, rawAsst, rawResult, rawKey, rawVal string) bool {
		id := validUTF8(rawID)
		name := validUTF8(rawName)
		asst := validUTF8(rawAsst)
		result := validUTF8(rawResult)
		key := validUTF8(rawKey)
		val := validUTF8(rawVal)
		if strings.TrimSpace(id) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(key) == "" {
			return true
		}
		input := map[string]any{key: val}
		reqMap := map[string]any{
			"model":      "coding-primary",
			"max_tokens": 128,
			"messages": []any{
				map[string]any{"role": "assistant", "content": []any{
					map[string]any{"type": "text", "text": asst},
					map[string]any{"type": "tool_use", "id": id, "name": name, "input": input},
				}},
				map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "tool_result", "tool_use_id": id, "content": result},
				}},
			},
		}
		raw, _ := json.Marshal(reqMap)
		var req anthropicMessageRequest
		if json.Unmarshal(raw, &req) != nil {
			return false
		}
		var parsed struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
				ToolCalls  []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if json.Unmarshal(req.toChatBody("coding-primary"), &parsed) != nil {
			return false
		}
		var sawAssistant, sawTool bool
		for _, m := range parsed.Messages {
			switch {
			case len(m.ToolCalls) > 0:
				if m.Role != "assistant" || m.Content != asst {
					return false
				}
				tc := m.ToolCalls[0]
				if tc.ID != id || tc.Type != "function" || tc.Function.Name != name {
					return false
				}
				var gotArgs, wantArgs any
				if json.Unmarshal([]byte(tc.Function.Arguments), &gotArgs) != nil {
					return false
				}
				_ = json.Unmarshal([]byte(`{}`), &wantArgs)
				wantArgs = map[string]any{key: val}
				if !reflect.DeepEqual(gotArgs, wantArgs) {
					return false
				}
				sawAssistant = true
			case m.Role == "tool":
				if m.ToolCallID != id || m.Content != result {
					return false
				}
				sawTool = true
			}
		}
		return sawAssistant && sawTool
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 150}); err != nil {
		t.Fatal(err)
	}
}

// --- Property 5 (1.7): non-streaming tool calls set stop_reason tool_use ---

func TestAnthropicNonStreamingToolUseStopReasonProperty(t *testing.T) {
	// Feature: governed-client-onboarding, Property 5: any chat completion that
	// returns tool calls yields stop_reason "tool_use" and a tool_use block.
	f := func(rawID, rawName string) bool {
		id := validUTF8(rawID)
		name := validUTF8(rawName)
		if strings.TrimSpace(name) == "" {
			return true
		}
		backend := &rawFakeBackend{chat: chatCompletionToolBody(id, name, `{"path":"x"}`)}
		g := anthropicGateway(backend, audit.NewFileStore(""))
		rec := anthropicServe(g, anthropicRequestBody("coding-primary", "hi", false), "sess_toolstop")
		if rec.Code != http.StatusOK {
			return false
		}
		var resp anthropicMessageResponse
		if json.Unmarshal(rec.Body.Bytes(), &resp) != nil {
			return false
		}
		if resp.StopReason != "tool_use" {
			return false
		}
		for _, b := range resp.Content {
			if b.Type == "tool_use" && b.ID == id && b.Name == name {
				return true
			}
		}
		return false
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 150}); err != nil {
		t.Fatal(err)
	}
}

// --- Property 6 (1.12): streaming tool arguments reassemble exactly ---

func TestAnthropicStreamToolArgsReassembleProperty(t *testing.T) {
	// Feature: governed-client-onboarding, Property 6: streamed argument
	// fragments reassemble to the exact JSON, framed by one content_block_start
	// and one content_block_stop for the tool block.
	f := func(rawID, rawName string, rawFrags []string) bool {
		id := validUTF8(rawID)
		name := validUTF8(rawName)
		frags := make([]string, len(rawFrags))
		for i, fr := range rawFrags {
			frags[i] = validUTF8(fr)
		}
		var sb strings.Builder
		sb.WriteString(sseToolStartChunk(0, id, name))
		for _, fr := range frags {
			sb.WriteString(sseToolArgChunk(0, fr))
		}
		sb.WriteString(sseFinishChunk("tool_calls"))
		sb.WriteString(sseDoneFrame)
		g := anthropicGateway(&rawChatStreamBackend{sse: sb.String()}, audit.NewFileStore(""))
		rec := anthropicServe(g, anthropicRequestBody("coding-primary", "hi", true), "sess_streamtool")
		if rec.Code != http.StatusOK {
			return false
		}
		events := parseAnthropicEvents(rec.Body.String())
		toolStarts, toolStops := 0, 0
		var partial string
		var stop string
		for _, e := range events {
			switch e.Type {
			case "content_block_start":
				if e.BlockType == "tool_use" {
					toolStarts++
					if e.Index != 1 {
						return false
					}
				}
			case "content_block_delta":
				if e.Index == 1 {
					partial += e.PartialJSON
				}
			case "content_block_stop":
				if e.Index == 1 {
					toolStops++
				}
			case "message_delta":
				stop = e.StopReason
			}
		}
		return toolStarts == 1 && toolStops == 1 && partial == strings.Join(frags, "") && stop == "tool_use"
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 150}); err != nil {
		t.Fatal(err)
	}
}

// --- Property 7 (1.13): interleaved streamed content uses distinct block indices ---

func TestAnthropicStreamInterleavedIndicesProperty(t *testing.T) {
	// Feature: governed-client-onboarding, Property 7: text stays at index 0,
	// each tool gets a distinct index, every content_block_start is matched by a
	// stop, and the terminal message_delta stop reason is tool_use.
	f := func(rawText string, nRaw uint8) bool {
		text := validUTF8(rawText)
		n := int(nRaw%5) + 1
		var sb strings.Builder
		if text != "" {
			sb.WriteString(sseTextChunk(text))
		}
		for i := 0; i < n; i++ {
			sb.WriteString(sseToolStartChunk(i, fmt.Sprintf("tool_%d", i), fmt.Sprintf("fn_%d", i)))
			sb.WriteString(sseToolArgChunk(i, fmt.Sprintf(`{"i":%d}`, i)))
		}
		sb.WriteString(sseFinishChunk("tool_calls"))
		sb.WriteString(sseDoneFrame)
		g := anthropicGateway(&rawChatStreamBackend{sse: sb.String()}, audit.NewFileStore(""))
		rec := anthropicServe(g, anthropicRequestBody("coding-primary", "hi", true), "sess_interleaved")
		if rec.Code != http.StatusOK {
			return false
		}
		events := parseAnthropicEvents(rec.Body.String())
		startIdx := map[int]int{}
		stopIdx := map[int]int{}
		toolIndices := map[int]bool{}
		var gotText, stop string
		for _, e := range events {
			switch e.Type {
			case "content_block_start":
				startIdx[e.Index]++
				if e.BlockType == "tool_use" {
					toolIndices[e.Index] = true
				}
			case "content_block_stop":
				stopIdx[e.Index]++
			case "content_block_delta":
				if e.Index == 0 {
					gotText += e.TextDelta
				}
			case "message_delta":
				stop = e.StopReason
			}
		}
		// text at index 0 reassembled
		if text != "" && gotText != text {
			return false
		}
		// distinct tool indices, one per tool, all = openAI index + 1
		if len(toolIndices) != n {
			return false
		}
		for i := 0; i < n; i++ {
			if !toolIndices[i+1] {
				return false
			}
		}
		// every start matched by exactly one stop at the same index
		if !reflect.DeepEqual(startIdx, stopIdx) {
			return false
		}
		return stop == "tool_use"
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 150}); err != nil {
		t.Fatal(err)
	}
}

// --- Example / edge tests (1.10) ---

func TestAnthropicResponsesPrecheckBranch(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &responsesFallbackBackend{}
	g := anthropicCopilotGateway(backend, auditStore)
	rec := anthropicServe(g, anthropicRequestBody("coding-primary", "hi", false), "sess_precheck")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if backend.responsesCalls == 0 {
		t.Fatalf("expected the Responses bridge to be used, responsesCalls=%d", backend.responsesCalls)
	}
	if backend.chatCalls != 0 {
		t.Fatalf("expected the chat path to be bypassed, chatCalls=%d", backend.chatCalls)
	}
	var resp anthropicMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "ok" {
		t.Fatalf("unexpected bridged content: %#v", resp.Content)
	}
}

func TestAnthropicResponsesFallbackBranch(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &responsesFallbackBackend{chatErr: errors.New("unsupported_api_for_model: this model is not accessible via the /chat/completions endpoint")}
	// Non-Responses-only model id so the pre-check is skipped and the failed
	// chat call drives the responses-only fallback.
	g := anthropicModelGateway("github-copilot", "gpt-4o", backend, auditStore)
	rec := anthropicServe(g, anthropicRequestBody("coding-primary", "hi", false), "sess_fallback")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after fallback, got %d: %s", rec.Code, rec.Body.String())
	}
	if backend.chatCalls == 0 {
		t.Fatalf("expected the chat path to be attempted first")
	}
	if backend.responsesCalls == 0 {
		t.Fatalf("expected the responses fallback to run")
	}
}

func TestAnthropicToolChoiceMappings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want any
		ok   bool
	}{
		{"string-auto", `"auto"`, "auto", true},
		{"obj-auto", `{"type":"auto"}`, "auto", true},
		{"obj-any", `{"type":"any"}`, "required", true},
		{"obj-none", `{"type":"none"}`, "none", true},
		{"obj-tool", `{"type":"tool","name":"read_file"}`, map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := anthropicToolChoiceToOpenAI(json.RawMessage(tc.in))
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v", ok, tc.ok)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
	if _, ok := anthropicToolChoiceToOpenAI(nil); ok {
		t.Fatalf("expected omitted tool_choice to map to ok=false")
	}
}

func TestAnthropicNonStreamingToolUseAuditUnchanged(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{chat: chatCompletionToolBody("toolu_1", "read_file", `{"path":"main.go"}`)}
	g := anthropicGateway(backend, auditStore)
	rec := anthropicServe(g, anthropicRequestBody("coding-primary", "hi", false), "sess_toolaudit")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp anthropicMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Fatalf("expected stop_reason tool_use, got %q", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "tool_use" || resp.Content[0].Name != "read_file" {
		t.Fatalf("unexpected tool_use content: %#v", resp.Content)
	}
	var gotInput any
	_ = json.Unmarshal(resp.Content[0].Input, &gotInput)
	if !reflect.DeepEqual(gotInput, map[string]any{"path": "main.go"}) {
		t.Fatalf("unexpected tool_use input: %#v", gotInput)
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_toolaudit")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "model.gateway_call" {
		t.Fatalf("expected one model.gateway_call audit event, got %#v", events)
	}
}
