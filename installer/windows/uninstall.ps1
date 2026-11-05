<#
.SYNOPSIS
  Uninstall NGG Next-Generation Gateway from Windows.

.DESCRIPTION
  Stops and removes the Windows service (or scheduled task), backs up config
  and data by default, then removes the installation directory.

    powershell -ExecutionPolicy Bypass -File uninstall.ps1
    powershell -ExecutionPolicy Bypass -File uninstall.ps1 -KeepData -Silent
    powershell -ExecutionPolicy Bypass -File uninstall.ps1 -BackupPath C:\ngg-backup.zip
#>
[CmdletBinding()]
param(
  [string]$InstallDir  = (Join-Path $env:ProgramFiles 'NGG'),
  [string]$ServiceName = 'NGG',
  [switch]$Silent,
  [switch]$KeepData,
  [switch]$NoBackup,
  [string]$BackupPath = ''
)

$ErrorActionPreference = 'Stop'
$TaskName = 'NGG Gateway'

function Write-Step { param([string]$Message) Write-Host "[ngg] $Message" -ForegroundColor Cyan }
function Write-Ok   { param([string]$Message) Write-Host "[ngg][ok] $Message" -ForegroundColor Green }
function Write-Warn { param([string]$Message) Write-Host "[ngg][warn] $Message" -ForegroundColor Yellow }

function Assert-Administrator {
  $identity  = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($identity)
  if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Administrator privileges are required. Right-click PowerShell -> Run as administrator.'
  }
}

try {
  Assert-Administrator
  if (-not (Test-Path $InstallDir)) { Write-Warn "$InstallDir does not exist; nothing to remove." }

  if (-not $Silent) {
    Write-Warn "This will stop NGG, remove the service and delete files under $InstallDir."
    if ($KeepData) { Write-Host '[ngg] KeepData: configs, data and logs will be preserved.' -ForegroundColor Cyan }
    $answer = Read-Host 'Continue? [y/N]'
    if ($answer -notmatch '^(y|yes)$') { Write-Host '[ngg] Cancelled.'; exit 0 }
  }

  # Stop and remove the real Windows service (install.ps1 default).
  $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
  if ($svc) {
    Write-Step "Stopping service $ServiceName ..."
    if ($svc.Status -ne 'Stopped') { Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue }
    & sc.exe delete $ServiceName | Out-Null
    Write-Ok 'Service removed.'
  }

  # Remove the fallback scheduled task if it exists.
  $existingTask = & schtasks.exe /Query /TN $TaskName 2>$null
  if ($existingTask) {
    & schtasks.exe /Delete /TN $TaskName /F | Out-Null
    Write-Ok 'Scheduled task removed.'
  }

  # Remove any firewall rules the administrator may have created for NGG.
  try {
    Get-NetFirewallRule -DisplayName 'NGG *' -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue
  } catch { }

  $cfgDir  = Join-Path $InstallDir 'configs'
  $dataDir = Join-Path $InstallDir 'data'
  $logDir  = Join-Path $InstallDir 'logs'

  if (-not $NoBackup) {
    $backup = $BackupPath
    if (-not $backup) {
      $dir = Join-Path $env:ProgramData 'NGG\backups'
      New-Item -ItemType Directory -Path $dir -Force | Out-Null
      $backup = Join-Path $dir ('ngg-backup-' + (Get-Date -Format 'yyyyMMddTHHmmss') + '.zip')
    }
    $parts = @($cfgDir, $dataDir, $logDir) | Where-Object { Test-Path $_ }
    if ($parts.Count -gt 0) {
      if (Test-Path $backup) { Remove-Item -LiteralPath $backup -Force }
      Compress-Archive -Path $parts -DestinationPath $backup -CompressionLevel Optimal
      Write-Ok "Backup saved: $backup (contains the API token and node credentials - encrypt it)."
    }
  } else {
    Write-Warn 'NoBackup specified: config, token and data will not be archived.'
  }

  # This file contains the API token; never leave it behind after uninstall.
  $accessFile = Join-Path $InstallDir 'NGG-ACCESS.txt'
  if (Test-Path $accessFile) { Remove-Item -LiteralPath $accessFile -Force }

  if ($KeepData) {
    foreach ($name in @('nggd.exe', 'web', 'docs', 'scripts', 'installer')) {
      $p = Join-Path $InstallDir $name
      if (Test-Path $p) { Remove-Item -LiteralPath $p -Recurse -Force -ErrorAction SilentlyContinue }
    }
    Write-Ok "Program files removed; kept $cfgDir, $dataDir, $logDir."
  } else {
    if (Test-Path $InstallDir) { Remove-Item -LiteralPath $InstallDir -Recurse -Force }
    Write-Ok "Removed $InstallDir."
  }
  Write-Host ''
  Write-Host '===== NGG uninstalled =====' -ForegroundColor Green
  Write-Host 'The system user / service account (LocalSystem) is a built-in account and was not changed.'
  if (-not $Silent) { Write-Host 'Press Enter to close...' -ForegroundColor DarkGray; Read-Host | Out-Null }
  exit 0
}
catch {
  Write-Host "[ngg][error] $($_.Exception.Message)" -ForegroundColor Red
  exit 1
}
