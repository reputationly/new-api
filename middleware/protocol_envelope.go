package middleware

import (
	"bytes"
	"net/http"

	"github.com/gin-gonic/gin"
)

// 第三方协议兼容层共用的响应改写骨架。
//
// 兼容层要把本仓自己的响应形态改写成对应厂商的形态，而这件事只能在处理链跑完之后做
// （提交走的是 controller.RelayTask，它自己 c.JSON 了一个 OpenAI 风格的 video 对象；
// 错误则散落在各层的 abort 里）。办法是先把下游写出的内容缓冲起来，待链结束再统一
// 改写落到真 writer 上。
//
// middleware/minimax_v2_adapter.go 里有一份同构的实现（早于本文件）。没有把它迁过来
// 是刻意的：那条路径有自己的测试，为一次新增去动它属于无关改动。第三个兼容层出现时
// 再合并。

// envelopeSpec 描述某个协议的错误信封该怎么产生、怎么识别。
type envelopeSpec struct {
	// buildError 组装该协议的错误体。
	buildError func(c *gin.Context, status int, message string) []byte
	// isEnvelope 判断 body 是否已经是该协议的错误信封，避免二次包装。
	isEnvelope func(body []byte) bool
	// transformSuccess 非 nil 时用于改写 2xx 响应体；nil 表示成功响应原样透出
	// （查询 / 列表这类 body 本来就已经是官方形态的端点）。
	transformSuccess func([]byte) ([]byte, error)
}

// envelopeWriter 缓冲下游写出的响应，待处理链跑完后统一改写再落到真 writer 上。
type envelopeWriter struct {
	gin.ResponseWriter
	spec        envelopeSpec
	buf         bytes.Buffer
	status      int
	wroteHeader bool
}

func (w *envelopeWriter) WriteHeader(code int) {
	if code > 0 {
		w.status = code
		w.wroteHeader = true
	}
}

// WriteHeaderNow 必须吞掉：真写头一旦发生就锁死了状态码，后面改写不了。
func (w *envelopeWriter) WriteHeaderNow() {}

func (w *envelopeWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }

func (w *envelopeWriter) WriteString(s string) (int, error) { return w.buf.WriteString(s) }

func (w *envelopeWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *envelopeWriter) Size() int { return w.buf.Len() }

func (w *envelopeWriter) Written() bool { return w.wroteHeader || w.buf.Len() > 0 }

// Flush 同样吞掉：缓冲期真 flush 会把未改写的内容送出去。这些端点都是一次性 JSON，
// 没有流式需求。
func (w *envelopeWriter) Flush() {}

func (w *envelopeWriter) commit(c *gin.Context) {
	target := w.ResponseWriter
	status := w.Status()
	body := w.buf.Bytes()

	switch {
	case status == http.StatusNoContent:
		// 官方「操作成功不返回业务响应体」的端点（方舟 DELETE）。别塞任何东西进去。
		body = nil
	case status >= 200 && status < 300 && len(bytes.TrimSpace(body)) == 0:
		// 处理链什么都没写就结束了（理论上不该发生）。给出合法的官方错误而不是空 200，
		// 免得调用方拿着一个解析不了的空响应去猜。
		status = http.StatusInternalServerError
		body = w.spec.buildError(c, status, "empty response from upstream handler")
	case status >= 200 && status < 300:
		if w.spec.transformSuccess != nil {
			converted, err := w.spec.transformSuccess(body)
			if err != nil {
				status = http.StatusInternalServerError
				body = w.spec.buildError(c, status, err.Error())
			} else {
				body = converted
			}
		}
	case w.spec.isEnvelope(body):
		// 已经是官方信封（本兼容层自己产生的错误）：别二次包装。
	default:
		body = w.spec.buildError(c, status, extractErrorMessage(body, status))
	}

	header := target.Header()
	if len(body) == 0 {
		header.Del("Content-Type")
		header.Del("Content-Length")
		target.WriteHeader(status)
		return
	}
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Del("Content-Length")
	target.WriteHeader(status)
	_, _ = target.Write(body)
}

// wrapProtocolResponse 装上缓冲 writer，并在处理链结束后提交改写结果。
//
// panic 路径**不提交**缓冲内容：main.go 的 CustomRecovery 会在我们这一帧展开之后
// 往真 writer 写它自己的 500。若我们也写一份，两段 body 会拼在一起变成非法 JSON。
// 代价是 panic 时的错误不是官方形态 —— panic 是 bug 不是协议状态，可以接受。
func wrapProtocolResponse(c *gin.Context, spec envelopeSpec) {
	w := &envelopeWriter{ResponseWriter: c.Writer, spec: spec}
	c.Writer = w
	completed := false
	defer func() {
		c.Writer = w.ResponseWriter
		if completed {
			w.commit(c)
		}
	}()
	c.Next()
	completed = true
}
