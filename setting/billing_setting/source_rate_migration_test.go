package billing_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/require"
)

func TestPlanGlobalModelSourceRateMigration(t *testing.T) {
	const oldRate = `{"gpt-5.5":6.8,"ordinary":6.8,"tier-name":6.8}`
	const modes = `{"gpt-5.5":"tiered_expr","tier-name":"tiered_expr"}`
	const gptExpr = `v2:len <= 272000 ? tier("≤272K", p * 5 * fx + c * 30 * fx + cr * 0.5 * fx) : tier(">272K", p * 10 * fx + c * 45 * fx + cr * fx)`
	const exprs = `{"gpt-5.5":"v2:len <= 272000 ? tier(\"≤272K\", p * 5 * fx + c * 30 * fx + cr * 0.5 * fx) : tier(\">272K\", p * 10 * fx + c * 45 * fx + cr * fx)","tier-name":"v2:tier(\"fx\", p * 2)"}`
	scalar, legacy, err := PlanGlobalModelSourceRateMigration(oldRate, modes, exprs)
	require.NoError(t, err)
	require.True(t, legacy)
	require.Equal(t, "6.8", scalar)

	rate, err := ParseGlobalModelSourceRate(scalar)
	require.NoError(t, err)
	for _, tc := range []struct {
		length float64
		cost   float64
	}{
		{272000, 241.4},
		{272001, 380.8},
	} {
		params := billingexpr.TokenParams{P: 1, C: 1, CR: 1, Len: tc.length, FX: rate}
		got, _, err := billingexpr.RunExprWithRequest(gptExpr, params, billingexpr.RequestInput{})
		require.NoError(t, err)
		require.InDelta(t, tc.cost, got, 1e-10)
	}
}

func TestPlanGlobalModelSourceRateMigrationRejectsAmbiguity(t *testing.T) {
	for _, input := range []string{`{"a":6.8,"b":7}`, `{"a":0}`, `{"a":null}`, `{"a":"6.8"}`} {
		_, _, err := PlanGlobalModelSourceRateMigration(input, "", "")
		require.Error(t, err, input)
	}
	_, _, err := PlanGlobalModelSourceRateMigration(`{"a":6.8}`, `{"b":"tiered_expr"}`, `{"b":"v2:p * fx"}`)
	require.ErrorContains(t, err, "没有旧汇率")

	scalar, legacy, err := PlanGlobalModelSourceRateMigration("{}", "", "")
	require.NoError(t, err)
	require.True(t, legacy)
	require.Empty(t, scalar)

	_, legacy, err = PlanGlobalModelSourceRateMigration("6.8", "", "")
	require.NoError(t, err)
	require.False(t, legacy)
}
