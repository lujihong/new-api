package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func aiccUserID(c *gin.Context) int { return c.GetInt("id") }

// Management scope is set only by the dedicated authenticated management route.
// A role, query parameter or model API token must never widen personal scope.
func isAICCAdmin(c *gin.Context) bool {
	return c.GetBool("aicc_management_scope") && c.GetInt("role") >= common.RoleAdminUser && c.GetInt("token_id") == 0
}

func AICCManagementScope(c *gin.Context) {
	if aiccUserID(c) <= 0 || c.GetInt("role") < common.RoleAdminUser || c.GetInt("token_id") != 0 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "请使用管理员登录会话进入素材管理"})
		return
	}
	c.Set("aicc_management_scope", true)
	c.Next()
}

func requireAICCGroupOwner(c *gin.Context, groupID string) bool {
	if isAICCAdmin(c) {
		return true
	}
	ok, err := model.UserOwnsAICCAssetGroup(aiccUserID(c), groupID)
	if err != nil {
		common.ApiError(c, err)
		return false
	}
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "无权访问该素材组"})
		return false
	}
	return true
}

func requireAICCAssetOwner(c *gin.Context, assetID string) bool {
	if isAICCAdmin(c) {
		return true
	}
	ok, err := model.UserOwnsAICCAsset(aiccUserID(c), assetID)
	if err != nil {
		common.ApiError(c, err)
		return false
	}
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "无权访问该素材"})
		return false
	}
	return true
}

func aiccBody(data map[string]any) any { return data["body"] }

func aiccGroupID(data map[string]any) string {
	body := aiccBody(data)
	if value, ok := body.(string); ok {
		return strings.TrimSpace(value)
	}
	if object, ok := body.(map[string]any); ok {
		value, _ := object["groupId"].(string)
		return strings.TrimSpace(value)
	}
	return ""
}

func aiccAssetID(data map[string]any) string {
	body := aiccBody(data)
	if value, ok := body.(string); ok {
		return strings.TrimSpace(value)
	}
	if object, ok := body.(map[string]any); ok {
		value, _ := object["assetId"].(string)
		return strings.TrimSpace(value)
	}
	return ""
}

type aiccResourceRefs struct {
	GroupIDs map[string]struct{}
	Assets   map[string]string
}

func aiccAuthStatus(value any) (hasStatus bool, terminalSuccess bool, terminalFailure bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return false, false, false
	}
	for _, key := range []string{"status", "authStatus", "authenticationStatus", "verifyStatus", "result"} {
		status, ok := object[key].(string)
		if !ok {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(status)) {
		case "SUCCESS", "SUCCEEDED", "COMPLETED", "APPROVED", "PASS", "PASSED", "AUTHENTICATED":
			terminalSuccess = true
			hasStatus = true
		case "PROCESSING", "PENDING", "WAITING", "INIT", "IN_PROGRESS", "FAILED", "FAIL", "REJECTED", "DENIED", "EXPIRED":
			terminalFailure = true
			hasStatus = true
		}
	}
	for _, child := range object {
		childHasStatus, childSuccess, childFailure := aiccAuthStatus(child)
		hasStatus = hasStatus || childHasStatus
		terminalSuccess = terminalSuccess || childSuccess
		terminalFailure = terminalFailure || childFailure
	}
	return hasStatus, terminalSuccess, terminalFailure
}

func newAICCResourceRefs() aiccResourceRefs {
	return aiccResourceRefs{
		GroupIDs: make(map[string]struct{}),
		Assets:   make(map[string]string),
	}
}

func validAICCResourceID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "/?#") {
		return ""
	}
	return value
}

