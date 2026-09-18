package service

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests share TestMain's in-memory database and must not run in parallel:
// the ratio settings are process globals, restored after every case.
func preserveTaskSnapshotRatios(t *testing.T) {
	t.Helper()
	modelRatios := ratio_setting.ModelRatio2JSONString()
	groupRatios := ratio_setting.GroupRatio2JSONString()
	groupGroupRatios := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(modelRatios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groupRatios))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(groupGroupRatios))
	})
}

func TestRecalculateTaskQuotaByTokensSnapshot(t *testing.T) {
	tests := []struct {
		name          string
		bc            *model.TaskBillingContext
		currentModels string
		currentGroups string
		currentPairs  string
		taskGroup     string
		preConsumed   int
		tokens        int
		wantRecalc    bool
		wantQuota     int
	}{
		{
			name: "positive snapshot survives model and group price changes",
			bc: &model.TaskBillingContext{ModelRatio: 2, GroupRatio: 0.5,
				OtherRatios: map[string]float64{"seconds": 2, "identity": 1, "zero": 0, "negative": -3}},
			preConsumed: 50, tokens: 100, wantRecalc: true, wantQuota: 200,
		},
		{
			name:          "current model price deleted after submission",
			bc:            &model.TaskBillingContext{ModelRatio: 2, GroupRatio: 0.5},
			currentModels: `{}`, preConsumed: 500, tokens: 100, wantRecalc: true, wantQuota: 100,
		},
		{
			name:          "current model price zero after submission",
			bc:            &model.TaskBillingContext{ModelRatio: 2, GroupRatio: 0.5},
			currentModels: `{"test-model":0}`, preConsumed: 500, tokens: 100, wantRecalc: true, wantQuota: 100,
		},
		{
			name:        "zero snapshot group remains free after JSON omitempty round trip",
			bc:          &model.TaskBillingContext{ModelRatio: 2, GroupRatio: 0},
			preConsumed: 500, tokens: 100, wantRecalc: true, wantQuota: 0,
		},
		{
			name:          "snapshot does not need current group configuration",
			bc:            &model.TaskBillingContext{ModelRatio: 2, GroupRatio: 0.5},
			currentGroups: `{}`, currentPairs: `{}`, preConsumed: 500, tokens: 100, wantRecalc: true, wantQuota: 100,
		},
		{
			name:      "snapshot does not need task or user group",
			bc:        &model.TaskBillingContext{ModelRatio: 2, GroupRatio: 0.5},
			taskGroup: "empty", preConsumed: 500, tokens: 100, wantRecalc: true, wantQuota: 100,
		},
		{
			name:        "negative model snapshot rejected",
			bc:          &model.TaskBillingContext{ModelRatio: -2, GroupRatio: 0.5},
			preConsumed: 500, tokens: 100, wantQuota: 500,
		},
		{
			name:        "negative group snapshot rejected",
			bc:          &model.TaskBillingContext{ModelRatio: 2, GroupRatio: -0.5},
			preConsumed: 500, tokens: 100, wantQuota: 500,
		},
		{
			name:        "legacy nil context preserves current same group special ratio",
			preConsumed: 500, tokens: 100, wantRecalc: true, wantQuota: 3500,
		},
		{
			name:        "legacy zero model and group cannot prove a saved free model",
			bc:          &model.TaskBillingContext{},
			preConsumed: 500, tokens: 100, wantRecalc: true, wantQuota: 3500,
		},
		{
			name:         "legacy zero model keeps current group fallback and filtered other ratios",
			bc:           &model.TaskBillingContext{GroupRatio: 0.5, OtherRatios: map[string]float64{"seconds": 2, "zero": 0, "negative": -1}},
			currentPairs: `{}`, preConsumed: 500, tokens: 100, wantRecalc: true, wantQuota: 4200,
		},
		{
			name:      "legacy nil context keeps user group lookup",
			taskGroup: "user", preConsumed: 500, tokens: 100, wantRecalc: true, wantQuota: 2800,
		},
		{
			name:      "legacy missing group skips recalculation",
			taskGroup: "empty", preConsumed: 500, tokens: 100, wantQuota: 500,
		},
		{
			name: "legacy deleted model skips recalculation",
			bc:   &model.TaskBillingContext{}, currentModels: `{}`,
			preConsumed: 500, tokens: 100, wantQuota: 500,
		},
		{
			name:          "legacy current zero model keeps existing skip behavior",
			currentModels: `{"test-model":0}`, preConsumed: 500, tokens: 100, wantQuota: 500,
		},
		{
			name:        "per call with positive snapshot rejects direct token fallback",
			bc:          &model.TaskBillingContext{ModelRatio: 2, GroupRatio: 0.5, PerCallBilling: true},
			preConsumed: 500, tokens: 100, wantQuota: 500,
		},
		{
			name:        "legacy per call rejects direct token fallback",
			bc:          &model.TaskBillingContext{PerCallBilling: true},
			preConsumed: 500, tokens: 100, wantQuota: 500,
		},
		{
			name:        "tiered snapshot rejects direct token fallback",
			bc:          &model.TaskBillingContext{ModelRatio: 2, GroupRatio: 0.5, TieredSnapshot: &billingexpr.BillingSnapshot{}},
			preConsumed: 500, tokens: 100, wantQuota: 500,
		},
		{
			name:        "legacy tiered snapshot rejects direct token fallback",
			bc:          &model.TaskBillingContext{TieredSnapshot: &billingexpr.BillingSnapshot{ExprString: `tier("broken",`}},
			preConsumed: 500, tokens: 100, wantQuota: 500,
		},
		{
			name:        "zero tokens leave snapshot and wallet untouched",
			bc:          &model.TaskBillingContext{ModelRatio: 2, GroupRatio: 0.5},
			preConsumed: 500, wantQuota: 500,
		},
		{
			name:        "negative tokens leave snapshot and wallet untouched",
			bc:          &model.TaskBillingContext{ModelRatio: 2, GroupRatio: 0.5},
			preConsumed: 500, tokens: -1, wantQuota: 500,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			truncate(t)
			preserveTaskSnapshotRatios(t)
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"test-model":2}`))
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":0.5}`))
			require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`))

			const userID, channelID, initialQuota = 81, 81, 10000
			seedUser(t, userID, initialQuota-tc.preConsumed)
			seedChannel(t, channelID)
			seedChargedAccounting(t, userID, channelID, 0, tc.preConsumed, 1)
			task := makeTask(userID, channelID, tc.preConsumed, 0, BillingSourceWallet, 0)
			task.PrivateData.BillingContext = tc.bc
			userGroup := "member"
			if tc.taskGroup != "" {
				task.Group = ""
				if tc.taskGroup == "empty" {
					userGroup = ""
				}
			}
			require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(&model.User{Group: userGroup}).Error)
			if userGroup == "" {
				require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Select("Group").Updates(&model.User{Group: ""}).Error)
			}
			require.NoError(t, model.DB.Create(task).Error)

			// Change the live configuration only after the submission snapshot is stored.
			models, groups, pairs := tc.currentModels, tc.currentGroups, tc.currentPairs
			if models == "" {
				models = `{"test-model":7}`
			}
			if groups == "" {
				groups = `{"default":3,"member":4}`
			}
			if pairs == "" {
				pairs = `{"default":{"default":5},"member":{"default":9}}`
			}
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(models))
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groups))
			require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(pairs))
			var loaded model.Task
			require.NoError(t, model.DB.First(&loaded, task.ID).Error)
			assert.Equal(t, tc.wantRecalc, RecalculateTaskQuotaByTokens(context.Background(), &loaded, tc.tokens))
			assert.Equal(t, tc.wantQuota, loaded.Quota)
			assert.Equal(t, tc.wantQuota, getTaskQuota(t, task.ID))
			assert.Equal(t, initialQuota-tc.wantQuota, getUserQuota(t, userID))
			used, requests := getUserUsageAccounting(t, userID)
			assert.Equal(t, tc.wantQuota, used)
			assert.Equal(t, 1, requests)
			assert.Equal(t, int64(tc.wantQuota), getChannelUsedQuota(t, channelID))
			if tc.wantQuota == tc.preConsumed {
				assert.Zero(t, countLogs(t))
			} else {
				assert.Equal(t, int64(1), countLogs(t))
				log := getLastLog(t)
				require.NotNil(t, log)
				if tc.wantQuota > tc.preConsumed {
					assert.Equal(t, model.LogTypeConsume, log.Type)
					assert.Equal(t, tc.wantQuota-tc.preConsumed, log.Quota)
				} else {
					assert.Equal(t, model.LogTypeRefund, log.Type)
					assert.Equal(t, tc.preConsumed-tc.wantQuota, log.Quota)
				}
			}
		})
	}
}

func TestRecalculateTaskQuotaByTokensSnapshotZeroModelDiscountVersion(t *testing.T) {
	// Only v1 is implemented. Neither an unversioned object nor an unknown
	// version proves that a zero model ratio was intentionally saved.
	for _, version := range []int{1, 0, -1, 2} {
		for _, groupRatio := range []float64{0, 0.5, 1} {
			for _, preConsumed := range []int{0, 500} {
				t.Run(fmt.Sprintf("version=%d/group=%g/pre=%d", version, groupRatio, preConsumed), func(t *testing.T) {
					truncate(t)
					preserveTaskSnapshotRatios(t)
					require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"test-model":0}`))
					require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(fmt.Sprintf(`{"default":%g}`, groupRatio)))
					require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`))

					const userID, channelID, tokenID, initialQuota = 83, 83, 83, 10000
					seedUser(t, userID, initialQuota-preConsumed)
					seedChannel(t, channelID)
					seedToken(t, tokenID, userID, "snapshot-zero-model-test", initialQuota-preConsumed)
					seedChargedAccounting(t, userID, channelID, tokenID, preConsumed, 1)
					task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
					task.PrivateData.BillingContext = &model.TaskBillingContext{
						ModelRatio: 0, GroupRatio: groupRatio, OriginModelName: "test-model",
						ModelDiscount: &model.TaskModelDiscountSnapshot{
							Version: version, OriginModel: "test-model", BaseGroupRatio: 1,
							Factor: groupRatio, Source: "global", Revision: "submission-test",
						},
					}
					require.NoError(t, task.Insert())

					// Live prices become positive only after the snapshot was persisted.
					require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"test-model":7}`))
					require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":3}`))
					require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"default":{"default":5}}`))
					var loaded model.Task
					require.NoError(t, model.DB.First(&loaded, task.ID).Error)
					require.Equal(t, task.PrivateData.BillingContext, loaded.PrivateData.BillingContext)

					wantQuota := 3500 // Legacy fallback: 100 tokens * current model 7 * same-group 5.
					if version == 1 {
						wantQuota = 0
					}
					// Replay with a fresh DB read also verifies no duplicate money movement or log.
					for attempt := 0; attempt < 2; attempt++ {
						require.NoError(t, model.DB.First(&loaded, task.ID).Error)
						assert.True(t, RecalculateTaskQuotaByTokens(context.Background(), &loaded, 100))
						assert.Equal(t, wantQuota, loaded.Quota)
						assert.Equal(t, wantQuota, getTaskQuota(t, task.ID))
						assert.Equal(t, initialQuota-wantQuota, getUserQuota(t, userID))
						assert.Equal(t, initialQuota-wantQuota, getTokenRemainQuota(t, tokenID))
						assert.Equal(t, wantQuota, getTokenUsedQuota(t, tokenID))
						used, requests := getUserUsageAccounting(t, userID)
						assert.Equal(t, wantQuota, used)
						assert.Equal(t, 1, requests)
						assert.Equal(t, int64(wantQuota), getChannelUsedQuota(t, channelID))
						if wantQuota == preConsumed {
							assert.Zero(t, countLogs(t))
						} else {
							assert.Equal(t, int64(1), countLogs(t))
							log := getLastLog(t)
							require.NotNil(t, log)
							assert.Equal(t, userID, log.UserId)
							assert.Equal(t, tokenID, log.TokenId)
							if wantQuota > preConsumed {
								assert.Equal(t, model.LogTypeConsume, log.Type)
								assert.Equal(t, wantQuota-preConsumed, log.Quota)
							} else {
								assert.Equal(t, model.LogTypeRefund, log.Type)
								assert.Equal(t, preConsumed-wantQuota, log.Quota)
							}
						}
					}
				})
			}
		}
	}
}

func TestRecalculateTaskQuotaByTokensSnapshotNonFinite(t *testing.T) {
	for _, field := range []string{"model", "group", "other"} {
		for _, value := range []struct {
			name  string
			ratio float64
		}{{"NaN", math.NaN()}, {"positive infinity", math.Inf(1)}, {"negative infinity", math.Inf(-1)}} {
			t.Run(field+"/"+value.name, func(t *testing.T) {
				truncate(t)
				preserveTaskSnapshotRatios(t)
				require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"test-model":7}`))
				require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":3}`))
				require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`))
				const userID, channelID, preConsumed = 82, 82, 500
				seedUser(t, userID, 9500)
				seedChannel(t, channelID)
				seedChargedAccounting(t, userID, channelID, 0, preConsumed, 1)
				task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
				task.PrivateData.BillingContext.ModelRatio = 2
				task.PrivateData.BillingContext.GroupRatio = 0.5
				require.NoError(t, model.DB.Create(task).Error)
				var loaded model.Task
				require.NoError(t, model.DB.First(&loaded, task.ID).Error)
				// JSON cannot store NaN/Inf. Inject only after a real DB round trip;
				// still exercise the public entry and all persisted accounting effects.
				bc := loaded.PrivateData.BillingContext
				wantQuota := preConsumed
				switch field {
				case "model":
					bc.ModelRatio = value.ratio
				case "group":
					bc.GroupRatio = value.ratio
				case "other":
					bc.OtherRatios = map[string]float64{"invalid": value.ratio, "seconds": 2}
					wantQuota = 200
				}
				assert.Equal(t, field == "other", RecalculateTaskQuotaByTokens(context.Background(), &loaded, 100))
				assert.Equal(t, wantQuota, loaded.Quota)
				assert.Equal(t, wantQuota, getTaskQuota(t, task.ID))
				assert.Equal(t, 10000-wantQuota, getUserQuota(t, userID))
				used, requests := getUserUsageAccounting(t, userID)
				assert.Equal(t, wantQuota, used)
				assert.Equal(t, 1, requests)
				assert.Equal(t, int64(wantQuota), getChannelUsedQuota(t, channelID))
				if field != "other" {
					assert.Zero(t, countLogs(t))
				} else {
					assert.Equal(t, int64(1), countLogs(t))
				}
			})
		}
	}
}

func TestRecalculateTaskQuotaByTokensSnapshotNilTask(t *testing.T) {
	assert.NotPanics(t, func() {
		assert.False(t, RecalculateTaskQuotaByTokens(context.Background(), nil, 100))
	})
}
