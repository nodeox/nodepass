package benchmark

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nodeox/nodepass/internal/mux"
	"go.uber.org/zap"
)

func BenchmarkThroughput(b *testing.B) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	data := make([]byte, 64*1024)

	b.ResetTimer()
	b.SetBytes(int64(len(data)))

	go func() {
		for i := 0; i < b.N; i++ {
			server.Write(data)
		}
	}()

	buf := make([]byte, len(data))
	for i := 0; i < b.N; i++ {
		io.ReadFull(client, buf)
	}
}

func BenchmarkAggregatorWrite(b *testing.B) {
	logger, _ := zap.NewDevelopment()

	// 创建 3 个 pipe 作为传输连接
	conns := make([]net.Conn, 3)
	for i := 0; i < 3; i++ {
		client, server := net.Pipe()
		conns[i] = client
		// 消费 server 端数据防止阻塞
		go func(s net.Conn) {
			buf := make([]byte, 128*1024)
			for {
				if _, err := s.Read(buf); err != nil {
					return
				}
			}
		}(server)
		defer client.Close()
		defer server.Close()
	}

	agg := mux.NewAggregator(conns, logger)
	sid := uuid.New()
	data := make([]byte, 64*1024)

	b.ResetTimer()
	b.SetBytes(int64(len(data)))

	for i := 0; i < b.N; i++ {
		agg.Write(sid, data)
	}
}

func BenchmarkReassemblerProcess(b *testing.B) {
	logger, _ := zap.NewDevelopment()

	// 预先构造帧数据
	conn := &discardConn{}
	agg := mux.NewAggregator([]net.Conn{conn}, logger)
	sid := uuid.New()
	data := make([]byte, 1024)
	agg.Write(sid, data)
	frame := conn.lastWrite

	b.ResetTimer()
	b.SetBytes(int64(len(frame)))

	for i := 0; i < b.N; i++ {
		reasm := mux.NewReassembler(logger)
		reasm.Process(frame)
	}
}

// discardConn 丢弃写入数据但记录最后一次写入
type discardConn struct {
	lastWrite []byte
}

func (d *discardConn) Write(b []byte) (int, error) {
	d.lastWrite = make([]byte, len(b))
	copy(d.lastWrite, b)
	return len(b), nil
}

func (d *discardConn) Read([]byte) (int, error)            { return 0, io.EOF }
func (d *discardConn) Close() error                        { return nil }
func (d *discardConn) LocalAddr() net.Addr                 { return nil }
func (d *discardConn) RemoteAddr() net.Addr                { return nil }
func (d *discardConn) SetDeadline(time.Time) error        { return nil }
func (d *discardConn) SetReadDeadline(time.Time) error    { return nil }
func (d *discardConn) SetWriteDeadline(time.Time) error   { return nil }
