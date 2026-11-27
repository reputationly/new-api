package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

// requireWorstLevel 同一个 key 可能有多条结论（每个玩法一条），取其中**最严重**的那级。
// requireLevel 只看第一条，用在这里会误判：字典序第一个玩法往往是 OK 的。
func requireWorstLevel(t *testing.T, res *AggregateDryRunResult, key, want string) {
	t.Helper()
	rank := map[string]int{AggregateCheckOK: 0, AggregateCheckWarn: 1, AggregateCheckError: 2}
	worst, found := "", ""
	for _, ch := range res.Checks {
		if ch.Key != key {
			continue
		}
		found = key
		if worst == "" || rank[ch.Level] > rank[worst] {
			worst = ch.Level
		}
	}
	if found == "" {
		t.Fatalf("没有 %q 这条检查：%+v", key, res.Checks)
	}
	if worst != want {
		t.Fatalf("%q 的最严级别应为 %s，实得 %s（%+v）", key, want, worst, res.Checks)
	}
}

// worstMessage 汇总该 key 下所有消息，便于断言"点名了哪个模型/分组"。
func worstMessage(res *AggregateDryRunResult, key string) string {
	var parts []string
	for _, ch := range res.Checks {
		if ch.Key == key {
			parts = append(parts, ch.Message)
		}
	}
	return strings.Join(parts, " | ")
}

func taskModelsAgg() *common.AggregateModel {
	return &common.AggregateModel{
		Name: "h3", Type: "video",
		Generate: common.AggregateGenerate{
			Model: "minimax-h3-fl2va",
			TaskModels: map[string]string{
				"t2v":   "minimax-h3-fl2va",
				"i2v":   "minimax-h3-fl2va",
				"l2va":  "minimax-h3-fl2va",
				"flf2v": "minimax-h3-fl2va",
				"r2va":  "minimax-h3-ref2va",
			},
		},
	}
}

// 每个 task_models 的 value 都要过一遍「生成段」那套检查，并计入 Billable ——
// 客户的账单上会出现它，配置的人就得在看到名字的那个地方看到它。
func TestDryRunChecksTaskModels(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"minimax-h3-fl2va":  {groups: []string{"default"}},
		"minimax-h3-ref2va": {groups: []string{"default"}},
	})

	res := DryRunAggregateModel(taskModelsAgg(), map[string]int{"h3": 1})
	requireWorstLevel(t, res, "task_models", AggregateCheckOK)
	if !res.Passed {
		t.Fatalf("合法配置应通过：%+v", res.Checks)
	}
	want := []string{"minimax-h3-fl2va", "minimax-h3-ref2va"}
	if len(res.Billable) != len(want) {
		t.Fatalf("Billable = %v, want %v", res.Billable, want)
	}
	for i := range want {
		if res.Billable[i] != want[i] {
			t.Fatalf("Billable = %v, want %v", res.Billable, want)
		}
	}
}

// key 拼错时运行时**静默不命中**（每个请求都回落默认段），配置页上完全看不出来 ——
// 必须报 error。
func TestDryRunRejectsUnknownTaskModelKey(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"minimax-h3-fl2va":  {groups: []string{"default"}},
		"minimax-h3-ref2va": {groups: []string{"default"}},
	})
	agg := taskModelsAgg()
	agg.Generate.TaskModels["r2v"] = "minimax-h3-ref2va" // r2v 是别名，不是门面 key

	res := DryRunAggregateModel(agg, map[string]int{"h3": 1})
	requireWorstLevel(t, res, "task_models", AggregateCheckError)
	if !strings.Contains(worstMessage(res, "task_models"), "r2v") {
		t.Errorf("错误消息要点名是哪个 key：%q", worstMessage(res, "task_models"))
	}
}

// value 写成另一个聚合模型：聚合名没有渠道 ability，原样发给上游等于发一个不存在的模型。
func TestDryRunRejectsAggregateTaskModelValue(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"minimax-h3-fl2va": {groups: []string{"default"}},
	})
	agg := taskModelsAgg()
	agg.Generate.TaskModels["r2va"] = "another-aggregate"

	res := DryRunAggregateModel(agg, map[string]int{"h3": 1, "another-aggregate": 1})
	requireWorstLevel(t, res, "task_models", AggregateCheckError)
}

// 覆盖不全不是错（回落默认段是合法配置），但要说出来：没覆盖的玩法落到默认段上，
// 而默认段未必在对应分区。
func TestDryRunWarnsOnUncoveredTaskTypes(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"minimax-h3-fl2va":  {groups: []string{"default"}},
		"minimax-h3-ref2va": {groups: []string{"default"}},
	})
	agg := taskModelsAgg()
	delete(agg.Generate.TaskModels, "l2va")

	res := DryRunAggregateModel(agg, map[string]int{"h3": 1})
	requireWorstLevel(t, res, "task_models", AggregateCheckWarn)
	if !res.Passed {
		t.Fatalf("覆盖不全是 warn 不是 error：%+v", res.Checks)
	}
}

// 留空 groups 时按「全部生成段都有渠道的分组」继承；交集为空必须报 error。
//
// 运行时留空 groups 等价于"不限制"，若这里放过，客户能调进来，而按玩法分派到的
// 那个 checkpoint 在该分组没有渠道 —— 某个玩法必然失败，错误却来自渠道层。
func TestDryRunRejectsEmptyGroupIntersection(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"minimax-h3-fl2va":  {groups: []string{"default"}},
		"minimax-h3-ref2va": {groups: []string{"ref"}},
	})

	res := DryRunAggregateModel(taskModelsAgg(), map[string]int{"h3": 1})
	requireWorstLevel(t, res, "groups", AggregateCheckError)
	msg := worstMessage(res, "groups")
	for _, want := range []string{"minimax-h3-fl2va", "minimax-h3-ref2va", "default", "ref"} {
		if !strings.Contains(msg, want) {
			t.Errorf("报错要点名是哪些模型/分组：%q 缺 %q", msg, want)
		}
	}
}

// 继承是**交集**，不是默认段那一份。
func TestDryRunInheritsGroupIntersection(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"minimax-h3-fl2va":  {groups: []string{"default", "vip"}},
		"minimax-h3-ref2va": {groups: []string{"vip"}},
	})

	res := DryRunAggregateModel(taskModelsAgg(), map[string]int{"h3": 1})
	requireWorstLevel(t, res, "groups", AggregateCheckOK)
	if len(res.Groups) != 1 || res.Groups[0] != "vip" {
		t.Fatalf("应继承交集 [vip]，实得 %v", res.Groups)
	}
}

// 显式配了 groups 时**逐个生成段**校验，消息里要写明是哪个模型哪个玩法。
func TestDryRunChecksGroupsPerTaskModel(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"minimax-h3-fl2va":  {groups: []string{"default", "vip"}},
		"minimax-h3-ref2va": {groups: []string{"default"}},
	})
	agg := taskModelsAgg()
	agg.Groups = []string{"default", "vip"}

	res := DryRunAggregateModel(agg, map[string]int{"h3": 1})
	requireWorstLevel(t, res, "groups", AggregateCheckError)
	msg := worstMessage(res, "groups")
	if !strings.Contains(msg, "minimax-h3-ref2va") || !strings.Contains(msg, "r2va") {
		t.Errorf("报错要指明是哪个生成段、哪个玩法：%q", msg)
	}
}
