package openai

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type realtimeReserveStub struct {
	relaycommon.BillingSettler
	quota int
}

func (s *realtimeReserveStub) Reserve(target int) error {
	if target > 800_000 {
		return errors.New("insufficient quota")
	}
	s.quota = target
	return nil
}

func TestRealtimeRejectsUnreservedUsageFromClosingTotal(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	billing := &realtimeReserveStub{}
	info := &relaycommon.RelayInfo{
		Billing: billing,
		PriceData: types.PriceData{
			PricingCurrency: "CNY", ModelRatio: 1,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
		},
	}
	usage := func(n int) *dto.RealtimeUsage {
		u := &dto.RealtimeUsage{TotalTokens: n, InputTokens: n}
		u.InputTokenDetails.TextTokens = n
		return u
	}
	total := &dto.RealtimeUsage{}
	require.NoError(t, preConsumeUsage(c, info, usage(400), total))
	require.NoError(t, preConsumeUsage(c, info, usage(200), total))
	require.Error(t, preConsumeUsage(c, info, usage(400), total))
	require.Equal(t, usage(600), total)
	require.Equal(t, 600_000, billing.quota)
}
