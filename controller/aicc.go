package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func aiccUserID(c *gin.Context) int { return c.GetInt("id") }

// Management scope is set only by the dedicated authenticated management route.
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

func aiccError(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{"success": false, "message": err.Error()})
}
func checkAICCAuth(c *gin.Context) bool {
	if aiccUserID(c) <= 0 {
		aiccError(c, http.StatusUnauthorized, fmt.Errorf("请先登录或提供有效的 API 令牌"))
		return false
	}
	return true
}

// Distinguish absence from invalid input, including repeated query/header values.
func aiccChannelIDFromRequest(c *gin.Context) (int, bool, error) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return 0, false, fmt.Errorf("invalid channel query")
	}
	values := append([]string{}, query["channel_id"]...)
	for name, headers := range c.Request.Header {
		if strings.EqualFold(name, "X-AICC-Channel-ID") {
			values = append(values, headers...)
		}
	}
	var selected int
	for _, raw := range values {
		if raw == "" {
			return 0, true, fmt.Errorf("channel_id must be a positive integer")
		}
		for _, ch := range raw {
			if ch < '0' || ch > '9' {
				return 0, true, fmt.Errorf("channel_id must be a positive integer")
			}
		}
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			return 0, true, fmt.Errorf("channel_id must be a positive integer")
		}
		if selected != 0 && selected != id {
			return 0, true, fmt.Errorf("conflicting channel_id values")
		}
		selected = id
	}
	return selected, len(values) > 0, nil
}

func aiccBinding(cfg service.AICCConfig) model.AICCBinding {
	return model.AICCBinding{ChannelID: cfg.ChannelID, AICCAccountID: cfg.AICCAccountID}
}

func aiccRequestConfig(c *gin.Context, bodyChannel *int) (service.AICCConfig, bool) {
	id, present, err := aiccChannelIDFromRequest(c)
	if err == nil && bodyChannel != nil {
		if *bodyChannel <= 0 || (present && id != *bodyChannel) {
			err = fmt.Errorf("invalid or conflicting channelId")
		} else {
			id, present = *bodyChannel, true
		}
	}
	if err == nil && isAICCAdmin(c) && !present {
		err = fmt.Errorf("管理请求必须显式指定 channel_id")
	}
	if err != nil {
		aiccError(c, 400, err)
		return service.AICCConfig{}, false
	}
	var cfg service.AICCConfig
	if present {
		cfg, err = service.GetAICCConfigForChannel(id)
	} else {
		cfg, err = service.ResolveDefaultAICCConfig()
	}
	if err != nil {
		aiccError(c, 400, err)
		return cfg, false
	}
	return cfg, true
}

func aiccConfigForBinding(c *gin.Context, binding model.AICCBinding) (service.AICCConfig, bool) {
	id, present, err := aiccChannelIDFromRequest(c)
	if err != nil {
		aiccError(c, 400, err)
		return service.AICCConfig{}, false
	}
	if isAICCAdmin(c) && !present {
		aiccError(c, 400, fmt.Errorf("管理请求必须显式指定 channel_id"))
		return service.AICCConfig{}, false
	}
	if !binding.Valid() {
		aiccError(c, 409, fmt.Errorf("素材绑定无效，请重新认证或联系管理员"))
		return service.AICCConfig{}, false
	}
	if present && id != binding.ChannelID {
		aiccError(c, 409, fmt.Errorf("请求渠道与素材绑定不一致"))
		return service.AICCConfig{}, false
	}
	cfg, err := service.GetAICCConfigForChannel(binding.ChannelID)
	if err != nil {
		aiccError(c, 409, err)
		return cfg, false
	}
	if cfg.ChannelID != binding.ChannelID || cfg.AICCAccountID != binding.AICCAccountID {
		aiccError(c, 409, fmt.Errorf("渠道配置身份已变更，请重新认证或核实归属"))
		return cfg, false
	}
	return cfg, true
}

