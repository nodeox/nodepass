package routing

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/nodeox/nodepass/internal/common"
	"go.uber.org/zap"
)

// mockOutbound 模拟出站处理器
type mockOutbound struct {
	name  string
	group string
}

func (m *mockOutbound) Name() string                                                { return m.name }
func (m *mockOutbound) Group() string                                               { return m.group }
func (m *mockOutbound) Dial(ctx context.Context, meta common.SessionMeta) (net.Conn, error) { return nil, nil }
func (m *mockOutbound) HealthCheck(ctx context.Context) float64                     { return 1.0 }

func TestNewRouter(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	router := NewRouter(logger)

	assert.NotNil(t, router)
	assert.NotNil(t, router.outbounds)
	assert.NotNil(t, router.groups)
	assert.NotNil(t, router.rules)
}

func TestAddOutbound(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	router := NewRouter(logger)

	out1 := &mockOutbound{name: "proxy1", group: "proxies"}
	out2 := &mockOutbound{name: "proxy2", group: "proxies"}
	out3 := &mockOutbound{name: "direct", group: ""}

	router.AddOutbound(out1)
	router.AddOutbound(out2)
	router.AddOutbound(out3)

	// 验证出站已添加
	assert.Len(t, router.outbounds, 3)
	assert.Len(t, router.groups["proxies"], 2)

	// 验证可以获取出站
	out, ok := router.GetOutbound("proxy1")
	assert.True(t, ok)
	assert.Equal(t, "proxy1", out.Name())
}

func TestRemoveOutbound(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	router := NewRouter(logger)

	out1 := &mockOutbound{name: "proxy1", group: "proxies"}
	out2 := &mockOutbound{name: "proxy2", group: "proxies"}

	router.AddOutbound(out1)
	router.AddOutbound(out2)

	assert.Len(t, router.groups["proxies"], 2)

	// 移除一个出站
	router.RemoveOutbound("proxy1")

	assert.Len(t, router.outbounds, 1)
	assert.Len(t, router.groups["proxies"], 1)

	_, ok := router.GetOutbound("proxy1")
	assert.False(t, ok)
}

func TestRouteDirect(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	router := NewRouter(logger)

	direct := &mockOutbound{name: "direct", group: ""}
	router.AddOutbound(direct)

	rules := []common.RoutingRule{
		{
			Type:     "default",
			Outbound: "direct",
		},
	}
	router.UpdateRules(rules)

	meta := common.SessionMeta{
		Target: "example.com:443",
	}

	out, err := router.Route(meta)
	require.NoError(t, err)
	assert.Equal(t, "direct", out.Name())
}

func TestRouteDomain(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	router := NewRouter(logger)

	proxy := &mockOutbound{name: "proxy", group: ""}
	direct := &mockOutbound{name: "direct", group: ""}
	router.AddOutbound(proxy)
	router.AddOutbound(direct)

	rules := []common.RoutingRule{
		{
			Type:     "domain",
			Pattern:  "google.com",
			Outbound: "proxy",
		},
		{
			Type:     "default",
			Outbound: "direct",
		},
	}
	router.UpdateRules(rules)

	// 匹配 google.com
	meta1 := common.SessionMeta{Target: "google.com:443"}
	out1, err := router.Route(meta1)
	require.NoError(t, err)
	assert.Equal(t, "proxy", out1.Name())

	// 不匹配，使用默认
	meta2 := common.SessionMeta{Target: "example.com:443"}
	out2, err := router.Route(meta2)
	require.NoError(t, err)
	assert.Equal(t, "direct", out2.Name())
}

func TestRouteGroup(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	router := NewRouter(logger)

	proxy1 := &mockOutbound{name: "proxy1", group: "proxies"}
	proxy2 := &mockOutbound{name: "proxy2", group: "proxies"}
	router.AddOutbound(proxy1)
	router.AddOutbound(proxy2)

	rules := []common.RoutingRule{
		{
			Type:          "default",
			OutboundGroup: "proxies",
			Strategy:      "random",
		},
	}
	router.UpdateRules(rules)

	meta := common.SessionMeta{Target: "example.com:443"}
	out, err := router.Route(meta)
	require.NoError(t, err)
	assert.Contains(t, []string{"proxy1", "proxy2"}, out.Name())
}

func TestMatchDomain(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		target  string
		want    bool
	}{
		{"exact match", "google.com", "google.com:443", true},
		{"exact match no port", "google.com", "google.com", true},
		{"wildcard match", "*.google.com", "www.google.com:443", true},
		{"wildcard no match", "*.google.com", "google.com:443", false},
		{"keyword match", "keyword:google", "www.google.com:443", true},
		{"keyword no match", "keyword:google", "www.example.com:443", false},
		{"no match", "google.com", "example.com:443", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchDomain(tt.pattern, tt.target)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatchIP(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		target  string
		want    bool
	}{
		{"exact IPv4", "1.2.3.4", "1.2.3.4:443", true},
		{"exact IPv4 no match", "1.2.3.4", "1.2.3.5:443", false},
		{"CIDR match", "192.168.1.0/24", "192.168.1.100:443", true},
		{"CIDR no match", "192.168.1.0/24", "192.168.2.100:443", false},
		{"not IP", "192.168.1.0/24", "example.com:443", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchIP(tt.pattern, tt.target)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatchUser(t *testing.T) {
	assert.True(t, matchUser("user123", "user123"))
	assert.False(t, matchUser("user123", "user456"))
}

// mockOutboundWithScore 带自定义健康分值的 mock
type mockOutboundWithScore struct {
	name  string
	group string
	score float64
}

func (m *mockOutboundWithScore) Name() string  { return m.name }
func (m *mockOutboundWithScore) Group() string { return m.group }
func (m *mockOutboundWithScore) Dial(ctx context.Context, meta common.SessionMeta) (net.Conn, error) {
	return nil, nil
}
func (m *mockOutboundWithScore) HealthCheck(ctx context.Context) float64 { return m.score }

func TestSelectLeastLoad(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	router := NewRouter(logger)

	out1 := &mockOutboundWithScore{name: "slow", group: "g", score: 0.3}
	out2 := &mockOutboundWithScore{name: "fast", group: "g", score: 0.9}
	out3 := &mockOutboundWithScore{name: "mid", group: "g", score: 0.6}

	router.AddOutbound(out1)
	router.AddOutbound(out2)
	router.AddOutbound(out3)

	router.UpdateRules([]common.RoutingRule{
		{Type: "default", OutboundGroup: "g", Strategy: "least-load"},
	})

	meta := common.SessionMeta{Target: "example.com:443"}
	selected, err := router.Route(meta)
	require.NoError(t, err)
	assert.Equal(t, "fast", selected.Name())
}

func TestSelectLeastLoad_SingleOutbound(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	router := NewRouter(logger)

	out := &mockOutboundWithScore{name: "only", group: "g", score: 0.5}
	router.AddOutbound(out)

	router.UpdateRules([]common.RoutingRule{
		{Type: "default", OutboundGroup: "g", Strategy: "least-load"},
	})

	selected, err := router.Route(common.SessionMeta{Target: "example.com:80"})
	require.NoError(t, err)
	assert.Equal(t, "only", selected.Name())
}
