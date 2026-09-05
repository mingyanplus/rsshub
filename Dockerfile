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

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S app -G app

WORKDIR /app
COPY --from=builder /out/rss-ai ./rss-ai
COPY web ./web
COPY config.example.yaml ./config.example.yaml
# 镜像内示例配置的数据库指向 /data 卷：首次生成的 config.yaml 即为正确路径（所见即所得）
RUN sed -i 's#path: "./data/rss.db"#path: "/data/rss.db"#' ./config.example.yaml

# 数据目录（SQLite 数据库与运行配置）
RUN mkdir -p /data && chown -R app:app /data /app
VOLUME /data
USER app

ENV TZ=Asia/Shanghai
EXPOSE 8080

# 首次启动生成 /data/config.yaml（镜像内示例已指向 /data）；
# 运行期 sed 仅用于把 v1.0.1 及更早旧卷中的相对路径迁移到 /data 卷内（幂等）
CMD ["sh", "-c", "[ -f /data/config.yaml ] || cp /app/config.example.yaml /data/config.yaml; sed -i 's#path: \"./data/rss.db\"#path: \"/data/rss.db\"#' /data/config.yaml; exec /app/rss-ai -config /data/config.yaml"]
