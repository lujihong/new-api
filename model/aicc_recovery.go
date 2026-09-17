package model

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AICCRecoveryAudit struct {
	ID           int64  `gorm:"primaryKey"`
	ActorUserID  int    `gorm:"not null;index"`
	TargetUserID int    `gorm:"not null;index"`
	GroupID      string `gorm:"size:255;not null;index"`
	Evidence     string `gorm:"type:text;not null"`
	CreatedAt    time.Time
}

var ErrAICCRecoveryConflict = errors.New("素材组已有其他归属，禁止覆盖")

// Recovery adds an attested historical grant, never transfers an existing grant.
func RecoverAICCGroup(actor, target int, groupID, groupType, evidence string) error {
	groupID, err := validateAICCResourceID(groupID)
	if err != nil {
		return err
	}
	evidence = strings.TrimSpace(evidence)
	if actor <= 0 || target <= 0 || evidence == "" || len(evidence) > 2000 || (groupType != "AIGC" && groupType != "LivenessFace") {
		return errors.New("invalid recovery request")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		row := AICCAssetGroupOwnership{UserID: target, GroupID: groupID, GroupType: groupType}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		var current AICCAssetGroupOwnership
		if err := tx.Where("group_id = ?", groupID).First(&current).Error; err != nil {
			return err
		}
		if current.UserID != target || current.GroupType != groupType {
			return ErrAICCRecoveryConflict
		}
		return tx.Create(&AICCRecoveryAudit{ActorUserID: actor, TargetUserID: target, GroupID: groupID, Evidence: evidence}).Error
	})
}
