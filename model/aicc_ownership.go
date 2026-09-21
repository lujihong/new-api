package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AICCBinding stores a channel and a configuration ownership snapshot.
// AICCAccountID is not a verified upstream account identity. A zero or invalid
// binding is legacy data and must never resolve through the default channel.
type AICCBinding struct {
	ChannelID     int
	AICCAccountID string
}

func (b AICCBinding) Valid() bool { return b.ChannelID > 0 && len(b.AICCAccountID) == 64 }

// AICCAssetGroupOwnership binds a remote Mobile Cloud asset group to a local user.
// The remote API uses a shared credential, so this table is the local tenant boundary.
type AICCAssetGroupOwnership struct {
	ID            int64     `gorm:"primaryKey" json:"id"`
	UserID        int       `gorm:"not null;uniqueIndex:idx_aicc_group_owner,priority:1" json:"user_id"`
	GroupID       string    `gorm:"size:255;not null;uniqueIndex:idx_aicc_group_owner,priority:2;uniqueIndex:idx_aicc_group_id" json:"group_id"`
	GroupType     string    `gorm:"size:32;not null" json:"group_type"`
	ChannelID     int       `gorm:"default:0;index:idx_aicc_group_channel" json:"channel_id"`
	AICCAccountID string    `gorm:"size:64;index" json:"aicc_account_id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (AICCAssetGroupOwnership) TableName() string { return "aicc_asset_group_ownerships" }

type AICCAssetOwnership struct {
	ID            int64     `gorm:"primaryKey" json:"id"`
	UserID        int       `gorm:"not null;uniqueIndex:idx_aicc_asset_owner,priority:1" json:"user_id"`
	AssetID       string    `gorm:"size:255;not null;uniqueIndex:idx_aicc_asset_owner,priority:2;uniqueIndex:idx_aicc_asset_id" json:"asset_id"`
	GroupID       string    `gorm:"size:255;not null" json:"group_id"`
	ChannelID     int       `gorm:"default:0;index:idx_aicc_asset_channel" json:"channel_id"`
	AICCAccountID string    `gorm:"size:64;index" json:"aicc_account_id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (AICCAssetOwnership) TableName() string { return "aicc_asset_ownerships" }

func validateAICCResourceID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "/?#") {
		return "", errors.New("invalid AICC resource id")
	}
	return value, nil
}

func RecordAICCAssetGroupOwnership(userID int, groupID, groupType string, channelID ...int) error {
	groupID, err := validateAICCResourceID(groupID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return errors.New("invalid AICC owner")
	}
	cID := 0
	if len(channelID) > 0 {
		cID = channelID[0]
	}
	if DB == nil {
		return errors.New("AICC database unavailable")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return recordAICCGroup(tx, userID, groupID, groupType, AICCBinding{ChannelID: cID})
	})
}

func recordAICCGroup(tx *gorm.DB, userID int, groupID, groupType string, binding AICCBinding) error {
	if binding.Valid() {
		var conflicts int64
		if err := tx.Model(&AICCAssetOwnership{}).Where("group_id = ? AND (user_id <> ? OR aicc_account_id IS NULL OR aicc_account_id <> ? OR channel_id IS NULL OR channel_id <= 0)", groupID, userID, binding.AICCAccountID).Count(&conflicts).Error; err != nil {
			return err
		}
		if conflicts > 0 {
			return errors.New("AICC group contains conflicting asset ownership")
		}
	}
	row := AICCAssetGroupOwnership{UserID: userID, GroupID: groupID, GroupType: groupType, ChannelID: binding.ChannelID, AICCAccountID: binding.AICCAccountID}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	var existing AICCAssetGroupOwnership
	if err := tx.Where("group_id = ?", groupID).First(&existing).Error; err != nil {
		return err
	}
	if existing.UserID != userID || existing.GroupType != groupType || existing.ChannelID != binding.ChannelID || existing.AICCAccountID != binding.AICCAccountID {
		return errors.New("AICC group ownership conflict")
	}
	return nil
}

