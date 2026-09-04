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

// GetModelMaxPriorities 返回「模型名 -> 最高渠道优先级」的快照。
//
// 展示顺序的唯一数据源,模型广场与体验区模型下拉共用(见 modelMaxPriority 的注释)。
// 返回副本而不是内部 map:调用方要拿着它排序、遍历,直接交出去等于把一个会被
// updatePricing 整体替换的 map 暴露在锁外。
func GetModelMaxPriorities() map[string]int64 {
	// 确保缓存最新。与本文件其它访问器同一写法:先在**不持任何锁**时触发刷新,
	// 再取读锁 —— updatePricing 内部会拿 modelEnableGroupsLock 写锁,顺序反了就死锁。
	GetPricing()

	modelEnableGroupsLock.RLock()
	defer modelEnableGroupsLock.RUnlock()
	out := make(map[string]int64, len(modelMaxPriority))
	for k, v := range modelMaxPriority {
		out[k] = v
	}
	return out
}
