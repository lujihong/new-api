package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// An attestation records a root administrator's evidence, not an automatic
// provider identity check. Changed credentials require a new attestation.
type AICCAccountAttestation struct {
	ID                int64     `gorm:"primaryKey" json:"id"`
	ChannelID         int       `gorm:"not null;index" json:"channelId"`
	AccountID         string    `gorm:"size:64;not null;index" json:"accountId"`
	UpstreamSubject   string    `gorm:"size:255;not null" json:"upstreamSubject"`
	ConfigFingerprint string    `gorm:"size:64;not null" json:"-"`
	ActorUserID       int       `gorm:"not null" json:"actorUserId"`
	Evidence          string    `gorm:"type:text;not null" json:"evidence"`
	Revoked           bool      `json:"revoked"`
	CreatedAt         time.Time `json:"createdAt"`
}

func LatestAICCAccountAttestation(channelID int) (AICCAccountAttestation, error) {
	var row AICCAccountAttestation
	if DB == nil || channelID <= 0 {
		return row, errors.New("invalid AICC channel")
	}
	err := DB.Where("channel_id = ?", channelID).Order("id DESC").First(&row).Error
	return row, err
}

// DeriveAICCAccountID binds the upstream subject to the canonical resource
// domain. Video credentials are verified separately by the config fingerprint;
// a key rotation therefore keeps the same account ID while a new video domain
// does not collide with the old one.
func DeriveAICCAccountID(channel Channel, subject string) (string, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", errors.New("upstream subject is required")
	}
	info := channel.GetOtherInfo()
	stringField := func(key string) string {
		value, _ := info[key].(string)
		return strings.TrimSpace(value)
	}
	videoDomain, err := canonicalAICCDomainURL(channel.GetBaseURL())
	if err != nil {
		return "", err
	}
	assetDomain, err := canonicalAICCDomainURL(stringField("endpoint"))
	if err != nil {
		return "", err
	}
	wire, err := common.Marshal([]string{
		"mobile-cloud-account-domain/v2", subject, videoDomain, assetDomain, stringField("pool_id"),
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(wire)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalAICCDomainURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid AICC resource domain")
	}
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = strings.TrimSuffix(u.Host, ":"+u.Port())
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

// The callback computes a fingerprint and account ID from the locked current
// channel, keeping evidence submission from attesting stale or caller-chosen
// credentials.
func RecordAICCAccountAttestation(row AICCAccountAttestation, fingerprint func(Channel) (string, error)) error {
	row.UpstreamSubject = strings.TrimSpace(row.UpstreamSubject)
	row.Evidence = strings.TrimSpace(row.Evidence)
	if DB == nil || row.ChannelID <= 0 || row.ActorUserID <= 0 || len(row.ConfigFingerprint) != 64 || row.UpstreamSubject == "" || len(row.UpstreamSubject) > 255 || len(row.Evidence) < 10 || len(row.Evidence) > 2000 {
		return errors.New("请填写真实上游账号标识及可追溯的核实依据")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := lockForUpdate(tx).Where("id = ?", row.ChannelID).First(&channel).Error; err != nil {
			return err
		}
		current, err := fingerprint(channel)
		if err != nil {
			return err
		}
		if current != row.ConfigFingerprint {
			return errors.New("渠道配置已变化，请重新核实后提交")
		}
		derivedAccountID, err := DeriveAICCAccountID(channel, row.UpstreamSubject)
		if err != nil {
			return err
		}
		if row.AccountID != "" && row.AccountID != derivedAccountID {
			return errors.New("账号标识与锁定渠道资源域不匹配")
		}
		row.AccountID = derivedAccountID
		row.ID = 0
		row.CreatedAt = time.Now()
		return tx.Create(&row).Error
	})
}
