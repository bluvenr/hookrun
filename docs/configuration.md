# Configuration Reference

[中文](configuration_zh.md)

HookRun uses two levels of YAML configuration:

1. **Global Config** (`config.yaml`) — Server, logging, and directory settings
2. **Rule Config** (`hooks/*.yaml`) — Authentication, filters, and actions per scenario

---

## 1. Global Config — `config.yaml`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `server.port` | int | `9000` | HTTP listen port (1–65535) |
| `server.route` | string | `/webhook` | Base webhook endpoint path |
| `server.allow_all` | bool | `false` | Allow base route (`/webhook`) to iterate all config files |
| `server.max_body_size_mb` | int | `10` | Max request body size in MB. `0` = unlimited |
| `log.mode` | string | `daily` | Log mode: `"daily"` (rotate by day) or `"single"` (one file) |
| `log.path` | string | `./logs` | Log directory (daily) or base path (single) |
| `log.retention_days` | int | `30` | Days to retain daily log files |
| `log.max_size_mb` | int | `0` (unlimited) | Max file size in MB before rotation (single mode only) |
| `config_dir` | string | `./hooks` | Directory containing rule YAML files |

### Example

```yaml
server:
  port: 9000
  route: "/webhook"
  allow_all: false

log:
  mode: "daily"                # "daily" (default) | "single"
  path: "./logs"
  retention_days: 30           # only for daily mode
  # max_size_mb: 0             # only for single mode, 0 = unlimited (default)

config_dir: "./hooks"
```

### `server.allow_all`

Controls whether the base route `/webhook` iterates through all YAML config files.

- `true` — Requests to `/webhook` iterate all config files, stopping at the first matching rule
- `false` (default) — Requests to `/webhook` return 400; clients must use `/webhook/{filename}`

### `server.max_body_size_mb`

Limits the maximum size of incoming request bodies to prevent DoS attacks.

- `10` (default) — Request bodies larger than 10 MB are rejected with HTTP 413
- `0` — No limit (use with caution)
- Any positive integer — Custom limit in MB

### `log.mode`

Controls how log files are generated.

| Mode | Behavior | File Naming |
|------|----------|-------------|
| `daily` (default) | One file per day, auto-cleanup | `hookrun-2026-06-11.log` |
| `single` | One fixed file, optional size rotation | `hookrun.log` |

**Daily mode** is suitable for long-running services with `retention_days` auto-cleanup.

**Single mode** is suitable for container environments (Docker/Kubernetes) or when external tools handle rotation. `max_size_mb: 0` means no size rotation (unlimited).

```yaml
# Container-friendly: single file, no rotation
log:
  mode: "single"
  path: "./logs"
  # max_size_mb: 0  (unlimited, let Docker handle rotation)
```

---

## 2. Rule Config — `hooks/*.yaml`

Each YAML file in `config_dir` defines a rule set. The **filename** (without `.yaml`) is used as the routing key for `/webhook/{filename}`.

### Top-Level Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Rule set name, used in logs and responses |
| `auth` | object | No | Authentication settings (AND relationship) |
| `execution` | object | No | File-level execution policy |
| `filters` | array | No | File-level global filters (AND with rule-level) |
| `log` | object | No | Rule-level independent log file (dual-write with global) |
| `rules` | array | Yes | List of rules (at least one required) |

---

### 2.1 `auth` — Authentication

All configured checks use **AND** relationship — every check must pass.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `auth.token.source` | string | If token set | `"header"` or `"query"` — where to read the token |
| `auth.token.key` | string | If token set | Header name or query parameter name |
| `auth.token.value` | string | If token set | Expected token value |
| `auth.hmac.header` | string | If hmac set | Signature header name, e.g. `X-Hub-Signature-256` |
| `auth.hmac.secret` | string | If hmac set | HMAC secret key (from webhook provider settings) |
| `auth.hmac.algorithm` | string | No | `"sha256"` (default) \| `"sha1"` \| `"sha512"` |
| `auth.hmac.prefix` | string | No | Signature prefix, e.g. `"sha256="` (auto-derived from algorithm if empty) |
| `auth.ip_whitelist` | array | No | List of allowed IPs (supports CIDR) |

#### Example

```yaml
auth:
  token:
    source: "header"
    key: "X-Webhook-Token"
    value: "secret123"
  hmac:
    header: "X-Hub-Signature-256"
    secret: "your-webhook-secret"
    algorithm: "sha256"
  ip_whitelist:
    - "192.168.1.100"
    - "10.0.0.0/24"
```

