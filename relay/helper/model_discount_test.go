package helper

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kittypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// These tests mutate process-wide settings and deliberately do not use Parallel.
func prepareModelDiscountTest(t *testing.T, rules string) {
	t.Helper()
	oldRules := ratio_setting.ModelDiscountRulesJSONString()
	oldGroups := ratio_setting.GroupRatio2JSONString()
	oldSpecial := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelDiscountRulesJSON(oldRules))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(oldSpecial))
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"route-a":2,"route-b":4,"free":0}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelDiscountRulesJSON(rules))
}

func modelDiscountRelayInfo(userID int, origin string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId: userID, UserGroup: "customer", UsingGroup: "route-a",
		OriginModelName: origin, BillingModelName: "billing-alias",
	}
}

func TestModelDiscountPriorityAndExactOrigin(t *testing.T) {
	prepareModelDiscountTest(t, `{
		"groups":{"customer":{"m":0.5,"free-model":0,"full-model":1,"billing-alias":0,"m-*":0},"route-a":{"m":0}},
		"users":{"7":{"m":0.25},"8":{"m":0},"9":{"m":1}}
	}`)
	for _, tc := range []struct {
		name   string
		userID int
		origin string
		factor float64
		source string
	}{
		{"personal wins", 7, "m", 0.25, "user"},
		{"personal zero wins", 8, "m", 0, "user"},
		{"personal one wins", 9, "m", 1, "user"},
		{"user group fallback", 10, "m", 0.5, "group"},
		{"group zero", 10, "free-model", 0, "group"},
		{"group one", 10, "full-model", 1, "group"},
		{"missing stays full price", 10, "missing", 1, "default"},
		{"no suffix or wildcard inheritance", 7, "m-high", 1, "default"},
		{"case sensitive", 7, "M", 1, "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := modelDiscountRelayInfo(tc.userID, tc.origin)
			group := HandleGroupRatio(nil, info)
			require.True(t, group.HasModelDiscount)
			require.Equal(t, 2.0, group.BaseGroupRatio)
			require.Equal(t, tc.factor, group.ModelDiscount)
			require.Equal(t, 2*tc.factor, group.GroupRatio)
			require.Equal(t, tc.source, group.ModelDiscountSource)
			require.Equal(t, tc.origin, group.ModelDiscountModel)
			require.NotEmpty(t, group.ModelDiscountRevision)
			require.False(t, group.HasSpecialRatio)
			require.Equal(t, -1.0, group.GroupSpecialRatio)
		})
	}
}

func TestModelDiscountEmptyRulesAndLegacyMetadata(t *testing.T) {
	prepareModelDiscountTest(t, `{}`)
	info := modelDiscountRelayInfo(7, "m")
	group := HandleGroupRatio(nil, info)
	require.Equal(t, 2.0, group.GroupRatio)
	require.Equal(t, 1.0, group.ModelDiscount)
	require.Equal(t, "default", group.ModelDiscountSource)

	legacy := hosttypes.GroupRatioInfo{GroupRatio: 3}
	require.Equal(t, 3.0, legacy.UndiscountedGroupRatio())
	require.Nil(t, model.NewTaskModelDiscountSnapshot(hosttypes.PriceData{GroupRatioInfo: legacy}))
	require.Equal(t, 0.0, (hosttypes.GroupRatioInfo{}).UndiscountedGroupRatio())

	// Even an explicitly supplied zero snapshot uses factor 1, not free pricing.
	info.ModelDiscountSnapshot = &ratio_setting.ModelDiscountSnapshot{}
	require.Equal(t, 2.0, info.ResolveGroupRatio().GroupRatio)

	info.UsingGroup = "free"
	group = info.ResolveGroupRatio()
	require.Equal(t, 0.0, group.BaseGroupRatio)
	require.Equal(t, 0.0, group.GroupRatio)
	require.Equal(t, 1.0, group.ModelDiscount)
}

