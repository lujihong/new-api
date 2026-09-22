package relay

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	jspluginadaptor "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/gin-gonic/gin"
)

// TaskSubmissionQuote describes one task's requested reservation, not its final
// settlement or the wallet/subscription funding decision. MissingFields means
// the quota is only a reservation estimate; public callers must display
// usage_required with a null amount instead of treating it as a known cost.
// Explicit zero is retained. No request payload or credentials are returned.
type TaskSubmissionQuote struct {
	Quota          int      `json:"quota"`
	QuotaPerUnit   float64  `json:"quota_per_unit"`
	Group          string   `json:"group"`
	Model          string   `json:"model"`
	BillingMode    string   `json:"billing_mode"`
	ExpressionHash string   `json:"expression_hash"`
	Estimated      bool     `json:"estimated"`
	MissingFields  []string `json:"missing_fields,omitempty"`
}

// QuoteTaskSubmission requires the same authenticated, distributed context as
// submit: original path/method/body, channel metadata and settings, model mapping,
// user/using group (including auto_group), and the exact plugin/route/protocol pin
// plus its decoded task_request/action. Construct fresh info with
// relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil); preserve the submit
// controller's resolved model/action and origin-task preparation when applicable.
// Both c and info are request-local and mutated during preparation; do not reuse
// them for submission. This function never performs billing or task DB writes.
func QuoteTaskSubmission(c *gin.Context, info *relaycommon.RelayInfo) (*TaskSubmissionQuote, *dto.TaskError) {
	if c == nil || c.Request == nil || c.Request.URL == nil || info == nil || info.TaskRelayInfo == nil {
		return nil, service.TaskErrorWrapperLocal(errors.New("task quote context is incomplete"), "invalid_request", http.StatusBadRequest)
	}
	if info.Billing != nil || info.TieredBillingSnapshot != nil || info.QuotaClamp != nil {
		return nil, service.TaskErrorWrapperLocal(errors.New("task quote requires fresh relay info"), "invalid_request", http.StatusBadRequest)
	}

	// Resolve once and pin the exact object before shared preparation. Never
	// infer a driver from a price/model alone, including unpinned dynamic routes.
	platform := constant.TaskPlatform(c.GetString("platform"))
	if platform == "" {
		platform = GetTaskPlatform(c)
	}
	_, adaptor := getTaskAdaptorForRequest(c, platform)
	if _, safe := adaptor.(*jspluginadaptor.TaskAdaptor); !safe {
		return nil, service.TaskErrorWrapperLocal(errors.New("task quote support is unknown for this adaptor"), "task_quote_unknown", http.StatusUnprocessableEntity)
	}
	var plugin *jsplugin.LoadedPlugin
	for _, key := range []string{jsplugin.ContextKeyPinnedPlugin, jsplugin.ContextKeyPinnedEndpoint, jsplugin.ContextKeyPinnedRoute} {
		value, exists := c.Get(key)
		if !exists {
			continue
		}
		switch pinned := value.(type) {
		case jsplugin.PinnedPlugin:
			plugin = pinned.Plugin
		case jsplugin.PinnedEndpoint:
			plugin = pinned.Plugin
		case jsplugin.PinnedRoute:
			plugin = pinned.Plugin
		}
		break
	}
	if plugin == nil {
		return nil, service.TaskErrorWrapperLocal(errors.New("task quote requires a resolved plugin"), "task_quote_unknown", http.StatusUnprocessableEntity)
	}
	// Sobek globals only expose bounded local crypto/JSON/time/log helpers;
	// request URL validation does no DNS or HTTP. However submitContext calls
	// resolveAuth, whose oauth2_jwt path fetches an access token. Reject it even
	// if cached. Asset authorization performs DB reads only. New auth types or
	// Go adaptors need an explicit audit before joining this allowlist.
	switch strings.TrimSpace(plugin.Meta.Auth.Type) {
	case "", "none", "api_key":
	default:
		return nil, service.TaskErrorWrapperLocal(errors.New("task quote requires network credential resolution"), "task_quote_unknown", http.StatusUnprocessableEntity)
	}

	_, _, taskErr := prepareTaskSubmission(c, info)
	if taskErr != nil {
		// Hooks and URL errors can echo credentials or request content. Keep the
		// status/code but never propagate arbitrary plugin text or error data.
		return nil, service.TaskErrorWrapperLocal(errors.New("task quote validation or pricing failed"), taskErr.Code, taskErr.StatusCode)
	}
	if info.QuotaClamp != nil || info.PriceData.Quota < 0 {
		return nil, service.TaskErrorWrapperLocal(errors.New("task quota is out of range"), "model_price_error", http.StatusBadRequest)
	}
	quote := &TaskSubmissionQuote{
		Quota: info.PriceData.Quota, QuotaPerUnit: common.QuotaPerUnit,
		Group: info.UsingGroup, Model: info.OriginModelName,
		BillingMode: billing_setting.BillingModeRatio, Estimated: true,
	}
	if snapshot := info.TieredBillingSnapshot; snapshot != nil {
		quote.BillingMode = snapshot.BillingMode
		quote.ExpressionHash = snapshot.ExprHash
		missing := map[string]bool{}
		for key := range billingexpr.UsedUsageKeys(snapshot.ExprString) {
			value, exists := snapshot.UsageFacts[key]
			if !exists || value == nil {
				missing[key] = true
				continue
			}
			// A plugin may coerce absent dimensions to zero. Only an explicit
			// matching request scalar establishes that a zero was intentional.
			if numeric, ok := value.(float64); ok && numeric == 0 {
				request, _ := c.Get("task_request")
				body, _ := request.(map[string]any)
				original, present := body[key]
				if !present || !explicitQuoteZero(original) {
					missing[key] = true
				}
			}
		}
		schema, _ := plugin.Meta.UsageForModel(info.UpstreamModelName)
		usedUsage := billingexpr.UsedUsageKeys(snapshot.ExprString)
		for key, field := range schema {
			// Only dimensions used by this price can prevent an estimate.
			// Token/credit quantities require actual settlement usage.
			if usedUsage[key] && (field.Unit == "token" || field.Unit == "credit") {
				if key == "tokens" && plugin.Meta.Key == "doubao" && quoteHasExplicitDoubaoOutput(snapshot) {
					continue
				}
				missing[key] = true
			}
		}
		for key := range missing {
			quote.MissingFields = append(quote.MissingFields, key)
		}
		sort.Strings(quote.MissingFields)
	} else if !info.PriceData.UsePrice {
		quote.MissingFields = []string{"usage"}
	}
	return quote, nil
}

