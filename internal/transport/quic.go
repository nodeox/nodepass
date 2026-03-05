package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"go.uber.org/zap"
)

// QUICPool QUIC 连接池
type QUICPool struct {
	address    string
	tlsConfig  *tls.Config
	quicConfig *quic.Config
	logger     *zap.Logger

	mu       sync.Mutex
	conns    []*quic.Conn
	maxConns int
	closed   bool
}

// NewQUICPool 创建 QUIC 连接池
func NewQUICPool(address string, tlsConfig *tls.Config, logger *zap.Logger) *QUICPool {
	return &QUICPool{
		address:   address,
		tlsConfig: tlsConfig,
		logger:    logger,
		conns:     make([]*quic.Conn, 0),
		maxConns:  10,
		quicConfig: &quic.Config{
			MaxIdleTimeout:  30 * time.Second,
			KeepAlivePeriod: 10 * time.Second,
			EnableDatagrams: true,
		},
	}
}

// Get 获取或创建 QUIC 连接
func (p *QUICPool) Get(ctx context.Context) (*quic.Conn, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("pool closed")
	}

	// 尝试复用健康连接
	for i := 0; i < len(p.conns); {
		conn := p.conns[i]
		if conn.Context().Err() == nil {
			p.mu.Unlock()
			return conn, nil
		}
		// 移除失效连接
		p.conns = append(p.conns[:i], p.conns[i+1:]...)
	}
	p.mu.Unlock()

	// 创建新连接
	return p.dial(ctx)
}

// dial 拨号建立 QUIC 连接
func (p *QUICPool) dial(ctx context.Context) (*quic.Conn, error) {
	p.logger.Debug("dialing QUIC connection", zap.String("address", p.address))

	conn, err := quic.DialAddr(ctx, p.address, p.tlsConfig, p.quicConfig)
	if err != nil {
		return nil, fmt.Errorf("dial QUIC %s: %w", p.address, err)
	}

	p.mu.Lock()
	if !p.closed && len(p.conns) < p.maxConns {
		p.conns = append(p.conns, conn)
	}
	p.mu.Unlock()

	p.logger.Debug("QUIC connection established", zap.String("address", p.address))
	return conn, nil
}

// OpenStream 获取连接并打开新流
func (p *QUICPool) OpenStream(ctx context.Context) (*quic.Stream, error) {
	conn, err := p.Get(ctx)
	if err != nil {
		return nil, err
	}

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}

	return stream, nil
}

// Close 关闭连接池及所有连接
func (p *QUICPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true

	for _, conn := range p.conns {
		conn.CloseWithError(0, "pool closed")
	}

	p.conns = nil
	p.logger.Debug("QUIC pool closed", zap.String("address", p.address))
	return nil
}

// Len 返回当前池中连接数
func (p *QUICPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}

// QUICConn 将 QUIC 流包装为 net.Conn 接口
type QUICConn struct {
	stream *quic.Stream
	conn   *quic.Conn
}

// NewQUICConn 创建 QUIC 连接包装
func NewQUICConn(stream *quic.Stream, conn *quic.Conn) *QUICConn {
	return &QUICConn{
		stream: stream,
		conn:   conn,
	}
}

func (qc *QUICConn) Read(b []byte) (int, error)  { return qc.stream.Read(b) }
func (qc *QUICConn) Write(b []byte) (int, error) { return qc.stream.Write(b) }
func (qc *QUICConn) Close() error                { return qc.stream.Close() }
func (qc *QUICConn) LocalAddr() net.Addr         { return qc.conn.LocalAddr() }
func (qc *QUICConn) RemoteAddr() net.Addr        { return qc.conn.RemoteAddr() }

func (qc *QUICConn) SetDeadline(t time.Time) error      { return qc.stream.SetDeadline(t) }
func (qc *QUICConn) SetReadDeadline(t time.Time) error   { return qc.stream.SetReadDeadline(t) }
func (qc *QUICConn) SetWriteDeadline(t time.Time) error  { return qc.stream.SetWriteDeadline(t) }

// 确保 QUICConn 实现 net.Conn 接口
var _ net.Conn = (*QUICConn)(nil)
