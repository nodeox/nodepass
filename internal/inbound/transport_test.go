package inbound

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/nodeox/nodepass/internal/common"
	"github.com/nodeox/nodepass/internal/protocol/npchain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// sendNPChainFrameToConn 发送 NP-Chain 帧到普通连接
func sendNPChainFrameToConn(conn net.Conn, meta common.SessionMeta) error {
	frame, err := npchain.Encode(meta, nil)
	if err != nil {
		return err
	}
	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(frame)))
	if _, err := conn.Write(lenBuf); err != nil {
		return err
	}
	_, err = conn.Write(frame)
	return err
}

func TestTCPInbound_Relay(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// echo 服务器
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer echoLn.Close()

	go func() {
		for {
			conn, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()

	// TCP 入站
	tcpIn, err := NewTCP(common.InboundConfig{
		Protocol: "tcp",
		Listen:   "127.0.0.1:0",
	}, logger)
	require.NoError(t, err)

	router := &mockRouterForRelay{
		outbound: &mockDirectOutbound{name: "direct"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tcpIn.Start(ctx, router)
	tcpIn.WaitReady()

	// 连接并发送 NP-Chain 帧
	conn, err := net.DialTimeout("tcp", tcpIn.Addr().String(), 5*time.Second)
	require.NoError(t, err)

	meta := common.SessionMeta{
		ID:       "550e8400-e29b-41d4-a716-446655440000",
		HopChain: []string{echoLn.Addr().String()},
	}
	err = sendNPChainFrameToConn(conn, meta)
	require.NoError(t, err)

	// 验证 echo
	testData := []byte("hello tcp inbound!")
	_, err = conn.Write(testData)
	require.NoError(t, err)

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(testData))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, testData, buf)

	conn.Close()
	tcpIn.Stop()
}

func TestNewTCP(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	tcpIn, err := NewTCP(common.InboundConfig{
		Protocol: "tcp",
		Listen:   "127.0.0.1:0",
	}, logger)
	require.NoError(t, err)
	assert.NotNil(t, tcpIn)
}

func TestWSInbound_Relay(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// echo 服务器
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer echoLn.Close()

	go func() {
		for {
			conn, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()

	// WS 入站
	wsIn, err := NewWS(common.InboundConfig{
		Protocol: "ws",
		Listen:   "127.0.0.1:0",
	}, logger)
	require.NoError(t, err)

	router := &mockRouterForRelay{
		outbound: &mockDirectOutbound{name: "direct"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go wsIn.Start(ctx, router)
	wsIn.WaitReady()

	// WebSocket 客户端连接
	conn, _, _, err := ws.Dial(ctx, "ws://"+wsIn.Addr().String())
	require.NoError(t, err)

	// 构造 NP-Chain 帧并通过 WebSocket 发送
	meta := common.SessionMeta{
		ID:       "550e8400-e29b-41d4-a716-446655440000",
		HopChain: []string{echoLn.Addr().String()},
	}
	frame, err := npchain.Encode(meta, nil)
	require.NoError(t, err)

	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(frame)))

	// 帧长度 + 帧数据合并发送
	payload := append(lenBuf, frame...)
	err = wsutil.WriteClientBinary(conn, payload)
	require.NoError(t, err)

	// 发送测试数据
	testData := []byte("hello ws inbound!")
	err = wsutil.WriteClientBinary(conn, testData)
	require.NoError(t, err)

	// 读取 echo 响应
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	data, err := wsutil.ReadServerBinary(conn)
	require.NoError(t, err)
	assert.Equal(t, testData, data)

	conn.Close()
	wsIn.Stop()
}

func TestNewWS(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	wsIn, err := NewWS(common.InboundConfig{
		Protocol: "ws",
		Listen:   "127.0.0.1:0",
	}, logger)
	require.NoError(t, err)
	assert.NotNil(t, wsIn)
}

func TestNewTLS_MissingCert(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	_, err := NewTLS(common.InboundConfig{
		Protocol: "tls",
		Listen:   "127.0.0.1:0",
	}, logger)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires cert and key")
}
