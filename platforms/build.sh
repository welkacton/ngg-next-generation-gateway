#!/usr/bin/env bash
# NGG 跨平台交叉编译脚本（仅纯 Go 目标，CGO_ENABLED=0）。
#
# 生成物输出到仓库根目录的 bin/platforms/ 下，文件名为
#   nggd-<GOOS>-<GOARCH>[.exe]
#
# 用法：
#   ./platforms/build.sh
#       构建下方 DEFAULT_TARGETS 中的 6 个默认目标。
#   ./platforms/build.sh linux/arm linux/mipsle windows/arm64 ...
#       只构建命令行给出的目标（格式 GOOS/GOARCH）。
#
# 全部可构建的纯 Go 目标：
#   linux/amd64 linux/arm64 linux/386 linux/arm linux/mipsle
#   windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 freebsd/amd64
#
# android/arm64 与 ios/arm64 不属于本脚本范围，请使用 build-mobile.sh
# （需要 gomobile + Android NDK / Xcode 等外部 SDK）。
#
# 设计要点：使用 `set -u`，但刻意不使用 `set -e`，这样单个目标构建失败
# 不会中止其余目标；循环结束后统一打印成功/失败汇总。

set -u
set -o pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

OUT_DIR="$ROOT/bin/platforms"
mkdir -p "$OUT_DIR"

# 6 个默认目标：覆盖主要服务器 / 桌面平台。其余目标可通过命令行追加传入。
DEFAULT_TARGETS=(
  linux/amd64
  linux/arm64
  windows/amd64
  darwin/amd64
  darwin/arm64
  freebsd/amd64
)

if [[ $# -gt 0 ]]; then
  TARGETS=("$@")
else
  TARGETS=("${DEFAULT_TARGETS[@]}")
fi

if ! command -v go >/dev/null 2>&1; then
  echo "[ERROR] 未找到 go 命令，无法交叉编译，请先安装 Go（见 go.mod 的版本要求）。" >&2
  exit 1
fi

total=0
ok=0
failed=0
failed_list=()

echo "==> 仓库根目录 : $ROOT"
echo "==> 输出目录   : $OUT_DIR"
echo "==> 目标数量   : ${#TARGETS[@]}"
echo

for target in "${TARGETS[@]}"; do
  total=$((total + 1))

  # 目标必须写成 GOOS/GOARCH。
  if [[ "$target" != */* || "$target" == */ || "$target" == /* ]]; then
    echo "[FAIL] $target : 目标格式应为 GOOS/GOARCH（例如 linux/amd64）"
    failed=$((failed + 1))
    failed_list+=("$target")
    continue
  fi

  os="${target%/*}"
  arch="${target#*/}"
  out="$OUT_DIR/nggd-${os}-${arch}"
  [[ "$os" == "windows" ]] && out="${out}.exe"

  echo "[....] $target -> bin/platforms/$(basename "$out")"
  if GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
      go build -trimpath -ldflags="-s -w" -o "$out" ./cmd/nggd 2>&1; then
    echo "[ OK ] $target"
    ok=$((ok + 1))
  else
    # 不退出，继续构建下一个目标。
    echo "[FAIL] $target （失败原因见上方 go build 输出）"
    failed=$((failed + 1))
    failed_list+=("$target")
  fi
done

echo
echo "================== 构建汇总 =================="
echo "总目标数 : $total"
echo "成功     : $ok"
echo "失败     : $failed"
if [[ $failed -gt 0 ]]; then
  printf '失败目标 : %s\n' "${failed_list[*]}"
fi
echo "生成物   : $OUT_DIR"
echo "=============================================="

# 任一目标失败返回非零，但此前已尝试构建全部目标。
if [[ $failed -gt 0 ]]; then
  exit 1
fi
exit 0
