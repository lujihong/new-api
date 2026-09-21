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
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
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
	AICCAccountID   string
	AccountVerified bool
	ChannelPinned   bool
}

// WithAICCConfig pins the resolved credential snapshot for the entire operation.
func WithAICCConfig(ctx context.Context, cfg AICCConfig) context.Context {
	return context.WithValue(ctx, aiccConfigContextKey{}, cfg)
}

type aiccConfigContextKey struct{}

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
	var cfg AICCConfig
	if len(channelID) == 0 {
		cfg, _ = ResolveDefaultAICCConfig()
	} else {
		cfg, _ = GetAICCConfigForChannel(channelID[0])
	}
	return cfg
}

func aiccConfigFromChannel(ch model.Channel) (AICCConfig, error) {
	cfg := AICCConfig{ChannelID: ch.Id, ChannelName: ch.Name}
	if ch.Id <= 0 || ch.Status != common.ChannelStatusEnabled || !gjson.Get(ch.OtherInfo, "aicc_enabled").Bool() {
		return cfg, errors.New("请由管理员显式启用该渠道的 AICC 账号配置")
	}
	cfg.AccessKeyID = strings.TrimSpace(gjson.Get(ch.OtherInfo, "access_key_id").String())
	cfg.AccessKeySecret = strings.TrimSpace(gjson.Get(ch.OtherInfo, "access_key_secret").String())
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(gjson.Get(ch.OtherInfo, "endpoint").String()), "/")
	cfg.PoolID = strings.TrimSpace(gjson.Get(ch.OtherInfo, "pool_id").String())
	u, err := url.Parse(cfg.Endpoint)
	if cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" || cfg.PoolID == "" || err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return AICCConfig{}, errors.New("移动云 AICC 渠道配置不完整，请配置 AK、SK、endpoint 和 pool_id")
	}
	account := strings.TrimSpace(ch.AICCAccountID)
	if account == "" {
		account = strings.TrimSpace(gjson.Get(ch.OtherInfo, "aicc_account_id").String())
	}
	// Pin credentials and resource domain conservatively until an audited account
	// binding exists. An administrator label alone is not upstream identity proof.
	identity := []string{"aicc-config/v1", account, fmt.Sprint(ch.Id), cfg.AccessKeyID, cfg.AccessKeySecret, cfg.Endpoint, cfg.PoolID}
	cfg.AccountVerified = false
	if gjson.Get(ch.OtherInfo, "aicc_channel_pinned").Bool() {
		fingerprint, err := AICCChannelCredentialFingerprint(ch)
		if err != nil {
			return AICCConfig{}, err
		}
		identity = append(identity, fingerprint)
		cfg.ChannelPinned = true
	}
	encoded, err := common.Marshal(identity)
	if err != nil {
		return AICCConfig{}, err
	}
	digest := sha256.Sum256(encoded)
	cfg.AICCAccountID = hex.EncodeToString(digest[:])
	attestation, err := model.LatestAICCAccountAttestation(ch.Id)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return AICCConfig{}, err
	}
	if err == nil && !attestation.Revoked {
		fingerprint, fpErr := AICCChannelCredentialFingerprint(ch)
		if fpErr == nil && fingerprint == attestation.ConfigFingerprint {
			cfg.AICCAccountID = attestation.AccountID
			cfg.AccountVerified = true
		}
	}
	return cfg, nil
}

// AICCChannelCredentialFingerprint binds evidence to both credential sets and
// the effective routing configuration. It never exposes the credential values.
func normalizedChannelBaseURL(ch model.Channel) string {
	base := ""
	if ch.BaseURL != nil {
		base = *ch.BaseURL
	}
	return strings.TrimRight(strings.TrimSpace(base), "/")
}

