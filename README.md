# HookRun

[![Go Version](https://img.shields.io/github/go-mod/go-version/bluvenr/hookrun)](https://go.dev/)
[![License](https://img.shields.io/github/license/bluvenr/hookrun)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/bluvenr/hookrun)](https://goreportcard.com/report/github.com/bluvenr/hookrun)

[中文文档](README_zh.md)

A lightweight webhook action engine — execute custom commands and scripts based on YAML rules when webhook requests arrive.

Single binary, cross-platform (Linux / Windows / macOS).

## Demo

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

# Incoming webhook
$ curl -X POST http://localhost:9000/webhook/github-auto-deploy \
    -H "X-Webhook-Token: your-secret-token" \
    -H "X-GitHub-Event: push" \
    -d '{"ref":"refs/heads/main"}'

→ {"code":200,"message":"ok","config":"github-auto-deploy","rule":"push-to-main","actions":3}
```

## Features

- **YAML Driven** — All rules defined in YAML files, zero coding required
- **Targeted Routing** — `/webhook/{filename}` directly routes to specific config for efficient matching
- **Flexible Auth** — Token (Header/Query) + IP whitelist with AND relationship
- **Multi-Condition Filters** — Match against Header / Query / Body with operators: `eq` `ne` `contains` `regex`
- **Execution Policies** — Three modes: `block` (prevent concurrency), `always` (always execute), `cooldown` (rate limiting)
- **Policy Inheritance** — File-level → Rule-level override
- **Matching Control** — `first_match_only` toggle for first-match-stops vs execute-all
- **Hot Reload** — Reload configs at runtime without restarting
- **Log Management** — Daily rotation with automatic cleanup

## Documentation

| Document | Description |
|----------|-------------|
| [Configuration Reference](docs/configuration.md) | Complete parameter reference for all config fields |
| [Usage Guide](docs/usage.md) | CLI commands, routing, response formats, and common scenarios |
| [Deployment Guide](docs/deployment.md) | Build, systemd, Docker, Windows, and reverse proxy setup |

[中文版文档](#中文文档)

<a id="中文文档"></a>

## 中文文档

| 文档 | 说明 |
|------|------|
| [配置参数说明](docs/configuration_zh.md) | 所有配置字段的完整参数参考 |
| [使用指南](docs/usage_zh.md) | CLI 命令、路由、响应格式和常见场景 |
| [部署说明](docs/deployment_zh.md) | 构建、systemd、Docker、Windows 及反向代理部署 |

## Quick Start

### Install

```bash
# Build from source
git clone https://github.com/bluvenr/hookrun.git
cd HookRun
go build -o hookrun ./cmd/hookrun/
```

### Configure

1. Edit global config `config.yaml`:

```yaml
server:
  port: 9000
  route: "/webhook"
  allow_all: true              # allow base route to iterate all configs
  first_match_only: true       # stop at first matching rule

log:
  path: "./logs"
  retention_days: 30

config_dir: "./hooks"          # rule YAML files directory
```

2. Create a rule file `hooks/my-app.yaml`:

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

### Run

```bash
# Validate configuration
hookrun validate

# Start server (daemon mode)
hookrun start

# Foreground mode (for debugging)
hookrun start -f
```

### Webhook Routing

| URL Pattern | Behavior |
|-------------|----------|
| `/webhook/my-app` | Directly route to `hooks/my-app.yaml`, execute first matching rule |
| `/webhook` | Iterate all configs (controlled by `allow_all` and `first_match_only`) |

## CLI Commands

| Command | Description |
|---------|-------------|
| `hookrun start` | Start server (daemon by default, `-f` for foreground) |
| `hookrun stop` | Stop the running server |
| `hookrun restart` | Restart the server |
| `hookrun status` | Show status (PID, port, rules count, uptime) |
| `hookrun reload` | Hot-reload all YAML configurations |
| `hookrun validate` | Validate all YAML files |
| `hookrun version` | Show version information |

## Configuration Guide

### Authentication

Token and IP whitelist use **AND** relationship — all configured checks must pass:

```yaml
auth:
  token:
    source: "header"           # "header" or "query"
    key: "X-Webhook-Token"
    value: "secret123"
  ip_whitelist:
    - "192.168.1.100"
    - "10.0.0.0/24"            # CIDR supported
```

### Filters

Multiple filters within a rule use **AND** relationship (all must match):

```yaml
filters:
  - type: "header"             # "header" | "query" | "body"
    key: "X-GitHub-Event"
    operator: "eq"             # "eq" | "ne" | "contains" | "regex"
    value: "push"
  - type: "body"
    key: "commits[0].message"  # JSON path supported
    operator: "contains"
    value: "release"
```

### Execution Policy

Supports file-level and rule-level inheritance. Rule-level takes priority:

```yaml
# File-level (default for all rules)
execution:
  policy: "block"              # "block" | "always" | "cooldown"
  cooldown_seconds: 300        # only for cooldown mode

rules:
  - name: "light-task"
    execution:
      policy: "always"         # overrides file-level
    ...
```

| Policy | Behavior | Use Case |
|--------|----------|----------|
| `block` | Reject if still running (409) | Deploy, build |
| `always` | Always spawn new execution | Stateless notifications |
| `cooldown` | Reject within cooldown period (429) | Rate-limited scenarios |

### Actions

```yaml
actions:
  - type: "command"            # "command" | "script"
    cmd: "echo hello"
    timeout: 60                # timeout in seconds
    isolate: false             # run in isolated subprocess
    continue_on_error: false   # continue to next action on failure
  - type: "script"
    path: "./scripts/deploy.sh"
    args: ["production"]
    timeout: 300
    isolate: true
```

## Response Format

All responses are JSON with English messages:

```json
{"code": 200, "message": "ok", "config": "my-app", "rule": "deploy-main", "actions": 3}
{"code": 401, "message": "Authentication failed"}
{"code": 409, "message": "Task 'my-app/deploy-main' is running, please try again later"}
{"code": 429, "message": "Task 'my-app/deploy-main' is in cooldown, retry in 120 seconds"}
{"code": 404, "message": "Config 'not-exist' not found"}
```

## Project Structure

```
HookRun/
├── cmd/hookrun/               # CLI entry point
├── internal/
│   ├── config/                # Config parsing & validation
│   ├── server/                # HTTP server & routing
│   ├── engine/                # Matching engine (Auth + Filter + Policy)
│   ├── executor/              # Command/script executor
│   ├── logger/                # Logging module
│   └── daemon/                # Daemon process management
├── config.yaml                # Global configuration
├── hooks/                     # Rule YAML directory
│   └── example.yaml
└── docs/                      # Design documentation
```

## License

MIT
