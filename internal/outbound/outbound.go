package outbound

import (
	"fmt"

	"github.com/nodeox/nodepass/internal/common"
	"go.uber.org/zap"
)

// New 创建出站处理器
func New(cfg common.OutboundConfig, logger *zap.Logger) (common.OutboundHandler, error) {
	switch cfg.Protocol {
	case "direct":
		return NewDirect(cfg, logger)
	case "np-chain":
		return NewNPChain(cfg, logger)
	default:
		return nil, fmt.Errorf("unsupported outbound protocol: %s", cfg.Protocol)
	}
}
