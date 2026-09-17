package controller

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestModelDiscountPersonalPricingIsolation(t *testing.T) {
	old := ratio_setting.ModelDiscountRulesJSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelDiscountRulesJSON(old)) })
	require.NoError(t, ratio_setting.UpdateModelDiscountRulesJSON(`{"groups":{"vip":{"m":0.8}},"users":{"1":{"m":0},"2":{"m":0.5}}}`))
	pricing := []model.Pricing{{ModelName: "m", EnableGroup: []string{"paid"}, ModelPrice: 2}, {ModelName: "alias", EnableGroup: []string{"all"}, ModelPrice: 3}}
	groups := map[string]float64{"paid": 2, "other": 4}
	a, ar := personalModelPricing(1, "vip", pricing, groups)
	b, br := personalModelPricing(2, "vip", pricing, groups)
	c, cr := personalModelPricing(3, "vip", pricing, groups)
	anonymous, anonymousRatios := personalModelPricing(0, "", pricing, groups)
	require.Equal(t, 0.0, a["m"].Factor)
	require.Equal(t, 0.0, ar["m"]["paid"])
	require.Equal(t, "user", b["m"].Source)
	require.Equal(t, 1.0, br["m"]["paid"])
	require.Equal(t, "group", c["m"].Source)
	require.Equal(t, 1.6, cr["m"]["paid"])
	require.NotContains(t, ar["m"], "other")
	require.Equal(t, 1.0, a["alias"].Factor)
	require.Empty(t, anonymous)
	require.Empty(t, anonymousRatios)
	ar["m"]["paid"] = 99
	require.Equal(t, 1.0, br["m"]["paid"])
	require.Equal(t, 2.0, groups["paid"])
	require.Equal(t, 2.0, pricing[0].ModelPrice)
	require.NotContains(t, a, "users")
}
