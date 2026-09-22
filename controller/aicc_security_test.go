package controller

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAICCChannelSelectionRejectsInvalidAndConflictingValues(t *testing.T) {
	for _, path := range []string{"/?channel_id=0", "/?channel_id=-1", "/?channel_id=no", "/?channel_id=", "/?channel_id=1&channel_id=2", "/?channel_id=1&channel_id=", "/?channel_id=999999999999999999999999"} {
		c, w := newAICCContext("GET", path, "", 101, common.RoleCommonUser)
		ListAICCAssetGroups(c)
		require.Equal(t, 400, w.Code, path)
	}
	for _, headers := range [][]string{{"0"}, {"-1"}, {"bad"}, {""}, {"1", "2"}, {"1,2"}} {
		c, w := newAICCContext("GET", "/", "", 101, common.RoleCommonUser)
		c.Request.Header["X-Aicc-Channel-Id"] = headers
		ListAICCAssetGroups(c)
		require.Equal(t, 400, w.Code)
	}
	c, w := newAICCContext("GET", "/?channel_id=1", "", 101, common.RoleCommonUser)
	c.Request.Header.Set("X-AICC-Channel-ID", "2")
	ListAICCAssetGroups(c)
	require.Equal(t, 400, w.Code)
	c, _ = newAICCContext("GET", "/?channel_id=1&channel_id=1", "", 101, common.RoleCommonUser)
	c.Request.Header.Set("X-AICC-Channel-ID", "1")
	id, present, err := aiccChannelIDFromRequest(c)
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, 1, id)
}

func TestAICCBindingRejectsChannelAndAccountChangesBeforeRemote(t *testing.T) {
	db := setupAICCTestDB(t)
	binding := aiccTestBinding(t)
	require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(101, "bound-group", "AIGC", binding))
	require.NoError(t, model.RecordBoundAICCAssetOwnership(101, "bound-asset", "bound-group", binding))
	require.NoError(t, model.RecordBoundAICCAuthSession(101, "bound-session", 1800, binding))
	calls := 0
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{"state":"OK","body":{}}`)) })
	for _, admin := range []bool{false, true} {
		for _, tc := range []struct {
			id      string
			handler gin.HandlerFunc
		}{{"bound-group", GetAICCAssetGroup}, {"bound-group", DeleteAICCAssetGroup}, {"bound-asset", GetAICCAsset}, {"bound-asset", DeleteAICCAsset}} {
			c, w := newAICCContext("GET", "/?channel_id=2", "", 101, common.RoleRootUser)
			c.Set("aicc_management_scope", admin)
			c.Params = gin.Params{{Key: "id", Value: tc.id}}
			tc.handler(c)
			require.Equal(t, 409, w.Code)
		}
	}
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 1).Update("aicc_account_id", "changed-account").Error)
	for _, tc := range []struct {
		id      string
		handler gin.HandlerFunc
	}{{"bound-group", DeleteAICCAssetGroup}, {"bound-asset", DeleteAICCAsset}} {
		c, w := newAICCContext("DELETE", "/?channel_id=1", "", 101, common.RoleRootUser)
		c.Set("aicc_management_scope", true)
		c.Params = gin.Params{{Key: "id", Value: tc.id}}
		tc.handler(c)
		require.Equal(t, 409, w.Code)
	}
	c, w := newAICCContext("POST", "/auth/group", `{"bytedToken":"bound-session"}`, 101, common.RoleCommonUser)
	QueryAICCGroupByBytedToken(c)
	require.Equal(t, 409, w.Code)
	require.Zero(t, calls)
	owned, err := model.UserOwnsAICCAsset(101, "bound-asset")
	require.NoError(t, err)
	require.True(t, owned)
}

func TestAICCSessionUsesBindingWhenDefaultBecomesAmbiguous(t *testing.T) {
	db := setupAICCTestDB(t)
	binding := aiccTestBinding(t)
	require.NoError(t, model.RecordBoundAICCAuthSession(101, "bound-session", 1800, binding))
	var ch model.Channel
	require.NoError(t, db.First(&ch, 1).Error)
	ch.Id = 2
	ch.Name = "second"
	require.NoError(t, db.Create(&ch).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "aicc-test-model", ChannelId: 2, Enabled: true}).Error)
	calls := 0
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"state":"OK","body":{"status":"SUCCESS","groupId":"bound-live","assetList":[{"assetId":"bound-live-asset"}]}}`))
	})
	c, w := newAICCContext("GET", "/", "", 101, common.RoleCommonUser)
	ListAICCAssetGroups(c)
	require.Equal(t, 400, w.Code)
	require.Zero(t, calls)
	c, w = newAICCContext("POST", "/auth/group", `{"bytedToken":"bound-session"}`, 101, common.RoleCommonUser)
	QueryAICCGroupByBytedToken(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"authenticated":true`)
	require.Contains(t, w.Body.String(), `"channelId":1`)
	require.Contains(t, w.Body.String(), binding.AICCAccountID)
	got, err := model.GetOwnedAICCAssetBinding(101, "bound-live-asset")
	require.NoError(t, err)
	require.Equal(t, binding, got)
	require.Equal(t, 1, calls)
}

func TestAICCManagementHistoryRequiresExplicitChannel(t *testing.T) {
	setupAICCTestDB(t)
	calls := 0
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"state":"OK","body":{"groupId":"history"}}`))
	})
	for _, tc := range []struct {
		query  string
		status int
	}{{"", 400}, {"?channel_id=1", 200}} {
		c, w := newAICCContext("GET", "/history"+tc.query, "", 999, common.RoleRootUser)
		c.Set("aicc_management_scope", true)
		c.Params = gin.Params{{Key: "id", Value: "history"}}
		GetAICCAssetGroup(c)
		require.Equal(t, tc.status, w.Code)
	}
	require.Equal(t, 1, calls)
	c, w := newAICCContext("GET", "/?channel_id=1", "", 101, common.RoleCommonUser)
	c.Params = gin.Params{{Key: "id", Value: "history"}}
	GetAICCAssetGroup(c)
	require.Equal(t, 403, w.Code)
	require.Equal(t, 1, calls)
}

