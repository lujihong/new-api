package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAICCBindingDB(t *testing.T) *gorm.DB {
	t.Helper()
	previous := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&AICCAssetGroupOwnership{}, &AICCAssetOwnership{}, &AICCAuthSession{}))
	DB = db
	t.Cleanup(func() { DB = previous; _ = sqlDB.Close() })
	return db
}

func TestAICCBindingOwnershipBoundaries(t *testing.T) {
	setupAICCBindingDB(t)
	a := AICCBinding{ChannelID: 11, AICCAccountID: strings.Repeat("a", 64)}
	sameAccount := AICCBinding{ChannelID: 12, AICCAccountID: a.AICCAccountID}
	other := AICCBinding{ChannelID: 21, AICCAccountID: strings.Repeat("b", 64)}
	require.True(t, a.Valid())
	require.False(t, (AICCBinding{ChannelID: 11}).Valid())
	require.False(t, (AICCBinding{AICCAccountID: a.AICCAccountID}).Valid())
	require.NoError(t, RecordBoundAICCAssetGroupOwnership(101, "group", "AIGC", a))
	require.NoError(t, RecordBoundAICCAssetGroupOwnership(101, "group", "AIGC", a))
	require.NoError(t, RecordBoundAICCAssetGroupOwnership(202, "foreign", "AIGC", other))
	require.NoError(t, RecordBoundAICCAssetOwnership(101, "asset", "group", a))
	for _, user := range []int{101, 0} {
		binding, err := GetOwnedAICCGroupBinding(user, "group")
		require.NoError(t, err)
		require.Equal(t, a, binding)
		binding, err = GetOwnedAICCAssetBinding(user, "asset")
		require.NoError(t, err)
		require.Equal(t, a, binding)
	}
	_, err := GetOwnedAICCGroupBinding(202, "group")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetOwnedAICCAssetBinding(202, "asset")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetOwnedAICCGroupBinding(0, "absent")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetOwnedAICCAssetBinding(0, "absent")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetOwnedAICCAssetBinding(-1, "asset")
	require.Error(t, err)
	binding, err := GetAICCAssetBinding("asset")
	require.NoError(t, err)
	require.Equal(t, a, binding)
	require.NoError(t, ValidateUserAICCAssetIDs(101, []string{"asset"}))
	require.Error(t, ValidateUserAICCAssetIDs(202, []string{"asset"}))
	require.Error(t, ValidateUserAICCAssetIDs(0, []string{"asset"}))
	for _, conflict := range []AICCBinding{sameAccount, other, {}} {
		require.Error(t, RecordBoundAICCAssetGroupOwnership(101, "group", "AIGC", conflict))
		require.Error(t, RecordBoundAICCAssetOwnership(101, "asset", "group", conflict))
	}
	require.Error(t, RecordBoundAICCAssetGroupOwnership(202, "group", "AIGC", a))
	require.Error(t, RecordBoundAICCAssetGroupOwnership(101, "group", "LivenessFace", a))
	require.Error(t, RecordBoundAICCAssetOwnership(202, "asset", "foreign", other))
	require.Error(t, RecordBoundAICCAssetOwnership(202, "new", "group", a))
	require.Error(t, RecordBoundAICCAssetOwnership(101, "new", "group", other))
	require.ErrorIs(t, RecordBoundAICCAssetOwnership(101, "new", "missing", a), gorm.ErrRecordNotFound)
	// Different channels configured for the same account can own different resources.
	require.NoError(t, RecordBoundAICCAssetOwnership(101, "asset-two", "group", sameAccount))
	require.NoError(t, RecordBoundAICCAssetGroupOwnership(101, "group-two", "AIGC", sameAccount))
	require.NoError(t, RecordBoundAICCAssetGroupOwnership(101, "other-account", "AIGC", other))
	ids, err := UserOwnedAICCGroupIDsForBinding(101, sameAccount)
	require.NoError(t, err)
	require.Equal(t, map[string]struct{}{"group": {}, "group-two": {}}, ids)
	_, err = UserOwnedAICCGroupIDsForBinding(0, a)
	require.Error(t, err)
}

