package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func aiccTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/aicc.db"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Ability{}, &model.Channel{}, &model.AICCAccountAttestation{}, &model.AICCAuthSession{}, &model.AICCAssetGroupOwnership{}, &model.AICCAssetOwnership{}))
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "aicc-fixture", AffCode: "aicc-fixture", Group: "default", Status: common.UserStatusEnabled}).Error)
	return db
}

func aiccTestInfo(t *testing.T, endpoint string) string {
	t.Helper()
	info, err := common.Marshal(map[string]any{
		"aicc_enabled": true, "access_key_id": "offline-ak", "access_key_secret": "offline-sk",
		"endpoint": endpoint, "pool_id": "CIDC-CORE-00",
	})
	require.NoError(t, err)
	return string(info)
}

func aiccTestChannel(t *testing.T, db *gorm.DB, id int, endpoint string) service.AICCConfig {
	t.Helper()
	require.NoError(t, db.Create(&model.Channel{Id: id, Type: 54, Name: "AICC fixture", Status: common.ChannelStatusEnabled, OtherInfo: aiccTestInfo(t, endpoint)}).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "aicc-test-model", ChannelId: id, Enabled: true}).Error)
	cfg, err := service.GetAICCConfigForChannel(id)
	require.NoError(t, err)
	require.Equal(t, id, cfg.ChannelID)
	require.Len(t, cfg.AICCAccountID, 64)
	return cfg
}

func aiccTestBinding(cfg service.AICCConfig) model.AICCBinding {
	return model.AICCBinding{ChannelID: cfg.ChannelID, AICCAccountID: cfg.AICCAccountID}
}

func aiccTestEnvironment(t *testing.T, endpoint string) {
	t.Helper()
	t.Setenv("AICC_ACCESS_KEY_ID", "offline-ak")
	t.Setenv("AICC_ACCESS_KEY_SECRET", "offline-sk")
	t.Setenv("AICC_ENDPOINT", endpoint)
	t.Setenv("AICC_POOL_ID", "CIDC-CORE-00")
}

func TestAICCRejectsInvalidUpstreamEnvelope(t *testing.T) {
	for _, body := range []string{`{"state":"EXCEPTION","errorCode":"gateway_error","body":{}}`, `<html>error</html>`, `{"state":"OK","body":{}}`} {
		t.Run(body, func(t *testing.T) {
			db := aiccTestDB(t)
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			aiccTestChannel(t, db, 101, srv.URL)
			_, err := service.CreateAICCH5Session(t.Context())
			require.Error(t, err)
			assert.EqualValues(t, 1, calls.Load(), "must exercise upstream envelope validation")
		})
	}
}