// A missing local row is allowed only in explicit-channel management scope.
// Invalid legacy bindings and database errors must never become this fallback.
func aiccResourceConfig(c *gin.Context, id string, group bool) (service.AICCConfig, bool) {
	if _, present, err := aiccChannelIDFromRequest(c); err != nil {
		aiccError(c, 400, err)
		return service.AICCConfig{}, false
	} else if isAICCAdmin(c) && !present {
		aiccError(c, 400, fmt.Errorf("管理请求必须显式指定 channel_id"))
		return service.AICCConfig{}, false
	}
	if validAICCResourceID(id) == "" {
		aiccError(c, 400, fmt.Errorf("invalid resource id"))
		return service.AICCConfig{}, false
	}
	user := aiccUserID(c)
	if isAICCAdmin(c) {
		user = 0
	}
	var binding model.AICCBinding
	var err error
	if group {
		binding, err = model.GetOwnedAICCGroupBinding(user, id)
	} else {
		binding, err = model.GetOwnedAICCAssetBinding(user, id)
	}
	if err != nil {
		if isAICCAdmin(c) && errors.Is(err, gorm.ErrRecordNotFound) {
			return aiccRequestConfig(c, nil)
		}
		aiccError(c, 403, fmt.Errorf("无权访问该素材或素材组: %w", err))
		return service.AICCConfig{}, false
	}
	return aiccConfigForBinding(c, binding)
}

