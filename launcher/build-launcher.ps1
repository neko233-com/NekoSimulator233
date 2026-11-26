$ErrorActionPreference = 'Continue'

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = Split-Path -Parent $root
$managedSdk = Join-Path $env:LOCALAPPDATA 'NekoSimulator233-data\runtime\sdk'
$sdk = if ($env:ANDROID_SDK_ROOT -and (Test-Path $env:ANDROID_SDK_ROOT)) { $env:ANDROID_SDK_ROOT } elseif (Test-Path $managedSdk) { $managedSdk } else { 'D:\Android\Sdk' }
$bt = Get-ChildItem (Join-Path $sdk 'build-tools') -Directory | Sort-Object Name -Descending | Select-Object -First 1 -ExpandProperty FullName
$platform = Get-ChildItem (Join-Path $sdk 'platforms') -Directory | Sort-Object Name -Descending | Select-Object -First 1 -ExpandProperty FullName

$managedJdk = Join-Path $env:LOCALAPPDATA 'NekoSimulator233-data\runtime\jdk\bin'
$jbr = if (Test-Path (Join-Path $managedJdk 'javac.exe')) { $managedJdk } elseif ($env:JAVA_HOME) { Join-Path $env:JAVA_HOME 'bin' } else { 'D:\IDE\Jetbrains\Android Studio\jbr\bin' }

$aapt2 = Join-Path $bt 'aapt2.exe'
$d8 = Join-Path $bt 'd8.bat'
$zipalign = Join-Path $bt 'zipalign.exe'
$apksigner = Join-Path $bt 'apksigner.bat'
$androidJar = Join-Path $platform 'android.jar'
$javac = Join-Path $jbr 'javac.exe'
$keytool = Join-Path $jbr 'keytool.exe'

foreach ($tool in @($aapt2, $d8, $zipalign, $apksigner, $androidJar, $javac, $keytool)) {
  if (-not (Test-Path $tool)) { throw "Missing build tool: $tool" }
}

$out = Join-Path $root 'out'
$gen = Join-Path $out 'gen'
$classes = Join-Path $out 'classes'
foreach ($dir in @($out, $gen, $classes)) {
  if (Test-Path $dir) { Remove-Item -Recurse -Force $dir }
  New-Item -ItemType Directory -Path $dir | Out-Null
}

$base = Join-Path $out 'base.apk'
& $aapt2 link -o $base -I $androidJar --manifest (Join-Path $root 'AndroidManifest.xml') --java $gen --min-sdk-version 29 --target-sdk-version 35 --version-code 2 --version-name 2.0 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'aapt2 link failed' }

$sources = @(Join-Path $root 'src\com\neko233\launcher\LauncherActivity.java')
$rJava = Get-ChildItem $gen -Recurse -Filter 'R.java' -ErrorAction SilentlyContinue | Select-Object -First 1
if ($rJava) { $sources += $rJava.FullName }
& $javac -source 17 -target 17 -encoding UTF-8 -cp $androidJar -d $classes @sources 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'javac failed' }

$classFiles = Get-ChildItem $classes -Recurse -Filter '*.class' | ForEach-Object { $_.FullName }
$env:JAVA_HOME = Split-Path -Parent $jbr
$env:PATH = "$jbr;$env:PATH"
& $d8 --lib $androidJar --output $out @classFiles 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'd8 failed' }
$dex = Join-Path $out 'classes.dex'
if (-not (Test-Path $dex)) { throw 'classes.dex was not produced' }

$unsigned = Join-Path $out 'launcher-unsigned.apk'
Copy-Item $base $unsigned -Force
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [System.IO.Compression.ZipFile]::Open($unsigned, [System.IO.Compression.ZipArchiveMode]::Update)
try {
  $existing = $zip.GetEntry('classes.dex')
  if ($existing) { $existing.Delete() }
  $entry = $zip.CreateEntry('classes.dex', [System.IO.Compression.CompressionLevel]::Optimal)
  $stream = $entry.Open()
  try {
    $bytes = [IO.File]::ReadAllBytes($dex)
    $stream.Write($bytes, 0, $bytes.Length)
  } finally { $stream.Dispose() }
} finally { $zip.Dispose() }

$aligned = Join-Path $out 'launcher-aligned.apk'
& $zipalign -p -f 4 $unsigned $aligned 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'zipalign failed' }

$keystore = Join-Path $root 'debug.keystore'
if (-not (Test-Path $keystore)) {
  & $keytool -genkeypair -v -keystore $keystore -alias neko -keyalg RSA -keysize 2048 -validity 10000 -storepass android -keypass android -dname 'CN=NekoLauncher, OU=Neko, O=Neko, L=NA, S=NA, C=NA' 2>&1 | Out-String | Write-Host
  if ($LASTEXITCODE -ne 0) { throw 'keytool failed' }
}

$assetsDir = Join-Path $repoRoot 'internal\avd\assets'
New-Item -ItemType Directory -Force -Path $assetsDir | Out-Null
$final = Join-Path $assetsDir 'neko-launcher.apk'
& $apksigner sign --ks $keystore --ks-pass pass:android --key-pass pass:android --out $final $aligned 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'apksigner failed' }

Write-Host "Built embedded APK: $final"
Get-Item $final | Select-Object FullName, Length | Format-List | Out-String | Write-Host
