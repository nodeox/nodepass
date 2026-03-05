package inbound

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

// mockDirectOutbound 直连出站 mock
type mockDirectOutbound struct {
	name string
}

func (m *mockDirectOutbound) Name() string  { return m.name }
func (m *mockDirectOutbound) Group() string { return "" }
func (m *mockDirectOutbound) Dial(ctx context.Context, meta common.SessionMeta) (net.Conn, error) {
	return net.DialTimeout("tcp", meta.Target, 5*time.Second)
}
func (m *mockDirectOutbound) HealthCheck(ctx context.Context) float64 { return 1.0 }

// mockRouterForRelay 用于 relay 测试的路由器
type mockRouterForRelay struct {
	outbound common.OutboundHandler
}

func (m *mockRouterForRelay) Route(meta common.SessionMeta) (common.OutboundHandler, error) {
	return m.outbound, nil
}
func (m *mockRouterForRelay) AddOutbound(out common.OutboundHandler)      {}
func (m *mockRouterForRelay) RemoveOutbound(name string)                  {}
func (m *mockRouterForRelay) UpdateRules(rules []common.RoutingRule)      {}

// sendNPChainFrame 发送 NP-Chain 帧到连接
func sendNPChainFrame(conn net.Conn, meta common.SessionMeta) error {
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

func TestNPChainInbound_Relay(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// 启动 echo 目标服务器
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

	// 创建 NP-Chain 入站
	npIn, err := NewNPChain(common.InboundConfig{
		Protocol: "np-chain",
		Listen:   "127.0.0.1:0",
	}, logger)
	require.NoError(t, err)

	router := &mockRouterForRelay{
		outbound: &mockDirectOutbound{name: "direct"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go npIn.Start(ctx, router)
	npIn.WaitReady()

	relayAddr := npIn.Addr().String()

	// 连接到 relay
	conn, err := net.DialTimeout("tcp", relayAddr, 5*time.Second)
	require.NoError(t, err)

	// 发送 NP-Chain 帧，hop chain 中第一个是 echo 服务器地址
	meta := common.SessionMeta{
		ID:       "550e8400-e29b-41d4-a716-446655440000",
		HopChain: []string{echoLn.Addr().String()},
	}
	err = sendNPChainFrame(conn, meta)
	require.NoError(t, err)

	// 发送数据并验证 echo
	testData := []byte("hello relay!")
	_, err = conn.Write(testData)
	require.NoError(t, err)

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(testData))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, testData, buf)

	conn.Close()
	npIn.Stop()
}

func TestNPChainInbound_InvalidFrame(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	npIn, err := NewNPChain(common.InboundConfig{
		Protocol: "np-chain",
		Listen:   "127.0.0.1:0",
	}, logger)
	require.NoError(t, err)

	router := &mockRouterForRelay{
		outbound: &mockDirectOutbound{name: "direct"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go npIn.Start(ctx, router)
	npIn.WaitReady()

	// 发送无效帧
	conn, err := net.DialTimeout("tcp", npIn.Addr().String(), 5*time.Second)
	require.NoError(t, err)

	// 写入长度但帧数据无效
	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, 30)
	conn.Write(lenBuf)
	conn.Write(make([]byte, 30)) // 全零，magic 不匹配

	// 连接应被关闭
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	assert.Error(t, err) // EOF or timeout

	conn.Close()
	npIn.Stop()
}

func TestNewNPChainInbound(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	npIn, err := NewNPChain(common.InboundConfig{
		Protocol: "np-chain",
		Listen:   "127.0.0.1:0",
	}, logger)
	require.NoError(t, err)
	assert.NotNil(t, npIn)
}
