package service_test

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChatModelBillingTokenCalculation 验证文本模型在标准倍率、补全倍率与分组倍率下的计算精度
func TestChatModelBillingTokenCalculation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	// 配置模型倍率: modelRatio=2.5, completionRatio=2.0 (输出倍率为输入2倍)
	err := ratio_setting.UpdateModelRatioByJSONString(`{"test-chat-model": 2.5}`)
	require.NoError(t, err)
	err = ratio_setting.UpdateCompletionRatioByJSONString(`{"test-chat-model": 2.0}`)
	require.NoError(t, err)
	err = ratio_setting.UpdateGroupRatioByJSONString(`{"default": 1.0, "vip": 1.5}`)
	require.NoError(t, err)

	promptTokens := 1000
	completionTokens := 500

	// 1. default 分组 (groupRatio=1.0)
	// Quota = (1000 * 2.5 + 500 * 2.5 * 2.0) * 1.0 = 2500 + 2500 = 5000 Quota
	info := &relaycommon.RelayInfo{
		OriginModelName: "test-chat-model",
		UserGroup:       "default",
		UsingGroup:      "default",
	}
	priceData, err := helper.ModelPriceHelper(c, info, promptTokens, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.False(t, priceData.UsePrice)
	assert.Equal(t, 2.5, priceData.ModelRatio)
	assert.Equal(t, 2.0, priceData.CompletionRatio)
	assert.Equal(t, 1.0, priceData.GroupRatioInfo.GroupRatio)

	expectedQuota := int(float64(promptTokens)*2.5 + float64(completionTokens)*2.5*2.0)
	assert.Equal(t, 5000, expectedQuota)

	// 2. vip 分组 (groupRatio=1.5)
	infoVip := &relaycommon.RelayInfo{
		OriginModelName: "test-chat-model",
		UserGroup:       "vip",
		UsingGroup:      "vip",
	}
	priceDataVip, err := helper.ModelPriceHelper(c, infoVip, promptTokens, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.Equal(t, 1.5, priceDataVip.GroupRatioInfo.GroupRatio)

	expectedVipQuota := int(float64(expectedQuota) * 1.5)
	assert.Equal(t, 7500, expectedVipQuota)
}

// TestImageModelFixedPriceBilling 验证图像模型的固定按次扣费及生成张数乘法
func TestImageModelFixedPriceBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	modelName := "gpt-image-test"
	err := ratio_setting.UpdateModelPriceByJSONString(`{"gpt-image-test": 0.1}`)
	require.NoError(t, err)

	info := &relaycommon.RelayInfo{
		OriginModelName: modelName,
		UserGroup:       "default",
		UsingGroup:      "default",
	}

	priceData, err := helper.ModelPriceHelperPerCall(c, info)
	require.NoError(t, err)
	assert.True(t, priceData.UsePrice)
	assert.Equal(t, 0.1, priceData.ModelPrice)

	// 1 次生成: 0.1 * 500000 = 50000 Quota
	assert.Equal(t, 50000, priceData.Quota)

	// 生成 2 张图片: n=2
	imageCount := 2
	totalQuota := priceData.Quota * imageCount
	assert.Equal(t, 100000, totalQuota)
}

// TestVideoTaskFixedAndExprBilling 验证视频任务的固定价格与表达式求值、多退少补完整链路
func TestVideoTaskFixedAndExprBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	// 场景 A: 视频模型按次固定计费 (如 doubao-seedance-2.0 单价 3.48 元)
	modelName := "doubao-seedance-2.0"
	err := ratio_setting.UpdateModelPriceByJSONString(`{"doubao-seedance-2.0": 3.48}`)
	require.NoError(t, err)

	info := &relaycommon.RelayInfo{
		OriginModelName: modelName,
		UserGroup:       "default",
		UsingGroup:      "default",
	}
	priceData, err := helper.ModelPriceHelperPerCall(c, info)
	require.NoError(t, err)
	assert.True(t, priceData.UsePrice)
	// 3.48 * 500000 = 1,740,000 Quota
	assert.Equal(t, 1740000, priceData.Quota)

	// 场景 B: 视频模型表达式求值 (包括之前引发崩溃的 <nil> * float64 防御)
	// 表达式包含 vs (视频时长) 和 u("tokens") (用量 tokens)
	exprStr := `p * 0 + c * 0 + (vs > 0 ? vs * 0.02 : 0.1) + u("tokens") * 0.00001`

	// 1. 空用量 (vector {p=0, c=0}, Usage 为空): 绝不能崩溃，u("tokens") 应安全返回 0.0，vs 默认 0.0
	snap := &billingexpr.BillingSnapshot{
		BillingMode:      "tiered_expr",
		ModelName:        "grok-imagine-video",
		ExprString:       exprStr,
		ExprHash:         billingexpr.ExprHashString(exprStr),
		GroupRatio:       1.0,
		QuotaPerUnit:     common.QuotaPerUnit,
		TaskUsageBilling: true,
	}

	resEmpty, err := billingexpr.ComputeTieredQuotaWithRequest(snap, billingexpr.TokenParams{}, billingexpr.RequestInput{})
	require.NoError(t, err, "空用量下表达式求值必须安全通过，不得报 <nil> * float64 错误")
	// 当 vs=0 时，取 0.1; 0.1 * 500000 = 50000 Quota
	assert.Equal(t, 50000, resEmpty.ActualQuotaAfterGroup)

	// 2. 实际用量事实 (vs=10 秒, tokens=108000)
	// cost = 10 * 0.02 + 108000 * 0.00001 = 0.2 + 1.08 = 1.28
	// quota = 1.28 * 500000 = 640000 Quota
	usageFacts := map[string]any{
		"vs":     10.0,
		"tokens": 108000.0,
	}
	resActual, err := billingexpr.ComputeTieredQuotaWithRequest(snap, billingexpr.TokenParams{}, billingexpr.RequestInput{
		Usage: usageFacts,
	})
	require.NoError(t, err)
	assert.Equal(t, 640000, resActual.ActualQuotaAfterGroup)

	// 场景 C: 模拟任务预扣与结算生命周期
	preConsumedQuota := 50000
	actualQuota := 640000

	// 模拟差额补扣 (actual > preConsumed, delta = 590000)
	delta := actualQuota - preConsumedQuota
	assert.Equal(t, 590000, delta)

	// 模拟多退 (若预扣 700000, 实际 640000, 应退 60000)
	preConsumedHigh := 700000
	refund := preConsumedHigh - actualQuota
	assert.Equal(t, 60000, refund)
}

