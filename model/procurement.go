package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
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
	ID            int64  `json:"id" gorm:"primary_key;AUTO_INCREMENT"`
	ChannelID     int    `json:"channel_id" gorm:"not null;uniqueIndex:ux_purchase_price_version_identity,priority:1"`
	UpstreamModel string `json:"upstream_model" gorm:"type:varchar(255);not null;uniqueIndex:ux_purchase_price_version_identity,priority:2"`
	BillingMode   string `json:"billing_mode" gorm:"type:varchar(50);not null"` // per_token | per_request | tiered_expr
	Currency      string `json:"currency" gorm:"type:varchar(10);not null"`     // CNY | USD

	// UnitPrice is retained for backwards-compatible reads of the legacy decimal column.
	// New code must write UnitPriceDecimal, which is an exact decimal string.
	UnitPrice        float64 `json:"unit_price" gorm:"type:decimal(20,8);not null"` // Deprecated: use UnitPriceDecimal.
	UnitPriceDecimal string  `json:"unit_price_decimal,omitempty" gorm:"type:varchar(64)"`
	Unit             string  `json:"unit" gorm:"type:varchar(50);not null"` // 1k_tokens | 1m_tokens | request | second
	Conditions       string  `json:"conditions,omitempty" gorm:"type:text"` // JSON rules for conditions (resolution, audio, etc.)
	Version          int     `json:"version" gorm:"not null;default:1;uniqueIndex:ux_purchase_price_version_identity,priority:3"`
	EffectiveTime    int64   `json:"effective_time" gorm:"not null;uniqueIndex:ux_purchase_price_version_identity,priority:4"`
	Note             string  `json:"note,omitempty" gorm:"type:text"`
	Status           string  `json:"status" gorm:"type:varchar(20);not null;default:'active'"`
	CreatedAt        int64   `json:"created_at" gorm:"index"`
	UpdatedAt        int64   `json:"updated_at"`
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
	CostAmount             *float64 `json:"cost_amount,omitempty" gorm:"type:decimal(20,8)"`                // Deprecated: use CostAmountDecimal.
	CostAmountDecimal      string   `json:"cost_amount_decimal,omitempty" gorm:"type:varchar(64)"`
	CostCurrency           string   `json:"cost_currency,omitempty" gorm:"type:varchar(10)"`
	UpstreamRequestID      string   `json:"upstream_request_id,omitempty" gorm:"type:varchar(255)"`
	UpstreamTaskID         string   `json:"upstream_task_id,omitempty" gorm:"type:varchar(255)"`
	UsageJSON              string   `json:"usage_json,omitempty" gorm:"type:text"`
	FailureReason          string   `json:"failure_reason,omitempty" gorm:"type:text"`
	CreatedAt              int64    `json:"created_at" gorm:"index"`
	UpdatedAt              int64    `json:"updated_at"`
}

func validCurrency(currency string) bool {
	return currency == "CNY" || currency == "USD"
}

func validUnit(unit string) bool {
	switch unit {
	case "1k_tokens", "1m_tokens", "request", "second":
		return true
	default:
		return false
	}
}

func exactPrice(decimalText string, legacy float64) (string, error) {
	if math.IsNaN(legacy) || math.IsInf(legacy, 0) || legacy < 0 {
		return "", errors.New("unit price must be a finite non-negative decimal")
	}
	if decimalText != "" {
		text := strings.TrimSpace(decimalText)
		if strings.ContainsAny(text, "eE") || len(text) > 64 {
			return "", errors.New("unit price must be a standard decimal without exponent")
		}
		d, err := decimal.NewFromString(text)
		if err != nil || d.IsNegative() {
			return "", errors.New("unit price must be a non-negative decimal")
		}
		return d.String(), nil
	}
	return decimal.NewFromFloat(legacy).String(), nil
}

