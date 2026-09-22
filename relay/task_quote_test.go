package relay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const quotePlugin = `
export const meta={apiVersion:1,key:"quote-test",name:"Quote test",version:"1.0.0",author:{name:"Test"},models:["quote-model"],fetchMode:"per_task",usageSchema:{seconds:{type:"number",unit:"second"}}};
export function buildSubmitRequest(ctx){
 if(typeof fetch!=="undefined" || typeof XMLHttpRequest!=="undefined" || typeof require!=="undefined") throw Error("unexpected network host");
 if(ctx.requestBody.seconds===undefined) throw Error("seconds required "+ctx.apiKey);
 return {url:ctx.baseUrl+"/submit",method:"POST",body:ctx.requestBody,action:"text_to_video"};
}
export function extractUsage(ctx){return {seconds:Number(ctx.requestBody.seconds)};}
export function parseSubmitResponse(){return {taskId:"test-id",taskData:{}};}
export function buildQueryRequest(ctx){return {url:ctx.baseUrl+"/query"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
`

func quoteContext(t *testing.T, source, baseURL string, body map[string]any) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "quote-model")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeTaskPlugin)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, baseURL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "secret-key")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	c.Set("task_request", body)
	pinMappingOrderPlugin(t, c, source)
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	require.NoError(t, err)
	return c, info
}

