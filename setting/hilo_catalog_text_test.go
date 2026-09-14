package setting

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

// **`text` 段必须被解析进来。**
//
// 结构里没有接它的字段时，配置里写了 `text` 会被 Unmarshal 静默丢弃 ——
// 表现是「后台保存成功，但下发的 textModels 还是空的」，而且哪里都不报错。
// 实测就是这么卡了很久。
func TestTextSectionIsNotSilentlyDropped(t *testing.T) {
	var c HiloCatalog
	raw := `{"image":[],"video":[],"audio":[],
	         "text":[{"platform_model":"m","model":{"id":"m","name":"M"}}]}`
	if err := common.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(c.Text) != 1 {
		t.Fatalf("text 段被丢掉了，实得 %d 条", len(c.Text))
	}
	if c.Text[0].Model.ID != "m" {
		t.Errorf("model.id 没读进来: %+v", c.Text[0])
	}
}

// 出厂目录要带上对话模型，否则新装的实例仍然是空选择器。
func TestDefaultCatalogHasTextModels(t *testing.T) {
	c := defaultHiloCatalog()
	if len(c.Text) == 0 {
		t.Fatal("出厂目录里没有对话模型")
	}
	var ids []string
	for _, e := range c.Text {
		ids = append(ids, e.Model.ID)
	}
	if !contains(ids, "qwen3.8-27b") {
		t.Errorf("出厂目录里应当有 qwen3.8-27b（实测三项全过的那个），实得 %v", ids)
	}
}

// **出厂条目的 id 不能带前缀。** 带了的话客户端补完就是两层。
func TestDefaultTextIdsCarryNoPrefix(t *testing.T) {
	for _, e := range defaultHiloTextCatalog() {
		if strings.Contains(e.Model.ID, "/") {
			t.Errorf("出厂条目 %q 带了 provider 前缀", e.Model.ID)
		}
	}
}

// 管理员写了带前缀的 id 要被拒 —— 客户端再补一层就是 `maas/maas/xxx`，
// OpenCode 找不到 provider 而直接 500，报错里完全看不出是前缀的问题。
func TestRejectsPrefixedModelId(t *testing.T) {
	err := validateTextEntries([]HiloTextEntry{
		{PlatformModel: "x", Model: dto.HiloTextModel{ID: "maas/x", Name: "X"}},
	})
	if err == nil {
		t.Fatal("带前缀的 id 没被拒")
	}
	if !strings.Contains(err.Error(), "不要带 provider 前缀") {
		t.Errorf("错误信息没说清原因: %v", err)
	}
}

// 缺 platform_model 就无法判断平台上有没有这个模型 —— 那会让一个
// 根本没部署的模型出现在选择器里，用户选中后发消息才失败。
func TestRejectsMissingPlatformModel(t *testing.T) {
	if err := validateTextEntries([]HiloTextEntry{
		{PlatformModel: "  ", Model: dto.HiloTextModel{ID: "x", Name: "X"}},
	}); err == nil {
		t.Error("缺 platform_model 没被拒")
	}
}

func TestRejectsMissingIdOrName(t *testing.T) {
	if err := validateTextEntries([]HiloTextEntry{
		{PlatformModel: "x", Model: dto.HiloTextModel{ID: "", Name: "X"}},
	}); err == nil {
		t.Error("缺 model.id 没被拒")
	}
	if err := validateTextEntries([]HiloTextEntry{
		{PlatformModel: "x", Model: dto.HiloTextModel{ID: "x", Name: ""}},
	}); err == nil {
		t.Error("缺 model.name 没被拒 —— 选择器里会是个没名字的条目")
	}
}

func TestRejectsDuplicateIds(t *testing.T) {
	err := validateTextEntries([]HiloTextEntry{
		{PlatformModel: "a", Model: dto.HiloTextModel{ID: "x", Name: "X"}},
		{PlatformModel: "b", Model: dto.HiloTextModel{ID: "x", Name: "X2"}},
	})
	if err == nil || !strings.Contains(err.Error(), "重复") {
		t.Errorf("重复 id 没被拒: %v", err)
	}
}

// **校验失败时保持原配置，不是清空。** 管理员手滑写坏一处就让所有客户端的
// 对话选择器变空，这个代价太大。
func TestBadTextSectionKeepsPreviousCatalog(t *testing.T) {
	before := len(GetHiloCatalog().Text)
	err := UpdateHiloCatalogByJsonString(
		`{"text":[{"platform_model":"x","model":{"id":"maas/x","name":"X"}}]}`)
	if err == nil {
		t.Fatal("坏配置没报错")
	}
	if got := len(GetHiloCatalog().Text); got != before {
		t.Errorf("校验失败时目录被改了：%d → %d", before, got)
	}
}

func contains(v []string, want string) bool {
	for _, s := range v {
		if s == want {
			return true
		}
	}
	return false
}
