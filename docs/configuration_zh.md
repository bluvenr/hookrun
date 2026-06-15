# 配置参数说明

[English](configuration.md)

HookRun 使用两级 YAML 配置：

1. **全局配置** (`config.yaml`) — 服务器、日志、目录设置
2. **规则配置** (`hooks/*.yaml`) — 每个场景的认证、过滤和动作

---

## 1. 全局配置 — `config.yaml`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `server.port` | int | `9000` | HTTP 监听端口（1–65535） |
| `server.route` | string | `/webhook` | Webhook 基础路由路径 |
| `server.allow_all` | bool | `false` | 是否允许基础路由 `/webhook` 遍历所有配置文件 |
| `server.max_body_size_mb` | int | `10` | 请求体大小上限 MB。`0` = 不限制 |
| `log.mode` | string | `daily` | 日志模式：`"daily"`（按天轮转）或 `"single"`（单文件） |
| `log.path` | string | `./logs` | 日志目录（daily）或基础路径（single） |
| `log.retention_days` | int | `30` | 日志保留天数（仅 daily 模式） |
| `log.max_size_mb` | int | `0`（不限） | 日志文件大小上限 MB，超过后轮转（仅 single 模式） |
| `config_dir` | string | `./hooks` | 规则 YAML 文件所在目录 |

### 示例

```yaml
server:
  port: 9000
  route: "/webhook"
  allow_all: false

log:
  mode: "daily"                # "daily"（默认）| "single"
  path: "./logs"
  retention_days: 30           # 仅 daily 模式
  # max_size_mb: 0             # 仅 single 模式，0 = 不限（默认）

config_dir: "./hooks"
```

### `server.allow_all`

控制基础路由 `/webhook` 是否遍历所有 YAML 配置文件。

- `true` — 请求 `/webhook` 时遍历所有配置文件，匹配第一个规则后停止
- `false`（默认）— 请求 `/webhook` 返回 400，必须使用 `/webhook/{filename}` 定向路由

### `server.max_body_size_mb`

限制传入请求体的最大大小，防止 DoS 攻击。

- `10`（默认）— 超过 10 MB 的请求体返回 HTTP 413
- `0` — 不限制（谨慎使用）
- 任意正整数 — 自定义限制（单位：MB）

### `log.mode`

控制日志文件的生成方式。

| 模式 | 行为 | 文件命名 |
|------|------|----------|
| `daily`（默认） | 每天一个文件，自动清理过期日志 | `hookrun-2026-06-11.log` |
| `single` | 固定一个文件，可按大小轮转 | `hookrun.log` |

**Daily 模式**适合长期运行的服务，配合 `retention_days` 自动清理。

**Single 模式**适合容器环境（Docker/Kubernetes）或使用外部工具（logrotate）管理轮转。`max_size_mb: 0` 表示不限大小。

```yaml
# 容器友好配置：单文件，不限制
log:
  mode: "single"
  path: "./logs"
  # max_size_mb: 0  （不限，交给 Docker 处理轮转）
```

---

## 2. 规则配置 — `hooks/*.yaml`

`config_dir` 目录下的每个 YAML 文件定义一个规则集。**文件名**（不含 `.yaml` 扩展名）用作 `/webhook/{filename}` 的路由标识。

### 顶层字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | 规则集名称，用于日志和响应 |
| `auth` | object | 否 | 认证设置（AND 关系） |
| `execution` | object | 否 | 文件级执行策略 |
| `filters` | array | 否 | 文件级全局过滤条件（与规则级 AND 组合） |
| `log` | object | 否 | 规则级独立日志文件（与全局双写） |
| `rules` | array | 是 | 规则列表（至少一个） |

---

### 2.1 `auth` — 认证

所有已配置的验证项为 **AND** 关系 — 每一项都必须通过。

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `auth.token.source` | string | 设置 token 时必填 | `"header"` 或 `"query"` — token 来源 |
| `auth.token.key` | string | 设置 token 时必填 | Header 名称或 Query 参数名 |
| `auth.token.value` | string | 设置 token 时必填 | 期望的 token 值 |
| `auth.hmac.header` | string | 设置 hmac 时必填 | 签名 Header 名称，如 `X-Hub-Signature-256` |
| `auth.hmac.secret` | string | 设置 hmac 时必填 | HMAC 密钥（来自 Webhook 提供商设置） |
| `auth.hmac.algorithm` | string | 否 | `"sha256"`（默认）\| `"sha1"` \| `"sha512"` |
| `auth.hmac.prefix` | string | 否 | 签名前缀，如 `"sha256="`（为空时根据 algorithm 自动推导） |
| `auth.ip_whitelist` | array | 否 | 允许的 IP 列表（支持 CIDR） |

