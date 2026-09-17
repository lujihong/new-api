package jsplugin

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDoubaoCompletedVideoExposesHostArtifactURL(t *testing.T) {
	oldSecret, oldServer, oldPublic := common.CryptoSecret, system_setting.ServerAddress, system_setting.TaskPublicAddress
	common.CryptoSecret, system_setting.ServerAddress, system_setting.TaskPublicAddress = "offline-test-secret", "https://gateway.example", ""
	t.Cleanup(func() {
		common.CryptoSecret, system_setting.ServerAddress, system_setting.TaskPublicAddress = oldSecret, oldServer, oldPublic
	})
	source, err := plugins.Source("doubao")
	require.NoError(t, err)
	plugin, err := pluginruntime.NewRegistry().RegisterFactory(source, pluginruntime.Options{Key: "doubao"})
	require.NoError(t, err)
	task := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess, Progress: "100%", Properties: model.Properties{OriginModelName: "doubao-seedance-2.0"}}
	task.SetData(map[string]any{"status": "succeeded", "content": map[string]any{"video_url": "https://upstream.example/private.mp4?secret=hidden"}})
	wire, err := New(plugin).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, common.Unmarshal(wire, &result))
	expected, err := service.BuildTaskArtifactContentURL(task.TaskID, "video")
	require.NoError(t, err)
	require.Equal(t, expected, result["video_url"])
	require.Equal(t, expected, result["url"])
	require.NotContains(t, string(wire), "upstream.example")
	for _, state := range []model.TaskStatus{model.TaskStatusInProgress, model.TaskStatusFailure} {
		task.Status = state
		wire, err = New(plugin).ConvertToOpenAIVideo(task)
		require.NoError(t, err)
		require.NotContains(t, string(wire), "access=")
	}
}