// The shipped Doubao meter estimates output tokens from explicit duration and
// resolution. It omits reference-video duration; never expose that as a full quote.
func quoteHasExplicitDoubaoOutput(snapshot *billingexpr.BillingSnapshot) bool {
	if snapshot.UsageFacts["video_input"] != "none" {
		return false
	}
	resolution, _ := snapshot.UsageFacts["resolution"].(string)
	if resolution != "480p" && resolution != "720p" && resolution != "1080p" {
		return false
	}
	params := snapshot.TaskRequestParams
	if params["resolution"] != resolution && params["resolution_name"] != resolution {
		return false
	}
	for _, key := range []string{"seconds", "duration"} {
		value := params[key]
		var seconds float64
		switch v := value.(type) {
		case float64:
			seconds = v
		case int:
			seconds = float64(v)
		case string:
			seconds, _ = strconv.ParseFloat(v, 64)
		}
		if seconds > 0 && seconds <= 15 {
			pixels := map[string]float64{"480p": 854 * 480, "720p": 1280 * 720, "1080p": 1920 * 1080}[resolution]
			tokens, ok := snapshot.UsageFacts["tokens"].(float64)
			return ok && tokens == seconds*pixels*24/1024
		}
	}
	return false
}

func explicitQuoteZero(value any) bool {
	switch v := value.(type) {
	case float64:
		return v == 0
	case int:
		return v == 0
	case int64:
		return v == 0
	case json.Number:
		n, err := v.Float64()
		return err == nil && n == 0
	case string:
		if strings.TrimSpace(v) == "" {
			return false
		}
		n, err := strconv.ParseFloat(v, 64)
		return err == nil && n == 0
	}
	return false
}
