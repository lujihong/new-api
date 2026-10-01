package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/xuri/excelize/v2"
)

func exportTestFields(log *model.Log, admin, root bool) map[string]any {
	out := map[string]any{}
	for _, f := range commonDetailFields(log, exportOtherMap(log.Other), admin, root) {
		out[f.Name] = f.Value
	}
	return out
}
func TestCommonExportNonEmptyDetailConditions(t *testing.T) {
	log := &model.Log{Type: 2, PromptTokens: 120, CompletionTokens: 30, Quota: 1, UseTime: 2, IsStream: true, RequestId: "req", UpstreamRequestId: "up", Ip: "192.0.2.1", Other: `{
		"group":"fallback-group","claude":true,"cache_tokens":10,"cache_creation_tokens":999,"cache_creation_tokens_5m":20,"cache_creation_tokens_1h":40,"image_cache_tokens":0,"image":true,"image_output":8,
		"billing_tokens":{"p":0,"c":30,"ai":7,"len":666},"audio":true,"audio_input":7,"audio_output":9,"text_input":11,"text_output":12,
		"model_ratio":1.5,"completion_ratio":2,"cache_ratio":0.1,"cache_creation_ratio_5m":1.25,"cache_creation_ratio_1h":2,"group_ratio":1,"user_group_ratio":0.5,
		"billing_source":"subscription","subscription_plan_id":4,"subscription_plan_title":"套餐","subscription_id":8,"subscription_pre_consumed":500000,"subscription_post_delta":-1,"subscription_consumed":499999,"subscription_remain":0,"subscription_total":1000000,
		"stream_status":{"status":"error","end_reason":"timeout","error_count":2,"end_error":"ended","errors":["first error","second error"]},
		"po":["set temperature = 0","delete secret",null],"usage_facts":{"duration":5,"mode":"高清"},"frt":250,
		"request_path":"SECRET_PATH","reason":"HIDDEN_REASON","task_id":"HIDDEN_TASK","admin_info":{"reject_reason":"ADMIN_SECRET"},"root_info":{"node_name":"ROOT_SECRET"},"unknown":"RAW_SECRET"}`}
	got := exportTestFields(log, false, false)
	want := map[string]any{"请求标识": "req", "上游请求标识": "up", "分组": "fallback-group", "IP地址": "192.0.2.1", "缓存写入词元数": float64(60), "缓存写入词元数（5分钟）": float64(20), "缓存写入词元数（1小时）": float64(40), "图像缓存词元数": float64(0), "计费输入词元数": float64(0), "音频输入词元数": float64(7), "文本输出词元数": float64(12), "图像词元数": float64(8), "计费模式": "按词元计费", "用户专属倍率": "0.5000x", "流错误1": "first error", "流错误2": "second error", "参数覆盖1": "设置：temperature = 0", "用量参数1名称": "duration", "用量参数2数值": "高清", "首响应耗时": "0.2秒"}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s: got %#v want %#v", key, got[key], value)
		}
	}
	for _, key := range []string{"订阅预扣", "订阅差额", "订阅最终扣费", "订阅剩余", "输入单价", "输出单价", "缓存创建（1小时）单价"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing %s", key)
		}
	}
	text := exportFieldText(commonDetailFields(log, exportOtherMap(log.Other), false, false))
	for _, secret := range []string{"SECRET", "HIDDEN", "map[", "RAW_", "计费长度"} {
		if strings.Contains(text, secret) {
			t.Errorf("unexpected %s in %s", secret, text)
		}
	}
}

