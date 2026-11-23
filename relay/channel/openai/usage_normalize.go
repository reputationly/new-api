package openai

import "github.com/QuantumNous/new-api/dto"

// mergeInputTokenDetails 把 Responses 协议的 input_tokens_details 合并进计费读取的
// PromptTokensDetails。
//
// 规则：**每个字段以主来源为准，主来源为零才回退到 input_tokens_details，绝不累加**。
// 两条理由：
//
//  1. 有的网关两份都返回同一份数据（实测网宿 /responses 只在 input_tokens_details 里给），
//     用 += 会把缓存命中/写入翻倍，扣费随之翻倍；
//  2. 有的网关只给 prompt_tokens_details，此时回退分支零成本。
//
// 图片/文本 token 同属这一类（旧实现用 += 累加，两份都在时同样翻倍），一并收进这里。
func mergeInputTokenDetails(dst *dto.InputTokenDetails, src *dto.InputTokenDetails) {
	if dst == nil || src == nil {
		return
	}
	if dst.CachedTokens == 0 {
		dst.CachedTokens = src.CachedTokens
	}
	if dst.CachedCreationTokens == 0 {
		dst.CachedCreationTokens = src.CachedCreationTokens
	}
	if dst.CacheWriteTokens == 0 {
		dst.CacheWriteTokens = src.CacheWriteTokens
	}
	if dst.CreationTokens == 0 {
		dst.CreationTokens = src.CreationTokens
	}
	if dst.TextTokens == 0 {
		dst.TextTokens = src.TextTokens
	}
	if dst.AudioTokens == 0 {
		dst.AudioTokens = src.AudioTokens
	}
	if dst.ImageTokens == 0 {
		dst.ImageTokens = src.ImageTokens
	}
	if dst.EcaCacheCreationDetails == nil {
		dst.EcaCacheCreationDetails = src.EcaCacheCreationDetails
	}
}