func TestAICCBindingLegacyCannotRunOrExpandGroupAccess(t *testing.T) {
	setupAICCBindingDB(t)
	a := AICCBinding{ChannelID: 11, AICCAccountID: strings.Repeat("a", 64)}
	require.NoError(t, RecordAICCAssetGroupOwnership(202, "foreign", "AIGC", 11))
	require.NoError(t, RecordAICCAssetOwnership(101, "legacy", "foreign", 11))
	require.NoError(t, RecordAICCAssetOwnership(101, "legacy", "foreign", 11))
	require.Error(t, RecordAICCAssetOwnership(101, "legacy", "foreign", 12))
	require.Error(t, RecordAICCAssetGroupOwnership(202, "foreign", "AIGC", 12))
	_, err := GetOwnedAICCGroupBinding(202, "foreign")
	require.Error(t, err)
	_, err = GetOwnedAICCGroupBinding(0, "foreign")
	require.Error(t, err)
	_, err = GetOwnedAICCAssetBinding(101, "legacy")
	require.Error(t, err)
	_, err = GetAICCAssetBinding("legacy")
	require.Error(t, err)
	require.Error(t, ValidateUserAICCAssetIDs(101, []string{"legacy"}))
	ids, err := UserOwnedAICCGroupIDsFromAssets(101)
	require.NoError(t, err)
	require.Empty(t, ids)
	require.Error(t, RecordBoundAICCAssetGroupOwnership(202, "foreign", "AIGC", a))
	require.NoError(t, RecordAICCAssetOwnership(101, "orphan", "orphan-group"))
	require.Error(t, RecordBoundAICCAssetGroupOwnership(101, "orphan-group", "AIGC", a))
	require.NoError(t, RecordBoundAICCAuthResources(101, "bound", map[string]string{"bound-asset": "bound"}, a))
	require.Error(t, RecordAICCAssetOwnership(101, "bound-asset", "bound", 11))
	require.Error(t, RecordAICCAssetGroupOwnership(101, "bound", "LivenessFace", 11))
}

func TestAICCBindingAuthTransactionRollback(t *testing.T) {
	db := setupAICCBindingDB(t)
	a := AICCBinding{ChannelID: 11, AICCAccountID: strings.Repeat("a", 64)}
	b := AICCBinding{ChannelID: 22, AICCAccountID: strings.Repeat("b", 64)}
	require.NoError(t, RecordBoundAICCAuthResources(202, "foreign", map[string]string{"taken": "foreign"}, b))
	for _, assets := range []map[string]string{
		{"new": "auth", "taken": "auth"},
		{"new": "auth", "wrong": "foreign"},
		{"bad/id": "auth"},
	} {
		require.Error(t, RecordBoundAICCAuthResources(101, "auth", assets, a))
		var groups, assetsCount int64
		require.NoError(t, db.Model(&AICCAssetGroupOwnership{}).Where("user_id = ?", 101).Count(&groups).Error)
		require.NoError(t, db.Model(&AICCAssetOwnership{}).Where("user_id = ?", 101).Count(&assetsCount).Error)
		require.Zero(t, groups)
		require.Zero(t, assetsCount)
	}
	// Force a failure on the second insert to prove an already-inserted asset rolls back.
	require.NoError(t, db.Exec(`CREATE TRIGGER aicc_fail_second BEFORE INSERT ON aicc_asset_ownerships
		WHEN NEW.group_id = 'auth' AND EXISTS (SELECT 1 FROM aicc_asset_ownerships WHERE group_id = 'auth')
		BEGIN SELECT RAISE(ABORT, 'injected second asset failure'); END`).Error)
	require.Error(t, RecordBoundAICCAuthResources(101, "auth", map[string]string{"one": "auth", "two": "auth"}, a))
	var count int64
	require.NoError(t, db.Model(&AICCAssetOwnership{}).Where("group_id = ?", "auth").Count(&count).Error)
	require.Zero(t, count)
	_, err := GetOwnedAICCGroupBinding(0, "auth")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, db.Exec("DROP TRIGGER aicc_fail_second").Error)
	require.NoError(t, RecordBoundAICCAuthResources(101, "auth", map[string]string{"one": "auth", "two": "auth"}, a))
	var group AICCAssetGroupOwnership
	require.NoError(t, db.Where("group_id = ?", "auth").First(&group).Error)
	require.Equal(t, "LivenessFace", group.GroupType)
	for _, id := range []string{"one", "two"} {
		binding, err := GetOwnedAICCAssetBinding(101, id)
		require.NoError(t, err)
		require.Equal(t, a, binding)
	}
}

