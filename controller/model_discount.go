package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// ListModelDiscountModels exposes only exact declared model IDs, never channel keys.
func ListModelDiscountModels(c *gin.Context) {
	names, err := model.ExactDiscountModels()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, names)
}
