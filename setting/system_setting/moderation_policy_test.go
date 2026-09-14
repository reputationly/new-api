package system_setting

import (
	"strings"
	"testing"
)

// 策略配置的校验。这几条错配的共同点是**静默失效**：保存成功、页面正常、
// 判定悄悄按另一套规则走，要等到有人问「为什么没拦住」才发现。
// 所以校验必须堵在唯一的写入路径上，而不是只做前端提示。

func TestValidateModerationPolicies(t *testing.T) {
	valid := ModerationPolicy{
		Name:       "标准",
		Strictness: StrictnessStandard,
		Categories: map[string]string{
			CategorySexual:  CategoryActionBlock,
			CategoryViolent: CategoryActionLog,
			CategoryPII:     CategoryActionIgnore,
		},
	}

	if err := ValidateModerationPolicies([]ModerationPolicy{valid}); err != nil {
		t.Fatalf("合法策略被拒: %v", err)
	}

	t.Run("空列表要拒绝", func(t *testing.T) {
		// 一条都没有时 ResolvePolicy 返回 nil，调用方按「无策略 = 只跑 L0」处理——
		// 模型层白配了，而界面上看不出来。
		if err := ValidateModerationPolicies(nil); err == nil {
			t.Fatal("空策略列表必须被拒绝")
		}
	})

	t.Run("未知类别要拒绝", func(t *testing.T) {
		p := valid
		p.Categories = map[string]string{"nsfw": CategoryActionIgnore}
		err := ValidateModerationPolicies([]ModerationPolicy{p})
		if err == nil {
			t.Fatal("未知类别必须被拒绝：CategoryAction 查不到会按「未登记即 block」处置，" +
				"于是一个本想放宽的类别反而变成最严的那档")
		}
		if !strings.Contains(err.Error(), "nsfw") {
			t.Fatalf("错误信息要指出是哪个类别: %v", err)
		}
	})

	t.Run("非法处置要拒绝", func(t *testing.T) {
		p := valid
		p.Categories = map[string]string{CategorySexual: "deny"}
		if err := ValidateModerationPolicies([]ModerationPolicy{p}); err == nil {
			t.Fatal("非法处置值必须被拒绝")
		}
	})

	t.Run("非法严格度要拒绝", func(t *testing.T) {
		p := valid
		p.Strictness = "very-strict"
		if err := ValidateModerationPolicies([]ModerationPolicy{p}); err == nil {
			t.Fatal("非法严格度必须被拒绝：parseVerdict 的 switch 会落到 default，" +
				"按 standard 处理，运营以为调了严格度其实没有")
		}
	})

	t.Run("空严格度合法", func(t *testing.T) {
		// 零值按 standard 处理，是既有行为，不该被校验挡住
		p := valid
		p.Strictness = ""
		if err := ValidateModerationPolicies([]ModerationPolicy{p}); err != nil {
			t.Fatalf("空严格度应当合法（按 standard 处理）: %v", err)
		}
	})

	t.Run("重名要拒绝", func(t *testing.T) {
		if err := ValidateModerationPolicies([]ModerationPolicy{valid, valid}); err == nil {
			t.Fatal("重名必须被拒绝：分组按名称查找策略，重名会让绑定指向哪一条变得不确定")
		}
	})

	t.Run("无名要拒绝", func(t *testing.T) {
		p := valid
		p.Name = "  "
		if err := ValidateModerationPolicies([]ModerationPolicy{p}); err == nil {
			t.Fatal("空名称必须被拒绝")
		}
	})
}

