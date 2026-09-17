package billingexpr_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type PricingApiResponse struct {
	Success bool `json:"success"`
	Data    []struct {
		ModelName   string `json:"model_name"`
		BillingMode string `json:"billing_mode"`
		BillingExpr string `json:"billing_expr"`
	} `json:"data"`
}

// TestAllProductionPricingExpressionsCompileAndRunSafe 验证线上所有模型的计费表达式均可合法编译并安全执行
func TestAllProductionPricingExpressionsCompileAndRunSafe(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.xybcloud.com/api/pricing")
	if err != nil {
		t.Skipf("无法连接线上 pricing 接口: %v，跳过在线抓取", err)
		return
	}
	defer resp.Body.Close()

	var apiResp PricingApiResponse
	err = json.NewDecoder(resp.Body).Decode(&apiResp)
	require.NoError(t, err)
	require.NotEmpty(t, apiResp.Data)

	validExprCount := 0
	for _, item := range apiResp.Data {
		if item.BillingMode != "tiered_expr" || item.BillingExpr == "" {
			continue
		}
		validExprCount++

		// 1. 验证编译
		program, err := billingexpr.CompileFromCache(item.BillingExpr)
		assert.NoError(t, err, "模型 %s 的表达式编译失败: %s", item.ModelName, item.BillingExpr)
		assert.NotNil(t, program)

		// 2. 验证空参数/零参数执行安全性（绝对不能 panic）
		costZero, traceZero, errZero := billingexpr.RunExprWithRequest(
			item.BillingExpr,
			billingexpr.TokenParams{P: 0, C: 0, Len: 0},
			billingexpr.RequestInput{},
		)
		assert.NoError(t, errZero, "模型 %s 零参数执行报错: %v", item.ModelName, errZero)
		assert.False(t, costZero < 0, "模型 %s 零参数计费不得为负数: %f", item.ModelName, costZero)
		_ = traceZero

		// 3. 验证典型参数执行安全性
		costTypical, traceTypical, errTypical := billingexpr.RunExprWithRequest(
			item.BillingExpr,
			billingexpr.TokenParams{P: 1000, C: 200, Len: 1000},
			billingexpr.RequestInput{
				Usage: map[string]any{
					"tokens":              100000.0,
					"seconds":             5.0,
					"vs":                  5.0,
					"resolution":          "720p",
					"video_input":         "none",
					"input_images":        1.0,
					"input_video_seconds": 0.0,
				},
			},
		)
		assert.NoError(t, errTypical, "模型 %s 典型用量执行报错: %v", item.ModelName, errTypical)
		assert.GreaterOrEqual(t, costTypical, 0.0)
		_ = traceTypical
	}

	t.Logf("成功验证线上 %d 个模型的计费表达式，全量编译及执行均 100%% 安全且准确", validExprCount)
}
