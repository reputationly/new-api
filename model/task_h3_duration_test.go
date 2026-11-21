package model

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// H3 的交付时长回显：`seconds` 保持请求值（OpenAI 整数语义，存量客户端按整数解析），
// 真实交付值放 metadata —— 两者都要有，否则"客户以为 1 s、账单是 1.625 s"无从解释。
func TestToOpenAIVideoEchoesH3DeliveredDuration(t *testing.T) {
	task := &Task{
		TaskID: "task_h3_echo",
		Status: TaskStatusSuccess,
		Properties: Properties{
			OriginModelName: "minimax-h3-fl2va",
			H3: &H3Properties{
				RequestedSeconds: 1,
				DeliveredSeconds: 1.625,
				Frames:           39,
			},
		},
	}

	video := task.ToOpenAIVideo()
	require.Equal(t, "1", video.Seconds, "seconds 必须保持整数请求值")

	delivered, ok := video.Metadata["duration_seconds"].(float64)
	require.True(t, ok, "metadata.duration_seconds 缺失")
	require.InDelta(t, 1.625, delivered, 1e-9)

	require.Equal(t, 39, video.Metadata["frames"])
	require.Equal(t, 1, video.Metadata["requested_seconds"])
}

// 非 H3 任务：一个字段都不加，行为与改造前逐位一致。
func TestToOpenAIVideoLeavesOtherModelsUntouched(t *testing.T) {
	task := &Task{
		TaskID:     "task_plain",
		Status:     TaskStatusSuccess,
		Properties: Properties{OriginModelName: "wan2.2-i2v"},
	}

	video := task.ToOpenAIVideo()
	require.Empty(t, video.Seconds)
	_, has := video.Metadata["duration_seconds"]
	require.False(t, has, "非 H3 任务不该带交付时长")
}

// 请求值存在但没算交付值（理论上不该发生）：不写半份快照，避免回显出 0 秒。
func TestToOpenAIVideoSkipsIncompleteH3Snapshot(t *testing.T) {
	task := &Task{
		TaskID: "task_h3_partial",
		Status: TaskStatusSuccess,
		Properties: Properties{
			OriginModelName: "minimax-h3-fl2va",
			H3:              &H3Properties{RequestedSeconds: 5},
		},
	}

	video := task.ToOpenAIVideo()
	_, has := video.Metadata["duration_seconds"]
	require.False(t, has)
	require.True(t, math.Abs(float64(video.Progress)) >= 0) // 保持零值可用，不 panic
}

// 续写回显：三个数（源/生成/总），不回源片引用（可能带签名或 data URI）。
func TestToOpenAIVideoEchoesContinuation(t *testing.T) {
	task := &Task{
		TaskID: "task_cont_echo",
		Status: TaskStatusSuccess,
		Properties: Properties{
			OriginModelName: "minimax-h3-fl2va",
			Continuation: &ContinuationProperties{
				SourceRef:       "https://x.com/a.mp4?sign=secret",
				SourceSeconds:   3,
				GenerateSeconds: 7,
				TotalSeconds:    10,
			},
		},
	}

	video := task.ToOpenAIVideo()
	raw, ok := video.Metadata["continuation"].(map[string]any)
	require.True(t, ok, "缺少 continuation 元数据")
	require.Equal(t, 3.0, raw["source_seconds"])
	require.Equal(t, 7, raw["generate_seconds"])
	require.Equal(t, 10, raw["total_seconds"])
	_, leaked := raw["source_ref"]
	require.False(t, leaked, "不该回显源片引用")
}
