package doubao

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel/task/doubao/arkasset"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// buildWithAssets 走真实入口：校验请求 → BuildRequestBody，返回发给上游的请求体。
func buildWithAssets(t *testing.T, raw string, settings dto.ChannelOtherSettings) (*requestPayload, error) {
	t.Helper()
	return buildWithAssetsOn(t, "https://ark.cn-beijing.volces.com", raw, settings)
}

func buildWithAssetsOn(t *testing.T, baseURL, raw string, settings dto.ChannelOtherSettings) (*requestPayload, error) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", bytes.NewReader([]byte(raw)))
	c.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: 7, ChannelBaseUrl: baseURL, ApiKey: "sk-channel",
			ChannelOtherSettings: settings,
		},
	}
	a := &TaskAdaptor{}
	a.Init(info)
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))

	reader, err := a.BuildRequestBody(c, info)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(reader)
	require.NoError(t, err)
	var p requestPayload
	require.NoError(t, common.Unmarshal(b, &p))
	return &p, nil
}

type assetCall struct {
	scope     string
	groupName string
	items     []arkasset.Item
}

func stubAssets(t *testing.T, err error) *assetCall {
	t.Helper()
	call := &assetCall{}
	orig := resolveArkAssets
	resolveArkAssets = func(_ context.Context, _ arkasset.AssetAPI, scope, groupName string, items []arkasset.Item) (map[string]string, error) {
		call.scope, call.groupName, call.items = scope, groupName, items
		if err != nil {
			return nil, err
		}
		ids := map[string]string{}
		for i, it := range items {
			ids[it.URL] = "asset-" + string(rune('a'+i))
		}
		return ids, nil
	}
	t.Cleanup(func() { resolveArkAssets = orig })
	return call
}

const referenceRequest = `{
	"model": "doubao-seedance-2-0-260128",
	"prompt": "人物对着镜头微笑",
	"metadata": {"content": [
		{"type": "text", "text": "人物对着镜头微笑"},
		{"type": "image_url", "image_url": {"url": "https://x/face.jpg"}, "role": "reference_image"},
		{"type": "video_url", "video_url": {"url": "https://x/ref.mp4"}, "role": "reference_video"},
		{"type": "audio_url", "audio_url": {"url": "https://x/voice.mp3"}, "role": "reference_audio"},
		{"type": "image_url", "image_url": {"url": "asset://asset-already"}, "role": "reference_image"}
	]}
}`

func TestArkAssetsRewritesImagesAndVideosOnly(t *testing.T) {
	call := stubAssets(t, nil)
	p, err := buildWithAssets(t, referenceRequest, dto.ChannelOtherSettings{ArkAssetEnabled: true})
	require.NoError(t, err)

	require.Equal(t, []arkasset.Item{
		{URL: "https://x/face.jpg", Type: arkasset.AssetTypeImage},
		{URL: "https://x/ref.mp4", Type: arkasset.AssetTypeVideo},
	}, call.items, "音频与已是 asset:// 的不入库")
	require.Equal(t, "new-api-channel-7", call.groupName)

	byRole := contentByRole(p.Content)
	require.Equal(t, "asset://asset-a", byRole["reference_image"][0].ImageURL.URL)
	require.Equal(t, "asset://asset-already", byRole["reference_image"][1].ImageURL.URL)
	require.Equal(t, "asset://asset-b", byRole["reference_video"][0].VideoURL.URL)
	require.Equal(t, "https://x/voice.mp3", byRole["reference_audio"][0].AudioURL.URL)
}

// 协议与地址的判定规则：显式指定压过自动识别，自动识别按 API 地址的 host，
// 认不出时保持改动前的「方舟兼容网关」语义。
func TestResolveAssetBackend(t *testing.T) {
	cases := []struct {
		name     string
		baseURL  string
		settings dto.ChannelOtherSettings
		protocol string
		endpoint string
	}{
		{
			"火山官方换用素材域名",
			"https://ark.cn-beijing.volces.com",
			dto.ChannelOtherSettings{},
			arkasset.ProtocolArk, "https://ark.cn-beijing.volcengineapi.com",
		},
		{
			"兼容网关素材与 API 同域",
			"https://realmdrama.cn/",
			dto.ChannelOtherSettings{},
			arkasset.ProtocolArk, "https://realmdrama.cn",
		},
		{
			"显式素材库地址压过 host 识别",
			"https://zhenze-huhehaote.cmecloud.cn",
			dto.ChannelOtherSettings{ArkAssetEndpoint: "https://assets.example.com/"},
			arkasset.ProtocolArk, "https://assets.example.com",
		},
		{
			"移动云按 host 自动识别",
			"https://zhenze-huhehaote.cmecloud.cn",
			dto.ChannelOtherSettings{},
			arkasset.ProtocolEcloud, arkasset.EcloudDefaultEndpoint,
		},
		{
			"手动指定移动云",
			"https://ark.cn-beijing.volces.com",
			dto.ChannelOtherSettings{ArkAssetProtocol: arkasset.ProtocolEcloud},
			arkasset.ProtocolEcloud, arkasset.EcloudDefaultEndpoint,
		},
		{
			"移动云忽略残留的素材库地址",
			"https://ark.cn-beijing.volces.com",
			dto.ChannelOtherSettings{ArkAssetProtocol: arkasset.ProtocolEcloud, ArkAssetEndpoint: "https://ec.example.com/"},
			arkasset.ProtocolEcloud, arkasset.EcloudDefaultEndpoint,
		},
		{
			"手动指定方舟压过移动云 host",
			"https://zhenze-huhehaote.cmecloud.cn",
			dto.ChannelOtherSettings{ArkAssetProtocol: arkasset.ProtocolArk},
			arkasset.ProtocolArk, "https://zhenze-huhehaote.cmecloud.cn",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveAssetBackend(tc.settings, tc.baseURL)
			require.Equal(t, tc.protocol, got.Protocol)
			require.Equal(t, tc.endpoint, got.Endpoint)
		})
	}
}