func AICCChannelCredentialFingerprint(ch model.Channel) (string, error) {
	if ch.ChannelInfo.IsMultiKey || strings.TrimSpace(ch.Key) == "" {
		return "", errors.New("请使用单一视频凭据完成账号核实")
	}
	wire, err := common.Marshal([]any{ch.Id, ch.Type, ch.Key, normalizedChannelBaseURL(ch), ch.OtherInfo, ch.Setting, ch.OtherSettings, ch.HeaderOverride, ch.ParamOverride, ch.ModelMapping, ch.AICCAccountID})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(wire)
	return hex.EncodeToString(digest[:]), nil
}

func AttestAICCChannelAccount(actor, channelID int, subject, evidence, expectedFingerprint string) error {
	if _, err := GetAICCConfigForChannel(channelID); err != nil {
		return err
	}
	var channel model.Channel
	if err := model.DB.Where("id = ?", channelID).First(&channel).Error; err != nil {
		return err
	}
	accountID, err := model.DeriveAICCAccountID(channel, subject)
	if err != nil {
		return err
	}
	return model.RecordAICCAccountAttestation(model.AICCAccountAttestation{
		ActorUserID:       actor,
		ChannelID:         channelID,
		AccountID:         accountID,
		UpstreamSubject:   subject,
		Evidence:          evidence,
		ConfigFingerprint: expectedFingerprint,
	}, AICCChannelCredentialFingerprint)
}

// Explicit channel selection never falls back, including on database errors.
func GetAICCConfigForChannel(channelID int) (AICCConfig, error) {
	if channelID <= 0 || model.DB == nil {
		return AICCConfig{}, errors.New("请选择有效的 AICC 渠道")
	}
	var ch model.Channel
	if err := model.DB.Where("id = ?", channelID).First(&ch).Error; err != nil {
		return AICCConfig{}, errors.New("AICC 渠道不可用，请检查渠道配置")
	}
	return aiccConfigFromChannel(ch)
}

// ResolveDefaultAICCConfig requires one eligible channel; priorities do not establish account ownership.
// Environment credentials are deliberately not treated as a channel.
func ResolveDefaultAICCConfig() (AICCConfig, error) {
	if model.DB == nil {
		return AICCConfig{}, errors.New("AICC 渠道配置不可用")
	}
	var channels []model.Channel
	if err := model.DB.Where("status = ?", common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return AICCConfig{}, errors.New("AICC 渠道配置读取失败")
	}
	var selected AICCConfig
	for _, ch := range channels {
		cfg, err := aiccConfigFromChannel(ch)
		if err != nil {
			continue
		}
		if selected.ChannelID != 0 {
			return AICCConfig{}, errors.New("存在多个 AICC 渠道，请明确选择素材所属渠道")
		}
		selected = cfg
	}
	if selected.ChannelID == 0 {
		return AICCConfig{}, errors.New("暂无配置完整的 AICC 渠道")
	}
	return selected, nil
}

