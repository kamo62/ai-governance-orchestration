# Copilot And GitHub Models Integration Plan

This document records the tested local findings and the recommended path for adding GitHub-backed model access to ai-orch.

The practical goal is:

- developers keep using OpenCode or similar local tools;
- OpenCode points at ai-orch as the model endpoint;
- ai-orch keeps the governed session, model-call audit, policy labels, cost records and tool evidence;
- model access can use either the developer's GitHub Copilot entitlement or the official GitHub Models API.

The preferred beta path is Option 2, per-user Copilot proxying through ai-orch. It matches the current licensing reality: developers already have GitHub Copilot seats. It also keeps ai-orch in the request path.

## Current Tested State

Tests were run locally on 2026-06-08.

OpenCode is installed locally:

```sh
opencode --version
```

Result:

```text
1.16.2
```

OpenCode has a GitHub Copilot OAuth credential configured:

```sh
opencode providers list
```

Result included:

```text
GitHub Copilot oauth
```

OpenCode can list Copilot models:

```sh
opencode models github-copilot
```

Result:

```text
github-copilot/claude-haiku-4.5
github-copilot/claude-opus-4.8
github-copilot/claude-opus-4.8-fast
github-copilot/claude-sonnet-4.5
github-copilot/claude-sonnet-4.6
github-copilot/gemini-2.5-pro
github-copilot/gemini-3-flash-preview
github-copilot/gemini-3.1-pro-preview
github-copilot/gemini-3.5-flash
github-copilot/gpt-5-mini
github-copilot/gpt-5.3-codex
github-copilot/gpt-5.4-mini
github-copilot/gpt-5.5
```

OpenCode can make a real Copilot inference call:

```sh
opencode run --model github-copilot/gpt-5-mini --format json "Reply with exactly: copilot-ok"
```

The exported session confirmed:

```text
providerID: github-copilot
modelID: gpt-5-mini
response: copilot-ok
cost: 0.002192
tokens: input 7832, output 77, cache read 3200
```

GitHub Models personal inference also works for the current GitHub account:

```sh
TOKEN=$(gh auth token)

curl -i -sS \
  -X POST "https://models.github.ai/inference/chat/completions" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  -d '{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"Reply exactly: github-models-ok"}],"max_tokens":16}'
```

Result:

```text
HTTP 200
github-models-ok
```

Org-attributed GitHub Models was tested against the visible org `kamomash`:

```sh
TOKEN=$(gh auth token)

curl -i -sS \
  -X POST "https://models.github.ai/orgs/kamomash/inference/chat/completions" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  -d '{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"Reply exactly: github-models-org-ok"}],"max_tokens":16}'
```

Result:

```text
HTTP 403
```

Interpretation:

- personal GitHub Models inference is enabled for the current GitHub account;
- org-attributed inference is not enabled, blocked by policy, or not allowed for the current token/org;
- Copilot per-user inference works through OpenCode today.

## Recommendation

Lead with Option 2 for beta: `copilot-user` backend behind ai-orch.

Keep Option 1 available as the official provider path: `github-models` backend behind ai-orch.

The reason is commercial and practical. The organisation already appears to have per-user Copilot licensing, and OpenCode already proves those user entitlements can reach Copilot models. A Copilot proxy backend lets ai-orch preserve the governed endpoint while still using the user's existing entitlement.

The risk is API stability. OpenCode is using Copilot endpoints that are not documented as a stable public inference API. The GitHub OAuth device flow is public. The Copilot model and inference endpoints are private or compatibility endpoints. This has to be labelled as experimental.

## Option 2: Per-User Copilot Proxy Through ai-orch

### Target Flow

```text
Developer
  -> runs ai-orch copilot login
  -> authenticates with GitHub device flow
  -> ai-orch stores encrypted user Copilot credential

OpenCode
  -> calls ai-orch model compatibility gateway
  -> sends AI_ORCH_RUNTIME_TOKEN and X-AI-Orch-Session-ID

ai-orch
  -> validates governed session
  -> resolves actor and model alias
  -> looks up the actor's Copilot credential
  -> injects Copilot-specific headers
  -> calls api.githubcopilot.com
  -> records audit, usage, model, cost and evidence
  -> returns OpenAI-compatible response to OpenCode
```

The important design point is that OpenCode never calls Copilot directly in governed mode. OpenCode only calls ai-orch.

### Why This Preserves The Product

Direct OpenCode to Copilot works, but it bypasses ai-orch model governance.

