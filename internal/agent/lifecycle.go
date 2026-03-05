package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Component 可管理的生命周期组件接口
type Component interface {
	// Start 启动组件
	Start(ctx context.Context) error

	// Stop 停止组件
	Stop() error

	// Name 组件名称
	Name() string
}

// LifecycleManager 组件生命周期管理器
type LifecycleManager struct {
	components []Component
	started    []Component // 已成功启动的组件（用于回滚）
	logger     *zap.Logger
	mu         sync.Mutex
}

// NewLifecycleManager 创建生命周期管理器
func NewLifecycleManager(logger *zap.Logger) *LifecycleManager {
	return &LifecycleManager{
		logger: logger,
	}
}

// Register 注册组件（按注册顺序启动）
func (lm *LifecycleManager) Register(c Component) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.components = append(lm.components, c)
}

// StartAll 按注册顺序启动所有组件，失败时回滚已启动的组件
func (lm *LifecycleManager) StartAll(ctx context.Context) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	lm.started = lm.started[:0]

	for _, c := range lm.components {
		lm.logger.Info("starting component", zap.String("name", c.Name()))

		if err := c.Start(ctx); err != nil {
			lm.logger.Error("failed to start component",
				zap.String("name", c.Name()),
				zap.Error(err),
			)
			// 回滚已启动的组件（逆序）
			lm.rollbackLocked()
			return fmt.Errorf("failed to start component %s: %w", c.Name(), err)
		}

		lm.started = append(lm.started, c)
		lm.logger.Info("component started", zap.String("name", c.Name()))
	}

	return nil
}

// StopAll 逆序停止所有已启动的组件（LIFO）
func (lm *LifecycleManager) StopAll() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	return lm.stopAllLocked()
}

// stopAllLocked 在持锁状态下逆序停止所有已启动的组件
func (lm *LifecycleManager) stopAllLocked() error {
	var firstErr error

	for i := len(lm.started) - 1; i >= 0; i-- {
		c := lm.started[i]
		lm.logger.Info("stopping component", zap.String("name", c.Name()))

		if err := c.Stop(); err != nil {
			lm.logger.Error("failed to stop component",
				zap.String("name", c.Name()),
				zap.Error(err),
			)
			if firstErr == nil {
				firstErr = fmt.Errorf("failed to stop component %s: %w", c.Name(), err)
			}
		}
	}

	lm.started = lm.started[:0]
	return firstErr
}

// rollbackLocked 在持锁状态下回滚已启动的组件（逆序停止）
func (lm *LifecycleManager) rollbackLocked() {
	lm.logger.Warn("rolling back started components",
		zap.Int("count", len(lm.started)),
	)

	for i := len(lm.started) - 1; i >= 0; i-- {
		c := lm.started[i]
		lm.logger.Info("rolling back component", zap.String("name", c.Name()))

		if err := c.Stop(); err != nil {
			lm.logger.Error("failed to rollback component",
				zap.String("name", c.Name()),
				zap.Error(err),
			)
		}
	}

	lm.started = lm.started[:0]
}

// GracefulShutdown 带超时的优雅关闭
func (lm *LifecycleManager) GracefulShutdown(timeout time.Duration) error {
	done := make(chan error, 1)

	go func() {
		done <- lm.StopAll()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("graceful shutdown timed out after %v", timeout)
	}
}

// Components 返回已注册的组件列表（仅用于测试/调试）
func (lm *LifecycleManager) Components() []Component {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	result := make([]Component, len(lm.components))
	copy(result, lm.components)
	return result
}

// Started 返回已启动的组件列表（仅用于测试/调试）
func (lm *LifecycleManager) Started() []Component {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	result := make([]Component, len(lm.started))
	copy(result, lm.started)
	return result
}
