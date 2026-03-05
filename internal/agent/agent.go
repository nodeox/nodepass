package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
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

// PartialReloadError indicates that ReloadConfig partially succeeded:
// some changes were applied and a reconciled config matching the actual
// running state has been stored internally. Callers should NOT retry
// the same config change, as that would attempt to re-apply already
// completed operations.
type PartialReloadError struct {
	Err error
}

func (e *PartialReloadError) Error() string {
	return fmt.Sprintf("partial reload: %v", e.Err)
}

func (e *PartialReloadError) Unwrap() error {
	return e.Err
}

// RollbackIncompleteError indicates that a rollback after a failed operation
// did not fully succeed. Some resources (listeners, connections) may still be
// running in the background but are no longer tracked by the Agent.
type RollbackIncompleteError struct {
	// Cause is the original error that triggered the rollback.
	Cause error
	// RollbackErrors lists the resources that failed to stop.
	RollbackErrors []string
}

func (e *RollbackIncompleteError) Error() string {
	return fmt.Sprintf("%v; rollback incomplete: %s", e.Cause, strings.Join(e.RollbackErrors, "; "))
}

func (e *RollbackIncompleteError) Unwrap() error {
	return e.Cause
}

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
	reloadMu           sync.Mutex // serializes ReloadConfig calls

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

	a.config.Store(cfg.DeepCopy())

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

	// 取消上下文（通知所有协程退出）
	a.cancel()

	// 停止所有入站（停止接收新连接）
	var stopErrors []error
	a.mu.Lock()
	for name, ib := range a.inbounds {
		a.logger.Info("stopping inbound", zap.String("name", name))
		if err := ib.Stop(); err != nil {
			a.logger.Error("failed to stop inbound",
				zap.String("name", name),
				zap.Error(err),
			)
			stopErrors = append(stopErrors, fmt.Errorf("inbound %s: %w", name, err))
		}
	}
	a.mu.Unlock()

	// 等待所有协程结束（包括 configWatchLoop）
	// 注意：不持有 reloadMu，避免与 configWatchLoop 中的 ReloadConfig() 死锁
	a.logger.Info("waiting for goroutines to finish")
	a.wg.Wait()

	a.state.Store(StateStopped)
	observability.AgentStateGauge.Set(float64(StateStopped))

	a.logger.Info("agent stopped")

	if len(stopErrors) > 0 {
		return fmt.Errorf("agent stopped with %d error(s): %v", len(stopErrors), stopErrors)
	}

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

