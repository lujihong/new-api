package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// BillingOperationStatus is the durable state of a settlement intent. The
// operation row is the source of truth; delivery of either outbox is separate.
type BillingOperationStatus string

const (
	BillingOperationStatusPending    BillingOperationStatus = "pending"
	BillingOperationStatusProcessing BillingOperationStatus = "processing"
	BillingOperationStatusSucceeded  BillingOperationStatus = "succeeded"
	BillingOperationStatusFailed     BillingOperationStatus = "failed"
)

// BillingOutboxStatus tracks delivery intent only. It must not be interpreted as
// proof that LOG_DB or a cache was written in the same transaction as DB.
type BillingOutboxStatus string

const (
	BillingOutboxStatusPending BillingOutboxStatus = "pending"
	BillingOutboxStatusSent    BillingOutboxStatus = "sent"
	BillingOutboxStatusFailed  BillingOutboxStatus = "failed"
)

var (
	ErrBillingOperationInvalid           = errors.New("invalid billing operation")
	ErrBillingOperationConflict          = errors.New("billing operation idempotency conflict")
	ErrBillingOperationVersionRegression = errors.New("billing operation version regression")
	ErrBillingOperationInvalidTransition = errors.New("invalid billing operation status transition")
)

// BillingOperation is a main-database durable settlement intent. Receipt
// amounts are integer quota units, avoiding floating-point money arithmetic.
type BillingOperation struct {
	ID                int64                  `json:"id" gorm:"primaryKey"`
	OperationID       string                 `json:"operation_id" gorm:"type:varchar(128);uniqueIndex"`
	EventID           string                 `json:"event_id" gorm:"type:varchar(128);uniqueIndex"`
	TaskID            string                 `json:"task_id" gorm:"type:varchar(191);index:idx_billing_operation_task_phase"`
	Phase             string                 `json:"phase" gorm:"type:varchar(64);index:idx_billing_operation_task_phase"`
	Version           int64                  `json:"version" gorm:"index:idx_billing_operation_task_phase"`
	Target            int64                  `json:"target"`
	Status            BillingOperationStatus `json:"status" gorm:"type:varchar(32);index"`
	RequestParamsHash string                 `json:"request_params_hash" gorm:"type:varchar(128)"`
	ReceiptAmount     int64                  `json:"receipt_amount"`
	ReceiptFee        int64                  `json:"receipt_fee"`
	ReceiptNetAmount  int64                  `json:"receipt_net_amount"`
	ReceiptCurrency   string                 `json:"receipt_currency" gorm:"type:varchar(16)"`
	LogOutboxStatus   BillingOutboxStatus    `json:"log_outbox_status" gorm:"type:varchar(32);index"`
	CacheOutboxStatus BillingOutboxStatus    `json:"cache_outbox_status" gorm:"type:varchar(32);index"`
	Attempt           int                    `json:"attempt"`
	NextAttemptAt     int64                  `json:"next_attempt_at" gorm:"bigint;index"`
	LastError         string                 `json:"last_error" gorm:"type:text"`
	CreatedAt         int64                  `json:"created_at" gorm:"bigint;index"`
	UpdatedAt         int64                  `json:"updated_at" gorm:"bigint;index"`
}

// BillingOperationIntent contains the immutable values used for idempotency.
type BillingOperationIntent struct {
	OperationID       string
	EventID           string
	TaskID            string
	Phase             string
	Version           int64
	Target            int64
	RequestParamsHash string
	ReceiptAmount     int64
	ReceiptFee        int64
	ReceiptNetAmount  int64
	ReceiptCurrency   string
}

func (o *BillingOperation) BeforeCreate(_ *gorm.DB) error {
	if err := o.validate(); err != nil {
		return err
	}
	now := common.GetTimestamp()
	if o.CreatedAt == 0 {
		o.CreatedAt = now
	}
	if o.UpdatedAt == 0 {
		o.UpdatedAt = now
	}
	if o.Status == "" {
		o.Status = BillingOperationStatusPending
	}
	if o.LogOutboxStatus == "" {
		o.LogOutboxStatus = BillingOutboxStatusPending
	}
	if o.CacheOutboxStatus == "" {
		o.CacheOutboxStatus = BillingOutboxStatusPending
	}
	return o.validate()
}

