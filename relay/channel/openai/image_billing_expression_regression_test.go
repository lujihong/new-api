package openai

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

const (
	imageRegressionOldExpr = `tier("base", p * 5 + img_o * 30 + img * 8 + cr * 1.25)`
	imageRegressionNewExpr = `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`
	imageRegressionQPU     = 500000.0
)

// These are synthetic contract fixtures, not captured upstream responses or
// evidence of official prices. The base fixture uses Images usage fields;
// cache modality details are a compatible-provider extension, as in the
// existing image_stream_test.go fixtures. Images output_tokens without explicit
// completion details must map to image output for expressions using img_o.
func TestImageBillingExpressionRegression(t *testing.T) {
	for _, vector := range []struct {
		name      string
		response  string
		cached    int
		oldParams billingexpr.TokenParams
		newParams billingexpr.TokenParams
		oldAmount float64
		newAmount float64
		oldQuotas [3]int // ratios 0, 0.5, 1; round only at final settlement
		newQuotas [3]int
	}{
		{
			name:      "no_cache",
			response:  `{"created":1710000000,"data":[{"b64_json":"aW1hZ2U="}],"usage":{"input_tokens":300,"output_tokens":1000,"total_tokens":1300,"input_tokens_details":{"text_tokens":100,"image_tokens":200}}}`,
			oldParams: billingexpr.TokenParams{P: 100, Len: 300, Img: 200, ImgO: 1000},
			newParams: billingexpr.TokenParams{P: 100, C: 1000, Len: 300, Img: 200, ImgO: 1000},
			oldAmount: 0.032100,
			newAmount: 0.032100,
			oldQuotas: [3]int{0, 8025, 16050},
			newQuotas: [3]int{0, 8025, 16050},
		},
		{
			name:     "valid_mixed_cache",
			response: `{"created":1710000000,"data":[{"b64_json":"aW1hZ2U="}],"usage":{"input_tokens":300,"output_tokens":1000,"total_tokens":1300,"input_tokens_details":{"text_tokens":100,"image_tokens":200,"cached_tokens":60,"cached_tokens_details":{"text_tokens":20,"image_tokens":40}}}}`,
			cached:   60,
			// After splitting the cache: P = 300 - 20 - 160 - 40 = 80.
			newParams: billingexpr.TokenParams{P: 80, C: 1000, Len: 300, CR: 20, Img: 160, ImgCR: 40, ImgO: 1000},
			newAmount: 0.031785,
			newQuotas: [3]int{0, 7946, 15893},
		},
	} {
		t.Run(vector.name, func(t *testing.T) {
			usage := decodeImageBillingRegressionUsage(t, vector.response)
			require.Equal(t, vector.cached, usage.PromptTokensDetails.CachedTokens)
			require.Equal(t, 1000, usage.CompletionTokenDetails.ImageTokens)
			require.Zero(t, usage.CompletionTokenDetails.TextTokens)
			if vector.cached != 0 {
				details := usage.PromptTokensDetails.CachedTokensDetails
				require.NotNil(t, details)
				require.NotNil(t, details.TextTokens)
				require.NotNil(t, details.ImageTokens)
				require.Equal(t, 20, *details.TextTokens)
				require.Equal(t, 40, *details.ImageTokens)
				require.Equal(t, vector.cached, *details.TextTokens+*details.ImageTokens)
			}

			for _, expression := range []struct {
				name, expr, tier string
				params           billingexpr.TokenParams
				amount           float64
				quotas           [3]int
			}{
				{"old_public", imageRegressionOldExpr, "base", vector.oldParams, vector.oldAmount, vector.oldQuotas},
				{"new_builtin", imageRegressionNewExpr, "standard", vector.newParams, vector.newAmount, vector.newQuotas},
			} {
				t.Run(expression.name, func(t *testing.T) {
					// UsedVars compiles the real expression; do not substitute a hand-built map.
					usedVars := billingexpr.UsedVars(expression.expr)
					require.NotEmpty(t, usedVars)
					require.True(t, usedVars["p"] && usedVars["img"] && usedVars["cr"])
					require.Equal(t, expression.name == "old_public", usedVars["img_o"])
					require.Equal(t, expression.name == "new_builtin", usedVars["c"])
					require.Equal(t, expression.name == "new_builtin", usedVars["img_cr"])
					params := service.BuildTieredTokenParams(usage, false, usedVars)
					if expression.name == "old_public" {
						require.Zero(t, params.C)
						require.Equal(t, float64(1000), params.ImgO)
						withOutput := runImageRegressionExpression(t, expression.expr, params, 1)
						withoutOutput := params
						withoutOutput.ImgO = 0
						inputOnly := runImageRegressionExpression(t, expression.expr, withoutOutput, 1)
						require.InDelta(t, 30000, (withOutput.ActualQuotaBeforeGroup-inputOnly.ActualQuotaBeforeGroup)*1e6/imageRegressionQPU, 1e-9)
						if vector.cached != 0 {
							// The old expression does not reference img_cr. Keep the
							// current cache policy; assert only the output contribution.
							return
						}
					}
					require.Equal(t, expression.params, params)

					for i, ratio := range []float64{0, 0.5, 1} {
						t.Run(fmt.Sprintf("ratio_%g", ratio), func(t *testing.T) {
							result := runImageRegressionExpression(t, expression.expr, params, ratio)
							// Check the unrounded amount independently of the final integer quota.
							require.InDelta(t, expression.amount, result.ActualQuotaBeforeGroup/imageRegressionQPU, 1e-12)
							require.Equal(t, expression.quotas[i], result.ActualQuotaAfterGroup,
								"apply the ratio once, then round (do not round before discounting)")
							require.Equal(t, expression.tier, result.MatchedTier)
							t.Logf("amount_before_group=%.6f ratio=%g final_quota=%d", result.ActualQuotaBeforeGroup/imageRegressionQPU, ratio, result.ActualQuotaAfterGroup)
						})
					}
				})
			}
		})
	}
}

