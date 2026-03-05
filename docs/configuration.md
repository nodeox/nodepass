# 配置指南

## 配置文件格式

NodePass 使用 YAML 格式配置文件。

## 完整配置结构

```yaml
version: "2.0"

node:
  id: "node-1"          # 节点唯一标识
  type: "ingress"        # 节点角色: ingress / relay / egress
  tags:                  # 自定义标签
    region: "us-west"

controller:
  enabled: true
  address: "http://controller:8443"

inbounds:                # 入站配置列表
  - protocol: "tcp"      # 协议: tcp / tls / ws / quic / np-chain / forward
    listen: "0.0.0.0:9000"
    target: ""           # 仅 forward 协议需要
    tls:                 # 仅 tls / quic 协议需要
      cert: ""
      key: ""

outbounds:               # 出站配置列表
  - name: "relay-1"      # 出站名称（唯一）
    protocol: "np-chain"  # 协议: direct / np-chain
    group: "relay"        # 所属组（路由使用）
    address: "relay:9100" # 目标地址

routing:
  rules:
    - type: "default"           # 规则类型: domain / ip / user / default
      outbound_group: "relay"   # 目标出站组
      strategy: "round-robin"   # 策略: random / round-robin / least-load
      values: []                # 匹配值列表

observability:
  pprof:
    enabled: true
    listen: "0.0.0.0:6060"
  metrics:
    enabled: true
    listen: "0.0.0.0:9090"
  logging:
    level: "info"          # debug / info / warn / error
    format: "json"         # json / console
```

## 配置示例

### Ingress 节点

接收客户端 TCP 连接，转发到 Relay 节点：

```yaml
version: "2.0"
node:
  id: "ingress-1"
  type: "ingress"
  tags:
    region: "us-west"

controller:
  enabled: true
  address: "http://controller:8443"

inbounds:
  - protocol: "tcp"
    listen: "0.0.0.0:9000"

outbounds:
  - name: "relay-1"
    protocol: "np-chain"
    group: "relay"
    address: "relay:9100"

routing:
  rules:
    - type: "default"
      outbound_group: "relay"
      strategy: "round-robin"

observability:
  metrics:
    enabled: true
    listen: "0.0.0.0:9090"
  logging:
    level: "info"
    format: "json"
```

### Relay 节点

接收 NP-Chain 帧，转发到 Egress 节点：

```yaml
version: "2.0"
node:
  id: "relay-1"
  type: "relay"

inbounds:
  - protocol: "np-chain"
    listen: "0.0.0.0:9100"

outbounds:
  - name: "egress-1"
    protocol: "np-chain"
    group: "egress"
    address: "egress:9200"

routing:
  rules:
    - type: "default"
      outbound_group: "egress"
      strategy: "round-robin"
```

### Egress 节点

接收 NP-Chain 帧，直连目标服务：

```yaml
version: "2.0"
node:
  id: "egress-1"
  type: "egress"

inbounds:
  - protocol: "np-chain"
    listen: "0.0.0.0:9200"

outbounds:
  - name: "direct"
    protocol: "direct"
    group: "direct"

routing:
  rules:
    - type: "default"
      outbound_group: "direct"
      strategy: "random"
```

### TLS 加密入站

```yaml
inbounds:
  - protocol: "tls"
    listen: "0.0.0.0:9443"
    tls:
      cert: "/etc/nodepass/certs/node.crt"
      key: "/etc/nodepass/certs/node.key"
```

### QUIC 入站

```yaml
inbounds:
  - protocol: "quic"
    listen: "0.0.0.0:9443"
    tls:
      cert: "/etc/nodepass/certs/node.crt"
      key: "/etc/nodepass/certs/node.key"
```

### WebSocket 入站

```yaml
inbounds:
  - protocol: "ws"
    listen: "0.0.0.0:9080"
```

### 端口转发

将本地端口直接转发到目标地址，无需 NP-Chain 协议帧：

```yaml
inbounds:
  - protocol: "forward"
    listen: "0.0.0.0:8080"
    target: "192.168.1.100:3000"
```

## 路由规则

### 域名匹配

```yaml
routing:
  rules:
    - type: "domain"
      values:
        - "google.com"           # 精确匹配
        - "*.google.com"         # 通配符匹配
        - "keyword:google"       # 关键字匹配
      outbound_group: "proxy"
      strategy: "least-load"
```

### IP/CIDR 匹配

```yaml
routing:
  rules:
    - type: "ip"
      values:
        - "1.2.3.4"
        - "192.168.0.0/16"
      outbound_group: "direct"
      strategy: "random"
```

### 用户匹配

```yaml
routing:
  rules:
    - type: "user"
      values:
        - "user123"
      outbound_group: "premium"
      strategy: "least-load"
```

## 配置热更新

Agent 支持运行时配置热更新，无需重启。

工作流程：
1. Agent 启动时通过 `--config` 指定配置文件路径
2. 后台以 5 秒间隔轮询文件修改时间
3. 检测到变化后自动加载、验证、计算差异并应用

支持热更新的配置项：
- 入站的增删改（自动启停监听）
- 出站的增删改（自动注册/注销路由）
- 路由规则变更

## 配置验证

```bash
nodepass --config config.yaml --validate
```

验证规则：
- 节点 ID 和类型不能为空
- 入站监听地址不能为空
- 出站名称不能为空且不能重复
- TLS/QUIC 协议必须配置证书
- forward 协议必须配置 target
