package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

// normalizeModerationPolicyConfig 是「校验 / 落库 / 运行期比对」三者一致的唯一保证。
//
// 少了它：校验用 TrimSpace 后的名字比对通过，落库存的却是带空格的原文，
// 而运行期 ResolvePolicy 是精确比较（Name == name）——于是判定悄悄落到
// Policies[0]，界面上完全看不出来。
func TestNormalizeModerationPolicyConfig(t *testing.T) {
	req := moderationPolicyConfigRequest{
		Policies: []system_setting.ModerationPolicy{
			{Name: "  标准  "},
			{Name: "严格\t"},
		},
		DefaultPolicy: " 标准 ",
		GroupPolicies: map[string]system_setting.GroupPolicy{
			"vip": {Mode: system_setting.ModerationModeObserve, Policy: "  严格 "},
		},
	}

	normalizeModerationPolicyConfig(&req)

	if req.Policies[0].Name != "标准" || req.Policies[1].Name != "严格" {
		t.Fatalf("策略名未被规范化: %+v", req.Policies)
	}
	if req.DefaultPolicy != "标准" {
		t.Fatalf("默认策略名未被规范化: %q", req.DefaultPolicy)
	}
	// 分组绑定里的策略名最容易漏——它藏在 map 的 value 里，而 map 的元素不可寻址，
	// 必须取出来改完再写回去。漏掉这一处，绑定就会在运行期失配。
	if req.GroupPolicies["vip"].Policy != "严格" {
		t.Fatalf("分组绑定的策略名未被规范化: %q", req.GroupPolicies["vip"].Policy)
	}
	// 规范化不该动其它字段
	if req.GroupPolicies["vip"].Mode != system_setting.ModerationModeObserve {
		t.Fatal("规范化不该改动运行模式")
	}
}

// TestMaterializeCategoryActionsIsBehaviorPreserving 补全类别必须按构造零行为变化。
//
// 这是整个「补全」能安全上线的前提：它补的必须是 CategoryAction 此刻就会返回的
// 那个动作。取错一张表（比如拿开箱默认 DefaultCategoryAction 去补）就等于借着
// 「补全」偷偷放宽——原来九类缺键时是 block，而开箱默认里 violent 是 log、
// pii 是 ignore，一次保存就把存量策略的判定改了，而运营只是点了保存。
func TestMaterializeCategoryActionsIsBehaviorPreserving(t *testing.T) {
	// 线上「严格」的真实形状：九类全显式 block，五个新类别缺键。
	strict := system_setting.ModerationPolicy{
		Name:       "严格",
		Strictness: system_setting.StrictnessStrict,
		Categories: map[string]string{
			system_setting.CategorySexual:    system_setting.CategoryActionBlock,
			system_setting.CategoryIllegal:   system_setting.CategoryActionBlock,
			system_setting.CategoryPolitical: system_setting.CategoryActionBlock,
			system_setting.CategoryJailbreak: system_setting.CategoryActionBlock,
			system_setting.CategoryViolent:   system_setting.CategoryActionBlock,
			system_setting.CategorySelfHarm:  system_setting.CategoryActionBlock,
			system_setting.CategoryUnethical: system_setting.CategoryActionBlock,
			system_setting.CategoryPII:       system_setting.CategoryActionBlock,
			system_setting.CategoryCopyright: system_setting.CategoryActionBlock,
		},
	}
	// 一条缺键更多的：只配了两项，其余七项靠「缺键 = block」。
	partial := system_setting.ModerationPolicy{
		Name: "只配了两项",
		Categories: map[string]string{
			system_setting.CategorySexual: system_setting.CategoryActionBlock,
			system_setting.CategoryPII:    system_setting.CategoryActionIgnore,
		},
	}
	// 连 map 都是 nil 的（手写 JSON 省略了 categories）。
	empty := system_setting.ModerationPolicy{Name: "空"}

	for _, p := range []system_setting.ModerationPolicy{strict, partial, empty} {
		// 补全前每一类的生效动作
		want := make(map[string]string, len(system_setting.AllCategories))
		for _, c := range system_setting.AllCategories {
			want[c] = p.CategoryAction(c)
		}

		got := p // 值拷贝，但 Categories 是同一个 map，所以下面单独建一份
		got.Categories = make(map[string]string, len(p.Categories))
		for k, v := range p.Categories {
			got.Categories[k] = v
		}
		materializeCategoryActions(&got)

		// 一、14 类必须都在
		for _, c := range system_setting.AllCategories {
			if _, ok := got.Categories[c]; !ok {
				t.Fatalf("策略「%s」补全后仍缺类别 %s", p.Name, c)
			}
		}
		// 二、每一类的生效动作必须与补全前完全一致
		for _, c := range system_setting.AllCategories {
			if got.CategoryAction(c) != want[c] {
				t.Fatalf("策略「%s」的类别 %s 在补全后变了：%s → %s（补全必须零行为变化）",
					p.Name, c, want[c], got.CategoryAction(c))
			}
		}
		// 三、补全后再补一次必须幂等
		before := len(got.Categories)
		materializeCategoryActions(&got)
		if len(got.Categories) != before {
			t.Fatalf("策略「%s」补全不幂等", p.Name)
		}
	}

	// 反面钉死：原九类缺键补出来的必须是 block，而不是开箱默认里的 log/ignore。
	// 这一条直接拦住「取错一张表」那个改动。
	for _, c := range []string{
		system_setting.CategoryViolent, system_setting.CategorySelfHarm,
		system_setting.CategoryUnethical, system_setting.CategoryPII,
		system_setting.CategoryCopyright,
	} {
		p := system_setting.ModerationPolicy{Name: "x"}
		materializeCategoryActions(&p)
		if p.Categories[c] != system_setting.CategoryActionBlock {
			t.Fatalf("原九类 %s 缺键时应补成 block（既有契约），得到 %s——"+
				"补成开箱默认会让一次保存静默放宽存量策略", c, p.Categories[c])
		}
	}
}
