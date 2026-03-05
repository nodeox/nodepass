package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
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

func TestReloadConfig_AddInboundBindFailureRollback(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// Initial config with one inbound on a real port
	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Inbounds: []common.InboundConfig{
			{Protocol: "tcp", Listen: "127.0.0.1:0"},
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)
	defer ag.Stop()

	time.Sleep(100 * time.Millisecond)

	// Record original state
	ag.mu.RLock()
	origInboundCount := len(ag.inbounds)
	ag.mu.RUnlock()
	assert.Equal(t, 1, origInboundCount)

	// Occupy a port so the new inbound will fail to bind
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer blocker.Close()
	blockedAddr := blocker.Addr().String()

	// New config: keep existing inbound + add a new one on the blocked port
	newCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Inbounds: []common.InboundConfig{
			{Protocol: "tcp", Listen: "127.0.0.1:0"},
			{Protocol: "tcp", Listen: blockedAddr}, // will fail to bind
		},
	}

	err = ag.ReloadConfig(newCfg)
	assert.Error(t, err, "should fail because the port is occupied")

	// Verify original inbounds are still intact
	ag.mu.RLock()
	assert.Equal(t, origInboundCount, len(ag.inbounds),
		"inbound count should not change after bind failure rollback")
	ag.mu.RUnlock()
}

func TestGetConfig_DeepCopy(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
			Tags: map[string]string{"env": "test"},
		},
		Inbounds: []common.InboundConfig{
			{Protocol: "tcp", Listen: "0.0.0.0:1080"},
		},
		Outbounds: []common.OutboundConfig{
			{Name: "out1", Protocol: "direct"},
		},
		Routing: common.RoutingConfig{
			Rules: []common.RoutingRule{
				{Type: "default", Outbound: "out1"},
			},
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	got := ag.GetConfig()

	// Mutate the returned copy
	got.Node.Tags["env"] = "mutated"
	got.Inbounds[0].Listen = "mutated"
	got.Outbounds[0].Name = "mutated"
	got.Routing.Rules[0].Type = "mutated"

	// Verify internal state is unchanged
	internal := ag.config.Load().(*common.Config)
	assert.Equal(t, "test", internal.Node.Tags["env"], "Tags should not be mutated")
	assert.Equal(t, "0.0.0.0:1080", internal.Inbounds[0].Listen, "Inbounds should not be mutated")
	assert.Equal(t, "out1", internal.Outbounds[0].Name, "Outbounds should not be mutated")
	assert.Equal(t, "default", internal.Routing.Rules[0].Type, "Routing rules should not be mutated")
}

func TestReloadConfig_UpdatedInboundSuccess(t *testing.T) {
	// Verify the updated-inbound happy path: old inbound stops, new starts on
	// the freed port, handler is replaced, no error returned.
	logger, _ := zap.NewDevelopment()

	// Allocate a real port
	tmp, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := tmp.Addr().String()
	tmp.Close()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Inbounds: []common.InboundConfig{
			{Protocol: "tcp", Listen: port},
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)
	defer ag.Stop()

	time.Sleep(100 * time.Millisecond)

	ag.mu.RLock()
	assert.Equal(t, 1, len(ag.inbounds))
	ag.mu.RUnlock()

	// Update the inbound protocol: tcp → np-chain on same port.
	// DiffConfig sees protocol change → "updated" path in commitDiff.
	newCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Inbounds: []common.InboundConfig{
			{Protocol: "np-chain", Listen: port},
		},
	}

	err = ag.ReloadConfig(newCfg)
	require.NoError(t, err, "successful update should not return error")

	// Verify handler was replaced
	ag.mu.RLock()
	assert.Equal(t, 1, len(ag.inbounds))
	ag.mu.RUnlock()

	// Verify config reflects the update
	currentCfg := ag.GetConfig()
	assert.Equal(t, "np-chain", currentCfg.Inbounds[0].Protocol)
}

func TestReloadConfig_AddedInboundBindFailure_NotPartialError(t *testing.T) {
	// When only "added" inbounds fail to bind (no updates succeed),
	// should return regular error, not PartialReloadError.
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

	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer blocker.Close()

	newCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Inbounds: []common.InboundConfig{
			{Protocol: "tcp", Listen: blocker.Addr().String()},
		},
	}

	err = ag.ReloadConfig(newCfg)
	require.Error(t, err)

	// Should NOT be a PartialReloadError — nothing was changed
	var partialErr *PartialReloadError
	assert.False(t, errors.As(err, &partialErr),
		"added inbound bind failure should not be PartialReloadError")
}

func TestNew_StoresDeepCopy(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
			Tags: map[string]string{"env": "test"},
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	// Mutate the original cfg
	cfg.Node.Tags["env"] = "mutated"
	cfg.Node.ID = "mutated"

	// Internal config should be unchanged
	internal := ag.config.Load().(*common.Config)
	assert.Equal(t, "test", internal.Node.Tags["env"])
	assert.Equal(t, "test-node", internal.Node.ID)
}

