# Deployment Guide

This guide covers building, deploying, and running HookRun in production environments.

---

## 1. Build from Source

### Prerequisites

- Go 1.21 or later
- Git

### Build

```bash
git clone https://github.com/bluvenr/hookrun.git
cd hookrun
go build -o hookrun ./cmd/hookrun/
```

### Build with Version Info

Use `-ldflags` to inject version and build time:

```bash
go build -ldflags "-X main.Version=1.1.0 -X 'main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" \
  -o hookrun ./cmd/hookrun/
```

### Cross-Compilation

Build for different platforms:

```bash
# Linux amd64
GOOS=linux GOARCH=amd64 go build -o hookrun-linux-amd64 ./cmd/hookrun/

# Linux arm64 (e.g. Raspberry Pi, AWS Graviton)
GOOS=linux GOARCH=arm64 go build -o hookrun-linux-arm64 ./cmd/hookrun/

# macOS amd64 (Intel)
GOOS=darwin GOARCH=amd64 go build -o hookrun-darwin-amd64 ./cmd/hookrun/

# macOS arm64 (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -o hookrun-darwin-arm64 ./cmd/hookrun/

# Windows amd64
GOOS=windows GOARCH=amd64 go build -o hookrun-windows-amd64.exe ./cmd/hookrun/
```

---

## 2. Directory Structure

Recommended production layout:

```
/opt/hookrun/
├── hookrun                 # Binary
├── config.yaml             # Global config
├── hooks/                  # Rule YAML files
│   ├── app-a.yaml
│   └── app-b.yaml
├── scripts/                # Custom scripts referenced by rules
│   └── deploy.sh
└── logs/                   # Log output directory
    └── hookrun-2026-06-11.log
```

Runtime data (PID file, status, signals) is stored in `~/.hookrun/`:

```
~/.hookrun/
├── hookrun.pid             # Process PID
├── hookrun.status          # JSON status file
├── hookrun.reload          # Reload signal file (Windows)
└── hookrun.stop            # Stop signal file (Windows)
```

---

## 3. Linux Deployment

### Option A: Direct Execution (Simple)

```bash
# Copy binary and config
cp hookrun /usr/local/bin/
mkdir -p /etc/hookrun/hooks
cp config.yaml /etc/hookrun/

# Run
cd /etc/hookrun
hookrun start
```

### Option B: systemd Service (Recommended)

Create `/etc/systemd/system/hookrun.service`:

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

# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/etc/hookrun/logs
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

Setup:

```bash
# Create user
sudo useradd -r -s /sbin/nologin hookrun

# Create directories
sudo mkdir -p /etc/hookrun/hooks /etc/hookrun/scripts /etc/hookrun/logs
sudo chown -R hookrun:hookrun /etc/hookrun

# Copy files
sudo cp hookrun /usr/local/bin/
sudo cp config.yaml /etc/hookrun/
sudo cp hooks/*.yaml /etc/hookrun/hooks/

# Set script permissions
sudo chmod +x /etc/hookrun/scripts/*.sh

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable hookrun
sudo systemctl start hookrun

# Check status
sudo systemctl status hookrun
```

**Note**: When using systemd, always use `start -f` (foreground) so systemd manages the process lifecycle directly.

### Systemd Management Commands

```bash
# Stop
sudo systemctl stop hookrun

# Restart
sudo systemctl restart hookrun

# View logs
sudo journalctl -u hookrun -f

# Reload config (hot-reload)
hookrun reload -c /etc/hookrun/config.yaml
```

---

## 4. Docker Deployment

### Dockerfile

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

### Build and Run

```bash
# Build image
docker build -t hookrun:latest .

# Run
docker run -d \
  --name hookrun \
  -p 9000:9000 \
  -v ./hooks:/app/hooks \
  -v ./logs:/app/logs \
  --restart unless-stopped \
  hookrun:latest
```

### Docker Compose

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

Run:

```bash
docker compose up -d
```

---

## 5. Windows Deployment

### Manual Setup

```powershell
# Create directory
New-Item -ItemType Directory -Force -Path C:\hookrun\hooks

# Copy files
Copy-Item hookrun.exe C:\hookrun\
Copy-Item config.yaml C:\hookrun\
Copy-Item hooks\*.yaml C:\hookrun\hooks\

# Run
cd C:\hookrun
.\hookrun.exe start
```

### Run as Windows Service

Use [NSSM (Non-Sucking Service Manager)](https://nssm.cc/):

```powershell
nssm install HookRun C:\hookrun\hookrun.exe
nssm set HookRun AppParameters "start -f -c C:\hookrun\config.yaml"
nssm set HookRun AppDirectory C:\hookrun
nssm start HookRun
```

### Windows-Specific Notes

- **Process control**: Uses signal file IPC (polled every 2 seconds) instead of Unix signals
- **PID detection**: Uses `tasklist` command to check if process is running
- **Shell**: Commands execute via `cmd /c`

---

## 6. Reverse Proxy

### Nginx

```nginx
location /webhook/ {
    proxy_pass http://127.0.0.1:9000/webhook/;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    # Increase timeout for long-running actions
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

## 7. Security Recommendations

1. **Use authentication**: Always set `auth.token` in rule configs
2. **IP whitelist**: Restrict to known webhook source IPs where possible
3. **HTTPS**: Use a reverse proxy with TLS termination
4. **Firewall**: Only expose port 443 (HTTPS); keep port 9000 internal
5. **Least privilege**: Run HookRun as a non-root user with minimal filesystem permissions
6. **Script permissions**: Ensure scripts in `scripts/` have appropriate execute permissions
7. **Config validation**: Always run `hookrun validate` after editing configs

---

## 8. Monitoring

### Health Check Endpoint

```bash
curl http://localhost:9000/health
```

Response:

```json
{"status": "ok", "uptime": "2h30m15s", "rules": 3, "version": "1.1.0"}
```

### Log Monitoring

```bash
# Tail logs
tail -f logs/hookrun-$(date +%Y-%m-%d).log

# Count requests today
grep -c "Webhook request" logs/hookrun-$(date +%Y-%m-%d).log

# Check for errors
grep "ERROR" logs/hookrun-$(date +%Y-%m-%d).log
```

### Integration with Monitoring Tools

Use the `/health` endpoint with:

- **Prometheus**: via `blackbox_exporter` HTTP probe
- **Uptime Kuma**: HTTP(s) monitor on `/health`
- **Nagios/Zabbix**: Custom check script hitting `/health`
