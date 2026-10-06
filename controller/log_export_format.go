package controller

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func exportNumber(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func exportN(m map[string]any, key string) float64 {
	n, _ := exportNumber(m[key])
	return n
}

func exportArray(value any) []any {
	a, _ := value.([]any)
	return a
}

func isViolationFeeLog(o map[string]any) bool {
	if o == nil {
		return false
	}
	return exportString(o["violation_fee_code"]) != "" || exportString(o["violation_fee_marker"]) != ""
}

func exportMoney(value float64, quota bool) string {
	kind := operation_setting.GetQuotaDisplayType()
	if quota && kind == operation_setting.QuotaDisplayTypeTokens {
		return strconv.FormatFloat(value, 'f', -1, 64)
	}
	if quota {
		unit := common.QuotaPerUnit
		if unit <= 0 {
			unit = 500000
		}
		value /= unit
	}
	symbol, rate := "$", 1.0
	switch kind {
	case operation_setting.QuotaDisplayTypeCNY:
		symbol, rate = "¥", operation_setting.USDExchangeRate
		if rate <= 0 {
			rate = 7
		}
	case operation_setting.QuotaDisplayTypeCustom:
		symbol = strings.TrimSpace(operation_setting.GetGeneralSetting().CustomCurrencySymbol)
		if symbol == "" {
			symbol = "¤"
		}
		symbol += " "
		rate = operation_setting.GetGeneralSetting().CustomCurrencyExchangeRate
		if rate <= 0 {
			rate = 1
		}
	}
	value *= rate
	digits := 6
	if math.Abs(value) >= 1 {
		digits = 4
	}
	minimum := math.Pow10(-digits)
	if value != 0 && math.Abs(value) < minimum {
		value = math.Copysign(minimum, value)
	}
	negative := value < 0
	text := strings.TrimRight(strings.TrimRight(strconv.FormatFloat(math.Abs(value), 'f', digits, 64), "0"), ".")
	if text == "" {
		text = "0"
	}
	parts := strings.SplitN(text, ".", 2)
	whole := parts[0]
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	if len(parts) > 1 {
		whole += "." + parts[1]
	}
	if kind == operation_setting.QuotaDisplayTypeCustom {
		if negative {
			whole = "-" + whole
		}
		return symbol + whole
	}
	if negative {
		symbol = "-" + symbol
	}
	return symbol + whole
}

var exportBillingVariables = []struct {
	key, label string
	cache      bool
}{
	{"p", "输入", false}, {"c", "输出", false}, {"cr", "缓存读取", true},
	{"cc", "缓存写入", true}, {"img_cr", "图像缓存", true}, {"cc1h", "缓存写入（1小时）", true},
	{"img", "图像输入", false}, {"img_o", "图像输出", false},
	{"ai", "音频输入", false}, {"ao", "音频输出", false},
}

// 通用日志表头与列宽
func commonLogExportHeaders(isAdmin bool) ([]string, []float64) {
	if isAdmin {
		headers := []string{
			"时间", "渠道", "用户", "类型", "令牌名称", "分组", "模型", "流式",
			"输入Tokens", "输出Tokens", "缓存Tokens", "费用", "耗时",
			"请求ID", "上游请求ID", "计费明细", "详情备注",
			"IP地址", "请求路径", "计费路径", "重试链路",
		}
		widths := []float64{
			20, 18, 16, 10, 24, 12, 24, 14,
			14, 14, 18, 14, 14,
			38, 36, 45, 35,
			16, 26, 14, 12,
		}
		return headers, widths
	}
	headers := []string{
		"时间", "类型", "令牌名称", "分组", "模型", "流式",
		"输入Tokens", "输出Tokens", "缓存Tokens", "费用", "耗时",
		"请求ID", "上游请求ID", "计费明细", "详情备注",
	}
	widths := []float64{
		20, 10, 24, 12, 24, 14,
		14, 14, 18, 14, 14,
		38, 36, 45, 35,
	}
	return headers, widths
}

// 格式化单行通用日志
func formatCommonLogRow(log *model.Log, o map[string]any, isAdmin bool) []any {
	createdAt := common.BeijingDateTime(log.CreatedAt)
	typeLabel := exportLogTypeLabel(log.Type)
	tokenName := log.TokenName
	groupName := log.Group
	if groupName == "" && o != nil {
		groupName = exportString(o["group"])
	}

	modelDisplay := log.ModelName
	if o != nil {
		if mapped, _ := o["is_model_mapped"].(bool); mapped {
			if actual := exportString(o["upstream_model_name"]); actual != "" && actual != log.ModelName {
				modelDisplay = fmt.Sprintf("%s (%s)", log.ModelName, actual)
			}
		}
	}

	streamDisplay := "非流"
	if log.IsStream {
		streamDisplay = "流式"
	} else if log.UseTime > 0 && log.CompletionTokens > 0 {
		tps := float64(log.CompletionTokens) / (float64(log.UseTime) / 1000.0)
		streamDisplay = fmt.Sprintf("非流 (%.0f t/s)", tps)
	}

	promptTokens := log.PromptTokens
	completionTokens := log.CompletionTokens

	cacheDisplay := "-"
	if o != nil {
		cacheRead := 0
		if cr, ok := exportNumber(o["cache_tokens"]); ok && cr > 0 {
			cacheRead = int(cr)
		}
		cacheWrite := 0
		if cw, ok := exportNumber(o["cache_creation_tokens"]); ok && cw > 0 {
			cacheWrite = int(cw)
		} else if cw5, ok := exportNumber(o["cache_creation_tokens_5m"]); ok && cw5 > 0 {
			cacheWrite = int(cw5)
		}
		if cacheRead > 0 && cacheWrite > 0 {
			cacheDisplay = fmt.Sprintf("读取 %d / 写入 %d", cacheRead, cacheWrite)
		} else if cacheRead > 0 {
			cacheDisplay = fmt.Sprintf("读取 %d", cacheRead)
		} else if cacheWrite > 0 {
			cacheDisplay = fmt.Sprintf("写入 %d", cacheWrite)
		}
	}

	costDisplay := exportQuota(log.Quota, o)

	timingDisplay := "-"
	if log.UseTime > 0 {
		timingDisplay = fmt.Sprintf("%.1fs", float64(log.UseTime)/1000.0)
		if o != nil {
			if frt, ok := exportNumber(o["frt"]); ok && frt > 0 {
				timingDisplay += fmt.Sprintf(" (首字 %.1fs)", frt/1000.0)
			}
		}
	}

	requestID := log.RequestId
	upstreamRequestID := log.UpstreamRequestId

	billingDetail := buildBillingDetailText(log, o)
	remarkDetail := buildRemarkDetailText(log, o)

	if isAdmin {
		channelDisplay := ""
		if log.ChannelId > 0 {
			if log.ChannelName != "" {
				channelDisplay = fmt.Sprintf("#%d (%s)", log.ChannelId, log.ChannelName)
			} else {
				channelDisplay = fmt.Sprintf("#%d", log.ChannelId)
			}
		}
		userDisplay := log.Username
		ipDisplay := log.Ip
		requestPath := ""
		retryChain := ""
		billingPath := ""
		if o != nil {
			requestPath = exportString(o["request_path"])
			if adminInfo, ok := o["admin_info"].(map[string]any); ok {
				billingPath = exportString(adminInfo["usage_billing_path"])
				if uc, ok := adminInfo["use_channel"].([]any); ok && len(uc) > 0 {
					var chainParts []string
					for _, c := range uc {
						chainParts = append(chainParts, fmt.Sprint(c))
					}
					retryChain = strings.Join(chainParts, " → ")
				}
			}
		}
		return []any{
			createdAt, channelDisplay, userDisplay, typeLabel, tokenName, groupName, modelDisplay, streamDisplay,
			promptTokens, completionTokens, cacheDisplay, costDisplay, timingDisplay,
			requestID, upstreamRequestID, billingDetail, remarkDetail,
			ipDisplay, requestPath, billingPath, retryChain,
		}
	}

	return []any{
		createdAt, typeLabel, tokenName, groupName, modelDisplay, streamDisplay,
		promptTokens, completionTokens, cacheDisplay, costDisplay, timingDisplay,
		requestID, upstreamRequestID, billingDetail, remarkDetail,
	}
}

// 组织人类可读的计费明细
func buildBillingDetailText(log *model.Log, o map[string]any) string {
	if o == nil {
		return "-"
	}
	var parts []string
	billingMode := exportString(o["billing_mode"])
	matchedTier := exportString(o["matched_tier"])

	if billingMode == "tiered_expr" {
		modeStr := "动态计费"
		if matchedTier != "" {
			modeStr += " · " + matchedTier
		}
		parts = append(parts, modeStr)

		if encoded, ok := o["expr_b64"].(string); ok && encoded != "" {
			if tiers, ok := parsePriceTiers(encoded); ok {
				for _, tier := range tiers {
					if exportNormalizeTier(tier.label) == exportNormalizeTier(matchedTier) {
						var priceParts []string
						if p, ok := tier.prices["p"]; ok && p > 0 {
							priceParts = append(priceParts, fmt.Sprintf("输入: %s/M", exportMoney(p, false)))
						}
						if c, ok := tier.prices["c"]; ok && c > 0 {
							priceParts = append(priceParts, fmt.Sprintf("输出: %s/M", exportMoney(c, false)))
						}
						if cr, ok := tier.prices["cr"]; ok && cr > 0 {
							priceParts = append(priceParts, fmt.Sprintf("缓存读取: %s/M", exportMoney(cr, false)))
						}
						if cw, ok := tier.prices["cw"]; ok && cw > 0 {
							priceParts = append(priceParts, fmt.Sprintf("缓存写入: %s/M", exportMoney(cw, false)))
						}
						if tier.fixed != nil {
							priceParts = append(priceParts, fmt.Sprintf("固定: %s/次", exportMoney(*tier.fixed, false)))
						}
						if len(priceParts) > 0 {
							parts = append(parts, "("+strings.Join(priceParts, ", ")+")")
						}
						break
					}
				}
			}
		}
	} else if price, ok := exportNumber(o["model_price"]); ok && price > 0 {
		parts = append(parts, fmt.Sprintf("按次计费 (%s/次)", exportMoney(price, false)))
	} else if ratio, ok := exportNumber(o["model_ratio"]); ok && ratio > 0 {
		inPrice := ratio * 2.0
		var priceParts []string
		priceParts = append(priceParts, fmt.Sprintf("输入: %s/M", exportMoney(inPrice, false)))
		if compRatio, ok := exportNumber(o["completion_ratio"]); ok && compRatio > 0 {
			priceParts = append(priceParts, fmt.Sprintf("输出: %s/M", exportMoney(inPrice*compRatio, false)))
		}
		parts = append(parts, "标准计价 ("+strings.Join(priceParts, ", ")+")")
	}

	if gr, ok := exportNumber(o["group_ratio"]); ok && gr != 1 && gr > 0 {
		parts = append(parts, fmt.Sprintf("分组倍率: %.4fx", gr))
	}

	if o["billing_source"] == "subscription" {
		planStr := "订阅扣费"
		if planID := exportString(o["subscription_plan_id"]); planID != "" {
			planStr += fmt.Sprintf(" (#%s)", planID)
		}
		parts = append(parts, planStr)
	}

	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " | ")
}

