package inbound

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/nodeox/nodepass/internal/common"
	"github.com/nodeox/nodepass/internal/observability"
	"github.com/nodeox/nodepass/internal/protocol/npchain"
	"go.uber.org/zap"
)

// handleNPChainStream 处理 NP-Chain 帧流的公共逻辑
// 所有传输层入站（TCP/TLS/WS/QUIC）共用此方法
func handleNPChainStream(ctx context.Context, conn net.Conn, router common.Router, logger *zap.Logger) {
	defer conn.Close()

	// 读取帧长度 (2 bytes big-endian)
	lenBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		logger.Debug("read frame length failed", zap.Error(err))
		return
	}
	frameLen := int(binary.BigEndian.Uint16(lenBuf))

	if frameLen < npchain.HeaderSize {
		logger.Debug("frame too short", zap.Int("length", frameLen))
		return
	}

	// 读取帧数据
	frame := make([]byte, frameLen)
	if _, err := io.ReadFull(conn, frame); err != nil {
		logger.Debug("read frame failed", zap.Error(err))
		return
	}

	// 解码 NP-Chain 帧
	sessionID, hops, _, err := npchain.Decode(frame)
	if err != nil {
		logger.Debug("decode np-chain frame failed", zap.Error(err))
		return
	}

	logger.Debug("session received",
		zap.String("session_id", sessionID.String()),
		zap.Int("hops", len(hops)),
	)

	// 构造会话元数据
	meta := common.SessionMeta{
		ID:       sessionID.String(),
		Source:   conn.RemoteAddr(),
		HopChain: hops,
	}

	if len(hops) > 0 {
		meta.Target = hops[0]
		meta.HopChain = hops[1:]
	}

	// 路由选择出站
	out, err := router.Route(meta)
	if err != nil {
		logger.Debug("route failed",
			zap.String("session_id", sessionID.String()),
			zap.Error(err),
		)
		return
	}

	// 拨号到目标
	targetConn, err := out.Dial(ctx, meta)
	if err != nil {
		logger.Debug("dial failed",
			zap.String("session_id", sessionID.String()),
			zap.String("outbound", out.Name()),
			zap.Error(err),
		)
		return
	}
	defer targetConn.Close()

	logger.Debug("relay established",
		zap.String("session_id", sessionID.String()),
		zap.String("outbound", out.Name()),
	)

	// 双向转发
	biRelay(conn, targetConn)

	logger.Debug("session closed",
		zap.String("session_id", sessionID.String()),
	)
}

// biRelay 双向转发
func biRelay(client, target net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(target, client)
		if tc, ok := target.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		io.Copy(client, target)
		if tc, ok := client.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	wg.Wait()
}

// TCPInbound TCP 传输层入站
type TCPInbound struct {
	listen   string
	logger   *zap.Logger
	listener net.Listener
	router   common.Router
	ready    chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewTCP 创建 TCP 入站
func NewTCP(cfg common.InboundConfig, logger *zap.Logger) (*TCPInbound, error) {
	return &TCPInbound{
		listen: cfg.Listen,
		logger: logger.With(zap.String("inbound", "tcp")),
		ready:  make(chan struct{}),
	}, nil
}

func (t *TCPInbound) Start(ctx context.Context, router common.Router) error {
	t.ctx, t.cancel = context.WithCancel(ctx)
	t.router = router

	ln, err := net.Listen("tcp", t.listen)
	if err != nil {
		return fmt.Errorf("tcp listen failed: %w", err)
	}
	t.listener = ln
	t.logger.Info("tcp inbound started", zap.String("listen", ln.Addr().String()))
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

		observability.InboundConnectionsTotal.WithLabelValues("tcp", t.listen).Inc()

		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			handleNPChainStream(t.ctx, conn, t.router, t.logger)
		}()
	}
}

func (t *TCPInbound) Stop() error {
	if t.cancel != nil {
		t.cancel()
	}
	if t.listener != nil {
		t.listener.Close()
	}
	t.wg.Wait()
	t.logger.Info("tcp inbound stopped")
	return nil
}

func (t *TCPInbound) Addr() net.Addr {
	if t.listener != nil {
		return t.listener.Addr()
	}
	return nil
}

func (t *TCPInbound) WaitReady() {
	<-t.ready
}
