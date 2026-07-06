# Docker 部署

ClipBridge 服务端提供官方 Docker 镜像，适合自托管到任意 Linux 主机。本文假设你熟悉 Docker / Docker Compose 的日常用法。

## 镜像

| 项 | 值 |
| --- | --- |
| 镜像地址 | `ghcr.io/mokeyjay/clipbridge` |
| 架构 | `linux/amd64`、`linux/arm64` |
| 基础镜像 | `alpine`，静态 Go 二进制（CGO off，纯 Go SQLite） |
| 运行用户 | 非 root（uid/gid `10001`，用户名 `clipbridge`） |
| 数据目录 | `/data`（声明为 VOLUME） |
| 端口 | `8443` 设备口、`8080` Web 控制台 |

标签策略：`latest` 指向最新正式版；每个发布另有 `X.Y.Z` 与 `X.Y` 语义化标签；预发布版（tag 含 `-`）不更新 `latest`。生产环境建议固定 `X.Y.Z`。

## 端口模型（重要）

两个端口的暴露方式**不同**，这是 ClipBridge 安全模型的一部分：

- **`8443` 设备端口**：自签名 HTTPS + WSS。客户端配对时对该端口的证书做 SHA-256 指纹 pinning，之后强校验。**直接暴露/端口映射即可，不要在它前面做 TLS 终止或反向代理**——那会改变客户端看到的指纹，导致全部设备连接失败。
- **`8080` Web 控制台**：纯 HTTP。公网部署时应由反向代理（Caddy / Nginx 等）在外层终止 HTTPS 并转发（含 WebSocket Upgrade）；建议只把它绑定到 `127.0.0.1` 或内网接口。

## 快速开始：docker run

```bash
docker run -d --name clipbridge \
  --restart unless-stopped \
  -p 8443:8443 \
  -p 127.0.0.1:8080:8080 \
  -v clipbridge-data:/data \
  ghcr.io/mokeyjay/clipbridge:latest
```

首次启动会在日志中**打印一次**随机管理员用户名/密码，以及设备端证书指纹：

```bash
docker logs clipbridge
# ... msg="初始管理员凭据（仅显示这一次）" username=admin-xxxx password=xxxxxxxx
# ... msg="device certificate ready" fingerprint_sha256=xxxx...
```

立即记录凭据并登录 `http://127.0.0.1:8080`（或经反向代理的地址）修改密码。凭据只打印这一次，但只要不清空日志就仍可用 `docker logs` 翻到。

## Docker Compose

仓库根目录提供 [docker-compose.yml](../docker-compose.yml)：

```yaml
services:
  clipbridge:
    image: ghcr.io/mokeyjay/clipbridge:latest
    container_name: clipbridge
    restart: unless-stopped
    ports:
      - "8443:8443"
      - "127.0.0.1:8080:8080"
    volumes:
      - clipbridge-data:/data

volumes:
  clipbridge-data:
```

```bash
docker compose up -d
docker compose logs clipbridge   # 查看首次启动的管理员凭据
```

## 数据持久化

`/data` 内布局（与二进制部署的 `-data-dir` 完全一致）：

```text
/data/
  clipbridge.db        # SQLite：用户、设备、业务配置、同步日志
  config.yaml          # 进程级配置（可选，见下节）
  certificates/        # 设备端口自签名证书 + 私钥（决定指纹，务必持久化）
  data/                # 剪贴板临时密文（服务端不可解密，短生命周期）
  logs/
```

- **命名卷（推荐）**：如上例。Docker 会继承镜像内 `/data` 的属主，无需额外处理权限。
- **bind mount**：容器以 uid `10001` 运行，需先准备目录属主：

  ```bash
  mkdir -p /opt/clipbridge/data
  chown -R 10001:10001 /opt/clipbridge/data
  # 然后 -v /opt/clipbridge/data:/data
  ```