func TestAICCControllerUpdateAndAuthContracts(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, upstreamPath string
		handler                                gin.HandlerFunc
		want                                   map[string]any
	}{
		{"auth JSON", "POST", "/auth/group", `{"bytedToken":"offline-token"}`, "/api/openapi-maas/exp/aicc/v2/real-person-auth/asset-group/by-byted-token", controller.QueryAICCGroupByBytedToken, map[string]any{"bytedToken": "offline-token"}},
		{"partial group", "PUT", "/asset-groups/group-test", `{"description":"changed"}`, "/api/openapi-maas/exp/aicc/v2/asset-group/group-test", controller.UpdateAICCAssetGroup, map[string]any{"description": "changed"}},
		{"rename asset", "PUT", "/assets/asset-test", `{"assetName":"new name"}`, "/api/openapi-maas/exp/aicc/v2/asset/asset-test", controller.UpdateAICCAsset, map[string]any{"assetName": "new name"}},
		{"group filters", "GET", "/asset-groups?groupName=test&groupIds=group-a,group-b&pageNo=2&pageSize=10", "", "/api/openapi-maas/exp/aicc/v2/asset-group/query", controller.ListAICCAssetGroups, map[string]any{"pageNo": float64(2), "pageSize": float64(10), "groupName": "test", "groupIds": []any{"group-a", "group-b"}}},
		{"asset filters", "GET", "/assets?groupIds=group-a&statuses=ACTIVE&statuses=PROCESSING&pageNo=1&pageSize=10", "", "/api/openapi-maas/exp/aicc/v2/asset/query", controller.QueryAICCAssets, map[string]any{"pageNo": float64(1), "pageSize": float64(10), "groupType": "AIGC", "groupIds": []any{"group-a"}, "statuses": []any{"ACTIVE", "PROCESSING"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]any
			called := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				method := tc.method
				if method == "GET" {
					method = "POST"
				}
				assert.Equal(t, method, r.Method)
				assert.Equal(t, tc.upstreamPath, r.URL.Path)
				assert.NoError(t, common.DecodeJson(r.Body, &got))
				if tc.method == "GET" {
					_, _ = w.Write([]byte(`{"state":"OK","body":{"data":[],"total":0}}`))
				} else if tc.name == "auth JSON" {
					_, _ = w.Write([]byte(`{"state":"OK","body":{"status":"PROCESSING"}}`))
				} else {
					_, _ = w.Write([]byte(`{"state":"OK","body":"group-test"}`))
				}
			}))
			defer srv.Close()
			db := aiccTestDB(t)
			cfg := aiccTestChannel(t, db, 101, srv.URL)
			binding := aiccTestBinding(cfg)
			for _, id := range []string{"group-test", "group-a", "group-b"} {
				require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(1, id, "AIGC", binding))
			}
			require.NoError(t, model.RecordBoundAICCAssetOwnership(1, "asset-test", "group-test", binding))
			require.NoError(t, model.RecordBoundAICCAuthSession(1, "offline-token", 1800, binding))
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("id", 1)
			c.Set("role", common.RoleRootUser)
			c.Params = gin.Params{{Key: "id", Value: strings.TrimPrefix(tc.path, "/asset-groups/")}}
			if strings.HasPrefix(tc.path, "/assets/") {
				c.Params[0].Value = strings.TrimPrefix(tc.path, "/assets/")
			}
			c.Request = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			tc.handler(c)
			require.True(t, called, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), `"success":true`)
			assert.Equal(t, tc.want, got)
		})
	}
}

