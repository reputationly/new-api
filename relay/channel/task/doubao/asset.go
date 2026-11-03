package doubao

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel/task/doubao/arkasset"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// arkAssetWaitTimeout 提交请求里同步等待素材入库的上限。图片实测约 5 秒 Active，视频更慢。
const arkAssetWaitTimeout = 2 * time.Minute

// 火山官方的生成接口与素材 Action 不在同一个域名；兼容网关（界云等）两者同域。
const (
	officialArkHost       = "ark.cn-beijing.volces.com"
	officialAssetEndpoint = "https://ark.cn-beijing.volcengineapi.com"

	// 移动云 MoMA 的生成网关是 *.cmecloud.cn，素材资产库另在固定域名上（见 EcloudDefaultEndpoint）。
	ecloudHostSuffix = ".cmecloud.cn"
)

// resolveArkAssets 是入库的接缝，单测替换它以免真打素材库。
var resolveArkAssets = func(ctx context.Context, api arkasset.AssetAPI, scope, groupName string, items []arkasset.Item) (map[string]string, error) {
	return arkasset.NewUploader(api, scope, groupName).Resolve(ctx, items)
}

func assetEndpoint(settings dto.ChannelOtherSettings, baseURL string) string {
	if ep := strings.TrimSpace(settings.ArkAssetEndpoint); ep != "" {
		return strings.TrimRight(ep, "/")
	}
	if u, err := url.Parse(baseURL); err == nil && u.Host == officialArkHost {
		return officialAssetEndpoint
	}
	return strings.TrimRight(baseURL, "/")
}

// assetBackend 素材库协议 + 请求落点。协议决定用哪个客户端，地址决定打到哪个域名。
type assetBackend struct {
	Protocol string
	Endpoint string
}

// resolveAssetBackend 决定素材库协议与地址。
//
// 协议不由渠道类型唯一决定——同一个「Doubao 视频」渠道可以指向火山官方、界云这类方舟兼容
// 网关，也可以指向移动云 MoMA，三者的生成接口同构、素材接口却不同。所以：
//
//	显式指定 ark_asset_protocol 时为最高优先级（识别错了靠这个覆盖）；
//	否则按 API 地址的 host 识别，识别不出时按历史语义落到「方舟兼容网关」，与改动前一致。
func resolveAssetBackend(settings dto.ChannelOtherSettings, baseURL string) assetBackend {
	explicitEndpoint := strings.TrimSpace(settings.ArkAssetEndpoint)
	switch strings.TrimSpace(settings.ArkAssetProtocol) {
	case arkasset.ProtocolEcloud:
		// 刻意忽略 ArkAssetEndpoint：移动云素材库只在固定域名上，而这个字段是方舟/兼容网关的
		// 语义，从别的协议切过来时往往残留旧值，跟着它走会打到不认识移动云协议的地址上。
		return assetBackend{Protocol: arkasset.ProtocolEcloud, Endpoint: arkasset.EcloudDefaultEndpoint}
	case arkasset.ProtocolArk:
		return assetBackend{Protocol: arkasset.ProtocolArk, Endpoint: assetEndpoint(settings, baseURL)}
	}
	// 自动识别。显式填了素材库地址时保持存量语义：那是「方舟兼容网关」，地址以填写为准。
	if explicitEndpoint == "" {
		if u, err := url.Parse(baseURL); err == nil && strings.HasSuffix(u.Hostname(), ecloudHostSuffix) {
			return assetBackend{Protocol: arkasset.ProtocolEcloud, Endpoint: arkasset.EcloudDefaultEndpoint}
		}
	}
	return assetBackend{Protocol: arkasset.ProtocolArk, Endpoint: assetEndpoint(settings, baseURL)}
}

