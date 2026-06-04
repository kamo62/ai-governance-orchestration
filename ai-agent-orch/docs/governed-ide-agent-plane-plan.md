# Governed IDE Agent Plane Plan

## Summary

The goal is to make the VS Code experience feel much closer to a practical coding agent, without turning this repository into a CLine clone.

The Governance Shell remains the product bet. The IDE agent plane is the developer-facing surface that makes the governance layer usable from any project folder. It should feel natural inside an engineer's workspace, but model calls, tool calls, patch visibility, audit, cost, and approvals should still pass through the Governance Shell.

## Current State

What exists today:

- VS Code Bridge commands for setup, connection check, invocation, patch review, and audit lookup.
- Local Governance Shell and Orchestrator services.
- Model proxying through the Governance Shell.
- Bounded workspace context packaging from the current VS Code project:
  - workspace name;
  - git branch;
  - origin remote where available;
  - active file path and language;
  - selected text or capped active-file excerpt.
- SSE event streaming from governed sessions.
- Staged patch buffer and native VS Code diff review.
- Explicit patch decisions recorded through audit.

What is still missing compared with a CLine-style experience:

- A real chat surface with session history.
- File and folder attachment controls.
- Explicit workspace context picker.
- Multi-turn interaction with the same governed session.
- Tool-call approval UI.
- Terminal command proposal and execution flow.
- Agent-visible file read/search tooling governed by policy.
- Streaming assistant text in the IDE, not only output-panel logs.
- Clear task state: planning, running, waiting for approval, patch proposed, completed, blocked.
- Recovery/resume for an interrupted session.
- A runtime adapter strategy for OpenCode, ACP, CLine-like runtimes, or other IDE-native agents.

## Recommendation

Use a **hybrid agent-plane strategy**:

- Build a richer VS Code Bridge experience where the Governance Shell is always the boundary.
- Do not rebuild CLine's whole runtime loop from scratch.
- Add an adapter layer so an existing runtime can be evaluated behind the Governance Shell when it becomes useful.

This keeps the system honest:

- The repo does not become another coding-agent product.
- Engineers get a usable local workflow.
- Governance remains independent of any one runtime.
- CLine, OpenCode, Claude Code, Cursor, Aider, or future tools can be treated as runtime patterns behind the boundary.

## Non-Goals

This plan should not:

- Replace CLine, Claude Code, OpenCode, Cursor, Aider, or similar tools.
- Build an autonomous agent loop before the governed local workflow is solid.
- Let a runtime call model providers directly with provider secrets.
- Let a runtime write directly to the workspace without staged patch review.
- Store all repo content in prompt context.
- Turn the Governance Shell into an IDE extension.

## Target Experience

An engineer should be able to:

1. Open any repository in VS Code.
2. Start or connect to the local Governance Shell.
3. Open the AI Agent Bridge panel.
4. Pick or confirm a use case/workflow.
5. Ask for coding help in a chat-style interface.
6. Attach files, folders, current selection, or search results deliberately.
7. See the selected agent, model alias, policy state, and estimated cost.
8. Approve or deny tool calls and terminal commands.
9. Review proposed patches in native VS Code diffs.
10. Apply, partially apply, or reject the patch.
11. See the audit trail and evidence record for the session.

The important behaviour is that the Bridge works from the currently open project folder. The Governance Shell is a local service; it is not tied to the `ai-orch` repository workspace.

## Architecture Direction

```text
VS Code Project Folder
  |
  |  bounded context, file refs, user intent
  v
VS Code Bridge Agent Plane
  |
  |  session create, context manifests, approval decisions
  v
Governance Shell
  |
  |  policy, audit, model proxy, MCP proxy, patch buffer, cost, evidence
  v
Orchestrator
  |
  |  route, runtime dispatch, tool event relay
  v
Runtime Adapter
  |
  |  OpenCode / ACP / future runtime candidate
  v
Model + Tools
```

The Bridge owns UX. The Governance Shell owns authority. The Orchestrator owns routing and runtime dispatch. Runtime adapters execute behind the governed boundary.

## Phase 1: Better Bridge Panel

Goal: move from command-palette invocation to an actual IDE agent surface.

Deliverables:

