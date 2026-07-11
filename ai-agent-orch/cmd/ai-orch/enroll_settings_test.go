package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/quick"
)

// claudeOverlay is a representative enrolment overlay used to exercise the
// deep-merge helper.
func claudeOverlay() map[string]any {
	return map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":   "https://models.example.com",
			"ANTHROPIC_AUTH_TOKEN": "runtime-token",
		},
		"hooks": map[string]any{
			"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "ai-orch hook stop"}}}},
		},
	}
}

// Property 9: Backup-then-merge preserves originals and is idempotent.
// Feature: governed-client-onboarding, Property 9
// Validates: Requirements 7.2, 7.5, 9.3
func TestProperty9BackupThenMergePreservesAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	var counter int64

	property := func(rawKeys map[string]string) bool {
		// Build a pre-existing settings object from unrelated keys (prefixed so
		// they never collide with the overlay's env/hooks keys).
		original := map[string]any{}
		for k, v := range rawKeys {
			original["u_"+k] = v
		}
		originalBytes, err := json.MarshalIndent(original, "", "  ")
		if err != nil {
			return false
		}
		originalBytes = append(originalBytes, '\n')

		path := filepath.Join(dir, fmt.Sprintf("settings-%d.json", atomic.AddInt64(&counter, 1)))
		if err := os.WriteFile(path, originalBytes, 0600); err != nil {
			return false
		}

		// backupFile must capture the original byte-for-byte.
		backup, err := backupFile(path)
		if err != nil || backup == "" {
			return false
		}
		backupBytes, err := os.ReadFile(backup)
		if err != nil || !bytes.Equal(backupBytes, originalBytes) {
			return false
		}

		// First merge: unrelated keys preserved.
		if err := mergeJSONSettings(path, claudeOverlay()); err != nil {
			return false
		}
		mergedOnce, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		var merged map[string]any
		if err := json.Unmarshal(mergedOnce, &merged); err != nil {
			return false
		}
		for k, v := range original {
			if merged[k] != v {
				return false
			}
		}
		// Overlay applied.
		env, ok := merged["env"].(map[string]any)
		if !ok || env["ANTHROPIC_AUTH_TOKEN"] != "runtime-token" {
			return false
		}

		// Second merge with the same overlay is a no-op (idempotent).
		if err := mergeJSONSettings(path, claudeOverlay()); err != nil {
			return false
		}
		mergedTwice, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		return bytes.Equal(mergedOnce, mergedTwice)
	}

	if err := quick.Check(property, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatalf("backup-then-merge property failed: %v", err)
	}
}

// Backup failure must leave the settings file byte-for-byte unchanged. A
// read-only parent directory makes the backup write fail while the original
// stays readable, mirroring the abort-before-mutation contract.
func TestBackupFailureLeavesSettingsUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := []byte("{\n  \"theme\": \"dark\"\n}\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	// Read-only dir: ReadFile(path) still succeeds, WriteFile(backup) fails.
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	if _, err := backupFile(path); err == nil {
		t.Fatal("expected backup to fail in a read-only directory")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings after failed backup: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("settings changed after failed backup: %q", got)
	}
}

func TestBackupFileAbsentSourceReturnsEmpty(t *testing.T) {
	backup, err := backupFile(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || backup != "" {
		t.Fatalf("expected (\"\", nil) for absent source, got %q, %v", backup, err)
	}
}
