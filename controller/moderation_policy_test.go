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