func TestCommonExportHidesNonApplicableDetails(t *testing.T) {
	for _, kind := range []int{1, 3, 4, 7} {
		log := &model.Log{Type: kind, PromptTokens: 20, UseTime: 5, IsStream: true, ModelName: "HIDDEN", Quota: 10, Other: `{"audio_input":3,"model_ratio":2,"cache_tokens":10,"task_id":"HIDDEN","reason":"HIDDEN","stream_status":{"status":"ok","errors":["HIDDEN"]},"subscription_post_delta":0}`}
		got := exportTestFields(log, false, false)
		for _, key := range []string{"模型", "费用", "流式", "响应耗时", "输入词元数", "缓存读取词元数", "计费模式", "音频输入词元数", "任务标识", "退款原因", "流错误1", "订阅差额"} {
			if _, ok := got[key]; ok {
				t.Errorf("type %d exposed %s", kind, key)
			}
		}
	}
	log := &model.Log{Type: 2, Other: `{"violation_fee":true,"fee_quota":0,"model_ratio":3,"billing_mode":"tiered_expr","expr_b64":"abc"}`}
	got := exportTestFields(log, false, false)
	if _, ok := got["计费模式"]; ok {
		t.Fatal("violation rendered normal billing")
	}
	if _, ok := got["违规费用"]; !ok {
		t.Fatal("zero violation fee omitted")
	}
}

func TestCommonExportAdminRootViewGate(t *testing.T) {
	log := &model.Log{Type: 2, ChannelId: 9, Other: `{"request_path":"ADMIN_PATH","request_conversion":["openai","anthropic"],"admin_info":{"reject_reason":"ADMIN_REJECT","quota_saturation":{"kind":"overflow","original":"9223372036854775808","clamped":9223372036854775000,"op":"quota"},"task_plugin":{"key":"plugin","name":"插件","author":{"name":"作者","url":"javascript:alert(1)"}}},"root_info":{"upstream_task_id":"ROOT_TASK","node_name":"ROOT_NODE","task_plugin":{"api_version":2,"generation":3}}}`}
	for _, tc := range []struct{ admin, root bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		got := exportTestFields(log, tc.admin, tc.root)
		_, a := got["请求路径"]
		_, r := got["节点名称"]
		if a != tc.admin || r != (tc.admin && tc.root) {
			t.Fatalf("view %+v: %#v", tc, got)
		}
		if _, ok := got["插件作者地址"]; ok {
			t.Fatal("unsafe author URL")
		}
	}
}

func TestExportMoneyMatchesLogDisplay(t *testing.T) {
	setting := operation_setting.GetGeneralSetting()
	saved := *setting
	unit, rate := common.QuotaPerUnit, operation_setting.USDExchangeRate
	t.Cleanup(func() { *setting = saved; common.QuotaPerUnit = unit; operation_setting.USDExchangeRate = rate })
	common.QuotaPerUnit = 500000
	for _, tc := range []struct {
		kind  string
		value float64
		quota bool
		want  string
	}{
		{"USD", 500000, true, "$1"}, {"USD", 1, true, "$0.000002"}, {"USD", 0.001, true, "$0.000001"}, {"USD", -1, true, "-$0.000002"},
		{"CNY", 500000, true, "¥7"}, {"TOKENS", 500000, true, "500000"}, {"TOKENS", 2, false, "$2"}, {"CUSTOM", 500000, true, "¤ 2"},
	} {
		setting.QuotaDisplayType = tc.kind
		setting.CustomCurrencySymbol = "¤"
		setting.CustomCurrencyExchangeRate = 2
		operation_setting.USDExchangeRate = 7
		if got := exportMoney(tc.value, tc.quota); got != tc.want {
			t.Errorf("%+v: %s", tc, got)
		}
	}
}

