package mux

import (
	"sync/atomic"
)

// WindowFlowController 窗口式流控器
type WindowFlowController struct {
	maxWindow int64
	consumed  atomic.Int64
	buffered  atomic.Int64
}

// NewWindowFlowController 创建流控器
func NewWindowFlowController(maxWindow int64) *WindowFlowController {
	return &WindowFlowController{
		maxWindow: maxWindow,
	}
}

// ShouldPause 是否应该暂停读取
func (w *WindowFlowController) ShouldPause() bool {
	return w.buffered.Load() > w.maxWindow
}

// OnConsumed 通知已消费 n 字节
func (w *WindowFlowController) OnConsumed(n int) {
	w.consumed.Add(int64(n))
	w.buffered.Add(-int64(n))
}

// OnBuffered 通知已缓冲 n 字节
func (w *WindowFlowController) OnBuffered(n int) {
	w.buffered.Add(int64(n))
}

// WindowSize 获取当前窗口大小
func (w *WindowFlowController) WindowSize() int {
	remaining := w.maxWindow - w.buffered.Load()
	if remaining < 0 {
		return 0
	}
	return int(remaining)
}

// Reset 重置窗口
func (w *WindowFlowController) Reset() {
	w.consumed.Store(0)
	w.buffered.Store(0)
}

// GetStats 获取统计信息
func (w *WindowFlowController) GetStats() (consumed, buffered int64) {
	return w.consumed.Load(), w.buffered.Load()
}
