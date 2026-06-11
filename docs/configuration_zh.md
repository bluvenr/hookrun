# 配置参数说明

HookRun 使用两级 YAML 配置：

1. **全局配置** (`config.yaml`) — 服务器、日志、目录设置
2. **规则配置** (`hooks/*.yaml`) — 每个场景的认证、过滤和动作

---

## 1. 全局配置 — `config.yaml`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `server.port` | int | `9000` | HTTP 监听端口（1–65535） |
| `server.route` | string | `/webhook` | Webhook 基础路由路径 |
| `server.allow_all` | bool | `true` | 是否允许基础路由 `/webhook` 遍历所有配置文件 |
| `server.first_match_only` | bool | `true` | 匹配第一个规则后停止（仅当 `allow_all: true` 时生效） |
| `log.path` | string | `./logs` | 日志文件目录 |
| `log.retention_days` | int | `30` | 日志保留天数 |
| `config_dir` | string | `./hooks` | 规则 YAML 文件所在目录 |

### 示例

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

控制基础路由 `/webhook` 是否遍历所有 YAML 配置文件。

- `true`（默认）— 请求 `/webhook` 时遍历所有配置文件
- `false` — 请求 `/webhook` 返回 400，必须使用 `/webhook/{filename}` 定向路由

### `server.first_match_only`

控制基础路由的匹配行为（仅当 `allow_all: true` 时生效）。

- `true`（默认）— 跨所有配置匹配第一个命中的规则后停止
- `false` — 跨所有配置执行所有命中的规则

---

## 2. 规则配置 — `hooks/*.yaml`

`config_dir` 目录下的每个 YAML 文件定义一个规则集。**文件名**（不含 `.yaml` 扩展名）用作 `/webhook/{filename}` 的路由标识。

### 顶层字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | 规则集名称，用于日志和响应 |
| `auth` | object | 否 | 认证设置（AND 关系） |
| `execution` | object | 否 | 文件级执行策略 |
| `rules` | array | 是 | 规则列表（至少一个） |

---

### 2.1 `auth` — 认证

所有已配置的验证项为 **AND** 关系 — 每一项都必须通过。

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `auth.token.source` | string | 设置 token 时必填 | `"header"` 或 `"query"` — token 来源 |
| `auth.token.key` | string | 设置 token 时必填 | Header 名称或 Query 参数名 |
| `auth.token.value` | string | 设置 token 时必填 | 期望的 token 值 |
| `auth.ip_whitelist` | array | 否 | 允许的 IP 列表（支持 CIDR） |

#### 示例

```yaml
auth:
  token:
    source: "header"
    key: "X-Webhook-Token"
    value: "secret123"
  ip_whitelist:
    - "192.168.1.100"
    - "10.0.0.0/24"
```

如果仅设置了 `token`，只需 token 验证。如果仅设置了 `ip_whitelist`，只需 IP 验证。如果两者都设置了，两者都必须通过。

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

### 2.3 `rules` — 规则列表

每条规则包含名称、过滤条件和动作。

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | 规则名称，用于日志和响应 |
| `execution` | object | 否 | 规则级执行策略（覆盖文件级） |
| `filters` | array | 是 | 匹配条件（AND 关系，至少一个） |
| `actions` | array | 是 | 要执行的命令/脚本（至少一个） |

---

### 2.4 `filters` — 过滤条件

同一规则内多个 filter 为 **AND** 关系 — 必须全部匹配。

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

### 2.5 `actions` — 执行动作

动作按定义顺序**依次执行**。

| 字段 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `type` | string | 是 | — | `"command"` 或 `"script"` |
| `cmd` | string | command 时必填 | — | Shell 命令 |
| `path` | string | script 时必填 | — | 脚本文件路径 |
| `args` | array | 否 | `[]` | 脚本参数 |
| `timeout` | int | 否 | `0`（无限制） | 超时秒数 |
| `isolate` | bool | 否 | `false` | 是否在隔离子进程中运行 |
| `continue_on_error` | bool | 否 | `false` | 失败后是否继续执行下一个动作 |

#### 动作类型

| 类型 | 说明 | 必填字段 |
|------|------|----------|
| `command` | 内联 Shell 命令 | `cmd` |
| `script` | 外部脚本文件 | `path` |

#### 平台行为

| 平台 | Shell | 参数 |
|------|-------|------|
| Linux/macOS | `sh` | `-c` |
| Windows | `cmd` | `/c` |

#### 环境变量

所有执行的命令都会收到环境变量 `HOOKRUN=1`。

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
  token:
    source: "header"
    key: "X-Hub-Signature-256"
    value: "sha256=abc123"
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
