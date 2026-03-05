package outbound

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/nodeox/nodepass/internal/common"
	"github.com/nodeox/nodepass/internal/protocol/npchain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// startNPChainEchoServer 启动一个接收 NP-Chain 帧后 echo 的服务器
func startNPChainEchoServer(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()

				// 读取帧长度
				lenBuf := make([]byte, 2)
				if _, err := io.ReadFull(c, lenBuf); err != nil {
					return
				}
				frameLen := int(binary.BigEndian.Uint16(lenBuf))

				// 读取帧数据
				frame := make([]byte, frameLen)
				if _, err := io.ReadFull(c, frame); err != nil {
					return
				}

				// 解码验证
				_, _, _, err := npchain.Decode(frame)
				if err != nil {
					return
				}

				// Echo 后续数据
				io.Copy(c, c)
			}(conn)
		}
	}()

	t.Cleanup(func() { ln.Close() })
	return ln
}

func TestNPChainOutbound_Dial(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	server := startNPChainEchoServer(t)

	out, err := NewNPChain(common.OutboundConfig{
		Name:    "relay-1",
		Group:   "relay",
		Address: server.Addr().String(),
	}, logger)
	require.NoError(t, err)

	assert.Equal(t, "relay-1", out.Name())
	assert.Equal(t, "relay", out.Group())

	ctx := context.Background()
	conn, err := out.Dial(ctx, common.SessionMeta{
		ID:       "550e8400-e29b-41d4-a716-446655440000",
		Target:   "example.com:443",
		HopChain: []string{"10.0.0.1:8443"},
	})
	require.NoError(t, err)

	// 发送数据并验证 echo
	testData := []byte("hello np-chain")
	_, err = conn.Write(testData)
	require.NoError(t, err)

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(testData))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, testData, buf)

	conn.Close()
}

func TestNPChainOutbound_MissingAddress(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	_, err := NewNPChain(common.OutboundConfig{
		Name: "bad",
	}, logger)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires address")
}

func TestNPChainOutbound_DialFail(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	out, _ := NewNPChain(common.OutboundConfig{
		Name:    "bad",
		Address: "127.0.0.1:1",
	}, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := out.Dial(ctx, common.SessionMeta{
		ID:     "550e8400-e29b-41d4-a716-446655440000",
		Target: "example.com:80",
	})
	assert.Error(t, err)
}

func TestNPChainOutbound_HealthCheck(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	out, _ := NewNPChain(common.OutboundConfig{
		Name:    "test",
		Address: ln.Addr().String(),
	}, logger)

	score := out.HealthCheck(context.Background())
	assert.Greater(t, score, 0.0)
}