func aiccStringField(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok {
			if value = validAICCResourceID(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func aiccOwnedGroupIDs(userID int) (map[string]struct{}, error) {
	// An asset-level grant must not imply access to every asset in its group.
	return model.UserOwnedAICCGroupIDs(userID)
}

func aiccRestrictIDs(requested []string, allowed map[string]struct{}) []string {
	if len(requested) == 0 {
		result := make([]string, 0, len(allowed))
		for id := range allowed {
			result = append(result, id)
		}
		return result
	}
	result := make([]string, 0, len(requested))
	for _, id := range requested {
		if _, ok := allowed[id]; ok {
			result = append(result, id)
		}
	}
	return result
}

func aiccEmptyPage(pageNo, pageSize int) map[string]any {
	return map[string]any{
		"state": "OK",
		"body": map[string]any{
			"data":     []any{},
			"total":    0,
			"pageNo":   pageNo,
			"pageSize": pageSize,
		},
	}
}

func aiccFilterPage(data map[string]any, allowed map[string]struct{}, key string) map[string]any {
	// Return a new envelope so alternate fields cannot bypass tenant filtering.
	result := aiccEmptyPage(1, 20)
	output := result["body"].(map[string]any)
	body, ok := data["body"].(map[string]any)
	if !ok {
		result["state"] = "EXCEPTION"
		return result
	}
	items, ok := body["data"].([]any)
	if !ok {
		result["state"] = "EXCEPTION"
		return result
	}
	filtered := make([]any, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, valid := object[key].(string)
		if _, permitted := allowed[strings.TrimSpace(id)]; valid && permitted {
			projected := make(map[string]any)
			for _, field := range []string{"groupId", "groupType", "groupName", "description", "assetId", "assetName", "assetType", "assetUrl", "status", "createdTime", "updatedTime", "assetCount", "failureReason"} {
				switch value := object[field].(type) {
				case string, float64, bool:
					projected[field] = value
				}
			}
			filtered = append(filtered, projected)
		}
	}
	output["data"] = filtered
	output["total"] = len(filtered)
	// Only trust upstream pagination when every row obeys the requested scope.
	if len(filtered) == len(items) {
		if total, valid := body["total"].(float64); valid && total >= float64(len(filtered)) {
			output["total"] = total
		}
	}
	for _, name := range []string{"pageNo", "pageSize"} {
		if value, valid := body[name].(float64); valid && value > 0 {
			output[name] = value
		}
	}
	return result
}

func checkAICCAuth(c *gin.Context) bool {
	if aiccUserID(c) <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "请先登录或提供有效的 API 令牌",
		})
		return false
	}
	return true
}

// CreateAICCH5Session 创建真人实名认证会话，返回 H5 认证链接和 bytedToken
func CreateAICCH5Session(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	session, err := service.CreateAICCH5Session(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	if err := model.RecordAICCAuthSession(aiccUserID(c), session.BytedToken, session.ExpiresIn); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "认证会话保存失败，请重新发起认证"})
		return
	}
	common.ApiSuccess(c, session)
}

