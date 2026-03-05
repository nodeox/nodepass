package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/nodeox/nodepass/internal/common"
)

func TestLoadConfig(t *testing.T) {
	// 创建临时配置文件
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
version: "2.0"
node:
  id: "test-node"
  type: "ingress"
  tags:
    env: "test"

inbounds:
  - protocol: "np-chain"
    listen: "0.0.0.0:1080"

outbounds:
  - name: "direct"
    protocol: "direct"
    group: "default"
`

	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// 加载配置
	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Equal(t, "2.0", cfg.Version)
	assert.Equal(t, "test-node", cfg.Node.ID)
	assert.Equal(t, "ingress", cfg.Node.Type)
	assert.Equal(t, "test", cfg.Node.Tags["env"])
	assert.Len(t, cfg.Inbounds, 1)
	assert.Len(t, cfg.Outbounds, 1)
}

func TestLoadConfigFileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/config.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read config file")
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *common.Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "ingress",
				},
			},
			wantErr: false,
		},
		{
			name: "missing version",
			config: &common.Config{
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "ingress",
				},
			},
			wantErr: true,
			errMsg:  "version is required",
		},
		{
			name: "missing node id",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					Type: "ingress",
				},
			},
			wantErr: true,
			errMsg:  "node.id is required",
		},
		{
			name: "invalid node type",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "invalid",
				},
			},
			wantErr: true,
			errMsg:  "invalid node.type",
		},
		{
			name: "invalid inbound listen",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "ingress",
				},
				Inbounds: []common.InboundConfig{
					{
						Protocol: "np-chain",
						Listen:   "invalid",
					},
				},
			},
			wantErr: true,
			errMsg:  "listen invalid",
		},
		{
			name: "invalid inbound protocol",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "ingress",
				},
				Inbounds: []common.InboundConfig{
					{Protocol: "http", Listen: "0.0.0.0:1080"},
				},
			},
			wantErr: true,
			errMsg:  "protocol invalid",
		},
		{
			name: "tls missing cert",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "ingress",
				},
				Inbounds: []common.InboundConfig{
					{Protocol: "tls", Listen: "0.0.0.0:1080"},
				},
			},
			wantErr: true,
			errMsg:  "requires tls.cert and tls.key",
		},
		{
			name: "quic missing cert",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "ingress",
				},
				Inbounds: []common.InboundConfig{
					{Protocol: "quic", Listen: "0.0.0.0:1080"},
				},
			},
			wantErr: true,
			errMsg:  "requires tls.cert and tls.key",
		},
		{
			name: "forward missing target",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "ingress",
				},
				Inbounds: []common.InboundConfig{
					{Protocol: "forward", Listen: "0.0.0.0:1080"},
				},
			},
			wantErr: true,
			errMsg:  "requires target",
		},
		{
			name: "duplicate listen address",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "ingress",
				},
				Inbounds: []common.InboundConfig{
					{Protocol: "tcp", Listen: "0.0.0.0:1080"},
					{Protocol: "tcp", Listen: "0.0.0.0:1080"},
				},
			},
			wantErr: true,
			errMsg:  "listen duplicate",
		},
		{
			name: "invalid outbound protocol",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "ingress",
				},
				Outbounds: []common.OutboundConfig{
					{Name: "out1", Protocol: "socks5"},
				},
			},
			wantErr: true,
			errMsg:  "protocol invalid",
		},
		{
			name: "np-chain outbound missing address",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "ingress",
				},
				Outbounds: []common.OutboundConfig{
					{Name: "out1", Protocol: "np-chain"},
				},
			},
			wantErr: true,
			errMsg:  "requires address",
		},
		{
			name: "duplicate outbound name",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "ingress",
				},
				Outbounds: []common.OutboundConfig{
					{Name: "out1", Protocol: "direct"},
					{Name: "out1", Protocol: "direct"},
				},
			},
			wantErr: true,
			errMsg:  "name duplicate",
		},
		{
			name: "np-chain outbound transport quic rejected",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test-node",
					Type: "ingress",
				},
				Outbounds: []common.OutboundConfig{
					{Name: "out1", Protocol: "np-chain", Address: "1.2.3.4:5678", Transport: "quic"},
				},
			},
			wantErr: true,
			errMsg:  "does not support transport=quic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDetectTCPoverTCP(t *testing.T) {
	tests := []struct {
		name    string
		config  *common.Config
		wantErr bool
	}{
		{
			name: "no TCP-over-TCP",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test",
					Type: "ingress",
				},
				Inbounds: []common.InboundConfig{
					{Protocol: "tcp", Listen: "0.0.0.0:1080"},
				},
				Outbounds: []common.OutboundConfig{
					{Name: "out1", Protocol: "direct", Transport: "quic"},
				},
			},
			wantErr: false,
		},
		{
			name: "TCP-over-TCP detected",
			config: &common.Config{
				Version: "2.0",
				Node: common.NodeConfig{
					ID:   "test",
					Type: "ingress",
				},
				Inbounds: []common.InboundConfig{
					{Protocol: "tcp", Listen: "0.0.0.0:1080"},
				},
				Outbounds: []common.OutboundConfig{
					{Name: "out1", Protocol: "direct", Transport: "tcp"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := detectTCPoverTCP(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "TCP-over-TCP")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDiffConfig(t *testing.T) {
	oldCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test",
			Type: "ingress",
		},
		Inbounds: []common.InboundConfig{
			{Protocol: "np-chain", Listen: "0.0.0.0:1080"},
		},
		Outbounds: []common.OutboundConfig{
			{Name: "out1", Protocol: "direct"},
		},
	}

	newCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test",
			Type: "ingress",
		},
		Inbounds: []common.InboundConfig{
			{Protocol: "np-chain", Listen: "0.0.0.0:1080"},
			{Protocol: "np-chain", Listen: "0.0.0.0:8080"}, // 新增
		},
		Outbounds: []common.OutboundConfig{
			{Name: "out2", Protocol: "direct"}, // 替换
		},
	}

	diff := DiffConfig(oldCfg, newCfg)

	assert.True(t, diff.HasChanges())
	assert.Len(t, diff.AddedInbounds, 1)
	assert.Equal(t, "0.0.0.0:8080", diff.AddedInbounds[0].Listen)
	assert.Len(t, diff.AddedOutbounds, 1)
	assert.Equal(t, "out2", diff.AddedOutbounds[0].Name)
	assert.Len(t, diff.RemovedOutbounds, 1)
	assert.Equal(t, "out1", diff.RemovedOutbounds[0])
}

func TestConfigDiffString(t *testing.T) {
	diff := &ConfigDiff{
		AddedInbounds:   make([]common.InboundConfig, 1),
		RemovedOutbounds: []string{"out1"},
		RoutingChanged:  true,
	}

	str := diff.String()
	assert.Contains(t, str, "added 1 inbounds")
	assert.Contains(t, str, "removed 1 outbounds")
	assert.Contains(t, str, "routing changed")
}

func TestMergeConfig(t *testing.T) {
	base := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test",
			Type: "ingress",
			Tags: map[string]string{
				"env": "dev",
			},
		},
	}

	override := &common.Config{
		Node: common.NodeConfig{
			Tags: map[string]string{
				"region": "us-west",
			},
		},
	}

	merged := MergeConfig(base, override)

	assert.Equal(t, "test", merged.Node.ID)
	assert.Equal(t, "ingress", merged.Node.Type)
	assert.Equal(t, "dev", merged.Node.Tags["env"])
	assert.Equal(t, "us-west", merged.Node.Tags["region"])
}
