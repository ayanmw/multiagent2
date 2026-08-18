# server/Dockerfile —— 纯 Go 多阶段构建（无需 CGO / 无需 C 编译器）
# 构建上下文：./server（go.mod 位于此目录）
FROM golang:1.25-alpine AS build

WORKDIR /src

# 利用层缓存：先拉依赖，再拷源码
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 纯 Go SQLite（glebarez/sqlite），关闭 CGO 即可跨平台静态构建
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---- 运行态：最小 alpine ----
FROM alpine:3.20

WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata

COPY --from=build /out/server /app/server

# 数据与运行时目录（挂载卷持久化）
ENV PORT=8080 \
    DB_PATH=/data/codeagent.db \
    WORKSPACE_ROOT=/data/workspaces \
    ARTIFACT_ROOT=/data/agent-state \
    SKILLS_DATA_DIR=/data/skills \
    SKILLS_ROOT=/app/skills

VOLUME ["/data", "/app/skills"]

EXPOSE 8080

# 健康检查同源端 /health（main.go 已实现，返回 200）；alpine 自带 busybox wget。
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["/app/server"]