#### 示例

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

如果仅设置了 `token`，只需 token 验证。如果仅设置了 `hmac`，只需 HMAC 签名验证。如果仅设置了 `ip_whitelist`，只需 IP 验证。如果设置了多个，所有已设置项都必须通过（AND 关系）。

#### HMAC 签名验证

HMAC 验证使用配置的密钥和算法对原始请求体计算签名，然后与请求 Header 中的签名进行比对。这是 GitHub、GitLab、Bitbucket 等平台的标准验证方式。

| 平台 | Header | 算法 | 前缀 |
|------|--------|------|------|
| GitHub | `X-Hub-Signature-256` | `sha256` | `sha256=` |
| GitLab | `X-Gitlab-Token` | —（明文 Token，请使用 `auth.token`） | — |
| Bitbucket | `X-Hub-Signature` | `sha256` | `sha256=` |

GitHub 配置示例：

```yaml
auth:
  hmac:
    header: "X-Hub-Signature-256"
    secret: "your-github-webhook-secret"
    # algorithm 默认为 "sha256"，prefix 自动推导为 "sha256="
```

自定义前缀示例（适配非标准格式的平台）：

```yaml
auth:
  hmac:
    header: "X-Signature"
    secret: "my-secret"
    algorithm: "sha512"
    prefix: "sha512="
```

---

### 2.2 `execution` — 执行策略

可在文件级（应用于所有规则）或规则级（覆盖文件级）设置。

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `policy` | string | 是 | `"block"` \| `"always"` \| `"cooldown"` |
| `cooldown_seconds` | int | cooldown 时必填 | 冷却间隔秒数（必须 > 0） |

#### 策略类型

| 策略 | 行为 | HTTP 响应 | 适用场景 |
|------|------|-----------|----------|
| `block` | 上次执行未完成则拒绝 | 409 | 部署、构建、耗时任务 |
| `always` | 每次都新开执行 | 200 | 无状态通知、日志记录 |
| `cooldown` | 冷却窗口内拒绝 | 429 | 限频场景 |

#### 继承关系

```yaml
# 文件级默认
execution:
  policy: "block"

rules:
  - name: "deploy"
    # 继承文件级: block
    ...

  - name: "notify"
    execution:
      policy: "always"    # 覆盖: 始终执行
    ...
```

优先级：**规则级 > 文件级 > 默认值 (block)**

---

### 2.3 `filters` — 文件级过滤条件

文件级 filters 作为**全局约束**，应用于该文件下的所有规则。与规则级 filters 为 **AND** 关系：两者必须同时匹配才能执行规则。

如果文件级 filters 不匹配，**所有规则都会被跳过**（短路）— 不会逐条评估。

无任何 filter 的规则（文件级和规则级都为空）为 **catch-all** — 匹配所有到达它的请求。在 first-match-wins 的链式匹配中，将 catch-all 规则放在末尾作为兆底。

```yaml
# 文件级：所有规则的公共约束
filters:
  - type: "header"
    key: "X-GitHub-Event"
    operator: "eq"
    value: "push"

rules:
  - name: "deploy-main"
    filters:                         # 与文件级 AND 组合
      - type: "body"
        key: "ref"
        operator: "eq"
        value: "refs/heads/main"
    actions: [...]

  - name: "notify-all"
    # 无规则级 filters — 只要文件级 filters 通过就执行
    actions: [...]
```

过滤字段的定义、类型和操作符与规则级 filters 相同（见 2.4 节）。

---

### 2.3.5 `log` — 规则级日志

每个规则配置文件可以指定独立的日志文件。日志会**双写**到全局日志和规则专属日志（类似 nginx 的 `access_log`）。

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `path` | string | 是 | 完整日志文件路径（如 `./logs/deploy.log`） |

文件命名跟随全局 `log.mode`：

