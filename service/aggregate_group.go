package service

// 聚合(编排)模型的分组准入判定。
//
// 这个判据有两个调用点，两处必须**同口径**：
//
//	券换入口   middleware/aggregate_expand.go  groupAllowedForAggregate
//	能力目录   service/ovcaps                  Build
//
// 判出不同结果的后果是「目录里有、调不通」（或反过来），而集成方拿到的只有一个
// 400，无法自查自己到底有没有权限 —— 所以候选分组怎么算放在这里只写一份。

// AggregateUsingGroups 判定聚合模型准入时要比对的分组集合。
//
// 令牌显式指定了分组就用它；令牌分组是 "auto" 时展开成用户实际能用的自动分组集合 ——
// **"auto" 本身不参与比对**，因为没有任何聚合模型会把 "auto" 写进 Groups。
//
// expandAuto 由调用方给，是为了保留 middleware 那边的测试替换点
// （middleware/aggregate_expand.go 的 expandAutoGroups 变量）。
func AggregateUsingGroups(usingGroup, userGroup string, expandAuto func(string) []string) []string {
	if usingGroup != "auto" {
		return []string{usingGroup}
	}
	if expandAuto == nil {
		return nil
	}
	return expandAuto(userGroup)
}
