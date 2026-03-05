package transport

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
)

// TCPPool TCP 连接池
type TCPPool struct {
	address string
	logger  *zap.Logger

	mu    sync.Mutex
	conns []net.Conn

	maxConns int
	maxIdle  int
	closed   bool
}

// NewTCPPool 创建 TCP 连接池
func NewTCPPool(address string, logger *zap.Logger) *TCPPool {
	return &TCPPool{
		address:  address,
		logger:   logger,
		conns:    make([]net.Conn, 0),
		maxConns: 100,
		maxIdle:  10,
	}
}

// Get 获取连接（优先复用空闲连接）
func (p *TCPPool) Get(ctx context.Context) (net.Conn, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("pool closed")
	}

	// 尝试从池中获取
	for len(p.conns) > 0 {
		conn := p.conns[len(p.conns)-1]
		p.conns = p.conns[:len(p.conns)-1]
		p.mu.Unlock()

		// 检查连接是否有效
		if err := p.checkConn(conn); err == nil {
			return conn, nil
		}
		conn.Close()

		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, fmt.Errorf("pool closed")
		}
	}
	p.mu.Unlock()

	// 创建新连接
	return p.dial(ctx)
}

// Put 归还连接到池中
func (p *TCPPool) Put(conn net.Conn) {
	if conn == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed || len(p.conns) >= p.maxIdle {
		conn.Close()
		return
	}

	p.conns = append(p.conns, conn)
}

// dial 创建新的 TCP 连接
func (p *TCPPool) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", p.address)
	if err != nil {
		return nil, fmt.Errorf("dial TCP %s: %w", p.address, err)
	}

	p.logger.Debug("TCP connection established", zap.String("address", p.address))
	return conn, nil
}

// checkConn 检查连接是否有效（使用短超时读取探测）
func (p *TCPPool) checkConn(conn net.Conn) error {
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	defer conn.SetReadDeadline(time.Time{})

	one := make([]byte, 1)
	_, err := conn.Read(one)

	if err == nil {
		// 不应该读到数据（说明对端发了意外数据）
		return fmt.Errorf("unexpected data on idle connection")
	}

	// 超时是正常的（说明连接仍然有效）
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return nil
	}

	return err
}

// Close 关闭连接池及所有连接
func (p *TCPPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true

	for _, conn := range p.conns {
		conn.Close()
	}

	p.conns = nil
	p.logger.Debug("TCP pool closed", zap.String("address", p.address))
	return nil
}

// Len 返回当前空闲连接数
func (p *TCPPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}