| 模式 | log.path | 生成文件 |
|------|----------|----------|
| `daily` | `./logs/deploy.log` | `./logs/deploy-2026-06-11.log` |
| `single` | `./logs/deploy.log` | `./logs/deploy.log` |

```yaml
log:
  path: "./logs/deploy.log"
```

规则级日志继承全局的 `retention_days` 和 `max_size_mb` 设置。

---

### 2.4 `rules` — 规则列表

每条规则包含名称、过滤条件和动作。

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | 规则名称，用于日志和响应 |
| `execution` | object | 否 | 规则级执行策略（覆盖文件级） |
| `filters` | array | 否 | 匹配条件（AND 关系；为空则匹配所有请求） |
| `actions` | array | 是 | 要执行的命令/脚本（至少一个） |

---

### 2.5 `filters` — 规则级过滤条件

同一规则内多个 filter 为 **AND** 关系 — 必须全部匹配。与文件级 filters（如有）同样为 AND 组合。

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `type` | string | 是 | `"header"` \| `"query"` \| `"body"` |
| `key` | string | 是 | 要检查的字段名（body 支持 JSON path） |
| `operator` | string | 是 | `"eq"` \| `"ne"` \| `"contains"` \| `"regex"` |
| `value` | string | 是 | 期望匹配的值 |

#### 过滤类型

| 类型 | 来源 | 示例 |
|------|------|------|
| `header` | HTTP 请求头 | `X-GitHub-Event` |
| `query` | URL 查询参数 | `?event=push` |
| `body` | JSON 请求体 | `ref`、`commits[0].message` |

#### 操作符

| 操作符 | 说明 | 示例 |
|--------|------|------|
| `eq` | 精确匹配 | `ref` eq `refs/heads/main` |
| `ne` | 不等于 | `status` ne `closed` |
| `contains` | 子串包含 | `message` contains `deploy` |
| `regex` | 正则表达式 | `ref` regex `refs/tags/v.*` |

#### JSON Path（body 类型）

支持点号和数组索引：

```yaml
- type: "body"
  key: "ref"                    # 顶层字段
- type: "body"
  key: "commits[0].message"     # 嵌套 + 数组索引
- type: "body"
  key: "repository.owner.name"  # 深层嵌套
```

---

### 2.6 `actions` — 执行动作

动作按定义顺序**依次执行**。

| 字段 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `type` | string | 是 | — | `"command"`、`"script"` 或 `"webhook"` |
| `cmd` | string | command 时必填 | — | Shell 命令（支持模板变量） |
| `path` | string | script 时必填 | — | 脚本文件路径（支持模板变量） |
| `args` | array | 否 | `[]` | 脚本参数（支持模板变量） |
| `pass_args` | array | 否 | `[]` | 从请求中提取参数并追加为命令参数 |
| `timeout` | int | 否 | `0`（无限制） | 超时秒数 |
| `isolate` | bool | 否 | `false` | 是否在隔离子进程中运行 |
| `continue_on_error` | bool | 否 | `false` | 失败后是否继续执行下一个动作 |

#### 动作类型

| 类型 | 说明 | 必填字段 |
|------|------|----------|
| `command` | 内联 Shell 命令 | `cmd` |
| `script` | 外部脚本文件 | `path` |
| `webhook` | 向外部 URL 发送 HTTP 请求 | `url` |

#### 平台行为

| 平台 | Shell | 参数 |
|------|-------|------|
| Linux/macOS | `sh` | `-c` |
| Windows | `cmd` | `/c` |

#### 环境变量

所有执行的命令都会收到环境变量 `HOOKRUN=1`。

#### Webhook 动作

`webhook` 类型向外部 URL 发送 HTTP 请求，适用于通知 Slack、钉钉、飞书、或级联 HookRun 实例。

| 字段 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `url` | string | 是 | — | 目标 URL（支持模板变量） |
| `method` | string | 否 | `POST` | HTTP 方法：`POST`、`PUT`、`PATCH`、`GET` |
| `headers` | map | 否 | `{}` | 自定义 headers（支持模板变量） |
| `forward_headers` | array | 否 | `[]` | 白名单转发原始请求 headers |
| `body` | string | 否 | `""` | 请求体模板（支持 `{{.raw_body}}`） |
| `timeout` | int | 否 | `30` | 超时时间（秒） |

**自动 Headers** — 每个 webhook 请求自动携带：

