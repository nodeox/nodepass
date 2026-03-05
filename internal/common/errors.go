package common

import "errors"

var (
	// ErrInvalidConfig 无效配置
	ErrInvalidConfig = errors.New("invalid config")

	// ErrNoAvailableOutbound 无可用出站
	ErrNoAvailableOutbound = errors.New("no available outbound")

	// ErrDialTimeout 拨号超时
	ErrDialTimeout = errors.New("dial timeout")

	// ErrAuthFailed 认证失败
	ErrAuthFailed = errors.New("authentication failed")

	// ErrRateLimitExceeded 超过速率限制
	ErrRateLimitExceeded = errors.New("rate limit exceeded")

	// ErrCircuitBreakerOpen 熔断器打开
	ErrCircuitBreakerOpen = errors.New("circuit breaker open")

	// ErrInvalidProtocol 无效协议
	ErrInvalidProtocol = errors.New("invalid protocol")

	// ErrReplayAttack 重放攻击
	ErrReplayAttack = errors.New("replay attack detected")

	// ErrInvalidMagic 无效的魔数
	ErrInvalidMagic = errors.New("invalid magic number")

	// ErrUnsupportedVersion 不支持的版本
	ErrUnsupportedVersion = errors.New("unsupported version")
)
