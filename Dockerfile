# ClipBridge 服务端镜像。
# 分三段构建：先用 Node 产出 React 控制台静态资源（server/web/dist），
# 再用 Go 把它通过 //go:embed 嵌入并编译出纯静态二进制（CGO 关闭，modernc sqlite 为纯 Go），
# 最后塞进精简的 alpine 运行镜像。构建上下文为仓库根目录。

# --- 阶段一：构建 Web 控制台 ---
FROM node:22-alpine AS web
WORKDIR /src/server/web
# 先复制锁文件以利用 Docker 层缓存
COPY server/web/package.json server/web/package-lock.json ./
RUN npm ci
COPY server/web/ ./
RUN npm run build

# --- 阶段二：编译 Go 服务端 ---
FROM golang:1.26-alpine AS build
WORKDIR /src
# server 模块通过 replace 指向 ../shared，关闭 workspace 后只需这两个模块
ENV GOWORK=off CGO_ENABLED=0
# 先复制依赖清单以缓存 go mod download
COPY shared/go.mod ./shared/
COPY server/go.mod server/go.sum ./server/
RUN cd server && go mod download
# 再复制源码与上一阶段产出的控制台资源（embed 需要 dist 存在）
COPY shared/ ./shared/
COPY server/ ./server/
COPY --from=web /src/server/web/dist ./server/web/dist
# VERSION 由 CI 传入，仅用于镜像标签与日志，编译本身无强依赖
ARG VERSION=dev
# 关闭 workspace 后无根模块，进入 server 模块编译（replace 指向 ../shared）
RUN cd server && go build -trimpath -ldflags "-s -w" \
    -o /out/clipbridge-server ./cmd/clipbridge-server

# --- 阶段三：运行镜像 ---
FROM alpine:3.21
# HTTPS 设备口需要 CA 证书；wget 便于健康检查
RUN apk add --no-cache ca-certificates wget && \
    adduser -D -u 10001 clipbridge
WORKDIR /app
COPY --from=build /out/clipbridge-server /usr/local/bin/clipbridge-server
# 运行期数据（sqlite、自签证书、blob、日志）写入挂载卷，预建目录并归属非 root 用户
RUN mkdir -p /data && chown clipbridge:clipbridge /data
VOLUME ["/data"]
USER clipbridge
# 8443=自签 HTTPS + WSS 设备口；8080=Web 控制台
EXPOSE 8443 8080
# 探活走 Web 口的 /healthz；若在 config.yaml 改了 web_listen_address 需自行覆盖该检查
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["clipbridge-server", "-data-dir", "/data"]
