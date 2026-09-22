package service

import (
	"errors"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
)

var (
	ErrAICCAuthenticationRequired = errors.New("请先登录或提供有效的 API 令牌")
	ErrAICCChannelForbidden       = errors.New("无权使用该 AICC 渠道的模型")
	ErrAICCAccessUnavailable      = errors.New("AICC 渠道权限暂无法核实")
)

// AICCChannelModels reads current abilities and channel status, never the routing
// cache. management is accepted only for an explicitly scoped dashboard admin.
func AICCChannelModels(c *gin.Context, channelID int, management bool) (map[int][]string, error) {
	if c.GetInt("id") <= 0 {
		return nil, ErrAICCAuthenticationRequired
	}
	if model.DB == nil {
		return nil, ErrAICCAccessUnavailable
	}
	admin := management && c.GetBool("aicc_management_scope") && c.GetInt("role") >= common.RoleAdminUser && c.GetInt("token_id") == 0
	query := model.DB.WithContext(c.Request.Context()).Model(&model.Ability{}).
		Select("abilities.channel_id, abilities.model").
		Joins("JOIN channels ON channels.id = abilities.channel_id").
		Where("abilities.enabled = ? AND channels.status = ?", true, common.ChannelStatusEnabled)
	if channelID > 0 {
		query = query.Where("abilities.channel_id = ?", channelID)
	}
	limited := false
	var limits map[string]bool
	if !admin {
		user, err := model.GetUserCache(c.GetInt("id"))
		if err != nil || user == nil {
			return nil, ErrAICCAccessUnavailable
		}
		if user.Status != common.UserStatusEnabled || user.Group == "" {
			return nil, ErrAICCChannelForbidden
		}
		groups := []string{user.Group}
		if c.GetInt("token_id") != 0 {
			group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
			if group == "" {
				return nil, ErrAICCChannelForbidden
			}
			if group == "auto" {
				groups = GetRequestAutoGroups(c, user.Group)
			} else {
				if group != user.Group && !IsUserSelectableGroup(user.Group, group) {
					return nil, ErrAICCChannelForbidden
				}
				groups = []string{group}
			}
			limited = c.GetBool("token_model_limit_enabled")
			if limited {
				value, _ := c.Get("token_model_limit")
				limits, _ = value.(map[string]bool)
			}
		}
		if len(groups) == 0 {
			return nil, ErrAICCChannelForbidden
		}
		values := make([]interface{}, len(groups))
		for i, group := range groups {
			values[i] = group
		}
		query = query.Where(clause.IN{Column: clause.Column{Table: "abilities", Name: "group"}, Values: values})
	}
	var abilities []model.Ability
	if err := query.Find(&abilities).Error; err != nil {
		return nil, ErrAICCAccessUnavailable
	}
	sets := make(map[int]map[string]bool)
	for _, ability := range abilities {
		name := ability.Model
		if name == "" || (limited && !limits[name] && !limits[ratio_setting.RoutingMatchModelName(name)]) {
			continue
		}
		if sets[ability.ChannelId] == nil {
			sets[ability.ChannelId] = make(map[string]bool)
		}
		sets[ability.ChannelId][name] = true
	}
	result := make(map[int][]string, len(sets))
	for id, names := range sets {
		for name := range names {
			result[id] = append(result[id], name)
		}
		sort.Strings(result[id])
	}
	if len(result) == 0 {
		return nil, ErrAICCChannelForbidden
	}
	return result, nil
}

func ListAccessibleAICCChannels(c *gin.Context, management bool) ([]map[string]any, error) {
	models, err := AICCChannelModels(c, 0, management)
	if err != nil {
		return nil, err
	}
	channels, err := ListAvailableAICCChannels()
	if err != nil {
		return nil, ErrAICCAccessUnavailable
	}
	result := make([]map[string]any, 0, len(channels))
	for _, channel := range channels {
		allowed := models[channel["id"].(int)]
		if len(allowed) == 0 {
			continue
		}
		channel["models"] = allowed
		result = append(result, channel)
	}
	if len(result) == 0 {
		return nil, ErrAICCChannelForbidden
	}
	return result, nil
}