func (p *PurchasePriceVersion) BeforeCreate(_ *gorm.DB) error {
	if p.ChannelID <= 0 || strings.TrimSpace(p.UpstreamModel) == "" || p.Version <= 0 || p.EffectiveTime <= 0 {
		return errors.New("channel, upstream model, positive version and effective time are required")
	}
	if !validCurrency(p.Currency) {
		return fmt.Errorf("invalid currency: %s", p.Currency)
	}
	if !validUnit(p.Unit) {
		return fmt.Errorf("invalid unit: %s", p.Unit)
	}
	price, err := exactPrice(p.UnitPriceDecimal, p.UnitPrice)
	if err != nil {
		return err
	}
		p.UnitPriceDecimal = price
		if legacy, err := decimal.NewFromString(price); err == nil {
			value, _ := legacy.Float64()
			if !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 {
				p.UnitPrice = value
			}
		}
	if p.Status == "" {
		p.Status = ProcurementStatusActive
	}
	if p.Status != ProcurementStatusActive && p.Status != ProcurementStatusDeprecated {
		return fmt.Errorf("invalid procurement status: %s", p.Status)
	}
	return nil
}

// BeforeUpdate prevents changing a released contract. Deprecation is deliberately
// available only through DeprecatePurchasePriceVersion.
func (p *PurchasePriceVersion) BeforeUpdate(tx *gorm.DB) error {
	if p.ID == 0 {
		return errors.New("purchase price version ID is required for update")
	}
	var current PurchasePriceVersion
	if err := tx.Session(&gorm.Session{NewDB: true}).First(&current, p.ID).Error; err != nil {
		return err
	}
	if current.Status == ProcurementStatusDeprecated {
		return errors.New("deprecated purchase price version is immutable")
	}
	if !allowProcurementDeprecation(tx.Statement.Context) {
		return errors.New("released purchase price version is immutable; use deprecation operation")
	}
	if p.Status != ProcurementStatusDeprecated {
		return errors.New("deprecation operation may only change status to deprecated")
	}
	return nil
}

func (p *PurchasePriceVersion) BeforeDelete(_ *gorm.DB) error {
	return errors.New("purchase price versions cannot be deleted; deprecate instead")
}

// AfterFind backfills the exact representation for legacy rows.
func (p *PurchasePriceVersion) AfterFind(_ *gorm.DB) error {
	if p.UnitPriceDecimal == "" && !math.IsNaN(p.UnitPrice) && !math.IsInf(p.UnitPrice, 0) {
		p.UnitPriceDecimal = decimal.NewFromFloat(p.UnitPrice).String()
	}
	return nil
}

// DeprecatePurchasePriceVersion is the only supported status transition for a
// released price version.
func DeprecatePurchasePriceVersion(id int64) error {
	if id <= 0 {
		return errors.New("purchase price version ID is required")
	}
	ctx := context.WithValue(context.Background(), procurementDeprecationKey{}, true)
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current PurchasePriceVersion
		if err := tx.Where("id = ? AND status = ?", id, ProcurementStatusActive).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				var deprecated PurchasePriceVersion
				if checkErr := tx.Where("id = ? AND status = ?", id, ProcurementStatusDeprecated).First(&deprecated).Error; checkErr == nil {
					return nil
				}
				return fmt.Errorf("active purchase price version not found: %d", id)
			}
			return err
		}
		current.Status = ProcurementStatusDeprecated
		return tx.Save(&current).Error
	})
}

type procurementDeprecationKey struct{}