func TestValidateModerationPolicyConfig(t *testing.T) {
	policies := []ModerationPolicy{
		{Name: "标准", Strictness: StrictnessStandard},
		{Name: "严格", Strictness: StrictnessStrict},
	}

	t.Run("三者一致时放行", func(t *testing.T) {
		err := ValidateModerationPolicyConfig(policies, "标准",
			map[string]GroupPolicy{"vip": {Mode: ModerationModeObserve, Policy: "严格"}})
		if err != nil {
			t.Fatalf("合法配置被拒: %v", err)
		}
	})

	t.Run("改名默认策略必须能通过", func(t *testing.T) {
		// 这是拆成三次 PUT 时必然死锁的那个操作：先写 policies 会因旧的
		// default_policy 还指着旧名被拒，先写 default_policy 又会因新名还不存在被拒。
		// 一次性提交时两边都是新值，天然一致。
		renamed := []ModerationPolicy{{Name: "标准v2", Strictness: StrictnessStandard}}
		if err := ValidateModerationPolicyConfig(renamed, "标准v2", nil); err != nil {
			t.Fatalf("改名默认策略应当能通过: %v", err)
		}
	})

	t.Run("同一次里先解绑再删策略必须能通过", func(t *testing.T) {
		// 另一个死锁：前端按本地状态判断可以删，后端拿已存的 group_policies 一比
		// 还绑着，于是拒。一次性提交时看到的是解绑后的绑定表。
		only := []ModerationPolicy{{Name: "标准", Strictness: StrictnessStandard}}
		if err := ValidateModerationPolicyConfig(only, "标准",
			map[string]GroupPolicy{"vip": {Mode: ModerationModeObserve}}); err != nil {
			t.Fatalf("解绑后删策略应当能通过: %v", err)
		}
	})

	t.Run("默认策略不在列表里要拒绝", func(t *testing.T) {
		if err := ValidateModerationPolicyConfig(policies, "不存在", nil); err == nil {
			t.Fatal("默认策略必须指向列表里真实存在的一条")
		}
	})

	t.Run("空默认策略要拒绝", func(t *testing.T) {
		if err := ValidateModerationPolicyConfig(policies, "", nil); err == nil {
			t.Fatal("空默认策略必须被拒绝：未绑定的分组会静默落到列表第一条")
		}
	})

	t.Run("分组绑定不存在的策略要拒绝", func(t *testing.T) {
		err := ValidateModerationPolicyConfig(policies, "标准",
			map[string]GroupPolicy{"vip": {Policy: "不存在"}})
		if err == nil {
			t.Fatal("绑定不存在的策略必须被拒绝——ResolvePolicy 会静默回退默认策略")
		}
		if !strings.Contains(err.Error(), "vip") {
			t.Fatalf("错误信息要指出是哪个分组: %v", err)
		}
	})

	t.Run("非法分组模式要拒绝", func(t *testing.T) {
		err := ValidateModerationPolicyConfig(policies, "标准",
			map[string]GroupPolicy{"vip": {Mode: "halt"}})
		if err == nil {
			t.Fatal("非法运行模式必须被拒绝")
		}
	})

	t.Run("名字带空格时校验与运行期必须一致", func(t *testing.T) {
		// 校验用 TrimSpace 比对、落库存原文、运行期精确比较——三者不一致时，
		// 「策略名尾部多一个空格」（从文档粘贴就会发生）能让校验通过、保存成功、
		// 界面正常，而判定悄悄落到 Policies[0]。
		//
		// 现在的约定是**落库前统一 trim**（controller 侧 normalize），
		// 所以这里校验带空格的输入仍应通过，且规范化后能被 ResolvePolicy 找到。
		spaced := []ModerationPolicy{{Name: " 标准 ", Strictness: StrictnessStandard}}
		if err := ValidateModerationPolicyConfig(spaced, " 标准 ", nil); err != nil {
			t.Fatalf("带空格的名字（规范化后一致）不该被拒: %v", err)
		}

		// 反面：规范化之后必须真的能被运行期找到
		s := GetModerationSettings()
		origPolicies, origDefault := s.Policies, s.DefaultPolicy
		t.Cleanup(func() { s.Policies, s.DefaultPolicy = origPolicies, origDefault })
		s.Policies = []ModerationPolicy{
			{Name: "别的"},
			{Name: "标准", Strictness: StrictnessStrict},
		}
		s.DefaultPolicy = "标准"
		got := s.ResolvePolicy("some-group")
		if got == nil || got.Name != "标准" {
			t.Fatalf("规范化后的名字应当能被 ResolvePolicy 精确匹配到，得到 %+v", got)
		}
		if got.Strictness != StrictnessStrict {
			t.Fatal("匹配到了错误的策略——这正是静默回退到 Policies[0] 的表现")
		}
	})
}

