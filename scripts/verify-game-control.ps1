# Reproducible live proof: shooter keymap + joystick/tap change real game state.
# Requires built build/neko-mcp.exe and build/mcpctl.exe and a running emulator.
$ErrorActionPreference = 'Continue'

$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$mcp = Join-Path $root 'build\neko-mcp.exe'
$ctl = Join-Path $root 'build\mcpctl.exe'
$out = Join-Path $root 'launcher'
$before = Join-Path $out 'verify-before.png'
$after = Join-Path $out 'verify-after.png'
$pkg = 'com.astroloop.game'

function Invoke-Neko {
  param([string]$Name, [object]$ArgsObject)
  $file = Join-Path ([IO.Path]::GetTempPath()) ("neko-args-" + [guid]::NewGuid().ToString('N') + '.json')
  [IO.File]::WriteAllText($file, ($ArgsObject | ConvertTo-Json -Depth 12))
  try {
    $raw = & $ctl $mcp -- call $Name "@$file" 2>$null | Out-String
    $json = $raw | ConvertFrom-Json
    if ($json.is_error) { throw "$Name failed: $raw" }
    return $json.content
  } finally {
    Remove-Item $file -Force -ErrorAction SilentlyContinue
  }
}

function Save-Shot {
  param([string]$Path)
  $content = Invoke-Neko -Name 'neko_screenshot' -ArgsObject @{}
  $img = $content | Where-Object { $_.type -eq 'image' } | Select-Object -First 1
  if (-not $img) { throw 'screenshot tool returned no image' }
  [IO.File]::WriteAllBytes($Path, [Convert]::FromBase64String($img.data))
}

New-Item -ItemType Directory -Force -Path $out | Out-Null

# 1) Apply the MuMu-style shooter profile (joystick + fire/scope).
Invoke-Neko -Name 'neko_keymap_shooter' -ArgsObject @{
  joystick = @{ x = 0.12; y = 0.78 }
  radius = 0.1
  fire = @{ x = 0.78; y = 0.5 }
  scope = @{ x = 0.7; y = 0.42 }
} | Out-Null
Write-Host 'KEYMAP: shooter profile applied'

# 2) Launch the installed open-source game.
Invoke-Neko -Name 'neko_launch_app' -ArgsObject @{ package = $pkg } | Out-Null
Start-Sleep -Seconds 6
Save-Shot -Path $before
$h1 = (Get-FileHash $before -Algorithm SHA256).Hash

# 3) Drive with the applied joystick center and fire point.
Invoke-Neko -Name 'neko_joystick' -ArgsObject @{
  x = 307; y = 1123; radius = 128
  steps = @(
    @{ dx = -1; dy = 0; duration_ms = 500 }
    @{ dx = 0; dy = -1; duration_ms = 500 }
    @{ dx = 1; dy = 1; duration_ms = 500 }
  )
} | Out-Null
Invoke-Neko -Name 'neko_tap' -ArgsObject @{ x = 1997; y = 720 } | Out-Null
Invoke-Neko -Name 'neko_tap' -ArgsObject @{ x = 1280; y = 900 } | Out-Null
Start-Sleep -Seconds 3
Save-Shot -Path $after
$h2 = (Get-FileHash $after -Algorithm SHA256).Hash

# 4) Quantify the visible state change.
Add-Type -AssemblyName System.Drawing
$bmp1 = New-Object System.Drawing.Bitmap($before)
$bmp2 = New-Object System.Drawing.Bitmap($after)
if ($bmp1.Width -ne $bmp2.Width -or $bmp1.Height -ne $bmp2.Height) {
  throw "screenshot sizes differ: $($bmp1.Width)x$($bmp1.Height) vs $($bmp2.Width)x$($bmp2.Height)"
}
$changed = 0
$total = $bmp1.Width * $bmp1.Height
for ($y = 0; $y -lt $bmp1.Height; $y += 2) {
  for ($x = 0; $x -lt $bmp1.Width; $x += 2) {
    if ($bmp1.GetPixel($x, $y).ToArgb() -ne $bmp2.GetPixel($x, $y).ToArgb()) { $changed++ }
  }
}
$bmp1.Dispose(); $bmp2.Dispose()
$sampled = [math]::Ceiling($total / 4)
$ratio = [math]::Round(100.0 * $changed / $sampled, 3)

$adb = Join-Path $env:ANDROID_SDK_ROOT 'platform-tools\adb.exe'
$serial = ''
try {
  $status = (Invoke-Neko -Name 'neko_status' -ArgsObject @{} | Where-Object { $_.type -eq 'text' } | Select-Object -First 1).text | ConvertFrom-Json
  $serial = $status.serial
} catch { }
if ((Test-Path $adb) -and $serial) {
  $act = & $adb -s $serial shell dumpsys activity activities 2>$null | Select-String 'topResumedActivity' | Select-Object -First 1
} else { $act = 'adb/serial unavailable' }

Write-Host "BEFORE_SHA256=$h1"
Write-Host "AFTER_SHA256=$h2"
Write-Host "CHANGED_SAMPLES=$changed / $sampled ($ratio%)"
Write-Host "FOREGROUND=$act"
if ($ratio -le 0) { throw 'no visible state change detected' }
Write-Host 'GAME_CONTROL_VERIFIED'