If only `token` is set, only token validation is required. If only `hmac` is set, only HMAC signature validation is required. If only `ip_whitelist` is set, only IP validation is required. If multiple are set, all must pass (AND relationship).

#### HMAC Signature Verification

HMAC verification computes a signature over the raw request body using the configured secret and algorithm, then compares it against the signature provided in the request header. This is the standard verification method used by GitHub, GitLab, Bitbucket, and other platforms.

| Platform | Header | Algorithm | Prefix |
|----------|--------|-----------|--------|
| GitHub | `X-Hub-Signature-256` | `sha256` | `sha256=` |
| GitLab | `X-Gitlab-Token` | — (plain token, use `auth.token` instead) | — |
| Bitbucket | `X-Hub-Signature` | `sha256` | `sha256=` |

GitHub example:

```yaml
auth:
  hmac:
    header: "X-Hub-Signature-256"
    secret: "your-github-webhook-secret"
    # algorithm defaults to "sha256", prefix auto-derived as "sha256="
```

Custom prefix example (for platforms with non-standard formats):

```yaml
auth:
  hmac:
    header: "X-Signature"
    secret: "my-secret"
    algorithm: "sha512"
    prefix: "sha512="
```

---

### 2.2 `execution` — Execution Policy

Can be set at file-level (applies to all rules) or rule-level (overrides file-level).

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `policy` | string | Yes | `"block"` \| `"always"` \| `"cooldown"` |
| `cooldown_seconds` | int | If cooldown | Cooldown interval in seconds (must be > 0) |

#### Policy Types

| Policy | Behavior | HTTP Response | Use Case |
|--------|----------|---------------|----------|
| `block` | Reject if previous execution is still running | 409 | Deploy, build, long tasks |
| `always` | Always spawn a new execution | 200 | Stateless notifications, logging |
| `cooldown` | Reject if within cooldown window | 429 | Rate-limited scenarios |

#### Inheritance

```yaml
# File-level default
execution:
  policy: "block"

rules:
  - name: "deploy"
    # Inherits file-level: block
    ...

  - name: "notify"
    execution:
      policy: "always"    # Overrides: always execute
    ...
```

Priority: **Rule-level > File-level > Default (block)**

---

### 2.3 `filters` — File-Level Filters

File-level filters act as **global constraints** applied to ALL rules in the config. They use **AND** logic with rule-level filters: both must match for a rule to execute.

If file-level filters fail, **all rules are skipped** (short-circuit) — no individual rule is evaluated.

A rule with no filters (at either level) acts as a **catch-all** — it matches every request that reaches it. In a first-match-wins chain, place catch-all rules last as fallbacks.

```yaml
# File-level: common constraint for all rules
filters:
  - type: "header"
    key: "X-GitHub-Event"
    operator: "eq"
    value: "push"

rules:
  - name: "deploy-main"
    filters:                         # AND with file-level
      - type: "body"
        key: "ref"
        operator: "eq"
        value: "refs/heads/main"
    actions: [...]

  - name: "notify-all"
    # No rule-level filters — matches whenever file-level filters pass
    actions: [...]
```

Filter field definitions, types, and operators are the same as rule-level filters (see section 2.4).

---

### 2.3.5 `log` — Rule-Level Log

Each rule config file can specify an independent log file. Logs are **dual-written** to both the global log and the rule-specific log (similar to nginx `access_log`).

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `path` | string | Yes | Full log file path (e.g. `./logs/deploy.log`) |

File naming follows the global `log.mode`:

| Mode | log.path | Generated File |
|------|----------|----------------|
| `daily` | `./logs/deploy.log` | `./logs/deploy-2026-06-11.log` |
| `single` | `./logs/deploy.log` | `./logs/deploy.log` |

```yaml
log:
  path: "./logs/deploy.log"
```

Rule-level loggers inherit `retention_days` and `max_size_mb` from global settings.

---

### 2.4 `rules` — Rule List

Each rule has a name, filters, and actions.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Rule name, used in logs and responses |
| `execution` | object | No | Rule-level execution policy (overrides file-level) |
| `filters` | array | No | Matching conditions (AND relationship; empty = catch-all) |
| `actions` | array | Yes | Commands/scripts to execute (at least one) |

---

### 2.5 `filters` — Rule-Level Matching Conditions

Multiple filters within a rule use **AND** relationship — all must match. Combined with file-level filters (if any) via AND.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | Yes | `"header"` \| `"query"` \| `"body"` |
| `key` | string | Yes | Field name to check (body supports JSON path) |
| `operator` | string | Yes | `"eq"` \| `"ne"` \| `"contains"` \| `"regex"` |
| `value` | string | Yes | Expected value to match against |

