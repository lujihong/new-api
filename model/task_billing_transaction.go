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

	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Settlement is durable: never move funds using an in-memory-only task.
		if task.ID <= 0 && task.TaskID == "" {
			return errors.New("task must have a persisted ID or TaskID")
		}

		var dbTask Task
		var err error
		if task.ID > 0 && task.TaskID != "" {
			err = lockForUpdate(tx).Where("id = ? AND task_id = ?", task.ID, task.TaskID).First(&dbTask).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				var byID Task
				idErr := lockForUpdate(tx).Where("id = ?", task.ID).First(&byID).Error
				if idErr != nil && !errors.Is(idErr, gorm.ErrRecordNotFound) {
					return fmt.Errorf("load task %d failed: %w", task.ID, idErr)
				}
				if idErr == nil {
					return fmt.Errorf("task ID %d and TaskID %q do not match", task.ID, task.TaskID)
				}
				return gorm.ErrRecordNotFound
			}
			if err != nil {
				return fmt.Errorf("load task %d/%q failed: %w", task.ID, task.TaskID, err)
			}
		} else if task.ID > 0 {
			err = lockForUpdate(tx).Where("id = ?", task.ID).First(&dbTask).Error
			if err != nil {
				return fmt.Errorf("load task %d failed: %w", task.ID, err)
			}
		} else {
			var matches []Task
			err = lockForUpdate(tx).Where("task_id = ?", task.TaskID).Limit(2).Find(&matches).Error
			if err != nil {
				return fmt.Errorf("load task_id %q failed: %w", task.TaskID, err)
			}
			if len(matches) == 0 {
				return gorm.ErrRecordNotFound
			}
			if len(matches) != 1 {
				return fmt.Errorf("task_id %q is not unique", task.TaskID)
			}
			dbTask = matches[0]
		}

		result.DBTaskID = dbTask.ID
		result.TaskID = dbTask.TaskID
		preQuota := dbTask.Quota
		userID := dbTask.UserId
		channelID := dbTask.ChannelId
		tokenID := dbTask.PrivateData.TokenId
		billingSource := dbTask.PrivateData.BillingSource
		subscriptionID := dbTask.PrivateData.SubscriptionId

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
		if userID <= 0 {
			return fmt.Errorf("task %d has invalid user_id %d", dbTask.ID, userID)
		}
		var user User
		if err := lockForUpdate(tx).Where("id = ?", userID).First(&user).Error; err != nil {
			return fmt.Errorf("lock user %d failed: %w", userID, err)
		}
		if billingSource == "subscription" {
			if subscriptionID <= 0 {
				return fmt.Errorf("task %d has invalid subscription_id %d", dbTask.ID, subscriptionID)
			}
			var sub UserSubscription
			if err := lockForUpdate(tx).Where("id = ?", subscriptionID).First(&sub).Error; err != nil {
				return fmt.Errorf("lock subscription %d failed: %w", subscriptionID, err)
			}
			if sub.UserId != userID {
				return fmt.Errorf("subscription %d belongs to user %d, task belongs to user %d", subscriptionID, sub.UserId, userID)
			}
			newUsed := max(sub.AmountUsed+int64(delta), 0)
			if sub.AmountTotal > 0 && newUsed > sub.AmountTotal {
				return fmt.Errorf("subscription used exceeds total, used=%d total=%d", newUsed, sub.AmountTotal)
			}
			sub.AmountUsed = newUsed
			updateRes := tx.Save(&sub)
			if updateRes.Error != nil {
				return fmt.Errorf("save subscription %d failed: %w", subscriptionID, updateRes.Error)
			}
			if updateRes.RowsAffected != 1 {
				return fmt.Errorf("save subscription %d affected %d rows", subscriptionID, updateRes.RowsAffected)
			}
		} else {
			if billingSource != "" && billingSource != "wallet" {
				return fmt.Errorf("task %d has unsupported billing source %q", dbTask.ID, billingSource)
			}
			if delta > 0 {
				// Additional charge; negative balances are allowed by existing policy.
				updateRes := tx.Model(&User{}).
					Where("id = ?", userID).
					Update("quota", gorm.Expr("quota - ?", delta))
				if updateRes.Error != nil {
					return fmt.Errorf("decrease user %d quota failed: %w", userID, updateRes.Error)
				}
				if updateRes.RowsAffected != 1 {
					return fmt.Errorf("decrease user %d affected %d rows", userID, updateRes.RowsAffected)
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

		// 3. Adjust token quota inside tx if token is associated.
		// A missing token is an explicit settlement error; do not silently move
		// wallet/subscription funds while skipping the token leg.
		if tokenID > 0 {
			var token Token
			tokenErr := lockForUpdate(tx).Where("id = ?", tokenID).First(&token).Error
			if tokenErr != nil {
				if errors.Is(tokenErr, gorm.ErrRecordNotFound) {
					return fmt.Errorf("token %d not found (possibly deleted): %w", tokenID, tokenErr)
				}
				return fmt.Errorf("lock token %d failed: %w", tokenID, tokenErr)
			}
			if token.UserId != userID {
				return fmt.Errorf("token %d belongs to user %d, task belongs to user %d", tokenID, token.UserId, userID)
			}
			result.TokenKey = token.Key
			var updateRes *gorm.DB
			if delta > 0 {
				updateRes = tx.Model(&Token{}).Where("id = ?", tokenID).Updates(map[string]any{
					"remain_quota":  gorm.Expr("remain_quota - ?", delta),
					"used_quota":    gorm.Expr("used_quota + ?", delta),
					"accessed_time": common.GetTimestamp(),
				})
			} else {
				refundAmount := -delta
				updateRes = tx.Model(&Token{}).Where("id = ?", tokenID).Updates(map[string]any{
					"remain_quota":  gorm.Expr("remain_quota + ?", refundAmount),
					"used_quota":    gorm.Expr("used_quota - ?", refundAmount),
					"accessed_time": common.GetTimestamp(),
				})
			}
			if updateRes.Error != nil {
				return fmt.Errorf("update token %d quota failed: %w", tokenID, updateRes.Error)
			}
			if updateRes.RowsAffected != 1 {
				return fmt.Errorf("update token %d affected %d rows", tokenID, updateRes.RowsAffected)
			}
			key := token.Key
			d := delta
			cacheOps = append(cacheOps, func() {
				if common.RedisEnabled {
					_, _ = cacheApplyTokenQuotaDelta(tokenID, key, int64(-d))
				}
			})
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

			// 5. Update the authoritative task quota in the same transaction.
			updateTask := tx.Model(&Task{}).Where("id = ?", dbTask.ID).Update("quota", targetQuota)
			if updateTask.Error != nil {
				return fmt.Errorf("update task %d quota failed: %w", dbTask.ID, updateTask.Error)
			}
			if updateTask.RowsAffected != 1 {
				return fmt.Errorf("update task %d quota affected %d rows", dbTask.ID, updateTask.RowsAffected)
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
