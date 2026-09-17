package model

import (
	"context"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

// TaskBillingSettlementResult records the outcome of an atomic task quota adjustment.
type TaskBillingSettlementResult struct {
	TaskID         string
	DBTaskID       int64
	UserID         int
	ChannelID      int
	TokenID        int
	TokenKey       string
	BillingSource  string
	SubscriptionID int
	PreQuota       int
	TargetQuota    int
	Delta          int
	AlreadySettled bool
}

// SettleTaskQuotaTransactional atomically synchronizes the task's quota in the database
// with user balances (wallet or subscription), token quota, and user/channel usage.
//
// It acquires an UPDATE lock on the task row in the database, derives the actual delta
// from the authoritative database quota, and executes all quota updates within the same
// transaction. This guarantees:
// 1. Crash consistency: if the transaction commits, both task quota and funding are updated.
// 2. Race immunity: concurrent callers are serialized by the row lock.
// 3. Idempotency: if the task in the database already has targetQuota, delta is 0 and no money is moved.
func SettleTaskQuotaTransactional(ctx context.Context, task *Task, targetQuota int) (*TaskBillingSettlementResult, error) {
	if task == nil {
		return nil, errors.New("task is nil")
	}
	if targetQuota < 0 {
		return nil, fmt.Errorf("targetQuota cannot be negative: %d", targetQuota)
	}

	result := &TaskBillingSettlementResult{
		TaskID:      task.TaskID,
		DBTaskID:    task.ID,
		TargetQuota: targetQuota,
	}

	var cacheOps []func()

	err := DB.Transaction(func(tx *gorm.DB) error {
		var dbTask Task
		hasDBTask := false

		// 1. Try to find and lock the task row in DB
		query := lockForUpdate(tx)
		if task.ID > 0 {
			if err := query.First(&dbTask, task.ID).Error; err == nil {
				hasDBTask = true
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if !hasDBTask && task.TaskID != "" {
			if err := query.Where("task_id = ?", task.TaskID).First(&dbTask).Error; err == nil {
				hasDBTask = true
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}

		preQuota := task.Quota
		userID := task.UserId
		channelID := task.ChannelId
		tokenID := task.PrivateData.TokenId
		billingSource := task.PrivateData.BillingSource
		subscriptionID := task.PrivateData.SubscriptionId

		if hasDBTask {
			result.DBTaskID = dbTask.ID
			preQuota = dbTask.Quota
			userID = dbTask.UserId
			channelID = dbTask.ChannelId
			if dbTask.PrivateData.TokenId > 0 {
				tokenID = dbTask.PrivateData.TokenId
			}
			if dbTask.PrivateData.BillingSource != "" {
				billingSource = dbTask.PrivateData.BillingSource
				subscriptionID = dbTask.PrivateData.SubscriptionId
			}
		}

		result.PreQuota = preQuota
		result.UserID = userID
		result.ChannelID = channelID
		result.TokenID = tokenID
		result.BillingSource = billingSource
		result.SubscriptionID = subscriptionID

		delta := targetQuota - preQuota
		result.Delta = delta

		// If the task is already at the target quota, nothing to do.
		if delta == 0 {
			result.AlreadySettled = true
			return nil
		}

		// 2. Adjust funding inside tx: delta > 0 is charge, delta < 0 is refund
		if billingSource == "subscription" && subscriptionID > 0 {
			var sub UserSubscription
			if err := lockForUpdate(tx).Where("id = ?", subscriptionID).First(&sub).Error; err != nil {
				return fmt.Errorf("lock subscription %d failed: %w", subscriptionID, err)
			}
			newUsed := max(sub.AmountUsed+int64(delta), 0)
			if sub.AmountTotal > 0 && newUsed > sub.AmountTotal {
				return fmt.Errorf("subscription used exceeds total, used=%d total=%d", newUsed, sub.AmountTotal)
			}
			sub.AmountUsed = newUsed
			if err := tx.Save(&sub).Error; err != nil {
				return fmt.Errorf("save subscription %d failed: %w", subscriptionID, err)
			}
		} else if userID > 0 {
			if delta > 0 {
				// Additional charge
				if err := tx.Model(&User{}).
					Where("id = ?", userID).
					Update("quota", gorm.Expr("quota - ?", delta)).Error; err != nil {
					return fmt.Errorf("decrease user %d quota failed: %w", userID, err)
				}
				cacheOps = append(cacheOps, func() {
					_ = cacheDecrUserQuota(userID, int64(delta))
				})
			} else {
				// Refund
				refundAmount := -delta
				if err := common.ValidateWalletQuota(refundAmount); err != nil {
					return err
				}
				updateRes := tx.Model(&User{}).
					Where("id = ? AND quota <= ?", userID, common.MaxWalletQuota-refundAmount).
					Update("quota", gorm.Expr("quota + ?", refundAmount))
				if updateRes.Error != nil {
					return fmt.Errorf("increase user %d quota failed: %w", userID, updateRes.Error)
				}
				if updateRes.RowsAffected == 0 {
					return ErrWalletQuotaLimitExceeded
				}
				cacheOps = append(cacheOps, func() {
					_ = cacheIncrUserQuota(userID, int64(refundAmount))
				})
			}
		}

		// 3. Adjust token quota inside tx if token is associated
		if tokenID > 0 {
			var token Token
			if err := lockForUpdate(tx).Where("id = ?", tokenID).First(&token).Error; err == nil {
				result.TokenKey = token.Key
				if delta > 0 {
					if err := tx.Model(&Token{}).Where("id = ?", tokenID).Updates(map[string]any{
						"remain_quota":  gorm.Expr("remain_quota - ?", delta),
						"used_quota":    gorm.Expr("used_quota + ?", delta),
						"accessed_time": common.GetTimestamp(),
					}).Error; err != nil {
						return fmt.Errorf("update token %d quota failed: %w", tokenID, err)
					}
				} else {
					refundAmount := -delta
					if err := tx.Model(&Token{}).Where("id = ?", tokenID).Updates(map[string]any{
						"remain_quota":  gorm.Expr("remain_quota + ?", refundAmount),
						"used_quota":    gorm.Expr("used_quota - ?", refundAmount),
						"accessed_time": common.GetTimestamp(),
					}).Error; err != nil {
						return fmt.Errorf("update token %d quota failed: %w", tokenID, err)
					}
				}
				key := token.Key
				d := delta
				cacheOps = append(cacheOps, func() {
					if common.RedisEnabled {
						_, _ = cacheApplyTokenQuotaDelta(tokenID, key, int64(-d))
					}
				})
			}
		}

		// 4. Adjust user used_quota and channel used_quota
		if userID > 0 && delta != 0 {
			if err := tx.Model(&User{}).
				Where("id = ?", userID).
				Update("used_quota", gorm.Expr("used_quota + ?", delta)).Error; err != nil {
				return fmt.Errorf("update user %d used_quota failed: %w", userID, err)
			}
		}
		if channelID > 0 && delta != 0 {
			if err := tx.Model(&Channel{}).
				Where("id = ?", channelID).
				Update("used_quota", gorm.Expr("used_quota + ?", delta)).Error; err != nil {
				return fmt.Errorf("update channel %d used_quota failed: %w", channelID, err)
			}
		}

		// 5. Update task quota in DB
		if hasDBTask {
			if err := tx.Model(&Task{}).Where("id = ?", dbTask.ID).Update("quota", targetQuota).Error; err != nil {
				return fmt.Errorf("update task %d quota failed: %w", dbTask.ID, err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Post-commit cache updates
	for _, op := range cacheOps {
		gopool.Go(op)
	}

	task.Quota = targetQuota
	return result, nil
}
