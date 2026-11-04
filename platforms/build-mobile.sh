#!/usr/bin/env bash
# 用 gomobile 把共享 Go 核心 internal/mobile 绑定成：
#   - Android : bin/platforms/mobile/ngg.aar
#   - iOS     : bin/platforms/mobile/NGG.xcframework
#
# 重要：本脚本会先检测 gomobile / Android NDK / Xcode 是否具备。
# 缺少任一工具链时，明确打印跳过原因并以退出码 0 结束，不生成任何占位文件，
# 也不假装构建成功。只有工具链齐备时才真正调用 gomobile bind。
#
# 用法：
#   ./platforms/build-mobile.sh
#
# 工具链准备（示例）：
#   go install golang.org/x/mobile/cmd/gomobile@latest
#   gomobile init
#   export ANDROID_HOME=/path/to/android-sdk
#   export ANDROID_NDK_HOME=$ANDROID_HOME/ndk/<version>
# iOS 目标只能在 macOS 上安装 Xcode 后构建。

set -u
set -o pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

OUT_DIR="$ROOT/bin/platforms/mobile"

missing_gomobile=""
missing_ndk=""
missing_xcode=""

echo "==> 检测移动端绑定工具链"

# 1) gomobile
if command -v gomobile >/dev/null 2>&1; then
  :
else
  missing_gomobile="未找到 gomobile：先执行 go install golang.org/x/mobile/cmd/gomobile@latest 并运行 gomobile init"
fi

# 2) Android NDK（gomobile -target=android 依赖 Android SDK + NDK）
if [[ -n "${ANDROID_NDK_HOME:-}" && -d "${ANDROID_NDK_HOME}" ]] \
   || [[ -n "${ANDROID_NDK_ROOT:-}" && -d "${ANDROID_NDK_ROOT}" ]] \
   || [[ -n "${ANDROID_HOME:-}" && -d "${ANDROID_HOME}/ndk" ]]; then
  :
else
  missing_ndk="未检测到 Android NDK：请设置 ANDROID_NDK_HOME / ANDROID_NDK_ROOT，或提供 ANDROID_HOME/ndk"
fi

# 3) Xcode（iOS 绑定只能在 macOS 上用 Xcode 工具链完成）
CURRENT_OS="$(uname -s 2>/dev/null || echo unknown)"
if [[ "$CURRENT_OS" == "Darwin" ]] && command -v xcodebuild >/dev/null 2>&1; then
  :
else
  missing_xcode="未检测到 macOS + Xcode：iOS 目标需要 xcodebuild（当前系统: ${CURRENT_OS}）"
fi

echo
echo "==> 检测结果"
[[ -z "$missing_gomobile" ]] && echo "  gomobile     : 可用" || echo "  gomobile     : 缺失"
[[ -z "$missing_ndk" ]]      && echo "  Android NDK  : 可用" || echo "  Android NDK  : 缺失"
[[ -z "$missing_xcode" ]]    && echo "  Xcode(macOS) : 可用" || echo "  Xcode(macOS) : 缺失"
echo

build_android=0
build_ios=0
[[ -z "$missing_gomobile" && -z "$missing_ndk" ]]   && build_android=1
[[ -z "$missing_gomobile" && -z "$missing_xcode" ]] && build_ios=1

# 两个目标都缺少工具链：打印明确原因后退出 0（预期行为，不算失败）。
if [[ $build_android -eq 0 && $build_ios -eq 0 ]]; then
  echo "[SKIP] 移动端工具链不完整，未生成任何产物（这是预期行为，不是构建失败）。原因："
  [[ -n "$missing_gomobile" ]] && echo "  - $missing_gomobile"
  [[ -n "$missing_ndk" ]]      && echo "  - $missing_ndk"
  [[ -n "$missing_xcode" ]]    && echo "  - $missing_xcode"
  echo "[SKIP] 补齐上述工具链后重新运行本脚本，即可生成 ngg.aar / NGG.xcframework。"
  exit 0
fi

mkdir -p "$OUT_DIR"
rc=0

# ---------------- Android AAR ----------------
if [[ $build_android -eq 1 ]]; then
  echo "[....] gomobile bind -target=android -o $OUT_DIR/ngg.aar ./internal/mobile"
  if gomobile bind -target=android -o "$OUT_DIR/ngg.aar" ./internal/mobile; then
    echo "[ OK ] 已生成 $OUT_DIR/ngg.aar"
  else
    echo "[FAIL] Android AAR 构建失败（不影响 iOS 目标）"
    rc=1
  fi
else
  echo "[SKIP] Android AAR，跳过原因："
  [[ -n "$missing_gomobile" ]] && echo "  - $missing_gomobile"
  [[ -n "$missing_ndk" ]]      && echo "  - $missing_ndk"
fi

# ---------------- iOS XCFramework ----------------
if [[ $build_ios -eq 1 ]]; then
  echo "[....] gomobile bind -target=ios -o $OUT_DIR/NGG.xcframework ./internal/mobile"
  if gomobile bind -target=ios -o "$OUT_DIR/NGG.xcframework" ./internal/mobile; then
    echo "[ OK ] 已生成 $OUT_DIR/NGG.xcframework"
  else
    echo "[FAIL] iOS XCFramework 构建失败（不影响 Android 目标）"
    rc=1
  fi
else
  echo "[SKIP] iOS XCFramework，跳过原因："
  [[ -n "$missing_gomobile" ]] && echo "  - $missing_gomobile"
  [[ -n "$missing_xcode" ]]    && echo "  - $missing_xcode"
fi

echo
echo "==> 移动端产物目录: $OUT_DIR（仅在成功构建时存在文件）"

# 仅当确实执行过 gomobile bind 且失败时返回非零；因缺工具链而跳过一律返回 0。
exit "$rc"
