package relay

// task:<task_id> 产物引用的第三方渠道兼容层(docs/canvas-orchestration-design.md §3.8)。
//
// gpustackplus 在输入物化层(nfsinput.AddString)原生解析 task: 引用并享受 NFS 同盘
// 直读;第三方渠道适配器只认 base64/URL,task: 字符串会被当普通值透传给上游导致报错。
// 因此在渠道确定之后、构建请求体之前,非 gpustackplus 渠道统一把请求中的 task: 引用
// 展开为 base64 data-url(体验区第三方链路本就以 base64 传媒体,适配器无需感知)。
//
// 遍历/回填/去重由 task_media_rewrite.go 的骨架负责,本文件只提供 task: 这一种 resolver。
//
// 重试语义:每次尝试都由 ValidateRequestAndSetAction 从**原始** body 重建 task_request。
// "原始"二字由 RestoreOriginalTaskBody 显式保证——common.ReplaceRequestBody 是持久替换,
// 不复位的话上一轮改写后的 body 会被当成原始输入,详见 task_media_rewrite.go 文件头。

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel/gpustackplus/nfsinput"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/mediastore"

	"github.com/gin-gonic/gin"
)

// taskRefResolver 把 task:<id> 展开为 data-url,或(白名单渠道)直接签成 OBS URL。
type taskRefResolver struct {
	// signDirect 白名单渠道下走 obs:// 直签捷径:被引用产物本来就存在 OBS 里,
	// 「下载 → base64 → 上传回 OBS → 签名」与「直接对原 key 签名」结果等价,
	// 却多两次全量网络传输和一次 base64 编解码(30 MB 视频约 40 MB 峰值驻留)。
	signDirect bool
	userID     int
}

func newTaskRefResolver(info *relaycommon.RelayInfo, signDirect bool) mediaResolver {
	return &taskRefResolver{userID: info.UserId, signDirect: signDirect}
}

func (r *taskRefResolver) Name() string {
	// 直签与展开对同一个 task: 产出不同结果,memo 必须分区——否则重试换渠道时
	// 会把上一轮的 data-url 复用给只认 URL 的那一侧(或反之)。
	if r.signDirect {
		return "taskref-obs"
	}
	return "taskref"
}

func (r *taskRefResolver) Want(value string) bool { return nfsinput.IsTaskRef(value) }

// Resolve 解析失败返回 error(硬错误 → 400 skip-retry):引用坏了换渠道也没用。
func (r *taskRefResolver) Resolve(ctx context.Context, value string) (string, error) {
	if r.signDirect {
		if signed, ok := r.signFromOBS(ctx, value); ok {
			return signed, nil
		}
		// 未落 OBS / 对象已被生命周期清掉 / 签名失败 → 回退字节路径,行为与改造前一致。
	}
	data, ext, err := nfsinput.ResolveTaskRefBytes(ctx, r.userID, value, 0)
	if err != nil {
		return "", err
	}
	return "data:" + mediastore.InferContentType("ref"+ext) + ";base64," +
		base64.StdEncoding.EncodeToString(data), nil
}

// signFromOBS 尝试直签。任何一步不成立都返回 (,false) 让调用方回退,不产生硬错误——
// 归属/终态那类真错误由后续的 ResolveTaskRefBytes 用同一套校验重新报出。
func (r *taskRefResolver) signFromOBS(ctx context.Context, value string) (string, bool) {
	key, err := nfsinput.TaskRefOBSKey(r.userID, value)
	if err != nil || key == "" {
		return "", false
	}
	// key 现在会流向外部第三方(进签名 URL 的路径部分)。ResultURL 虽只由我方代码写入,
	// 但既然信任边界变了,这道 sanity check 是廉价的纵深。
	if strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		logger.LogWarn(ctx, "task ref: 可疑的 OBS key,回退字节路径: "+key)
		return "", false
	}
	// 签名是纯离线计算,对已被生命周期删掉的 key 一样签得出 URL,上游会拿到 403,
	// 错误从"提交时明确 400"劣化成"静默生成一个忽略参考物的产物"。先 Head 一次确认。
	exists, err := mediastore.Exists(ctx, key)
	if err != nil || !exists {
		return "", false
	}
	signed, err := mediastore.Sign(ctx, key)
	if err != nil {
		return "", false
	}
	return signed, true
}

// activeMediaResolvers 按当前渠道装配生效的 resolver(装配点,顺序即优先级)。
//
// gpustackplus 两者都不装:task: 由它的物化层原生解析并走 NFS 同盘直读,
// data-url 由它物化落 NFS——都轮不到这里改写。
func activeMediaResolvers(c *gin.Context, info *relaycommon.RelayInfo) []mediaResolver {
	if info == nil || info.ChannelMeta == nil {
		return nil
	}
	if info.ChannelType == constant.ChannelTypeGPUStackPlus {
		return nil
	}
	offload := offloadEnabled(info)
	resolvers := []mediaResolver{newTaskRefResolver(info, offload)}
	if offload {
		resolvers = append(resolvers, newDataURLResolver(c, info))
	}
	return resolvers
}

func taskRefExpandError(err error) *dto.TaskError {
	taskErr := service.TaskErrorWrapperLocal(fmt.Errorf("任务产物引用解析失败: %w", err), "task_ref_expand_failed", http.StatusBadRequest)
	return taskErr
}
