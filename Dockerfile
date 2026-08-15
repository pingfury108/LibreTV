# syntax=docker/dockerfile:1

# ---- 构建阶段：宿主机原生执行 + Go 交叉编译，无需 QEMU ----
FROM --platform=$BUILDPLATFORM golang:alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-s -w" -o libretv .

# ---- 运行阶段：alpine 基础（保留 wget 用于健康检查），最终镜像约 20MB ----
FROM alpine
RUN apk add --no-cache ca-certificates wget
COPY --from=build /app/libretv /usr/local/bin/libretv

ENV PORT=8080
ENV CORS_ORIGIN=*
ENV DEBUG=false
ENV REQUEST_TIMEOUT=5000
ENV MAX_RETRIES=2
ENV CACHE_MAX_AGE=1d

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://localhost:8080/ || exit 1

ENTRYPOINT ["libretv"]
