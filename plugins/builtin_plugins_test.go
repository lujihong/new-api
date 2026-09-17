package plugins

import (
	"io/fs"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoubaoReferenceAndParameterContract(t *testing.T) {
	source, err := Source("doubao")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "doubao"})
	require.NoError(t, err)
	for _, tc := range []struct {
		name string
		body map[string]any
		want string
	}{
		{"bracket reference", map[string]any{"input_reference[]": []any{"https://example.com/a.png"}}, "https://example.com/a.png"},
		{"object reference", map[string]any{"image": map[string]any{"url": "https://example.com/a.png"}}, "https://example.com/a.png"},
		{"legacy metadata reference", map[string]any{"metadata": map[string]any{"input_reference[]": "https://example.com/a.png"}}, "https://example.com/a.png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.body["model"] = "doubao-seedance-2.0"
			tc.body["prompt"] = "landscape"
			tc.body["duration"] = 6
			tc.body["resolution"] = "720p"
			tc.body["generate_audio"] = false
			value, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"requestBody": tc.body, "baseUrl": "https://zhenze-huhehaote.cmecloud.cn/api/v3", "upstreamModel": "doubao-seedance-2.0", "apiKey": "offline-test"})
			require.NoError(t, err)
			encoded, err := common.Marshal(value)
			require.NoError(t, err)
			var result struct {
				Body map[string]any `json:"body"`
			}
			require.NoError(t, common.Unmarshal(encoded, &result))
			assert.Equal(t, float64(6), result.Body["duration"])
			assert.Equal(t, "720p", result.Body["resolution"])
			assert.Equal(t, false, result.Body["generate_audio"])
			content := result.Body["content"].([]any)
			require.Len(t, content, 2)
			assert.Equal(t, tc.want, content[0].(map[string]any)["image_url"].(map[string]any)["url"])
		})
	}
}

func TestDoubaoProtocolReferencesKeepRoles(t *testing.T) {
	source, err := Source("doubao")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "doubao"})
	require.NoError(t, err)
	for _, body := range []map[string]any{
		{"kind": "json", "value": map[string]any{"input_reference[]": []any{"https://example.com/a.png"}}},
		{"kind": "multipart", "fields": map[string]any{"input_reference[]": []any{"https://example.com/a.png", "https://example.com/b.png"}}, "files": []any{}},
	} {
		decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": "doubao-seedance-2.0", "body": body})
		require.NoError(t, err)
		encoded, err := common.Marshal(decoded)
		require.NoError(t, err)
		var intent struct {
			Action string `json:"action"`
		}
		require.NoError(t, common.Unmarshal(encoded, &intent))
		assert.Equal(t, "image_to_video", intent.Action)
	}
	value, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{
		"baseUrl": "https://example.com", "upstreamModel": "doubao-seedance-2.0", "apiKey": "offline-test",
		"requestBody": map[string]any{"images": []any{"asset-123"}, "content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset-123"}, "role": "first_frame"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset-123"}, "role": "last_frame"},
		}},
	})
	require.NoError(t, err)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var result struct {
		Body struct {
			Content []map[string]any `json:"content"`
		} `json:"body"`
	}
	require.NoError(t, common.Unmarshal(encoded, &result))
	require.Len(t, result.Body.Content, 2)
	assert.Equal(t, "first_frame", result.Body.Content[0]["role"])
	assert.Equal(t, "last_frame", result.Body.Content[1]["role"])
	for _, item := range result.Body.Content {
		assert.Equal(t, "asset://asset-123", item["image_url"].(map[string]any)["url"])
	}
}

func TestDoubaoMultimodalRequestKeepsMediaAndFalse(t *testing.T) {
	source, err := Source("doubao")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "doubao"})
	require.NoError(t, err)
	value, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{
		"baseUrl": "https://example.com", "upstreamModel": "doubao-seedance-2.0", "apiKey": "offline-test",
		"requestBody": map[string]any{"prompt": "landscape", "seconds": "6", "resolution_name": "720p", "size": "16:9", "video_generate_audio": "false", "seed": 0,
			"first_frame_url": "https://example.com/a.png", "last_frame_url": "https://example.com/b.png",
			"video_reference[]": []any{"https://example.com/a.mp4"}, "audio_reference[]": []any{"https://example.com/a.mp3"}},
	})
	require.NoError(t, err)
	wire, err := common.Marshal(value)
	require.NoError(t, err)
	var result struct {
		Body map[string]any `json:"body"`
	}
	require.NoError(t, common.Unmarshal(wire, &result))
	assert.Equal(t, float64(6), result.Body["duration"])
	assert.Equal(t, false, result.Body["generate_audio"])
	assert.Equal(t, float64(0), result.Body["seed"])
	assert.Equal(t, "16:9", result.Body["ratio"])
	content := result.Body["content"].([]any)
	require.Len(t, content, 5)
	for i, role := range []string{"first_frame", "last_frame", "reference_video", "reference_audio"} {
		assert.Equal(t, role, content[i].(map[string]any)["role"])
	}
}