func setQuoteExpression(t *testing.T, expression string) {
	t.Helper()
	exprs, err := common.Marshal(map[string]string{"quote-model": expression})
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"quote-model":"tiered_expr"}`,
		"billing_setting.billing_expr": string(exprs), billing_setting.PluginBillingExprOption: `{}`,
	}))
}

func TestTaskQuoteMatchesWalletReservation(t *testing.T) {
	saveBillingConfig(t)
	oldPrices := ratio_setting.ModelPrice2JSONString()
	oldGroups := ratio_setting.GroupRatio2JSONString()
	oldDB, oldRedis := model.DB, common.RedisEnabled
	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedis
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
	})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	model.DB, common.RedisEnabled = db, false
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "quote-test", Quota: 100000000}).Error)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"quote-group":0.5}`))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"test-id"}`)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, expr string
		price      float64
		want       int
	}{
		{"USD expression", `tier("seconds",u("seconds")*0.2)`, 0, 250000},
		{"expression zero", `tier("free",0)`, 0, 0},
		{"legacy per call", "", 0.2, 250000},
		{"legacy explicit zero", "", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.expr != "" {
				setQuoteExpression(t, tc.expr)
			} else {
				require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"billing_setting.billing_mode": `{}`, "billing_setting.billing_expr": `{}`, billing_setting.PluginBillingExprOption: `{}`}))
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(fmt.Sprintf(`{"quote-model":%g}`, tc.price)))
			}
			body := map[string]any{"seconds": 5.0, "prompt": "secret-prompt", "url": "https://secret.example"}
			c, info := quoteContext(t, quotePlugin, server.URL, body)
			c.Set("auto_group", "quote-group")
			before, err := model.GetUserQuota(1, true)
			require.NoError(t, err)
			count := requests.Load()
			quote, taskErr := QuoteTaskSubmission(c, info)
			require.Nil(t, taskErr, "%+v", taskErr)
			require.NotNil(t, quote)
			assert.Equal(t, tc.want, quote.Quota)
			assert.Equal(t, "quote-group", quote.Group)
			assert.Empty(t, quote.MissingFields)
			assert.Nil(t, info.Billing)
			assert.False(t, info.ForcePreConsume)
			assert.Equal(t, count, requests.Load())
			after, err := model.GetUserQuota(1, true)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			wire, err := common.Marshal(quote)
			require.NoError(t, err)
			assert.NotContains(t, string(wire), "secret")
			assert.NotContains(t, string(wire), server.URL)
			if tc.expr != "" {
				assert.Equal(t, billingexpr.ExprHashString(tc.expr), quote.ExpressionHash)
			}
			c, submit := quoteContext(t, quotePlugin, server.URL, body)
			c.Set("auto_group", "quote-group")
			submit.UserId = 1
			submit.IsPlayground = true
			submit.UserSetting.BillingPreference = "wallet_only"
			result, taskErr := RelayTaskSubmit(c, submit)
			require.Nil(t, taskErr, "%+v", taskErr)
			require.NotNil(t, result)
			assert.Equal(t, quote.Quota, result.Quota)
			if submit.Billing != nil {
				assert.Equal(t, quote.Quota, submit.Billing.GetPreConsumedQuota())
			} else {
				assert.Zero(t, quote.Quota)
			}
			after, err = model.GetUserQuota(1, true)
			require.NoError(t, err)
			assert.Equal(t, quote.Quota, before-after)
			assert.Equal(t, count+1, requests.Load())
		})
	}
}

func TestTaskQuoteRejectsUnsafeOrInvalidRequests(t *testing.T) {
	saveBillingConfig(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	for _, tc := range []struct {
		name, source, expr, code string
		body                     map[string]any
	}{
		{"missing seconds", quotePlugin, `tier("base",1)`, "plugin_request_invalid", map[string]any{}},
		{"duration bound", quotePlugin, `tier("base",1)`, "plugin_usage_invalid", map[string]any{"seconds": 3601.0}},
		{"negative usage", quotePlugin, `tier("base",1)`, "plugin_usage_invalid", map[string]any{"seconds": -1.0}},
		{"quota overflow", quotePlugin, `tier("base",1e100)`, "model_price_error", map[string]any{"seconds": 1.0}},
		{"negative price", quotePlugin, `tier("base",-1)`, "model_price_error", map[string]any{"seconds": 1.0}},
		{"fixed is not task USD", quotePlugin, `tier("base",fixed(1))`, "model_price_error", map[string]any{"seconds": 1.0}},
		{"network auth", strings.Replace(quotePlugin, `fetchMode:"per_task"`, `fetchMode:"per_task",auth:{type:"oauth2_jwt"}`, 1), `tier("base",1)`, "task_quote_unknown", map[string]any{"seconds": 1.0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setQuoteExpression(t, tc.expr)
			c, info := quoteContext(t, tc.source, server.URL, tc.body)
			quote, taskErr := QuoteTaskSubmission(c, info)
			require.Nil(t, quote)
			require.NotNil(t, taskErr)
			assert.Equal(t, tc.code, taskErr.Code)
			assert.Nil(t, info.Billing)
			assert.False(t, info.ForcePreConsume)
			wire, err := common.Marshal(taskErr)
			require.NoError(t, err)
			assert.NotContains(t, string(wire), "secret-key")
			assert.NotContains(t, string(wire), server.URL)
		})
	}
	assert.Zero(t, requests.Load())
	quote, taskErr := QuoteTaskSubmission(nil, nil)
	assert.Nil(t, quote)
	require.NotNil(t, taskErr)
	assert.Equal(t, "invalid_request", taskErr.Code)
	c, info := quoteContext(t, quotePlugin, server.URL, map[string]any{"seconds": 1.0})
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{})
	quote, taskErr = QuoteTaskSubmission(c, info)
	assert.Nil(t, quote)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_quote_unknown", taskErr.Code)
}

func TestDoubaoQuoteRequiresExplicitOutputAndNoReferenceVideo(t *testing.T) {
	snapshot := &billingexpr.BillingSnapshot{TaskRequestParams: map[string]any{"seconds": "5", "resolution": "720p"}, UsageFacts: map[string]any{"tokens": 108000.0, "resolution": "720p", "video_input": "none"}}
	assert.True(t, quoteHasExplicitDoubaoOutput(snapshot))
	snapshot.UsageFacts["video_input"] = "video"
	assert.False(t, quoteHasExplicitDoubaoOutput(snapshot))
	snapshot.UsageFacts["video_input"] = "none"
	snapshot.UsageFacts["tokens"] = 999.0
	assert.False(t, quoteHasExplicitDoubaoOutput(snapshot))
	snapshot.UsageFacts["tokens"] = 108000.0
	delete(snapshot.TaskRequestParams, "seconds")
	assert.False(t, quoteHasExplicitDoubaoOutput(snapshot))
}

func TestTaskQuoteDefaultedZeroIsNotFree(t *testing.T) {
	saveBillingConfig(t)
	setQuoteExpression(t, `tier("seconds",u("seconds")*0.1)`)
	source := strings.Replace(quotePlugin, `if(ctx.requestBody.seconds===undefined) throw Error("seconds required "+ctx.apiKey);`, ``, 1)
	source = strings.Replace(source, `Number(ctx.requestBody.seconds)`, `Number(ctx.requestBody.seconds || 0)`, 1)
	for _, tc := range []struct {
		name    string
		body    map[string]any
		missing bool
	}{
		{"absent", map[string]any{}, true},
		{"empty", map[string]any{"seconds": ""}, true},
		{"explicit zero", map[string]any{"seconds": 0.0}, false},
		{"valid seconds", map[string]any{"seconds": 5.0}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, info := quoteContext(t, source, "https://provider.example", tc.body)
			quote, taskErr := QuoteTaskSubmission(c, info)
			if tc.name == "empty" {
				require.NotNil(t, taskErr)
				assert.Equal(t, "plugin_usage_invalid", taskErr.Code)
				assert.Nil(t, quote)
				return
			}
			require.Nil(t, taskErr, "%+v", taskErr)
			assert.Equal(t, tc.missing, len(quote.MissingFields) > 0)
			assert.Nil(t, info.Billing)
		})
	}
}

func TestTaskQuoteFutureUsageIsNotCertain(t *testing.T) {
	saveBillingConfig(t)
	setQuoteExpression(t, `tier("base",u("seconds")*0.2)`)
	source := strings.Replace(quotePlugin, `unit:"second"`, `unit:"credit"`, 1)
	c, info := quoteContext(t, source, "https://provider.example", map[string]any{"seconds": 1.0})
	quote, taskErr := QuoteTaskSubmission(c, info)
	require.Nil(t, taskErr, "%+v", taskErr)
	assert.Equal(t, []string{"seconds"}, quote.MissingFields)
	assert.Positive(t, quote.Quota)
	assert.True(t, quote.Estimated)
	assert.Nil(t, info.Billing)
}