func TestModelDiscountRetriesAndRuleUpdates(t *testing.T) {
	prepareModelDiscountTest(t, `{"groups":{"customer":{"m":0.5}}}`)
	info := modelDiscountRelayInfo(7, "m")
	require.Nil(t, info.ModelDiscountSnapshot)
	info.PriceData.GroupRatioInfo = HandleGroupRatio(nil, info)
	first := info.PriceData.GroupRatioInfo
	pinned := info.ModelDiscountSnapshot
	require.NotNil(t, pinned)

	require.NoError(t, ratio_setting.UpdateModelDiscountRulesJSON(`{"users":{"7":{"m":0.25}}}`))
	for attempt := 0; attempt < 2; attempt++ {
		info.PriceData.GroupRatioInfo = HandleGroupRatio(nil, info)
		require.Equal(t, first, info.PriceData.GroupRatioInfo)
		require.Same(t, pinned, info.ModelDiscountSnapshot)
	}
	require.Equal(t, 1.0, first.GroupRatio)

	fresh := modelDiscountRelayInfo(7, "m")
	updated := HandleGroupRatio(nil, fresh)
	require.Equal(t, 0.5, updated.GroupRatio)
	require.Equal(t, "user", updated.ModelDiscountSource)
	require.NotEqual(t, first.ModelDiscountRevision, updated.ModelDiscountRevision)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info.InitChannelMeta(ctx)
	require.Same(t, pinned, info.ModelDiscountSnapshot)
	require.Equal(t, first, HandleGroupRatio(nil, info))
}

func TestModelDiscountAutoGroupChangesBaseOnly(t *testing.T) {
	prepareModelDiscountTest(t, `{"groups":{"customer":{"m":0.5},"route-a":{"m":0},"route-b":{"m":1}}}`)
	info := modelDiscountRelayInfo(7, "m")
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("auto_group", "route-a")
	first := HandleGroupRatio(ctx, info)
	require.Equal(t, 1.0, first.GroupRatio)

	ctx.Set("auto_group", "route-b")
	second := HandleGroupRatio(ctx, info)
	require.Equal(t, "route-b", info.UsingGroup)
	require.Equal(t, 4.0, second.BaseGroupRatio)
	require.Equal(t, 2.0, second.GroupRatio)
	require.Equal(t, first.ModelDiscount, second.ModelDiscount)
	require.Equal(t, first.ModelDiscountRevision, second.ModelDiscountRevision)

	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"customer":{"route-b":3,"free":0}}`))
	special := HandleGroupRatio(ctx, info)
	require.True(t, special.HasSpecialRatio)
	require.Equal(t, 3.0, special.GroupSpecialRatio)
	require.Equal(t, 3.0, special.BaseGroupRatio)
	require.Equal(t, 1.5, special.GroupRatio)
	require.Equal(t, 3.0, special.UndiscountedGroupRatio())

	ctx.Set("auto_group", "free")
	free := HandleGroupRatio(ctx, info)
	require.True(t, free.HasSpecialRatio)
	require.Equal(t, 0.0, free.GroupRatio)
	require.Equal(t, 0.0, free.UndiscountedGroupRatio())
	require.Equal(t, 0.5, free.ModelDiscount)
}

func TestModelDiscountTaskSnapshotSerialization(t *testing.T) {
	prepareModelDiscountTest(t, `{"users":{"7":{"m":0},"999":{"private-other-model":0.1}}}`)
	info := modelDiscountRelayInfo(7, "m")
	info.PriceData.GroupRatioInfo = HandleGroupRatio(nil, info)
	bc := model.TaskBillingContext{
		GroupRatio: info.PriceData.GroupRatioInfo.GroupRatio,
		ModelRatio: 2, OriginModelName: info.OriginModelName,
		ModelDiscount: model.NewTaskModelDiscountSnapshot(info.PriceData),
	}
	require.NotNil(t, bc.ModelDiscount)
	require.Equal(t, 1, bc.ModelDiscount.Version)
	require.Equal(t, "m", bc.ModelDiscount.OriginModel)
	require.Equal(t, 2.0, bc.ModelDiscount.BaseGroupRatio)
	require.Equal(t, 0.0, bc.ModelDiscount.Factor)
	require.Equal(t, "user", bc.ModelDiscount.Source)
	require.Equal(t, 2.0, info.PriceData.GroupRatioInfo.UndiscountedGroupRatio())

	encoded, err := json.Marshal(bc)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"factor":0`)
	require.NotContains(t, string(encoded), "private-other-model")
	require.NotContains(t, string(encoded), `"999"`)
	var decoded model.TaskBillingContext
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, bc, decoded)

	// Persisted provenance is detached and never reevaluates current rules.
	require.NoError(t, ratio_setting.UpdateModelDiscountRulesJSON(`{}`))
	info.PriceData.GroupRatioInfo = hosttypes.GroupRatioInfo{}
	require.Equal(t, 0.0, decoded.ModelDiscount.Factor)
	require.Equal(t, 0.0, decoded.GroupRatio)
	legacyJSON, err := json.Marshal(model.TaskBillingContext{GroupRatio: 2})
	require.NoError(t, err)
	require.NotContains(t, string(legacyJSON), "model_discount")

	free := model.NewTaskModelDiscountSnapshot(hosttypes.PriceData{GroupRatioInfo: hosttypes.GroupRatioInfo{
		HasModelDiscount: true, BaseGroupRatio: 0, ModelDiscount: 1,
	}})
	freeJSON, err := json.Marshal(free)
	require.NoError(t, err)
	require.Contains(t, string(freeJSON), `"base_group_ratio":0`)
}

