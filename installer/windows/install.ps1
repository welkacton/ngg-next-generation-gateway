<#
.SYNOPSIS
  Install NGG Next-Generation Gateway on Windows.

.DESCRIPTION
  Copies nggd.exe + Web UI + configs, writes a config that uses absolute paths,
  registers a real Windows service (the Go binary implements svc.Handler),
  starts it and verifies /api/v1/health.

  Run from an elevated PowerShell:
    powershell -ExecutionPolicy Bypass -File install.ps1 -Source C:\path\to\bundle
    powershell -ExecutionPolicy Bypass -File install.ps1 -Version v1.0.0 -Token ghp_xxx
    powershell -ExecutionPolicy Bypass -File install.ps1 -ConfigureOnly -Silent

  The control plane binds to 127.0.0.1 only and requires a Bearer token.
  This script never opens a firewall port.
#>
[CmdletBinding()]
param(
  [string]$InstallDir  = (Join-Path $env:ProgramFiles 'NGG'),
  [string]$Source      = '',
  [string]$Version     = '',
  [string]$Token       = '',
  [switch]$ConfigureOnly,
  [switch]$NoService,
  [switch]$NoStart,
  [switch]$NoAutostart,
  [switch]$OpenBrowser,
  [switch]$Silent,
  [switch]$Force,
  [int]$Port           = 9090,
  [string]$ServiceName = 'NGG'
)

$ErrorActionPreference = 'Stop'
$RepoOwner = 'welkacton'
$RepoName  = 'ngg-next-generation-gateway'
$TaskName  = 'NGG Gateway'
$script:TmpDir = ''
$script:InstallFailed = $false

function Write-Step { param([string]$Message) Write-Host "[ngg] $Message" -ForegroundColor Cyan }
function Write-Ok   { param([string]$Message) Write-Host "[ngg][ok] $Message" -ForegroundColor Green }
function Write-Warn { param([string]$Message) Write-Host "[ngg][warn] $Message" -ForegroundColor Yellow }
function Fail       { param([string]$Message) throw $Message }

function Assert-Administrator {
  $identity  = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($identity)
  if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Fail 'Administrator privileges are required. Right-click PowerShell -> Run as administrator.'
  }
}

function New-RandomToken {
  $bytes = New-Object byte[] 32
  $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
  try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
  return (($bytes | ForEach-Object { $_.ToString('x2') }) -join '')
}

function Get-GitHubHeaders {
  param([string]$Accept = 'application/vnd.github+json')
  $h = @{ 'Accept' = $Accept; 'User-Agent' = 'NGG-Windows-Installer' }
  if ($env:GITHUB_TOKEN) { $h['Authorization'] = 'Bearer ' + $env:GITHUB_TOKEN }
  if ($Token)            { $h['Authorization'] = 'Bearer ' + $Token }
  return $h
}

function Save-ReleaseAsset {
  param([string]$AssetName, [string]$OutFile)
  $base = "https://github.com/$RepoOwner/$RepoName/releases/download/$Version/$AssetName"
  $headers = Get-GitHubHeaders -Accept 'application/octet-stream'
  try {
    Invoke-WebRequest -Uri $base -Headers $headers -OutFile $OutFile -UseBasicParsing -TimeoutSec 120 | Out-Null
    if ((Get-Item $OutFile).Length -gt 0) { return }
  } catch {
    Write-Warn "Direct release URL failed, falling back to GitHub API (private repos need a PAT)."
  }
  $api = "https://api.github.com/repos/$RepoOwner/$RepoName/releases/tags/$Version"
  $release = Invoke-RestMethod -Uri $api -Headers (Get-GitHubHeaders) -TimeoutSec 60
  $asset = $release.assets | Where-Object { $_.name -eq $AssetName } | Select-Object -First 1
  if (-not $asset) { Fail "Asset $AssetName not found in release $Version." }
  Invoke-WebRequest -Uri "https://api.github.com/repos/$RepoOwner/$RepoName/releases/assets/$($asset.id)" `
    -Headers (Get-GitHubHeaders -Accept 'application/octet-stream') `
    -OutFile $OutFile -UseBasicParsing -TimeoutSec 300 | Out-Null
}

