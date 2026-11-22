package relay

// 续写源片抓取器的接线。
//
// 为什么注入而不是让 relay/continuation 直接 import nfsinput：nfsinput 依赖 service
// 包，而 service 的任务完成路径要调续写（拼接整段），直接相互 import 会成环。
// 注入点选在这里是因为 relay 已经同时依赖两边（task_ref_expand.go 就在用 nfsinput），
// 且它是最外层——service 不会反过来依赖 relay。
//
// 注入的实现与"输入物化"用的是同一套：http(s) 走 SSRF/域名白名单校验过的下载，
// task:<id> 走归属 + 终态校验。

import (
	"context"

	"github.com/QuantumNous/new-api/relay/channel/gpustackplus/nfsinput"
	"github.com/QuantumNous/new-api/relay/continuation"
)

func init() {
	continuation.SetSourceFetcher(fetchContinuationSourceBytes)
}

func fetchContinuationSourceBytes(ctx context.Context, userID int, source string) ([]byte, string, error) {
	if nfsinput.IsTaskRef(source) {
		return nfsinput.ResolveTaskRefBytes(ctx, userID, source, 0)
	}
	body, err := nfsinput.FetchURLBytes(ctx, source, 0)
	if err != nil {
		return nil, "", err
	}
	return body, ".mp4", nil
}
