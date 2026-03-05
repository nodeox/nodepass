package mux

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWindowFlowController_Basic(t *testing.T) {
	fc := NewWindowFlowController(1024)

	assert.False(t, fc.ShouldPause())
	assert.Equal(t, 1024, fc.WindowSize())
}

func TestWindowFlowController_ShouldPause(t *testing.T) {
	fc := NewWindowFlowController(100)

	// 缓冲 50 字节，不应暂停
	fc.OnBuffered(50)
	assert.False(t, fc.ShouldPause())
	assert.Equal(t, 50, fc.WindowSize())

	// 缓冲到 101 字节，应暂停
	fc.OnBuffered(51)
	assert.True(t, fc.ShouldPause())
	assert.Equal(t, 0, fc.WindowSize())
}

func TestWindowFlowController_OnConsumed(t *testing.T) {
	fc := NewWindowFlowController(100)

	// 缓冲 80 字节
	fc.OnBuffered(80)
	assert.Equal(t, 20, fc.WindowSize())

	// 消费 50 字节
	fc.OnConsumed(50)
	assert.Equal(t, 70, fc.WindowSize())
	assert.False(t, fc.ShouldPause())

	consumed, buffered := fc.GetStats()
	assert.Equal(t, int64(50), consumed)
	assert.Equal(t, int64(30), buffered)
}

func TestWindowFlowController_Reset(t *testing.T) {
	fc := NewWindowFlowController(100)

	fc.OnBuffered(80)
	fc.OnConsumed(30)

	fc.Reset()

	consumed, buffered := fc.GetStats()
	assert.Equal(t, int64(0), consumed)
	assert.Equal(t, int64(0), buffered)
	assert.Equal(t, 100, fc.WindowSize())
}

func TestWindowFlowController_PauseAndResume(t *testing.T) {
	fc := NewWindowFlowController(100)

	// 缓冲超过窗口
	fc.OnBuffered(150)
	assert.True(t, fc.ShouldPause())

	// 消费后恢复
	fc.OnConsumed(100)
	assert.False(t, fc.ShouldPause())
	assert.Equal(t, 50, fc.WindowSize())
}