// buildAssetAPI 按协议构造客户端，并给出素材 ID 的缓存隔离键。
func buildAssetAPI(backend assetBackend, settings dto.ChannelOtherSettings, apiKey string, httpClient *http.Client) (arkasset.AssetAPI, string, error) {
	accessKey := strings.TrimSpace(settings.ArkAssetAccessKey)
	secretKey := strings.TrimSpace(settings.ArkAssetSecretKey)

	if backend.Protocol == arkasset.ProtocolEcloud {
		// 移动云统一签名必须用 AccessKey/SecretKey，没有「回落到渠道 API Key」这一说。
		if accessKey == "" || secretKey == "" {
			return nil, "", types.NewErrorWithStatusCode(
				errors.New("移动云素材库需要 AccessKey / SecretKey（移动云统一签名鉴权），当前未配置"),
				types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		cfg := arkasset.EcloudConfig{Endpoint: backend.Endpoint, AccessKey: accessKey, SecretKey: secretKey}
		return arkasset.NewEcloudClient(cfg, httpClient), arkasset.EcloudScope(cfg), nil
	}

	cfg := arkasset.Config{
		Endpoint:    backend.Endpoint,
		AccessKey:   accessKey,
		SecretKey:   secretKey,
		APIKey:      apiKey,
		ProjectName: strings.TrimSpace(settings.ArkAssetProjectName),
	}
	if cfg.ProjectName == "" {
		cfg.ProjectName = "default"
	}
	return arkasset.NewClient(cfg, httpClient), arkasset.Scope(cfg), nil
}

// collectAssetItems 挑出要入库的媒体：http(s) 的图片与视频。音频不涉及人脸不入库；
// data: 说明入站卸载没开或失败了，素材库只收公网 URL，只能原样直传；asset:// 已是素材。
func collectAssetItems(content []ContentItem) []arkasset.Item {
	var items []arkasset.Item
	for _, it := range content {
		if it.ImageURL != nil && isHTTPURL(it.ImageURL.URL) {
			items = append(items, arkasset.Item{URL: it.ImageURL.URL, Type: arkasset.AssetTypeImage})
		}
		if it.VideoURL != nil && isHTTPURL(it.VideoURL.URL) {
			items = append(items, arkasset.Item{URL: it.VideoURL.URL, Type: arkasset.AssetTypeVideo})
		}
	}
	return items
}

func isHTTPURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

func applyAssetIDs(content []ContentItem, ids map[string]string) {
	for i := range content {
		if m := content[i].ImageURL; m != nil {
			if id, ok := ids[m.URL]; ok {
				m.URL = "asset://" + id
			}
		}
		if m := content[i].VideoURL; m != nil {
			if id, ok := ids[m.URL]; ok {
				m.URL = "asset://" + id
			}
		}
	}
}

// useArkAssets 把 content 里的参考图 / 视频换成素材库引用。
func (a *TaskAdaptor) useArkAssets(c *gin.Context, info *relaycommon.RelayInfo, body *requestPayload) error {
	items := collectAssetItems(body.Content)
	if len(items) == 0 {
		return nil
	}
	settings := info.ChannelOtherSettings
	backend := resolveAssetBackend(settings, a.baseURL)
	httpClient := service.GetHttpClient()
	if proxy := info.ChannelSetting.Proxy; proxy != "" {
		pc, err := service.NewProxyHttpClient(proxy)
		if err != nil {
			return fmt.Errorf("new proxy http client failed: %w", err)
		}
		httpClient = pc
	}
	api, scope, err := buildAssetAPI(backend, settings, a.apiKey, httpClient)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), arkAssetWaitTimeout)
	defer cancel()
	start := time.Now()
	ids, err := resolveArkAssets(ctx, api, scope, "new-api-channel-"+strconv.Itoa(info.ChannelId), items)
	if err != nil {
		logger.LogWarn(c, fmt.Sprintf("ark asset: 素材入库失败（协议=%s 地址=%s %d 个媒体，耗时 %s）：%s",
			backend.Protocol, backend.Endpoint, len(items), time.Since(start), err.Error()))
		return assetError(err)
	}
	applyAssetIDs(body.Content, ids)
	logger.LogInfo(c, fmt.Sprintf("ark asset: %d 个媒体已入素材库，耗时 %s", len(ids), time.Since(start)))
	return nil
}

// assetError 输入本身的问题（素材预处理失败、参数不合法）回 400 且不跨渠道重试——换个渠道
// 也是同一张图；协议选错属渠道配置问题，单独提示（不改 400，因为用户请求本身没问题）；其余
// （网络、超时、素材服务异常）按上游故障处理，允许重试其它渠道。
func assetError(err error) error {
	var mismatch *arkasset.ProtocolMismatchError
	if errors.As(err, &mismatch) {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeDoRequestFailed, http.StatusBadGateway)
	}
	var failed *arkasset.FailedError
	if errors.As(err, &failed) {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("参考素材入库失败（%s）：%s %s", failed.URL, failed.Code, failed.Message),
			types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	var apiErr *arkasset.APIError
	if errors.As(err, &apiErr) && strings.HasPrefix(apiErr.Code, "InvalidParameter") {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("参考素材入库失败：%s %s", apiErr.Code, apiErr.Message),
			types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return types.NewErrorWithStatusCode(fmt.Errorf("参考素材入库失败：%w", err),
		types.ErrorCodeDoRequestFailed, http.StatusBadGateway)
}
