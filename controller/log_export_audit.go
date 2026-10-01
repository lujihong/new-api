package controller

import (
	"strings"

	"github.com/QuantumNous/new-api/model"
)

// Match the operation descriptor used by quota-audit-operation.ts; only named
// display parameters are consumed, never arbitrary audit metadata.
func exportQuotaOperation(o map[string]any, success bool) (string, logExportFields) {
	op := exportMap(o["op"])
	p := exportMap(op["params"])
	action := exportString(op["action"])
	mode := map[string]string{"user.quota_add": "增加用户额度", "user.quota_subtract": "减少用户额度", "user.quota_override": "覆盖用户额度"}[action]
	unknown := action == "generic" && p["action"] == "add_quota"
	if unknown {
		mode = "调整用户额度"
	}
	if mode == "" {
		return "", nil
	}
	text := func(v any) string {
		if n, ok := exportNumber(v); ok {
			return exportMoney(n, true)
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
		return "未记录"
	}
	name, id := exportString(p["target_username"]), exportString(p["target_user_id"])
	summary := mode
	if name != "" {
		summary += "：" + name
	}
	if id != "" {
		summary += " (ID: " + id + ")"
	}
	if name == "" && id == "" {
		summary += " · 未记录目标"
	}
	requested := p["requested_quota"]
	if requested == nil {
		requested = p["quota"]
	}
	if requested == nil && success && action == "user.quota_override" {
		requested = p["to"]
	}
	if name == "" {
		name = "未记录"
	}
	if id == "" {
		id = "未记录"
	}
	var fields logExportFields
	fields.add("目标用户名", name)
	fields.add("用户编号", id)
	if unknown {
		mode = exportString(p["mode"])
		if mode == "" {
			mode = "未记录"
		}
	}
	fields.add("调整模式", mode)
	fields.add("请求调整额度", text(requested))
	description := "请求调整额度：" + text(requested)
	if success {
		before, after := text(p["from"]), text(p["to"])
		fields.add("调整前额度", before)
		fields.add("调整后额度", after)
		description += " · " + before + " → " + after
	} else {
		reason := map[string]string{"invalid_parameters": "调整参数无效", "permission_denied": "没有权限调整此用户", "target_not_found": "目标用户不存在", "quota_limit_exceeded": "超出钱包额度上限", "database_error": "额度更新失败"}[exportString(p["failure_reason"])]
		if reason == "" {
			reason = "未记录"
		}
		fields.add("调整失败原因", reason)
		description += " · " + reason
	}
	return summary + " · " + description, fields
}

func exportAuditOperation(o map[string]any) string {
	route := exportMap(o["audit_info"])
	if text, _ := exportQuotaOperation(o, route["success"] != false); text != "" {
		return text
	}
	op := exportMap(o["op"])
	params := exportMap(op["params"])
	action := exportString(op["action"])
	templates := map[string]string{
		"login": "通过{{method}}登录成功", "token.create": "创建API令牌", "token.update": "更新API令牌配置", "token.status_update": "更新API令牌状态", "token.delete": "删除API令牌", "token.delete_batch": "批量删除API令牌", "token.key_view": "查看API令牌密钥", "token.key_view_batch": "批量查看API令牌密钥",
		"access_token.generate": "生成系统访问令牌", "access_token.revoke": "撤销系统访问令牌",
		"user.create": "创建用户{{username}}（角色{{role}}）", "user.update": "更新用户{{username}}（ID: {{id}}）", "user.delete": "删除用户{{username}}（ID: {{id}}）", "user.manage": "对用户{{username}}（ID: {{id}}）执行{{action}}", "user.account_delete": "删除账户", "user.password_change": "修改账户密码",
		"user.binding_clear": "清除用户{{username}}的{{bindingType}}绑定", "user.2fa_setup": "开始设置双重认证", "user.2fa_enable": "启用双重认证", "user.2fa_disable_self": "禁用双重认证", "user.2fa_backup_codes": "重新生成双重认证备用码", "user.security_verify": "完成安全验证", "user.2fa_disable": "强制禁用用户双重认证",
		"user.binding_start": "发起账户绑定请求", "user.binding_bind": "绑定账户", "user.binding_unbind": "解除账户绑定", "user.email_binding_resend": "重新发送邮箱验证码", "user.passkey_register": "注册通行密钥", "user.passkey_delete": "删除通行密钥", "user.reset_passkey": "重置用户通行密钥", "user.oauth_unbind": "解除OAuth绑定", "user.topup_complete": "完成用户充值订单",
		"option.update": "更新系统设置{{key}}", "option.payment_compliance": "确认支付合规", "option.reset_ratio": "重置模型倍率", "option.clear_affinity_cache": "清除渠道亲和缓存",
		"channel.create": "创建渠道{{name}}（类型{{type}}，数量{{count}}）", "channel.update": "更新渠道{{name}}（ID: {{id}}）", "channel.status_update": "更新渠道状态（ID: {{id}}）", "channel.status_update_batch": "批量更新渠道状态（{{count}}/{{total}}已变更）", "channel.delete": "删除渠道{{name}}（ID: {{id}}）", "channel.delete_batch": "批量删除{{count}}个渠道", "channel.delete_disabled": "删除全部禁用渠道（{{count}}）", "channel.key_view": "查看渠道密钥{{name}}（ID: {{id}}）",
		"channel.tag_disable": "禁用标签为{{tag}}的渠道", "channel.tag_enable": "启用标签为{{tag}}的渠道", "channel.tag_edit": "编辑标签为{{tag}}的渠道", "channel.tag_batch_set": "批量设置{{count}}个渠道的标签", "channel.copy": "复制渠道（来源ID: {{sourceId}}）为{{name}}（新ID: {{id}}）", "channel.multi_key_manage": "对渠道（ID: {{id}}）执行多密钥管理{{action}}", "channel.upstream_apply": "应用渠道（ID: {{id}}）的上游模型变更", "channel.upstream_apply_all": "应用{{count}}个渠道的上游模型变更",
		"redemption.create": "创建{{count}}个名为{{name}}的兑换码（每个{{quota}}）", "redemption.update": "更新兑换码", "redemption.delete": "删除兑换码", "redemption.delete_invalid": "删除失效兑换码",
		"custom_oauth.create": "创建自定义OAuth提供商", "custom_oauth.update": "更新自定义OAuth提供商", "custom_oauth.delete": "删除自定义OAuth提供商", "performance.clear_disk_cache": "清除磁盘缓存", "performance.gc": "触发垃圾回收", "performance.clear_logs": "清除日志文件",
		"prefill_group.create": "创建预填分组", "prefill_group.update": "更新预填分组", "prefill_group.delete": "删除预填分组", "vendor.create": "创建供应商", "vendor.update": "更新供应商", "vendor.delete": "删除供应商", "model.create": "创建模型", "model.update": "更新模型", "model.delete": "删除模型", "model.sync_upstream": "同步上游模型", "deployment.create": "创建部署", "deployment.update": "更新部署", "deployment.delete": "删除部署", "subscription.plan_create": "创建订阅套餐", "subscription.plan_update": "更新订阅套餐", "subscription.bind": "绑定订阅", "log.clear": "清除历史日志", "log.cleanup_start": "日志清理任务已启动。", "generic": "{{method}} {{route}}",
	}
	if action == "redemption.delete_batch" || (action == "redemption.delete" && route["route"] == "/api/redemption/batch") {
		if route["success"] == false {
			return "批量删除兑换码失败"
		}
		if n, ok := exportNumber(params["count"]); ok && n >= 0 {
			return "批量删除" + exportString(params["count"]) + "个兑换码"
		}
		return "批量删除兑换码（未记录数量）"
	}
	template := templates[action]
	for {
		start := strings.Index(template, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(template[start+2:], "}}")
		if end < 0 {
			break
		}
		end += start + 2
		key := template[start+2 : end]
		template = template[:start] + exportString(params[key]) + template[end+2:]
	}
	return template
}
func exportAuditFields(f *logExportFields, log *model.Log, o map[string]any, admin bool) string {
	operation := exportAuditOperation(o)
	if log.Type == model.LogTypeTopup {
		_, fields := exportQuotaOperation(o, true)
		*f = append(*f, fields...)
		if operation != "" {
			return operation
		}
	}
	if log.Type == model.LogTypeManage && admin {
		f.text("审计操作", operation)
	}
	if log.Type == model.LogTypeLogin && (exportTruthy(o["login_method"]) || log.Ip != "" || exportTruthy(o["user_agent"])) {
		f.text("登录操作", operation)
	}
	return log.Content
}
