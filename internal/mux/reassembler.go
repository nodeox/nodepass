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
	chunks   map[uint32][]byte
	nextSeq  uint32
	finSeq   int64 // FIN 分片的 seq，-1 表示未收到
	output   chan []byte
	flowCtrl *WindowFlowController

	mu sync.Mutex
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
		flowCtrl: NewWindowFlowController(16 * 1024 * 1024), // 16MB
	})
	sb := val.(*SessionBuffer)

	// 存储分片
	sb.mu.Lock()
	sb.chunks[seq] = chunk

	// 记录 FIN 序号
	if flags&FlagFIN != 0 {
		sb.finSeq = int64(seq)
	}
	sb.mu.Unlock()

	// 尝试重组
	return r.tryReassemble(sb)
}

// tryReassemble 尝试按序重组分片
func (r *Reassembler) tryReassemble(sb *SessionBuffer) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	for {
		// 检查背压
		if sb.flowCtrl.ShouldPause() {
			r.logger.Debug("flow control paused")
			break
		}

		// 查找下一个序号的分片
		chunk, ok := sb.chunks[sb.nextSeq]
		if !ok {
			break // 等待缺失的分片
		}

		// 发送到输出通道
		select {
		case sb.output <- chunk:
			sb.flowCtrl.OnBuffered(len(chunk))
			currentSeq := sb.nextSeq
			delete(sb.chunks, currentSeq)
			sb.nextSeq++

			// 如果是 FIN 分片，关闭输出通道
			if sb.finSeq >= 0 && int64(currentSeq) == sb.finSeq {
				close(sb.output)
				return nil
			}

		default:
			// 输出通道满，暂停
			return nil
		}
	}

	return nil
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
func (r *Reassembler) RemoveSession(sessionID uuid.UUID) {
	r.sessions.Delete(sessionID)
}
