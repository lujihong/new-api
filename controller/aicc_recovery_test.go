package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestAICCRecoveryRootOnlyAndCannotReassign(t *testing.T) {
	db := setupAICCTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AICCRecoveryAudit{}))
	require.NoError(t, db.Create(&model.User{Id: 101, Username: "test-a", AffCode: "test-a", Password: "offline", Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.User{Id: 202, Username: "test-b", AffCode: "test-b", Password: "offline", Status: common.UserStatusEnabled}).Error)
	calls := 0
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, http.MethodGet, r.Method)
		_, _ = w.Write([]byte(`{"state":"OK","body":{"groupId":"group-history","groupType":"AIGC"}}`))
	})
	body := `{"userId":101,"groupId":"group-history","evidence":"核对历史创建记录与原用户身份"}`
	c, w := newAICCContext("POST", "/aicc/admin/recover-group?channel_id=1", body, 101, common.RoleCommonUser)
	RecoverAICCGroup(c)
	require.Equal(t, 403, w.Code)
	require.Zero(t, calls)
	c, w = newAICCContext("POST", "/aicc/admin/recover-group?channel_id=1", body, 1, common.RoleRootUser)
	c.Set("token_id", 77)
	RecoverAICCGroup(c)
	require.Equal(t, 403, w.Code)
	require.Zero(t, calls)
	for _, query := range []string{"", "?channel_id=0", "?channel_id=bad", "?channel_id=1&channel_id=2"} {
		c, w = newAICCContext("POST", "/aicc/admin/recover-group"+query, body, 1, common.RoleRootUser)
		RecoverAICCGroup(c)
		require.Equal(t, 400, w.Code)
		require.Zero(t, calls)
	}
	c, w = newAICCContext("POST", "/aicc/admin/recover-group?channel_id=1", body, 1, common.RoleRootUser)
	RecoverAICCGroup(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"success":true`)
	var count int64
	require.NoError(t, db.Model(&model.AICCRecoveryAudit{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	c, w = newAICCContext("POST", "/aicc/admin/recover-group?channel_id=1", `{"userId":202,"groupId":"group-history","evidence":"试图覆盖归属"}`, 1, common.RoleRootUser)
	RecoverAICCGroup(c)
	require.Equal(t, 409, w.Code)
	owned, err := model.UserOwnsAICCAssetGroup(101, "group-history")
	require.NoError(t, err)
	require.True(t, owned)
	require.NoError(t, db.Model(&model.AICCRecoveryAudit{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestAICCRecoveryBoundConflictsAndRollback(t *testing.T) {
	db := setupAICCTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AICCRecoveryAudit{}))
	binding := aiccTestBinding(t)
	require.NoError(t, model.RecoverBoundAICCGroup(1, 101, "group-bound", "AIGC", "verified", binding))
	var audit model.AICCRecoveryAudit
	require.NoError(t, db.First(&audit).Error)
	require.Equal(t, binding.ChannelID, audit.ChannelID)
	require.Equal(t, binding.AICCAccountID, audit.AICCAccountID)
	for _, tc := range []struct {
		owner   int
		kind    string
		binding model.AICCBinding
	}{
		{202, "AIGC", binding}, {101, "LivenessFace", binding},
		{101, "AIGC", model.AICCBinding{ChannelID: 2, AICCAccountID: binding.AICCAccountID}},
		{101, "AIGC", model.AICCBinding{ChannelID: 1, AICCAccountID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
	} {
		require.ErrorIs(t, model.RecoverBoundAICCGroup(1, tc.owner, "group-bound", tc.kind, "verified", tc.binding), model.ErrAICCRecoveryConflict)
	}
	require.NoError(t, model.RecordAICCAssetGroupOwnership(101, "group-legacy", "AIGC"))
	require.ErrorIs(t, model.RecoverBoundAICCGroup(1, 101, "group-legacy", "AIGC", "verified", binding), model.ErrAICCRecoveryConflict)
	for _, tc := range []struct {
		group          string
		owner, channel int
		account        string
	}{
		{"group-orphan-owner", 202, binding.ChannelID, binding.AICCAccountID},
		{"group-orphan-account", 101, binding.ChannelID, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		{"group-orphan-legacy", 101, 0, ""},
	} {
		require.NoError(t, db.Create(&model.AICCAssetOwnership{UserID: tc.owner, AssetID: "asset-" + tc.group, GroupID: tc.group, ChannelID: tc.channel, AICCAccountID: tc.account}).Error)
		require.ErrorIs(t, model.RecoverBoundAICCGroup(1, 101, tc.group, "AIGC", "verified", binding), model.ErrAICCRecoveryConflict)
		var count int64
		require.NoError(t, db.Model(&model.AICCAssetGroupOwnership{}).Where("group_id = ?", tc.group).Count(&count).Error)
		require.Zero(t, count)
	}
	var count int64
	require.NoError(t, db.Model(&model.AICCRecoveryAudit{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestAICCRecoveryAuditFailureRollsBackOwnership(t *testing.T) {
	setupAICCTestDB(t) // No audit table: simulate an audit write failure.
	require.Error(t, model.RecoverAICCGroup(1, 101, "group-rollback", "AIGC", "已核对依据"))
	owned, err := model.UserOwnsAICCAssetGroup(101, "group-rollback")
	require.NoError(t, err)
	require.False(t, owned)
	require.Error(t, model.RecoverBoundAICCGroup(1, 101, "group-bound-rollback", "AIGC", "已核对依据", aiccTestBinding(t)))
	owned, err = model.UserOwnsAICCAssetGroup(101, "group-bound-rollback")
	require.NoError(t, err)
	require.False(t, owned)
}
