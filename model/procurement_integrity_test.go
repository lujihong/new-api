package model

import (
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func procurementPrice() *PurchasePriceVersion {
	return &PurchasePriceVersion{ChannelID: 6, UpstreamModel: "model", BillingMode: "per_request", Currency: "CNY", UnitPrice: 0.125, Unit: "request", Version: 1, EffectiveTime: 100}
}

func TestPurchasePriceVersion_Integrity(t *testing.T) {
	setupProcurementTestDB(t)
	p := procurementPrice()
	p.UnitPrice = 0
	p.UnitPriceDecimal = "123456789012.12345678"
	require.NoError(t, DB.Create(p).Error)
	var saved PurchasePriceVersion
	require.NoError(t, DB.First(&saved, p.ID).Error)
	require.Equal(t, "123456789012.12345678", saved.UnitPriceDecimal)
	require.NotZero(t, saved.UnitPrice, "legacy readers must not see a false zero")
	for _, field := range []string{"unit_price", "unit_price_decimal", "currency", "unit", "effective_time", "channel_id", "upstream_model", "version", "conditions", "status"} {
		t.Run(field, func(t *testing.T) {
			require.Error(t, DB.Model(&saved).Update(field, "changed").Error)
		})
	}
	copy := saved
	copy.UnitPrice = 1
	require.Error(t, DB.Save(&copy).Error)
	require.Error(t, DB.Delete(&saved).Error)
	require.NoError(t, DeprecatePurchasePriceVersion(p.ID))
	require.NoError(t, DeprecatePurchasePriceVersion(p.ID))
	require.NoError(t, DB.First(&saved, p.ID).Error)
	require.Equal(t, ProcurementStatusDeprecated, saved.Status)
	require.Error(t, DB.Model(&saved).Update("status", ProcurementStatusActive).Error)
	duplicate := procurementPrice()
	require.Error(t, DB.Create(duplicate).Error)
}

func TestPurchasePriceVersion_InvalidInputs(t *testing.T) {
	setupProcurementTestDB(t)
	cases := []struct {
		name   string
		mutate func(*PurchasePriceVersion)
	}{
		{"negative", func(p *PurchasePriceVersion) { p.UnitPrice = -1 }},
		{"nan", func(p *PurchasePriceVersion) { p.UnitPrice = math.NaN() }},
		{"inf", func(p *PurchasePriceVersion) { p.UnitPrice = math.Inf(1) }},
		{"nan_with_decimal", func(p *PurchasePriceVersion) { p.UnitPrice = math.NaN(); p.UnitPriceDecimal = "1" }},
		{"negative_decimal", func(p *PurchasePriceVersion) { p.UnitPriceDecimal = "-1" }},
		{"invalid_decimal", func(p *PurchasePriceVersion) { p.UnitPriceDecimal = "NaN" }},
		{"huge_exponent", func(p *PurchasePriceVersion) { p.UnitPriceDecimal = "1e999999999" }},
		{"currency", func(p *PurchasePriceVersion) { p.Currency = "XYZ" }},
		{"unit", func(p *PurchasePriceVersion) { p.Unit = "invalid" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { p := procurementPrice(); tc.mutate(p); require.Error(t, DB.Create(p).Error) })
	}
}

func TestUpstreamAttempt_Integrity(t *testing.T) {
	setupProcurementTestDB(t)
	a := UpstreamAttempt{AttemptID: "integrity", ChannelID: 6, TaskID: "task", CostStatus: CostStatusUnknown, FailureReason: "pending"}
	require.NoError(t, RecordUpstreamAttempt(&a))
	dup := a
	require.NoError(t, RecordUpstreamAttempt(&dup))
	for _, mutate := range []func(*UpstreamAttempt){
		func(a *UpstreamAttempt) { a.ChannelID = 7 },
		func(a *UpstreamAttempt) { a.TaskID = "different" },
		func(a *UpstreamAttempt) { a.CostStatus = CostStatusNotApplicable },
		func(a *UpstreamAttempt) { a.FailureReason = "different" },
	} {
		bad := a
		mutate(&bad)
		require.Error(t, RecordUpstreamAttempt(&bad))
	}
	require.NoError(t, FinalizeUpstreamAttemptCost(a.AttemptID, CostStatusUnknown, nil, "", `{"pending":true}`, "upstream"))
	var saved UpstreamAttempt
	require.NoError(t, DB.First(&saved, a.ID).Error)
	require.Nil(t, saved.CostAmount)
	require.Equal(t, "pending", saved.FailureReason)
	require.Equal(t, `{"pending":true}`, saved.UsageJSON)
	zero := 0.0
	require.Error(t, FinalizeUpstreamAttemptCost(a.AttemptID, CostStatusUnknown, &zero, "CNY", "", ""))
	value := 1.25
	require.NoError(t, FinalizeUpstreamAttemptCost(a.AttemptID, CostStatusKnown, &value, "CNY", `{"tokens":1}`, "upstream"))
	require.NoError(t, FinalizeUpstreamAttemptCost(a.AttemptID, CostStatusKnown, &value, "CNY", `{"tokens":1}`, "upstream"))
	require.Error(t, FinalizeUpstreamAttemptCost(a.AttemptID, CostStatusKnown, &zero, "CNY", "", ""))
	require.Error(t, FinalizeUpstreamAttemptCost(a.AttemptID, CostStatusKnown, &value, "USD", "", ""))
	require.Error(t, FinalizeUpstreamAttemptCost(a.AttemptID, CostStatusUnknown, nil, "", "", ""))
	require.Error(t, FinalizeUpstreamAttemptCost(a.AttemptID, CostStatusKnown, nil, "CNY", "", ""))
	require.NoError(t, DB.First(&saved, a.ID).Error)
	require.Equal(t, value, *saved.CostAmount)
	require.Equal(t, "CNY", saved.CostCurrency)
}

func TestUpstreamAttempt_InvalidCost(t *testing.T) {
	setupProcurementTestDB(t)
	for _, amount := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		a := UpstreamAttempt{AttemptID: "invalid", ChannelID: 6, CostStatus: CostStatusKnown, CostAmount: &amount, CostCurrency: "CNY"}
		require.Error(t, RecordUpstreamAttempt(&a))
	}
	zero := 0.0
	require.Error(t, RecordUpstreamAttempt(&UpstreamAttempt{AttemptID: "unknown-zero", ChannelID: 6, CostStatus: CostStatusUnknown, CostAmount: &zero}))
	require.Error(t, RecordUpstreamAttempt(&UpstreamAttempt{AttemptID: "known-nil", ChannelID: 6, CostStatus: CostStatusKnown, CostCurrency: "CNY"}))
	require.Error(t, RecordUpstreamAttempt(&UpstreamAttempt{AttemptID: "bad-currency", ChannelID: 6, CostStatus: CostStatusKnown, CostAmount: &zero, CostCurrency: "XYZ"}))
}

func TestProcurement_ConcurrentUniqueness(t *testing.T) {
	setupProcurementTestDB(t)
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	require.NoError(t, DB.Exec("PRAGMA journal_mode=WAL").Error)
	const n = 8
	var wg sync.WaitGroup
	results := make(chan error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- RecordUpstreamAttempt(&UpstreamAttempt{AttemptID: "concurrent", ChannelID: 6})
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, DB.Model(&UpstreamAttempt{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	results = make(chan error, n)
	start = make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- DB.Session(&gorm.Session{SkipDefaultTransaction: true}).Create(procurementPrice()).Error
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	require.Equal(t, 1, wins)
	require.NoError(t, DB.Model(&PurchasePriceVersion{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
