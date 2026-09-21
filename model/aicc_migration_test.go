package model

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// These schemas preserve the released ownership constraints before adding binding snapshots.
type legacyAICCGroup struct {
	ID        int64  `gorm:"primaryKey"`
	UserID    int    `gorm:"not null;uniqueIndex:idx_aicc_group_owner,priority:1"`
	GroupID   string `gorm:"size:255;not null;uniqueIndex:idx_aicc_group_owner,priority:2;uniqueIndex:idx_aicc_group_id"`
	GroupType string `gorm:"size:32;not null"`
	ChannelID int    `gorm:"default:0;index:idx_aicc_group_channel"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (legacyAICCGroup) TableName() string { return "aicc_asset_group_ownerships" }

type legacyAICCAsset struct {
	ID        int64  `gorm:"primaryKey"`
	UserID    int    `gorm:"not null;uniqueIndex:idx_aicc_asset_owner,priority:1"`
	AssetID   string `gorm:"size:255;not null;uniqueIndex:idx_aicc_asset_owner,priority:2;uniqueIndex:idx_aicc_asset_id"`
	GroupID   string `gorm:"size:255;not null"`
	ChannelID int    `gorm:"default:0;index:idx_aicc_asset_channel"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (legacyAICCAsset) TableName() string { return "aicc_asset_ownerships" }

type legacyAICCSession struct {
	ID        int64     `gorm:"primaryKey"`
	UserID    int       `gorm:"not null;index"`
	TokenHash string    `gorm:"size:64;not null;uniqueIndex"`
	ChannelID int       `gorm:"default:0;index"`
	ExpiresAt time.Time `gorm:"not null;index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (legacyAICCSession) TableName() string { return "aicc_auth_sessions" }

func TestAICCBindingSchemaMigration(t *testing.T) {
	var dialect gorm.Dialector
	switch os.Getenv("AICC_VERIFY_DB") {
	case "postgres":
		dialect = postgres.Open(os.Getenv("AICC_VERIFY_DSN"))
	case "mysql":
		dialect = mysql.Open(os.Getenv("AICC_VERIFY_DSN"))
	case "":
		dialect = sqlite.Open(t.TempDir() + "/migration.db")
	default:
		t.Fatal("unsupported test database")
	}
	db, err := gorm.Open(dialect, &gorm.Config{})
	require.NoError(t, err)
	if os.Getenv("AICC_VERIFY_DB") != "" {
		require.Equal(t, "YES", os.Getenv("AICC_VERIFY_DISPOSABLE"), "external database must be explicitly marked disposable")
		var database string
		query := "SELECT current_database()"
		if db.Dialector.Name() == "mysql" {
			query = "SELECT DATABASE()"
		}
		require.NoError(t, db.Raw(query).Scan(&database).Error)
		require.Equal(t, "aicc_verify", database, "refuse to modify a non-verification database")
	}
	sqlDB, err := db.DB()
	require.NoError(t, err)
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous; _ = sqlDB.Close() })
	ownershipModels := []any{&AICCAssetOwnership{}, &AICCAssetGroupOwnership{}, &AICCAuthSession{}}
	models := append(ownershipModels, &AICCAccountAttestation{}, &AICCRecoveryAudit{}, &Channel{})
	for _, scenario := range []string{"fresh", "upgrade"} {
		t.Run(scenario, func(t *testing.T) {
			// AICC_VERIFY_DSN must point only to the disposable verification database.
			require.NoError(t, db.Migrator().DropTable(models...))
			if scenario == "upgrade" {
				require.NoError(t, db.AutoMigrate(&legacyAICCGroup{}, &legacyAICCAsset{}, &legacyAICCSession{}))
				require.NoError(t, db.Create(&legacyAICCGroup{UserID: 7, GroupID: "legacy-group", GroupType: "AIGC", ChannelID: 6}).Error)
				require.NoError(t, db.Create(&legacyAICCAsset{UserID: 7, AssetID: "legacy-asset", GroupID: "legacy-group", ChannelID: 6}).Error)
				require.NoError(t, db.Create(&legacyAICCSession{UserID: 7, TokenHash: aiccTokenHash("legacy-session"), ChannelID: 6, ExpiresAt: time.Now().Add(time.Hour)}).Error)
			}
			for range 2 {
				require.NoError(t, db.AutoMigrate(models...))
			}
			if db.Dialector.Name() == "postgres" {
				// Scenarios drop/recreate the same tables on one client. Reconnect as
				// a restarted application would, discarding the old pgx statement cache.
				require.NoError(t, sqlDB.Close())
				db, err = gorm.Open(dialect, &gorm.Config{})
				require.NoError(t, err)
				sqlDB, err = db.DB()
				require.NoError(t, err)
				DB = db
			}
			for _, table := range ownershipModels {
				require.True(t, db.Migrator().HasColumn(table, "aicc_account_id"))
			}
			if scenario == "upgrade" {
				var group AICCAssetGroupOwnership
				require.NoError(t, db.Where("group_id = ?", "legacy-group").First(&group).Error)
				require.Equal(t, 7, group.UserID)
				require.Equal(t, 6, group.ChannelID)
				require.Empty(t, group.AICCAccountID)
				_, err = GetOwnedAICCGroupBinding(7, "legacy-group")
				require.Error(t, err)
				_, err = GetOwnedAICCAssetBinding(7, "legacy-asset")
				require.Error(t, err)
				_, err = GetAICCAuthSessionBinding(7, "legacy-session")
				require.Error(t, err)
			}
			require.NoError(t, db.Create(&Channel{Id: 11, Key: "synthetic"}).Error)
			var attestedChannel Channel
			require.NoError(t, db.First(&attestedChannel, 11).Error)
			derivedAccountID, deriveErr := DeriveAICCAccountID(attestedChannel, "test-account")
			require.NoError(t, deriveErr)
			attestation := AICCAccountAttestation{ChannelID: 11, AccountID: derivedAccountID, UpstreamSubject: "test-account", ActorUserID: 1, Evidence: "local database test evidence", ConfigFingerprint: strings.Repeat("b", 64)}
			require.NoError(t, RecordAICCAccountAttestation(attestation, func(Channel) (string, error) { return strings.Repeat("b", 64), nil }))
			stored, lookupErr := LatestAICCAccountAttestation(11)
			require.NoError(t, lookupErr)
			require.Equal(t, attestation.AccountID, stored.AccountID)
			require.Error(t, RecordAICCAccountAttestation(attestation, func(Channel) (string, error) { return strings.Repeat("c", 64), nil }))
			a := AICCBinding{ChannelID: 11, AICCAccountID: strings.Repeat("a", 64)}
			b := AICCBinding{ChannelID: 22, AICCAccountID: strings.Repeat("b", 64)}
			require.NoError(t, RecordBoundAICCAuthResources(1, "new-group", map[string]string{"new-asset": "new-group"}, a))
			require.Error(t, RecordBoundAICCAuthResources(2, "foreign-group", map[string]string{"new-asset": "foreign-group"}, b))
			var count int64
			require.NoError(t, db.Model(&AICCAssetGroupOwnership{}).Where("group_id = ?", "foreign-group").Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, RecordBoundAICCAuthSession(1, "new-session", 60, a))
			require.Error(t, RecordBoundAICCAuthSession(2, "new-session", 60, b))
			require.True(t, db.Migrator().HasIndex(&AICCAssetOwnership{}, "idx_aicc_asset_id"))
			require.True(t, db.Migrator().HasIndex(&AICCAssetGroupOwnership{}, "idx_aicc_group_id"))
		})
	}
}
