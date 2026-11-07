package helper

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// ModelMappedHelper 把「客户端请求的模型名」翻译成「发给上游的模型名」。
//
// 两条改写链，按顺序求值，同一条链里支持继续套用（环检测见下）：
//
//	① 模型别名（model_alias）：对外名 -> 内部名。这是新功能，见 model/model_alias.go：
//	   对外名是对用户可见、可计费、可展示的那个名字，内部名只出现在发往上游的请求里。
//	   所以即使一个渠道只配了别名、没配 mapping，这里也会把请求改写成内部名。
//	② 渠道适配（model_mapping）：内部名 -> 真正上游名。Azure 部署名、Gemini 的
//	   -thinking 变体等，都靠它做最后一跳，语义与改造前完全一致。
//
// 两个名字的分工是这套改造的核心：OriginModelName（= 对外名）负责计费、日志、
// 响应体；UpstreamModelName（= 内部名/上游名）只负责发请求。
func ModelMappedHelper(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) error {
	if info.ChannelMeta == nil {
		info.ChannelMeta = &relaycommon.ChannelMeta{}
	}

	isResponsesCompact := info.RelayMode == relayconstant.RelayModeResponsesCompact
	originModelName := info.OriginModelName
	// 对外名要在任何改写之前定下来：下面 isResponsesCompact 分支会重写
	// info.OriginModelName（既有行为），而那之后拿到的已经不是"客户端请求的名字"了。
	publicModelName := originModelName
	mappingModelName := originModelName
	if isResponsesCompact && strings.HasSuffix(originModelName, ratio_setting.CompactModelSuffix) {
		mappingModelName = strings.TrimSuffix(originModelName, ratio_setting.CompactModelSuffix)
	}

	modelMap := make(map[string]string)
	modelMapping := c.GetString(string(constant.ContextKeyChannelModelMapping))
	if modelMapping != "" && modelMapping != "{}" {
		if err := common.UnmarshalJsonStr(modelMapping, &modelMap); err != nil {
			return fmt.Errorf("unmarshal_model_mapping_failed")
		}
	}
	aliasMap, err := model.ParseModelAlias(c.GetString(string(constant.ContextKeyChannelModelAlias)))
	if err != nil {
		return fmt.Errorf("unmarshal_model_alias_failed")
	}

	if len(modelMap) > 0 || len(aliasMap) > 0 {
		// 支持链式重定向，最终使用链尾的模型。每步**先查别名再查 mapping**：
		// 一个名字同时出现在两边时，别名（对外名->内部名）优先 —— 它表达的是
		// "这个名字对用户长什么样"，比渠道适配更靠前。
		currentModel := mappingModelName
		visitedModels := map[string]bool{
			currentModel: true,
		}
		for {
			nextModel := aliasMap[currentModel]
			if nextModel == "" {
				nextModel = modelMap[currentModel]
			}
			if nextModel == "" {
				break
			}
			// 模型重定向循环检测，避免无限循环
			if visitedModels[nextModel] {
				if nextModel == currentModel {
					if currentModel == info.OriginModelName {
						info.IsModelMapped = false
						return nil
					}
					info.IsModelMapped = true
					break
				}
				return errors.New("model_mapping_contains_cycle")
			}
			visitedModels[nextModel] = true
			currentModel = nextModel
			info.IsModelMapped = true
		}
		if info.IsModelMapped {
			info.UpstreamModelName = currentModel
		}
	}

	if isResponsesCompact {
		finalUpstreamModelName := mappingModelName
		if info.IsModelMapped && info.UpstreamModelName != "" {
			finalUpstreamModelName = info.UpstreamModelName
		}
		info.UpstreamModelName = finalUpstreamModelName
		info.OriginModelName = ratio_setting.WithCompactModelSuffix(finalUpstreamModelName)
	}

	// 发生改名时记下"对外名"。响应体、错误文本、响应头都靠它把上游名字换回来：
	// 没有改名（绝大多数渠道）时不设置这个键，那些改写点走零成本的原始路径。
	if info.IsModelMapped {
		common.SetContextKey(c, constant.ContextKeyPublicModelName, publicModelName)
		common.SetContextKey(c, constant.ContextKeyUpstreamModelName, info.UpstreamModelName)
	}

	if request != nil {
		request.SetModelName(info.UpstreamModelName)
	}
	return nil
}
