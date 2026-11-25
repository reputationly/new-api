package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

// 发往上游的 User-Agent：默认不发；系统设置了就用设置值；请求里显式设置的（渠道请求头覆盖、
// 适配器自带）一律保留。
func TestOutboundUserAgent(t *testing.T) {
	type seen struct {
		has bool
		ua  string
	}
	got := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, has := r.Header["User-Agent"]
		got <- seen{has: has, ua: r.Header.Get("User-Agent")}
	}))
	defer srv.Close()

	prevClient := GetHttpClient()
	prevUA := system_setting.GetOutboundUserAgent()
	t.Cleanup(func() {
		SetHttpClientForTest(prevClient)
		system_setting.SetOutboundUserAgent(prevUA)
	})
	InitHttpClient()

	send := func(explicit *string) seen {
		req, err := http.NewRequest(http.MethodPost, srv.URL, nil)
		require.NoError(t, err)
		if explicit != nil {
			req.Header.Set("User-Agent", *explicit)
		}
		resp, err := GetHttpClient().Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		return <-got
	}

	system_setting.SetOutboundUserAgent("")
	require.Equal(t, seen{has: false}, send(nil), "默认不发 User-Agent，而不是 Go-http-client/1.1")

	system_setting.SetOutboundUserAgent("Disguised/1.0")
	require.Equal(t, seen{has: true, ua: "Disguised/1.0"}, send(nil))

	explicit := "kling-sdk/1.0"
	require.Equal(t, seen{has: true, ua: explicit}, send(&explicit), "显式设置的 UA 优先")
}
