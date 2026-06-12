# Security Policy

This project is a proof of concept for a governance control plane. It is not yet intended for production deployments, but security reports are taken seriously because the project's whole purpose is enforcing boundaries around AI-assisted engineering work.

## Reporting a vulnerability

Please report vulnerabilities privately through [GitHub Security Advisories](https://github.com/kamo62/ai-governance-orchestration/security/advisories/new). Do not open public issues for security problems.

Include what you can: affected component (Governance Shell, model gateway, MCP gateway, CLI, bridge), reproduction steps, and impact. You can expect an acknowledgement within a week.

## Scope notes

- The local beta runs with shared dev tokens and header-asserted identity by design. Reports that local-dev defaults are unsafe for production are appreciated but known; `AI_ORCH_ENV=production` refuses to start with those defaults.
- Bypasses of the enforcement paths are always in scope: policy checks, classification ceilings, secret scanning, kill switches, cost caps, session ownership, audit hash chains, and the MCP/tool authorization boundary.
- Provider keys and user tokens must never reach runtimes or logs. Any leak path is in scope.

## Supported versions

Only the latest tagged beta release and `main` receive fixes.
