param(
    [ValidatePattern('^\d+\.\d+\.\d+$')][string]$Version = '0.2.0',
    [string]$BundleSdkRoot = $env:ANDROID_HOME,
    [switch]$WithoutRuntime,
    [switch]$WithRuntime
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$build = Join-Path $root 'build'
New-Item -ItemType Directory -Path $build -Force | Out-Null
Push-Location $root
try {
    $includeRuntime = 0
    $runtimeBundle = Join-Path $build ("android-runtime-{0}.zip" -f [guid]::NewGuid().ToString('N'))
    if ($WithRuntime -and -not $WithoutRuntime) {
        if (-not $BundleSdkRoot) { $BundleSdkRoot = Join-Path $env:LOCALAPPDATA 'Android/Sdk' }
        & (Join-Path $PSScriptRoot 'bundle-runtime.ps1') -SdkRoot $BundleSdkRoot -OutputPath $runtimeBundle
        $includeRuntime = 1
    }
    $env:CGO_ENABLED = '0'
    go build -tags gpui_prebuilt -trimpath -ldflags "-s -w -H=windowsgui -X main.version=$Version" -o (Join-Path $build 'NekoSimulator233.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Go GPUI build failed.' }
    go build -trimpath -ldflags "-s -w -X github.com/neko233-com/NekoSimulator233/internal/agent.Version=$Version" -o (Join-Path $build 'neko-mcp.exe') ./cmd/neko-mcp
    if ($LASTEXITCODE -ne 0) { throw 'MCP server build failed.' }
    go build -trimpath -ldflags "-s -w -X main.Version=$Version" -o (Join-Path $build 'neko-cli.exe') ./cmd/neko-cli
    if ($LASTEXITCODE -ne 0) { throw 'CLI build failed.' }
    wix build (Join-Path $root 'installer/NekoSimulator233.wxs') -arch x64 -d "Version=$Version" -d "Exe=$(Join-Path $build 'NekoSimulator233.exe')" -d "McpExe=$(Join-Path $build 'neko-mcp.exe')" -d "CliExe=$(Join-Path $build 'neko-cli.exe')" -d "GpuiLicense=$(Join-Path $root 'third_party/gpui-go/LICENSE')" -d "GpuiNotice=$(Join-Path $root 'third_party/gpui-go/NOTICE')" -d "ConfigureJDK=$(Join-Path $root 'scripts/configure-jdk.ps1')" -d "IncludeRuntime=$includeRuntime" -d "RuntimeBundle=$runtimeBundle" -out (Join-Path $build "NekoSimulator233-$Version-x64.msi")
    if ($LASTEXITCODE -ne 0) { throw 'MSI build failed.' }
} finally { Pop-Location }
