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
)

// resolveArkAssets 是入库的接缝，单测替换它以免真打素材库。
var resolveArkAssets = func(ctx context.Context, cfg arkasset.Config, httpClient *http.Client, groupName string, items []arkasset.Item) (map[string]string, error) {
	return arkasset.NewUploader(arkasset.NewClient(cfg, httpClient), arkasset.Scope(cfg), groupName).Resolve(ctx, items)
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
	cfg := arkasset.Config{
		Endpoint:    assetEndpoint(settings, a.baseURL),
		AccessKey:   strings.TrimSpace(settings.ArkAssetAccessKey),
		SecretKey:   strings.TrimSpace(settings.ArkAssetSecretKey),
		APIKey:      a.apiKey,
		ProjectName: strings.TrimSpace(settings.ArkAssetProjectName),
	}
	if cfg.ProjectName == "" {
		cfg.ProjectName = "default"
	}
	httpClient := service.GetHttpClient()
	if proxy := info.ChannelSetting.Proxy; proxy != "" {
		pc, err := service.NewProxyHttpClient(proxy)
		if err != nil {
			return fmt.Errorf("new proxy http client failed: %w", err)
		}
		httpClient = pc
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), arkAssetWaitTimeout)
	defer cancel()
	start := time.Now()
	ids, err := resolveArkAssets(ctx, cfg, httpClient, "new-api-channel-"+strconv.Itoa(info.ChannelId), items)
	if err != nil {
		logger.LogWarn(c, fmt.Sprintf("ark asset: 素材入库失败（%d 个媒体，耗时 %s）：%s", len(items), time.Since(start), err.Error()))
		return assetError(err)
	}
	applyAssetIDs(body.Content, ids)
	logger.LogInfo(c, fmt.Sprintf("ark asset: %d 个媒体已入素材库，耗时 %s", len(ids), time.Since(start)))
	return nil
}

// assetError 输入本身的问题（素材预处理失败、参数不合法）回 400 且不跨渠道重试——换个渠道
// 也是同一张图；其余（网络、超时、素材服务异常）按上游故障处理，允许重试其它渠道。
func assetError(err error) error {
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
