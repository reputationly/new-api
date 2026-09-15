package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

// 从**真实入口**进：AddChannel / UpdateChannel 走的是 validateChannel →
// ValidateSettings。只测 dto.ValidateProxy 证明不了这条线接上了 ——
// 之前踩过"函数写好了但没人调"的亏。
func TestValidateSettings_RejectsBadProxy(t *testing.T) {
	setting := `{"proxy":"root"}`
	ch := &Channel{Setting: common.GetPointer(setting)}

	err := ch.ValidateSettings()
	if err == nil {
		t.Fatal("带 proxy=root 的渠道保存通过了；请求时才会炸，且报错不含渠道和原值")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("报错没带原值: %v", err)
	}
}

func TestValidateSettings_AllowsGoodProxy(t *testing.T) {
	for _, s := range []string{`{}`, `{"proxy":""}`, `{"proxy":"socks5://127.0.0.1:1080"}`} {
		ch := &Channel{Setting: common.GetPointer(s)}
		if err := ch.ValidateSettings(); err != nil {
			t.Errorf("%s 该通过却被拦: %v", s, err)
		}
	}
	// 完全没设置 setting 的渠道也不能被拦
	if err := (&Channel{}).ValidateSettings(); err != nil {
		t.Errorf("空 setting 被拦: %v", err)
	}
}
