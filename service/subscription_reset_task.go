package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	subscriptionResetTickInterval = 1 * time.Minute
	subscriptionResetBatchSize    = 300
	subscriptionCleanupInterval   = 30 * time.Minute
)

var (
	subscriptionResetOnce    sync.Once
	subscriptionResetRunning atomic.Bool
	subscriptionCleanupLast  atomic.Int64
)

func StartSubscriptionQuotaResetTask() {
	subscriptionResetOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("subscription quota reset task started: tick=%s", subscriptionResetTickInterval))
			ticker := time.NewTicker(subscriptionResetTickInterval)
			defer ticker.Stop()

			runSubscriptionQuotaResetOnce()
			for range ticker.C {
				runSubscriptionQuotaResetOnce()
			}
		})
	})
}

func runSubscriptionQuotaResetOnce() {
	if !subscriptionResetRunning.CompareAndSwap(false, true) {
		return
	}
	defer subscriptionResetRunning.Store(false)

	ctx := context.Background()
	totalReset := 0
	totalExpired := 0
	for {
		n, err := model.ExpireDueSubscriptions(subscriptionResetBatchSize)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("subscription expire task failed: %v", err))
			return
		}
		if n == 0 {
			break
		}
		totalExpired += n
		if n < subscriptionResetBatchSize {
			break
		}
	}
	for {
		n, err := model.ResetDueSubscriptions(subscriptionResetBatchSize)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("subscription quota reset task failed: %v", err))
			return
		}
		if n == 0 {
			break
		}
		totalReset += n
		if n < subscriptionResetBatchSize {
			break
		}
	}
	// 算力点批次的展示状态同步。纯粹是给报表/管理页看的，扣费判定不依赖它——
	// TryConsumeComputePoints 自己按 expires_at 实时判定，这个任务哪怕不跑，
	// 过期批次照样花不出去。放在同一个 tick 里，理由与订阅任务同构：
	// 都是「后台任务只做清理与展示同步，不参与资金正确性」。
	totalLotSynced := 0
	for {
		n, err := model.ExpireDueComputePointLots(subscriptionResetBatchSize)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("compute point lot sync task failed: %v", err))
			break
		}
		if n == 0 {
			break
		}
		totalLotSynced += n
		if n < subscriptionResetBatchSize {
			break
		}
	}
	// 权益次数重置。与算力点批次不同，这个必须跑：次数闸门的归零只有这一条路径，
	// 任务不跑就意味着客户用完本期次数后再也回不来（算力点那边即使任务停摆也只是
	// 展示状态不同步，扣费判定不受影响）。
	totalEntitlementReset := 0
	for {
		n, err := model.ResetDueUserSubscriptionEntitlements(subscriptionResetBatchSize)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("entitlement reset task failed: %v", err))
			break
		}
		if n == 0 {
			break
		}
		totalEntitlementReset += n
		if n < subscriptionResetBatchSize {
			break
		}
	}
	lastCleanup := time.Unix(subscriptionCleanupLast.Load(), 0)
	if time.Since(lastCleanup) >= subscriptionCleanupInterval {
		if _, err := model.CleanupSubscriptionPreConsumeRecords(7 * 24 * 3600); err == nil {
			subscriptionCleanupLast.Store(time.Now().Unix())
		}
	}
	if common.DebugEnabled && (totalReset > 0 || totalExpired > 0 || totalLotSynced > 0 || totalEntitlementReset > 0) {
		logger.LogDebug(ctx, "subscription maintenance: reset_count=%d, expired_count=%d, lot_synced=%d, entitlement_reset=%d",
			totalReset, totalExpired, totalLotSynced, totalEntitlementReset)
	}
}
