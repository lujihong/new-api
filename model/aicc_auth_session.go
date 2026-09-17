package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store only a digest: possession of an upstream token does not grant tenant ownership.
type AICCAuthSession struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	UserID    int       `gorm:"not null;index" json:"user_id"`
	TokenHash string    `gorm:"size:64;not null;uniqueIndex" json:"-"`
	ExpiresAt time.Time `gorm:"not null;index" json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (AICCAuthSession) TableName() string { return "aicc_auth_sessions" }

func aiccTokenHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func RecordAICCAuthSession(userID int, token string, expiresIn int) error {
	token = strings.TrimSpace(token)
	if userID <= 0 || token == "" || expiresIn <= 0 || int64(expiresIn) > int64((1<<63-1)/time.Second) {
		return errors.New("invalid AICC auth session")
	}
	session := AICCAuthSession{UserID: userID, TokenHash: aiccTokenHash(token), ExpiresAt: time.Now().Add(time.Duration(expiresIn) * time.Second)}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&session).Error; err != nil {
			return err
		}
		var existing AICCAuthSession
		if err := tx.Where("token_hash = ?", session.TokenHash).First(&existing).Error; err != nil {
			return err
		}
		if existing.UserID != userID {
			return errors.New("AICC auth session ownership conflict")
		}
		// Retries must not reassign the owner or extend an existing token's lifetime.
		return nil
	})
}

func UserOwnsAICCAuthSession(userID int, token string) (bool, error) {
	if userID <= 0 || strings.TrimSpace(token) == "" {
		return false, nil
	}
	var count int64
	err := DB.Model(&AICCAuthSession{}).Where("user_id = ? AND token_hash = ? AND expires_at > ?", userID, aiccTokenHash(token), time.Now()).Count(&count).Error
	return count > 0, err
}
