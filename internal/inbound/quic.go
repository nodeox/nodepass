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
	"github.com/nodeox/nodepass/internal/transport"
	"github.com/quic-go/quic-go"
	"go.uber.org/zap"
)

// QUICInbound QUIC 传输层入站
type QUICInbound struct {
	listen    string
	tlsConfig *tls.Config
	logger    *zap.Logger
	listener  *quic.Listener
	router    common.Router
	tracker   *connTracker
	ready     chan struct{}
	readyOnce sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex // Protects listener access during Start/Stop race
	wg        sync.WaitGroup
}

// NewQUIC 创建 QUIC 入站
func NewQUIC(cfg common.InboundConfig, logger *zap.Logger) (*QUICInbound, error) {
	if cfg.TLS.Cert == "" || cfg.TLS.Key == "" {
		return nil, fmt.Errorf("quic inbound requires cert and key")
	}

	cert, err := tls.LoadX509KeyPair(cfg.TLS.Cert, cfg.TLS.Key)
	if err != nil {
		return nil, fmt.Errorf("load tls cert: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"np-chain"},
		MinVersion:   tls.VersionTLS12,
	}

	return &QUICInbound{
		listen:    cfg.Listen,
		tlsConfig: tlsCfg,
		logger:    logger.With(zap.String("inbound", "quic")),
		tracker:   newConnTracker(),
		ready:     make(chan struct{}),
	}, nil
}

func (q *QUICInbound) Start(ctx context.Context, router common.Router) error {
	q.router = router

	// Ensure cleanup on early return
	var started bool
	defer func() {
		if !started {
			q.readyOnce.Do(func() { close(q.ready) })
		}
	}()

	ln, err := quic.ListenAddr(q.listen, q.tlsConfig, &quic.Config{
		EnableDatagrams: true,
	})
	if err != nil {
		return fmt.Errorf("quic listen failed: %w", err)
	}

	// CRITICAL: Set both cancel and listener atomically under lock
	q.mu.Lock()
	q.ctx, q.cancel = context.WithCancel(ctx)
	q.listener = ln
	q.mu.Unlock()

	q.logger.Info("quic inbound started", zap.String("listen", q.listen))
	q.readyOnce.Do(func() { close(q.ready) })
	started = true

	for {
		// Read ctx under lock for Accept
		q.mu.Lock()
		acceptCtx := q.ctx
		q.mu.Unlock()

		conn, err := ln.Accept(acceptCtx)
		if err != nil {
			// Check context under lock
			q.mu.Lock()
			ctxDone := q.ctx != nil && q.ctx.Err() != nil
			q.mu.Unlock()

			if ctxDone {
				return nil
			}
			q.logger.Error("accept connection failed", zap.Error(err))
			continue
		}

		q.wg.Add(1)
		go q.handleConn(conn)
	}
}

func (q *QUICInbound) handleConn(conn *quic.Conn) {
	defer q.wg.Done()

	for {
		stream, err := conn.AcceptStream(q.ctx)
		if err != nil {
			select {
			case <-q.ctx.Done():
				return
			default:
				q.logger.Debug("accept stream failed", zap.Error(err))
				return
			}
		}

		observability.InboundConnectionsTotal.WithLabelValues("quic", q.listen).Inc()

		q.wg.Add(1)
		go func(s *quic.Stream) {
			defer q.wg.Done()
			netConn := transport.NewQUICConn(s, conn)
			handleNPChainStream(q.ctx, netConn, q.router, q.logger, q.tracker)
		}(stream)
	}
}

func (q *QUICInbound) Stop() error {
	q.readyOnce.Do(func() { close(q.ready) })

	// Cancel context and close listener atomically under lock
	q.mu.Lock()
	if q.cancel != nil {
		q.cancel()
	}
	if q.listener != nil {
		q.listener.Close()
	}
	q.mu.Unlock()

	q.tracker.CloseAll()
	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		q.logger.Info("quic inbound stopped")
		return nil
	case <-time.After(10 * time.Second):
		q.logger.Warn("quic inbound stop timed out after 10s")
		return fmt.Errorf("quic inbound stop timed out: goroutines still running after 10s")
	}
}

func (q *QUICInbound) Addr() net.Addr {
	if q.listener != nil {
		return q.listener.Addr()
	}
	return nil
}

func (q *QUICInbound) WaitReady() {
	<-q.ready
}
