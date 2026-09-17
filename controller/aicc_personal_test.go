package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAICCManagementScopeRequiresDashboardAdminAndDoesNotClaimAssets(t *testing.T) {
	db := setupAICCTestDB(t)
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"state":"OK","body":{"data":[{"assetId":"asset-unclaimed","groupId":"group-unclaimed"}],"total":1}}`))
	})
	for _, tc := range []struct{ role, token, want int }{
		{common.RoleCommonUser, 0, 403}, {common.RoleRootUser, 5, 403}, {common.RoleAdminUser, 0, 200},
	} {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("id", 999); c.Set("role", tc.role); c.Set("token_id", tc.token) })
		router.GET("/api/aicc/admin/assets", AICCManagementScope, QueryAICCAssets)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/aicc/admin/assets", nil))
		require.Equal(t, tc.want, recorder.Code)
		if tc.want == 200 {
			require.Contains(t, recorder.Body.String(), "asset-unclaimed")
		}
	}
	var count int64
	require.NoError(t, db.Model(&model.AICCAssetOwnership{}).Count(&count).Error)
	require.Zero(t, count, "management reads cannot acquire assets")
}

func TestAICCAdminPersonalScopeCannotReadForeignResources(t *testing.T) {
	setupAICCTestDB(t)
	require.NoError(t, model.RecordAICCAssetGroupOwnership(202, "foreign-group", "AIGC"))
	require.NoError(t, model.RecordAICCAssetOwnership(202, "asset-foreign", "foreign-group"))
	require.NoError(t, model.RecordAICCAuthSession(202, "foreign-session", 1800))
	calls := 0
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"state":"OK","body":{"data":[],"total":0}}`))
	})
	for _, role := range []int{common.RoleAdminUser, common.RoleRootUser} {
		for _, tc := range []struct {
			path, id, body string
			handler        gin.HandlerFunc
		}{
			{"/api/aicc/assets/asset-foreign", "asset-foreign", "", GetAICCAsset},
			{"/api/aicc/asset-groups/foreign-group", "foreign-group", "", GetAICCAssetGroup},
			{"/api/aicc/auth/group", "", `{"bytedToken":"foreign-session"}`, QueryAICCGroupByBytedToken},
		} {
			method := http.MethodGet
			if tc.body != "" {
				method = http.MethodPost
			}
			ctx, recorder := newAICCContext(method, tc.path, tc.body, 999, role)
			ctx.Params = gin.Params{{Key: "id", Value: tc.id}}
			tc.handler(ctx)
			require.Equal(t, http.StatusForbidden, recorder.Code, tc.path)
		}
		for _, handler := range []gin.HandlerFunc{ListAICCAssetGroups, QueryAICCAssets} {
			ctx, recorder := newAICCContext(http.MethodGet, "/api/aicc/assets?scope=all", "", 999, role)
			handler(ctx)
			require.Contains(t, recorder.Body.String(), `"total":0`)
		}
	}
	require.Zero(t, calls, "personal administrators must not query foreign upstream resources")
}

func TestAICCAdminPersonalAuthenticationRecordsOwnership(t *testing.T) {
	setupAICCTestDB(t)
	require.NoError(t, model.RecordAICCAuthSession(999, "admin-session", 1800))
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"state":"OK","body":{"groupId":"group-live-admin","assetList":[{"assetId":"asset-live-admin"}]}}`))
	})
	ctx, recorder := newAICCContext(http.MethodPost, "/api/aicc/auth/group", `{"bytedToken":"admin-session"}`, 999, common.RoleRootUser)
	QueryAICCGroupByBytedToken(ctx)
	require.Contains(t, recorder.Body.String(), `"authenticated":true`)
	owned, err := model.UserOwnsAICCAssetGroup(999, "group-live-admin")
	require.NoError(t, err)
	require.True(t, owned)
	require.NoError(t, model.ValidateUserAICCAssetIDs(999, []string{"asset-live-admin"}))
}

func TestAICCAdminPersonalCreationRecordsOwnership(t *testing.T) {
	setupAICCTestDB(t)
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"state":"OK","body":"group-admin-own"}`))
	})
	ctx, recorder := newAICCContext(http.MethodPost, "/api/aicc/asset-groups", `{"groupName":"my group"}`, 999, common.RoleRootUser)
	CreateAICCAssetGroup(ctx)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	owned, err := model.UserOwnsAICCAssetGroup(999, "group-admin-own")
	require.NoError(t, err)
	require.True(t, owned)
}
