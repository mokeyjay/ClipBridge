# 配置参考

ClipBridge 的配置分为三层：

1. 服务端进程配置：设备端口与 Web 端口监听地址。
2. Web 控制台策略：实例上限和用户默认值，存储在 SQLite。
3. 客户端本地设置：设备覆盖、同步方向、通知与界面偏好。

## 服务端进程配置

服务端从 `<data-dir>/config.yaml` 读取配置；文件不存在时直接使用默认值。Docker 镜像固定使用 `-data-dir /data`，因此对应路径为 `/data/config.yaml`。

```yaml
device_listen_address: ":8443"
web_listen_address: ":8080"
```

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `device_listen_address` | `:8443` | 自签名 HTTPS + WSS 设备端口；客户端固定其证书指纹 |
| `web_listen_address` | `:8080` | HTTP + WSS Web 控制台端口；公网使用时由反向代理终止 HTTPS |

修改后重启服务端生效：

```bash
docker compose restart clipbridge
```

容器部署通常只需调整宿主机端口映射，不要修改容器内监听端口。镜像健康检查固定访问 `127.0.0.1:8080/healthz`；如果修改 `web_listen_address`，需要同步覆盖 Compose 的 `healthcheck`。

### 数据目录参数

二进制部署通过命令行参数选择数据目录：

```bash
clipbridge-server -data-dir /var/lib/clipbridge
```

`config.yaml`、SQLite、证书和临时密文都会放在该目录中。数据目录不能通过 `config.yaml` 自身重定向。

### 当前未启用的预留字段

源码配置结构中还保留了 `runtime_dir`、`public_base_url` 和 `trusted_proxy_cidrs`，但当前服务端启动链路不会使用它们，请不要依赖这些字段。配对请求走直连的 `8443` 设备端口，限速键直接取连接的 `RemoteAddr`。

## Web 控制台策略

### 实例级

管理员可以配置：

| 配置 | 默认值 | 行为 |
| --- | --- | --- |
| 实例名称 | `ClipBridge` | Web 控制台显示名称 |
| 最大同步尺寸 | 100 MiB | 所有用户和设备的硬上限 |
| 允许同步类型 | 全部 | `text`、`image`、`file`、`rich_text` 的实例级允许列表 |
| 同步日志保留 | 30 天 | `0` 表示清理时不保留历史同步日志 |

服务端密文中转 TTL 固定为 300 秒。密文被全部目标设备确认或拒绝后会提前删除。

### 用户级

每个普通用户拥有一套设备默认策略：

| 配置 | 默认值 |
| --- | --- |
| 最大同步尺寸 | 100 MiB |
| 允许同步类型 | 全部 |
| 自动上传阈值 | 10 MiB |
| 自动下载阈值 | 10 MiB |
| 接收文件保留 | 7 天 |

设备可以逐项继承或覆盖用户默认值。最终允许的内容类型取实例允许列表与设备有效列表的交集；最终最大同步尺寸不会超过实例上限。

## 客户端本地设置

以下设置只保存在当前设备：

- 同步方向：双向、仅上传、仅下载。
- 暂停状态。
- 通知策略：安静、默认、详细。
- 主题与界面语言。
- 开机自启。
- 接收文件目录与本机保留天数覆盖。
- Windows 11 窗口材质：Mica 或 Acrylic。
- 窗口位置与尺寸。

建议通过客户端设置页修改，不要直接编辑 `profile.json`。

### 配置目录

| 平台 | 默认路径 |
| --- | --- |
| macOS | `~/Library/Application Support/ClipBridge/` |
| Windows | `%APPDATA%\ClipBridge\` |

开发或无头 CLI 场景可以通过 `CLIPBRIDGE_CONFIG_DIR` 覆盖：

```bash
CLIPBRIDGE_CONFIG_DIR=/tmp/clipbridge-dev ./clipbridge
```

目录结构：

```text
ClipBridge/
  profile.json
  received/
  credentials/
    device.json
    device-token
    hpke-private-key
    server-fingerprint
    known-peers.json
```

`credentials/` 包含设备身份、Bearer token、私钥和信任状态。客户端重置会删除该目录，之后必须重新配对。

## 服务端数据目录

```text
/data/
  clipbridge.db
  config.yaml
  certificates/
  data/
    ciphertext/
    incoming/
  logs/
```

备份至少应包含：

- `clipbridge.db`：用户、设备、策略与日志。
- `certificates/`：设备端证书与私钥；丢失后所有客户端都会检测到指纹变化。
- `config.yaml`：进程配置。

`data/` 只保存短生命周期密文，不需要纳入常规备份。完整备份与恢复命令见 [Docker 部署](./docker.md)。