// These regressions exercise real controllers against two isolated upstream accounts.
func TestAICCAccountRoutingRegression(t *testing.T) {
	db := aiccTestDB(t)

	calls := map[string]int{"A": 0, "B": 0}
	for index, account := range []string{"A", "B"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls[account]++
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(r.URL.Path, "/sessions") {
				_, _ = w.Write([]byte(`{"state":"OK","body":{"bytedToken":"session-` + account + `","h5Link":"https://example.invalid/auth","expiresIn":1800}}`))
				return
			}
			_, _ = w.Write([]byte(`{"state":"OK","body":{"status":"PROCESSING"}}`))
		}))
		t.Cleanup(srv.Close)
		aiccTestChannel(t, db, 101+index, srv.URL)
		if account == "A" {
			aiccTestEnvironment(t, srv.URL)
		}
	}

	t.Run("explicit invalid channel never falls back", func(t *testing.T) {
		before := calls["A"] + calls["B"]
		_, callErr := service.CreateAICCH5Session(t.Context(), 999)
		assert.Error(t, callErr)
		assert.Equal(t, before, calls["A"]+calls["B"], "invalid explicit channel must make zero upstream calls")
	})
	t.Run("default session records resolved channel", func(t *testing.T) {
		// A single eligible account is required for default resolution.
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 102).Update("status", 2).Error)
		t.Cleanup(func() {
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 102).Update("status", common.ChannelStatusEnabled).Error)
		})
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", 1)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/aicc/auth/session", strings.NewReader(`{}`))
		controller.CreateAICCH5Session(c)
		require.Contains(t, recorder.Body.String(), `"success":true`)
		channelID, lookupErr := model.GetAICCAuthSessionChannelID(1, "session-A")
		require.NoError(t, lookupErr)
		assert.Equal(t, 101, channelID)
		binding, lookupErr := model.GetAICCAuthSessionBinding(1, "session-A")
		require.NoError(t, lookupErr)
		cfg, lookupErr := service.GetAICCConfigForChannel(101)
		require.NoError(t, lookupErr)
		assert.Equal(t, aiccTestBinding(cfg), binding)
	})
	t.Run("session query stays on account B", func(t *testing.T) {
		cfg, err := service.GetAICCConfigForChannel(102)
		require.NoError(t, err)
		require.NoError(t, model.RecordBoundAICCAuthSession(1, "existing-session-B", 1800, aiccTestBinding(cfg)))
		beforeA, beforeB := calls["A"], calls["B"]
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", 1)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/aicc/auth/group", strings.NewReader(`{"bytedToken":"existing-session-B"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		controller.QueryAICCGroupByBytedToken(c)
		require.Contains(t, recorder.Body.String(), `"success":true`)
		assert.Equal(t, beforeA, calls["A"], "B session must never be sent to account A")
		assert.Equal(t, beforeB+1, calls["B"])
	})
}

func requireAICCLiveTest(t *testing.T) {
	t.Helper()
	if os.Getenv("AICC_LIVE_TEST") != "1" {
		t.Skip("explicit AICC_LIVE_TEST=1 required for upstream side effects")
	}
}

func TestSignAICCRequestDeterministic(t *testing.T) {
	queryParams := map[string]string{
		"AccessKey":        "test-ak",
		"Timestamp":        "2026-09-14T23:00:00Z",
		"SignatureMethod":  "HmacSHA1",
		"SignatureNonce":   "12345678-1234-1234-1234-123456789012",
		"SignatureVersion": "V2.0",
		"Version":          "2016-12-05",
	}

	sig1 := service.SignAICCRequest("POST", "/api/openapi-maas/exp/aicc/v2/real-person-auth/sessions", queryParams, "test-sk")
	sig2 := service.SignAICCRequest("POST", "/api/openapi-maas/exp/aicc/v2/real-person-auth/sessions", queryParams, "test-sk")
	assert.NotEmpty(t, sig1)
	assert.Equal(t, sig1, sig2, "signature must be deterministic")

	// 改变参数名或值应产生不同签名
	queryParams2 := map[string]string{
		"AccessKey":        "test-ak",
		"Timestamp":        "2026-09-14T23:00:01Z",
		"SignatureMethod":  "HmacSHA1",
		"SignatureNonce":   "12345678-1234-1234-1234-123456789012",
		"SignatureVersion": "V2.0",
		"Version":          "2016-12-05",
	}
	sig3 := service.SignAICCRequest("POST", "/api/openapi-maas/exp/aicc/v2/real-person-auth/sessions", queryParams2, "test-sk")
	assert.NotEqual(t, sig1, sig3)
}

func TestAICCAccountAttestationAllowsOnlyConfirmedCredentials(t *testing.T) {
	db := aiccTestDB(t)
	aiccTestChannel(t, db, 101, "https://aicc.invalid")
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 101).Update("key", "synthetic-video-key").Error)
	var ch model.Channel
	require.NoError(t, db.First(&ch, 101).Error)
	fingerprint, err := service.AICCChannelCredentialFingerprint(ch)
	require.NoError(t, err)
	require.Error(t, service.AttestAICCChannelAccount(1, 101, "account-a", "control panel verified fake test evidence", strings.Repeat("0", 64)))
	require.NoError(t, service.AttestAICCChannelAccount(1, 101, "account-a", "control panel verified fake test evidence", fingerprint))
	cfg, err := service.GetAICCConfigForChannel(101)
	require.NoError(t, err)
	require.True(t, cfg.AccountVerified)
	binding := aiccTestBinding(cfg)
	require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(1, "group-attested", "AIGC", binding))
	require.NoError(t, model.RecordBoundAICCAssetOwnership(1, "asset-attested", "group-attested", binding))
	require.NoError(t, service.ValidateAICCVideoAssetChannel(101, "asset-attested"))
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 101).Update("key", "changed-video-key").Error)
	require.Error(t, service.ValidateAICCVideoAssetChannel(101, "asset-attested"))
	require.NoError(t, db.First(&ch, 101).Error)
	fingerprint, err = service.AICCChannelCredentialFingerprint(ch)
	require.NoError(t, err)
	require.NoError(t, service.AttestAICCChannelAccount(1, 101, "account-a", "same account rotation verified in test", fingerprint))
	rotated, err := service.GetAICCConfigForChannel(101)
	require.NoError(t, err)
	require.Equal(t, cfg.AICCAccountID, rotated.AICCAccountID)
	require.NoError(t, service.ValidateAICCVideoAssetChannel(101, "asset-attested"))
	require.NoError(t, service.AttestAICCChannelAccount(1, 101, "account-b", "different account explicitly attested test", fingerprint))
	require.Error(t, service.ValidateAICCVideoAssetChannel(101, "asset-attested"))
	aiccTestChannel(t, db, 102, "https://aicc.invalid")
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 102).Update("key", "same-account-second-key").Error)
	var second model.Channel
	require.NoError(t, db.First(&second, 102).Error)
	secondFingerprint, err := service.AICCChannelCredentialFingerprint(second)
	require.NoError(t, err)
	require.NoError(t, service.AttestAICCChannelAccount(1, 102, "account-a", "same account second channel verified test", secondFingerprint))
	require.NoError(t, service.ValidateAICCVideoAssetChannel(102, "asset-attested"), "same attested account and resource domain may share assets")
	filter, err := service.AICCAssetChannelFilter(1, []string{"asset-attested"})
	require.NoError(t, err)
	require.Equal(t, []int{102}, filter.AllowedChannelIDs)
	base := "https://video.invalid"
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 102).Update("base_url", base).Error)
	require.NoError(t, db.First(&second, 102).Error)
	secondFingerprint, err = service.AICCChannelCredentialFingerprint(second)
	require.NoError(t, err)
	require.NoError(t, service.AttestAICCChannelAccount(1, 102, "account-a", "updated endpoint verified in synthetic test", secondFingerprint))
	require.Error(t, service.ValidateAICCVideoAssetChannel(102, "asset-attested"), "changed video domain cannot inherit assets")
	secondCfg, err := service.GetAICCConfigForChannel(102)
	require.NoError(t, err)
	secondBinding := aiccTestBinding(secondCfg)
	require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(1, "group-attested-2", "AIGC", secondBinding))
	require.NoError(t, model.RecordBoundAICCAssetOwnership(1, "asset-attested-2", "group-attested-2", secondBinding))
	request, err := http.NewRequest("POST", base+"/v1/videos", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+second.Key)
	var snapshot model.AICCDispatchSnapshot
	require.NoError(t, service.ValidateAICCDispatch(1, 102, []string{"asset-attested-2"}, second.Key, base, request, &snapshot))
	task := &model.Task{ChannelId: 102, PrivateData: model.TaskPrivateData{Execution: &model.TaskExecutionSnapshot{AICC: &snapshot}}}
	require.NoError(t, service.ValidateAICCTaskSource(task, &second))
	changed := second
	changed.Key = "another-account-key"
	require.Error(t, service.ValidateAICCTaskSource(task, &changed))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("aicc_dispatch_snapshot", snapshot)
	c.Request = request
	require.Equal(t, snapshot, *service.TaskExecutionSnapshotFromContext(c).AICC)
	require.Error(t, service.ValidateAICCDispatch(1, 102, []string{"asset-attested-2"}, "cached-old-key", base, request))
	request.Header.Set("Authorization", "Bearer other-key")
	require.Error(t, service.ValidateAICCDispatch(1, 102, []string{"asset-attested-2"}, second.Key, base, request))
	request.Header.Set("Authorization", "Bearer "+second.Key)
	request.URL.Host = "other.invalid"
	require.Error(t, service.ValidateAICCDispatch(1, 102, []string{"asset-attested-2"}, second.Key, base, request))
}

func TestAICCChannelPinnedModeDoesNotImplyAccountVerification(t *testing.T) {
	db := aiccTestDB(t)
	cfg := aiccTestChannel(t, db, 101, "https://aicc.invalid")
	info, err := common.Marshal(map[string]any{"aicc_enabled": true, "aicc_channel_pinned": true, "access_key_id": "offline-ak", "access_key_secret": "offline-sk", "endpoint": cfg.Endpoint, "pool_id": cfg.PoolID})
	require.NoError(t, err)
	base := "https://video.invalid"
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 101).Updates(map[string]any{"key": "single-channel-key", "base_url": base, "other_info": string(info)}).Error)
	cfg, err = service.GetAICCConfigForChannel(101)
	require.NoError(t, err)
	require.True(t, cfg.ChannelPinned)
	require.False(t, cfg.AccountVerified)
	binding := aiccTestBinding(cfg)
	require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(1, "group-pinned", "AIGC", binding))
	require.NoError(t, model.RecordBoundAICCAssetOwnership(1, "asset-pinned", "group-pinned", binding))
	filter, err := service.AICCAssetChannelFilter(1, []string{"asset-pinned"})
	require.NoError(t, err)
	require.Equal(t, []int{101}, filter.AllowedChannelIDs)
	req, err := http.NewRequest("POST", base+"/videos", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer single-channel-key")
	require.NoError(t, service.ValidateAICCDispatch(1, 101, []string{"asset-pinned"}, "single-channel-key", base, req))
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 101).Update("key", "changed-account-key").Error)
	require.Error(t, service.ValidateAICCDispatch(1, 101, []string{"asset-pinned"}, "changed-account-key", base, req))
}

func TestAICCConfigDefaults(t *testing.T) {
	db := aiccTestDB(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	aiccTestEnvironment(t, srv.URL)
	_, err := service.ResolveDefaultAICCConfig()
	require.Error(t, err, "environment credentials alone are not a channel")
	assert.Empty(t, service.GetAICCConfig().AccessKeyID)
	_, err = service.CreateAICCH5Session(t.Context())
	require.Error(t, err)

	cfg := aiccTestChannel(t, db, 101, srv.URL)
	got, err := service.ResolveDefaultAICCConfig()
	require.NoError(t, err)
	assert.Equal(t, cfg, got)
	assert.Equal(t, "CIDC-CORE-00", got.PoolID)

	// Incomplete and explicitly disabled channels cannot make the default ambiguous.
	require.NoError(t, db.Create(&model.Channel{Id: 102, Status: common.ChannelStatusEnabled, OtherInfo: `{"aicc_enabled":true,"access_key_id":"offline-ak"}`}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 103, Status: common.ChannelStatusEnabled, OtherInfo: strings.Replace(aiccTestInfo(t, srv.URL), `"aicc_enabled":true`, `"aicc_enabled":false`, 1)}).Error)
	got, err = service.ResolveDefaultAICCConfig()
	require.NoError(t, err)
	assert.Equal(t, 101, got.ChannelID)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 102).Update("other_info", aiccTestInfo(t, srv.URL)).Error)
	_, err = service.ResolveDefaultAICCConfig()
	require.Error(t, err, "two complete enabled channels require explicit selection")
	_, err = service.CreateAICCH5Session(t.Context())
	require.Error(t, err)
	for _, id := range []int{0, -1, 999} {
		_, err = service.GetAICCConfigForChannel(id)
		require.Error(t, err)
	}
	assert.Zero(t, calls.Load())
}

func TestValidateAICCVideoAssetChannel(t *testing.T) {
	db := aiccTestDB(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	aiccTestEnvironment(t, srv.URL)
	cfg := aiccTestChannel(t, db, 101, srv.URL)
	aiccTestChannel(t, db, 102, srv.URL)
	aiccTestChannel(t, db, 103, srv.URL)
	binding := aiccTestBinding(cfg)
	require.False(t, cfg.AccountVerified, "a configuration hash is not upstream account proof")
	require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(1, "group-1", "AIGC", binding))
	require.NoError(t, model.RecordBoundAICCAssetOwnership(1, "asset-chan-101", "group-1", binding))
	// Deliberately preserve unbound historical rows to prove generation rejects them.
	require.NoError(t, model.RecordAICCAssetGroupOwnership(1, "group-legacy", "AIGC", 0))
	require.NoError(t, model.RecordAICCAssetOwnership(1, "asset-chan-legacy", "group-legacy", 0))
	for _, tc := range []struct {
		name      string
		channelID int
		assetID   string
	}{
		{"matching snapshot still unverified", 101, "asset-chan-101"},
		{"mismatched channel", 102, "asset-chan-101"},
		{"third channel with identical fake keys", 103, "asset-chan-101"},
		{"unknown asset", 101, "asset-unknown"},
		{"legacy matching environment", 101, "asset-chan-legacy"},
		{"legacy other channel", 102, "asset-chan-legacy"},
		{"unknown channel", 999, "asset-chan-101"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, service.ValidateAICCVideoAssetChannel(tc.channelID, tc.assetID))
		})
	}
	require.Error(t, service.ValidateAICCVideoChannel(101))
	assert.Zero(t, calls.Load())
}

func TestAICCDisabledAndDatabaseFailureNeverUseEnvironment(t *testing.T) {
	for _, mode := range []string{"disabled channel", "aicc disabled", "incomplete", "closed database", "nil database"} {
		t.Run(mode, func(t *testing.T) {
			db := aiccTestDB(t)
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = w.Write([]byte(`{"state":"OK","body":{"bytedToken":"unexpected","h5Link":"https://example.invalid/auth","expiresIn":1800}}`))
			}))
			defer srv.Close()
			aiccTestEnvironment(t, srv.URL)
			aiccTestChannel(t, db, 101, srv.URL)
			switch mode {
			case "disabled channel":
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 101).Update("status", 2).Error)
			case "aicc disabled":
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 101).Update("other_info", strings.Replace(aiccTestInfo(t, srv.URL), `"aicc_enabled":true`, `"aicc_enabled":false`, 1)).Error)
			case "incomplete":
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 101).Update("other_info", `{"aicc_enabled":true}`).Error)
			case "closed database":
				sqlDB, err := db.DB()
				require.NoError(t, err)
				require.NoError(t, sqlDB.Close())
			case "nil database":
				model.DB = nil
			}
			_, err := service.GetAICCConfigForChannel(101)
			require.Error(t, err)
			_, err = service.ResolveDefaultAICCConfig()
			require.Error(t, err)
			_, err = service.CreateAICCH5Session(t.Context(), 101)
			require.Error(t, err)
			_, err = service.CreateAICCH5Session(t.Context())
			require.Error(t, err)
			assert.Zero(t, calls.Load(), "neither explicit nor default requests may fall back to environment")
		})
	}
}

func TestAICCConfigChangesInvalidateBindings(t *testing.T) {
	for _, field := range []string{"access_key_id", "access_key_secret", "endpoint", "pool_id"} {
		t.Run(field, func(t *testing.T) {
			db := aiccTestDB(t)
			var calls atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
			srv := httptest.NewServer(handler)
			defer srv.Close()
			other := httptest.NewServer(handler)
			defer other.Close()
			cfg := aiccTestChannel(t, db, 101, srv.URL)
			binding := aiccTestBinding(cfg)
			require.NoError(t, model.RecordBoundAICCAuthSession(1, "old-session", 1800, binding))
			require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(1, "old-group", "AIGC", binding))
			require.NoError(t, model.RecordBoundAICCAssetOwnership(1, "old-asset", "old-group", binding))
			var info map[string]any
			require.NoError(t, common.DecodeJson(strings.NewReader(aiccTestInfo(t, srv.URL)), &info))
			info[field] = "changed-value"
			if field == "endpoint" {
				info[field] = other.URL
			}
			encoded, err := common.Marshal(info)
			require.NoError(t, err)
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 101).Update("other_info", string(encoded)).Error)
			changed, err := service.GetAICCConfigForChannel(101)
			require.NoError(t, err)
			assert.NotEqual(t, cfg.AICCAccountID, changed.AICCAccountID)
			stored, err := model.GetAICCAuthSessionBinding(1, "old-session")
			require.NoError(t, err)
			assert.Equal(t, binding, stored)
			assert.NotEqual(t, aiccTestBinding(changed), stored)
			require.Error(t, service.ValidateAICCVideoAssetChannel(101, "old-asset"))
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("id", 1)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/aicc/auth/group", strings.NewReader(`{"bytedToken":"old-session"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			controller.QueryAICCGroupByBytedToken(c)
			require.Contains(t, recorder.Body.String(), `"success":false`)
			assert.Zero(t, calls.Load(), "stale binding must be rejected before contacting either endpoint")
		})
	}
}

