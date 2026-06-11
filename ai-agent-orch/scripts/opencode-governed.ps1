param(
  [string]$EnvFile = "",
  [Parameter(ValueFromRemainingArguments = $true)]
  [string[]]$OpenCodeArgs
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

if ($EnvFile) {
  Import-AiOrchEnvFile -Path $EnvFile
}

if (-not $env:AI_ORCH_DEV_TOKEN) { $env:AI_ORCH_DEV_TOKEN = "local-dev" }
if (-not $env:AI_ORCH_RUNTIME_TOKEN) { $env:AI_ORCH_RUNTIME_TOKEN = "local-runtime-token" }
if (-not $env:AI_ORCH_ACTOR_SUBJECT) {
  if ($env:USERNAME) { $env:AI_ORCH_ACTOR_SUBJECT = $env:USERNAME }
  elseif ($env:USER) { $env:AI_ORCH_ACTOR_SUBJECT = $env:USER }
  else { $env:AI_ORCH_ACTOR_SUBJECT = "local-dev" }
}

Push-Location $Root
try {
  & go run ./cmd/ai-orch opencode -- @OpenCodeArgs
  exit $LASTEXITCODE
}
finally {
  Pop-Location
}
