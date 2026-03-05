package inbound

import (
	"net"
	"sync"
)

// connTracker tracks active connections for graceful shutdown.
// When Stop() is called, CloseAll() force-closes all tracked connections,
// unblocking any io.Copy calls in biRelay.
type connTracker struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func newConnTracker() *connTracker {
	return &connTracker{
		conns: make(map[net.Conn]struct{}),
	}
}

// Add registers a connection and returns a remove function.
func (ct *connTracker) Add(c net.Conn) (remove func()) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.conns[c] = struct{}{}
	return func() {
		ct.mu.Lock()
		defer ct.mu.Unlock()
		delete(ct.conns, c)
	}
}

// CloseAll force-closes all tracked connections. Idempotent.
func (ct *connTracker) CloseAll() {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	for c := range ct.conns {
		c.Close()
	}
	ct.conns = make(map[net.Conn]struct{})
}
