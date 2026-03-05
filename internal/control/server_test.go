package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func setupTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	srv := NewServer(logger)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv.StartOnListener(ln)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Stop(ctx)
	})

	return srv, "http://" + ln.Addr().String()
}

func postJSON(t *testing.T, url string, body interface{}) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)

	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	require.NoError(t, err)
	return resp
}

func TestServer_Register(t *testing.T) {
	_, baseURL := setupTestServer(t)

	resp := postJSON(t, baseURL+"/api/v1/register", RegisterRequest{
		NodeID:   "node-1",
		NodeType: "ingress",
		Tags:     map[string]string{"region": "us-west"},
	})
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result RegisterResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.NotEmpty(t, result.Token)
	assert.Equal(t, int64(10), result.HeartbeatInterval)
}

func TestServer_RegisterMissingFields(t *testing.T) {
	_, baseURL := setupTestServer(t)

	resp := postJSON(t, baseURL+"/api/v1/register", RegisterRequest{
		NodeID: "",
	})
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestServer_Heartbeat(t *testing.T) {
	srv, baseURL := setupTestServer(t)

	// 先注册
	regResp, err := srv.Register(&RegisterRequest{
		NodeID:   "node-1",
		NodeType: "egress",
	})
	require.NoError(t, err)

	// 发送心跳
	resp := postJSON(t, baseURL+"/api/v1/heartbeat", HeartbeatRequest{
		NodeID: "node-1",
		Token:  regResp.Token,
		Status: &NodeStatus{
			ActiveSessions: 5,
			CPUUsage:       0.3,
			MemUsage:       0.5,
		},
	})
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result HeartbeatResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.False(t, result.ConfigUpdated)

	// 验证状态已更新
	node, ok := srv.GetNode("node-1")
	require.True(t, ok)
	assert.Equal(t, int64(5), node.Status.ActiveSessions)
}

func TestServer_HeartbeatInvalidToken(t *testing.T) {
	srv, baseURL := setupTestServer(t)

	srv.Register(&RegisterRequest{
		NodeID:   "node-1",
		NodeType: "egress",
	})

	resp := postJSON(t, baseURL+"/api/v1/heartbeat", HeartbeatRequest{
		NodeID: "node-1",
		Token:  "wrong-token",
	})
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestServer_HeartbeatUnknownNode(t *testing.T) {
	_, baseURL := setupTestServer(t)

	resp := postJSON(t, baseURL+"/api/v1/heartbeat", HeartbeatRequest{
		NodeID: "unknown",
		Token:  "any",
	})
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestServer_ReportStats(t *testing.T) {
	srv, baseURL := setupTestServer(t)

	srv.Register(&RegisterRequest{
		NodeID:   "node-1",
		NodeType: "ingress",
	})

	resp := postJSON(t, baseURL+"/api/v1/stats", ReportStatsRequest{
		NodeID: "node-1",
		Sessions: []SessionStats{
			{SessionID: "s1", UserID: "u1", BytesIn: 1024, BytesOut: 2048, DurationMs: 5000},
		},
	})
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result ReportStatsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.True(t, result.Success)
}

func TestServer_ReportStatsUnknownNode(t *testing.T) {
	_, baseURL := setupTestServer(t)

	resp := postJSON(t, baseURL+"/api/v1/stats", ReportStatsRequest{
		NodeID: "unknown",
	})
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestServer_ListNodes(t *testing.T) {
	srv, baseURL := setupTestServer(t)

	srv.Register(&RegisterRequest{NodeID: "node-1", NodeType: "ingress"})
	srv.Register(&RegisterRequest{NodeID: "node-2", NodeType: "egress"})

	resp, err := http.Get(baseURL + "/api/v1/nodes")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var nodes []*NodeInfo
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&nodes))
	assert.Len(t, nodes, 2)
}

func TestServer_MethodNotAllowed(t *testing.T) {
	_, baseURL := setupTestServer(t)

	// GET on register endpoint
	resp, err := http.Get(baseURL + "/api/v1/register")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)

	// POST on nodes endpoint
	resp2 := postJSON(t, baseURL+"/api/v1/nodes", nil)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp2.StatusCode)
}

