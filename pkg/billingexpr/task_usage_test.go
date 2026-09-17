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
