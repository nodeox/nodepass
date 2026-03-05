package inbound

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/nodeox/nodepass/internal/common"
	"go.uber.org/zap"
)

func TestNew(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	tests := []struct {
		name     string
		protocol string
		wantErr  bool
	}{
		{"np-chain", "np-chain", false},
		{"unknown", "unknown", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := common.InboundConfig{
				Protocol: tt.protocol,
				Listen:   "127.0.0.1:0",
			}

			inbound, err := New(cfg, logger)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, inbound)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, inbound)
			}
		})
	}
}
