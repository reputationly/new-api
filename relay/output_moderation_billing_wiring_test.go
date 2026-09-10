package relay

import (
	"strings"
	"testing"
)

// ImageHelper 必须在返回审核拦截错误之前显式结算。
//
// 层级错位决定了这条没法靠行为测试：判违规在渠道适配器里（只有那里能在写响应
// 之前拿到产物），计费在 ImageHelper 里，而退款在 controller/relay.go 的 defer 里。
// 漏了这段结算，钱会被 defer 静悄悄退回去，拦截记录里却写着按实际消耗计费。
func TestImageHelperSettlesOnModerationBlock(t *testing.T) {
	src := readRelaySource(t, "image_handler.go")

	// 只看 DoResponse 之后紧随的那个错误分支。
	//
	// **两个锚点缺任何一个都必须 t.Fatal。** 上一版这里写的是 `if end > 0`——
	// 结束锚点一旦被改名，切片会悄悄退化成「到文件末尾」，而成功路径里也有一次
	// PostTextConsumeQuota，于是断言恒真：拦截分支不结算了测试照样绿，
	// 正是这条测试要防的那个静默退款。
	tail := sliceBetween(t, src,
		"usage, newAPIError := adaptor.DoResponse(", "// reset status code")
	if !strings.Contains(tail, "IsOutputModerationBlocked(c)") {
		t.Fatal("DoResponse 的错误分支没有识别产物审核拦截——预扣费会被 defer 退还")
	}
	if !strings.Contains(tail, "PostTextConsumeQuota") {
		t.Fatal("识别出拦截却没有结算：Billing.Refund 只在 settled 时才失效，" +
			"不结算就等于「拦了但没收钱」")
	}
}