func (o *BillingOperation) BeforeUpdate(_ *gorm.DB) error {
	if o.ID == 0 && o.OperationID == "" {
		return nil
	}
	return o.validate()
}

func (o *BillingOperation) validate() error {
	if strings.TrimSpace(o.OperationID) == "" || strings.TrimSpace(o.EventID) == "" ||
		strings.TrimSpace(o.TaskID) == "" || strings.TrimSpace(o.Phase) == "" || o.Version <= 0 {
		return fmt.Errorf("%w: operation_id, event_id, task_id, phase and positive version are required", ErrBillingOperationInvalid)
	}
	if o.Target < 0 || o.ReceiptAmount < 0 || o.ReceiptFee < 0 || o.ReceiptNetAmount < 0 {
		return fmt.Errorf("%w: target and receipt amounts must be non-negative", ErrBillingOperationInvalid)
	}
	if o.ReceiptNetAmount > o.ReceiptAmount {
		return fmt.Errorf("%w: receipt net amount exceeds receipt amount", ErrBillingOperationInvalid)
	}
	if !validBillingOperationStatus(o.Status) || !validBillingOutboxStatus(o.LogOutboxStatus) || !validBillingOutboxStatus(o.CacheOutboxStatus) {
		return fmt.Errorf("%w: unknown status", ErrBillingOperationInvalid)
	}
	if o.Attempt < 0 || o.NextAttemptAt < 0 {
		return fmt.Errorf("%w: attempt and next_attempt_at must be non-negative", ErrBillingOperationInvalid)
	}
	return nil
}

func validBillingOperationStatus(status BillingOperationStatus) bool {
	return status == "" || status == BillingOperationStatusPending || status == BillingOperationStatusProcessing || status == BillingOperationStatusSucceeded || status == BillingOperationStatusFailed
}

func validBillingOutboxStatus(status BillingOutboxStatus) bool {
	return status == "" || status == BillingOutboxStatusPending || status == BillingOutboxStatusSent || status == BillingOutboxStatusFailed
}

func (i BillingOperationIntent) model() *BillingOperation {
	return &BillingOperation{
		OperationID: i.OperationID, EventID: i.EventID, TaskID: i.TaskID, Phase: i.Phase,
		Version: i.Version, Target: i.Target, Status: BillingOperationStatusPending,
		RequestParamsHash: i.RequestParamsHash, ReceiptAmount: i.ReceiptAmount,
		ReceiptFee: i.ReceiptFee, ReceiptNetAmount: i.ReceiptNetAmount, ReceiptCurrency: i.ReceiptCurrency,
		LogOutboxStatus: BillingOutboxStatusPending, CacheOutboxStatus: BillingOutboxStatusPending,
	}
}

func (o *BillingOperation) matchesIntent(i BillingOperationIntent) bool {
	return o.OperationID == i.OperationID && o.EventID == i.EventID && o.TaskID == i.TaskID &&
		o.Phase == i.Phase && o.Version == i.Version && o.Target == i.Target &&
		o.RequestParamsHash == i.RequestParamsHash && o.ReceiptAmount == i.ReceiptAmount &&
		o.ReceiptFee == i.ReceiptFee && o.ReceiptNetAmount == i.ReceiptNetAmount && o.ReceiptCurrency == i.ReceiptCurrency
}