func TestAICCBindingDeleteScopedAndAtomic(t *testing.T) {
	db := setupAICCBindingDB(t)
	a := AICCBinding{ChannelID: 11, AICCAccountID: strings.Repeat("a", 64)}
	b := AICCBinding{ChannelID: 22, AICCAccountID: strings.Repeat("b", 64)}
	require.NoError(t, RecordBoundAICCAuthResources(101, "group", map[string]string{"asset": "group", "keep": "group"}, a))
	require.NoError(t, DeleteBoundAICCAssetOwnership("asset", b))
	require.NoError(t, DeleteBoundAICCAssetGroupOwnership("group", b))
	_, err := GetOwnedAICCAssetBinding(101, "asset")
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TRIGGER aicc_fail_delete BEFORE DELETE ON aicc_asset_group_ownerships
		BEGIN SELECT RAISE(ABORT, 'injected delete failure'); END`).Error)
	require.Error(t, DeleteBoundAICCAssetGroupOwnership("group", a))
	_, err = GetOwnedAICCAssetBinding(101, "asset")
	require.NoError(t, err)
	require.NoError(t, db.Exec("DROP TRIGGER aicc_fail_delete").Error)
	a.ChannelID++
	require.NoError(t, DeleteBoundAICCAssetOwnership("asset", a))
	_, err = GetAICCAssetBinding("asset")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, DeleteBoundAICCAssetGroupOwnership("group", a))
	_, err = GetAICCAssetBinding("keep")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetOwnedAICCGroupBinding(0, "group")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestAICCBindingSessionConflictAndExpiry(t *testing.T) {
	db := setupAICCBindingDB(t)
	a := AICCBinding{ChannelID: 11, AICCAccountID: strings.Repeat("a", 64)}
	require.NoError(t, RecordBoundAICCAuthSession(101, " token ", 60, a))
	var before AICCAuthSession
	require.NoError(t, db.First(&before).Error)
	require.NotEqual(t, "token", before.TokenHash)
	require.NoError(t, RecordBoundAICCAuthSession(101, "token", 3600, a))
	require.Error(t, RecordBoundAICCAuthSession(202, "token", 3600, a))
	require.Error(t, RecordBoundAICCAuthSession(101, "token", 3600, AICCBinding{ChannelID: 12, AICCAccountID: a.AICCAccountID}))
	require.Error(t, RecordBoundAICCAuthSession(101, "token", 3600, AICCBinding{ChannelID: 11, AICCAccountID: strings.Repeat("b", 64)}))
	require.Error(t, RecordAICCAuthSession(101, "token", 3600, 11))
	var after AICCAuthSession
	require.NoError(t, db.First(&after).Error)
	require.True(t, before.ExpiresAt.Equal(after.ExpiresAt))
	require.Equal(t, before.UserID, after.UserID)
	binding, err := GetAICCAuthSessionBinding(101, "token")
	require.NoError(t, err)
	require.Equal(t, a, binding)
	_, err = GetAICCAuthSessionBinding(202, "token")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetAICCAuthSessionBinding(0, "token")
	require.Error(t, err)
	require.NoError(t, db.Model(&after).Update("expires_at", time.Now().Add(-time.Minute)).Error)
	require.NoError(t, RecordBoundAICCAuthSession(101, "token", 3600, a))
	_, err = GetAICCAuthSessionBinding(101, "token")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	owned, err := UserOwnsAICCAuthSession(101, "token")
	require.NoError(t, err)
	require.False(t, owned)
	_, err = GetAICCAuthSessionChannelID(101, "token")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, RecordAICCAuthSession(101, "legacy", 60, 11))
	require.Error(t, RecordAICCAuthSession(101, "legacy", 60, 12))
	_, err = GetAICCAuthSessionBinding(101, "legacy")
	require.Error(t, err)
	require.Error(t, RecordBoundAICCAuthSession(101, "invalid", 0, a))
	require.Error(t, RecordBoundAICCAuthSession(101, "invalid", 60, AICCBinding{}))
}

func TestAICCBindingDatabaseErrorsPropagate(t *testing.T) {
	db := setupAICCBindingDB(t)
	injected := errors.New("injected AICC query failure")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("aicc_query_error", func(tx *gorm.DB) { tx.AddError(injected) }))
	_, err := GetOwnedAICCGroupBinding(0, "group")
	require.ErrorIs(t, err, injected)
	_, err = GetOwnedAICCAssetBinding(0, "asset")
	require.ErrorIs(t, err, injected)
	_, err = GetAICCAuthSessionBinding(101, "token")
	require.ErrorIs(t, err, injected)
	_, err = UserOwnedAICCGroupIDsForBinding(101, AICCBinding{ChannelID: 11, AICCAccountID: strings.Repeat("a", 64)})
	require.ErrorIs(t, err, injected)
}
