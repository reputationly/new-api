package quotaunitmigration

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// EnsurePriceUnitReady 是 V2 的启动保护（设计文档 §4 B9）：
//
//	options.QuotaUnitMigrationV2 != done 且 users 表非空 → 整个进程拒绝启动。
//
// 原因是新代码按「1 quota = ¥0.00000001 + 人民币定价」计算，而旧数据里配置还是美元口径
// （倍率 1 = $2/百万、单价是 $/次）、余额也还是旧单位。两边单位不一致，每条请求会按错误
// 倍数扣费：倍率路径多扣约 6.85 倍（100 / 14.6），按次 / 视频 / 工具路径多扣约 13.7 倍
// （10⁸ / 7.3×10⁶）；余额与报表的人民币金额同时错位。这种错误不会自愈也不会报错，
// 只能靠拒绝启动把它暴露出来。
// 空库（全新部署）直接标记为 done，不需要迁移。
//
// 迁移模式（--migrate-quota-unit-v2=...）在 main 里先返回，不会走到这里。
func EnsurePriceUnitReady() error {
	v2 := planV2()
	state, found, err := loadStateFor(v2.stateKey)
	if err != nil {
		return err
	}
	if found && state.State == stateDone {
		return nil
	}

	var users int64
	if err := model.DB.Model(&model.User{}).Limit(1).Count(&users).Error; err != nil {
		return fmt.Errorf("检查 users 表失败: %w", err)
	}
	if users == 0 {
		raw, err := common.Marshal(migrationState{State: stateDone})
		if err != nil {
			return err
		}
		if err := upsertOption(model.DB, v2.stateKey, string(raw)); err != nil {
			return err
		}
		common.SysLog("额度单位迁移 V2：空库，已直接标记 " + v2.stateKey + " = done")
		return nil
	}

	return fmt.Errorf(
		"额度单位迁移 V2 未完成（options.%s 不是 done），而库里已有用户数据。\n"+
			"新代码在旧单位数据上会按错误倍数扣费（倍率路径约 6.85 倍、按次/视频/工具路径约 13.7 倍），\n"+
			"余额与报表的人民币金额也同时错位，因此拒绝启动。请按顺序执行：\n"+
			"  1) 停服并备份：pg_dump 全库（同时记下 SUM(users.quota) 与行数）\n"+
			"  2) 预演并核对报告：docker compose run --rm --no-deps new-api --log-dir /app/logs --%s=dry-run\n"+
			"  3) 落地并保存报告：docker compose run --rm --no-deps new-api --log-dir /app/logs --%s=apply\n"+
			"  4) 启动服务（本检查会看到 state = done 并放行）\n"+
			"若 V1 迁移也未完成，先跑 --%s=apply。\n"+
			"回滚：恢复第 1 步的备份 **并**换回上一版镜像，两者必须同时做。",
		v2.stateKey, v2.flag, v2.flag, planV1().flag)
}