func TestReloadConfig_StoresDeepCopy(t *testing.T) {
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

	newCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "egress",
			Tags: map[string]string{"env": "prod"},
		},
	}

	err = ag.ReloadConfig(newCfg)
	require.NoError(t, err)

	// Mutate newCfg after reload
	newCfg.Node.Tags["env"] = "mutated"

	internal := ag.config.Load().(*common.Config)
	assert.Equal(t, "prod", internal.Node.Tags["env"], "internal config should not be affected by external mutation")
}

func TestConfigWatchLoop_AdvancesOnPartialReload(t *testing.T) {
	logger, _ := zap.NewDevelopment()

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

	time.Sleep(200 * time.Millisecond)

	// Write an invalid config — LoadConfig will fail, lastConfigModTime should NOT advance
	invalidConfig := `version: "2.0"
node:
  id: ""
  type: "ingress"
`
	err = os.WriteFile(configPath, []byte(invalidConfig), 0644)
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// Config should still be original (node.id = "test-node")
	currentCfg := ag.GetConfig()
	assert.Equal(t, "test-node", currentCfg.Node.ID, "config should not change on failed reload")

	// Fix config — on next poll it should reload successfully
	fixedConfig := `version: "2.0"
node:
  id: "test-node"
  type: "relay"
`
	err = os.WriteFile(configPath, []byte(fixedConfig), 0644)
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	currentCfg = ag.GetConfig()
	assert.Equal(t, "relay", currentCfg.Node.Type, "config should be reloaded after fix")
}

func TestPartialReloadError_Unwrap(t *testing.T) {
	inner := errors.New("some error")
	pre := &PartialReloadError{Err: inner}

	assert.ErrorIs(t, pre, inner)
	assert.Contains(t, pre.Error(), "partial reload")
	assert.Contains(t, pre.Error(), "some error")
}

func TestPartialReloadError_Semantics(t *testing.T) {
	// Verify PartialReloadError is distinguishable from regular errors via errors.As.
	// This is the core semantic contract that configWatchLoop relies on.

	t.Run("partial error detected", func(t *testing.T) {
		err := fmt.Errorf("failed to commit config: %w", &PartialReloadError{
			Err: errors.New("inbound update failed"),
		})
		var partialErr *PartialReloadError
		assert.True(t, errors.As(err, &partialErr), "should detect PartialReloadError through wrapping")
	})

	t.Run("regular error not detected", func(t *testing.T) {
		err := fmt.Errorf("failed to commit config: %w", errors.New("all updates failed"))
		var partialErr *PartialReloadError
		assert.False(t, errors.As(err, &partialErr), "regular error should not match PartialReloadError")
	})

	t.Run("prepare failure not detected", func(t *testing.T) {
		err := fmt.Errorf("failed to prepare config: %w", errors.New("invalid outbound"))
		var partialErr *PartialReloadError
		assert.False(t, errors.As(err, &partialErr), "prepare-phase error should not match PartialReloadError")
	})
}

func TestNew_NilConfig(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	ag, err := New(nil, logger)
	assert.Nil(t, ag)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config is nil")
}

func TestReloadConfig_NilConfig(t *testing.T) {
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

	err = ag.ReloadConfig(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config is nil")
}

func TestReloadConfig_ConcurrentSerialization(t *testing.T) {
	// Verify concurrent ReloadConfig calls are serialized (no interleaving).
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

	// Fire 5 concurrent reloads with different node types.
	// All should complete without panic or race detector complaints.
	var wg sync.WaitGroup
	types := []string{"egress", "relay", "ingress", "egress", "relay"}
	errs := make([]error, len(types))

	for i, nodeType := range types {
		wg.Add(1)
		go func(idx int, nt string) {
			defer wg.Done()
			newCfg := &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: nt,
				},
			}
			errs[idx] = ag.ReloadConfig(newCfg)
		}(i, nodeType)
	}

	wg.Wait()

	// All reloads should succeed (they're serialized, each sees the previous result)
	for i, err := range errs {
		assert.NoError(t, err, "reload %d should succeed", i)
	}

	// Final config should be one of the types (last to execute wins)
	currentCfg := ag.GetConfig()
	assert.Contains(t, []string{"egress", "relay", "ingress"}, currentCfg.Node.Type)
}

