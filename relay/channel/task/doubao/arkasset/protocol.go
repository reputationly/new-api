package arkasset

import "fmt"

// 素材库协议。同一个「Doubao 视频」渠道可能指向火山方舟、方舟兼容网关或移动云 MoMA——
// 三者的视频生成接口同构，素材接口却是两套互不兼容的东西：
//
//	方舟 Action : POST /?Action=ListAssetGroups&Version=2024-01-01，火山 V4 签名，ResponseMetadata 信封
//	移动云 REST : POST /api/openapi-maas/exp/aicc/v2/asset-group/query，移动云统一签名，state/errorCode 信封
//
// 所以协议必须能显式指定：留空按 API 地址自动识别（见 doubao.resolveAssetBackend），
// 识别错了可以手动覆盖，不必为此改代码发版。
const (
	ProtocolArk    = "ark-action"  // 方舟 Action 协议：火山官方与界云等兼容网关
	ProtocolEcloud = "ecloud-rest" // 移动云 MoMA REST 协议

	ProtocolArkLabel    = "方舟 Action 协议"
	ProtocolEcloudLabel = "移动云 MoMA REST 协议"
)

// ProtocolMismatchError 上游响应既不是声明协议的正常回包，也不是它的业务错误信封。
//
// 典型现场：把移动云的 REST 地址当方舟素材接口打，对方网关回一段
// {"timestamp":...,"path":"/","status":400}，旧代码只能把它截断成一句 502，看的人无从下手。
// 单独成一类错误，是为了让报错直接说「协议可能选错」，并带出当前判定与地址。
type ProtocolMismatchError struct {
	Protocol string // 当前判定或显式指定的协议标签
	Endpoint string
	Sample   string // 上游原始响应片段
}

func (e *ProtocolMismatchError) Error() string {
	return fmt.Sprintf("素材库协议可能选错（当前：%s，地址 %s），上游返回：%s", e.Protocol, e.Endpoint, e.Sample)
}
