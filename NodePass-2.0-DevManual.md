# NodePass 2.0 开发手册与项目总结

## 项目概述

NodePass 2.0 是一个分布式边缘网络 SDN Agent，使用 Go 语言实现。它支持多协议入站、多跳链路转发、带宽聚合、配置热更新和集中式控制平面管理。

**模块路径：** `github.com/nodeox/nodepass`
**Go 版本：** 1.25.0
**测试覆盖：** 120+ 测试函数，全部通过 race detector 检测

---

## 架构总览

```
┌─────────────────────────────────────────────────────────┐
│                        Agent                            │
│                                                         │
│  ┌──────────┐   ┌──────────┐   ┌───────────────────┐   │
│  │ Inbound  │──▶│  Router  │──▶│    Outbound       │   │
│  │ Handler  │   │          │   │    Handler         │   │
│  └──────────┘   └──────────┘   └───────────────────┘   │
│                                                         │
│  ┌──────────────────────────────────────────────────┐   │
│  │              Control Plane Client                 │   │
│  │  Register │ Heartbeat │ GetConfig │ ReportStats   │   │
│  └──────────────────────────────────────────────────┘   │
│                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │ Observability│  │  Lifecycle   │  │   Config     │  │
│  │ Metrics/Log  │  │  Manager     │  │   HotReload  │  │
│  └──────────────┘  └──────────────┘  └──────────────┘  │
└─────────────────────────────────────────────────────────┘
```

### 节点角色

| 角色 | 说明 | 典型入站 | 典型出站 |
|------|------|----------|----------|
| ingress | 入口节点，接收客户端连接 | tcp / tls / ws / quic | np-chain |
| relay | 中继节点，转发流量 | np-chain | np-chain |
| egress | 出口节点，连接目标服务 | np-chain | direct |

### 多跳链路示例

```
Client ──TCP──▶ Ingress ──NP-Chain──▶ Relay ──NP-Chain──▶ Egress ──Direct──▶ Target
```

---

## 项目结构

```
nodepass/
├── cmd/nodepass/main.go           # 程序入口
├── internal/
│   ├── agent/                     # Agent 核心引擎
│   │   ├── agent.go               #   生命周期管理、启动/停止
│   │   ├── config.go              #   配置加载/验证/差异比较/热更新
│   │   ├── lifecycle.go           #   组件生命周期管理器
│   │   └── pprof.go               #   pprof 调试端点
│   ├── common/                    # 公共类型与接口
│   │   ├── types.go               #   SessionMeta, InboundHandler, OutboundHandler, Router 等
│   │   └── errors.go              #   公共错误定义
│   ├── control/                   # 控制平面
│   │   ├── server.go              #   HTTP/JSON 控制面板服务端
│   │   └── client.go              #   控制面板客户端
│   ├── inbound/                   # 入站处理器
│   │   ├── inbound.go             #   工厂函数
│   │   ├── tcp.go                 #   TCP 入站 + 公共 handleNPChainStream/biRelay
│   │   ├── tls.go                 #   TLS 加密入站
│   │   ├── ws.go                  #   WebSocket 入站
│   │   ├── wsconn.go              #   WebSocket net.Conn 适配器
│   │   ├── quic.go                #   QUIC 入站
│   │   ├── npchain.go             #   NP-Chain 协议入站（Relay）
│   │   └── forward.go             #   端口转发入站
│   ├── outbound/                  # 出站处理器
│   │   ├── outbound.go            #   工厂函数
│   │   ├── direct.go              #   直连出站
│   │   └── npchain.go             #   NP-Chain 协议出站
│   ├── protocol/npchain/          # NP-Chain 协议编解码
│   │   └── codec.go               #   Encode/Decode，支持 IPv4/IPv6/域名
│   ├── routing/                   # 路由层
│   │   └── router.go              #   域名/IP/用户规则匹配，round-robin/least-load/random 策略
│   ├── transport/                 # 传输层
│   │   ├── tcp.go                 #   TCP 连接池
│   │   └── quic.go                #   QUIC 连接池 + QUICConn 适配器
│   ├── mux/                       # 多路复用
│   │   ├── aggregator.go          #   带宽聚合器（数据分片 + 轮询分发）
│   │   ├── reassembler.go         #   重组器（乱序接收 + 顺序重组）
│   │   └── flowcontrol.go         #   窗口流控（原子操作，无锁）
│   └── observability/             # 可观测性
│       ├── logging.go             #   zap 日志初始化
│       └── metrics.go             #   Prometheus 指标定义
├── test/
│   ├── e2e/                       # 端到端测试
│   │   ├── basic_test.go          #   单节点 NP-Chain echo 测试
│   │   └── chain_test.go          #   多节点链路测试（ingress→relay→egress）
│   └── benchmark/                 # 性能基准测试
│       └── throughput_test.go
├── configs/                       # 配置示例
│   ├── controller.yaml
│   ├── ingress.yaml
│   ├── egress.yaml
│   └── relay.yaml
├── scripts/
│   ├── deploy.sh                  # 部署脚本
│   └── gen-certs.sh               # TLS 证书生成
├── Dockerfile                     # 多阶段构建（golang:1.25-alpine → scratch）
├── docker-compose.yml             # 四节点编排
├── prometheus.yml                 # Prometheus 监控配置
└── Makefile                       # 构建/测试/部署命令
```

