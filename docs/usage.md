# Usage Guide

[中文](usage_zh.md)

This guide covers day-to-day usage of HookRun: CLI commands, routing behavior, response formats, and common scenarios.

---

## 1. CLI Commands

All commands support a global `-c` flag to specify the config file path:

```bash
hookrun -c /path/to/config.yaml <command>
```

Default config path: `config.yaml` (current directory).

---

### `init` — Initialize Configuration

```bash
# Initialize with generic template (default)
hookrun init

# Initialize with GitHub webhook template
hookrun init --template github

# Initialize with GitLab webhook template
hookrun init --template gitlab

# Overwrite existing files without prompting
hookrun init --force
```

Creates `config.yaml` and `hooks/example.yaml` in the current directory.

Available templates:

| Template | Description |
|----------|-------------|
| `generic` | Generic webhook template with token auth |
| `github` | GitHub webhook auto-deploy with HMAC auth |
| `gitlab` | GitLab webhook auto-deploy with token auth |

---

### `start` — Start the Server

```bash
# Daemon mode (default, runs in background)
hookrun start

# Foreground mode (for debugging / terminal)
hookrun start -f

# With custom config path
hookrun start -c /etc/hookrun/config.yaml
```

- Daemon mode creates a PID file in `~/.hookrun/`
- Foreground mode logs directly to the terminal

---

### `stop` — Stop the Server

```bash
hookrun stop
```

- **Linux/macOS**: Sends SIGTERM to the process
- **Windows**: Uses a signal file IPC mechanism (polled every 2 seconds)

---

### `restart` — Restart the Server

```bash
hookrun restart
```

Equivalent to `stop` + `start`. Waits up to 15 seconds for the process to stop before starting.

---

### `status` — Show Server Status

```bash
hookrun status
```

Output example:

```
Status:  running
PID:     12345
Port:    9000
Rules:   3 config(s)
Uptime:  2h30m15s
Started: 2026-06-11 10:00:00
```

If the server is stopped:

```
Status: stopped (no PID file)
```

---

### `reload` — Hot-Reload Configuration

```bash
hookrun reload
```

- Reloads all YAML files (global + rule configs) without restarting
- **Linux/macOS**: Uses the HTTP reload API (`POST /_reload`)
- **Windows**: Uses signal file IPC

After reload, new rules take effect immediately for subsequent requests.

---

### `validate` — Validate Configuration

```bash
hookrun validate
```

Output example:

```
Validating config: config.yaml
PASS: All configurations are valid
  Server port: 9000
  Webhook route: /webhook
  Allow all: false
  Max body size: 10 MB
  Log mode: daily
  Log path: ./logs
  Log retention: 30 days
  Config dir: ./hooks
  Rule files loaded: 1
  Relay registry: disabled
    - github-auto-deploy (2 rules: push-to-main, tag-release) [auth: token]
```

Run `validate` before starting or reloading to catch configuration errors early.

---

### `version` — Show Version Info

```bash
hookrun version
```

Output:

```
HookRun vx.y.z
Build time: 2026-06-11
Go version: go1.23.3
OS/Arch:    linux/amd64
```

---

## 2. Webhook Routing

HookRun supports two URL patterns:

### Targeted Route: `/webhook/{filename}`

Routes directly to a specific YAML config file (matched by filename without extension).

```
POST /webhook/my-app
→ Matches: hooks/my-app.yaml
→ Executes: first matching rule in that file
```

- If the config file doesn't exist → 404
- If no rule matches → 200 "No matching rules"

### Base Route: `/webhook`

Behavior controlled by `allow_all`:

| `allow_all` | Behavior |
|-------------|----------|
| `true` | Iterate all configs, stop at first matching rule |
| `false` (default) | Return 400 error (must use targeted route) |

### Route Matching Examples

```
# Targeted: directly to hooks/frontend.yaml
POST /webhook/frontend

# Base route: iterate all YAML files
POST /webhook
```

### Health Check

```
GET /health
```

Returns:

```json
{"status": "ok", "uptime": "2h30m15s", "rules": 3, "version": "x.y.z"}
```

### Relay Registry API

