# NodePass 2.0

[![CI](https://github.com/nodeox/nodepass/workflows/CI/badge.svg)](https://github.com/nodeox/nodepass/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/nodeox/nodepass)](https://goreportcard.com/report/github.com/nodeox/nodepass)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

NodePass 2.0 是一个生产级分布式边缘网络 SDN Agent，支持多跳、多入多出、带宽聚合和智能路由。

## ✨ 特性

- 🚀 **单一二进制**: 跨平台静态链接，动态角色切换
- ⚡ **极致并发**: 全协程调度，单节点支持 10K+ 并发会话
- 🌐 **智能拓扑**: 原生支持多跳、多入多出、带宽聚合
- 📊 **生产就绪**: 全链路追踪、故障熔断、安全审计
- 🔒 **安全第一**: 强制 mTLS、配置加密、审计日志

## 🏗️ 架构

```
Control Plane (控制面)
    ↓
Protocol Layer (协议层)
    ↓
Routing Layer (路由层)
    ↓
Mux/Agg Layer (复用/聚合层)
    ↓
Transport Layer (传输层: QUIC-First)
```

## 🚀 快速开始

### 安装

```bash
# 从源码构建
git clone https://github.com/nodeox/nodepass.git
cd nodepass
make build

# 或使用 go install
go install github.com/nodeox/nodepass/cmd/nodepass@latest
```

### 运行

```bash
# 使用示例配置
./bin/nodepass --config configs/example.yaml

# 指定角色
./bin/nodepass --config config.yaml --run-as ingress

# 验证配置
./bin/nodepass --config config.yaml --validate
```

## 📖 文档

- [架构设计](docs/architecture.md)
- [协议规范](docs/protocol.md)
- [配置指南](docs/configuration.md)
- [部署指南](docs/deployment.md)
- [开发指南](docs/development.md)

## 🛠️ 开发

### 环境要求

- Go 1.25+
- Make

### 开发命令

```bash
# 运行测试
make test

# 代码检查
make lint

# 格式化代码
make fmt

# 运行基准测试
make bench

# 构建所有平台
make build-all
```

### 项目结构

```
nodepass/
├── cmd/nodepass/          # 主入口
├── internal/              # 内部包
│   ├── agent/            # Agent 核心引擎
│   ├── transport/        # 传输层
│   ├── mux/              # 多路复用
│   ├── routing/          # 路由层
│   ├── protocol/         # 协议层
│   ├── inbound/          # 入站处理器
│   ├── outbound/         # 出站处理器
│   ├── control/          # 控制平面
│   ├── observability/    # 可观测性
│   └── common/           # 公共组件
├── configs/              # 配置示例
├── test/                 # 测试
└── docs/                 # 文档
```

## 🧪 测试

```bash
# 单元测试
make test

# 集成测试
make test-e2e

# 基准测试
make bench

# 覆盖率报告
go tool cover -html=coverage.out
```

## 📊 性能指标

- 单节点并发会话: **10,000+**
- 单会话吞吐量: **1 Gbps+**
- 多跳延迟增量: **< 10%**
- 内存占用: **< 1GB** (10K 会话)

## 🤝 贡献

欢迎贡献！请查看 [CONTRIBUTING.md](CONTRIBUTING.md) 了解详情。

### 贡献流程

1. Fork 项目
2. 创建特性分支 (`git checkout -b feature/amazing-feature`)
3. 提交更改 (`git commit -m 'Add amazing feature'`)
4. 推送到分支 (`git push origin feature/amazing-feature`)
5. 创建 Pull Request

## 📝 许可证

本项目采用 MIT 许可证 - 查看 [LICENSE](LICENSE) 文件了解详情。

## 🙏 致谢

- [quic-go](https://github.com/quic-go/quic-go) - QUIC 协议实现
- [gobwas/ws](https://github.com/gobwas/ws) - WebSocket
- [zap](https://github.com/uber-go/zap) - 结构化日志
- [Prometheus](https://github.com/prometheus/client_golang) - 指标监控

## 📞 联系方式

- 问题反馈: [GitHub Issues](https://github.com/nodeox/nodepass/issues)
- 讨论: [GitHub Discussions](https://github.com/nodeox/nodepass/discussions)

---

**当前版本**: 2.0.0-dev
**状态**: 开发中
