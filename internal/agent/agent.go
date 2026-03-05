package agent

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nodeox/nodepass/internal/common"
	"github.com/nodeox/nodepass/internal/inbound"
	"github.com/nodeox/nodepass/internal/observability"
	"github.com/nodeox/nodepass/internal/outbound"
	"github.com/nodeox/nodepass/internal/routing"
	"go.uber.org/zap"
)

// Agent 核心引擎
type Agent struct {
	config atomic.Value // *common.Config
	logger *zap.Logger

	// 组件
	inbounds  map[string]common.InboundHandler
	outbounds map[string]common.OutboundHandler
	router    common.Router

	// 生命周期
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// 状态
	state atomic.Int32 // 0: stopped, 1: starting, 2: running, 3: stopping

	// 配置热更新
	configPath         string
	configPollInterval time.Duration
	lastConfigModTime  time.Time

	mu sync.RWMutex
}

// 状态常量
const (
	StateStopped = iota
	StateStarting
	StateRunning
	StateStopping
)

// New 创建新的 Agent
func New(cfg *common.Config, logger *zap.Logger) (*Agent, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	a := &Agent{
		logger:    logger,
		inbounds:  make(map[string]common.InboundHandler),
		outbounds: make(map[string]common.OutboundHandler),
		router:    routing.NewRouter(logger),
	}

	a.config.Store(cfg)

	return a, nil
}

// Start 启动 Agent
func (a *Agent) Start(ctx context.Context) error {
	// CAS 状态检查
	if !a.state.CompareAndSwap(StateStopped, StateStarting) {
		return fmt.Errorf("agent already started")
	}

	a.ctx, a.cancel = context.WithCancel(ctx)

	cfg := a.config.Load().(*common.Config)

	a.logger.Info("starting agent",
		zap.String("node_id", cfg.Node.ID),
		zap.String("node_type", cfg.Node.Type),
		zap.Any("tags", cfg.Node.Tags),
	)

	// 更新状态指标
	observability.AgentStateGauge.Set(float64(StateStarting))

	// 初始化出站
	if err := a.initOutbounds(cfg); err != nil {
		a.state.Store(StateStopped)
		observability.AgentStateGauge.Set(float64(StateStopped))
		return fmt.Errorf("failed to init outbounds: %w", err)
	}

	// 初始化入站
	if err := a.initInbounds(cfg); err != nil {
		a.state.Store(StateStopped)
		observability.AgentStateGauge.Set(float64(StateStopped))
		return fmt.Errorf("failed to init inbounds: %w", err)
	}

	// 启动健康检查
	a.wg.Add(1)
	go a.healthCheckLoop()

	// 启动指标收集
	a.wg.Add(1)
	go a.metricsCollectionLoop()

	// 启动配置监听
	if a.configPath != "" {
		a.wg.Add(1)
		go a.configWatchLoop()
	}

	// 启动 pprof 调试端点
	if cfg.Observability.Pprof.Enabled && cfg.Observability.Pprof.Listen != "" {
		a.startPprof(cfg.Observability.Pprof.Listen)
	}

	a.state.Store(StateRunning)
	observability.AgentStateGauge.Set(float64(StateRunning))

	a.logger.Info("agent started successfully")

	return nil
}

// Stop 停止 Agent
func (a *Agent) Stop() error {
	// CAS 状态检查
	if !a.state.CompareAndSwap(StateRunning, StateStopping) {
		return fmt.Errorf("agent not running")
	}

	a.logger.Info("stopping agent")
	observability.AgentStateGauge.Set(float64(StateStopping))

	// 取消上下文
	a.cancel()

	// 停止所有入站（停止接收新连接）
	a.mu.Lock()
	for name, ib := range a.inbounds {
		a.logger.Info("stopping inbound", zap.String("name", name))
		if err := ib.Stop(); err != nil {
			a.logger.Error("failed to stop inbound",
				zap.String("name", name),
				zap.Error(err),
			)
		}
	}
	a.mu.Unlock()

	// 等待所有协程结束
	a.logger.Info("waiting for goroutines to finish")
	a.wg.Wait()

	a.state.Store(StateStopped)
	observability.AgentStateGauge.Set(float64(StateStopped))

	a.logger.Info("agent stopped")

	return nil
}