func TestDialectCoveredCategoriesMatchesModel(t *testing.T) {
	// ShieldGemma 2 只有三条固定策略，是训练时定死的。覆盖集合是配置页
	// 标注「这一类当前生效吗」的依据——标错了会让运营以为涉政图片被拦住了，
	// 而 ShieldGemma 对涉政一点覆盖都没有（AC 自动机扫不了图，没有任何兜底）。
	sg := DialectCoveredCategories(DialectShieldGemma2)
	want := []string{CategorySexual, CategoryIllegal, CategoryViolent}
	if len(sg) != len(want) {
		t.Fatalf("ShieldGemma 覆盖的类别应恰好三个，得到 %d 个", len(sg))
	}
	for _, c := range want {
		if !sg[c] {
			t.Fatalf("类别 %s 应标记为 ShieldGemma 覆盖", c)
		}
	}
	// 反面：涉政绝不能被标成 ShieldGemma 覆盖
	if sg[CategoryPolitical] {
		t.Fatal("涉政类别不能标成 ShieldGemma 覆盖——它对涉政完全没有覆盖")
	}

	// ZSWS 相对 ShieldGemma 的增量正是接它的理由：涉政有覆盖了。
	// 这条钉住的是「换 dialect 真的换了能力边界」，而不只是换了解析器。
	if !DialectCoveredCategories(DialectZSWS)[CategoryPolitical] {
		t.Fatal("ZSWS 覆盖涉政——这是相对 ShieldGemma 的关键增量，标注不能丢")
	}

	// Zhongsen 没有越狱检测：码表里没有对应项。标成覆盖会让运营以为
	// 配了「越狱 → 拒绝」就能拦住提示攻击，而换到这个 dialect 之后那一行是死配置。
	if DialectCoveredCategories(DialectZhongsenText)[CategoryJailbreak] {
		t.Fatal("Zhongsen 的 29 码表里没有越狱类别，不能标成覆盖")
	}
	if !DialectCoveredCategories(DialectQwen3Guard)[CategoryJailbreak] {
		t.Fatal("Qwen3Guard 有 Jailbreak 类别，标注不能丢")
	}

	// 覆盖表里出现 AllCategories 之外的类别，说明两处已经对不上——
	// 配置页按 AllCategories 渲染行，多出来的类别永远不会被显示，
	// 于是一个真的会被判出来的类别在界面上根本不存在。
	all := make(map[string]bool, len(AllCategories))
	for _, c := range AllCategories {
		all[c] = true
	}
	for dialect, covered := range dialectCoveredCategories {
		for c := range covered {
			if !all[c] {
				t.Fatalf("dialect %s 标了一个不在 AllCategories 里的类别 %s", dialect, c)
			}
		}
	}
}

