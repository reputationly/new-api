package arkasset

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// 移动云官方签名示例（https://ecloud.10086.cn/op-help-center/doc/article/24283）：
// AccessKey=testid / SecretKey=testsecret / GET /api/keypair，文档给出的签名是
// 2976158792407a581305786b2a82c56d9b007362。这条用例把算法钉死，改实现必须同步核对。
//
// 注意文档正文的示例 URL 写的是 /api/v2/keypair，但签名只对 /api/keypair 成立——文档自身
// 不一致，以签名结果为准。
func TestEcloudSignatureMatchesOfficialVector(t *testing.T) {
	params := map[string]string{
		"AccessKey":        "testid",
		"SignatureMethod":  "HmacSHA1",
		"SignatureNonce":   "9d81ffbeaaf7477390db5df577bb3299",
		"SignatureVersion": "V2.0",
		"Timestamp":        "2017-01-11T15:15:11Z",
		"Version":          "2016-12-05",
	}
	require.Equal(t,
		"2976158792407a581305786b2a82c56d9b007362",
		ecloudSignature(http.MethodGet, "/api/keypair", params, "testsecret"))
}

// 移动云要的是北京时间墙钟值 + 字面 Z；发 UTC 会被判 Invalid parameter Timestamp（实测）。
// 这条用例把时区语义单独钉住，避免有人「顺手」改回 UTC。
func TestEcloudTimestampIsBeijingWallClock(t *testing.T) {
	client := NewEcloudClient(EcloudConfig{AccessKey: "AK", SecretKey: "SK"}, http.DefaultClient)
	// 2023-11-14T22:13:20Z 是 UTC；北京时间应比它早 8 小时「跨到次日」。
	client.now = func() time.Time { return time.Date(2023, 11, 14, 22, 13, 20, 0, time.UTC) }
	req, err := http.NewRequest(http.MethodGet, "https://ecloud.10086.cn/api/x", nil)
	require.NoError(t, err)

	client.sign(req, "/api/x")

	require.Equal(t, "2023-11-15T06:13:20Z", req.URL.Query().Get("Timestamp"))
}

func TestEcloudFindGroupSignsRequestAndMatchesExactName(t *testing.T) {
	var gotPath, gotRawQuery string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotRawQuery = r.URL.Path, r.URL.RawQuery
		raw, _ := io.ReadAll(r.Body)
		_ = common.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		// groupName 是模糊搜索，先返回一个名字更长的近似项，客户端必须只认精确同名。
		_, _ = w.Write([]byte(`{"requestId":"r","state":"OK","body":{"data":[
			{"groupId":"group-fuzzy","groupName":"new-api-channel-70-old"},
			{"groupId":"group-exact","groupName":"new-api-channel-70"}],"total":2}}`))
	}))
	defer srv.Close()

	client := NewEcloudClient(EcloudConfig{Endpoint: srv.URL, AccessKey: "AK", SecretKey: "SK"}, srv.Client())
	client.now = func() time.Time { return time.Unix(1700000000, 0) }

	id, err := client.FindGroup(context.Background(), "new-api-channel-70")
	require.NoError(t, err)
	require.Equal(t, "group-exact", id)

	require.Equal(t, "/api/openapi-maas/exp/aicc/v2/asset-group/query", gotPath)
	require.Equal(t, float64(1), gotBody["pageNo"])
	require.Equal(t, "AIGC", gotBody["groupType"])
	require.Equal(t, "new-api-channel-70", gotBody["groupName"])

	q, err := url.ParseQuery(gotRawQuery)
	require.NoError(t, err)
	require.Equal(t, "AK", q.Get("AccessKey"))
	require.Equal(t, "2023-11-15T06:13:20Z", q.Get("Timestamp"), "Timestamp 必须是北京时间的墙钟值再拼 Z")
	require.Equal(t, "V2.0", q.Get("SignatureVersion"))
	require.Equal(t, "HmacSHA1", q.Get("SignatureMethod"))
	require.NotEmpty(t, q.Get("SignatureNonce"))

	// 发出去的 query 必须能用同一套规则还原出同一个签名：签了 A 却发了 B 是这类对接最常见的坑。
	signed := map[string]string{}
	for _, k := range []string{"AccessKey", "SignatureMethod", "SignatureNonce", "SignatureVersion", "Timestamp"} {
		signed[k] = q.Get(k)
	}
	require.Equal(t, ecloudSignature(http.MethodPost, gotPath, signed, "SK"), q.Get("Signature"))
}

