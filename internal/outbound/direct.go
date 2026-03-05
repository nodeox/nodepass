package outbound

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/nodeox/nodepass/internal/common"
	"github.com/nodeox/nodepass/internal/observability"
	"go.uber.org/zap"
)

// DirectOutbound 直连出站处理器
type DirectOutbound struct {
	name   string
	group  string
	logger *zap.Logger
}

// NewDirect 创建直连出站
func NewDirect(cfg common.OutboundConfig, logger *zap.Logger) (*DirectOutbound, error) {
	return &DirectOutbound{
		name:   cfg.Name,
		group:  cfg.Group,
		logger: logger,
	}, nil
}

// Name 返回出站名称
func (d *DirectOutbound) Name() string {
	return d.name
}

// Group 返回出站组
func (d *DirectOutbound) Group() string {
	return d.group
}

// Dial 拨号到目标
func (d *DirectOutbound) Dial(ctx context.Context, meta common.SessionMeta) (net.Conn, error) {
	start := time.Now()

	d.logger.Debug("dialing target",
		zap.String("outbound", d.name),
		zap.String("target", meta.Target),
		zap.String("session_id", meta.ID),
	)

	// 使用 context 的超时设置
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", meta.Target)
	if err != nil {
		observability.OutboundDialsTotal.WithLabelValues(d.name, "failure").Inc()
		return nil, fmt.Errorf("dial failed: %w", err)
	}

	// 记录拨号延迟
	duration := time.Since(start)
	observability.OutboundDialsTotal.WithLabelValues(d.name, "success").Inc()
	observability.OutboundDialDurationMs.WithLabelValues(d.name).Observe(float64(duration.Milliseconds()))

	d.logger.Debug("dial successful",
		zap.String("outbound", d.name),
		zap.String("target", meta.Target),
		zap.Duration("duration", duration),
	)

	return conn, nil
}

// HealthCheck 健康检查
func (d *DirectOutbound) HealthCheck(ctx context.Context) float64 {
	// 直连出站总是健康的
	return 1.0
}
