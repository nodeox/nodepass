package mux

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Reassembler 重组器，从多个物理连接接收分片并按序重组
type Reassembler struct {
	sessions sync.Map // map[uuid.UUID]*SessionBuffer
	logger   *zap.Logger
}

// SessionBuffer 会话缓冲区
type SessionBuffer struct {
	chunks  map[uint32][]byte
	nextSeq uint32
	finSeq  int64 // FIN 分片的 seq，-1 表示未收到
	output  chan []byte
	done    chan struct{} // closed when session is terminated, safe to select on
	doneOnce sync.Once   // guards closing done

	outputClosed bool // guards closing output, protected by mu
	flowCtrl     *WindowFlowController

	retrying bool // true = retry goroutine owns sending; protected by mu
	mu       sync.Mutex
}

// NewReassembler 创建重组器
func NewReassembler(logger *zap.Logger) *Reassembler {
	return &Reassembler{
		logger: logger,
	}
}

// Process 处理接收到的分片数据
func (r *Reassembler) Process(data []byte) error {
	buf := bytes.NewReader(data)

	// 解析 Header
	sessionIDBytes := make([]byte, 16)
	if _, err := io.ReadFull(buf, sessionIDBytes); err != nil {
		return fmt.Errorf("read session ID: %w", err)
	}

	var seq uint32
	if err := binary.Read(buf, binary.BigEndian, &seq); err != nil {
		return fmt.Errorf("read sequence: %w", err)
	}

	var chunkSize uint16
	if err := binary.Read(buf, binary.BigEndian, &chunkSize); err != nil {
		return fmt.Errorf("read chunk size: %w", err)
	}

	flags, err := buf.ReadByte()
	if err != nil {
		return fmt.Errorf("read flags: %w", err)
	}

	// Skip Reserved
	if _, err := buf.ReadByte(); err != nil {
		return fmt.Errorf("read reserved: %w", err)
	}

	chunk := make([]byte, chunkSize)
	if _, err := io.ReadFull(buf, chunk); err != nil {
		return fmt.Errorf("read chunk data: %w", err)
	}

	// 获取或创建 Session Buffer
	sid, err := uuid.FromBytes(sessionIDBytes)
	if err != nil {
		return fmt.Errorf("parse session ID: %w", err)
	}

	val, _ := r.sessions.LoadOrStore(sid, &SessionBuffer{
		chunks:   make(map[uint32][]byte),
		finSeq:   -1,
		output:   make(chan []byte, 100),
		done:     make(chan struct{}),
		flowCtrl: NewWindowFlowController(16 * 1024 * 1024), // 16MB
	})
	sb := val.(*SessionBuffer)

	// 存储分片
	sb.mu.Lock()
	select {
	case <-sb.done:
		sb.mu.Unlock()
		return nil
	default:
	}

	sb.chunks[seq] = chunk

	// 记录 FIN 序号
	if flags&FlagFIN != 0 {
		sb.finSeq = int64(seq)
	}
	sb.mu.Unlock()

	// 尝试重组
	return r.tryReassemble(sb)
}

// tryReassemble 尝试按序重组分片。
// 所有发送操作在锁内以非阻塞方式进行。
// 当 retrying 为 true 时，retry goroutine 独占发送权，本方法直接返回。
func (r *Reassembler) tryReassemble(sb *SessionBuffer) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	select {
	case <-sb.done:
		return nil
	default:
	}

	// retry goroutine 正在运行，它独占发送权
	if sb.retrying {
		return nil
	}

	for {
		if sb.flowCtrl.ShouldPause() {
			r.logger.Debug("flow control paused")
			break
		}

		chunk, ok := sb.chunks[sb.nextSeq]
		if !ok {
			break
		}

		// 非阻塞发送
		select {
		case sb.output <- chunk:
			sb.flowCtrl.OnBuffered(len(chunk))
			currentSeq := sb.nextSeq
			delete(sb.chunks, currentSeq)
			sb.nextSeq++

			if sb.finSeq >= 0 && int64(currentSeq) == sb.finSeq {
				if !sb.outputClosed {
					close(sb.output)
					sb.outputClosed = true
				}
				sb.doneOnce.Do(func() { close(sb.done) })
				return nil
			}

		default:
			// channel 满，将发送权交给 retry goroutine
			sb.retrying = true
			go r.retryReassemble(sb)
			return nil
		}
	}

	return nil
}

