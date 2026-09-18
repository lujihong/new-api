package billingexpr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskUsageExpressionUsesFactsAndTaskQuotaConversion(t *testing.T) {
	expression := `tier("1080p", u("seconds") * (u("resolution") == "1080p" ? 0.4 : 0.2))`
	cost, trace, err := RunExprWithRequest(expression, TokenParams{}, RequestInput{Usage: map[string]any{"seconds": 10.0, "resolution": "1080p"}})
	require.NoError(t, err)
	assert.Equal(t, 4.0, cost)
	assert.Equal(t, "1080p", trace.MatchedTier)
	result, err := ComputeTieredQuotaWithRequest(&BillingSnapshot{ExprString: expression, ExprHash: ExprHashString(expression), GroupRatio: 2, QuotaPerUnit: 500000, ExprVersion: 1, TaskUsageBilling: true}, TokenParams{}, RequestInput{Usage: map[string]any{"seconds": 10.0, "resolution": "1080p"}})
	require.NoError(t, err)
	assert.Equal(t, 4_000_000, result.ActualQuotaAfterGroup)
}

// Characterize the currently published legacy expressions before changing the
// task request snapshot contract. Usage facts alone do not satisfy param().
func TestLegacyGrokResolutionRequiresRequestBody(t *testing.T) {
	models := []struct {
		name   string
		expr   string
		prices map[string]float64
	}{
		{"base", `has(param("resolution"), "480") ? tier("480p", (vs == 0 ? 5 : vs) * 0.05) : tier("720p", (vs == 0 ? 5 : vs) * 0.07)`, map[string]float64{"480p": 0.05, "720p": 0.07}},
		{"1.5", `has(param("resolution"), "1080") ? tier("1080p", (vs == 0 ? 5 : vs) * 0.25) : has(param("resolution"), "480") ? tier("480p", (vs == 0 ? 5 : vs) * 0.08) : tier("720p", (vs == 0 ? 5 : vs) * 0.14)`, map[string]float64{"480p": 0.08, "720p": 0.14, "1080p": 0.25}},
	}
	for _, m := range models {
		for resolution, price := range m.prices {
			t.Run(m.name+"/"+resolution, func(t *testing.T) {
				for _, seconds := range []float64{5, 8} {
					facts := map[string]any{"seconds": seconds, "resolution": resolution}
					snapshot := &BillingSnapshot{ExprString: m.expr, ExprHash: ExprHashString(m.expr), GroupRatio: 1, QuotaPerUnit: 500000, ExprVersion: 1, TaskUsageBilling: true}
					missingBody, err := ComputeTieredQuotaWithRequest(snapshot, TokenParams{}, RequestInput{Usage: facts})
					require.NoError(t, err)
					assert.Equal(t, "720p", missingBody.MatchedTier)
					assert.InDelta(t, seconds*m.prices["720p"]*500000, missingBody.ActualQuotaAfterGroup, 1)
					withBody, err := ComputeTieredQuotaWithRequest(snapshot, TokenParams{}, RequestInput{Usage: facts, Body: []byte(`{"resolution":"` + resolution + `"}`)})
					require.NoError(t, err)
					assert.Equal(t, resolution, withBody.MatchedTier)
					assert.InDelta(t, seconds*price*500000, withBody.ActualQuotaAfterGroup, 1)
					if resolution != "720p" {
						assert.NotEqual(t, withBody.ActualQuotaAfterGroup, missingBody.ActualQuotaAfterGroup)
					}
				}
			})
		}
	}
}

func TestDoubaoExpressionSafeOnMissingOrDifferentUsage(t *testing.T) {
	expression := `u("resolution") == "1080p" && u("video_input") == "none" ? tier("1080p·none", u("tokens") * 51 / 1000000) : u("resolution") == "1080p" && u("video_input") == "video" ? tier("1080p·video", u("tokens") * 31 / 1000000) : u("resolution") == "480p" && u("video_input") == "none" ? tier("480p·none", u("tokens") * 28 / 1000000) : u("resolution") == "480p" && u("video_input") == "video" ? tier("480p·video", u("tokens") * 22 / 1000000) : u("resolution") == "4k" && u("video_input") == "none" ? tier("4k·none", u("tokens") * 26 / 1000000) : u("resolution") == "4k" && u("video_input") == "video" ? tier("4k·video", u("tokens") * 16 / 1000000) : u("resolution") == "720p" && u("video_input") == "video" ? tier("720p·video", u("tokens") * 28 / 1000000) : tier("720p·none", u("tokens") * 46 / 1000000)`
	cost, trace, err := RunExprWithRequest(expression, TokenParams{}, RequestInput{Usage: map[string]any{"seconds": 6.0, "size": "720x1280"}})
	require.NoError(t, err)
	assert.Equal(t, 0.0, cost)
	assert.Equal(t, "720p·none", trace.MatchedTier)

	costWithTokens, traceWithTokens, err := RunExprWithRequest(expression, TokenParams{}, RequestInput{Usage: map[string]any{"tokens": 100000.0, "resolution": "720p", "video_input": "none"}})
	require.NoError(t, err)
	assert.InDelta(t, 4.6, costWithTokens, 0.001)
	assert.Equal(t, "720p·none", traceWithTokens.MatchedTier)
}
