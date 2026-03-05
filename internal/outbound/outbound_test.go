package outbound

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/nodeox/nodepass/internal/common"
	"go.uber.org/zap"
)

func TestNewDirect(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := common.OutboundConfig{
		Name:     "direct",
		Protocol: "direct",
		Group:    "default",
	}

	out, err := NewDirect(cfg, logger)
	require.NoError(t, err)
	assert.NotNil(t, out)
	assert.Equal(t, "direct", out.Name())
	assert.Equal(t, "default", out.Group())
}

func TestDirectHealthCheck(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := common.OutboundConfig{
		Name:     "direct",
		Protocol: "direct",
	}

	out, err := NewDirect(cfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	score := out.HealthCheck(ctx)
	assert.Equal(t, 1.0, score)
}

func TestDirectDial(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// 启动一个测试服务器
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	go func() {
		conn, _ := ln.Accept()
		if conn != nil {
			conn.Close()
		}
	}()

	cfg := common.OutboundConfig{
		Name:     "direct",
		Protocol: "direct",
	}

	out, err := NewDirect(cfg, logger)
	require.NoError(t, err)

	meta := common.SessionMeta{
		ID:     "test-session",
		Target: ln.Addr().String(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := out.Dial(ctx, meta)
	require.NoError(t, err)
	assert.NotNil(t, conn)
	conn.Close()
}

func TestDirectDialTimeout(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := common.OutboundConfig{
		Name:     "direct",
		Protocol: "direct",
	}

	out, err := NewDirect(cfg, logger)
	require.NoError(t, err)

	meta := common.SessionMeta{
		ID:     "test-session",
		Target: "192.0.2.1:9999", // 不可达的地址
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	conn, err := out.Dial(ctx, meta)
	assert.Error(t, err)
	assert.Nil(t, conn)
}

func TestNew(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	tests := []struct {
		name     string
		protocol string
		address  string
		wantErr  bool
	}{
		{"direct", "direct", "", false},
		{"np-chain", "np-chain", "127.0.0.1:9000", false},
		{"unknown", "unknown", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := common.OutboundConfig{
				Name:     tt.name,
				Protocol: tt.protocol,
				Address:  tt.address,
			}

			out, err := New(cfg, logger)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, out)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, out)
			}
		})
	}
}