// healthCheckLoop 健康检查循环
func (a *Agent) healthCheckLoop() {
	defer a.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	a.logger.Debug("health check loop started")

	for {
		select {
		case <-ticker.C:
			a.performHealthCheck()

		case <-a.ctx.Done():
			a.logger.Debug("health check loop stopped")
			return
		}
	}
}

// performHealthCheck 执行健康检查
func (a *Agent) performHealthCheck() {
	a.mu.RLock()
	defer a.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for name, out := range a.outbounds {
		score := out.HealthCheck(ctx)

		a.logger.Debug("health check",
			zap.String("outbound", name),
			zap.String("group", out.Group()),
			zap.Float64("score", score),
		)

		// 更新指标
		observability.OutboundHealthScore.WithLabelValues(name, out.Group()).Set(score)
	}
}

// metricsCollectionLoop 指标收集循环
func (a *Agent) metricsCollectionLoop() {
	defer a.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	a.logger.Debug("metrics collection loop started")

	for {
		select {
		case <-ticker.C:
			a.collectMetrics()

		case <-a.ctx.Done():
			a.logger.Debug("metrics collection loop stopped")
			return
		}
	}
}

// collectMetrics 收集指标
func (a *Agent) collectMetrics() {
	// 收集 Goroutine 数量
	goroutineCount := runtime.NumGoroutine()
	observability.GoroutineCount.Set(float64(goroutineCount))

	a.logger.Debug("metrics collected",
		zap.Int("goroutines", goroutineCount),
	)
}

// ReloadConfig 重新加载配置
func (a *Agent) ReloadConfig(newCfg *common.Config) error {
	if err := ValidateConfig(newCfg); err != nil {
		observability.ConfigReloadErrorsTotal.Inc()
		return fmt.Errorf("invalid config: %w", err)
	}

	a.logger.Info("reloading config")

	oldCfg := a.config.Load().(*common.Config)

	// 计算配置差异
	diff := DiffConfig(oldCfg, newCfg)

	if !diff.HasChanges() {
		a.logger.Info("no config changes detected")
		return nil
	}

	a.logger.Info("config changes detected", zap.String("changes", diff.String()))

	// Prepare 阶段：创建所有新对象，任何失败立即返回，不修改运行态
	prep, err := a.prepareDiff(diff, newCfg)
	if err != nil {
		observability.ConfigReloadErrorsTotal.Inc()
		return fmt.Errorf("failed to prepare config: %w", err)
	}

	// Commit 阶段：原子替换配置并应用变更
	a.config.Store(newCfg)
	a.commitDiff(prep)

	observability.ConfigReloadTotal.Inc()
	a.logger.Info("config reloaded successfully")

	return nil
}

// GetState 获取当前状态
func (a *Agent) GetState() int32 {
	return a.state.Load()
}

// initOutbounds 初始化出站处理器
func (a *Agent) initOutbounds(cfg *common.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, outCfg := range cfg.Outbounds {
		out, err := outbound.New(outCfg, a.logger)
		if err != nil {
			return fmt.Errorf("failed to create outbound %s: %w", outCfg.Name, err)
		}

		a.outbounds[outCfg.Name] = out
		a.router.AddOutbound(out)

		a.logger.Info("outbound initialized",
			zap.String("name", outCfg.Name),
			zap.String("protocol", outCfg.Protocol),
			zap.String("group", outCfg.Group),
		)
	}

	// 更新路由规则
	a.router.UpdateRules(cfg.Routing.Rules)

	return nil
}

