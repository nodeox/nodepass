package agent

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/nodeox/nodepass/internal/common"
	"gopkg.in/yaml.v3"
)

// LoadConfig 加载配置文件
func LoadConfig(path string) (*common.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg common.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := ValidateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

// ValidateConfig 验证配置
func ValidateConfig(c *common.Config) error {
	if c.Version == "" {
		return fmt.Errorf("version is required")
	}

	if c.Node.ID == "" {
		return fmt.Errorf("node.id is required")
	}

	if c.Node.Type == "" {
		return fmt.Errorf("node.type is required")
	}

	// 验证节点类型
	switch c.Node.Type {
	case "ingress", "egress", "relay":
		// OK
	default:
		return fmt.Errorf("invalid node.type: %s (must be ingress, egress, or relay)", c.Node.Type)
	}

	// 验证入站配置
	for i, in := range c.Inbounds {
		if in.Protocol == "" {
			return fmt.Errorf("inbounds[%d].protocol is required", i)
		}
		if in.Listen == "" {
			return fmt.Errorf("inbounds[%d].listen is required", i)
		}
		// 验证监听地址格式
		if _, _, err := net.SplitHostPort(in.Listen); err != nil {
			return fmt.Errorf("inbounds[%d].listen invalid: %w", i, err)
		}
	}

	// 验证出站配置
	for i, out := range c.Outbounds {
		if out.Name == "" {
			return fmt.Errorf("outbounds[%d].name is required", i)
		}
		if out.Protocol == "" {
			return fmt.Errorf("outbounds[%d].protocol is required", i)
		}
	}

	// 检测 TCP-over-TCP
	if err := detectTCPoverTCP(c); err != nil {
		return err
	}

	return nil
}

// detectTCPoverTCP 检测 TCP-over-TCP 配置
func detectTCPoverTCP(c *common.Config) error {
	// 检查入站是否为 TCP 类协议
	hasTCPInbound := false
	for _, in := range c.Inbounds {
		switch in.Protocol {
		case "tcp", "np-chain", "tls", "ws":
			hasTCPInbound = true
		}
		if hasTCPInbound {
			break
		}
	}

	if !hasTCPInbound {
		return nil
	}

	// 检查出站是否有 TCP 传输
	for _, out := range c.Outbounds {
		if out.Transport == "tcp" {
			return fmt.Errorf("TCP-over-TCP detected: inbound uses TCP and outbound '%s' uses TCP transport (use QUIC instead)", out.Name)
		}
	}

	return nil
}

// SaveConfig 保存配置到文件
func SaveConfig(cfg *common.Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	return nil
}

// MergeConfig 合并配置（用于热更新）
func MergeConfig(base, override *common.Config) *common.Config {
	merged := *base

	// 合并节点配置
	if override.Node.ID != "" {
		merged.Node.ID = override.Node.ID
	}
	if override.Node.Type != "" {
		merged.Node.Type = override.Node.Type
	}
	if len(override.Node.Tags) > 0 {
		if merged.Node.Tags == nil {
			merged.Node.Tags = make(map[string]string)
		}
		for k, v := range override.Node.Tags {
			merged.Node.Tags[k] = v
		}
	}

	// 合并入站配置
	if len(override.Inbounds) > 0 {
		merged.Inbounds = override.Inbounds
	}

	// 合并出站配置
	if len(override.Outbounds) > 0 {
		merged.Outbounds = override.Outbounds
	}

	// 合并路由配置
	if len(override.Routing.Rules) > 0 {
		merged.Routing = override.Routing
	}

	return &merged
}

// ConfigDiff 配置差异
type ConfigDiff struct {
	NodeChanged bool

	AddedInbounds   []common.InboundConfig
	RemovedInbounds []string
	UpdatedInbounds []common.InboundConfig

	AddedOutbounds   []common.OutboundConfig
	RemovedOutbounds []string
	UpdatedOutbounds []common.OutboundConfig

	RoutingChanged bool
}

// DiffConfig 比较配置差异
func DiffConfig(old, new *common.Config) *ConfigDiff {
	diff := &ConfigDiff{}

	// 比较节点配置
	if !nodeEqual(old.Node, new.Node) {
		diff.NodeChanged = true
	}

	// 比较入站
	oldInMap := make(map[string]common.InboundConfig)
	for _, in := range old.Inbounds {
		oldInMap[in.Listen] = in
	}

	newInMap := make(map[string]common.InboundConfig)
	for _, in := range new.Inbounds {
		newInMap[in.Listen] = in
	}

	// 查找新增和更新的入站
	for listen, newIn := range newInMap {
		if oldIn, exists := oldInMap[listen]; exists {
			if !inboundEqual(oldIn, newIn) {
				diff.UpdatedInbounds = append(diff.UpdatedInbounds, newIn)
			}
		} else {
			diff.AddedInbounds = append(diff.AddedInbounds, newIn)
		}
	}

	// 查找删除的入站
	for listen := range oldInMap {
		if _, exists := newInMap[listen]; !exists {
			diff.RemovedInbounds = append(diff.RemovedInbounds, listen)
		}
	}

	// 比较出站
	oldOutMap := make(map[string]common.OutboundConfig)
	for _, out := range old.Outbounds {
		oldOutMap[out.Name] = out
	}

	newOutMap := make(map[string]common.OutboundConfig)
	for _, out := range new.Outbounds {
		newOutMap[out.Name] = out
	}

	// 查找新增和更新的出站
	for name, newOut := range newOutMap {
		if oldOut, exists := oldOutMap[name]; exists {
			if !outboundEqual(oldOut, newOut) {
				diff.UpdatedOutbounds = append(diff.UpdatedOutbounds, newOut)
			}
		} else {
			diff.AddedOutbounds = append(diff.AddedOutbounds, newOut)
		}
	}

	// 查找删除的出站
	for name := range oldOutMap {
		if _, exists := newOutMap[name]; !exists {
			diff.RemovedOutbounds = append(diff.RemovedOutbounds, name)
		}
	}

	// 比较路由规则
	if !routingEqual(old.Routing, new.Routing) {
		diff.RoutingChanged = true
	}

	return diff
}

// inboundEqual 比较两个入站配置是否相等
func inboundEqual(a, b common.InboundConfig) bool {
	return a.Protocol == b.Protocol &&
		a.Listen == b.Listen &&
		a.Auth.Enabled == b.Auth.Enabled
}

// nodeEqual 比较两个节点配置是否相等
func nodeEqual(a, b common.NodeConfig) bool {
	if a.ID != b.ID || a.Type != b.Type {
		return false
	}

	// 比较 tags
	if len(a.Tags) != len(b.Tags) {
		return false
	}

	for k, v := range a.Tags {
		if b.Tags[k] != v {
			return false
		}
	}

	return true
}

// outboundEqual 比较两个出站配置是否相等
func outboundEqual(a, b common.OutboundConfig) bool {
	return a.Name == b.Name &&
		a.Protocol == b.Protocol &&
		a.Group == b.Group &&
		a.Address == b.Address &&
		a.Transport == b.Transport
}

// routingEqual 比较两个路由配置是否相等
func routingEqual(a, b common.RoutingConfig) bool {
	if len(a.Rules) != len(b.Rules) {
		return false
	}

	for i := range a.Rules {
		if !ruleEqual(a.Rules[i], b.Rules[i]) {
			return false
		}
	}

	return true
}

// ruleEqual 比较两个路由规则是否相等
func ruleEqual(a, b common.RoutingRule) bool {
	return a.Type == b.Type &&
		a.Pattern == b.Pattern &&
		a.Outbound == b.Outbound &&
		a.OutboundGroup == b.OutboundGroup &&
		a.Strategy == b.Strategy
}

// HasChanges 检查配置差异是否有变化
func (d *ConfigDiff) HasChanges() bool {
	return d.NodeChanged ||
		len(d.AddedInbounds) > 0 ||
		len(d.RemovedInbounds) > 0 ||
		len(d.UpdatedInbounds) > 0 ||
		len(d.AddedOutbounds) > 0 ||
		len(d.RemovedOutbounds) > 0 ||
		len(d.UpdatedOutbounds) > 0 ||
		d.RoutingChanged
}

// String 返回配置差异的字符串表示
func (d *ConfigDiff) String() string {
	var parts []string

	if d.NodeChanged {
		parts = append(parts, "node config changed")
	}
	if len(d.AddedInbounds) > 0 {
		parts = append(parts, fmt.Sprintf("added %d inbounds", len(d.AddedInbounds)))
	}
	if len(d.RemovedInbounds) > 0 {
		parts = append(parts, fmt.Sprintf("removed %d inbounds", len(d.RemovedInbounds)))
	}
	if len(d.UpdatedInbounds) > 0 {
		parts = append(parts, fmt.Sprintf("updated %d inbounds", len(d.UpdatedInbounds)))
	}
	if len(d.AddedOutbounds) > 0 {
		parts = append(parts, fmt.Sprintf("added %d outbounds", len(d.AddedOutbounds)))
	}
	if len(d.RemovedOutbounds) > 0 {
		parts = append(parts, fmt.Sprintf("removed %d outbounds", len(d.RemovedOutbounds)))
	}
	if len(d.UpdatedOutbounds) > 0 {
		parts = append(parts, fmt.Sprintf("updated %d outbounds", len(d.UpdatedOutbounds)))
	}
	if d.RoutingChanged {
		parts = append(parts, "routing changed")
	}

	if len(parts) == 0 {
		return "no changes"
	}

	return strings.Join(parts, ", ")
}
