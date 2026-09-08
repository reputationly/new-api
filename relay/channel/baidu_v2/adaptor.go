package baidu_v2

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.ClaudeRequest) (any, error) {
	adaptor := openai.Adaptor{}
	converted, err := adaptor.ConvertClaudeRequest(c, info, req)
	if err != nil {
		return nil, err
	}
	openAIRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	if !ok {
		return converted, nil
	}
	// openai.Adaptor 会为非 OpenAI/Azure 渠道清空 stream_options，
	// 而千帆流式默认不返回 usage，不显式要 include_usage 就只能按估算计费
	if info.SupportStreamOptions && info.IsStream {
		openAIRequest.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
	}
	// openai.Adaptor 内部调的是它自己的 ConvertOpenAIRequest，不会走到本 adaptor 的
	// -search 处理，所以在这里补上，否则 /v1/messages 打 xxx-search 会把后缀原样发给千帆
	return applySearchSuffix(info, openAIRequest)
}

// -search 是渠道模型列表里手工约定的后缀：剥掉后缀取回真实模型名，
// 并按千帆 /v2/chat/completions 的 web_search 参数开启联网搜索
func applySearchSuffix(info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if !strings.HasSuffix(info.UpstreamModelName, "-search") {
		return request, nil
	}
	info.UpstreamModelName = strings.TrimSuffix(info.UpstreamModelName, "-search")
	request.Model = info.UpstreamModelName
	if len(request.WebSearch) > 0 {
		return request, nil
	}
	// 不用 ToMap：返回 map 会让 GuessRelayFormatFromRequest 认不出格式，
	// 转换链停在 Claude，usage 语义和日志里的最终请求格式都会判错
	webSearch, err := common.Marshal(map[string]any{
		"enable":          true,
		"enable_citation": true,
		"enable_trace":    true,
		"enable_status":   false,
	})
	if err != nil {
		return nil, fmt.Errorf("error marshalling web_search: %w", err)
	}
	request.WebSearch = webSearch
	return request, nil
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		// /v1/messages 不走 Path2RelayMode，RelayMode 恒为 0；
		// ConvertClaudeRequest 已把请求体转成 OpenAI 格式，所以仍打千帆的 OpenAI 兼容端点
		return fmt.Sprintf("%s/v2/chat/completions", info.ChannelBaseUrl), nil
	default:
		switch info.RelayMode {
		case constant.RelayModeChatCompletions:
			return fmt.Sprintf("%s/v2/chat/completions", info.ChannelBaseUrl), nil
		case constant.RelayModeEmbeddings:
			return fmt.Sprintf("%s/v2/embeddings", info.ChannelBaseUrl), nil
		case constant.RelayModeImagesGenerations:
			return fmt.Sprintf("%s/v2/images/generations", info.ChannelBaseUrl), nil
		case constant.RelayModeImagesEdits:
			return fmt.Sprintf("%s/v2/images/edits", info.ChannelBaseUrl), nil
		case constant.RelayModeRerank:
			return fmt.Sprintf("%s/v2/rerank", info.ChannelBaseUrl), nil
		default:
		}
	}
	return "", fmt.Errorf("unsupported relay mode: %d", info.RelayMode)
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	keyParts := strings.Split(info.ApiKey, "|")
	if len(keyParts) == 0 || keyParts[0] == "" {
		return errors.New("invalid API key: authorization token is required")
	}
	if len(keyParts) > 1 {
		if keyParts[1] != "" {
			req.Set("appid", keyParts[1])
		}
	}
	req.Set("Authorization", "Bearer "+keyParts[0])
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	return applySearchSuffix(info, request)
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	// TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	adaptor := openai.Adaptor{}
	usage, err = adaptor.DoResponse(c, resp, info)
	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
