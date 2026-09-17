package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAsyncChannelHealthCheckDoesNotReportSuccess(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	types := []int{constant.ChannelTypeMidjourney, constant.ChannelTypeMidjourneyPlus, constant.ChannelTypeSunoAPI, constant.ChannelTypeKling, constant.ChannelTypeJimeng, constant.ChannelTypeDoubaoVideo, constant.ChannelTypeVidu, constant.ChannelTypeTaskPlugin}
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()
	for _, typ := range types {
		for _, status := range []int{common.ChannelStatusEnabled, common.ChannelStatusAutoDisabled} {
			t.Run(strconv.Itoa(typ)+"/"+strconv.Itoa(status), func(t *testing.T) {
				channel := model.Channel{Type: typ, Name: "async-test", BaseURL: common.GetPointer(upstream.URL), Status: status, ResponseTime: 1234, TestTime: 123456}
				require.NoError(t, db.Create(&channel).Error)
				result := testChannelForHealthCheck(context.Background(), &channel, 1, true, 10000)
				require.Zero(t, result.Succeeded, "unsupported test is not a successful upstream request")
				require.Zero(t, result.Tested, "skipped channels must not count as actually tested")
				require.Zero(t, result.Failed)
				require.Equal(t, 1, result.Skipped)
				require.Zero(t, result.Disabled)
				require.Zero(t, result.Enabled)
				var stored model.Channel
				require.NoError(t, db.First(&stored, channel.Id).Error)
				require.Equal(t, 1234, stored.ResponseTime)
				require.Equal(t, int64(123456), stored.TestTime)
				require.Equal(t, status, stored.Status)
			})
		}
	}
	require.Zero(t, upstreamCalls.Load())
}

func TestChannelTestWorkersReduceSkippedSeparately(t *testing.T) {
	channels := []*model.Channel{
		{Type: constant.ChannelTypeDoubaoVideo, Status: common.ChannelStatusEnabled},
		{Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled},
	}
	processed, total := 0, 0
	result := runChannelTestWorkers(context.Background(), channels, 2,
		func(ctx context.Context, channel *model.Channel) channelTestSummary {
			return testChannelForHealthCheck(ctx, channel, 1, true, 10000)
		}, func(p, n int) { processed, total = p, n })
	require.Equal(t, channelTestSummary{Skipped: 2}, result)
	require.Equal(t, 2, processed)
	require.Equal(t, 2, total)
}

func TestAsyncChannelManualTestReturnsSkipped(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	previous := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previous })
	channel := model.Channel{Type: constant.ChannelTypeDoubaoVideo, Name: "async-test", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)
	for _, endpoint := range []string{"", "openai", "image-generation"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/test/"+strconv.Itoa(channel.Id)+"?endpoint_type="+endpoint, nil)
		c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(channel.Id)}}
		c.Set("id", 1)
		TestChannel(c)
		require.Equal(t, http.StatusOK, w.Code)
		var result map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.Equal(t, false, result["success"])
		require.Equal(t, "skipped", result["status"])
		require.Equal(t, "channel_test_unsupported", result["error_code"])
		require.Contains(t, result["message"], "未请求上游")
	}
}
