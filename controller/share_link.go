package controller

import (
	"bytes"
	"errors"
	"html/template"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/service/sharelink"

	"github.com/gin-gonic/gin"
)

// 微信内置浏览器 UA 特征。落地页据此显示「在浏览器打开」的引导——只有在免登录
// 分享链接上这句引导才成立，在需要登录的页面上说这句话是把用户支进死胡同。
var weChatUARegex = regexp.MustCompile(`(?i)micromessenger`)

// isExternalDirectResultURL 判断 ResultURL 是不是一条「拿去就能打开」的外部地址。
//
// 必须排除 taskcommon.BuildProxyURL 生成的我方代理路径（/v1/videos/<id>/content）：
// 那条路要鉴权、且会带渠道 Key 回源上游，当成直链发出去等于把上游代理送人。
// 非 OBS 的 ResultURL 有两类，正是「上游直链」与「我方代理」，这里只放行前者。
func isExternalDirectResultURL(raw, taskID string) bool {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return false
	}
	return !isTaskProxyContentURL(raw, taskID)
}

// CreateTaskShareLink 为自己的任务成品签发免登录分享 token。
//
// 按 user_id + task_id 查库，查不到就发不出 token；签出的 token 里也带着这个
// user_id，解析端据此原样按 (user_id, task_id) 回查，归属由查询强制而非靠推理。
func CreateTaskShareLink(c *gin.Context) {
	taskID := c.Param("id")
	if taskID == "" {
		common.ApiErrorMsg(c, "task_id is required")
		return
	}
	userID := c.GetInt("id")
	task, exists, err := model.GetByTaskId(userID, taskID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !exists || task == nil {
		common.ApiErrorMsg(c, "任务不存在")
		return
	}
	if task.Status != model.TaskStatusSuccess {
		common.ApiErrorMsg(c, "任务尚未完成，暂不能分享")
		return
	}
	// 只有落到我方 OBS 的成品才签得出 /s/ token：那个端点是**匿名**的，必须能用
	// 「签名 key → 302」这一条简单路径服务完，绝不能为免登录访问去复刻 VideoProxy
	// 里带渠道 Key 回源上游的那套逻辑（等于把上游代理暴露给匿名用户）。
	raw := task.GetResultURL()
	if !mediastore.IsOBSRef(raw) {
		// 渠道开了「透传成品地址」时成品本就是公网直链（见 dto.ChannelSettings
		// .PassThroughResultURL）。这个端点是 UserAuth + self/:id，把该 URL 交给
		// 任务主人不新增任何暴露面——他在自己的任务列表里本来就看得到。不签 token：
		// 上面那条匿名端点的约束一点不动。
		//
		// ⚠️ 已知缺口：这条支路交出去的是上游直链，加不了 Content-Disposition，
		// 落不进下面 ShareContent 那条「站外只能下载」的策略（点开仍会在线播放）。
		// 要堵只有两条路，都不在本次范围：拒绝这类任务的分享，或由 /s/ 回源代理成下载流。
		//
		// 这一支对微信是刚需：ShareBar 在微信里只有「复制分享链接」一条路（平台
		// 不给音视频任何本地保存途径），拒了就等于成品彻底拿不到。
		if isExternalDirectResultURL(raw, task.TaskID) {
			common.ApiSuccess(c, gin.H{"url": raw, "expires_at": 0})
			return
		}
		common.ApiErrorMsg(c, "该结果未落对象存储，暂不支持分享")
		return
	}

	token, expiresAt := sharelink.Sign(userID, taskID, time.Now())
	common.ApiSuccess(c, gin.H{
		"token":      token,
		"path":       "/s/" + token,
		"expires_at": expiresAt,
	})
}

// resolveShareTask 校验 token 并取回对应任务。失败时已写好响应，返回 false。
//
// 按 token 里的 (userID, taskID) 查，而不是只按 taskID 查全表：tasks.task_id 上没有
// 唯一约束，只按它查一旦撞车就会解析到别人的成品。带上 userID 后越权在结构上不可能。
func resolveShareTask(c *gin.Context, asHTML bool) (*model.Task, bool) {
	userID, taskID, err := sharelink.Verify(c.Param("token"), time.Now())
	if err != nil {
		switch {
		case errors.Is(err, sharelink.ErrExpired):
			writeShareError(c, asHTML, http.StatusGone, "链接已过期", "分享链接有效期为 7 天，请让分享者重新生成。")
		default:
			writeShareError(c, asHTML, http.StatusForbidden, "链接无效", "这个分享链接不正确，请检查是否复制完整。")
		}
		return nil, false
	}

	task, exists, err := model.GetByTaskId(userID, taskID)
	if err != nil {
		writeShareError(c, asHTML, http.StatusInternalServerError, "服务异常", "请稍后重试。")
		return nil, false
	}
	if !exists || task == nil || task.Status != model.TaskStatusSuccess {
		writeShareError(c, asHTML, http.StatusNotFound, "内容不存在", "对应的生成结果已不可用。")
		return nil, false
	}
	if !mediastore.IsOBSRef(task.GetResultURL()) {
		writeShareError(c, asHTML, http.StatusNotFound, "内容不可用", "该结果未落对象存储。")
		return nil, false
	}
	return task, true
}

// ShareLandingPage 免登录落地页 GET /s/:token。
//
// 服务端渲染一页自包含 HTML，不走 SPA：收到链接的人多半不是本站用户，为一个下载
// 页拉整个 React 包不划算，且移动端 SPA 在微信里有过首屏白屏的前科。
//
// 页面**只给下载入口，不内嵌播放器**：内容审核尚不完善，站外任何人拿到链接就能
// 当场播放的话，未审内容等于直接摆在浏览器里。在线浏览只保留在站内（需登录的
// 控制台/移动端页面），站外一律落到「下载文件」这一个动作上。
func ShareLandingPage(c *gin.Context) {
	task, ok := resolveShareTask(c, true)
	if !ok {
		return
	}

	key := mediastore.KeyFromRef(task.GetResultURL())
	// 过去「已过期」是靠媒体元素的 onerror 兜的（OBS 对象 7 天后由生命周期清除）。
	// 播放器拿掉之后那条路没了，改成渲染前 HeadObject 一次。探测本身出错就放行：
	// 宁可让用户点了下载再失败，也不能因为一次网络抖动把好链接判死。
	if exists, err := mediastore.Exists(c.Request.Context(), key); err == nil && !exists {
		writeShareError(c, true, http.StatusGone, "内容已过期", "生成结果保留 7 天，超期后会自动清理。")
		return
	}

	renderSharePage(c, http.StatusOK, sharePageData{
		Brand:    brandName(),
		Kind:     mediaKindLabel(key),
		Token:    c.Param("token"),
		InWeChat: weChatUARegex.MatchString(c.Request.UserAgent()),
	})
}

// mediaKindLabel 把对象的 content-type 折成一个中文名词，落地页只拿它拼说明文案。
func mediaKindLabel(key string) string {
	ct := mediastore.InferContentType(key)
	switch {
	case strings.HasPrefix(ct, "video/"):
		return "视频"
	case strings.HasPrefix(ct, "audio/"):
		return "音频"
	case strings.HasPrefix(ct, "image/"):
		return "图片"
	default:
		return "文件"
	}
}

// ShareContent 免登录取内容 GET /s/:token/content。
// 与 VideoProxy 的 OBS 分支一致：实时签名后 302，流量直连 OBS 不经我方带宽。
//
// 签名**无条件带 Content-Disposition: attachment**。原先是 ?download=1 才带，不带
// 就内联播放——只要留着那条路，随手把参数删掉就绕过了整条「站外只能下载」的策略，
// 所以开关直接去掉，参数存不存在都按下载走。站内在线播放走的是另一套带鉴权的端点
// （/v1/videos/:task_id/content 等），不受这里影响。
func ShareContent(c *gin.Context) {
	task, ok := resolveShareTask(c, false)
	if !ok {
		return
	}

	key := mediastore.KeyFromRef(task.GetResultURL())
	signed, err := mediastore.Sign(c.Request.Context(), key, mediastore.WithDownloadName(buildDownloadName(task, key)))
	if err != nil {
		writeShareError(c, false, http.StatusBadGateway, "内容暂不可用", "媒体存储签名失败。")
		return
	}
	c.Redirect(http.StatusFound, signed)
}

// brandName 取运营配置的站点名称，与 index.html 的品牌替换保持一致。
func brandName() string {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	if common.SystemName == "" {
		return "New API"
	}
	return common.SystemName
}

// Token 只作为 URL 路径段插值（模板里写成 /s/{{.Token}}/content），不拼成整条 URL
// 再塞进模板：整条 URL 插值要用 template.URL 绕过转义，而 html/template 对路径段插值
// 会正常施加 URL 转义，安全边界不依赖「token 一定只含 base64/hex 字符」这个前提。
type sharePageData struct {
	Brand     string
	Kind      string // 「视频」「音频」「图片」「文件」，只进文案
	Token     string
	InWeChat  bool
	ErrTitle  string
	ErrDetail string
}

func writeShareError(c *gin.Context, asHTML bool, status int, title, detail string) {
	if !asHTML {
		c.JSON(status, gin.H{"success": false, "message": title})
		return
	}
	renderSharePage(c, status, sharePageData{
		Brand:     brandName(),
		ErrTitle:  title,
		ErrDetail: detail,
	})
}

// renderSharePage 先渲染到缓冲区再整体写出：直接往 c.Writer 渲染的话，模板中途
// 出错会给客户端留下一个截断的半张页面，且状态码已发出无法改判。
func renderSharePage(c *gin.Context, status int, data sharePageData) {
	var buf bytes.Buffer
	if err := sharePageTmpl.Execute(&buf, data); err != nil {
		common.SysError("render share page failed: " + err.Error())
		c.Status(http.StatusInternalServerError)
		return
	}
	// 分享内容不该被搜索引擎收录——链接持有即可访问，被索引等于公开。
	c.Header("X-Robots-Tag", "noindex, nofollow")
	c.Header("Cache-Control", "no-store")
	c.Data(status, "text/html; charset=utf-8", buf.Bytes())
}

// 落地页模板：无外部资源、无 JS 框架，弱网和微信内也能秒开。
// 页面上没有任何 <video>/<audio>/<img> —— 站外只给下载，见 ShareLandingPage 注释。
var sharePageTmpl = template.Must(template.New("share").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="robots" content="noindex,nofollow">
<title>{{.Brand}}</title>
<style>
*{box-sizing:border-box}
body{margin:0;padding:24px 16px;font:15px/1.6 -apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei",sans-serif;background:#f5f5f7;color:#1d1d1f;-webkit-text-size-adjust:100%}
.wrap{max-width:640px;margin:0 auto}
.brand{font-size:13px;color:#86868b;margin-bottom:16px;text-align:center}
.card{background:#fff;border-radius:14px;padding:20px 16px;box-shadow:0 1px 3px rgba(0,0,0,.06)}
.head{font-size:17px;font-weight:600;text-align:center}
.desc{margin:10px 0 0;color:#86868b;font-size:13px;text-align:center}
.btn{display:block;margin-top:18px;padding:12px;border-radius:10px;background:#0071e3;color:#fff;text-align:center;text-decoration:none;font-weight:500}
.tip{margin-top:14px;padding:12px;border-radius:10px;background:#fff8e6;color:#8a6d00;font-size:13px}
.note{margin-top:12px;color:#86868b;font-size:12px;text-align:center}
.err{text-align:center;padding:40px 16px}
.err h1{font-size:19px;margin:0 0 8px}
.err p{color:#86868b;margin:0;font-size:14px}
</style>
</head>
<body>
<div class="wrap">
<div class="brand">{{.Brand}}</div>
{{if .ErrTitle}}
<div class="card err"><h1>{{.ErrTitle}}</h1><p>{{.ErrDetail}}</p></div>
{{else}}
<div class="card">
  <div class="head">{{.Kind}}文件</div>
  <p class="desc">该链接仅提供下载，不支持在线播放。</p>
  {{if .InWeChat}}
  <div class="tip">微信内无法保存文件。请点右上角「···」，选择「在浏览器打开」，再点下面的按钮下载到手机。</div>
  {{end}}
  <a class="btn" href="/s/{{.Token}}/content">下载到本地</a>
  <div class="note">生成结果保留 7 天，超期后链接自动失效。</div>
</div>
{{end}}
</div>
</body>
</html>`))
