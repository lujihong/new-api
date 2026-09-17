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
	c, w := newAICCContext("POST", "/aicc/admin/recover-group", body, 101, common.RoleCommonUser)
	RecoverAICCGroup(c)
	require.Equal(t, 403, w.Code)
	require.Zero(t, calls)
	c, w = newAICCContext("POST", "/aicc/admin/recover-group", body, 1, common.RoleRootUser)
	c.Set("token_id", 77)
	RecoverAICCGroup(c)
	require.Equal(t, 403, w.Code)
	require.Zero(t, calls)
	c, w = newAICCContext("POST", "/aicc/admin/recover-group", body, 1, common.RoleRootUser)
	RecoverAICCGroup(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"success":true`)
	var count int64
	require.NoError(t, db.Model(&model.AICCRecoveryAudit{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	c, w = newAICCContext("POST", "/aicc/admin/recover-group", `{"userId":202,"groupId":"group-history","evidence":"试图覆盖归属"}`, 1, common.RoleRootUser)
	RecoverAICCGroup(c)
	require.Equal(t, 409, w.Code)
	owned, err := model.UserOwnsAICCAssetGroup(101, "group-history")
	require.NoError(t, err)
	require.True(t, owned)
	require.NoError(t, db.Model(&model.AICCRecoveryAudit{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestAICCRecoveryAuditFailureRollsBackOwnership(t *testing.T) {
	setupAICCTestDB(t) // No audit table: simulate an audit write failure.
	require.Error(t, model.RecoverAICCGroup(1, 101, "group-rollback", "AIGC", "已核对依据"))
	owned, err := model.UserOwnsAICCAssetGroup(101, "group-rollback")
	require.NoError(t, err)
	require.False(t, owned)
}
