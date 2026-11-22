package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/continuation"
)

func stubContinuationDeps(t *testing.T, sourceBytes []byte, fetchErr error, concatErr error) {
	t.Helper()
	prevFetch, prevConcat := fetchContinuationSource, concatContinuation
	fetchContinuationSource = func(context.Context, continuation.ContinuationPrepareOptions) ([]byte, string, error) {
		return sourceBytes, ".mp4", fetchErr
	}
	concatContinuation = func(ctx context.Context, src, gen, out string, _ *continuation.ConcatDeps) (*continuation.ConcatResult, error) {
		if concatErr != nil {
			return nil, concatErr
		}
		if err := os.WriteFile(out, []byte("merged-mp4"), 0o600); err != nil {
			return nil, err
		}
		return &continuation.ConcatResult{OutPath: out, Width: 1344, Height: 768, FPS: 24}, nil
	}
	t.Cleanup(func() {
		fetchContinuationSource, concatContinuation = prevFetch, prevConcat
	})
}

func continuationTask(dir string, snap *model.ContinuationProperties) (*model.Task, string) {
	generated := filepath.Join(dir, "task_generated.mp4")
	_ = os.WriteFile(generated, []byte("gen"), 0o600)
	return &model.Task{
		TaskID: "task_cont_1",
		UserId: 42,
		Status: model.TaskStatusInProgress,
		Properties: model.Properties{
			OriginModelName: "minimax-h3-fl2va",
			Continuation:    snap,
			H3:              &model.H3Properties{RequestedSeconds: 7, DeliveredSeconds: 7.291666666666667, Frames: 175},
		},
	}, generated
}

func TestTryConcatContinuationSkipsNonContinuation(t *testing.T) {
	dir := t.TempDir()
	task := &model.Task{TaskID: "x", Properties: model.Properties{OriginModelName: "wan"}}
	if path, ok := TryConcatContinuation(context.Background(), task, filepath.Join(dir, "a.mp4")); ok || path != "" {
		t.Fatalf("非续写任务不该拼接, got (%q,%v)", path, ok)
	}
}

func TestTryConcatContinuationSkipsWithoutGeneratedFile(t *testing.T) {
	dir := t.TempDir()
	task, _ := continuationTask(dir, &model.ContinuationProperties{SourceRef: "task:t1", SourceSeconds: 3, TotalSeconds: 10})
	if _, ok := TryConcatContinuation(context.Background(), task, ""); ok {
		t.Fatal("没有生成段文件时不该拼接")
	}
}

func TestTryConcatContinuationSuccess(t *testing.T) {
	dir := t.TempDir()
	task, generated := continuationTask(dir, &model.ContinuationProperties{
		SourceRef: "task:t1", SourceSeconds: 3, GenerateSeconds: 7, TotalSeconds: 10,
	})
	stubContinuationDeps(t, []byte("source"), nil, nil)

	merged, ok := TryConcatContinuation(context.Background(), task, generated)
	if !ok || merged == "" {
		t.Fatalf("应拼接成功, got (%q,%v)", merged, ok)
	}
	if !strings.Contains(merged, "-continuation") {
		t.Fatalf("产物名应带 -continuation 后缀: %s", merged)
	}
	if fi, err := os.Stat(merged); err != nil || fi.Size() == 0 {
		t.Fatal("拼接产物未落盘")
	}
	snap := task.Properties.Continuation
	if !snap.ConcatDone || snap.MergedPath != merged || snap.ConcatError != "" {
		t.Fatalf("快照未更新: %+v", snap)
	}
	// 交付时长 = 源 3 s + 生成段交付 7.291666…
	if got := task.Properties.H3.DeliveredSeconds; got < 10.28 || got > 10.30 {
		t.Fatalf("H3 交付时长应累加源片, got %v", got)
	}
	if task.Properties.H3.RequestedSeconds != 10 {
		t.Fatalf("请求时长应改成最终总时长, got %d", task.Properties.H3.RequestedSeconds)
	}
}

func TestTryConcatContinuationIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	task, generated := continuationTask(dir, &model.ContinuationProperties{SourceRef: "task:t1", SourceSeconds: 3})
	out := continuationOutputPath(generated)
	if err := os.WriteFile(out, []byte("already-merged"), 0o600); err != nil {
		t.Fatal(err)
	}
	stubContinuationDeps(t, nil, errors.New("不该调用"), errors.New("不该调用"))

	merged, ok := TryConcatContinuation(context.Background(), task, generated)
	if !ok || merged != out {
		t.Fatalf("已有产物应直接复用, got (%q,%v)", merged, ok)
	}
	if !task.Properties.Continuation.ConcatDone {
		t.Fatal("复用时应标记 ConcatDone")
	}
}

// 取源片失败：记原因、返回 false（调用方按生成段交付），任务不判失败。
func TestTryConcatContinuationDegradesOnSourceFailure(t *testing.T) {
	dir := t.TempDir()
	task, generated := continuationTask(dir, &model.ContinuationProperties{SourceRef: "task:gone", SourceSeconds: 3})
	stubContinuationDeps(t, nil, errors.New("源片已过期"), nil)

	merged, ok := TryConcatContinuation(context.Background(), task, generated)
	if ok || merged != "" {
		t.Fatalf("应降级, got (%q,%v)", merged, ok)
	}
	if !strings.Contains(task.Properties.Continuation.ConcatError, "源片已过期") {
		t.Fatalf("失败原因未记录: %+v", task.Properties.Continuation)
	}
	if task.Status == model.TaskStatusFailure {
		t.Fatal("拼接失败不该把任务判失败")
	}
}

func TestTryConcatContinuationDegradesOnConcatFailure(t *testing.T) {
	dir := t.TempDir()
	task, generated := continuationTask(dir, &model.ContinuationProperties{SourceRef: "task:t1", SourceSeconds: 3})
	stubContinuationDeps(t, []byte("source"), nil, errors.New("ffmpeg 崩了"))

	if _, ok := TryConcatContinuation(context.Background(), task, generated); ok {
		t.Fatal("拼接失败应降级")
	}
	if !strings.Contains(task.Properties.Continuation.ConcatError, "ffmpeg 崩了") {
		t.Fatalf("失败原因未记录: %+v", task.Properties.Continuation)
	}
	if task.Properties.Continuation.ConcatDone {
		t.Fatal("失败时不该标记 ConcatDone（否则重试会跳过）")
	}
}

func TestContinuationOutputPath(t *testing.T) {
	got := continuationOutputPath("/nfs/out/task_abc.mp4")
	if got != "/nfs/out/task_abc-continuation.mp4" {
		t.Fatalf("got %q", got)
	}
	// 无扩展名时补 .mp4
	if got := continuationOutputPath("/nfs/out/task_abc"); got != "/nfs/out/task_abc-continuation.mp4" {
		t.Fatalf("got %q", got)
	}
}