Available only when `server.relay_registry_token` is configured (see [Configuration Reference](configuration.md#serverrelay_registry_token--dynamic-target-discovery)).

```
POST   /api/relay/register   # Register or refresh a target
DELETE /api/relay/register   # Unregister a target
GET    /api/relay/targets    # List all registered targets
```

All registry API requests require the Bearer token:

```bash
curl -H "Authorization: Bearer your-registry-secret" \
     http://main-hookrun:9000/api/relay/targets
```

---

## 3. Response Format

All webhook responses are JSON with English messages.

### Success (200)

```json
{
  "code": 200,
  "message": "ok",
  "config": "github-auto-deploy",
  "rule": "push-to-main",
  "actions": 3
}
```

### No Matching Rules (200)

```json
{
  "code": 200,
  "message": "No matching rules",
  "config": "github-auto-deploy"
}
```

### Authentication Failed (401)

```json
{
  "code": 401,
  "message": "Authentication failed"
}
```

### Config Not Found (404)

```json
{
  "code": 404,
  "message": "Config 'not-exist' not found"
}
```

### Execution Blocked (409)

When `policy: "block"` and a task is still running:

```json
{
  "code": 409,
  "message": "Task 'github-auto-deploy/push-to-main' is running, please try again later"
}
```

### Cooldown Active (429)

When `policy: "cooldown"` and the cooldown window is active:

```json
{
  "code": 429,
  "message": "Task 'github-auto-deploy/push-to-main' is in cooldown, retry in 120 seconds"
}
```

### Base Route Disabled (400)

When `allow_all: false` and a request is sent to `/webhook`:

```json
{
  "code": 400,
  "message": "Base route iteration is disabled, please specify target: /webhook/{name}"
}
```

### Relay Registry — Register (200)

```json
{
  "status": "registered",
  "targets_count": 3
}
```

### Relay Registry — Unregister (200)

```json
{
  "status": "unregistered",
  "targets_count": 2
}
```

### Relay Registry — List Targets (200)

```json
{
  "targets": [
    {
      "url": "http://10.0.0.5:9000/webhook/deploy-app",
      "tags": ["web", "prod"],
      "ttl": 120,
      "last_seen": "2026-06-11T10:00:00Z",
      "expires_at": "2026-06-11T10:02:00Z"
    }
  ],
  "total": 1
}
```

### Relay Registry — Errors

| Code | Meaning |
|------|--------|
| `401` | Missing or invalid Bearer token |
| `405` | Method not allowed |
| `429` | Registry is full (`max_registry_entries` reached) |
| `503` | Relay registry not enabled (no `relay_registry_token` configured) |

---

## 4. Common Scenarios

### Scenario 1: GitHub Push Auto-Deploy

```yaml
name: "github-deploy"
auth:
  hmac:
    header: "X-Hub-Signature-256"
    secret: "your-github-webhook-secret"
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
        cmd: "cd /var/www/app && git pull origin {{.body.ref}} && npm run build"
        timeout: 120
```

Webhook URL: `POST /webhook/github-deploy`

### Scenario 2: GitLab CI Pipeline Complete Notification

```yaml
name: "gitlab-notify"
auth:
  token:
    source: "header"
    key: "X-Gitlab-Token"
    value: "your-gitlab-token"
execution:
  policy: "always"
rules:
  - name: "pipeline-success"
    filters:
      - type: "header"
        key: "X-Gitlab-Event"
        operator: "eq"
        value: "Pipeline Hook"
      - type: "body"
        key: "object_attributes.status"
        operator: "eq"
        value: "success"
    actions:
      - type: "command"
        cmd: "curl -s https://slack.com/api/chat.postMessage -d 'text=Pipeline succeeded!'"
        timeout: 10
```

### Scenario 3: Rate-Limited Deployment

```yaml
name: "rate-limited-deploy"
execution:
  policy: "cooldown"
  cooldown_seconds: 600    # max once per 10 minutes
rules:
  - name: "deploy"
    filters:
      - type: "header"
        key: "X-Event"
        operator: "eq"
        value: "deploy"
    actions:
      - type: "script"
        path: "/opt/scripts/deploy.sh"
        args: ["production"]
        timeout: 300
        isolate: true
```

### Scenario 4: Multiple Environments in One File

```yaml
name: "multi-env-deploy"
execution:
  policy: "block"
rules:
  - name: "deploy-staging"
    filters:
      - type: "body"
        key: "ref"
        operator: "eq"
        value: "refs/heads/develop"
    actions:
      - type: "command"
        cmd: "deploy.sh staging"
        timeout: 120

  - name: "deploy-production"
    execution:
      policy: "cooldown"
      cooldown_seconds: 1800    # 30 min cooldown for prod
    filters:
      - type: "body"
        key: "ref"
        operator: "eq"
        value: "refs/heads/main"
    actions:
      - type: "command"
        cmd: "deploy.sh production"
        timeout: 300
```

### Scenario 5: Force Targeted Routes Only

Disable base route iteration to require explicit targeting:

```yaml
# config.yaml
server:
  port: 9000
  route: "/webhook"
  allow_all: false    # must use /webhook/{filename}
```

All requests to `/webhook` return 400. Clients must use:

```
POST /webhook/frontend
POST /webhook/backend
POST /webhook/api
```

### Scenario 6: Multi-Server Relay Deployment

Forward webhooks from a central HookRun to multiple downstream instances using static targets and dynamic tag matching:

```yaml
# Upstream (main HookRun) — hooks/deploy-app.yaml
name: "deploy-app"
execution:
  policy: "block"
rules:
  - name: "relay-to-servers"
    filters:
      - type: "body"
        key: "ref"
        operator: "regex"
        value: "refs/heads/main"
    actions:
      - type: "relay"
        relay:
          targets:
            - url: "http://10.0.0.2:9000/webhook/deploy-app"   # static target
              token: "relay-secret-B"
            - tag: "prod"                                      # dynamic: all registered targets with "prod" tag
          forward_headers:
            - "X-GitHub-Event"
          timeout: 30
          max_relay_hops: 3
```

```yaml
# Upstream config.yaml — enable relay registry for dynamic discovery
server:
  port: 9000
  relay_registry_token: "your-registry-secret"
  max_relay_ttl: 300
  max_registry_entries: 100
```

Downstream instances auto-register on startup and maintain heartbeat:

```yaml
# Downstream (10.0.0.2) config.yaml
server:
  port: 9000

relay_client:
  upstream: "http://main-hookrun:9000"
  url: "http://10.0.0.2:9000/webhook/deploy-app"
  token: "relay-secret-B"
  registry_token: "your-registry-secret"
  tags: ["prod"]
  ttl: 120
```

### Scenario 7: Self-Registering Relay Client

A downstream HookRun instance auto-registers with an upstream on startup and sends periodic heartbeats. No manual registration needed:

```yaml
# Downstream config.yaml
server:
  port: 9000

relay_client:
  upstream: "http://main-hookrun:9000"          # upstream URL (required)
  url: "http://10.0.0.5:9000/webhook/deploy-app" # local reachable URL
  token: "my-auth-token"                         # auth token for relay forwarding
  registry_token: "your-registry-secret"         # registry API Bearer token
  tags: ["web", "prod"]                          # tags for target matching
  ttl: 120                                       # TTL in seconds
  webhook_path: "/webhook/deploy-app"            # used for URL auto-detection
```

- On startup, the client sends `POST /api/relay/register` to the upstream
- Heartbeat is sent every `ttl / 3` seconds to keep the registration alive
- On graceful shutdown, the client sends `DELETE /api/relay/register` to unregister
- If `url` is omitted, it is auto-detected from local IP + port + `webhook_path`

---

## 5. Logging

### Daily Mode (default)

- Logs are written to `{log.path}/hookrun-YYYY-MM-DD.log`
- A new file is created each day
- Files older than `retention_days` are automatically deleted on startup

### Single Mode

- Logs are written to `{log.path}/hookrun.log`
- One fixed file; optional size-based rotation (`max_size_mb`)
- Suitable for container environments or external log management

### Rule-Level Log

- Each rule config can specify `log.path` for an independent log file
- Logs are dual-written to both global and rule-specific log files

Each webhook request and execution result is logged with timestamp.

---

## 6. Configuration Validation Best Practices

1. **Always validate before starting**:
   ```bash
   hookrun validate && hookrun start
   ```

2. **Validate after editing**:
   ```bash
   # Edit a YAML file
   vim hooks/my-app.yaml
   # Validate without reloading
   hookrun validate
   # If valid, reload
   hookrun reload
   ```

3. **Use foreground mode for debugging**:
   ```bash
   hookrun start -f
   ```
   This shows all logs in the terminal for immediate feedback.