func RecordAICCAssetOwnership(userID int, assetID, groupID string, channelID ...int) error {
	assetID, err := validateAICCResourceID(assetID)
	if err != nil {
		return err
	}
	groupID, err = validateAICCResourceID(groupID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return errors.New("invalid AICC owner")
	}
	cID := 0
	if len(channelID) > 0 {
		cID = channelID[0]
	}
	if DB == nil {
		return errors.New("AICC database unavailable")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return recordAICCAsset(tx, userID, assetID, groupID, AICCBinding{ChannelID: cID})
	})
}

func recordAICCAsset(tx *gorm.DB, userID int, assetID, groupID string, binding AICCBinding) error {
	row := AICCAssetOwnership{UserID: userID, AssetID: assetID, GroupID: groupID, ChannelID: binding.ChannelID, AICCAccountID: binding.AICCAccountID}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	var existing AICCAssetOwnership
	if err := tx.Where("asset_id = ?", assetID).First(&existing).Error; err != nil {
		return err
	}
	if existing.UserID != userID || existing.GroupID != groupID || existing.ChannelID != binding.ChannelID || existing.AICCAccountID != binding.AICCAccountID {
		return errors.New("AICC asset ownership conflict")
	}
	return nil
}

func RecordBoundAICCAssetGroupOwnership(userID int, groupID, groupType string, binding AICCBinding) error {
	groupID, err := validateAICCResourceID(groupID)
	if err != nil {
		return err
	}
	if DB == nil || userID <= 0 || !binding.Valid() {
		return errors.New("invalid AICC group owner or binding, or database unavailable")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return recordAICCGroup(tx, userID, groupID, groupType, binding)
	})
}

func requireAICCGroupBinding(tx *gorm.DB, userID int, groupID string, binding AICCBinding) error {
	var group AICCAssetGroupOwnership
	if err := tx.Where("group_id = ?", groupID).First(&group).Error; err != nil {
		return err
	}
	if group.UserID != userID || group.AICCAccountID != binding.AICCAccountID || !(AICCBinding{ChannelID: group.ChannelID, AICCAccountID: group.AICCAccountID}).Valid() {
		return errors.New("AICC asset group ownership conflict")
	}
	return nil
}

func RecordBoundAICCAssetOwnership(userID int, assetID, groupID string, binding AICCBinding) error {
	assetID, err := validateAICCResourceID(assetID)
	if err != nil {
		return err
	}
	groupID, err = validateAICCResourceID(groupID)
	if err != nil {
		return err
	}
	if DB == nil || userID <= 0 || !binding.Valid() {
		return errors.New("invalid AICC asset owner or binding, or database unavailable")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := requireAICCGroupBinding(tx, userID, groupID, binding); err != nil {
			return err
		}
		return recordAICCAsset(tx, userID, assetID, groupID, binding)
	})
}