The proxy flow keeps these controls alive:

- session creation before model generation;
- model alias routing;
- classification and risk metadata;
- prompt and response hashing;
- token and cost reporting;
- backend attribution;
- kill switch and future policy checks;
- MCP and patch evidence correlation;
- per-user attribution.

### OpenCode Configuration For Governed Copilot

OpenCode should still use the custom ai-orch provider.

Example local or managed OpenCode config:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "ai-orch": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "AI Orch Governed Router",
      "options": {
        "baseURL": "http://127.0.0.1:18082/v1",
        "apiKey": "{env:AI_ORCH_RUNTIME_TOKEN}",
        "headers": {
          "X-AI-Orch-Session-ID": "{env:AI_ORCH_SESSION_ID}"
        }
      },
      "models": {
        "copilot-gpt-5-mini": {
          "name": "Governed Copilot GPT-5 Mini"
        },
        "copilot-gpt-5.3-codex": {
          "name": "Governed Copilot GPT-5.3 Codex"
        },
        "copilot-gpt-5.5": {
          "name": "Governed Copilot GPT-5.5"
        }
      }
    }
  },
  "enabled_providers": ["ai-orch"],
  "model": "ai-orch/copilot-gpt-5-mini",
  "small_model": "ai-orch/copilot-gpt-5-mini"
}
```

For enterprise-managed OpenCode, use OpenCode managed settings so developers cannot override the provider list casually:

- macOS: `/Library/Application Support/opencode/opencode.json`
- Linux: `/etc/opencode/opencode.json`
- Windows: `%ProgramData%\opencode\opencode.json`
- macOS MDM preference domain: `ai.opencode.managed`

Managed settings are useful for beta because they keep the default model path pointed at ai-orch while still letting developers use their local repo and local OpenCode install.

### Required ai-orch Components

#### 1. New Backend Type

Add a backend value:

```text
AI_ORCH_MODEL_BACKEND=copilot-user
```

or add it as a routeable provider behind the existing model backend abstraction:

```go
const BackendCopilotUser = "copilot-user"
```

Recommended model-registry aliases:

```yaml
models:
  - alias: copilot-gpt-5-mini
    provider: copilot-user
    model: gpt-5-mini
    purpose: low-cost governed Copilot coding path
    allowed_classifications: [public, internal]

  - alias: copilot-gpt-5.3-codex
    provider: copilot-user
    model: gpt-5.3-codex
    purpose: high-quality governed Copilot coding path
    allowed_classifications: [public, internal]

  - alias: copilot-gpt-5.5
    provider: copilot-user
    model: gpt-5.5
    purpose: high-reasoning governed Copilot path
    allowed_classifications: [public, internal]