// 组织详情备注（错误、退款、说明）
func buildRemarkDetailText(log *model.Log, o map[string]any) string {
	if log.Type == model.LogTypeError || log.Type == 5 {
		errStr := "调用失败"
		if log.Quota == 0 {
			errStr += " (未扣费)"
		}
		if reason := exportString(o["reason"]); reason != "" {
			errStr += ": " + reason
		} else if log.Content != "" {
			errStr += ": " + log.Content
		}
		return errStr
	}
	if log.Type == model.LogTypeRefund || log.Type == 6 {
		refundStr := "额度退还"
		if reason := exportString(o["reason"]); reason != "" {
			refundStr += ": " + reason
		} else if log.Content != "" {
			refundStr += ": " + log.Content
		}
		return refundStr
	}
	if o != nil {
		if isViolationFeeLog(o) {
			code := exportString(o["violation_fee_code"])
			if code != "" {
				return fmt.Sprintf("违规扣费 (代码: %s)", code)
			}
			return "违规扣费"
		}
	}
	if log.Content != "" {
		return log.Content
	}
	return "-"
}

// 任务日志表头与列宽
func taskLogExportHeaders(isAdmin bool) ([]string, []float64) {
	if isAdmin {
		headers := []string{
			"提交时间", "开始时间", "完成时间", "任务标识", "平台", "操作类型",
			"状态", "进度", "任务耗时", "调用模型", "产物", "失败原因",
			"用户", "渠道", "分组", "费用", "请求标识", "插件",
		}
		widths := []float64{
			20, 20, 20, 38, 14, 16,
			12, 10, 12, 24, 30, 35,
			16, 18, 12, 14, 38, 22,
		}
		return headers, widths
	}
	headers := []string{
		"提交时间", "开始时间", "完成时间", "任务标识", "平台", "操作类型",
		"状态", "进度", "任务耗时", "调用模型", "产物", "失败原因",
	}
	widths := []float64{
		20, 20, 20, 38, 14, 16,
		12, 10, 12, 24, 30, 35,
	}
	return headers, widths
}

