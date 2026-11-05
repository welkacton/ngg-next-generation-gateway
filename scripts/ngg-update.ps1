<#
.SYNOPSIS
  NGG in-place updater for Windows. Launched detached by
  POST /api/v1/update/apply. Backs up, replaces, restarts, health-checks and
  rolls back on failure. Never reports success without a real HTTP 200.
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][string]$StageDir,
  [Parameter(Mandatory = $true)][string]$Asset,
  [string]$InstallDir = (Split-Path -Parent $PSScriptRoot),
  [string]$ServiceName = 'NGG',
  [string]$LogPath = '',
  [int]$Port = 9090
)

$ErrorActionPreference = 'Stop'
if (-not $LogPath) { $LogPath = Join-Path $InstallDir 'logs\ngg-update.log' }
$logDir = Split-Path -Parent $LogPath
if ($logDir) { New-Item -ItemType Directory -Path $logDir -Force | Out-Null }

function Write-Log {
  param([string]$Message)
  $line = "[ngg-update] $(Get-Date -Format o) $Message"
  Add-Content -LiteralPath $LogPath -Value $line -Encoding UTF8
}

function Fail {
  param([string]$Message)
  Write-Log "ERROR: $Message"
  throw $Message
}

function Invoke-Rollback {
  param([string]$BackupDir)
  Write-Log 'Rolling back to the previous binaries'
  $binBackup = Join-Path $BackupDir 'nggd.exe'
  if (Test-Path $binBackup) {
    Copy-Item -LiteralPath $binBackup -Destination (Join-Path $InstallDir 'nggd.exe') -Force
  }
  $webBackup = Join-Path $BackupDir 'web-dist'
  if (Test-Path $webBackup) {
    $dest = Join-Path $InstallDir 'web\dist'
    if (Test-Path $dest) { Remove-Item -LiteralPath $dest -Recurse -Force }
    New-Item -ItemType Directory -Path (Split-Path -Parent $dest) -Force | Out-Null
    Copy-Item -LiteralPath $webBackup -Destination $dest -Recurse -Force
  }
  try { Start-Service -Name $ServiceName -ErrorAction SilentlyContinue } catch { }
}

try {
  # Let the HTTP response finish before we stop our own service.
  Start-Sleep -Seconds 2
  $assetPath = Join-Path $StageDir $Asset
  $sumsPath = Join-Path $StageDir 'SHA256SUMS'
  if (-not (Test-Path $assetPath)) { Fail "staged asset not found: $assetPath" }
  if (-not (Test-Path $sumsPath)) { Fail 'missing SHA256SUMS; refusing to apply' }

  $expected = ''
  foreach ($line in (Get-Content -LiteralPath $sumsPath)) {
    $parts = $line -split '\s+', 2
    if ($parts.Count -ge 2) {
      $name = $parts[1].TrimStart('*').TrimStart('./')
      if ($name -eq $Asset) { $expected = $parts[0].ToLowerInvariant(); break }
    }
  }
  if (-not $expected) { Fail "SHA256SUMS has no entry for $Asset" }
  $actual = (Get-FileHash -LiteralPath $assetPath -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($actual -ne $expected) { Fail "SHA256 mismatch: $actual != $expected" }
  Write-Log "Checksum verified: $Asset"

  $stamp = Get-Date -Format 'yyyyMMddTHHmmss'
  $backup = Join-Path $InstallDir "backup-$stamp"
  New-Item -ItemType Directory -Path $backup -Force | Out-Null
  $bin = Join-Path $InstallDir 'nggd.exe'
  if (Test-Path $bin) { Copy-Item -LiteralPath $bin -Destination (Join-Path $backup 'nggd.exe') -Force }
  $web = Join-Path $InstallDir 'web\dist'
  if (Test-Path $web) { Copy-Item -LiteralPath $web -Destination (Join-Path $backup 'web-dist') -Recurse -Force }

  try { Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue } catch { }
  Write-Log "Service $ServiceName stopped"

  if ($Asset -like '*.exe') {
    # The Inno Setup installer replaces the binaries and re-registers/starts
    # the service itself. It is the same installer the user double-clicks.
    Write-Log "Running silent installer: $assetPath"
    $p = Start-Process -FilePath $assetPath -ArgumentList @(
      '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/DIR=$InstallDir"
    ) -Wait -PassThru
    if ($p.ExitCode -ne 0) { throw "installer exited with $($p.ExitCode)" }
  }
  elseif ($Asset -like '*.zip') {
    $tmp = Join-Path $env:TEMP ("ngg-update-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp -Force | Out-Null
    try {
      Expand-Archive -LiteralPath $assetPath -DestinationPath $tmp -Force
      $newBin = Join-Path $tmp 'nggd.exe'
      if (-not (Test-Path $newBin)) { throw 'ZIP has no nggd.exe at its root' }
      Copy-Item -LiteralPath $newBin -Destination $bin -Force
      $newWeb = Join-Path $tmp 'web-dist'
      if (Test-Path $newWeb) {
        if (Test-Path $web) { Remove-Item -LiteralPath $web -Recurse -Force }
        New-Item -ItemType Directory -Path (Split-Path -Parent $web) -Force | Out-Null
        Copy-Item -LiteralPath $newWeb -Destination $web -Recurse -Force
      }
      try { Start-Service -Name $ServiceName -ErrorAction Stop } catch {
        Write-Log "Start-Service after ZIP update: $($_.Exception.Message)"
      }
    } finally {
      Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
  }
  else { throw "unsupported update asset: $Asset" }

  # Real health check: read the token from the config and call /api/v1/health.
  $cfg = Join-Path $InstallDir 'configs\ngg.yaml'
  $token = ''
  if (Test-Path $cfg) {
    $hits = @(Select-String -LiteralPath $cfg -Pattern '^[ \t]*token:[ \t]*"?(?<t>[0-9a-fA-F]{32,})"?')
    if ($hits.Count -gt 0) { $token = $hits[0].Matches[0].Groups['t'].Value }
  }
  if (-not $token) { throw "cannot read API token from $cfg" }

  $healthy = $false
  for ($i = 0; $i -lt 40; $i++) {
    try {
      $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/api/v1/health" `
        -Headers @{ Authorization = "Bearer $token" } -UseBasicParsing -TimeoutSec 3
      if ($r.StatusCode -eq 200) { $healthy = $true; break }
    } catch { Start-Sleep -Seconds 1 }
  }
  if (-not $healthy) { throw 'health check did not return 200 after update' }

  Write-Log "Update applied successfully: $Asset (backup: $backup)"
  exit 0
}
catch {
  Write-Log "FAILED: $($_.Exception.Message)"
  try { Invoke-Rollback -BackupDir $backup } catch { Write-Log "rollback error: $($_.Exception.Message)" }
  exit 1
}
