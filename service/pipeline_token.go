package service

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 体验区流水线的**内部令牌**。
//
// 为什么需要它：后置段（超分/配音）由服务端在巡检里提交，提交走的是"以用户身份调
// 本站 /v1/videos"这条路 —— 计费、限流、日志、渠道重试全部复用主链路，不必手写
// 第二份算价与路由逻辑（那份必然与主链路漂移）。
//
// 而体验区没有可用的凭证：/pg 的请求体是**用户会话**，`playgroundSetupContext()` 现场
// 造一个内存临时令牌（未入库、Id=0），拿它发不出 /v1 请求。所以给每个用户懒建一个
// **内部令牌**：
//   - 只放行后置段模型（ModelLimits），调别的模型直接被 token 白名单拒掉；
//   - 不进令牌列表（名字前缀是保留的，列表查询把它过滤掉）；
//   - 与用户自己的令牌同表同鉴权路径，因此计费/日志与用户自己调一次完全一致
//     （分段计费，与体验区今天的口径一致）。
//
// 明文 key 的权衡：令牌表本来就存明文 key（`Token.Key`），这不是新增的泄露面；
// 换来的是不必在巡检里重写一遍 relay 的提交与计费链路。
// PipelineTokenNamePrefix 与 model 层同一份（列表 / 计数 / 搜索的过滤都用它）。
const PipelineTokenNamePrefix = model.PipelineTokenNamePrefix

// IsPipelineTokenName 判断一个令牌名是不是内部流水线令牌（列表查询据此过滤）。
func IsPipelineTokenName(name string) bool {
	return model.IsPipelineTokenNameLike(name)
}

// GetPipelineToken 取该用户在指定分组下的内部令牌，没有就建一个。
//
// group 用**父任务落库的 Group**：体验区选的就是它，后置段必须落在同一分组里，
// 否则会路由到另一个分组下同名模型的渠道上去（价格与套餐都可能不同）。
func GetPipelineToken(userId int, group string, models []string) (*model.Token, error) {
	if userId <= 0 {
		return nil, fmt.Errorf("无效的用户 id")
	}
	limits := normalizePipelineModels(models)
	name := PipelineTokenNamePrefix + strings.TrimSpace(group)

	var tok model.Token
	err := model.DB.Where("user_id = ? AND name = ?", userId, name).First(&tok).Error
	switch {
	case err == nil:
		// 已存在：把新出现的后置段模型补进白名单（今天只有 sr，将来加 v2a 时不至于
		// 因为旧令牌的白名单里没有它而整段失败）。
		if merged := mergeModelLimits(tok.ModelLimits, limits); merged != tok.ModelLimits {
			tok.ModelLimits = merged
			tok.ModelLimitsEnabled = true
			if uerr := tok.Update(); uerr != nil {
				return nil, fmt.Errorf("更新内部令牌白名单失败: %w", uerr)
			}
		}
		return &tok, nil
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, fmt.Errorf("查询内部令牌失败: %w", err)
	}

	key, kerr := common.GenerateKey()
	if kerr != nil {
		return nil, fmt.Errorf("生成内部令牌失败: %w", kerr)
	}
	tok = model.Token{
		UserId:             userId,
		Key:                key,
		Status:             common.TokenStatusEnabled,
		Name:               name,
		CreatedTime:        common.GetTimestamp(),
		ExpiredTime:        -1, // 永不失效：它是服务端自己用的，不该因为"到期"让流水线断掉
		UnlimitedQuota:     true,
		ModelLimitsEnabled: true,
		ModelLimits:        limits,
		Group:              strings.TrimSpace(group),
	}
	if ierr := tok.Insert(); ierr != nil {
		// 并发下另一个巡检轮次可能刚建好同名令牌：再查一次，别把整个后置段判失败。
		var existing model.Token
		if qerr := model.DB.Where("user_id = ? AND name = ?", userId, name).First(&existing).Error; qerr == nil {
			return &existing, nil
		}
		return nil, fmt.Errorf("创建内部令牌失败: %w", ierr)
	}
	return &tok, nil
}

func normalizePipelineModels(models []string) string {
	seen := map[string]bool{}
	out := make([]string, 0, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return strings.Join(out, ",")
}

func mergeModelLimits(current, add string) string {
	all := strings.Split(current, ",")
	seen := map[string]bool{}
	out := make([]string, 0, len(all)+2)
	for _, m := range append(all, strings.Split(add, ",")...) {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return strings.Join(out, ",")
}