// RecordBoundAICCAuthResources atomically records a confirmed authentication group
// and its assets. Each map entry is assetID -> groupID; no other group is accepted.
func RecordBoundAICCAuthResources(userID int, groupID string, assets map[string]string, binding AICCBinding) error {
	groupID, err := validateAICCResourceID(groupID)
	if err != nil {
		return err
	}
	if DB == nil || userID <= 0 || !binding.Valid() {
		return errors.New("invalid AICC auth resource owner or binding, or database unavailable")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := recordAICCGroup(tx, userID, groupID, "LivenessFace", binding); err != nil {
			return err
		}
		for rawAssetID, rawGroupID := range assets {
			assetID, err := validateAICCResourceID(rawAssetID)
			if err != nil {
				return err
			}
			assetGroupID, err := validateAICCResourceID(rawGroupID)
			if err != nil {
				return err
			}
			if assetGroupID != groupID {
				return errors.New("AICC auth asset belongs to a different group")
			}
			if err := recordAICCAsset(tx, userID, assetID, groupID, binding); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetOwnedAICCGroupBinding accepts userID=0 only for explicitly authorized admin
// callers. It never infers ownership or fills legacy bindings from configuration.
func GetOwnedAICCGroupBinding(userID int, groupID string) (AICCBinding, error) {
	groupID, err := validateAICCResourceID(groupID)
	if err != nil {
		return AICCBinding{}, err
	}
	if DB == nil || userID < 0 {
		return AICCBinding{}, errors.New("invalid AICC owner or database unavailable")
	}
	query := DB.Where("group_id = ?", groupID)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	var row AICCAssetGroupOwnership
	if err := query.First(&row).Error; err != nil {
		return AICCBinding{}, err
	}
	binding := AICCBinding{ChannelID: row.ChannelID, AICCAccountID: row.AICCAccountID}
	if row.UserID <= 0 || !binding.Valid() {
		return AICCBinding{}, errors.New("AICC group has no valid binding")
	}
	return binding, nil
}

// GetOwnedAICCAssetBinding has the same explicit-admin convention as the group
// lookup. An asset lookup never creates or grants group ownership.
func GetOwnedAICCAssetBinding(userID int, assetID string) (AICCBinding, error) {
	assetID, err := validateAICCResourceID(assetID)
	if err != nil {
		return AICCBinding{}, err
	}
	if DB == nil || userID < 0 {
		return AICCBinding{}, errors.New("invalid AICC owner or database unavailable")
	}
	query := DB.Where("asset_id = ?", assetID)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	var row AICCAssetOwnership
	if err := query.First(&row).Error; err != nil {
		return AICCBinding{}, err
	}
	binding := AICCBinding{ChannelID: row.ChannelID, AICCAccountID: row.AICCAccountID}
	if row.UserID <= 0 || !binding.Valid() {
		return AICCBinding{}, errors.New("AICC asset has no valid binding")
	}
	if err := requireAICCGroupBinding(DB, row.UserID, row.GroupID, binding); err != nil {
		// The asset exists: a missing parent is corruption, not an untracked
		// remote asset eligible for an explicit admin channel fallback.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AICCBinding{}, errors.New("AICC asset has no owned group binding")
		}
		return AICCBinding{}, err
	}
	return binding, nil
}

// GetAICCAssetBinding is an unscoped lookup for generation binding validation;
// callers remain responsible for authorizing the user before upstream execution.
func GetAICCAssetBinding(assetID string) (AICCBinding, error) {
	return GetOwnedAICCAssetBinding(0, assetID)
}

func UserOwnedAICCGroupIDsForBinding(userID int, binding AICCBinding) (map[string]struct{}, error) {
	if DB == nil || userID <= 0 || !binding.Valid() {
		return nil, errors.New("invalid AICC owner or binding, or database unavailable")
	}
	var rows []AICCAssetGroupOwnership
	if err := DB.Where("user_id = ? AND aicc_account_id = ? AND channel_id > 0", userID, binding.AICCAccountID).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		ids[row.GroupID] = struct{}{}
	}
	return ids, nil
}

func DeleteBoundAICCAssetOwnership(assetID string, binding AICCBinding) error {
	assetID, err := validateAICCResourceID(assetID)
	if err != nil {
		return err
	}
	if DB == nil || !binding.Valid() {
		return errors.New("invalid AICC binding or database unavailable")
	}
	return DB.Where("asset_id = ? AND aicc_account_id = ?", assetID, binding.AICCAccountID).Delete(&AICCAssetOwnership{}).Error
}

func DeleteBoundAICCAssetGroupOwnership(groupID string, binding AICCBinding) error {
	groupID, err := validateAICCResourceID(groupID)
	if err != nil {
		return err
	}
	if DB == nil || !binding.Valid() {
		return errors.New("invalid AICC binding or database unavailable")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ? AND aicc_account_id = ?", groupID, binding.AICCAccountID).Delete(&AICCAssetOwnership{}).Error; err != nil {
			return err
		}
		return tx.Where("group_id = ? AND aicc_account_id = ?", groupID, binding.AICCAccountID).Delete(&AICCAssetGroupOwnership{}).Error
	})
}