#### Filter Types

| Type | Source | Example |
|------|--------|---------|
| `header` | HTTP request headers | `X-GitHub-Event` |
| `query` | URL query parameters | `?event=push` |
| `body` | JSON request body | `ref`, `commits[0].message` |

#### Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `eq` | Exact match | `ref` eq `refs/heads/main` |
| `ne` | Not equal | `status` ne `closed` |
| `contains` | Substring match | `message` contains `deploy` |
| `regex` | Regular expression | `ref` regex `refs/tags/v.*` |

#### JSON Path (body type)

Supports dot notation and array indexing:

```yaml
- type: "body"
  key: "ref"                    # top-level field
- type: "body"
  key: "commits[0].message"     # nested with array index
- type: "body"
  key: "repository.owner.name"  # deeply nested
```

---

### 2.6 `actions` — Actions to Execute

Actions execute **sequentially** in the order defined.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `type` | string | Yes | — | `"command"`, `"script"`, or `"webhook"` |
| `cmd` | string | If command | — | Shell command to execute (supports template variables) |
| `path` | string | If script | — | Script file path (supports template variables) |
| `args` | array | No | `[]` | Arguments for script (supports template variables) |
| `pass_args` | array | No | `[]` | Extract from request and append as arguments |
| `timeout` | int | No | `0` (no limit) | Timeout in seconds |
| `isolate` | bool | No | `false` | Run in isolated subprocess |
| `continue_on_error` | bool | No | `false` | Continue to next action if this one fails |

#### Action Types

| Type | Description | Required Fields |
|------|-------------|------------------|
| `command` | Inline shell command | `cmd` |
| `script` | External script file | `path` |
| `webhook` | HTTP request to external URL | `url` |

#### Platform Behavior

| Platform | Shell | Flag |
|----------|-------|------|
| Linux/macOS | `sh` | `-c` |
| Windows | `cmd` | `/c` |

#### Environment Variable

All executed commands receive `HOOKRUN=1` in their environment.

#### Webhook Action

The `webhook` type sends an HTTP request to an external URL — useful for notifying Slack, DingTalk, Feishu, or cascading HookRun instances.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `url` | string | Yes | — | Target URL (supports template variables) |
| `method` | string | No | `POST` | HTTP method: `POST`, `PUT`, `PATCH`, `GET` |
| `headers` | map | No | `{}` | Custom headers (supports template variables) |
| `forward_headers` | array | No | `[]` | Whitelist of original request headers to forward |
| `body` | string | No | `""` | Request body template (supports `{{.raw_body}}`) |
| `timeout` | int | No | `30` | Timeout in seconds |

**Auto Headers** — Every webhook request includes:

| Header | Value |
|--------|-------|
| `X-HookRun-Source` | `HookRun/v1.1.1` |
| `X-HookRun-Config` | Config file name (e.g. `deploy-app`) |
| `X-HookRun-Rule` | Rule name (e.g. `on-push`) |

**Header merge priority**: Auto headers → `forward_headers` → `headers` (custom overrides all).

**`{{.raw_body}}` template** — Injects the entire original request body as raw JSON. Only available in webhook `body` field (not in command/script actions to prevent shell injection).

> **Important**: The `body` field must be a YAML **string**. Wrap JSON in quotes (`'...'`) or use block scalar (`|`). Template variables like `{{.raw_body}}` must include the `{{ }}` delimiters — writing `.raw_body` alone will be treated as literal text.

```yaml
# ✓ Correct — body is a string with template syntax
body: '{"event":"push","payload":{{.raw_body}}}'

# ✓ Correct — block scalar
body: |
  {"text": "Deploy: {{.body.ref}}", "payload": {{.raw_body}}}

# ✗ Wrong — YAML mapping (causes parse error)
body:
  event: "push"
  payload: .raw_body
```

```yaml
actions:
  # Forward entire body to another service
  - type: "webhook"
    url: "https://another-hookrun.example.com/webhook/deploy"
    body: "{{.raw_body}}"

  # Wrap original body with extra fields
  - type: "webhook"
    url: "https://hooks.slack.com/services/xxx"
    body: |
      {"text": "Deploy: {{.body.ref}}", "payload": {{.raw_body}}}

  # Forward specific headers + custom auth
  - type: "webhook"
    url: "https://api.example.com/notify"
    method: "PUT"
    forward_headers:
      - "X-GitHub-Event"
      - "X-Request-Id"
    headers:
      Authorization: "Bearer {{.body.token}}"
    body: '{"event": "{{.header.x-event}}"}'
```

