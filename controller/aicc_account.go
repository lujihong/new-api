package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Root sessions explicitly attest that the AICC and video credentials belong
// to the same upstream account. This is evidence recording, not provider verification.
func AICCAccountBinding(c *gin.Context) {
	if c.GetInt("id") <= 0 || c.GetInt("role") < common.RoleRootUser || c.GetInt("token_id") != 0 {
		aiccError(c, http.StatusForbidden, errAICCAccountPermission{})
		return
	}
	channelID, present, err := aiccChannelIDFromRequest(c)
	if err != nil || !present {
		c.JSON(400, gin.H{"success": false, "message": "请明确指定 channel_id"})
		return
	}
	cfg, err := service.GetAICCConfigForChannel(channelID)
	if err != nil {
		aiccError(c, 400, err)
		return
	}
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		aiccError(c, 400, err)
		return
	}
	fingerprint, err := service.AICCChannelCredentialFingerprint(*channel)
	if err != nil {
		aiccError(c, 400, err)
		return
	}
	if c.Request.Method == http.MethodGet {
		common.ApiSuccess(c, gin.H{"channelId": channelID, "channelName": cfg.ChannelName, "configFingerprint": fingerprint, "verified": cfg.AccountVerified, "notice": "请核对上游账号编号、AICC密钥和视频APIKey归属后提交；渠道名和地域不能证明账号相同"})
		return
	}
	var input struct {
		UpstreamSubject   string `json:"upstreamSubject" binding:"required,max=255"`
		Evidence          string `json:"evidence" binding:"required,min=10,max=2000"`
		ConfigFingerprint string `json:"configFingerprint" binding:"required,len=64"`
		Confirmed         bool   `json:"confirmed"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || !input.Confirmed || strings.TrimSpace(input.UpstreamSubject) == "" {
		c.JSON(400, gin.H{"success": false, "message": "请填写真实账号标识和核实依据，并明确确认两类凭据属于同一账号；依据中不要填写密钥"})
		return
	}
	if err := service.AttestAICCChannelAccount(c.GetInt("id"), channelID, input.UpstreamSubject, input.Evidence, input.ConfigFingerprint); err != nil {
		aiccError(c, 409, err)
		return
	}
	common.ApiSuccess(c, gin.H{"channelId": channelID, "verified": true})
}

type errAICCAccountPermission struct{}

func (errAICCAccountPermission) Error() string {
	return "仅超级管理员登录会话可核实素材账号关联"
}
