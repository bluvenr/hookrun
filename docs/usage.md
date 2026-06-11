# Usage Guide

This guide covers day-to-day usage of HookRun: CLI commands, routing behavior, response formats, and common scenarios.

---

## 1. CLI Commands

All commands support a global `-c` flag to specify the config file path:

```bash
hookrun -c /path/to/config.yaml <command>
```

Default config path: `config.yaml` (current directory).

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
  Allow all: true
  First match only: true
  Log path: ./logs
  Log retention: 30 days
  Config dir: ./hooks
  Rule files loaded: 2
    - github-auto-deploy (2 rules: push-to-main, tag-release)
    - gitlab-ci-trigger (1 rules: pipeline-complete)
```

Run `validate` before starting or reloading to catch configuration errors early.

---

### `version` — Show Version Info

```bash
hookrun version
```

Output:

```
HookRun v1.0.0
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

Behavior controlled by `allow_all` and `first_match_only`:

| `allow_all` | `first_match_only` | Behavior |
|-------------|-------------------|----------|
| `true` (default) | `true` (default) | Iterate all configs, stop at first matching rule |
| `true` | `false` | Iterate all configs, execute ALL matching rules |
| `false` | — | Return 400 error (must use targeted route) |

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
{"status": "ok", "uptime": "2h30m15s", "rules": 3, "version": "1.0.0"}
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

### Multiple Responses

When `first_match_only: false`, the response is an array:

```json
[
  {"code": 200, "message": "ok", "config": "app-a", "rule": "deploy", "actions": 2},
  {"code": 200, "message": "ok", "config": "app-b", "rule": "sync", "actions": 1}
]
```

---

## 4. Common Scenarios

### Scenario 1: GitHub Push Auto-Deploy

```yaml
name: "github-deploy"
auth:
  token:
    source: "header"
    key: "X-Hub-Signature-256"
    value: "sha256=your-secret"
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
        cmd: "cd /var/www/app && git pull && npm run build"
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

---

## 5. Logging

- Logs are written to `{log.path}/hookrun-YYYY-MM-DD.log`
- A new file is created each day
- Files older than `retention_days` are automatically deleted on startup
- Each webhook request and execution result is logged with timestamp

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
