package dispatch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (h *acpHandle) handleWriteTextFile(params map[string]any) error {
	if !h.acpWritesAllowed() {
		return fmt.Errorf("ACP write denied by workspace permissions")
	}
	path, _ := params["path"].(string)
	if path == "" {
		path, _ = params["filePath"].(string)
	}
	content, _ := params["content"].(string)
	if path == "" {
		return fmt.Errorf("ACP write missing path")
	}
	fullPath, err := h.workspaceFilePath(path)
	if err != nil {
		return err
	}
	action := "create"
	if _, err := os.Stat(fullPath); err == nil {
		action = "modify"
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat ACP file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create ACP write directory: %w", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write ACP file: %w", err)
	}
	patchID := acpPatchID(h.config.SessionID, path)
	writePayload, _ := json.Marshal(map[string]any{"path": path, "action": action, "patch_id": patchID})
	h.emitEvent(RuntimeEvent{Type: "stream", Payload: fmt.Sprintf("ACP wrote file %s", path)})
	h.emitEvent(RuntimeEvent{Type: "acp_file_write", Payload: string(writePayload)})
	h.emitEvent(RuntimeEvent{Type: "patch", Payload: h.patchEnvelopeForWrite(path, action, content)})
	return nil
}

func (h *acpHandle) patchEnvelopeForWrite(path string, action string, content string) string {
	patchID := acpPatchID(h.config.SessionID, path)
	envelope := map[string]any{
		"protocolVersion": 1,
		"patchId":         patchID,
		"sessionId":       h.config.SessionID,
		"summary":         "ACP file write",
		"rationale":       "OpenCode ACP requested a governed workspace file write.",
		"files": []map[string]string{
			{
				"path":       path,
				"action":     action,
				"newContent": content,
			},
		},
	}
	data, _ := json.Marshal(envelope)
	return string(data)
}

type acpWorkspaceFile struct {
	Hash    string
	Content string
}

type acpWorkspaceSnapshot map[string]acpWorkspaceFile

func captureACPWorkspaceSnapshot(root string) (acpWorkspaceSnapshot, error) {
	root = filepath.Clean(root)
	snapshot := acpWorkspaceSnapshot{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			switch name {
			case ".git", "node_modules", "dist", "dist-paper", ".next", ".turbo", ".wrangler", ".opencode":
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > 512*1024 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !isLikelyText(data) {
			return nil
		}
		snapshot[filepath.ToSlash(rel)] = acpWorkspaceFile{Hash: sha256HexBytes(data), Content: string(data)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (h *acpHandle) workspacePatchSince(root string, before acpWorkspaceSnapshot) string {
	after, err := captureACPWorkspaceSnapshot(root)
	if err != nil {
		h.emitEvent(RuntimeEvent{Type: "stream", Payload: fmt.Sprintf("ACP workspace diff skipped: %v", err)})
		return ""
	}
	paths := make(map[string]struct{}, len(before)+len(after))
	for path := range before {
		paths[path] = struct{}{}
	}
	for path := range after {
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	files := make([]map[string]string, 0)
	for _, path := range ordered {
		beforeFile, hadBefore := before[path]
		afterFile, hasAfter := after[path]
		switch {
		case !hadBefore && hasAfter:
			files = append(files, map[string]string{"path": path, "action": "create", "newContent": afterFile.Content})
		case hadBefore && !hasAfter:
			files = append(files, map[string]string{"path": path, "action": "delete", "originalContentHash": beforeFile.Hash})
		case hadBefore && hasAfter && beforeFile.Hash != afterFile.Hash:
			files = append(files, map[string]string{"path": path, "action": "modify", "originalContentHash": beforeFile.Hash, "newContent": afterFile.Content})
		}
	}
	if len(files) == 0 {
		return ""
	}
	envelope := map[string]any{
		"protocolVersion": 1,
		"patchId":         acpPatchID(h.config.SessionID, "workspace-diff"),
		"sessionId":       h.config.SessionID,
		"summary":         "ACP workspace diff",
		"rationale":       "OpenCode ACP changed workspace files during the governed run.",
		"files":           files,
	}
	data, _ := json.Marshal(envelope)
	return string(data)
}

func sha256HexBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func isLikelyText(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return true
}

func acpPatchID(sessionID string, path string) string {
	sum := sha256.Sum256([]byte(sessionID + "|" + path))
	return "acp_write_" + hex.EncodeToString(sum[:8])
}

func (h *acpHandle) handleReadTextFile(params map[string]any) (string, error) {
	path, _ := params["path"].(string)
	if path == "" {
		path, _ = params["filePath"].(string)
	}
	if path == "" {
		return "", fmt.Errorf("ACP read missing path")
	}
	fullPath, err := h.workspaceFilePath(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("read ACP file: %w", err)
	}
	return string(data), nil
}

func (h *acpHandle) workspaceFilePath(path string) (string, error) {
	workspace, err := h.workspacePath()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	var fullPath string
	if filepath.IsAbs(path) {
		fullPath = filepath.Clean(path)
	} else {
		fullPath = filepath.Clean(filepath.Join(workspace, path))
	}
	workspaceClean := filepath.Clean(workspace)
	rel, err := filepath.Rel(workspaceClean, fullPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("ACP file path escapes workspace: %s", path)
	}
	return fullPath, nil
}

func (h *acpHandle) workspacePath() (string, error) {
	if h != nil && h.config.WorkspacePath != "" {
		return h.config.WorkspacePath, nil
	}
	return "", fmt.Errorf("ACP workspace path is required")
}
