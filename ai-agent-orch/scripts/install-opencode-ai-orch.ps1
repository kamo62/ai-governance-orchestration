param(
  [ValidateSet("project", "global")]
  [string]$Scope = "global",
  [string]$Path = "",
  [switch]$Force
)

$ErrorActionPreference = "Stop"

if ($env:AI_ORCH_OPENCODE_SCOPE -and -not $PSBoundParameters.ContainsKey("Scope")) {
  $Scope = $env:AI_ORCH_OPENCODE_SCOPE
}

$Root = Resolve-Path (Join-Path $PSScriptRoot "..")
Push-Location $Root
try {
  $ArgsList = @("run", "./cmd/ai-orch", "opencode", "install-config", "--scope", $Scope)
  if ($Path) {
    $ArgsList += @("--path", $Path)
  }
  if ($Force) {
    $ArgsList += "--force"
  }
  & go @ArgsList
  exit $LASTEXITCODE
}
finally {
  Pop-Location
}
