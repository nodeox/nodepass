package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/nodeox/nodepass/internal/common"
	"go.uber.org/zap"
)

func TestNew(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)
	assert.NotNil(t, ag)
	assert.Equal(t, int32(StateStopped), ag.GetState())
}

func TestAgentLifecycle(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
			Tags: map[string]string{
				"env": "test",
			},
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	// 测试启动
	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)
	assert.Equal(t, int32(StateRunning), ag.GetState())
	assert.Equal(t, "running", ag.GetStateName())

	// 等待一小段时间让协程启动
	time.Sleep(100 * time.Millisecond)

	// 测试获取统计信息
	stats := ag.GetStats()
	assert.Equal(t, "running", stats["state"])
	assert.Greater(t, stats["goroutines"].(int), 0)

	// 测试停止
	err = ag.Stop()
	require.NoError(t, err)
	assert.Equal(t, int32(StateStopped), ag.GetState())
	assert.Equal(t, "stopped", ag.GetStateName())
}

func TestAgentDoubleStart(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	ctx := context.Background()

	// 第一次启动
	err = ag.Start(ctx)
	require.NoError(t, err)

	// 第二次启动应该失败
	err = ag.Start(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already started")

	// 清理
	ag.Stop()
}

func TestAgentStopWithoutStart(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	// 未启动就停止应该失败
	err = ag.Stop()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestAgentReloadConfig(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)
	defer ag.Stop()

	// 重新加载配置
	newCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "egress", // 改变类型
			Tags: map[string]string{
				"env": "production",
			},
		},
	}

	err = ag.ReloadConfig(newCfg)
	require.NoError(t, err)

	// 验证配置已更新
	currentCfg := ag.GetConfig()
	assert.Equal(t, "egress", currentCfg.Node.Type)
	assert.Equal(t, "production", currentCfg.Node.Tags["env"])
}

func TestAgentReloadConfigInvalid(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)
	defer ag.Stop()

	// 尝试加载无效配置
	invalidCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "", // 无效：ID 为空
			Type: "ingress",
		},
	}

	err = ag.ReloadConfig(invalidCfg)
	assert.Error(t, err)

	// 验证配置未改变
	currentCfg := ag.GetConfig()
	assert.Equal(t, "test-node", currentCfg.Node.ID)
}

func TestAgentWithInbounds(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Inbounds: []common.InboundConfig{
			{
				Protocol: "np-chain",
				Listen:   "127.0.0.1:0",
			},
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)
	assert.Equal(t, int32(StateRunning), ag.GetState())

	// 等待入站启动
	time.Sleep(200 * time.Millisecond)

	// 验证入站已注册
	stats := ag.GetStats()
	assert.Equal(t, 1, stats["inbounds_count"])

	// 停止
	err = ag.Stop()
	require.NoError(t, err)
	assert.Equal(t, int32(StateStopped), ag.GetState())
}

func TestApplyConfigDiff_AddInbound(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)
	defer ag.Stop()

	// 验证初始无入站
	stats := ag.GetStats()
	assert.Equal(t, 0, stats["inbounds_count"])

	// 热更新添加入站
	newCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Inbounds: []common.InboundConfig{
			{
				Protocol: "np-chain",
				Listen:   "127.0.0.1:0",
			},
		},
	}

	err = ag.ReloadConfig(newCfg)
	require.NoError(t, err)

	// 等待入站启动
	time.Sleep(200 * time.Millisecond)

	// 验证入站已添加
	stats = ag.GetStats()
	assert.Equal(t, 1, stats["inbounds_count"])
}

func TestApplyConfigDiff_AddRemoveOutbound(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Outbounds: []common.OutboundConfig{
			{
				Name:     "direct-1",
				Protocol: "direct",
				Group:    "default",
			},
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)
	defer ag.Stop()

	// 验证初始出站
	stats := ag.GetStats()
	assert.Equal(t, 1, stats["outbounds_count"])

	// 热更新：移除 direct-1，添加 direct-2
	newCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Outbounds: []common.OutboundConfig{
			{
				Name:     "direct-2",
				Protocol: "direct",
				Group:    "default",
			},
		},
	}

	err = ag.ReloadConfig(newCfg)
	require.NoError(t, err)

	// 验证出站已更新
	stats = ag.GetStats()
	assert.Equal(t, 1, stats["outbounds_count"])

	// 验证旧出站被移除、新出站被添加
	ag.mu.RLock()
	_, hasDirect1 := ag.outbounds["direct-1"]
	_, hasDirect2 := ag.outbounds["direct-2"]
	ag.mu.RUnlock()

	assert.False(t, hasDirect1, "direct-1 should be removed")
	assert.True(t, hasDirect2, "direct-2 should be added")
}

