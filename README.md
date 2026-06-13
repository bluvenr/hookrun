# HookRun

[![Release](https://img.shields.io/github/v/release/bluvenr/hookrun?include_prereleases&sort=semver)](https://github.com/bluvenr/hookrun/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/bluvenr/hookrun)](https://go.dev/)
[![Go Report Card](https://goreportcard.com/badge/github.com/bluvenr/hookrun)](https://goreportcard.com/report/github.com/bluvenr/hookrun)
[![License](https://img.shields.io/github/license/bluvenr/hookrun)](LICENSE)

[中文文档](README_zh.md) | [Website](https://bluvenr.github.io/hookrun/)

A lightweight webhook action engine — execute custom commands and scripts based on YAML rules when webhook requests arrive.

Single binary under 5MB. No database. No container runtime. Cross-platform (Linux / Windows / macOS).

```
┌──────────┐     POST /webhook/{name}     ┌─────────────┐     Auth + Filter     ┌────────────┐     Execute     ┌──────────┐
│  GitHub   │ ──────────────────────────▶ │   HookRun    │ ──────────────────▶  │   Engine   │ ─────────────▶ │ Commands │
│  GitLab   │     Token / HMAC / IP       │  (routing &  │     Match rules      │  (policy   │    shell &     │ Scripts  │
│  Grafana  │ ◀────────────────────────── │   matching)  │ ◀──────────────────  │   check)   │ ◀───────────── │ Deploy   │
│  Custom   │       JSON response         │              │     Action result    │            │    stdout      │          │
└──────────┘                              └─────────────┘                      └────────────┘                └──────────┘
```

## Why HookRun?

A purpose-built action engine for webhook automation, compared with general-purpose tools:

| | HookRun | [adnanh/webhook](https://github.com/adnanh/webhook) | n8n | Huginn |
|---|---------|------|------|------|
| **Execution Policy** | block / always / cooldown — 3 modes | No concurrency control, always triggers | Partial (retry / wait) | Scenario-based triggers |
| **Authentication** | Token + HMAC + IP whitelist, top-level auth, AND-combined | Token + HMAC, nested in trigger-rule, scattered config | Basic / Header / JWT / IP whitelist | Devise user auth + OAuth services |
| **Binary Size** | Single file ~3MB, zero deps | Single file ~7MB, zero deps | Docker + SQLite (default) / PostgreSQL | Docker + MySQL / PostgreSQL |
| **Hot Reload** | Auto-watches directory, zero-config | `-hotreload` flag, specified files only | Restart required | Restart required |
| **Routing** | `/webhook/{filename}` targeted, on-demand | `/hooks/{id}` flat, single entry | Workflow engine | Scenario graph (agent links) |
| **Process Mgmt** | Built-in CLI daemon (start/stop/status) | Requires external systemd, etc. | Docker container management | Docker container management |
| **Config Format** | YAML (readable, comments supported) | JSON / YAML | Visual editor / JSON | JSON (agent config) |
| **Health Check** | Built-in `/health` endpoint | No built-in endpoint | Has health check | Has health check |
| **License** | MIT, full freedom | MIT, full freedom | Fair-code (SUL), commercial restrictions | MIT, full freedom |

- **Security First** — Token auth, HMAC signature verification, and IP whitelisting with AND-combined enforcement
- **Lightweight** — One binary, under 5MB, zero dependencies. No database, no container runtime. `scp` it to your server and run — minimal resources required
- **Full Control** — MIT licensed. Self-hosted. Your rules, your data, your infrastructure

## Demo

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
- **Flexible Auth** — Token (Header/Query) + HMAC Signature (GitHub/GitLab) + IP whitelist with AND relationship
- **Multi-Condition Filters** — Match against Header / Query / Body with operators: `eq` `ne` `contains` `regex`
- **Execution Policies** — Three modes: `block` (prevent concurrency), `always` (always execute), `cooldown` (rate limiting)
- **Policy Inheritance** — File-level → Rule-level override
- **File-Level Filters** — Global constraints applied to all rules, AND with rule-level filters
- **Parameter Passing** — Template variables (`{{.body.ref}}`) and `pass_args` to inject request data into commands
- **Hot Reload** — Reload configs at runtime without restarting
- **Log Management** — Daily/single mode with auto-cleanup, per-rule independent log files
- **Health Endpoint** — Built-in `/health` endpoint for monitoring integration (Prometheus, Uptime Kuma, etc.)

## Use Cases

### Git Auto Deploy

Push to GitHub/GitLab, server automatically pulls, builds, and deploys.

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

### CI/CD Pipeline Trigger

Chain webhook events into multi-step build pipelines with timeout control and concurrency protection.

### Monitoring Alert Response

Receive alerts from Grafana, Prometheus, or any service and run automated remediation scripts.

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
        cmd: "echo 'CPU alert received, scaling up...'"
        timeout: 30
      - type: "script"
        path: "./scripts/scale-up.sh"
        timeout: 120
```

### Custom Automation

Any HTTP POST becomes a trigger. Sync data, send notifications, manage infrastructure, and more.

## Quick Start

### Install

```bash
# Option 1: Build from source
git clone https://github.com/bluvenr/hookrun.git
cd hookrun
go build -o hookrun ./cmd/hookrun/

# Option 2: go install
go install github.com/bluvenr/hookrun/cmd/hookrun@latest

# Option 3: Download pre-built binary
# Visit https://github.com/bluvenr/hookrun/releases
```

Cross-compile for other platforms:

```bash
# Linux amd64
GOOS=linux GOARCH=amd64 go build -o hookrun ./cmd/hookrun/

# Linux arm64 (Raspberry Pi, AWS Graviton)
GOOS=linux GOARCH=arm64 go build -o hookrun ./cmd/hookrun/

# macOS (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -o hookrun ./cmd/hookrun/

# Windows
GOOS=windows GOARCH=amd64 go build -o hookrun.exe ./cmd/hookrun/
```

### Configure

1. Edit global config `config.yaml`:

```yaml
server:
  port: 9000
  route: "/webhook"
  allow_all: false             # require targeted routing (default: false)

log:
  mode: "daily"                # "daily" (default) | "single"
  path: "./logs"
  retention_days: 30           # only for daily mode

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

### Webhook Routing

| URL Pattern | Behavior |
|-------------|----------|
| `/webhook/my-app` | Directly route to `hooks/my-app.yaml`, execute first matching rule |
| `/webhook` | Iterate all configs, stop at first matching rule (controlled by `allow_all`) |
| `/health` | Health check endpoint for monitoring |

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

Token, HMAC signature, and IP whitelist use **AND** relationship — all configured checks must pass:

```yaml
auth:
  token:
    source: "header"           # "header" or "query"
    key: "X-Webhook-Token"
    value: "secret123"
  hmac:
    header: "X-Hub-Signature-256"   # GitHub signature header
    secret: "your-webhook-secret"   # HMAC secret from GitHub webhook settings
    algorithm: "sha256"              # "sha256" (default) | "sha1" | "sha512"
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
  - type: "command"
    # Template variables: embed request data directly into commands
    cmd: "git checkout {{.body.ref}}"
  - type: "command"
    # pass_args: extract and append as trailing arguments
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
```

### Logging

```yaml
# Global log (config.yaml)
log:
  mode: "daily"                # "daily" | "single"
  path: "./logs"
  retention_days: 30           # daily mode only
  # max_size_mb: 0             # single mode only, 0 = unlimited

# Per-rule log (rule YAML) — dual-write: global + this file
log:
  path: "./logs/my-app.log"
```

## Response Format

All responses are JSON:

```json
{"code": 200, "message": "ok", "config": "my-app", "rule": "deploy-main", "actions": 3}
{"code": 401, "message": "Authentication failed"}
{"code": 409, "message": "Task 'my-app/deploy-main' is running, please try again later"}
{"code": 429, "message": "Task 'my-app/deploy-main' is in cooldown, retry in 120 seconds"}
{"code": 404, "message": "Config 'not-exist' not found"}
```

## Health Check

```bash
curl http://localhost:9000/health
```

```json
{"status": "ok", "uptime": "2h30m15s", "rules": 3, "version": "1.1.1"}
```

Integrate with Prometheus (`blackbox_exporter`), Uptime Kuma, Nagios, or any monitoring tool that supports HTTP probes.

## Documentation

| Document | Description |
|----------|-------------|
| [Configuration Reference](docs/configuration.md) | Complete parameter reference for all config fields |
| [Usage Guide](docs/usage.md) | CLI commands, routing, response formats, and common scenarios |
| [Deployment Guide](docs/deployment.md) | Build, systemd, Docker, Windows, and reverse proxy setup |

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
└── docs/                      # Documentation
```

## Contributing

Contributions are welcome! Here are some ways you can help:

- **Report bugs** — Open an [issue](https://github.com/bluvenr/hookrun/issues) with reproduction steps
- **Suggest features** — Share your use cases and ideas
- **Improve docs** — Fix typos, add examples, translate documentation
- **Submit code** — Fork, branch, and send a pull request

Please run `hookrun validate` and ensure all tests pass before submitting PRs.

## License

[MIT](LICENSE)
