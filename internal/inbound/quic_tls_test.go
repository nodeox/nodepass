package inbound

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nodeox/nodepass/internal/common"
	"github.com/nodeox/nodepass/internal/protocol/npchain"
	"github.com/nodeox/nodepass/internal/transport"
	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// generateTestCert 生成测试用自签名证书，返回 cert 和 key 文件路径
func generateTestCert(t *testing.T) (certPath, keyPath string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	certFile, err := os.Create(certPath)
	require.NoError(t, err)
	pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certFile.Close()

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	keyFile, err := os.Create(keyPath)
	require.NoError(t, err)
	pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	keyFile.Close()

	return certPath, keyPath
}

func TestQUICInbound_Relay(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	certPath, keyPath := generateTestCert(t)

	// echo 服务器
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer echoLn.Close()

	go func() {
		for {
			conn, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()

	// QUIC 入站
	qIn, err := NewQUIC(common.InboundConfig{
		Protocol: "quic",
		Listen:   "127.0.0.1:0",
		TLS: common.TLSConfig{
			Cert: certPath,
			Key:  keyPath,
		},
	}, logger)
	require.NoError(t, err)

	router := &mockRouterForRelay{
		outbound: &mockDirectOutbound{name: "direct"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go qIn.Start(ctx, router)
	qIn.WaitReady()

	// QUIC 客户端连接
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"np-chain"},
	}

	qConn, err := quic.DialAddr(ctx, qIn.Addr().String(), tlsCfg, &quic.Config{})
	require.NoError(t, err)

	stream, err := qConn.OpenStreamSync(ctx)
	require.NoError(t, err)

	// 包装为 net.Conn
	conn := transport.NewQUICConn(stream, qConn)

	// 发送 NP-Chain 帧
	meta := common.SessionMeta{
		ID:       "550e8400-e29b-41d4-a716-446655440000",
		HopChain: []string{echoLn.Addr().String()},
	}
	frame, err := npchain.Encode(meta, nil)
	require.NoError(t, err)

	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(frame)))
	_, err = conn.Write(lenBuf)
	require.NoError(t, err)
	_, err = conn.Write(frame)
	require.NoError(t, err)

	// 发送测试数据
	testData := []byte("hello quic inbound!")
	_, err = conn.Write(testData)
	require.NoError(t, err)

	// 读取 echo 响应
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(testData))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, testData, buf)

	conn.Close()
	qConn.CloseWithError(0, "done")
	qIn.Stop()
}

func TestNewQUIC_MissingCert(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	_, err := NewQUIC(common.InboundConfig{
		Protocol: "quic",
		Listen:   "127.0.0.1:0",
	}, logger)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires cert and key")
}

func TestTLSInbound_Relay(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	certPath, keyPath := generateTestCert(t)

	// echo 服务器
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer echoLn.Close()

	go func() {
		for {
			conn, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()

	// TLS 入站
	tlsIn, err := NewTLS(common.InboundConfig{
		Protocol: "tls",
		Listen:   "127.0.0.1:0",
		TLS: common.TLSConfig{
			Cert: certPath,
			Key:  keyPath,
		},
	}, logger)
	require.NoError(t, err)

	router := &mockRouterForRelay{
		outbound: &mockDirectOutbound{name: "direct"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tlsIn.Start(ctx, router)
	tlsIn.WaitReady()

	// TLS 客户端连接
	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 5 * time.Second},
		"tcp",
		tlsIn.Addr().String(),
		&tls.Config{InsecureSkipVerify: true},
	)
	require.NoError(t, err)

	// 发送 NP-Chain 帧
	meta := common.SessionMeta{
		ID:       "550e8400-e29b-41d4-a716-446655440000",
		HopChain: []string{echoLn.Addr().String()},
	}
	err = sendNPChainFrameToConn(conn, meta)
	require.NoError(t, err)

	// 验证 echo
	testData := []byte("hello tls inbound!")
	_, err = conn.Write(testData)
	require.NoError(t, err)

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(testData))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, testData, buf)

	conn.Close()
	tlsIn.Stop()
}