// ReloadConfig 重新加载配置。
// 串行化执行：同一时刻只有一个 reload 操作在运行，且与 Stop() 互斥。
func (a *Agent) ReloadConfig(newCfg *common.Config) error {
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()

	// 检查 agent 是否仍在运行（Stop 可能已完成或正在进行）
	if a.state.Load() != StateRunning {
		return fmt.Errorf("agent not running, cannot reload config")
	}

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

	// Commit 阶段：停旧启新，完成后再替换配置
	if err := a.commitDiff(prep, oldCfg, newCfg); err != nil {
		observability.ConfigReloadErrorsTotal.Inc()
		return fmt.Errorf("failed to commit config: %w", err)
	}

	a.config.Store(newCfg.DeepCopy())

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
// For new inbounds (not replacing existing ones): starts first, confirms ready, then registers.
// For updated inbounds (replacing existing ones): must stop old first (to free the port),
// then start new. If old Stop() fails, the update is skipped to preserve the management handle.
// On partial update failure, a reconciled config matching actual running state is stored.
// Removed inbounds and outbound changes are applied atomically under lock.
func (a *Agent) commitDiff(prep *prepareResult, oldCfg, newCfg *common.Config) error {
	// Separate new inbounds into "added" (no existing handler) vs "updated" (replacing existing).
	a.mu.RLock()
	addedInbounds := make(map[string]common.InboundHandler)
	updatedInbounds := make(map[string]common.InboundHandler)
	for listen, newIb := range prep.newInbounds {
		if _, exists := a.inbounds[listen]; exists {
			updatedInbounds[listen] = newIb
		} else {
			addedInbounds[listen] = newIb
		}
	}
	a.mu.RUnlock()

	// Phase 1: Start all "added" inbounds (no port conflict) and wait for Ready.
	// If any fails, stop all of them and return error — no running state was changed.
	if len(addedInbounds) > 0 {
		if err := a.startAndWaitInbounds(addedInbounds); err != nil {
			// If rollback failed (RollbackIncompleteError), resources are leaked.
			// Wrap in PartialReloadError to signal configWatchLoop to advance lastConfigModTime.
			var rbErr *RollbackIncompleteError
			if errors.As(err, &rbErr) {
				return &PartialReloadError{Err: err}
			}
			return err
		}
	}

	// Phase 2: Handle updated inbounds FIRST (before any removes/adds).
	// Track which updates succeeded vs failed for config reconciliation.
	var updateErrors []string
	successfulUpdates := make(map[string]bool) // listen → true if update succeeded
	failedAndRemoved := make(map[string]bool)  // listen → true if handler was removed from map
	if len(updatedInbounds) > 0 {
		a.mu.Lock()
		for listen, newIb := range updatedInbounds {
			if oldIb, ok := a.inbounds[listen]; ok {
				a.logger.Info("updating inbound", zap.String("listen", listen))
				if err := oldIb.Stop(); err != nil {
					// 旧 Stop 失败 → 保留旧 handler，跳过本次更新
					a.logger.Error("failed to stop old inbound, keeping existing",
						zap.String("listen", listen),
						zap.Error(err),
					)
					newIb.Stop() // 清理未使用的新 handler
					updateErrors = append(updateErrors, fmt.Sprintf("inbound %s: old stop failed: %v", listen, err))
					continue
				}
			}

			// Start the new inbound and wait for Ready
			a.wg.Add(1)
			startErr := make(chan error, 1)
			go func(handler common.InboundHandler, addr string) {
				defer a.wg.Done()
				err := handler.Start(a.ctx, a.router)
				if err != nil {
					startErr <- err
				}
			}(newIb, listen)

			// Wait for Ready or Start failure (release lock during wait)
			a.mu.Unlock()
			readyCh := make(chan struct{})
			go func() {
				newIb.WaitReady()
				close(readyCh)
			}()

			var bindErr error
			select {
			case <-readyCh:
				// OK
			case err := <-startErr:
				bindErr = err
			case <-a.ctx.Done():
				// Agent is stopping, treat as failure
				bindErr = fmt.Errorf("agent stopping during inbound update")
			}
			a.mu.Lock()

			if bindErr != nil {
				a.logger.Error("updated inbound failed to start",
					zap.String("listen", listen),
					zap.Error(bindErr),
				)
				// Stop newIb to release WaitReady goroutine (Stop closes ready via readyOnce)
				newIb.Stop()
				// Old is already stopped, new failed — remove from map
				delete(a.inbounds, listen)
				failedAndRemoved[listen] = true
				updateErrors = append(updateErrors, fmt.Sprintf("inbound %s: %v", listen, bindErr))
				continue
			}

			a.inbounds[listen] = newIb
			successfulUpdates[listen] = true
		}
		a.mu.Unlock()
	}

	// If any updated inbound failed, roll back Phase 1 added inbounds and return error.
	if len(updateErrors) > 0 {
		var rollbackErrors []string
		for listen, ib := range addedInbounds {
			if err := ib.Stop(); err != nil {
				a.logger.Error("failed to stop added inbound during rollback",
					zap.String("listen", listen),
					zap.Error(err),
				)
				rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s: %v", listen, err))
			}
		}

		cause := fmt.Errorf("updated inbound(s) failed: %s", strings.Join(updateErrors, "; "))

		// Wrap with RollbackIncompleteError if any rollback Stop() failed
		var underlying error
		if len(rollbackErrors) > 0 {
			underlying = &RollbackIncompleteError{Cause: cause, RollbackErrors: rollbackErrors}
		} else {
			underlying = cause
		}

		// Distinguish: did any state actually change?
		// - successfulUpdates: old stopped + new running (config changed)
		// - failedAndRemoved: old stopped + new failed (handler lost from map)
		// - rollbackErrors: added inbounds failed to stop (resources leaked)
		// If neither state change nor rollback failure, all failures were "old Stop failed"
		// → old handlers still running, no state change → return regular error so configWatchLoop will retry.
		if len(successfulUpdates) > 0 || len(failedAndRemoved) > 0 || len(rollbackErrors) > 0 {
			a.storeReconciledConfig(oldCfg, newCfg, successfulUpdates, failedAndRemoved)
			return &PartialReloadError{Err: underlying}
		}
		return underlying
	}

	// Phase 3: Apply remaining changes under lock (removes, adds, outbounds, routing).
	a.mu.Lock()
	defer a.mu.Unlock()

	// 3a. Stop and remove deleted inbounds
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

	// 3b. Register added inbounds (already confirmed running from Phase 1)
	for listen, newIb := range addedInbounds {
		a.inbounds[listen] = newIb
	}

	// 3c. Remove old outbounds
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

	// 3d. Register new outbounds
	for name, newOut := range prep.newOutbounds {
		a.logger.Info("adding outbound", zap.String("name", name))
		a.outbounds[name] = newOut
		a.router.AddOutbound(newOut)
	}

	// 3e. Update routing rules if changed
	if prep.routingChanged {
		a.logger.Info("updating routing rules")
		a.router.UpdateRules(prep.routingRules)
	}

	return nil
}

// storeReconciledConfig 在 updated inbound 部分失败后，构建并存储与运行态一致的配置。
// 成功更新的 inbound 使用 newCfg 的配置，未变更的保留 oldCfg 配置，
// 失败且已从 map 中移除的 inbound 不出现在配置中。
// Outbound 和 Routing 未变更，沿用 oldCfg。
func (a *Agent) storeReconciledConfig(oldCfg, newCfg *common.Config, successfulUpdates, failedAndRemoved map[string]bool) {
	reconciled := oldCfg.DeepCopy()

	// 构建 newCfg 的 inbound 索引
	newInboundMap := make(map[string]common.InboundConfig)
	for _, in := range newCfg.Inbounds {
		newInboundMap[in.Listen] = in
	}

	var inbounds []common.InboundConfig
	for _, in := range oldCfg.Inbounds {
		if failedAndRemoved[in.Listen] {
			// handler 已从 map 中移除（旧停成功、新启动失败），跳过
			continue
		}
		if successfulUpdates[in.Listen] {
			// 更新成功，使用新配置
			if newIn, ok := newInboundMap[in.Listen]; ok {
				inbounds = append(inbounds, newIn)
			}
		} else {
			// 未变更或更新被跳过（旧 Stop 失败），保留旧配置
			inbounds = append(inbounds, in)
		}
	}
	reconciled.Inbounds = inbounds

	a.config.Store(reconciled)
	a.logger.Warn("stored reconciled config after partial update failure",
		zap.Int("successful_updates", len(successfulUpdates)),
		zap.Int("failed_and_removed", len(failedAndRemoved)),
	)
}

// startAndWaitInbounds starts the given inbound handlers and waits for all
// to bind successfully (WaitReady). If any fails, all are stopped and error returned.
func (a *Agent) startAndWaitInbounds(inbounds map[string]common.InboundHandler) error {
	type startResult struct {
		listen string
		err    error
	}
	startErrors := make(chan startResult, len(inbounds))
	readyWg := sync.WaitGroup{}

	for listen, newIb := range inbounds {
		readyWg.Add(1)
		a.wg.Add(1)
		go func(handler common.InboundHandler, addr string) {
			defer a.wg.Done()
			errCh := make(chan error, 1)
			go func() {
				errCh <- handler.Start(a.ctx, a.router)
			}()

			readyCh := make(chan struct{})
			go func() {
				handler.WaitReady()
				close(readyCh)
			}()

			select {
			case <-readyCh:
				readyWg.Done()
				// Wait for Start to finish (accept loop exit)
				err := <-errCh
				if err != nil && a.ctx.Err() == nil {
					a.logger.Error("inbound exited with error",
						zap.String("listen", addr),
						zap.Error(err),
					)
				}
			case err := <-errCh:
				if err != nil {
					startErrors <- startResult{listen: addr, err: err}
				}
				readyWg.Done()
			}
		}(newIb, listen)
	}

	readyWg.Wait()

	select {
	case res := <-startErrors:
		a.logger.Error("new inbound startup failed, rolling back",
			zap.String("listen", res.listen),
			zap.Error(res.err),
		)
		var rollbackErrors []string
		for listen, ib := range inbounds {
			if err := ib.Stop(); err != nil {
				a.logger.Error("failed to stop inbound during rollback",
					zap.String("listen", listen),
					zap.Error(err),
				)
				rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s: %v", listen, err))
			}
		}
		cause := fmt.Errorf("inbound %s failed to start: %w", res.listen, res.err)
		if len(rollbackErrors) > 0 {
			return &RollbackIncompleteError{Cause: cause, RollbackErrors: rollbackErrors}
		}
		return cause
	default:
		return nil
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

				newCfg, err := LoadConfig(a.configPath)
				if err != nil {
					a.logger.Error("failed to load config",
						zap.String("path", a.configPath),
						zap.Error(err),
					)
					// 不推进 lastConfigModTime，下次轮询会重试
					continue
				}

				if err := a.ReloadConfig(newCfg); err != nil {
					a.logger.Error("failed to reload config",
						zap.Error(err),
					)
					// Partial reload: some changes were applied and reconciled config stored.
					// Advance lastConfigModTime to avoid retrying the same partial-success operation.
					var partialErr *PartialReloadError
					if errors.As(err, &partialErr) {
						a.lastConfigModTime = modTime
					}
					// 不推进 lastConfigModTime，下次轮询会重试
					continue
				}

				// 只有完全成功才推进 lastConfigModTime
				a.lastConfigModTime = modTime
			}

		case <-a.ctx.Done():
			a.logger.Debug("config watch loop stopped")
			return
		}
	}
}

