package nodedeploy

// The node-side probe.
//
// This is the Nezha-probe model applied to a proxy node: a small process ON the
// node opens an outbound HTTP connection to the panel and pushes its own real
// numbers. The panel never dials in, so there is no inbound management port and
// no SSH dependency for reading node state.
//
// Why shell and not a second Go binary: the node already needs nothing but
// curl, and shipping a Linux binary would mean a release asset that cannot be
// an .exe (the release only publishes .exe), plus a GitHub download the node
// may not be able to reach. /proc and systemctl are the same sources the Go
// agent uses on Linux, so the numbers are equally real.
//
// Everything sent is measured, never remembered: the probe reads /proc/stat,
// /proc/meminfo, /proc/net/dev, /proc/uptime, df, and the node's own manifest
// + kernel binary on every tick.

// probeConfDelimiter terminates the agent.conf heredoc.
const probeConfDelimiter = "NGG_PROBE_CONF"

// probeDelimiter terminates the probe heredoc.
const probeDelimiter = "NGG_PROBE_BODY"

// probeTpl is installed as NodeProbeBin. It must stay POSIX sh (dash on
// Debian/Ubuntu), must never use backticks, and must survive set -u.
const probeTpl = `#!/bin/sh
# ============================================================================
# ngg-probe — NGG 节点探针（由 NGG 部署脚本安装，重复部署会覆盖本文件）
#
# 只做出站连接：每 INTERVAL 秒把本机真实指标 + 节点状态 POST 到
#   $PANEL_URL/v1/agent/report
# 面板从不主动连本机：不需要 SSH，不需要入站管理端口。
#
# 配置：{{PROBE_CONF}}（0600）
# 手动调试：
#   NGG_PROBE_ONCE=1 {{PROBE_BIN}}
# ============================================================================
set -u
umask 077

CONF=${NGG_PROBE_CONF:-{{PROBE_CONF}}}
if [ ! -r "$CONF" ]; then
  printf 'ngg-probe: 读不到配置 %s\n' "$CONF" >&2
  exit 1
fi
# shellcheck disable=SC1090
. "$CONF"

: "${PANEL_URL:=}"
: "${AGENT_ID:=}"
: "${AGENT_TOKEN:=}"
: "${INTERVAL:=10}"
: "${NODE_MANIFEST:=/etc/ngg/node.json}"
BASE=${PANEL_URL%/}
STATE=/run/ngg-probe.state
BODY=/run/ngg-probe.body
PROBE_VERSION='{{NGG_VERSION}}'

log() { printf '%s ngg-probe: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2; }
now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

if [ -z "$BASE" ] || [ -z "$AGENT_ID" ] || [ -z "$AGENT_TOKEN" ]; then
  log "配置不完整（PANEL_URL / AGENT_ID / AGENT_TOKEN 必填），退出"
  exit 1
fi

# ---------------------------------------------------------------- JSON 工具
# Only the two characters JSON requires are escaped; control bytes are dropped.
jstr() {
  printf '%s' "${1:-}" | tr -d '\000-\037' | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

# field/nfield read one value out of the node manifest written by the deploy.
field() {
  sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$NODE_MANIFEST" 2>/dev/null | head -1
}
nfield() {
  sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$NODE_MANIFEST" 2>/dev/null | head -1
}

# ---------------------------------------------------------------- 指标采集
# read_cpu prints "total_ticks idle_ticks"; read_net prints "rx tx" (lo excluded).
read_cpu() {
  awk '/^cpu /{ t = 0; for (i = 2; i <= NF; i++) t += $i; printf "%d %d", t, $5 + $6; exit }' /proc/stat 2>/dev/null
}
read_net() {
  awk '{ line = $0; sub(/^[ \t]+/, "", line); if (index(line, ":") == 0) next
         split(line, p, ":"); if (p[1] == "lo") next
         n = split(p[2], f, /[ \t]+/); if (n < 10) next
         rx += f[2]; tx += f[10] }
       END { printf "%d %d", rx + 0, tx + 0 }' /proc/net/dev 2>/dev/null
}

# iface_json emits the per-interface array the console renders.
iface_json() {
  out=""
  for d in /sys/class/net/*; do
    [ -e "$d" ] || continue
    nm=${d##*/}
    case "$nm" in lo|'') continue ;; esac
    mac=$(cat "$d/address" 2>/dev/null || printf '')
    mtu=$(cat "$d/mtu" 2>/dev/null || printf '0')
    up=false
    [ "$(cat "$d/operstate" 2>/dev/null || printf '')" = "up" ] && up=true
    pair=$(awk -v want="$nm" '{ line = $0; sub(/^[ \t]+/, "", line)
             if (index(line, ":") == 0) next
             split(line, p, ":"); gsub(/[ \t]/, "", p[1]); if (p[1] != want) next
             n = split(p[2], f, /[ \t]+/); printf "%d %d", f[2] + 0, f[10] + 0; exit }' /proc/net/dev 2>/dev/null)
    rx=${pair% *}; tx=${pair#* }
    addrs=""
    if command -v ip >/dev/null 2>&1; then
      addrs=$(ip -o addr show dev "$nm" 2>/dev/null | awk '{ printf "%s%s", sep, $4; sep = "," }')
    fi
    [ -n "$out" ] && out="$out,"
    out="$out{\"name\":\"$(jstr "$nm")\",\"mac\":\"$(jstr "$mac")\",\"mtu\":${mtu:-0},\"up\":$up,\"addresses\":[$(printf '%s' "$addrs" | awk -F, '{ for (i = 1; i <= NF; i++) if ($i != "") printf "%s\"%s\"", (i > 1 ? "," : ""), $i }')],\"rx_bytes\":${rx:-0},\"tx_bytes\":${tx:-0},\"rx_rate\":0,\"tx_rate\":0}"
  done
  printf '[%s]' "$out"
}

# disk_facts prints "fstype total used" for the root filesystem, in bytes.
# df -PkT columns: Filesystem Type 1024-blocks Used Available Capacity Mounted.
disk_facts() {
  df -PkT / 2>/dev/null | awk 'NR == 2 { printf "%s %d %d", $2, $3 * 1024, $4 * 1024 }'
}

# node_json emits the node section, or nothing when this machine has no
# manifest (a plain metrics agent), so the panel can tell "no node here" from
# "node is down".
node_json() {
  [ -r "$NODE_MANIFEST" ] || return 0
  nid=$(field node_id); mode=$(field mode); proto=$(field protocol); port=$(nfield port)
  unit=$(field service); cfgp=$(field config_path); kbin=$(field kernel_bin)
  listen=false
  if [ -n "$port" ]; then
    if command -v ss >/dev/null 2>&1; then
      ss -lntH 2>/dev/null | awk '{ print $4 }' | grep -qE "[:.]${port}$" && listen=true
    else
      hexp=$(printf '%04X' "$port" 2>/dev/null || printf '')
      if [ -n "$hexp" ]; then
        awk -v h=":$hexp" '$4 == "0A" && substr($2, length($2) - 4) == h { f = 1 } END { exit !f }' \
          /proc/net/tcp /proc/net/tcp6 2>/dev/null && listen=true
      fi
    fi
  fi
  ustate="unknown"
  if [ -n "$unit" ]; then
    ustate=$(systemctl is-active "$unit" 2>/dev/null) || true
    [ -n "$ustate" ] || ustate="unknown"
  fi
  csha=""
  [ -r "$cfgp" ] && csha=$(sha256sum "$cfgp" 2>/dev/null | cut -d' ' -f1)
  kver=""
  if [ -n "$kbin" ] && [ -x "$kbin" ]; then
    kver=$("$kbin" version 2>/dev/null | head -1)
  fi
  printf '{"node_id":"%s","mode":"%s","protocol":"%s","port":%s,"unit":"%s","unit_state":"%s","listening":%s,"config_path":"%s","config_sha256":"%s","kernel_version":"%s","collected_at":"%s"}' \
    "$(jstr "$nid")" "$(jstr "$mode")" "$(jstr "$proto")" "${port:-0}" "$(jstr "$unit")" \
    "$(jstr "$ustate")" "$listen" "$(jstr "$cfgp")" "$(jstr "$csha")" "$(jstr "$kver")" "$(now)"
}

# ---------------------------------------------------------------- 上报
report() {
  # 上一采样点（首次运行时为空，留 1 秒基线，避免把 0% 当成真实值）
  P_CPU=""; P_IDLE=""; P_RX=""; P_TX=""; P_TS=""
  if [ -r "$STATE" ]; then
    read -r P_CPU P_IDLE P_RX P_TX P_TS < "$STATE" || true
  fi

  set -- $(read_cpu); C_TOTAL=${1:-0}; C_IDLE=${2:-0}
  set -- $(read_net); C_RX=${1:-0}; C_TX=${2:-0}
  C_TS=$(date +%s)

  if [ -z "${P_TS:-}" ]; then
    P_CPU=$C_TOTAL; P_IDLE=$C_IDLE; P_RX=$C_RX; P_TX=$C_TX; P_TS=$C_TS
    sleep 1
    set -- $(read_cpu); C_TOTAL=${1:-0}; C_IDLE=${2:-0}
    set -- $(read_net); C_RX=${1:-0}; C_TX=${2:-0}
    C_TS=$(date +%s)
  fi

  DT=$((C_TS - P_TS)); [ "$DT" -le 0 ] && DT=1
  D_TOTAL=$((C_TOTAL - P_CPU)); D_IDLE=$((C_IDLE - P_IDLE))
  CPU_MILLE=0
  if [ "$D_TOTAL" -gt 0 ]; then
    CPU_MILLE=$(( (1000 * (D_TOTAL - D_IDLE)) / D_TOTAL ))
    [ "$CPU_MILLE" -lt 0 ] && CPU_MILLE=0
    [ "$CPU_MILLE" -gt 1000 ] && CPU_MILLE=1000
  fi

  RATES=$(awk -v a="$C_RX" -v b="$P_RX" -v c="$C_TX" -v d="$P_TX" -v t="$DT" \
    'BEGIN { if (t <= 0) t = 1; printf "%.2f %.2f", (a - b) / t, (c - d) / t }')
  RX_RATE=${RATES% *}; TX_RATE=${RATES#* }

  MEM=$(awk '/^MemTotal:/{ t = $2 } /^MemAvailable:/{ a = $2 } /^MemFree:/{ f = $2 }
             /^SwapTotal:/{ st = $2 } /^SwapFree:/{ sf = $2 }
             END { if (t == "") t = 0; if (a == "") a = f + 0;
                   printf "%d %d %d %d", t * 1024, (t - a) * 1024, st * 1024, (st - sf) * 1024 }' /proc/meminfo 2>/dev/null)
  set -- $MEM; MEM_TOTAL=${1:-0}; MEM_USED=${2:-0}; SWAP_TOTAL=${3:-0}; SWAP_USED=${4:-0}

  set -- $(disk_facts); DSK_TYPE=${1:-unknown}; DSK_TOTAL=${2:-0}; DSK_USED=${3:-0}

  CORES=$(nproc 2>/dev/null) || CORES=""
  [ -n "${CORES:-}" ] || CORES=$(grep -c '^processor' /proc/cpuinfo 2>/dev/null || printf '0')
  LOAD1=$(cut -d' ' -f1 /proc/loadavg 2>/dev/null || printf '0')
  UPTIME=$(cut -d. -f1 /proc/uptime 2>/dev/null || printf '0')
  ARCH=$(uname -m 2>/dev/null | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')

  METRICS=$(printf '{"cpu_pct":%d.%d,"cpu_cores":%s,"load1":%s,"mem_total":%s,"mem_used":%s,"swap_total":%s,"swap_used":%s,"disk_total":%s,"disk_used":%s,"disks":[{"mount":"/","fstype":"%s","total":%s,"used":%s}],"interfaces":%s,"rx_bytes":%s,"tx_bytes":%s,"rx_rate":%s,"tx_rate":%s,"uptime_sec":%s,"sampled_at":"%s","source":"ngg-probe"}' \
    $((CPU_MILLE / 10)) $((CPU_MILLE % 10)) "${CORES:-0}" "${LOAD1:-0}" \
    "$MEM_TOTAL" "$MEM_USED" "$SWAP_TOTAL" "$SWAP_USED" "$DSK_TOTAL" "$DSK_USED" \
    "$(jstr "$DSK_TYPE")" "$DSK_TOTAL" "$DSK_USED" "$(iface_json)" \
    "$C_RX" "$C_TX" "${RX_RATE:-0}" "${TX_RATE:-0}" "${UPTIME:-0}" "$(now)")

  NODE=$(node_json)
  if [ -n "$NODE" ]; then
    printf '{"agent_id":"%s","hostname":"%s","os":"%s","arch":"%s","agent_version":"%s","interval_sec":%s,"metrics":%s,"node":%s}' \
      "$(jstr "$AGENT_ID")" "$(jstr "$(hostname 2>/dev/null || printf unknown)")" \
      "$(uname -s 2>/dev/null | tr 'A-Z' 'a-z')" "$(jstr "$ARCH")" "$PROBE_VERSION" \
      "${INTERVAL:-10}" "$METRICS" "$NODE" > "$BODY"
  else
    printf '{"agent_id":"%s","hostname":"%s","os":"%s","arch":"%s","agent_version":"%s","interval_sec":%s,"metrics":%s}' \
      "$(jstr "$AGENT_ID")" "$(jstr "$(hostname 2>/dev/null || printf unknown)")" \
      "$(uname -s 2>/dev/null | tr 'A-Z' 'a-z')" "$(jstr "$ARCH")" "$PROBE_VERSION" \
      "${INTERVAL:-10}" "$METRICS" > "$BODY"
  fi

  CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 \
    -H 'Content-Type: application/json' \
    -H "X-NGG-Agent-Token: $AGENT_TOKEN" \
    --data-binary @"$BODY" "$BASE/v1/agent/report" 2>/dev/null) || CODE=000
  printf '%s %s %s %s %s\n' "$C_TOTAL" "$C_IDLE" "$C_RX" "$C_TX" "$C_TS" > "$STATE"

  case "$CODE" in
    200)
      log "上报成功 cpu=${CPU_MILLE}‰ rx=${C_RX} tx=${C_TX}"
      return 0
      ;;
    401)
      log "面板拒绝令牌（401）：该节点已被删除或令牌已轮换，请在控制台重新部署"
      ;;
    000)
      log "连接不上 ${BASE}：检查面板端口 / 安全组 / URL"
      ;;
    *)
      log "面板返回 HTTP $CODE"
      ;;
  esac
  return 1
}

if [ "${NGG_PROBE_ONCE:-0}" = "1" ]; then
  report && exit 0
  exit 1
fi

log "启动：agent_id=$AGENT_ID panel=$BASE interval=${INTERVAL}s version=$PROBE_VERSION"
backoff=1
while :; do
  if report; then
    backoff=1
    sleep "${INTERVAL:-10}"
  else
    sleep "$backoff"
    backoff=$((backoff * 2))
    [ "$backoff" -gt 60 ] && backoff=60
  fi
done
`
