# Builds NekoSimulator233 (Go + GPUI) as a true Windows GUI app (no console window).
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Push-Location $root
try {
  $out = Join-Path $root 'build\NekoSimulator233.exe'
  New-Item -ItemType Directory -Force -Path (Split-Path $out) | Out-Null
  $env:CGO_ENABLED = '0'
  go build -tags gpui_prebuilt -ldflags '-H=windowsgui' -o $out .
  if ($LASTEXITCODE -ne 0) { throw 'GUI build failed' }
  Write-Host "Built $out (Go + GPUI, GUI subsystem)"
} finally {
  Pop-Location
}