---

## 核心接口

### InboundHandler — 入站处理器

```go
type InboundHandler interface {
    Start(ctx context.Context, router Router) error  // 启动监听（阻塞）
    Stop() error                                      // 停止监听（幂等）
    Addr() net.Addr                                   // 获取监听地址
}
```

### OutboundHandler — 出站处理器

```go
type OutboundHandler interface {
    Dial(ctx context.Context, meta SessionMeta) (net.Conn, error)  // 拨号到目标
    HealthCheck(ctx context.Context) float64                       // 健康检查 (0-1)
    Name() string                                                  // 节点名称
    Group() string                                                 // 所属组
}
```

### Router — 路由器

```go
type Router interface {
    Route(meta SessionMeta) (OutboundHandler, error)  // 路由选择
    AddOutbound(out OutboundHandler)                   // 添加出站
    RemoveOutbound(name string)                        // 移除出站
    UpdateRules(rules []RoutingRule)                    // 更新规则
}
```

---

## 支持的协议

### 入站协议（6 种）

| 协议 | 说明 | 需要 TLS | 需要 Target |
|------|------|----------|-------------|
| `tcp` | 纯 TCP 隧道，接收 NP-Chain 帧 | 否 | 否 |
| `tls` | TLS 加密 TCP 隧道 | 是 | 否 |
| `ws` | WebSocket 隧道（gobwas/ws） | 否 | 否 |
| `quic` | QUIC 传输隧道 | 是 | 否 |
| `np-chain` | NP-Chain 协议入站（Relay 节点） | 否 | 否 |
| `forward` | 端口转发，无需协议帧 | 否 | 是 |

所有非 forward 入站共享 `handleNPChainStream` 逻辑：读取 NP-Chain 帧 → 解码 → 路由 → 双向 relay。

### 出站协议（2 种）

| 协议 | 说明 |
|------|------|
| `direct` | 直连目标地址 |
| `np-chain` | 连接下一跳节点，重新编码 NP-Chain 帧转发 |

---

## NP-Chain 协议

自定义二进制协议，支持多跳链路转发。

### 帧格式

```
┌──────────┬─────────┬──────────┬──────────┬────────────┬─────────┐
│ Magic(4) │ Ver(1)  │ HopCnt(1)│ Rsv(2)   │ SessionID  │ Hops... │
│ 0x4E504332│ 0x01   │          │          │   (16)     │ (34×N)  │
└──────────┴─────────┴──────────┴──────────┴────────────┴─────────┘
```

- 固定头部：24 字节
- 每个 Hop 条目：34 字节（AddrType + Port + AddrLen + Addr + Padding）
- 地址类型：IPv4 (0x01) / IPv6 (0x02) / Domain (0x03)

### 多跳转发流程

```
客户端发送: HopChain = [relayAddr, egressAddr, targetAddr]

Ingress 解码: Target=relayAddr, HopChain=[egressAddr, targetAddr]
  → NP-Chain 出站重新编码: HopChain=[relayAddr, egressAddr, targetAddr] 发给 relay

Relay 解码: Target=egressAddr, HopChain=[targetAddr]
  → NP-Chain 出站重新编码: HopChain=[egressAddr, targetAddr] 发给 egress

Egress 解码: Target=targetAddr, HopChain=[]
  → Direct 出站直连 targetAddr
```

---

## 路由系统

### 规则类型

| 类型 | 匹配方式 | 示例 |
|------|----------|------|
| `domain` | 精确/通配符/关键字 | `google.com`, `*.google.com`, `keyword:google` |
| `ip` | 精确 IP / CIDR | `1.2.3.4`, `192.168.0.0/16` |
| `user` | 用户 ID 精确匹配 | `user123` |
| `default` | 兜底规则 | — |

### 负载均衡策略

| 策略 | 说明 |
|------|------|
| `random` | 随机选择 |
| `round-robin` | 轮询（当前实现为随机） |
| `least-load` | 基于 HealthCheck 分值选择最优节点 |

---

## 控制平面 API