- Add a VS Code Webview panel or sidebar view named `AI Agent Bridge`.
- Show connection status, active workspace, active branch, and current Governance Shell URL.
- Show current session status.
- Provide a prompt input with chat-style history.
- Show assistant stream events in the panel.
- Keep the Output panel for diagnostics only.
- Add buttons for:
  - start session;
  - attach current file;
  - attach selection;
  - attach files;
  - run;
  - abort;
  - show audit.

Acceptance criteria:

- The user can invoke an agent without using the Command Palette after setup.
- The panel clearly shows which project folder is being used as context.
- The panel refuses to run if the Governance Shell readiness payload is not correct.
- The Bridge still works when the opened VS Code folder is not the `ai-orch` repo.

## Phase 2: Context Manifest UX

Goal: make context deliberate and auditable instead of dumping the workspace into the model.

Deliverables:

- Add a context manifest builder in the Bridge.
- Let the user attach:
  - current file;
  - selected text;
  - multiple files;
  - folder summaries;
  - search results;
  - test output;
  - terminal output snippets.
- Display token/character estimates before sending.
- Send context references and hashes to the Governance Shell.
- Persist a context manifest record for the session.
- Keep full raw context out of audit logs unless explicitly allowed by policy.

Acceptance criteria:

- The user can see exactly what context will be sent.
- The Governance Shell can record context provenance without storing raw secrets.
- Large files are capped or summarised before model submission.
- Secret scanning runs before context is sent to a runtime.

## Phase 3: Governed Tool Calls

Goal: support a CLine-like tool loop, but with policy authority in the Governance Shell.

Deliverables:

- Define a Bridge tool-call request shape:
  - `read_file`;
  - `search_workspace`;
  - `list_files`;
  - `run_command`;
  - `write_patch`;
  - `fetch_context_manifest`.
- Add a tool-call approval UI in VS Code.
- Route tool-call decisions through the Governance Shell.
- Enforce `agent.config.yaml` permissions before execution.
- Use command allow-lists for terminal operations.
- Record every approved, denied, and failed tool call in audit.
- Keep the current consecutive tool-call cap.

Acceptance criteria:

- Agents cannot run commands directly from the Bridge.
- Agents cannot write directly to the workspace.
- Every tool call has an approval state and audit event.
- Terminal commands show command, working directory, risk level, and policy reason before execution.

## Phase 4: Patch And Edit Loop

Goal: make patch production feel natural while preserving staged patch governance.

Deliverables:

- Show proposed patches inside the Bridge panel as a session artifact.
- Keep native VS Code diff review.
- Add per-file decisions:
  - apply;
  - reject;
  - mark as manually applied;
  - request revision.
- Add a patch revision path where user feedback is sent back into the same governed session.
- Add rollback hints for applied patches where possible.
- Record patch content hashes, decisions, and reviewer notes.

Acceptance criteria:

- The user can review and decide per file, not only per patch envelope.
- Raw patch content is still fetched from the Governance Shell patch buffer.
- The audit trail records the decision and the reason.
- Unsafe paths and secret-containing patches are blocked before display.

## Phase 5: Runtime Adapter Evaluation

Goal: decide whether to build a local runtime adapter, adapt an existing runtime, or support both.

Options:

1. **Native Bridge Runtime**
   - The Bridge and Orchestrator implement the tool loop directly.
   - Best for governance clarity.
   - Highest risk of becoming a CLine clone.

2. **OpenCode / ACP Adapter**
   - Use ACP-style session protocol and keep the runtime behind the Governance Shell.
   - Best fit with current repo direction.
   - Needs proof that runtime events, tools, and patches can be fully governed.

3. **External IDE Runtime Adapter**
   - Treat CLine-like tools as runtime candidates.
   - Best for borrowing mature ergonomics.
   - Risk: extension/runtime APIs may not expose enough control for governance.

Recommendation:

- Build enough native Bridge UX to make the local workflow usable.
- Continue evaluating ACP/OpenCode as the first runtime adapter path.
- Treat CLine-style runtimes as references or future adapter candidates, not dependencies.

Acceptance criteria:

- A runtime cannot bypass the model proxy.
- A runtime cannot bypass patch buffering.
- A runtime cannot bypass tool-call policy.
- A runtime emits enough events for audit, cost, approvals, and evidence.

## Phase 6: Multi-Turn Session State

