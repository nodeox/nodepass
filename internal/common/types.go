package common

import (
	"context"
	"net"
	"time"
)

// SessionMeta 会话元数据
type SessionMeta struct {
	ID        string            // 唯一会话 ID (UUID)
	TraceID   string            // OpenTelemetry Trace ID
	UserID    string            // 租户/用户 ID
	Source    net.Addr          // 来源地址
	Target    string            // 最终目标地址 (host:port)
	HopChain  []string          // 多跳链路列表
	RouteTags map[string]string // 路由标签
	CreatedAt time.Time         // 创建时间
}

// InboundHandler 入站处理器接口
type InboundHandler interface {
	// Start 启动监听，阻塞直到 ctx 取消
	Start(ctx context.Context, router Router) error

	// Stop 停止监听，必须是幂等的
	Stop() error

	// Addr 获取监听地址
	Addr() net.Addr

	// WaitReady 阻塞直到 listener 绑定成功
	WaitReady()
}

// OutboundHandler 出站处理器接口
type OutboundHandler interface {
	// Dial 拨号到目标，必须在 ctx 超时前返回
	Dial(ctx context.Context, meta SessionMeta) (net.Conn, error)

	// HealthCheck 健康检查，返回 0-1 的分值
	HealthCheck(ctx context.Context) float64

	// Name 节点名称
	Name() string

	// Group 所属节点组
	Group() string
}

// Router 路由器接口
type Router interface {
	// Route 根据会话元数据选择出站
	Route(meta SessionMeta) (OutboundHandler, error)

	// AddOutbound 添加出站处理器
	AddOutbound(out OutboundHandler)

	// RemoveOutbound 移除出站处理器
	RemoveOutbound(name string)

	// UpdateRules 更新路由规则
	UpdateRules(rules []RoutingRule)
}

// RoutingRule 路由规则
type RoutingRule struct {
	Type          string   // "domain", "ip", "user", "default"
	Pattern       string   // 匹配模式
	Outbound      string   // 出站名称
	OutboundGroup string   // 出站组名称
	Strategy      string   // "round-robin", "lowest-latency", "hash"
}

// FlowController 流控接口
type FlowController interface {
	// ShouldPause 是否应该暂停读取
	ShouldPause() bool

	// OnConsumed 通知已消费 n 字节
	OnConsumed(n int)

	// WindowSize 获取当前窗口大小
	WindowSize() int

	// Reset 重置窗口
	Reset()
}

// Config 配置结构
type Config struct {
	Version        string              `yaml:"version"`
	Node           NodeConfig          `yaml:"node"`
	Controller     ControllerConfig    `yaml:"controller"`
	Inbounds       []InboundConfig     `yaml:"inbounds"`
	Outbounds      []OutboundConfig    `yaml:"outbounds"`
	Routing        RoutingConfig       `yaml:"routing"`
	Aggregation    AggregationConfig   `yaml:"aggregation"`
	Observability  ObservabilityConfig `yaml:"observability"`
	Limits         LimitsConfig        `yaml:"limits"`
}

// NodeConfig 节点配置
type NodeConfig struct {
	ID   string            `yaml:"id"`
	Type string            `yaml:"type"` // "ingress", "egress", "relay"
	Tags map[string]string `yaml:"tags"`
}

// ControllerConfig 控制器配置
type ControllerConfig struct {
	Enabled bool      `yaml:"enabled"`
	Address string    `yaml:"address"`
	TLS     TLSConfig `yaml:"tls"`
}

// InboundConfig 入站配置
type InboundConfig struct {
	Protocol string     `yaml:"protocol"`
	Listen   string     `yaml:"listen"`
	Target   string     `yaml:"target"`
	Auth     AuthConfig `yaml:"auth"`
	TLS      TLSConfig  `yaml:"tls"`
}

// OutboundConfig 出站配置
type OutboundConfig struct {
	Name      string    `yaml:"name"`
	Protocol  string    `yaml:"protocol"`
	Group     string    `yaml:"group"`
	Address   string    `yaml:"address"`
	Transport string    `yaml:"transport"`
	TLS       TLSConfig `yaml:"tls"`
}

// RoutingConfig 路由配置
type RoutingConfig struct {
	Rules []RoutingRule `yaml:"rules"`
}

// AggregationConfig 聚合配置
type AggregationConfig struct {
	Enabled     bool `yaml:"enabled"`
	Connections int  `yaml:"connections"`
	ChunkSize   int  `yaml:"chunk_size"`
}

// ObservabilityConfig 可观测性配置
type ObservabilityConfig struct {
	Tracing TracingConfig `yaml:"tracing"`
	Metrics MetricsConfig `yaml:"metrics"`
	Logging LoggingConfig `yaml:"logging"`
	Pprof   PprofConfig   `yaml:"pprof"`
}

// PprofConfig pprof 调试配置
type PprofConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

// TracingConfig 追踪配置
type TracingConfig struct {
	Enabled    bool    `yaml:"enabled"`
	Endpoint   string  `yaml:"endpoint"`
	SampleRate float64 `yaml:"sample_rate"`
}

// MetricsConfig 指标配置
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

// LoggingConfig 日志配置
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Output string `yaml:"output"`
}

// LimitsConfig 限制配置
type LimitsConfig struct {
	MaxSessions         int `yaml:"max_sessions"`
	MaxBandwidthMbps    int `yaml:"max_bandwidth_mbps"`
	PerUserBandwidthMbps int `yaml:"per_user_bandwidth_mbps"`
}

// TLSConfig TLS 配置
type TLSConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Cert       string `yaml:"cert"`
	Key        string `yaml:"key"`
	CA         string `yaml:"ca"`
	ServerName string `yaml:"server_name"`
}

// AuthConfig 认证配置
type AuthConfig struct {
	Enabled bool       `yaml:"enabled"`
	Users   []UserAuth `yaml:"users"`
}

// UserAuth 用户认证
type UserAuth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// DeepCopy 返回 Config 的深拷贝
func (c *Config) DeepCopy() *Config {
	if c == nil {
		return nil
	}
	cp := *c

	// Node.Tags
	if c.Node.Tags != nil {
		cp.Node.Tags = make(map[string]string, len(c.Node.Tags))
		for k, v := range c.Node.Tags {
			cp.Node.Tags[k] = v
		}
	}

	// Inbounds
	if c.Inbounds != nil {
		cp.Inbounds = make([]InboundConfig, len(c.Inbounds))
		for i, in := range c.Inbounds {
			cp.Inbounds[i] = in
			if in.Auth.Users != nil {
				cp.Inbounds[i].Auth.Users = make([]UserAuth, len(in.Auth.Users))
				copy(cp.Inbounds[i].Auth.Users, in.Auth.Users)
			}
		}
	}

	// Outbounds
	if c.Outbounds != nil {
		cp.Outbounds = make([]OutboundConfig, len(c.Outbounds))
		copy(cp.Outbounds, c.Outbounds)
	}

	// Routing.Rules
	if c.Routing.Rules != nil {
		cp.Routing.Rules = make([]RoutingRule, len(c.Routing.Rules))
		copy(cp.Routing.Rules, c.Routing.Rules)
	}

	return &cp
}
