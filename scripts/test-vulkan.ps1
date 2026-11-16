param(
    [Parameter(Mandatory)][ValidatePattern('^emulator-[0-9]+$')][string]$Serial,
    [string]$ExpectedAvd = 'NekoSimulator233'
)
$ErrorActionPreference = 'Stop'
$sdk = if ($env:ANDROID_HOME) { $env:ANDROID_HOME } elseif ($env:ANDROID_SDK_ROOT) { $env:ANDROID_SDK_ROOT } else { Join-Path $env:LOCALAPPDATA 'Android/Sdk' }
$adb = Join-Path $sdk 'platform-tools/adb.exe'
$clang = Get-ChildItem (Join-Path $sdk 'ndk') -Filter 'x86_64-linux-android26-clang.cmd' -Recurse -ErrorAction Stop | Select-Object -First 1 -ExpandProperty FullName
if (-not $clang -or -not (Test-Path -LiteralPath $adb)) { throw 'Android NDK and platform-tools are required.' }
$nameOutput = & $adb -s $Serial emu avd name
$nameExitCode = $LASTEXITCODE
$actual = ($nameOutput | Select-Object -First 1).Trim()
if ($nameExitCode -ne 0 -or $actual -ne $ExpectedAvd) { throw 'The target emulator does not match the expected project instance.' }
$root = Split-Path -Parent $PSScriptRoot
$output = Join-Path $root 'build/neko-vulkan-probe'
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $output) | Out-Null
& $clang -O2 -Wall -Wextra (Join-Path $root 'tests/vulkan-probe/main.c') -lvulkan -o $output
if ($LASTEXITCODE -ne 0) { throw 'Vulkan probe compilation failed.' }
$remote = '/data/local/tmp/neko-vulkan-probe-' + [guid]::NewGuid().ToString('N')
try {
    & $adb -s $Serial push $output $remote
    if ($LASTEXITCODE -ne 0) { throw 'Vulkan probe transfer failed.' }
    & $adb -s $Serial shell chmod 700 $remote
    if ($LASTEXITCODE -ne 0) { throw 'Vulkan probe permission setup failed.' }
    & $adb -s $Serial shell $remote
    if ($LASTEXITCODE -ne 0) { throw 'Vulkan probe execution failed.' }
} finally {
    # This path contains a freshly generated UUID and is created by this run.
    & $adb -s $Serial shell rm -f $remote | Out-Null
}
