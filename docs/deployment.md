# 部署指南

## 环境要求

- Go 1.25.0+（仅编译需要）
- Docker / Docker Compose（容器部署）
- OpenSSL（生成 TLS 证书）

## 构建

### 本地构建

```bash
make build          # 构建当前平台二进制 → bin/nodepass
```

### 交叉编译

```bash
make build-all      # linux/darwin/windows × amd64/arm64
```

产物位于 `bin/` 目录：

```
bin/
├── nodepass-linux-amd64
├── nodepass-linux-arm64
├── nodepass-darwin-amd64
├── nodepass-darwin-arm64
├── nodepass-windows-amd64.exe
└── nodepass-windows-arm64.exe
```

## TLS 证书生成

```bash
./scripts/gen-certs.sh
```

默认在 `certs/` 目录生成 CA 和节点证书（controller、ingress、egress、relay），可通过 `CERT_DIR` 环境变量指定输出目录：

```bash
CERT_DIR=/etc/nodepass/certs ./scripts/gen-certs.sh
```

## 运行

```bash
# 启动节点
nodepass --config configs/ingress.yaml

# 指定角色覆盖配置
nodepass --config config.yaml --run-as relay

# 验证配置
nodepass --config config.yaml --validate

# 查看版本
nodepass --version
```

## Docker 部署

### Dockerfile

多阶段构建，最终镜像基于 scratch，体积极小：

```bash
docker build -t nodepass:latest .
```

### Docker Compose 编排

四节点编排（controller + ingress + relay + egress）：

```bash
docker-compose up -d
```

服务端口映射：

| 服务 | 数据端口 | 指标端口 | 控制端口 |
|------|----------|----------|----------|
| controller | — | 9090 | 8443 |
| ingress | 9000 | 9090 | — |
| relay | 9100 | 9090 | — |
| egress | 9200 | 9090 | — |

### 查看日志

```bash
docker-compose logs -f ingress
docker-compose logs -f relay
```

## 手动部署

使用部署脚本将二进制和配置分发到远程服务器：

```bash
# 部署到指定服务器
SERVERS="ingress-1 egress-1 relay-1" ./scripts/deploy.sh
```

脚本会：
1. 执行 `make build-all` 编译
2. 通过 scp 分发二进制到 `/usr/local/bin/nodepass`
3. 分发配置到 `/etc/nodepass/config.yaml`
4. 分发证书到 `/etc/nodepass/certs/`（如果存在）
5. 通过 systemctl 重启服务

### systemd 服务文件示例

```ini
[Unit]
Description=NodePass Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/nodepass --config /etc/nodepass/config.yaml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

## 监控

### Prometheus

使用项目根目录的 `prometheus.yml` 配置 Prometheus 抓取：

```yaml
scrape_configs:
  - job_name: 'nodepass'
    static_configs:
      - targets:
          - 'ingress:9090'
          - 'egress:9090'
          - 'relay:9090'
          - 'controller:9090'
```

### pprof 调试

在配置中启用 pprof：

```yaml
observability:
  pprof:
    enabled: true
    listen: "0.0.0.0:6060"
```

```bash
# CPU Profile
curl http://localhost:6060/debug/pprof/profile?seconds=30 > cpu.prof
go tool pprof cpu.prof

# 内存 Profile
curl http://localhost:6060/debug/pprof/heap > mem.prof

# Goroutine 泄漏检测
curl http://localhost:6060/debug/pprof/goroutine?debug=2
```
