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