func TestAICCSessionQueryRejectsUnboundAndMismatchedSessions(t *testing.T) {
	db := aiccTestDB(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	aiccTestEnvironment(t, srv.URL)
	cfg := aiccTestChannel(t, db, 101, srv.URL)
	aiccTestChannel(t, db, 102, srv.URL)
	require.NoError(t, model.RecordAICCAuthSession(1, "legacy-session", 1800, 0))
	require.NoError(t, model.RecordAICCAuthSession(1, "channel-only-session", 1800, 101))
	require.NoError(t, model.RecordBoundAICCAuthSession(1, "bound-session", 1800, aiccTestBinding(cfg)))
	for _, tc := range []struct {
		name, token, query string
		userID             int
	}{
		{"legacy", "legacy-session", "", 1},
		{"channel without snapshot", "channel-only-session", "", 1},
		{"unknown token", "unknown-session", "", 1},
		{"different user", "bound-session", "", 2},
		{"mismatched request channel", "bound-session", "?channel_id=102", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("id", tc.userID)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/aicc/auth/group"+tc.query, strings.NewReader(`{"bytedToken":"`+tc.token+`"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			controller.QueryAICCGroupByBytedToken(c)
			require.Contains(t, recorder.Body.String(), `"success":false`)
			assert.Zero(t, calls.Load())
		})
	}
}

