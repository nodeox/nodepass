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

// startEchoServer 启动一个 echo 服务器，返回监听地址
func startEchoServer(t *testing.T) net.Listener {
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
				io.Copy(c, c)
			}(conn)
		}
	}()

	t.Cleanup(func() { ln.Close() })
	return ln
}

// setupNPChainRelay 创建并启动 NP-Chain relay，返回 relay 地址
func setupNPChainRelay(t *testing.T, logger *zap.Logger) string {
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

// npchainConnect 通过 NP-Chain relay 连接目标
func npchainConnect(relayAddr, targetAddr string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", relayAddr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial relay: %w", err)
	}

	meta := common.SessionMeta{
		ID:       "550e8400-e29b-41d4-a716-446655440000",
		HopChain: []string{targetAddr},
	}

	frame, err := npchain.Encode(meta, nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("encode frame: %w", err)
	}

	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(frame)))
	if _, err := conn.Write(lenBuf); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write length: %w", err)
	}
	if _, err := conn.Write(frame); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write frame: %w", err)
	}

	return conn, nil
}

func TestEndToEnd_NPChainEcho(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	echoLn := startEchoServer(t)
	targetAddr := echoLn.Addr().String()
	relayAddr := setupNPChainRelay(t, logger)

	conn, err := npchainConnect(relayAddr, targetAddr)
	require.NoError(t, err)

	// 发送数据并验证 echo
	testData := []byte("hello nodepass e2e test!")
	_, err = conn.Write(testData)
	require.NoError(t, err)

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(testData))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)

	assert.Equal(t, testData, buf)

	conn.Close()
}

func TestEndToEnd_NPChainMultipleConnections(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	echoLn := startEchoServer(t)
	targetAddr := echoLn.Addr().String()
	relayAddr := setupNPChainRelay(t, logger)

	const numConns = 5
	done := make(chan error, numConns)

	for i := 0; i < numConns; i++ {
		go func(id int) {
			conn, err := npchainConnect(relayAddr, targetAddr)
			if err != nil {
				done <- err
				return
			}

			msg := fmt.Sprintf("message from conn %d", id)
			conn.Write([]byte(msg))

			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, len(msg))
			_, err = io.ReadFull(conn, buf)
			if err != nil {
				conn.Close()
				done <- err
				return
			}

			conn.Close()

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
