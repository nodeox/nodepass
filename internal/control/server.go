package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// NodeStatus 节点状态
type NodeStatus struct {
	ActiveSessions int64   `json:"active_sessions"`
	TotalBytesIn   int64   `json:"total_bytes_in"`
	TotalBytesOut  int64   `json:"total_bytes_out"`
	CPUUsage       float64 `json:"cpu_usage"`
	MemUsage       float64 `json:"mem_usage"`
}

// NodeInfo 节点信息
type NodeInfo struct {
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	Tags          map[string]string `json:"tags"`
	Token         string            `json:"token"`
	Status        *NodeStatus       `json:"status"`
	LastSeen      time.Time         `json:"last_seen"`
	ConfigVersion int64             `json:"config_version"`
	ConfigData    []byte            `json:"-"` // 不在 ListNodes 中暴露
}

// SessionStats 会话统计
type SessionStats struct {
	SessionID  string `json:"session_id"`
	UserID     string `json:"user_id"`
	BytesIn    int64  `json:"bytes_in"`
	BytesOut   int64  `json:"bytes_out"`
	DurationMs int64  `json:"duration_ms"`
}

// RegisterRequest 注册请求
type RegisterRequest struct {
	NodeID   string            `json:"node_id"`
	NodeType string            `json:"node_type"`
	Tags     map[string]string `json:"tags"`
}

// RegisterResponse 注册响应
type RegisterResponse struct {
	Token             string `json:"token"`
	HeartbeatInterval int64  `json:"heartbeat_interval"`
}

// HeartbeatRequest 心跳请求
type HeartbeatRequest struct {
	NodeID string      `json:"node_id"`
	Token  string      `json:"token"`
	Status *NodeStatus `json:"status"`
}

// HeartbeatResponse 心跳响应
type HeartbeatResponse struct {
	ConfigUpdated bool `json:"config_updated"`
}

// ReportStatsRequest 上报统计请求
type ReportStatsRequest struct {
	NodeID   string         `json:"node_id"`
	Sessions []SessionStats `json:"sessions"`
}

// ReportStatsResponse 上报统计响应
type ReportStatsResponse struct {
	Success bool `json:"success"`
}

// GetConfigRequest 获取配置请求
type GetConfigRequest struct {
	NodeID        string `json:"node_id"`
	Token         string `json:"token"`
	ConfigVersion int64  `json:"config_version"`
}

// GetConfigResponse 获取配置响应
type GetConfigResponse struct {
	ConfigVersion int64  `json:"config_version"`
	ConfigData    []byte `json:"config_data,omitempty"`
	Updated       bool   `json:"updated"`
}

// Server 控制平面服务端
type Server struct {
	logger *zap.Logger
	nodes  map[string]*NodeInfo
	mu     sync.RWMutex

	httpServer *http.Server
}

// NewServer 创建控制平面服务
func NewServer(logger *zap.Logger) *Server {
	return &Server{
		logger: logger,
		nodes:  make(map[string]*NodeInfo),
	}
}

// Register 注册节点
func (s *Server) Register(req *RegisterRequest) (*RegisterResponse, error) {
	if req.NodeID == "" || req.NodeType == "" {
		return nil, fmt.Errorf("node_id and node_type are required")
	}

	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	s.mu.Lock()
	s.nodes[req.NodeID] = &NodeInfo{
		ID:       req.NodeID,
		Type:     req.NodeType,
		Tags:     req.Tags,
		Token:    token,
		LastSeen: time.Now(),
	}
	s.mu.Unlock()

	s.logger.Info("node registered",
		zap.String("node_id", req.NodeID),
		zap.String("node_type", req.NodeType),
	)

	return &RegisterResponse{
		Token:             token,
		HeartbeatInterval: 10,
	}, nil
}

// Heartbeat 处理心跳
func (s *Server) Heartbeat(req *HeartbeatRequest) (*HeartbeatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[req.NodeID]
	if !ok {
		return nil, fmt.Errorf("node not found: %s", req.NodeID)
	}

	if node.Token != req.Token {
		return nil, fmt.Errorf("invalid token")
	}

	node.Status = req.Status
	node.LastSeen = time.Now()

	return &HeartbeatResponse{
		ConfigUpdated: false,
	}, nil
}

