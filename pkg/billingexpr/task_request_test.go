package billingexpr

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskRequestSnapshotCompatibility(t *testing.T) {
	const expression = `has(param("resolution"), "480") ? tier("480p", vs * 0.05) : tier("720p", vs * 0.07)`
	for _, tc := range []struct {
		name   string
		task   bool
		params map[string]any
		body   string
		tier   string
	}{
		{"frozen task", true, map[string]any{"resolution": "480p"}, `{"resolution":"720p"}`, "480p"},
		{"legacy task missing body", true, nil, "", "720p"},
		{"legacy task caller body", true, nil, `{"resolution":"480p"}`, "480p"},
		{"non task ignores frozen task fields", false, map[string]any{"resolution": "480p"}, `{"resolution":"720p"}`, "720p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := BillingSnapshot{ExprString: expression, ExprHash: ExprHashString(expression), TaskUsageBilling: tc.task, TaskRequestParams: tc.params, QuotaPerUnit: 500000, GroupRatio: 1}
			wire, err := common.Marshal(snap)
			require.NoError(t, err)
			var restored BillingSnapshot
			require.NoError(t, common.Unmarshal(wire, &restored))
			if tc.params == nil {
				assert.Nil(t, restored.TaskRequestParams)
				assert.NotContains(t, string(wire), "task_request_params")
			}
			result, err := ComputeTieredQuotaWithRequest(&restored, TokenParams{}, RequestInput{Body: []byte(tc.body), Usage: map[string]any{"seconds": 8.0, "resolution": "1080p"}})
			require.NoError(t, err)
			assert.Equal(t, tc.tier, result.MatchedTier)
		})
	}
}

func TestTaskRequestSnapshotLeavesUsageSemanticsUnchanged(t *testing.T) {
	const expression = `param("resolution") == "480p" && param("seconds") == 5 && u("resolution") == "1080p" ? tier("separate", u("seconds") * 0.05) : tier("wrong", 99)`
	snap := &BillingSnapshot{ExprString: expression, TaskUsageBilling: true, TaskRequestParams: map[string]any{"resolution": "480p", "seconds": 5.0}, QuotaPerUnit: 500000, GroupRatio: 1}
	result, err := ComputeTieredQuotaWithRequest(snap, TokenParams{}, RequestInput{Usage: map[string]any{"seconds": 8.0, "resolution": "1080p"}})
	require.NoError(t, err)
	assert.Equal(t, "separate", result.MatchedTier)
	assert.Equal(t, 200000, result.ActualQuotaAfterGroup)
	snap.TaskRequestParams["seconds"] = math.NaN()
	_, err = ComputeTieredQuotaWithRequest(snap, TokenParams{}, RequestInput{})
	require.Error(t, err)
}
