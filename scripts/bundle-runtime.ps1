param(
    [Parameter(Mandatory)][string]$SdkRoot,
    [Parameter(Mandatory)][string]$OutputPath
)
$ErrorActionPreference = 'Stop'
# [IO.Path]::GetRelativePath is .NET Core only; keep the packaging script
# usable with Windows PowerShell 5.1.
function Get-RelativePath([string]$Base, [string]$Target) {
    $prefix = [IO.Path]::GetFullPath($Base).TrimEnd('\') + '\'
    $full = [IO.Path]::GetFullPath($Target)
    if (-not $full.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Path is outside SDK root: $Target"
    }
    return $full.Substring($prefix.Length)
}
$sdk = (Resolve-Path -LiteralPath $SdkRoot).Path
$paths = @('emulator', 'platform-tools', 'build-tools/35.0.0', 'system-images/android-34/google_apis_playstore/x86_64')
foreach ($file in @('emulator/emulator.exe', 'platform-tools/adb.exe', 'build-tools/35.0.0/aapt.exe', 'system-images/android-34/google_apis_playstore/x86_64/system.img', 'system-images/android-34/google_apis_playstore/x86_64/source.properties', 'system-images/android-34/google_apis_playstore/x86_64/package.xml', 'system-images/android-34/google_apis_playstore/x86_64/build.prop')) {
    if (-not (Test-Path -LiteralPath (Join-Path $sdk $file) -PathType Leaf)) { throw "Missing runtime file: $file" }
}
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$output = [IO.Path]::GetFullPath($OutputPath)
if (Test-Path -LiteralPath $output) { throw "Bundle output already exists: $output" }
$stream = [IO.File]::Open($output, [IO.FileMode]::CreateNew)
try {
    $zip = [IO.Compression.ZipArchive]::new($stream, [IO.Compression.ZipArchiveMode]::Create, $true)
    try {
        foreach ($path in $paths) {
            $directory = Get-Item -LiteralPath (Join-Path $sdk $path)
            if ($directory.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Runtime path is a link: $path" }
            foreach ($item in Get-ChildItem -LiteralPath $directory.FullName -Recurse -Force) {
                if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Runtime path is a link: $($item.FullName)" }
                if ($item.PSIsContainer) { continue }
                $name = (Get-RelativePath $sdk $item.FullName).Replace('\', '/')
                [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, $item.FullName, $name, [IO.Compression.CompressionLevel]::Fastest) | Out-Null
            }
        }
    } finally { $zip.Dispose() }
} catch {
    $stream.Dispose()
    Remove-Item -LiteralPath $output -Force
    throw
} finally { $stream.Dispose() }
# Windows Installer File table sizes are signed 32-bit values.
if ((Get-Item -LiteralPath $output).Length -gt [int]::MaxValue) {
    Remove-Item -LiteralPath $output -Force
    throw 'Compressed runtime exceeds the MSI single-file limit; use a smaller licensed image.'
}
