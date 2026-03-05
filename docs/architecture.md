# 架构设计

## 总览

NodePass 2.0 是一个分布式边缘网络 SDN Agent，采用分层架构设计。每个节点运行一个 Agent 实例，通过控制平面协调工作。

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

## 节点角色

| 角色 | 说明 | 典型入站 | 典型出站 |
|------|------|----------|----------|
| ingress | 入口节点，接收客户端连接 | tcp / tls / ws / quic | np-chain |
| relay | 中继节点，转发流量 | np-chain | np-chain |
| egress | 出口节点，连接目标服务 | np-chain | direct |

## 多跳链路

```
Client ──TCP──▶ Ingress ──NP-Chain──▶ Relay ──NP-Chain──▶ Egress ──Direct──▶ Target
```

每个节点只需知道下一跳地址，通过 NP-Chain 协议帧中的 HopChain 实现逐跳剥离转发。

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

## 数据流

1. 客户端连接到 Inbound Handler（TCP/TLS/WS/QUIC）
2. Inbound 读取 NP-Chain 帧，解码出 SessionMeta（目标地址、HopChain）
3. Router 根据规则选择 OutboundHandler
4. Outbound 拨号到目标（直连或下一跳），建立双向 relay
5. 数据在 inbound 连接和 outbound 连接之间双向转发

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
| `round-robin` | 轮询 |
| `least-load` | 基于 HealthCheck 分值选择最优节点 |

## 多路复用层

- **Aggregator**：将数据分片后轮询写入多条物理连接，实现带宽叠加
- **Reassembler**：接收乱序分片，按序号重组后输出完整数据
- **FlowControl**：基于窗口的流控，使用 atomic int64 实现无锁操作

## 控制平面

HTTP/JSON 接口，基础路径 `/api/v1/`。

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/v1/register` | POST | 节点注册，返回 token |
| `/api/v1/heartbeat` | POST | 心跳上报，携带节点状态 |
| `/api/v1/stats` | POST | 会话统计上报 |
| `/api/v1/nodes` | GET | 列出所有注册节点 |
| `/api/v1/config` | POST | 拉取节点配置（带版本号，增量更新） |
| `/api/v1/config/set` | POST | 设置节点配置（管理端调用） |

## 可观测性

- **Prometheus 指标**：Agent 状态、入站连接数、出站拨号数/延迟、Goroutine 数量
- **结构化日志**：基于 zap，支持 JSON/console 格式
- **pprof 调试**：CPU/内存 Profile、Goroutine 泄漏检测
