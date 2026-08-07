package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// 「体验区管理」页专属的配置读写。
//
// 为什么不直接放开 /api/option/：那组路由是 RootAuth，PUT 能写**任意** option key
// ——SMTP 凭据、OAuth secret、全站计费配置都在里面，且没有任何键白名单。把它降到
// AdminAuth 等于把超管权限整个交出去。
//
// 所以照全站其余管理页的做法（用户管理 /api/user、渠道管理 /api/channel、
// 兑换码 /api/redemption 都是各有专属接口 + AdminAuth）给这一页开一组，
// 权限严格限定在下面这几个键上。体验区管理此前是全站唯一的 RootRoute 页面，
// 正是因为它缺这组接口、只能借用 /api/option/。

// playgroundWritableOptions 管理员可改的键。**白名单，不是黑名单**：
// 将来新增体验区配置项要显式加进来，漏加只是页面存不上，而不是意外放开写权限。
var playgroundWritableOptions = map[string]bool{
	"PlaygroundTabConfig":  true,
	"ImageModelSizeConfig": true,
	"VideoModelConfig":     true,
	"AudioModelConfig":     true,
	"MusicModelConfig":     true,
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
