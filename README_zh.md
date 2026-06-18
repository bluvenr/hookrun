# HookRun

[![Release](https://img.shields.io/github/v/release/bluvenr/hookrun?include_prereleases&sort=semver)](https://github.com/bluvenr/hookrun/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/bluvenr/hookrun)](https://go.dev/)
[![Go Report Card](https://goreportcard.com/badge/github.com/bluvenr/hookrun)](https://goreportcard.com/report/github.com/bluvenr/hookrun)
[![codecov](https://codecov.io/gh/bluvenr/hookrun/branch/main/graph/badge.svg)](https://codecov.io/gh/bluvenr/hookrun)
[![PkgGoDev](https://pkg.go.dev/badge/github.com/bluvenr/hookrun)](https://pkg.go.dev/github.com/bluvenr/hookrun)
[![License](https://img.shields.io/github/license/bluvenr/hookrun)](LICENSE)

[English](README.md) | [官网](https://bluvenr.github.io/hookrun/)

轻量级 Webhook 动作执行引擎 —— 基于 YAML 配置，接收 Webhook 请求后自动执行自定义命令、脚本或转发 Webhook。

单二进制文件，不到 5MB。无需数据库，无需容器运行时。跨平台支持（Linux / Windows / macOS）。

```
┌──────────┐     POST /webhook/{name}     ┌─────────────┐     认证 + 过滤      ┌────────────┐     执行命令     ┌──────────────────┐
│  GitHub  │ ──────────────────────────▶  │   HookRun   │ ─────────────────▶  │   匹配引擎  │ ─────────────▶  │ 命令/脚本/Webhook │
│  GitLab  │     Token / HMAC / IP        │  (路由匹配)  │      命中规则         │  (策略检查) │    shell 执行   │ 部署任务          │
│  Grafana │ ◀──────────────────────────  │             │ ◀─────────────────  │            │ ◀───────────── │                  │
│  自定义   │       JSON 响应               │             │     执行结果         │            │     标准输出    │                  │
└──────────┘                              └─────────────┘                     └────────────┘                └──────────────────┘
```

## 为什么选 HookRun？

专为 Webhook 场景设计的动作执行引擎，与通用自动化工具对比：

| | HookRun | [adnanh/webhook](https://github.com/adnanh/webhook) | n8n | Huginn |
|---|---------|------|------|------|
| **部署体积** | 单文件 ~3MB，零依赖 | 单文件 ~7MB，零依赖 | Docker + SQLite（默认）/ PostgreSQL | Docker + MySQL / PostgreSQL |
| **执行策略** | block / always / cooldown 三种模式 | 无并发控制，每次触发 | 部分支持（重试/等待） | 基于场景触发 |
| **热更新** | 默认自动监听目录，零配置热加载 | `-hotreload` 参数，仅监听指定文件 | 需重启 | 需重启 |
| **认证体系** | Token + HMAC + IP 白名单，顶层 auth 字段，AND 组合 | Token + HMAC，嵌套在 trigger-rule 中，配置分散 | Basic / Header / JWT / IP 白名单 | Devise 用户认证 + OAuth 服务 |
| **路由机制** | `/webhook/{filename}` 精准路由，按需匹配 | `/hooks/{id}` 扁平路由，统一入口 | 工作流引擎 | 场景图（Agent 链接） |
| **进程管理** | 内置 CLI 守护进程（start/stop/status） | 依赖外部 systemd 等 | Docker 容器管理 | Docker 容器管理 |
| **配置格式** | YAML（可读性强，支持注释） | JSON / YAML | 可视化编辑器 / JSON | JSON（Agent 配置） |
| **Relay 中转** | 内置多目标中转 + 动态注册池 + tag 匹配 | 不支持中转 | 需搭建 HTTP 节点工作流 | 手动配置 Scenario 代理 |
| **失败重试** | 内置指数退避 + 随机抖动 | 不支持重试 | Retry on Fail 节点 | 部分支持（场景级） |
| **健康检查** | 内置 `/health` 端点，对接监控 | 无内置端点 | 有健康检查 | 有健康检查 |
| **开源协议** | MIT，完全自由使用 | MIT，完全自由使用 | Fair-code (SUL)，有商业限制 | MIT，完全自由使用 |

- **安全优先** — Token 认证、HMAC 签名验证、IP 白名单，多重防护 AND 组合，保障端点安全
- **轻巧灵活** — 单二进制文件，不到 5MB，零依赖。无需数据库、无需容器运行时。`scp` 到服务器就能跑，极低资源即可运行
- **完全可控** — MIT 开源，自托管在你自己的服务器上。你的规则、你的数据、你的基础设施

## 运行演示

```
$ hookrun validate
Validating config: config.yaml
PASS: All configurations are valid
  Server port: 9000
  Webhook route: /webhook
  Allow all: false
  Log mode: daily
  Log path: ./logs
  Log retention: 30 days
  Config dir: ./hooks
  Rule files loaded: 1
    - github-auto-deploy (2 rules: push-to-main, tag-release) [auth: token]

$ hookrun start
HookRun started in background (PID: 8421)

$ hookrun status
Status:  running
PID:     8421
Port:    9000
Rules:   2 config(s)
Uptime:  12m30s

# 收到 Webhook 推送
$ curl -X POST http://localhost:9000/webhook/github-auto-deploy \
    -H "X-Webhook-Token: your-secret-token" \
    -H "X-GitHub-Event: push" \
    -d '{"ref":"refs/heads/main"}'

→ {"code":200,"message":"ok","config":"github-auto-deploy","rule":"push-to-main","actions":3}
```

## 特性

- **YAML 驱动** — 所有规则通过 YAML 文件定义，无需编码
- **定向路由** — `/webhook/{文件名}` 精准定位配置，高效匹配
- **灵活验证** — Token（Header/Query）+ HMAC 签名（GitHub/GitLab）+ IP 白名单，AND 组合
- **多条件过滤** — 支持 Header / Query / Body 匹配，操作符：`eq` `ne` `contains` `regex`
- **执行策略** — 三种模式：`block`（防并发）、`always`（始终执行）、`cooldown`（冷却限频）
- **策略继承** — 文件级 → 规则级逐层覆盖
- **文件级过滤** — 全局约束应用于所有规则，与规则级 filters 为 AND 组合
- **参数传递** — 模板变量（`{{.body.ref}}`）、`pass_args` 和环境变量注入（`env_from`）将请求数据传入命令/脚本
- **Webhook 转发** — 将 Webhook 负载转发到其他服务，支持模板构建 Body、Header 白名单转发、`{{.raw_body}}` 原始数据注入
- **Relay 中转** — 多目标中转到其他 HookRun 实例，支持静态目标、动态注册池和 tag 发现
- **失败重试** — 内置指数退避 + 随机抖动，命令/脚本执行失败时自动重试
- **热重载** — 运行中自动重载配置，无需重启服务
- **日志管理** — Daily/Single 双模式，自动清理，支持规则级独立日志
- **健康检查** — 内置 `/health` 端点，便于对接 Prometheus、Uptime Kuma 等监控系统

## 使用场景

### Git 自动部署

推送到 GitHub/GitLab，服务器自动 pull、build、deploy，无需手动 SSH。

```yaml
name: "auto-deploy"
auth:
  hmac:
    header: "X-Hub-Signature-256"
    secret: "your-webhook-secret"
    algorithm: "sha256"
execution:
  policy: "block"
rules:
  - name: "deploy-main"
    filters:
      - type: "body"
        key: "ref"
        operator: "eq"
        value: "refs/heads/main"
    actions:
      - type: "command"
        cmd: "cd /var/www/app && git pull origin main"
        timeout: 30
      - type: "command"
        cmd: "cd /var/www/app && npm install --production && npm run build"
        timeout: 120
      - type: "script"
        path: "./scripts/restart.sh"
        timeout: 30
```

### CI/CD 流水线触发

将 Webhook 事件串联为多步骤构建流水线，支持超时控制和并发保护。

### 监控告警响应

接收 Grafana、Prometheus 等监控告警，自动执行修复脚本。

```yaml
name: "alert-handler"
auth:
  token:
    source: "header"
    key: "Authorization"
    value: "Bearer your-grafana-token"
execution:
  policy: "cooldown"
  cooldown_seconds: 300
rules:
  - name: "high-cpu-response"
    filters:
      - type: "body"
        key: "alerts[0].labels.alertname"
        operator: "eq"
        value: "HighCPU"
    actions:
      - type: "command"
        cmd: "echo '收到 CPU 告警，正在扩容...'"
        timeout: 30
      - type: "script"
        path: "./scripts/scale-up.sh"
        timeout: 120
```

### 自定义自动化

任何 HTTP POST 都是触发器 — 同步数据、发送通知、管理基础设施，一切皆可自动化。

### 多服务器部署

通过 Relay 将 Webhook 事件从中心 HookRun 分发到多台服务器上的下游实例。支持静态目标，也可让实例启动时自注册并用 tag 动态发现。

```yaml
# 上游：中转到所有带 "prod" 标签的已注册实例
- type: "relay"
  relay:
    targets:
      - tag: "prod"
    forward_headers: ["X-GitHub-Event"]
    timeout: 30
```

```yaml
# 下游（每台服务器）：启动时自动向上游注册
# 在 config.yaml 中：
relay_client:
  upstream: "http://10.0.0.1:9000"
  registry_token: "shared-registry-secret"
  tags: ["prod"]
  ttl: 120
```

## 快速开始

### 安装

```bash
# 方式一：从源码编译
git clone https://github.com/bluvenr/hookrun.git
cd hookrun
go build -o hookrun ./cmd/hookrun/

# 方式二：go install
go install github.com/bluvenr/hookrun/cmd/hookrun@latest

# 方式三：下载预编译二进制
# 访问 https://github.com/bluvenr/hookrun/releases
```

交叉编译到其他平台：

```bash
# Linux amd64
GOOS=linux GOARCH=amd64 go build -o hookrun ./cmd/hookrun/

# Linux arm64（树莓派、AWS Graviton）
GOOS=linux GOARCH=arm64 go build -o hookrun ./cmd/hookrun/

# macOS（Apple Silicon）
GOOS=darwin GOARCH=arm64 go build -o hookrun ./cmd/hookrun/

# Windows
GOOS=windows GOARCH=amd64 go build -o hookrun.exe ./cmd/hookrun/
```

### 配置

1. 编辑全局配置 `config.yaml`：

```yaml
server:
  port: 9000
  route: "/webhook"
  allow_all: false             # 是否允许基础路由遍历所有配置（默认 false）

log:
  mode: "daily"                # "daily"（默认）| "single"
  path: "./logs"
  retention_days: 30           # 仅 daily 模式

config_dir: "./hooks"          # 规则 YAML 文件目录
```

2. 创建规则文件 `hooks/my-app.yaml`：

```yaml
name: "my-app-deploy"

auth:
  token:
    source: "header"
    key: "X-Webhook-Token"
    value: "your-secret"

execution:
  policy: "block"

rules:
  - name: "deploy-main"
    filters:
      - type: "header"
        key: "X-GitHub-Event"
        operator: "eq"
        value: "push"
      - type: "body"
        key: "ref"
        operator: "eq"
        value: "refs/heads/main"
    actions:
      - type: "command"
        cmd: "cd /var/www/my-app && git pull origin main"
        timeout: 30
      - type: "command"
        cmd: "cd /var/www/my-app && npm install --production && npm run build"
        timeout: 120
      - type: "script"
        path: "./scripts/deploy.sh"
        args: ["production"]
        timeout: 300
```

### 运行

```bash
# 校验配置
hookrun validate

# 启动服务（后台守护模式）
hookrun start

# 前台模式（便于调试）
hookrun start -f
```

### Docker

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o hookrun ./cmd/hookrun/

FROM alpine:latest
RUN apk --no-cache add bash
WORKDIR /app
COPY --from=builder /build/hookrun /app/hookrun
COPY config.yaml /app/
COPY hooks/ /app/hooks/
EXPOSE 9000
ENTRYPOINT ["/app/hookrun", "start", "-f", "-c", "/app/config.yaml"]
```

```bash
docker build -t hookrun:latest .
docker run -d --name hookrun \
  -p 9000:9000 \
  -v ./hooks:/app/hooks \
  -v ./logs:/app/logs \
  --restart unless-stopped \
  hookrun:latest
```

### Webhook 路由

| 请求路径 | 行为 |
|----------|------|
| `/webhook/my-app` | 直接定位 `hooks/my-app.yaml`，匹配第一个规则执行 |
| `/webhook` | 遍历所有配置，匹配第一个规则后停止（受 `allow_all` 控制） |
| `/health` | 健康检查端点，用于监控集成 |

## CLI 命令

| 命令 | 说明 |
|------|------|
| `hookrun start` | 启动服务（默认后台守护，`-f` 前台模式） |
| `hookrun stop` | 停止服务 |
| `hookrun restart` | 重启服务 |
| `hookrun status` | 查看运行状态（PID、端口、规则数、运行时长） |
| `hookrun reload` | 热重载所有 YAML 配置 |
| `hookrun validate` | 校验所有 YAML 文件语法 |
| `hookrun version` | 查看版本信息 |

## 配置详解

### 请求验证（Auth）

Token、HMAC 签名和 IP 白名单为 **AND** 关系，配置了的都必须通过：

```yaml
auth:
  token:
    source: "header"           # "header" 或 "query"
    key: "X-Webhook-Token"
    value: "secret123"
  hmac:
    header: "X-Hub-Signature-256"   # GitHub 签名 Header
    secret: "your-webhook-secret"   # HMAC 密钥（来自 GitHub Webhook 设置）
    algorithm: "sha256"              # "sha256"（默认）| "sha1" | "sha512"
  ip_whitelist:
    - "192.168.1.100"
    - "10.0.0.0/24"            # 支持 CIDR
```

### 过滤条件（Filters）

同一规则内多个 filter 为 **AND** 关系（全部匹配才触发）：

```yaml
filters:
  - type: "header"             # "header" | "query" | "body"
    key: "X-GitHub-Event"
    operator: "eq"             # "eq" | "ne" | "contains" | "regex"
    value: "push"
  - type: "body"
    key: "commits[0].message"  # 支持 JSON path
    operator: "contains"
    value: "release"
```

### 执行策略（Execution）

支持文件级和规则级继承，规则级优先：

```yaml
# 文件级（默认应用于所有规则）
execution:
  policy: "block"              # "block" | "always" | "cooldown"
  cooldown_seconds: 300        # 仅 cooldown 模式

rules:
  - name: "light-task"
    execution:
      policy: "always"         # 覆盖文件级设置
    ...
```

| 策略 | 行为 | 适用场景 |
|------|------|----------|
| `block` | 上次未执行完则拒绝（409） | 部署、构建 |
| `always` | 每次都新开执行 | 无状态通知 |
| `cooldown` | 冷却时间内拒绝（429） | 限频场景 |

### 动作（Actions）

```yaml
actions:
  - type: "command"            # "command" | "script" | "webhook" | "relay"
    cmd: "echo hello"
    timeout: 60                # 超时秒数
    isolate: false             # 是否子进程隔离
    continue_on_error: false   # 失败后是否继续下一个
  - type: "command"
    # 模板变量：将请求数据直接嵌入命令
    cmd: "git checkout {{.body.ref}}"
  - type: "command"
    # pass_args：提取请求数据并追加为尾部参数
    cmd: "echo 'Deploying:'"
    pass_args:
      - source: "body"
        key: "ref"
      - source: "header"
        key: "X-GitHub-Event"
  - type: "script"
    path: "./scripts/deploy.sh"
    args: ["production"]
    timeout: 300
    isolate: true
    # env_from：将请求数据注入为环境变量（自动添加 HOOKRUN_ 前缀）
    env_from:
      - source: "body"
        key: "ref"
        env: "GIT_REF"            # → $HOOKRUN_GIT_REF
      - source: "header"
        key: "X-GitHub-Event"
        env: "GITHUB_EVENT"       # → $HOOKRUN_GITHUB_EVENT
    # 默认环境变量始终可用：$HOOKRUN_RAW_BODY、$HOOKRUN_TRIGGER_IP
  - type: "command"
    cmd: "deploy.sh"
    retry:                      # 失败时指数退避重试
      max_attempts: 3
      interval_seconds: 5
  - type: "webhook"
    url: "https://api.example.com/deploy"
    method: "POST"
    headers:
      Authorization: "Bearer your-token"
    forward_headers: ["X-GitHub-Event"]   # 白名单转发指定 Header
    body: '{"event":"{{.header.X-GitHub-Event}}","payload":{{.raw_body}}}'
    timeout: 30
  - type: "relay"                  # 中转到其他 HookRun 实例
    relay:
      targets:
        - url: "http://10.0.0.2:9000/webhook/deploy-app"   # 静态目标
          token: "relay-secret-B"
        - url: "http://10.0.0.3:9000/webhook/deploy-app"
          token: "relay-secret-C"
        - tag: "prod"                                      # 动态：匹配所有带 "prod" 标签的已注册实例
      forward_headers: ["X-GitHub-Event"]
      timeout: 30
      max_relay_hops: 3
```

### 日志（Logging）

```yaml
# 全局日志（config.yaml）
log:
  mode: "daily"                # "daily" | "single"
  path: "./logs"
  retention_days: 30           # 仅 daily 模式
  # max_size_mb: 0             # 仅 single 模式，0 = 不限制

# 规则级日志（规则 YAML）—— 双写：全局 + 此文件
log:
  path: "./logs/my-app.log"
```

## 响应格式

所有响应为 JSON 格式：

```json
{"code": 200, "message": "ok", "config": "my-app", "rule": "deploy-main", "actions": 3}
{"code": 401, "message": "Authentication failed"}
{"code": 409, "message": "Task 'my-app/deploy-main' is running, please try again later"}
{"code": 429, "message": "Task 'my-app/deploy-main' is in cooldown, retry in 120 seconds"}
{"code": 404, "message": "Config 'not-exist' not found"}
```

## 健康检查

```bash
curl http://localhost:9000/health
```

```json
{"status": "ok", "uptime": "2h30m15s", "rules": 3, "version": "x.y.z"}
```

可对接 Prometheus（`blackbox_exporter`）、Uptime Kuma、Nagios 等任何支持 HTTP 探针的监控工具。

## 文档

| 文档 | 说明 |
|------|------|
| [配置参数说明](docs/configuration_zh.md) | 所有配置字段的完整参数参考 |
| [使用指南](docs/usage_zh.md) | CLI 命令、路由、响应格式和常见场景 |
| [部署说明](docs/deployment_zh.md) | 构建、systemd、Docker、Windows 及反向代理部署 |

## 项目结构

```
HookRun/
├── cmd/hookrun/               # CLI 入口
├── internal/
│   ├── config/                # 配置解析与验证
│   ├── server/                # HTTP 服务与路由
│   ├── engine/                # 匹配引擎（Auth + Filter + Policy）
│   ├── executor/              # 命令/脚本执行器
│   ├── engine/webhook.go      # Webhook 动作转发
│   ├── logger/                # 日志模块
│   └── daemon/                # 守护进程管理
├── config.yaml                # 全局配置
├── hooks/                     # 规则 YAML 目录
│   └── example.yaml
└── docs/                      # 文档
```

## 参与贡献

欢迎各种形式的贡献：

- **报告 Bug** — 在 [Issues](https://github.com/bluvenr/hookrun/issues) 中提交，附上复现步骤
- **功能建议** — 分享你的使用场景和想法
- **完善文档** — 修正错别字、补充示例、翻译文档
- **提交代码** — Fork、建分支、提交 Pull Request

提交 PR 前请运行 `hookrun validate` 确保配置校验通过。

## License

[MIT](LICENSE)