Goal: support ongoing coding sessions rather than one-shot prompts.

Deliverables:

- Add session history in the Bridge panel.
- Support follow-up user messages to the same session.
- Store bounded conversation summaries in session state.
- Record context additions as separate manifests.
- Let users resume recent sessions from the current workspace.
- Add session abort/cancel from the Bridge.

Acceptance criteria:

- Follow-up messages preserve the same session ID.
- The user can see what context has already been attached.
- The Governance Shell audit chain links session events in order.
- Resume does not require raw prompt history to be stored in audit.

## Phase 7: Use-Case And Workflow Binding

Goal: connect the IDE experience to governance outcomes.

Deliverables:

- Add use-case and workflow pickers in the Bridge.
- Allow default bindings per repository.
- Show classification and risk level before invocation.
- Attach work item IDs where available.
- Record expected benefit, evidence requirements, and maturity output category.

Acceptance criteria:

- A session can be traced back to a use case or workflow.
- The user can still run an exploratory session without heavy setup.
- Governance reporting can distinguish test generation, code review, documentation, security scan, and refactor workflows.

## Phase 8: Observability And Evidence

Goal: make the agent plane observable without becoming the reporting UI.

Deliverables:

- Show session timeline in the Bridge:
  - created;
  - routed;
  - confirmed;
  - tool requested;
  - tool approved/denied;
  - patch proposed;
  - patch decided;
  - completed/blocked.
- Add cost and token usage display.
- Show policy decisions and reasons.
- Add a local audit link from every session.
- Export evidence records through the existing governance APIs.

Acceptance criteria:

- The Bridge can explain what happened in a session.
- The Governance Shell remains the source of truth for audit and evidence.
- No raw provider secrets, raw prompts, or raw patch content are exposed in audit lookup.

## Implementation Order

Recommended order:

1. Better Bridge panel.
2. Context manifest UX.
3. Governed tool calls.
4. Patch and edit loop.
5. Multi-turn session state.
6. Runtime adapter evaluation.
7. Use-case and workflow binding.
8. Observability and evidence polish.

This order keeps the system usable early while protecting the governance boundary.

## Technical Decisions To Make Next

Open decisions:

- Should the Bridge panel be a Webview sidebar, a custom editor panel, or both?
- Should context manifests store raw context locally, hashes only, or policy-dependent snapshots?
- Should terminal commands run through the Bridge host process, a local worker, or a container-per-session runtime?
- Should the first runtime adapter target ACP/OpenCode, or should the native Bridge loop be built first?
- What is the minimum policy envelope required before allowing command execution?
- How should the Bridge discover and bind use cases without making first-run setup painful?

Recommended answers for the next build slice:

- Use a Webview sidebar for the Bridge panel.
- Store context hashes and metadata by default; raw context should be transient unless policy allows storage.
- Keep terminal command execution disabled until the governed tool-call approval path exists.
- Build native Bridge UX first, then evaluate ACP/OpenCode as the first runtime adapter.
- Require command allow-list, classification check, secret scan, and explicit human approval before any command execution.
- Start use-case binding as optional metadata, not a required first-run gate.

## Risks

Main risks:

- The Bridge becomes a half-built CLine clone and distracts from governance.
- Runtime adapters do not expose enough control to enforce policy.
- Workspace context grows until it becomes uncontrolled memory.
- Tool execution becomes convenient before it becomes governable.
- The UX becomes too heavy for quick local experimentation.

Mitigations:

- Keep the Governance Shell as the authority boundary.
- Keep context explicit, bounded, and inspectable.
- Keep raw writes staged in patch buffers.
- Keep terminal commands disabled until policy and approval are wired.
- Keep the first Bridge panel small and workflow-focused.

## Definition Of Done For "Closer To CLine"

This POC is meaningfully closer to a CLine-style experience when:

- It can be used from any VS Code project folder.
- It has a persistent chat-style Bridge panel.
- It can attach deliberate workspace context.
- It supports multi-turn sessions.
- It can request governed file reads/searches.
- It can request governed terminal commands.
- It streams useful agent output.
- It proposes patches through the Governance Shell.
- It records decisions, policy outcomes, costs, and evidence.
- It still cannot bypass governance by calling providers, tools, or workspace writes directly.

That is the line: richer agent-plane ergonomics, not runtime ownership.