// initInbounds 初始化入站处理器
// 创建所有入站 handler，启动并等待所有 listener 绑定成功后才返回。
// 如果任何 listener 绑定失败，所有已启动的入站将被停止并返回错误。
func (a *Agent) initInbounds(cfg *common.Config) error {
	// Phase 1: Create all inbound handlers
	handlers := make(map[string]common.InboundHandler)
	for _, inCfg := range cfg.Inbounds {
		ib, err := inbound.New(inCfg, a.logger)
		if err != nil {
			return fmt.Errorf("failed to create inbound %s: %w", inCfg.Listen, err)
		}
		handlers[inCfg.Listen] = ib

		a.logger.Info("inbound initialized",
			zap.String("listen", inCfg.Listen),
			zap.String("protocol", inCfg.Protocol),
		)
	}

	// Phase 2: Start all inbounds and wait for bind success
	if err := a.startAndWaitInbounds(handlers); err != nil {
		return err
	}

	// Phase 3: Register under lock
	a.mu.Lock()
	for listen, ib := range handlers {
		a.inbounds[listen] = ib
	}
	a.mu.Unlock()

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

// GetConfig 获取当前配置（返回深拷贝，外部修改不影响内部状态）
func (a *Agent) GetConfig() *common.Config {
	return a.config.Load().(*common.Config).DeepCopy()
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
