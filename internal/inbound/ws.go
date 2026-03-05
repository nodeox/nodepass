package inbound

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/nodeox/nodepass/internal/common"
	"github.com/nodeox/nodepass/internal/observability"
	"go.uber.org/zap"
)

// WSInbound WebSocket 加密传输层入站
type WSInbound struct {
	listen    string
	logger    *zap.Logger
	listener  net.Listener
	router    common.Router
	tracker   *connTracker
	ready     chan struct{}
	readyOnce sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex // Protects listener access during Start/Stop race
	wg        sync.WaitGroup
}

// NewWS 创建 WebSocket 入站
func NewWS(cfg common.InboundConfig, logger *zap.Logger) (*WSInbound, error) {
	return &WSInbound{
		listen:  cfg.Listen,
		logger:  logger.With(zap.String("inbound", "ws")),
		tracker: newConnTracker(),
		ready:   make(chan struct{}),
	}, nil
}

func (w *WSInbound) Start(ctx context.Context, router common.Router) error {
	w.router = router

	// Ensure cleanup on early return
	var started bool
	defer func() {
		if !started {
			w.readyOnce.Do(func() { close(w.ready) })
		}
	}()

	ln, err := net.Listen("tcp", w.listen)
	if err != nil {
		return fmt.Errorf("ws listen failed: %w", err)
	}

	// CRITICAL: Set both cancel and listener atomically under lock
	w.mu.Lock()
	w.ctx, w.cancel = context.WithCancel(ctx)
	w.listener = ln
	w.mu.Unlock()

	w.logger.Info("ws inbound started", zap.String("listen", ln.Addr().String()))
	w.readyOnce.Do(func() { close(w.ready) })
	started = true

	upgrader := ws.Upgrader{}

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Check context under lock
			w.mu.Lock()
			ctxDone := w.ctx != nil && w.ctx.Err() != nil
			w.mu.Unlock()

			if ctxDone {
				return nil
			}
			w.logger.Error("accept failed", zap.Error(err))
			continue
		}

		observability.InboundConnectionsTotal.WithLabelValues("ws", w.listen).Inc()

		w.wg.Add(1)
		go func(c net.Conn) {
			defer w.wg.Done()

			// WebSocket 升级
			if _, err := upgrader.Upgrade(c); err != nil {
				w.logger.Debug("ws upgrade failed", zap.Error(err))
				c.Close()
				return
			}

			wsc := newWSConn(c)
			// Read ctx under lock
			w.mu.Lock()
			ctx := w.ctx
			w.mu.Unlock()
			handleNPChainStream(ctx, wsc, w.router, w.logger, w.tracker)
		}(conn)
	}
}

func (w *WSInbound) Stop() error {
	w.readyOnce.Do(func() { close(w.ready) })

	// Cancel context and close listener atomically under lock
	w.mu.Lock()
	if w.cancel != nil {
		w.cancel()
	}
	if w.listener != nil {
		w.listener.Close()
	}
	w.mu.Unlock()

	w.tracker.CloseAll()
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		w.logger.Info("ws inbound stopped")
		return nil
	case <-time.After(10 * time.Second):
		w.logger.Warn("ws inbound stop timed out after 10s")
		return fmt.Errorf("ws inbound stop timed out: goroutines still running after 10s")
	}
}

func (w *WSInbound) Addr() net.Addr {
	if w.listener != nil {
		return w.listener.Addr()
	}
	return nil
}

func (w *WSInbound) WaitReady() {
	<-w.ready
}
