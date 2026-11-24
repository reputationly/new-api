package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

// 积分批次过期任务（docs/points-expiry-design.md §6）。
//
// 它与订阅维护任务刻意不挂在一起：那边的权益次数重置必须每分钟跑，而积分到期时刻
// 全部落在某天 23:59:59，一天一次就够。
//
// 积分系统总开关关闭时**照跑**（决策 8）：否则关掉开关等于冻结过期，把开关用成了
// 「永久保存积分」的后门。

const (
	// pointsExpiryBatchSize 每批处理的用户数。逐用户一个事务，单用户失败只记日志继续。
	pointsExpiryBatchSize = 300
	// pointsExpiryArchiveKeep 到期/用尽批次的保留期，超过才归档删除。
	pointsExpiryArchiveKeep = 90 * 86400
	// pointsExpiryArchiveBatch 单次归档删除的行数上限。
	pointsExpiryArchiveBatch = 300
)

var pointsExpiryOnce sync.Once

// StartPointsExpiryTask 启动过期任务：仅主节点；启动时先补跑一次，之后每天北京时间 00:05。
func StartPointsExpiryTask() {
	pointsExpiryOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			ctx := context.Background()
			logger.LogInfo(ctx, "points expiry task started: daily at 00:05 Asia/Shanghai")

			// 启动补跑：主节点重启过夜时，昨天的到期批次不该等到今晚才处理。
			runPointsExpiryOnceLogged(ctx, common.GetTimestamp())
			for {
				time.Sleep(nextPointsExpiryDelay(time.Now()))
				runPointsExpiryOnceLogged(ctx, common.GetTimestamp())
			}
		})
	})
}

// nextPointsExpiryDelay 返回距离下一个「北京时间 00:05」的时长。
//
// 不依赖主机时区：无论容器 TZ 是 UTC 还是本地，都按 Asia/Shanghai 的 00:05 触发。
func nextPointsExpiryDelay(now time.Time) time.Duration {
	t := now.In(shanghaiLoc)
	next := time.Date(t.Year(), t.Month(), t.Day(), 0, 5, 0, 0, shanghaiLoc)
	if !next.After(t) {
		next = next.AddDate(0, 0, 1)
	}
	return next.Sub(t)
}

func runPointsExpiryOnceLogged(ctx context.Context, now int64) {
	total, users, err := RunPointsExpiryOnce(now)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("points expiry task failed: %v", err))
		return
	}
	if total > 0 || users > 0 {
		logger.LogInfo(ctx, fmt.Sprintf(
			"points expiry: forfeited %d quota units across %d users", total, users))
	}
}

// RunPointsExpiryOnce 跑一轮过期处理，返回作废总额（quota unit）与涉及的用户数。
//
// 分批列出「有已到期批次」的用户，逐用户开事务：单用户失败只记日志、继续下一个，
// 不让一个脏数据把整轮卡住。若一整批全部失败则跳出——否则同一批用户会被反复重选，
// 变成死循环。
func RunPointsExpiryOnce(now int64) (forfeited int64, users int, err error) {
	if now <= 0 {
		now = common.GetTimestamp()
	}
	ctx := context.Background()

	for {
		ids, qErr := model.UserIDsWithDuePointsLots(now, pointsExpiryBatchSize)
		if qErr != nil {
			return forfeited, users, qErr
		}
		if len(ids) == 0 {
			break
		}

		succeeded := 0
		for _, uid := range ids {
			amount, bySource, eErr := model.ExpireUserPointsLots(uid, now)
			if eErr != nil {
				logger.LogWarn(ctx, fmt.Sprintf("points expiry for user %d failed: %v", uid, eErr))
				continue
			}
			succeeded++
			if amount <= 0 {
				continue
			}
			forfeited += amount
			users++
			logPointsExpiry(ctx, uid, bySource)
		}

		if succeeded == 0 {
			logger.LogWarn(ctx, fmt.Sprintf(
				"points expiry: whole batch of %d users failed, aborting this run", len(ids)))
			break
		}
		if len(ids) < pointsExpiryBatchSize {
			break
		}
	}

	// 归档清理：删掉早已到期/用尽的批次行。只删 expires_at > 0 的，永不过期的
	// 批次（legacy / surplus / 永不失效的管理员发放）一律保留。
	for {
		n, cErr := model.CleanupArchivedPointsLots(now, pointsExpiryArchiveKeep, pointsExpiryArchiveBatch)
		if cErr != nil {
			return forfeited, users, cErr
		}
		if n < pointsExpiryArchiveBatch {
			break
		}
	}
	return forfeited, users, nil
}

// logPointsExpiry 按来源合并成一条使用日志。
//
// 只在作废额 > 0 时写：常用 API 的用户签到积分早就花完了，不必为 0 刷新日志。
// 日志在事务提交后写（LOG_DB 可能是独立库，落不进同一个事务）。
func logPointsExpiry(ctx context.Context, userId int, bySource []model.PointsLotExpiry) {
	parts := make([]string, 0, len(bySource))
	for _, agg := range bySource {
		if agg.Forfeited <= 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %d 积分（%s 到期）",
			model.PointsLotSourceLabel(agg.Source),
			common.QuotaToPoints(int(agg.Forfeited)),
			model.FormatPointsExpiryDate(agg.ExpiresAt)))
	}
	if len(parts) == 0 {
		return
	}
	model.RecordLog(userId, model.LogTypeSystem, "积分过期："+strings.Join(parts, "；"))
	logger.LogDebug(ctx, "points expiry logged for user %d", userId)
}
