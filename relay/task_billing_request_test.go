package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
)

func TestRelayTaskBillingRequestFrozen(t *testing.T) {
	const expression = `has(param("resolution"), "1080") ? tier("1080p", (vs == 0 ? 5 : vs) * 0.25) : has(param("resolution"), "480") ? tier("480p", (vs == 0 ? 5 : vs) * 0.08) : tier("720p", (vs == 0 ? 5 : vs) * 0.14)`
	for _, tc := range []struct {
		key, value, tier string
		price            float64
	}{
		{"resolution", "480p", "480p", .08}, {"resolution", "720p", "720p", .14}, {"resolution", "1080p", "1080p", .25},
		{"resolution_name", "480p", "480p", .08},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			saveBillingConfig(t)
			exprs, err := common.Marshal(map[string]string{"declared-model": expression})
			require.NoError(t, err)
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
				"billing_setting.billing_mode": `{"declared-model":"tiered_expr"}`, "billing_setting.billing_expr": string(exprs), billing_setting.PluginBillingExprOption: `{}`,
			}))
			c, info := newTaskSubmitContext(t, "declared-model", "")
			common.SetContextKey(c, constant.ContextKeyChannelType, 60)
			c.Set("group", "default")
			info.UserGroup, info.UsingGroup, info.OriginModelName = "default", "default", "declared-model"
			body := map[string]any{tc.key: tc.value, "seconds": 5.0, "size": "720x1280", "prompt": "secret-prompt", "url": "https://secret.example", "headers": map[string]any{"Authorization": "secret-token"}, "nested": map[string]any{"resolution": "1080p"}}
			c.Set("task_request", body)
			source := strings.Replace(billingFallbackPlugin, `fetchMode:"per_task"`, `fetchMode:"per_task",usageSchema:{seconds:{type:"number",unit:"second"},size:{enum:["720x1280"]}}`, 1)
			source += `export function extractUsage(ctx){return {seconds:Number(ctx.requestBody.seconds),size:ctx.requestBody.size};}`
			pinMappingOrderPlugin(t, c, source)
			_, taskErr := RelayTaskSubmit(c, info)
			require.NotNil(t, taskErr) // Fixture stops at reservation, before upstream I/O.
			require.NotNil(t, info.TieredBillingSnapshot, "%+v", taskErr)
			assert.NotEqual(t, "model_price_error", taskErr.Code)
			assert.False(t, info.IsModelMapped)
			assert.Equal(t, tc.tier, info.TieredBillingSnapshot.EstimatedTier)
			assert.Equal(t, common.QuotaRound(5*tc.price*common.QuotaPerUnit), info.PriceData.Quota)

			wire, err := common.Marshal(&model.TaskBillingContext{TieredSnapshot: info.TieredBillingSnapshot})
			require.NoError(t, err)
			assert.NotContains(t, string(wire), "secret")
			var stored model.TaskBillingContext
			require.NoError(t, common.Unmarshal(wire, &stored))
			var snapshotJSON map[string]any
			snapWire, err := common.Marshal(stored.TieredSnapshot)
			require.NoError(t, err)
			require.NoError(t, common.Unmarshal(snapWire, &snapshotJSON))
			expectedParams := map[string]any{tc.key: tc.value, "seconds": 5.0, "size": "720x1280"}
			if tc.key == "resolution_name" {
				expectedParams["resolution"] = tc.value
			}
			assert.Equal(t, expectedParams, snapshotJSON["task_request_params"])

			body[tc.key], body["seconds"] = "changed", 100.0
			c.Set("task_request", map[string]any{"resolution": "720p"})
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"billing_setting.billing_expr": `{"declared-model":"tier(\"changed\", 999)"}`}))
			facts := map[string]any{"seconds": 8.0, "resolution": "720p"}
			result, usage, err := service.EvaluateTaskCompletionUsage(stored.TieredSnapshot, facts)
			require.NoError(t, err)
			assert.Equal(t, tc.tier, result.MatchedTier)
			assert.Equal(t, common.QuotaRound(8*tc.price*common.QuotaPerUnit), result.ActualQuotaAfterGroup)
			assert.Equal(t, 8.0, usage["seconds"])
			assert.Equal(t, 5.0, stored.TieredSnapshot.UsageFacts["seconds"])
			result, err = billingexpr.ComputeTieredQuotaWithRequest(stored.TieredSnapshot, billingexpr.TokenParams{}, billingexpr.RequestInput{Body: []byte(`{"resolution":"1080p"}`), Usage: facts})
			require.NoError(t, err)
			assert.Equal(t, tc.tier, result.MatchedTier)
		})
	}
}
