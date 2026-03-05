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
	listen string
	target string
	logger *zap.Logger

	listener net.Listener
	ready    chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewForward 创建端口转发入站
func NewForward(cfg common.InboundConfig, logger *zap.Logger) (*ForwardInbound, error) {
	if cfg.Target == "" {
		return nil, fmt.Errorf("forward inbound requires target")
	}

	return &ForwardInbound{
		listen: cfg.Listen,
		target: cfg.Target,
		logger: logger.With(zap.String("inbound", "forward")),
		ready:  make(chan struct{}),
	}, nil
}

func (f *ForwardInbound) Start(ctx context.Context, router common.Router) error {
	f.ctx, f.cancel = context.WithCancel(ctx)

	ln, err := net.Listen("tcp", f.listen)
	if err != nil {
		return fmt.Errorf("forward listen failed: %w", err)
	}
	f.listener = ln
	f.logger.Info("forward inbound started",
		zap.String("listen", ln.Addr().String()),
		zap.String("target", f.target),
	)
	close(f.ready)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-f.ctx.Done():
				return nil
			default:
				f.logger.Error("accept failed", zap.Error(err))
				continue
			}
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

	targetConn, err := net.DialTimeout("tcp", f.target, 10*time.Second)
	if err != nil {
		f.logger.Debug("dial target failed",
			zap.String("target", f.target),
			zap.Error(err),
		)
		return
	}
	defer targetConn.Close()

	f.logger.Debug("forward relay established",
		zap.String("source", conn.RemoteAddr().String()),
		zap.String("target", f.target),
	)

	biRelay(conn, targetConn)
}

func (f *ForwardInbound) Stop() error {
	if f.cancel != nil {
		f.cancel()
	}
	if f.listener != nil {
		f.listener.Close()
	}
	f.wg.Wait()
	f.logger.Info("forward inbound stopped")
	return nil
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
