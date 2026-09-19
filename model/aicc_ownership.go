package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AICCAssetGroupOwnership binds a remote Mobile Cloud asset group to a local user.
// The remote API uses a shared credential, so this table is the local tenant boundary.
type AICCAssetGroupOwnership struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	UserID    int       `gorm:"not null;uniqueIndex:idx_aicc_group_owner,priority:1" json:"user_id"`
	GroupID   string    `gorm:"size:255;not null;uniqueIndex:idx_aicc_group_owner,priority:2;uniqueIndex:idx_aicc_group_id" json:"group_id"`
	GroupType string    `gorm:"size:32;not null" json:"group_type"`
	ChannelID int       `gorm:"default:0;index:idx_aicc_group_channel" json:"channel_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (AICCAssetGroupOwnership) TableName() string { return "aicc_asset_group_ownerships" }

type AICCAssetOwnership struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	UserID    int       `gorm:"not null;uniqueIndex:idx_aicc_asset_owner,priority:1" json:"user_id"`
	AssetID   string    `gorm:"size:255;not null;uniqueIndex:idx_aicc_asset_owner,priority:2;uniqueIndex:idx_aicc_asset_id" json:"asset_id"`
	GroupID   string    `gorm:"size:255;not null" json:"group_id"`
	ChannelID int       `gorm:"default:0;index:idx_aicc_asset_channel" json:"channel_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
	return DB.Transaction(func(tx *gorm.DB) error {
		row := AICCAssetGroupOwnership{UserID: userID, GroupID: groupID, GroupType: groupType, ChannelID: cID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		var existing AICCAssetGroupOwnership
		if err := tx.Where("group_id = ?", groupID).First(&existing).Error; err != nil {
			return err
		}
		if existing.UserID != userID || existing.GroupType != groupType {
			return errors.New("AICC group ownership conflict")
		}
		return nil
	})
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
	return DB.Transaction(func(tx *gorm.DB) error {
		row := AICCAssetOwnership{UserID: userID, AssetID: assetID, GroupID: groupID, ChannelID: cID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		var existing AICCAssetOwnership
		if err := tx.Where("asset_id = ?", assetID).First(&existing).Error; err != nil {
			return err
		}
		if existing.UserID != userID || existing.GroupID != groupID {
			return errors.New("AICC asset ownership conflict")
		}
		return nil
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
		owned, err := UserOwnsAICCAsset(userID, assetID)
		if err != nil {
			return err
		}
		if !owned {
			return fmt.Errorf("AICC asset %s is not owned by the current user", assetID)
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

// UserOwnedAICCGroupIDsFromAssets covers assets imported through authentication
// before a local group ownership row existed.
func UserOwnedAICCGroupIDsFromAssets(userID int) (map[string]struct{}, error) {
	var rows []AICCAssetOwnership
	if err := DB.Select("DISTINCT group_id").Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.GroupID != "" {
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