func TestAICCPinnedConfigDoesNotResolveAgain(t *testing.T) {
	db := aiccTestDB(t)
	var originalCalls, replacementCalls atomic.Int32
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originalCalls.Add(1)
		assert.Equal(t, "offline-ak", r.URL.Query().Get("AccessKey"))
		params := make(map[string]string)
		for key, values := range r.URL.Query() {
			params[key] = values[0]
		}
		assert.Equal(t, service.SignAICCRequest(r.Method, r.URL.Path, params, "offline-sk"), params["Signature"])
		_, _ = w.Write([]byte(`{"state":"OK","body":{"bytedToken":"pinned-session","h5Link":"https://example.invalid/auth","expiresIn":1800}}`))
	}))
	defer original.Close()
	replacement := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { replacementCalls.Add(1) }))
	defer replacement.Close()
	cfg := aiccTestChannel(t, db, 101, original.URL)
	ctx := service.WithAICCConfig(t.Context(), cfg)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 101).Update("other_info", strings.Replace(aiccTestInfo(t, replacement.URL), "offline-ak", "replacement-ak", 1)).Error)
	current, err := service.GetAICCConfigForChannel(101)
	require.NoError(t, err)
	assert.NotEqual(t, cfg.AICCAccountID, current.AICCAccountID)
	for _, mode := range []string{"changed configuration", "database unavailable"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "database unavailable" {
				model.DB = nil
				t.Cleanup(func() { model.DB = db })
			}
			session, err := service.CreateAICCH5Session(ctx, cfg.ChannelID)
			require.NoError(t, err)
			require.NotNil(t, session)
			assert.Equal(t, "pinned-session", session.BytedToken)
		})
	}
	_, err = service.CreateAICCH5Session(ctx, 102)
	require.Error(t, err, "a pinned snapshot cannot be used for a different channel")
	assert.EqualValues(t, 2, originalCalls.Load())
	assert.Zero(t, replacementCalls.Load())
}

