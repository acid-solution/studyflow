# 第一阶段：编译 Go 程序
FROM golang:1.26.4-alpine3.23 AS builder

WORKDIR /app

# 先复制依赖描述文件，利用 Docker 构建缓存
COPY go.mod go.sum ./

# 下载项目依赖
RUN go mod download

# 再复制项目源码
COPY . .

# 编译 Linux 可执行文件
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /app/todo-api \
    .


# 第二阶段：运行 Go 程序
FROM alpine:3.23

# 安装 HTTPS 证书，并创建非 root 用户
RUN apk add --no-cache ca-certificates \
    && addgroup -S app \
    && adduser -S app -G app

WORKDIR /app

# 只从构建阶段复制编译后的可执行文件
COPY --from=builder /app/todo-api ./todo-api
COPY --from=builder /app/static ./static

# 使用普通用户运行服务
USER app

# 声明应用默认监听端口
EXPOSE 8081

# 启动 Todo 服务
ENTRYPOINT ["./todo-api"]