// 缓存隔离键必须带上协议、地址与凭据身份，否则同一 URL 在两种协议/两个账号之间会复用错素材 ID。
func TestAssetScopeCarriesProtocolEndpointAndCredential(t *testing.T) {
	call := stubAssets(t, nil)
	_, err := buildWithAssets(t, referenceRequest, dto.ChannelOtherSettings{ArkAssetEnabled: true})
	require.NoError(t, err)
	// 没填 AK/SK 时身份取渠道 API Key 的摘要（不落明文），与方舟兼容网关的 Bearer 路径一致。
	require.Regexp(t, `^https://ark\.cn-beijing\.volcengineapi\.com\|default\|bearer:[0-9a-f]{16}$`, call.scope)

	call = stubAssets(t, nil)
	_, err = buildWithAssets(t, referenceRequest, dto.ChannelOtherSettings{
		ArkAssetEnabled: true, ArkAssetEndpoint: "https://assets.example.com/", ArkAssetProjectName: "p1",
		ArkAssetAccessKey: " AK ", ArkAssetSecretKey: "SK",
	})
	require.NoError(t, err)
	require.Equal(t, "https://assets.example.com|p1|AK", call.scope)

	call = stubAssets(t, nil)
	_, err = buildWithAssetsOn(t, "https://zhenze-huhehaote.cmecloud.cn", referenceRequest, dto.ChannelOtherSettings{
		ArkAssetEnabled: true, ArkAssetAccessKey: "ECAK", ArkAssetSecretKey: "ECSK",
	})
	require.NoError(t, err)
	require.Equal(t, arkasset.EcloudScope(arkasset.EcloudConfig{
		Endpoint: arkasset.EcloudDefaultEndpoint, AccessKey: "ECAK",
	}), call.scope)
	require.Contains(t, call.scope, arkasset.ProtocolEcloud)
}

// 移动云走的是统一签名，没有「AK/SK 留空回落到渠道 API Key」这条退路。
func TestEcloudAssetsRequireCredentials(t *testing.T) {
	stubAssets(t, nil)
	_, err := buildWithAssetsOn(t, "https://zhenze-huhehaote.cmecloud.cn", referenceRequest,
		dto.ChannelOtherSettings{ArkAssetEnabled: true})
	var apiErr *types.NewAPIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	require.True(t, types.IsSkipRetryError(apiErr))
}

func TestAssetEndpointFollowsCompatibleGatewayBaseURL(t *testing.T) {
	require.Equal(t, "https://realmdrama.cn", assetEndpoint(dto.ChannelOtherSettings{}, "https://realmdrama.cn/"))
}

func TestArkAssetsDisabledLeavesURLsUntouched(t *testing.T) {
	call := stubAssets(t, nil)
	p, err := buildWithAssets(t, referenceRequest, dto.ChannelOtherSettings{})
	require.NoError(t, err)
	require.Nil(t, call.items, "未开启时不应访问素材库")
	require.Equal(t, "https://x/face.jpg", contentByRole(p.Content)["reference_image"][0].ImageURL.URL)
}

func TestArkAssetsErrorMapping(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		status    int
		skipRetry bool
	}{
		{"素材预处理失败是输入问题", &arkasset.FailedError{URL: "https://x/face.jpg", Code: "X", Message: "bad"}, http.StatusBadRequest, true},
		{"参数不合法是输入问题", &arkasset.APIError{Action: "CreateAsset", Code: "InvalidParameter", Message: "m"}, http.StatusBadRequest, true},
		{"协议选错要指出协议与地址", &arkasset.ProtocolMismatchError{Protocol: "移动云 MoMA REST 协议", Endpoint: "https://ecloud.10086.cn", Sample: "{}"}, http.StatusBadGateway, false},
		{"网络/超时可换渠道重试", context.DeadlineExceeded, http.StatusBadGateway, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubAssets(t, tc.err)
			_, err := buildWithAssets(t, referenceRequest, dto.ChannelOtherSettings{ArkAssetEnabled: true})
			var apiErr *types.NewAPIError
			require.True(t, errors.As(err, &apiErr))
			require.Equal(t, tc.status, apiErr.StatusCode)
			require.Equal(t, tc.skipRetry, types.IsSkipRetryError(apiErr))
		})
	}
}
