package official_price_setting

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/setting/config"
)

func TestValidateOfficialPriceItems(t *testing.T) {
	validCached := 0.4
	tests := []struct {
		name    string
		items   []OfficialPriceItem
		wantErr bool
	}{
		{
			name:  "empty list is valid",
			items: nil,
		},
		{
			name: "valid multi-vendor items",
			items: []OfficialPriceItem{
				{Model: "DeepSeek-V4.1-Flash", Vendor: "deepseek", Input: 2, CachedInput: &validCached, Output: 8},
				{Model: "glm-5.3", Vendor: "zhipu", VendorLabel: "Zhipu GLM", Input: 8, Output: 28},
			},
		},
		{
			name:    "blank model",
			items:   []OfficialPriceItem{{Model: "  ", Vendor: "v", Input: 1, Output: 1}},
			wantErr: true,
		},
		{
			name:    "blank vendor",
			items:   []OfficialPriceItem{{Model: "m", Vendor: "", Input: 1, Output: 1}},
			wantErr: true,
		},
		{
			name:    "zero input",
			items:   []OfficialPriceItem{{Model: "m", Vendor: "v", Input: 0, Output: 1}},
			wantErr: true,
		},
		{
			name:    "zero output",
			items:   []OfficialPriceItem{{Model: "m", Vendor: "v", Input: 1, Output: 0}},
			wantErr: true,
		},
		{
			name:    "NaN input",
			items:   []OfficialPriceItem{{Model: "m", Vendor: "v", Input: math.NaN(), Output: 1}},
			wantErr: true,
		},
		{
			name:    "negative cached input",
			items:   []OfficialPriceItem{{Model: "m", Vendor: "v", Input: 1, CachedInput: &[]float64{-0.1}[0], Output: 1}},
			wantErr: true,
		},
		{
			name: "duplicate model ignoring case",
			items: []OfficialPriceItem{
				{Model: "glm-5.3", Vendor: "zhipu", Input: 8, Output: 28},
				{Model: "GLM-5.3", Vendor: "zhipu", Input: 8, Output: 28},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOfficialPriceItems(tt.items)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestConfigRoundTripThroughOptionMap 验证 options 键值经全局配置管理器
// 分发后能整体替换 Items(与 model.UpdateOption 的实际分发路径一致)
func TestConfigRoundTripThroughOptionMap(t *testing.T) {
	cfg := &OfficialPriceSetting{
		Items: []OfficialPriceItem{{Model: "old", Vendor: "v", Input: 1, Output: 2}},
	}
	stored, err := config.ConfigToMap(cfg)
	require.NoError(t, err)
	require.Contains(t, stored, "items")

	next := &OfficialPriceSetting{}
	err = config.UpdateConfigFromMap(next, map[string]string{
		"items": `[{"model":"glm-5.3","vendor":"zhipu","vendor_label":"Zhipu GLM","input":8,"output":28}]`,
	})
	require.NoError(t, err)
	require.Len(t, next.Items, 1)
	assert.Equal(t, "glm-5.3", next.Items[0].Model)
	assert.Equal(t, "Zhipu GLM", next.Items[0].VendorLabel)
	assert.Equal(t, 8.0, next.Items[0].Input)
	assert.Nil(t, next.Items[0].CachedInput)

	// 空数组必须清空列表,而不是保留旧值
	err = config.UpdateConfigFromMap(next, map[string]string{"items": `[]`})
	require.NoError(t, err)
	assert.Empty(t, next.Items)
}
