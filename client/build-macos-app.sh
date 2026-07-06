#!/usr/bin/env bash
# 构建 macOS 客户端为 .app 包。系统级通知要求以带 bundle identifier 的 .app 运行，
# 裸二进制无法发送系统通知。本脚本编译前端 + Go，并组装 ClipBridge.app。
#
# 用法：在 client/ 目录执行  ./build-macos-app.sh
# 产物：client/bin/ClipBridge.app  （用 `open client/bin/ClipBridge.app` 启动）
#
# 可选环境变量（CI 发布时使用，本地默认无需设置）：
#   APP_VERSION   版本号，注入二进制与 Info.plist；默认 0.1.0
#   TARGET_ARCH   目标架构：arm64 | amd64 | universal；默认当前机器架构
#   SKIP_FRONTEND 非空则跳过前端构建（前端已在外部构建好时使用）
set -euo pipefail

cd "$(dirname "$0")"
APP="bin/ClipBridge.app"
BUNDLE_ID="com.clipbridge.desktop"
APP_VERSION="${APP_VERSION:-0.1.0}"
TARGET_ARCH="${TARGET_ARCH:-$(go env GOARCH)}"
# Info.plist 的版本号字段需为纯数字版本，剥掉可能的前导 v
PLIST_VERSION="${APP_VERSION#v}"

if [ -z "${SKIP_FRONTEND:-}" ]; then
  echo "==> 构建前端"
  (cd frontend && npm install --silent && npm run build)
else
  echo "==> 跳过前端构建（SKIP_FRONTEND）"
fi

echo "==> 编译 Go 二进制（arch=${TARGET_ARCH}, version=${APP_VERSION}）"
# darwin 剪贴板适配依赖 cgo（Cocoa），编译必须开启 CGO
LDFLAGS="-X main.version=${APP_VERSION}"
build_arch() {
  # 按指定架构编译单架构二进制到 $1
  CGO_ENABLED=1 GOOS=darwin GOARCH="$2" go build -ldflags "${LDFLAGS}" -o "$1" .
}
mkdir -p bin
if [ "$TARGET_ARCH" = "universal" ]; then
  build_arch bin/clipbridge-arm64 arm64
  build_arch bin/clipbridge-amd64 amd64
  lipo -create -output bin/clipbridge bin/clipbridge-arm64 bin/clipbridge-amd64
  rm -f bin/clipbridge-arm64 bin/clipbridge-amd64
else
  build_arch bin/clipbridge "$TARGET_ARCH"
fi

echo "==> 组装 $APP"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp bin/clipbridge "$APP/Contents/MacOS/clipbridge"

# 尝试用 logo.png 生成 .icns（需要 sips/iconutil；失败则跳过，不影响通知）。
if command -v sips >/dev/null && command -v iconutil >/dev/null; then
  ICONSET="$(mktemp -d)/icon.iconset"
  mkdir -p "$ICONSET"
  for sz in 16 32 64 128 256 512; do
    sips -z $sz $sz assets/logo.png --out "$ICONSET/icon_${sz}x${sz}.png" >/dev/null 2>&1 || true
    sips -z $((sz*2)) $((sz*2)) assets/logo.png --out "$ICONSET/icon_${sz}x${sz}@2x.png" >/dev/null 2>&1 || true
  done
  iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/icon.icns" >/dev/null 2>&1 || true
fi

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>剪驿</string>
  <key>CFBundleDisplayName</key><string>剪驿</string>
  <key>CFBundleIdentifier</key><string>${BUNDLE_ID}</string>
  <key>CFBundleExecutable</key><string>clipbridge</string>
  <key>CFBundleVersion</key><string>${PLIST_VERSION}</string>
  <key>CFBundleShortVersionString</key><string>${PLIST_VERSION}</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleIconFile</key><string>icon</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <!-- 菜单栏常驻、无 Dock 图标 -->
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

# 临时 ad-hoc 签名，便于本机授予通知权限（无开发者证书时使用 - 签名）。
codesign --force --deep --sign - "$APP" >/dev/null 2>&1 || echo "（codesign 跳过，未签名也可本机测试）"

echo "==> 完成：$APP"
echo "启动：open $APP   （首次会请求通知权限）"
