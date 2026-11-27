package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/hilo"
)

// 一个聚合模型按门面 task_type 分派到两个 checkpoint ——
// 生成段默认 fl2va，参考族 r2va 落到 ref2va。
const taskModelsConfig = `[{
	"name":"h3-both","type":"video","enabled":true,
	"generate":{
		"model":"minimax-h3-fl2va",
		"task_models":{
			"t2v":"minimax-h3-fl2va","i2v":"minimax-h3-fl2va",
			"l2va":"minimax-h3-fl2va","flf2v":"minimax-h3-fl2va",
			"r2va":"minimax-h3-ref2va"
		}
	}
}]`

// 分派结果必须同时出现在 body["model"] 和**返回值**里。
//
// 两处都要断言：body 是适配器真正读的那份，返回值是 distributor 拿去选渠道/计费/
// 写日志的那份。只改其中一处不会报错 —— 请求会打到 A 部署、却按 B 的名字记账。
func TestTaskModelsDispatchByRoutingTaskType(t *testing.T) {
	withAggregateConfig(t, taskModelsConfig)

	cases := []struct {
		name  string
		body  string
		want  string
		notes string
	}{
		{"显式 t2v", `{"prompt":"cat","metadata":{"task_type":"t2v"}}`, "minimax-h3-fl2va", "纯文生"},
		{"显式 i2v", `{"prompt":"cat","images":["a.png"],"metadata":{"task_type":"i2v"}}`, "minimax-h3-fl2va", "首帧"},
		{"显式 l2va", `{"prompt":"cat","images":["a.png"],"metadata":{"task_type":"l2va"}}`, "minimax-h3-fl2va", "只给尾帧"},
		{"显式 flf2v", `{"prompt":"cat","images":["a.png","b.png"],"metadata":{"task_type":"flf2v"}}`, "minimax-h3-fl2va", "首尾帧"},
		{"显式 r2va", `{"prompt":"cat","metadata":{"task_type":"r2va","src_ref_images":["a.png"]}}`, "minimax-h3-ref2va", "多模态参考"},
		{"r2v 别名", `{"prompt":"cat","metadata":{"task_type":"r2v","src_ref_images":["a.png"]}}`, "minimax-h3-ref2va", "参考族别名归一"},
		{"rv2v 别名", `{"prompt":"cat","metadata":{"task_type":"rv2v","src_ref_images":["a.png"]}}`, "minimax-h3-ref2va", "参考族别名归一"},
		{"续写按 i2v", `{"prompt":"cat","metadata":{"task_type":"continuation","source_video":"task:tk1"}}`, "minimax-h3-fl2va", "展开时还是 continuation，必须先归一"},
		{"续写 image_mode 入口", `{"prompt":"cat","metadata":{"image_mode":"video-extension","source_video":"task:tk1"}}`, "minimax-h3-fl2va", "第二个等价入口没有 task_type"},
		{"无 task_type + 参考图", `{"prompt":"cat","metadata":{"src_ref_images":["a.png"]}}`, "minimax-h3-ref2va", "形态推断：有参考素材"},
		{"无 task_type + 无素材", `{"prompt":"cat"}`, "minimax-h3-fl2va", "形态推断：纯文生"},
		{"两张帧图无 task_type", `{"prompt":"cat","images":["a.png","b.png"]}`, "minimax-h3-fl2va", "帧图不推断，回落默认段"},
		{"不认识的玩法", `{"prompt":"cat","metadata":{"task_type":"s2v","audio":"a.mp3"}}`, "minimax-h3-fl2va", "显式声明别的玩法时不猜，回落默认段"},
		{"metadata 是 JSON 字符串", `{"prompt":"cat","metadata":"{\"task_type\":\"r2va\",\"src_ref_images\":[\"a.png\"]}"}`, "minimax-h3-ref2va", "字符串形态的 metadata 也要认"},
		{"字符串 metadata 只有参考图", `{"prompt":"cat","metadata":"{\"src_ref_images\":[\"a.png\"]}"}`, "minimax-h3-ref2va", "没有 task_type 时也要能从字符串 metadata 里推断素材"},
		{"metadata 为 null", `{"prompt":"cat","metadata":null}`, "minimax-h3-fl2va", "null 是合法的空 meta，照常推断 t2v"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(tc.body))

			agg := common.GetAggregateModel("h3-both")
			require.NotNil(t, agg)

			selected, err := applyAggregateExpansion(c, "h3-both", agg.Generate.Model, agg)
			require.NoError(t, err, tc.notes)
			require.Equal(t, tc.want, selected, "返回值（选渠道/计费）:%s", tc.notes)
			require.Equal(t, tc.want, readBodyMap(t, c)["model"], "body（发给上游）:%s", tc.notes)

			exp := GetAggregateExpansion(c)
			require.NotNil(t, exp)
			require.Equal(t, tc.want, exp.GenerateModel, "展开记录里要留痕:排障才知道这次打到哪个部署")
		})
	}
}

// 没配 task_models 的聚合行为不变：仍然按 generate.model 走，不去读目录。
func TestTaskModelsAbsentKeepsDefaultModel(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"prompt":"cat","metadata":{"task_type":"r2va"}}`))

	agg := common.GetAggregateModel("h3-2k")
	require.NotNil(t, agg)
	selected, err := applyAggregateExpansion(c, "h3-2k", agg.Generate.Model, agg)
	require.NoError(t, err)
	require.Equal(t, "minimax-h3", selected, "没配 task_models 时不该改变行为")
}

// 「metadata 读不出来」的判据必须是**归一化之后**（norm.Metadata == nil），
// 不是"原始 body 里它不是对象"：字符串编码的 metadata 会被 UnmarshalJSON 解开，
// 那时拒绝推断等于明明有证据却说没有，参考图请求会被按帧族提交、被引擎 400。
func TestResolveCompilerTaskTypeMetadataShapes(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		want      hilo.TaskType
		rationale string
	}{
		{
			name:      "字符串 metadata + 参考图（无 task_type）",
			body:      `{"prompt":"cat","metadata":"{\"src_ref_images\":[\"a.png\"]}"}`,
			want:      hilo.TaskR2VA,
			rationale: "字符串形态解出来有参考素材，必须推断成 r2va",
		},
		{
			name:      "对象 metadata + 参考图（无 task_type）",
			body:      `{"prompt":"cat","metadata":{"src_ref_images":["a.png"]}}`,
			want:      hilo.TaskR2VA,
			rationale: "对照项：对象形态一直是对的",
		},
		{
			name:      "metadata 为 null 且无素材",
			body:      `{"prompt":"cat","metadata":null}`,
			want:      hilo.TaskT2V,
			rationale: "null 与「没有 metadata 键」同义，照常推断 t2v（不能退化成不增强）",
		},
		{
			name:      "没有 metadata 键",
			body:      `{"prompt":"cat"}`,
			want:      hilo.TaskT2V,
			rationale: "同上",
		},
		{
			name:      "metadata 是数组（读不出来）",
			body:      `{"prompt":"cat","metadata":[{"src_ref_images":["a.png"]}]}`,
			want:      "",
			rationale: "确实没有证据时才拒绝推断——缺证据不等于证据表明没有",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			require.NoError(t, common.Unmarshal([]byte(tc.body), &body))
			norm := normalizeTaskRequest(body)
			require.NotNil(t, norm)
			require.Equal(t, tc.want, resolveCompilerTaskType(body, norm), tc.rationale)
		})
	}
}
