package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

const ModelDiscountOptionKey = "ModelDiscountRules"

// Serialize persistence/publication and periodic reload so a stale DB read or
// slower writer cannot publish older rules after a newer commit in this process.
var modelDiscountOptionMu sync.Mutex

var ErrModelDiscountConflict = errors.New("折扣规则已被其他管理员修改，请重新加载后合并修改")

func ExactDiscountModels() ([]string, error) {
	var channels []Channel
	if err := DB.Select("models").Find(&channels).Error; err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, channel := range channels {
		for _, name := range channel.GetModels() {
			set[name] = true
		}
	}
	result := make([]string, 0, len(set))
	for name := range set {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

// Expected is the exact configuration read by the editor. Row locking protects
// concurrent writers in different application processes as well as this one.
func UpdateModelDiscountRulesConditional(value, expected string) error {
	if err := ValidateModelDiscountReferences(value); err != nil {
		return err
	}
	modelDiscountOptionMu.Lock()
	defer modelDiscountOptionMu.Unlock()
	err := DB.Transaction(func(tx *gorm.DB) error {
		seed := Option{Key: ModelDiscountOptionKey, Value: "{}"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		var current Option
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("key = ?", ModelDiscountOptionKey).First(&current).Error; err != nil {
			return err
		}
		// Use the same normalization as the read path, including negative zero.
		left, err := ratio_setting.CanonicalModelDiscountRulesJSON(current.Value)
		if err != nil {
			return err
		}
		right, err := ratio_setting.CanonicalModelDiscountRulesJSON(expected)
		if err != nil {
			return ErrModelDiscountConflict
		}
		if left != right {
			return ErrModelDiscountConflict
		}
		return tx.Model(&Option{}).Where("key = ?", ModelDiscountOptionKey).Update("value", value).Error
	})
	if err != nil {
		return err
	}
	return updateOptionMap(ModelDiscountOptionKey, value)
}

// Reference checks run on writes only. Loading previously saved rules must not
// fail because an administrator subsequently disabled a channel or user.
func ValidateModelDiscountReferences(raw string) error {
	if err := ratio_setting.ValidateModelDiscountRulesJSON(raw); err != nil {
		return err
	}
	var rules ratio_setting.ModelDiscountRules
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return err
	}
	for group := range rules.Groups {
		if !ratio_setting.ContainsGroupRatio(group) {
			return fmt.Errorf("客户组不存在：%s", group)
		}
	}
	for id := range rules.Users {
		userID, _ := strconv.Atoi(id) // Canonical positive IDs have already been validated.
		var count int64
		if err := DB.Model(&User{}).Where("id = ?", userID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("用户不存在或已删除：%s", id)
		}
	}
	requested := map[string]bool{}
	for _, models := range rules.Groups {
		for name := range models {
			requested[name] = true
		}
	}
	for _, models := range rules.Users {
		for name := range models {
			requested[name] = true
		}
	}
	if len(requested) == 0 {
		return nil
	}
	// Public metadata supports aliases/patterns; channel declarations are the
	// exact callable IDs and do not require reading any channel credentials.
	var channels []Channel
	if err := DB.Select("models").Find(&channels).Error; err != nil {
		return err
	}
	for _, channel := range channels {
		for _, name := range channel.GetModels() {
			delete(requested, name)
		}
	}
	for name := range requested {
		return fmt.Errorf("模型未在渠道中配置，请选择精确模型 ID：%s", name)
	}
	return nil
}