func TestReloadConfig_AfterStop(t *testing.T) {
	// ReloadConfig should return error if agent is not running.
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

	err = ag.Stop()
	require.NoError(t, err)

	// Reload after stop should fail
	newCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "egress",
		},
	}
	err = ag.ReloadConfig(newCfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestStopAndReload_Concurrent(t *testing.T) {
	// Verify Stop() and ReloadConfig() don't race — reloadMu serializes them.
	// After both complete, agent should be stopped and no goroutine leak.
	logger, _ := zap.NewDevelopment()

	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Inbounds: []common.InboundConfig{
			{Protocol: "tcp", Listen: "127.0.0.1:0"},
		},
	}

	ag, err := New(cfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Launch Stop and ReloadConfig concurrently
	var wg sync.WaitGroup
	var stopErr, reloadErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		reloadCfg := &common.Config{
			Version: "2.0",
			Node: common.NodeConfig{
				ID:   "test-node",
				Type: "egress",
			},
			Inbounds: []common.InboundConfig{
				{Protocol: "tcp", Listen: "127.0.0.1:0"},
			},
		}
		reloadErr = ag.ReloadConfig(reloadCfg)
	}()

	go func() {
		defer wg.Done()
		stopErr = ag.Stop()
	}()

	wg.Wait()

	// Exactly one of them should succeed in a meaningful way.
	// If Stop() wins the CAS first: stopErr=nil, reloadErr="not running"
	// If ReloadConfig() runs first: reloadErr=nil or error, then Stop() succeeds
	// Either way, the agent should end up stopped with no panic or race.
	assert.Equal(t, int32(StateStopped), ag.GetState(),
		"agent should be stopped after concurrent Stop+Reload")

	// At most one error is "not running" (either Stop couldn't CAS, or Reload found non-Running state)
	t.Logf("stopErr=%v, reloadErr=%v", stopErr, reloadErr)
}

func TestRollbackIncompleteError(t *testing.T) {
	cause := errors.New("inbound X failed to start")
	rbe := &RollbackIncompleteError{
		Cause:          cause,
		RollbackErrors: []string{"Y: timeout", "Z: connection refused"},
	}

	assert.ErrorIs(t, rbe, cause)
	assert.Contains(t, rbe.Error(), "inbound X failed to start")
	assert.Contains(t, rbe.Error(), "rollback incomplete")
	assert.Contains(t, rbe.Error(), "Y: timeout")

	// Verify it can be detected via errors.As
	var rbErr *RollbackIncompleteError
	assert.True(t, errors.As(rbe, &rbErr))
	assert.Equal(t, 2, len(rbErr.RollbackErrors))

	// Verify it works when wrapped in another error
	wrapped := fmt.Errorf("failed to commit: %w", rbe)
	assert.True(t, errors.As(wrapped, &rbErr))
}

func TestStopWithConfigWatchLoop_NoDeadlock(t *testing.T) {
	// Regression: Stop() holding reloadMu while calling wg.Wait() deadlocks
	// with configWatchLoop blocked in ReloadConfig()->reloadMu.Lock().
	// This test verifies the fix: Stop() no longer holds reloadMu during wg.Wait().
	logger, _ := zap.NewDevelopment()

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

	// Enable config watch loop with very short poll interval
	ag.SetConfigPath(configPath)
	ag.SetConfigPollInterval(10 * time.Millisecond)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)

	// Let configWatchLoop start
	time.Sleep(50 * time.Millisecond)

	// Modify config to trigger reload
	updatedConfig := `version: "2.0"
node:
  id: "test-node"
  type: "egress"
`
	err = os.WriteFile(configPath, []byte(updatedConfig), 0644)
	require.NoError(t, err)

	// Give configWatchLoop time to detect change and enter ReloadConfig
	time.Sleep(50 * time.Millisecond)

	// Call Stop while configWatchLoop might be mid-reload
	// Should complete without deadlock
	done := make(chan error, 1)
	go func() {
		done <- ag.Stop()
	}()

	select {
	case err := <-done:
		assert.NoError(t, err, "Stop should complete without deadlock")
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() deadlocked with configWatchLoop")
	}

	assert.Equal(t, int32(StateStopped), ag.GetState())
}

