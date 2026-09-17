package jsplugin

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/plugins"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAICCReferenceTraversalRejectsCyclesAndExcessiveDepth(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	require.ErrorContains(t, validateAICCAssetReferences(1, cycle), "validation limits")
	var nested any = "ordinary text"
	for i := 0; i < 150; i++ {
		nested = []any{nested}
	}
	require.ErrorContains(t, validateAICCAssetReferences(1, nested), "validation limits")
}

func TestAICCVideoReferencesCheckAllSupportedShapes(t *testing.T) {
	old := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB = db
	t.Cleanup(func() { model.DB = old; _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.AICCAssetOwnership{}, &model.Channel{}))
	t.Setenv("AICC_ACCESS_KEY_ID", "offline-ak")
	t.Setenv("AICC_ACCESS_KEY_SECRET", "offline-sk")
	require.NoError(t, db.Create(&model.Channel{Id: 6, Name: "移动云", Status: 1, Key: "offline", OtherInfo: `{"access_key_id":"offline-ak","access_key_secret":"offline-sk"}`}).Error)
	require.NoError(t, model.RecordAICCAssetOwnership(1, "asset-private", "group-private"))
	metadata := `{"image":"asset://asset-private"}`
	metadataJSON, err := json.Marshal(metadata)
	require.NoError(t, err)
	requests := []any{
		map[string]any{"metadata": &metadata},
		map[string]any{"metadata": json.RawMessage(metadataJSON)},
		map[string]any{"image": "asset://asset-private", "custom_ratio": math.NaN()},
		map[string]any{"image": "asset://asset-private"},
		map[string]any{"asset_id": "asset-private"},
		map[string]any{"input_reference[]": []string{"asset-private"}},
		map[string]any{"metadata": `{"content":[{"type":"image_url","image_url":{"url":"asset://asset-private"}}]}`},
		relaycommon.TaskSubmitReq{Images: []string{"asset://asset-private"}},
	}
	for _, request := range requests {
		require.NoError(t, validateAICCAssetReferences(1, request))
		require.Error(t, validateAICCAssetReferences(2, request))
	}
	require.NoError(t, validateAICCAssetReferences(2, map[string]any{"prompt": "explain asset://asset-private", "image": "https://example.com/photo.png"}))
	require.Error(t, validateAICCAssetReferences(1, map[string]any{"image": "asset://asset-private%2fother"}))
	source, err := plugins.Source("doubao")
	require.NoError(t, err)
	plugin, err := pluginruntime.NewRegistry().RegisterFactory(source, pluginruntime.Options{Key: "doubao"})
	require.NoError(t, err)
	for _, uid := range []int{1, 2} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
		c.Set("task_request", map[string]any{"model": "doubao-seedance-2.0", "prompt": "landscape", "image": "asset-private"})
		info := &relaycommon.RelayInfo{UserId: uid, OriginModelName: "doubao-seedance-2.0", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 6, ChannelBaseUrl: "https://example.com", UpstreamModelName: "doubao-seedance-2.0"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
		a := New(plugin)
		a.Init(info)
		taskErr := a.ValidateRequestAndSetAction(c, info)
		if uid == 1 {
			require.Nil(t, taskErr)
		} else {
			require.NotNil(t, taskErr)
			require.Equal(t, http.StatusForbidden, taskErr.StatusCode)
		}
	}
	require.NoError(t, db.Create(&model.Channel{Id: 7, Name: "other", Status: 1, Key: "offline"}).Error)
	wrongChannel := 7
	request := map[string]any{"image": "asset://asset-private"}
	require.Error(t, validateAICCAssetReferencesForChannel(1, request, &wrongChannel))
	rightChannel := 6
	require.NoError(t, validateAICCAssetReferencesForChannel(1, request, &rightChannel))
}
