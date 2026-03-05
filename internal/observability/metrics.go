package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Agent 状态
	AgentStateGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "np_agent_state",
			Help: "Agent state (0=stopped, 1=starting, 2=running, 3=stopping)",
		},
	)

	// 活跃会话数
	SessionActiveTotal = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "np_session_active_total",
			Help: "Current number of active sessions",
		},
	)

	// 会话总数
	SessionTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "np_session_total",
			Help: "Total number of sessions",
		},
	)

	// 会话关闭总数
	SessionClosedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "np_session_closed_total",
			Help: "Total number of closed sessions",
		},
	)

	// 出站健康分值
	OutboundHealthScore = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "np_outbound_health_score",
			Help: "Outbound health score (0-1)",
		},
		[]string{"outbound", "group"},
	)

	// 节点延迟
	NodeLatencyMs = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "np_node_latency_ms",
			Help:    "Node latency distribution in milliseconds",
			Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000},
		},
		[]string{"node", "group"},
	)

	// 配置重载次数
	ConfigReloadTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "np_config_reload_total",
			Help: "Total number of config reloads",
		},
	)

	// 配置重载失败次数
	ConfigReloadErrorsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "np_config_reload_errors_total",
			Help: "Total number of config reload errors",
		},
	)

	// 入站连接总数
	InboundConnectionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "np_inbound_connections_total",
			Help: "Total number of inbound connections",
		},
		[]string{"protocol", "listen"},
	)

	// 出站拨号总数
	OutboundDialsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "np_outbound_dials_total",
			Help: "Total number of outbound dials",
		},
		[]string{"outbound", "status"}, // status: success, failure
	)

	// 出站拨号延迟
	OutboundDialDurationMs = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "np_outbound_dial_duration_ms",
			Help:    "Outbound dial duration in milliseconds",
			Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000},
		},
		[]string{"outbound"},
	)

	// 字节传输统计
	BytesTransferred = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "np_bytes_transferred_total",
			Help: "Total bytes transferred",
		},
		[]string{"direction"}, // direction: in, out
	)

	// Goroutine 数量
	GoroutineCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "np_goroutine_count",
			Help: "Current number of goroutines",
		},
	)
)
