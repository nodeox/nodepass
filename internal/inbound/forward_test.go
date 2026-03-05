package inbound

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/nodeox/nodepass/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestForwardInbound_Relay(t *testing.T) {
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

	// forward 入站
	fwd, err := NewForward(common.InboundConfig{
		Protocol: "forward",
		Listen:   "127.0.0.1:0",
		Target:   echoLn.Addr().String(),
	}, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go fwd.Start(ctx, nil) // forward 不需要 router
	fwd.WaitReady()

	// 直接连接，无需任何协议帧
	conn, err := net.DialTimeout("tcp", fwd.Addr().String(), 5*time.Second)
	require.NoError(t, err)

	testData := []byte("hello port forward!")
	_, err = conn.Write(testData)
	require.NoError(t, err)

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(testData))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, testData, buf)

	conn.Close()
	fwd.Stop()
}

func TestForwardInbound_MultipleConnections(t *testing.T) {
	logger, _ := zap.NewDevelopment()

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

	fwd, err := NewForward(common.InboundConfig{
		Protocol: "forward",
		Listen:   "127.0.0.1:0",
		Target:   echoLn.Addr().String(),
	}, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go fwd.Start(ctx, nil)
	fwd.WaitReady()

	const numConns = 5
	done := make(chan error, numConns)

	for i := 0; i < numConns; i++ {
		go func(id int) {
			conn, err := net.DialTimeout("tcp", fwd.Addr().String(), 5*time.Second)
			if err != nil {
				done <- err
				return
			}

			msg := []byte("forward test")
			conn.Write(msg)

			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, len(msg))
			_, err = io.ReadFull(conn, buf)
			conn.Close()

			if err != nil {
				done <- err
				return
			}
			if string(buf) != string(msg) {
				done <- fmt.Errorf("mismatch")
				return
			}
			done <- nil
		}(i)
	}

	for i := 0; i < numConns; i++ {
		err := <-done
		assert.NoError(t, err)
	}

	fwd.Stop()
}

func TestNewForward_MissingTarget(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	_, err := NewForward(common.InboundConfig{
		Protocol: "forward",
		Listen:   "127.0.0.1:0",
	}, logger)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires target")
}
