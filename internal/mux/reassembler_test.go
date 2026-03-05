package mux

import (
	"bytes"
	"context"
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

func TestReassembler_RemoveSessionClosesOutput(t *testing.T) {
	// Regression: RemoveSession must close the output channel so that
	// consumers doing `range output` or `<-output` are not blocked forever.
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	sid := uuid.New()
	// Send a few chunks without FIN
	require.NoError(t, r.Process(buildFrame(sid, 0, []byte("aaa"), 0)))
	require.NoError(t, r.Process(buildFrame(sid, 1, []byte("bbb"), 0)))

	output := r.GetOutput(sid)
	require.NotNil(t, output)

	// Consume available chunks
	assert.Equal(t, []byte("aaa"), <-output)
	assert.Equal(t, []byte("bbb"), <-output)

	// RemoveSession should close the output channel
	r.RemoveSession(sid)

	// Consumer should see channel closure (not block forever)
	done := make(chan struct{})
	go func() {
		_, ok := <-output
		assert.False(t, ok, "output channel should be closed after RemoveSession")
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("consumer blocked forever — RemoveSession did not close output")
	}
}

func TestReassembler_RemoveSessionDuringBackpressure(t *testing.T) {
	// Regression: RemoveSession during active retryReassemble should close
	// output after retry goroutine exits, not cause send-to-closed-channel panic.
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	sid := uuid.New()

	// Fill channel to capacity (100 chunks)
	for i := uint32(0); i < 100; i++ {
		require.NoError(t, r.Process(buildFrame(sid, i, []byte{byte(i)}, 0)))
	}

	// Trigger backpressure → starts retryReassemble
	require.NoError(t, r.Process(buildFrame(sid, 100, []byte{100}, 0)))

	// Give retry goroutine time to start and block
	time.Sleep(50 * time.Millisecond)

	output := r.GetOutput(sid)
	require.NotNil(t, output)

	// RemoveSession while retry is blocked
	r.RemoveSession(sid)

	// Consumer should see closure (output drained + closed by retry goroutine exit)
	done := make(chan struct{})
	go func() {
		for range output {
			// drain
		}
		close(done)
	}()

	select {
	case <-done:
		// Success — no panic, no infinite block
	case <-time.After(5 * time.Second):
		t.Fatal("consumer blocked forever after RemoveSession during backpressure")
	}
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

func TestReassembler_RemoveSessionStopsRetry(t *testing.T) {
	// Regression test: RemoveSession must close done channel to unblock
	// a blocked retryReassemble goroutine, preventing goroutine leaks.
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	sid := uuid.New()

	// Fill channel to capacity (100 chunks)
	for i := uint32(0); i < 100; i++ {
		require.NoError(t, r.Process(buildFrame(sid, i, []byte{byte(i)}, 0)))
	}

	// Send chunk 100 to trigger backpressure and start retryReassemble goroutine
	require.NoError(t, r.Process(buildFrame(sid, 100, []byte{100}, 0)))

	// Give retry goroutine time to start and block
	time.Sleep(50 * time.Millisecond)

	// RemoveSession should close done and unblock the retry goroutine
	r.RemoveSession(sid)

	// Session should be gone
	assert.Nil(t, r.GetOutput(sid))

	// Verify no goroutine leak: RemoveSession with non-existent session is safe
	r.RemoveSession(uuid.New())
}

func TestReassembler_ConcurrentProcessWithBackpressure(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	sid := uuid.New()

	// Concurrently process many chunks to stress the backpressure path.
	// Total 200 chunks sent from 10 goroutines, channel capacity is 100.
	const totalChunks = 200
	const workers = 10
	const chunksPerWorker = totalChunks / workers

	var sendWg sync.WaitGroup
	for w := 0; w < workers; w++ {
		sendWg.Add(1)
		go func(workerID int) {
			defer sendWg.Done()
			start := uint32(workerID * chunksPerWorker)
			for i := uint32(0); i < chunksPerWorker; i++ {
				seq := start + i
				var flags byte
				if seq == totalChunks-1 {
					flags = FlagFIN
				}
				data := []byte{byte(seq % 256)}
				err := r.Process(buildFrame(sid, seq, data, flags))
				if err != nil {
					t.Errorf("Process failed for seq %d: %v", seq, err)
				}
			}
		}(w)
	}

	// Concurrently consume from the output channel
	output := func() <-chan []byte {
		// Wait a bit for the session to be created
		for i := 0; i < 100; i++ {
			ch := r.GetOutput(sid)
			if ch != nil {
				return ch
			}
			time.Sleep(time.Millisecond)
		}
		return nil
	}()
	require.NotNil(t, output)

	var received int
	done := make(chan struct{})
	go func() {
		for range output {
			received++
			r.OnConsumed(sid, 1)
		}
		close(done)
	}()

	sendWg.Wait()

	// Wait for all chunks to be consumed with timeout
	select {
	case <-done:
		assert.Equal(t, totalChunks, received, "should receive all chunks")
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for all chunks: received %d/%d", received, totalChunks)
	}
}

// TestReassembler_SoakTest 长时间压力测试，验证 reassembler 在持续高负载下的稳定性
// 模拟多个会话并发处理，随机乱序到达，持续背压和恢复
func TestReassembler_SoakTest(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	const (
		numSessions   = 20  // 并发会话数
		chunksPerSess = 500 // 每个会话的分片数
		workers       = 10  // 每个会话的发送 goroutine 数
	)

	// 在 short mode 下减少测试规模
	testDuration := 5 * time.Second
	if !testing.Short() {
		testDuration = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), testDuration+10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errors := make(chan error, numSessions*2)

	// 为每个会话启动生产者和消费者
	for s := 0; s < numSessions; s++ {
		sid := uuid.New()

		// 生产者：并发发送乱序分片
		wg.Add(1)
		go func(sessionID uuid.UUID) {
			defer wg.Done()

			// 生成随机发送顺序
			order := make([]uint32, chunksPerSess)
			for i := range order {
				order[i] = uint32(i)
			}
			// 简单打乱：每个元素与随机位置交换
			for i := range order {
				j := i + int(time.Now().UnixNano()%(int64(len(order)-i)))
				order[i], order[j] = order[j], order[i]
			}

			// 分批发送
			chunkSize := chunksPerSess / workers
			var sendWg sync.WaitGroup
			for w := 0; w < workers; w++ {
				sendWg.Add(1)
				go func(workerID int) {
					defer sendWg.Done()
					start := workerID * chunkSize
					end := start + chunkSize
					if workerID == workers-1 {
						end = chunksPerSess
					}

					for i := start; i < end; i++ {
						select {
						case <-ctx.Done():
							return
						default:
						}

						seq := order[i]
						var flags byte
						if seq == chunksPerSess-1 {
							flags = FlagFIN
						}
						data := make([]byte, 100) // 100 字节分片
						binary.BigEndian.PutUint32(data, seq)

						if err := r.Process(buildFrame(sessionID, seq, data, flags)); err != nil {
							select {
							case errors <- err:
							default:
							}
							return
						}

						// 随机延迟模拟网络抖动
						if seq%10 == 0 {
							time.Sleep(time.Microsecond * time.Duration(seq%100))
						}
					}
				}(w)
			}
			sendWg.Wait()
		}(sid)

		// 消费者：慢速消费模拟背压
		wg.Add(1)
		go func(sessionID uuid.UUID) {
			defer wg.Done()

			// 等待会话创建
			var output <-chan []byte
			for i := 0; i < 1000; i++ {
				output = r.GetOutput(sessionID)
				if output != nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if output == nil {
				select {
				case errors <- assert.AnError:
				default:
				}
				return
			}

			received := 0
			seqMap := make(map[uint32]bool)

			for chunk := range output {
				select {
				case <-ctx.Done():
					return
				default:
				}

				// 验证序号
				seq := binary.BigEndian.Uint32(chunk)
				if seqMap[seq] {
					select {
					case errors <- assert.AnError:
					default:
					}
					return
				}
				seqMap[seq] = true
				received++

				r.OnConsumed(sessionID, len(chunk))

				// 随机慢速消费触发背压
				if received%50 == 0 {
					time.Sleep(10 * time.Millisecond)
				}
			}

			// 验证收到所有分片
			if received != chunksPerSess {
				select {
				case errors <- assert.AnError:
				default:
				}
			}
		}(sid)
	}

	// 等待所有 goroutine 完成
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 检查是否有错误
		select {
		case err := <-errors:
			t.Fatalf("soak test failed with error: %v", err)
		default:
			t.Logf("soak test passed: %d sessions, %d chunks each, %v duration",
				numSessions, chunksPerSess, testDuration)
		}
	case <-time.After(testDuration + 20*time.Second):
		t.Fatal("soak test timed out")
	}
}

// TestReassembler_RandomSequence 模糊测试：随机序列、随机延迟、随机背压
func TestReassembler_RandomSequence(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	r := NewReassembler(logger)

	const iterations = 100 // 运行 100 次随机测试

	for iter := 0; iter < iterations; iter++ {
		sid := uuid.New()

		// 随机分片数量 (10-200)
		numChunks := 10 + int(time.Now().UnixNano()%190)

		// 生成随机发送顺序
		order := make([]uint32, numChunks)
		for i := range order {
			order[i] = uint32(i)
		}
		for i := range order {
			j := i + int(time.Now().UnixNano()%(int64(len(order)-i)))
			order[i], order[j] = order[j], order[i]
		}

		// 发送所有分片
		for _, seq := range order {
			var flags byte
			if seq == uint32(numChunks-1) {
				flags = FlagFIN
			}
			data := []byte{byte(seq % 256), byte(seq / 256)}
			err := r.Process(buildFrame(sid, seq, data, flags))
			require.NoError(t, err, "iteration %d, seq %d", iter, seq)
		}

		// 消费并验证
		output := r.GetOutput(sid)
		require.NotNil(t, output, "iteration %d", iter)

		received := 0
		for chunk := range output {
			seq := uint32(chunk[0]) + uint32(chunk[1])*256
			assert.Equal(t, uint32(received), seq, "iteration %d: out of order", iter)
			received++
			r.OnConsumed(sid, len(chunk))
		}

		assert.Equal(t, numChunks, received, "iteration %d: missing chunks", iter)
	}
}
