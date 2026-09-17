package controller

import (
	"maps"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func filterPricingByUsableGroups(pricing []model.Pricing, usableGroup map[string]string) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		if common.StringsContains(item.EnableGroup, "all") {
			filtered = append(filtered, item)
			continue
		}
		for _, group := range item.EnableGroup {
			if _, ok := usableGroup[group]; ok {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

// Return detached current-user-only values, never mutate shared pricing rows.
func personalModelPricing(userID int, group string, pricing []model.Pricing, groupRatio map[string]float64) (map[string]ratio_setting.ModelDiscountSelection, map[string]map[string]float64) {
	discounts := make(map[string]ratio_setting.ModelDiscountSelection)
	ratios := make(map[string]map[string]float64)
	if userID <= 0 || group == "" {
		return discounts, ratios
	}
	snapshot := ratio_setting.CaptureModelDiscountRules()
	for _, item := range pricing {
		selection := snapshot.Lookup(userID, group, item.ModelName)
		discounts[item.ModelName] = selection
		values := make(map[string]float64)
		for usingGroup, base := range groupRatio {
			if common.StringsContains(item.EnableGroup, "all") || common.StringsContains(item.EnableGroup, usingGroup) {
				values[usingGroup] = base * selection.Factor
			}
		}
		ratios[item.ModelName] = values
	}
	return discounts, ratios
}

func GetPricing(c *gin.Context) {
	pricing := model.GetPricing()
	userId, exists := c.Get("id")
	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	maps.Copy(groupRatio, ratio_setting.GetGroupRatioCopy())
	var group string
	if exists {
		user, err := model.GetUserCache(userId.(int))
		if err != nil {
			c.Header("Cache-Control", "private, no-store")
			c.JSON(503, gin.H{"success": false, "message": "暂时无法读取当前账号报价，请稍后重试"})
			return
		}
		group = user.Group
		for g := range groupRatio {
			ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
			if ok {
				groupRatio[g] = ratio
			}
		}
	}

	usableGroup = service.GetUserUsableGroups(group)
	pricing = filterPricingByUsableGroups(pricing, usableGroup)
	// check groupRatio contains usableGroup
	for group := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroup[group]; !ok {
			delete(groupRatio, group)
		}
	}

	modelDiscounts, modelGroupRatios := personalModelPricing(c.GetInt("id"), group, pricing, groupRatio)
	c.Header("Cache-Control", "private, no-store")
	c.JSON(200, gin.H{
		"model_discounts":    modelDiscounts,
		"model_group_ratio":  modelGroupRatios,
		"success":            true,
		"data":               pricing,
		"vendors":            model.GetVendors(),
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"supported_endpoint": model.GetSupportedEndpointMap(),
		"auto_groups":        service.GetUserAutoGroup(group),
		"pricing_version":    "a42d372ccf0b5dd13ecf71203521f9d2",
	})
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	err := model.UpdateOption("ModelRatio", defaultStr)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	err = ratio_setting.UpdateModelRatioByJSONString(defaultStr)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "重置模型倍率成功",
	})
}
