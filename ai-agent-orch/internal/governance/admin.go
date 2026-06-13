package governance

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

// KillSwitchStore manages kill-switch state.
type KillSwitchStore interface {
	IsBlocked(scope, id string) bool
	Block(scope, id string) error
	Unblock(scope, id string) error
	List() map[string][]string
}

// MemoryKillSwitch is an in-memory kill-switch store for local Phase 1.
type MemoryKillSwitch struct {
	mu    sync.RWMutex
	state map[string]map[string]struct{}
}

func NewMemoryKillSwitch() *MemoryKillSwitch {
	return &MemoryKillSwitch{
		state: make(map[string]map[string]struct{}),
	}
}

func (m *MemoryKillSwitch) IsBlocked(scope, id string) bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if ids, ok := m.state[scope]; ok {
		_, blocked := ids[id]
		return blocked
	}
	return false
}

func (m *MemoryKillSwitch) Block(scope, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state[scope] == nil {
		m.state[scope] = make(map[string]struct{})
	}
	m.state[scope][id] = struct{}{}
	return nil
}

func (m *MemoryKillSwitch) Unblock(scope, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ids, ok := m.state[scope]; ok {
		delete(ids, id)
	}
	return nil
}

func (m *MemoryKillSwitch) List() map[string][]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string][]string)
	for scope, ids := range m.state {
		var list []string
		for id := range ids {
			list = append(list, id)
		}
		sort.Strings(list)
		result[scope] = list
	}
	return result
}

// AdminHandler serves kill-switch endpoints.
type AdminHandler struct {
	store   KillSwitchStore
	service *SessionService
}

func NewAdminHandler(store KillSwitchStore, service *SessionService) http.Handler {
	return &AdminHandler{store: store, service: service}
}

func (h *AdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return
	}
	if h.store == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "kill switch store unavailable"})
		return
	}
	if !h.service.RequireAdminRequest(w, r) {
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/admin/killswitch")
	if path == "" || path == "/" {
		// List all kill switches.
		if r.Method != http.MethodGet {
			httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"killswitches": h.store.List(),
		})
		return
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "path must be /v1/admin/killswitch/{scope}/{id}"})
		return
	}
	scope, id := parts[0], parts[1]

	switch r.Method {
	case http.MethodPost:
		if err := h.store.Block(scope, id); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"scope":  scope,
			"id":     id,
			"status": "blocked",
		})
	case http.MethodDelete:
		if err := h.store.Unblock(scope, id); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"scope":  scope,
			"id":     id,
			"status": "unblocked",
		})
	default:
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}
