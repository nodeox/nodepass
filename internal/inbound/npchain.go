package inbound

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/nodeox/nodepass/internal/common"
	"github.com/nodeox/nodepass/internal/observability"
	"go.uber.org/zap"
)

// NPChainInbound NP-Chain 协议入站处理器（Relay 节点）
// 接收 NP-Chain 帧，剥离当前 hop，转发到下一跳或路由到出站
type NPChainInbound struct {
	listen string
	logger *zap.Logger

	listener net.Listener
	router   common.Router
	ready    chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewNPChain 创建 NP-Chain 入站
func NewNPChain(cfg common.InboundConfig, logger *zap.Logger) (*NPChainInbound, error) {
	return &NPChainInbound{
		listen: cfg.Listen,
		logger: logger.With(zap.String("inbound", "np-chain")),
		ready:  make(chan struct{}),
	}, nil
}

// Start 启动 NP-Chain 入站监听
func (n *NPChainInbound) Start(ctx context.Context, router common.Router) error {
	n.ctx, n.cancel = context.WithCancel(ctx)
	n.router = router

	ln, err := net.Listen("tcp", n.listen)
	if err != nil {
		return fmt.Errorf("listen failed: %w", err)
	}
	n.listener = ln

	n.logger.Info("np-chain inbound started", zap.String("listen", ln.Addr().String()))

	close(n.ready)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-n.ctx.Done():
				return nil
			default:
				n.logger.Error("accept failed", zap.Error(err))
				continue
			}
		}

		observability.InboundConnectionsTotal.WithLabelValues("np-chain", n.listen).Inc()

		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			handleNPChainStream(n.ctx, conn, n.router, n.logger)
		}()
	}
}

// Stop 停止入站
func (n *NPChainInbound) Stop() error {
	if n.cancel != nil {
		n.cancel()
	}
	if n.listener != nil {
		n.listener.Close()
	}
	n.wg.Wait()
	n.logger.Info("np-chain inbound stopped", zap.String("listen", n.listen))
	return nil
}

// Addr 获取监听地址
func (n *NPChainInbound) Addr() net.Addr {
	if n.listener != nil {
		return n.listener.Addr()
	}
	return nil
}

// WaitReady 等待 listener 就绪
func (n *NPChainInbound) WaitReady() {
	<-n.ready
}
