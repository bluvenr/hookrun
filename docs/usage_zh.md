# 使用指南

[English](usage.md)

本指南介绍 HookRun 的日常使用：CLI 命令、路由行为、响应格式和常见场景。

---

## 1. CLI 命令

所有命令支持全局 `-c` 参数指定配置文件路径：

```bash
hookrun -c /path/to/config.yaml <command>
```

默认配置路径：当前目录下的 `config.yaml`。

---

### `init` — 初始化配置

```bash
# 使用通用模板初始化（默认）
hookrun init

# 使用 GitHub webhook 模板初始化
hookrun init --template github

# 使用 GitLab webhook 模板初始化
hookrun init --template gitlab

# 覆盖现有文件无需确认
hookrun init --force
```

在当前目录创建 `config.yaml` 和 `hooks/example.yaml`。

可用模板：

| 模板 | 说明 |
|------|------|
| `generic` | 通用 webhook 模板，使用 Token 认证 |
| `github` | GitHub webhook 自动部署，使用 HMAC 认证 |
| `gitlab` | GitLab webhook 自动部署，使用 Token 认证 |

---

### `relay` — 查看 Relay 状态

```bash
# 查看当前实例的 relay 状态
hookrun relay status

# 列出已注册的下游目标（需 Bearer Token 认证）
hookrun relay targets

# 指定注册中心 Token（默认使用配置中的 relay_registry_token）
hookrun relay targets --token your-registry-token
```

HookRun 实例可以是以下四种 relay 角色之一：

| 角色 | 说明 |
|------|------|
| `upstream` | 接受下游注册（需配置 `relay_registry_token`） |
| `downstream` | 向上游注册（需配置 `relay_client`） |
| `upstream+downstream` | 同时作为上游和下游 |
| `none` | 无 relay 配置 |

**状态输出示例：**

```
Relay Status
============
Role: upstream + downstream

Upstream (Registry):
  Enabled:     yes
  Targets:     3 registered
  Max entries: 100
  Max TTL:     300s

Downstream (Client):
  Enabled:        yes
  Upstream:       http://main:9000
  Registered as:  http://10.0.0.2:9000/webhook
  Tags:           prod, web
  TTL:            120s
  Connected:      yes
  Last heartbeat: 2026-06-24 14:00:00 (15s ago)
  Failures:       0
```

**目标列表输出示例：**

```
Registered Targets (3)
+------------------------------+------------+------+---------------------+
| URL                          | Tags       | TTL  | Last Seen           |
+------------------------------+------------+------+---------------------+
| http://10.0.0.2:9000/webhook | prod, web  | 120s | 2026-06-24 14:00:00 |
| http://10.0.0.3:9000/webhook | prod, api  | 120s | 2026-06-24 13:59:50 |
| http://10.0.0.4:9000/webhook | staging    | 60s  | 2026-06-24 14:00:05 |
+------------------------------+------------+------+---------------------+
```

---

### `start` — 启动服务

```bash
# 守护模式（默认，后台运行）
hookrun start

# 前台模式（调试/终端使用）
hookrun start -f

# 指定配置文件路径
hookrun start -c /etc/hookrun/config.yaml
```

- 守护模式会在 `~/.hookrun/` 创建 PID 文件
- 前台模式直接将日志输出到终端

---

### `stop` — 停止服务

```bash
hookrun stop
```

- **Linux/macOS**：向进程发送 SIGTERM 信号
- **Windows**：使用信号文件 IPC 机制（每 2 秒轮询一次）

---

### `restart` — 重启服务

```bash
hookrun restart
```

等同于 `stop` + `start`。等待进程停止最多 15 秒后重新启动。

---

### `status` — 查看状态

```bash
hookrun status
```

输出示例：

```
Status:  running
PID:     12345
Port:    9000
Rules:   3 config(s)
Uptime:  2h30m15s
Started: 2026-06-11 10:00:00
```

服务停止时：

```
Status: stopped (no PID file)
```

---

### `reload` — 热重载配置

```bash
hookrun reload
```

- 重新加载所有 YAML 文件（全局 + 规则配置），无需重启
- **Linux/macOS**：使用 HTTP 重载 API (`POST /_reload`)
- **Windows**：使用信号文件 IPC

重载后，新规则对后续请求立即生效。

---

### `validate` — 校验配置

```bash
hookrun validate
```

输出示例：

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

建议在启动或重载前运行 `validate` 以提前发现配置错误。

---

### `version` — 查看版本

```bash
hookrun version
```

输出：

