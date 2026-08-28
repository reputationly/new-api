package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// 充值套餐。设计见 docs/user-tier-pricing-and-topup-package-design.md §6。

// resolveTopupPackage 校验套餐并算出实付金额与到账额度。
//
// 套餐**不走** getDirectPayMoney / getQuotaFromMoney 那条散充链路，有两个原因：
//
//  1. payment_setting.AmountDiscount 是散充的按额支付折扣（充 100 打 9 折，
//     controller/topup.go:216）。套餐售价是自己定的，两条路径都生效的话，
//     ¥1000 的套餐会被 AmountDiscount[1000] 再打一次折——**静默的重复让利**，
//     账面上完全看不出来（设计 §6.3）。
//  2. TopupGroupRatio 是按分组的充值汇率。套餐已经用赠送积分让利了，再叠一层
//     分组汇率就是双重优惠，而运营在套餐配置页上看不到这一层。
//
// 所以套餐一律 **1:1 所见即所得**：付 ¥1000，到账价值 ¥1000 的额度，外加配置的积分。
func resolveTopupPackage(userId, packageId int) (*model.TopupPackage, float64, int64, error) {
	pkg, err := model.GetTopupPackageById(packageId)
	if err != nil {
		return nil, 0, 0, errors.New("充值套餐不存在")
	}
	if !pkg.Enabled {
		return nil, 0, 0, errors.New("该套餐已下架")
	}
	if pkg.PriceAmount < 0.01 {
		return nil, 0, 0, errors.New("套餐售价配置有误")
	}
	if pkg.MaxPurchasePerUser > 0 {
		count, err := model.CountUserPackagePurchases(userId, pkg.Id)
		if err != nil {
			return nil, 0, 0, errors.New("校验购买次数失败")
		}
		if count >= int64(pkg.MaxPurchasePerUser) {
			return nil, 0, 0, errors.New("已达到该套餐的购买次数上限")
		}
	}

	payMoney := pkg.PriceAmount
	// 1:1 到账：金额 × QuotaPerUnit ÷ Price，与 CNY 展示模式的换算一致。
	// 先乘后除，只在最后截断一次，误差恒 < 1 内部单位。
	internalQuota := decimal.NewFromFloat(payMoney).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
		Div(decimal.NewFromFloat(operation_setting.Price)).
		IntPart()
	if internalQuota <= 0 {
		return nil, 0, 0, errors.New("套餐到账额度计算异常")
	}
	return pkg, payMoney, internalQuota, nil
}

// GetTopupPackages GET /api/topup_package/ 用户侧：只回上架的套餐。
//
// 支付方式不在这里回——充值页已有的 /api/user/topup_info 会按实际配置动态返回
// payMethods（配了支付宝就有 alipay_direct，没配就没有）。套餐页复用它即可，
// 在这里再实现一遍判断会多出一处要同步维护的真相。
func GetTopupPackages(c *gin.Context) {
	list, err := model.GetTopupPackages(true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// GetAllTopupPackages GET /api/topup_package/admin 管理侧：含已下架的。
func GetAllTopupPackages(c *gin.Context) {
	list, err := model.GetTopupPackages(false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

func validateTopupPackage(pkg *model.TopupPackage) error {
	pkg.Title = strings.TrimSpace(pkg.Title)
	if pkg.Title == "" {
		return errors.New("套餐名称不能为空")
	}
	if pkg.PriceAmount < 0.01 {
		return errors.New("售价必须大于 0")
	}
	if pkg.GrantPoints < 0 {
		return errors.New("赠送积分不能为负")
	}
	if pkg.MaxPurchasePerUser < 0 {
		return errors.New("购买次数上限不能为负")
	}
	if strings.TrimSpace(pkg.Currency) == "" {
		pkg.Currency = "CNY"
	}
	return nil
}

func CreateTopupPackage(c *gin.Context) {
	var pkg model.TopupPackage
	if err := c.ShouldBindJSON(&pkg); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	if err := validateTopupPackage(&pkg); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	pkg.Id = 0
	if err := pkg.Insert(); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, &pkg)
}

func UpdateTopupPackage(c *gin.Context) {
	var pkg model.TopupPackage
	if err := c.ShouldBindJSON(&pkg); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	if pkg.Id <= 0 {
		common.ApiErrorMsg(c, "缺少套餐 ID")
		return
	}
	if err := validateTopupPackage(&pkg); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if err := pkg.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, &pkg)
}

// DeleteTopupPackage 删除套餐。
//
// 不连带处理历史订单：TopUp.PackageId 保留原值，用于对账与「购买次数」统计。
// GrantTopupPackageBonus 查不到套餐时只记日志不发积分——删掉在售套餐后仍有未支付
// 订单的话，那笔订单到账时不会有赠品，这是刻意的：套餐都删了，赠品条款也就不存在了。
func DeleteTopupPackageHandler(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的套餐 ID")
		return
	}
	if err := model.DeleteTopupPackage(id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// packageOrderSubject 支付订单的商品名。
func packageOrderSubject(pkg *model.TopupPackage) string {
	return fmt.Sprintf("充值套餐-%s", pkg.Title)
}
