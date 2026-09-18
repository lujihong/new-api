package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestAICCRejectsInvalidUpstreamEnvelope(t *testing.T) {
	for _, body := range []string{`{"state":"EXCEPTION","errorCode":"gateway_error","body":{}}`, `<html>error</html>`, `{"state":"OK","body":{}}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			t.Setenv("AICC_ACCESS_KEY_ID", "offline-ak")
			t.Setenv("AICC_ACCESS_KEY_SECRET", "offline-sk")
			t.Setenv("AICC_ENDPOINT", srv.URL)
			_, err := service.CreateAICCH5Session(t.Context())
			require.Error(t, err)
		})
	}
}

func TestAICCControllerUpdateAndAuthContracts(t *testing.T) {
	previousDB := model.DB
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/aicc-contract.db"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB; sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.AICCAssetGroupOwnership{}, &model.AICCAssetOwnership{}, &model.AICCAuthSession{}))
	for _, id := range []string{"group-test", "group-a", "group-b"} {
		require.NoError(t, model.RecordAICCAssetGroupOwnership(1, id, "AIGC"))
	}
	require.NoError(t, model.RecordAICCAssetOwnership(1, "asset-test", "group-test"))
	require.NoError(t, model.RecordAICCAuthSession(1, "offline-token", 1800))
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
			t.Setenv("AICC_ACCESS_KEY_ID", "offline-ak")
			t.Setenv("AICC_ACCESS_KEY_SECRET", "offline-sk")
			t.Setenv("AICC_ENDPOINT", srv.URL)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("id", 1)
			c.Set("role", common.RoleRootUser)
			c.Params = gin.Params{{Key: "id", Value: strings.TrimPrefix(tc.path, "/asset-groups/")}}
			if strings.HasPrefix(tc.path, "/assets/") {
				c.Params[0].Value = strings.TrimPrefix(tc.path, "/assets/")
			}
			c.Request = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			tc.handler(c)
			require.True(t, called)
			assert.Equal(t, tc.want, got)
		})
	}
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

func TestAICCConfigDefaults(t *testing.T) {
	t.Setenv("AICC_ACCESS_KEY_ID", "")
	t.Setenv("AICC_ACCESS_KEY_SECRET", "")
	cfg := service.GetAICCConfig()
	assert.Empty(t, cfg.AccessKeyID)
	assert.Empty(t, cfg.AccessKeySecret)
		assert.Equal(t, "https://ecloud.10086.cn", cfg.Endpoint)
		assert.Equal(t, "CIDC-CORE-00", cfg.PoolID)
	}

func TestValidateAICCVideoAssetChannel(t *testing.T) {
	previousDB := model.DB
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/aicc-validate.db"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB; sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.AICCAssetGroupOwnership{}, &model.AICCAssetOwnership{}, &model.Channel{}))

	t.Setenv("AICC_ACCESS_KEY_ID", "ak-mobile-1")
	t.Setenv("AICC_ACCESS_KEY_SECRET", "sk-mobile-1")

	// Channel 1: Mobile Cloud 1
	ch1 := model.Channel{
		Id:        101,
		Type:      54,
		Name:      "移动云-专线1",
		Status:    common.ChannelStatusEnabled,
		OtherInfo: `{"access_key_id":"ak-mobile-1","access_key_secret":"sk-mobile-1"}`,
	}
	require.NoError(t, db.Create(&ch1).Error)

	// Channel 2: Mobile Cloud 2 (different AK/SK)
	ch2 := model.Channel{
		Id:        102,
		Type:      54,
		Name:      "移动云-专线2",
		Status:    common.ChannelStatusEnabled,
		OtherInfo: `{"access_key_id":"ak-mobile-2","access_key_secret":"sk-mobile-2"}`,
	}
	require.NoError(t, db.Create(&ch2).Error)

	// Asset 1 bound to Channel 101
	require.NoError(t, model.RecordAICCAssetOwnership(1, "asset-chan-101", "group-1", 101))
	// Asset 2 legacy (channel_id = 0)
	require.NoError(t, model.RecordAICCAssetOwnership(1, "asset-chan-legacy", "group-1", 0))

	// 1. Target Channel 101 with Asset 101 should succeed
	err = service.ValidateAICCVideoAssetChannel(101, "asset-chan-101")
	assert.NoError(t, err)

	// 2. Target Channel 102 with Asset 101 (mismatched channel) should be rejected
	err = service.ValidateAICCVideoAssetChannel(102, "asset-chan-101")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不匹配")

	// 3. Legacy asset on Channel 101 matches global credentials
	err = service.ValidateAICCVideoAssetChannel(101, "asset-chan-legacy")
	assert.NoError(t, err)

	// 4. Legacy asset on Channel 102 fails credential match
	err = service.ValidateAICCVideoAssetChannel(102, "asset-chan-legacy")
	require.Error(t, err)
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
	ctx := context.Background()

	// 1. 先创建一个临时素材组
	groupRes, err := service.CreateAICCAssetGroup(ctx, "测试素材组-素材CRUD", "临时测试")
	require.NoError(t, err)
	body := groupRes["body"].(map[string]any)
	groupId := body["groupId"].(string)
	defer service.DeleteAICCAssetGroup(ctx, groupId)

	// 2. 在组下创建素材（使用真实可下载的图片）
	testImgUrl := "https://images.unsplash.com/photo-1534528741775-53994a69daeb?w=500"
	createRes, err := service.CreateAICCAsset(ctx, groupId, "测试素材", testImgUrl, "Image")
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
