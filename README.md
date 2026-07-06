# 剪驿 ClipBridge

端到端加密的自托管剪贴板同步工具：在同一用户当前在线的多台设备间同步剪贴板，服务端只中转临时密文、无法解密正文。详细设计见 [PRD.md](./PRD.md)。

本仓库是一个 Go workspace（[`go.work`](./go.work)），包含三个模块：

- `shared/` — 服务端与客户端共用的协议、DTO、错误码。
- `server/` — 控制面与密文中转（HTTP/JSON + WSS），内嵌 React Web 控制台。
- `client/` — macOS 桌面客户端（Wails v3）与无头同步 CLI。

> 下面所有命令默认在**仓库根目录**执行（workspace 会自动解析三个模块）。

## 环境要求

| 依赖 | 版本 | 用途 |
| --- | --- | --- |
| Go | 1.26+ | 服务端、客户端 |
| Node.js + npm | 22+ | 构建 Web 控制台与客户端前端 |
| macOS | 13+ | 运行桌面客户端（需 Xcode Command Line Tools 提供 cgo） |
| Wails v3 CLI | `v3.0.0-alpha.96` | 仅在改动客户端绑定方法签名时用于重新生成绑定 |

安装 Wails CLI（按需）：

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha.96
```

## 一键开发（推荐）

```bash
./scripts/dev.sh
```

该脚本会：构建 Web 控制台并编译+后台启动服务端、交叉编译 Windows 同步 CLI（`bin/clipbridge-cli.exe`）、构建并启动 macOS 客户端 `.app`，并实时打印服务端日志（含首次启动的管理员凭据）。按 `Ctrl-C` 停止服务端。加 `--no-client` 只起服务端并交叉编译、不启动桌面客户端。

下面是各部分的手动步骤。

---

## 服务端

### 编译

Web 控制台会通过 `//go:embed` 嵌入服务端二进制，因此**先构建前端，再编译 Go**：

```bash
# 1) 构建 Web 控制台（产出 server/web/dist）
cd server/web && npm install && npm run build && cd ../..

# 2) 编译服务端
go build -o bin/clipbridge-server ./server/cmd/clipbridge-server
```

> 也可用 `go run ./server/cmd/clipbridge-server` 直接运行（仍需先构建一次前端 dist）。

### 启动

```bash
./bin/clipbridge-server -data-dir ./runtime
```

- **首次启动**会在控制台打印一次随机管理员用户名/密码，以及设备端证书的 SHA-256 指纹——请立即记下（之后不再显示）。
- **Web 控制台**（HTTP）：`http://localhost:8080`
- **设备端口**（自签名 HTTPS + WSS）：`:8443`，客户端对其证书做指纹 pinning。
- 运行数据（SQLite、证书、临时密文、日志）位于 `-data-dir` 指定目录（默认 `./runtime`）。
- 端口可在 `<data-dir>/config.yaml` 调整：`device_listen_address` / `web_listen_address`。

### 忘记管理员密码（离线重置）

```bash
./bin/clipbridge-server -data-dir ./runtime -reset-admin-password
```

打印新随机密码后立即退出，不启动监听。请登录后尽快修改。

### 公网部署

- **Web 端口**只监听 HTTP，公网暴露时用 Caddy / Nginx 等反向代理在外层实现 HTTPS，并正确转发 WSS、保留来源 IP。
- **设备端口**固定自签名 + 指纹 pinning，直接暴露/映射即可，**不要**在其前面再套 TLS 终止。

### Docker 部署

服务端提供多架构官方镜像 `ghcr.io/mokeyjay/clipbridge`（amd64/arm64），仓库根目录含 [docker-compose.yml](./docker-compose.yml)：

```bash
docker compose up -d
docker compose logs clipbridge   # 首次启动打印一次管理员凭据与证书指纹
```

端口模型、数据卷、反向代理、密码重置、备份升级等详见 [docs/docker.md](./docs/docker.md)。

---

## 桌面客户端（macOS）

### 编译并打包为 `.app`（推荐）

系统级原生通知要求以带 bundle id 的 `.app` 运行，因此推荐用打包脚本：

```bash
cd client && ./build-macos-app.sh        # 产出 client/bin/ClipBridge.app
open client/bin/ClipBridge.app           # 启动；首次会请求通知权限
```

- 菜单栏托盘常驻，左键点击托盘图标打开设置窗口；窗口顶部可拖动。
- 关闭窗口不退出（托盘菜单「退出」才退出）。

### 开发快速运行（无系统通知）

不打包直接跑裸二进制更快，但**无法发送系统级通知**：

```bash
cd client/frontend && npm install && npm run build && cd ..
go build -o bin/clipbridge ./client
./bin/clipbridge
```

### 重新生成前端↔后端绑定

仅当改动了 `client/internal/guiservice` 的导出方法签名时需要：

```bash
cd client && wails3 generate bindings -d frontend/bindings -ts && cd frontend && npm run build
```

### 配对流程（TOFU）

1. 在 Web 控制台用普通用户登录 → 「配对」页生成 6 位配对码（页面同时显示服务器证书指纹）。
2. 客户端「概览」填入：服务器设备端口地址（如 `https://127.0.0.1:8443`）、6 位配对码、设备名（默认取本机名）→ 点「连接」。
3. 客户端展示它实际握手到的证书指纹——与 Web 配对页核对一致后点「信任并配对」。
4. 回到 Web 配对页点「确认」。完成后客户端显示「已连接」并开始同步。

---

## 同步测试客户端（Windows / 无头 CLI）

只有一台 Mac 时，可用无头 CLI 在第二台设备（如 Windows）上做真机双设备同步验收。它复用与桌面客户端**相同的同步内核**，无 GUI、CGO-free，可从 macOS 交叉编译。

> 这是测试工具，不是正式的 Windows GUI 客户端（后者属后置里程碑）。

### 编译

```bash
# 交叉编译出 Windows 可执行（在 macOS 上）
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bin/clipbridge-cli.exe ./client/cmd/clipbridge-cli

# 本机 macOS 版（可选）
go build -o bin/clipbridge-cli ./client/cmd/clipbridge-cli
```

### 运行（在第二台设备上）

把 `clipbridge-cli.exe` 拷到 Windows，在 Web 控制台再生成一个配对码后：

```bash
clipbridge-cli.exe -server https://<服务器局域网IP>:8443 -code <6位配对码>
```

- 会显示服务端证书指纹 → 与 Web 配对页核对 → 输入 `y` 信任并配对 → 之后常驻，复制文本即双向同步，终端打印同步记录。
- 常用 flags：`-data-dir` 凭据目录、`-name` 设备名、`-trust` 跳过指纹确认（自动信任）。

注意：

- 用服务器**局域网 IP** 连接即可（指纹 pinning 不校验主机名/SAN）；记得放行服务器 `8443` 入站。
- 两台设备需在**同一用户**下分别配对（同一用户同时只有一个有效配对码，每配一台重新生成）。

---

## 测试

纯逻辑与后端（无 GUI / cgo 依赖）：

```bash
go test ./shared/... ./server/... \
  ./client/internal/e2ee/... ./client/internal/credstore/... \
  ./client/internal/engine/... ./client/internal/pairing/...
```

桌面 GUI 相关包（`client` 主程序、`clipboardadapter`、`guiservice`）依赖 macOS cgo 与嵌入前端，由本机构建验证（见上文）。
