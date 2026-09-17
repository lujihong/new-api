package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskProcurementAttempt_KnownAndUnknownResolution(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	require.NoError(t, model.DB.AutoMigrate(&model.PurchasePriceVersion{}, &model.UpstreamAttempt{}))

	now := time.Now().Unix()

	// Seed a procurement contract for channel 6, doubao-seedance-2.0
	contract := &model.PurchasePriceVersion{
		ChannelID:     6,
		UpstreamModel: "doubao-seedance-2.0",
		BillingMode:   "per_token",
		Currency:      "CNY",
		UnitPrice:     0.028,
		Unit:          "1k_tokens",
		Version:       1,
		EffectiveTime: now - 3600,
		Status:        model.ProcurementStatusActive,
	}
	require.NoError(t, model.DB.Create(contract).Error)

	// Task A on Channel 6 (known cost)
	taskA := makeTask(1, 6, 500000, 0, BillingSourceWallet, 0)
	taskA.TaskID = "task_video_known_001"
	taskA.Properties.UpstreamModelName = "doubao-seedance-2.0"
	taskA.Properties.OriginModelName = "doubao-seedance-2.0"
	taskA.CreatedAt = now

	taskResultA := &relaycommon.TaskInfo{
		TotalTokens: 40000,
		UsageFacts:  map[string]any{"resolution": "480p", "tokens": 40000},
	}

	recordTaskProcurementAttempt(ctx, taskA, taskResultA)

	var attemptA model.UpstreamAttempt
	require.NoError(t, model.DB.Where("task_id = ?", taskA.TaskID).First(&attemptA).Error)
	assert.Equal(t, model.CostStatusKnown, attemptA.CostStatus)
	require.NotNil(t, attemptA.CostAmount)
	// 40000 tokens / 1000 * 0.028 = 1.12 CNY
	assert.InDelta(t, 1.12, *attemptA.CostAmount, 0.0001)
	assert.Equal(t, "CNY", attemptA.CostCurrency)
	assert.Equal(t, contract.ID, *attemptA.PurchasePriceVersionID)

	// Task B on Channel 8 (no contract configured -> must be strictly unknown, never 0)
	taskB := makeTask(1, 8, 500000, 0, BillingSourceWallet, 0)
	taskB.TaskID = "task_video_unknown_002"
	taskB.Properties.UpstreamModelName = "unknown-vendor-model"
	taskB.Properties.OriginModelName = "unknown-vendor-model"
	taskB.CreatedAt = now

	taskResultB := &relaycommon.TaskInfo{
		TotalTokens: 50000,
	}

	recordTaskProcurementAttempt(ctx, taskB, taskResultB)

	var attemptB model.UpstreamAttempt
	require.NoError(t, model.DB.Where("task_id = ?", taskB.TaskID).First(&attemptB).Error)
	assert.Equal(t, model.CostStatusUnknown, attemptB.CostStatus)
	assert.Nil(t, attemptB.CostAmount, "uncontracted channel cost must be nil, never 0")
	assert.Nil(t, attemptB.PurchasePriceVersionID)
}