// A compatible supplier may explicitly return completion_tokens_details. This
// is a separate contract, not a claimed standard Images response field. The
// mixed-output and explicit-zero cases ensure supplier details take precedence.
func TestImageBillingCompletionDetailsAreOptIn(t *testing.T) {
	for _, fixture := range []struct {
		name        string
		details     string
		imageTokens int
		textTokens  int
		oldAmount   float64
		oldQuota    int
	}{
		{"absent", "", 1000, 0, 0.032100, 16050},
		{"explicit_empty", `,"completion_tokens_details":{}`, 0, 0, 0.002100, 1050},
		{"explicit_all_zero", `,"completion_tokens_details":{"text_tokens":0,"image_tokens":0}`, 0, 0, 0.002100, 1050},
		{"explicit_zero_image", `,"completion_tokens_details":{"text_tokens":1000,"image_tokens":0}`, 0, 1000, 0.002100, 1050},
		{"mixed_output", `,"completion_tokens_details":{"text_tokens":750,"image_tokens":250}`, 250, 750, 0.009600, 4800},
		{"all_image_explicit", `,"completion_tokens_details":{"text_tokens":0,"image_tokens":1000}`, 1000, 0, 0.032100, 16050},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			body := `{"data":[{"b64_json":"aW1hZ2U="}],"usage":{"input_tokens":300,"output_tokens":1000,"input_tokens_details":{"text_tokens":100,"image_tokens":200}` + fixture.details + `}}`
			usage := decodeImageBillingRegressionUsage(t, body)
			require.Equal(t, fixture.imageTokens, usage.CompletionTokenDetails.ImageTokens)
			require.Equal(t, fixture.textTokens, usage.CompletionTokenDetails.TextTokens)
			oldParams := service.BuildTieredTokenParams(usage, false, billingexpr.UsedVars(imageRegressionOldExpr))
			require.Equal(t, float64(1000-fixture.imageTokens), oldParams.C)
			require.Equal(t, float64(fixture.imageTokens), oldParams.ImgO)
			oldResult := runImageRegressionExpression(t, imageRegressionOldExpr, oldParams, 1)
			require.InDelta(t, fixture.oldAmount, oldResult.ActualQuotaBeforeGroup/imageRegressionQPU, 1e-12)
			require.Equal(t, fixture.oldQuota, oldResult.ActualQuotaAfterGroup)

			// Without an img_o reference the builtin expression keeps the full
			// completion count in c, even when supplier modality details exist.
			newParams := service.BuildTieredTokenParams(usage, false, billingexpr.UsedVars(imageRegressionNewExpr))
			require.Equal(t, float64(1000), newParams.C)
			require.Equal(t, float64(fixture.imageTokens), newParams.ImgO)
			newResult := runImageRegressionExpression(t, imageRegressionNewExpr, newParams, 1)
			require.InDelta(t, 0.032100, newResult.ActualQuotaBeforeGroup/imageRegressionQPU, 1e-12)
			require.Equal(t, 16050, newResult.ActualQuotaAfterGroup)
		})
	}
}

