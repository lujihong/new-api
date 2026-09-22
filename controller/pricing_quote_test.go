package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPricingQuoteHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(k, v string) error { saved[k] = v; return nil }))
	oldPrices := ratio_setting.ModelPrice2JSONString()
	oldDB, oldRedis, oldMemory := model.DB, common.RedisEnabled, common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open("file:pricing-quote?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	oldLogDB, oldMaster := model.LOG_DB, common.IsMasterNode
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.IsMasterNode = false
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	t.Cleanup(func() {
		model.LOG_DB, common.IsMasterNode = oldLogDB, oldMaster
		common.SetDatabaseTypes(oldMainType, oldLogType)
		model.DB = oldDB
		common.RedisEnabled = oldRedis
		common.MemoryCacheEnabled = oldMemory
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
		sqlDB, e := db.DB()
		if e == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Task{}))
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":    `{"quote-fixed":"tiered_expr","quote-token":"tiered_expr","quote-param":"tiered_expr","quote-image":"tiered_expr","quote-usage":"tiered_expr","dall-e-3":"ratio"}`,
		"billing_setting.billing_expr":    `{"quote-fixed":"tier(\"request\", fixed(0.000003))","quote-token":"tier(\"base\", p * 2 + c * 8)","quote-param":"param(\"quality\") == \"hd\" ? tier(\"hd\", fixed(0.02)) : tier(\"standard\", fixed(0.01))","quote-image":"tier(\"image\", fixed(0.01)) * image_count","quote-usage":"tier(\"usage\", u(\"output_tokens\") * 2)"}`,
		"group_ratio_setting.group_ratio": `{"default":1}`,
	}))
	oldUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldUnit })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"dall-e-3":0.04,"quote-legacy":0.000003,"quote-tiny":0.0000008,"quote-zero":0,"quote-search-preview":0.000003}`))
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { upstreamCalls.Add(1); w.WriteHeader(500) }))
	defer upstream.Close()
	user := model.User{Id: 197301, Username: "quote-user", AffCode: "quote-user", Group: "default", Status: common.UserStatusEnabled, Quota: 1000000}
	require.NoError(t, db.Create(&user).Error)
	key := strings.Repeat("q", 48)
	token := model.Token{UserId: user.Id, Key: key, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000000, ModelLimitsEnabled: true, ModelLimits: "quote-fixed,quote-token,quote-param,quote-image,quote-usage,dall-e-3,missing-route,quote-legacy,quote-tiny,quote-zero,quote-search-preview"}
	require.NoError(t, db.Create(&token).Error)
	channel := model.Channel{Id: 197301, Name: "quote", Type: constant.ChannelTypeOpenAI, Key: "first\nsecond", Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL, Group: "default", Models: token.ModelLimits, ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeyPollingIndex: 1, MultiKeyMode: constant.MultiKeyModePolling}}
	require.NoError(t, db.Create(&channel).Error)
	for _, name := range strings.Split(token.ModelLimits, ",") {
		if name != "missing-route" {
			require.NoError(t, db.Create(&model.Ability{Group: "default", Model: name, ChannelId: channel.Id, Enabled: true}).Error)
		}
	}
	engine := gin.New()
	engine.POST("/api/pricing/quote", middleware.TokenAuth(), middleware.QuoteRequest(), middleware.PinTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(), middleware.Distribute(), Quote)
	for _, tc := range []struct {
		name, endpoint, body, status string
		batch, code                  int
		quota                        *int
		missing                      string
		zeroGroup                    bool
	}{
		{name: "legacy text decimal half", endpoint: "/v1/chat/completions", body: `{"model":"quote-legacy","messages":[{"role":"user","content":"hi"}]}`, batch: 1, code: 200, status: "estimated", quota: common.GetPointer(2)},
		{name: "legacy text rounded before batch", endpoint: "/v1/chat/completions", body: `{"model":"quote-legacy","messages":[{"role":"user","content":"hi"}]}`, batch: 3, code: 200, status: "estimated", quota: common.GetPointer(6)},
		{name: "legacy responses decimal half", endpoint: "/v1/responses", body: `{"model":"quote-legacy","input":"hi"}`, batch: 1, code: 200, status: "estimated", quota: common.GetPointer(2)},
		{name: "legacy tiny has zero model ratio", endpoint: "/v1/chat/completions", body: `{"model":"quote-tiny","messages":[{"role":"user","content":"hi"}]}`, batch: 3, code: 200, status: "estimated", quota: common.GetPointer(0)},
		{name: "legacy free price", endpoint: "/v1/chat/completions", body: `{"model":"quote-zero","messages":[{"role":"user","content":"hi"}]}`, batch: 1, code: 200, status: "estimated", quota: common.GetPointer(0)},
		{name: "legacy free group", endpoint: "/v1/chat/completions", body: `{"model":"quote-legacy","messages":[{"role":"user","content":"hi"}]}`, batch: 1, code: 200, status: "estimated", quota: common.GetPointer(0), zeroGroup: true},
		{name: "legacy image still truncates", endpoint: "/v1/images/generations", body: `{"model":"quote-legacy","prompt":"cat","n":1}`, batch: 3, code: 200, status: "estimated", quota: common.GetPointer(3)},
		{name: "legacy audio path unknown", endpoint: "/v1/audio/speech", body: `{"model":"quote-legacy","input":"hi"}`, batch: 1, code: 200, status: "usage_required", missing: "usage.audio_output_tokens"},
		{name: "legacy tools unknown", endpoint: "/v1/chat/completions", body: `{"model":"quote-legacy","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"search"}}]}`, batch: 3, code: 200, status: "usage_required", missing: "usage.tool_calls"},
		{name: "expression tools unknown", endpoint: "/v1/responses", body: `{"model":"quote-fixed","input":"hi","tools":[{"type":"web_search_preview"}]}`, batch: 1, code: 200, status: "usage_required", missing: "usage.tool_calls"},
		{name: "implicit search surcharge", endpoint: "/v1/chat/completions", body: `{"model":"quote-search-preview","messages":[{"role":"user","content":"hi"}]}`, batch: 1, code: 200, status: "usage_required", missing: "usage.tool_calls"},
		{name: "fixed rounded before batch", endpoint: "/v1/chat/completions", body: `{"model":"quote-fixed","messages":[{"role":"user","content":"hi"}]}`, batch: 3, code: 200, status: "estimated", quota: common.GetPointer(6)},
		{name: "token unknown", endpoint: "/v1/chat/completions", body: `{"model":"quote-token","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`, batch: 1, code: 200, status: "usage_required", missing: "usage.output_tokens"},
		{name: "missing request parameter", endpoint: "/v1/audio/speech", body: `{"model":"quote-param","input":"hi"}`, batch: 1, code: 200, status: "usage_required", missing: "body.quality"},
		{name: "speech fixed", endpoint: "/v1/audio/speech", body: `{"model":"quote-fixed","input":"hi"}`, batch: 1, code: 200, status: "estimated", quota: common.GetPointer(2)},
		{name: "responses token unknown", endpoint: "/v1/responses", body: `{"model":"quote-token","input":"hi"}`, batch: 1, code: 200, status: "usage_required", missing: "usage.output_tokens"},
		{name: "usage not zero", endpoint: "/v1/audio/speech", body: `{"model":"quote-usage","input":"hi"}`, batch: 1, code: 200, status: "usage_required", missing: "usage.output_tokens"},
		{name: "image quantity", endpoint: "/v1/images/generations", body: `{"model":"quote-image","prompt":"cat","n":3}`, batch: 2, code: 200, status: "estimated", quota: common.GetPointer(30000)},
		{name: "image quality and quantity", endpoint: "/v1/images/generations", body: `{"model":"dall-e-3","prompt":"cat","n":2,"quality":"hd","size":"1024x1792"}`, batch: 2, code: 200, status: "estimated", quota: common.GetPointer(240000)},
		{name: "image edit quantity", endpoint: "/v1/images/edits", body: `{"model":"quote-image","prompt":"cat","n":2,"image":"https://example.invalid/image.png"}`, batch: 1, code: 200, status: "estimated", quota: common.GetPointer(10000)},
		{name: "unknown native task", endpoint: "/v1/videos", body: `{"model":"quote-fixed","prompt":"cat"}`, batch: 1, code: 200, status: "unavailable"},
		{name: "model forbidden", endpoint: "/v1/audio/speech", body: `{"model":"forbidden","input":"hi"}`, batch: 1, code: 403},
		{name: "no channel", endpoint: "/v1/audio/speech", body: `{"model":"missing-route","input":"hi"}`, batch: 1, code: 503},
		{name: "oversized count", endpoint: "/v1/images/generations", body: `{"model":"quote-image","n":129}`, batch: 1, code: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.zeroGroup {
				require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"group_ratio_setting.group_ratio": `{"default":0}`}))
				t.Cleanup(func() {
					require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"group_ratio_setting.group_ratio": `{"default":1}`}))
				})
			}
			body := fmt.Sprintf(`{"endpoint":%q,"body":%s,"batch_count":%d}`, tc.endpoint, tc.body, tc.batch)
			req := httptest.NewRequest("POST", "/api/pricing/quote", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer sk-"+key)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			require.Equal(t, tc.code, w.Code, w.Body.String())
			if tc.code != 200 {
				return
			}
			var response struct {
				Success bool         `json:"success"`
				Data    PricingQuote `json:"data"`
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
			assert.True(t, response.Success)
			assert.Equal(t, tc.status, response.Data.Status)
			assert.Equal(t, tc.quota, response.Data.Quota)
			if response.Data.Status == "estimated" && response.Data.BillingMode == "per_call" {
				assert.Contains(t, response.Data.Message, "successful billable request")
				assert.Contains(t, response.Data.Message, "zero charge")
				require.Len(t, response.Data.UnitRates, 1)
				assert.Equal(t, float64(*tc.quota)/float64(tc.batch), response.Data.UnitRates[0].Quota)
				assert.True(t, response.Data.UnitRates[0].Conditional)
			}
			if tc.missing != "" {
				assert.Contains(t, response.Data.MissingFields, tc.missing)
			}
			assert.NotContains(t, w.Body.String(), "first")
			assert.NotContains(t, w.Body.String(), key)
			if tc.name == "token unknown" {
				require.Len(t, response.Data.UnitRates, 2)
				assert.Equal(t, 1.0, response.Data.UnitRates[0].Quota)
				assert.Equal(t, 4.0, response.Data.UnitRates[1].Quota)
			}
		})
	}
	unauth := httptest.NewRecorder()
	engine.ServeHTTP(unauth, httptest.NewRequest("POST", "/api/pricing/quote", strings.NewReader(`{}`)))
	assert.Equal(t, 401, unauth.Code)
	var afterUser model.User
	require.NoError(t, db.First(&afterUser, user.Id).Error)
	assert.Equal(t, user.Quota, afterUser.Quota)
	assert.Zero(t, afterUser.UsedQuota)
	var afterToken model.Token
	require.NoError(t, db.First(&afterToken, token.Id).Error)
	assert.Equal(t, token.RemainQuota, afterToken.RemainQuota)
	assert.Zero(t, afterToken.UsedQuota)
	var afterChannel model.Channel
	require.NoError(t, db.First(&afterChannel, channel.Id).Error)
	assert.Equal(t, 1, afterChannel.ChannelInfo.MultiKeyPollingIndex)
	assert.Zero(t, afterChannel.UsedQuota)
	var tasks int64
	require.NoError(t, db.Model(&model.Task{}).Count(&tasks).Error)
	assert.Zero(t, tasks)
	assert.Zero(t, upstreamCalls.Load())
}

