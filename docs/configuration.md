# Configuration Reference

HookRun uses two levels of YAML configuration:

1. **Global Config** (`config.yaml`) — Server, logging, and directory settings
2. **Rule Config** (`hooks/*.yaml`) — Authentication, filters, and actions per scenario

---

## 1. Global Config — `config.yaml`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `server.port` | int | `9000` | HTTP listen port (1–65535) |
| `server.route` | string | `/webhook` | Base webhook endpoint path |
| `server.allow_all` | bool | `true` | Allow base route (`/webhook`) to iterate all config files |
| `server.first_match_only` | bool | `true` | Stop at first matching rule (only effective when `allow_all: true`) |
| `log.path` | string | `./logs` | Log file directory |
| `log.retention_days` | int | `30` | Number of days to retain log files |
| `config_dir` | string | `./hooks` | Directory containing rule YAML files |

### Example

```yaml
server:
  port: 9000
  route: "/webhook"
  allow_all: true
  first_match_only: true

log:
  path: "./logs"
  retention_days: 30

config_dir: "./hooks"
```

### `server.allow_all`

Controls whether the base route `/webhook` iterates through all YAML config files.

- `true` (default) — Requests to `/webhook` iterate all config files
- `false` — Requests to `/webhook` return 400; clients must use `/webhook/{filename}`

### `server.first_match_only`

Controls matching behavior for the base route (only effective when `allow_all: true`).

- `true` (default) — Execute the first matching rule across all configs, then stop
- `false` — Execute ALL matching rules across all configs

---

## 2. Rule Config — `hooks/*.yaml`

Each YAML file in `config_dir` defines a rule set. The **filename** (without `.yaml`) is used as the routing key for `/webhook/{filename}`.

### Top-Level Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Rule set name, used in logs and responses |
| `auth` | object | No | Authentication settings (AND relationship) |
| `execution` | object | No | File-level execution policy |
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

### 2.3 `rules` — Rule List

Each rule has a name, filters, and actions.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Rule name, used in logs and responses |
| `execution` | object | No | Rule-level execution policy (overrides file-level) |
| `filters` | array | Yes | Matching conditions (AND relationship, at least one) |
| `actions` | array | Yes | Commands/scripts to execute (at least one) |

---

### 2.4 `filters` — Matching Conditions

Multiple filters within a rule use **AND** relationship — all must match.

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

### 2.5 `actions` — Actions to Execute

Actions execute **sequentially** in the order defined.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `type` | string | Yes | — | `"command"` or `"script"` |
| `cmd` | string | If command | — | Shell command to execute |
| `path` | string | If script | — | Script file path |
| `args` | array | No | `[]` | Arguments for script |
| `timeout` | int | No | `0` (no limit) | Timeout in seconds |
| `isolate` | bool | No | `false` | Run in isolated subprocess |
| `continue_on_error` | bool | No | `false` | Continue to next action if this one fails |

#### Action Types

| Type | Description | Required Fields |
|------|-------------|-----------------|
| `command` | Inline shell command | `cmd` |
| `script` | External script file | `path` |

#### Platform Behavior

| Platform | Shell | Flag |
|----------|-------|------|
| Linux/macOS | `sh` | `-c` |
| Windows | `cmd` | `/c` |

#### Environment Variable

All executed commands receive `HOOKRUN=1` in their environment.

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

rules:
  - name: "push-to-main"
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
        cmd: "cd /var/www/app && git pull origin main"
        timeout: 30
      - type: "command"
        cmd: "cd /var/www/app && npm install --production && npm run build"
        timeout: 120
      - type: "script"
        path: "./scripts/restart.sh"
        timeout: 60

  - name: "tag-release"
    execution:
      policy: "always"
    filters:
      - type: "header"
        key: "X-GitHub-Event"
        operator: "eq"
        value: "push"
      - type: "body"
        key: "ref"
        operator: "regex"
        value: "refs/tags/v.*"
    actions:
      - type: "command"
        cmd: "echo 'Release tag detected'"
        timeout: 10
```
