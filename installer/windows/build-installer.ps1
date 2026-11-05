<#
.SYNOPSIS
  Build the Windows deliverables: portable ZIP + optional Inno Setup EXE.

.DESCRIPTION
  Two modes:
   1) From source (default): cross-compiles nggd.exe, builds the Vue Web UI,
      stages everything, then packs it.
        powershell -ExecutionPolicy Bypass -File build-installer.ps1 -Version v1.0.0
   2) From an existing release bundle (used by CI):
        powershell -ExecutionPolicy Bypass -File build-installer.ps1 -Version v1.0.0 -StageDir dist/ngg -SkipBuild

  Output (default <repo>/out):
   NGG-<version>-windows-amd64.zip         portable bundle (contains install.ps1)
   NGG-Setup-<version>.exe                 GUI installer (only when ISCC is available)
   *.sha256                                checksums
#>
[CmdletBinding()]
param(
  [string]$Version   = 'dev',
  [string]$StageDir  = '',
  [string]$RepoRoot  = '',
  [string]$OutputDir = '',
  [switch]$SkipBuild,
  [switch]$SkipInno
)

$ErrorActionPreference = 'Stop'
if (-not $RepoRoot) { $RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path }
if (-not $OutputDir) { $OutputDir = Join-Path $RepoRoot 'out' }

function Write-Step { param([string]$Message) Write-Host "[ngg] $Message" -ForegroundColor Cyan }
function Write-Ok   { param([string]$Message) Write-Host "[ngg][ok] $Message" -ForegroundColor Green }
function Write-Warn { param([string]$Message) Write-Host "[ngg][warn] $Message" -ForegroundColor Yellow }
function Fail       { param([string]$Message) throw $Message }

function Copy-Tree {
  param([string]$From, [string]$To)
  if (-not (Test-Path $From)) { return }
  New-Item -ItemType Directory -Path $To -Force | Out-Null
  Copy-Item -Path (Join-Path $From '*') -Destination $To -Recurse -Force
}

function Write-Sha256File {
  param([string]$FilePath)
  $hash = (Get-FileHash -LiteralPath $FilePath -Algorithm SHA256).Hash.ToLowerInvariant()
  $name = Split-Path -Leaf $FilePath
  [IO.File]::WriteAllText("$FilePath.sha256", "$hash  $name`r`n", (New-Object System.Text.UTF8Encoding($false)))
  return $hash
}

function Find-ISCC {
  $pf86 = ${env:ProgramFiles(x86)}
  $pf   = $env:ProgramFiles
  $candidates = @()
  if ($pf86) { $candidates += (Join-Path $pf86 'Inno Setup 6\ISCC.exe') }
  if ($pf)   { $candidates += (Join-Path $pf   'Inno Setup 6\ISCC.exe') }
  $cmd = Get-Command iscc.exe -ErrorAction SilentlyContinue
  if ($cmd) { $candidates += $cmd.Source }
  foreach ($c in $candidates) { if (Test-Path $c) { return $c } }
  return ''
}

