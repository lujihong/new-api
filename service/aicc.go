package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	DefaultAICCEndpoint = "https://ecloud.10086.cn"
	DefaultAICCPoolID   = "CIDC-CORE-00"
)

type AICCConfig struct {
	AccessKeyID     string
	AccessKeySecret string
	Endpoint        string
	PoolID          string
	ChannelID       int
	ChannelName     string
}

type AICCH5SessionResponse struct {
	BytedToken string `json:"bytedToken"`
	H5Link     string `json:"h5Link"`
	ExpiresIn  int    `json:"expiresIn"`
}

func validateAICCResourceID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "/?#") {
		return "", errors.New("invalid AICC resource id")
	}
	return value, nil
}

func GetAICCConfig(channelID ...int) AICCConfig {
	cID := 0
	if len(channelID) > 0 {
		cID = channelID[0]
	}
	cfg, _ := GetAICCConfigForChannel(cID)
	return cfg
}

func GetAICCConfigForChannel(channelID int) (AICCConfig, error) {
	ak := strings.TrimSpace(os.Getenv("AICC_ACCESS_KEY_ID"))
	sk := strings.TrimSpace(os.Getenv("AICC_ACCESS_KEY_SECRET"))
	endpoint := strings.TrimSpace(os.Getenv("AICC_ENDPOINT"))
	if endpoint == "" {
		endpoint = DefaultAICCEndpoint
	}

	if model.DB != nil {
		var ch model.Channel
		var err error
		if channelID > 0 {
			err = model.DB.Where("id = ? AND status = 1", channelID).First(&ch).Error
		} else {
			err = model.DB.Where("status = 1 AND (type = 54 OR other_info LIKE '%access_key_id%' OR name LIKE '%移动%' OR name LIKE '%AICC%')").
				Order("priority DESC, id ASC").
				First(&ch).Error
		}
		if err == nil && ch.OtherInfo != "" {
			cAK := gjson.Get(ch.OtherInfo, "access_key_id").String()
			cSK := gjson.Get(ch.OtherInfo, "access_key_secret").String()
			cEP := gjson.Get(ch.OtherInfo, "endpoint").String()
			cPool := gjson.Get(ch.OtherInfo, "pool_id").String()
			if cEP == "" {
				cEP = endpoint
			}
			if cPool == "" {
				cPool = DefaultAICCPoolID
			}
			if cAK != "" && cSK != "" {
				return AICCConfig{
					AccessKeyID:     cAK,
					AccessKeySecret: cSK,
					Endpoint:        cEP,
					PoolID:          cPool,
					ChannelID:       int(ch.Id),
					ChannelName:     ch.Name,
				}, nil
			}
		}
	}

	if ak == "" || sk == "" {
		return AICCConfig{AccessKeyID: ak, AccessKeySecret: sk, Endpoint: endpoint, PoolID: DefaultAICCPoolID}, errors.New("移动云 AICC 账号未配置")
	}

	return AICCConfig{
		AccessKeyID:     ak,
		AccessKeySecret: sk,
		Endpoint:        endpoint,
		PoolID:          DefaultAICCPoolID,
	}, nil
}

// ListAvailableAICCChannels 列出系统当前所有已启用并支持 AICC 资产的移动云专线渠道
func ListAvailableAICCChannels() ([]map[string]any, error) {
	if model.DB == nil {
		return []map[string]any{}, nil
	}
	var channels []model.Channel
	err := model.DB.Where("status = 1 AND (type = 54 OR other_info LIKE '%access_key_id%' OR name LIKE '%移动%')").
		Order("priority DESC, id ASC").
		Find(&channels).Error
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(channels))
		for _, ch := range channels {
			region := "官方移动专线"
			base := ""
			if ch.BaseURL != nil {
				base = *ch.BaseURL
			}
			if strings.Contains(ch.Name, "呼和浩特") || strings.Contains(base, "huhehaote") {
				region = "呼和浩特节点"
			} else if strings.Contains(ch.Name, "内蒙") {
				region = "内蒙节点"
			} else if strings.Contains(ch.Name, "北京") {
				region = "北京节点"
			}
		var modelsList []string
		if strings.TrimSpace(ch.Models) != "" {
			modelsList = strings.Split(ch.Models, ",")
		}
		result = append(result, map[string]any{
			"id":     ch.Id,
			"name":   ch.Name,
			"region": region,
			"models": modelsList,
		})
	}
	return result, nil
}