// 格式化单行任务日志
func formatTaskLogRow(row model.TaskExportRow, isAdmin bool) []any {
	submitTime := common.BeijingDateTime(row.SubmitTime)
	startTime := "-"
	if row.StartTime > 0 {
		startTime = common.BeijingDateTime(row.StartTime)
	}
	finishTime := "-"
	if row.FinishTime > 0 {
		finishTime = common.BeijingDateTime(row.FinishTime)
	}

	modelDisplay := row.OriginModel
	if row.ActualModel != "" && row.ActualModel != row.OriginModel {
		if modelDisplay != "" {
			modelDisplay = fmt.Sprintf("%s (%s)", modelDisplay, row.ActualModel)
		} else {
			modelDisplay = row.ActualModel
		}
	}
	if modelDisplay == "" {
		modelDisplay = "-"
	}

	duration := "-"
	if row.FinishTime > row.SubmitTime {
		duration = fmt.Sprintf("%.1f秒", float64(row.FinishTime-row.SubmitTime))
	}

	artifactDisplay := "-"
	if row.UpstreamTaskID != "" {
		artifactDisplay = fmt.Sprintf("/api/task/%s/artifacts", row.TaskID)
	}

	failReason := "-"
	if row.FailReason != "" {
		failReason = row.FailReason
	}

	res := []any{
		submitTime, startTime, finishTime, row.TaskID, row.Platform,
		exportTaskActionLabel(row.Action), exportTaskStatusLabel(row.Status),
		row.Progress, duration, modelDisplay, artifactDisplay, failReason,
	}

	if isAdmin {
		channelDisplay := fmt.Sprintf("#%d", row.ChannelID)
		userDisplay := row.Username
		if userDisplay == "" && row.UserID > 0 {
			userDisplay = fmt.Sprint(row.UserID)
		}
		costDisplay := exportMoney(float64(row.Quota), true)
		pluginDisplay := "-"
		if row.PluginName != "" {
			pluginDisplay = row.PluginName
			if row.PluginVersion != "" {
				pluginDisplay += " @ " + row.PluginVersion
			}
			if row.PluginAuthor != "" {
				pluginDisplay += fmt.Sprintf(" (by %s)", row.PluginAuthor)
			}
		}

		res = append(res,
			userDisplay, channelDisplay, row.Group, costDisplay,
			row.RequestID, pluginDisplay,
		)
	}

	return res
}

