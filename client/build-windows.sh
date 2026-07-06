#!/usr/bin/env bash
# 从 macOS/Linux 交叉编译 Windows 便携客户端（CGO-free）。
# Wails v3 的 Windows 后端是纯 Go（go-winloader 加载 WebView2），因此无需 mingw，
# 直接 CGO_ENABLED=0 GOOS=windows 即可产出单文件 .exe。
#
# 用法：在 client/ 目录执行  ./build-windows.sh
# 产物：client/bin/ClipBridge.exe
# 运行要求：目标机需安装 WebView2 运行时（Windows 11 内置；Windows 10 需安装一次）。
#
# 可选环境变量（CI 发布时使用）：
#   APP_VERSION   版本号，注入二进制；默认 0.1.0
#   TARGET_ARCH   目标架构：amd64 | arm64；默认 amd64
#   SKIP_FRONTEND 非空则跳过前端构建
set -euo pipefail

cd "$(dirname "$0")"
OUT="bin/ClipBridge.exe"
APP_VERSION="${APP_VERSION:-0.1.0}"
TARGET_ARCH="${TARGET_ARCH:-amd64}"

if [ -z "${SKIP_FRONTEND:-}" ]; then
  echo "==> 构建前端"
  (cd frontend && npm install --silent && npm run build)
else
  echo "==> 跳过前端构建（SKIP_FRONTEND）"
fi

echo "==> 生成 Windows 资源（图标置于资源 ID 3 + 版本信息 + GUI 清单）"
# 关键：Wails 的 Windows 系统通知从 .exe 的图标资源 ID 3（RT_ICON=3）提取应用图标。
# 必须用 Wails 自带的 generate syso —— 它的 SetIcon(RT_ICON) 正好把图标组放在 ID 3，
# 与通知代码一致；旧的 go-winres simply 放在别的 ID，会导致通知中心不显示应用图标。
# 用 go run 固定到与库一致的版本，保持脚本自包含（无需预装 wails3）。
RES_VERSION="${APP_VERSION#v}"
WAILS3="go run github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha2.106"
ICO="build/windows/icon.ico"
# 1) 由 PNG 生成多尺寸 .ico（generate syso 需要 .ico，不接受 png）
# Linux runner 只借用 Wails CLI 生成 Windows 资源；关闭 CGO，避免加载宿主 GTK/WebKitGTK。
CGO_ENABLED=0 $WAILS3 generate icons -input assets/logo.png -windowsfilename "$ICO"
# 2) 写入版本信息（注入版本号，供 Explorer「文件属性」显示）
cat > build/windows/info.json <<INFO
{
  "fixed": { "file_version": "${RES_VERSION}" },
  "info": { "0000": {
    "ProductVersion": "${RES_VERSION}",
    "ProductName": "剪驿 ClipBridge",
    "FileDescription": "剪驿 ClipBridge 剪贴板同步",
    "CompanyName": "ClipBridge",
    "LegalCopyright": "© ClipBridge"
  } }
}
INFO
# 3) 生成 rsrc_windows_<arch>.syso（图标 ID 3 + GUI 清单 + 版本），go build 自动链接
CGO_ENABLED=0 $WAILS3 generate syso -arch "${TARGET_ARCH}" -icon "$ICO" \
  -manifest build/windows/wails.exe.manifest -info build/windows/info.json

echo "==> 交叉编译 Windows GUI（CGO-free，windowsgui 子系统不弹控制台；arch=${TARGET_ARCH}, version=${APP_VERSION}）"
CGO_ENABLED=0 GOOS=windows GOARCH="${TARGET_ARCH}" \
  go build -ldflags "-H=windowsgui -s -w -X main.version=${APP_VERSION}" -o "$OUT" .

# 资源对象文件与生成的图标/版本信息是构建产物，编译完即清理（避免污染源码树/被误链接）。
rm -f rsrc_windows_*.syso "$ICO" build/windows/info.json

echo "==> 完成：$OUT"
echo "拷贝到 Windows 机器双击运行（需 WebView2 运行时）。"
