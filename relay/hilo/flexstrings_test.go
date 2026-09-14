package hilo

import (
	"encoding/json"
	"testing"
)

func unmarshalFlex(t *testing.T, raw string) FlexStrings {
	t.Helper()
	var v struct {
		X FlexStrings `json:"x"`
	}
	if err := json.Unmarshal([]byte(`{"x":`+raw+`}`), &v); err != nil {
		t.Fatalf("解析 %s 失败：%v", raw, err)
	}
	return v.X
}

func eq(t *testing.T, got FlexStrings, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("条数不对：实得 %v，应为 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 条：实得 %q，应为 %q", i, got[i], want[i])
		}
	}
}

// 正常的字符串数组。
func TestFlexStringsPlainArray(t *testing.T) {
	eq(t, unmarshalFlex(t, `["a","b"]`), "a", "b")
}

// **对象数组:实测打回来的那一种。**
//
// qwen3.8-flash-fp8 把 sync_rules 写成了
// {"type":"lip_sync","subject_id":…,"instruction":"Sync mouth…"}。
// 严格反序列化在这里是整份失败 —— 一个字段的形态差异,把一次其余全对的
// 编译整个打掉,报的还是一句 Go 类型错误,重修那侧完全看不懂。
func TestFlexStringsObjectArrayTakesInstruction(t *testing.T) {
	eq(t, unmarshalFlex(t,
		`[{"type":"lip_sync","subject_id":"subject_1","instruction":"Sync mouth movements."}]`),
		"Sync mouth movements.")
}

// 没有正文键时退成 key: value,并**按键名排序**。
//
// Go 的 map 遍历顺序是随机的,不排序会让同一份 IR 每次渲染出不同的文本 ——
// 那种不确定性最难查。
func TestFlexStringsObjectWithoutTextKeyIsDeterministic(t *testing.T) {
	raw := `[{"zeta":"3","alpha":"1","mid":"2"}]`
	first := unmarshalFlex(t, raw)
	for i := 0; i < 20; i++ {
		eq(t, unmarshalFlex(t, raw), first...)
	}
	eq(t, first, "alpha: 1, mid: 2, zeta: 3")
}

// 单个字符串 = 只有一条。模型偷懒时常这么写。
func TestFlexStringsBareString(t *testing.T) {
	eq(t, unmarshalFlex(t, `"only one"`), "only one")
}

// 整个字段写成一个对象。
func TestFlexStringsSingleObject(t *testing.T) {
	eq(t, unmarshalFlex(t, `{"rule":"keep it quiet"}`), "keep it quiet")
}

// 数字和布尔也读得进去,不至于为了一个 0 让整份编译失败。
func TestFlexStringsScalars(t *testing.T) {
	eq(t, unmarshalFlex(t, `[1,2.5,true,"x"]`), "1", "2.5", "true", "x")
}

// 空值和空串跳过,不留空条 —— 渲染时会变成一个空行。
func TestFlexStringsSkipsEmpty(t *testing.T) {
	eq(t, unmarshalFlex(t, `["a","","  ",null,"b"]`), "a", "b")
	eq(t, unmarshalFlex(t, `null`))
}

// **容忍形态,不容忍类型错乱。** 数字当成整个列表是写错了,该报出来。
func TestFlexStringsRejectsNonList(t *testing.T) {
	var v struct {
		X FlexStrings `json:"x"`
	}
	if err := json.Unmarshal([]byte(`{"x":42}`), &v); err == nil {
		t.Error("把一个数字当成列表应当报错")
	}
}

// 序列化回去仍是干净的字符串数组(IR 会被回放给模型做重修)。
func TestFlexStringsMarshalsAsArray(t *testing.T) {
	b, _ := json.Marshal(FlexStrings{"a", "b"})
	if string(b) != `["a","b"]` {
		t.Errorf("序列化结果不对：%s", b)
	}
	b, _ = json.Marshal(FlexStrings(nil))
	if string(b) != `[]` {
		t.Errorf("空列表应序列化成 []，实得 %s", b)
	}
}

// **实测形状的回归守卫。**
//
// 这是 qwen3.8-flash-fp8 真实交来的 audio_plan,它让一次其余字段全对的
// 编译整个失败,报的是 "cannot unmarshal object into Go struct field
// IRAudioPlan.audio_plan.sync_rules of type string" —— 重修那侧完全看不懂。
func TestContextIRAcceptsObservedSyncRulesShape(t *testing.T) {
	raw := `{
	  "audio_plan": {
	    "ambient_sound": "soft ocean waves",
	    "sync_rules": [
	      {"type":"lip_sync","subject_id":"subject_1","shot_id":"02",
	       "instruction":"Sync mouth movements with the Chinese dialogue."}
	    ]
	  }
	}`
	var ir ContextIR
	if err := json.Unmarshal([]byte(raw), &ir); err != nil {
		t.Fatalf("实测形状解析失败：%v", err)
	}
	eq(t, ir.AudioPlan.SyncRules, "Sync mouth movements with the Chinese dialogue.")
}