// ValidateAICCVideoChannel prevents account-scoped assets from falling back to an unrelated provider.
func ValidateAICCVideoChannel(channelID int) error {
	return ValidateAICCVideoAssetChannel(channelID, "")
}

// ValidateAICCVideoAssetChannel validates that the target video channel matches the asset's registered mobile channel.
func ValidateAICCVideoAssetChannel(channelID int, assetID string) error {
	if channelID <= 0 || model.DB == nil {
		return errors.New("AICC asset channel is unavailable")
	}
	ch, err := model.GetChannelById(channelID, true)
	if err != nil || ch == nil || ch.Status != common.ChannelStatusEnabled {
		return errors.New("AICC asset channel is unavailable")
	}

	ak := gjson.Get(ch.OtherInfo, "access_key_id").String()
	sk := gjson.Get(ch.OtherInfo, "access_key_secret").String()

	if strings.TrimSpace(assetID) != "" {
		assetChannelID, err := model.GetAICCAssetChannelID(assetID)
		if err == nil && assetChannelID > 0 && assetChannelID != channelID {
			return fmt.Errorf("真人素材所属移动云渠道(ID: %d)与当前视频任务派发渠道(ID: %d)不匹配，移动云真人肖像为强租户绑定资产，无法跨渠道跨账号使用，请选用该素材对应的视频渠道", assetChannelID, channelID)
		}
		if err == nil && assetChannelID == 0 {
			defaultCfg := GetAICCConfig()
			if defaultCfg.AccessKeyID != "" && (ak != defaultCfg.AccessKeyID || sk != defaultCfg.AccessKeySecret) {
				return errors.New("人物素材仅可使用其所属移动云账号渠道，请选择移动云渠道后重试")
			}
		}
	}

	targetCfg, err := GetAICCConfigForChannel(channelID)
	if err != nil || targetCfg.AccessKeyID == "" || targetCfg.AccessKeySecret == "" {
		return errors.New("该视频渠道未配置有效的移动云 AICC 密钥凭据，请选择已配置的移动云专线渠道")
	}

	if ak != targetCfg.AccessKeyID || sk != targetCfg.AccessKeySecret {
		return errors.New("人物素材仅可使用其所属移动云账号渠道，请选择对应移动云渠道后重试")
	}
	return nil
}

// PercentEncode 严格遵循移动云 RFC 3986 URL 编码规范
func percentEncode(s string) string {
	res := url.QueryEscape(s)
	res = strings.ReplaceAll(res, "+", "%20")
	res = strings.ReplaceAll(res, "*", "%2A")
	res = strings.ReplaceAll(res, "%7E", "~")
	return res
}

