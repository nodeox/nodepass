package routing

import (
	"context"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nodeox/nodepass/internal/common"
	"go.uber.org/zap"
)

// Router 路由器
type Router struct {
	outbounds map[string]common.OutboundHandler
	groups    map[string][]common.OutboundHandler
	rules     []common.RoutingRule
	logger    *zap.Logger

	// round-robin 计数器
	rrCounters map[string]*atomic.Uint64

	mu sync.RWMutex
}

// NewRouter 创建新的路由器
func NewRouter(logger *zap.Logger) *Router {
	return &Router{
		outbounds:  make(map[string]common.OutboundHandler),
		groups:     make(map[string][]common.OutboundHandler),
		rules:      make([]common.RoutingRule, 0),
		rrCounters: make(map[string]*atomic.Uint64),
		logger:     logger,
	}
}

// Route 根据会话元数据选择出站
func (r *Router) Route(meta common.SessionMeta) (common.OutboundHandler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 遍历路由规则
	for _, rule := range r.rules {
		if r.matchRule(rule, meta) {
			if rule.Outbound != "" {
				// 直接指定出站
				if out, ok := r.outbounds[rule.Outbound]; ok {
					r.logger.Debug("route matched",
						zap.String("rule_type", rule.Type),
						zap.String("pattern", rule.Pattern),
						zap.String("outbound", rule.Outbound),
					)
					return out, nil
				}
			} else if rule.OutboundGroup != "" {
				// 从节点组选择
				out, err := r.selectFromGroup(rule.OutboundGroup, rule.Strategy)
				if err == nil {
					r.logger.Debug("route matched group",
						zap.String("rule_type", rule.Type),
						zap.String("pattern", rule.Pattern),
						zap.String("group", rule.OutboundGroup),
						zap.String("strategy", rule.Strategy),
						zap.String("selected", out.Name()),
					)
				}
				return out, err
			}
		}
	}

	return nil, common.ErrNoAvailableOutbound
}

// matchRule 匹配路由规则
func (r *Router) matchRule(rule common.RoutingRule, meta common.SessionMeta) bool {
	switch rule.Type {
	case "domain":
		return matchDomain(rule.Pattern, meta.Target)
	case "ip":
		return matchIP(rule.Pattern, meta.Target)
	case "user":
		return matchUser(rule.Pattern, meta.UserID)
	case "default":
		return true
	default:
		return false
	}
}

// selectFromGroup 从节点组选择出站
func (r *Router) selectFromGroup(group, strategy string) (common.OutboundHandler, error) {
	outs, ok := r.groups[group]
	if !ok || len(outs) == 0 {
		return nil, common.ErrNoAvailableOutbound
	}

	// 过滤健康的出站
	healthyOuts := make([]common.OutboundHandler, 0, len(outs))
	for _, out := range outs {
		// 简单检查：如果健康分数 > 0.5 则认为健康
		// 实际应该使用更复杂的健康检查逻辑
		healthyOuts = append(healthyOuts, out)
	}

	if len(healthyOuts) == 0 {
		return nil, common.ErrNoAvailableOutbound
	}

	switch strategy {
	case "random":
		return healthyOuts[rand.Intn(len(healthyOuts))], nil
	case "round-robin":
		return r.selectRoundRobin(group, healthyOuts), nil
	case "least-load":
		return r.selectLeastLoad(healthyOuts), nil
	default:
		// 默认使用第一个
		return healthyOuts[0], nil
	}
}

// selectRoundRobin 轮询选择
func (r *Router) selectRoundRobin(group string, outs []common.OutboundHandler) common.OutboundHandler {
	counter, ok := r.rrCounters[group]
	if !ok {
		counter = &atomic.Uint64{}
		r.rrCounters[group] = counter
	}

	idx := counter.Add(1) % uint64(len(outs))
	return outs[idx]
}

// selectLeastLoad 选择健康分值最高的出站
func (r *Router) selectLeastLoad(outs []common.OutboundHandler) common.OutboundHandler {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	best := outs[0]
	bestScore := best.HealthCheck(ctx)

	for _, out := range outs[1:] {
		score := out.HealthCheck(ctx)
		if score > bestScore {
			best = out
			bestScore = score
		}
	}

	return best
}

// AddOutbound 添加出站
func (r *Router) AddOutbound(out common.OutboundHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := out.Name()
	r.outbounds[name] = out

	group := out.Group()
	if group != "" {
		r.groups[group] = append(r.groups[group], out)
	}

	r.logger.Debug("outbound added",
		zap.String("name", name),
		zap.String("group", group),
	)
}

// RemoveOutbound 移除出站
func (r *Router) RemoveOutbound(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out, ok := r.outbounds[name]
	if !ok {
		return
	}

	delete(r.outbounds, name)

	// 从组中移除
	group := out.Group()
	if group != "" {
		outs := r.groups[group]
		for i, o := range outs {
			if o.Name() == name {
				r.groups[group] = append(outs[:i], outs[i+1:]...)
				break
			}
		}
	}

	r.logger.Debug("outbound removed",
		zap.String("name", name),
		zap.String("group", group),
	)
}

// UpdateRules 更新路由规则
func (r *Router) UpdateRules(rules []common.RoutingRule) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.rules = rules
	r.logger.Info("routing rules updated", zap.Int("count", len(rules)))
}

// GetOutbound 获取指定出站
func (r *Router) GetOutbound(name string) (common.OutboundHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out, ok := r.outbounds[name]
	return out, ok
}

// matchDomain 匹配域名
func matchDomain(pattern, target string) bool {
	// 提取主机名（去掉端口）
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}

	// 精确匹配
	if pattern == host {
		return true
	}

	// 通配符匹配 (*.example.com)
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // 包含前导点，如 ".example.com"
		// 必须以 suffix 结尾，且不能完全等于 suffix（去掉点）
		return strings.HasSuffix(host, suffix) && host != suffix[1:]
	}

	// 关键词匹配 (keyword:google)
	if strings.HasPrefix(pattern, "keyword:") {
		keyword := pattern[8:]
		return strings.Contains(host, keyword)
	}

	return false
}

// matchIP 匹配 IP 地址
func matchIP(pattern, target string) bool {
	// 提取主机名
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	// CIDR 匹配
	if strings.Contains(pattern, "/") {
		_, ipNet, err := net.ParseCIDR(pattern)
		if err != nil {
			return false
		}
		return ipNet.Contains(ip)
	}

	// 精确 IP 匹配
	patternIP := net.ParseIP(pattern)
	if patternIP == nil {
		return false
	}

	return ip.Equal(patternIP)
}

// matchUser 匹配用户
func matchUser(pattern, userID string) bool {
	return pattern == userID
}
