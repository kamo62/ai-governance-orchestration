import type { Plugin } from "@opencode-ai/plugin"

// ai-orch-context detects the local git checkout once and sends it to the
// governed gateway as request headers. It runs wherever OpenCode runs (in the
// developer's checkout), which is how Codex/Claude/OpenCode self-detect git,
// rather than relying on a server-side resolver that only sees the container.
//
// chat.headers sends X-AI-Orch-Client-Session-ID (so the gateway reuses one
// governed session per conversation), X-AI-Orch-Parent-Client-Session-ID for
// local subagent sessions, plus the three git headers the gateway records.
// Scoped to the ai-orch providers so nothing leaks elsewhere.
//
// The plugin deliberately does not mutate message parts: OpenCode validates part
// ids and synthetic parts, so editing the transcript from a plugin is fragile.
// The gateway records git context for governance and audit but does not inject it
// into prompts; agent awareness of the repo is left to the agent itself
// (specialists can run git).

function sanitizeRemote(raw: string): string {
  const value = (raw ?? "").trim()
  if (!value) return ""
  try {
    const parsed = new URL(value)
    parsed.username = ""
    parsed.password = ""
    return parsed.toString()
  } catch {
    // scp-style (git@host:org/repo.git) or non-URL remotes: pass through as-is.
    return value
  }
}

function isAiOrchProvider(providerID?: string): boolean {
  return !!providerID && providerID.startsWith("ai-orch")
}

export const AiOrchContextPlugin: Plugin = async ({ $, worktree, directory }) => {
  const dir = worktree || directory || process.cwd()

  const git = async (command: string): Promise<string> => {
    try {
      const res = await $`git -C ${dir} ${{ raw: command }}`.nothrow().quiet()
      return res.exitCode === 0 ? res.text().trim() : ""
    } catch {
      return ""
    }
  }

  const repoURL = sanitizeRemote(await git("remote get-url origin"))
  const branch = await git("rev-parse --abbrev-ref HEAD")
  const commit = await git("rev-parse HEAD")

  let rootClientSessionID = ""

  return {
    "chat.headers": async (input, output) => {
      if (!isAiOrchProvider(input.model?.providerID)) return
      if (input.sessionID) {
        if (!rootClientSessionID) rootClientSessionID = input.sessionID
        output.headers["X-AI-Orch-Client-Session-ID"] = input.sessionID
        if (rootClientSessionID !== input.sessionID) {
          output.headers["X-AI-Orch-Parent-Client-Session-ID"] = rootClientSessionID
        }
      }
      if (repoURL) output.headers["X-AI-Orch-Repo-URL"] = repoURL
      if (branch) output.headers["X-AI-Orch-Branch"] = branch
      if (commit) output.headers["X-AI-Orch-Commit-SHA"] = commit
    },
  }
}

export default AiOrchContextPlugin
