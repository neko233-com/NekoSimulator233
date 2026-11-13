param(
    [Parameter(Mandatory)][ValidateScript({ Test-Path -LiteralPath $_ -PathType Leaf })][string]$MsiPath,
    [switch]$Quiet
)
$ErrorActionPreference = 'Stop'
$msi = (Resolve-Path -LiteralPath $MsiPath).Path
$arguments = @('/i', ('"{0}"' -f $msi), '/norestart')
if ($Quiet) { $arguments += '/qn' }
$process = Start-Process -FilePath 'msiexec.exe' -ArgumentList $arguments -Wait -PassThru -WindowStyle Hidden
if ($process.ExitCode -notin @(0, 3010)) { throw "MSI installation failed with exit code $($process.ExitCode)." }
if ($process.ExitCode -eq 3010) { Write-Host 'Installation succeeded; Windows requires a restart.' }
else { Write-Host 'Installation succeeded.' }
