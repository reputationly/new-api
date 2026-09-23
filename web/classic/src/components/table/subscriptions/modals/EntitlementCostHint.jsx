import React, { useEffect, useState } from 'react';
import { Typography } from '@douyinfe/semi-ui';
import { API } from '../../../../helpers';
import {
  formatCNY,
  isWorstCostOverPrice,
  quotaToCNY,
} from '../../../../helpers/planPrice';
import { splitModels } from '../../../../helpers/entitlementOverlap';

const { Text } = Typography;

/**
 * 外采权益的「最坏成本」提示：次数上限 × 最贵模型的单次外采成本。
 *
 * 这是运营配次数时唯一要看的数字——保证最坏情况不亏。计算全在后端做，
 * 口径与记账侧共用一份公式；前端只负责展示与拿套餐售价做对比。
 *
 * 不限次的权益不发请求：它的最坏成本是无穷大，给任何有限数字都是错的。
 */
const EntitlementCostHint = ({ entitlement, priceAmount, plan, t }) => {
  const [estimate, setEstimate] = useState(null);
  const models = splitModels(entitlement.models);
  const limit = Number(entitlement.limit_count) || 0;
  const channels = entitlement.channel_ids || '';
  const modelsKey = models.join(',');
  const resetPeriod = entitlement.reset_period || 'never';
  const planKey = JSON.stringify(plan || {});

  useEffect(() => {
    // 输入一变就先清掉旧结果。否则在 600ms 防抖 + 请求往返这段时间里，卡片上
    // 挂着的是上一套配置算出来的数字——数字本身没错，但它描述的已经不是眼前
    // 这条权益了。权益列表按下标做 key，删除/换序时同一个组件实例会接手别人的
    // 数据，不清的话能看到张冠李戴的成本。
    setEstimate(null);
    if (limit <= 0 || models.length === 0) {
      return;
    }
    // 响应可能乱序返回（慢的那个后到会盖掉新的），用这个标记丢弃过期响应
    let cancelled = false;
    // 运营还在输入模型名时不该每个字符打一次请求
    const timer = setTimeout(() => {
      API.post('/api/subscription/admin/entitlement_cost_preview', {
        models: modelsKey.split(',').filter(Boolean),
        channel_ids: channels ? [channels] : [],
        limit_count: limit,
        reset_period: resetPeriod,
        ...(plan || {}),
      })
        .then((res) => {
          if (cancelled) return;
          setEstimate(res.data?.success ? res.data.data : null);
        })
        .catch(() => {
          if (!cancelled) setEstimate(null);
        });
    }, 600);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [modelsKey, channels, limit, resetPeriod, planKey]);

  if (limit <= 0) {
    return (
      <Text size='small' type='tertiary'>
        {t('不限次：最坏成本无上限，依赖速率限制兜底')}
      </Text>
    );
  }
  if (!estimate) return null;

  const unresolved = (estimate.models || []).filter((m) => !m.resolvable);
  // 与售价比大小的必须是**整个计费周期**的最坏成本：次数上限是每个重置窗口的额度，
  // 月付套餐配每周重置时客户一个周期内能用四五倍的量，只按单窗口比会系统性低估。
  const periodQuota = estimate.worst_period_quota || estimate.worst_total_quota;
  const hasNumber = periodQuota > 0;
  // 统一折成人民币再比：price_amount 是实付人民币，成本是 quota unit。两边
  // 币种不一致会差一个汇率倍数，这个红字的全部意义就是「配亏了要看得见」，
  // 乱响或漏报都等于废掉它。
  const priceCNY = Number(priceAmount) || 0;
  const over = isWorstCostOverPrice(periodQuota, priceCNY);
  const windows = Number(estimate.reset_windows) || 1;

  return (
    <div className='mt-1'>
      {hasNumber && (
        <Text size='small' type={over ? 'danger' : 'secondary'}>
          {t('整个计费周期最坏成本')} ≈ {formatCNY(quotaToCNY(periodQuota))}
          {'　'}
          {windows > 1
            ? t('（{{w}} 个重置窗口 × {{n}} 次 × 最贵的 {{m}}）')
                .replace('{{w}}', String(windows))
                .replace('{{n}}', String(estimate.limit_count))
                .replace('{{m}}', estimate.worst_model)
            : t('（{{n}} 次 × 最贵的 {{m}}）')
                .replace('{{n}}', String(estimate.limit_count))
                .replace('{{m}}', estimate.worst_model)}
          {priceCNY > 0 && `　${t('套餐售价')} ${formatCNY(priceCNY)}`}
        </Text>
      )}
      {estimate.reset_windows_capped && (
        <div>
          <Text size='small' type='warning'>
            {t('重置周期过短，窗口数已按上限截断，实际最坏成本更高')}
          </Text>
        </div>
      )}
      {unresolved.length > 0 && (
        <div>
          <Text size='small' type='tertiary'>
            {t('{{n}} 个模型无法估算：{{reason}}')
              .replace('{{n}}', String(unresolved.length))
              .replace('{{reason}}', unresolved[0].reason)}
          </Text>
        </div>
      )}
    </div>
  );
};

export default EntitlementCostHint;
