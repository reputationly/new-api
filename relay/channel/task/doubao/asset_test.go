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
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", bytes.NewReader([]byte(raw)))
	c.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: 7, ChannelBaseUrl: "https://ark.cn-beijing.volces.com", ApiKey: "sk-channel",
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
	cfg       arkasset.Config
	groupName string
	items     []arkasset.Item
}

func stubAssets(t *testing.T, err error) *assetCall {
	t.Helper()
	call := &assetCall{}
	orig := resolveArkAssets
	resolveArkAssets = func(_ context.Context, cfg arkasset.Config, _ *http.Client, groupName string, items []arkasset.Item) (map[string]string, error) {
		call.cfg, call.groupName, call.items = cfg, groupName, items
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

// 官方生成接口与素材 Action 不同域；没填 AK/SK 时回落到渠道 API Key。
func TestArkAssetsConfigDefaults(t *testing.T) {
	call := stubAssets(t, nil)
	_, err := buildWithAssets(t, referenceRequest, dto.ChannelOtherSettings{ArkAssetEnabled: true})
	require.NoError(t, err)
	require.Equal(t, "https://ark.cn-beijing.volcengineapi.com", call.cfg.Endpoint)
	require.Equal(t, "default", call.cfg.ProjectName)
	require.Equal(t, "sk-channel", call.cfg.APIKey)
	require.Empty(t, call.cfg.AccessKey)

	call = stubAssets(t, nil)
	_, err = buildWithAssets(t, referenceRequest, dto.ChannelOtherSettings{
		ArkAssetEnabled: true, ArkAssetEndpoint: "https://assets.example.com/", ArkAssetProjectName: "p1",
		ArkAssetAccessKey: " AK ", ArkAssetSecretKey: "SK",
	})
	require.NoError(t, err)
	require.Equal(t, "https://assets.example.com", call.cfg.Endpoint)
	require.Equal(t, "p1", call.cfg.ProjectName)
	require.Equal(t, "AK", call.cfg.AccessKey)
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