```

The router should treat these as normal aliases. The backend implementation decides how to call Copilot.

#### 2. Copilot OAuth Login Command

Add a CLI command:

```sh
ai-orch copilot login
```

Expected behavior:

1. Start GitHub OAuth device flow.
2. Print the device login URL and code.
3. Poll GitHub until the user authorizes.
4. Store the returned token encrypted.
5. Fetch Copilot `/models` to prove entitlement.
6. Store user identity metadata.

Useful commands:

```sh
ai-orch copilot status
ai-orch copilot models
ai-orch copilot logout
ai-orch copilot refresh
```

The command should not read OpenCode's auth files. OpenCode's implementation is a reference for protocol behavior, not a credential source.

#### 3. OAuth Device Flow

OpenCode uses GitHub device-code auth.

For GitHub.com:

```text
POST https://github.com/login/device/code
POST https://github.com/login/oauth/access_token
```

For GitHub Enterprise, OpenCode asks for the enterprise URL and derives:

```text
https://<enterprise-domain>/login/device/code
https://<enterprise-domain>/login/oauth/access_token
https://copilot-api.<enterprise-domain>
```

The OpenCode source uses the OAuth scope:

```text
read:user
```

The POC can start with GitHub.com only. Enterprise URL support can follow after the GitHub.com path works.

#### 4. User Identity Binding

After OAuth succeeds, call GitHub user API:

```text
GET https://api.github.com/user
```

Store at least:

```text
github_login
github_user_id
github_profile_url
token_subject
created_at
last_verified_at
copilot_base_url
```

Bind that record to the ai-orch actor identity.

For local beta, the actor can be the existing local identity header or dev-token subject. For team beta, bind to OIDC subject.

Do not let a request choose an arbitrary GitHub identity by header. The lookup must derive identity from the authenticated ai-orch actor.

#### 5. Token Storage

The POC can use SQLite, but token material must be encrypted before persistence.

Minimum POC table:

```sql
CREATE TABLE copilot_user_tokens (
  actor_subject TEXT PRIMARY KEY,
  github_login TEXT NOT NULL,
  github_user_id TEXT NOT NULL,
  copilot_base_url TEXT NOT NULL,
  access_token_ciphertext BLOB NOT NULL,
  token_fingerprint TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_verified_at TEXT,
  revoked_at TEXT
);
```

Encryption key options for beta:

- macOS Keychain for local developer machine storage;
- `AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY` for local Compose;
- cloud secret manager or Key Vault for shared deployments.

Production should not rely on a raw environment encryption key for long-lived shared services.

#### 6. Session Binding

Model generation should require:

```text
Authorization: Bearer <ai-orch runtime token>
X-AI-Orch-Session-ID: <session id>
```

The gateway must then:

1. Load the durable session.
2. Resolve the session actor subject.
3. Load the Copilot token for that actor.
4. Confirm the token has not been revoked.
5. Route only allowed Copilot aliases for that session classification.
6. Call Copilot.

This avoids a runtime client using one user's session with another user's Copilot token.

For beta, if the current runtime session record does not carry a stable actor subject, add it before building this backend.

#### 7. Copilot Model Discovery

OpenCode proves model discovery with:

```text
GET https://api.githubcopilot.com/models
```

Headers used by OpenCode include:

```text
Authorization: Bearer <github oauth token>
User-Agent: opencode/<version>
X-GitHub-Api-Version: 2026-06-01
```

For ai-orch, use:

```text
Authorization: Bearer <github oauth token>
User-Agent: ai-orch/<version>
X-GitHub-Api-Version: 2026-06-01
```

Cache the result per user for a short period, such as 5 minutes. Do not hard-code availability from one developer's account.

The model list contains important metadata:

- model id;
- supported endpoints;
- context limits;
- output limits;
- picker enabled flag;
- tool support;
- image support;
- pricing and AI unit information;
- model family;
- release date;
- provider-specific headers for variants.

Store a normalized copy for reporting. Keep the raw response hash for troubleshooting without storing full raw content if that becomes sensitive.

#### 8. Copilot Inference Headers

OpenCode injects these headers for Copilot calls:

```text
Authorization: Bearer <github oauth token>
User-Agent: opencode/<version>
X-GitHub-Api-Version: 2026-06-01
Openai-Intent: conversation-edits
x-initiator: user
```

ai-orch should use:

```text
Authorization: Bearer <github oauth token>
User-Agent: ai-orch/<version>
X-GitHub-Api-Version: 2026-06-01
Openai-Intent: conversation-edits
x-initiator: user
```

Optional headers based on request/model:

```text
Copilot-Vision-Request: true
anthropic-beta: interleaved-thinking-2025-05-14
anthropic-beta: fast-mode-2026-02-01
```

Do not blindly send all optional headers. Use the model metadata from `/models` and the selected alias config.

#### 9. Copilot Endpoints

OpenCode uses these endpoint shapes:

```text
https://api.githubcopilot.com/models
https://api.githubcopilot.com/chat/completions
https://api.githubcopilot.com/responses
https://api.githubcopilot.com/v1/messages
```

Selection logic:

- OpenAI-compatible models use `/chat/completions` or `/responses`.
- GPT-5 class models often use Responses API in OpenCode's transform layer.
- Some Claude models advertise `/v1/messages` and are handled through the Anthropic SDK shape.

For the first ai-orch POC, choose one simple model and one endpoint:

```text
model: gpt-5-mini
endpoint: /chat/completions
```

After the first call works, add:

```text
gpt-5.3-codex via /responses
claude-sonnet-4.5 via /v1/messages
```

#### 10. Request Mapping For First POC

ai-orch already exposes OpenAI-compatible `/v1/chat/completions`.

For `copilot-user`, forward the request in OpenAI chat shape:

```json
{
  "model": "gpt-5-mini",
  "messages": [
    {"role": "user", "content": "Reply with exactly: copilot-ok"}
  ],
  "max_tokens": 16,
  "stream": false
}
```

Return the ai-orch alias to the caller:

```json
{
  "model": "copilot-gpt-5-mini"
}
```

Audit the resolved backend fields separately:

```text
model_backend: copilot-user
provider: github-copilot
model_alias: copilot-gpt-5-mini
model_resolved: gpt-5-mini
github_login: <login>
```

#### 11. Usage And Cost Mapping

OpenCode exported the Copilot session with:

```text
cost: 0.002192
input tokens: 7832
output tokens: 77
cache read tokens: 3200
```

OpenCode source also handles Copilot-specific usage fields such as:

```text
copilot_usage.total_nano_aiu
```

ai-orch should capture:

- standard token usage if returned;
- cache read/write tokens if returned;
- Copilot AI unit fields if returned;
- provider-reported cost if returned;
- estimated cost if only token counts and model pricing are available;
- cost source: `provider_reported`, `copilot_aiu`, `estimated`, or `unknown`.

For beta, it is acceptable to report cost as provider-reported when Copilot returns usable cost/AIU information, and unknown when it does not.

#### 12. Audit Events

Add dedicated event fields or metadata for Copilot calls:

```text
event_type: model.gateway_call
trust_level: gateway_enforced
enforcement_mode: gateway
model_backend: copilot-user
provider: github-copilot
model_alias: copilot-gpt-5-mini
model_resolved: gpt-5-mini
actor_subject: <ai-orch actor>
github_login: <github login>
request_hash: <sha256>
response_hash: <sha256>
prompt_tokens: <number>
completion_tokens: <number>
cache_read_tokens: <number>
estimated_cost_usd: <number>
cost_source: <source>
```

Do not store raw prompts or raw responses unless the session policy explicitly allows it.

### POC Milestones For Option 2

#### Milestone 0: Commit Current Branch

Commit the current branch before starting this POC. This branch already contains broad beta changes. The Copilot POC should start from a clean branch so failures are easy to isolate.

Suggested branch:

```sh
git checkout -b spike/copilot-user-backend
```

#### Milestone 1: CLI Login And Model List

Build:

```sh
ai-orch copilot login
ai-orch copilot status
ai-orch copilot models
```

Acceptance criteria:

- device-code login completes;
- GitHub username is shown;
- `/models` returns at least `gpt-5-mini`;
- token is stored encrypted;
- logout revokes or removes local token.

#### Milestone 2: Direct Backend Smoke

Build a small internal smoke command:

```sh
ai-orch copilot smoke --model gpt-5-mini --prompt "Reply exactly: copilot-ok"
```

Acceptance criteria:

- response equals `copilot-ok`;
- usage is printed if present;
- request does not use OpenCode auth files;
- all Copilot auth comes from ai-orch token storage.

#### Milestone 3: Model Gateway Integration

Add `copilot-user` to the model backend path and route a governed alias through it.

Example:

```sh
curl -H "Authorization: Bearer local-runtime-token" \
  -H "X-AI-Orch-Session-ID: <session_id>" \
  -H "Content-Type: application/json" \
  -d '{"model":"copilot-gpt-5-mini","messages":[{"role":"user","content":"Reply with exactly: model-gateway-copilot-ok"}],"max_tokens":16}' \
  http://127.0.0.1:18082/v1/chat/completions
