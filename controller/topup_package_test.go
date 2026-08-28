package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// withAmountDiscount 铺一条散充的按额支付折扣，用于验证套餐**不受它影响**。
func withAmountDiscount(t *testing.T, discounts map[int]float64) {
	t.Helper()
	s := operation_setting.GetPaymentSetting()
	prev := s.AmountDiscount
	s.AmountDiscount = discounts
	t.Cleanup(func() { s.AmountDiscount = prev })
}

// TestResolveTopupPackage_IgnoresAmountDiscount 套餐售价不得再走散充的金额折扣。
//
// AmountDiscount 是散充的按额支付折扣（充 1000 打 9 折）。套餐售价是自己定的，
// 两条路径都生效的话，¥1000 的套餐会被 AmountDiscount[1000] 再打一次折——
// **静默的重复让利**，订单金额直接少 10%，而账面上完全看不出来（设计 §6.3）。
func TestResolveTopupPackage_IgnoresAmountDiscount(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopupPackage{}, &model.TopUp{}))
	withAmountDiscount(t, map[int]float64{1000: 0.9})

	pkg := model.TopupPackage{
		Title:       "专业版",
		PriceAmount: 1000,
		GrantPoints: 5000,
		Enabled:     true,
	}
	require.NoError(t, db.Create(&pkg).Error)

	_, payMoney, _, err := resolveTopupPackage(1, pkg.Id)
	require.NoError(t, err)
	require.InDelta(t, 1000.0, payMoney, 1e-9,
		"套餐售价必须原样收取，不得叠加散充的金额折扣")
	require.NotEqual(t, 900.0, payMoney)
}

// TestResolveTopupPackage_OneToOneCredit 到账额度与售价 1:1（所见即所得）。
func TestResolveTopupPackage_OneToOneCredit(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopupPackage{}, &model.TopUp{}))

	pkg := model.TopupPackage{Title: "入门包", PriceAmount: 100, Enabled: true}
	require.NoError(t, db.Create(&pkg).Error)

	_, payMoney, quota, err := resolveTopupPackage(1, pkg.Id)
	require.NoError(t, err)

	expected := int64(payMoney * common.QuotaPerUnit / operation_setting.Price)
	require.Equal(t, expected, quota)
	require.Greater(t, quota, int64(0))
}

func TestResolveTopupPackage_Rejects(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopupPackage{}, &model.TopUp{}))

	t.Run("套餐不存在", func(t *testing.T) {
		_, _, _, err := resolveTopupPackage(1, 99999)
		require.Error(t, err)
	})

	t.Run("已下架的套餐不可购买", func(t *testing.T) {
		// 走真实的 Insert：GORM 的 default:true 会把 Enabled=false 覆盖掉，
		// 用 db.Create 绕开它就测不到这个坑
		pkg := model.TopupPackage{Title: "已下架", PriceAmount: 100, Enabled: false}
		require.NoError(t, pkg.Insert())

		// 两条断言缺一不可：GORM 的 Create 回调会在 INSERT 前改写结构体字段，
		// 只查 DB 的话，「库里对了但结构体没同步」这一半永远测不到——而结构体正是
		// 接口序列化回客户端的那份数据
		require.False(t, pkg.Enabled,
			"Insert 后结构体本身也必须反映真实状态，它会被 ApiSuccess 直接回给调用方")

		var saved model.TopupPackage
		require.NoError(t, db.First(&saved, pkg.Id).Error)
		require.False(t, saved.Enabled,
			"新建时选择「不上架」必须被保留——default:true 会把 false 覆盖成 true，导致套餐直接开卖")

		_, _, _, err := resolveTopupPackage(1, pkg.Id)
		require.Error(t, err)
		require.Contains(t, err.Error(), "下架")
	})

	t.Run("售价为 0 的套餐被拒", func(t *testing.T) {
		pkg := model.TopupPackage{Title: "零元购", PriceAmount: 0, Enabled: true}
		require.NoError(t, db.Create(&pkg).Error)

		_, _, _, err := resolveTopupPackage(1, pkg.Id)
		require.Error(t, err)
	})
}

// TestResolveTopupPackage_MaxPurchaseLimit 购买次数上限只统计**成功**的订单。
//
// 把待支付订单也算进去的话，用户下单后放弃支付就会永久占掉一个名额，
// 而他并没有买到任何东西。
func TestResolveTopupPackage_MaxPurchaseLimit(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopupPackage{}, &model.TopUp{}))

	pkg := model.TopupPackage{
		Title: "限购一次", PriceAmount: 100, Enabled: true, MaxPurchasePerUser: 1,
	}
	require.NoError(t, db.Create(&pkg).Error)

	t.Run("未购买时放行", func(t *testing.T) {
		_, _, _, err := resolveTopupPackage(1001, pkg.Id)
		require.NoError(t, err)
	})

	t.Run("待支付订单不占名额", func(t *testing.T) {
		require.NoError(t, db.Create(&model.TopUp{
			UserId: 1001, PackageId: pkg.Id, TradeNo: "pending-1",
			Status: common.TopUpStatusPending,
		}).Error)

		_, _, _, err := resolveTopupPackage(1001, pkg.Id)
		require.NoError(t, err, "未支付的订单不该占用购买名额")
	})

	t.Run("已成功的订单占名额", func(t *testing.T) {
		require.NoError(t, db.Create(&model.TopUp{
			UserId: 1001, PackageId: pkg.Id, TradeNo: "success-1",
			Status: common.TopUpStatusSuccess,
		}).Error)

		_, _, _, err := resolveTopupPackage(1001, pkg.Id)
		require.Error(t, err)
		require.Contains(t, err.Error(), "上限")
	})

	t.Run("其他用户不受影响", func(t *testing.T) {
		_, _, _, err := resolveTopupPackage(1002, pkg.Id)
		require.NoError(t, err)
	})
}

// TestTopupPackageUpdateSelectIncludesZeroableFields 把赠送积分改回 0、把套餐下架，
// 都是正常操作，非零字段更新做不到。漏进白名单的表现是保存成功但没存上。
func TestTopupPackageUpdateSelectIncludesZeroableFields(t *testing.T) {
	fields := model.TopupPackageUpdateSelectFieldsForTest()
	for _, f := range []string{"grant_points", "enabled", "max_purchase_per_user"} {
		require.Contains(t, fields, f,
			"%s 可以被改成零值，必须在 Update 的 Select 白名单里", f)
	}
}