// 绘图日志表头与列宽
func drawingLogExportHeaders(isAdmin bool) ([]string, []float64) {
	if isAdmin {
		headers := []string{
			"提交时间", "操作类型", "状态", "任务标识", "进度", "任务耗时",
			"图片地址", "提示词", "英文提示词", "失败原因",
			"用户", "渠道", "提交结果",
		}
		widths := []float64{
			20, 14, 12, 32, 10, 12,
			38, 40, 40, 35,
			16, 18, 14,
		}
		return headers, widths
	}
	headers := []string{
		"提交时间", "操作类型", "状态", "任务标识", "进度", "任务耗时",
		"图片地址", "提示词", "英文提示词", "失败原因",
	}
	widths := []float64{
		20, 14, 12, 32, 10, 12,
		38, 40, 40, 35,
	}
	return headers, widths
}

// 格式化单行绘图日志
func formatDrawingLogRow(row model.DrawingExportRow, isAdmin bool) []any {
	submitTime := common.BeijingDateTime(row.SubmitTime)
	action := exportDrawingActionLabel(row.Action)
	status := exportTaskStatusLabel(row.Status)
	if row.Status == "MODAL" {
		status = "等待中"
	}
	duration := "-"
	if row.FinishTime > row.SubmitTime {
		duration = fmt.Sprintf("%.1f秒", float64(row.FinishTime-row.SubmitTime))
	}
	imageURL := "-"
	if row.ImageURL != "" {
		imageURL = row.ImageURL
	}
	prompt := "-"
	if row.Prompt != "" {
		prompt = row.Prompt
	}
	promptEN := "-"
	if row.PromptEN != "" {
		promptEN = row.PromptEN
	}
	failReason := "-"
	if row.FailReason != "" {
		failReason = row.FailReason
	}

	res := []any{
		submitTime, action, status, row.MJID, row.Progress, duration,
		imageURL, prompt, promptEN, failReason,
	}

	if isAdmin {
		userDisplay := row.Username
		if userDisplay == "" && row.UserID > 0 {
			userDisplay = fmt.Sprint(row.UserID)
		}
		channelDisplay := fmt.Sprintf("#%d", row.ChannelID)
		codeDisplay := fmt.Sprint(row.Code)
		if row.Code == 0 {
			codeDisplay = "未提交 (0)"
		} else if row.Code == 1 {
			codeDisplay = "成功 (1)"
		}

		res = append(res, userDisplay, channelDisplay, codeDisplay)
	}

	return res
}

func exportDrawingActionLabel(action string) string {
	actions := map[string]string{
		"IMAGINE": "文生图", "UPSCALE": "放大", "VIDEO": "生成视频",
		"EDITS": "编辑", "VARIATION": "变换", "HIGH_VARIATION": "强变换",
		"LOW_VARIATION": "弱变换", "PAN": "平移", "DESCRIBE": "图生文",
		"BLEND": "图混合", "UPLOAD": "上传", "SHORTEN": "精简词",
		"REROLL": "重绘", "INPAINT": "局部重绘", "SWAP_FACE": "换脸",
		"ZOOM": "缩放", "CUSTOM_ZOOM": "自定义缩放",
	}
	if label, ok := actions[action]; ok {
		return label
	}
	return action
}
