package model

func GetModelEnableGroups(modelName string) []string {
	// 确保缓存最新
	GetPricing()

	if modelName == "" {
		return make([]string, 0)
	}

	modelEnableGroupsLock.RLock()
	groups, ok := modelEnableGroups[modelName]
	modelEnableGroupsLock.RUnlock()
	if !ok {
		return make([]string, 0)
	}
	return groups
}

// GetModelEnableChannels 返回提供该模型的渠道 ID（来自缓存，已去重）。
//
// 只服务展示侧：模型广场要标出哪些模型能用积分抵扣，得知道它由哪些渠道提供。
// 计费侧不走这里——那边直接判本次请求选中的 ChannelId，精确且不受缓存延迟影响。
func GetModelEnableChannels(modelName string) []int {
	GetPricing()

	if modelName == "" {
		return make([]int, 0)
	}
	modelEnableGroupsLock.RLock()
	channels, ok := modelEnableChannels[modelName]
	modelEnableGroupsLock.RUnlock()
	if !ok {
		return make([]int, 0)
	}
	return channels
}

// GetModelQuotaTypes 返回指定模型的计费类型集合（来自缓存）
func GetModelQuotaTypes(modelName string) []int {
	GetPricing()

	modelEnableGroupsLock.RLock()
	quota, ok := modelQuotaTypeMap[modelName]
	modelEnableGroupsLock.RUnlock()
	if !ok {
		return []int{}
	}
	return []int{quota}
}