// 新增类别时，存量策略里没有它——此时必须拿到 defaultCategoryActions 的值，
// 而不是「未登记即 block」那条兜底。
//
// 这是升级安全性的关键：cyber 默认 block 会在升级当天开始拦「这段 SQL 注入怎么修」
// 这类 coding 常态提问，advice 默认 block 会拦掉投资与医疗咨询，
// 而配置页上那几行看起来根本没被配过，运营无从判断为什么突然开始误杀。
func TestNewCategoriesFallBackToDefaultsNotBlock(t *testing.T) {
	// 模拟存量策略：只配了接 Zhongsen 之前的九类。
	legacy := &ModerationPolicy{
		Name: "标准",
		Categories: map[string]string{
			CategorySexual:    CategoryActionBlock,
			CategoryIllegal:   CategoryActionBlock,
			CategoryPolitical: CategoryActionBlock,
			CategoryJailbreak: CategoryActionBlock,
			CategoryViolent:   CategoryActionLog,
			CategorySelfHarm:  CategoryActionLog,
			CategoryUnethical: CategoryActionLog,
			CategoryPII:       CategoryActionIgnore,
			CategoryCopyright: CategoryActionIgnore,
		},
	}

	cases := map[string]string{
		CategoryCyber:  CategoryActionLog,
		CategoryAdvice: CategoryActionIgnore,
		CategoryMinor:  CategoryActionBlock,
		CategoryTerror: CategoryActionBlock,
		CategoryVulgar: CategoryActionLog,
	}
	for cat, want := range cases {
		if got := legacy.CategoryAction(cat); got != want {
			t.Fatalf("存量策略下类别 %s 应回落到默认处置 %s，得到 %s", cat, want, got)
		}
	}

	// **反面，而且是更要紧的那一半**：原来那九类缺键时必须仍然 block。
	//
	// 「缺键 = block」是既有契约——前端 updateCategory 只写用户点过的那个键，
	// 它自己的注释就写着「存量策略、手写 JSON 都不要求九类齐全」。拿新增类别的
	// 默认表去兜全部类别，会把存量策略静默放松：violent / self_harm / unethical
	// 从 block 变 log，pii / copyright 从 block 变 ignore。
	partial := &ModerationPolicy{
		Name:       "严格",
		Categories: map[string]string{CategoryPII: CategoryActionLog},
	}
	for _, cat := range []string{
		CategorySexual, CategoryIllegal, CategoryPolitical, CategoryJailbreak,
		CategoryViolent, CategorySelfHarm, CategoryUnethical, CategoryCopyright,
	} {
		if got := partial.CategoryAction(cat); got != CategoryActionBlock {
			t.Fatalf("原九类缺键时必须仍按 block 处置（既有契约），类别 %s 得到 %s——"+
				"这会把「只列放宽项、其余留空靠默认拦」构造出来的严格策略整条掏空", cat, got)
		}
	}
	// 显式配的那个不受影响。
	if got := partial.CategoryAction(CategoryPII); got != CategoryActionLog {
		t.Fatalf("显式配置必须优先，得到 %s", got)
	}

	// 反面：真正未登记的类别（上游报了个我们没见过的）仍然必须 block。
	// 这条兜底不能被 defaultCategoryActions 顺手削掉——丢了就等于把未知风险当安全。
	if got := legacy.CategoryAction(CategoryUnknownUpstream); got != CategoryActionBlock {
		t.Fatalf("未登记类别必须按 block 处置，得到 %s", got)
	}
	if got := legacy.CategoryAction("something-nobody-registered"); got != CategoryActionBlock {
		t.Fatalf("完全陌生的类别必须按 block 处置，得到 %s", got)
	}

	// 显式配置永远优先于默认值。
	legacy.Categories[CategoryCyber] = CategoryActionBlock
	if got := legacy.CategoryAction(CategoryCyber); got != CategoryActionBlock {
		t.Fatalf("显式配置应优先于默认值，得到 %s", got)
	}
}

// 内置「标准」策略必须覆盖 AllCategories 的每一项。
//
// 漏一项的后果不是报错，是那一行在新装站点的配置页上显示为未配置——
// 而它实际按 defaultCategoryActions 在生效，界面和行为对不上。
func TestBuiltinPolicyCoversAllCategories(t *testing.T) {
	builtin := GetModerationSettings().Policies[0]
	for _, c := range AllCategories {
		action, ok := builtin.Categories[c]
		if !ok {
			t.Fatalf("内置策略漏了类别 %s；新增类别时必须同步补进去", c)
		}
		if want := DefaultCategoryAction(c); action != want {
			t.Fatalf("内置策略里 %s 配的是 %s，与默认处置 %s 不一致——"+
				"两处都是「开箱行为」的定义，对不上时无法判断哪个准", c, action, want)
		}
	}
}

// 两个运行模式的默认值都必须是显式的 "off"，不能靠零值。
//
// 零值是 ModerationModeInherit（""），而 configToMap 会把它原样导出成
// moderation.output_mode = ""。前端拿到空串：下拉框选不中「关闭」（显示空白），
// 「产物审核已开启但没有图片节点」那条红色告警的判断也会命中——
// 于是**每一个从没动过这项配置的部署**都会看到一条说自己配错了的红条，
// 而实际上产物审核是关着的。
func TestModerationModeDefaultsAreExplicitOff(t *testing.T) {
	s := GetModerationSettings()

	if s.Mode != ModerationModeOff {
		t.Fatalf("输入侧默认模式应为显式 off，得到 %q", s.Mode)
	}
	if s.OutputMode != ModerationModeOff {
		t.Fatalf("产物侧默认模式应为显式 off 而不是零值 %q——"+
			"零值会以空串导出到前端，让下拉框空白、并误触发配置告警", s.OutputMode)
	}

	// 语义上两者等价（ResolveOutputMode 把 inherit 也解析成 off），
	// 所以这条测试钉的是**导出值的形状**，不是判定行为。
	if got := s.ResolveOutputMode("default"); got != ModerationModeOff {
		t.Fatalf("默认配置下产物审核必须是关的，得到 %q", got)
	}
}
