# 开发指南

## 环境要求

- Go 1.25.0+
- Make

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
│   │   └── router.go              #   域名/IP/用户规则匹配，负载均衡策略
│   ├── transport/                 # 传输层
│   │   ├── tcp.go                 #   TCP 连接池
│   │   └── quic.go                #   QUIC 连接池 + QUICConn 适配器
│   ├── mux/                       # 多路复用
│   │   ├── aggregator.go          #   带宽聚合器
│   │   ├── reassembler.go         #   重组器
│   │   └── flowcontrol.go         #   窗口流控
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
├── scripts/                       # 部署/证书脚本
├── Dockerfile                     # 多阶段构建
├── docker-compose.yml             # 四节点编排
├── prometheus.yml                 # Prometheus 监控配置
└── Makefile                       # 构建/测试/部署命令
```

## 开发命令

```bash
make build          # 构建二进制
make test           # 运行测试（含覆盖率和 race detector）
make bench          # 基准测试
make test-e2e       # 端到端测试
make lint           # 代码检查
make fmt            # 格式化
make build-all      # 交叉编译所有平台
```

## 添加新的入站协议

1. 在 `internal/inbound/` 下创建新文件，实现 `common.InboundHandler` 接口
2. 如果是基于 NP-Chain 帧的协议，复用 `handleNPChainStream` 和 `biRelay`（定义在 `tcp.go`）
3. 在 `internal/inbound/inbound.go` 工厂函数中注册新协议
4. 在 `internal/common/types.go` 中添加需要的配置字段（如有）
5. 编写测试

示例（tcp.go 中的共享逻辑）：

```go
// 所有非 forward 入站共享此函数
func handleNPChainStream(conn net.Conn, router common.Router, logger *zap.Logger) {
    meta, _, err := npchain.Decode(conn)
    if err != nil { return }

    outbound, err := router.Route(meta)
    if err != nil { return }

    targetConn, err := outbound.Dial(context.Background(), meta)
    if err != nil { return }
    defer targetConn.Close()

    biRelay(conn, targetConn)
}
```

## 添加新的出站协议

1. 在 `internal/outbound/` 下创建新文件，实现 `common.OutboundHandler` 接口
2. 在 `internal/outbound/outbound.go` 工厂函数中注册
3. 编写测试

## 测试

```bash
# 全部测试（含 race detector）
go test ./... -race

# 单个包
go test ./internal/inbound/ -v

# 端到端测试
go test ./test/e2e/ -v

# 基准测试
go test ./test/benchmark/ -bench=. -benchmem
```

测试约定：
- 使用 `127.0.0.1:0` 自动分配端口，避免端口冲突
- TLS/QUIC 测试使用运行时生成的自签名证书
- e2e 测试构建完整的 ingress→relay→egress 链路拓扑

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

## 代码规范

- 所有导出函数需要注释
- 错误处理使用 `fmt.Errorf("context: %w", err)` 包装
- 并发安全：使用 `sync.RWMutex` 保护共享状态，`atomic` 用于状态标志
- 日志使用 zap 的结构化字段（`zap.String`, `zap.Error` 等）
- 配置变更通过 `ConfigDiff` 计算差异，按序应用
