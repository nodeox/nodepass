package e2e

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/nodeox/nodepass/internal/common"
	"github.com/nodeox/nodepass/internal/inbound"
	"github.com/nodeox/nodepass/internal/outbound"
	"github.com/nodeox/nodepass/internal/protocol/npchain"
	"github.com/nodeox/nodepass/internal/routing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// setupEgress 创建 egress 节点：NP-Chain 入站 + Direct 出站
func setupEgress(t *testing.T, logger *zap.Logger) string {
	t.Helper()

	router := routing.NewRouter(logger)
	directOut, err := outbound.NewDirect(common.OutboundConfig{
		Name: "direct", Protocol: "direct",
	}, logger)
	require.NoError(t, err)
	router.AddOutbound(directOut)
	router.UpdateRules([]common.RoutingRule{
		{Type: "default", Outbound: "direct"},
	})

	npIn, err := inbound.NewNPChain(common.InboundConfig{
		Protocol: "np-chain", Listen: "127.0.0.1:0",
	}, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go npIn.Start(ctx, router)
	npIn.WaitReady()

	t.Cleanup(func() {
		cancel()
		npIn.Stop()
	})

	return npIn.Addr().String()
}

// setupRelay 创建 relay 节点：NP-Chain 入站 + NP-Chain 出站（指向 egress）
func setupRelay(t *testing.T, logger *zap.Logger, egressAddr string) string {
	t.Helper()

	router := routing.NewRouter(logger)
	npOut, err := outbound.NewNPChain(common.OutboundConfig{
		Name:     "to-egress",
		Protocol: "np-chain",
		Address:  egressAddr,
	}, logger)
	require.NoError(t, err)
	router.AddOutbound(npOut)
	router.UpdateRules([]common.RoutingRule{
		{Type: "default", Outbound: "to-egress"},
	})

	npIn, err := inbound.NewNPChain(common.InboundConfig{
		Protocol: "np-chain", Listen: "127.0.0.1:0",
	}, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go npIn.Start(ctx, router)
	npIn.WaitReady()

	t.Cleanup(func() {
		cancel()
		npIn.Stop()
	})

	return npIn.Addr().String()
}

// setupTCPIngress 创建 ingress 节点：TCP 入站 + NP-Chain 出站（指向 relay）
func setupTCPIngress(t *testing.T, logger *zap.Logger, relayAddr string) string {
	t.Helper()

	router := routing.NewRouter(logger)
	npOut, err := outbound.NewNPChain(common.OutboundConfig{
		Name:     "to-relay",
		Protocol: "np-chain",
		Address:  relayAddr,
	}, logger)
	require.NoError(t, err)
	router.AddOutbound(npOut)
	router.UpdateRules([]common.RoutingRule{
		{Type: "default", Outbound: "to-relay"},
	})

	tcpIn, err := inbound.NewTCP(common.InboundConfig{
		Protocol: "tcp", Listen: "127.0.0.1:0",
	}, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go tcpIn.Start(ctx, router)
	tcpIn.WaitReady()

	t.Cleanup(func() {
		cancel()
		tcpIn.Stop()
	})

	return tcpIn.Addr().String()
}

// TestEndToEnd_MultiNodeChain 测试完整链路：client → ingress → relay → egress → echo
func TestEndToEnd_MultiNodeChain(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// 1. echo 服务器
	echoLn := startEchoServer(t)
	echoAddr := echoLn.Addr().String()

	// 2. 搭建链路：egress → relay → ingress
	egressAddr := setupEgress(t, logger)
	relayAddr := setupRelay(t, logger, egressAddr)
	ingressAddr := setupTCPIngress(t, logger, relayAddr)

	// 3. 客户端通过 ingress 发送 NP-Chain 帧，hop chain 包含 echo 地址
	conn, err := net.DialTimeout("tcp", ingressAddr, 5*time.Second)
	require.NoError(t, err)

	meta := common.SessionMeta{
		ID:       "550e8400-e29b-41d4-a716-446655440000",
		HopChain: []string{echoAddr},
	}
	frame, err := npchain.Encode(meta, nil)
	require.NoError(t, err)

	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(frame)))
	_, err = conn.Write(lenBuf)
	require.NoError(t, err)
	_, err = conn.Write(frame)
	require.NoError(t, err)

	// 4. 发送数据并验证 echo
	testData := []byte("hello multi-node chain!")
	_, err = conn.Write(testData)
	require.NoError(t, err)

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(testData))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, testData, buf)

	conn.Close()
}

