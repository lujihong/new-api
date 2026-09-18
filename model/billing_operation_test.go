package model

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prepareBillingOperationTable(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&BillingOperation{}))
}

func cleanBillingOperations(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { DB.Where("1 = 1").Delete(&BillingOperation{}) })
}

func TestBillingOperationCreateIsIdempotentAndRejectsConflicts(t *testing.T) {
	prepareBillingOperationTable(t)
	cleanBillingOperations(t)

	intent := BillingOperationIntent{
		OperationID:       "op-idempotent-1",
		EventID:           "event-idempotent-1",
		TaskID:            "task-1",
		Phase:             "finalize",
		Version:           1,
		Target:            120,
		RequestParamsHash: "hash-a",
		ReceiptAmount:     120,
		ReceiptFee:        0,
		ReceiptNetAmount:  120,
		ReceiptCurrency:   "quota",
	}
	first, created, err := CreateBillingOperation(context.Background(), intent)
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, first)

	second, created, err := CreateBillingOperation(context.Background(), intent)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, second.ID)

	conflict := intent
	conflict.Target++
	_, _, err = CreateBillingOperation(context.Background(), conflict)
	assert.ErrorIs(t, err, ErrBillingOperationConflict)

	eventConflict := intent
	eventConflict.OperationID = "op-idempotent-2"
	_, _, err = CreateBillingOperation(context.Background(), eventConflict)
	assert.ErrorIs(t, err, ErrBillingOperationConflict)
}

func TestBillingOperationEnforcesNonNegativeAmountsAndMonotonicVersions(t *testing.T) {
	prepareBillingOperationTable(t)
	cleanBillingOperations(t)

	negative := BillingOperationIntent{
		OperationID: "op-negative", EventID: "event-negative", TaskID: "task-2",
		Phase: "finalize", Version: 1, Target: -1, ReceiptAmount: 1,
	}
	_, _, err := CreateBillingOperation(context.Background(), negative)
	assert.ErrorIs(t, err, ErrBillingOperationInvalid)

	base := BillingOperationIntent{
		OperationID: "op-version-2", EventID: "event-version-2", TaskID: "task-3",
		Phase: "finalize", Version: 2, Target: 10, ReceiptAmount: 10,
	}
	_, _, err = CreateBillingOperation(context.Background(), base)
	require.NoError(t, err)

	older := base
	older.OperationID = "op-version-1"
	older.EventID = "event-version-1"
	older.Version = 1
	_, _, err = CreateBillingOperation(context.Background(), older)
	assert.ErrorIs(t, err, ErrBillingOperationVersionRegression)
}

func TestBillingOperationRetryTransitionsDurableIntentAndBackoff(t *testing.T) {
	prepareBillingOperationTable(t)
	cleanBillingOperations(t)

	intent := BillingOperationIntent{
		OperationID: "op-retry-1", EventID: "event-retry-1", TaskID: "task-4",
		Phase: "finalize", Version: 1, Target: 20, RequestParamsHash: "hash-retry",
		ReceiptAmount: 20,
	}
	op, _, err := CreateBillingOperation(context.Background(), intent)
	require.NoError(t, err)

	_, err = TransitionBillingOperation(context.Background(), op.OperationID, BillingOperationStatusProcessing, "", time.Now())
	require.NoError(t, err)
	_, err = TransitionBillingOperation(context.Background(), op.OperationID, BillingOperationStatusFailed, "temporary", time.Now())
	require.NoError(t, err)

	now := time.Now().Add(10 * time.Minute)
	retried, err := RetryBillingOperation(context.Background(), op.OperationID, intent.RequestParamsHash, now)
	require.NoError(t, err)
	assert.Equal(t, BillingOperationStatusPending, retried.Status)
	assert.Equal(t, 1, retried.Attempt)
	assert.Greater(t, retried.NextAttemptAt, now.Unix())
	assert.Equal(t, BillingOutboxStatusPending, retried.LogOutboxStatus)
	assert.Equal(t, BillingOutboxStatusPending, retried.CacheOutboxStatus)

	_, err = RetryBillingOperation(context.Background(), op.OperationID, "different-hash", now)
	assert.ErrorIs(t, err, ErrBillingOperationConflict)
}
