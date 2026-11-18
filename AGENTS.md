# AGENTS.md

SubConverter-Bystander（订阅转换旁路）。事实标准以代码与各目录内文档为准，本文件不重复实现细节与历史决策。

## 唯一约定

**禁止未经明确授权修改 AGENTS.md。** 未获得用户明确授权，任何智能体不得增删改本文件。

## 定位

- 本仓库不做订阅转换、不解析节点、不生成配置；只把转换请求转给转换器，并并发读取订阅源的 `Subscription-Userinfo`。
- 对外表述统一为「订阅转换旁路」「补回流量头」，不写成第二个 subconverter。
- 内置转换器来自 SubConverter-Extended 的 Release，不复制、不修改其源码。

## 目录

- `main.go` - 单一入口：HTTP 反向代理、订阅源流量头旁路、内置转换器进程管理。
- `main_test.go` - Go 单测（URL 提取、SSRF 判定、注入与降级、缓存）。
- `tests/` - 镜像冒烟脚本与假上游。
- `docker/` - 容器内启动内置转换器的脚本（自带 glibc loader）。
- `Dockerfile` - 构建旁路服务并打入 SubConverter-Extended 最新 Release。
- `.github/workflows/` - 构建、冒烟与每日镜像发布。

根目录只放 `AGENTS.md`、`README.md`、`LICENSE`、`Dockerfile`、`go.mod`、`main.go`、`main_test.go`、`.gitignore`、`.dockerignore`；其余代码与文档放进所属目录内部。

## 隐私与密钥

- 严禁把订阅 URL、token、节点密码、UUID、Cookie、内部域名写进源码、测试夹具、日志或文档。
- 日志只记录请求路径与耗时，不记录查询串（`logRequests` 已按此实现）。
- 生产配置一律用环境变量；仓库内不提交 `.env`、`notes/`。

## 协作与提交

- 作者与提交者统一使用当前 GitHub 账号的名称和邮箱，SSH 私钥或 GPG 私钥签名：

  ```bash
  git config user.name "Sakana"
  git config user.email "admin@yangrucheng.top"
  git config user.signingkey "C:/Users/admin/.ssh/private.key"
  git config gpg.format ssh
  git config commit.gpgsign true
  ```

- 必须走 PR：`gh pr create` → 等 `构建镜像` 工作流 → `gh pr checks` 全绿后 `gh pr merge --squash --delete-branch` 并删除 head 分支；禁止本地 merge 后直推 `main`（会把 PR 关成 CLOSED 而非 MERGED）。
- 空仓库首次初始化允许直接推 `main` 建立基线；基线之后所有改动必须走 PR。
- 分支前缀 `feat/`、`fix/`、`refactor/`、`docs/`、`chore/`；提交信息中文，格式 `type(scope): 描述`。
- 测试由 GitHub Actions `构建镜像` 执行；未绿不得合并。本地最低检查：`gofmt -l .`、`go vet ./...`、`go test ./...`。
- 工作流 `name` 用中文（专有名词除外）；严禁硬编码 IP、密钥与内部域名，一律 `${{ secrets.* }}`。
- PR 描述用 `## 改动`、`## 验证` 两节；有风险或未覆盖项时加 `## 已知限制`。

## 发布模型

- 每天 01:00（东八区，即 `0 17 * * *` UTC）拉取 SubConverter-Extended 最新 Release 构建并推送镜像；`workflow_dispatch` 手动触发与 `main` 推送同样发布，PR 只校验。
- 镜像标签为 `latest` 与转换器版本号（如 `v1.11.7`）；`SUBCONVERTER_VERSION` 决定内置转换器版本。
- 镜像内 `/usr/local/bin/bystander` 是旁路服务；`CONVERTER_CMD` 负责拉起内置转换器，`CONVERTER_URL` 指向它。

## 实现约束（改动前必读）

- 旁路读取失败必须静默放行，绝不能让转换请求失败。
- 不读订阅正文：`HEAD` 优先，`GET` 只作兜底且带 `Range: bytes=0-0`。
- 转换器已返回 `Subscription-Userinfo` 时不覆盖。
- 默认拒绝私网、回环、链路本地与 CGNAT 目标；只有 `ALLOW_PRIVATE_TARGETS=1` 才放开。
- 行为变更必须补 Go 单测；镜像冒烟只验证端到端注入与内置转换器可用。