func SignAICCRequest(method, servletPath string, queryParams map[string]string, secretKey string) string {
	keys := make([]string, 0, len(queryParams))
	for k := range queryParams {
		if k == "Signature" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var canonicalizedQueryBuilder strings.Builder
	for _, k := range keys {
		canonicalizedQueryBuilder.WriteString("&")
		canonicalizedQueryBuilder.WriteString(percentEncode(k))
		canonicalizedQueryBuilder.WriteString("=")
		canonicalizedQueryBuilder.WriteString(percentEncode(queryParams[k]))
	}
	canonicalizedQuery := ""
	if canonicalizedQueryBuilder.Len() > 0 {
		canonicalizedQuery = canonicalizedQueryBuilder.String()[1:]
	}

	h := sha256.New()
	h.Write([]byte(canonicalizedQuery))
	querySha256 := hex.EncodeToString(h.Sum(nil))

	stringToSign := fmt.Sprintf("%s\n%s\n%s", method, percentEncode(servletPath), querySha256)
	macKey := []byte("BC_SIGNATURE&" + secretKey)

	mac := hmac.New(sha1.New, macKey)
	mac.Write([]byte(stringToSign))
	return hex.EncodeToString(mac.Sum(nil))
}

func aiccChannelIDFromSlice(channelID []int) int {
	if len(channelID) > 0 {
		return channelID[0]
	}
	return 0
}

func doAICCRequest(ctx context.Context, channelID int, method, path string, body any) ([]byte, error) {
	cfg, err := GetAICCConfigForChannel(channelID)
	if err != nil {
		return nil, err
	}
	if cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return nil, errors.New("AICC AccessKey ID or SecretKey is unconfigured")
	}

	// 移动云服务要求北京时间（CST）带 Z 格式
	cstZone := time.FixedZone("CST", 8*3600)
	timestamp := time.Now().In(cstZone).Format("2006-01-02T15:04:05Z")

	params := map[string]string{
		"AccessKey":        cfg.AccessKeyID,
		"Timestamp":        timestamp,
		"SignatureMethod":  "HmacSHA1",
		"SignatureNonce":   uuid.New().String(),
		"SignatureVersion": "V2.0",
		"Version":          "2016-12-05",
	}

	sig := SignAICCRequest(method, path, params, cfg.AccessKeySecret)
	params["Signature"] = sig

	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}

	reqURL := fmt.Sprintf("%s%s?%s", strings.TrimRight(cfg.Endpoint, "/"), path, q.Encode())

	var bodyReader io.Reader
	if body != nil {
		jsonBytes, err := common.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(jsonBytes)
	} else {
		bodyReader = bytes.NewReader([]byte("{}"))
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "LaunchAI-AICC-Client/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if !gjson.ValidBytes(respBytes) {
		return nil, fmt.Errorf("invalid AICC response: expected JSON (HTTP %d)", resp.StatusCode)
	}
	state := gjson.GetBytes(respBytes, "state").String()
	if state != "OK" {
		errMsg := gjson.GetBytes(respBytes, "errorMessage").String()
		errCode := gjson.GetBytes(respBytes, "errorCode").String()
		if errMsg == "" {
			errMsg = string(respBytes)
		}
		return respBytes, fmt.Errorf("AICC API error [%s]: %s", errCode, errMsg)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return respBytes, fmt.Errorf("AICC API HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}

// CreateAICCH5Session 创建真人实名认证会话，返回 H5 认证链接和 bytedToken
func CreateAICCH5Session(ctx context.Context, channelID ...int) (*AICCH5SessionResponse, error) {
	cID := aiccChannelIDFromSlice(channelID)
	path := "/api/openapi-maas/exp/aicc/v2/real-person-auth/sessions"
	respBytes, err := doAICCRequest(ctx, cID, http.MethodPost, path, map[string]any{})
	if err != nil {
		return nil, err
	}

	bodyNode := gjson.GetBytes(respBytes, "body")
	if !bodyNode.IsObject() || bodyNode.Get("bytedToken").String() == "" || bodyNode.Get("h5Link").String() == "" || bodyNode.Get("expiresIn").Int() <= 0 {
		return nil, fmt.Errorf("invalid AICC response: incomplete authentication session")
	}

	return &AICCH5SessionResponse{
		BytedToken: bodyNode.Get("bytedToken").String(),
		H5Link:     bodyNode.Get("h5Link").String(),
		ExpiresIn:  int(bodyNode.Get("expiresIn").Int()),
	}, nil
}

// QueryAICCGroupByBytedToken 通过 bytedToken 查询真人认证状态及素材组信息
func QueryAICCGroupByBytedToken(ctx context.Context, bytedToken string, channelID ...int) (map[string]any, error) {
	cID := aiccChannelIDFromSlice(channelID)
	path := "/api/openapi-maas/exp/aicc/v2/real-person-auth/asset-group/by-byted-token"
	respBytes, err := doAICCRequest(ctx, cID, http.MethodPost, path, map[string]any{
		"bytedToken": strings.TrimSpace(bytedToken),
	})
	if err != nil {
		return nil, err
	}

	var res map[string]any
	if err := common.Unmarshal(respBytes, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// GetAICCAsset 查询单个素材详情（包含 12 小时有效的临时 assetUrl 用于预览）
func GetAICCAsset(ctx context.Context, assetId string, channelID ...int) (map[string]any, error) {
	cID := aiccChannelIDFromSlice(channelID)
	var err error
	assetId, err = validateAICCResourceID(assetId)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/openapi-maas/exp/aicc/v2/asset/%s", assetId)
	respBytes, err := doAICCRequest(ctx, cID, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := common.Unmarshal(respBytes, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// UpdateAICCAsset 更新可用素材的名称；nil 表示保留原值。
func UpdateAICCAsset(ctx context.Context, assetID string, assetName *string, channelID ...int) (map[string]any, error) {
	cID := aiccChannelIDFromSlice(channelID)
	assetID = strings.TrimSpace(assetID)
	if assetID == "" || strings.ContainsAny(assetID, "/?#") {
		return nil, errors.New("invalid assetId")
	}
	payload := map[string]any{}
	if assetName != nil {
		payload["assetName"] = *assetName
	}
	resp, err := doAICCRequest(ctx, cID, http.MethodPut, "/api/openapi-maas/exp/aicc/v2/asset/"+assetID, payload)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := common.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// DeleteAICCAsset 删除素材
func DeleteAICCAsset(ctx context.Context, assetId string, channelID ...int) (map[string]any, error) {
	cID := aiccChannelIDFromSlice(channelID)
	var err error
	assetId, err = validateAICCResourceID(assetId)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/openapi-maas/exp/aicc/v2/asset/%s", assetId)
	respBytes, err := doAICCRequest(ctx, cID, http.MethodDelete, path, nil)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := common.Unmarshal(respBytes, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// GetAICCAssetGroup 查询单个素材组详情
func GetAICCAssetGroup(ctx context.Context, groupId string, channelID ...int) (map[string]any, error) {
	cID := aiccChannelIDFromSlice(channelID)
	var err error
	groupId, err = validateAICCResourceID(groupId)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/openapi-maas/exp/aicc/v2/asset-group/%s", groupId)
	respBytes, err := doAICCRequest(ctx, cID, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := common.Unmarshal(respBytes, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// UpdateAICCAssetGroup 更新素材组名称和描述
func UpdateAICCAssetGroup(ctx context.Context, groupId, groupName, description string, channelID ...int) (map[string]any, error) {
	return UpdateAICCAssetGroupFields(ctx, groupId, &groupName, &description, channelID...)
}

func UpdateAICCAssetGroupFields(ctx context.Context, groupId string, groupName, description *string, channelID ...int) (map[string]any, error) {
	cID := aiccChannelIDFromSlice(channelID)
	var err error
	groupId, err = validateAICCResourceID(groupId)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/openapi-maas/exp/aicc/v2/asset-group/%s", groupId)
	payload := map[string]any{}
	if groupName != nil {
		payload["groupName"] = strings.TrimSpace(*groupName)
	}
	if description != nil {
		payload["description"] = strings.TrimSpace(*description)
	}
	respBytes, err := doAICCRequest(ctx, cID, http.MethodPut, path, payload)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := common.Unmarshal(respBytes, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// DeleteAICCAssetGroup 删除素材组
func DeleteAICCAssetGroup(ctx context.Context, groupId string, channelID ...int) (map[string]any, error) {
	cID := aiccChannelIDFromSlice(channelID)
	var err error
	groupId, err = validateAICCResourceID(groupId)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/openapi-maas/exp/aicc/v2/asset-group/%s", groupId)
	respBytes, err := doAICCRequest(ctx, cID, http.MethodDelete, path, nil)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := common.Unmarshal(respBytes, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// ListAICCAssetGroups 查询素材组列表
type AICCGroupFilters struct {
	GroupName string
	GroupIDs  []string
	ChannelID int
}

func ListAICCAssetGroups(ctx context.Context, pageNo, pageSize int, groupType string, filters ...AICCGroupFilters) (map[string]any, error) {
	if pageNo <= 0 {
		pageNo = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	path := "/api/openapi-maas/exp/aicc/v2/asset-group/query"
	payload := map[string]any{
		"pageNo":   pageNo,
		"pageSize": pageSize,
	}
	if groupType != "" {
		payload["groupType"] = groupType
	}
	cID := 0
	if len(filters) > 0 {
		if filters[0].GroupName != "" {
			payload["groupName"] = filters[0].GroupName
		}
		if len(filters[0].GroupIDs) > 0 {
			payload["groupIds"] = filters[0].GroupIDs
		}
		cID = filters[0].ChannelID
	}

	respBytes, err := doAICCRequest(ctx, cID, http.MethodPost, path, payload)
	if err != nil {
		return nil, err
	}

	var res map[string]any
	if err := common.Unmarshal(respBytes, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// CreateAICCAssetGroup 创建 AIGC（虚拟人像）素材组
func CreateAICCAssetGroup(ctx context.Context, groupName, description string, channelID ...int) (map[string]any, error) {
	cID := aiccChannelIDFromSlice(channelID)
	path := "/api/openapi-maas/exp/aicc/v2/asset-group"
	payload := map[string]any{
		"groupType":   "AIGC",
		"groupName":   groupName,
		"description": description,
	}
	respBytes, err := doAICCRequest(ctx, cID, http.MethodPost, path, payload)
	if err != nil {
		return nil, err
	}

	var res map[string]any
	if err := common.Unmarshal(respBytes, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// CreateAICCAsset 向素材组添加素材（图片/视频 URL）
func CreateAICCAsset(ctx context.Context, groupId, assetName, assetUrl, assetType string, channelID ...int) (map[string]any, error) {
	cID := aiccChannelIDFromSlice(channelID)
	var err error
	groupId, err = validateAICCResourceID(groupId)
	if err != nil {
		return nil, err
	}
	path := "/api/openapi-maas/exp/aicc/v2/asset"
	payload := map[string]any{
		"groupId":   groupId,
		"assetName": assetName,
		"assetUrl":  assetUrl,
		"assetType": assetType,
	}
	respBytes, err := doAICCRequest(ctx, cID, http.MethodPost, path, payload)
	if err != nil {
		return nil, err
	}

	var res map[string]any
	if err := common.Unmarshal(respBytes, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// QueryAICCAssets 查询素材列表（支持 AIGC / LivenessFace）
func QueryAICCAssets(ctx context.Context, pageNo, pageSize int, groupType string, groupIds []string, assetName string, statuses ...string) (map[string]any, error) {
	return QueryAICCAssetsWithChannel(ctx, pageNo, pageSize, groupType, groupIds, assetName, 0, statuses...)
}

func QueryAICCAssetsWithChannel(ctx context.Context, pageNo, pageSize int, groupType string, groupIds []string, assetName string, channelID int, statuses ...string) (map[string]any, error) {
	if pageNo <= 0 {
		pageNo = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	if groupType == "" {
		groupType = "AIGC"
	}
	path := "/api/openapi-maas/exp/aicc/v2/asset/query"
	payload := map[string]any{
		"pageNo":    pageNo,
		"pageSize":  pageSize,
		"groupType": groupType,
	}
	if len(groupIds) > 0 {
		payload["groupIds"] = groupIds
	}
	if assetName != "" {
		payload["assetName"] = assetName
	}
	if len(statuses) > 0 {
		payload["statuses"] = statuses
	}

	respBytes, err := doAICCRequest(ctx, channelID, http.MethodPost, path, payload)
	if err != nil {
		return nil, err
	}

	var res map[string]any
	if err := common.Unmarshal(respBytes, &res); err != nil {
		return nil, err
	}
	return res, nil
}