// ReportStats 上报统计
func (s *Server) ReportStats(req *ReportStatsRequest) (*ReportStatsResponse, error) {
	s.mu.RLock()
	_, ok := s.nodes[req.NodeID]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("node not found: %s", req.NodeID)
	}

	s.logger.Debug("stats reported",
		zap.String("node_id", req.NodeID),
		zap.Int("sessions", len(req.Sessions)),
	)

	return &ReportStatsResponse{Success: true}, nil
}

// GetNode 获取节点信息
func (s *Server) GetNode(nodeID string) (*NodeInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	node, ok := s.nodes[nodeID]
	return node, ok
}

// GetConfig 获取节点配置
func (s *Server) GetConfig(req *GetConfigRequest) (*GetConfigResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	node, ok := s.nodes[req.NodeID]
	if !ok {
		return nil, fmt.Errorf("node not found: %s", req.NodeID)
	}

	if node.Token != req.Token {
		return nil, fmt.Errorf("invalid token")
	}

	// 如果客户端已有最新版本，不返回配置数据
	if req.ConfigVersion >= node.ConfigVersion {
		return &GetConfigResponse{
			ConfigVersion: node.ConfigVersion,
			Updated:       false,
		}, nil
	}

	return &GetConfigResponse{
		ConfigVersion: node.ConfigVersion,
		ConfigData:    node.ConfigData,
		Updated:       true,
	}, nil
}

// SetNodeConfig 设置节点配置（由管理端调用）
func (s *Server) SetNodeConfig(nodeID string, configData []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[nodeID]
	if !ok {
		return fmt.Errorf("node not found: %s", nodeID)
	}

	node.ConfigVersion++
	node.ConfigData = configData

	s.logger.Info("node config updated",
		zap.String("node_id", nodeID),
		zap.Int64("version", node.ConfigVersion),
	)

	return nil
}

// ListNodes 列出所有节点
func (s *Server) ListNodes() []*NodeInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nodes := make([]*NodeInfo, 0, len(s.nodes))
	for _, n := range s.nodes {
		nodes = append(nodes, n)
	}
	return nodes
}

// Start 启动 HTTP 服务
func (s *Server) Start(listen string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/register", s.handleRegister)
	mux.HandleFunc("/api/v1/heartbeat", s.handleHeartbeat)
	mux.HandleFunc("/api/v1/stats", s.handleReportStats)
	mux.HandleFunc("/api/v1/nodes", s.handleListNodes)
	mux.HandleFunc("/api/v1/config", s.handleGetConfig)
	mux.HandleFunc("/api/v1/config/set", s.handleSetConfig)

	s.httpServer = &http.Server{
		Addr:    listen,
		Handler: mux,
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listen, err)
	}

	s.logger.Info("control plane server started", zap.String("listen", ln.Addr().String()))

	go s.httpServer.Serve(ln)
	return nil
}

// StartOnListener 在指定 listener 上启动（用于测试）
func (s *Server) StartOnListener(ln net.Listener) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/register", s.handleRegister)
	mux.HandleFunc("/api/v1/heartbeat", s.handleHeartbeat)
	mux.HandleFunc("/api/v1/stats", s.handleReportStats)
	mux.HandleFunc("/api/v1/nodes", s.handleListNodes)
	mux.HandleFunc("/api/v1/config", s.handleGetConfig)
	mux.HandleFunc("/api/v1/config/set", s.handleSetConfig)

	s.httpServer = &http.Server{Handler: mux}
	go s.httpServer.Serve(ln)
}

// Stop 停止服务
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

// HTTP handlers

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resp, err := s.Register(&req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resp, err := s.Heartbeat(&req)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleReportStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ReportStatsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resp, err := s.ReportStats(&req)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	nodes := s.ListNodes()
	writeJSON(w, http.StatusOK, nodes)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req GetConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resp, err := s.GetConfig(&req)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// SetConfigRequest 设置节点配置请求
type SetConfigRequest struct {
	NodeID     string `json:"node_id"`
	ConfigData []byte `json:"config_data"`
}

func (s *Server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SetConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if err := s.SetNodeConfig(req.NodeID, req.ConfigData); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