```
HookRun vx.y.z
Build time: 2026-06-11
Go version: go1.23.3
OS/Arch:    linux/amd64
```

---

### `help` — 查看帮助

```bash
# 显示所有可用命令
hookrun help

# 显示指定命令的帮助
hookrun help relay
hookrun help relay status

# 也可使用 --help 或 -h 参数
hookrun start --help
hookrun relay -h
```

所有命令均支持 `--help` / `-h` 参数，用于显示详细用法和示例。

---

## 2. Webhook 路由

HookRun 支持两种 URL 模式：

### 定向路由：`/webhook/{filename}`

直接路由到指定 YAML 配置文件（按文件名不含扩展名匹配）。

```
POST /webhook/my-app
→ 匹配: hooks/my-app.yaml
→ 执行: 该文件中第一个匹配的规则
```

- 配置文件不存在 → 404
- 没有规则匹配 → 200 "No matching rules"

### 基础路由：`/webhook`

行为受 `allow_all` 控制：

| `allow_all` | 行为 |
|-------------|------|
| `true` | 遍历所有配置，匹配第一个规则后停止 |
| `false`（默认） | 返回 400 错误（必须使用定向路由） |

### 路由匹配示例

```
# 定向：直接到 hooks/frontend.yaml
POST /webhook/frontend

# 基础路由：遍历所有 YAML 文件
POST /webhook
```

### 健康检查

```
GET /health
```

返回：

```json
{"status": "ok", "uptime": "2h30m15s", "rules": 3, "version": "x.y.z"}
```

当配置了 Relay 时，响应会包含额外的 `relay` 字段：

```json
{"status": "ok", "uptime": "2h30m15s", "rules": 3, "version": "x.y.z", "relay": {"role": "upstream+downstream", "upstream_targets": 3, "downstream_connected": true}}
```

### Relay API

#### `GET /api/relay/status` — 综合 Relay 状态（始终可用，无需认证）

返回当前实例的 relay 角色和状态，无论是否配置了 relay。

```bash
curl http://hookrun:9000/api/relay/status
```

响应（上游 + 下游）：

```json
{
  "role": "upstream+downstream",
  "upstream": {
    "enabled": true,
    "targets_count": 3,
    "max_entries": 100,
    "max_ttl": 300
  },
  "downstream": {
    "enabled": true,
    "upstream_url": "http://main:9000",
    "registered_url": "http://10.0.0.2:9000/webhook",
    "tags": ["prod", "web"],
    "ttl": 120,
    "connected": true,
    "last_heartbeat": "2026-06-24T14:00:00Z",
    "fail_count": 0
  }
}
```

响应（无 Relay）：

```json
{
  "role": "none",
  "upstream": {"enabled": false},
  "downstream": {"enabled": false}
}
```

#### 注册池 API（需配置 `relay_registry_token`）