```

Acceptance criteria:

- response returns through ai-orch `/v1/chat/completions`;
- audit event records `model_backend=copilot-user`;
- audit event records the Copilot resolved model;
- OpenCode is not configured to call Copilot directly.

#### Milestone 4: OpenCode E2E

Generate an OpenCode config that points only at ai-orch:

```sh
ai-orch opencode install-config --provider ai-orch --model copilot-gpt-5-mini
```

Run:

```sh
AI_ORCH_SESSION_ID=<session_id> \
AI_ORCH_RUNTIME_TOKEN=local-runtime-token \
opencode run --model ai-orch/copilot-gpt-5-mini "Reply with exactly: opencode-ai-orch-copilot-ok"
```

Acceptance criteria:

- OpenCode response equals `opencode-ai-orch-copilot-ok`;
- OpenCode session model is `ai-orch/copilot-gpt-5-mini`;
- ai-orch audit records the Copilot backend;
- Copilot usage is attributed to the logged-in GitHub user.

#### Milestone 5: Responses API And Streaming

Add:

- `/responses` support for GPT-5 class models;
- streaming support;
- raw chunk usage extraction;
- encrypted reasoning passthrough where required by the model family.

Acceptance criteria:

- `gpt-5.3-codex` works through ai-orch;
- `gpt-5.5` works through ai-orch;
- stream completion usage is captured;
- tool-calling clients do not break.

#### Milestone 6: Claude/Gemini Models

Add support for model records whose `supported_endpoints` route through non-chat-completions shapes.

Acceptance criteria:

- `claude-sonnet-4.5` works if entitlement allows it;
- `gemini-2.5-pro` works if entitlement allows it;
- model-specific headers from Copilot discovery are respected.

### POC Risks

The main risk is not implementation difficulty. The main risk is API contract stability.

Known risks:

- `api.githubcopilot.com` endpoints are not documented as a stable public inference API;
- model list schema can change;
- required headers can change;
- GitHub can block non-official clients;
- Copilot entitlement and billing semantics may be user-plan-specific;
- some models require Pro+ or enterprise policy enablement;
- encrypted reasoning and provider-native chunks may require exact passthrough behavior;
- enterprise Copilot may have a different base URL and policy controls.

Mitigation:

- label backend as experimental;
- keep it behind explicit config: `AI_ORCH_EXPERIMENTAL_COPILOT_USER=true`;
- keep GitHub Models and other providers available as fallback;
- discover models dynamically;
- avoid hard-coding model IDs beyond registry aliases;
- store minimal raw provider data;
- write contract tests from captured non-sensitive fixtures;
- add a kill switch for this backend.

## Option 1: Official GitHub Models Backend

GitHub Models is the official server-side path.

It should still be implemented because it is a cleaner long-term provider backend than Copilot private endpoints.

### Personal Inference Endpoint

Tested working:

```text
POST https://models.github.ai/inference/chat/completions
```

Example:

```sh
TOKEN=$(gh auth token)