func TestQuoteRequestBoundariesAndContext(t *testing.T) {
	for _, body := range []string{
		`{"endpoint":"https://example.com/v1/videos","body":{"model":"x"}}`,
		`{"endpoint":"/v1/videos?key=x","body":{"model":"x"}}`,
		`{"endpoint":"/v1/../videos","body":{"model":"x"}}`,
		`{"endpoint":"/v1/videos","body":[]}`,
		`{"endpoint":"/v1/videos","body":{}}`,
		`{"endpoint":"/v1/videos","body":{"model":"x","model":"y"}}`,
		`{"endpoint":"/v1/videos","body":{"model":"x","usage":{}}}`,
		`{"endpoint":"/v1/videos","body":{"model":"x","groupRatio":0}}`,
		`{"endpoint":"/v1/videos","body":{"model":"x","key":"secret"}}`,
		`{"endpoint":"/v1/videos","body":{"model":"x"},"batch_count":0}`,
		`{"endpoint":"/v1/videos","body":{"model":"x"},"batch_count":21}`,
		strings.Repeat(" ", 1<<20) + `{}`,
	} {
		engine := gin.New()
		engine.POST("/quote", middleware.QuoteRequest(), func(c *gin.Context) { t.Error("invalid quote reached handler") })
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest("POST", "/quote", strings.NewReader(body)))
		assert.Equal(t, 400, w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	engine := gin.New()
	wrapper := `{"endpoint":"/v1/audio/speech","body":{"model":"x","input":"hi"}}`
	engine.POST("/quote", func(c *gin.Context) {
		storage, err := common.CreateBodyStorage([]byte(wrapper))
		require.NoError(t, err)
		c.Set(common.KeyBodyStorage, storage)
		c.Set(common.KeyRequestBody, []byte(wrapper))
		c.Set("user_id", 77)
	}, middleware.QuoteRequest(), func(c *gin.Context) {
		assert.Equal(t, ctx, c.Request.Context())
		assert.ErrorIs(t, c.Request.Context().Err(), context.Canceled)
		assert.Equal(t, "Bearer sk-local", c.GetHeader("Authorization"))
		assert.Equal(t, 77, c.GetInt("user_id"))
		assert.Equal(t, "/v1/audio/speech", c.Request.URL.Path)
		var body map[string]any
		require.NoError(t, common.UnmarshalBodyReusable(c, &body))
		assert.Equal(t, "x", body["model"])
		assert.NotContains(t, body, "endpoint")
		c.Status(204)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/quote", bytes.NewBufferString(wrapper)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer sk-local")
	engine.ServeHTTP(w, req)
	assert.Equal(t, 204, w.Code)
}

func TestQuoteExpressionDoesNotInventRates(t *testing.T) {
	assert.Empty(t, quoteLinearTokenRates(`len < 1000 ? tier("a", p * 2) : tier("b", p * 4)`, 1))
	assert.Contains(t, quoteExpressionMissing(`tier("a", u(param("dimension")) * 2)`, billingexpr.RequestInput{}), "usage")
	assert.Empty(t, quoteExpressionMissing(`tier("free", fixed(0))`, billingexpr.RequestInput{}))
}
