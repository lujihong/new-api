package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFixedTextQuoteMatchesSettlement(t *testing.T) {
	oldUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldUnit })
	for _, tc := range []struct {
		name                            string
		price, modelRatio, group, other float64
		tokens, want                    int
	}{
		{"half rounds up", 0.000003, 0, 1, 1, 1, 2},
		{"tiny without minimum", 0.0000008, 0, 1, 1, 1, 0},
		{"tiny with nonzero ratio", 0.0000008, 1, 1, 1, 1, 1},
		{"zero price", 0, 0, 1, 1, 1, 0},
		{"zero price with nonzero ratio", 0, 1, 1, 1, 1, 1},
		{"zero group", 0.000003, 1, 0, 1, 1, 0},
		{"no billable usage", 0.000003, 1, 1, 1, 0, 0},
		{"other ratios before rounding", 0.000001, 0, 1, 3, 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			price := hosttypes.PriceData{UsePrice: true, ModelPrice: tc.price, ModelRatio: tc.modelRatio, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: tc.group}}
			price.AddOtherRatio("request", tc.other)
			quota, clamp := CalculateFixedTextQuota(price, common.QuotaPerUnit, decimal.Zero, tc.tokens > 0)
			require.Nil(t, clamp)
			assert.Equal(t, tc.want, quota)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{OriginModelName: "fixed-text-quote-test", PriceData: price}
			usage := &dto.Usage{PromptTokens: tc.tokens, TotalTokens: tc.tokens}
			summary := calculateTextQuotaSummary(ctx, info, usage)
			assert.Equal(t, tc.want, summary.Quota)
			assert.Equal(t, quota, summary.Quota)
			assert.Nil(t, info.QuotaClamp)
		})
	}
}

func TestFixedTextQuoteSurchargeAndClamp(t *testing.T) {
	price := hosttypes.PriceData{UsePrice: true, ModelPrice: 0.000001, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
	price.AddOtherRatio("request", 2)
	quota, clamp := CalculateFixedTextQuota(price, 500000, decimal.RequireFromString("0.5"), true)
	require.Nil(t, clamp)
	assert.Equal(t, 2, quota, "surcharge is added after OtherRatios, before rounding")
	price.ModelPrice = 0
	quota, clamp = CalculateFixedTextQuota(price, 500000, decimal.RequireFromString("1.5"), true)
	require.Nil(t, clamp)
	assert.Equal(t, 2, quota, "tools alone can be billable")
	price.ModelPrice = 1e20
	quota, clamp = CalculateFixedTextQuota(price, 500000, decimal.Zero, true)
	require.NotNil(t, clamp)
	assert.Equal(t, common.MaxQuota, quota)
}