func GetAICCAssetChannelID(assetID string) (int, error) {
	assetID, err := validateAICCResourceID(assetID)
	if err != nil {
		return 0, err
	}
	var row AICCAssetOwnership
	if err := DB.Select("channel_id").Where("asset_id = ?", assetID).First(&row).Error; err != nil {
		return 0, err
	}
	return row.ChannelID, nil
}

func GetAICCGroupChannelID(groupID string) (int, error) {
	groupID, err := validateAICCResourceID(groupID)
	if err != nil {
		return 0, err
	}
	var row AICCAssetGroupOwnership
	if err := DB.Select("channel_id").Where("group_id = ?", groupID).First(&row).Error; err != nil {
		return 0, err
	}
	return row.ChannelID, nil
}

func UserOwnsAICCAssetGroup(userID int, groupID string) (bool, error) {
	groupID, err := validateAICCResourceID(groupID)
	if err != nil {
		return false, err
	}
	var count int64
	err = DB.Model(&AICCAssetGroupOwnership{}).Where("user_id = ? AND group_id = ?", userID, groupID).Count(&count).Error
	return count > 0, err
}

func UserOwnsAICCAsset(userID int, assetID string) (bool, error) {
	assetID, err := validateAICCResourceID(assetID)
	if err != nil {
		return false, err
	}
	var count int64
	err = DB.Model(&AICCAssetOwnership{}).Where("user_id = ? AND asset_id = ?", userID, assetID).Count(&count).Error
	return count > 0, err
}

// ValidateUserAICCAssetIDs verifies every asset reference before it reaches an upstream channel.
func ValidateUserAICCAssetIDs(userID int, assetIDs []string) error {
	for _, rawID := range assetIDs {
		assetID, err := validateAICCResourceID(rawID)
		if err != nil {
			return err
		}
		if userID <= 0 {
			return errors.New("invalid AICC owner")
		}
		if _, err := GetOwnedAICCAssetBinding(userID, assetID); err != nil {
			return fmt.Errorf("AICC asset %s is unavailable to the current user: %w", assetID, err)
		}
	}
	return nil
}

func UserOwnedAICCGroupIDs(userID int) (map[string]struct{}, error) {
	var rows []AICCAssetGroupOwnership
	if err := DB.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		ids[row.GroupID] = struct{}{}
	}
	return ids, nil
}

func UserOwnedAICCAssetIDs(userID int) (map[string]struct{}, error) {
	var rows []AICCAssetOwnership
	if err := DB.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		ids[row.AssetID] = struct{}{}
	}
	return ids, nil
}

// UserOwnedAICCGroupIDsFromAssets only returns explicitly owned, bound groups.
// Legacy asset-level grants must never expand into permission over an entire group.
func UserOwnedAICCGroupIDsFromAssets(userID int) (map[string]struct{}, error) {
	if DB == nil || userID <= 0 {
		return nil, errors.New("invalid AICC owner or database unavailable")
	}
	var rows []AICCAssetGroupOwnership
	err := DB.Table("aicc_asset_group_ownerships AS g").Select("DISTINCT g.*").
		Joins("JOIN aicc_asset_ownerships AS a ON a.group_id = g.group_id AND a.user_id = g.user_id AND a.aicc_account_id = g.aicc_account_id").
		Where("g.user_id = ? AND g.channel_id > 0 AND a.channel_id > 0", userID).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if (AICCBinding{ChannelID: row.ChannelID, AICCAccountID: row.AICCAccountID}).Valid() {
			ids[row.GroupID] = struct{}{}
		}
	}
	return ids, nil
}

func DeleteAICCAssetOwnership(assetID string) error {
	assetID, err := validateAICCResourceID(assetID)
	if err != nil {
		return err
	}
	return DB.Where("asset_id = ?", assetID).Delete(&AICCAssetOwnership{}).Error
}

func DeleteAICCAssetGroupOwnership(groupID string) error {
	groupID, err := validateAICCResourceID(groupID)
	if err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", groupID).Delete(&AICCAssetOwnership{}).Error; err != nil {
			return err
		}
		return tx.Where("group_id = ?", groupID).Delete(&AICCAssetGroupOwnership{}).Error
	})
}