function Get-ReleaseBundle {
  $script:TmpDir = Join-Path ([IO.Path]::GetTempPath()) ('ngg-install-' + [guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $script:TmpDir -Force | Out-Null
  $asset = "ngg-$Version-windows-amd64.zip"
  $zip   = Join-Path $script:TmpDir $asset
  Write-Step "Downloading $asset ..."
  Save-ReleaseAsset -AssetName $asset -OutFile $zip

  # Verify against SHA256SUMS when the release provides it.
  $sums = Join-Path $script:TmpDir 'SHA256SUMS'
  try {
    Save-ReleaseAsset -AssetName 'SHA256SUMS' -OutFile $sums
    $expected = (Get-Content $sums | Where-Object { $_ -match [regex]::Escape($asset) } | Select-Object -First 1) -split '\s+' | Select-Object -First 1
    if ($expected) {
      $actual = (Get-FileHash -Path $zip -Algorithm SHA256).Hash.ToLowerInvariant()
      if ($actual -ne $expected.ToLowerInvariant()) { Fail 'SHA256 mismatch - archive rejected.' }
      Write-Ok 'SHA256 verified.'
    }
  } catch {
    Write-Warn 'SHA256SUMS unavailable; archive integrity could not be verified.'
  }

  Expand-Archive -Path $zip -DestinationPath $script:TmpDir -Force
  $candidate = Join-Path $script:TmpDir 'ngg'
  if (Test-Path (Join-Path $candidate 'nggd.exe')) { return $candidate }
  return $script:TmpDir
}

function Resolve-BundleRoot {
  if ($Version) { return (Get-ReleaseBundle) }
  $candidates = @()
  if ($Source) { $candidates += $Source }
  $candidates += $PSScriptRoot
  $candidates += (Join-Path $PSScriptRoot 'payload')
  $candidates += (Join-Path $PSScriptRoot '..\..')
  foreach ($c in $candidates) {
    if (-not $c) { continue }
    try { $full = (Resolve-Path $c -ErrorAction Stop).Path } catch { continue }
    if ((Test-Path (Join-Path $full 'nggd.exe')) -or (Test-Path (Join-Path $full 'bin\nggd.exe'))) {
      return $full
    }
  }
  Fail 'Cannot find nggd.exe. Pass -Source <bundle dir>, -Version <tag>, or run from the repo root.'
}

function Find-PayloadPath {
  param([string]$Root, [string[]]$Relative)
  foreach ($r in $Relative) {
    $p = Join-Path $Root $r
    if (Test-Path -LiteralPath $p) { return $p }
  }
  return ''
}

function Copy-DirectoryContents {
  param([string]$From, [string]$To)
  if (-not $From) { return }
  New-Item -ItemType Directory -Path $To -Force | Out-Null
  Copy-Item -Path (Join-Path $From '*') -Destination $To -Recurse -Force -ErrorAction SilentlyContinue
}

try {
  Assert-Administrator
  # Inno Setup already placed the payload in {app}; -ConfigureOnly skips copying.
  if ($ConfigureOnly -and -not $Source) { $Source = $InstallDir }
  $root = Resolve-BundleRoot
  Write-Step "Payload: $root"
  Write-Step "Install dir: $InstallDir"

  $binSource = Find-PayloadPath -Root $root -Relative @('nggd.exe', 'bin\nggd.exe')
  $tplSource = Find-PayloadPath -Root $root -Relative @('configs\ngg.example.yaml', 'configs\ngg.yaml.example')
  if (-not $binSource) { Fail 'Bundle does not contain nggd.exe.' }
  if (-not $tplSource) { Fail 'Bundle does not contain configs/ngg.example.yaml.' }

  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  $binPath  = Join-Path $InstallDir 'nggd.exe'
  $cfgDir   = Join-Path $InstallDir 'configs'
  $cfgPath  = Join-Path $cfgDir 'ngg.yaml'
  $dataDir  = Join-Path $InstallDir 'data'
  $logDir   = Join-Path $InstallDir 'logs'
  $webDir   = Join-Path $InstallDir 'web\dist'

  if (-not $ConfigureOnly) {
    Write-Step 'Copying program files ...'
    Copy-Item -LiteralPath $binSource -Destination $binPath -Force
    $webSource = Find-PayloadPath -Root $root -Relative @('web-dist', 'web\dist')
    if ($webSource) { Copy-DirectoryContents -From $webSource -To $webDir }
    else { Write-Warn 'Web UI (web-dist) not found; the control plane will show the "not built" status page.' }

    Copy-DirectoryContents -From (Find-PayloadPath -Root $root -Relative @('configs\cores.d')) -To (Join-Path $cfgDir 'cores.d')
    Copy-DirectoryContents -From (Find-PayloadPath -Root $root -Relative @('docs')) -To (Join-Path $InstallDir 'docs')
    Copy-DirectoryContents -From (Find-PayloadPath -Root $root -Relative @('scripts')) -To (Join-Path $InstallDir 'scripts')
    New-Item -ItemType Directory -Path (Join-Path $InstallDir 'installer') -Force | Out-Null
    foreach ($f in @('install.ps1', 'uninstall.ps1')) {
      $src = Join-Path $PSScriptRoot $f
      if (Test-Path $src) { Copy-Item -LiteralPath $src -Destination (Join-Path $InstallDir "installer\$f") -Force }
    }
    Write-Ok 'Program files copied.'
  }

  New-Item -ItemType Directory -Path $cfgDir, $dataDir, $logDir -Force | Out-Null

  # Configure: rewrite all relative paths to absolute Windows paths (forward
  # slashes keep YAML plain scalars unambiguous), and pre-generate the token so
  # the service account never needs to write back to the config file.
  $existing = (Test-Path $cfgPath) -and (-not $Force)
  $apiToken = ''
  if ($existing) {
    Write-Step "Keeping existing config $cfgPath"
    $match = Select-String -Path $cfgPath -Pattern '^[ \t]*token:[ \t]*"?(?<t>[0-9a-fA-F]{64})"?' | Select-Object -First 1
    if ($match) { $apiToken = $match.Matches[0].Groups['t'].Value }
    if (-not $apiToken) { Fail "Cannot read API token from $cfgPath; re-run with -Force to regenerate it." }
  } else {
    $apiToken = New-RandomToken
    $text = [IO.File]::ReadAllText($tplSource)
    $slashes = $InstallDir.Replace('\', '/')
    # \r? keeps this working on CRLF checkouts (git core.autocrlf=true on Windows).
    $text = $text -replace '(?m)^([ \t]*dir:[ \t]*)data[ \t]*\r?$',          ('${1}' + $slashes + '/data')
    $text = $text -replace '(?m)^([ \t]*dir:[ \t]*)data/logs[ \t]*\r?$',     ('${1}' + $slashes + '/logs')
    $text = $text -replace '(?m)^([ \t]*web_dir:[ \t]*).*?\r?$',             ('${1}' + $slashes + '/web/dist')
    $text = $text -replace '(?m)^([ \t]*descriptor_dir:[ \t]*).*?\r?$',      ('${1}' + $slashes + '/configs/cores.d')
    $text = $text -replace '(?m)^([ \t]*token:[ \t]*).*?\r?$',               ('${1}"' + $apiToken + '"')
    if ($Port -ne 9090) {
      $rx = [System.Text.RegularExpressions.Regex]::new('(?m)^([ \t]*port:[ \t]*)9090[ \t]*\r?$', [System.Text.RegularExpressions.RegexOptions]::Multiline)
      $text = $rx.Replace($text, '${1}' + $Port, 1)   # only server.port, not ingress/dns ports
    }
    [IO.File]::WriteAllText($cfgPath, $text, (New-Object System.Text.UTF8Encoding($false)))
    Write-Ok "Config written: $cfgPath (absolute paths + random API token)"
  }

  # Restrict the token file to SYSTEM (the service account) and Administrators.
  & icacls.exe $cfgPath /inheritance:r /grant:r '*S-1-5-18:F' '*S-1-5-32-544:F' | Out-Null
  & icacls.exe $dataDir /grant '*S-1-5-18:(OI)(CI)F' | Out-Null
  & icacls.exe $logDir  /grant '*S-1-5-18:(OI)(CI)F' | Out-Null

  $binArg = '"{0}" -config "{1}"' -f $binPath, $cfgPath
  if ($NoAutostart) {
    Write-Warn 'NoAutostart: no Windows service or scheduled task was registered.'
  } elseif (-not $NoService) {
    Write-Step "Registering Windows service '$ServiceName' ..."
    $current = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($current) {
      if ($current.Status -ne 'Stopped') { Stop-Service -Name $ServiceName -Force }
      & sc.exe delete $ServiceName | Out-Null
      Start-Sleep -Seconds 2
    }
    New-Service -Name $ServiceName -BinaryPathName $binArg `
      -DisplayName 'NGG Next-Generation Gateway' -StartupType Automatic | Out-Null
    & sc.exe description $ServiceName 'NGG unified gateway control plane and network engine' | Out-Null
    & sc.exe failure $ServiceName reset= 86400 actions= 'restart/5000/restart/10000/restart/30000' | Out-Null
    Write-Ok 'Windows service registered (Automatic, restart on failure).'
  } else {
    Write-Step "Registering scheduled task '$TaskName' at system startup ..."
    & schtasks.exe /Delete /TN $TaskName /F 2>$null | Out-Null
    & schtasks.exe /Create /TN $TaskName /TR $binArg /SC ONSTART /RU SYSTEM /RL HIGHEST /F | Out-Null
    Write-Warn 'Scheduled-task mode has no console: log output is discarded. Prefer the Windows service.'
  }

  if (-not $NoStart -and -not $NoAutostart) {
    Write-Step 'Starting NGG and waiting for /api/v1/health ...'
    if ($NoService) { & schtasks.exe /Run /TN $TaskName | Out-Null }
    else { Start-Service -Name $ServiceName }
    $healthy = $false
    $deadline = (Get-Date).AddSeconds(40)
    while ((Get-Date) -lt $deadline) {
      try {
        $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/api/v1/health" `
          -Headers @{ Authorization = "Bearer $apiToken" } -UseBasicParsing -TimeoutSec 3
        if ($r.StatusCode -eq 200) { $healthy = $true; break }
      } catch { Start-Sleep -Milliseconds 800 }
    }
    if (-not $healthy) {
      $logFile = Join-Path $logDir 'ngg-service.log'
      Write-Warn "Health check failed. Inspect $logFile and the Application event log."
      if (Test-Path $logFile) { Get-Content $logFile -Tail 40 }
      Fail 'NGG did not become healthy; installation is incomplete.'
    }
    Write-Ok 'Health check passed.'
  }

  $url = "http://127.0.0.1:$Port/?token=$apiToken"

  # The GUI installer runs PowerShell hidden, so persist the first-run URL.
  # ACL is restricted because this file contains the API token.
  $accessFile = Join-Path $InstallDir 'NGG-ACCESS.txt'
  $accessLines = @(
    'NGG Next-Generation Gateway - first-run access',
    '',
    "Web UI      : $url",
    "API token   : $apiToken",
    "Config      : $cfgPath",
    "Service     : $ServiceName (Automatic)",
    "Service log : $(Join-Path $logDir 'ngg-service.log')",
    '',
    'This file contains a secret. Do not share, commit or screenshot it.',
    'Rotate the token by clearing token: "" in the config and restarting the service.'
  )
  [IO.File]::WriteAllText($accessFile, ($accessLines -join "`r`n") + "`r`n", (New-Object System.Text.UTF8Encoding($false)))
  & icacls.exe $accessFile /inheritance:r /grant:r '*S-1-5-18:F' '*S-1-5-32-544:F' | Out-Null

  Write-Host ''
  Write-Host '===== NGG installed =====' -ForegroundColor Green
  Write-Host "Service      : $ServiceName  (Get-Service $ServiceName / Restart-Service $ServiceName)"
  Write-Host "Install dir  : $InstallDir"
  Write-Host "Config       : $cfgPath  (0600-equivalent ACL: SYSTEM + Administrators)"
  Write-Host "Service log  : $(Join-Path $logDir 'ngg-service.log')"
  Write-Host "Web UI       : $url"
  Write-Host "WARNING      : save the token in a password manager; never commit it or paste it in chat."
  Write-Host ''
  if ($OpenBrowser) { Start-Process $url }
  if (-not $Silent) { Write-Host 'Press Enter to close...' -ForegroundColor DarkGray; Read-Host | Out-Null }
}
catch {
  Write-Host "[ngg][error] $($_.Exception.Message)" -ForegroundColor Red
  $script:InstallFailed = $true
}
finally {
  if ($script:TmpDir -and (Test-Path $script:TmpDir)) {
    Remove-Item -LiteralPath $script:TmpDir -Recurse -Force -ErrorAction SilentlyContinue
  }
}
if ($script:InstallFailed) { exit 1 }
exit 0
