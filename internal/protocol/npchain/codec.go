package npchain

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/google/uuid"
	"github.com/nodeox/nodepass/internal/common"
)

const (
	// Magic NP-Chain 协议魔数 "NPC2"
	Magic uint32 = 0x4E504332

	// Version 协议版本
	Version byte = 0x01

	// 地址类型
	AddrTypeIPv4   byte = 0x01
	AddrTypeIPv6   byte = 0x02
	AddrTypeDomain byte = 0x03

	// HopEntrySize 每个 Hop 条目固定大小: AddrType(1) + Port(2) + AddrLen(1) + Addr(30) = 34
	HopEntrySize = 34

	// HeaderSize 固定头部大小: Magic(4) + Version(1) + HopCount(1) + Reserved(2) + SessionID(16) = 24
	HeaderSize = 24
)

// Encode 编码 NP-Chain 协议帧
func Encode(meta common.SessionMeta, payload []byte) ([]byte, error) {
	buf := new(bytes.Buffer)
	buf.Grow(HeaderSize + len(meta.HopChain)*HopEntrySize + len(payload))

	// Header
	binary.Write(buf, binary.BigEndian, Magic)
	buf.WriteByte(Version)
	buf.WriteByte(uint8(len(meta.HopChain)))
	binary.Write(buf, binary.BigEndian, uint16(0)) // Reserved

	// Session ID
	sessionID, err := uuid.Parse(meta.ID)
	if err != nil {
		return nil, fmt.Errorf("parse session ID: %w", err)
	}
	buf.Write(sessionID[:])

	// Hop List
	for _, hop := range meta.HopChain {
		if err := encodeHop(buf, hop); err != nil {
			return nil, fmt.Errorf("encode hop %q: %w", hop, err)
		}
	}

	// Payload
	buf.Write(payload)

	return buf.Bytes(), nil
}

// Decode 解码 NP-Chain 协议帧，返回 SessionMeta、HopChain 和 payload
func Decode(data []byte) (sessionID uuid.UUID, hops []string, payload []byte, err error) {
	if len(data) < HeaderSize {
		return uuid.Nil, nil, nil, fmt.Errorf("frame too short: %d bytes", len(data))
	}

	r := bytes.NewReader(data)

	// Magic
	var magic uint32
	if err := binary.Read(r, binary.BigEndian, &magic); err != nil {
		return uuid.Nil, nil, nil, fmt.Errorf("read magic: %w", err)
	}
	if magic != Magic {
		return uuid.Nil, nil, nil, common.ErrInvalidMagic
	}

	// Version
	ver, err := r.ReadByte()
	if err != nil {
		return uuid.Nil, nil, nil, fmt.Errorf("read version: %w", err)
	}
	if ver != Version {
		return uuid.Nil, nil, nil, common.ErrUnsupportedVersion
	}

	// Hop Count
	hopCount, err := r.ReadByte()
	if err != nil {
		return uuid.Nil, nil, nil, fmt.Errorf("read hop count: %w", err)
	}

	// Reserved
	var reserved uint16
	binary.Read(r, binary.BigEndian, &reserved)

	// Session ID
	sidBytes := make([]byte, 16)
	if _, err := io.ReadFull(r, sidBytes); err != nil {
		return uuid.Nil, nil, nil, fmt.Errorf("read session ID: %w", err)
	}
	sessionID, err = uuid.FromBytes(sidBytes)
	if err != nil {
		return uuid.Nil, nil, nil, fmt.Errorf("parse session ID: %w", err)
	}

	// Hop List
	hops = make([]string, 0, hopCount)
	for i := 0; i < int(hopCount); i++ {
		hop, err := decodeHop(r)
		if err != nil {
			return uuid.Nil, nil, nil, fmt.Errorf("decode hop %d: %w", i, err)
		}
		hops = append(hops, hop)
	}

	// Payload (剩余数据)
	payload = make([]byte, r.Len())
	if _, err := io.ReadFull(r, payload); err != nil {
		return uuid.Nil, nil, nil, fmt.Errorf("read payload: %w", err)
	}

	return sessionID, hops, payload, nil
}

// encodeHop 编码单个 Hop 条目（固定 34 字节）
func encodeHop(buf *bytes.Buffer, hop string) error {
	host, portStr, err := net.SplitHostPort(hop)
	if err != nil {
		return fmt.Errorf("invalid hop address: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}

	var addrType byte
	var addrBytes []byte

	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			addrType = AddrTypeIPv4
			addrBytes = ip4
		} else {
			addrType = AddrTypeIPv6
			addrBytes = ip.To16()
		}
	} else {
		addrType = AddrTypeDomain
		addrBytes = []byte(host)
	}

	if len(addrBytes) > 30 {
		return fmt.Errorf("address too long: %d bytes (max 30)", len(addrBytes))
	}

	buf.WriteByte(addrType)
	binary.Write(buf, binary.BigEndian, uint16(port))
	buf.WriteByte(uint8(len(addrBytes)))
	buf.Write(addrBytes)

	// Padding 到 30 字节
	if padding := 30 - len(addrBytes); padding > 0 {
		buf.Write(make([]byte, padding))
	}

	return nil
}

// decodeHop 解码单个 Hop 条目
func decodeHop(r *bytes.Reader) (string, error) {
	addrType, err := r.ReadByte()
	if err != nil {
		return "", fmt.Errorf("read addr type: %w", err)
	}

	var port uint16
	if err := binary.Read(r, binary.BigEndian, &port); err != nil {
		return "", fmt.Errorf("read port: %w", err)
	}

	addrLen, err := r.ReadByte()
	if err != nil {
		return "", fmt.Errorf("read addr len: %w", err)
	}

	addrBytes := make([]byte, addrLen)
	if _, err := io.ReadFull(r, addrBytes); err != nil {
		return "", fmt.Errorf("read addr: %w", err)
	}

	// 跳过 padding
	padding := 30 - int(addrLen)
	if padding > 0 {
		r.Seek(int64(padding), io.SeekCurrent)
	}

	var host string
	switch addrType {
	case AddrTypeIPv4:
		if len(addrBytes) != 4 {
			return "", fmt.Errorf("invalid IPv4 length: %d", len(addrBytes))
		}
		host = net.IP(addrBytes).String()
	case AddrTypeIPv6:
		if len(addrBytes) != 16 {
			return "", fmt.Errorf("invalid IPv6 length: %d", len(addrBytes))
		}
		host = net.IP(addrBytes).String()
	case AddrTypeDomain:
		host = string(addrBytes)
	default:
		return "", fmt.Errorf("unknown addr type: %d", addrType)
	}

	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}
