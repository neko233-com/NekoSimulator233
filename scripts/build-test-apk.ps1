# Builds a code-free, self-signed APK for isolated integration tests. The
# signing key exists only in memory and is not persisted to the workspace.
param([string]$Output = 'build/neko-agent-fixture.apk')
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$sdk = if ($env:ANDROID_HOME) { $env:ANDROID_HOME } elseif ($env:ANDROID_SDK_ROOT) { $env:ANDROID_SDK_ROOT } else { Join-Path $env:LOCALAPPDATA 'Android/Sdk' }
$aapt = Get-ChildItem (Join-Path $sdk 'build-tools') -Directory | Sort-Object Name -Descending | ForEach-Object { $p = Join-Path $_.FullName 'aapt.exe'; if (Test-Path -LiteralPath $p) { $p } } | Select-Object -First 1
$jar = Get-ChildItem (Join-Path $sdk 'platforms') -Directory | Sort-Object Name -Descending | ForEach-Object { $p = Join-Path $_.FullName 'android.jar'; if (Test-Path -LiteralPath $p) { $p } } | Select-Object -First 1
if (-not $aapt -or -not $jar) { throw 'Android SDK build-tools and platform android.jar are required.' }
$build = Join-Path $root 'build'
New-Item -ItemType Directory -Force -Path $build | Out-Null
$fixtureDir = Join-Path $build 'apk-fixture'
New-Item -ItemType Directory -Force -Path $fixtureDir | Out-Null
$manifest = Join-Path $fixtureDir 'AndroidManifest.xml'
@'
<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="invalid.example.nekoagentfixture">
  <uses-sdk android:minSdkVersion="26" android:targetSdkVersion="28" />
  <application android:label="Neko Agent Test Fixture" android:hasCode="false" android:debuggable="true" />
</manifest>
'@ | Set-Content -LiteralPath $manifest -Encoding utf8NoBOM
$apk = if ([IO.Path]::IsPathRooted($Output)) { $Output } else { Join-Path $root $Output }
& $aapt package -f -M $manifest -I $jar -F $apk
if ($LASTEXITCODE -ne 0) { throw 'aapt failed.' }
Add-Type -AssemblyName System.Security.Cryptography.Pkcs
Add-Type -AssemblyName System.IO.Compression
$zip = [IO.Compression.ZipFile]::Open($apk,[IO.Compression.ZipArchiveMode]::Update)
try {
    $entry = $zip.GetEntry('AndroidManifest.xml')
    if (-not $entry) { throw 'APK manifest missing.' }
    $input = $entry.Open(); $copy = [IO.MemoryStream]::new()
    try { $input.CopyTo($copy); $payload = $copy.ToArray() } finally { $input.Dispose(); $copy.Dispose() }
    $hash = [Convert]::ToBase64String([Security.Cryptography.SHA256]::HashData($payload))
    $sectionBytes = [Text.Encoding]::UTF8.GetBytes("Name: AndroidManifest.xml`r`nSHA-256-Digest: $hash`r`n`r`n")
    $manifestBytes = [Text.Encoding]::UTF8.GetBytes("Manifest-Version: 1.0`r`n`r`n" + [Text.Encoding]::UTF8.GetString($sectionBytes))
    $manifestHash = [Convert]::ToBase64String([Security.Cryptography.SHA256]::HashData($manifestBytes))
    $sectionHash = [Convert]::ToBase64String([Security.Cryptography.SHA256]::HashData($sectionBytes))
    $signatureBytes = [Text.Encoding]::UTF8.GetBytes("Signature-Version: 1.0`r`nSHA-256-Digest-Manifest: $manifestHash`r`n`r`nName: AndroidManifest.xml`r`nSHA-256-Digest: $sectionHash`r`n`r`n")
    $rsa = [Security.Cryptography.RSA]::Create(2048)
    try {
        $request = [Security.Cryptography.X509Certificates.CertificateRequest]::new('CN=Neko Test Fixture',$rsa,[Security.Cryptography.HashAlgorithmName]::SHA256,[Security.Cryptography.RSASignaturePadding]::Pkcs1)
        $cert = $request.CreateSelfSigned([DateTimeOffset]::UtcNow.AddDays(-1),[DateTimeOffset]::UtcNow.AddDays(7))
        try {
            $content = [Security.Cryptography.Pkcs.ContentInfo]::new($signatureBytes)
            $cms = [Security.Cryptography.Pkcs.SignedCms]::new($content,$true)
            $signer = [Security.Cryptography.Pkcs.CmsSigner]::new($cert)
            $cms.ComputeSignature($signer)
            foreach ($item in @(@('META-INF/MANIFEST.MF',$manifestBytes),@('META-INF/CERT.SF',$signatureBytes),@('META-INF/CERT.RSA',$cms.Encode()))) {
                $outEntry = $zip.CreateEntry($item[0]); $stream = $outEntry.Open()
                try { $stream.Write($item[1],0,$item[1].Length) } finally { $stream.Dispose() }
            }
        } finally { $cert.Dispose() }
    } finally { $rsa.Dispose() }
} finally { $zip.Dispose() }
Write-Output $apk