HTTP/JSON 接口，基础路径 `/api/v1/`。

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/v1/register` | POST | 节点注册，返回 token |
| `/api/v1/heartbeat` | POST | 心跳上报，携带节点状态 |
| `/api/v1/stats` | POST | 会话统计上报 |
| `/api/v1/nodes` | GET | 列出所有注册节点 |
| `/api/v1/config` | POST | 拉取节点配置（带版本号，增量更新） |
| `/api/v1/config/set` | POST | 设置节点配置（管理端调用） |

### 客户端使用

```go
client := control.NewClient("http://controller:8443", "node-1", "ingress", logger)
client.Register(ctx, map[string]string{"region": "us-west"})
client.Heartbeat(ctx, &control.NodeStatus{ActiveSessions: 5})
data, updated, _ := client.GetConfig(ctx)
```

---

## 配置热更新

Agent 支持运行时配置热更新，无需重启。

### 工作流程

1. Agent 启动时调用 `SetConfigPath(path)` 设置配置文件路径
2. `configWatchLoop` 以可配置间隔（默认 5s）轮询文件修改时间
3. 检测到变化后调用 `LoadConfig` → `ReloadConfig` → `DiffConfig` → `applyConfigDiff`
4. `applyConfigDiff` 按顺序处理：移除入站 → 更新入站 → 新增入站 → 移除出站 → 更新出站 → 新增出站 → 更新路由规则

### 支持的热更新项

- 入站的增删改（自动启停监听）
- 出站的增删改（自动注册/注销路由）
- 路由规则变更
- 节点配置变更

---

## 多路复用层

### Aggregator（带宽聚合器）

将数据分片后轮询写入多条物理连接，实现带宽叠加。

```go
agg := mux.NewAggregator([]net.Conn{conn1, conn2, conn3}, logger)
agg.Write(sessionID, data)  // 自动分片 + 轮询分发
```

### Reassembler（重组器）

接收乱序分片，按序号重组后输出完整数据。

```go
reassembler := mux.NewReassembler(logger)
reassembler.Process(chunkData)
output := reassembler.GetOutput(sessionID)
```

### FlowControl（流控）

基于窗口的流控，使用 atomic int64 实现无锁操作。

---

## 可观测性

### Prometheus 指标

- `nodepass_agent_state` — Agent 状态
- `nodepass_inbound_connections_total` — 入站连接计数
- `nodepass_outbound_dials_total` — 出站拨号计数
- `nodepass_outbound_dial_duration_ms` — 出站拨号延迟

### pprof 调试

配置启用后可通过 HTTP 访问：

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

---

## 构建与部署

### 本地开发

```bash
make build          # 构建二进制
make test           # 运行测试（含覆盖率）
make bench          # 基准测试
make test-e2e       # 端到端测试
make lint           # 代码检查
make fmt            # 格式化
```

### 交叉编译

```bash
make build-all      # linux/darwin/windows × amd64/arm64
```

### Docker 部署

```bash
docker-compose up -d    # 启动 controller + ingress + relay + egress
```

### 手动部署

```bash
./scripts/gen-certs.sh          # 生成 TLS 证书
SERVERS="node1 node2" ./scripts/deploy.sh   # 部署到服务器
```

### 运行

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

---

## 配置示例

### Ingress 节点

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
  pprof:
    enabled: true
    listen: "0.0.0.0:6060"
  metrics:
    enabled: true
    listen: "0.0.0.0:9090"
  logging:
    level: "info"
    format: "json"
```

### 端口转发

```yaml
inbounds:
  - protocol: "forward"
    listen: "0.0.0.0:8080"
    target: "192.168.1.100:3000"
```

### TLS 入站

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

---

## 关键依赖

| 依赖 | 版本 | 用途 |
|------|------|------|
| quic-go | v0.59.0 | QUIC 协议实现 |
| gobwas/ws | v1.4.0 | WebSocket（零拷贝升级） |
| google/uuid | v1.6.0 | UUID 生成 |
| zap | v1.27.1 | 结构化日志 |
| prometheus/client_golang | v1.23.2 | Prometheus 指标 |
| testify | v1.11.1 | 测试断言 |
| yaml.v3 | v3.0.1 | YAML 配置解析 |

---

## 开发阶段回顾

| 阶段 | 内容 | 状态 |
|------|------|------|
| Phase 0 | 项目结构、Agent 骨架、配置系统、路由层、可观测性 | ✅ |
| Phase 1 | Agent 核心引擎、生命周期管理、配置热更新 | ✅ |
| Phase 2 | 传输层（TCP/QUIC 连接池）、多路复用（聚合/重组/流控） | ✅ |
| Phase 3 | NP-Chain 协议编解码、HTTP/JSON 控制平面、e2e 测试、基准测试 | ✅ |
| Phase 4 | NP-Chain 出站、NP-Chain 入站(Relay)、leastLoad 策略、传输层入站(TCP/TLS/WS/QUIC)、端口转发 | ✅ |
| Phase 5 | 控制平面增强(GetConfig/SetConfig)、控制平面客户端、Dockerfile、docker-compose、配置示例 | ✅ |
| Phase 6 | 多节点链路 e2e 测试、pprof 调试、监控配置、部署脚本 | ✅ |
