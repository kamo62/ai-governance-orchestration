param(
  [ValidateSet("project", "global")]
  [string]$Scope = "global",
  [string]$EnvFile = "",
  [string]$Path = "",
  [switch]$NoRefreshJob
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
    if ($name) { [Environment]::SetEnvironmentVariable($name, $value, "Process") }
  }
}

if ($EnvFile) { Import-AiOrchEnvFile -Path $EnvFile }
if ($env:AI_ORCH_OPENCODE_SCOPE -and -not $PSBoundParameters.ContainsKey("Scope")) { $Scope = $env:AI_ORCH_OPENCODE_SCOPE }

Push-Location $Root
try {
  $ArgsList = @("run", "./cmd/ai-orch", "developer", "enroll", "--client", "opencode", "--scope", $Scope)
  if ($Path) { $ArgsList += @("--path", $Path) }
  if ($NoRefreshJob) { $ArgsList += @("--install-refresh-job=false") }
  & go @ArgsList
  exit $LASTEXITCODE
}
finally {
  Pop-Location
}
