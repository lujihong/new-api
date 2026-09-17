package controller

import (
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAICCFilteredPageNeverLeaksAlternateFields(t *testing.T) {
	for _, raw := range []map[string]any{
		{"body": map[string]any{"list": []any{map[string]any{"groupId": "foreign-secret"}}}},
		{"body": map[string]any{"data": []any{}, "list": []any{map[string]any{"groupId": "foreign-secret"}}}},
		{"body": []any{map[string]any{"groupId": "foreign-secret"}}},
		{"body": map[string]any{"data": []any{}}, "debug": map[string]any{"groupId": "foreign-secret"}},
	} {
		result := aiccFilterPage(raw, map[string]struct{}{"owned": {}}, "groupId")
		wire, err := common.Marshal(result)
		require.NoError(t, err)
		require.NotContains(t, string(wire), "foreign-secret")
	}
}

func TestAICCSessionCannotBeReassignedOrQueriedByAnotherUser(t *testing.T) {
	db := setupAICCTestDB(t)
	require.NoError(t, model.RecordAICCAuthSession(101, "private-auth-token", 1800))
	require.Error(t, model.RecordAICCAuthSession(202, "private-auth-token", 1800))
	var row model.AICCAuthSession
	require.NoError(t, db.First(&row).Error)
	require.NotEqual(t, "private-auth-token", row.TokenHash)
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("foreign auth session must never reach upstream")
	})
	ctx, recorder := newAICCContext(http.MethodPost, "/auth/group", `{"bytedToken":"private-auth-token"}`, 202, common.RoleCommonUser)
	QueryAICCGroupByBytedToken(ctx)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.NoError(t, db.Model(&row).Update("expires_at", time.Now().Add(-time.Minute)).Error)
	ctx, recorder = newAICCContext(http.MethodGet, "/auth/session/token", "", 101, common.RoleCommonUser)
	ctx.Params = gin.Params{{Key: "token", Value: "private-auth-token"}}
	QueryAICCGroupByBytedToken(ctx)
	require.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestAICCOwningOneAssetDoesNotGrantItsEntireGroup(t *testing.T) {
	setupAICCTestDB(t)
	require.NoError(t, model.RecordAICCAssetGroupOwnership(202, "foreign-group", "AIGC"))
	require.NoError(t, model.RecordAICCAssetOwnership(101, "asset-owned", "foreign-group"))
	groups, err := aiccOwnedGroupIDs(101)
	require.NoError(t, err)
	require.NotContains(t, groups, "foreign-group")
}

func TestAICCOwnedGroupAssetsBecomeUsableWithoutClaimingForeignAssets(t *testing.T) {
	setupAICCTestDB(t)
	require.NoError(t, model.RecordAICCAssetGroupOwnership(101, "group-own", "LivenessFace"))
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &payload))
		require.Equal(t, []any{"group-own"}, payload["groupIds"])
		_, _ = w.Write([]byte(`{"state":"OK","body":{"data":[{"groupId":"group-own","assetId":"asset-own","status":"ACTIVE"},{"groupId":"group-foreign","assetId":"asset-foreign","status":"ACTIVE"}],"total":2}}`))
	})
	ctx, recorder := newAICCContext(http.MethodGet, "/assets?groupType=LivenessFace", "", 101, common.RoleCommonUser)
	QueryAICCAssets(ctx)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	require.Contains(t, recorder.Body.String(), "asset-own")
	require.NotContains(t, recorder.Body.String(), "asset-foreign")
	require.NoError(t, model.ValidateUserAICCAssetIDs(101, []string{"asset-own"}))
	require.Error(t, model.ValidateUserAICCAssetIDs(101, []string{"asset-foreign"}))
	require.Error(t, model.ValidateUserAICCAssetIDs(202, []string{"asset-own"}))
}

func TestAICCAuthDoesNotClaimGroupsFromNestedAssetsOrEmptySuccess(t *testing.T) {
	setupAICCTestDB(t)
	require.NoError(t, model.RecordAICCAuthSession(101, "token", 1800))
	responses := []string{
		`{"state":"OK","body":{"status":"SUCCESS"}}`,
		`{"state":"OK","body":{"assetId":"asset-foreign","groupId":"group-foreign"}}`,
		`{"state":"OK","body":{"metadata":{"groupId":"group-foreign"}}}`,
		`{"state":"OK","body":{"groupId":"group-foreign","status":"PROCESSING"}}`,
	}
	for _, response := range responses {
		t.Run(response, func(t *testing.T) {
			withAICCMock(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(response)) })
			ctx, recorder := newAICCContext(http.MethodPost, "/auth/group", `{"bytedToken":"token"}`, 101, common.RoleCommonUser)
			QueryAICCGroupByBytedToken(ctx)
			require.Contains(t, recorder.Body.String(), `"authenticated":false`)
			groups, err := model.UserOwnedAICCGroupIDs(101)
			require.NoError(t, err)
			require.Empty(t, groups)
		})
	}
}

func TestAICCCreateConflictNeverDeletesExistingRemoteGroup(t *testing.T) {
	setupAICCTestDB(t)
	require.NoError(t, model.RecordAICCAssetGroupOwnership(202, "group-existing", "AIGC"))
	deletes := 0
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes++
		}
		_, _ = w.Write([]byte(`{"state":"OK","body":"group-existing"}`))
	})
	ctx, recorder := newAICCContext(http.MethodPost, "/asset-groups", `{"groupName":"test"}`, 101, common.RoleCommonUser)
	CreateAICCAssetGroup(ctx)
	require.Zero(t, deletes)
	require.Contains(t, recorder.Body.String(), `"success":false`)
	owned, err := model.UserOwnsAICCAssetGroup(202, "group-existing")
	require.NoError(t, err)
	require.True(t, owned)
}
