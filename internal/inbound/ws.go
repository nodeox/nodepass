package inbound

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/gobwas/ws"
	"github.com/nodeox/nodepass/internal/common"
	"github.com/nodeox/nodepass/internal/observability"
	"go.uber.org/zap"
)

// WSInbound WebSocket 加密传输层入站
type WSInbound struct {
	listen   string
	logger   *zap.Logger
	listener net.Listener
	router   common.Router
	ready    chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewWS 创建 WebSocket 入站
func NewWS(cfg common.InboundConfig, logger *zap.Logger) (*WSInbound, error) {
	return &WSInbound{
		listen: cfg.Listen,
		logger: logger.With(zap.String("inbound", "ws")),
		ready:  make(chan struct{}),
	}, nil
}

func (w *WSInbound) Start(ctx context.Context, router common.Router) error {
	w.ctx, w.cancel = context.WithCancel(ctx)
	w.router = router

	ln, err := net.Listen("tcp", w.listen)
	if err != nil {
		return fmt.Errorf("ws listen failed: %w", err)
	}
	w.listener = ln
	w.logger.Info("ws inbound started", zap.String("listen", ln.Addr().String()))
	close(w.ready)

	upgrader := ws.Upgrader{}

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-w.ctx.Done():
				return nil
			default:
				w.logger.Error("accept failed", zap.Error(err))
				continue
			}
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
			handleNPChainStream(w.ctx, wsc, w.router, w.logger)
		}(conn)
	}
}

func (w *WSInbound) Stop() error {
	if w.cancel != nil {
		w.cancel()
	}
	if w.listener != nil {
		w.listener.Close()
	}
	w.wg.Wait()
	w.logger.Info("ws inbound stopped")
	return nil
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
