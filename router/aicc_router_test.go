package router

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAICCRealAuthenticationScopeMatrix(t *testing.T) {
	oldDB, oldLogDB, oldRedis, oldSecret := model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/auth.db"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret = db, db, false, "aicc-auth-test-secret"
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret = oldDB, oldLogDB, oldRedis, oldSecret
		sqlDB, _ := db.DB()
		sqlDB.Close()
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Token{}, &model.AuditLog{}, &model.AICCAssetGroupOwnership{}, &model.AICCAssetOwnership{}, &model.Channel{}, &model.AICCAccountAttestation{}))
	oldMainType, oldLogType, oldMaster := common.MainDatabaseType(), common.LogDatabaseType(), common.IsMasterNode
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.IsMasterNode = false
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB()) // Initialize the dialect's quoted key columns as production startup does.
	t.Cleanup(func() { common.SetDatabaseTypes(oldMainType, oldLogType); common.IsMasterNode = oldMaster })
	keys := make(map[string]string)
	for i, name := range []string{"admin", "ordinary", "disabled"} {
		role, status := common.RoleCommonUser, common.UserStatusEnabled
		if name == "admin" {
			role = common.RoleRootUser
		}
		if name == "disabled" {
			status = common.UserStatusDisabled
		}
		u := model.User{Id: 700 + i, Username: "aicc-" + name, AffCode: "aicc-" + name, Role: role, Status: status, Group: "default", AuthVersion: 1}
		require.NoError(t, db.Create(&u).Error)
		session := &model.UserSession{SID: "aicc-session-" + name, UserID: u.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: "hash-" + name, LoginMethod: "password", LastActiveAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix()}
		require.NoError(t, model.CreateUserSession(session))
		keys[name], _, err = service.IssueAccessToken(service.AuthIdentity{UserID: u.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1})
		require.NoError(t, err)
	}
	tokenKey := strings.Repeat("a", 48)
	require.NoError(t, db.Create(&model.Token{UserId: 700, Key: tokenKey, Status: common.TokenStatusEnabled, Name: "aicc-model-token", ExpiredTime: -1, UnlimitedQuota: true}).Error)
	keys["model"] = "sk-" + tokenKey
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"state":"OK","body":{"data":[{"groupId":"not-owned"}],"total":1}}`))
	}))
	defer upstream.Close()
	info, err := common.Marshal(map[string]any{"aicc_enabled": true, "access_key_id": "offline-ak", "access_key_secret": "offline-sk", "endpoint": upstream.URL, "pool_id": "CIDC-CORE-00"})
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Channel{Id: 6, Status: common.ChannelStatusEnabled, OtherInfo: string(info)}).Error)
	engine := gin.New()
	SetApiRouter(engine)
	for _, tc := range []struct {
		identity, path string
		want           int
		foreign        bool
	}{
		{"admin", "/api/aicc/asset-groups", 200, false},
		{"model", "/api/aicc/asset-groups", 200, false},
		{"ordinary", "/api/aicc/admin/asset-groups", 403, false},
		{"disabled", "/api/aicc/admin/asset-groups", 401, false},
		{"model", "/api/aicc/admin/asset-groups", 401, false},
		{"admin", "/api/aicc/admin/asset-groups?channel_id=6", 200, true},
		{"admin", "/api/aicc/admin/asset-groups", 400, false},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+keys[tc.identity])
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, tc.want, rec.Code, tc.identity+" "+tc.path+" "+rec.Body.String())
		if tc.foreign {
			require.Contains(t, rec.Body.String(), "not-owned")
		} else {
			require.NotContains(t, rec.Body.String(), "not-owned")
		}
	}
}

func TestAICCRoutesSeparateManagementAndBinaryDownload(t *testing.T) {
	oldSecret, oldAddress := common.CryptoSecret, system_setting.TaskPublicAddress
	common.CryptoSecret, system_setting.TaskPublicAddress = "test-aicc-signing-secret", "https://example.test"
	t.Cleanup(func() { common.CryptoSecret, system_setting.TaskPublicAddress = oldSecret, oldAddress })
	t.Setenv("AICC_UPLOAD_DIR", t.TempDir())
	store, err := service.DefaultAICCUploadStore()
	require.NoError(t, err)
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 400, 400))))
	upload, err := store.Save(context.Background(), 101, "group-test", "Image", "image/png", bytes.NewReader(data.Bytes()))
	require.NoError(t, err)
	parsed, err := url.Parse(upload.URL)
	require.NoError(t, err)
	engine := gin.New()
	SetApiRouter(engine)
	server := httptest.NewServer(engine)
	defer server.Close()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req, err := http.NewRequest(method, server.URL+parsed.RequestURI(), nil)
		require.NoError(t, err)
		req.Header.Set("Accept-Encoding", "gzip")
		res, err := server.Client().Do(req)
		require.NoError(t, err)
		got, err := io.ReadAll(res.Body)
		res.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Empty(t, res.Header.Get("Content-Encoding"))
		require.Contains(t, res.Header.Get("Cache-Control"), "no-store")
		if method == http.MethodHead {
			require.Empty(t, got)
		} else {
			require.Equal(t, data.Bytes(), got)
		}
	}
	for _, path := range []string{"/api/aicc/assets", "/api/aicc/admin/assets", "/api/aicc/admin/asset-groups"} {
		res := performPluginRequest(engine, http.MethodGet, path)
		require.Equal(t, http.StatusUnauthorized, res.Code, path)
	}
}
