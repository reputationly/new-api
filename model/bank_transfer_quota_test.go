package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// 对公转账入账：分 → quota。
//
// 单位切换后 QPU = 7,300,000,000，必须先乘 QPU 再除汇率——decimal 除法默认只保留 16 位
// 小数，先算「分 ÷ 100 ÷ 汇率」会把 1/7.3 截断，再乘 QPU 落在 999999.9999…，
// IntPart 后每笔少到账 1 quota（¥1 只到账 ¥0.999999）。
func TestBankTransferQuotaForFenMultipliesBeforeDividing(t *testing.T) {
	originalQPU := common.QuotaPerUnit
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQPU
	})

	common.QuotaPerUnit = 7300000000
	for _, c := range []struct {
		fen   int64
		quota int
	}{
		{100, 1_000_000_000},     // ¥1
		{1, 10_000_000},          // ¥0.01
		{12345, 123_450_000_000}, // ¥123.45
		{10000, 100_000_000_000}, // ¥100
	} {
		got, err := BankTransferQuotaForFen(c.fen)
		require.NoError(t, err, "%d 分", c.fen)
		require.Equal(t, c.quota, got, "%d 分应到账 %d quota", c.fen, c.quota)
	}

	common.QuotaPerUnit = 500000
	for _, c := range []struct {
		fen   int64
		quota int
	}{
		{100, 1_000_000_000},
		{1, 10_000_000},
		{12345, 123_450_000_000},
	} {
		got, err := BankTransferQuotaForFen(c.fen)
		require.NoError(t, err, "%d 分", c.fen)
		require.Equal(t, c.quota, got, "%d 分应到账 %d quota", c.fen, c.quota)
	}
}

func TestBankTransferQuotaForFenRejectsInvalidAmount(t *testing.T) {
	quota, err := BankTransferQuotaForFen(0)
	require.Error(t, err)
	require.Zero(t, quota)
}
