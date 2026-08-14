# tool/workbuddyLLMAPI/Dockerfile —— 纯 Go 多阶段构建
# 构建上下文：./tool/workbuddyLLMAPI（独立 Go 模块）
FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -trimpath -ldflags="-s -w" -o /out/gateway .

FROM alpine:3.20

WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata

COPY --from=build /out/gateway /app/gateway

# 容器内默认 passthrough 后端；对接外部 LLM 由 WB_BASE_URL/WB_API_KEY 控制。
# codebuddy 后端依赖本机 WorkBuddy 桌面，不应在容器内使用。
ENV WB_LISTEN=:8088 \
    WB_BACKEND=passthrough \
    WB_BASE_URL=https://api.openai.com/v1 \
    WB_API_KEY=

EXPOSE 8088
ENTRYPOINT ["/app/gateway"]
