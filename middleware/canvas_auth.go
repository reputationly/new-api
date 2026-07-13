package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// CanvasStaticAuth 画布静态应用(/canvas-app/*)的登录态门禁。
//
// 与 UserAuth 的区别:浏览器加载 JS/CSS/图片等静态资源不会附带 New-Api-User
// 自定义头,因此这里 session 只用于取用户 id;不接受 access token
// (避免用系统 token 直接打开内置 UI)。
//
// 角色与状态与 authHelper 同源——每次请求直接查库,不信任 session 快照,
// 保证管理员被降级/封禁后立即失效,而不必等 session 过期。
//
// 画布仅对管理员及以上(role >= RoleAdminUser)开放;普通用户、企业账户、
// 企业子账户(均为 role=1)不可见:
// - 未登录/用户已删除 -> 302 /login
// - 已登录但被禁用    -> 403
// - 非管理员          -> 404(不暴露功能存在)
func CanvasStaticAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		session := sessions.Default(c)
		id, ok := session.Get("id").(int)
		if !ok {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}
		user, err := model.GetUserById(id, false)
		if err != nil {
			// 用户已被删除或数据库异常,视为登录态失效
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}
		if user.Status != common.UserStatusEnabled {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		if user.Role < common.RoleAdminUser {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}
