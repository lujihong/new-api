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
	ID            int64     `gorm:"primaryKey" json:"id"`
	UserID        int       `gorm:"not null;index" json:"user_id"`
	TokenHash     string    `gorm:"size:64;not null;uniqueIndex" json:"-"`
	ChannelID     int       `gorm:"default:0;index" json:"channel_id"`
	AICCAccountID string    `gorm:"size:64;index" json:"aicc_account_id"`
	ExpiresAt     time.Time `gorm:"not null;index" json:"expires_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (AICCAuthSession) TableName() string { return "aicc_auth_sessions" }

func aiccTokenHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func RecordAICCAuthSession(userID int, token string, expiresIn int, channelID ...int) error {
	token = strings.TrimSpace(token)
	if userID <= 0 || token == "" || expiresIn <= 0 || int64(expiresIn) > int64((1<<63-1)/time.Second) {
		return errors.New("invalid AICC auth session")
	}
	cID := 0
	if len(channelID) > 0 {
		cID = channelID[0]
	}
	return recordAICCAuthSession(userID, token, expiresIn, AICCBinding{ChannelID: cID})
}

func RecordBoundAICCAuthSession(userID int, token string, expiresIn int, binding AICCBinding) error {
	if !binding.Valid() || userID <= 0 || strings.TrimSpace(token) == "" || expiresIn <= 0 || int64(expiresIn) > int64((1<<63-1)/time.Second) {
		return errors.New("invalid AICC auth session binding")
	}
	return recordAICCAuthSession(userID, token, expiresIn, binding)
}

func recordAICCAuthSession(userID int, token string, expiresIn int, binding AICCBinding) error {
	if DB == nil {
		return errors.New("AICC database unavailable")
	}
	session := AICCAuthSession{UserID: userID, TokenHash: aiccTokenHash(token), ChannelID: binding.ChannelID, AICCAccountID: binding.AICCAccountID, ExpiresAt: time.Now().Add(time.Duration(expiresIn) * time.Second)}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&session).Error; err != nil {
			return err
		}
		var existing AICCAuthSession
		if err := tx.Where("token_hash = ?", session.TokenHash).First(&existing).Error; err != nil {
			return err
		}
		if existing.UserID != userID || existing.ChannelID != binding.ChannelID || existing.AICCAccountID != binding.AICCAccountID {
			return errors.New("AICC auth session ownership conflict")
		}
		// Retries must not reassign the owner or extend an existing token's lifetime.
		return nil
	})
}

func GetAICCAuthSessionBinding(userID int, token string) (AICCBinding, error) {
	var session AICCAuthSession
	if DB == nil || userID <= 0 || strings.TrimSpace(token) == "" {
		return AICCBinding{}, errors.New("认证会话不可用")
	}
	if err := DB.Where("user_id = ? AND token_hash = ? AND expires_at > ?", userID, aiccTokenHash(token), time.Now()).First(&session).Error; err != nil {
		return AICCBinding{}, err
	}
	binding := AICCBinding{ChannelID: session.ChannelID, AICCAccountID: session.AICCAccountID}
	if !binding.Valid() {
		return AICCBinding{}, errors.New("历史认证会话尚未绑定账号，请重新认证")
	}
	return binding, nil
}

func GetAICCAuthSessionChannelID(userID int, token string) (int, error) {
	if userID <= 0 || strings.TrimSpace(token) == "" {
		return 0, nil
	}
	if DB == nil {
		return 0, errors.New("AICC database unavailable")
	}
	var session AICCAuthSession
	err := DB.Select("channel_id").Where("user_id = ? AND token_hash = ? AND expires_at > ?", userID, aiccTokenHash(token), time.Now()).First(&session).Error
	if err != nil {
		return 0, err
	}
	return session.ChannelID, nil
}

func UserOwnsAICCAuthSession(userID int, token string) (bool, error) {
	if userID <= 0 || strings.TrimSpace(token) == "" {
		return false, nil
	}
	if DB == nil {
		return false, errors.New("AICC database unavailable")
	}
	var count int64
	err := DB.Model(&AICCAuthSession{}).Where("user_id = ? AND token_hash = ? AND expires_at > ?", userID, aiccTokenHash(token), time.Now()).Count(&count).Error
	return count > 0, err
}
