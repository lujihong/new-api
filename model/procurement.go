package model

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const (
	CostStatusKnown         = "known"
	CostStatusUnknown       = "unknown"
	CostStatusNotApplicable = "not_applicable"
)

const (
	ProcurementStatusActive     = "active"
	ProcurementStatusDeprecated = "deprecated"
)

// PurchasePriceVersion records the authoritative procurement rate contracted with an upstream vendor.
type PurchasePriceVersion struct {
	ID            int64   `json:"id" gorm:"primary_key;AUTO_INCREMENT"`
	ChannelID     int     `json:"channel_id" gorm:"index;not null"`
	UpstreamModel string  `json:"upstream_model" gorm:"type:varchar(255);index;not null"`
	BillingMode   string  `json:"billing_mode" gorm:"type:varchar(50);not null"` // per_token | per_request | tiered_expr
	Currency      string  `json:"currency" gorm:"type:varchar(10);not null"`     // CNY | USD
	UnitPrice     float64 `json:"unit_price" gorm:"type:decimal(20,8);not null"`
	Unit          string  `json:"unit" gorm:"type:varchar(50);not null"` // 1k_tokens | 1m_tokens | request | second
	Conditions    string  `json:"conditions,omitempty" gorm:"type:text"` // JSON rules for conditions (resolution, audio, etc.)
	Version       int     `json:"version" gorm:"not null;default:1"`
	EffectiveTime int64   `json:"effective_time" gorm:"index;not null"`
	Note          string  `json:"note,omitempty" gorm:"type:text"`
	Status        string  `json:"status" gorm:"type:varchar(20);not null;default:'active'"`
	CreatedAt     int64   `json:"created_at" gorm:"index"`
	UpdatedAt     int64   `json:"updated_at"`
}

// UpstreamAttempt logs each physical request or task submitted to an upstream provider.
type UpstreamAttempt struct {
	ID                     int64    `json:"id" gorm:"primary_key;AUTO_INCREMENT"`
	AttemptID              string   `json:"attempt_id" gorm:"type:varchar(128);uniqueIndex;not null"`
	RequestID              string   `json:"request_id,omitempty" gorm:"type:varchar(128);index"`
	TaskID                 string   `json:"task_id,omitempty" gorm:"type:varchar(191);index"`
	ChannelID              int      `json:"channel_id" gorm:"index;not null"`
	OriginModel            string   `json:"origin_model" gorm:"type:varchar(255);index"`
	UpstreamModel          string   `json:"upstream_model" gorm:"type:varchar(255)"`
	PurchasePriceVersionID *int64   `json:"purchase_price_version_id,omitempty" gorm:"index"`
	CostStatus             string   `json:"cost_status" gorm:"type:varchar(30);not null;default:'unknown'"` // known | unknown | not_applicable
	CostAmount             *float64 `json:"cost_amount,omitempty" gorm:"type:decimal(20,8)"`
	CostCurrency           string   `json:"cost_currency,omitempty" gorm:"type:varchar(10)"`
	UpstreamRequestID      string   `json:"upstream_request_id,omitempty" gorm:"type:varchar(255)"`
	UpstreamTaskID         string   `json:"upstream_task_id,omitempty" gorm:"type:varchar(255)"`
	UsageJSON              string   `json:"usage_json,omitempty" gorm:"type:text"`
	FailureReason          string   `json:"failure_reason,omitempty" gorm:"type:text"`
	CreatedAt              int64    `json:"created_at" gorm:"index"`
	UpdatedAt              int64    `json:"updated_at"`
}

// FindEffectivePurchasePriceVersion finds the active purchase contract for the channel and model at timestamp.
func FindEffectivePurchasePriceVersion(channelID int, upstreamModel string, timestamp int64) (*PurchasePriceVersion, error) {
	if channelID <= 0 || upstreamModel == "" {
		return nil, errors.New("channelID and upstreamModel required")
	}
	if timestamp <= 0 {
		timestamp = time.Now().Unix()
	}

	var version PurchasePriceVersion
	err := DB.Where(
		"channel_id = ? AND upstream_model = ? AND status = ? AND effective_time <= ?",
		channelID, upstreamModel, ProcurementStatusActive, timestamp,
	).Order("effective_time DESC, version DESC").First(&version).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &version, nil
}

// RecordUpstreamAttempt creates an idempotent upstream attempt ledger entry.
func RecordUpstreamAttempt(attempt *UpstreamAttempt) error {
	if attempt == nil || attempt.AttemptID == "" {
		return errors.New("attempt and AttemptID are required")
	}
	now := time.Now().Unix()
	if attempt.CreatedAt == 0 {
		attempt.CreatedAt = now
	}
	attempt.UpdatedAt = now

	if attempt.CostStatus == "" {
		if attempt.CostAmount != nil {
			attempt.CostStatus = CostStatusKnown
		} else {
			attempt.CostStatus = CostStatusUnknown
		}
	}

	// Insert with conflict ignore by AttemptID
	return DB.Where(UpstreamAttempt{AttemptID: attempt.AttemptID}).
		Attrs(*attempt).
		FirstOrCreate(attempt).Error
}

// FinalizeUpstreamAttemptCost updates the actual procurement cost of an attempt once usage is authoritative.
func FinalizeUpstreamAttemptCost(attemptID string, costStatus string, amount *float64, currency string, usageJSON string, upstreamTaskID string) error {
	if attemptID == "" {
		return errors.New("attemptID is required")
	}

	updates := map[string]any{
		"cost_status": costStatus,
		"updated_at":  time.Now().Unix(),
	}
	if amount != nil {
		updates["cost_amount"] = *amount
	}
	if currency != "" {
		updates["cost_currency"] = currency
	}
	if usageJSON != "" {
		updates["usage_json"] = usageJSON
	}
	if upstreamTaskID != "" {
		updates["upstream_task_id"] = upstreamTaskID
	}

	result := DB.Model(&UpstreamAttempt{}).Where("attempt_id = ?", attemptID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("upstream attempt not found: %s", attemptID)
	}
	return nil
}