// TestEndToEnd_MultiNodeChain_MultipleConnections 多连接并发测试
func TestEndToEnd_MultiNodeChain_MultipleConnections(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	echoLn := startEchoServer(t)
	echoAddr := echoLn.Addr().String()

	egressAddr := setupEgress(t, logger)
	relayAddr := setupRelay(t, logger, egressAddr)
	ingressAddr := setupTCPIngress(t, logger, relayAddr)

	const numConns = 10
	done := make(chan error, numConns)

	for i := 0; i < numConns; i++ {
		go func(id int) {
			conn, err := net.DialTimeout("tcp", ingressAddr, 5*time.Second)
			if err != nil {
				done <- fmt.Errorf("conn %d dial: %w", id, err)
				return
			}

			meta := common.SessionMeta{
				ID:       fmt.Sprintf("550e8400-e29b-41d4-a716-44665544%04d", id),
				HopChain: []string{echoAddr},
			}
			frame, err := npchain.Encode(meta, nil)
			if err != nil {
				conn.Close()
				done <- fmt.Errorf("conn %d encode: %w", id, err)
				return
			}

			lenBuf := make([]byte, 2)
			binary.BigEndian.PutUint16(lenBuf, uint16(len(frame)))
			conn.Write(lenBuf)
			conn.Write(frame)

			msg := fmt.Sprintf("chain message %d", id)
			conn.Write([]byte(msg))

			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, len(msg))
			_, err = io.ReadFull(conn, buf)
			conn.Close()

			if err != nil {
				done <- fmt.Errorf("conn %d read: %w", id, err)
				return
			}
			if string(buf) != msg {
				done <- fmt.Errorf("conn %d: expected %q, got %q", id, msg, string(buf))
				return
			}
			done <- nil
		}(i)
	}

	for i := 0; i < numConns; i++ {
		err := <-done
		assert.NoError(t, err)
	}
}

// TestEndToEnd_ForwardChain 端口转发 + 多节点链路
func TestEndToEnd_ForwardChain(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	echoLn := startEchoServer(t)
	echoAddr := echoLn.Addr().String()

	// egress: NP-Chain 入站 + Direct 出站
	egressAddr := setupEgress(t, logger)

	// relay: NP-Chain 入站 + NP-Chain 出站
	relayAddr := setupRelay(t, logger, egressAddr)

	// ingress: forward 入站直接转发到 relay（客户端自己发 NP-Chain 帧）
	fwd, err := inbound.NewForward(common.InboundConfig{
		Protocol: "forward",
		Listen:   "127.0.0.1:0",
		Target:   relayAddr,
	}, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go fwd.Start(ctx, nil)
	fwd.WaitReady()
	t.Cleanup(func() {
		cancel()
		fwd.Stop()
	})

	// 客户端连接 forward 端口，发送 NP-Chain 帧
	conn, err := net.DialTimeout("tcp", fwd.Addr().String(), 5*time.Second)
	require.NoError(t, err)

	meta := common.SessionMeta{
		ID:       "550e8400-e29b-41d4-a716-446655440099",
		HopChain: []string{echoAddr},
	}
	frame, err := npchain.Encode(meta, nil)
	require.NoError(t, err)

	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(frame)))
	conn.Write(lenBuf)
	conn.Write(frame)

	testData := []byte("hello forward chain!")
	conn.Write(testData)

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(testData))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, testData, buf)

	conn.Close()
}