// prepareResult holds pre-created objects for a config diff commit.
type prepareResult struct {
	removeInbounds  []string
	newInbounds     map[string]common.InboundHandler
	removeOutbounds []string
	newOutbounds    map[string]common.OutboundHandler
	routingChanged  bool
	routingRules    []common.RoutingRule
}

// prepareDiff creates all new handlers without modifying running state.
// If any creation fails, nothing has been changed.
func (a *Agent) prepareDiff(diff *ConfigDiff, cfg *common.Config) (*prepareResult, error) {
	prep := &prepareResult{
		removeInbounds:  diff.RemovedInbounds,
		newInbounds:     make(map[string]common.InboundHandler),
		removeOutbounds: diff.RemovedOutbounds,
		newOutbounds:    make(map[string]common.OutboundHandler),
		routingChanged:  diff.RoutingChanged,
		routingRules:    cfg.Routing.Rules,
	}

	// Pre-create updated + added inbounds
	for _, inCfg := range diff.UpdatedInbounds {
		newIb, err := inbound.New(inCfg, a.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create updated inbound %s: %w", inCfg.Listen, err)
		}
		prep.newInbounds[inCfg.Listen] = newIb
	}

	for _, inCfg := range diff.AddedInbounds {
		newIb, err := inbound.New(inCfg, a.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create new inbound %s: %w", inCfg.Listen, err)
		}
		prep.newInbounds[inCfg.Listen] = newIb
	}

	// Pre-create updated + added outbounds
	for _, outCfg := range diff.UpdatedOutbounds {
		newOut, err := outbound.New(outCfg, a.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create updated outbound %s: %w", outCfg.Name, err)
		}
		prep.newOutbounds[outCfg.Name] = newOut
	}

	for _, outCfg := range diff.AddedOutbounds {
		newOut, err := outbound.New(outCfg, a.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create new outbound %s: %w", outCfg.Name, err)
		}
		prep.newOutbounds[outCfg.Name] = newOut
	}

	return prep, nil
}

// commitDiff applies prepared changes to the running state.
// This phase only does stop-old/register-new/start-new, which should not fail.
func (a *Agent) commitDiff(prep *prepareResult) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 1. Stop and remove old inbounds (removed + updated)
	for _, listen := range prep.removeInbounds {
		if ib, ok := a.inbounds[listen]; ok {
			a.logger.Info("removing inbound", zap.String("listen", listen))
			if err := ib.Stop(); err != nil {
				a.logger.Error("failed to stop removed inbound",
					zap.String("listen", listen),
					zap.Error(err),
				)
			}
			delete(a.inbounds, listen)
		}
	}

	// Stop old inbounds that are being updated (they have replacements in newInbounds)
	for listen := range prep.newInbounds {
		if oldIb, ok := a.inbounds[listen]; ok {
			a.logger.Info("updating inbound", zap.String("listen", listen))
			if err := oldIb.Stop(); err != nil {
				a.logger.Error("failed to stop old inbound",
					zap.String("listen", listen),
					zap.Error(err),
				)
			}
			delete(a.inbounds, listen)
		}
	}

	// 2. Register and start new inbounds
	for listen, newIb := range prep.newInbounds {
		a.logger.Info("starting inbound", zap.String("listen", listen))
		a.inbounds[listen] = newIb
		a.wg.Add(1)
		go func(handler common.InboundHandler, addr string) {
			defer a.wg.Done()
			if err := handler.Start(a.ctx, a.router); err != nil {
				if a.ctx.Err() == nil {
					a.logger.Error("inbound exited with error",
						zap.String("listen", addr),
						zap.Error(err),
					)
				}
			}
		}(newIb, listen)
	}

	// 3. Remove old outbounds
	for _, name := range prep.removeOutbounds {
		a.logger.Info("removing outbound", zap.String("name", name))
		a.router.RemoveOutbound(name)
		delete(a.outbounds, name)
	}

	// Stop old outbounds that are being updated
	for name := range prep.newOutbounds {
		if _, ok := a.outbounds[name]; ok {
			a.logger.Info("updating outbound", zap.String("name", name))
			a.router.RemoveOutbound(name)
			delete(a.outbounds, name)
		}
	}

	// 4. Register new outbounds
	for name, newOut := range prep.newOutbounds {
		a.logger.Info("adding outbound", zap.String("name", name))
		a.outbounds[name] = newOut
		a.router.AddOutbound(newOut)
	}

	// 5. Update routing rules if changed
	if prep.routingChanged {
		a.logger.Info("updating routing rules")
		a.router.UpdateRules(prep.routingRules)
	}
}