// retryReassemble 独占发送权，处理背压恢复。
// 当 retrying 为 true 时，tryReassemble 不发送任何东西，
// 所以本 goroutine 是唯一发送者，不存在并发发送问题。
// 阻塞发送时不修改 nextSeq，直到发送成功后才更新状态。
// 退出时负责关闭 output（如果尚未关闭），确保消费者不会永久阻塞。
func (r *Reassembler) retryReassemble(sb *SessionBuffer) {
	// retryExit 统一退出：设 retrying=false，按需关 output
	retryExit := func(locked bool) {
		if !locked {
			sb.mu.Lock()
		}
		sb.retrying = false
		if !sb.outputClosed {
			select {
			case <-sb.done:
				// done 已关闭（RemoveSession 或 FIN），关闭 output 通知消费者
				close(sb.output)
				sb.outputClosed = true
			default:
				// done 未关闭，不关 output（正常放弃发送权路径）
			}
		}
		sb.mu.Unlock()
	}

	for {
		sb.mu.Lock()

		// 检查是否已关闭
		select {
		case <-sb.done:
			retryExit(true)
			return
		default:
		}

		chunk, ok := sb.chunks[sb.nextSeq]
		if !ok {
			// 没有更多有序 chunk，放弃发送权
			retryExit(true)
			return
		}
		seq := sb.nextSeq

		// 先尝试非阻塞发送（锁内）
		select {
		case sb.output <- chunk:
			sb.flowCtrl.OnBuffered(len(chunk))
			delete(sb.chunks, seq)
			sb.nextSeq++

			if sb.finSeq >= 0 && int64(seq) == sb.finSeq {
				if !sb.outputClosed {
					close(sb.output)
					sb.outputClosed = true
				}
				sb.doneOnce.Do(func() { close(sb.done) })
				sb.retrying = false
				sb.mu.Unlock()
				return
			}
			sb.mu.Unlock()
			continue
		default:
			// channel 仍满，需要阻塞等待
		}
		sb.mu.Unlock()

		// 锁外阻塞发送。安全性保证：
		// 1. retrying=true，tryReassemble 不会发送（无并发发送者）
		// 2. nextSeq 未推进，chunk 仍在 map 中（无顺序/丢失问题）
		// 3. select on done 防止向已关闭通道发送
		select {
		case sb.output <- chunk:
			// 发送成功，加锁更新状态
			sb.mu.Lock()
			select {
			case <-sb.done:
				// RemoveSession 或 FIN 已关闭 done，停止
				retryExit(true)
				return
			default:
			}

			sb.flowCtrl.OnBuffered(len(chunk))
			delete(sb.chunks, seq)
			sb.nextSeq++

			if sb.finSeq >= 0 && int64(seq) == sb.finSeq {
				if !sb.outputClosed {
					close(sb.output)
					sb.outputClosed = true
				}
				sb.doneOnce.Do(func() { close(sb.done) })
				sb.retrying = false
				sb.mu.Unlock()
				return
			}
			sb.mu.Unlock()

		case <-sb.done:
			// RemoveSession 已关闭 done，退出并关 output
			retryExit(false)
			return
		}
	}
}

// GetOutput 获取会话的输出通道
func (r *Reassembler) GetOutput(sessionID uuid.UUID) <-chan []byte {
	val, ok := r.sessions.Load(sessionID)
	if !ok {
		return nil
	}

	sb := val.(*SessionBuffer)
	return sb.output
}

// OnConsumed 通知已消费数据
func (r *Reassembler) OnConsumed(sessionID uuid.UUID, n int) {
	val, ok := r.sessions.Load(sessionID)
	if !ok {
		return
	}

	sb := val.(*SessionBuffer)
	sb.flowCtrl.OnConsumed(n)
}

// RemoveSession 移除会话
// 关闭 done channel 以通知所有相关 goroutine 退出。
// 如果没有 retryReassemble 在运行（retrying=false），直接关闭 output 通知消费者。
// 如果 retryReassemble 在运行，由它在退出时关闭 output，避免并发 send-to-closed-channel。
func (r *Reassembler) RemoveSession(sessionID uuid.UUID) {
	val, ok := r.sessions.LoadAndDelete(sessionID)
	if !ok {
		return
	}
	sb := val.(*SessionBuffer)
	sb.mu.Lock()
	sb.doneOnce.Do(func() { close(sb.done) })
	if !sb.retrying && !sb.outputClosed {
		close(sb.output)
		sb.outputClosed = true
	}
	sb.mu.Unlock()
}
