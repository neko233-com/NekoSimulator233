param([ValidatePattern('^\d+\.\d+\.\d+$')][string]$Version = '0.1.0')
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$build = Join-Path $root 'build'
New-Item -ItemType Directory -Path $build -Force | Out-Null
Push-Location $root
try {
    go build -trimpath -ldflags '-s -w -H=windowsgui' -o (Join-Path $build 'NekoSimulator233.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
    wix build (Join-Path $root 'installer/NekoSimulator233.wxs') -arch x64 -d "Version=$Version" -d "Exe=$(Join-Path $build 'NekoSimulator233.exe')" -out (Join-Path $build "NekoSimulator233-$Version-x64.msi")
    if ($LASTEXITCODE -ne 0) { throw 'MSI build failed.' }
} finally { Pop-Location }