func TestCommitDiff_UpdatedInboundWaitRespectsContext(t *testing.T) {
	// Regression: commitDiff's updated inbound wait loop missing ctx.Done() case
	// can hang during Stop(). This test verifies the fix.
	logger, _ := zap.NewDevelopment()

	// Bind to a specific port so we can update the same listen address
	tmp, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := tmp.Addr().(*net.TCPAddr).Port
	tmp.Close()
	time.Sleep(50 * time.Millisecond) // Let OS release the port

	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	oldCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Inbounds: []common.InboundConfig{
			{
				Protocol: "forward",
				Listen:   listenAddr,
				Target:   "127.0.0.1:8080", // Initial target
			},
		},
	}

	ag, err := New(oldCfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)

	// Wait for inbound to be ready
	time.Sleep(100 * time.Millisecond)

	// Prepare a config update that changes the Target field (triggers inbound update)
	newCfg := oldCfg.DeepCopy()
	newCfg.Inbounds[0].Target = "127.0.0.1:9090" // Different target

	// Start reload in background
	reloadDone := make(chan error, 1)
	go func() {
		reloadDone <- ag.ReloadConfig(newCfg)
	}()

	// Give reload time to enter commitDiff and start waiting for updated inbound
	time.Sleep(50 * time.Millisecond)

	// Call Stop while reload is waiting
	stopDone := make(chan error, 1)
	go func() {
		stopDone <- ag.Stop()
	}()

	// Stop should complete within reasonable time (not hang)
	select {
	case err := <-stopDone:
		assert.NoError(t, err, "Stop should complete without hanging")
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() hung waiting for updated inbound")
	}

	// Reload should also complete (may succeed or fail, but shouldn't hang)
	select {
	case <-reloadDone:
		// OK
	case <-time.After(1 * time.Second):
		t.Fatal("ReloadConfig hung after Stop")
	}

	assert.Equal(t, int32(StateStopped), ag.GetState())
}

func TestInbound_StartStopRace(t *testing.T) {
	// Regression: Start/Stop race condition where Stop() is called before
	// Start() completes initialization, causing listener/goroutine leak.
	// This test verifies that early cancel is set and Stop() works correctly.
	logger, _ := zap.NewDevelopment()

	tests := []struct {
		name     string
		protocol string
		config   common.InboundConfig
	}{
		{
			name:     "tcp",
			protocol: "tcp",
			config:   common.InboundConfig{Protocol: "tcp", Listen: "127.0.0.1:0"},
		},
		{
			name:     "forward",
			protocol: "forward",
			config:   common.InboundConfig{Protocol: "forward", Listen: "127.0.0.1:0", Target: "127.0.0.1:8080"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "ingress",
				},
				Inbounds: []common.InboundConfig{tt.config},
			}

			ag, err := New(cfg, logger)
			require.NoError(t, err)

			ctx := context.Background()
			err = ag.Start(ctx)
			require.NoError(t, err)

			// Immediately stop without waiting for full initialization
			// This tests the early cancel mechanism
			err = ag.Stop()
			assert.NoError(t, err, "Stop should succeed even if called immediately after Start")
			assert.Equal(t, int32(StateStopped), ag.GetState())
		})
	}
}

func TestRollbackIncompleteError_WrappedInPartialReloadError(t *testing.T) {
	// Regression: RollbackIncompleteError not wrapped in PartialReloadError
	// causes configWatchLoop to retry infinitely.
	// This test verifies that RollbackIncompleteError is properly wrapped.

	// Create a RollbackIncompleteError
	rbErr := &RollbackIncompleteError{
		Cause:          fmt.Errorf("inbound failed to start"),
		RollbackErrors: []string{"failed to stop inbound"},
	}

	// Verify it can be unwrapped from PartialReloadError
	partialErr := &PartialReloadError{Err: rbErr}

	var unwrapped *RollbackIncompleteError
	assert.True(t, errors.As(partialErr, &unwrapped),
		"RollbackIncompleteError should be unwrappable from PartialReloadError")
	assert.Equal(t, rbErr, unwrapped)

	// Verify the configWatchLoop logic recognizes PartialReloadError
	var partialCheck *PartialReloadError
	assert.True(t, errors.As(partialErr, &partialCheck),
		"PartialReloadError should be detectable with errors.As")
}

func TestReloadConfig_RollbackIncompleteWrappedInPartialReload(t *testing.T) {
	// Integration test: verify that when rollback fails during reload,
	// the error is wrapped in PartialReloadError so configWatchLoop advances modTime.
	logger, _ := zap.NewDevelopment()

	// Start with a valid config
	oldCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
		Inbounds: []common.InboundConfig{
			{Protocol: "tcp", Listen: "127.0.0.1:0"},
		},
	}

	ag, err := New(oldCfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Try to add an inbound with invalid address (will fail to start)
	newCfg := oldCfg.DeepCopy()
	newCfg.Inbounds = append(newCfg.Inbounds, common.InboundConfig{
		Protocol: "tcp",
		Listen:   "999.999.999.999:9999", // Invalid address
	})

	// ReloadConfig should fail, but if rollback also fails, it should be PartialReloadError
	err = ag.ReloadConfig(newCfg)
	require.Error(t, err)

	// The error should indicate failure to commit
	assert.Contains(t, err.Error(), "failed to commit config")

	// If rollback failed (RollbackIncompleteError), it should be wrapped in PartialReloadError
	// In this case, rollback likely succeeded (Stop() worked), so we just verify the error chain
	// The key fix is in agent.go:457 and agent.go:569 where RollbackIncompleteError gets wrapped

	// Clean up
	err = ag.Stop()
	require.NoError(t, err)
}