func TestAICCCreationPersistsOriginalConfigSnapshot(t *testing.T) {
	db := setupAICCTestDB(t)
	binding := aiccTestBinding(t)
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 1).Update("aicc_account_id", "rotated-during-request").Error)
		w.Write([]byte(`{"state":"OK","body":"created-before-rotation"}`))
	})
	c, w := newAICCContext("POST", "/?channel_id=1", `{"groupName":"snapshot"}`, 101, common.RoleCommonUser)
	CreateAICCAssetGroup(c)
	require.Equal(t, 200, w.Code)
	got, err := model.GetOwnedAICCGroupBinding(101, "created-before-rotation")
	require.NoError(t, err)
	require.Equal(t, binding, got)
	require.NotEqual(t, binding, aiccTestBinding(t))
}

func TestAICCAuthConflictRollsBackNewGroup(t *testing.T) {
	setupAICCTestDB(t)
	binding := aiccTestBinding(t)
	require.NoError(t, model.RecordBoundAICCAuthSession(101, "token", 1800, binding))
	require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(202, "foreign-live", "LivenessFace", binding))
	require.NoError(t, model.RecordBoundAICCAssetOwnership(202, "conflicting-asset", "foreign-live", binding))
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"state":"OK","body":{"status":"SUCCESS","groupId":"new-live","assetList":[{"assetId":"conflicting-asset"}]}}`))
	})
	c, w := newAICCContext("POST", "/", `{"bytedToken":"token"}`, 101, common.RoleCommonUser)
	QueryAICCGroupByBytedToken(c)
	require.Equal(t, 409, w.Code)
	owned, err := model.UserOwnsAICCAssetGroup(101, "new-live")
	require.NoError(t, err)
	require.False(t, owned)
	owned, err = model.UserOwnsAICCAsset(202, "conflicting-asset")
	require.NoError(t, err)
	require.True(t, owned)
}

func TestAICCListsAreScopedAndAnnotatedBySelectedBinding(t *testing.T) {
	db := setupAICCTestDB(t)
	binding := aiccTestBinding(t)
	var ch model.Channel
	require.NoError(t, db.First(&ch, 1).Error)
	ch.Id = 2
	ch.Name = "other channel"
	require.NoError(t, db.Create(&ch).Error)
	other := binding
	other.ChannelID = 2
	other.AICCAccountID = strings.Repeat("b", 64)
	require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(101, "selected", "AIGC", binding))
	require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(101, "other", "AIGC", other))
	calls := 0
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &payload))
		require.Equal(t, []any{"selected"}, payload["groupIds"])
		w.Write([]byte(`{"state":"OK","body":{"data":[{"groupId":"selected","assetId":"selected-asset","channelId":99,"aiccAccountId":"spoof"},{"groupId":"other","assetId":"foreign-asset"}],"total":2}}`))
	})
	for _, handler := range []gin.HandlerFunc{ListAICCAssetGroups, QueryAICCAssets} {
		c, w := newAICCContext("GET", "/?channel_id=1", "", 101, common.RoleCommonUser)
		handler(c)
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), `"channelId":1`)
		require.Contains(t, w.Body.String(), binding.AICCAccountID)
		require.NotContains(t, w.Body.String(), "spoof")
		require.NotContains(t, w.Body.String(), "foreign-asset")
	}
	require.Equal(t, 2, calls)
}

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
	require.NoError(t, model.RecordBoundAICCAuthSession(101, "private-auth-token", 1800, aiccTestBinding(t)))
	require.Error(t, model.RecordBoundAICCAuthSession(202, "private-auth-token", 1800, aiccTestBinding(t)))
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
	require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(202, "foreign-group", "AIGC", aiccTestBinding(t)))
	require.NoError(t, model.DB.Create(&model.AICCAssetOwnership{UserID: 101, AssetID: "asset-owned", GroupID: "foreign-group", ChannelID: 1, AICCAccountID: aiccTestBinding(t).AICCAccountID}).Error)
	groups, err := aiccOwnedGroupIDs(101, aiccTestBinding(t))
	require.NoError(t, err)
	require.NotContains(t, groups, "foreign-group")
}

func TestAICCOwnedGroupAssetsBecomeUsableWithoutClaimingForeignAssets(t *testing.T) {
	setupAICCTestDB(t)
	require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(101, "group-own", "LivenessFace", aiccTestBinding(t)))
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
	require.NoError(t, model.RecordBoundAICCAuthSession(101, "token", 1800, aiccTestBinding(t)))
	responses := []string{
		`{"state":"OK","body":{"status":"SUCCESS"}}`,
		`{"state":"OK","body":{"assetId":"asset-foreign","groupId":"group-foreign"}}`,
		`{"state":"OK","body":{"metadata":{"groupId":"group-foreign"}}}`,
		`{"state":"OK","body":{"groupId":"group-foreign","status":"PROCESSING"}}`,
		`{"state":"OK","body":{"groupId":"group-foreign"}}`,
		`{"state":"OK","body":{"groupId":"group-foreign","status":"COMPLETED"}}`,
		`{"state":"OK","body":{"groupId":"group-foreign","metadata":{"status":"SUCCESS"}}}`,
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
	require.NoError(t, model.RecordBoundAICCAssetGroupOwnership(202, "group-existing", "AIGC", aiccTestBinding(t)))
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
