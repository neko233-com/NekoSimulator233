param(
    [Parameter(Mandatory)][ValidateScript({ Test-Path -LiteralPath $_ -PathType Leaf })][string]$MsiPath,
    [Parameter(Mandatory)][ValidatePattern('^\d+\.\d+\.\d+$')][string]$Version
)
$ErrorActionPreference = 'Stop'
$msi = (Resolve-Path -LiteralPath $MsiPath).Path
if ($msi.StartsWith('\\') -or [IO.Path]::GetExtension($msi) -ine '.msi') {
    throw 'The MSI must be an absolute local .msi file.'
}
$dir = Join-Path $env:LOCALAPPDATA 'NekoSimulator233-data'
New-Item -ItemType Directory -Path $dir -Force | Out-Null
$config = Join-Path $dir 'update.json'
$temporary = Join-Path $dir ('.update-' + [guid]::NewGuid().ToString('N') + '.tmp')
try {
    $manifest = @{
        version = $Version
        msi = $msi
        sha256 = (Get-FileHash -LiteralPath $msi -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    $manifest | ConvertTo-Json | Set-Content -LiteralPath $temporary -Encoding utf8NoBOM
    [IO.File]::Move($temporary, $config, $true)
} finally {
    if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary }
}
Write-Output 'Local update configured. Only use an MSI from a trusted source.'
