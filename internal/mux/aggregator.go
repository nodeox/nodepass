package mux

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"sync/atomic"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	// HeaderSize 分片头部大小: SessionID(16) + Seq(4) + ChunkSize(2) + Flags(1) + Reserved(1) = 24
	HeaderSize = 24

	// FlagFIN 最后一个分片标志
	FlagFIN byte = 0x01

	// DefaultChunkSize 默认分片大小 64KB
	DefaultChunkSize = 64 * 1024
)

// Aggregator 带宽聚合器，将数据分片并轮询写入多个物理连接
type Aggregator struct {
	transports []net.Conn
	logger     *zap.Logger

	nextConn  atomic.Int32
	chunkSize int
}

// NewAggregator 创建聚合器
func NewAggregator(transports []net.Conn, logger *zap.Logger) *Aggregator {
	return &Aggregator{
		transports: transports,
		logger:     logger,
		chunkSize:  DefaultChunkSize,
	}
}

// SetChunkSize 设置分片大小
func (a *Aggregator) SetChunkSize(size int) {
	if size > 0 && size <= 65535 {
		a.chunkSize = size
	}
}

// Write 写入数据（分片并聚合到多个物理连接）
func (a *Aggregator) Write(sessionID uuid.UUID, data []byte) error {
	if len(a.transports) == 0 {
		return fmt.Errorf("no transports available")
	}

	seq := uint32(0)

	for len(data) > 0 {
		size := len(data)
		if size > a.chunkSize {
			size = a.chunkSize
		}
		chunk := data[:size]
		data = data[size:]

		// 构造分片帧
		buf := new(bytes.Buffer)
		buf.Grow(HeaderSize + size)

		// Session ID (16 bytes)
		buf.Write(sessionID[:])

		// Sequence Number (4 bytes)
		binary.Write(buf, binary.BigEndian, seq)

		// Chunk Size (2 bytes)
		binary.Write(buf, binary.BigEndian, uint16(size))

		// Flags (1 byte)
		flags := byte(0)
		if len(data) == 0 {
			flags |= FlagFIN
		}
		buf.WriteByte(flags)

		// Reserved (1 byte)
		buf.WriteByte(0)

		// Data
		buf.Write(chunk)

		// 轮询选择物理连接
		connIdx := a.nextConn.Add(1) % int32(len(a.transports))
		conn := a.transports[connIdx]

		if _, err := conn.Write(buf.Bytes()); err != nil {
			return fmt.Errorf("write to transport %d: %w", connIdx, err)
		}

		seq++
	}

	return nil
}

// Close 关闭所有传输连接
func (a *Aggregator) Close() error {
	for _, conn := range a.transports {
		conn.Close()
	}
	return nil
}
