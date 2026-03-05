package transport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestNewQUICPool(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	pool := NewQUICPool("example.com:443", nil, logger)
	assert.NotNil(t, pool)
	assert.Equal(t, "example.com:443", pool.address)
	assert.Equal(t, 10, pool.maxConns)
	assert.Equal(t, 0, pool.Len())
}

func TestQUICPool_Close(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	pool := NewQUICPool("example.com:443", nil, logger)

	err := pool.Close()
	assert.NoError(t, err)

	// 关闭后 Get 应失败
	_, err = pool.Get(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pool closed")
}

// 注意：完整的 QUIC 集成测试需要真实的 QUIC 服务端，
// 这里仅测试连接池的管理逻辑。集成测试放在 test/e2e 中。
