package relay

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
)

func TestIsImageUsageMissingDoesNotInventTokens(t *testing.T) {
	for _, tc := range []struct {
		name string
		usage *dto.Usage
		want bool
	}{
		{name: "nil usage", usage: nil, want: true},
		{name: "zero input and output", usage: &dto.Usage{InputTokens: 0, OutputTokens: 0}, want: true},
		{name: "real one token", usage: &dto.Usage{InputTokens: 1, PromptTokens: 1, TotalTokens: 1}, want: false},
		{name: "reported image token", usage: &dto.Usage{PromptTokensDetails: dto.InputTokenDetails{ImageTokens: 1}}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isImageUsageMissing(tc.usage))
		})
	}
}

func TestMarkImageUsageMissingPreservesUsageAndPreconsume(t *testing.T) {
	usage := &dto.Usage{}
	info := &relaycommon.RelayInfo{FinalPreConsumedQuota: 123}

	assert.True(t, markImageUsageMissing(info, usage))
	assert.True(t, info.UsageMissing)
	assert.Equal(t, 123, info.FinalPreConsumedQuota)
	assert.Zero(t, usage.TotalTokens)
	assert.False(t, markImageUsageMissing(info, &dto.Usage{InputTokens: 1, PromptTokens: 1, TotalTokens: 1}))
}
