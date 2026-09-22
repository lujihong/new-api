package service_test

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAICCChannelModelsCurrentPermissions(t *testing.T) {
	db := aiccTestDB(t)
	aiccTestChannel(t, db, 1, "https://example.test")
	aiccTestChannel(t, db, 2, "https://example.test")
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", 2).Update("group", "vip").Error)
	require.NoError(t, db.Create(&model.Ability{ChannelId: 1, Group: "default", Model: "second", Enabled: true}).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set("id", 1)
	// A dashboard context cannot select a different group with stale context data.
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "vip")
	got, err := service.AICCChannelModels(c, 0, false)
	require.NoError(t, err)
	require.Equal(t, map[int][]string{1: {"aicc-test-model", "second"}}, got)
	c.Set("token_id", 4)
	c.Set("token_model_limit_enabled", true)
	c.Set("token_model_limit", map[string]bool{"second": true})
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	got, err = service.AICCChannelModels(c, 1, false)
	require.NoError(t, err)
	require.Equal(t, []string{"second"}, got[1])
	_, err = service.AICCChannelModels(c, 2, false)
	require.ErrorIs(t, err, service.ErrAICCChannelForbidden)
	c.Set("token_model_limit", "malformed")
	_, err = service.AICCChannelModels(c, 1, false)
	require.ErrorIs(t, err, service.ErrAICCChannelForbidden)
	c.Set("token_model_limit_enabled", false)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 1).Update("status", 2).Error)
	_, err = service.AICCChannelModels(c, 1, false)
	require.ErrorIs(t, err, service.ErrAICCChannelForbidden)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 1).Update("status", 1).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1).Update("group", "vip").Error)
	c.Set("token_id", 0)
	got, err = service.AICCChannelModels(c, 0, false)
	require.NoError(t, err)
	require.Equal(t, map[int][]string{2: {"aicc-test-model"}}, got)
	require.NoError(t, db.Migrator().DropTable(&model.Ability{}))
	_, err = service.AICCChannelModels(c, 0, false)
	require.ErrorIs(t, err, service.ErrAICCAccessUnavailable)
	model.DB = nil
	_, err = service.AICCChannelModels(c, 0, false)
	require.ErrorIs(t, err, service.ErrAICCAccessUnavailable)
	model.DB = db
	c.Set("id", 0)
	_, err = service.AICCChannelModels(c, 0, false)
	require.ErrorIs(t, err, service.ErrAICCAuthenticationRequired)
}

func TestAICCChannelModelsAutoGroups(t *testing.T) {
	db := aiccTestDB(t)
	aiccTestChannel(t, db, 1, "https://example.test")
	aiccTestChannel(t, db, 2, "https://example.test")
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", 2).Update("group", "vip").Error)
	oldUsable, oldAuto, oldRatio := setting.UserUsableGroups2JSONString(), setting.AutoGroups2JsonString(), ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldUsable))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(oldAuto))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatio))
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","auto":"Auto"}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","vip"]`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set("id", 1)
	c.Set("token_id", 1)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "auto")
	got, err := service.AICCChannelModels(c, 0, false)
	require.NoError(t, err)
	require.Len(t, got, 2)
	common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	got, err = service.AICCChannelModels(c, 0, false)
	require.NoError(t, err)
	require.Equal(t, map[int][]string{2: {"aicc-test-model"}}, got)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","auto":"Auto"}`))
	_, err = service.AICCChannelModels(c, 0, false)
	require.ErrorIs(t, err, service.ErrAICCChannelForbidden)
}
