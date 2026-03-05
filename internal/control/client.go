package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// Client 控制平面客户端
type Client struct {
	baseURL    string
	nodeID     string
	nodeType   string
	token      string
	httpClient *http.Client
	logger     *zap.Logger

	configVersion int64
}

// NewClient 创建控制平面客户端
func NewClient(baseURL, nodeID, nodeType string, logger *zap.Logger) *Client {
	return &Client{
		baseURL:  baseURL,
		nodeID:   nodeID,
		nodeType: nodeType,
		logger:   logger.With(zap.String("component", "control-client")),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Register 向控制平面注册
func (c *Client) Register(ctx context.Context, tags map[string]string) error {
	req := RegisterRequest{
		NodeID:   c.nodeID,
		NodeType: c.nodeType,
		Tags:     tags,
	}

	var resp RegisterResponse
	if err := c.post(ctx, "/api/v1/register", req, &resp); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	c.token = resp.Token
	c.logger.Info("registered with control plane",
		zap.String("node_id", c.nodeID),
		zap.Int64("heartbeat_interval", resp.HeartbeatInterval),
	)

	return nil
}

// Heartbeat 发送心跳
func (c *Client) Heartbeat(ctx context.Context, status *NodeStatus) (bool, error) {
	req := HeartbeatRequest{
		NodeID: c.nodeID,
		Token:  c.token,
		Status: status,
	}

	var resp HeartbeatResponse
	if err := c.post(ctx, "/api/v1/heartbeat", req, &resp); err != nil {
		return false, fmt.Errorf("heartbeat: %w", err)
	}

	return resp.ConfigUpdated, nil
}

// GetConfig 拉取配置
func (c *Client) GetConfig(ctx context.Context) ([]byte, bool, error) {
	req := GetConfigRequest{
		NodeID:        c.nodeID,
		Token:         c.token,
		ConfigVersion: c.configVersion,
	}

	var resp GetConfigResponse
	if err := c.post(ctx, "/api/v1/config", req, &resp); err != nil {
		return nil, false, fmt.Errorf("get config: %w", err)
	}

	if resp.Updated {
		c.configVersion = resp.ConfigVersion
		c.logger.Info("new config received",
			zap.Int64("version", resp.ConfigVersion),
		)
		return resp.ConfigData, true, nil
	}

	return nil, false, nil
}

// ReportStats 上报统计
func (c *Client) ReportStats(ctx context.Context, sessions []SessionStats) error {
	req := ReportStatsRequest{
		NodeID:   c.nodeID,
		Sessions: sessions,
	}

	var resp ReportStatsResponse
	if err := c.post(ctx, "/api/v1/stats", req, &resp); err != nil {
		return fmt.Errorf("report stats: %w", err)
	}

	return nil
}

// Token 返回当前 token
func (c *Client) Token() string {
	return c.token
}

// post 发送 POST 请求
func (c *Client) post(ctx context.Context, path string, body, result interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}
