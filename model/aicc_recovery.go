package model

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AICCRecoveryAudit struct {
	ID            int64  `gorm:"primaryKey"`
	ActorUserID   int    `gorm:"not null;index"`
	TargetUserID  int    `gorm:"not null;index"`
	GroupID       string `gorm:"size:255;not null;index"`
	Evidence      string `gorm:"type:text;not null"`
	ChannelID     int    `gorm:"not null;default:0;index"`
	AICCAccountID string `gorm:"size:64;not null;default:''"`
	CreatedAt     time.Time
}

var ErrAICCRecoveryConflict = errors.New("素材组已有其他归属，禁止覆盖")

// RecoverAICCGroup is retained for legacy callers; production HTTP uses the bound API.
func RecoverAICCGroup(actor, target int, groupID, groupType, evidence string) error {
	return recoverAICCGroup(actor, target, groupID, groupType, evidence, AICCBinding{}, false)
}

// RecoverBoundAICCGroup adds an attested grant. It never migrates unknown legacy
// bindings or transfers ownership; such migration requires a separate audited flow.
func RecoverBoundAICCGroup(actor, target int, groupID, groupType, evidence string, binding AICCBinding) error {
	if !binding.Valid() {
		return errors.New("invalid recovery binding")
	}
	return recoverAICCGroup(actor, target, groupID, groupType, evidence, binding, true)
}

func recoverAICCGroup(actor, target int, groupID, groupType, evidence string, binding AICCBinding, bound bool) error {
	groupID, err := validateAICCResourceID(groupID)
	if err != nil {
		return err
	}
	evidence = strings.TrimSpace(evidence)
	if actor <= 0 || target <= 0 || evidence == "" || len(evidence) > 2000 || (groupType != "AIGC" && groupType != "LivenessFace") {
		return errors.New("invalid recovery request")
	}
	if DB == nil {
		return errors.New("AICC database unavailable")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		row := AICCAssetGroupOwnership{UserID: target, GroupID: groupID, GroupType: groupType, ChannelID: binding.ChannelID, AICCAccountID: binding.AICCAccountID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		var current AICCAssetGroupOwnership
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("group_id = ?", groupID).First(&current).Error; err != nil {
			return err
		}
		if current.UserID != target || current.GroupType != groupType || current.ChannelID != binding.ChannelID || current.AICCAccountID != binding.AICCAccountID {
			return ErrAICCRecoveryConflict
		}
		if bound {
			var assets []AICCAssetOwnership
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("group_id = ?", groupID).Find(&assets).Error; err != nil {
				return err
			}
			for _, asset := range assets {
				if asset.UserID != target || asset.ChannelID != binding.ChannelID || asset.AICCAccountID != binding.AICCAccountID {
					return ErrAICCRecoveryConflict
				}
			}
		}
		return tx.Create(&AICCRecoveryAudit{ActorUserID: actor, TargetUserID: target, GroupID: groupID, Evidence: evidence, ChannelID: binding.ChannelID, AICCAccountID: binding.AICCAccountID}).Error
	})
}
