package hilo

import (
	"strings"
	"testing"
)

// **只给角色，不给 URL。**
//
// 角色必须给：模型看图看不出"这张是尾帧"，那是客户端用哪个字段装它决定的
// （l2va 与 i2v 的输入形态完全相同）。猜反了视频会从结尾往后长，且不报错。
//
// URL 不能给：素材本身是随消息一起发给模型的（多模态），把地址写进文字里
// 只会让它把那串 URL 抄进描述。
func TestDescribeGivesRolesNotURLs(t *testing.T) {
	in := CompilerInput{
		UserRequest: "a cat runs", TaskType: TaskL2VA, DurationSeconds: 5,
		Assets: []CompilerAsset{
			{AssetID: "image_1", MediaType: "image", Role: "last_frame", URL: "https://secret.example/x.png"},
		},
	}
	out := in.describe()

	if !strings.Contains(out, "role: last_frame") {
		t.Error("没把角色告诉模型 —— 它推不出这张是尾帧")
	}
	if strings.Contains(out, "secret.example") {
		t.Error("URL 被写进描述了 —— 模型会把地址抄进提示词")
	}
	if !strings.Contains(out, "task.type: l2va") {
		t.Error("没下发 task_type")
	}
	if !strings.Contains(out, "task.duration_seconds: 5") {
		t.Error("没下发时长 —— 镜头时间点会落在片子之外")
	}
}

// 没有素材时要明说，而不是留一段空白让模型以为"这里本该有信息"。
func TestDescribeSaysNoneWhenNoAssets(t *testing.T) {
	in := CompilerInput{UserRequest: "a cat", TaskType: TaskT2V, DurationSeconds: 5}
	if !strings.Contains(in.describe(), "assets: (none)") {
		t.Errorf("没素材时应显式说明:\n%s", in.describe())
	}
}
