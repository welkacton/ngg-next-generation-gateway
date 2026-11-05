#requires -Version 5.1
<#
.SYNOPSIS
  NGG TUN end-to-end acceptance script (requires Administrator).

  1. Starts a temporary instance of bin\nggd.exe (port 9092, own data dir)
  2. Downloads + verifies the signed wintun.dll
  3. Creates the wintun adapter and reads its real state back
  4. Adds ONE temporary /32 route for the probe address, sends a real HTTP GET
  5. Reads the adapter counters, then removes the route, destroys the adapter
  6. Writes every step to data\tun-verify\report.json

  Why not "take over the default route": that moves the whole machine into the
  tunnel and cuts connectivity if anything fails. A /32 route proves the same
  data path with a blast radius of exactly one address.
#>
param(
  [string]$Root = (Split-Path -Parent $PSScriptRoot),
  [int]$Port = 9092,
  [string]$TunName = 'nggverify',
  [string]$TunIPv4 = '10.99.0.1/24',
  [string]$ProbeIP = '1.1.1.1'
)

$ErrorActionPreference = 'Continue'

function Now { (Get-Date).ToString('o') }

$steps = New-Object System.Collections.ArrayList
function Add-Step {
  param([string]$Name, [bool]$Ok, [string]$Detail)
  [void]$steps.Add([ordered]@{ step = $Name; ok = $Ok; detail = $Detail; at = (Now) })
  $mark = 'FAIL'
  if ($Ok) { $mark = 'PASS' }
  Write-Host ("[{0}] {1} - {2}" -f $mark, $Name, $Detail)
}

function OneLine {
  param([string]$Text)
  if (-not $Text) { return '' }
  return ($Text -replace "\r?\n", ' ').Trim()
}

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
  Write-Host 'Administrator rights required: run this script from an elevated PowerShell.' -ForegroundColor Red
  exit 2
}

$dataDir = Join-Path $Root 'data\tun-verify'
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
$reportPath = Join-Path $dataDir 'report.json'
$exe = Join-Path $Root 'bin\nggd.exe'
if (-not (Test-Path $exe)) { throw "missing $exe - run node scripts/release.mjs <version> first" }

$token = 'tun-verify-0123456789abcdef0123456789abcdef0123456789abcdef01234567'
$cfgPath = Join-Path $dataDir 'config.yaml'
$tunIP = ($TunIPv4 -split '/')[0]
$cfg = @"
server:
  listen: 127.0.0.1
  port: $Port
  token: $token
  web_dir: web/dist
storage:
  dir: data/tun-verify
cores:
  descriptor_dir: configs/cores.d
  port_base: 47890
  port_stride: 10
logging:
  level: info
  dir: data/tun-verify/logs
  ring_capacity: 500
network:
  enabled: true
  fail_closed: false
  failover: false
  default_core: ""
  direct_on_failure: true
  ingress:
    enabled: false
    listen: 127.0.0.1
    port: 47980
  tun:
    name: $TunName
    mtu: 1500
    ipv4: $TunIPv4
    ipv6: ""
    auto_route: false
    strict_route: false
  dns:
    enabled: false
    listen: 127.0.0.1
    port: 47953
    upstreams: ["223.5.5.5:53"]
    cache_ttl: 60
    hosts: {}
    block: []
  egress:
    interface: ""
    local_addr: ""
    mark: 0
"@
Set-Content -LiteralPath $cfgPath -Value $cfg -Encoding UTF8

$base = "http://127.0.0.1:$Port/api/v1"
$headers = @{ Authorization = "Bearer $token" }
$proc = $null
$routeAdded = $false
$attached = $false

function Wait-Health {
  for ($i = 0; $i -lt 40; $i++) {
    try {
      $r = Invoke-RestMethod -Uri "$base/health" -Headers $headers -TimeoutSec 2
      if ($r) { return $true }
    } catch { Start-Sleep -Milliseconds 500 }
  }
  return $false
}

