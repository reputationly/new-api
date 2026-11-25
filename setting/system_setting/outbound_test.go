package system_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateOutboundUserAgent(t *testing.T) {
	require.NoError(t, ValidateOutboundUserAgent(""))
	require.NoError(t, ValidateOutboundUserAgent("  Mozilla/5.0 (X11; Linux x86_64)  "))
	require.Error(t, ValidateOutboundUserAgent("a\nb"))
	require.Error(t, ValidateOutboundUserAgent("a\r\nX-Evil: 1"))
}
