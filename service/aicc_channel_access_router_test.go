package service_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/router"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Production router and TokenOrUserAuth, backed by real DB tokens and abilities.
func TestAICCAccessRealRouterSK(t *testing.T) {
	db := aiccTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.UserSession{}))
	oldLogDB, oldMain, oldLog, oldMaster := model.LOG_DB, common.MainDatabaseType(), common.LogDatabaseType(), common.IsMasterNode
	model.LOG_DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.IsMasterNode = false
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	t.Cleanup(func() {
		model.LOG_DB = oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.IsMasterNode = oldMaster
	})
	oldUsable, oldAuto, oldRatio := setting.UserUsableGroups2JSONString(), setting.AutoGroups2JsonString(), ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldUsable))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(oldAuto))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatio))
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","auto":"Auto"}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","vip"]`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`))
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1).Update("role", common.RoleRootUser).Error)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.HasSuffix(r.URL.Path, "/asset-group/query") {
			w.Write([]byte(`{"state":"OK","body":{"data":[],"total":0}}`))
			return
		}
		w.Write([]byte(`{"state":"OK","body":{"bytedToken":"new-session","h5Link":"https://example.test/auth","expiresIn":1800}}`))
	}))
	defer upstream.Close()
	a := aiccTestChannel(t, db, 1, upstream.URL)
	b := aiccTestChannel(t, db, 2, upstream.URL)
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", 2).Update("group", "vip").Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "hidden-model", ChannelId: 1, Enabled: true}).Error)
	require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(1, "group-b", "AIGC", aiccTestBinding(b)))
	require.NoError(t, model.RecordBoundAICCAuthSession(1, "bound-b", 1800, aiccTestBinding(b)))
	require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(1, "group-a", "AIGC", aiccTestBinding(a)))
	tokens := []model.Token{
		{Group: "default", ModelLimitsEnabled: true, ModelLimits: "aicc-test-model"},
		{Group: "vip", ModelLimitsEnabled: true, ModelLimits: "aicc-test-model"},
		{Group: "default", ModelLimitsEnabled: true, ModelLimits: "absent"},
		{Group: "auto", AutoGroups: `["vip"]`, ModelLimitsEnabled: true, ModelLimits: "aicc-test-model"},
	}
	keys := make([]string, len(tokens))
	for i := range tokens {
		token := &tokens[i]
		token.UserId = 1
		token.Key = strings.Repeat(string(rune('a'+i)), 48)
		token.Status = common.TokenStatusEnabled
		token.ExpiredTime = -1
		token.UnlimitedQuota = true
		require.NoError(t, db.Create(token).Error)
		keys[i] = "sk-" + token.Key
	}
	engine := gin.New()
	router.SetApiRouter(engine)
	request := func(method, path, key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}
	for _, tc := range []struct {
		method, path string
		token, want  int
		body         string
	}{
		{"GET", "/api/aicc/channels", 0, 200, ""},
		{"GET", "/api/aicc/channels", 2, 403, ""},
		{"POST", "/api/aicc/auth/session?channel_id=1", 0, 200, "{}"},
		{"POST", "/api/aicc/auth/session?channel_id=2", 0, 403, "{}"},
		{"GET", "/api/aicc/asset-groups/group-b", 0, 403, ""},
		{"POST", "/api/aicc/auth/group", 0, 403, `{"bytedToken":"bound-b"}`},
		{"GET", "/api/aicc/asset-groups?channel_id=1", 1, 403, ""},
		{"GET", "/api/aicc/asset-groups?channel_id=2", 3, 200, ""},
		{"GET", "/api/aicc/admin/asset-groups?channel_id=2", 0, 401, ""},
	} {
		before := calls
		w := request(tc.method, tc.path, keys[tc.token], tc.body)
		require.Equal(t, tc.want, w.Code, w.Body.String())
		if tc.want != 200 {
			require.Equal(t, before, calls)
		}
		if tc.path == "/api/aicc/channels" && tc.want == 200 {
			require.Contains(t, w.Body.String(), `"models":["aicc-test-model"]`)
			require.NotContains(t, w.Body.String(), `"id":2`)
			require.NotContains(t, w.Body.String(), "hidden-model")
			require.NotContains(t, w.Body.String(), "access_key")
		}
	}
	require.Equal(t, 401, request("GET", "/api/aicc/channels", "", "").Code)
	require.Equal(t, 401, request("GET", "/api/aicc/channels", "sk-invalid", "").Code)
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", 1).Update("enabled", false).Error)
	require.Equal(t, 403, request("GET", "/api/aicc/channels", keys[0], "").Code)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 2).Update("status", 2).Error)
	require.Equal(t, 403, request("GET", "/api/aicc/channels", keys[1], "").Code)
	require.NoError(t, db.Migrator().DropTable(&model.Ability{}))
	require.Equal(t, 503, request("GET", "/api/aicc/channels", keys[0], "").Code)
	require.Equal(t, 503, request("GET", "/api/aicc/asset-groups/group-a", keys[0], "").Code)
}