func TestAICCRedirectIsNotFollowed(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			db := aiccTestDB(t)
			var sourceCalls, targetCalls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetCalls.Add(1)
				_, _ = w.Write([]byte(`{"state":"OK","body":{"bytedToken":"redirected","h5Link":"https://example.invalid/auth","expiresIn":1800}}`))
			}))
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sourceCalls.Add(1)
				http.Redirect(w, r, target.URL+r.URL.RequestURI(), status)
			}))
			defer source.Close()
			aiccTestChannel(t, db, 101, source.URL)
			_, err := service.CreateAICCH5Session(t.Context(), 101)
			require.Error(t, err)
			assert.EqualValues(t, 1, sourceCalls.Load())
			assert.Zero(t, targetCalls.Load(), "signed requests must never reach redirect targets")
		})
	}
}

func TestAICCH5SessionLiveCall(t *testing.T) {
	requireAICCLiveTest(t)
	session, err := service.CreateAICCH5Session(context.Background())
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.NotEmpty(t, session.BytedToken)
	assert.NotEmpty(t, session.H5Link)
	assert.Contains(t, session.H5Link, "https://ark.volcengine.com")
	assert.Greater(t, session.ExpiresIn, 0)
}

func TestAICCListAssetGroupsLiveCall(t *testing.T) {
	requireAICCLiveTest(t)
	res, err := service.ListAICCAssetGroups(context.Background(), 1, 10, "")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "OK", res["state"])
}

