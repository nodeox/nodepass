package transport

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestTCPPool_GetAndPut(t *testing.T) {
	// 启动测试服务器
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// 保持连接打开
			go func(c net.Conn) {
				buf := make([]byte, 1024)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	logger, _ := zap.NewDevelopment()
	pool := NewTCPPool(ln.Addr().String(), logger)
	defer pool.Close()

	ctx := context.Background()

	// 获取连接
	conn1, err := pool.Get(ctx)
	require.NoError(t, err)
	assert.NotNil(t, conn1)

	conn2, err := pool.Get(ctx)
	require.NoError(t, err)
	assert.NotNil(t, conn2)

	// 归还连接
	pool.Put(conn1)
	assert.Equal(t, 1, pool.Len())

	pool.Put(conn2)
	assert.Equal(t, 2, pool.Len())

	// 再次获取应复用
	conn3, err := pool.Get(ctx)
	require.NoError(t, err)
	assert.NotNil(t, conn3)
	assert.Equal(t, 1, pool.Len())

	conn3.Close()
}

func TestTCPPool_Close(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()

	logger, _ := zap.NewDevelopment()
	pool := NewTCPPool(ln.Addr().String(), logger)

	ctx := context.Background()
	conn, err := pool.Get(ctx)
	require.NoError(t, err)
	pool.Put(conn)

	// 关闭池
	err = pool.Close()
	require.NoError(t, err)

	// 关闭后获取应失败
	_, err = pool.Get(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pool closed")
}

func TestTCPPool_PutNil(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	pool := NewTCPPool("127.0.0.1:0", logger)
	defer pool.Close()

	// 归还 nil 不应 panic
	pool.Put(nil)
	assert.Equal(t, 0, pool.Len())
}

func TestTCPPool_MaxIdle(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 1024)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	logger, _ := zap.NewDevelopment()
	pool := NewTCPPool(ln.Addr().String(), logger)
	pool.maxIdle = 2
	defer pool.Close()

	ctx := context.Background()

	// 创建 3 个连接
	conns := make([]net.Conn, 3)
	for i := 0; i < 3; i++ {
		conns[i], err = pool.Get(ctx)
		require.NoError(t, err)
	}

	// 归还 3 个，但 maxIdle=2，第 3 个会被关闭
	for _, c := range conns {
		pool.Put(c)
	}

	assert.Equal(t, 2, pool.Len())
}

func TestTCPPool_DialTimeout(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	// 不可达地址
	pool := NewTCPPool("192.0.2.1:9999", logger)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := pool.Get(ctx)
	assert.Error(t, err)
}