仅在 `server.relay_registry_token` 已配置时可用（详见[配置参考](configuration_zh.md#serverrelay_registrytoken--动态目标发现)）。

```
POST   /api/relay/register   # 注册或刷新目标
DELETE /api/relay/register   # 注销目标
GET    /api/relay/targets    # 列出所有已注册目标
```

所有 Registry API 请求需携带 Bearer Token：

```bash
curl -H "Authorization: Bearer your-registry-secret" \
     http://main-hookrun:9000/api/relay/targets
```

---

## 3. 响应格式

所有 Webhook 响应均为 JSON 格式，消息使用英文。

### 成功（200）

```json
{
  "code": 200,
  "message": "ok",
  "config": "github-auto-deploy",
  "rule": "push-to-main",
  "actions": 3
}
```

### 无匹配规则（200）

```json
{
  "code": 200,
  "message": "No matching rules",
  "config": "github-auto-deploy"
}
```

### 认证失败（401）

```json
{
  "code": 401,
  "message": "Authentication failed"
}
```

### 配置未找到（404）

```json
{
  "code": 404,
  "message": "Config 'not-exist' not found"
}
```

### 执行被阻止（409）

当 `policy: "block"` 且任务仍在运行时：

```json
{
  "code": 409,
  "message": "Task 'github-auto-deploy/push-to-main' is running, please try again later"
}
```

### 冷却中（429）

当 `policy: "cooldown"` 且处于冷却窗口内时：

```json
{
  "code": 429,
  "message": "Task 'github-auto-deploy/push-to-main' is in cooldown, retry in 120 seconds"
}
```

### 基础路由已禁用（400）

当 `allow_all: false` 时请求 `/webhook`：

```json
{
  "code": 400,
  "message": "Base route iteration is disabled, please specify target: /webhook/{name}"
}
```

### Relay Registry — 注册（200）

```json
{
  "status": "registered",
  "targets_count": 3
}
```

### Relay Registry — 注销（200）

```json
{
  "status": "unregistered",
  "targets_count": 2
}
```

### Relay Registry — 列出目标（200）

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

### Relay Registry — 错误

| 状态码 | 含义 |
|--------|------|
| `401` | Bearer Token 缺失或无效 |
| `405` | 方法不允许 |
| `429` | 注册池已满（达到 `max_registry_entries` 上限） |
| `503` | Relay Registry 未启用（未配置 `relay_registry_token`） |

---

## 4. 常见场景

### 场景 1：GitHub Push 自动部署

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

Webhook URL：`POST /webhook/github-deploy`

### 场景 2：GitLab CI 流水线完成通知

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

### 场景 3：限频部署

```yaml
name: "rate-limited-deploy"
execution:
  policy: "cooldown"
  cooldown_seconds: 600    # 最多每 10 分钟一次
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

### 场景 4：单文件多环境

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
      cooldown_seconds: 1800    # 生产环境 30 分钟冷却
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

### 场景 5：强制使用定向路由

禁用基础路由遍历，要求明确指定目标：

```yaml
# config.yaml
server:
  port: 9000
  route: "/webhook"
  allow_all: false    # 必须使用 /webhook/{filename}
```

所有请求 `/webhook` 的请求返回 400。客户端必须使用：

```
POST /webhook/frontend
POST /webhook/backend
POST /webhook/api
```

### 场景 6：多服务器 Relay 部署

通过静态目标和动态 Tag 匹配，将 Webhook 从中心 HookRun 转发到多个下游实例：

```yaml
# 上游（主 HookRun）— hooks/deploy-app.yaml
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
            - url: "http://10.0.0.2:9000/webhook/deploy-app"   # 静态目标
              token: "relay-secret-B"
            - tag: "prod"                                      # 动态：所有带 "prod" 标签的已注册目标
          forward_headers:
            - "X-GitHub-Event"
          timeout: 30
          max_relay_hops: 3
```

```yaml
# 上游 config.yaml — 启用 Relay Registry 实现动态发现
server:
  port: 9000
  relay_registry_token: "your-registry-secret"
  max_relay_ttl: 300
  max_registry_entries: 100
```

下游实例启动时自动注册并维持心跳：

```yaml
# 下游（10.0.0.2）config.yaml
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

### 场景 7：自注册 Relay 客户端

下游 HookRun 实例启动时自动向上游注册并发送周期性心跳，无需手动操作：

```yaml
# 下游 config.yaml
server:
  port: 9000

relay_client:
  upstream: "http://main-hookrun:9000"          # 上游 URL（必填）
  url: "http://10.0.0.5:9000/webhook/deploy-app" # 本地可达 URL
  token: "my-auth-token"                         # Relay 转发时的认证 Token
  registry_token: "your-registry-secret"         # Registry API Bearer Token
  tags: ["web", "prod"]                          # 目标匹配标签
  ttl: 120                                       # TTL（秒）
  webhook_path: "/webhook/deploy-app"            # 用于 URL 自动探测
```

- 启动时自动发送 `POST /api/relay/register` 到上游
- 每 `ttl / 3` 秒发送心跳保活
- 优雅关闭时发送 `DELETE /api/relay/register` 注销
- 若省略 `url`，则从本地 IP + 端口 + `webhook_path` 自动探测

---

## 5. 日志

### Daily 模式（默认）

- 日志写入 `{log.path}/hookrun-YYYY-MM-DD.log`
- 每天创建新文件
- 启动时自动删除超过 `retention_days` 的过期日志

### Single 模式

- 日志写入 `{log.path}/hookrun.log`
- 固定单文件，可按大小轮转（`max_size_mb`）
- 适合容器环境或外部日志管理

### 规则级日志

- 每个规则配置可指定 `log.path` 写入独立日志文件
- 日志双写到全局日志和规则专属日志

每次 Webhook 请求和执行结果都带时间戳记录。

---

## 6. 配置校验最佳实践

1. **启动前务必校验**：
   ```bash
   hookrun validate && hookrun start
   ```

2. **编辑后校验**：
   ```bash
   # 编辑 YAML 文件
   vim hooks/my-app.yaml
   # 校验（不重载）
   hookrun validate
   # 校验通过后重载
   hookrun reload
   ```

3. **调试时使用前台模式**：
   ```bash
   hookrun start -f
   ```
   所有日志直接输出到终端，方便实时排查。