func TestAICCListAssetsLiveCall(t *testing.T) {
	requireAICCLiveTest(t)
	res, err := service.QueryAICCAssets(context.Background(), 1, 10, "AIGC", nil, "")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "OK", res["state"])
}

func TestAICCAssetGroupCRUDLiveCall(t *testing.T) {
	requireAICCLiveTest(t)
	ctx := context.Background()
	groupName := "单元测试组-AIGC"
	desc := "用于自动化回归测试"

	// 1. 创建
	createRes, err := service.CreateAICCAssetGroup(ctx, groupName, desc)
	require.NoError(t, err)
	require.NotNil(t, createRes)
	assert.Equal(t, "OK", createRes["state"])

	body, ok := createRes["body"].(map[string]any)
	require.True(t, ok)
	groupId, ok := body["groupId"].(string)
	require.True(t, ok)
	assert.NotEmpty(t, groupId)

	// 2. 查询详情
	detailRes, err := service.GetAICCAssetGroup(ctx, groupId)
	require.NoError(t, err)
	require.NotNil(t, detailRes)
	assert.Equal(t, "OK", detailRes["state"])

	// 3. 更新
	updateRes, err := service.UpdateAICCAssetGroup(ctx, groupId, groupName+"-已修改", desc)
	require.NoError(t, err)
	require.NotNil(t, updateRes)
	assert.Equal(t, "OK", updateRes["state"])

	// 4. 删除
	delRes, err := service.DeleteAICCAssetGroup(ctx, groupId)
	require.NoError(t, err)
	require.NotNil(t, delRes)
	assert.Equal(t, "OK", delRes["state"])
}

