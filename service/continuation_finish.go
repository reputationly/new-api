package service

// 续写的收尾：生成段完成后，把源片与生成段拼成整段，再交给既有的落盘与结算。
//
// 为什么挂在轮询的终态分支、而不是提交时：拼接的输入是**生成段的成品**，它几百秒后
// 才出现；而且这里已经有 taskResult.NFSPath（共享存储上的真实文件），不必再猜路径。
//
// 失败口径（重要）：拼接失败**不判任务失败**。生成段已经真烧了 GPU 并计费，把它降级
// 交付出去，好过让客户既没拿到片子、又要等退款；失败原因记进 Properties.Continuation，
// 回显在 metadata.continuation.concat_error 里。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/continuation"
)

// 两个依赖抽成变量供测试注入：真实实现要下载源片并跑 ffmpeg，
// 而这一层要测的是"幂等 → 降级 → 快照改写"这条链。
var (
	concatContinuation      = continuation.Concat
	fetchContinuationSource = continuation.FetchSourceBytes
)

// TryConcatContinuation 把续写任务的"生成段"与源片拼接成整段。
//
// 返回 (mergedPath, ok)：
//   - (路径, true)  拼接成功，调用方应改用这个路径落盘；
//   - ("", false)   不是续写任务、或拼接失败（失败已记进任务，调用方按原路径交付）。
func TryConcatContinuation(ctx context.Context, task *model.Task, generatedPath string) (string, bool) {
	if task == nil || task.Properties.Continuation == nil {
		return "", false
	}
	snap := task.Properties.Continuation
	if strings.TrimSpace(generatedPath) == "" {
		// 上游只回了 URL、没有共享存储上的文件 —— 没有可拼接的输入，按原样交付。
		return "", false
	}
	outPath := continuationOutputPath(generatedPath)
	// 幂等：上一轮已经拼好（或拼到一半留下完整产物）就直接复用，别重复烧 CPU。
	if fi, err := os.Stat(outPath); err == nil && fi.Size() > 0 {
		snap.ConcatDone = true
		snap.MergedPath = outPath
		return outPath, true
	}

	dir := filepath.Dir(generatedPath)
	sourceBytes, ext, err := fetchContinuationSource(ctx, continuation.ContinuationPrepareOptions{
		Source: snap.SourceRef,
		UserID: task.UserId,
	})
	if err != nil {
		return "", failConcat(task, fmt.Errorf("取源片失败: %w", err))
	}
	if ext == "" {
		ext = ".mp4"
	}
	sourcePath := filepath.Join(dir, fmt.Sprintf("continuation_source_%s%s", task.TaskID, ext))
	if err := os.WriteFile(sourcePath, sourceBytes, 0o600); err != nil {
		return "", failConcat(task, fmt.Errorf("源片落临时文件失败: %w", err))
	}
	defer os.Remove(sourcePath)

	result, err := concatContinuation(ctx, sourcePath, generatedPath, outPath, nil)
	if err != nil {
		return "", failConcat(task, err)
	}

	snap.ConcatDone = true
	snap.MergedPath = result.OutPath
	snap.ConcatError = ""
	// 交付时长以**整段**为准：源片 + 生成段。H3 的时长快照原本只记生成段，
	// 续写拼上源片后必须一起改写，否则回显的 duration_seconds 比实际产物短一截。
	if h3 := task.Properties.H3; h3 != nil && h3.DeliveredSeconds > 0 {
		h3.DeliveredSeconds += snap.SourceSeconds
		if snap.TotalSeconds > 0 {
			h3.RequestedSeconds = snap.TotalSeconds
		}
	}
	common.SysLog(fmt.Sprintf("[continuation] 任务 %s 拼接完成: 源 %.2fs + 生成段 → %s",
		task.TaskID, snap.SourceSeconds, result.OutPath))
	return result.OutPath, true
}

// failConcat 记录失败但让调用方继续按生成段交付。
func failConcat(task *model.Task, err error) bool {
	task.Properties.Continuation.ConcatError = err.Error()
	common.SysError(fmt.Sprintf("[continuation] 任务 %s 拼接失败，降级交付生成段: %v", task.TaskID, err))
	return false
}

// continuationOutputPath 拼接产物与生成段同目录：保证与源片/生成段同一文件系统
// （concat 的临时文件也写在这里），且能被既有落盘逻辑（按 NFSRoot 相对路径取 key）识别。
func continuationOutputPath(generatedPath string) string {
	ext := filepath.Ext(generatedPath)
	base := strings.TrimSuffix(filepath.Base(generatedPath), ext)
	if ext == "" {
		ext = ".mp4"
	}
	return filepath.Join(filepath.Dir(generatedPath), base+"-continuation"+ext)
}
