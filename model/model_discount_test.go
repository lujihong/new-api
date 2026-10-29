package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModelDiscountOptionPersistenceAndReferences(t *testing.T) {
	oldDB := DB
	oldRules, oldGroups := ratio_setting.ModelDiscountRulesJSONString(), ratio_setting.GroupRatio2JSONString()
	common.OptionMapRWMutex.Lock()
	oldOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/discount.db"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	t.Cleanup(func() {
		DB = oldDB
		ratio_setting.UpdateModelDiscountRulesJSON(oldRules)
		ratio_setting.UpdateGroupRatioByJSONString(oldGroups)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptions
		common.OptionMapRWMutex.Unlock()
		sqlDB, _ := db.DB()
		sqlDB.Close()
	})
	require.NoError(t, db.AutoMigrate(&Option{}, &User{}, &Channel{}))
	require.NoError(t, db.Create(&User{Id: 99, Username: "discount-test", AffCode: "discount-test", Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&Channel{Name: "discount-test", Key: "unused", Models: "exact-model", Group: "vip"}).Error)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"vip":1}`))
	valid := `{"groups":{"vip":{"exact-model":0.8}},"users":{"99":{"exact-model":0.7}}}`
	require.NoError(t, UpdateOption(ModelDiscountOptionKey, valid))
	require.Equal(t, 0.7, ratio_setting.CaptureModelDiscountRules().Lookup(99, "vip", "exact-model").Factor)
	var row Option
	require.NoError(t, db.Where("key = ?", ModelDiscountOptionKey).First(&row).Error)
	require.JSONEq(t, valid, row.Value)
	for _, raw := range []string{
		`{"groups":{"missing":{"exact-model":0.5}}}`,
		`{"users":{"1000":{"exact-model":0.5}}}`,
		`{"groups":{"vip":{"missing-model":0.5}}}`,
		`{"users":{"99":{"exact-model":null}}}`,
	} {
		require.Error(t, UpdateOption(ModelDiscountOptionKey, raw))
		require.Equal(t, 0.7, ratio_setting.CaptureModelDiscountRules().Lookup(99, "vip", "exact-model").Factor)
	}
	require.Error(t, updateOptionMap(ModelDiscountOptionKey, `{"groups":null}`))
	require.Equal(t, 0.7, ratio_setting.CaptureModelDiscountRules().Lookup(99, "vip", "exact-model").Factor)
	next := `{"users":{"99":{"exact-model":0.3}}}`
	require.NoError(t, UpdateModelDiscountRulesConditional(next, valid))
	require.ErrorIs(t, UpdateModelDiscountRulesConditional(`{}`, valid), ErrModelDiscountConflict)
	require.Equal(t, 0.3, ratio_setting.CaptureModelDiscountRules().Lookup(99, "vip", "exact-model").Factor)
	require.NoError(t, UpdateModelDiscountRulesConditional(valid, next))
	negativeZero := `{"groups":{"vip":{"exact-model":-0.0}},"users":{"99":{"exact-model":0.7}}}`
	require.NoError(t, UpdateModelDiscountRulesConditional(negativeZero, valid))
	require.NoError(t, UpdateModelDiscountRulesConditional(valid, ratio_setting.ModelDiscountRulesJSONString()), "negative zero and read canonical zero are equivalent")
		names, err := ExactDiscountModels()
		require.NoError(t, err)
		require.Contains(t, names, "exact-model")

		// 验证历史残留模型：即便渠道中已无 legacy-model，也必须允许管理员删除它或修改已有规则，只有新增才会拦截
		legacyBaseline := `{"users":{"99":{"exact-model":0.8,"legacy-model":0.5}}}`
		deleteLegacy := `{"users":{"99":{"exact-model":0.8}}}`
		require.NoError(t, ValidateModelDiscountReferences(deleteLegacy, legacyBaseline), "deleting legacy models must be allowed")
		modifyLegacy := `{"users":{"99":{"exact-model":0.8,"legacy-model":0.4}}}`
		require.NoError(t, ValidateModelDiscountReferences(modifyLegacy, legacyBaseline), "modifying legacy models factor must be allowed")
		addNewMissing := `{"users":{"99":{"exact-model":0.8,"brand-new-missing":0.5}}}`
		require.Error(t, ValidateModelDiscountReferences(addNewMissing, legacyBaseline), "adding brand-new missing model must be rejected")

		require.NoError(t, db.Migrator().DropTable(&Option{}))
	require.Error(t, UpdateOption(ModelDiscountOptionKey, `{"users":{"99":{"exact-model":0.2}}}`))
	require.Equal(t, 0.7, ratio_setting.CaptureModelDiscountRules().Lookup(99, "vip", "exact-model").Factor, "failed DB write must not publish new prices")
}