// ListAvailableAICCChannels 列出系统当前所有已启用并支持 AICC 资产的移动云专线渠道
func ListAvailableAICCChannels() ([]map[string]any, error) {
	if model.DB == nil {
		return []map[string]any{}, nil
	}
	var channels []model.Channel
	err := model.DB.Where("status = ?", common.ChannelStatusEnabled).
		Order("priority DESC, id ASC").
		Find(&channels).Error
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(channels))
	for _, ch := range channels {
		if _, err := aiccConfigFromChannel(ch); err != nil {
			continue
		}
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
	cfg, err := GetAICCConfigForChannel(channelID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(assetID) != "" {
		binding, err := model.GetAICCAssetBinding(assetID)
		if err != nil {
			return errors.New("素材来源校验失败，请从素材库重新选择或联系管理员核实归属")
		}
		if binding.AICCAccountID != cfg.AICCAccountID {
			return errors.New("素材来源与视频渠道配置不匹配，请核实账号绑定")
		}
	}
	if !cfg.AccountVerified && !cfg.ChannelPinned {
		return errors.New("该渠道的视频凭据与素材账号关联尚未核实，请联系管理员完成账号绑定")
	}
	return nil
}

// ValidateAICCDispatch checks the actual materialized request, not cached channel
// metadata. No credential from a different configuration may be sent after approval.
func ValidateAICCDispatch(userID, channelID int, assetIDs []string, apiKey, baseURL string, request *http.Request, snapshot ...*model.AICCDispatchSnapshot) error {
	if len(assetIDs) == 0 {
		return nil
	}
	if err := model.ValidateUserAICCAssetIDs(userID, assetIDs); err != nil {
		return err
	}
	ch, err := model.GetChannelById(channelID, true)
	if err != nil {
		return err
	}
	cfg, err := aiccConfigFromChannel(*ch)
	if err != nil || (!cfg.AccountVerified && !cfg.ChannelPinned) {
		return errors.New("素材账号凭据关联已失效，请重新核实")
	}
	if ch.BaseURL == nil || strings.TrimRight(*ch.BaseURL, "/") != strings.TrimRight(baseURL, "/") || ch.Key != apiKey || ch.ChannelInfo.IsMultiKey {
		return errors.New("实际视频请求与已核实的渠道凭据不一致")
	}
	base, err := url.Parse(baseURL)
	if err != nil || request == nil || request.URL == nil || request.URL.User != nil || request.URL.Scheme != base.Scheme || !strings.EqualFold(request.URL.Host, base.Host) {
		return errors.New("实际视频请求地址与已核实的渠道不一致")
	}
	if request.Header.Get("Authorization") != "Bearer "+apiKey {
		return errors.New("实际视频鉴权与账号核实凭据不一致")
	}
	for _, id := range assetIDs {
		binding, err := model.GetOwnedAICCAssetBinding(userID, id)
		if err != nil || binding.AICCAccountID != cfg.AICCAccountID {
			return errors.New("实际视频请求包含不属于当前素材账号的引用")
		}
	}
	if len(snapshot) > 0 && snapshot[0] != nil {
		fingerprint, err := AICCChannelCredentialFingerprint(*ch)
		if err != nil {
			return err
		}
		*snapshot[0] = model.AICCDispatchSnapshot{ChannelID: channelID, AccountID: cfg.AICCAccountID, BaseURL: baseURL, ConfigFingerprint: fingerprint}
	}
	return nil
}

func ValidateAICCTaskSource(task *model.Task, ch *model.Channel) error {
	if task.PrivateData.Execution == nil || task.PrivateData.Execution.AICC == nil {
		return nil
	}
	snapshot := task.PrivateData.Execution.AICC
	if ch == nil || ch.Id != snapshot.ChannelID || task.ChannelId != snapshot.ChannelID {
		return errors.New("素材视频任务渠道来源不一致")
	}
	cfg, err := aiccConfigFromChannel(*ch)
	if err != nil || (!cfg.AccountVerified && !cfg.ChannelPinned) || cfg.AICCAccountID != snapshot.AccountID {
		return errors.New("素材视频任务账号关联已变化，请恢复原配置后查询")
	}
	fingerprint, err := AICCChannelCredentialFingerprint(*ch)
	if err != nil || fingerprint != snapshot.ConfigFingerprint || ch.BaseURL == nil || strings.TrimRight(*ch.BaseURL, "/") != strings.TrimRight(snapshot.BaseURL, "/") {
		return errors.New("素材视频任务凭据或地址已变化，已暂停查询")
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
	cfg, pinned := ctx.Value(aiccConfigContextKey{}).(AICCConfig)
	var err error
	if pinned {
		if channelID != cfg.ChannelID || cfg.ChannelID <= 0 {
			return nil, errors.New("AICC 请求与已固定的渠道来源不一致")
		}
	} else if channelID == 0 {
		cfg, err = ResolveDefaultAICCConfig()
	} else {
		cfg, err = GetAICCConfigForChannel(channelID)
	}
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

	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		// Transport errors may include the signed URL and its access key.
		return nil, errors.New("移动云素材服务请求失败，请稍后重试")
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, errors.New("移动云素材服务返回非预期跳转")
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
