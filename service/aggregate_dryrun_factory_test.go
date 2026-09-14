package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
)

// 出厂配置必须**自己能过干跑校验**。
//
// 校验页报错而配置其实能跑，会让运营去"修"一个没坏的东西；反过来更糟：
// 校验放过一个会静默降级的配置。两边的判据必须和运行时一致。
func TestFactoryConfigPassesDryRun(t *testing.T) {
	items, err := common.ParseAggregateModelList(common.DefaultAggregateModelConfig)
	if err != nil {
		t.Fatal(err)
	}
	peers := map[string]int{}
	for _, m := range items {
		peers[m.Name]++
	}
	for _, m := range items {
		res := DryRunAggregateModel(m, peers)
		for _, c := range res.Checks {
			if c.Level == AggregateCheckError {
				t.Errorf("出厂配置 %s 在干跑校验里报错：%s", m.Name, c.Message)
			}
		}
	}
}
