#!/usr/bin/env bash
# 一键编译：服务端（含内嵌 Web 控制台）+ macOS 桌面客户端 + Windows 客户端。
# 结束后打印所有产物的绝对路径。
#
# 用法：在仓库任意位置执行  ./scripts/build-all.sh
# 说明：
#   - 服务端：先构建 server/web 前端（go:embed 嵌入），再编译服务端二进制。
#   - macOS 客户端：组装 .app（仅在 macOS 上构建；其它平台跳过）。
#   - Windows 客户端：CGO-free 交叉编译，任意平台均可产出 .exe。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

SERVER_BIN="$ROOT/bin/clipbridge-server"
MAC_APP="$ROOT/client/bin/ClipBridge.app"
WIN_EXE="$ROOT/client/bin/ClipBridge.exe"
mac_built=""
results=()

echo "==> [1/3] 构建服务端 Web 控制台"
(cd "$ROOT/server/web" && npm install --silent && npm run build)

echo "==> 编译服务端二进制"
mkdir -p "$ROOT/bin"
go build -o "$SERVER_BIN" ./server/cmd/clipbridge-server
results+=("服务端           : $SERVER_BIN")

echo "==> [2/3] 构建 macOS 客户端"
if [[ "$(uname -s)" == "Darwin" ]]; then
  (cd "$ROOT/client" && ./build-macos-app.sh)
  mac_built="1"
  results+=("macOS 客户端 (.app): $MAC_APP")
else
  echo "    （非 macOS，跳过 .app 构建）"
fi

echo "==> [3/3] 交叉编译 Windows 客户端"
(cd "$ROOT/client" && ./build-windows.sh)
results+=("Windows 客户端    : $WIN_EXE")

echo
echo "============================ 构建完成 ============================"
for line in "${results[@]}"; do
  echo "  $line"
done
if [[ -z "$mac_built" ]]; then
  echo "  （macOS 客户端未构建：请在 macOS 上运行本脚本以生成 .app）"
fi
echo "================================================================="
