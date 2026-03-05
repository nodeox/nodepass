package mux

import (
	"bytes"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// buildFrame 构造一个分片帧
func buildFrame(sid uuid.UUID, seq uint32, data []byte, flags byte) []byte {
	buf := new(bytes.Buffer)
	buf.Write(sid[:])
	binary.Write(buf, binary.BigEndian, seq)
	binary.Write(buf, binary.BigEndian, uint16(len(data)))
	buf.WriteByte(flags)
	buf.WriteByte(0) // reserved
	buf.Write(data)
	return buf.Bytes()
}

func TestReassembler_SingleChunk(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	sid := uuid.New()
	data := []byte("hello world")

	err := r.Process(buildFrame(sid, 0, data, FlagFIN))
	require.NoError(t, err)

	output := r.GetOutput(sid)
	require.NotNil(t, output)

	chunk := <-output
	assert.Equal(t, data, chunk)

	// FIN 之后通道应关闭
	_, ok := <-output
	assert.False(t, ok)
}

func TestReassembler_OrderedChunks(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	sid := uuid.New()

	// 按序发送 3 个分片
	require.NoError(t, r.Process(buildFrame(sid, 0, []byte("aaa"), 0)))
	require.NoError(t, r.Process(buildFrame(sid, 1, []byte("bbb"), 0)))
	require.NoError(t, r.Process(buildFrame(sid, 2, []byte("ccc"), FlagFIN)))

	output := r.GetOutput(sid)
	require.NotNil(t, output)

	assert.Equal(t, []byte("aaa"), <-output)
	assert.Equal(t, []byte("bbb"), <-output)
	assert.Equal(t, []byte("ccc"), <-output)

	_, ok := <-output
	assert.False(t, ok)
}

func TestReassembler_OutOfOrderChunks(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	sid := uuid.New()

	// 乱序发送: seq 2, seq 0, seq 1(FIN)
	require.NoError(t, r.Process(buildFrame(sid, 2, []byte("ccc"), FlagFIN)))

	output := r.GetOutput(sid)
	require.NotNil(t, output)

	// seq 0 还没到，不应有输出
	select {
	case <-output:
		t.Fatal("should not have output yet")
	default:
		// ok
	}

	require.NoError(t, r.Process(buildFrame(sid, 0, []byte("aaa"), 0)))

	// seq 0 到了，应该输出 aaa
	assert.Equal(t, []byte("aaa"), <-output)

	// seq 1 还没到
	select {
	case <-output:
		t.Fatal("should not have output yet")
	default:
		// ok
	}

	require.NoError(t, r.Process(buildFrame(sid, 1, []byte("bbb"), 0)))

	// seq 1 和 seq 2 应该连续输出
	assert.Equal(t, []byte("bbb"), <-output)
	assert.Equal(t, []byte("ccc"), <-output)

	// FIN 后通道关闭
	_, ok := <-output
	assert.False(t, ok)
}

func TestReassembler_MultipleSessions(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	sid1 := uuid.New()
	sid2 := uuid.New()

	require.NoError(t, r.Process(buildFrame(sid1, 0, []byte("s1-a"), FlagFIN)))
	require.NoError(t, r.Process(buildFrame(sid2, 0, []byte("s2-a"), FlagFIN)))

	output1 := r.GetOutput(sid1)
	output2 := r.GetOutput(sid2)
	require.NotNil(t, output1)
	require.NotNil(t, output2)

	assert.Equal(t, []byte("s1-a"), <-output1)
	assert.Equal(t, []byte("s2-a"), <-output2)
}

func TestReassembler_GetOutputNonExistent(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	output := r.GetOutput(uuid.New())
	assert.Nil(t, output)
}

func TestReassembler_OnConsumed(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	sid := uuid.New()

	require.NoError(t, r.Process(buildFrame(sid, 0, []byte("data"), FlagFIN)))

	output := r.GetOutput(sid)
	chunk := <-output
	assert.Equal(t, []byte("data"), chunk)

	// 通知消费
	r.OnConsumed(sid, len(chunk))

	// 对不存在的 session 调用不应 panic
	r.OnConsumed(uuid.New(), 100)
}

func TestReassembler_RemoveSession(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	sid := uuid.New()
	require.NoError(t, r.Process(buildFrame(sid, 0, []byte("data"), FlagFIN)))

	r.RemoveSession(sid)

	output := r.GetOutput(sid)
	assert.Nil(t, output)
}

func TestReassembler_InvalidFrame(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	// 帧数据太短
	err := r.Process([]byte{1, 2, 3})
	assert.Error(t, err)
}

func TestReassembler_AggregatorIntegration(t *testing.T) {
	// 端到端测试：Aggregator 写入 -> Reassembler 读出
	logger, _ := zap.NewDevelopment()

	conn := &mockConn{}
	agg := NewAggregator([]net.Conn{conn}, logger)
	agg.SetChunkSize(10)

	reasm := NewReassembler(logger)

	sid := uuid.New()
	original := []byte("hello world, this is an integration test!")

	// 通过聚合器写入
	err := agg.Write(sid, original)
	require.NoError(t, err)

	// 将所有帧送入重组器
	for _, frame := range conn.getFrames() {
		err := reasm.Process(frame)
		require.NoError(t, err)
	}

	// 从重组器读出并拼接
	output := reasm.GetOutput(sid)
	require.NotNil(t, output)

	var result []byte
	for chunk := range output {
		result = append(result, chunk...)
	}

	assert.Equal(t, original, result)
}

func TestReassembler_BackpressureRecovery(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	sid := uuid.New()

	// The output channel has capacity 100.
	// Fill it with 100 ordered chunks (seq 0..99).
	for i := uint32(0); i < 100; i++ {
		data := []byte{byte(i)}
		err := r.Process(buildFrame(sid, i, data, 0))
		require.NoError(t, err)
	}

	output := r.GetOutput(sid)
	require.NotNil(t, output)

	// Verify channel is full: all 100 chunks should be in the channel
	assert.Equal(t, 100, len(output))

	// Send chunk 100 — this triggers backpressure (channel is full),
	// which should start the retry goroutine
	err := r.Process(buildFrame(sid, 100, []byte{100}, 0))
	require.NoError(t, err)

	// Now consume 50 chunks to free up space
	for i := 0; i < 50; i++ {
		chunk := <-output
		assert.Equal(t, []byte{byte(i)}, chunk)
		r.OnConsumed(sid, len(chunk))
	}

	// Wait a bit for the retry goroutine to push backpressured chunks
	time.Sleep(200 * time.Millisecond)

	// Now send the FIN chunk (seq 101)
	err = r.Process(buildFrame(sid, 101, []byte{101}, FlagFIN))
	require.NoError(t, err)

	// Consume remaining chunks
	var remaining []byte
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for chunk := range output {
			remaining = append(remaining, chunk...)
			r.OnConsumed(sid, len(chunk))
		}
	}()

	// Wait for completion with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("backpressure recovery timed out — chunks were not delivered")
	}

	// Verify we got all remaining chunks (50..101)
	assert.Equal(t, 52, len(remaining), "should have received chunks 50..101")
}