// QueryAICCGroupByBytedToken 通过 bytedToken 查询真人认证状态及素材组信息
func QueryAICCGroupByBytedToken(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	token := strings.TrimSpace(c.Param("token"))
	if c.Request.Method == http.MethodPost {
		var req struct {
			BytedToken string `json:"bytedToken" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			common.ApiErrorMsg(c, "bytedToken is required")
			return
		}
		token = strings.TrimSpace(req.BytedToken)
	}
	if token == "" {
		token = strings.TrimSpace(c.Query("token"))
	}
	if token == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "token is required",
		})
		return
	}

	if !isAICCAdmin(c) {
		owned, ownershipErr := model.UserOwnsAICCAuthSession(aiccUserID(c), token)
		if ownershipErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "认证会话校验失败"})
			return
		}
		if !owned {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "认证会话不存在、已过期或不属于当前用户，请重新发起认证"})
			return
		}
	}
	data, err := service.QueryAICCGroupByBytedToken(c.Request.Context(), token)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	refs := newAICCResourceRefs()
	// A nested asset or metadata object is not proof of whole-group ownership.
	body, bodyIsObject := data["body"].(map[string]any)
	groupID := validAICCResourceID(aiccGroupID(data))
	if bodyIsObject && aiccStringField(body, "assetId", "assetID") != "" {
		groupID = ""
	}
	if groupID != "" {
		refs.GroupIDs[groupID] = struct{}{}
		if assets, ok := body["assetList"].([]any); ok {
			for _, item := range assets {
				asset, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if explicitGroup := aiccStringField(asset, "groupId"); explicitGroup != "" && explicitGroup != groupID {
					continue
				}
				if assetID := aiccStringField(asset, "assetId"); assetID != "" {
					refs.Assets[assetID] = groupID
				}
			}
		}
	}
	hasStatus, terminalSuccess, terminalFailure := aiccAuthStatus(body)
	authenticated := len(refs.GroupIDs) > 0
	if hasStatus {
		authenticated = authenticated && terminalSuccess && !terminalFailure
	}
	if authenticated && !isAICCAdmin(c) {
		for groupID := range refs.GroupIDs {
			if err := model.RecordAICCAssetGroupOwnership(aiccUserID(c), groupID, "LivenessFace"); err != nil {
				common.ApiError(c, err)
				return
			}
		}
		for assetID, groupID := range refs.Assets {
			if err := model.RecordAICCAssetOwnership(aiccUserID(c), assetID, groupID); err != nil {
				common.ApiError(c, err)
				return
			}
		}
	}
	data["authenticated"] = authenticated

	common.ApiSuccess(c, data)
}

func aiccQueryValues(c *gin.Context, key string) []string {
	var result []string
	for _, raw := range append(c.QueryArray(key), c.QueryArray(key+"[]")...) {
		for _, value := range strings.Split(raw, ",") {
			if value = strings.TrimSpace(value); value != "" {
				result = append(result, value)
			}
		}
	}
	return result
}

// ListAICCAssetGroups 查询素材组列表
func ListAICCAssetGroups(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	pageNo, _ := strconv.Atoi(c.DefaultQuery("pageNo", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	groupType := c.DefaultQuery("groupType", "")
	requestedGroupIDs := aiccQueryValues(c, "groupIds")
	var ownedGroupIDs map[string]struct{}
	if !isAICCAdmin(c) {
		var ownershipErr error
		ownedGroupIDs, ownershipErr = aiccOwnedGroupIDs(aiccUserID(c))
		if ownershipErr != nil {
			common.ApiError(c, ownershipErr)
			return
		}
		requestedGroupIDs = aiccRestrictIDs(requestedGroupIDs, ownedGroupIDs)
		if len(requestedGroupIDs) == 0 {
			common.ApiSuccess(c, aiccEmptyPage(pageNo, pageSize))
			return
		}
	}

	data, err := service.ListAICCAssetGroups(c.Request.Context(), pageNo, pageSize, groupType, service.AICCGroupFilters{GroupName: c.Query("groupName"), GroupIDs: requestedGroupIDs})
	if err == nil && !isAICCAdmin(c) {
		data = aiccFilterPage(data, ownedGroupIDs, "groupId")
		if data["state"] != "OK" {
			err = fmt.Errorf("移动云素材列表响应格式异常，请稍后重试")
		}
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	common.ApiSuccess(c, data)
}

// CreateAICCAssetGroup 创建 AIGC 素材组
func CreateAICCAssetGroup(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	var req struct {
		GroupName   string `json:"groupName" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误: " + err.Error(),
		})
		return
	}

	data, err := service.CreateAICCAssetGroup(c.Request.Context(), req.GroupName, req.Description)
	if err == nil && !isAICCAdmin(c) {
		if groupID := aiccGroupID(data); groupID != "" {
			err = model.RecordAICCAssetGroupOwnership(aiccUserID(c), groupID, "AIGC")
			if err != nil {
				// A duplicate upstream ID may already belong to another tenant; never delete it on a local write failure.
				common.SysError(fmt.Sprintf("AICC ownership reconciliation required: user=%d group=%s", aiccUserID(c), groupID))
			}
		} else {
			err = fmt.Errorf("AICC API response missing groupId")
		}
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	common.ApiSuccess(c, data)
}

// CreateAICCAsset 创建/上传素材
func CreateAICCAsset(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	var req struct {
		GroupId   string `json:"groupId" binding:"required"`
		AssetName string `json:"assetName" binding:"required"`
		AssetUrl  string `json:"assetUrl" binding:"required"`
		AssetType string `json:"assetType" binding:"required"` // Image / Video / Audio
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误: " + err.Error(),
		})
		return
	}

	if !requireAICCGroupOwner(c, req.GroupId) {
		return
	}
	if err := service.ValidateAICCUploadURL(req.AssetUrl, aiccUserID(c), req.GroupId, req.AssetType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "上传文件已失效或与当前账号、素材组、类型不匹配，请重新上传"})
		return
	}
	data, err := service.CreateAICCAsset(c.Request.Context(), req.GroupId, req.AssetName, req.AssetUrl, req.AssetType)
	if err == nil {
		assetID := aiccAssetID(data)
		if assetID == "" {
			err = fmt.Errorf("AICC API response missing assetId")
		} else if !isAICCAdmin(c) {
			err = model.RecordAICCAssetOwnership(aiccUserID(c), assetID, req.GroupId)
			if err != nil {
				common.SysError(fmt.Sprintf("AICC ownership reconciliation required: user=%d asset=%s group=%s", aiccUserID(c), assetID, req.GroupId))
			}
		}
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	common.ApiSuccess(c, data)
}