func TestModelDiscountPriceHelpersUseFinalRatio(t *testing.T) {
	prepareModelDiscountTest(t, `{}`)
	oldPrices := ratio_setting.ModelPrice2JSONString()
	oldRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatios))
	})
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"discount-fixed":2}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"discount-token":2}`))
	for _, factor := range []float64{0, 0.5, 1} {
		t.Run(fmt.Sprint(factor), func(t *testing.T) {
			rules := fmt.Sprintf(`{"users":{"7":{"discount-fixed":%g,"discount-token":%g}}}`, factor, factor)
			require.NoError(t, ratio_setting.UpdateModelDiscountRulesJSON(rules))
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			fixed := modelDiscountRelayInfo(7, "discount-fixed")
			fixed.BillingModelName = ""
			price, err := ModelPriceHelper(ctx, fixed, 1000, &kittypes.TokenCountMeta{})
			require.NoError(t, err)
			require.Equal(t, int(4*common.QuotaPerUnit*factor), price.QuotaToPreConsume)
			require.Equal(t, 2*factor, price.GroupRatioInfo.GroupRatio)
			perCall, err := ModelPriceHelperPerCall(ctx, fixed)
			require.NoError(t, err)
			require.Equal(t, price.QuotaToPreConsume, perCall.Quota)

			token := modelDiscountRelayInfo(7, "discount-token")
			token.BillingModelName = ""
			price, err = ModelPriceHelper(ctx, token, 1000, &kittypes.TokenCountMeta{})
			require.NoError(t, err)
			require.Equal(t, int(float64(common.Max(1000, common.PreConsumedQuota))*4*factor), price.QuotaToPreConsume)
			perCall, err = ModelPriceHelperPerCall(ctx, token)
			require.NoError(t, err)
			require.Equal(t, int(2*common.QuotaPerUnit*factor), perCall.Quota)
		})
	}
}

func TestModelDiscountConcurrentSnapshotReads(t *testing.T) {
	prepareModelDiscountTest(t, `{"users":{"7":{"m":0.25}}}`)
	info := modelDiscountRelayInfo(7, "m")
	want := HandleGroupRatio(nil, info) // Initialize before sharing with workers.
	var workers sync.WaitGroup
	for reader := 0; reader < 8; reader++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 100; i++ {
				if got := info.ResolveGroupRatio(); got != want {
					t.Errorf("pinned request changed: got %+v, want %+v", got, want)
					return
				}
				// New requests may capture either complete revision during updates.
				fresh := modelDiscountRelayInfo(7, "m")
				got := HandleGroupRatio(nil, fresh)
				if got.GroupRatio != 0.5 && got.GroupRatio != 1.5 {
					t.Errorf("partial rules observed: %+v", got)
					return
				}
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		for i := 0; i < 100; i++ {
			rules := `{"users":{"7":{"m":0.75}}}`
			if i%2 == 0 {
				rules = `{"users":{"7":{"m":0.25}}}`
			}
			if err := ratio_setting.UpdateModelDiscountRulesJSON(rules); err != nil {
				t.Errorf("update rules: %v", err)
				return
			}
		}
	}()
	workers.Wait()
}