func TestDoubaoImageFramesAreNotBilledAsVideoInput(t *testing.T) {
	source, err := Source("doubao")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "doubao"})
	require.NoError(t, err)
	for _, tc := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"first_frame_url": "https://example.com/a.png", "last_frame_url": "https://example.com/b.png"}, "none"},
		{map[string]any{"metadata": map[string]any{"video_reference[]": []any{"https://example.com/a.mp4"}}}, "video"},
		{map[string]any{"content": []any{map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://example.com/a.mp4"}}}}, "video"},
		{map[string]any{"video_reference[]": []any{}}, "none"},
	} {
		got, err := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{"requestBody": tc.body})
		require.NoError(t, err)
		wire, err := common.Marshal(got)
		require.NoError(t, err)
		var facts struct {
			VideoInput string `json:"video_input"`
		}
		require.NoError(t, common.Unmarshal(wire, &facts))
		assert.Equal(t, tc.want, facts.VideoInput)
	}
}

func TestDoubaoResolutionConflictUsesSubmittedTier(t *testing.T) {
	source, err := Source("doubao")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "doubao"})
	require.NoError(t, err)
	ctx := map[string]any{"baseUrl": "https://example.com", "requestBody": map[string]any{"prompt": "landscape", "seconds": 6, "resolution": "720p", "metadata": map[string]any{"resolution": "1080p"}}}
	built, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.NoError(t, err)
	usage, err := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
	require.NoError(t, err)
	wire, err := common.Marshal(built)
	require.NoError(t, err)
	var request struct {
		Body struct {
			Resolution string `json:"resolution"`
		} `json:"body"`
	}
	require.NoError(t, common.Unmarshal(wire, &request))
	wire, err = common.Marshal(usage)
	require.NoError(t, err)
	var facts struct {
		Resolution string `json:"resolution"`
	}
	require.NoError(t, common.Unmarshal(wire, &facts))
	assert.Equal(t, "720p", request.Body.Resolution)
	assert.Equal(t, request.Body.Resolution, facts.Resolution)
}

var expectedKeys = []string{"alibaba", "doubao", "google", "hailuo", "jimeng", "kling", "sora", "sunoapi", "vertex-ai", "vidu"}

func TestBuiltInVendorPluginsDeclareNativeRoutesAndLegacyChannelTypes(t *testing.T) {
	generation := jsplugin.DefaultRegistry.Generation()
	require.NotNil(t, generation)

	routes := []struct {
		method    string
		path      string
		key       string
		routeType jsplugin.RouteType
		action    string
		renderer  string
	}{
		{"POST", "/kling/v1/videos/text2video", "kling", jsplugin.RouteTypeSubmit, "text_to_video", "taskCreated"},
		{"POST", "/kling/v1/videos/image2video", "kling", jsplugin.RouteTypeSubmit, "image_to_video", "taskCreated"},
		{"GET", "/kling/v1/videos/text2video/:task_id", "kling", jsplugin.RouteTypeQuery, "", "taskStatus"},
		{"GET", "/kling/v1/videos/image2video/:task_id", "kling", jsplugin.RouteTypeQuery, "", "taskStatus"},
		{"POST", "/jimeng/", "jimeng", jsplugin.RouteTypeDynamic, "", "renderTask"},
		{"POST", "/suno/submit/:action", "sunoapi", jsplugin.RouteTypeSubmit, "", "renderSubmit"},
		{"POST", "/suno/fetch", "sunoapi", jsplugin.RouteTypeDynamic, "", "renderTasks"},
		{"GET", "/suno/fetch/:task_id", "sunoapi", jsplugin.RouteTypeQuery, "", "renderTask"},
		{"POST", "/doubao/api/v3/contents/generations/tasks", "doubao", jsplugin.RouteTypeSubmit, "", "taskCreated"},
		{"GET", "/doubao/api/v3/contents/generations/tasks/:task_id", "doubao", jsplugin.RouteTypeQuery, "", "taskStatus"},
	}
	for _, expected := range routes {
		t.Run(expected.method+" "+expected.path, func(t *testing.T) {
			binding, found := generation.LookupDeclaredRoute(expected.method, expected.path)
			require.True(t, found)
			require.Equal(t, expected.key, binding.Plugin.Meta.Key)
			require.Equal(t, expected.routeType, binding.Route.Type)
			require.Equal(t, expected.action, binding.Route.Action)
			require.Equal(t, expected.renderer, binding.Route.Render)
		})
	}

	channelTypes := []struct {
		value int
		key   string
	}{
		{1, "sora"},
		{8, "sora"},
		{36, "sunoapi"},
		{45, "doubao"},
		{50, "kling"},
		{51, "jimeng"},
		{54, "doubao"},
		{55, "sora"},
		{58, "sora"},
		{59, "sora"},
		{60, "sora"},
	}
	for _, channelType := range channelTypes {
		plugin, found := generation.GetByChannelType(channelType.value)
		require.True(t, found)
		require.Equal(t, channelType.key, plugin.Meta.Key)
	}
}

