# SubConverter-Bystander

给 provider 模式的订阅转换补回 `Subscription-Userinfo` 流量头。

SubConverter-Extended 对 `target=clash` / `target=clashr` 默认生成 `proxy-providers`，由内核直接拉取订阅，后端不下载订阅，所以转换链接的响应里没有 `Subscription-Userinfo`。而 Clash Verge、OpenClash 等客户端的订阅卡片只读转换链接自身的响应头，于是已用流量、剩余流量、到期时间全部消失。

本项目**不做订阅转换**，只做两件事：

1. 把转换请求原样转交给订阅转换器；
2. 同时直接请求请求参数里的订阅地址，读取 `Subscription-Userinfo`，和转换后的文件一起返回。

## 快速开始

```bash
docker run -d --name bystander -p 8080:8080 \
  ghcr.io/sakana-1314/subconverter-bystander:latest
```

镜像内置 SubConverter-Extended（跟随其最新 Release），默认监听 `8080`。把原来的转换地址端口换成 `8080` 即可：

```text
http://<host>:8080/sub?target=clash&url=<订阅地址>
```

接入已有转换器时关闭内置转换器：

```bash
docker run -d --name bystander -p 8080:8080 \
  -e CONVERTER_CMD= \
  -e CONVERTER_URL=http://converter:25500 \
  ghcr.io/sakana-1314/subconverter-bystander:latest
```

## 行为

- 只处理带 `url` 参数的 `GET` / `HEAD` 请求；其他请求原样转发。
- 从 `url` 参数中取第一个 HTTP(S) 订阅地址，支持 `|` 多订阅和 `tag:` / `provider:` / `interval:` / `ua:` / `node:` 前缀。
- 订阅源请求优先 `HEAD`，没有流量头时用 `GET` + `Range: bytes=0-0` 兜底；只读响应头，不读正文。
- 默认超时 3 秒，失败静默：拿不到就不加头，转换结果不受影响。
- 转换器已经返回 `Subscription-Userinfo` 时不覆盖。
- `/sub` 响应固定 `Cache-Control: no-store, private`，避免 CDN 缓存动态流量和转换结果。
- UA 取 `ua:` 前缀，否则用请求头 UA，最后回退 `clash.meta`。
- 同一订阅地址 + UA 的结果缓存 60 秒，避免重复请求机场。
- 默认拒绝私网、回环、链路本地和 CGNAT 目标（SSRF 防护）。

## 配置

| 环境变量 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `LISTEN` | `:8080` | 监听地址 |
| `CONVERTER_URL` | `http://127.0.0.1:25500` | 转换器地址 |
| `CONVERTER_CMD` | 空 | 启动内置转换器的命令；镜像内默认为 `/usr/local/bin/start-converter` |
| `HEADER_TIMEOUT` | `3s` | 读取订阅源响应头的超时 |
| `HEADER_CACHE_TTL` | `60s` | 响应头缓存时间；`0` 关闭 |
| `ALLOW_PRIVATE_TARGETS` | `false` | 允许请求私网地址（内网部署时开启） |
| `FALLBACK_UA` | `clash.meta` | 请求没有 UA 时使用 |
| `MAX_CONCURRENT_FETCHES` | `32` | 同时读取订阅源的最大并发 |
| `SUBCONVERTER_SECURITY_PROFILE` | 镜像内为 `public` | 内置转换器的安全档位；内网订阅改为 `lan` |

自定义内置转换器配置：挂载自己的 `pref.toml`，并设置 `PREF_PATH`；镜像内默认使用 `/opt/subconverter/base/pref.toml`。

## 构建与发布

- GitHub Actions 每天 **01:00（东八区，即 `0 17 * * *` UTC）** 解析 SubConverter-Extended 最新 Release 并构建多架构镜像，推送 `latest` 与转换器版本号标签（如 `v1.11.7`）。
- PR 只跑 `gofmt` / `go vet` / `go test -race` 与镜像冒烟，不推送。
- 本地检查：

```bash
gofmt -l .
go vet ./...
go test ./...
go build .
```

## 限制

- 机场若同时按出口 IP 或 UA 限制访问，旁路请求也可能拿不到流量头；这是尽力而为。
- 不代取节点内容，也不会把订阅正文落盘。
- 公开部署时建议加访问控制或限流；本服务会按请求中的 `url` 发起外部请求，虽然默认拦截私网，仍应避免匿名开放。

## 许可

本仓库代码为 MIT。镜像内置的 [SubConverter-Extended](https://github.com/Aethersailor/SubConverter-Extended) 为 GPL-3.0，其源码与许可证以上游仓库为准。
