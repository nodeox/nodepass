package inbound

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/nodeox/nodepass/internal/common"
	"github.com/nodeox/nodepass/internal/observability"
	"go.uber.org/zap"
)

// NPChainInbound NP-Chain 协议入站处理器（Relay 节点）
// 接收 NP-Chain 帧，剥离当前 hop，转发到下一跳或路由到出站
type NPChainInbound struct {
	listen    string
	logger    *zap.Logger

	listener  net.Listener
	router    common.Router
	tracker   *connTracker
	ready     chan struct{}
	readyOnce sync.Once

	ctx    context.Context
	cancel context.CancelFunc
	mu        sync.Mutex // Protects listener access during Start/Stop race
	wg     sync.WaitGroup
}

// NewNPChain 创建 NP-Chain 入站
func NewNPChain(cfg common.InboundConfig, logger *zap.Logger) (*NPChainInbound, error) {
	return &NPChainInbound{
		listen:  cfg.Listen,
		logger:  logger.With(zap.String("inbound", "np-chain")),
		tracker: newConnTracker(),
		ready:   make(chan struct{}),
	}, nil
}

// Start 启动 NP-Chain 入站监听
func (n *NPChainInbound) Start(ctx context.Context, router common.Router) error {
	n.router = router

	// Ensure cleanup on early return
	var started bool
	defer func() {
		if !started {
			n.readyOnce.Do(func() { close(n.ready) })
		}
	}()

	ln, err := net.Listen("tcp", n.listen)
	if err != nil {
		return fmt.Errorf("listen failed: %w", err)
	}

	// CRITICAL: Set both cancel and listener atomically under lock
	n.mu.Lock()
	n.ctx, n.cancel = context.WithCancel(ctx)
	n.listener = ln
	n.mu.Unlock()

	n.logger.Info("np-chain inbound started", zap.String("listen", ln.Addr().String()))

	n.readyOnce.Do(func() { close(n.ready) })
	started = true

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Check context under lock
			n.mu.Lock()
			ctxDone := n.ctx != nil && n.ctx.Err() != nil
			n.mu.Unlock()

			if ctxDone {
				return nil
			}
			n.logger.Error("accept failed", zap.Error(err))
			continue
		}

		observability.InboundConnectionsTotal.WithLabelValues("np-chain", n.listen).Inc()

		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			// Read ctx under lock
			n.mu.Lock()
			ctx := n.ctx
			n.mu.Unlock()
			handleNPChainStream(ctx, conn, n.router, n.logger, n.tracker)
		}()
	}
}

// Stop 停止入站
func (n *NPChainInbound) Stop() error {
	n.readyOnce.Do(func() { close(n.ready) })

	// Cancel context and close listener atomically under lock
	n.mu.Lock()
	if n.cancel != nil {
		n.cancel()
	}
	if n.listener != nil {
		n.listener.Close()
	}
	n.mu.Unlock()

	n.tracker.CloseAll()
	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		n.logger.Info("np-chain inbound stopped", zap.String("listen", n.listen))
		return nil
	case <-time.After(10 * time.Second):
		n.logger.Warn("np-chain inbound stop timed out after 10s")
		return fmt.Errorf("np-chain inbound stop timed out: goroutines still running after 10s")
	}
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
