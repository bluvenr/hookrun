# HookRun

[![Go Version](https://img.shields.io/github/go-mod/go-version/bluvenr/hookrun)](https://go.dev/)
[![License](https://img.shields.io/github/license/bluvenr/hookrun)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/bluvenr/hookrun)](https://goreportcard.com/report/github.com/bluvenr/hookrun)

[English](README.md)

轻量级 Webhook 动作执行引擎 —— 基于 YAML 配置，接收 Webhook 请求后自动执行自定义命令/脚本。

单二进制文件，跨平台支持（Linux / Windows / macOS）。

## 运行演示

```
$ hookrun validate
Validating config: config.yaml
PASS: All configurations are valid
  Server port: 9000
  Webhook route: /webhook
  Allow all: true
  First match only: true
  Log path: ./logs
  Log retention: 30 days
  Config dir: ./hooks
  Rule files loaded: 2
    - github-auto-deploy (2 rules: push-to-main, tag-release)
    - gitlab-ci-trigger (1 rules: pipeline-complete)

$ hookrun start
HookRun started in background (PID: 8421)

$ hookrun status
Status:  running
PID:     8421
Port:    9000
Rules:   2 config(s)
Uptime:  12m30s

# 收到 webhook 推送
$ curl -X POST http://localhost:9000/webhook/github-auto-deploy \
    -H "X-Webhook-Token: your-secret-token" \
    -H "X-GitHub-Event: push" \
    -d '{"ref":"refs/heads/main"}'

→ {"code":200,"message":"ok","config":"github-auto-deploy","rule":"push-to-main","actions":3}
```

## 特性

- **YAML 驱动** — 所有规则通过 YAML 文件定义，无需编码
- **定向路由** — 支持 `/webhook/{filename}` 精准定位配置，高效匹配
- **灵活验证** — Token 验证（Header/Query）+ IP 白名单，AND 关系组合
- **多条件过滤** — 支持 Header / Query / Body 匹配，操作符：`eq` `ne` `contains` `regex`
- **执行策略** — 三种模式：`block`（防并发）、`always`（始终执行）、`cooldown`（冷却限频）
- **策略继承** — 文件级 → Rule 级逐层覆盖
- **匹配可控** — `first_match_only` 开关控制匹配即停或全量执行
- **热重载** — 运行中 reload 配置，无需重启服务
- **日志管理** — 按日切割、自动清理过期日志

## 文档

| 文档 | 说明 |
|------|------|
| [配置参数说明](docs/configuration_zh.md) | 所有配置字段的完整参数参考 |
| [使用指南](docs/usage_zh.md) | CLI 命令、路由、响应格式和常见场景 |
| [部署说明](docs/deployment_zh.md) | 构建、systemd、Docker、Windows 及反向代理部署 |

[English Docs](../docs/)

| Document | Description |
|----------|-------------|
| [Configuration Reference](docs/configuration.md) | Complete parameter reference for all config fields |
| [Usage Guide](docs/usage.md) | CLI commands, routing, response formats, and common scenarios |
| [Deployment Guide](docs/deployment.md) | Build, systemd, Docker, Windows, and reverse proxy setup |

## 快速开始

### 安装

```bash
# 从源码编译
git clone https://github.com/bluvenr/hookrun.git
cd HookRun
go build -o hookrun ./cmd/hookrun/
```

### 配置

1. 编辑全局配置 `config.yaml`：

```yaml
server:
  port: 9000
  route: "/webhook"
  allow_all: true              # 是否允许基础路由遍历所有配置
  first_match_only: true       # 匹配第一个规则后停止

log:
  path: "./logs"
  retention_days: 30

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

### Webhook 路由

| 请求路径 | 行为 |
|----------|------|
| `/webhook/my-app` | 直接定位 `hooks/my-app.yaml`，匹配第一个 rule 执行 |
| `/webhook` | 遍历所有 yaml（受 `allow_all` 和 `first_match_only` 控制） |

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

Token 和 IP 白名单为 **AND** 关系，设置了的都必须通过：

```yaml
auth:
  token:
    source: "header"           # "header" 或 "query"
    key: "X-Webhook-Token"
    value: "secret123"
  ip_whitelist:
    - "192.168.1.100"
    - "10.0.0.0/24"            # 支持 CIDR
```

### 过滤条件（Filters）

同一 rule 内多个 filter 为 **AND** 关系（全部匹配才触发）：

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

支持文件级和 Rule 级继承，Rule 级优先：

```yaml
# 文件级（默认）
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
  - type: "command"            # "command" | "script"
    cmd: "echo hello"
    timeout: 60                # 超时秒数
    isolate: false             # 是否子进程隔离
    continue_on_error: false   # 失败后是否继续下一个
  - type: "script"
    path: "./scripts/deploy.sh"
    args: ["production"]
    timeout: 300
    isolate: true
```

## 响应格式

所有响应为 JSON 格式，英文消息：

```json
{"code": 200, "message": "ok", "config": "my-app", "rule": "deploy-main", "actions": 3}
{"code": 401, "message": "Authentication failed"}
{"code": 409, "message": "Task 'my-app/deploy-main' is running, please try again later"}
{"code": 429, "message": "Task 'my-app/deploy-main' is in cooldown, retry in 120 seconds"}
{"code": 404, "message": "Config 'not-exist' not found"}
```

## 项目结构

```
HookRun/
├── cmd/hookrun/               # CLI 入口
├── internal/
│   ├── config/                # 配置解析与验证
│   ├── server/                # HTTP 服务与路由
│   ├── engine/                # 匹配引擎（Auth + Filter + Policy）
│   ├── executor/              # 命令/脚本执行器
│   ├── logger/                # 日志模块
│   └── daemon/                # 守护进程管理
├── config.yaml                # 全局配置
├── hooks/                     # 规则 YAML 目录
│   └── example.yaml
└── docs/                      # 设计文档
```

## License

MIT
