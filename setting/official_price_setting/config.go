package official_price_setting

import (
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

// 官方 API 牌价配置:管理员维护的各模型官方定价,仅供前端成本估算展示,
// 不参与网关计费。单价单位固定为 CNY / 百万 token,与前端价目表语义一致。

// OfficialPriceItem 单个模型的官方牌价
type OfficialPriceItem struct {
	// Model 模型名,匹配时忽略大小写
	Model string `json:"model"`
	// Vendor 厂商标识,用于估算结果分组
	Vendor string `json:"vendor"`
	// VendorLabel 厂商显示名,缺省回退到前端内置映射
	VendorLabel string `json:"vendor_label,omitempty"`
	// Input 输入单价(缓存未命中)
	Input float64 `json:"input"`
	// CachedInput 输入单价(缓存命中),nil 时前端回退为 Input
	CachedInput *float64 `json:"cached_input,omitempty"`
	// Output 输出单价
	Output float64 `json:"output"`
}

type OfficialPriceSetting struct {
	Items []OfficialPriceItem `json:"items"`
}

// OfficialPriceItemsOptionKey options 表中存放牌价数组的键,
// 经 model.UpdateOption 写入后由 handleConfigUpdate 分发到本配置
const OfficialPriceItemsOptionKey = "official_price_setting.items"

const maxOfficialPriceItems = 500

var officialPriceSetting = OfficialPriceSetting{}

func init() {
	config.GlobalConfig.Register("official_price_setting", &officialPriceSetting)
}

// GetOfficialPriceSetting 获取当前牌价配置
func GetOfficialPriceSetting() *OfficialPriceSetting {
	return &officialPriceSetting
}

// ValidateOfficialPriceItems 校验牌价列表:模型/厂商非空、单价为正且非 NaN、
// 模型名不重复(忽略大小写)
func ValidateOfficialPriceItems(items []OfficialPriceItem) error {
	if len(items) > maxOfficialPriceItems {
		return fmt.Errorf("official price items must not exceed %d entries", maxOfficialPriceItems)
	}
	seen := make(map[string]struct{}, len(items))
	for i := range items {
		item := &items[i]
		model := strings.ToLower(strings.TrimSpace(item.Model))
		if model == "" {
			return fmt.Errorf("items[%d].model is required", i)
		}
		if len(item.Model) > 255 {
			return fmt.Errorf("items[%d].model is too long", i)
		}
		vendor := strings.TrimSpace(item.Vendor)
		if vendor == "" {
			return fmt.Errorf("items[%d].vendor is required", i)
		}
		if len(item.Vendor) > 64 || len(item.VendorLabel) > 64 {
			return fmt.Errorf("items[%d].vendor or vendor_label is too long", i)
		}
		if !(item.Input > 0) || math.IsNaN(item.Input) {
			return fmt.Errorf("items[%d].input must be a positive number", i)
		}
		if !(item.Output > 0) || math.IsNaN(item.Output) {
			return fmt.Errorf("items[%d].output must be a positive number", i)
		}
		if item.CachedInput != nil && (*item.CachedInput < 0 || math.IsNaN(*item.CachedInput)) {
			return fmt.Errorf("items[%d].cached_input must be a non-negative number", i)
		}
		if _, dup := seen[model]; dup {
			return fmt.Errorf("items[%d]: duplicate model %q", i, item.Model)
		}
		seen[model] = struct{}{}
	}
	return nil
}
