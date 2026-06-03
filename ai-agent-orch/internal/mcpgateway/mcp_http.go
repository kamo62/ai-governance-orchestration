package mcpgateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"ai-agent-orch/internal/httpauth"
)

// HTTPServer wraps an MCP Server with HTTP/SSE transport.
type HTTPServer struct {
	mcp      *Server
	devToken string
	mu       sync.Mutex
	sessions map[string]*httpSession // keyed by session ID for SSE
}

type httpSession struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

// NewHTTPServer creates a new HTTP transport for the MCP server.
func NewHTTPServer(mcp *Server, devToken string) *HTTPServer {
	return &HTTPServer{
		mcp:      mcp,
		devToken: devToken,
		sessions: make(map[string]*httpSession),
	}
}

// RegisterRoutes registers the MCP HTTP routes on the provided mux.
func (h *HTTPServer) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/mcp/v1/sse", h.handleSSE)
	mux.HandleFunc("/mcp/v1/messages", h.handleMessages)
	mux.HandleFunc("/mcp/healthz", h.handleHealthz)
}

func (h *HTTPServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "server": h.mcp.info.Name})
}

func (h *HTTPServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		h.handleOptions(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.allowOrigin(w, r) {
		return
	}
	if !h.authenticate(w, r) {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	sessionID := generateSessionID()
	sess := &httpSession{w: w, flusher: flusher}
	h.mu.Lock()
	h.sessions[sessionID] = sess
	h.mu.Unlock()

	// Send the endpoint event so the client knows where to POST messages.
	fmt.Fprintf(w, "event: endpoint\ndata: /mcp/v1/messages?session_id=%s\n\n", sessionID)
	flusher.Flush()

	// Keep connection open until client disconnects.
	<-r.Context().Done()

	h.mu.Lock()
	delete(h.sessions, sessionID)
	h.mu.Unlock()
}

func (h *HTTPServer) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		h.handleOptions(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.allowOrigin(w, r) {
		return
	}
	if !h.authenticate(w, r) {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(nil, ErrParseError, "parse error"))
		return
	}

	resp := h.mcp.Handle(r.Context(), &req)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// If the client connected via SSE, send the response as an SSE event.
	sessionID := r.URL.Query().Get("session_id")
	if sessionID != "" {
		h.mu.Lock()
		sess, ok := h.sessions[sessionID]
		h.mu.Unlock()
		if ok && sess != nil {
			sess.mu.Lock()
			defer sess.mu.Unlock()
			data, _ := json.Marshal(resp)
			fmt.Fprintf(sess.w, "event: message\ndata: %s\n\n", string(data))
			sess.flusher.Flush()
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}

	// Otherwise, return the response directly (for non-SSE clients).
	writeJSON(w, http.StatusOK, resp)
}

func (h *HTTPServer) authenticate(w http.ResponseWriter, r *http.Request) bool {
	if h.devToken == "" {
		http.Error(w, "mcp bearer token not configured", http.StatusServiceUnavailable)
		return false
	}
	if !httpauth.AuthorizedBearer(r.Header.Get("Authorization"), h.devToken) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (h *HTTPServer) handleOptions(w http.ResponseWriter, r *http.Request) {
	if !h.allowOrigin(w, r) {
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPServer) allowOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if !isLocalOrigin(origin) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	return true
}

func isLocalOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func generateSessionID() string {
	return fmt.Sprintf("sess_%d", time.Now().UnixNano())
}
