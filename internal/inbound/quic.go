package inbound

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"

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
	ready     chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
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
		ready:     make(chan struct{}),
	}, nil
}

func (q *QUICInbound) Start(ctx context.Context, router common.Router) error {
	q.ctx, q.cancel = context.WithCancel(ctx)
	q.router = router

	ln, err := quic.ListenAddr(q.listen, q.tlsConfig, &quic.Config{
		EnableDatagrams: true,
	})
	if err != nil {
		return fmt.Errorf("quic listen failed: %w", err)
	}
	q.listener = ln
	q.logger.Info("quic inbound started", zap.String("listen", q.listen))
	close(q.ready)

	for {
		conn, err := ln.Accept(q.ctx)
		if err != nil {
			select {
			case <-q.ctx.Done():
				return nil
			default:
				q.logger.Error("accept connection failed", zap.Error(err))
				continue
			}
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
			handleNPChainStream(q.ctx, netConn, q.router, q.logger)
		}(stream)
	}
}

func (q *QUICInbound) Stop() error {
	if q.cancel != nil {
		q.cancel()
	}
	if q.listener != nil {
		q.listener.Close()
	}
	q.wg.Wait()
	q.logger.Info("quic inbound stopped")
	return nil
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
