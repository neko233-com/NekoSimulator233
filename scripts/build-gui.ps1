# Builds NekoSimulator233 as a true Windows GUI app (no console window).
# This matters together with the emulator's DETACHED_PROCESS launch flag:
# closing the launching terminal must never take the emulator down with it.
$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Push-Location $root
try {
  $out = Join-Path $root 'build\NekoSimulator233.exe'
  New-Item -ItemType Directory -Force -Path (Split-Path $out) | Out-Null
  go build -ldflags '-H=windowsgui' -o $out . 2>&1 | Out-String | Write-Host
  if ($LASTEXITCODE -ne 0) { throw 'GUI build failed' }
  Write-Host "Built $out (GUI subsystem, no console)"
} finally {
  Pop-Location
}