try {
  $existing = Get-NetAdapter -Name $TunName -ErrorAction SilentlyContinue
  if ($existing) {
    Write-Host "adapter $TunName already exists, disabling it first"
    & netsh interface set interface "name=$TunName" admin=disable | Out-Null
  }

  $proc = Start-Process -FilePath $exe -ArgumentList @('-config', $cfgPath) -PassThru -WindowStyle Hidden
  if (-not (Wait-Health)) { throw 'temporary instance did not become healthy within 20s (see data\tun-verify\logs)' }
  Add-Step 'start-instance' $true ("nggd pid=" + $proc.Id + " port=" + $Port)

  $drv = Invoke-RestMethod -Uri "$base/network/tun/driver/install" -Method Post -Headers $headers -TimeoutSec 180
  Add-Step 'install-driver' ([bool]$drv.ok) ("path=" + $drv.driver.path + " version=" + $drv.driver.version + " signature=" + $drv.driver.signature)

  $before = Invoke-RestMethod -Uri "$base/network/tun/status" -Headers $headers
  Add-Step 'capability' ([bool]$before.capabilities.tun_supported) ("admin=" + $before.capabilities.admin + " driver_signed=" + $before.capabilities.driver.signed)

  $attach = Invoke-RestMethod -Uri "$base/network/tun/attach" -Method Post -Headers $headers -Body '{"route_all":false}' -ContentType 'application/json' -TimeoutSec 60
  $attached = $true
  Add-Step 'attach-tun' ([bool]$attach.ok) ("adapter=" + $attach.adapter.adapter + " ipv4=" + $attach.adapter.ipv4)

  Start-Sleep -Seconds 1
  $nic = Get-NetAdapter -Name $TunName -ErrorAction SilentlyContinue
  if ($nic) {
    Add-Step 'adapter-present' $true ("name=" + $nic.Name + " status=" + $nic.Status + " desc=" + $nic.InterfaceDescription)
  } else {
    Add-Step 'adapter-present' $false 'Get-NetAdapter did not find the adapter'
  }

  $addrOut = & netsh interface ipv4 show addresses "name=$TunName" | Out-String
  $addrOk = $addrOut.Contains($tunIP)
  Add-Step 'adapter-address' $addrOk (OneLine $addrOut)

  & netsh interface ipv4 add route "$ProbeIP/32" "interface=$TunName" "nexthop=$tunIP" metric=1 store=active | Out-Null
  $routeOk = ($LASTEXITCODE -eq 0)
  $routeAdded = $routeOk
  Add-Step 'add-probe-route' $routeOk ("route " + $ProbeIP + "/32 -> " + $TunName)

  $code = ''
  $curlOut = & curl.exe -s -o NUL -w '%{http_code}' -m 12 "http://$ProbeIP/"
  if ($LASTEXITCODE -eq 0) { $code = ($curlOut | Out-String).Trim() }
  $httpOk = $false
  if ($code -match '^(200|204|301|302|403|404)$') { $httpOk = $true }
  Add-Step 'tunnel-http' $httpOk ("GET http://" + $ProbeIP + "/ -> " + $code)

  Start-Sleep -Seconds 1
  $after = Invoke-RestMethod -Uri "$base/network/tun/status" -Headers $headers
  $stats = $after.adapter.stats
  $trafficOk = $false
  if (([int64]$stats.packets_in -gt 0) -and ([int64]$stats.packets_out -gt 0)) { $trafficOk = $true }
  Add-Step 'tun-counters' $trafficOk ("packets_in=" + $stats.packets_in + " packets_out=" + $stats.packets_out + " bytes_in=" + $stats.bytes_in + " sessions=" + $stats.sessions)
}
catch {
  Add-Step 'exception' $false $_.Exception.Message
}
finally {
  if ($routeAdded) {
    & netsh interface ipv4 delete route "$ProbeIP/32" "interface=$TunName" | Out-Null
    Write-Host "[cleanup] removed $ProbeIP/32 route"
  }
  if ($attached) {
    try {
      Invoke-RestMethod -Uri "$base/network/tun/detach" -Method Post -Headers $headers -TimeoutSec 30 | Out-Null
      Write-Host '[cleanup] adapter destroyed'
    } catch {
      Write-Host ("[cleanup] detach failed: " + $_.Exception.Message)
    }
  }
  if ($proc -and -not $proc.HasExited) {
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Write-Host '[cleanup] temporary instance stopped'
  }
}

$failed = @($steps | Where-Object { -not $_.ok })
$report = [ordered]@{
  generated_at = (Now)
  host = $env:COMPUTERNAME
  os = (Get-CimInstance Win32_OperatingSystem).Caption
  tun_name = $TunName
  probe_ip = $ProbeIP
  passed = ($failed.Count -eq 0)
  failed_steps = $failed.Count
  steps = $steps
}
$report | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $reportPath -Encoding UTF8

Write-Host ''
if ($failed.Count -eq 0) {
  Write-Host 'ALL PASS: the TUN adapter was really created and traffic went through the userspace stack + dispatcher.' -ForegroundColor Green
} else {
  Write-Host ("{0} step(s) failed, see {1}" -f $failed.Count, $reportPath) -ForegroundColor Yellow
}
Write-Host "report: $reportPath"
if ($failed.Count -ne 0) { exit 1 }