// SetConfigPath 设置配置文件路径（启用配置热更新）
func (a *Agent) SetConfigPath(path string) {
	a.configPath = path
	if a.configPollInterval == 0 {
		a.configPollInterval = 5 * time.Second
	}
}

// SetConfigPollInterval 设置配置轮询间隔（用于测试）
func (a *Agent) SetConfigPollInterval(d time.Duration) {
	a.configPollInterval = d
}

// getConfigModTime 获取配置文件修改时间
func (a *Agent) getConfigModTime() time.Time {
	info, err := os.Stat(a.configPath)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// configWatchLoop 配置文件监听循环（轮询方式）
func (a *Agent) configWatchLoop() {
	defer a.wg.Done()

	interval := a.configPollInterval
	if interval == 0 {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// 记录初始修改时间
	a.lastConfigModTime = a.getConfigModTime()

	a.logger.Debug("config watch loop started",
		zap.String("path", a.configPath),
		zap.Duration("interval", interval),
	)

	for {
		select {
		case <-ticker.C:
			modTime := a.getConfigModTime()
			if modTime.IsZero() {
				continue
			}

			if modTime.After(a.lastConfigModTime) {
				a.logger.Info("config file changed, reloading",
					zap.String("path", a.configPath),
				)

				a.lastConfigModTime = modTime

				newCfg, err := LoadConfig(a.configPath)
				if err != nil {
					a.logger.Error("failed to load config",
						zap.String("path", a.configPath),
						zap.Error(err),
					)
					continue
				}

				if err := a.ReloadConfig(newCfg); err != nil {
					a.logger.Error("failed to reload config",
						zap.Error(err),
					)
				}
			}

		case <-a.ctx.Done():
			a.logger.Debug("config watch loop stopped")
			return
		}
	}
}

// initInbounds 初始化入站处理器
func (a *Agent) initInbounds(cfg *common.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, inCfg := range cfg.Inbounds {
		ib, err := inbound.New(inCfg, a.logger)
		if err != nil {
			return fmt.Errorf("failed to create inbound %s: %w", inCfg.Listen, err)
		}

		a.inbounds[inCfg.Listen] = ib

		a.logger.Info("inbound initialized",
			zap.String("listen", inCfg.Listen),
			zap.String("protocol", inCfg.Protocol),
		)

		// 每个入站在独立 goroutine 中运行（Start 是阻塞的 accept 循环）
		a.wg.Add(1)
		go func(handler common.InboundHandler, listen string) {
			defer a.wg.Done()
			if err := handler.Start(a.ctx, a.router); err != nil {
				// ctx 取消导致的错误属于正常关闭
				if a.ctx.Err() == nil {
					a.logger.Error("inbound exited with error",
						zap.String("listen", listen),
						zap.Error(err),
					)
				}
			}
		}(ib, inCfg.Listen)
	}

	return nil
}

// GetStateName 获取状态名称
func (a *Agent) GetStateName() string {
	switch a.GetState() {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	default:
		return "unknown"
	}
}

// GetConfig 获取当前配置
func (a *Agent) GetConfig() *common.Config {
	return a.config.Load().(*common.Config)
}

// GetStats 获取统计信息
func (a *Agent) GetStats() map[string]interface{} {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return map[string]interface{}{
		"state":            a.GetStateName(),
		"goroutines":       runtime.NumGoroutine(),
		"inbounds_count":   len(a.inbounds),
		"outbounds_count":  len(a.outbounds),
	}
}