#### Template Variables

Commands, script paths, and script arguments support template variables that are resolved from the incoming request at runtime:

| Template | Source | Example |
|----------|--------|---------|
| `{{.body.<path>}}` | JSON request body | `{{.body.ref}}`, `{{.body.repository.owner.name}}` |
| `{{.header.<name>}}` | HTTP request header | `{{.header.X-GitHub-Event}}` |
| `{{.query.<name>}}` | URL query parameter | `{{.query.token}}` |

Body paths support dot notation and array indexing (same as filter body type).

```yaml
actions:
  - type: "command"
    cmd: "git checkout {{.body.ref}} && echo 'Event: {{.header.X-GitHub-Event}}'"
```

If a template variable cannot be resolved, it is replaced with an empty string and a warning is logged.

#### `pass_args` — Extract and Append Parameters

`pass_args` extracts values from the request and appends them as trailing arguments to the command or script. This is useful for passing dynamic data without embedding template variables in the command string.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source` | string | Yes | `"header"` \| `"query"` \| `"body"` |
| `key` | string | Yes | Field name or JSON path for body |

```yaml
actions:
  - type: "command"
    cmd: "echo 'Deploying:'"
    pass_args:
      - source: "body"
        key: "ref"
      - source: "header"
        key: "X-GitHub-Event"
```

When the webhook receives `{"ref": "refs/heads/main"}` with header `X-GitHub-Event: push`, the executed command becomes:

```
echo 'Deploying:' refs/heads/main push
```

#### Example

```yaml
actions:
  - type: "command"
    cmd: "cd /var/www/app && git pull"
    timeout: 60
  - type: "command"
    cmd: "npm install --production"
    timeout: 120
    continue_on_error: true
  - type: "script"
    path: "./scripts/deploy.sh"
    args: ["production", "v2.1"]
    timeout: 300
    isolate: true
```

---

## 3. Complete Example

```yaml
name: "github-auto-deploy"

auth:
  hmac:
    header: "X-Hub-Signature-256"
    secret: "your-github-webhook-secret"
  ip_whitelist:
    - "192.30.252.0/22"

execution:
  policy: "block"

# File-level filters: global constraints for ALL rules
filters:
  - type: "header"
    key: "X-GitHub-Event"
    operator: "eq"
    value: "push"

rules:
  - name: "push-to-main"
    filters:   # AND with file-level
      - type: "body"
        key: "ref"
        operator: "eq"
        value: "refs/heads/main"
    actions:
      - type: "command"
        cmd: "cd /var/www/app && git pull origin {{.body.ref}}"
        timeout: 30
      - type: "command"
        cmd: "cd /var/www/app && npm install --production && npm run build"
        timeout: 120
      - type: "script"
        path: "./scripts/restart.sh"
        args: ["{{.body.ref}}"]
        timeout: 60

  - name: "tag-release"
    execution:
      policy: "always"
    filters:
      - type: "body"
        key: "ref"
        operator: "regex"
        value: "refs/tags/v.*"
    actions:
      - type: "command"
        cmd: "echo 'Release tag:'"
        pass_args:
          - source: "body"
            key: "ref"
        timeout: 10

# Rule-level independent log (dual-write: global + this file)
log:
  path: "./logs/github-auto-deploy.log"
```

## 4. Secret References

All string fields in YAML configuration support `${env:}` and `${file:}` interpolation to avoid storing secrets in plain text.

### `${env:VAR_NAME}`

Reads the value from an environment variable.

```yaml
auth:
  token:
    source: "header"
    key: "X-Webhook-Token"
    value: "${env:WEBHOOK_TOKEN}"
  hmac:
    header: "X-Hub-Signature-256"
    secret: "${env:GITHUB_WEBHOOK_SECRET}"
```

If the environment variable is not set, HookRun will fail to start with a clear error message.

### `${file:/path/to/secret}`

Reads the value from a file. Trailing whitespace and newlines are automatically trimmed.

```yaml
auth:
  token:
    source: "header"
    key: "X-Webhook-Token"
    value: "${file:/run/secrets/webhook_token}"
```

This is compatible with Docker Secrets, Kubernetes Secrets (mounted as files), and systemd credentials.

### Notes

- Interpolation is applied to **all** string fields in both `config.yaml` and `hooks/*.yaml`
- Multiple references can be used in the same file
- If a referenced env var or file is missing, HookRun reports an error at load time (fail-safe)
