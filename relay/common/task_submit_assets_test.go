package common

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
)

// 这组用例钉的是**口径本身**:每一种合法编码,访问器都要给出同一个答案。
// 三轮检视抓到的都是「生成段读得到、增强段读不到」,而两处现在都只经过这里 ——
// 新增一种编码时若漏改,这里先红,而不是等到线上出现"提示词描述的是另一个输入"。
func TestTaskSubmitReqRefAccessorsCoverEveryLegalEncoding(t *testing.T) {
	cases := []struct {
		name             string
		raw              string
		imgs, vids, auds int
	}{
		{
			name: "数组",
			raw:  `{"metadata":{"src_ref_images":["a.png","b.png"],"reference_videos":["v.mp4"],"reference_audios":["x.wav","y.wav"]}}`,
			imgs: 2, vids: 1, auds: 2,
		},
		{
			name: "单数键名(与 doubao/Ark 对齐的合法拼法)",
			raw:  `{"metadata":{"reference_video":"v.mp4","reference_audio":"x.wav"}}`,
			imgs: 0, vids: 1, auds: 1,
		},
		{
			name: "逗号分隔的单串",
			raw:  `{"metadata":{"src_ref_images":"a.png,b.png,c.png"}}`,
			imgs: 3,
		},
		{
			name: "metadata 是 JSON 编码的字符串",
			raw:  `{"metadata":"{\"src_ref_images\":[\"a.png\"]}"}`,
			imgs: 1,
		},
		{
			name: "data URL 不按逗号拆",
			raw:  `{"metadata":{"src_ref_images":"data:image/png;base64,iVBORw0KGgo="}}`,
			imgs: 1,
		},
	}
	for _, c := range cases {
		var req TaskSubmitReq
		if err := common.Unmarshal([]byte(c.raw), &req); err != nil {
			t.Fatalf("%s: 解码失败 %v", c.name, err)
		}
		if got := len(req.RefImages()); got != c.imgs {
			t.Errorf("%s: RefImages 数量 = %d, want %d", c.name, got, c.imgs)
		}
		if got := len(req.RefVideos()); got != c.vids {
			t.Errorf("%s: RefVideos 数量 = %d, want %d", c.name, got, c.vids)
		}
		if got := len(req.RefAudios()); got != c.auds {
			t.Errorf("%s: RefAudios 数量 = %d, want %d", c.name, got, c.auds)
		}
	}
}

// 时长的两种合法写法必须给出同一个数:适配器按它出片、计费按它记账、
// 增强段按它约束分镜时间点,三处分叉任何一处都是静默错误。
func TestTaskSubmitReqEffectiveDuration(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"duration 整数", `{"duration":5}`, 5},
		{"duration 字符串", `{"duration":"5"}`, 5},
		{"只给 seconds", `{"seconds":"8"}`, 8},
		{"duration 优先于 seconds", `{"duration":5,"seconds":"8"}`, 5},
		{"都没有", `{"prompt":"x"}`, 0},
		// 整值浮点:Python 的 json.dumps(15.0)/JS 的 15.0 都发这个形状。旧实现解不出来,
		// Duration 留 0,请求照常 200 而引擎按默认帧数出片 —— 时长被无声换掉。
		{"duration 整值浮点", `{"duration":15.0}`, 15},
		{"duration 整值浮点字符串", `{"duration":"15.0"}`, 15},
		{"duration 浮点零", `{"duration":0.0}`, 0},
		{"duration null 当作没传", `{"duration":null}`, 0},
		{"duration 空串当作没传", `{"duration":""}`, 0},
	}
	for _, c := range cases {
		var req TaskSubmitReq
		if err := common.Unmarshal([]byte(c.raw), &req); err != nil {
			t.Fatalf("%s: 解码失败 %v", c.name, err)
		}
		if got := req.EffectiveDuration(); got != c.want {
			t.Errorf("%s: EffectiveDuration = %d, want %d", c.name, got, c.want)
		}
	}
}

// 给不出准确秒数就报错,不截断也不静默丢弃。
//
// 15.5 截成 15 是把调用方要的时长换成另一个,与旧实现"解不出来就当没传"是同一类
// 错误(都让请求照常成功却出了别的长度)。这几家上游的时长档位本来都是整秒,
// 非整值只可能是调用方算错了,让它当场知道比事后对着片子长度猜强。
func TestTaskSubmitReqRejectsNonIntegerDuration(t *testing.T) {
	for _, raw := range []string{
		`{"duration":15.5}`,
		`{"duration":"15.5"}`,
		`{"duration":"abc"}`,
	} {
		var req TaskSubmitReq
		if err := common.Unmarshal([]byte(raw), &req); err == nil {
			t.Errorf("%s: 期望报错,实际解出 Duration=%d", raw, req.Duration)
		}
	}
}

// 不认识的类型维持既有的宽松处理(当作没传),别在这次修复里顺手扩大成报错 ——
// 那会让一批今天还能跑的调用方突然 400。
func TestTaskSubmitReqIgnoresUnusableDurationTypes(t *testing.T) {
	for _, raw := range []string{
		`{"duration":true}`,
		`{"duration":[5]}`,
		`{"duration":{"seconds":5}}`,
	} {
		var req TaskSubmitReq
		if err := common.Unmarshal([]byte(raw), &req); err != nil {
			t.Errorf("%s: 不应报错,得到 %v", raw, err)
		} else if req.Duration != 0 {
			t.Errorf("%s: 期望 Duration=0, 得到 %d", raw, req.Duration)
		}
	}
}

// nil 接收者不能 panic:中间件在图片聚合等场景下拿到的就是解不出来的请求。
func TestTaskSubmitReqAccessorsNilSafe(t *testing.T) {
	var req *TaskSubmitReq
	if req.RefImages() != nil || req.RefVideos() != nil || req.RefAudios() != nil ||
		req.TaskType() != "" || req.EffectiveDuration() != 0 {
		t.Error("nil 接收者应返回零值")
	}
}

// 条件图是**回落**不是并集:调用方同时给 images 和 image 时,生成段只看 images。
// 取并集会让同一张图数两遍,增强段的 <Picture N> 标号整体后移 —— flf2v 会把
// "<Picture 2> 是尾帧"指到重复的首帧上,对齐指令正好写反,而生成段毫无反应。
func TestTaskSubmitReqFrameImagesFallsBackNotUnions(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"只有 images", `{"images":["a.png","b.png"]}`, []string{"a.png", "b.png"}},
		{"只有 image", `{"image":"a.png"}`, []string{"a.png"}},
		{"只有 input_reference", `{"input_reference":"a.png"}`, []string{"a.png"}},
		{"images 优先于 image", `{"images":["a.png","b.png"],"image":"a.png"}`, []string{"a.png", "b.png"}},
		{"images 优先于 input_reference", `{"images":["a.png"],"input_reference":"z.png"}`, []string{"a.png"}},
		{"image 优先于 input_reference", `{"image":"a.png","input_reference":"z.png"}`, []string{"a.png"}},
		{"都没有", `{"prompt":"x"}`, nil},
	}
	for _, c := range cases {
		var req TaskSubmitReq
		if err := common.Unmarshal([]byte(c.raw), &req); err != nil {
			t.Fatalf("%s: 解码失败 %v", c.name, err)
		}
		got := req.FrameImages()
		if len(got) != len(c.want) {
			t.Errorf("%s: FrameImages = %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: FrameImages = %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}