try {
  New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null

  if (-not $StageDir) {
    if ($SkipBuild) { Fail '-SkipBuild requires -StageDir pointing at an existing release bundle.' }
    $StageDir = Join-Path $OutputDir 'stage'
    Write-Step "Staging from source into $StageDir"
    New-Item -ItemType Directory -Path $StageDir -Force | Out-Null

    if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Fail 'Go 1.26+ is required to build from source.' }
    if (-not (Get-Command npm -ErrorAction SilentlyContinue)) { Fail 'Node 20+ is required to build the Web UI.' }
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    Push-Location $RepoRoot
    try {
      & go build -trimpath "-ldflags=-s -w" -o (Join-Path $StageDir 'nggd.exe') './cmd/nggd'
      if ($LASTEXITCODE -ne 0) { Fail 'go build failed.' }
    } finally { Pop-Location }

    Push-Location (Join-Path $RepoRoot 'web')
    try {
      & npm ci --no-audit --no-fund
      if ($LASTEXITCODE -ne 0) { Fail 'npm ci failed.' }
      & npm run build
      if ($LASTEXITCODE -ne 0) { Fail 'npm run build failed.' }
    } finally { Pop-Location }
    Copy-Tree -From (Join-Path $RepoRoot 'web\dist') -To (Join-Path $StageDir 'web-dist')

    Copy-Tree -From (Join-Path $RepoRoot 'configs')  -To (Join-Path $StageDir 'configs')
    Copy-Tree -From (Join-Path $RepoRoot 'docs')     -To (Join-Path $StageDir 'docs')
    Copy-Tree -From (Join-Path $RepoRoot 'scripts')  -To (Join-Path $StageDir 'scripts')
    Copy-Tree -From (Join-Path $RepoRoot 'deploy')   -To (Join-Path $StageDir 'deploy')
    foreach ($f in @('LICENSE', 'README.md')) {
      $p = Join-Path $RepoRoot $f
      if (Test-Path $p) { Copy-Item -LiteralPath $p -Destination $StageDir -Force }
    }
  }

  $StageDir = (Resolve-Path $StageDir).Path
  if (-not (Test-Path (Join-Path $StageDir 'nggd.exe'))) { Fail "StageDir has no nggd.exe: $StageDir" }

  # Installer scripts must be inside the bundle for both ZIP and EXE.
  # Put them at the ZIP root as well so `install.bat` / `.\install.ps1` work
  # right after extracting, without hunting through subfolders.
  $stageInstaller = Join-Path $StageDir 'installer'
  New-Item -ItemType Directory -Path $stageInstaller -Force | Out-Null
  foreach ($f in @('install.ps1', 'uninstall.ps1', 'install.bat', 'set-system-proxy.ps1')) {
    $src = Join-Path $PSScriptRoot $f
    Copy-Item -LiteralPath $src -Destination (Join-Path $stageInstaller $f) -Force
    Copy-Item -LiteralPath $src -Destination (Join-Path $StageDir $f) -Force
  }
  # NGG.iss references these directories with wildcards; keep them non-empty.
  foreach ($d in @('docs', 'scripts', 'configs', 'web-dist')) {
    $p = Join-Path $StageDir $d
    if (-not (Test-Path $p)) { New-Item -ItemType Directory -Path $p -Force | Out-Null }
    if (-not (Get-ChildItem -LiteralPath $p -Force)) {
      Set-Content -LiteralPath (Join-Path $p 'README.txt') -Value "NGG $d" -Encoding ASCII
    }
  }

  Write-Step 'Creating portable ZIP ...'
  $zip = Join-Path $OutputDir "NGG-$Version-windows-amd64.zip"
  if (Test-Path $zip) { Remove-Item -LiteralPath $zip -Force }
  Compress-Archive -Path (Join-Path $StageDir '*') -DestinationPath $zip -CompressionLevel Optimal
  $zipHash = Write-Sha256File -FilePath $zip
  Write-Ok "ZIP : $zip  sha256=$zipHash"

  if (-not $SkipInno) {
    $iscc = Find-ISCC
    if ($iscc) {
      Write-Step "Compiling installer with $iscc ..."
      # ISCC 6.7 has no /D switch. Generate NGG.build.iss in the (gitignored)
      # output dir with concrete #define values and an absolute LicenseFile.
      $issTemplate = Join-Path $PSScriptRoot 'NGG.iss'
      $issBuild    = Join-Path $OutputDir 'NGG.build.iss'
      $issText     = [IO.File]::ReadAllText($issTemplate)
      $stageForIss = $StageDir.Replace('\', '/')
      $issText = [regex]::Replace($issText, '#ifndef MyAppVersion\r?\n\s*#define MyAppVersion "[^"]*"\r?\n#endif', ('#define MyAppVersion "' + $Version + '"'))
      $issText = [regex]::Replace($issText, '#ifndef StageDir\r?\n\s*#define StageDir "[^"]*"\r?\n#endif', ('#define StageDir "' + $stageForIss + '"'))
      $license = (Join-Path $RepoRoot 'LICENSE').Replace('\', '/')
      if (Test-Path $license) {
        $issText = $issText -replace 'LicenseFile=\.\.\\\.\.\\LICENSE', ('LicenseFile=' + $license)
      } else {
        $issText = $issText -replace 'LicenseFile=\.\.\\\.\.\\LICENSE', '; LicenseFile omitted (not present)'
      }
      # The generated .iss lives in the output dir, so installer script sources
      # must be absolute (Inno resolves relative Source paths from the .iss dir).
      foreach ($scriptName in @('install.ps1', 'uninstall.ps1', 'install.bat', 'set-system-proxy.ps1')) {
        $abs = (Join-Path $PSScriptRoot $scriptName).Replace('\', '/')
        $issText = $issText.Replace(('Source: "' + $scriptName + '"'), ('Source: "' + $abs + '"'))
      }
      [IO.File]::WriteAllText($issBuild, $issText, (New-Object System.Text.UTF8Encoding($false)))
      & $iscc "/O$OutputDir" $issBuild
      if ($LASTEXITCODE -ne 0) { Fail 'ISCC failed; portable ZIP is still valid.' }
      $exe = Join-Path $OutputDir "NGG-Setup-$Version.exe"
      if (-not (Test-Path $exe)) { Fail "Expected installer not found: $exe" }
      $exeHash = Write-Sha256File -FilePath $exe
      Write-Ok "EXE : $exe  sha256=$exeHash"
    } else {
      Write-Warn 'Inno Setup 6 (ISCC.exe) not found - skipped EXE. Install it from https://jrsoftware.org/isdl.php or choco install innosetup.'
      Write-Warn 'Portable ZIP is fully functional: extract it and run install.ps1 as administrator.'
    }
  }

  Write-Host ''
  Write-Host '===== Windows deliverables =====' -ForegroundColor Green
  Get-ChildItem -LiteralPath $OutputDir -File |
    Where-Object { $_.Name -like "NGG-$Version*" -or $_.Name -like "NGG-Setup-$Version*" } |
    Select-Object Name, Length | Format-Table -AutoSize | Out-Host
  Write-Host "Verify with: Get-FileHash <file> -Algorithm SHA256; compare with <file>.sha256"
  exit 0
}
catch {
  Write-Host "[ngg][error] $($_.Exception.Message)" -ForegroundColor Red
  exit 1
}
