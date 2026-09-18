package jsplugin

import (
	"bytes"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ channel.TaskBillingRequestParametersProvider = (*TaskAdaptor)(nil)

func TestTaskBillingRequestParametersParsedContext(t *testing.T) {
	for _, contentType := range []string{"json", "multipart"} {
		t.Run(contentType, func(t *testing.T) {
			source := strings.Replace(mockPlugin, `export function extractUsage(ctx) { return {seconds: 5, mode: "pro"}; }`, `export function extractUsage(ctx) { return {seconds: Number(ctx.requestBody.seconds)}; }`, 1)
			plugin, err := pluginruntime.NewRegistry().Register(source, pluginruntime.Options{})
			require.NoError(t, err)
			adaptor := New(plugin)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://provider.example"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
			adaptor.Init(info)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			fields := map[string]any{"resolution": "480p", "resolution_name": "1080p", "seconds": "5", "duration": "5", "size": "720x1280", "aspect_ratio": "9:16", "prompt": "secret", "url": "https://secret.example", "headers": map[string]any{"Authorization": "secret"}}
			if contentType == "json" {
				wire, err := common.Marshal(fields)
				require.NoError(t, err)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(wire))
				c.Request.Header.Set("Content-Type", "application/json")
			} else {
				var input bytes.Buffer
				writer := multipart.NewWriter(&input)
				for key, value := range fields {
					if text, ok := value.(string); ok {
						require.NoError(t, writer.WriteField(key, text))
					}
				}
				require.NoError(t, writer.Close())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", &input)
				c.Request.Header.Set("Content-Type", writer.FormDataContentType())
			}
			// The protocol decoder has already provided these canonical scalar fields.
			// A task_request override must win over the original route context.
			c.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{RequestBody: map[string]any{"resolution": "720p"}})
			c.Set("task_request", fields)
			facts, err := adaptor.ExtractUsageFactsValidated(c, info)
			require.NoError(t, err)
			assert.Equal(t, 5.0, facts["seconds"])
			c.Set("task_request", map[string]any{"resolution": "changed"})
			params, err := adaptor.TaskBillingRequestParameters(c, info)
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"resolution": "480p", "resolution_name": "1080p", "seconds": "5", "duration": "5", "size": "720x1280", "aspect_ratio": "9:16"}, params)
			fields["resolution"] = "changed"
			assert.Equal(t, "480p", params["resolution"])
			wire, err := common.Marshal(params)
			require.NoError(t, err)
			assert.NotContains(t, string(wire), "secret")
		})
	}
}

func TestTaskBillingRequestParametersValidation(t *testing.T) {
	plugin, err := pluginruntime.NewRegistry().Register(mockPlugin, pluginruntime.Options{})
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	for _, tc := range []struct {
		name, key string
		value     any
	}{
		{"object", "resolution", map[string]any{"prompt": "secret"}}, {"array", "size", []any{"480p"}},
		{"boolean", "seconds", true}, {"long", "resolution", strings.Repeat("a", 65)},
		{"URL", "resolution_name", "https://secret.example"}, {"URL query", "size", "cdn.example?token=secret"},
		{"control", "aspect_ratio", "9:16\nAuthorization"}, {"negative", "seconds", -1.0},
		{"duration limit", "duration", float64(relaycommon.MaxTaskDurationSeconds + 1)},
		{"nan", "seconds", math.NaN()}, {"inf", "resolution", math.Inf(1)},
		{"string nan", "seconds", "NaN"}, {"string limit", "duration", "3601"}, {"huge number", "size", 1e30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adaptor := New(plugin)
			adaptor.routeRequest = &pluginruntime.RouteRequestContext{RequestBody: map[string]any{tc.key: tc.value}}
			params, err := adaptor.TaskBillingRequestParameters(nil, info)
			require.Error(t, err)
			assert.Nil(t, params)
		})
	}
	t.Run("absent stays nil", func(t *testing.T) {
		adaptor := New(plugin)
		adaptor.routeRequest = &pluginruntime.RouteRequestContext{RequestBody: map[string]any{"prompt": "secret", "resolution": nil}}
		params, err := adaptor.TaskBillingRequestParameters(nil, info)
		require.NoError(t, err)
		assert.Nil(t, params)
	})
	t.Run("zero fractional numeric and original labels survive", func(t *testing.T) {
		adaptor := New(plugin)
		values := map[string]any{"seconds": 0.0, "duration": 2.5, "resolution": 480.0, "resolution_name": "480P", "size": "1280*720", "aspect_ratio": "adaptive"}
		adaptor.routeRequest = &pluginruntime.RouteRequestContext{RequestBody: values}
		params, err := adaptor.TaskBillingRequestParameters(nil, info)
		require.NoError(t, err)
		assert.Equal(t, values, params)
	})
}
