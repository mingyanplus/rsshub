# 多阶段构建：纯 Go SQLite（modernc），无需 CGO
FROM golang:1.25-alpine AS builder

ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.gitCommit=${COMMIT} -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /out/rss-ai ./cmd/server

# 运行时镜像
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata su-exec && \
    addgroup -S app && adduser -S app -G app

WORKDIR /app
COPY --from=builder /out/rss-ai ./rss-ai
COPY web ./web
COPY config.example.yaml ./config.example.yaml
# 镜像内示例配置：数据库指向 /data 卷、监听地址改为 0.0.0.0（容器内 127.0.0.1 无法被端口映射访问），
# 首次生成的 config.yaml 即为正确路径（所见即所得）
RUN sed -i -e 's#path: "./data/rss.db"#path: "/data/rss.db"#' -e 's#host: "127.0.0.1"#host: "0.0.0.0"#' ./config.example.yaml
COPY docker-entrypoint.sh /docker-entrypoint.sh

# 数据目录（SQLite 数据库与运行配置）
RUN mkdir -p /data && chown -R app:app /data /app && chmod +x /docker-entrypoint.sh
VOLUME /data

ENV TZ=Asia/Shanghai
EXPOSE 8080

# 以 root 启动以修正任意挂载方式下 /data 的属主（bind mount 兼容），
# 入口脚本内修正后降权为 app 用户运行主程序
ENTRYPOINT ["/docker-entrypoint.sh"]
