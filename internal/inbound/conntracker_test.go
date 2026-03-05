package inbound

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/nodeox/nodepass/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestConnTracker_AddAndCloseAll(t *testing.T) {
	ct := newConnTracker()

	// Create pipe pairs to simulate connections
	c1a, c1b := net.Pipe()
	c2a, c2b := net.Pipe()
	defer c1b.Close()
	defer c2b.Close()

	remove1 := ct.Add(c1a)
	remove2 := ct.Add(c2a)
	_ = remove1
	_ = remove2

	ct.mu.Lock()
	assert.Len(t, ct.conns, 2)
	ct.mu.Unlock()

	// CloseAll should close both connections
	ct.CloseAll()

	ct.mu.Lock()
	assert.Len(t, ct.conns, 0)
	ct.mu.Unlock()

	// Verify connections are actually closed by trying to write
	_, err := c1a.Write([]byte("test"))
	assert.Error(t, err)
	_, err = c2a.Write([]byte("test"))
	assert.Error(t, err)
}

func TestConnTracker_Remove(t *testing.T) {
	ct := newConnTracker()

	c1a, c1b := net.Pipe()
	defer c1a.Close()
	defer c1b.Close()

	remove := ct.Add(c1a)

	ct.mu.Lock()
	assert.Len(t, ct.conns, 1)
	ct.mu.Unlock()

	remove()

	ct.mu.Lock()
	assert.Len(t, ct.conns, 0)
	ct.mu.Unlock()
}

func TestConnTracker_CloseAllIdempotent(t *testing.T) {
	ct := newConnTracker()

	c1a, c1b := net.Pipe()
	defer c1b.Close()

	ct.Add(c1a)

	// Calling CloseAll multiple times should not panic
	ct.CloseAll()
	ct.CloseAll()
	ct.CloseAll()
}

func TestConnTracker_AddAfterCloseAll(t *testing.T) {
	ct := newConnTracker()

	c1a, c1b := net.Pipe()
	defer c1b.Close()

	ct.Add(c1a)
	ct.CloseAll()

	// Adding after CloseAll should immediately close the new connection
	c2a, c2b := net.Pipe()
	defer c2b.Close()

	remove := ct.Add(c2a)
	remove() // should be a no-op

	// c2a should be closed
	_, err := c2a.Write([]byte("test"))
	assert.Error(t, err, "connection added after CloseAll should be immediately closed")

	// Tracker should still be empty
	ct.mu.Lock()
	assert.Len(t, ct.conns, 0)
	ct.mu.Unlock()
}

func TestConnTracker_ConcurrentAddAndCloseAll(t *testing.T) {
	ct := newConnTracker()

	var wg sync.WaitGroup
	// Concurrently add connections
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c1, c2 := net.Pipe()
			defer c2.Close()
			remove := ct.Add(c1)
			time.Sleep(time.Millisecond)
			remove()
		}()
	}

	// Concurrently close all
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		ct.CloseAll()
	}()

	wg.Wait()
}

// mockRouter is a simple no-op router that always returns an error.
// Used to test Stop() drain behavior — the handler will fail at Route and exit.
type mockRouter struct{}

func (m *mockRouter) Route(meta common.SessionMeta) (common.OutboundHandler, error) {
	// Block forever to simulate a long-running connection
	select {}
}
func (m *mockRouter) AddOutbound(out common.OutboundHandler) {}
func (m *mockRouter) RemoveOutbound(name string)            {}
func (m *mockRouter) UpdateRules(rules []common.RoutingRule) {}

func TestTCPInbound_StopDrainsConnections(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// Create a TCP inbound on a random port
	tcp, err := NewTCP(common.InboundConfig{
		Protocol: "tcp",
		Listen:   "127.0.0.1:0",
	}, logger)
	require.NoError(t, err)

	// Use a router that blocks forever so biRelay never starts
	router := &mockRouter{}

	// Start in background
	go tcp.Start(context.Background(), router)
	tcp.WaitReady()

	addr := tcp.Addr().String()

	// Establish a connection that will block (router blocks forever)
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()

	// Give the handler goroutine time to accept
	time.Sleep(50 * time.Millisecond)

	// Stop should complete within 2s despite the blocking connection
	done := make(chan struct{})
	go func() {
		tcp.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success - Stop() returned
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within 2s - connections were not drained")
	}
}
