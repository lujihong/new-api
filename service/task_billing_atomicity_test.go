package service

import (
	"context"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefundTaskQuota_IdempotencyRepeatedCalls(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 101, 101, 101
	const initQuota, preConsumed = 10000, 2500
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-idem-test", tokenRemain)
	seedChannel(t, channelID)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumed, 1)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)

	// First call: should successfully refund
	require.True(t, RefundTaskQuota(ctx, task, "initial failure"))
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, 0, task.Quota)
	assert.Equal(t, 0, getTaskQuota(t, task.ID))
	assert.Equal(t, int64(1), countLogs(t))

	// Second call: must be idempotent, no duplicate refund, no duplicate log
	require.True(t, RefundTaskQuota(ctx, task, "retry failure"))
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID), "wallet must not be refunded twice")
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID), "token must not be refunded twice")
	assert.Equal(t, 0, getTaskQuota(t, task.ID))
	assert.Equal(t, int64(1), countLogs(t), "must not create duplicate refund log")

	// Third call: still strictly idempotent
	require.True(t, RefundTaskQuota(ctx, task, "third attempt"))
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestRefundTaskQuota_ConcurrentRaceProtection(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 102, 102, 102
	const initQuota, preConsumed = 20000, 3000
	const tokenRemain = 10000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-race-test", tokenRemain)
	seedChannel(t, channelID)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumed, 1)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)

	// 20 concurrent workers racing to refund the same task
	const concurrency = 20
	var wg sync.WaitGroup
	results := make([]bool, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			// Each worker has its own task reference pointing to the same DB task
			taskCopy := *task
			results[index] = RefundTaskQuota(ctx, &taskCopy, "concurrent poll timeout")
		}(i)
	}
	wg.Wait()

	for _, ok := range results {
		assert.True(t, ok)
	}

	// Final user quota must be EXACTLY initQuota + preConsumed (not multiplied by concurrency)
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID), "concurrent refunds must never multiply refund amount")
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, 0, getTaskQuota(t, task.ID))
	assert.Equal(t, int64(1), countLogs(t), "concurrent refunds must produce exactly one refund log")
}

func TestRecalculateTaskQuota_IdempotencyAndConcurrency(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 103, 103, 103
	const initQuota, preConsumed = 20000, 2000
	const actualQuota = 3500 // delta = +1500 additional charge
	const tokenRemain = 10000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-race", tokenRemain)
	seedChannel(t, channelID)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumed, 1)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)

	// 10 concurrent workers racing to recalculate the same task
	const concurrency = 10
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			taskCopy := *task
			RecalculateTaskQuota(ctx, &taskCopy, actualQuota, "concurrent token recalculate")
		}()
	}
	wg.Wait()

	// Additional charge should be exactly 1500 once
	assert.Equal(t, initQuota-(actualQuota-preConsumed), getUserQuota(t, userID), "must only deduct extra delta once")
	assert.Equal(t, tokenRemain-(actualQuota-preConsumed), getTokenRemainQuota(t, tokenID))
	assert.Equal(t, actualQuota, getTaskQuota(t, task.ID))
	assert.Equal(t, int64(1), countLogs(t), "must only produce one consumption log")

	// Another sequential call should be completely no-op
	taskReloaded := makeTask(userID, channelID, actualQuota, tokenID, BillingSourceWallet, 0)
	taskReloaded.ID = task.ID
	RecalculateTaskQuota(ctx, taskReloaded, actualQuota, "manual sequential call")
	assert.Equal(t, initQuota-(actualQuota-preConsumed), getUserQuota(t, userID))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestRefundTaskQuota_TransactionRollbackOnFailure(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, channelID, preConsumed = 104, 104, 2000
	const initQuota = 5000

	seedUser(t, userID, initQuota)
	seedChannel(t, channelID)
	seedChargedAccounting(t, userID, channelID, 0, preConsumed, 1)

	// Task with an invalid subscription ID 99999 that does not exist in DB
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceSubscription, 99999)
	require.NoError(t, model.DB.Create(task).Error)

	// Should fail cleanly and roll back the transaction
	ok := RefundTaskQuota(ctx, task, "subscription fail test")
	assert.False(t, ok)

	// User quota, task quota, and logs must be completely untouched
	assert.Equal(t, initQuota, getUserQuota(t, userID), "user quota must not change when refund transaction fails")
	assert.Equal(t, preConsumed, getTaskQuota(t, task.ID), "task quota in DB must retain preConsumed when refund transaction fails")
	assert.Equal(t, preConsumed, task.Quota, "task in memory must retain preConsumed")
	assert.Equal(t, int64(0), countLogs(t), "no refund log must be created on transaction rollback")
}

