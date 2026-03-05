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
	readyOnce sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex // Protects listener access during Start/Stop race
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
	t.router = router

	// Ensure cleanup on early return
	var started bool
	defer func() {
		if !started {
			t.readyOnce.Do(func() { close(t.ready) })
		}
	}()

	ln, err := tls.Listen("tcp", t.listen, t.tlsConfig)
	if err != nil {
		return fmt.Errorf("tls listen failed: %w", err)
	}

	// CRITICAL: Set both cancel and listener atomically under lock
	t.mu.Lock()
	t.ctx, t.cancel = context.WithCancel(ctx)
	t.listener = ln
	t.mu.Unlock()

	t.logger.Info("tls inbound started", zap.String("listen", ln.Addr().String()))
	t.readyOnce.Do(func() { close(t.ready) })
	started = true

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Check context under lock
			t.mu.Lock()
			ctxDone := t.ctx != nil && t.ctx.Err() != nil
			t.mu.Unlock()

			if ctxDone {
				return nil
			}
			t.logger.Error("accept failed", zap.Error(err))
			continue
		}

		observability.InboundConnectionsTotal.WithLabelValues("tls", t.listen).Inc()

		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			// Read ctx under lock
			t.mu.Lock()
			ctx := t.ctx
			t.mu.Unlock()
			handleNPChainStream(ctx, conn, t.router, t.logger, t.tracker)
		}()
	}
}

func (t *TLSInbound) Stop() error {
	t.readyOnce.Do(func() { close(t.ready) })

	// Cancel context and close listener atomically under lock
	t.mu.Lock()
	if t.cancel != nil {
		t.cancel()
	}
	if t.listener != nil {
		t.listener.Close()
	}
	t.mu.Unlock()

	t.tracker.CloseAll()
	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		t.logger.Info("tls inbound stopped")
		return nil
	case <-time.After(10 * time.Second):
		t.logger.Warn("tls inbound stop timed out after 10s")
		return fmt.Errorf("tls inbound stop timed out: goroutines still running after 10s")
	}
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