// QueryAICCAssets 查询素材列表
func QueryAICCAssets(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	pageNo, _ := strconv.Atoi(c.DefaultQuery("pageNo", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	groupType := c.DefaultQuery("groupType", "AIGC")
	assetName := c.DefaultQuery("assetName", "")
	groupIds := aiccQueryValues(c, "groupIds")
	var ownedGroupIDs map[string]struct{}
	if !isAICCAdmin(c) {
		var ownershipErr error
		ownedGroupIDs, ownershipErr = aiccOwnedGroupIDs(aiccUserID(c))
		if ownershipErr != nil {
			common.ApiError(c, ownershipErr)
			return
		}
		groupIds = aiccRestrictIDs(groupIds, ownedGroupIDs)
		if len(groupIds) == 0 {
			common.ApiSuccess(c, aiccEmptyPage(pageNo, pageSize))
			return
		}
	}

	data, err := service.QueryAICCAssets(c.Request.Context(), pageNo, pageSize, groupType, groupIds, assetName, aiccQueryValues(c, "statuses")...)
	if err == nil && !isAICCAdmin(c) {
		// groupIds 是租户边界；资产 ID 归属表仍用于详情、更新和删除鉴权。
		// 列表不能再按 asset ownership 二次收窄，否则远端组内的历史资产会被错误隐藏。
		data = aiccFilterPage(data, ownedGroupIDs, "groupId")
		if data["state"] != "OK" {
			err = fmt.Errorf("移动云素材列表响应格式异常，请稍后重试")
		}
		body := data["body"].(map[string]any)
		for _, item := range body["data"].([]any) {
			asset := item.(map[string]any)
			assetID := aiccStringField(asset, "assetId")
			groupID := aiccStringField(asset, "groupId")
			if assetID == "" {
				err = fmt.Errorf("invalid AICC asset list response")
				break
			}
			if err = model.RecordAICCAssetOwnership(aiccUserID(c), assetID, groupID); err != nil {
				break
			}
		}
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	common.ApiSuccess(c, data)
}

// GetAICCAsset 查询单个素材详情（获取 12 小时有效预览链接）
func GetAICCAsset(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	assetId := strings.TrimSpace(c.Param("id"))
	if assetId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "asset id is required"})
		return
	}
	if !requireAICCAssetOwner(c, assetId) {
		return
	}

	data, err := service.GetAICCAsset(c.Request.Context(), assetId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	common.ApiSuccess(c, data)
}

func UpdateAICCAsset(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	var req struct {
		AssetName *string `json:"assetName" binding:"omitempty,max=64"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid asset update parameters")
		return
	}
	assetID := strings.TrimSpace(c.Param("id"))
	if assetID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "asset id is required"})
		return
	}
	if !requireAICCAssetOwner(c, assetID) {
		return
	}
	data, err := service.UpdateAICCAsset(c.Request.Context(), assetID, req.AssetName)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}

// DeleteAICCAsset 删除素材
func DeleteAICCAsset(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	assetId := strings.TrimSpace(c.Param("id"))
	if assetId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "asset id is required"})
		return
	}
	if !requireAICCAssetOwner(c, assetId) {
		return
	}

	data, err := service.DeleteAICCAsset(c.Request.Context(), assetId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if err := model.DeleteAICCAssetOwnership(assetId); err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, data)
}

// GetAICCAssetGroup 查询单个素材组详情
func GetAICCAssetGroup(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	groupId := strings.TrimSpace(c.Param("id"))
	if groupId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "group id is required"})
		return
	}
	if !requireAICCGroupOwner(c, groupId) {
		return
	}

	data, err := service.GetAICCAssetGroup(c.Request.Context(), groupId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	common.ApiSuccess(c, data)
}

// UpdateAICCAssetGroup 更新素材组
func UpdateAICCAssetGroup(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	groupId := strings.TrimSpace(c.Param("id"))
	if groupId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "group id is required"})
		return
	}
	if !requireAICCGroupOwner(c, groupId) {
		return
	}
	var req struct {
		GroupName   *string `json:"groupName" binding:"omitempty,max=64"`
		Description *string `json:"description" binding:"omitempty,max=300"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误: " + err.Error(),
		})
		return
	}

	data, err := service.UpdateAICCAssetGroupFields(c.Request.Context(), groupId, req.GroupName, req.Description)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	common.ApiSuccess(c, data)
}

// DeleteAICCAssetGroup 删除素材组
func DeleteAICCAssetGroup(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	groupId := strings.TrimSpace(c.Param("id"))
	if groupId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "group id is required"})
		return
	}
	if !requireAICCGroupOwner(c, groupId) {
		return
	}

	data, err := service.DeleteAICCAssetGroup(c.Request.Context(), groupId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if err := model.DeleteAICCAssetGroupOwnership(groupId); err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, data)
}
