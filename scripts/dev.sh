#!/usr/bin/env bash
#
# 一键开发脚本：编译并启动服务端 + 桌面客户端，并交叉编译 Windows 同步 CLI。
#
#   - 构建 Web 控制台并编译服务端，后台启动（日志见 runtime/dev-server.log）
#   - 交叉编译 Windows 无头同步 CLI → bin/clipbridge-cli.exe
#   - 构建 macOS 客户端 .app 并启动（系统通知需要 .app）
#   - Ctrl-C 退出时自动停止后台服务端（桌面客户端请用其托盘菜单退出）
#
# 用法（仓库根目录）：  ./scripts/dev.sh
# 选项：  --no-client  只起服务端、只交叉编译，不启动桌面客户端
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
DATA_DIR="$ROOT/runtime"
SERVER_LOG="$DATA_DIR/dev-server.log"
START_CLIENT=1
[ "${1:-}" = "--no-client" ] && START_CLIENT=0

mkdir -p "$DATA_DIR"

# 退出时停止后台服务端。
SERVER_PID=""
cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "==> 停止服务端 (pid $SERVER_PID)"
    kill "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

echo "==> [1/4] 构建 Web 控制台并编译服务端"
(cd server/web && npm install --silent && npm run build >/dev/null)
go build -o bin/clipbridge-server ./server/cmd/clipbridge-server

echo "==> [2/4] 交叉编译 Windows 同步 CLI"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bin/clipbridge-cli.exe ./client/cmd/clipbridge-cli
echo "    产出 bin/clipbridge-cli.exe（拷到 Windows，clipbridge-cli.exe -server https://<本机IP>:8443 -code <配对码>）"

echo "==> [3/4] 启动服务端（后台，日志 ${SERVER_LOG} ）"
: > "$SERVER_LOG"
"$ROOT/bin/clipbridge-server" -data-dir "$DATA_DIR" >>"$SERVER_LOG" 2>&1 &
SERVER_PID=$!

# 等待 Web 端口就绪。
for _ in $(seq 1 50); do
  if curl -fsS http://localhost:8080/healthz >/dev/null 2>&1; then break; fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then echo "服务端启动失败，见 $SERVER_LOG"; exit 1; fi
  sleep 0.2
done
echo "    服务端就绪：Web http://localhost:8080  设备端口 :8443"
# 首次启动会打印一次管理员凭据与证书指纹。
grep -E "初始管理员凭据|fingerprint_sha256" "$SERVER_LOG" || \
  echo "    （非首次启动，未重复输出管理员凭据；忘记密码用 -reset-admin-password）"

if [ "$START_CLIENT" = "1" ]; then
  echo "==> [4/4] 构建并启动 macOS 客户端"
  (cd client && ./build-macos-app.sh >/dev/null)
  open "$ROOT/client/bin/ClipBridge.app"
  echo "    已启动 ClipBridge.app（菜单栏托盘）"
else
  echo "==> [4/4] 跳过桌面客户端（--no-client）"
fi

echo
echo "服务端运行中。按 Ctrl-C 停止服务端并退出脚本（桌面客户端请用托盘菜单退出）。"
echo "==> 实时服务端日志："
tail -f "$SERVER_LOG"
