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

func GetAICCConfig() AICCConfig {
	ak := strings.TrimSpace(os.Getenv("AICC_ACCESS_KEY_ID"))
	sk := strings.TrimSpace(os.Getenv("AICC_ACCESS_KEY_SECRET"))
	endpoint := strings.TrimSpace(os.Getenv("AICC_ENDPOINT"))
	if endpoint == "" {
		endpoint = DefaultAICCEndpoint
	}

	if ak == "" || sk == "" {
		// 从启用的渠道中读取配置
		var channel model.Channel
		err := model.DB.Where("status = 1 AND (name LIKE '%移动%' OR name LIKE '%AICC%' OR other_info LIKE '%access_key_id%')").
			Order("priority DESC, id DESC").
			First(&channel).Error
		if err == nil && channel.OtherInfo != "" {
			if v := gjson.Get(channel.OtherInfo, "access_key_id").String(); v != "" {
				ak = v
			}
			if v := gjson.Get(channel.OtherInfo, "access_key_secret").String(); v != "" {
				sk = v
			}
		}
	}

	if ak == "" || sk == "" {
		return AICCConfig{AccessKeyID: ak, AccessKeySecret: sk, Endpoint: endpoint, PoolID: DefaultAICCPoolID}
	}

	return AICCConfig{
		AccessKeyID:     ak,
		AccessKeySecret: sk,
		Endpoint:        endpoint,
		PoolID:          DefaultAICCPoolID,
	}
}

// ValidateAICCVideoChannel prevents account-scoped assets from falling back to an unrelated provider.
func ValidateAICCVideoChannel(channelID int) error {
	if channelID <= 0 || model.DB == nil {
		return errors.New("AICC asset channel is unavailable")
	}
	cfg := GetAICCConfig()
	if cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return errors.New("AICC account is unconfigured")
	}
	ch, err := model.GetChannelById(channelID, true)
	if err != nil || ch == nil || ch.Status != common.ChannelStatusEnabled {
		return errors.New("AICC asset channel is unavailable")
	}
	if gjson.Get(ch.OtherInfo, "access_key_id").String() != cfg.AccessKeyID || gjson.Get(ch.OtherInfo, "access_key_secret").String() != cfg.AccessKeySecret {
		return errors.New("人物素材仅可使用其所属移动云账号渠道，请选择移动云渠道后重试")
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

func doAICCRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	cfg := GetAICCConfig()
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
func CreateAICCH5Session(ctx context.Context) (*AICCH5SessionResponse, error) {
	path := "/api/openapi-maas/exp/aicc/v2/real-person-auth/sessions"
	respBytes, err := doAICCRequest(ctx, http.MethodPost, path, map[string]any{})
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
func QueryAICCGroupByBytedToken(ctx context.Context, bytedToken string) (map[string]any, error) {
	path := "/api/openapi-maas/exp/aicc/v2/real-person-auth/asset-group/by-byted-token"
	respBytes, err := doAICCRequest(ctx, http.MethodPost, path, map[string]any{
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
func GetAICCAsset(ctx context.Context, assetId string) (map[string]any, error) {
	var err error
	assetId, err = validateAICCResourceID(assetId)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/openapi-maas/exp/aicc/v2/asset/%s", assetId)
	respBytes, err := doAICCRequest(ctx, http.MethodGet, path, nil)
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
func UpdateAICCAsset(ctx context.Context, assetID string, assetName *string) (map[string]any, error) {
	assetID = strings.TrimSpace(assetID)
	if assetID == "" || strings.ContainsAny(assetID, "/?#") {
		return nil, errors.New("invalid assetId")
	}
	payload := map[string]any{}
	if assetName != nil {
		payload["assetName"] = *assetName
	}
	resp, err := doAICCRequest(ctx, http.MethodPut, "/api/openapi-maas/exp/aicc/v2/asset/"+assetID, payload)
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
func DeleteAICCAsset(ctx context.Context, assetId string) (map[string]any, error) {
	var err error
	assetId, err = validateAICCResourceID(assetId)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/openapi-maas/exp/aicc/v2/asset/%s", assetId)
	respBytes, err := doAICCRequest(ctx, http.MethodDelete, path, nil)
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
func GetAICCAssetGroup(ctx context.Context, groupId string) (map[string]any, error) {
	var err error
	groupId, err = validateAICCResourceID(groupId)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/openapi-maas/exp/aicc/v2/asset-group/%s", groupId)
	respBytes, err := doAICCRequest(ctx, http.MethodGet, path, nil)
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
func UpdateAICCAssetGroup(ctx context.Context, groupId, groupName, description string) (map[string]any, error) {
	return UpdateAICCAssetGroupFields(ctx, groupId, &groupName, &description)
}

func UpdateAICCAssetGroupFields(ctx context.Context, groupId string, groupName, description *string) (map[string]any, error) {
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
	respBytes, err := doAICCRequest(ctx, http.MethodPut, path, payload)
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
func DeleteAICCAssetGroup(ctx context.Context, groupId string) (map[string]any, error) {
	var err error
	groupId, err = validateAICCResourceID(groupId)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/openapi-maas/exp/aicc/v2/asset-group/%s", groupId)
	respBytes, err := doAICCRequest(ctx, http.MethodDelete, path, nil)
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
	if len(filters) > 0 {
		if filters[0].GroupName != "" {
			payload["groupName"] = filters[0].GroupName
		}
		if len(filters[0].GroupIDs) > 0 {
			payload["groupIds"] = filters[0].GroupIDs
		}
	}

	respBytes, err := doAICCRequest(ctx, http.MethodPost, path, payload)
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
func CreateAICCAssetGroup(ctx context.Context, groupName, description string) (map[string]any, error) {
	path := "/api/openapi-maas/exp/aicc/v2/asset-group"
	payload := map[string]any{
		"groupType":   "AIGC",
		"groupName":   groupName,
		"description": description,
	}
	respBytes, err := doAICCRequest(ctx, http.MethodPost, path, payload)
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
func CreateAICCAsset(ctx context.Context, groupId, assetName, assetUrl, assetType string) (map[string]any, error) {
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
	respBytes, err := doAICCRequest(ctx, http.MethodPost, path, payload)
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

	respBytes, err := doAICCRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return nil, err
	}

	var res map[string]any
	if err := common.Unmarshal(respBytes, &res); err != nil {
		return nil, err
	}
	return res, nil
}
