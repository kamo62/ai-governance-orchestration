param(
  [ValidateSet("project", "global")]
  [string]$Scope = "global",
  [string]$EnvFile = "",
  [string]$KeyFile = ""
)

$ErrorActionPreference = "Stop"

$Root = Resolve-Path (Join-Path $PSScriptRoot "..")

function Import-AiOrchEnvFile {
  param([string]$Path)
  if (-not (Test-Path $Path)) { return }
  Get-Content $Path | ForEach-Object {
    $line = $_.Trim()
    if (-not $line -or $line.StartsWith("#") -or -not $line.Contains("=")) { return }
    $parts = $line.Split("=", 2)
    $name = $parts[0].Trim()
    $value = $parts[1].Trim().Trim('"').Trim("'")
    if ($name) {
      [Environment]::SetEnvironmentVariable($name, $value, "Process")
    }
  }
}

function Get-ActorSubject {
  if ($env:AI_ORCH_ACTOR_SUBJECT) { return $env:AI_ORCH_ACTOR_SUBJECT }
  if ($env:USERNAME) { return $env:USERNAME }
  if ($env:USER) { return $env:USER }
  return "local-dev"
}

if ($EnvFile) {
  Import-AiOrchEnvFile -Path $EnvFile
}

if (-not $env:AI_ORCH_DEV_TOKEN) { $env:AI_ORCH_DEV_TOKEN = "local-dev" }
if (-not $env:AI_ORCH_RUNTIME_TOKEN) { $env:AI_ORCH_RUNTIME_TOKEN = "local-runtime-token" }
if (-not $env:AI_ORCH_ACTOR_SUBJECT) { $env:AI_ORCH_ACTOR_SUBJECT = Get-ActorSubject }
if (-not $env:AI_ORCH_GOVERNANCE_URL) { $env:AI_ORCH_GOVERNANCE_URL = "http://127.0.0.1:18080" }
if (-not $env:AI_ORCH_MODEL_GATEWAY_URL) { $env:AI_ORCH_MODEL_GATEWAY_URL = "http://127.0.0.1:18082" }

if (-not $KeyFile) {
  $KeyFile = Join-Path $HOME ".ai-orch\copilot-token.key"
}

if (-not $env:AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY) {
  $keyDir = Split-Path $KeyFile -Parent
  New-Item -ItemType Directory -Force -Path $keyDir | Out-Null
  if (-not (Test-Path $KeyFile)) {
    $bytes = New-Object byte[] 32
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
    [Convert]::ToBase64String($bytes) | Set-Content -NoNewline -Path $KeyFile
  }
  $env:AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY = (Get-Content -Raw $KeyFile).Trim()
  Write-Host "Using local Copilot encryption key file: $KeyFile"
}

Push-Location $Root
try {
  Write-Host "Checking Governance Shell at $env:AI_ORCH_GOVERNANCE_URL"
  & go run ./cmd/ai-orch copilot status

  & go run ./cmd/ai-orch copilot refresh *> $null
  if ($LASTEXITCODE -ne 0) {
    Write-Host "Starting GitHub Copilot device login for actor $env:AI_ORCH_ACTOR_SUBJECT"
    & go run ./cmd/ai-orch copilot login
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
  }

  Write-Host "Verifying Copilot models through ai-orch"
  & go run ./cmd/ai-orch copilot models *> $null
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

  Write-Host "Installing OpenCode ai-orch provider config ($Scope)"
  $argsList = @("run", "./cmd/ai-orch", "opencode", "install-config", "--scope", $Scope, "--force", "--runtime-token", $env:AI_ORCH_RUNTIME_TOKEN, "--actor-subject", $env:AI_ORCH_ACTOR_SUBJECT)
  & go @argsList
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

  Write-Host ""
  Write-Host "Enrollment complete."
  Write-Host "Direct OpenCode/T3 launches can now use the installed ai-orch provider without manually setting session IDs or gateway tokens."
  Write-Host ""
  Write-Host "No Copilot credential was written to OpenCode config."
}
finally {
  Pop-Location
}
