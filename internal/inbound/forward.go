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

// ForwardInbound 端口转发入站
// 将监听端口的所有流量直接转发到固定目标，无需协议帧
type ForwardInbound struct {
	listen    string
	target    string
	logger    *zap.Logger

	listener  net.Listener
	tracker   *connTracker
	ready     chan struct{}
	readyOnce sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex // Protects listener access during Start/Stop race
}

// NewForward 创建端口转发入站
func NewForward(cfg common.InboundConfig, logger *zap.Logger) (*ForwardInbound, error) {
	if cfg.Target == "" {
		return nil, fmt.Errorf("forward inbound requires target")
	}

	return &ForwardInbound{
		listen:  cfg.Listen,
		target:  cfg.Target,
		logger:  logger.With(zap.String("inbound", "forward")),
		tracker: newConnTracker(),
		ready:   make(chan struct{}),
	}, nil
}

func (f *ForwardInbound) Start(ctx context.Context, router common.Router) error {
	// Ensure cleanup on early return
	var started bool
	defer func() {
		if !started {
			f.readyOnce.Do(func() { close(f.ready) })
		}
	}()

	ln, err := net.Listen("tcp", f.listen)
	if err != nil {
		return fmt.Errorf("forward listen failed: %w", err)
	}

	// CRITICAL: Set both cancel and listener atomically under lock
	f.mu.Lock()
	f.ctx, f.cancel = context.WithCancel(ctx)
	f.listener = ln
	f.mu.Unlock()

	f.logger.Info("forward inbound started",
		zap.String("listen", ln.Addr().String()),
		zap.String("target", f.target),
	)
	f.readyOnce.Do(func() { close(f.ready) })
	started = true

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Check context under lock
			f.mu.Lock()
			ctxDone := f.ctx != nil && f.ctx.Err() != nil
			f.mu.Unlock()

			if ctxDone {
				return nil
			}
			f.logger.Error("accept failed", zap.Error(err))
			continue
		}

		observability.InboundConnectionsTotal.WithLabelValues("forward", f.listen).Inc()

		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.handleConnection(conn)
		}()
	}
}

func (f *ForwardInbound) handleConnection(conn net.Conn) {
	defer conn.Close()

	removeConn := f.tracker.Add(conn)
	defer removeConn()

	targetConn, err := net.DialTimeout("tcp", f.target, 10*time.Second)
	if err != nil {
		f.logger.Debug("dial target failed",
			zap.String("target", f.target),
			zap.Error(err),
		)
		return
	}
	defer targetConn.Close()

	removeTarget := f.tracker.Add(targetConn)
	defer removeTarget()

	f.logger.Debug("forward relay established",
		zap.String("source", conn.RemoteAddr().String()),
		zap.String("target", f.target),
	)

	biRelay(conn, targetConn)
}

func (f *ForwardInbound) Stop() error {
	f.readyOnce.Do(func() { close(f.ready) })

	// Cancel context and close listener atomically under lock
	f.mu.Lock()
	if f.cancel != nil {
		f.cancel()
	}
	if f.listener != nil {
		f.listener.Close()
	}
	f.mu.Unlock()

	f.tracker.CloseAll()
	done := make(chan struct{})
	go func() {
		f.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		f.logger.Info("forward inbound stopped")
		return nil
	case <-time.After(10 * time.Second):
		f.logger.Warn("forward inbound stop timed out after 10s")
		return fmt.Errorf("forward inbound stop timed out: goroutines still running after 10s")
	}
}

func (f *ForwardInbound) Addr() net.Addr {
	if f.listener != nil {
		return f.listener.Addr()
	}
	return nil
}

func (f *ForwardInbound) WaitReady() {
	<-f.ready
}
