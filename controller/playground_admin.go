package controller

import (
	"net/http"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// 「体验区管理」页专属的配置读写。路由是 RootAuth（见 router/api-router.go），
// 与用户管理 /api/user、渠道管理 /api/channel、分组管理 /api/group 的管理接口同级。
//
// 为什么仍不复用 /api/option/：那组 PUT 能写**任意** option key——SMTP 凭据、
// OAuth secret、全站计费配置都在里面。这一页只该碰体验区那几个键，专属接口 +
// 下面的白名单把误写面收窄到最小。白名单不是权限闸（超管本就能走 /api/option/），
// 而是防这一页越界。

// playgroundWritableOptions 这一页可改的键。**白名单，不是黑名单**：
// 将来新增体验区配置项要显式加进来，漏加只是页面存不上，而不是意外放开写权限。
var playgroundWritableOptions = map[string]bool{
	"PlaygroundTabConfig":  true,
	"ImageModelSizeConfig": true,
	"VideoModelConfig":     true,
	"AudioModelConfig":     true,
	"MusicModelConfig":     true,
	// 「分类显示」区块（SettingsSidebarModulesAdmin）读写的侧边栏全局开关。漏加这条
	// 时页面表现是：GET 拿不到它 → 组件回落到前端的 DEFAULT_ADMIN_CONFIG（图像/视频/
	// 语音/音乐一律 false）→ 开关永远显示成默认态，而且下一次保存会把库里的真实配置
	// 整个覆盖成默认值。它只控制侧边栏菜单的显隐，不改任何接口权限。
	"SidebarModulesAdmin": true,
}

// playgroundReadOnlyOptions 页面渲染需要、但不该让这页改的键。
// UserUsableGroups 用于标注「该分组是不是所有用户都能访问」，改它会影响全站分组权限。
var playgroundReadOnlyOptions = map[string]bool{
	"UserUsableGroups": true,
}

// GetPlaygroundAdminOptions 只回体验区相关的键，形态与 GetOptions 一致（[{key,value}]），
// 前端可直接沿用同一套解析。
func GetPlaygroundAdminOptions(c *gin.Context) {
	options := make([]*model.Option, 0, len(playgroundWritableOptions)+len(playgroundReadOnlyOptions))
	common.OptionMapRWMutex.RLock()
	for k, v := range common.OptionMap {
		if !playgroundWritableOptions[k] && !playgroundReadOnlyOptions[k] {
			continue
		}
		options = append(options, &model.Option{
			Key:   k,
			Value: common.Interface2String(v),
		})
	}
	common.OptionMapRWMutex.RUnlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    options,
	})
}

// UpdatePlaygroundAdminOption 写单个键，非白名单一律拒绝。
// 落库仍走 model.UpdateOption —— 校验、缓存失效、热更新都在那条既有路径上，不另起一套。
func UpdatePlaygroundAdminOption(c *gin.Context) {
	var option OptionUpdateRequest
	if err := common.DecodeJson(c.Request.Body, &option); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}
	if !playgroundWritableOptions[option.Key] {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "该配置项不属于体验区管理，无法在此修改：" + option.Key,
		})
		return
	}
	// 这几个键的值都是 JSON 字符串，不会是 bool/数字，无需 UpdateOption 那套类型归一。
	value, ok := option.Value.(string)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "配置值必须是字符串：" + option.Key,
		})
		return
	}
	if err := model.UpdateOption(option.Key, value); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

// GetPlaygroundAdminModelAvailability 返回**当前有可用渠道**的模型名集合。
//
// 只服务体验区管理页的一件事：把「渠道已禁用 / 模型已从渠道里删掉」之后遗留的配置
// 折叠起来。那些条目对新请求毫无作用，却让分类页越堆越长 —— 运营要翻半天才找到要改
// 的那一条，而它们本来就只该在需要清理时出现。
//
// 判据只有一条：abilities 里是否存在该模型**至少一个 enabled 的记录**。刻意不判分组
// 可用性（那是「这个 key 能不能调」，见 service/ovcaps）也不判广场可见性 —— 这里回答
// 的是"这条配置还有没有意义"。
//
// 查询失败返回 500 而不是空列表：前端对空列表会折叠，对失败则一律不折叠（fail-open）。
// 把两者混成同一个空数组，等于让一次 DB 抖动把运营的配置从页面上藏起来。
func GetPlaygroundAdminModelAvailability(c *gin.Context) {
	models, err := model.GetEnabledModelNames()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	sort.Strings(models)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"models": models},
	})
}