func allowProcurementDeprecation(ctx context.Context) bool {
	value, _ := ctx.Value(procurementDeprecationKey{}).(bool)
	return value
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
	err := DB.Where("channel_id = ? AND upstream_model = ? AND status = ? AND effective_time <= ?", channelID, upstreamModel, ProcurementStatusActive, timestamp).Order("effective_time DESC, version DESC").First(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &version, nil
}

func validateAttempt(attempt *UpstreamAttempt) error {
	if attempt == nil || strings.TrimSpace(attempt.AttemptID) == "" {
		return errors.New("attempt and AttemptID are required")
	}
	if attempt.CostStatus != "" && attempt.CostStatus != CostStatusUnknown && attempt.CostStatus != CostStatusKnown && attempt.CostStatus != CostStatusNotApplicable {
		return fmt.Errorf("invalid cost status: %s", attempt.CostStatus)
	}
	if attempt.CostAmount != nil {
		if math.IsNaN(*attempt.CostAmount) || math.IsInf(*attempt.CostAmount, 0) || *attempt.CostAmount < 0 {
			return errors.New("cost amount must be a finite non-negative number")
		}
		if attempt.CostAmountDecimal == "" {
			attempt.CostAmountDecimal = decimal.NewFromFloat(*attempt.CostAmount).String()
		}
	}
		if attempt.CostAmountDecimal != "" {
			text := strings.TrimSpace(attempt.CostAmountDecimal)
			if strings.ContainsAny(text, "eE") || len(text) > 64 {
				return errors.New("cost amount must be a standard decimal without exponent")
			}
			d, err := decimal.NewFromString(text)
			if err != nil || d.IsNegative() {
				return errors.New("cost amount must be a non-negative decimal")
			}
		}
		if attempt.CostStatus == CostStatusUnknown && (attempt.CostAmount != nil || attempt.CostAmountDecimal != "") {
			return errors.New("unknown cost status cannot have an amount")
		}
		if attempt.CostStatus == CostStatusKnown && attempt.CostAmount == nil && attempt.CostAmountDecimal == "" {
			return errors.New("known cost status requires an amount")
		}
		if attempt.CostStatus == CostStatusKnown && attempt.CostCurrency == "" {
			return errors.New("known cost status requires a currency")
		}
		if attempt.CostCurrency != "" && !validCurrency(attempt.CostCurrency) {
			return fmt.Errorf("invalid currency: %s", attempt.CostCurrency)
		}
		return nil
	}

	func attemptConflicts(existing, incoming *UpstreamAttempt) error {
		compare := func(name, a, b string) error {
			if b != "" && a != b {
				return fmt.Errorf("attempt %s conflict", name)
			}
			return nil
		}
		if incoming.ChannelID != 0 && existing.ChannelID != incoming.ChannelID {
			return errors.New("attempt channel_id conflict")
		}
		for _, pair := range [][3]string{
			{"request_id", existing.RequestID, incoming.RequestID},
			{"task_id", existing.TaskID, incoming.TaskID},
			{"origin_model", existing.OriginModel, incoming.OriginModel},
			{"upstream_model", existing.UpstreamModel, incoming.UpstreamModel},
			{"upstream_request_id", existing.UpstreamRequestID, incoming.UpstreamRequestID},
			{"upstream_task_id", existing.UpstreamTaskID, incoming.UpstreamTaskID},
			{"usage_json", existing.UsageJSON, incoming.UsageJSON},
			{"failure_reason", existing.FailureReason, incoming.FailureReason},
		} {
			if err := compare(pair[0], pair[1], pair[2]); err != nil {
				return err
			}
		}
		if incoming.PurchasePriceVersionID != nil && (existing.PurchasePriceVersionID == nil || *existing.PurchasePriceVersionID != *incoming.PurchasePriceVersionID) {
			return errors.New("attempt purchase price version conflict")
		}
		if incoming.CostStatus != "" && incoming.CostStatus != CostStatusUnknown && existing.CostStatus != "" && incoming.CostStatus != existing.CostStatus {
			return errors.New("attempt cost status conflict")
		}
		if incoming.CostAmountDecimal != "" && existing.CostAmountDecimal != "" && incoming.CostAmountDecimal != existing.CostAmountDecimal {
			return errors.New("attempt cost amount conflict")
		}
		if incoming.CostCurrency != "" && existing.CostCurrency != "" && incoming.CostCurrency != existing.CostCurrency {
			return errors.New("attempt cost currency conflict")
		}
		return nil
	}

// RecordUpstreamAttempt creates an idempotent upstream attempt ledger entry.
func RecordUpstreamAttempt(attempt *UpstreamAttempt) error {
	if err := validateAttempt(attempt); err != nil {
		return err
	}
	if attempt.CostStatus == "" {
		attempt.CostStatus = CostStatusUnknown
	}
	if attempt.CreatedAt == 0 {
		attempt.CreatedAt = time.Now().Unix()
	}
	attempt.UpdatedAt = time.Now().Unix()

	var existing UpstreamAttempt
	err := DB.Where("attempt_id = ?", attempt.AttemptID).First(&existing).Error
	if err == nil {
		return attemptConflicts(&existing, attempt)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err = DB.Create(attempt).Error; err == nil {
		return nil
	}
	// A concurrent creator may have won the unique constraint. Re-read and
	// perform the same consistency check rather than overwriting it.
	if rereadErr := DB.Where("attempt_id = ?", attempt.AttemptID).First(&existing).Error; rereadErr != nil {
		return err
	}
	if conflictErr := attemptConflicts(&existing, attempt); conflictErr != nil {
		return conflictErr
	}
	return nil
}

func decimalAmount(amount *float64) (string, error) {
	if amount == nil {
		return "", nil
	}
	if math.IsNaN(*amount) || math.IsInf(*amount, 0) || *amount < 0 {
		return "", errors.New("cost amount must be a finite non-negative number")
	}
	return strconv.FormatFloat(*amount, 'f', -1, 64), nil
}

// FinalizeUpstreamAttemptCost allows unknown→known exactly once. Known values
// are idempotent only when amount and currency agree; unknown updates may add
// usage/error data without inventing a zero cost.
func FinalizeUpstreamAttemptCost(attemptID string, costStatus string, amount *float64, currency string, usageJSON string, upstreamTaskID string) error {
	if strings.TrimSpace(attemptID) == "" {
		return errors.New("attemptID is required")
	}
	if costStatus != CostStatusKnown && costStatus != CostStatusUnknown && costStatus != CostStatusNotApplicable {
		return fmt.Errorf("invalid cost status: %s", costStatus)
	}
	if costStatus == CostStatusUnknown && (amount != nil || currency != "") {
		return errors.New("unknown cost status cannot include amount or currency")
	}
	if currency != "" && !validCurrency(currency) {
		return fmt.Errorf("invalid currency: %s", currency)
	}
	amountText, err := decimalAmount(amount)
	if err != nil {
		return err
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		var current UpstreamAttempt
		if err := tx.Where("attempt_id = ?", attemptID).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("upstream attempt not found: %s", attemptID)
			}
			return err
		}
		if current.CostStatus == CostStatusKnown {
			if costStatus != CostStatusKnown {
				return errors.New("known attempt cost cannot transition away from known")
			}
			if amount == nil || current.CostAmountDecimal == "" || current.CostAmountDecimal != amountText || (currency != "" && current.CostCurrency != currency) {
				return errors.New("known attempt cost conflict")
			}
			return nil
		}
		if current.CostStatus == CostStatusNotApplicable {
			if costStatus != CostStatusNotApplicable {
				return errors.New("not_applicable attempt cost is immutable")
			}
			return nil
		}
		updates := map[string]any{"updated_at": time.Now().Unix()}
		if costStatus == CostStatusKnown {
			if amount == nil || amountText == "" || currency == "" {
				return errors.New("known cost requires amount and currency")
			}
			updates["cost_status"], updates["cost_amount"], updates["cost_amount_decimal"], updates["cost_currency"] = CostStatusKnown, *amount, amountText, currency
		} else if costStatus == CostStatusNotApplicable {
			if amount != nil || currency != "" {
				return errors.New("not_applicable cost cannot include amount or currency")
			}
			updates["cost_status"] = CostStatusNotApplicable
		}
		if usageJSON != "" {
			updates["usage_json"] = usageJSON
		}
		if upstreamTaskID != "" {
			updates["upstream_task_id"] = upstreamTaskID
		}
		return tx.Model(&UpstreamAttempt{}).Where("id = ? AND cost_status = ?", current.ID, CostStatusUnknown).Updates(updates).Error
	})
}
