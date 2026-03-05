package inbound

import (
	"fmt"

	"github.com/nodeox/nodepass/internal/common"
	"go.uber.org/zap"
)

// New 创建入站处理器
func New(cfg common.InboundConfig, logger *zap.Logger) (common.InboundHandler, error) {
	switch cfg.Protocol {
	case "tcp":
		return NewTCP(cfg, logger)
	case "tls":
		return NewTLS(cfg, logger)
	case "ws":
		return NewWS(cfg, logger)
	case "quic":
		return NewQUIC(cfg, logger)
	case "np-chain":
		return NewNPChain(cfg, logger)
	case "forward":
		return NewForward(cfg, logger)
	default:
		return nil, fmt.Errorf("unsupported inbound protocol: %s", cfg.Protocol)
	}
}
