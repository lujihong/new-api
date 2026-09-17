package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// RecoverAICCGroup requires a root dashboard identity and a verifiable recovery reason.
// Ordinary API tokens cannot convert a known remote resource ID into ownership.
func RecoverAICCGroup(c *gin.Context) {
	if c.GetInt("id") <= 0 || c.GetInt("role") < common.RoleRootUser || c.GetInt("token_id") != 0 {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "仅超级管理员可恢复历史素材归属"})
		return
	}
	var input struct {
		UserID   int    `json:"userId" binding:"required,gt=0"`
		GroupID  string `json:"groupId" binding:"required,max=255"`
		Evidence string `json:"evidence" binding:"required,max=2000"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请填写目标用户、素材组和归属核实依据"})
		return
	}
	input.GroupID = strings.TrimSpace(input.GroupID)
	input.Evidence = strings.TrimSpace(input.Evidence)
	if validAICCResourceID(input.GroupID) == "" || !strings.HasPrefix(input.GroupID, "group-") || input.Evidence == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "恢复参数无效"})
		return
	}
	target, err := model.GetUserById(input.UserID, false)
	if err != nil || target == nil || target.Status != common.UserStatusEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "目标用户不存在或已停用"})
		return
	}
	remote, err := service.GetAICCAssetGroup(c.Request.Context(), input.GroupID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "无法核实移动云素材组，未写入归属"})
		return
	}
	body, ok := remote["body"].(map[string]any)
	groupType, _ := body["groupType"].(string)
	if !ok || aiccGroupID(remote) != input.GroupID || (groupType != "AIGC" && groupType != "LivenessFace") {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "移动云未返回有效素材组身份，未写入归属"})
		return
	}
	err = model.RecoverAICCGroup(c.GetInt("id"), input.UserID, input.GroupID, groupType, input.Evidence)
	if errors.Is(err, model.ErrAICCRecoveryConflict) {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "归属恢复与审计事务失败，未完成恢复"})
		return
	}
	common.ApiSuccess(c, gin.H{"groupId": input.GroupID, "userId": input.UserID, "groupType": groupType})
}
