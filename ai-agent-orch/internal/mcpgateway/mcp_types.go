// Package mcpgateway implements the Model Context Protocol gateway for the Governance Shell.
package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// ProtocolVersion is the MCP protocol version this gateway speaks.
const ProtocolVersion = "2025-11-25"

// Request is an MCP JSON-RPC request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is an MCP JSON-RPC response.
type Response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

// Error is an MCP JSON-RPC error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error codes per MCP spec.
const (
	ErrParseError     = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternalError  = -32603
)

// InitializeParams is sent by the client during initialize.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
}

// InitializeResult is returned by the server during initialize.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// ClientCapabilities describes what the client supports.
type ClientCapabilities struct {
	Roots *struct {
		ListChanged bool `json:"listChanged,omitempty"`
	} `json:"roots,omitempty"`
	Sampling    *struct{} `json:"sampling,omitempty"`
	Elicitation *struct{} `json:"elicitation,omitempty"`
}

// ServerCapabilities describes what the server supports.
type ServerCapabilities struct {
	Logging   *struct{}                   `json:"logging,omitempty"`
	Prompts   *struct{ ListChanged bool } `json:"prompts,omitempty"`
	Resources *struct {
		Subscribe   bool `json:"subscribe,omitempty"`
		ListChanged bool `json:"listChanged,omitempty"`
	} `json:"resources,omitempty"`
	Tools *struct{ ListChanged bool } `json:"tools,omitempty"`
}

// Implementation identifies a client or server.
type Implementation struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// ToolsListParams is the params for tools/list.
type ToolsListParams struct {
	Cursor string `json:"cursor,omitempty"`
}

// ToolsListResult is the result for tools/list.
type ToolsListResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// Tool describes an available tool.
type Tool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations *Annotations    `json:"annotations,omitempty"`
}

// Annotations provides optional metadata for tools/resources.
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint,omitempty"`
	OpenWorldHint   bool   `json:"openWorldHint,omitempty"`
	DestructiveHint bool   `json:"destructiveHint,omitempty"`
}

// ToolsCallParams is the params for tools/call.
type ToolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolsCallResult is the result for tools/call.
type ToolsCallResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ContentItem is a single item in a tool call result.
type ContentItem struct {
	Type        string       `json:"type"`
	Text        string       `json:"text,omitempty"`
	Data        string       `json:"data,omitempty"`
	MimeType    string       `json:"mimeType,omitempty"`
	URI         string       `json:"uri,omitempty"`
	Resource    *Resource    `json:"resource,omitempty"`
	Annotations *Annotations `json:"annotations,omitempty"`
}

// Resource represents an embedded resource.
type Resource struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// TextContent returns a text content item.
func TextContent(text string) ContentItem {
	return ContentItem{Type: "text", Text: text}
}

// ToolHandler is the signature for a tool implementation.
type ToolHandler func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error)

// Server is an MCP server.
type Server struct {
	mu       sync.RWMutex
	tools    map[string]Tool
	handlers map[string]ToolHandler
	info     Implementation
}

// NewServer creates a new MCP server.
func NewServer(name, version string) *Server {
	return &Server{
		tools:    make(map[string]Tool),
		handlers: make(map[string]ToolHandler),
		info: Implementation{
			Name:    name,
			Version: version,
		},
	}
}

// RegisterTool adds a tool to the server.
func (s *Server) RegisterTool(t Tool, handler ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[t.Name] = t
	s.handlers[t.Name] = handler
}

// Handle processes a single MCP request and returns a response.
func (s *Server) Handle(ctx context.Context, req *Request) *Response {
	if req.JSONRPC != "2.0" {
		return errorResponse(req.ID, ErrInvalidRequest, "invalid jsonrpc version")
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	case "notifications/initialized":
		// No response needed for notifications.
		return nil
	default:
		return errorResponse(req.ID, ErrMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *Server) handleInitialize(req *Request) *Response {
	var params InitializeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, ErrInvalidParams, "invalid initialize params")
	}

	result := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities: ServerCapabilities{
			Tools: &struct{ ListChanged bool }{ListChanged: true},
		},
		ServerInfo: s.info,
		Instructions: `This is the ai-orch MCP Gateway.
Use start_governed_session before agentic engineering work.
Use delegate_governed_work to route real work through the Governance Shell.
Use record_patch_decision to submit patch decisions.
Use lookup_audit to verify governance metadata.
Do not call provider models directly for governed work.`,
	}
	return &Response{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func (s *Server) handleToolsList(req *Request) *Response {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tools := make([]Tool, 0, len(s.tools))
	for _, t := range s.tools {
		tools = append(tools, t)
	}

	result := ToolsListResult{Tools: tools}
	return &Response{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func (s *Server) handleToolsCall(ctx context.Context, req *Request) *Response {
	var params ToolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, ErrInvalidParams, "invalid tools/call params")
	}

	s.mu.RLock()
	handler, ok := s.handlers[params.Name]
	s.mu.RUnlock()

	if !ok {
		return errorResponse(req.ID, ErrInvalidParams, fmt.Sprintf("unknown tool: %s", params.Name))
	}

	result, err := handler(ctx, params.Arguments)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolsCallResult{
				Content: []ContentItem{TextContent(err.Error())},
				IsError: true,
			},
		}
	}

	return &Response{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func errorResponse(id any, code int, message string) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: code, Message: message},
	}
}
