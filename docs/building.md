# 自行编译

本文面向需要修改源码、制作自定义构建或参与开发的用户。普通部署优先使用 GHCR 镜像和 [Releases](https://github.com/mokeyjay/ClipBridge/releases) 中的客户端产物。

## 工具链

| 依赖 | 版本 / 要求 | 用途 |
| --- | --- | --- |
| Go | 1.26+ | 三个 Go 模块、服务端与客户端 |
| Node.js | 22+ | Web 控制台与桌面客户端前端 |
| npm | 随 Node.js | 锁文件安装与前端构建 |
| macOS | 13+ | 构建 macOS 客户端 |
| Xcode Command Line Tools | 当前稳定版 | macOS cgo、`lipo`、`codesign`、图标工具 |
| Docker + Buildx | 当前稳定版 | 本地镜像与多架构镜像 |

仓库使用 [`go.work`](../go.work) 组织 `shared`、`server` 和 `client` 三个模块。

## 一次构建全部产物

```bash
git clone https://github.com/mokeyjay/ClipBridge.git
cd ClipBridge
./scripts/build-all.sh
```

产物：

| 目标 | 路径 |
| --- | --- |
| 服务端 | `bin/clipbridge-server` |
| macOS 客户端 | `client/bin/ClipBridge.app` |
| Windows 客户端 | `client/bin/ClipBridge.exe` |

macOS 客户端只会在 macOS 主机上构建；Windows 客户端为 CGO-free，可以从 macOS 或 Linux 交叉编译。

## 分组件构建

### 服务端

服务端通过 `//go:embed` 嵌入 Web 控制台，因此必须先生成 `server/web/dist`：

```bash
cd server/web
npm ci
npm run build
cd ../..

go build -o bin/clipbridge-server ./server/cmd/clipbridge-server
```

启动：

```bash
./bin/clipbridge-server -data-dir ./runtime
```

### macOS 客户端

```bash
cd client
./build-macos-app.sh
open bin/ClipBridge.app
```

脚本构建前端、编译当前架构的 Go 二进制、组装 `.app` 并进行 ad-hoc 签名。发布流水线通过 `TARGET_ARCH=universal` 分别构建 amd64 / arm64 后使用 `lipo` 合并。

可用环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `APP_VERSION` | `0.1.0` | 注入二进制与 `Info.plist` |
| `TARGET_ARCH` | 当前 Go 架构 | `amd64`、`arm64` 或 `universal` |
| `SKIP_FRONTEND` | 空 | 非空时复用已有 `client/frontend/dist` |

### Windows 客户端

```bash
cd client
TARGET_ARCH=amd64 ./build-windows.sh
```

脚本会：

1. 构建 `client/frontend`。
2. 使用固定版本的 Wails CLI 生成 `.ico` 与 Windows `.syso` 资源。
3. 以 `CGO_ENABLED=0 GOOS=windows` 编译 GUI 子系统的单文件 `ClipBridge.exe`。
4. 清理中间资源文件。

目标 Windows 10 需要 WebView2 Runtime；Windows 11 通常已内置。

## 开发模式

macOS 上可使用：

```bash
./scripts/dev.sh
```

该脚本会构建并后台启动服务端、构建 Windows 无头测试 CLI、组装并启动 macOS `.app`，随后持续输出服务端日志。只启动服务端时：

```bash
./scripts/dev.sh --no-client
```

服务端开发数据写入仓库根目录的 `runtime/`。

## 前端

Web 控制台：

```bash
cd server/web
npm ci
npm run build
```

桌面客户端：

```bash
cd client/frontend
npm ci
npm run build
```

两个 `build` 命令都包含 TypeScript 类型检查。

## 重新生成 Wails bindings

仅当 `client/internal/guiservice` 的导出方法或 DTO 签名变化时执行：

```bash
cd client
go run github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha2.106 \
  generate bindings -d frontend/bindings -ts
cd frontend
npm run build
```

Wails CLI 版本应与 [`client/go.mod`](../client/go.mod) 固定的库版本一致。

## 测试与静态检查

CI 覆盖的纯 Go 包：

```bash
(cd server/web && npm ci && npm run build)

go test ./shared/... ./server/... \
  ./client/internal/e2ee/... \
  ./client/internal/credstore/... \
  ./client/internal/engine/... \
  ./client/internal/pairing/...
```

完整平台构建前建议再执行：

```bash
test -z "$(gofmt -l server shared client)"
go vet ./shared/... ./server/...
./scripts/build-all.sh
```

桌面剪贴板、托盘、系统通知、开机自启和窗口材质属于 OS 集成能力，需要在对应系统实机验证。

## 本地构建 Docker 镜像

```bash
docker build -t clipbridge:local .
```

根目录 [`Dockerfile`](../Dockerfile) 依次构建 Web 控制台、静态 Go 服务端和 Alpine 运行镜像。运行参数与官方镜像一致，详见 [Docker 部署](./docker.md)。