curl -i -sS \
  -X POST "https://models.github.ai/inference/chat/completions" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  -d '{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"Reply exactly: github-models-ok"}],"max_tokens":16}'
```

Expected success:

```text
HTTP 200
```

### Org-Attributed Endpoint

Tested but blocked for `kamomash`:

```text
POST https://models.github.ai/orgs/{org}/inference/chat/completions
```

Example:

```sh
ORG="your-org"
TOKEN=$(gh auth token)

curl -i -sS \
  -X POST "https://models.github.ai/orgs/${ORG}/inference/chat/completions" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  -d '{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"Reply exactly: github-models-org-ok"}],"max_tokens":16}'
```

Result meanings:

```text
200: org-attributed GitHub Models works
401: token invalid or missing required permission
403: org policy blocks it, Models not enabled, or token lacks access
404: wrong org or feature not available for that org/account
422: model blocked or request shape unsupported
```

### Required ai-orch Backend

Add backend:

```text
AI_ORCH_MODEL_BACKEND=github-models
```

or make it a provider under the existing backend abstraction:

```go
const BackendGitHubModels = "github-models"
```

Config:

```text
AI_ORCH_GITHUB_MODELS_BASE_URL=https://models.github.ai
AI_ORCH_GITHUB_MODELS_TOKEN=<token or token provider>
AI_ORCH_GITHUB_MODELS_ORG=<optional org>
```

Model registry examples:

```yaml
models:
  - alias: github-gpt-4.1
    provider: github-models
    model: openai/gpt-4.1
    purpose: official GitHub Models GPT-4.1 route
    allowed_classifications: [public, internal]

  - alias: github-gpt-4o
    provider: github-models
    model: openai/gpt-4o
    purpose: official GitHub Models GPT-4o route
    allowed_classifications: [public, internal]
