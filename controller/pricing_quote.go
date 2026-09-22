package controller

import (
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/service"
	"github.com/shopspring/decimal"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/expr-lang/expr/ast"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type QuoteUnitRate struct {
	Dimension   string  `json:"dimension"`
	Quota       float64 `json:"quota"`
	Per         float64 `json:"per"`
	Unit        string  `json:"unit"`
	Conditional bool    `json:"conditional,omitempty"`
}

type PricingQuote struct {
	Status          string          `json:"status"`
	Quota           *int            `json:"quota"`
	QuotaPerUnit    float64         `json:"quota_per_unit"`
	Group           string          `json:"group"`
	Model           string          `json:"model"`
	BillingMode     string          `json:"billing_mode"`
	BillingRevision string          `json:"billing_revision"`
	BatchCount      int             `json:"batch_count"`
	MissingFields   []string        `json:"missing_fields"`
	UnitRates       []QuoteUnitRate `json:"unit_rates"`
	Message         string          `json:"message"`
}

// Quote prices only the selected route. It never dispatches Relay, opens a
// billing session, submits a task, or treats a pre-consume reserve as a price.
func Quote(c *gin.Context) {
	result := PricingQuote{Status: "unavailable", QuotaPerUnit: common.QuotaPerUnit,
		Group: common.GetContextKeyString(c, constant.ContextKeyUsingGroup), Model: common.GetContextKeyString(c, constant.ContextKeyOriginalModel),
		BatchCount: c.GetInt(middleware.QuoteBatchCountKey), MissingFields: []string{}, UnitRates: []QuoteUnitRate{}}
	if result.BatchCount < 1 || result.BatchCount > 20 {
		c.JSON(400, gin.H{"success": false, "message": "invalid quote batch"})
		return
	}
	respond := func() { c.JSON(http.StatusOK, gin.H{"success": true, "data": result}) }
	unavailable := func(message string) { result.Message = message; respond() }
	invalid := func(message string) { c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": message}) }
	format := types.RelayFormatOpenAI
	switch c.Request.URL.Path {
	case "/v1/images/generations", "/v1/images/edits":
		format = types.RelayFormatOpenAIImage
	case "/v1/audio/speech":
		format = types.RelayFormatOpenAIAudio
	case "/v1/responses":
		format = types.RelayFormatOpenAIResponses
	case "/v1/videos", "/api/v3/contents/generations/tasks":
		format = types.RelayFormatTask
	}
	_, pinned := c.Get(jsplugin.ContextKeyPinnedEndpoint)
	if pinned || common.GetContextKeyInt(c, constant.ContextKeyChannelType) == constant.ChannelTypeTaskPlugin {
		format = types.RelayFormatTask
	}
	var request dto.Request
	var err error
	if format != types.RelayFormatTask {
		request, err = helper.GetAndValidateRequest(c, format)
		if err != nil {
			invalid(err.Error())
			return
		}
	}
	info, err := relaycommon.GenRelayInfo(c, format, request, nil)
	if err != nil {
		unavailable("quote route metadata unavailable")
		return
	}
	info.InitChannelMeta(c)
	if len(info.ParamOverride) > 0 || len(info.HeadersOverride) > 0 {
		unavailable("selected route has parameter/header overrides; effective billing inputs are unknown")
		return
	}
	if format == types.RelayFormatTask {
		if !pinned {
			unavailable("task quote unknown: safe submission metadata or billing facts unavailable")
			return
		}
		taskQuote, taskErr := relay.QuoteTaskSubmission(c, info)
		if taskErr != nil {
			if taskErr.Code == "task_quote_unknown" {
				unavailable("task quote unknown: safe submission metadata or billing facts unavailable")
				return
			}
			status := taskErr.StatusCode
			if status != 400 && status != 401 && status != 403 {
				status = 503
			}
			c.JSON(status, gin.H{"success": false, "message": "task quote preparation failed", "code": taskErr.Code})
			return
		}
		result.Group = taskQuote.Group
		result.Model = taskQuote.Model
		result.BillingMode = taskQuote.BillingMode
		result.BillingRevision = taskQuote.ExpressionHash
		if len(taskQuote.MissingFields) > 0 {
			result.MissingFields = taskQuote.MissingFields
			result.Status = "usage_required"
			result.Message = "Task settlement usage is required; no upstream request was made. The selected route/group may change on a real retry."
			respond()
			return
		}
		total := taskQuote.Quota * result.BatchCount
		result.Quota = &total
		result.Status = "estimated"
		result.Message = "Each task is rounded before multiplying batch_count; final usage and retry route may change the charge."
		respond()
		return
	}
	meta := request.GetTokenCountMeta()
	price, priceErr := helper.ModelPriceHelper(c, info, 0, meta)
	result.Group = info.UsingGroup
	result.BillingMode = billing_setting.GetBillingMode(info.GetBillingModelName())
	if snapshot := info.TieredBillingSnapshot; snapshot != nil {
		result.BillingRevision = snapshot.ExprHash
	}
	// Runtime tool calls can add charges even to a fixed-price expression.
	// Request declarations do not establish how many calls will be billed.
	body, bodyErr := common.Marshal(request)
	if bodyErr != nil {
		unavailable("quote request metadata unavailable")
		return
	}
	toolsUnknown := strings.HasSuffix(info.GetBillingModelName(), "search-preview")
	for _, field := range []string{"tools", "functions", "web_search_options"} {
		value := gjson.GetBytes(body, field)
		if value.Exists() && value.Raw != "[]" {
			toolsUnknown = true
		}
	}
	if toolsUnknown {
		result.Status = "usage_required"
		result.MissingFields = []string{"usage.tool_calls"}
		result.Message = "Actual tool calls and their surcharges are unknown; a total charge cannot be quoted before settlement."
		respond()
		return
	}
	// Validate expression dependencies even if pre-consume failed because a
	// request parameter is absent. Zero-filled token estimates are never facts.
	if result.BillingMode == billing_setting.BillingModeTieredExpr {
		expression, ok := billing_setting.GetBillingExpr(info.GetBillingModelName())
		if !ok {
			unavailable("billing expression is not configured")
			return
		}
		result.BillingRevision = billingexpr.ExprHashString(expression)
		if _, err := billingexpr.CompileFromCache(expression); err != nil {
			unavailable("billing expression is invalid")
			return
		}
		input, inputErr := helper.ResolveIncomingBillingExprRequestInput(c, info)
		if inputErr != nil {
			invalid("invalid billing request")
			return
		}
		if billingexpr.UsedVars(expression)["image_count"] {
			input, inputErr = helper.ResolveImageBillingRequestInput(c, info, input)
			if inputErr != nil {
				invalid(inputErr.Error())
				return
			}
		}
		result.MissingFields = quoteExpressionMissing(expression, input)
		if len(result.MissingFields) > 0 {
			result.Status = "usage_required"
			result.UnitRates = quoteLinearTokenRates(expression, info.ResolveGroupRatio().GroupRatio)
			result.Message = "Actual settlement dimensions are missing. Unit rates, when available, are ledger quota per token; this is not a reservation or a final charge."
			respond()
			return
		}
		if priceErr != nil {
			unavailable("billing expression could not be evaluated for this request")
			return
		}
		settled, err := billingexpr.ComputeTieredQuotaWithRequest(info.TieredBillingSnapshot, billingexpr.TokenParams{}, input)
		if err != nil || settled.Clamp != nil {
			unavailable("billing expression quote is unavailable or outside the supported quota range")
			return
		}
		total := settled.ActualQuotaAfterGroup * result.BatchCount
		result.Quota = &total
		result.Status = "estimated"
	} else {
		if priceErr != nil {
			unavailable("model pricing is not configured or exceeds the supported quota range")
			return
		}
		revision, _ := common.Marshal(struct {
			Price any
			Unit  float64
			Model string
		}{price, common.QuotaPerUnit, info.GetBillingModelName()})
		result.BillingRevision = billingexpr.ExprHashString(string(revision))
		if price.UsePrice {
			result.BillingMode = "per_call"
			if format == types.RelayFormatOpenAIAudio {
				// AudioHelper selects PostAudioConsumeQuota or text settlement from
				// returned audio tokens. Their fixed-price semantics differ.
				result.Status = "usage_required"
				result.MissingFields = []string{"usage.audio_input_tokens", "usage.audio_output_tokens"}
				result.Message = "Audio usage determines the settlement path; fixed text pricing cannot establish the audio charge."
				respond()
				return
			}
			// Legacy images retain their request-aware truncation (size/quality/n).
			perRequest := price.QuotaToPreConsume
			if format != types.RelayFormatOpenAIImage {
				var clamp *common.QuotaClamp
				perRequest, clamp = service.CalculateFixedTextQuota(price, common.QuotaPerUnit, decimal.Zero, true)
				if clamp != nil {
					unavailable("fixed text quote exceeds the supported quota range")
					return
				}
			}
			total := perRequest * result.BatchCount
			result.Quota = &total
			result.Status = "estimated"
			result.UnitRates = []QuoteUnitRate{{Dimension: "request", Quota: float64(perRequest), Per: 1, Unit: "request", Conditional: true}}
		} else {
			result.BillingMode = "ratio"
			result.Status = "usage_required"
			result.MissingFields = []string{"usage.input_tokens", "usage.output_tokens"}
			base := price.ModelRatio * price.GroupRatioInfo.GroupRatio
			result.UnitRates = []QuoteUnitRate{{Dimension: "input_tokens", Quota: base, Per: 1, Unit: "token"}, {Dimension: "output_tokens", Quota: base * price.CompletionRatio, Per: 1, Unit: "token"}, {Dimension: "cache_read_tokens", Quota: base * price.CacheRatio, Per: 1, Unit: "token"}, {Dimension: "image_input_tokens", Quota: base * price.ImageRatio, Per: 1, Unit: "token"}}
			if format == types.RelayFormatOpenAIAudio {
				result.UnitRates = []QuoteUnitRate{{Dimension: "input_tokens", Quota: base, Per: 1, Unit: "token"}, {Dimension: "audio_input_tokens", Quota: base * price.AudioRatio, Per: 1, Unit: "token"}, {Dimension: "audio_output_tokens", Quota: base * price.AudioRatio * price.AudioCompletionRatio, Per: 1, Unit: "token"}}
			}
		}
	}
	result.Message = "Quota is gateway ledger units for a successful billable request. Each request uses its settlement rounding before multiplying batch_count. Missing billable usage or failure may result in zero charge. Actual usage and retry route/group can change the final charge; convert points as quota / quota_per_unit * 10."
	respond()
}

var quoteTokenDimensions = map[string]string{"p": "input_tokens", "c": "output_tokens", "len": "context_tokens", "cr": "cache_read_tokens", "cc": "cache_creation_tokens", "cc1h": "cache_creation_1h_tokens", "img": "image_input_tokens", "img_cr": "image_cache_tokens", "img_o": "image_output_tokens", "ai": "audio_input_tokens", "ao": "audio_output_tokens", "vs": "video_seconds"}

// Inspect the existing compiled AST; do not evaluate absent facts as zeros.
func quoteExpressionMissing(expression string, input billingexpr.RequestInput) []string {
	missing := map[string]bool{}
	used := billingexpr.UsedVars(expression)
	for variable, dimension := range quoteTokenDimensions {
		if used[variable] {
			missing["usage."+dimension] = true
		}
	}
	for key := range billingexpr.UsedUsageKeys(expression) {
		missing["usage."+key] = true
	}
	if used["u"] && len(billingexpr.UsedUsageKeys(expression)) == 0 {
		missing["usage"] = true
	}
	program, err := billingexpr.CompileFromCache(expression)
	if err == nil {
		ast.Find(program.Node(), func(node ast.Node) bool {
			call, ok := node.(*ast.CallNode)
			if !ok || len(call.Arguments) != 1 {
				return false
			}
			identifier, ok := call.Callee.(*ast.IdentifierNode)
			if !ok || identifier.Value != "param" {
				return false
			}
			path, ok := call.Arguments[0].(*ast.StringNode)
			if !ok {
				missing["request.dynamic_parameter"] = true
			} else if !gjson.GetBytes(input.Body, path.Value).Exists() {
				missing["body."+path.Value] = true
			}
			return false
		})
	}
	fields := make([]string, 0, len(missing))
	for field := range missing {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

// Only expose coefficients when the compiled expression is a plain linear
// token sum. Conditional/nonlinear/task prices remain an indivisible expression.
func quoteLinearTokenRates(expression string, groupRatio float64) []QuoteUnitRate {
	rates := []QuoteUnitRate{}
	program, err := billingexpr.CompileFromCache(expression)
	if err != nil {
		return rates
	}
	if !quoteIsLinearTokenSum(program.Node()) {
		return rates
	}
	baseline, _, err := billingexpr.RunExpr(expression, billingexpr.TokenParams{})
	if err != nil || baseline != 0 {
		return rates
	}
	samples := map[string]billingexpr.TokenParams{"p": {P: 1}, "c": {C: 1}, "cr": {CR: 1}, "cc": {CC: 1}, "cc1h": {CC1h: 1}, "img": {Img: 1}, "img_cr": {ImgCR: 1}, "img_o": {ImgO: 1}, "ai": {AI: 1}, "ao": {AO: 1}}
	used := billingexpr.UsedVars(expression)
	for name, params := range samples {
		if !used[name] {
			continue
		}
		value, _, err := billingexpr.RunExpr(expression, params)
		if err != nil {
			return []QuoteUnitRate{}
		}
		rates = append(rates, QuoteUnitRate{Dimension: quoteTokenDimensions[name], Quota: value / 1e6 * common.QuotaPerUnit * groupRatio, Per: 1, Unit: "token"})
	}
	sort.Slice(rates, func(i, j int) bool { return rates[i].Dimension < rates[j].Dimension })
	return rates
}

func quoteIsLinearTokenSum(node ast.Node) bool {
	switch n := node.(type) {
	case *ast.IntegerNode, *ast.FloatNode:
		return true
	case *ast.IdentifierNode:
		return quoteTokenDimensions[n.Value] != "" && n.Value != "len" && n.Value != "vs"
	case *ast.BinaryNode:
		if n.Operator == "+" {
			return quoteIsLinearTokenSum(n.Left) && quoteIsLinearTokenSum(n.Right)
		}
		if n.Operator == "*" {
			_, leftInt := n.Left.(*ast.IntegerNode)
			_, leftFloat := n.Left.(*ast.FloatNode)
			_, rightInt := n.Right.(*ast.IntegerNode)
			_, rightFloat := n.Right.(*ast.FloatNode)
			return ((leftInt || leftFloat) && quoteIsLinearTokenSum(n.Right)) || ((rightInt || rightFloat) && quoteIsLinearTokenSum(n.Left))
		}
	case *ast.CallNode:
		id, ok := n.Callee.(*ast.IdentifierNode)
		return ok && id.Value == "tier" && len(n.Arguments) == 2 && quoteIsLinearTokenSum(n.Arguments[1])
	}
	return false
}
