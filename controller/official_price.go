package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/official_price_setting"
	"github.com/gin-gonic/gin"
)

// GetOfficialPrices 返回官方牌价配置(仅前端成本估算展示用),登录用户可读
func GetOfficialPrices(c *gin.Context) {
	items := official_price_setting.GetOfficialPriceSetting().Items
	if items == nil {
		items = []official_price_setting.OfficialPriceItem{}
	}
	common.ApiSuccess(c, items)
}

// UpdateOfficialPrices 整体替换官方牌价配置,管理员可写
func UpdateOfficialPrices(c *gin.Context) {
	var request struct {
		Items []official_price_setting.OfficialPriceItem `json:"items"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	items := request.Items
	if items == nil {
		items = []official_price_setting.OfficialPriceItem{}
	}
	if err := official_price_setting.ValidateOfficialPriceItems(items); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	value, err := common.Marshal(items)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOption(official_price_setting.OfficialPriceItemsOptionKey, string(value)); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "official_price.update", map[string]any{"count": len(items)})
	common.ApiSuccess(c, gin.H{"count": len(items)})
}
