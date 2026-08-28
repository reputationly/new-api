package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

/*
handler 级的下单路径测试。

这一层是本次真正缺的：resolveTopupPackage 的单测全绿（那个函数本身没错），但
handler 里把套餐价格的覆盖放在了 `payMoney < 0.01` 守卫**之后**，而套餐请求的
req.Amount 为 0——守卫直接拦下，覆盖永远执行不到，套餐一单也下不出来。

纯函数测得再密也覆盖不了接线。凡是「算对了但没接对」的缺陷，只能靠打到 handler
的用例抓住。
*/

// enableAlipayForTest 填上 isAlipayEnabled 需要的三个配置项。
//
// 不填的话 handler 第一行就返回「支付宝直连未配置」，后面的断言全都恒真——
// 本文件第一版就是这样，「不含金额错误」在从未走到金额守卫的情况下永远成立。
// 用的是假凭据：断言只到「金额守卫已被跳过」为止，不需要真的连上支付宝。
func enableAlipayForTest(t *testing.T) {
	t.Helper()
	prevId, prevPriv, prevPub := setting.AlipayAppId, setting.AlipayPrivateKey, setting.AlipayPublicKey
	setting.AlipayAppId = "test-app-id"
	setting.AlipayPrivateKey = "test-private-key"
	setting.AlipayPublicKey = "test-public-key"
	t.Cleanup(func() {
		setting.AlipayAppId, setting.AlipayPrivateKey, setting.AlipayPublicKey = prevId, prevPriv, prevPub
	})
	require.True(t, isAlipayEnabled(), "前提：支付宝必须处于已配置状态，否则 handler 会在入口直接返回")
}

func enableWxpayForTest(t *testing.T) {
	t.Helper()
	prev := []string{
		setting.WxpayAppId, setting.WxpayMchId, setting.WxpayPrivateKey,
		setting.WxpayApiV3Key, setting.WxpayCertSerial, setting.WxpayPublicKey,
	}
	setting.WxpayAppId = "test-app-id"
	setting.WxpayMchId = "test-mch-id"
	setting.WxpayPrivateKey = "test-private-key"
	setting.WxpayApiV3Key = "test-api-v3-key"
	setting.WxpayCertSerial = "test-cert-serial"
	setting.WxpayPublicKey = "test-public-key"
	t.Cleanup(func() {
		setting.WxpayAppId, setting.WxpayMchId, setting.WxpayPrivateKey = prev[0], prev[1], prev[2]
		setting.WxpayApiV3Key, setting.WxpayCertSerial, setting.WxpayPublicKey = prev[3], prev[4], prev[5]
	})
}

// callPayHandler 用给定 JSON body 调用一个下单 handler。
func callPayHandler(t *testing.T, h gin.HandlerFunc, userId int, body string) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/pay", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", userId)
	h(ctx)

	var out map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &out))
	return out
}

// TestRequestAlipay_PackageOrderNotRejectedByAmountGuard 回归用例：套餐下单不得被
// 散充的最小金额守卫拦下。
//
// 请求体只有 package_id、没有 amount，所以 req.Amount = 0。若实现是「先按散充算、
// 再用套餐价覆盖」，getDirectPayMoney(0) 返回 0，守卫会以「充值金额过低」直接返回，
// 而覆盖那行在守卫下方，永远执行不到。
//
// 本用例不依赖支付宝服务可用：断言的是**没有因为金额被拒**，而不是下单成功——
// 走到调用支付宝 SDK 那一步就说明守卫已经被正确跳过了。
func TestRequestAlipay_PackageOrderNotRejectedByAmountGuard(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopupPackage{}, &model.TopUp{}))
	enableAlipayForTest(t)

	prevMin := setting.AlipayMinTopUp
	setting.AlipayMinTopUp = 10
	t.Cleanup(func() { setting.AlipayMinTopUp = prevMin })

	pkg := model.TopupPackage{Title: "入门包", PriceAmount: 100, GrantPoints: 500, Enabled: true}
	require.NoError(t, pkg.Insert())

	out := callPayHandler(t, RequestAlipay, 3001,
		`{"package_id": `+strconv.Itoa(pkg.Id)+`}`)

	data, _ := out["data"].(string)
	require.NotContains(t, data, "充值金额过低",
		"套餐下单被散充的最小金额守卫拦下——套餐价的覆盖必须在守卫之前生效")
	require.NotContains(t, data, "充值数量不能小于",
		"套餐不适用最小充值额")
}

func TestRequestWxpay_PackageOrderNotRejectedByAmountGuard(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopupPackage{}, &model.TopUp{}))
	enableWxpayForTest(t)

	prevMin := setting.WxpayMinTopUp
	setting.WxpayMinTopUp = 10
	t.Cleanup(func() { setting.WxpayMinTopUp = prevMin })

	pkg := model.TopupPackage{Title: "入门包", PriceAmount: 100, Enabled: true}
	require.NoError(t, pkg.Insert())

	out := callPayHandler(t, RequestWxpay, 3002,
		`{"package_id": `+strconv.Itoa(pkg.Id)+`}`)

	data, _ := out["data"].(string)
	require.NotContains(t, data, "支付金额无效",
		"套餐下单被散充的金额守卫拦下——套餐价的覆盖必须在守卫之前生效")
	require.NotContains(t, data, "充值数量不能小于")
}

// TestRequestAlipay_InvalidPackageRejectedEarly 不存在 / 已下架的套餐必须在下单前
// 就被拒，且给出套餐相关的错误而不是金额错误——后者会让用户以为是自己填错了金额。
func TestRequestAlipay_InvalidPackageRejectedEarly(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopupPackage{}, &model.TopUp{}))
	enableAlipayForTest(t)

	t.Run("套餐不存在", func(t *testing.T) {
		out := callPayHandler(t, RequestAlipay, 3003, `{"package_id": 99999}`)
		require.Equal(t, "error", out["message"])
		require.Contains(t, out["data"], "套餐")
	})

	t.Run("已下架", func(t *testing.T) {
		pkg := model.TopupPackage{Title: "下架包", PriceAmount: 100, Enabled: false}
		require.NoError(t, pkg.Insert())

		out := callPayHandler(t, RequestAlipay, 3003,
			`{"package_id": `+strconv.Itoa(pkg.Id)+`}`)
		require.Equal(t, "error", out["message"])
		require.Contains(t, out["data"], "下架")
	})
}

// TestPackagePurchaseKey_IndependentOfProvider 限购锁必须与支付渠道无关。
//
// LockUserPayCreation 的 key 带 provider，同一用户走支付宝和微信并不互斥；限购却是
// 跨支付方式的，两边各下一单且都支付成功就能突破限制。
func TestPackagePurchaseKey_IndependentOfProvider(t *testing.T) {
	require.Equal(t, packagePurchaseKey(1, 2), packagePurchaseKey(1, 2))
	require.NotEqual(t, packagePurchaseKey(1, 2), packagePurchaseKey(1, 3))
	require.NotEqual(t, packagePurchaseKey(1, 2), packagePurchaseKey(2, 2))

	// 与按 provider 分的下单锁不共用命名空间，避免误互斥
	require.NotEqual(t, packagePurchaseKey(1, 2), userPayCreationKey(1, "alipay"))
}
