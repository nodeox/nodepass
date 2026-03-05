package control

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestClient_RegisterAndHeartbeat(t *testing.T) {
	_, baseURL := setupTestServer(t)
	logger, _ := zap.NewDevelopment()

	client := NewClient(baseURL, "client-node", "ingress", logger)

	ctx := context.Background()

	// 注册
	err := client.Register(ctx, map[string]string{"region": "us-west"})
	require.NoError(t, err)
	assert.NotEmpty(t, client.Token())

	// 心跳
	configUpdated, err := client.Heartbeat(ctx, &NodeStatus{
		ActiveSessions: 3,
		CPUUsage:       0.2,
		MemUsage:       0.4,
	})
	require.NoError(t, err)
	assert.False(t, configUpdated)
}

func TestClient_GetConfig(t *testing.T) {
	srv, baseURL := setupTestServer(t)
	logger, _ := zap.NewDevelopment()

	client := NewClient(baseURL, "client-node", "ingress", logger)

	ctx := context.Background()

	err := client.Register(ctx, nil)
	require.NoError(t, err)

	// 无配置时不应有更新
	data, updated, err := client.GetConfig(ctx)
	require.NoError(t, err)
	assert.False(t, updated)
	assert.Nil(t, data)

	// 服务端设置配置
	configData := []byte(`{"version":"2.0","node":{"id":"client-node"}}`)
	err = srv.SetNodeConfig("client-node", client.Token(), configData)
	require.NoError(t, err)

	// 客户端拉取 → 应该有更新
	data, updated, err = client.GetConfig(ctx)
	require.NoError(t, err)
	assert.True(t, updated)
	assert.Equal(t, configData, data)

	// 再次拉取 → 版本已是最新，无更新
	data, updated, err = client.GetConfig(ctx)
	require.NoError(t, err)
	assert.False(t, updated)
	assert.Nil(t, data)
}

func TestClient_ReportStats(t *testing.T) {
	_, baseURL := setupTestServer(t)
	logger, _ := zap.NewDevelopment()

	client := NewClient(baseURL, "client-node", "ingress", logger)

	ctx := context.Background()

	err := client.Register(ctx, nil)
	require.NoError(t, err)

	err = client.ReportStats(ctx, []SessionStats{
		{SessionID: "s1", BytesIn: 1024, BytesOut: 2048},
	})
	require.NoError(t, err)
}

func TestClient_RegisterFail(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// 连接到不存在的服务
	client := NewClient("http://127.0.0.1:1", "node", "ingress", logger)

	err := client.Register(context.Background(), nil)
	assert.Error(t, err)
}
