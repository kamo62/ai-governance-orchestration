// This TypeScript VS Code extension intentionally sits under the repository's
// Go service tree. Keep it as a nested module so root-level Go package
// discovery, tests, and vulnerability scans do not traverse JS dependencies
// installed under node_modules.
module github.com/kamo62/ai-governance-orchestration/ai-agent-orch/agent-bridge

go 1.26

toolchain go1.26.4
