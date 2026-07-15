package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// backupFile copies src to "<src>.bak-<RFC3339-safe timestamp>" and returns the
// backup path. If src does not exist there is nothing to clobber, so it returns
// ("", nil) and the caller proceeds against a fresh file. Any other read/write
// error propagates so the caller can abort before mutating the original.
func backupFile(src string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	// Colons are illegal in Windows paths and awkward everywhere; keep the
	// timestamp filesystem-safe.
	ts := strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339), ":", "-")
	backup := src + ".bak-" + ts
	if err := os.WriteFile(backup, data, 0600); err != nil {
		return "", err
	}
	return backup, nil
}

// mergeJSONSettings reads dst (or {} when absent), deep-merges overlay into it
// (objects merge recursively, overlay scalars/arrays win), and writes the result
// with 0600 perms. Idempotent: merging the same overlay twice equals merging it
// once.
func mergeJSONSettings(dst string, overlay map[string]any) error {
	base := map[string]any{}
	data, err := os.ReadFile(dst)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &base); err != nil {
			return fmt.Errorf("parse %s: %w", dst, err)
		}
	}
	merged := deepMergeJSON(base, overlay)
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	return os.WriteFile(dst, append(out, '\n'), 0600)
}

// deepMergeJSON recursively merges overlay into dst in place. Objects merge
// recursively; scalars and arrays from overlay replace whatever is in dst.
func deepMergeJSON(dst, overlay map[string]any) map[string]any {
	for k, v := range overlay {
		if overlayObj, ok := v.(map[string]any); ok {
			if existingObj, ok := dst[k].(map[string]any); ok {
				dst[k] = deepMergeJSON(existingObj, overlayObj)
				continue
			}
		}
		dst[k] = v
	}
	return dst
}
