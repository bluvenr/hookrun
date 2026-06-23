# 部署说明

[English](deployment.md)

本指南介绍如何在生产环境中构建、部署和运行 HookRun。

---

## 1. 从源码构建

### 前置要求

- Go 1.23 或更高版本
- Git

### 构建

```bash
git clone https://github.com/bluvenr/hookrun.git
cd hookrun
go build -o hookrun ./cmd/hookrun/
```

### 注入版本信息构建

使用 `-ldflags` 注入版本号和构建时间：

```bash
go build -ldflags "-X github.com/bluvenr/hookrun/internal/version.Version=1.1.3 -X 'github.com/bluvenr/hookrun/internal/version.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" \
  -o hookrun ./cmd/hookrun/
```

### 交叉编译

为不同平台构建：

```bash
# Linux amd64
GOOS=linux GOARCH=amd64 go build -o hookrun-linux-amd64 ./cmd/hookrun/

# Linux arm64（如树莓派、AWS Graviton）
GOOS=linux GOARCH=arm64 go build -o hookrun-linux-arm64 ./cmd/hookrun/

# macOS amd64（Intel）
GOOS=darwin GOARCH=amd64 go build -o hookrun-darwin-amd64 ./cmd/hookrun/

# macOS arm64（Apple Silicon）
GOOS=darwin GOARCH=arm64 go build -o hookrun-darwin-arm64 ./cmd/hookrun/

# Windows amd64
GOOS=windows GOARCH=amd64 go build -o hookrun-windows-amd64.exe ./cmd/hookrun/
```

---

## 2. 目录结构

推荐的生产环境目录布局：

```
/opt/hookrun/
├── hookrun                 # 二进制文件
├── config.yaml             # 全局配置
├── hooks/                  # 规则 YAML 文件
│   ├── app-a.yaml
│   └── app-b.yaml
├── scripts/                # 规则引用的自定义脚本
│   └── deploy.sh
└── logs/                   # 日志输出目录
    └── hookrun-2026-06-11.log
```

运行时数据（PID 文件、状态、信号）存储在 `~/.hookrun/`：

```
~/.hookrun/
├── hookrun.pid             # 进程 PID
├── hookrun.status          # JSON 状态文件
├── hookrun.reload          # 重载信号文件（Windows）
└── hookrun.stop            # 停止信号文件（Windows）
```

---

## 3. Linux 部署

### 方式 A：直接运行（简单）

```bash
# 复制二进制和配置
cp hookrun /usr/local/bin/
mkdir -p /etc/hookrun/hooks
cp config.yaml /etc/hookrun/

# 运行
cd /etc/hookrun
hookrun start
```

### 方式 B：systemd 服务（推荐）

创建 `/etc/systemd/system/hookrun.service`：

```ini
[Unit]
Description=HookRun - Webhook Action Engine
After=network.target

[Service]
Type=simple
User=hookrun
Group=hookrun
WorkingDirectory=/etc/hookrun
ExecStart=/usr/local/bin/hookrun start -f -c /etc/hookrun/config.yaml
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

# 安全加固
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/etc/hookrun/logs
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

安装步骤：

```bash
# 创建用户
sudo useradd -r -s /sbin/nologin hookrun

# 创建目录
sudo mkdir -p /etc/hookrun/hooks /etc/hookrun/scripts /etc/hookrun/logs
sudo chown -R hookrun:hookrun /etc/hookrun