func TestCommonExportDynamicPricesAndRules(t *testing.T) {
	expr := `v1:(p <= 1000 ? tier("small", p * 2 + c * 4 + cr * 0.5) : tier("large", p * 3 + c * 6 + cr * 1)) * (param("mode") == "fast" ? 2 : 1)`
	o := map[string]any{"billing_mode": "tiered_expr", "expr_b64": base64.StdEncoding.EncodeToString([]byte(expr)), "matched_tier": "small", "billing_tokens": map[string]any{"p": 100}, "request_rules": []any{map[string]any{"cond": `param("mode") == "fast"`, "multiplier": float64(2), "matched": true}}}
	raw, _ := json.Marshal(o)
	got := exportTestFields(&model.Log{Type: 2, PromptTokens: 100, Other: string(raw)}, false, false)
	for _, key := range []string{"匹配档位输入单价", "匹配档位输出单价", "动态档位1输入单价", "动态档位2输出单价", "动态规则1条件", "动态规则1倍率", "动态规则1匹配状态"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing %s in %#v", key, got)
		}
	}
	if _, ok := got["匹配档位缓存读取单价"]; ok {
		t.Fatal("cache price exposed without cache usage")
	}
	if got["动态规则1匹配状态"] != "是" {
		t.Fatal("trace match lost")
	}
	if _, ok := got["计费表达式"]; ok {
		t.Fatal("parsed expression fell back to raw")
	}
}

func TestTaskExportLegacySunoVisibleFields(t *testing.T) {
	task := &model.Task{Status: model.TaskStatusSuccess, Platform: "suno", Data: json.RawMessage(`[{"title":"曲名","tags":"爵士","duration":65.8,"audio_url":"https://media.example/a.mp3","image_url":"https://media.example/a.jpg","secret":"HIDDEN"},{"audio_url":"https://media.example/b.mp3","metadata":{"duration":121,"tags":"古典"}},{"title":"HIDDEN","audio_url":""}]`)}
	got, err := exportTaskArtifacts(task)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"音频1标题：曲名", "音频1标签：爵士", "音频1时长：1:05", "音频1地址：https://media.example/a.mp3", "音频1封面地址：https://media.example/a.jpg", "音频2标题：未命名", "音频2标签：古典", "音频2时长：2:01"} {
		if !strings.Contains(got, part) {
			t.Errorf("missing %s", part)
		}
	}
	if strings.Contains(got, "HIDDEN") {
		t.Fatal("nonvisible task data leaked")
	}
	task.Status = model.TaskStatusFailure
	if got, _ := exportTaskArtifacts(task); got != "" {
		t.Fatal("failed task exposed artifacts")
	}
}

func TestCommonExportSpoolHeaderUnionAndCancel(t *testing.T) {
	spool, err := newLogExportSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer spool.close()
	ctx := context.Background()
	for _, log := range []*model.Log{{Type: 2, PromptTokens: 12, Other: `{"model_ratio":1,"usage_facts":{"seconds":4}}`}, {Type: 6, Other: `{"task_id":"task","reason":"=SUM(A1)"}`}} {
		fields := commonDetailFields(log, exportOtherMap(log.Other), false, false)
		if err := spool.append(ctx, fields); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "export.xlsx")
	_, err = common.BuildXLSXStreamFile(path, "测试", 1, 2, spool.rows, common.XLSXStreamSheet{Name: "日志", Headers: spool.headers, Context: ctx, WriteRowsChecked: func(w *common.XLSXRowWriter) error { return spool.write(ctx, w) }})
	if err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	rows, err := book.GetRows("日志")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows=%d", len(rows))
	}
	indices := map[string]int{}
	for i, h := range rows[0] {
		indices[h] = i
	}
	for _, h := range []string{"输入词元数", "计费模式", "用量参数1名称", "任务标识", "退款原因"} {
		if _, ok := indices[h]; !ok {
			t.Errorf("missing %s", h)
		}
	}
	column, _ := excelize.CoordinatesToCellName(indices["退款原因"]+1, 3)
	if formula, _ := book.GetCellFormula("日志", column); formula != "" {
		t.Fatal("formula executed")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := spool.append(cancelled, nil); err == nil {
		t.Fatal("cancellation ignored")
	}
	name := spool.file.Name()
	spool.close()
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatal("spool not removed")
	}
}
