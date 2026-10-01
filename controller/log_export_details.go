package controller

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// These fields mirror details-dialog.tsx. Never enumerate Other: it contains
// diagnostics and credentials which are not part of the visible UI contract.
type logExportField struct {
	Name  string
	Value any
}

type logExportFields []logExportField

func (f *logExportFields) add(name string, value any) {
	*f = append(*f, logExportField{Name: name, Value: value})
}
func (f *logExportFields) text(name string, value any) {
	if s := exportString(value); s != "" {
		f.add(name, s)
	}
}
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
func exportN(m map[string]any, key string) float64 { n, _ := exportNumber(m[key]); return n }
func exportMap(value any) map[string]any           { m, _ := value.(map[string]any); return m }
func exportTruthy(value any) bool {
	if value == nil {
		return false
	}
	if b, ok := value.(bool); ok {
		return b
	}
	if s, ok := value.(string); ok {
		return s != ""
	}
	if n, ok := exportNumber(value); ok {
		return n != 0
	}
	return true
}
func exportArray(value any) []any { a, _ := value.([]any); return a }
func exportYes(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

// formatLogQuota uses 4/6 digits, a minimum nonzero amount, and a normal USD
// symbol. logger.FormatQuota uses fixed six digits and can turn tiny fees into
// zero. Keep all conversions presentation-only; no quota is recomputed.
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

func exportHasToolSurcharge(o map[string]any) bool {
	for _, raw := range exportArray(o["tool_surcharges"]) {
		item := exportMap(raw)
		if strings.TrimSpace(exportString(item["name"])) != "" && exportN(item, "count") > 0 && exportN(item, "price") > 0 {
			return true
		}
	}
	for _, key := range []string{"web_search", "file_search"} {
		if o[key] == true && exportN(o, key+"_call_count") > 0 && exportN(o, key+"_price") > 0 {
			return true
		}
	}
	return o["image_generation_call"] == true && exportN(o, "image_generation_call_price") > 0
}

func exportHasCache(o map[string]any) bool {
	for _, key := range []string{"cache_tokens", "image_cache_tokens", "cache_creation_tokens", "cache_creation_tokens_5m", "cache_creation_tokens_1h"} {
		if exportN(o, key) > 0 {
			return true
		}
	}
	return false
}

func commonDetailFields(log *model.Log, o map[string]any, admin, root bool) logExportFields {
	var f logExportFields
	consume, timing := log.Type == model.LogTypeConsume, log.Type == model.LogTypeConsume || log.Type == model.LogTypeError
	displayable := log.Type == 0 || timing || log.Type == model.LogTypeRefund
	violation := o["violation_fee"] == true || exportTruthy(o["violation_fee_code"]) || exportTruthy(o["violation_fee_marker"])
	f.text("请求标识", log.RequestId)
	f.text("上游请求标识", log.UpstreamRequestId)
	f.text("令牌名称", log.TokenName)
	group := log.Group
	if group == "" {
		group = exportString(o["group"])
	}
	f.text("分组", group)
	if timing || (admin && log.Type == model.LogTypeTopup) || log.Type == model.LogTypeLogin {
		f.text("IP地址", log.Ip)
	}
	if timing && log.UseTime > 0 {
		f.add("响应耗时", exportDuration(log.UseTime))
		if log.IsStream && exportN(o, "frt") > 0 {
			f.add("首响应耗时", fmt.Sprintf("%.1f秒", exportN(o, "frt")/1000))
		}
	}
	if timing {
		if o["is_task"] == true {
			f.add("请求方式", "异步任务")
		} else {
			f.add("流式", exportYes(log.IsStream))
		}
		if log.UseTime > 0 && log.CompletionTokens > 0 {
			f.add("每秒输出词元数", float64(log.CompletionTokens)/float64(log.UseTime))
		}
	}
	if displayable {
		f.text("模型", log.ModelName)
		f.add("费用", exportQuota(log.Quota, o))
		if exportHasToolSurcharge(o) {
			f.add("工具附加费提示", "包含工具调用附加费")
		}
		if log.TokenName != "" && !consume {
			gr, ok := exportNumber(o["user_group_ratio"])
			label := "用户专属倍率"
			if !ok || gr == -1 {
				gr, ok = exportNumber(o["group_ratio"])
				label = "分组倍率"
				ok = ok && gr != 1
			}
			if ok {
				f.add(label, fmt.Sprintf("%.4fx", gr))
			}
		}
	}
	if violation {
		f.text("违规代码", o["violation_fee_code"])
		f.text("违规标记", o["violation_fee_marker"])
		fee, ok := exportNumber(o["fee_quota"])
		if !ok {
			fee = float64(log.Quota)
		}
		f.add("违规费用", exportMoney(fee, true))
	}
	if log.Type == model.LogTypeRefund {
		f.text("任务标识", o["task_id"])
		f.text("退款原因", o["reason"])
	}
	if exportTruthy(o["audio"]) || exportTruthy(o["ws"]) {
		for _, p := range []struct{ key, label string }{{"audio_input", "音频输入词元数"}, {"audio_output", "音频输出词元数"}, {"text_input", "文本输入词元数"}, {"text_output", "文本输出词元数"}} {
			if exportN(o, p.key) > 0 {
				f.add(p.label, o[p.key])
			}
		}
	}
	f.text("推理强度", o["reasoning_effort"])
	if exportTruthy(o["is_system_prompt_overwritten"]) {
		f.add("系统提示词", "已覆盖")
	}
	if exportTruthy(o["is_model_mapped"]) && exportTruthy(o["upstream_model_name"]) {
		f.add("请求模型", log.ModelName)
		f.add("实际模型", o["upstream_model_name"])
	}
	if displayable && (log.PromptTokens > 0 || log.CompletionTokens > 0) {
		f.add("输入词元数", log.PromptTokens)
		f.add("输出词元数", log.CompletionTokens)
		if exportN(o, "cache_tokens") > 0 {
			f.add("缓存读取词元数", o["cache_tokens"])
		}
		if n, ok := exportNumber(o["image_cache_tokens"]); ok {
			f.add("图像缓存词元数", n)
		}
		five, hour := exportN(o, "cache_creation_tokens_5m"), exportN(o, "cache_creation_tokens_1h")
		write := exportN(o, "cache_creation_tokens")
		if five > 0 || hour > 0 {
			write = five + hour
		}
		if write > 0 {
			f.add("缓存写入词元数", write)
		}
		if five > 0 {
			f.add("缓存写入词元数（5分钟）", five)
		}
		if hour > 0 {
			f.add("缓存写入词元数（1小时）", hour)
		}
		if exportTruthy(o["image"]) && exportTruthy(o["image_output"]) {
			f.add("图像词元数", o["image_output"])
		}
		bill := exportMap(o["billing_tokens"])
		for _, v := range exportBillingVariables {
			if n, ok := exportNumber(bill[v.key]); ok {
				f.add("计费"+v.label+"词元数", n)
			}
		}
	}
	if consume && o != nil && !violation {
		exportBillingFields(&f, log, o, admin)
	}
	if status := exportMap(o["stream_status"]); status != nil && status["status"] != "ok" {
		s := exportString(status["status"])
		if s == "" {
			s = "错误"
		}
		f.add("流式状态", s)
		f.text("流结束原因", status["end_reason"])
		if exportN(status, "error_count") > 0 {
			f.add("流软错误数", status["error_count"])
		}
		f.text("流结束错误", status["end_error"])
		for i, e := range exportArray(status["errors"]) {
			f.text(fmt.Sprintf("流错误%d", i+1), e)
		}
	}
	if o["billing_source"] == "subscription" {
		if exportTruthy(o["subscription_plan_id"]) {
			f.add("订阅套餐", strings.TrimSpace("#"+exportString(o["subscription_plan_id"])+" "+exportString(o["subscription_plan_title"])))
		}
		if exportTruthy(o["subscription_id"]) {
			f.add("订阅实例", "#"+exportString(o["subscription_id"]))
		}
		for _, p := range []struct{ key, label string }{{"subscription_pre_consumed", "订阅预扣"}, {"subscription_post_delta", "订阅差额"}, {"subscription_consumed", "订阅最终扣费"}, {"subscription_remain", "订阅剩余"}} {
			if n, ok := exportNumber(o[p.key]); ok && (p.key != "subscription_post_delta" || n != 0) {
				value := exportMoney(n, true)
				if p.key == "subscription_remain" {
					if total, ok := exportNumber(o["subscription_total"]); ok {
						value += " / " + exportMoney(total, true)
					}
				}
				f.add(p.label, value)
			}
		}
	}
	for i, line := range exportArray(o["po"]) {
		s, ok := line.(string)
		if !ok || s == "" {
			continue
		}
		action, content, found := strings.Cut(s, " ")
		if !found {
			content = s
		}
		label := exportOverrideActions[strings.ToLower(action)]
		if label == "" {
			label = action
		}
		f.add(fmt.Sprintf("参数覆盖%d", i+1), label+"："+content)
	}
	if log.Type == model.LogTypeLogin {
		f.text("登录方式", o["login_method"])
		f.text("用户代理", o["user_agent"])
	}
	if admin {
		exportAdminDetails(&f, log, o)
	}
	if admin && root {
		if r := exportMap(o["root_info"]); r != nil {
			f.text("上游任务标识", r["upstream_task_id"])
			f.text("节点名称", r["node_name"])
			if plugin := exportMap(r["task_plugin"]); plugin != nil {
				f.add("接口版本", exportString(plugin["api_version"]))
				f.add("插件代次", exportString(plugin["generation"]))
			}
		}
	}
	f.text("内容", exportAuditFields(&f, log, o, admin))
	return f
}

func exportBillingFields(f *logExportFields, log *model.Log, o map[string]any, admin bool) {
	tiered := o["billing_mode"] == "tiered_expr"
	base := exportN(o, "model_ratio") * 2
	price := func(label string, n float64) { f.add(label+"单价", exportMoney(n, false)+"/百万词元") }
	if tiered {
		f.add("计费模式", "动态计价")
		exportDynamicPricing(f, o)
	} else if exportN(o, "model_price") > 0 {
		f.add("计费模式", "按次计费")
		f.add("模型单价", exportMoney(exportN(o, "model_price"), false))
	} else {
		f.add("计费模式", "按词元计费")
		if _, ok := exportNumber(o["model_ratio"]); ok {
			price("输入", base)
			if n, ok := exportNumber(o["completion_ratio"]); ok {
				price("输出", base*n)
			}
		}
	}
	gr, ok := exportNumber(o["user_group_ratio"])
	label := "用户专属倍率"
	if !ok || gr == -1 {
		gr, ok = exportNumber(o["group_ratio"])
		label = "分组倍率"
	}
	if ok {
		f.add(label, fmt.Sprintf("%.4fx", gr))
	}
	if !tiered {
		if exportHasCache(o) && (o["claude"] == true || (exportN(o, "model_price") <= 0 && o["model_ratio"] != nil)) {
			for _, p := range []struct {
				key, label string
				skip       float64
			}{{"cache_ratio", "缓存读取", 1}, {"cache_creation_ratio", "缓存创建", 1}, {"cache_creation_ratio_5m", "缓存创建（5分钟）", 0}, {"cache_creation_ratio_1h", "缓存创建（1小时）", 0}} {
				if p.key == "cache_creation_ratio_5m" && o["claude"] != true {
					continue
				}
				if n, ok := exportNumber(o[p.key]); ok && n != p.skip {
					price(p.label, base*n)
				}
			}
		}
		for _, p := range []struct{ key, label string }{{"audio_ratio", "音频输入"}, {"audio_completion_ratio", "音频输出"}, {"image_ratio", "图像输入"}} {
			if n, ok := exportNumber(o[p.key]); ok && n != 1 {
				price(p.label, base*n)
			}
		}
	}
	for _, p := range []struct{ key, label string }{{"web_search", "网络搜索"}, {"file_search", "文件搜索"}} {
		if exportTruthy(o[p.key]) && exportTruthy(o[p.key+"_call_count"]) {
			f.add(p.label+"次数", o[p.key+"_call_count"])
			if n, ok := exportNumber(o[p.key+"_price"]); ok && n != 0 {
				f.add(p.label+"价格", exportMoney(n, false))
			}
		}
	}
	if exportTruthy(o["image_generation_call"]) && exportTruthy(o["image_generation_call_price"]) {
		f.add("图像生成价格", exportMoney(exportN(o, "image_generation_call_price"), false))
	}
	if exportTruthy(o["audio_input_seperate_price"]) && exportTruthy(o["audio_input_price"]) {
		f.add("音频输入独立价格", exportMoney(exportN(o, "audio_input_price"), false))
	}
	if admin && exportMap(o["admin_info"]) != nil {
		f.add("计费路径", exportBillingPath(exportMap(o["admin_info"])))
	}
	facts := exportMap(o["usage_facts"])
	keys := make([]string, 0, len(facts))
	for key := range facts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		f.add(fmt.Sprintf("用量参数%d名称", i+1), key)
		f.add(fmt.Sprintf("用量参数%d数值", i+1), exportString(facts[key]))
	}
	f.add("总费用", exportMoney(float64(log.Quota), true))
}

