package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/require"
)

// 对公转账入账：分 → quota。
//
// 单位切换后 QPU = 7,300,000,000，必须先乘 QPU 再除汇率——decimal 除法默认只保留 16 位
// 小数，先算「分 ÷ 100 ÷ 汇率」会把 1/7.3 截断，再乘 QPU 落在 999999.9999…，
// IntPart 后每笔少到账 1 quota（¥1 只到账 ¥0.999999）。
func TestBankTransferQuotaForFenMultipliesBeforeDividing(t *testing.T) {
	originalRate := operation_setting.USDExchangeRate
	originalQPU := common.QuotaPerUnit
	t.Cleanup(func() {
		operation_setting.USDExchangeRate = originalRate
		common.QuotaPerUnit = originalQPU
	})
	operation_setting.USDExchangeRate = 7.3

	common.QuotaPerUnit = 7300000000
	for _, c := range []struct {
		fen   int64
		quota int
	}{
		{100, 1_000_000_000},     // ¥1
		{1, 10_000_000},          // ¥0.01
		{12345, 123_450_000_000}, // ¥123.45
	} {
		got, err := BankTransferQuotaForFen(c.fen)
		require.NoError(t, err, "%d 分", c.fen)
		require.Equal(t, c.quota, got, "%d 分应到账 %d quota", c.fen, c.quota)
	}

	// 旧单位下与改动前逐位相同（截断语义不变，只是把乘法提到除法之前）
	common.QuotaPerUnit = 500000
	for _, c := range []struct {
		fen   int64
		quota int
	}{
		{100, 68493},
		{1, 684},
		{12345, 8455479},
	} {
		got, err := BankTransferQuotaForFen(c.fen)
		require.NoError(t, err, "%d 分", c.fen)
		require.Equal(t, c.quota, got, "%d 分应到账 %d quota", c.fen, c.quota)
	}
}

// 汇率非法时保持原有错误语义（拒绝入账，不产生额度）。
func TestBankTransferQuotaForFenRejectsInvalidRate(t *testing.T) {
	originalRate := operation_setting.USDExchangeRate
	t.Cleanup(func() { operation_setting.USDExchangeRate = originalRate })

	operation_setting.USDExchangeRate = 0
	quota, err := BankTransferQuotaForFen(100)
	require.Error(t, err)
	require.Zero(t, quota)
}