func TestBuiltInTaskPluginResponsesAndUsageContracts(t *testing.T) {
	generation := jsplugin.DefaultRegistry.Generation()
	require.NotNil(t, generation)

	entries, err := fs.ReadDir(taskPlugins, "tasks")
	require.NoError(t, err)
	actualKeys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			actualKeys = append(actualKeys, entry.Name())
		}
	}
	assert.Equal(t, expectedKeys, actualKeys)

	for _, key := range expectedKeys {
		t.Run(key, func(t *testing.T) {
			_, found := generation.Get(key)
			require.True(t, found, "factory plugin was excluded from the active generation")

			source, sourceErr := Source(key)
			require.NoError(t, sourceErr)
			registry := jsplugin.NewRegistry()
			plugin, registerErr := registry.RegisterFactory(source, jsplugin.Options{Key: key})
			require.NoError(t, registerErr)

			var responsesClaim jsplugin.ProtocolClaim
			foundResponses := false
			for _, claim := range plugin.Meta.Protocols {
				if claim.Name == "openai_responses" {
					responsesClaim = claim
					foundResponses = true
					break
				}
			}
			require.True(t, foundResponses, "openai_responses claim must be present")
			assert.Equal(t, []string{"stream", "sync", "background"}, responsesClaim.Supports)
			for _, model := range plugin.Meta.Models {
				binding, claimed := registry.Generation().LookupEndpoint("POST", "/v1/responses", model)
				require.True(t, claimed, model)
				assert.Same(t, plugin, binding.Plugin)
			}
			for _, hook := range []string{"decodeRequest", "renderEvents", "renderFinal"} {
				callable, callableErr := plugin.Engine.HasCallablePath(t.Context(), "protocols", "openai_responses", hook)
				require.NoError(t, callableErr)
				assert.True(t, callable, hook)
			}
			for _, hook := range []string{"extractUsage", "extractUsageOnComplete"} {
				callable, callableErr := plugin.Engine.HasExport(t.Context(), hook)
				require.NoError(t, callableErr)
				assert.True(t, callable, hook)
			}
			require.NotEmpty(t, plugin.Meta.UsageSchema)
			for usageKey, schema := range plugin.Meta.UsageSchema {
				assert.NotEmpty(t, schema.Description, usageKey)
			}
		})
	}
}

func TestBuiltInResponsesDecodersEchoChannelMappedAlias(t *testing.T) {
	bodyOverrides := map[string]map[string]any{}
	for _, key := range expectedKeys {
		t.Run(key, func(t *testing.T) {
			source, sourceErr := Source(key)
			require.NoError(t, sourceErr)
			registry := jsplugin.NewRegistry()
			plugin, registerErr := registry.RegisterFactory(source, jsplugin.Options{Key: key})
			require.NoError(t, registerErr)
			require.NotEmpty(t, plugin.Meta.Models)

			alias := "alias-under-test"
			upstreamModel := plugin.Meta.Models[0]
			body := map[string]any{"model": alias, "input": "a cat walking on the beach"}
			if override, ok := bodyOverrides[key]; ok {
				body = override
			}
			value, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_responses", "decodeRequest"}, map[string]any{
				"model": alias, "upstreamModel": upstreamModel, "stream": false,
				"body": map[string]any{"kind": "json", "value": body},
			})
			require.NoError(t, callErr)
			encoded, marshalErr := common.Marshal(value)
			require.NoError(t, marshalErr)
			var decoded map[string]any
			require.NoError(t, common.Unmarshal(encoded, &decoded))
			assert.Equal(t, "submit", decoded["kind"])
			assert.Equal(t, alias, decoded["model"])
		})
	}
}