// Also used by the local upload controller; no remote request is made here.
func requireAICCGroupOwner(c *gin.Context, groupID string) bool {
	_, ok := aiccResourceConfig(c, groupID, true)
	return ok
}
func requireAICCAssetOwner(c *gin.Context, assetID string) bool {
	_, ok := aiccResourceConfig(c, assetID, false)
	return ok
}
func aiccBody(data map[string]any) any { return data["body"] }
func aiccGroupID(data map[string]any) string {
	if value, ok := data["body"].(string); ok {
		return validAICCResourceID(value)
	}
	if object, ok := data["body"].(map[string]any); ok {
		return aiccStringField(object, "groupId")
	}
	return ""
}
func aiccAssetID(data map[string]any) string {
	if value, ok := data["body"].(string); ok {
		return validAICCResourceID(value)
	}
	if object, ok := data["body"].(map[string]any); ok {
		return aiccStringField(object, "assetId")
	}
	return ""
}
func validAICCResourceID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "/\\?#\x00") {
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

// Only the explicit top-level SUCCESS status confirms authentication. Unknown,
// missing or nested statuses do not establish ownership.
func aiccAuthStatus(value any) (hasStatus, terminalSuccess, terminalFailure bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return false, false, false
	}
	raw, exists := object["status"]
	if !exists {
		return false, false, false
	}
	status, ok := raw.(string)
	success := ok && strings.EqualFold(strings.TrimSpace(status), "SUCCESS")
	return true, success, !success
}
func aiccOwnedGroupIDs(userID int, binding model.AICCBinding) (map[string]struct{}, error) {
	return model.UserOwnedAICCGroupIDsForBinding(userID, binding)
}
func aiccRestrictIDs(requested []string, allowed map[string]struct{}) []string {
	result := make([]string, 0)
	if len(requested) == 0 {
		for id := range allowed {
			result = append(result, id)
		}
		return result
	}
	for _, id := range requested {
		if _, ok := allowed[id]; ok {
			result = append(result, id)
		}
	}
	return result
}
func aiccEmptyPage(pageNo, pageSize int) map[string]any {
	return map[string]any{"state": "OK", "body": map[string]any{"data": []any{}, "total": 0, "pageNo": pageNo, "pageSize": pageSize}}
}
func aiccFilterPage(data map[string]any, allowed map[string]struct{}, key string) map[string]any {
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
	output["data"], output["total"] = filtered, len(filtered)
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

// Identity is the local configuration snapshot, never an upstream account claim.
func aiccAnnotate(value any, binding model.AICCBinding) {
	switch object := value.(type) {
	case map[string]any:
		if aiccStringField(object, "groupId", "assetId") != "" {
			object["channelId"], object["aiccAccountId"] = binding.ChannelID, binding.AICCAccountID
		}
		for _, child := range object {
			aiccAnnotate(child, binding)
		}
	case []any:
		for _, child := range object {
			aiccAnnotate(child, binding)
		}
	}
}
func aiccRespond(c *gin.Context, data map[string]any, cfg service.AICCConfig, err error) {
	if err != nil {
		aiccError(c, 409, err)
		return
	}
	aiccAnnotate(data, aiccBinding(cfg))
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

func ListAICCChannels(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	if _, _, err := aiccChannelIDFromRequest(c); err != nil {
		aiccError(c, 400, err)
		return
	}
	channels, err := service.ListAvailableAICCChannels()
	if err != nil {
		aiccError(c, 409, err)
		return
	}
	common.ApiSuccess(c, channels)
}
func CreateAICCH5Session(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	cfg, ok := aiccRequestConfig(c, nil)
	if !ok {
		return
	}
	session, err := service.CreateAICCH5Session(service.WithAICCConfig(c.Request.Context(), cfg), cfg.ChannelID)
	if err != nil {
		aiccError(c, 409, err)
		return
	}
	if err = model.RecordBoundAICCAuthSession(aiccUserID(c), session.BytedToken, session.ExpiresIn, aiccBinding(cfg)); err != nil {
		aiccError(c, 409, err)
		return
	}
	common.ApiSuccess(c, gin.H{"bytedToken": session.BytedToken, "h5Link": session.H5Link, "expiresIn": session.ExpiresIn, "channelId": cfg.ChannelID, "aiccAccountId": cfg.AICCAccountID})
}
func QueryAICCGroupByBytedToken(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	if _, _, err := aiccChannelIDFromRequest(c); err != nil {
		aiccError(c, 400, err)
		return
	}
	token := strings.TrimSpace(c.Param("token"))
	if c.Request.Method == http.MethodPost {
		var req struct {
			BytedToken string `json:"bytedToken" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			aiccError(c, 400, fmt.Errorf("bytedToken is required"))
			return
		}
		token = strings.TrimSpace(req.BytedToken)
	}
	if token == "" {
		token = strings.TrimSpace(c.Query("token"))
	}
	if token == "" {
		aiccError(c, 400, fmt.Errorf("token is required"))
		return
	}
	binding, err := model.GetAICCAuthSessionBinding(aiccUserID(c), token)
	if err != nil {
		aiccError(c, 403, err)
		return
	}
	cfg, ok := aiccConfigForBinding(c, binding)
	if !ok {
		return
	}
	data, err := service.QueryAICCGroupByBytedToken(service.WithAICCConfig(c.Request.Context(), cfg), token, cfg.ChannelID)
	if err != nil {
		aiccError(c, 409, err)
		return
	}
	body, _ := data["body"].(map[string]any)
	groupID := aiccGroupID(data)
	if aiccStringField(body, "assetId", "assetID") != "" {
		groupID = ""
	}
	hasStatus, success, failure := aiccAuthStatus(body)
	authenticated := groupID != "" && hasStatus && success && !failure
	// Some upstream versions return only the token-bound group ID. Verify its
	// real-person group details through the same pinned credentials before granting ownership.
	if groupID != "" && !hasStatus {
		details, detailErr := service.GetAICCAssetGroup(service.WithAICCConfig(c.Request.Context(), cfg), groupID, cfg.ChannelID)
		if detailErr != nil {
			aiccError(c, http.StatusBadGateway, fmt.Errorf("认证组详情暂无法核实，请稍后重试"))
			return
		}
		group, _ := details["body"].(map[string]any)
		_, _, rejected := aiccAuthStatus(group)
		authenticated = aiccGroupID(details) == groupID && group["groupType"] == "LivenessFace" && !rejected
	}
	if authenticated && !isAICCAdmin(c) {
		assets := make(map[string]string)
		if list, ok := body["assetList"].([]any); ok {
			for _, item := range list {
				asset, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if explicit := aiccStringField(asset, "groupId"); explicit != "" && explicit != groupID {
					continue
				}
				if id := aiccStringField(asset, "assetId"); id != "" {
					assets[id] = groupID
				}
			}
		}
		if err = model.RecordBoundAICCAuthResources(aiccUserID(c), groupID, assets, binding); err != nil {
			aiccError(c, 409, err)
			return
		}
	}
	data["authenticated"] = authenticated
	aiccRespond(c, data, cfg, nil)
}

func ListAICCAssetGroups(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	cfg, ok := aiccRequestConfig(c, nil)
	if !ok {
		return
	}
	pageNo, _ := strconv.Atoi(c.DefaultQuery("pageNo", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	ids := aiccQueryValues(c, "groupIds")
	var owned map[string]struct{}
	var err error
	if !isAICCAdmin(c) {
		owned, err = aiccOwnedGroupIDs(aiccUserID(c), aiccBinding(cfg))
		if err != nil {
			aiccError(c, 409, err)
			return
		}
		ids = aiccRestrictIDs(ids, owned)
		if len(ids) == 0 {
			common.ApiSuccess(c, aiccEmptyPage(pageNo, pageSize))
			return
		}
	}
	data, err := service.ListAICCAssetGroups(service.WithAICCConfig(c.Request.Context(), cfg), pageNo, pageSize, c.Query("groupType"), service.AICCGroupFilters{GroupName: c.Query("groupName"), GroupIDs: ids, ChannelID: cfg.ChannelID})
	if err == nil && !isAICCAdmin(c) {
		data = aiccFilterPage(data, owned, "groupId")
		if data["state"] != "OK" {
			err = fmt.Errorf("invalid AICC group list response")
		}
	}
	aiccRespond(c, data, cfg, err)
}
func CreateAICCAssetGroup(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	var req struct {
		GroupName   string `json:"groupName" binding:"required"`
		Description string `json:"description"`
		ChannelID   *int   `json:"channelId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		aiccError(c, 400, err)
		return
	}
	cfg, ok := aiccRequestConfig(c, req.ChannelID)
	if !ok {
		return
	}
	data, err := service.CreateAICCAssetGroup(service.WithAICCConfig(c.Request.Context(), cfg), req.GroupName, req.Description, cfg.ChannelID)
	if err == nil && !isAICCAdmin(c) {
		if id := aiccGroupID(data); id != "" {
			err = model.RecordBoundAICCAssetGroupOwnership(aiccUserID(c), id, "AIGC", aiccBinding(cfg))
		} else {
			err = fmt.Errorf("AICC API response missing groupId")
		}
	}
	aiccRespond(c, data, cfg, err)
}
func CreateAICCAsset(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	var req struct {
		GroupID   string `json:"groupId" binding:"required"`
		AssetName string `json:"assetName" binding:"required"`
		AssetURL  string `json:"assetUrl" binding:"required"`
		AssetType string `json:"assetType" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		aiccError(c, 400, err)
		return
	}
	cfg, ok := aiccResourceConfig(c, req.GroupID, true)
	if !ok {
		return
	}
	if err := service.ValidateAICCUploadURL(req.AssetURL, aiccUserID(c), req.GroupID, req.AssetType, aiccBinding(cfg)); err != nil {
		aiccError(c, 400, fmt.Errorf("上传文件已失效或与当前账号、素材组、类型不匹配，请重新上传"))
		return
	}
	data, err := service.CreateAICCAsset(service.WithAICCConfig(c.Request.Context(), cfg), req.GroupID, req.AssetName, req.AssetURL, req.AssetType, cfg.ChannelID)
	if err == nil {
		if id := aiccAssetID(data); id == "" {
			err = fmt.Errorf("AICC API response missing assetId")
		} else if !isAICCAdmin(c) {
			err = model.RecordBoundAICCAssetOwnership(aiccUserID(c), id, req.GroupID, aiccBinding(cfg))
		}
	}
	aiccRespond(c, data, cfg, err)
}
func QueryAICCAssets(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	cfg, ok := aiccRequestConfig(c, nil)
	if !ok {
		return
	}
	pageNo, _ := strconv.Atoi(c.DefaultQuery("pageNo", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	ids := aiccQueryValues(c, "groupIds")
	var owned map[string]struct{}
	var err error
	if !isAICCAdmin(c) {
		owned, err = aiccOwnedGroupIDs(aiccUserID(c), aiccBinding(cfg))
		if err != nil {
			aiccError(c, 409, err)
			return
		}
		ids = aiccRestrictIDs(ids, owned)
		if len(ids) == 0 {
			common.ApiSuccess(c, aiccEmptyPage(pageNo, pageSize))
			return
		}
	}
	data, err := service.QueryAICCAssetsWithChannel(service.WithAICCConfig(c.Request.Context(), cfg), pageNo, pageSize, c.DefaultQuery("groupType", "AIGC"), ids, c.Query("assetName"), cfg.ChannelID, aiccQueryValues(c, "statuses")...)
	if err == nil && !isAICCAdmin(c) {
		data = aiccFilterPage(data, owned, "groupId")
		if data["state"] != "OK" {
			err = fmt.Errorf("invalid AICC asset list response")
		} else {
			for _, item := range data["body"].(map[string]any)["data"].([]any) {
				asset := item.(map[string]any)
				id, group := aiccStringField(asset, "assetId"), aiccStringField(asset, "groupId")
				if id == "" {
					err = fmt.Errorf("invalid AICC asset list response")
					break
				}
				if err = model.RecordBoundAICCAssetOwnership(aiccUserID(c), id, group, aiccBinding(cfg)); err != nil {
					break
				}
			}
		}
	}
	aiccRespond(c, data, cfg, err)
}

func GetAICCAsset(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	cfg, ok := aiccResourceConfig(c, id, false)
	if !ok {
		return
	}
	data, err := service.GetAICCAsset(service.WithAICCConfig(c.Request.Context(), cfg), id, cfg.ChannelID)
	aiccRespond(c, data, cfg, err)
}
func UpdateAICCAsset(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	cfg, ok := aiccResourceConfig(c, id, false)
	if !ok {
		return
	}
	var req struct {
		AssetName *string `json:"assetName" binding:"omitempty,max=64"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		aiccError(c, 400, err)
		return
	}
	data, err := service.UpdateAICCAsset(service.WithAICCConfig(c.Request.Context(), cfg), id, req.AssetName, cfg.ChannelID)
	aiccRespond(c, data, cfg, err)
}
func DeleteAICCAsset(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	cfg, ok := aiccResourceConfig(c, id, false)
	if !ok {
		return
	}
	data, err := service.DeleteAICCAsset(service.WithAICCConfig(c.Request.Context(), cfg), id, cfg.ChannelID)
	if err == nil {
		err = model.DeleteBoundAICCAssetOwnership(id, aiccBinding(cfg))
	}
	aiccRespond(c, data, cfg, err)
}
func GetAICCAssetGroup(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	cfg, ok := aiccResourceConfig(c, id, true)
	if !ok {
		return
	}
	data, err := service.GetAICCAssetGroup(service.WithAICCConfig(c.Request.Context(), cfg), id, cfg.ChannelID)
	aiccRespond(c, data, cfg, err)
}
func UpdateAICCAssetGroup(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	cfg, ok := aiccResourceConfig(c, id, true)
	if !ok {
		return
	}
	var req struct {
		GroupName   *string `json:"groupName" binding:"omitempty,max=64"`
		Description *string `json:"description" binding:"omitempty,max=300"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		aiccError(c, 400, err)
		return
	}
	data, err := service.UpdateAICCAssetGroupFields(service.WithAICCConfig(c.Request.Context(), cfg), id, req.GroupName, req.Description, cfg.ChannelID)
	aiccRespond(c, data, cfg, err)
}
func DeleteAICCAssetGroup(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	cfg, ok := aiccResourceConfig(c, id, true)
	if !ok {
		return
	}
	data, err := service.DeleteAICCAssetGroup(service.WithAICCConfig(c.Request.Context(), cfg), id, cfg.ChannelID)
	if err == nil {
		err = model.DeleteBoundAICCAssetGroupOwnership(id, aiccBinding(cfg))
	}
	aiccRespond(c, data, cfg, err)
}
