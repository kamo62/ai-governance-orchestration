param(
  [ValidateSet("project", "global")]
  [string]$Scope = "global",
  [string]$Path = "",
  [switch]$InstallRefreshJob
)

$ErrorActionPreference = "Stop"
if ($env:AI_ORCH_OPENCODE_SCOPE -and -not $PSBoundParameters.ContainsKey("Scope")) { $Scope = $env:AI_ORCH_OPENCODE_SCOPE }

$Root = Resolve-Path (Join-Path $PSScriptRoot "..")
Push-Location $Root
try {
  $ArgsList = @("run", "./cmd/ai-orch", "opencode", "refresh", "--scope", $Scope)
  if ($Path) { $ArgsList += @("--path", $Path) }
  if ($InstallRefreshJob) { $ArgsList += "--install-refresh-job" }
  & go @ArgsList
  exit $LASTEXITCODE
}
finally {
  Pop-Location
}
