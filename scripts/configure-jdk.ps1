param(
    [Parameter(Mandatory)][string]$JdkHome,
    [switch]$Force
)
$ErrorActionPreference = 'Stop'
$homePath = (Resolve-Path -LiteralPath $JdkHome).Path
$java = Join-Path $homePath 'bin/java.exe'
if (-not (Test-Path -LiteralPath $java -PathType Leaf)) { throw 'JDK bin/java.exe was not found.' }
# The current MSI contains an x64 application. Inspect the selected runtime
# without changing JAVA_HOME or other system-wide environment variables.
$details = (& $java -XshowSettings:properties -version 2>&1 | Out-String)
if ($LASTEXITCODE -ne 0) { throw 'The selected Java runtime could not start.' }
if ($details -notmatch '(?m)^\s*java\.specification\.version\s*=\s*(\d+)\s*$') { throw 'Could not determine the Java version.' }
$major = [int]$Matches[1]
if ($major -lt 17) { throw 'JDK 17 or later is required.' }
if ($details -notmatch '(?m)^\s*os\.arch\s*=\s*(amd64|x86_64)\s*$') { throw 'The current x64 MSI requires an x64 JDK.' }
if ($homePath.Length -gt 4000 -or $homePath.IndexOfAny([char[]]"`r`n") -ge 0) { throw 'JDK path is invalid or too long.' }
$dir = Join-Path $env:LOCALAPPDATA 'NekoSimulator233-data'
$path = Join-Path $dir 'jdk-home.txt'
if ((Test-Path -LiteralPath $path) -and -not $Force) { throw 'Neko JDK configuration already exists; pass -Force to replace it.' }
New-Item -ItemType Directory -Path $dir -Force | Out-Null
if ($Force) {
    [IO.File]::WriteAllText($path, $homePath, [Text.UTF8Encoding]::new($false))
} else {
    $stream = [IO.File]::Open($path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        $bytes = [Text.UTF8Encoding]::new($false).GetBytes($homePath)
        $stream.Write($bytes, 0, $bytes.Length)
    } finally { $stream.Dispose() }
}
Write-Host "Configured Neko JDK $major ($homePath)."