func TestImageBillingOutputMappingAcrossResponseFormats(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	for _, endpoint := range []string{"generations", "edits"} {
		for _, format := range []string{"json", "sse", "json_as_sse"} {
			for _, fixture := range []struct {
				name, usage                                           string
				imageTokens, textTokens, audioTokens, reasoningTokens int
			}{
				{"absent_details", `{"input_tokens":300,"output_tokens":1000}`, 1000, 0, 0, 0},
				{"explicit_zero", `{"input_tokens":300,"output_tokens":1000,"completion_tokens_details":{"image_tokens":0}}`, 0, 0, 0, 0},
				{"mixed_details", `{"input_tokens":300,"output_tokens":1000,"completion_tokens_details":{"image_tokens":250,"text_tokens":700,"audio_tokens":50,"reasoning_tokens":10}}`, 250, 700, 50, 10},
				{"missing_usage", `null`, 0, 0, 0, 0},
			} {
				t.Run(endpoint+"/"+format+"/"+fixture.name, func(t *testing.T) {
					body := `{"data":[{"b64_json":"image"}],"usage":` + fixture.usage + `}`
					contentType := "application/json"
					if format == "sse" {
						event := "image_generation.completed"
						if endpoint == "edits" {
							event = "image_edit.completed"
						}
						body = "data: {\"type\":\"" + event + "\",\"usage\":" + fixture.usage + "}\n\ndata: [DONE]\n\n"
						contentType = "text/event-stream"
					}
					ctx, _, response, info := newImageTestContext(t, body, contentType, format != "json")
					ctx.Request.URL.Path = "/v1/images/" + endpoint
					info.RelayMode = relayconstant.RelayModeImagesGenerations
					if endpoint == "edits" {
						info.RelayMode = relayconstant.RelayModeImagesEdits
					}
					result, apiErr := (&Adaptor{}).DoResponse(ctx, response, info)
					require.Nil(t, apiErr)
					usage := result.(*dto.Usage)
					require.Equal(t, dto.OutputTokenDetails{
						ImageTokens: fixture.imageTokens, TextTokens: fixture.textTokens,
						AudioTokens: fixture.audioTokens, ReasoningTokens: fixture.reasoningTokens,
					}, usage.CompletionTokenDetails)
					if fixture.name == "missing_usage" {
						require.Zero(t, usage.CompletionTokens, "normalization cannot reconstruct missing usage")
						return
					}
					require.Equal(t, 1000, usage.CompletionTokens)
					params := service.BuildTieredTokenParams(usage, false, billingexpr.UsedVars(imageRegressionOldExpr))
					require.Equal(t, float64(fixture.imageTokens), params.ImgO)
					require.Equal(t, float64(1000-fixture.imageTokens), params.C)
				})
			}
		}
	}
}

func decodeImageBillingRegressionUsage(t *testing.T, body string) *dto.Usage {
	t.Helper()
	var response dto.SimpleResponse
	// Match OpenaiImageHandler's decoder and DTO, including the nested usage key.
	require.NoError(t, common.Unmarshal([]byte(body), &response))
	require.Equal(t, 300, response.InputTokens)
	require.Equal(t, 1000, response.OutputTokens)
	require.Zero(t, response.PromptTokens)
	require.Zero(t, response.CompletionTokens)
	require.NotNil(t, response.InputTokensDetails)
	normalizeOpenAIUsage(&response.Usage, []byte(body))
	require.Equal(t, 300, response.PromptTokens)
	require.Equal(t, 1000, response.CompletionTokens)
	require.Equal(t, 1300, response.TotalTokens)
	require.Equal(t, 100, response.PromptTokensDetails.TextTokens)
	require.Equal(t, 200, response.PromptTokensDetails.ImageTokens)
	return &response.Usage
}

func runImageRegressionExpression(t *testing.T, expression string, params billingexpr.TokenParams, ratio float64) billingexpr.TieredResult {
	t.Helper()
	snapshot := &billingexpr.BillingSnapshot{
		BillingMode:  "tiered_expr",
		ExprVersion:  1,
		ExprString:   expression,
		ExprHash:     billingexpr.ExprHashString(expression),
		GroupRatio:   ratio,
		QuotaPerUnit: imageRegressionQPU,
	}
	result, err := billingexpr.ComputeTieredQuotaWithRequest(snapshot, params, billingexpr.RequestInput{})
	require.NoError(t, err)
	return result
}