丢失 `certificates/` 意味着设备端证书指纹变化，**所有客户端都需重新确认信任**；丢失 `clipbridge.db` 意味着全部账号与设备配对作废。请将两者纳入备份。

## 配置

进程级配置读取 `/data/config.yaml`（不存在则用默认值，开箱即用）：

```yaml
device_listen_address: ":8443"    # 容器内设备口监听地址
web_listen_address: ":8080"       # 容器内 Web 口监听地址
public_base_url: "https://clip.example.com"   # 对外可达的 Web 基址，配对时展示
trusted_proxy_cidrs: []           # 信任的反向代理网段，用于还原真实客户端 IP（配对限速）
```

修改后 `docker restart clipbridge` 生效。容器场景下一般**不需要**改监听端口——在宿主机侧调整 `-p` 映射即可。若确实修改了 `web_listen_address`，注意镜像内置健康检查探测的是 `127.0.0.1:8080/healthz`，需在 compose 中覆盖 `healthcheck`。

可通过 Web 控制台修改的业务配置（是否开放注册、最大同步尺寸等）保存在 SQLite 中，与本文件无关。

### trusted_proxy_cidrs 与 Docker 网络

Web 口经反向代理转发时，服务端看到的来源 IP 是代理的 IP。要让配对限速按真实客户端 IP 生效，需把代理地址所在网段加入 `trusted_proxy_cidrs`：

- 反向代理跑在宿主机、Web 口映射到 `127.0.0.1:8080`：来源 IP 是 Docker 网桥网关（默认桥通常为 `172.17.0.1`，compose 项目网络一般在 `172.16.0.0/12` 内）。可配置 `trusted_proxy_cidrs: ["172.16.0.0/12"]`。
- 反向代理与 ClipBridge 同一个 compose 网络：加入该网络的子网即可。

## 反向代理示例（Web 口）

Caddy（自动 HTTPS，WebSocket 自动透传）：

```caddyfile
clip.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Nginx：

```nginx
server {
    listen 443 ssl;
    server_name clip.example.com;
    # ssl_certificate / ssl_certificate_key ...

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;      # Web 控制台使用 WSS
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

再次强调：以上只针对 `8080` Web 口；`8443` 设备口保持直连，不要代理。

## 管理员密码重置（离线）

忘记密码时，停止服务端容器后用同一数据卷跑一次重置（打印新密码后立即退出，不监听端口）：

```bash
docker compose stop clipbridge
docker compose run --rm clipbridge -reset-admin-password
docker compose start clipbridge
```

纯 `docker run` 场景等价于：

```bash
docker stop clipbridge
docker run --rm -v clipbridge-data:/data ghcr.io/mokeyjay/clipbridge:latest -reset-admin-password
docker start clipbridge
```

> 镜像 ENTRYPOINT 已含 `-data-dir /data`，命令行只需追加额外 flag。

## 健康检查

镜像内置 `HEALTHCHECK`，每 30s 请求一次容器内 `http://127.0.0.1:8080/healthz`。`docker ps` 的 STATUS 列会显示 `healthy` / `unhealthy`。外部监控也可以直接探测两个端口的 `GET /healthz`（设备口为自签 HTTPS，探测时跳过证书校验）。

## 升级

```bash
docker compose pull
docker compose up -d
```

数据库 migration 在启动时自动执行且幂等。跨大版本升级前建议先备份 `/data`（至少 `clipbridge.db` 与 `certificates/`）：

```bash
docker run --rm -v clipbridge-data:/data -v "$PWD:/backup" alpine \
  tar czf /backup/clipbridge-backup.tgz -C /data clipbridge.db certificates config.yaml
```

`data/` 下的临时密文生命周期极短，不必备份。

## 本地构建镜像

不依赖 GHCR，直接从源码构建（构建上下文为仓库根目录，多阶段完成 Web 控制台构建、Go 编译、运行镜像组装）：

```bash
docker build -t clipbridge-server:local .
# 或在 docker-compose.yml 中启用 build: . 后
docker compose up -d --build
```
