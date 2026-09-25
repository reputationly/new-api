package arkasset

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type capturedRequest struct {
	action, version, auth, xDate, body string
}

func assetServer(t *testing.T, reply string, status int) (*httptest.Server, *capturedRequest) {
	t.Helper()
	got := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*got = capturedRequest{
			action: r.URL.Query().Get("Action"), version: r.URL.Query().Get("Version"),
			auth: r.Header.Get("Authorization"), xDate: r.Header.Get("X-Date"), body: string(b),
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestClientUsesBearerWithoutAKSK(t *testing.T) {
	srv, got := assetServer(t, `{"ResponseMetadata":{},"Result":{"Id":"asset-1"}}`, 200)
	c := NewClient(Config{Endpoint: srv.URL, APIKey: "sk-channel"}, srv.Client())

	id, err := c.CreateAsset(context.Background(), "group-1", "https://x/a.jpg", AssetTypeImage, "n")
	require.NoError(t, err)
	require.Equal(t, "asset-1", id)
	require.Equal(t, "CreateAsset", got.action)
	require.Equal(t, "2024-01-01", got.version)
	require.Equal(t, "Bearer sk-channel", got.auth)
	require.Contains(t, got.body, `"ProjectName":"default"`)
}

func TestClientSignsWithAKSK(t *testing.T) {
	srv, got := assetServer(t, `{"ResponseMetadata":{},"Result":{"Id":"asset-1","Status":"Active"}}`, 200)
	c := NewClient(Config{Endpoint: srv.URL, AccessKey: "AK", SecretKey: "SK", APIKey: "sk-ignored"}, srv.Client())

	a, err := c.GetAsset(context.Background(), "asset-1")
	require.NoError(t, err)
	require.Equal(t, StatusActive, a.Status)
	require.True(t, strings.HasPrefix(got.auth, "HMAC-SHA256 Credential=AK/"), got.auth)
	require.NotEmpty(t, got.xDate)
}

// 业务错误可能伴随 HTTP 200，只看状态码会把失败当成功。
func TestClientReportsBusinessErrorOnHTTP200(t *testing.T) {
	srv, _ := assetServer(t, `{"ResponseMetadata":{"Error":{"Code":"InvalidParameter","Message":"C400999: 素材服务错误"}}}`, 200)
	c := NewClient(Config{Endpoint: srv.URL, APIKey: "k"}, srv.Client())

	_, err := c.CreateAsset(context.Background(), "g", "https://x/a.jpg", AssetTypeImage, "n")
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, "InvalidParameter", apiErr.Code)
	require.Contains(t, apiErr.Message, "C400999")
}

func TestFindGroupRequiresExactName(t *testing.T) {
	srv, _ := assetServer(t, `{"ResponseMetadata":{},"Result":{"Items":[{"Id":"g-1","Name":"new-api-channel-10"},{"Id":"g-2","Name":"new-api-channel-1"}]}}`, 200)
	c := NewClient(Config{Endpoint: srv.URL, APIKey: "k"}, srv.Client())

	id, err := c.FindGroup(context.Background(), "new-api-channel-1")
	require.NoError(t, err)
	require.Equal(t, "g-2", id, "名称过滤是模糊匹配，必须取精确同名的组")
}