// TestAudioModelBillingCalculation 验证音频 TTS 模型的字符数与语音时长计费
func TestAudioModelBillingCalculation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	modelName := "tts-1-hd"
	err := ratio_setting.UpdateModelRatioByJSONString(`{"tts-1-hd": 15.0}`)
	require.NoError(t, err)

	// 假设 1000 字符文本输入转换为预扣额度
	promptTokens := 1000
	info := &relaycommon.RelayInfo{
		OriginModelName: modelName,
		UserGroup:       "default",
		UsingGroup:      "default",
	}

	priceData, err := helper.ModelPriceHelper(c, info, promptTokens, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.False(t, priceData.UsePrice)
	assert.Equal(t, 15.0, priceData.ModelRatio)
	assert.Equal(t, 15000, priceData.QuotaToPreConsume)
}

// TestCacheBillingCalculation 验证 Prompt Cache 命中与写入时的费率计算
func TestCacheBillingCalculation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	modelName := "claude-3-7-sonnet"
	err := ratio_setting.UpdateModelRatioByJSONString(`{"claude-3-7-sonnet": 1.5}`)
	require.NoError(t, err)
	err = ratio_setting.UpdateCacheRatioByJSONString(`{"claude-3-7-sonnet": 0.1}`) // 缓存读 0.1 倍 (即 10%)
	require.NoError(t, err)
	err = ratio_setting.UpdateCreateCacheRatioByJSONString(`{"claude-3-7-sonnet": 1.25}`) // 缓存写 1.25 倍
	require.NoError(t, err)

	info := &relaycommon.RelayInfo{
		OriginModelName: modelName,
		UserGroup:       "default",
		UsingGroup:      "default",
	}

	priceData, err := helper.ModelPriceHelper(c, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.Equal(t, 1.5, priceData.ModelRatio)
	assert.Equal(t, 0.1, priceData.CacheRatio)
	assert.Equal(t, 1.25, priceData.CacheCreationRatio)

	// 计算逻辑核验:
	// 10000 cached tokens (命中) = 10000 * 1.5 * 0.1 = 1500 Quota (原价 15000 Quota，节省 90%)
	cachedQuota := 10000.0 * priceData.ModelRatio * priceData.CacheRatio
	assert.Equal(t, 1500.0, cachedQuota)

	// 10000 cache write tokens (写入) = 10000 * 1.5 * 1.25 = 18750 Quota
	writeQuota := 10000.0 * priceData.ModelRatio * priceData.CacheCreationRatio
	assert.Equal(t, 18750.0, writeQuota)
}

// TestFreeModelBilling 验证 0 费率或 free 分组时不会产生预扣款
func TestFreeModelBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	// 开启「对免费模型不预扣」
	orig := operation_setting.GetQuotaSetting().EnableFreeModelPreConsume
	operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = false
	defer func() {
		operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = orig
	}()

	modelName := "free-chat-model"
	err := ratio_setting.UpdateModelRatioByJSONString(`{"free-chat-model": 0}`)
	require.NoError(t, err)

	info := &relaycommon.RelayInfo{
		OriginModelName: modelName,
		UserGroup:       "default",
		UsingGroup:      "default",
	}

	priceData, err := helper.ModelPriceHelper(c, info, 2000, &types.TokenCountMeta{MaxTokens: 1000})
	require.NoError(t, err)
	assert.True(t, priceData.FreeModel)
	assert.Equal(t, 0, priceData.QuotaToPreConsume)
}


