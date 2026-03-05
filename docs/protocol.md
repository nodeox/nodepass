# 协议规范

## NP-Chain 协议

NP-Chain 是 NodePass 自定义的二进制协议，用于多跳链路转发。

### 帧格式

```
┌──────────┬─────────┬──────────┬──────────┬────────────┬─────────┐
│ Magic(4) │ Ver(1)  │ HopCnt(1)│ Rsv(2)   │ SessionID  │ Hops... │
│ 0x4E504332│ 0x01   │          │          │   (16)     │ (34×N)  │
└──────────┴─────────┴──────────┴──────────┴────────────┴─────────┘
```

### 字段说明

| 字段 | 偏移 | 大小 | 说明 |
|------|------|------|------|
| Magic | 0 | 4 字节 | 固定值 `0x4E504332`（ASCII "NPC2"） |
| Version | 4 | 1 字节 | 协议版本，当前 `0x01` |
| HopCount | 5 | 1 字节 | Hop 条目数量 |
| Reserved | 6 | 2 字节 | 保留字段 |
| SessionID | 8 | 16 字节 | UUID，标识会话 |
| Hops | 24 | 34×N 字节 | Hop 条目数组 |

固定头部：24 字节。

### Hop 条目格式（34 字节）

| 字段 | 大小 | 说明 |
|------|------|------|
| AddrType | 1 字节 | 地址类型 |
| Port | 2 字节 | 端口号（大端序） |
| AddrLen | 1 字节 | 地址长度 |
| Addr | 最大 26 字节 | 地址数据 |
| Padding | 填充至 34 字节 | 零填充 |

### 地址类型

| 值 | 类型 | 地址长度 |
|----|------|----------|
| `0x01` | IPv4 | 4 字节 |
| `0x02` | IPv6 | 16 字节 |
| `0x03` | Domain | 可变（最大 26 字节） |

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

每一跳节点从 HopChain 头部取出下一跳地址作为 Target，剩余部分继续传递。NP-Chain 出站在转发时会将 Target 重新插入 HopChain 头部再编码，确保下游节点能正确解析完整链路。

### 编解码 API

```go
// 编码
frame, err := npchain.Encode(meta, payload)

// 解码
meta, payload, err := npchain.Decode(reader)
```

## 支持的传输协议

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