var exportOverrideActions = map[string]string{
	"set": "设置", "delete": "删除", "copy": "复制", "move": "移动", "append": "追加", "prepend": "前置",
	"trim_prefix": "移除前缀", "trim_suffix": "移除后缀", "ensure_prefix": "确保前缀", "ensure_suffix": "确保后缀",
	"trim_space": "去除空白", "to_lower": "转小写", "to_upper": "转大写", "replace": "替换", "regex_replace": "正则替换",
	"set_header": "设置请求头", "delete_header": "删除请求头", "copy_header": "复制请求头", "move_header": "移动请求头",
	"pass_headers": "透传请求头", "sync_fields": "同步字段", "return_error": "返回错误",
}

func exportBillingPath(a map[string]any) string {
	path := exportString(a["usage_billing_path"])
	if path == "local" || (path == "" && a["local_count_tokens"] == true) {
		return "本地计费"
	}
	for _, kind := range []string{"openai", "openai-estimated", "anthropic", "anthropic-estimated", "gemini", "gemini-estimated"} {
		if path == "billing-usage-"+kind {
			return "上游响应（" + path + "）"
		}
	}
	return "上游响应"
}

func exportAdminDetails(f *logExportFields, log *model.Log, o map[string]any) {
	a := exportMap(o["admin_info"])
	f.text("用户", log.Username)
	if log.ChannelId > 0 || log.Type == 0 || log.Type == 2 || log.Type == 5 || log.Type == 6 {
		f.add("渠道编号", log.ChannelId)
		f.text("渠道名称", log.ChannelName)
	}
	if chain := exportArray(a["use_channel"]); len(chain) > 0 {
		parts := make([]string, 0, len(chain))
		for _, n := range chain {
			parts = append(parts, exportString(n))
		}
		f.add("重试链", strings.Join(parts, " → "))
	}
	if log.Type != model.LogTypeRefund {
		chain := make([]string, 0)
		for _, v := range exportArray(o["request_conversion"]) {
			if exportTruthy(v) {
				chain = append(chain, exportString(v))
			}
		}
		if exportTruthy(o["request_path"]) || len(chain) > 0 {
			f.text("请求路径", o["request_path"])
			s := "原生格式"
			if len(chain) > 1 {
				s = strings.Join(chain, " -> ")
			}
			f.add("请求转换", s)
		}
		if log.Type != model.LogTypeConsume && a != nil {
			f.add("计费路径", exportBillingPath(a))
		}
	}
	f.text("拦截原因", a["reject_reason"])
	if q := exportMap(a["quota_saturation"]); q != nil {
		kind := map[string]string{"overflow": "溢出", "underflow": "下溢", "nan": "无效值（NaN）"}[exportString(q["kind"])]
		f.add("额度钳制告警", "额度饱和保护已触发")
		f.add("额度钳制类型", kind)
		f.text("额度原始值", q["original"])
		f.text("额度钳制结果", q["clamped"])
		f.text("额度钳制操作", q["op"])
	}
	if p := exportMap(a["task_plugin"]); p != nil {
		f.text("插件标识", p["key"])
		f.text("插件名称", p["name"])
		f.text("插件版本", p["version"])
		if author := exportMap(p["author"]); author != nil {
			f.text("插件作者", author["name"])
			f.text("插件作者地址", safeExportAuthorURL(exportString(author["url"])))
		}
	}
	if log.Type == model.LogTypeTopup {
		for _, p := range []struct{ key, label string }{{"payment_method", "订单支付方式"}, {"callback_payment_method", "回调支付方式"}, {"caller_ip", "回调来源IP"}, {"server_ip", "服务器IP"}, {"node_name", "充值节点名称"}, {"version", "系统版本"}} {
			f.text(p.label, a[p.key])
		}
		if a == nil {
			f.add("充值审计提示", "此历史记录早于审计信息追踪，无法补录。新充值已记录服务器IP、回调IP、支付方式和系统版本。")
		}
	}
	if log.Type == model.LogTypeManage {
		name, id := exportString(a["admin_username"]), exportString(a["admin_id"])
		if id != "" {
			if name != "" {
				name += " "
			}
			name += "(ID: " + id + ")"
		}
		f.text("操作管理员", name)
		route := exportMap(o["audit_info"])
		if route != nil || exportAuditOperation(o) != "" {
			method := exportString(a["auth_method"])
			if method == "access_token" {
				method = "访问令牌"
			}
			if method == "session" {
				method = "会话"
			}
			f.text("认证方式", method)
			if exportTruthy(route["method"]) && exportTruthy(route["route"]) {
				f.add("审计请求", exportString(route["method"])+" "+exportString(route["route"]))
			}
			if route["status"] != nil {
				result := "失败"
				if exportTruthy(route["success"]) {
					result = "成功"
				}
				f.add("审计结果", result+"（"+exportString(route["status"])+"）")
			}
			params := exportMap(exportMap(o["op"])["params"])
			var changed []string
			labels := map[string]string{"status": "状态", "models": "模型", "group": "分组", "type": "类型", "base_url": "基础地址", "key": "密钥"}
			for _, v := range exportArray(params["changed_fields"]) {
				s := exportString(v)
				if label, ok := labels[s]; ok {
					s = label
				}
				changed = append(changed, s)
			}
			f.text("变更字段", strings.Join(changed, ", "))
		}
	}
}