# 复制文件
sudo cp hookrun /usr/local/bin/
sudo cp config.yaml /etc/hookrun/
sudo cp hooks/*.yaml /etc/hookrun/hooks/

# 设置脚本权限
sudo chmod +x /etc/hookrun/scripts/*.sh

# 启用并启动
sudo systemctl daemon-reload
sudo systemctl enable hookrun
sudo systemctl start hookrun

# 检查状态
sudo systemctl status hookrun
```

**注意**：使用 systemd 时，务必使用 `start -f`（前台模式），让 systemd 直接管理进程生命周期。

### systemd 管理命令

```bash
# 停止
sudo systemctl stop hookrun

# 重启
sudo systemctl restart hookrun

# 查看日志
sudo journalctl -u hookrun -f

# 热重载配置
hookrun reload -c /etc/hookrun/config.yaml
```

---

## 4. Docker 部署

### 方式 A：拉取预构建镜像（推荐）

```bash
docker pull ghcr.io/bluvenr/hookrun:latest

docker run -d \
  --name hookrun \
  -p 9000:9000 \
  -v ./config.yaml:/app/config.yaml \
  -v ./hooks:/app/hooks \
  -v ./logs:/app/logs \
  --restart unless-stopped \
  ghcr.io/bluvenr/hookrun:latest
```

可用标签：

| 标签 | 说明 |
|------|------|
| `latest` | 最新稳定版 |
| `1.1.3` | 指定版本 |
| `1.1` | 当前次版本最新补丁 |

### 方式 B：从源码构建

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o hookrun ./cmd/hookrun/

FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /build/hookrun /usr/local/bin/hookrun
COPY config.yaml /app/
COPY hooks/ /app/hooks/
EXPOSE 9000
ENTRYPOINT ["hookrun", "start", "-f", "-c", "/app/config.yaml"]
```

```bash
# 构建镜像
docker build -t hookrun:latest .

# 运行
docker run -d \
  --name hookrun \
  -p 9000:9000 \
  -v ./hooks:/app/hooks \
  -v ./logs:/app/logs \
  --restart unless-stopped \
  hookrun:latest
```

### Docker Compose

使用 GHCR 镜像（推荐）：

```yaml
version: "3.8"
services:
  hookrun:
    image: ghcr.io/bluvenr/hookrun:latest
    container_name: hookrun
    ports:
      - "9000:9000"
    volumes:
      - ./config.yaml:/app/config.yaml
      - ./hooks:/app/hooks
      - ./logs:/app/logs
      - ./scripts:/app/scripts
    restart: unless-stopped
```

或从源码构建：

```yaml
version: "3.8"
services:
  hookrun:
    build: .
    container_name: hookrun
    ports:
      - "9000:9000"
    volumes:
      - ./hooks:/app/hooks
      - ./logs:/app/logs
      - ./scripts:/app/scripts
    restart: unless-stopped
```

运行：

```bash
docker compose up -d
```

---

## 5. Windows 部署

### 手动安装

```powershell
# 创建目录
New-Item -ItemType Directory -Force -Path C:\hookrun\hooks

# 复制文件
Copy-Item hookrun.exe C:\hookrun\
Copy-Item config.yaml C:\hookrun\
Copy-Item hooks\*.yaml C:\hookrun\hooks\

# 运行
cd C:\hookrun
.\hookrun.exe start
```

### 注册为 Windows 服务

使用 [NSSM (Non-Sucking Service Manager)](https://nssm.cc/)：

```powershell
nssm install HookRun C:\hookrun\hookrun.exe
nssm set HookRun AppParameters "start -f -c C:\hookrun\config.yaml"
nssm set HookRun AppDirectory C:\hookrun
nssm start HookRun
```

### Windows 注意事项

- **进程控制**：使用信号文件 IPC（每 2 秒轮询），而非 Unix 信号
- **PID 检测**：使用 `tasklist` 命令检查进程是否运行
- **Shell**：命令通过 `cmd /c` 执行

---

## 6. 反向代理

### Nginx

```nginx
location /webhook/ {
    proxy_pass http://127.0.0.1:9000/webhook/;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    # 增加超时时间以适应长时间运行的动作
    proxy_read_timeout 300s;
}
```

### Caddy

```
webhook.example.com {
    reverse_proxy localhost:9000
}
```

---

## 7. 安全建议

1. **启用认证**：始终在规则配置中设置 `auth.token`
2. **IP 白名单**：尽可能限制为已知的 Webhook 来源 IP
3. **HTTPS**：使用反向代理进行 TLS 终结
4. **防火墙**：仅暴露 443 端口（HTTPS），9000 端口保持内部访问
5. **最小权限**：以非 root 用户运行 HookRun，赋予最小文件系统权限
6. **脚本权限**：确保 `scripts/` 目录中的脚本具有适当的执行权限
7. **配置校验**：编辑配置后务必运行 `hookrun validate`

---

## 8. 监控

### 健康检查接口

```bash
curl http://localhost:9000/health
```

响应：

```json
{"status": "ok", "uptime": "2h30m15s", "rules": 3, "version": "x.y.z"}
```

### 日志监控

```bash
# 实时查看日志
tail -f logs/hookrun-$(date +%Y-%m-%d).log

# 统计今日请求数
grep -c "Webhook request" logs/hookrun-$(date +%Y-%m-%d).log

# 检查错误
grep "ERROR" logs/hookrun-$(date +%Y-%m-%d).log
```

### 监控工具集成

使用 `/health` 接口对接：

- **Prometheus**：通过 `blackbox_exporter` HTTP 探针
- **Uptime Kuma**：HTTP(s) 监控 `/health`
- **Nagios/Zabbix**：自定义检查脚本访问 `/health`