func TestAICCAssetCRUDLiveCall(t *testing.T) {
	requireAICCLiveTest(t)
	testImgURL := strings.TrimSpace(os.Getenv("AICC_LIVE_TEST_ASSET_URL"))
	if testImgURL == "" {
		t.Skip("explicit AICC_LIVE_TEST_ASSET_URL for an authorized test image required")
	}
	ctx := context.Background()

	// 1. 先创建一个临时素材组
	groupRes, err := service.CreateAICCAssetGroup(ctx, "测试素材组-素材CRUD", "临时测试")
	require.NoError(t, err)
	body := groupRes["body"].(map[string]any)
	groupId := body["groupId"].(string)
	defer service.DeleteAICCAssetGroup(ctx, groupId)

	// 2. Use only an explicitly supplied, authorized test image.
	createRes, err := service.CreateAICCAsset(ctx, groupId, "测试素材", testImgURL, "Image")
	require.NoError(t, err)
	require.NotNil(t, createRes)
	assert.Equal(t, "OK", createRes["state"])

	assetId, ok := createRes["body"].(string)
	require.True(t, ok)
	assert.NotEmpty(t, assetId)

	// 3. 查询素材详情（获取 12 小时有效预览链接）
	detailRes, err := service.GetAICCAsset(ctx, assetId)
	require.NoError(t, err)
	require.NotNil(t, detailRes)
	assert.Equal(t, "OK", detailRes["state"])

	// 4. 删除素材
	delRes, err := service.DeleteAICCAsset(ctx, assetId)
	require.NoError(t, err)
	require.NotNil(t, delRes)
	assert.Equal(t, "OK", delRes["state"])
}
