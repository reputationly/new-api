package controller

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 审核策略的原子保存。见 docs/content-moderation-design.md §8.2、§8.3。
//
// 为什么不走通用的 PUT /api/option/ 逐个保存：策略、默认策略、分组绑定三者互相引用，
// 逐个提交必然经过一个自相矛盾的中间态，而校验又只能看到当时已存的值——
// 结果是最自然的两种编辑在**任何**拆分顺序下都做不成：
//
//	改名默认策略：先写 policies 会因旧的 default_policy 还指着旧名被拒；
//	              先写 default_policy 会因新名还不存在被拒。两个方向都死锁。
//	先解绑再删策略：前端按本地状态判断可以删，后端拿已存的 group_policies 一比还绑着，拒。
//
// 所以三个键必须放在同一次请求里，对照同一份快照校验、再一起落库。

type moderationPolicyConfigRequest struct {
	Policies      []system_setting.ModerationPolicy     `json:"policies"`
	DefaultPolicy string                                `json:"default_policy"`
	GroupPolicies map[string]system_setting.GroupPolicy `json:"group_policies"`
}

// SaveModerationPolicyConfig 一次性保存策略三件套（管理员）。
func SaveModerationPolicyConfig(c *gin.Context) {
	var req moderationPolicyConfigRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiErrorMsg(c, "请求参数有误")
		return
	}

	// 先规范化再校验再落库，三步用的是同一份值。
	//
	// 不这么做会重新引入这段校验本要杜绝的静默回退：校验用 TrimSpace 后的名字比对，
	// 落库存的却是带空格的原文，而运行期 ResolvePolicy 是**精确**比较
	// （s.Policies[i].Name == name）。于是「策略名尾部多一个空格」这种从文档粘贴
	// 就会发生的事，能让校验通过、保存成功、界面正常，而判定悄悄落到 Policies[0]。
	normalizeModerationPolicyConfig(&req)

	if err := system_setting.ValidateModerationPolicyConfig(
		req.Policies, req.DefaultPolicy, req.GroupPolicies); err != nil {
		common.ApiError(c, err)
		return
	}

	policiesJSON, err := common.Marshal(req.Policies)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	groupsJSON, err := common.Marshal(req.GroupPolicies)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// 校验已经整体通过，这里逐个落库。
	//
	// 中途失败会留下一个只更新了一部分的状态——但那个状态里的每一项都是**校验过的**，
	// 最坏情况是某个分组暂时指向旧策略，而 ResolvePolicy 对找不到的绑定本就有回退，
	// 不会崩。真要做成事务得改 UpdateOption 的签名，牵动全部配置项，
	// 代价远大于这里的收益。
	updates := []struct {
		key   string
		value string
	}{
		{"moderation.policies", string(policiesJSON)},
		{"moderation.default_policy", req.DefaultPolicy},
		{"moderation.group_policies", string(groupsJSON)},
	}
	for _, u := range updates {
		if err := model.UpdateOption(u.key, u.value); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	common.ApiSuccess(c, nil)
}

// normalizeModerationPolicyConfig 把三处策略名去掉首尾空白。
//
// 三处必须一起处理：策略自己的 Name、默认策略名、以及每个分组绑定的 Policy。
// 漏掉任何一处，那一处就会在运行期与另外两处失配。
func normalizeModerationPolicyConfig(req *moderationPolicyConfigRequest) {
	for i := range req.Policies {
		req.Policies[i].Name = strings.TrimSpace(req.Policies[i].Name)
	}
	req.DefaultPolicy = strings.TrimSpace(req.DefaultPolicy)
	for g, gp := range req.GroupPolicies {
		gp.Policy = strings.TrimSpace(gp.Policy)
		req.GroupPolicies[g] = gp
	}
}
