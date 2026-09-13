package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

// MiniMax Design（内部代号 hilo）本地 gateway 的云侧接口。
//
// # 这一层在做什么
//
// 官方桌面端的形态是「renderer → 本地 gateway → 云」。本地 gateway 是一个
// 独立的 NestJS 进程，画布 / 素材 / 文件 / 生成队列 / 占位符 / 重试恢复
// 全在它里面 —— 那些我们不需要重写。它只有**模型目录和实际的模型调用**
// 要发到云上，把 `CLOUD_GATEWAY_BASE_URL` 指到 new-api，这一层就接管了。
//
// # 目录是总开关
//
// `/api/v1/models/config` 决定界面上能选哪些模型、每个模型有哪些参数、
// 参数之间怎么互斥。官方 gateway 用 zod 校验这份响应，**校验不过就整份
// 拒绝**，表现是模型选择器全空。所以 [dto.HiloModelsConfig] 的字段必须
// 逐条对齐，见那边的注释。
//
// # 取值为什么不能照抄官方
//
// 结构抄官方，**取值必须用我们平台实测的**。两者的差别是真实存在的：
// 官方 H3 的时长档位是 4–15 秒，而我们这套部署单段最长 10 秒 ——
// 照抄的话用户选 12 秒，请求发出去被平台拒掉，而界面上那个档位一直在。
//
// 官方的真实响应存在 `ovaijisuandesign/reference/hilo/` 下，用来对照升级。

// GetHiloModelsConfig 处理 `GET /api/v1/models/config`。
//
// 只报出**平台上真的有渠道的**模型。报一个没渠道的出来，用户能在界面上
// 选中它，点生成才失败 —— 而失败信息是渠道层的，说不清"这个模型没部署"。
func GetHiloModelsConfig(c *gin.Context) {
	enabled := make(map[string]bool)
	for _, name := range model.GetEnabledModels() {
		enabled[name] = true
	}

	// **一律 make 而不是 var**：nil 切片序列化出来是 `null`，而 zod 要的是
	// 数组，`null` 会让整份目录被判 invalid schema。
	out := dto.HiloModelsConfig{
		ImageModels: make([]dto.HiloMediaModel, 0),
		VideoModels: make([]dto.HiloMediaModel, 0),
		AudioModels: make([]dto.HiloMediaModel, 0),
		TextModels:  make([]dto.HiloTextModel, 0),
	}

	// 目录由管理员配置（`HiloCatalog` 选项），不写死在代码里。
	catalog := setting.GetHiloCatalog()
	collect := func(entries []setting.HiloCatalogEntry, dst *[]dto.HiloMediaModel) {
		for _, e := range entries {
			// 平台上没有这个模型就不报出去 —— 报了用户能选中，点生成
			// 才失败，而失败信息是渠道层的，说不清"这个模型没部署"。
			if !enabled[e.PlatformModel] {
				continue
			}
			*dst = append(*dst, e.Model)
		}
	}
	collect(catalog.Image, &out.ImageModels)
	collect(catalog.Video, &out.VideoModels)
	collect(catalog.Audio, &out.AudioModels)

	c.JSON(http.StatusOK, out)
}

// GetHiloUserEquity 处理 `GET /api/v1/user/equity`。
//
// 官方用它判断用户的额度和权益。我们这边**没有计费概念**，一律回
// 「已登录、额度无限」—— 这就是"把登录 hack 掉"的全部内容：不需要改
// 客户端，我们在这一侧直接给一个恒真的答复。
//
// 字段名沿用官方那套（客户端只读它认识的几个，多余的会被忽略）。
func GetHiloUserEquity(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		// 负数在官方界面里表示"不限"。给 0 会被显示成"额度已用尽"，
		// 那时所有生成按钮都是灰的。
		"credits":   -1,
		"remaining": -1,
		"unlimited": true,
		"vip":       true,
	})
}
