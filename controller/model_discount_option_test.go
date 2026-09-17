package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModelDiscountOptionRequiresExpectedValue(t *testing.T) {
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"key":"ModelDiscountRules","value":"{}"}`))
	UpdateOption(c)
	require.Equal(t, http.StatusConflict, r.Code)
	require.Contains(t, r.Body.String(), "携带原值")
}

func TestModelDiscountOptionRejectsStaleEditor(t *testing.T) {
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/discount-option.db"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB; sqlDB, _ := db.DB(); sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	require.NoError(t, db.Create(&model.Option{Key: model.ModelDiscountOptionKey, Value: `{"users":{"999":{"exact":0.3}}}`}).Error)
	old := ratio_setting.ModelDiscountRulesJSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelDiscountRulesJSON(old)) })
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"key":"ModelDiscountRules","value":"{}","expected_value":"{}"}`))
	UpdateOption(c)
	require.Equal(t, http.StatusConflict, r.Code)
	var current model.Option
	require.NoError(t, db.First(&current).Error)
	require.Contains(t, current.Value, "0.3")
	require.Equal(t, old, ratio_setting.ModelDiscountRulesJSONString())
}
