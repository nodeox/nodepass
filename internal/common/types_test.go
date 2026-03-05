package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSessionMeta(t *testing.T) {
	meta := SessionMeta{
		ID:     "test-session-id",
		UserID: "user1",
		Target: "example.com:443",
	}

	assert.Equal(t, "test-session-id", meta.ID)
	assert.Equal(t, "user1", meta.UserID)
	assert.Equal(t, "example.com:443", meta.Target)
}

func TestRoutingRule(t *testing.T) {
	rule := RoutingRule{
		Type:     "domain",
		Pattern:  "*.google.com",
		Outbound: "direct",
	}

	assert.Equal(t, "domain", rule.Type)
	assert.Equal(t, "*.google.com", rule.Pattern)
	assert.Equal(t, "direct", rule.Outbound)
}

func TestConfig_DeepCopy(t *testing.T) {
	original := &Config{
		Version: "2.0",
		Node: NodeConfig{
			ID:   "node-1",
			Type: "ingress",
			Tags: map[string]string{"env": "prod", "region": "us"},
		},
		Inbounds: []InboundConfig{
			{
				Protocol: "tcp",
				Listen:   "0.0.0.0:1080",
				Target:   "127.0.0.1:8080",
				Auth: AuthConfig{
					Enabled: true,
					Users: []UserAuth{
						{Username: "admin", Password: "secret"},
					},
				},
			},
		},
		Outbounds: []OutboundConfig{
			{Name: "out1", Protocol: "direct", Group: "default"},
		},
		Routing: RoutingConfig{
			Rules: []RoutingRule{
				{Type: "default", Outbound: "out1"},
			},
		},
	}

	cp := original.DeepCopy()

	// Values should be equal
	assert.Equal(t, original.Version, cp.Version)
	assert.Equal(t, original.Node.ID, cp.Node.ID)
	assert.Equal(t, original.Node.Tags, cp.Node.Tags)
	assert.Equal(t, original.Inbounds, cp.Inbounds)
	assert.Equal(t, original.Outbounds, cp.Outbounds)
	assert.Equal(t, original.Routing.Rules, cp.Routing.Rules)

	// Mutate copy — original should remain unchanged
	cp.Node.Tags["env"] = "mutated"
	assert.Equal(t, "prod", original.Node.Tags["env"])

	cp.Inbounds[0].Listen = "mutated"
	assert.Equal(t, "0.0.0.0:1080", original.Inbounds[0].Listen)

	cp.Inbounds[0].Auth.Users[0].Username = "mutated"
	assert.Equal(t, "admin", original.Inbounds[0].Auth.Users[0].Username)

	cp.Outbounds[0].Name = "mutated"
	assert.Equal(t, "out1", original.Outbounds[0].Name)

	cp.Routing.Rules[0].Type = "mutated"
	assert.Equal(t, "default", original.Routing.Rules[0].Type)
}

func TestConfig_DeepCopy_Nil(t *testing.T) {
	var c *Config
	assert.Nil(t, c.DeepCopy())
}

func TestConfig_DeepCopy_Empty(t *testing.T) {
	c := &Config{}
	cp := c.DeepCopy()
	assert.NotNil(t, cp)
	assert.Nil(t, cp.Node.Tags)
	assert.Nil(t, cp.Inbounds)
	assert.Nil(t, cp.Outbounds)
	assert.Nil(t, cp.Routing.Rules)
}
