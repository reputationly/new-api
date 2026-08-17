package router

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

const (
	// wellKnownDirEnv 站点根目录验证文件所在目录，可用环境变量覆盖。
	wellKnownDirEnv = "WELL_KNOWN_DIR"
	// defaultWellKnownDir 默认取 /data 下的子目录——compose 已经把宿主机的
	// ./data 挂进 /data，运维放文件不需要改 compose，也不需要重新打镜像。
	defaultWellKnownDir = "/data/wellknown"
	// maxWellKnownFileSize 文件在启动时整份读进内存，给个上限兜底，
	// 避免有人误把大文件丢进这个目录把内存吃掉。
	maxWellKnownFileSize = 1 << 20
)

// wellKnownNameRegex 只放行「带扩展名的普通文件名」。
//
// 强制要求扩展名不是洁癖，是为了不和已注册的根级路由撞车：/api、/v1、/pg、/m、/s、
// /dashboard 这些段全都不带点，gin 底层的 httprouter 一旦注册出冲突路径会直接 panic
// 在启动阶段。限定必须含点，等于从规则上保证不可能撞上它们。
// 同时也挡掉了 ".."、以 "." 开头的隐藏文件和带斜杠的路径穿越。
var wellKnownNameRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.[A-Za-z0-9]+$`)

// SetWellKnownRouter 把 wellKnownDir 下的文件挂成站点根目录的精确路由。
//
// 用途是各家平台的域名归属验证文件（微信、百度、Google Search Console 等），它们都要求
// https://域名/<给定文件名>.txt 原样返回一串校验码。本项目前端全部 go:embed 进二进制、
// 且 NoRoute 会把任何未知路径回落成首页 HTML（见 web-router.go），所以这类文件既没法
// 靠宿主机放文件解决，也不会自然 404——抓取方拿到的是 200 + 一整页 HTML，验证必然失败。
//
// 必须在 SetRouter 的最前面调用：gin 在注册路由时就把当前的全局中间件链快照进去了，
// 早于 SetWebRouter / SetMobileRouter 注册，这些路由才不会被 GlobalWebRateLimit 限流
// （验证方的抓取往往集中且 UA 异常，被限流拦掉就白部署了）、不被 gzip 改写、
// 也不被移动端 UA 重定向中间件碰到。
func SetWellKnownRouter(router *gin.Engine) {
	dir := common.GetEnvOrDefaultString(wellKnownDirEnv, defaultWellKnownDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		// 目录不存在是绝大多数部署的常态，静默跳过；其余错误（权限等）要能在日志里看见。
		if !os.IsNotExist(err) {
			common.SysLog("读取站点验证文件目录失败 " + dir + ": " + err.Error())
		}
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !wellKnownNameRegex.MatchString(name) {
			continue
		}
		path := filepath.Join(dir, name)
		// 用 os.Stat 而不是 entry.Info()：后者是 lstat，符号链接量到的是链接自身那几十
		// 字节，而下面的 os.ReadFile 会跟随链接，大小上限就形同虚设。
		//
		// 也必须 stat 在前、开文件在后：FIFO 上的 os.Open 会阻塞到有写入端出现，而这段
		// 代码跑在路由注册阶段，一旦卡住就是整个进程起不来、且之后再没有任何日志输出。
		// stat 不打开文件，不受此影响。
		info, err := os.Stat(path)
		if err != nil {
			common.SysLog("站点验证文件读取失败 " + name + ": " + err.Error())
			continue
		}
		// IsRegular 一并挡掉目录、FIFO、设备、套接字，以及指向它们的符号链接。
		if !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > maxWellKnownFileSize {
			common.SysLog("站点验证文件过大已跳过: " + name)
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			common.SysLog("站点验证文件读取失败 " + name + ": " + err.Error())
			continue
		}
		contentType := mime.TypeByExtension(filepath.Ext(name))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		handler := func(c *gin.Context) {
			c.Set(middleware.RouteTagKey, "web")
			// no-store：验证文件随时会被换掉，让 ELB / WAF / 浏览器缓存住旧内容，
			// 换文件后重新提交验证就会一直失败在缓存上，且很难排查。
			c.Header("Cache-Control", "no-store")
			c.Data(http.StatusOK, contentType, content)
		}
		router.GET("/"+name, handler)
		router.HEAD("/"+name, handler)
		common.SysLog("已挂载站点根目录验证文件 /" + name)
	}
}
