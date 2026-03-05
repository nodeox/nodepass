package outbound

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/nodeox/nodepass/internal/common"
	"github.com/nodeox/nodepass/internal/observability"
	"github.com/nodeox/nodepass/internal/protocol/npchain"
	"go.uber.org/zap"
)

// NPChainOutbound NP-Chain 协议出站处理器
// 将流量封装为 NP-Chain 协议帧发送到下一跳节点
type NPChainOutbound struct {
	name      string
	group     string
	address   string
	transport string // "tcp" or "quic"
	logger    *zap.Logger
}

// NewNPChain 创建 NP-Chain 出站
func NewNPChain(cfg common.OutboundConfig, logger *zap.Logger) (*NPChainOutbound, error) {
	if cfg.Address == "" {
		return nil, fmt.Errorf("np-chain outbound requires address")
	}

	transport := cfg.Transport
	if transport == "" {
		transport = "tcp"
	}

	return &NPChainOutbound{
		name:      cfg.Name,
		group:     cfg.Group,
		address:   cfg.Address,
		transport: transport,
		logger:    logger,
	}, nil
}

func (n *NPChainOutbound) Name() string  { return n.name }
func (n *NPChainOutbound) Group() string { return n.group }

// Dial 连接到下一跳节点，返回封装了 NP-Chain 协议的连接
func (n *NPChainOutbound) Dial(ctx context.Context, meta common.SessionMeta) (net.Conn, error) {
	start := time.Now()

	n.logger.Debug("dialing np-chain next hop",
		zap.String("outbound", n.name),
		zap.String("address", n.address),
		zap.String("target", meta.Target),
		zap.String("transport", n.transport),
	)

	// 连接到下一跳
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", n.address)
	if err != nil {
		observability.OutboundDialsTotal.WithLabelValues(n.name, "failure").Inc()
		return nil, fmt.Errorf("dial np-chain %s: %w", n.address, err)
	}

	// 编码 NP-Chain 握手帧（携带会话元数据和 hop chain）
	// 将 Target 重新放回 HopChain 头部，保持完整链路
	encodeMeta := common.SessionMeta{
		ID:       meta.ID,
		HopChain: append([]string{meta.Target}, meta.HopChain...),
	}
	frame, err := npchain.Encode(encodeMeta, nil)
	if err != nil {
		conn.Close()
		observability.OutboundDialsTotal.WithLabelValues(n.name, "failure").Inc()
		return nil, fmt.Errorf("encode np-chain header: %w", err)
	}

	// 发送帧长度 + 帧数据
	lenBuf := []byte{byte(len(frame) >> 8), byte(len(frame) & 0xff)}
	if _, err := conn.Write(lenBuf); err != nil {
		conn.Close()
		observability.OutboundDialsTotal.WithLabelValues(n.name, "failure").Inc()
		return nil, fmt.Errorf("write frame length: %w", err)
	}
	if _, err := conn.Write(frame); err != nil {
		conn.Close()
		observability.OutboundDialsTotal.WithLabelValues(n.name, "failure").Inc()
		return nil, fmt.Errorf("write np-chain frame: %w", err)
	}

	duration := time.Since(start)
	observability.OutboundDialsTotal.WithLabelValues(n.name, "success").Inc()
	observability.OutboundDialDurationMs.WithLabelValues(n.name).Observe(float64(duration.Milliseconds()))

	n.logger.Debug("np-chain dial successful",
		zap.String("outbound", n.name),
		zap.String("address", n.address),
		zap.Duration("duration", duration),
	)

	return conn, nil
}

// HealthCheck 健康检查
func (n *NPChainOutbound) HealthCheck(ctx context.Context) float64 {
	start := time.Now()

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", n.address)
	if err != nil {
		return 0.0
	}
	conn.Close()

	latency := time.Since(start)
	score := 1.0 - float64(latency.Milliseconds())/1000.0
	if score < 0 {
		score = 0
	}
	return score
}
