# 使用指南

本指南介绍 HookRun 的日常使用：CLI 命令、路由行为、响应格式和常见场景。

---

## 1. CLI 命令

所有命令支持全局 `-c` 参数指定配置文件路径：

```bash
hookrun -c /path/to/config.yaml <command>
```

默认配置路径：当前目录下的 `config.yaml`。

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
  Allow all: true
  First match only: true
  Log path: ./logs
  Log retention: 30 days
  Config dir: ./hooks
  Rule files loaded: 2
    - github-auto-deploy (2 rules: push-to-main, tag-release)
    - gitlab-ci-trigger (1 rules: pipeline-complete)
```

建议在启动或重载前运行 `validate` 以提前发现配置错误。

---

### `version` — 查看版本

```bash
hookrun version
```

输出：

```
HookRun v1.0.0
Build time: 2026-06-11
Go version: go1.23.3
OS/Arch:    linux/amd64
```

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

行为受 `allow_all` 和 `first_match_only` 控制：

| `allow_all` | `first_match_only` | 行为 |
|-------------|-------------------|------|
| `true`（默认） | `true`（默认） | 遍历所有配置，匹配第一个规则后停止 |
| `true` | `false` | 遍历所有配置，执行所有匹配的规则 |
| `false` | — | 返回 400 错误（必须使用定向路由） |

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
{"status": "ok", "uptime": "2h30m15s", "rules": 3, "version": "1.0.0"}
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

### 多响应

当 `first_match_only: false` 时，响应为数组：

```json
[
  {"code": 200, "message": "ok", "config": "app-a", "rule": "deploy", "actions": 2},
  {"code": 200, "message": "ok", "config": "app-b", "rule": "sync", "actions": 1}
]
```

---

## 4. 常见场景

### 场景 1：GitHub Push 自动部署

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

---

## 5. 日志

- 日志写入 `{log.path}/hookrun-YYYY-MM-DD.log`
- 每天创建新文件
- 启动时自动删除超过 `retention_days` 的过期日志
- 每次 Webhook 请求和执行结果都带时间戳记录

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
