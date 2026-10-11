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
// 原因是新代码的定价与计费都按 1 quota = ¥0.00000001 写，旧数据（1 quota = ¥0.000001）上
// 会让计费整体失真：配置里的美元数值被当人民币用、倍率又少乘了 14.6，于是按次实收只有
// 应有值的 1/7.3、倍率路径 1/14.6；同时存量余额会被按新单位重新解释而缩水约 14 倍。
// 这种错误不会自愈也不会报错，只能靠拒绝启动暴露出来。
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
			"新代码在旧单位数据上会让计费整体失真（按次实收约 1/7.3、倍率路径约 1/14.6，\n"+
			"存量余额也会被按新单位重新解释），因此拒绝启动。请按顺序执行：\n"+
			"  1) 停服并备份：pg_dump 全库（同时记下 SUM(users.quota) 与行数）\n"+
			"  2) 预演并核对报告：docker compose run --rm --no-deps new-api --log-dir /app/logs --%s=dry-run\n"+
			"  3) 落地并保存报告：docker compose run --rm --no-deps new-api --log-dir /app/logs --%s=apply\n"+
			"  4) 启动服务（本检查会看到 state = done 并放行）\n"+
			"若 V1 迁移也未完成，先跑 --%s=apply。\n"+
			"回滚：恢复第 1 步的备份 **并**换回上一版镜像，两者必须同时做。",
		v2.stateKey, v2.flag, v2.flag, planV1().flag)
}