func TestServer_DirectAPI(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	srv := NewServer(logger)

	// 直接调用 API（不通过 HTTP）
	regResp, err := srv.Register(&RegisterRequest{
		NodeID:   "direct-node",
		NodeType: "relay",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, regResp.Token)

	// 心跳
	hbResp, err := srv.Heartbeat(&HeartbeatRequest{
		NodeID: "direct-node",
		Token:  regResp.Token,
		Status: &NodeStatus{ActiveSessions: 10},
	})
	require.NoError(t, err)
	assert.False(t, hbResp.ConfigUpdated)

	// 获取节点
	node, ok := srv.GetNode("direct-node")
	require.True(t, ok)
	assert.Equal(t, "relay", node.Type)
	assert.Equal(t, int64(10), node.Status.ActiveSessions)

	// 列出节点
	nodes := srv.ListNodes()
	assert.Len(t, nodes, 1)
}

func TestServer_GetConfig(t *testing.T) {
	srv, baseURL := setupTestServer(t)

	// 注册节点
	regResp, err := srv.Register(&RegisterRequest{
		NodeID:   "node-1",
		NodeType: "ingress",
	})
	require.NoError(t, err)

	// 设置配置
	configData := []byte(`{"version":"2.0","node":{"id":"node-1"}}`)
	err = srv.SetNodeConfig("node-1", regResp.Token, configData)
	require.NoError(t, err)

	// 获取配置（版本 0 → 应该返回更新）
	resp := postJSON(t, baseURL+"/api/v1/config", GetConfigRequest{
		NodeID:        "node-1",
		Token:         regResp.Token,
		ConfigVersion: 0,
	})
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result GetConfigResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.True(t, result.Updated)
	assert.Equal(t, int64(1), result.ConfigVersion)
	assert.Equal(t, configData, result.ConfigData)
}

func TestServer_GetConfigNoUpdate(t *testing.T) {
	srv, baseURL := setupTestServer(t)

	regResp, err := srv.Register(&RegisterRequest{
		NodeID:   "node-1",
		NodeType: "ingress",
	})
	require.NoError(t, err)

	// 未设置配置，版本为 0，客户端也是 0 → 无更新
	resp := postJSON(t, baseURL+"/api/v1/config", GetConfigRequest{
		NodeID:        "node-1",
		Token:         regResp.Token,
		ConfigVersion: 0,
	})
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result GetConfigResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.False(t, result.Updated)
}

func TestServer_GetConfigInvalidToken(t *testing.T) {
	srv, baseURL := setupTestServer(t)

	srv.Register(&RegisterRequest{
		NodeID:   "node-1",
		NodeType: "ingress",
	})

	resp := postJSON(t, baseURL+"/api/v1/config", GetConfigRequest{
		NodeID: "node-1",
		Token:  "wrong-token",
	})
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestServer_SetConfig(t *testing.T) {
	srv, baseURL := setupTestServer(t)

	regResp, err := srv.Register(&RegisterRequest{
		NodeID:   "node-1",
		NodeType: "ingress",
	})
	require.NoError(t, err)

	configData := []byte(`{"version":"2.0"}`)
	resp := postJSON(t, baseURL+"/api/v1/config/set", SetConfigRequest{
		NodeID:     "node-1",
		Token:      regResp.Token,
		ConfigData: configData,
	})
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 验证版本递增
	node, ok := srv.GetNode("node-1")
	require.True(t, ok)
	assert.Equal(t, int64(1), node.ConfigVersion)
	assert.Equal(t, configData, node.ConfigData)

	// 再次设置，版本应递增到 2
	err = srv.SetNodeConfig("node-1", regResp.Token, []byte(`{"version":"2.1"}`))
	require.NoError(t, err)

	node, _ = srv.GetNode("node-1")
	assert.Equal(t, int64(2), node.ConfigVersion)
}

func TestServer_SetConfigUnknownNode(t *testing.T) {
	_, baseURL := setupTestServer(t)

	resp := postJSON(t, baseURL+"/api/v1/config/set", SetConfigRequest{
		NodeID:     "unknown",
		Token:      "any-token",
		ConfigData: []byte("{}"),
	})
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestServer_SetConfigInvalidToken(t *testing.T) {
	srv, baseURL := setupTestServer(t)

	srv.Register(&RegisterRequest{
		NodeID:   "node-1",
		NodeType: "ingress",
	})

	resp := postJSON(t, baseURL+"/api/v1/config/set", SetConfigRequest{
		NodeID:     "node-1",
		Token:      "wrong-token",
		ConfigData: []byte(`{"version":"2.0"}`),
	})
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// Verify config was not changed
	node, ok := srv.GetNode("node-1")
	require.True(t, ok)
	assert.Equal(t, int64(0), node.ConfigVersion)
}
