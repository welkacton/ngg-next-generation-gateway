<#
.SYNOPSIS
  Sync the freshly built NGG client into an installed Windows deployment.

.DESCRIPTION
  Copies web/dist and nggd.exe from the development tree into the installed
  client directory, restarts the NGG Windows service and verifies that the
  served index.html is the new bundle. This is the "sync client" half of the
  mandatory release workflow (see scripts/release.mjs).

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File scripts/sync-client.ps1
  powershell -ExecutionPolicy Bypass -File scripts/sync-client.ps1 -InstallDir 'D:\NGG'
#>
param(
  [string] $InstallDir = 'C:\Program Files\NGG',
  [string] $ServiceName = 'NGG',
  [int]    $Port = 9090,
  [switch] $SkipServiceRestart
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$SrcDist = Join-Path $Root 'web\dist'
$SrcExe = Join-Path $Root 'bin\nggd.exe'

if (-not (Test-Path (Join-Path $SrcDist 'index.html'))) {
  throw "web/dist/index.html not found. Run 'node scripts/release.mjs <version>' first."
}
if (-not (Test-Path $SrcExe)) {
  throw "bin/nggd.exe not found. Run 'node scripts/release.mjs <version>' first."
}
if (-not (Test-Path $InstallDir)) {
  throw "Install directory not found: $InstallDir"
}

$dstDist = Join-Path $InstallDir 'web\dist'
New-Item -ItemType Directory -Force -Path $dstDist | Out-Null

$service = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
$restarted = $false
if ($service -and -not $SkipServiceRestart) {
  Write-Host "Stopping service $ServiceName ..."
  Stop-Service -Name $ServiceName -Force
  $restarted = $true
  # Wait until the executable is really released by the OS.
  $deadline = (Get-Date).AddSeconds(20)
  while ((Get-Process -Name nggd -ErrorAction SilentlyContinue) -and (Get-Date) -lt $deadline) {
    Start-Sleep -Milliseconds 300
  }
}

Write-Host "Copying client bundle -> $dstDist"
Copy-Item -Path (Join-Path $SrcDist '*') -Destination $dstDist -Recurse -Force

$dstExe = Join-Path $InstallDir 'nggd.exe'
if (Test-Path $dstExe) {
  $backup = "$dstExe.bak"
  Copy-Item -Path $dstExe -Destination $backup -Force
  Write-Host "Backup of previous binary -> $backup"
}
Write-Host "Copying backend binary -> $dstExe"
Copy-Item -Path $SrcExe -Destination $dstExe -Force

if ($restarted) {
  Write-Host "Starting service $ServiceName ..."
  Start-Service -Name $ServiceName
}

# Real verification: the served index must reference the exact new bundle.
$expected = Select-String -Path (Join-Path $SrcDist 'index.html') -Pattern 'assets/index-[A-Za-z0-9_-]+\.js' |
  ForEach-Object { $_.Matches.Value } | Select-Object -First 1
$ok = $false
for ($i = 0; $i -lt 20; $i++) {
  try {
    $page = (Invoke-WebRequest -Uri "http://127.0.0.1:$Port/" -UseBasicParsing -TimeoutSec 5).Content
    if ($page -and $expected -and $page.Contains($expected)) { $ok = $true; break }
  } catch {
    Start-Sleep -Milliseconds 500
  }
}
if (-not $ok) {
  throw "Client sync verification failed: served index.html does not reference $expected"
}
Write-Host "OK: client bundle $expected is being served on port $Port"

$cfg = Join-Path $InstallDir 'configs\ngg.yaml'
if (Test-Path $cfg) {
  $line = Select-String -Path $cfg -Pattern '^\s*token:\s*(\S+)' | Select-Object -First 1
  if ($line) {
    $token = $line.Matches[0].Groups[1].Value.Trim('"', "'")
    try {
      $sys = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/v1/system" `
        -Headers @{ Authorization = "Bearer $token" } -TimeoutSec 5
      Write-Host ("OK: backend reports version {0}" -f $sys.ngg_version)
    } catch {
      Write-Host "WARN: could not read backend version: $($_.Exception.Message)"
    }
  }
}
# Marker consumed by automation to prove the elevated run really finished.
$marker = Join-Path $Root 'data\sync-client.last'
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $marker) | Out-Null
Set-Content -Path $marker -Value ("OK {0} bundle={1}" -f (Get-Date -Format o), $expected) -Encoding UTF8
Write-Host 'Client sync complete.'
