package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskBillingRequestCompletionAndPolling(t *testing.T) {
	const expression = `has(param("resolution"), "480") ? tier("480p", vs * 0.05) : tier("720p", vs * 0.07)`
	for _, frozen := range []bool{true, false} {
		t.Run(map[bool]string{true: "new", false: "legacy"}[frozen], func(t *testing.T) {
			snapshot := &billingexpr.BillingSnapshot{ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), TaskUsageBilling: true, GroupRatio: 1, QuotaPerUnit: 500000, UsageFacts: map[string]any{"seconds": 5.0}, EstimatedTier: "submitted"}
			wantTier, wantQuota := "720p", 280000
			if frozen {
				snapshot.TaskRequestParams = map[string]any{"resolution": "480p", "seconds": 5.0}
				wantTier, wantQuota = "480p", 200000
			}
			private := model.TaskPrivateData{BillingContext: &model.TaskBillingContext{TieredSnapshot: snapshot}}
			wire, err := common.Marshal(private)
			require.NoError(t, err)
			var restored model.TaskPrivateData
			require.NoError(t, common.Unmarshal(wire, &restored))
			task := &model.Task{PrivateData: restored}
			original := *task
			completion := map[string]any{"seconds": 8.0, "resolution": "1080p"}
			immediate, _, err := EvaluateTaskCompletionUsage(task.PrivateData.BillingContext.TieredSnapshot, completion)
			require.NoError(t, err)
			polled, err := prepareTaskTieredUsage(task, &relaycommon.TaskInfo{UsageFacts: completion})
			require.NoError(t, err)
			assert.Equal(t, wantTier, immediate.MatchedTier)
			assert.Equal(t, wantQuota, immediate.ActualQuotaAfterGroup)
			assert.Equal(t, immediate, polled)
			assert.Equal(t, 5.0, original.PrivateData.BillingContext.TieredSnapshot.UsageFacts["seconds"])
			assert.Equal(t, "submitted", original.PrivateData.BillingContext.TieredSnapshot.EstimatedTier)
			if frozen {
				task.PrivateData.BillingContext.TieredSnapshot.TaskRequestParams["resolution"] = "1080p"
				assert.Equal(t, "480p", original.PrivateData.BillingContext.TieredSnapshot.TaskRequestParams["resolution"])
			} else {
				assert.Nil(t, task.PrivateData.BillingContext.TieredSnapshot.TaskRequestParams)
			}
		})
	}
}
