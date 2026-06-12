package governance

import (
	"context"
	"time"
)

// rememberEventID stores the latest audit event ID for a session.
func (s *SessionService) rememberEventID(sessionID, eventID string) {
	if s == nil || sessionID == "" || eventID == "" {
		return
	}
	s.lastEventMu.Lock()
	defer s.lastEventMu.Unlock()
	if s.lastEventID == nil {
		s.lastEventID = make(map[string]string)
	}
	s.lastEventID[sessionID] = eventID
}

// parentEventID returns the last known audit event ID for a session.
func (s *SessionService) parentEventID(sessionID string) string {
	if s == nil {
		return ""
	}
	s.lastEventMu.Lock()
	defer s.lastEventMu.Unlock()
	return s.lastEventID[sessionID]
}

func (s *SessionService) registerCancel(sessionID string, cancel context.CancelFunc) {
	if s == nil || sessionID == "" {
		return
	}
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	if s.cancels == nil {
		s.cancels = make(map[string]context.CancelFunc)
		s.cancelTimes = make(map[string]time.Time)
	}
	s.cancels[sessionID] = cancel
	s.cancelTimes[sessionID] = time.Now().UTC()
}

func (s *SessionService) cancelExecution(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	s.evictCancelsLocked()
	if cancel, ok := s.cancels[sessionID]; ok {
		cancel()
		delete(s.cancels, sessionID)
		delete(s.cancelTimes, sessionID)
	}
}

func (s *SessionService) evictCancelsLocked() {
	if s == nil || s.localStateTTL <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-s.localStateTTL)
	for id, t := range s.cancelTimes {
		if t.Before(cutoff) {
			delete(s.cancels, id)
			delete(s.cancelTimes, id)
		}
	}
}

func (s *SessionService) rememberPrompt(sessionID string, prompt string) {
	if s == nil || sessionID == "" || prompt == "" {
		return
	}
	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	s.prompts[sessionID] = prompt
	s.promptTimes[sessionID] = time.Now().UTC()
}

func (s *SessionService) promptForSession(sessionID string) (string, bool) {
	if s == nil || sessionID == "" {
		return "", false
	}
	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	s.evictPromptsLocked()
	prompt, ok := s.prompts[sessionID]
	return prompt, ok
}

func (s *SessionService) forgetPrompt(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	delete(s.prompts, sessionID)
	delete(s.promptTimes, sessionID)
}

func (s *SessionService) evictPromptsLocked() {
	if s == nil || s.localStateTTL <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-s.localStateTTL)
	for id, t := range s.promptTimes {
		if t.Before(cutoff) {
			delete(s.prompts, id)
			delete(s.promptTimes, id)
		}
	}
}

func (s *SessionService) rememberPatch(sessionID string, patchID string) {
	if s == nil || sessionID == "" || patchID == "" {
		return
	}
	s.patchMu.Lock()
	defer s.patchMu.Unlock()
	if s.patches[sessionID] == nil {
		s.patches[sessionID] = make(map[string]struct{})
	}
	if s.patchTimes[sessionID] == nil {
		s.patchTimes[sessionID] = make(map[string]time.Time)
	}
	s.patches[sessionID][patchID] = struct{}{}
	s.patchTimes[sessionID][patchID] = time.Now().UTC()
}

func (s *SessionService) patchKnown(sessionID string, patchID string) bool {
	if s == nil || sessionID == "" || patchID == "" {
		return false
	}
	s.patchMu.Lock()
	defer s.patchMu.Unlock()
	s.evictPatchesLocked()
	patches := s.patches[sessionID]
	_, ok := patches[patchID]
	return ok
}

func (s *SessionService) evictPatchesLocked() {
	if s == nil || s.localStateTTL <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-s.localStateTTL)
	for sessionID, patchTimes := range s.patchTimes {
		for patchID, t := range patchTimes {
			if t.Before(cutoff) {
				delete(s.patches[sessionID], patchID)
				delete(patchTimes, patchID)
			}
		}
		if len(s.patches[sessionID]) == 0 {
			delete(s.patches, sessionID)
		}
		if len(patchTimes) == 0 {
			delete(s.patchTimes, sessionID)
		}
	}
}