func TestEcloudCreateAssetThenPollStatus(t *testing.T) {
	var createBody map[string]any
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/openapi-maas/exp/aicc/v2/asset":
			raw, _ := io.ReadAll(r.Body)
			_ = common.Unmarshal(raw, &createBody)
			_, _ = w.Write([]byte(`{"requestId":"r","state":"OK","body":"asset-2026"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/openapi-maas/exp/aicc/v2/asset/asset-2026":
			polls++
			if polls == 1 {
				_, _ = w.Write([]byte(`{"state":"OK","body":{"assetId":"asset-2026","status":"PROCESSING"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"state":"OK","body":{"assetId":"asset-2026","status":"FAILED","errorMessage":"人脸素材未通过"}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := NewEcloudClient(EcloudConfig{Endpoint: srv.URL, AccessKey: "AK", SecretKey: "SK"}, srv.Client())

	id, err := client.CreateAsset(context.Background(), "group-1", "https://x/face.jpg", AssetTypeImage, "newapi-abc")
	require.NoError(t, err)
	require.Equal(t, "asset-2026", id)
	require.Equal(t, "group-1", createBody["groupId"])
	require.Equal(t, "https://x/face.jpg", createBody["assetUrl"])
	require.Equal(t, AssetTypeImage, createBody["assetType"])
	require.Equal(t, "newapi-abc", createBody["assetName"])

	asset, err := client.GetAsset(context.Background(), "asset-2026")
	require.NoError(t, err)
	require.Equal(t, StatusProcessing, asset.Status)

	asset, err = client.GetAsset(context.Background(), "asset-2026")
	require.NoError(t, err)
	require.Equal(t, StatusFailed, asset.Status)
	require.Equal(t, "人脸素材未通过", asset.Error.Message)
}

// 地址不是移动云（例如误配成方舟或别的网关）时要明确指出协议可能选错，而不是给一句上游 502。
func TestEcloudNonEnvelopeIsProtocolMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"timestamp":"2026-11-03T06:27:14.643+00:00","path":"/","status":400,"error":"Bad Request"}`))
	}))
	defer srv.Close()

	client := NewEcloudClient(EcloudConfig{Endpoint: srv.URL, AccessKey: "AK", SecretKey: "SK"}, srv.Client())
	_, err := client.FindGroup(context.Background(), "g")
	var mismatch *ProtocolMismatchError
	require.True(t, errors.As(err, &mismatch))
	require.Equal(t, ProtocolEcloudLabel, mismatch.Protocol)
	require.Contains(t, err.Error(), "协议可能选错")
}

func TestEcloudErrorEnvelopeBecomesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"state":"ERROR","errorCode":"MISSING_PARAMETER","errorMessage":"Input parameter AccessKey missing"}`))
	}))
	defer srv.Close()

	client := NewEcloudClient(EcloudConfig{Endpoint: srv.URL, AccessKey: "AK", SecretKey: "SK"}, srv.Client())
	_, err := client.FindGroup(context.Background(), "g")
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, "MISSING_PARAMETER", apiErr.Code)
	require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
}

// 素材 ID 只在同一账号下可复用：换 AccessKey 必须换 scope，否则会把别人的 asset:// 发出去。
func TestEcloudScopeIsolatesByCredential(t *testing.T) {
	a := EcloudScope(EcloudConfig{AccessKey: "AK1", SecretKey: "SECRET-ONE"})
	b := EcloudScope(EcloudConfig{AccessKey: "AK2", SecretKey: "SECRET-TWO"})
	require.NotEqual(t, a, b)
	require.Contains(t, a, EcloudDefaultEndpoint)
	require.Contains(t, a, ProtocolEcloud)
	require.NotContains(t, a, "SECRET-ONE", "SecretKey 不进缓存键")
}

// 代码 502/504 的 HTML 错误页来自网关或代理，不是协议问题——判成「协议选错」会把排查方向带偏。
func TestEcloudGatewayErrorPageIsNotProtocolMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`<html><body>502 Bad Gateway</body></html>`))
	}))
	defer srv.Close()

	client := NewEcloudClient(EcloudConfig{Endpoint: srv.URL, AccessKey: "AK", SecretKey: "SK"}, srv.Client())
	_, err := client.FindGroup(context.Background(), "g")
	var mismatch *ProtocolMismatchError
	require.False(t, errors.As(err, &mismatch))
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, http.StatusBadGateway, apiErr.HTTPStatus)
}
