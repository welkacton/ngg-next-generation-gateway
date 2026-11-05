<#
.SYNOPSIS
  Interim "make most Windows apps go through NGG" helper (system proxy, NOT TUN).

.DESCRIPTION
  NGG's Windows build has no TUN device yet (ErrTUNUnsupported). Until a
  Wintun driver + userspace TCP stack are implemented (see
  docs/TUN-GLOBAL-TAKEOVER.md), this script routes WinINET/WinHTTP traffic
  through NGG's userspace ingress at 127.0.0.1:18080.

  It is NOT global takeover: apps that ignore the system proxy, raw UDP and
  some games/downloaders will bypass it. A backup is always written first and
  -Disable restores the exact previous settings.

    powershell -ExecutionPolicy Bypass -File set-system-proxy.ps1 -Enable
    powershell -ExecutionPolicy Bypass -File set-system-proxy.ps1 -ShowStatus
    powershell -ExecutionPolicy Bypass -File set-system-proxy.ps1 -Disable
#>
[CmdletBinding()]
param(
  [switch]$Enable,
  [switch]$Disable,
  [switch]$ShowStatus,
  [string]$ProxyAddress = '127.0.0.1:18080'
)

$ErrorActionPreference = 'Stop'
$RegKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'
$BackupDir = Join-Path $env:LOCALAPPDATA 'NGG'
$BackupFile = Join-Path $BackupDir 'proxy-backup.json'

function Write-Step { param([string]$Message) Write-Host "[ngg] $Message" -ForegroundColor Cyan }
function Write-Ok   { param([string]$Message) Write-Host "[ngg][ok] $Message" -ForegroundColor Green }
function Write-Warn { param([string]$Message) Write-Host "[ngg][warn] $Message" -ForegroundColor Yellow }

function Get-WinHttpProxy {
  try {
    $text = (& netsh winhttp show proxy) -join "`n"
    $m = [regex]::Match($text, 'Proxy Server\(s\)\s*:\s*([^\r\n]+)')
    if ($m.Success) { return $m.Groups[1].Value.Trim() }
  } catch { }
  return ''
}

function Save-Backup {
  if (Test-Path $BackupFile) { return }   # never overwrite the original backup
  New-Item -ItemType Directory -Path $BackupDir -Force | Out-Null
  $props = Get-ItemProperty -Path $RegKey -ErrorAction SilentlyContinue
  $state = [ordered]@{
    savedAt       = (Get-Date).ToString('o')
    ProxyEnable   = if ($props) { $props.ProxyEnable } else { 0 }
    ProxyServer   = if ($props) { $props.ProxyServer } else { '' }
    ProxyOverride = if ($props) { $props.ProxyOverride } else { '' }
    AutoConfigURL = if ($props) { $props.AutoConfigURL } else { '' }
    WinHttpProxy  = Get-WinHttpProxy
  }
  $state | ConvertTo-Json -Depth 3 | Set-Content -LiteralPath $BackupFile -Encoding UTF8
  Write-Ok "Original proxy settings saved to $BackupFile"
}

try {
  if (-not ($Enable -or $Disable -or $ShowStatus)) {
    Write-Warn 'Nothing to do. Use -Enable, -Disable or -ShowStatus.'
    exit 0
  }

  if ($ShowStatus) {
    $p = Get-ItemProperty -Path $RegKey -ErrorAction SilentlyContinue
    "WinINET ProxyEnable : $($p.ProxyEnable)"
    "WinINET ProxyServer : $($p.ProxyServer)"
    "WinHTTP proxy       : $(Get-WinHttpProxy)"
    "NGG ingress         : $ProxyAddress"
    if (Test-Path $BackupFile) { "Backup              : $BackupFile" }
    exit 0
  }

  if ($Enable) {
    Save-Backup
    Set-ItemProperty -Path $RegKey -Name ProxyEnable -Value 1 -Type DWord
    Set-ItemProperty -Path $RegKey -Name ProxyServer -Value $ProxyAddress -Type String
    Set-ItemProperty -Path $RegKey -Name ProxyOverride -Value '<local>;localhost;127.*;10.*;192.168.*' -Type String
    $r = & netsh winhttp set proxy $ProxyAddress 2>&1
    if ($LASTEXITCODE -ne 0) { Write-Warn "WinHTTP proxy not set (admin required): $r" }
    Write-Ok "System proxy -> $ProxyAddress"
    Write-Warn 'This is NOT TUN/global takeover: apps ignoring the system proxy and raw UDP bypass NGG.'
    Write-Warn 'Restore anytime with: set-system-proxy.ps1 -Disable'
    exit 0
  }

  if ($Disable) {
    if (-not (Test-Path $BackupFile)) {
      Write-Warn 'No backup found; falling back to a clean reset.'
      Set-ItemProperty -Path $RegKey -Name ProxyEnable -Value 0 -Type DWord
      & netsh winhttp reset proxy | Out-Null
      Write-Ok 'Proxy disabled / WinHTTP reset.'
      exit 0
    }
    $s = Get-Content -LiteralPath $BackupFile -Raw -Encoding UTF8 | ConvertFrom-Json
    Set-ItemProperty -Path $RegKey -Name ProxyEnable -Value ([int]$s.ProxyEnable) -Type DWord
    if ($s.ProxyServer)   { Set-ItemProperty -Path $RegKey -Name ProxyServer   -Value ([string]$s.ProxyServer)   -Type String }
    if ($s.ProxyOverride) { Set-ItemProperty -Path $RegKey -Name ProxyOverride -Value ([string]$s.ProxyOverride) -Type String }
    if ($s.AutoConfigURL) { Set-ItemProperty -Path $RegKey -Name AutoConfigURL -Value ([string]$s.AutoConfigURL) -Type String }
    if ($s.WinHttpProxy) {
      & netsh winhttp set proxy ([string]$s.WinHttpProxy) | Out-Null
    } else {
      & netsh winhttp reset proxy | Out-Null
    }
    Write-Ok "Restored original proxy settings from $BackupFile"
    exit 0
  }
}
catch {
  Write-Host "[ngg][error] $($_.Exception.Message)" -ForegroundColor Red
  exit 1
}
