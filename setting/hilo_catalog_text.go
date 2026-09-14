package setting

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

// 对话模型目录。
//
// # 为什么原先没有
//
// `HiloCatalog` 上原本写着「文本模型走 OpenCode，不在这里配」。**那是错的。**
//
// `/api/v1/models/config` 是客户端**所有**模型选择器的数据源 —— 官方 gateway
// 的原注释：「Canonical model catalog route consumed by all renderer model
// pickers」，对话那个选择器也在其中。`textModels` 空着的后果不是「少一个列表」：
//
//  1. 界面上选不到我们的模型
//  2. 客户端回落到它自己记着的上一次选择（官方模型，`gamma/gamma_high` 这种）
//  3. 而 OpenCode 那边的 provider 已经被换成我们的了，官方 provider 根本不存在
//     —— 发消息直接 500
//
// 报错是「OpenCode responded with 500」，看不出是模型选错了。实测就是这么卡住的。
//
// 结构里没有接它的字段时，配置里写了 `text` 段也会被 `Unmarshal` **静默丢弃**,
// 表现为「后台保存成功，但下发的 textModels 还是空的」。
//
// # 为什么不复用 HiloCatalogEntry
//
// 媒体条目要报 `backend`（官方有枚举，写错整份目录被拒）、`max_refs`、参数约束
// 这些；对话条目只要 id / name，以及它能不能吃图和音视频。字段面不同。

// HiloTextEntry 目录里的一条对话模型。
type HiloTextEntry struct {
	// PlatformModel 平台上的模型名，要和渠道里配的一致。
	//
	// 平台上没有这个模型时这一条**不会出现在目录里** —— 判据和媒体模型
	// 完全一样（见 HiloCatalogEntry.PlatformModel）。
	PlatformModel string `json:"platform_model"`
	// Model 客户端看到的定义。
	Model dto.HiloTextModel `json:"model"`
}

// 对话模型的 id 在目录里写**裸模型名**（`qwen3.8-27b`），不带 provider 前缀。
//
// # 为什么不在这里加前缀
//
// 客户端拿 `textModels[].id` 去 OpenCode 里找 provider，前缀必须和它那边
// `/api/v1/config` 下发的 provider id 一致 —— 而**那个接口不由 new-api 提供**
// （这里是 404）。现在只有 DesignPlusPlus 的 shim 实现它，前缀取自用户自己的
// `config.json`，用户随时能改。
//
// 在服务端写死一个前缀，只对「provider id 恰好也叫那个」的客户端成立；对不上
// 就是 provider 找不到 —— 同样 500，同样看不出原因。所以这里只报模型名，
// **前缀由持有 provider 配置的那一方补**。
//
// 将来 new-api 自己也下发 provider 配置了，再在那时统一加前缀，两处仍然一致。

// defaultHiloTextCatalog 出厂的对话模型。
//
// 只放**实测验证过**的。两项是硬门槛：
//
//   - 视觉：画布会大量传图，读不了图的模型不会报错，只会凭空臆造
//   - 工具调用：agent 少了它完全跑不起来
//
// 实测结果（2026-09-14，本平台）：
//
//	qwen3.8-27b              视觉 ✓  工具 ✓  视频 ✓
//	qwen3.8-flash-fp8        视觉 ✓  工具 ✓  视频 ✓
//	GPT-5.4                  视觉 ✓  工具 ✓  视频 未验
//	Gemini-3.1-Pro-Preview   视觉 ✓  工具 ✓  视频 未验
//	deepseek-v4-flash-0731   视觉 ✗          —— 不收
//	sensenova-u1.5           平台返回「模型不是…」，实际调不通 —— 不收
//	GPT-5.5                  两项都超时（120s），**结论未知** —— 暂不收
func defaultHiloTextCatalog() []HiloTextEntry {
	entry := func(platform, id, name string, video bool) HiloTextEntry {
		return HiloTextEntry{
			PlatformModel: platform,
			Model: dto.HiloTextModel{
				ID:            id,
				Name:          name,
				SupportsVideo: video,
				// **没验过的一律 false。** 报了 true 之后客户端会把音频喂过去，
				// 而模型只会编一段听起来合理的描述，不报错。
				SupportsAudio: false,
			},
		}
	}
	return []HiloTextEntry{
		entry("qwen3.8-27b", "qwen3.8-27b", "Qwen3.8 27B", true),
		entry("qwen3.8-flash-fp8", "qwen3.8-flash-fp8", "Qwen3.8 Flash", true),
		entry("GPT-5.4", "GPT-5.4", "GPT-5.4", false),
		entry("Gemini-3.1-Pro-Preview", "Gemini-3.1-Pro-Preview", "Gemini 3.1 Pro", false),
	}
}

// validateTextEntries 校验对话模型这一段。
//
// 和媒体那边同一个原则：**校验失败保持原配置、返回错误，不是清空** ——
// 管理员手滑写坏一处就让所有客户端的对话选择器变空，代价太大。
func validateTextEntries(entries []HiloTextEntry) error {
	seen := map[string]bool{}
	for i, e := range entries {
		where := fmt.Sprintf("text 分组第 %d 条", i+1)
		if strings.TrimSpace(e.PlatformModel) == "" {
			return fmt.Errorf("%s 的 platform_model 是空的，无法判断平台上有没有这个模型", where)
		}
		id := strings.TrimSpace(e.Model.ID)
		if id == "" {
			return fmt.Errorf("%s 缺少 model.id，客户端靠它选模型", where)
		}
		// **不能带前缀。** 前缀由持有 provider 配置的那一方补（见上面的说明）；
		// 这里再带一个，客户端补完就成了 `maas/maas/xxx`，provider 找不到。
		if strings.Contains(id, "/") {
			return fmt.Errorf(
				"%s 的 model.id 是 %q，不要带 provider 前缀 —— 前缀由客户端按自己的 provider 配置补，"+
					"这里再带一个会拼成两层，OpenCode 找不到 provider 而直接 500", where, id)
		}
		if seen[id] {
			return fmt.Errorf("%s 的 model.id %q 和前面重复了", where, id)
		}
		seen[id] = true
		if strings.TrimSpace(e.Model.Name) == "" {
			return fmt.Errorf("%s 缺少 model.name，选择器里会是一个没有名字的条目", where)
		}
	}
	return nil
}
