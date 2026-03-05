package mux

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockConn 用于测试的 mock 连接，记录写入的数据
type mockConn struct {
	mu   sync.Mutex
	data [][]byte
}

func (m *mockConn) Write(b []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(b))
	copy(cp, b)
	m.data = append(m.data, cp)
	return len(b), nil
}

func (m *mockConn) Read(b []byte) (int, error)         { return 0, io.EOF }
func (m *mockConn) Close() error                       { return nil }
func (m *mockConn) LocalAddr() net.Addr                { return nil }
func (m *mockConn) RemoteAddr() net.Addr               { return nil }
func (m *mockConn) SetDeadline(_ time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(_ time.Time) error   { return nil }
func (m *mockConn) SetWriteDeadline(_ time.Time) error  { return nil }

// getFrames 返回写入的所有帧数据
func (m *mockConn) getFrames() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data
}

// parseChunk 解析一个分片帧，返回 sessionID, seq, flags, data
func parseChunk(t *testing.T, frame []byte) (uuid.UUID, uint32, byte, []byte) {
	t.Helper()
	require.GreaterOrEqual(t, len(frame), HeaderSize)

	r := bytes.NewReader(frame)

	// Session ID
	sidBytes := make([]byte, 16)
	_, err := io.ReadFull(r, sidBytes)
	require.NoError(t, err)
	sid, err := uuid.FromBytes(sidBytes)
	require.NoError(t, err)

	// Sequence Number
	var seq uint32
	err = binary.Read(r, binary.BigEndian, &seq)
	require.NoError(t, err)

	// Chunk Size
	var chunkSize uint16
	err = binary.Read(r, binary.BigEndian, &chunkSize)
	require.NoError(t, err)

	// Flags
	flags, err := r.ReadByte()
	require.NoError(t, err)

	// Reserved
	_, err = r.ReadByte()
	require.NoError(t, err)

	// Data
	data := make([]byte, chunkSize)
	_, err = io.ReadFull(r, data)
	require.NoError(t, err)

	return sid, seq, flags, data
}

func TestAggregator_SingleChunk(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	conn := &mockConn{}
	agg := NewAggregator([]net.Conn{conn}, logger)

	sid := uuid.New()
	data := []byte("hello world")

	err := agg.Write(sid, data)
	require.NoError(t, err)

	frames := conn.getFrames()
	require.Len(t, frames, 1)

	parsedSID, seq, flags, chunk := parseChunk(t, frames[0])
	assert.Equal(t, sid, parsedSID)
	assert.Equal(t, uint32(0), seq)
	assert.Equal(t, FlagFIN, flags)
	assert.Equal(t, data, chunk)
}

func TestAggregator_MultipleChunks(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	conn := &mockConn{}
	agg := NewAggregator([]net.Conn{conn}, logger)
	agg.SetChunkSize(5) // 每片 5 字节

	sid := uuid.New()
	data := []byte("hello world!") // 12 字节 -> 3 片 (5+5+2)

	err := agg.Write(sid, data)
	require.NoError(t, err)

	frames := conn.getFrames()
	require.Len(t, frames, 3)

	// 第 1 片
	_, seq, flags, chunk := parseChunk(t, frames[0])
	assert.Equal(t, uint32(0), seq)
	assert.Equal(t, byte(0), flags) // 不是最后一片
	assert.Equal(t, []byte("hello"), chunk)

	// 第 2 片
	_, seq, flags, chunk = parseChunk(t, frames[1])
	assert.Equal(t, uint32(1), seq)
	assert.Equal(t, byte(0), flags)
	assert.Equal(t, []byte(" worl"), chunk)

	// 第 3 片 (FIN)
	_, seq, flags, chunk = parseChunk(t, frames[2])
	assert.Equal(t, uint32(2), seq)
	assert.Equal(t, FlagFIN, flags)
	assert.Equal(t, []byte("d!"), chunk)
}

func TestAggregator_RoundRobin(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	conn1 := &mockConn{}
	conn2 := &mockConn{}
	conn3 := &mockConn{}

	agg := NewAggregator([]net.Conn{conn1, conn2, conn3}, logger)
	agg.SetChunkSize(4) // 每片 4 字节

	sid := uuid.New()
	data := []byte("abcdefghijkl") // 12 字节 -> 3 片 (4+4+4)

	err := agg.Write(sid, data)
	require.NoError(t, err)

	// 验证轮询分发到 3 个连接（由于 atomic.Add 先加再取模，分配可能不是从 conn1 开始）
	totalFrames := len(conn1.getFrames()) + len(conn2.getFrames()) + len(conn3.getFrames())
	assert.Equal(t, 3, totalFrames)

	// 收集所有帧并按 seq 排序验证
	allFrames := make(map[uint32][]byte)
	for _, c := range []*mockConn{conn1, conn2, conn3} {
		for _, frame := range c.getFrames() {
			_, seq, _, chunk := parseChunk(t, frame)
			allFrames[seq] = chunk
		}
	}

	assert.Equal(t, []byte("abcd"), allFrames[0])
	assert.Equal(t, []byte("efgh"), allFrames[1])
	assert.Equal(t, []byte("ijkl"), allFrames[2])
}

func TestAggregator_NoTransports(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	agg := NewAggregator([]net.Conn{}, logger)

	err := agg.Write(uuid.New(), []byte("data"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no transports")
}

func TestAggregator_EmptyData(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	conn := &mockConn{}
	agg := NewAggregator([]net.Conn{conn}, logger)

	err := agg.Write(uuid.New(), []byte{})
	require.NoError(t, err)

	// 空数据不应产生任何分片
	assert.Len(t, conn.getFrames(), 0)
}

func TestAggregator_Close(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	conn := &mockConn{}
	agg := NewAggregator([]net.Conn{conn}, logger)

	err := agg.Close()
	assert.NoError(t, err)
}

func TestAggregator_SetChunkSize(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	agg := NewAggregator(nil, logger)

	// 有效大小
	agg.SetChunkSize(1024)
	assert.Equal(t, 1024, agg.chunkSize)

	// 无效大小（0 和负数不生效）
	agg.SetChunkSize(0)
	assert.Equal(t, 1024, agg.chunkSize)

	agg.SetChunkSize(-1)
	assert.Equal(t, 1024, agg.chunkSize)

	// 超过 uint16 最大值不生效
	agg.SetChunkSize(70000)
	assert.Equal(t, 1024, agg.chunkSize)
}
