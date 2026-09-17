package model

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupProcurementTestDB(t *testing.T) {
	oldDB := DB
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/procurement.db"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, DB.AutoMigrate(&PurchasePriceVersion{}, &UpstreamAttempt{}))
	t.Cleanup(func() {
		DB = oldDB
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
}

func TestPurchasePriceVersion_EffectiveResolution(t *testing.T) {
	setupProcurementTestDB(t)

	now := time.Now().Unix()
	past := now - 3600
	future := now + 3600

	// Seed version 1 from past
	v1 := &PurchasePriceVersion{
		ChannelID:     6,
		UpstreamModel: "doubao-seedance-2.0",
		BillingMode:   "per_token",
		Currency:      "CNY",
		UnitPrice:     0.028,
		Unit:          "1k_tokens",
		Version:       1,
		EffectiveTime: past,
		Status:        ProcurementStatusActive,
	}
	require.NoError(t, DB.Create(v1).Error)

	// Seed version 2 in the future (not yet effective)
	v2 := &PurchasePriceVersion{
		ChannelID:     6,
		UpstreamModel: "doubao-seedance-2.0",
		BillingMode:   "per_token",
		Currency:      "CNY",
		UnitPrice:     0.025,
		Unit:          "1k_tokens",
		Version:       2,
		EffectiveTime: future,
		Status:        ProcurementStatusActive,
	}
	require.NoError(t, DB.Create(v2).Error)

	// At current time, v1 must be selected
	eff, err := FindEffectivePurchasePriceVersion(6, "doubao-seedance-2.0", now)
	require.NoError(t, err)
	require.NotNil(t, eff)
	assert.Equal(t, int64(v1.ID), eff.ID)
	assert.Equal(t, 0.028, eff.UnitPrice)

	// In the future, v2 must be selected
	effFuture, err := FindEffectivePurchasePriceVersion(6, "doubao-seedance-2.0", future+10)
	require.NoError(t, err)
	require.NotNil(t, effFuture)
	assert.Equal(t, int64(v2.ID), effFuture.ID)
	assert.Equal(t, 0.025, effFuture.UnitPrice)

	// Missing channel or model returns nil
	missing, err := FindEffectivePurchasePriceVersion(999, "doubao-seedance-2.0", now)
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestUpstreamAttempt_CostStatusAndIdempotency(t *testing.T) {
	setupProcurementTestDB(t)

	// Unknown cost attempt (no contract yet)
	attempt1 := &UpstreamAttempt{
		AttemptID:   "att_test_unknown_001",
		TaskID:      "task_video_001",
		ChannelID:   6,
		OriginModel: "doubao-seedance-2.0",
		CostStatus:  CostStatusUnknown,
	}
	require.NoError(t, RecordUpstreamAttempt(attempt1))

	var saved1 UpstreamAttempt
	require.NoError(t, DB.Where("attempt_id = ?", "att_test_unknown_001").First(&saved1).Error)
	assert.Equal(t, CostStatusUnknown, saved1.CostStatus)
	assert.Nil(t, saved1.CostAmount)

	// Finalizing cost once known
	costVal := 1.1366
	require.NoError(t, FinalizeUpstreamAttemptCost(
		"att_test_unknown_001",
		CostStatusKnown,
		&costVal,
		"CNY",
		`{"tokens":40594}`,
		"upstream_task_cgt_001",
	))

	var finalized UpstreamAttempt
	require.NoError(t, DB.Where("attempt_id = ?", "att_test_unknown_001").First(&finalized).Error)
	assert.Equal(t, CostStatusKnown, finalized.CostStatus)
	require.NotNil(t, finalized.CostAmount)
	assert.Equal(t, 1.1366, *finalized.CostAmount)
	assert.Equal(t, "CNY", finalized.CostCurrency)
	assert.Equal(t, "upstream_task_cgt_001", finalized.UpstreamTaskID)

	// Idempotent record: inserting same attempt ID does not duplicate
	attemptDup := &UpstreamAttempt{
		AttemptID: "att_test_unknown_001",
		ChannelID: 6,
	}
	require.NoError(t, RecordUpstreamAttempt(attemptDup))
	var count int64
	require.NoError(t, DB.Model(&UpstreamAttempt{}).Where("attempt_id = ?", "att_test_unknown_001").Count(&count).Error)
	assert.Equal(t, int64(1), count)
}
