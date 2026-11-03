#Requires -Version 5.1
<#
.SYNOPSIS
Install a checksummed Vortexify CLI release for Windows AMD64.
.EXAMPLE
.\install.ps1
.EXAMPLE
.\install.ps1 -FromDir .\dist -InstallDir C:\Tools\Vortexify
#>
[CmdletBinding()]
param(
    [string]$BaseUrl = $env:VORTEXIFY_DOWNLOAD_BASE_URL,
    [string]$FromDir,
    [string]$InstallDir = $env:VORTEXIFY_INSTALL_DIR,
    [switch]$NoPathUpdate
)

$ErrorActionPreference = 'Stop'
$temporaryDirectory = $null
$stagedBinary = $null

try {
    if ($env:OS -ne 'Windows_NT') { throw 'This installer supports Windows only.' }
    $architecture = $env:PROCESSOR_ARCHITEW6432
    if (-not $architecture) { $architecture = $env:PROCESSOR_ARCHITECTURE }
    if ($architecture -ne 'AMD64') { throw 'This release supports Windows AMD64 only.' }
    if ($BaseUrl -and $FromDir) { throw 'Choose -BaseUrl or -FromDir, not both.' }
    if (-not $BaseUrl -and -not $FromDir) {
        $BaseUrl = 'https://github.com/vortexifyai/cli/releases/latest/download'
    }
    if ($BaseUrl) {
        $uri = [Uri]$BaseUrl
        if (-not $uri.IsAbsoluteUri -or $uri.Scheme -ne 'https') { throw '-BaseUrl must use HTTPS.' }
    }
    if (-not $InstallDir) {
        $InstallDir = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Vortexify\bin'
    }
    $InstallDir = [IO.Path]::GetFullPath([Environment]::ExpandEnvironmentVariables($InstallDir))
    if ($InstallDir.Contains(';')) { throw 'The installation directory cannot contain a semicolon.' }
    $artifact = 'vortexify-windows-amd64.exe'
    $temporaryDirectory = Join-Path ([IO.Path]::GetTempPath()) ('vortexify-install-' + [Guid]::NewGuid().ToString('N'))
    [IO.Directory]::CreateDirectory($temporaryDirectory) | Out-Null

    foreach ($name in @('SHA256SUMS', $artifact)) {
        $destination = Join-Path $temporaryDirectory $name
        if ($FromDir) {
            Copy-Item -LiteralPath (Join-Path $FromDir $name) -Destination $destination
        } else {
            # Windows PowerShell 5.1 can otherwise default to older TLS protocols.
            $previousProtocol = [Net.ServicePointManager]::SecurityProtocol
            try {
                [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
                Invoke-WebRequest -Uri ($BaseUrl.TrimEnd('/') + '/' + $name) -OutFile $destination -UseBasicParsing
            } finally {
                [Net.ServicePointManager]::SecurityProtocol = $previousProtocol
            }
        }
    }

    $checksums = @(
        foreach ($line in Get-Content -LiteralPath (Join-Path $temporaryDirectory 'SHA256SUMS')) {
            if ($line -match '^([0-9a-fA-F]{64})\s+\*?vortexify-windows-amd64\.exe\s*$') { $Matches[1] }
        }
    )
    if ($checksums.Count -ne 1) { throw 'Release must contain exactly one SHA-256 checksum for the Windows executable.' }
    $download = Join-Path $temporaryDirectory $artifact
    if ((Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash -ne $checksums[0]) {
        throw 'Checksum mismatch; the installed CLI was not changed.'
    }
    if ((Get-Item -LiteralPath $download).Length -eq 0) { throw 'Release executable is empty.' }

    [IO.Directory]::CreateDirectory($InstallDir) | Out-Null
    $target = Join-Path $InstallDir 'vortexify.exe'
    $stagedBinary = Join-Path $InstallDir ('.vortexify-' + [Guid]::NewGuid().ToString('N') + '.exe')
    Copy-Item -LiteralPath $download -Destination $stagedBinary
    if (Test-Path -LiteralPath $target) {
        $backup = Join-Path $InstallDir ('.vortexify-' + [Guid]::NewGuid().ToString('N') + '.backup')
        try {
            [IO.File]::Replace($stagedBinary, $target, $backup)
        } catch {
            throw "Could not replace $target. Close running Vortexify commands and retry. $($_.Exception.Message)"
        }
        Remove-Item -LiteralPath $backup -Force
    } else {
        [IO.File]::Move($stagedBinary, $target)
    }
    $stagedBinary = $null
    Write-Host "Installed Vortexify to $target"

    if (-not $NoPathUpdate) {
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        $entries = @($userPath -split ';' | Where-Object { $_.Trim() } | ForEach-Object {
            [Environment]::ExpandEnvironmentVariables($_.Trim().Trim('"')).TrimEnd('\')
        })
        if ($entries -notcontains $InstallDir.TrimEnd('\')) {
            $newPath = if ($userPath) { $userPath.TrimEnd(';') + ';' + $InstallDir } else { $InstallDir }
            [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        }
        # Also make the CLI available when this script runs in the current shell.
        $processEntries = @($env:Path -split ';' | ForEach-Object { $_.TrimEnd('\') })
        if ($processEntries -notcontains $InstallDir.TrimEnd('\')) { $env:Path += ';' + $InstallDir }
        Write-Host 'User PATH configured. Open a new terminal if vortexify is not found in your current terminal.'
    }
    Write-Host 'Next: vortexify --version, then vortexify login'
    Write-Host 'Skills: vortexify skills install --agent codex (or --agent claude)'
} catch {
    Write-Error "Vortexify installer: $($_.Exception.Message)" -ErrorAction Continue
    exit 1
} finally {
    if ($stagedBinary -and (Test-Path -LiteralPath $stagedBinary)) { Remove-Item -LiteralPath $stagedBinary -Force }
    if ($temporaryDirectory -and (Test-Path -LiteralPath $temporaryDirectory)) { Remove-Item -LiteralPath $temporaryDirectory -Recurse -Force }
}