```

Routing behavior:

- if `AI_ORCH_GITHUB_MODELS_ORG` is set, call `/orgs/{org}/inference/chat/completions`;
- otherwise call `/inference/chat/completions`;
- forward OpenAI chat request fields supported by GitHub Models;
- normalize usage fields into ai-orch audit.

### Why This Is Not Enough By Itself

GitHub Models personal access works, but this does not automatically mean enterprise split billing is solved.

For enterprise use, org-attributed inference is the important path. The local test returned `403` for the visible org, so someone with org or enterprise admin rights needs to check:

- whether GitHub Models is enabled for the org;
- whether model usage is allowed by policy;
- whether BYOK is enabled if the company wants its own OpenAI/AzureAI keys;
- whether the token used has `models: read` permission;
- whether GitHub App tokens can be used for the intended service flow.

## Decision Matrix

| Requirement | Copilot User Proxy | GitHub Models Backend | Direct OpenCode Copilot |
| --- | --- | --- | --- |
| Uses existing per-user Copilot seats | Yes | No, separate GitHub Models access | Yes |
| Keeps ai-orch as model endpoint | Yes | Yes | No |
| Gateway-enforced audit | Yes | Yes | No |
| Official public inference API | No | Yes | No |
| User identity available | Yes | Depends on token flow | Yes in OpenCode, not enforced by ai-orch |
| Org billing/attribution | Weak or plan-dependent | Best if org endpoint enabled | Copilot seat billing only |
| Implementation risk | Medium to high | Low to medium | Low |
| Product fit | Strong beta fit | Strong official backend | Weak governance fit |

Recommended beta posture:

```text
Default governed mode: OpenCode -> ai-orch -> copilot-user
Official fallback: OpenCode -> ai-orch -> github-models
Ungoverned escape hatch: OpenCode -> github-copilot directly, labelled self_reported
```

## Branch And Commit Strategy

Commit the current branch before starting the production track or Copilot POC.

Reason:

- the current branch already contains broad beta functionality;
- Copilot proxy work will touch auth, model backends, model gateway, CLI, storage and docs;
- production hardening will touch almost every boundary;
- mixing those into the current branch will make review and rollback harder.

Suggested sequence:

```sh
git status
git diff --stat
git add -A
git commit -m "Advance governed beta orchestration"
git checkout -b spike/copilot-user-backend
```

After the Copilot POC branch proves the end-to-end flow, start a separate production hardening branch:

```sh
git checkout main
git pull
git checkout -b harden/prod-readiness
```

Keep the production branch focused on hardening:

- identity;
- session ownership;
- token binding;
- audit durability;
- deployment assets;
- metrics;
- CI and supply-chain checks.

Keep the Copilot POC branch focused on model access:

- OAuth login;
- token storage;
- Copilot model discovery;
- Copilot backend calls;
- OpenCode E2E;
- audit and usage mapping.

## Immediate Next Steps

1. Keep OpenCode configured only against ai-orch in governed mode.
2. Keep the gateway contract aligned to OpenCode `v1.16.2` request shapes.
3. Preserve OpenAI-compatible chat and Responses payload fields through ai-orch before adding more providers.
4. Prove Bifrost routes Bedrock and Foundry-compatible payloads without dropping tool-call fields.
5. Create `spike/copilot-user-backend`.
6. Implement `ai-orch copilot login` and `ai-orch copilot models`.
7. Prove `gpt-5-mini` through a direct ai-orch Copilot smoke command.
8. Add `copilot-gpt-5-mini` to `models/registry.yaml` using `model_id`.
9. Route `/v1/chat/completions` through `copilot-user`.
10. Add `/v1/responses` support for GPT-5 class Copilot models after the chat path works.
11. Run OpenCode against ai-orch, not against `github-copilot` directly.
12. Record audit evidence showing `gateway_enforced` Copilot model calls.

Current ACP posture: OpenCode ACP is the direct runtime lane. It should not receive MCP servers through `session/new`; MCP remains a separate gateway route. ACP file writes and workspace diffs are recorded as patch evidence, while model calls remain gateway-enforced through ai-orch.

Implemented local commands:

```sh
ai-orch copilot login
ai-orch copilot status
ai-orch copilot models
ai-orch copilot smoke --model gpt-5-mini --prompt "Reply exactly: copilot-smoke-ok"
ai-orch copilot logout
```

Required local storage setting:

```sh
AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY=<32+ byte secret>
```

Gateway backend setting:

```sh
AI_ORCH_MODEL_BACKEND=copilot-user
AI_ORCH_COPILOT_TOKEN_DB=~/.ai-orch/copilot-tokens.db
```

GitHub Models remains a lower-priority optional backend for environments that need an official GitHub inference API. The beta priority is Copilot user entitlement plus enterprise Bedrock and Foundry routes through the governed ai-orch gateway.

## Open Questions

- Which GitHub organisation or enterprise owns the Copilot seats?
- Is GitHub Enterprise Server involved, or only GitHub.com?
- Are Copilot private endpoint calls acceptable for an internal beta?
- Should ai-orch ask each user to run `ai-orch copilot login`, or should login happen through the Governance UI?
- Should Copilot tokens live only on developer machines for team-local deployments?
- For shared deployments, which secrets system should hold the token encryption key?
- Which model should be the default beta alias: `gpt-5-mini`, `gpt-5.3-codex`, or `gpt-5.5`?
- Should direct OpenCode Copilot be blocked by managed config, or allowed with `self_reported` evidence?
