package inbound

import (
	"net"
	"sync"
)

// connTracker tracks active connections for graceful shutdown.
// When Stop() is called, CloseAll() force-closes all tracked connections,
// unblocking any io.Copy calls in biRelay.
type connTracker struct {
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
}

func newConnTracker() *connTracker {
	return &connTracker{
		conns: make(map[net.Conn]struct{}),
	}
}

// Add registers a connection and returns a remove function.
// If the tracker is already closed, the connection is immediately closed
// and the returned remove function is a no-op.
func (ct *connTracker) Add(c net.Conn) (remove func()) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if ct.closed {
		// Tracker already shut down; close this late-arriving connection immediately
		c.Close()
		return func() {}
	}

	ct.conns[c] = struct{}{}
	return func() {
		ct.mu.Lock()
		defer ct.mu.Unlock()
		delete(ct.conns, c)
	}
}

// CloseAll force-closes all tracked connections and marks the tracker as closed.
// Any subsequent Add() calls will immediately close the connection. Idempotent.
func (ct *connTracker) CloseAll() {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.closed = true
	for c := range ct.conns {
		c.Close()
	}
	ct.conns = make(map[net.Conn]struct{})
}
