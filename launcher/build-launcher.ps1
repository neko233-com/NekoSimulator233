# Builds a minimal, resource-free custom launcher APK for the Neko emulator.
# Uses only tools already present in the Android SDK and Android Studio JBR.
# Native tools write warnings to stderr; keep explicit $LASTEXITCODE checks
# instead of letting PowerShell turn stderr into terminating errors.
$ErrorActionPreference = 'Continue'

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$sdk = if ($env:ANDROID_SDK_ROOT) { $env:ANDROID_SDK_ROOT } else { 'D:\Android\Sdk' }
$bt = Join-Path $sdk 'build-tools\35.0.0'
$platform = Join-Path $sdk 'platforms\android-36'
$jbr = 'D:\IDE\Jetbrains\Android Studio\jbr\bin'

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
Write-Host 'Linking manifest...'
& $aapt2 link -o $base -I $androidJar --manifest (Join-Path $root 'AndroidManifest.xml') --java $gen --min-sdk-version 30 --target-sdk-version 36 --version-code 1 --version-name 1.0 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'aapt2 link failed' }

Write-Host 'Compiling Java...'
$sources = @(Join-Path $root 'src\com\neko233\launcher\LauncherActivity.java')
$rJava = Get-ChildItem $gen -Recurse -Filter 'R.java' -ErrorAction SilentlyContinue | Select-Object -First 1
if ($rJava) { $sources += $rJava.FullName }
& $javac -source 17 -target 17 -encoding UTF-8 -cp $androidJar -d $classes @sources 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'javac failed' }

Write-Host 'Dexing...'
$classFiles = Get-ChildItem $classes -Recurse -Filter '*.class' | ForEach-Object { $_.FullName }
& $d8 --lib $androidJar --output $out @classFiles 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'd8 failed' }
$dex = Join-Path $out 'classes.dex'
if (-not (Test-Path $dex)) { throw 'classes.dex was not produced' }

Write-Host 'Packaging...'
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

# Keep the signing key outside the disposable out/ directory so reinstalling
# an updated launcher keeps the same signature.
$keystore = Join-Path $root 'debug.keystore'
if (-not (Test-Path $keystore)) {
  & $keytool -genkeypair -v -keystore $keystore -alias neko -keyalg RSA -keysize 2048 -validity 10000 -storepass android -keypass android -dname 'CN=NekoLauncher, OU=Neko, O=Neko, L=NA, S=NA, C=NA' 2>&1 | Out-String | Write-Host
  if ($LASTEXITCODE -ne 0) { throw 'keytool failed' }
}

$final = Join-Path $root 'neko-launcher.apk'
& $apksigner sign --ks $keystore --ks-pass pass:android --key-pass pass:android --out $final $aligned 2>&1 | Out-String | Write-Host
if ($LASTEXITCODE -ne 0) { throw 'apksigner failed' }

Write-Host "Built $final"
Get-Item $final | Select-Object FullName, Length | Format-List | Out-String | Write-Host
