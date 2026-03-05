package npchain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nodeox/nodepass/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeBasic(t *testing.T) {
	sid := uuid.New()
	meta := common.SessionMeta{
		ID:       sid.String(),
		HopChain: []string{"192.168.1.1:8080", "10.0.0.1:443"},
	}
	payload := []byte("hello npchain")

	encoded, err := Encode(meta, payload)
	require.NoError(t, err)

	decodedSID, hops, decodedPayload, err := Decode(encoded)
	require.NoError(t, err)

	assert.Equal(t, sid, decodedSID)
	assert.Equal(t, meta.HopChain, hops)
	assert.Equal(t, payload, decodedPayload)
}

func TestEncodeDecodeIPv6(t *testing.T) {
	sid := uuid.New()
	meta := common.SessionMeta{
		ID:       sid.String(),
		HopChain: []string{"[::1]:8080", "[2001:db8::1]:443"},
	}
	payload := []byte("ipv6 test")

	encoded, err := Encode(meta, payload)
	require.NoError(t, err)

	decodedSID, hops, decodedPayload, err := Decode(encoded)
	require.NoError(t, err)

	assert.Equal(t, sid, decodedSID)
	assert.Len(t, hops, 2)
	assert.Equal(t, "[::1]:8080", hops[0])
	assert.Equal(t, "[2001:db8::1]:443", hops[1])
	assert.Equal(t, payload, decodedPayload)
}

func TestEncodeDecodeDomain(t *testing.T) {
	sid := uuid.New()
	meta := common.SessionMeta{
		ID:       sid.String(),
		HopChain: []string{"example.com:443", "relay.node.io:8443"},
	}
	payload := []byte("domain test")

	encoded, err := Encode(meta, payload)
	require.NoError(t, err)

	decodedSID, hops, decodedPayload, err := Decode(encoded)
	require.NoError(t, err)

	assert.Equal(t, sid, decodedSID)
	assert.Equal(t, meta.HopChain, hops)
	assert.Equal(t, payload, decodedPayload)
}

func TestEncodeDecodeNoHops(t *testing.T) {
	sid := uuid.New()
	meta := common.SessionMeta{
		ID:       sid.String(),
		HopChain: nil,
	}
	payload := []byte("no hops")

	encoded, err := Encode(meta, payload)
	require.NoError(t, err)

	decodedSID, hops, decodedPayload, err := Decode(encoded)
	require.NoError(t, err)

	assert.Equal(t, sid, decodedSID)
	assert.Empty(t, hops)
	assert.Equal(t, payload, decodedPayload)
}

func TestEncodeDecodeEmptyPayload(t *testing.T) {
	sid := uuid.New()
	meta := common.SessionMeta{
		ID:       sid.String(),
		HopChain: []string{"10.0.0.1:80"},
	}

	encoded, err := Encode(meta, nil)
	require.NoError(t, err)

	decodedSID, hops, decodedPayload, err := Decode(encoded)
	require.NoError(t, err)

	assert.Equal(t, sid, decodedSID)
	assert.Equal(t, []string{"10.0.0.1:80"}, hops)
	assert.Empty(t, decodedPayload)
}

func TestDecodeInvalidMagic(t *testing.T) {
	data := make([]byte, HeaderSize)
	// 写入错误的 magic
	data[0] = 0xFF
	data[1] = 0xFF
	data[2] = 0xFF
	data[3] = 0xFF

	_, _, _, err := Decode(data)
	assert.ErrorIs(t, err, common.ErrInvalidMagic)
}

func TestDecodeInvalidVersion(t *testing.T) {
	sid := uuid.New()
	meta := common.SessionMeta{
		ID: sid.String(),
	}

	encoded, err := Encode(meta, nil)
	require.NoError(t, err)

	// 篡改版本号
	encoded[4] = 0xFF

	_, _, _, err = Decode(encoded)
	assert.ErrorIs(t, err, common.ErrUnsupportedVersion)
}

func TestDecodeTooShort(t *testing.T) {
	_, _, _, err := Decode([]byte{1, 2, 3})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too short")
}

func TestEncodeInvalidSessionID(t *testing.T) {
	meta := common.SessionMeta{
		ID: "not-a-uuid",
	}

	_, err := Encode(meta, nil)
	assert.Error(t, err)
}

func TestEncodeAddressTooLong(t *testing.T) {
	sid := uuid.New()
	longDomain := "a]very-long-domain-name-that-exceeds-thirty-bytes.example.com:443"
	meta := common.SessionMeta{
		ID:       sid.String(),
		HopChain: []string{longDomain},
	}

	_, err := Encode(meta, nil)
	assert.Error(t, err)
}

func TestHeaderConstants(t *testing.T) {
	assert.Equal(t, uint32(0x4E504332), Magic)
	assert.Equal(t, byte(0x01), Version)
	assert.Equal(t, 24, HeaderSize)
	assert.Equal(t, 34, HopEntrySize)
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	// 完整的 SessionMeta 往返测试
	sid := uuid.New()
	meta := common.SessionMeta{
		ID:        sid.String(),
		Target:    "target.example.com:443",
		UserID:    "user-1",
		CreatedAt: time.Now(),
		HopChain:  []string{"192.168.1.1:8080", "example.com:443", "10.0.0.1:9090"},
	}
	payload := make([]byte, 1024)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	encoded, err := Encode(meta, payload)
	require.NoError(t, err)

	// 验证帧大小
	expectedSize := HeaderSize + 3*HopEntrySize + len(payload)
	assert.Equal(t, expectedSize, len(encoded))

	decodedSID, hops, decodedPayload, err := Decode(encoded)
	require.NoError(t, err)

	assert.Equal(t, sid, decodedSID)
	assert.Equal(t, meta.HopChain, hops)
	assert.Equal(t, payload, decodedPayload)
}
