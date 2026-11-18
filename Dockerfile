# syntax=docker/dockerfile:1

ARG GO_VERSION=1.27
ARG ALPINE_VERSION=3.22
ARG SUBCONVERTER_VERSION=latest

# ---------------------------------------------------------------- 旁路服务
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" -o /out/bystander .

# ---------------------------------------------------------------- 内置转换器（SubConverter-Extended 最新 Release）
FROM --platform=$BUILDPLATFORM alpine:${ALPINE_VERSION} AS converter
ARG SUBCONVERTER_VERSION
ARG TARGETARCH
RUN apk add --no-cache curl tar jq \
 && case "${TARGETARCH}" in \
      amd64) arch=amd64 ;; \
      arm64) arch=arm64 ;; \
      *) echo "不支持的架构：${TARGETARCH}" >&2; exit 1 ;; \
    esac \
 && version="${SUBCONVERTER_VERSION}" \
 && if [ "${version}" = "latest" ]; then \
      version="$(curl -fsSL https://api.github.com/repos/Aethersailor/SubConverter-Extended/releases/latest | jq -r .tag_name)"; \
    fi \
 && test -n "${version}" && test "${version}" != "null" \
 && echo "SubConverter-Extended ${version} (linux-${arch})" \
 && curl -fsSL -o /tmp/subconverter.tar.gz \
      "https://github.com/Aethersailor/SubConverter-Extended/releases/download/${version}/SubConverter-Extended-${version}-linux-${arch}.tar.gz" \
 && mkdir -p /opt \
 && tar -xzf /tmp/subconverter.tar.gz -C /opt \
 && mv /opt/SubConverter-Extended /opt/subconverter \
 && rm -f /tmp/subconverter.tar.gz

# ---------------------------------------------------------------- 运行时
FROM alpine:${ALPINE_VERSION}
ARG SUBCONVERTER_VERSION=latest

LABEL org.opencontainers.image.title="SubConverter-Bystander" \
      org.opencontainers.image.description="订阅转换旁路：转发转换请求并补回 Subscription-Userinfo 流量头" \
      org.opencontainers.image.source="https://github.com/Sakana-1314/SubConverter-Bystander" \
      org.opencontainers.image.licenses="MIT" \
      io.github.sakana.subconverter.version="${SUBCONVERTER_VERSION}"

RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -g 10001 bystander \
 && adduser -D -H -u 10001 -G bystander bystander

COPY --from=build /out/bystander /usr/local/bin/bystander
COPY --from=converter /opt/subconverter /opt/subconverter
COPY --from=converter /opt/subconverter/etc/nsswitch.conf /etc/nsswitch.conf
COPY docker/start-converter.sh /usr/local/bin/start-converter

# 首次启动直接使用示例配置；需要自定义时挂载并设置 PREF_PATH。
# 转换器只监听容器内回环，对外只暴露旁路服务的 8080。
RUN chmod +x /usr/local/bin/start-converter \
 && cp /opt/subconverter/base/pref.example.toml /opt/subconverter/base/pref.toml \
 && sed -i 's/^listen = "0.0.0.0"/listen = "127.0.0.1"/' /opt/subconverter/base/pref.toml \
 && chown -R 10001:10001 /opt/subconverter

ENV CONVERTER_URL=http://127.0.0.1:25500 \
    CONVERTER_CMD=/usr/local/bin/start-converter \
    SUBCONVERTER_SECURITY_PROFILE=public \
    LISTEN=:8080 \
    TZ=Asia/Shanghai

USER 10001:10001
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=20s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/bystander"]