func TestApplyConfigDiff_RoutingChanged(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Outbounds: []common.OutboundConfig{
			{
				Name:     "direct-1",
				Protocol: "direct",
				Group:    "default",
			},
		},
		Routing: common.RoutingConfig{
			Rules: []common.RoutingRule{
				{
					Type:     "default",
					Outbound: "direct-1",
				},
			},
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)
	defer ag.Stop()

	// 热更新路由规则
	newCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Outbounds: []common.OutboundConfig{
			{
				Name:     "direct-1",
				Protocol: "direct",
				Group:    "default",
			},
		},
		Routing: common.RoutingConfig{
			Rules: []common.RoutingRule{
				{
					Type:          "domain",
					Pattern:       "*.example.com",
					OutboundGroup: "default",
					Strategy:      "round-robin",
				},
				{
					Type:     "default",
					Outbound: "direct-1",
				},
			},
		},
	}

	err = ag.ReloadConfig(newCfg)
	require.NoError(t, err)

	// 验证配置已更新
	currentCfg := ag.GetConfig()
	assert.Len(t, currentCfg.Routing.Rules, 2)
	assert.Equal(t, "domain", currentCfg.Routing.Rules[0].Type)
}

func TestConfigWatchLoop(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// 创建临时配置文件
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `version: "2.0"
node:
  id: "test-node"
  type: "ingress"
`
	err := os.WriteFile(configPath, []byte(initialConfig), 0644)
	require.NoError(t, err)

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	ag.SetConfigPath(configPath)
	ag.SetConfigPollInterval(100 * time.Millisecond)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)
	defer ag.Stop()

	// 验证初始配置
	assert.Equal(t, "ingress", ag.GetConfig().Node.Type)

	// 等待初始 modtime 被记录
	time.Sleep(200 * time.Millisecond)

	// 修改配置文件
	updatedConfig := `version: "2.0"
node:
  id: "test-node"
  type: "egress"
`
	err = os.WriteFile(configPath, []byte(updatedConfig), 0644)
	require.NoError(t, err)

	// 等待配置轮询检测到变化
	time.Sleep(500 * time.Millisecond)

	// 验证配置已更新
	currentCfg := ag.GetConfig()
	assert.Equal(t, "egress", currentCfg.Node.Type)
}

func TestReloadConfig_PartialFailureNoSideEffects(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// Initial config with one outbound
	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Outbounds: []common.OutboundConfig{
			{Name: "direct-1", Protocol: "direct", Group: "default"},
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)
	defer ag.Stop()

	// Record original state
	origOutboundCount := len(ag.outbounds)
	origInboundCount := len(ag.inbounds)

	// New config: adds a valid inbound change AND an invalid outbound (np-chain without address)
	newCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Inbounds: []common.InboundConfig{
			{Protocol: "tcp", Listen: "127.0.0.1:0"},
		},
		Outbounds: []common.OutboundConfig{
			{Name: "direct-1", Protocol: "direct", Group: "default"},
			{Name: "chain-1", Protocol: "np-chain"}, // invalid: missing address
		},
	}

	// ReloadConfig should fail because np-chain outbound has no address
	err = ag.ReloadConfig(newCfg)
	assert.Error(t, err)

	// Verify original state is unchanged — prepare phase failed, nothing was modified
	ag.mu.RLock()
	assert.Equal(t, origInboundCount, len(ag.inbounds), "inbound count should not change after failed reload")
	assert.Equal(t, origOutboundCount, len(ag.outbounds), "outbound count should not change after failed reload")
	ag.mu.RUnlock()

	// Verify config was not updated
	currentCfg := ag.GetConfig()
	assert.Equal(t, cfg, currentCfg, "config should not change after failed reload")
}
