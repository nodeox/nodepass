package inbound

import (
	"io"
	"net"
	"sync"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// wsConn 将 WebSocket 连接包装为 net.Conn
// 读取时解析 WebSocket 帧，写入时封装为 Binary 帧
type wsConn struct {
	net.Conn
	reader io.Reader
	mu     sync.Mutex // 保护写操作
}

func newWSConn(conn net.Conn) *wsConn {
	return &wsConn{Conn: conn}
}

func (c *wsConn) Read(p []byte) (int, error) {
	for {
		if c.reader != nil {
			n, err := c.reader.Read(p)
			if n > 0 {
				return n, nil
			}
			if err != io.EOF {
				return 0, err
			}
			c.reader = nil
		}

		// 读取下一个 WebSocket 帧
		header, err := ws.ReadHeader(c.Conn)
		if err != nil {
			return 0, err
		}

		// 跳过 close/ping/pong 控制帧
		if header.OpCode == ws.OpClose {
			return 0, io.EOF
		}
		if header.OpCode == ws.OpPing {
			// 回复 pong
			ws.WriteHeader(c.Conn, ws.Header{
				Fin:    true,
				OpCode: ws.OpPong,
				Length: header.Length,
			})
			io.CopyN(io.Discard, c.Conn, header.Length)
			continue
		}
		if header.OpCode == ws.OpPong {
			io.CopyN(io.Discard, c.Conn, header.Length)
			continue
		}

		c.reader = &io.LimitedReader{R: c.Conn, N: header.Length}

		// 如果客户端发送了 masked 数据，需要解码
		if header.Masked {
			c.reader = wsutil.NewCipherReader(c.reader, header.Mask)
		}
	}
}

func (c *wsConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	header := ws.Header{
		Fin:    true,
		OpCode: ws.OpBinary,
		Length: int64(len(p)),
	}

	if err := ws.WriteHeader(c.Conn, header); err != nil {
		return 0, err
	}

	return c.Conn.Write(p)
}
