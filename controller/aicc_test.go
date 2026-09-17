package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAICCTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	db, err := gorm.Open(sqlite.Open("file:aicc-controller-test?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.AICCAssetGroupOwnership{}, &model.AICCAssetOwnership{}, &model.AICCAuthSession{}))
	t.Cleanup(func() {
		model.DB = previousDB
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func newAICCContext(method, path, body string, userID, role int) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", userID)
	c.Set("role", role)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

func withAICCMock(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("AICC_ACCESS_KEY_ID", "test-ak")
	t.Setenv("AICC_ACCESS_KEY_SECRET", "test-sk")
	t.Setenv("AICC_ENDPOINT", srv.URL)
}

func TestAICCControllerAuthenticationStatusAndOwnership(t *testing.T) {
	setupAICCTestDB(t)
	responses := []string{
		`{"state":"OK","body":{"status":"PROCESSING"}}`,
		`{"state":"OK","body":{"groupId":"live-group","assetList":[{"assetId":"live-asset"}]}}`,
	}
	call := 0
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(responses[call]))
		call++
	})
	require.NoError(t, model.RecordAICCAuthSession(101, "token", 1800))

	for i, wantAuthenticated := range []bool{false, true} {
		ctx, recorder := newAICCContext(http.MethodGet, "/api/aicc/auth/session/token", "", 101, common.RoleCommonUser)
		ctx.Params = gin.Params{{Key: "token", Value: "token"}}
		QueryAICCGroupByBytedToken(ctx)
		require.Equal(t, http.StatusOK, recorder.Code)
		assert.Contains(t, recorder.Body.String(), `"authenticated":`+strconv.FormatBool(wantAuthenticated))
		if i == 0 {
			owned, err := model.UserOwnsAICCAssetGroup(101, "live-group")
			require.NoError(t, err)
			assert.False(t, owned)
		} else {
			ownedGroup, err := model.UserOwnsAICCAssetGroup(101, "live-group")
			require.NoError(t, err)
			ownedAsset, err := model.UserOwnsAICCAsset(101, "live-asset")
			require.NoError(t, err)
			assert.True(t, ownedGroup)
			assert.True(t, ownedAsset)
		}
	}
}

func TestAICCControllerRecordsOwnershipAndFiltersList(t *testing.T) {
	db := setupAICCTestDB(t)
	calls := 0
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/api/openapi-maas/exp/aicc/v2/asset-group":
			_, _ = w.Write([]byte(`{"state":"OK","body":"group-owned"}`))
		case "/api/openapi-maas/exp/aicc/v2/asset-group/query":
			_, _ = w.Write([]byte(`{"state":"OK","body":{"data":[{"groupId":"group-owned"},{"groupId":"group-other"}],"total":2}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})

	createCtx, createRecorder := newAICCContext(http.MethodPost, "/api/aicc/asset-groups", `{"groupName":"owned","description":"test"}`, 101, common.RoleCommonUser)
	CreateAICCAssetGroup(createCtx)
	require.Equal(t, http.StatusOK, createRecorder.Code)
	owned, err := model.UserOwnsAICCAssetGroup(101, "group-owned")
	require.NoError(t, err)
	assert.True(t, owned)

	listCtx, listRecorder := newAICCContext(http.MethodGet, "/api/aicc/asset-groups", "", 101, common.RoleCommonUser)
	ListAICCAssetGroups(listCtx)
	require.Equal(t, http.StatusOK, listRecorder.Code)
	assert.Contains(t, listRecorder.Body.String(), "group-owned")
	assert.NotContains(t, listRecorder.Body.String(), "group-other")
	assert.Equal(t, 2, calls)

	var count int64
	require.NoError(t, db.Model(&model.AICCAssetGroupOwnership{}).Where("user_id = ?", 101).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestAICCControllerRejectsForeignResourceBeforeUpstream(t *testing.T) {
	setupAICCTestDB(t)
	for _, tc := range []struct {
		name   string
		path   string
		handle gin.HandlerFunc
		seed   func(t *testing.T)
	}{
		{
			name:   "group",
			path:   "/api/aicc/asset-groups/foreign-group",
			handle: GetAICCAssetGroup,
			seed: func(t *testing.T) {
				require.NoError(t, model.RecordAICCAssetGroupOwnership(202, "foreign-group", "AIGC"))
			},
		},
		{
			name:   "asset",
			path:   "/api/aicc/assets/foreign-asset",
			handle: GetAICCAsset,
			seed: func(t *testing.T) {
				require.NoError(t, model.RecordAICCAssetOwnership(202, "foreign-asset", "foreign-group"))
			},
		},
		{
			name:   "asset update",
			path:   "/api/aicc/assets/foreign-asset",
			handle: UpdateAICCAsset,
			seed: func(t *testing.T) {
				require.NoError(t, model.RecordAICCAssetOwnership(202, "foreign-asset", "foreign-group"))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.seed(t)
			called := false
			withAICCMock(t, func(w http.ResponseWriter, r *http.Request) { called = true })
			ctx, recorder := newAICCContext(http.MethodGet, tc.path, `{"assetName":"blocked"}`, 101, common.RoleCommonUser)
			if tc.name == "group" {
				ctx.Params = gin.Params{{Key: "id", Value: "foreign-group"}}
			} else {
				ctx.Params = gin.Params{{Key: "id", Value: "foreign-asset"}}
			}
			tc.handle(ctx)
			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assert.Contains(t, recorder.Body.String(), "无权访问")
			assert.False(t, called)
		})
	}
}

func TestAICCControllerAdminCanManageAndDeleteOwnership(t *testing.T) {
	setupAICCTestDB(t)
	calls := 0
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"state":"OK","body":{"groupId":"foreign-group"}}`))
		case http.MethodDelete:
			_, _ = w.Write([]byte(`{"state":"OK","body":{}}`))
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	})
	require.NoError(t, model.RecordAICCAssetGroupOwnership(202, "foreign-group", "AIGC"))
	require.NoError(t, model.RecordAICCAssetOwnership(202, "foreign-asset", "foreign-group"))

	groupCtx, groupRecorder := newAICCContext(http.MethodGet, "/api/aicc/admin/asset-groups/foreign-group", "", 999, common.RoleRootUser)
	groupCtx.Set("aicc_management_scope", true)
	groupCtx.Params = gin.Params{{Key: "id", Value: "foreign-group"}}
	GetAICCAssetGroup(groupCtx)
	assert.Equal(t, http.StatusOK, groupRecorder.Code)

	assetCtx, assetRecorder := newAICCContext(http.MethodDelete, "/api/aicc/admin/assets/foreign-asset", "", 999, common.RoleRootUser)
	assetCtx.Set("aicc_management_scope", true)
	assetCtx.Params = gin.Params{{Key: "id", Value: "foreign-asset"}}
	DeleteAICCAsset(assetCtx)
	assert.Equal(t, http.StatusOK, assetRecorder.Code)
	owned, err := model.UserOwnsAICCAsset(202, "foreign-asset")
	require.NoError(t, err)
	assert.False(t, owned)
	assert.Equal(t, 2, calls)
}

func TestAICCControllerRejectsMissingResourceID(t *testing.T) {
	setupAICCTestDB(t)
	for _, tc := range []struct {
		name   string
		handle gin.HandlerFunc
		path   string
	}{
		{"asset get", GetAICCAsset, "/api/aicc/assets/"},
		{"asset update", UpdateAICCAsset, "/api/aicc/assets/"},
		{"asset delete", DeleteAICCAsset, "/api/aicc/assets/"},
		{"group get", GetAICCAssetGroup, "/api/aicc/asset-groups/"},
		{"group update", UpdateAICCAssetGroup, "/api/aicc/asset-groups/"},
		{"group delete", DeleteAICCAssetGroup, "/api/aicc/asset-groups/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recorder := newAICCContext(http.MethodGet, tc.path, `{}`, 101, common.RoleCommonUser)
			tc.handle(ctx)
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}
