package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockComponent 用于测试的 mock 组件
type mockComponent struct {
	name      string
	startErr  error
	stopErr   error
	started   bool
	stopped   bool
	startSeq  int // 启动顺序记录
	stopSeq   int // 停止顺序记录
	mu        sync.Mutex
	seqTracker *int // 共享序号追踪器
}

func newMockComponent(name string, seqTracker *int) *mockComponent {
	return &mockComponent{
		name:       name,
		seqTracker: seqTracker,
	}
}

func (m *mockComponent) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.startErr != nil {
		return m.startErr
	}
	m.started = true
	if m.seqTracker != nil {
		*m.seqTracker++
		m.startSeq = *m.seqTracker
	}
	return nil
}

func (m *mockComponent) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopErr != nil {
		return m.stopErr
	}
	m.stopped = true
	if m.seqTracker != nil {
		*m.seqTracker++
		m.stopSeq = *m.seqTracker
	}
	return nil
}

func (m *mockComponent) Name() string {
	return m.name
}

func (m *mockComponent) isStarted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started
}

func (m *mockComponent) isStopped() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopped
}

func TestLifecycleManager_StartAll(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	seq := 0

	c1 := newMockComponent("comp-1", &seq)
	c2 := newMockComponent("comp-2", &seq)
	c3 := newMockComponent("comp-3", &seq)

	lm := NewLifecycleManager(logger)
	lm.Register(c1)
	lm.Register(c2)
	lm.Register(c3)

	err := lm.StartAll(context.Background())
	require.NoError(t, err)

	// 所有组件都已启动
	assert.True(t, c1.isStarted())
	assert.True(t, c2.isStarted())
	assert.True(t, c3.isStarted())

	// 按注册顺序启动
	assert.Equal(t, 1, c1.startSeq)
	assert.Equal(t, 2, c2.startSeq)
	assert.Equal(t, 3, c3.startSeq)

	assert.Len(t, lm.Started(), 3)
}

func TestLifecycleManager_StopAll_LIFO(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	seq := 0

	c1 := newMockComponent("comp-1", &seq)
	c2 := newMockComponent("comp-2", &seq)
	c3 := newMockComponent("comp-3", &seq)

	lm := NewLifecycleManager(logger)
	lm.Register(c1)
	lm.Register(c2)
	lm.Register(c3)

	err := lm.StartAll(context.Background())
	require.NoError(t, err)

	// 重置序号追踪器
	seq = 0

	err = lm.StopAll()
	require.NoError(t, err)

	// 所有组件都已停止
	assert.True(t, c1.isStopped())
	assert.True(t, c2.isStopped())
	assert.True(t, c3.isStopped())

	// 逆序停止（LIFO）
	assert.Equal(t, 1, c3.stopSeq) // 最后注册的先停
	assert.Equal(t, 2, c2.stopSeq)
	assert.Equal(t, 3, c1.stopSeq) // 最先注册的最后停

	// 停止后 started 列表清空
	assert.Len(t, lm.Started(), 0)
}

func TestLifecycleManager_StartAll_Rollback(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	seq := 0

	c1 := newMockComponent("comp-1", &seq)
	c2 := newMockComponent("comp-2", &seq)
	c3 := newMockComponent("comp-3", &seq)
	c3.startErr = fmt.Errorf("comp-3 failed to start")

	lm := NewLifecycleManager(logger)
	lm.Register(c1)
	lm.Register(c2)
	lm.Register(c3)

	err := lm.StartAll(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "comp-3")

	// c1 和 c2 启动成功后被回滚
	assert.True(t, c1.isStarted())
	assert.True(t, c2.isStarted())
	assert.False(t, c3.isStarted()) // 启动失败

	// c1 和 c2 已被回滚停止
	assert.True(t, c1.isStopped())
	assert.True(t, c2.isStopped())

	// started 列表已清空
	assert.Len(t, lm.Started(), 0)
}

func TestLifecycleManager_StopAll_PartialError(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	seq := 0

	c1 := newMockComponent("comp-1", &seq)
	c2 := newMockComponent("comp-2", &seq)
	c2.stopErr = fmt.Errorf("comp-2 stop failed")
	c3 := newMockComponent("comp-3", &seq)

	lm := NewLifecycleManager(logger)
	lm.Register(c1)
	lm.Register(c2)
	lm.Register(c3)

	err := lm.StartAll(context.Background())
	require.NoError(t, err)

	// 即使 c2 停止失败，也应继续停止其他组件
	err = lm.StopAll()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "comp-2")

	// c1 和 c3 正常停止
	assert.True(t, c1.isStopped())
	assert.True(t, c3.isStopped())
}

func TestLifecycleManager_GracefulShutdown(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	seq := 0

	c1 := newMockComponent("comp-1", &seq)
	c2 := newMockComponent("comp-2", &seq)

	lm := NewLifecycleManager(logger)
	lm.Register(c1)
	lm.Register(c2)

	err := lm.StartAll(context.Background())
	require.NoError(t, err)

	err = lm.GracefulShutdown(5 * time.Second)
	require.NoError(t, err)

	assert.True(t, c1.isStopped())
	assert.True(t, c2.isStopped())
}

func TestLifecycleManager_GracefulShutdown_Timeout(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// 创建一个停止时会阻塞的组件
	slowComp := &slowStopComponent{name: "slow"}

	lm := NewLifecycleManager(logger)
	lm.Register(slowComp)

	err := lm.StartAll(context.Background())
	require.NoError(t, err)

	err = lm.GracefulShutdown(100 * time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

// slowStopComponent 一个停止很慢的组件
type slowStopComponent struct {
	name    string
	started bool
}

func (s *slowStopComponent) Start(_ context.Context) error {
	s.started = true
	return nil
}

func (s *slowStopComponent) Stop() error {
	time.Sleep(5 * time.Second)
	return nil
}

func (s *slowStopComponent) Name() string {
	return s.name
}

func TestLifecycleManager_Register(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	lm := NewLifecycleManager(logger)
	assert.Len(t, lm.Components(), 0)

	seq := 0
	lm.Register(newMockComponent("a", &seq))
	lm.Register(newMockComponent("b", &seq))

	assert.Len(t, lm.Components(), 2)
	assert.Equal(t, "a", lm.Components()[0].Name())
	assert.Equal(t, "b", lm.Components()[1].Name())
}
