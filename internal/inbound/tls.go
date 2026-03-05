package inbound

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/nodeox/nodepass/internal/common"
	"github.com/nodeox/nodepass/internal/observability"
	"go.uber.org/zap"
)

// TLSInbound TLS 加密传输层入站
type TLSInbound struct {
	listen    string
	tlsConfig *tls.Config
	logger    *zap.Logger
	listener  net.Listener
	router    common.Router
	tracker   *connTracker
	ready     chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// NewTLS 创建 TLS 入站
func NewTLS(cfg common.InboundConfig, logger *zap.Logger) (*TLSInbound, error) {
	if cfg.TLS.Cert == "" || cfg.TLS.Key == "" {
		return nil, fmt.Errorf("tls inbound requires cert and key")
	}

	cert, err := tls.LoadX509KeyPair(cfg.TLS.Cert, cfg.TLS.Key)
	if err != nil {
		return nil, fmt.Errorf("load tls cert: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	return &TLSInbound{
		listen:    cfg.Listen,
		tlsConfig: tlsCfg,
		logger:    logger.With(zap.String("inbound", "tls")),
		tracker:   newConnTracker(),
		ready:     make(chan struct{}),
	}, nil
}

func (t *TLSInbound) Start(ctx context.Context, router common.Router) error {
	t.ctx, t.cancel = context.WithCancel(ctx)
	t.router = router

	ln, err := tls.Listen("tcp", t.listen, t.tlsConfig)
	if err != nil {
		return fmt.Errorf("tls listen failed: %w", err)
	}
	t.listener = ln
	t.logger.Info("tls inbound started", zap.String("listen", ln.Addr().String()))
	close(t.ready)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-t.ctx.Done():
				return nil
			default:
				t.logger.Error("accept failed", zap.Error(err))
				continue
			}
		}

		observability.InboundConnectionsTotal.WithLabelValues("tls", t.listen).Inc()

		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			handleNPChainStream(t.ctx, conn, t.router, t.logger, t.tracker)
		}()
	}
}

func (t *TLSInbound) Stop() error {
	if t.cancel != nil {
		t.cancel()
	}
	if t.listener != nil {
		t.listener.Close()
	}
	t.tracker.CloseAll()
	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.logger.Warn("tls inbound stop timed out after 10s")
	}
	t.logger.Info("tls inbound stopped")
	return nil
}

func (t *TLSInbound) Addr() net.Addr {
	if t.listener != nil {
		return t.listener.Addr()
	}
	return nil
}

func (t *TLSInbound) WaitReady() {
	<-t.ready
}
