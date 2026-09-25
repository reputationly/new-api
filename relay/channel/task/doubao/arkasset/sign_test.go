package arkasset

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 期望值由按官方文档独立实现的 Python 脚本算出，不是拿本实现自证。
func TestSignRequestMatchesReferenceVector(t *testing.T) {
	body := []byte(`{"GroupId":"group-1","URL":"https://x/a.jpg","AssetType":"Image","ProjectName":"default"}`)
	// query 故意乱序：签名必须按 key 排序，不能依赖调用方拼 URL 的顺序。
	req, err := http.NewRequest(http.MethodPost,
		"https://ark.cn-beijing.volcengineapi.com/?Version=2024-01-01&Action=CreateAsset", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	signRequest(req, body, "AKLTexample", "c2VjcmV0LWtleQ==", time.Date(2026, 9, 25, 1, 2, 3, 0, time.UTC))

	require.Equal(t, "20260925T010203Z", req.Header.Get("X-Date"))
	require.Equal(t, "482297bafa237d18a991a8ec515370cacb837ef896e791443ea90c636fd62b7e", req.Header.Get("X-Content-Sha256"))
	require.Equal(t,
		"HMAC-SHA256 Credential=AKLTexample/20260925/cn-beijing/ark/request, "+
			"SignedHeaders=content-type;host;x-content-sha256;x-date, "+
			"Signature=1f4e0d918f6e6da4567ae55ed6323d541bf14fc4639aac848e97a867eac3d846",
		req.Header.Get("Authorization"))
}
