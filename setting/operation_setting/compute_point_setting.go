package operation_setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// ComputePointSetting 算力点配置。
//
// 算力点是套餐的计费单位，与积分同构地以 quota unit 记账（见 common/compute_points.go）。
// 这里**只有一个换算率**——刻意不建模型价目表：模型消耗多少算力点由现有倍率体系
// 算出的 quota 直接换算，新模型上架零配置、不会漏配，单价也不随汇率波动。
//
// 运营唯一要决策的就是这个面值。数字太小（1 元 = 10 点）套餐显得小气，太大
// （1 元 = 10000 点）用户算不清。默认 1 元 = 100 点，与积分同口径。
type ComputePointSetting struct {
	// QuotaPerComputePoint 1 算力点对应多少 quota unit。默认 ≈684.93（1 点 = 1 分钱）。
	QuotaPerComputePoint float64 `json:"quota_per_compute_point"`
	// ShowcaseModels 套餐对比表里展示换算的代表模型（设计文档 §8.3）。
	// 由运营挑而不是按权益范围自动展开：通配符（qwen3-*）一展开就是几十行，
	// 客户要的是「这点数大概能干多少事」，几个代表模型就回答了。
	ShowcaseModels []string `json:"showcase_models"`
}

var computePointSetting = ComputePointSetting{
	QuotaPerComputePoint: common.QuotaPerUnit / 730.0,
	ShowcaseModels:       []string{},
}

func init() {
	config.GlobalConfig.Register("compute_point_setting", &computePointSetting)
	// 依赖倒置：把实时换算率注入 common 换算层（common 不能 import 本包）
	common.QuotaPerComputePointFunc = func() float64 { return computePointSetting.QuotaPerComputePoint }
	// 汇率同理注入。算力点的「元 ↔ 点」预览要用它，而 Price 定义在本包。
	common.USDExchangeRateFunc = func() float64 { return Price }
}

// GetComputePointSetting 获取算力点配置。
func GetComputePointSetting() *ComputePointSetting {
	return &computePointSetting
}