| Header | 值 |
|--------|----|
| `X-HookRun-Source` | `HookRun/v1.1.1` |
| `X-HookRun-Config` | 配置文件名（如 `deploy-app`） |
| `X-HookRun-Rule` | 规则名（如 `on-push`） |

**Header 合并优先级**：自动 Headers → `forward_headers` → `headers`（自定义最高优先）。

**`{{.raw_body}}` 模板** — 将原始请求体以 JSON 原文注入。仅限 webhook 的 `body` 字段使用（command/script 不支持，防止 shell 注入）。

> **注意**：`body` 字段必须是 YAML **字符串**。请用引号（`'...'`）包裹 JSON 或使用块标量（`|`）。模板变量如 `{{.raw_body}}` 必须包含 `{{ }}` 分隔符，单独写 `.raw_body` 会被当作普通文本。

```yaml
# ✓ 正确 — body 是字符串，使用模板语法
body: '{"event":"push","payload":{{.raw_body}}}'

# ✓ 正确 — 块标量写法
body: |
  {"text": "Deploy: {{.body.ref}}", "payload": {{.raw_body}}}

# ✗ 错误 — YAML 映射（会导致解析错误）
body:
  event: "push"
  payload: .raw_body
```

```yaml
actions:
  # 完整转发原始 body
  - type: "webhook"
    url: "https://another-hookrun.example.com/webhook/deploy"
    body: "{{.raw_body}}"

  # 包裹原始 body + 自定义字段
  - type: "webhook"
    url: "https://hooks.slack.com/services/xxx"
    body: |
      {"text": "Deploy: {{.body.ref}}", "payload": {{.raw_body}}}

  # 转发指定 headers + 自定义认证
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

#### 模板变量

命令、脚本路径和脚本参数支持模板变量，在运行时从请求中解析实际值：

| 模板 | 来源 | 示例 |
|------|------|------|
| `{{.body.<path>}}` | JSON 请求体 | `{{.body.ref}}`、`{{.body.repository.owner.name}}` |
| `{{.header.<name>}}` | HTTP 请求头 | `{{.header.X-GitHub-Event}}` |
| `{{.query.<name>}}` | URL 查询参数 | `{{.query.token}}` |

body 路径支持点号和数组索引（与 filter body 类型相同）。

```yaml
actions:
  - type: "command"
    cmd: "git checkout {{.body.ref}} && echo 'Event: {{.header.X-GitHub-Event}}'"
```

如果模板变量无法解析，将被替换为空字符串并记录警告日志。

#### `pass_args` — 提取并追加参数

`pass_args` 从请求中提取值并追加为命令或脚本的尾部参数。适合传递动态数据而无需在命令字符串中嵌入模板变量。

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `source` | string | 是 | `"header"` \| `"query"` \| `"body"` |
| `key` | string | 是 | 字段名或 body 的 JSON path |

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

当 webhook 收到 `{"ref": "refs/heads/main"}` 且 header 为 `X-GitHub-Event: push` 时，实际执行的命令为：

```
echo 'Deploying:' refs/heads/main push
```

#### 示例

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

## 3. 完整示例

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

# 文件级过滤条件：所有规则的全局约束
filters:
  - type: "header"
    key: "X-GitHub-Event"
    operator: "eq"
    value: "push"

rules:
  - name: "push-to-main"
    filters:   # 与文件级 AND 组合
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

# 规则级独立日志（双写：全局 + 此文件）
log:
  path: "./logs/github-auto-deploy.log"
```

## 4. 密钥引用

YAML 配置中的所有字符串字段均支持 `${env:}` 和 `${file:}` 插值，避免明文存储密钥。

### `${env:VAR_NAME}`

从环境变量读取值。

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

如果环境变量未设置，HookRun 启动时会报错并终止。

### `${file:/path/to/secret}`

从文件读取值，自动去除末尾的空白和换行符。

```yaml
auth:
  token:
    source: "header"
    key: "X-Webhook-Token"
    value: "${file:/run/secrets/webhook_token}"
```

兼容 Docker Secrets、Kubernetes Secrets（挂载为文件）和 systemd credentials。

### 说明

- 插值适用于 `config.yaml` 和 `hooks/*.yaml` 中的**所有**字符串字段
- 同一文件中可使用多个引用
- 如果引用的环境变量或文件不存在，HookRun 在加载时报错（安全失败）