// CreateBillingOperation records a durable main-DB intent. A repeated operation
// or event with identical immutable parameters returns the existing row; a
// parameter mismatch is an explicit conflict rather than a silent overwrite.
func CreateBillingOperation(ctx context.Context, intent BillingOperationIntent) (*BillingOperation, bool, error) {
	candidate := intent.model()
	if err := candidate.validate(); err != nil {
		return nil, false, err
	}
	var operation *BillingOperation
	created := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var byOperation, byEvent BillingOperation
		if err := tx.Where("operation_id = ?", intent.OperationID).First(&byOperation).Error; err == nil {
			if !byOperation.matchesIntent(intent) {
				return ErrBillingOperationConflict
			}
			operation = &byOperation
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Where("event_id = ?", intent.EventID).First(&byEvent).Error; err == nil {
			if !byEvent.matchesIntent(intent) {
				return ErrBillingOperationConflict
			}
			operation = &byEvent
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var maxVersion int64
		if err := tx.Model(&BillingOperation{}).Where("task_id = ? AND phase = ?", intent.TaskID, intent.Phase).Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error; err != nil {
			return err
		}
		if maxVersion > intent.Version {
			return ErrBillingOperationVersionRegression
		}
		if err := tx.Create(candidate).Error; err != nil {
			// A concurrent creator may have won the unique key. Resolve it to
			// preserve idempotency while still surfacing parameter conflicts.
			var existing BillingOperation
			if findErr := tx.Where("operation_id = ? OR event_id = ?", intent.OperationID, intent.EventID).First(&existing).Error; findErr == nil {
				if existing.matchesIntent(intent) {
					operation = &existing
					return nil
				}
				return ErrBillingOperationConflict
			}
			return err
		}
		operation = candidate
		created = true
		return nil
	})
	return operation, created, err
}

// TransitionBillingOperation performs a guarded lifecycle transition.
func TransitionBillingOperation(ctx context.Context, operationID string, next BillingOperationStatus, message string, now time.Time) (*BillingOperation, error) {
	if !validBillingOperationStatus(next) || next == "" {
		return nil, fmt.Errorf("%w: %s", ErrBillingOperationInvalidTransition, next)
	}
	var operation BillingOperation
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("operation_id = ?", operationID).First(&operation).Error; err != nil {
			return err
		}
		if !allowedBillingTransition(operation.Status, next) {
			if operation.Status == next {
				return nil
			}
			return fmt.Errorf("%w: %s -> %s", ErrBillingOperationInvalidTransition, operation.Status, next)
		}
		updates := map[string]any{"status": next, "updated_at": common.GetTimestamp()}
		if message != "" {
			updates["last_error"] = message
		}
		if next == BillingOperationStatusProcessing && operation.NextAttemptAt == 0 {
			updates["next_attempt_at"] = now.Unix()
		}
		if err := tx.Model(&BillingOperation{}).Where("id = ? AND status = ?", operation.ID, operation.Status).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", operation.ID).First(&operation).Error
	})
	return &operation, err
}

func allowedBillingTransition(from, to BillingOperationStatus) bool {
	switch from {
	case BillingOperationStatusPending:
		return to == BillingOperationStatusProcessing || to == BillingOperationStatusFailed
	case BillingOperationStatusProcessing:
		return to == BillingOperationStatusSucceeded || to == BillingOperationStatusFailed
	case BillingOperationStatusFailed:
		return to == BillingOperationStatusPending
	case BillingOperationStatusSucceeded:
		return false
	default:
		return false
	}
}

// RetryBillingOperation makes a failed operation pending again. Repeating the
// call while already pending/processing/succeeded is idempotent and does not
// increment attempts; a different request hash is always a conflict.
func RetryBillingOperation(ctx context.Context, operationID, requestParamsHash string, now time.Time) (*BillingOperation, error) {
	var operation BillingOperation
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("operation_id = ?", operationID).First(&operation).Error; err != nil {
			return err
		}
		if operation.RequestParamsHash != requestParamsHash {
			return ErrBillingOperationConflict
		}
		if operation.Status != BillingOperationStatusFailed {
			return nil
		}
		nextAttempt := operation.Attempt + 1
		backoff := BillingOperationBackoff(nextAttempt)
		updates := map[string]any{
			"status":              BillingOperationStatusPending,
			"attempt":             nextAttempt,
			"next_attempt_at":     now.Add(backoff).Unix(),
			"last_error":          "",
			"log_outbox_status":   BillingOutboxStatusPending,
			"cache_outbox_status": BillingOutboxStatusPending,
			"updated_at":          common.GetTimestamp(),
		}
		if err := tx.Model(&BillingOperation{}).Where("id = ? AND status = ?", operation.ID, BillingOperationStatusFailed).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", operation.ID).First(&operation).Error
	})
	return &operation, err
}

// BillingOperationBackoff is capped exponential retry delay.
func BillingOperationBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(5*(1<<(attempt-1))) * time.Second
}

// BillingOperationParamsHash is a stable helper for callers that need to
// derive the request hash before creating an intent.
func BillingOperationParamsHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
