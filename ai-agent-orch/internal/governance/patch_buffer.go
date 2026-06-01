package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	patchproto "ai-agent-orch/internal/patch"
)

// defaultPatchBufferTTL is the TTL for buffered patch entries.
const defaultPatchBufferTTL = 30 * time.Minute

type PatchBuffer struct {
	mu      sync.RWMutex
	patches map[string]map[string]string
	times   map[string]map[string]time.Time
	ttl     time.Duration
}

func NewPatchBuffer() *PatchBuffer {
	return &PatchBuffer{
		patches: make(map[string]map[string]string),
		times:   make(map[string]map[string]time.Time),
		ttl:     defaultPatchBufferTTL,
	}
}

func (b *PatchBuffer) Store(ctx context.Context, sessionID string, payload string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if sessionID == "" {
		return "", errors.New("session_id is required")
	}

	var envelope patchproto.PatchEnvelope
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return "", fmt.Errorf("parse patch envelope: %w", err)
	}
	if envelope.PatchID == "" {
		return "", errors.New("patchId is required")
	}
	if envelope.SessionID == "" {
		envelope.SessionID = sessionID
	}
	if envelope.SessionID != sessionID {
		return "", errors.New("patch session mismatch")
	}
	if len(envelope.Files) == 0 {
		return "", errors.New("patch must include files")
	}
	if envelope.BufferID == "" {
		envelope.BufferID = bufferID(sessionID, envelope.PatchID, payload)
	}

	sanitized := envelope
	sanitized.Files = make([]patchproto.PatchFile, 0, len(envelope.Files))
	fullFiles := make([]patchproto.PatchFile, 0, len(envelope.Files))
	for _, file := range envelope.Files {
		if err := validatePatchPath(file.Path); err != nil {
			return "", err
		}
		if findings := detectSecrets(file.NewContent); len(findings) > 0 {
			return "", fmt.Errorf("secret detected in patch content: %s", strings.Join(findings, ","))
		}
		if findings := detectSecrets(file.OriginalContent); len(findings) > 0 {
			return "", fmt.Errorf("secret detected in original patch content: %s", strings.Join(findings, ","))
		}
		if file.NewContent != "" {
			file.ProposedContentHash = sha256Hex([]byte(file.NewContent))
		}
		fullFiles = append(fullFiles, file)
		file.NewContent = ""
		file.OriginalContent = ""
		sanitized.Files = append(sanitized.Files, file)
	}
	envelope.Files = fullFiles

	full, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode buffered patch: %w", err)
	}
	safe, err := json.Marshal(sanitized)
	if err != nil {
		return "", fmt.Errorf("encode sanitized patch: %w", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.patches[sessionID] == nil {
		b.patches[sessionID] = make(map[string]string)
	}
	if b.times[sessionID] == nil {
		b.times[sessionID] = make(map[string]time.Time)
	}
	b.patches[sessionID][envelope.PatchID] = string(full)
	b.times[sessionID][envelope.PatchID] = time.Now().UTC()
	return string(safe), nil
}

func (b *PatchBuffer) Get(ctx context.Context, sessionID string, patchID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if sessionID == "" || patchID == "" {
		return "", errors.New("session_id and patch_id are required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.evictLocked()
	patches := b.patches[sessionID]
	if patches == nil {
		return "", errors.New("patch not found")
	}
	payload, ok := patches[patchID]
	if !ok {
		return "", errors.New("patch not found")
	}
	return payload, nil
}

func (b *PatchBuffer) evictLocked() {
	if b == nil || b.ttl <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-b.ttl)
	for sessionID, patchTimes := range b.times {
		for patchID, t := range patchTimes {
			if t.Before(cutoff) {
				delete(b.patches[sessionID], patchID)
				delete(patchTimes, patchID)
			}
		}
		if len(b.patches[sessionID]) == 0 {
			delete(b.patches, sessionID)
		}
		if len(patchTimes) == 0 {
			delete(b.times, sessionID)
		}
	}
}

func validatePatchPath(path string) error {
	if path == "" {
		return errors.New("patch path is required")
	}
	if filepath.IsAbs(path) || windowsAbsPath.MatchString(path) {
		return fmt.Errorf("patch path must be relative: %s", path)
	}
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	for _, part := range parts {
		if part == "." || part == ".." {
			return fmt.Errorf("patch path contains unsafe segment: %s", path)
		}
	}
	return nil
}

var windowsAbsPath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func bufferID(sessionID, patchID, payload string) string {
	sum := sha256.Sum256([]byte(sessionID + "|" + patchID + "|" + payload))
	return "buf_" + hex.EncodeToString(sum[:8])
}
