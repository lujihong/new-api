package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestAICCChannelAccessBindingAndUploadRevocation(t *testing.T) {
	setupAICCUploadController(t)
	binding := aiccTestBinding(t)
	require.NoError(t, model.RecordBoundAICCAssetOwnership(101, "mine-asset", "mine", binding))
	require.NoError(t, model.RecordBoundAICCAuthSession(101, "mine-session", 1800, binding))
	calls := 0
	withAICCMock(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
	require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", 1).Update("enabled", false).Error)
	for _, tc := range []struct {
		handler  gin.HandlerFunc
		id, body string
	}{
		{GetAICCAssetGroup, "mine", ""}, {GetAICCAsset, "mine-asset", ""},
		{QueryAICCGroupByBytedToken, "", `{"bytedToken":"mine-session"}`},
		{CreateAICCAsset, "", `{"groupId":"mine","assetName":"a","assetUrl":"https://example.test","assetType":"Image"}`},
		{CreateAICCAssetGroup, "", `{"groupName":"new","channelId":1}`},
	} {
		c, w := newAICCContext("POST", "/?channel_id=1", tc.body, 101, common.RoleRootUser)
		c.Params = gin.Params{{Key: "id", Value: tc.id}}
		tc.handler(c)
		require.Equal(t, 403, w.Code, w.Body.String())
	}
	c, w := aiccUploadControllerRequest(t, aiccUploadControllerPNG(t), "mine", "Image", "image/png")
	CreateAICCUpload(c)
	require.Equal(t, 403, w.Code)
	require.Zero(t, calls)
}

func TestAICCChannelAccessTokenCannotUseManagementException(t *testing.T) {
	setupAICCTestDB(t)
	for _, handler := range []gin.HandlerFunc{ListAICCChannels, ListAICCAssetGroups} {
		c, w := newAICCContext("GET", "/?channel_id=1", "", 101, common.RoleRootUser)
		c.Set("token_id", 1)
		c.Set("aicc_management_scope", true)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		c.Set("token_model_limit_enabled", true)
		c.Set("token_model_limit", map[string]bool{"foreign": true})
		handler(c)
		require.Equal(t, 403, w.Code)
	}
}
