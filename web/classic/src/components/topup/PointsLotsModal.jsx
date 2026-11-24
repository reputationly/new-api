import React, { useCallback, useEffect, useState } from 'react';
import {
  Banner,
  Modal,
  TabPane,
  Tabs,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError } from '../../helpers';
import { quotaToPoints } from '../../helpers/quota';
import { formatPointsExpiryFullDate } from '../../helpers/pointsExpiry';

const { Text } = Typography;

// 批次来源 → 展示名。刻意与对账面板的 SOURCE_LABEL 分开：那边是流水来源，
// 这边还多了 legacy / surplus 两个只存在于批次表里的来源。
export const LOT_SOURCE_LABEL = {
  legacy: '存量积分',
  surplus: '余额调整',
  register: '注册赠送',
  checkin: '签到',
  kyc: '实名认证',
  invite: '邀请奖励',
  redemption: '兑换码',
  package_bonus: '套餐赠品',
  admin_gift: '管理员赠送',
  admin_adjust: '管理员调整',
};

const PAGE_SIZE = 10;

function ulid(prefix, idx) {
  return `${prefix}-${idx}`;
}

/**
 * PointsLotsModal 积分批次明细。
 *
 * 两个 Tab：可用（剩余 > 0，按到期升序）与历史（已过期 / 已用完）。
 * 金额字段接口给的是 quota unit，这里一律换算成「积分数」展示，与页面其它地方一致。
 */
export default function PointsLotsModal({ visible, onClose }) {
  const { t } = useTranslation();
  const [tab, setTab] = useState('active');
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState({ active: 1, history: 1 });
  const [cache, setCache] = useState({
    active: { items: [], total: 0 },
    history: { items: [], total: 0 },
  });

  const load = useCallback(
    async (status, p) => {
      setLoading(true);
      try {
        const res = await API.get(
          `/api/user/points/lots?status=${status}&p=${p}&page_size=${PAGE_SIZE}`,
        );
        if (res.data?.success) {
          const data = res.data.data || {};
          setCache((prev) => ({
            ...prev,
            [status]: {
              items: Array.isArray(data.items) ? data.items : [],
              total: Number(data.total) || 0,
            },
          }));
        } else {
          showError(res.data?.message);
        }
      } catch {
        showError(t('加载积分明细失败'));
      }
      setLoading(false);
    },
    [t],
  );

  useEffect(() => {
    if (!visible) return;
    setTab('active');
    setPage({ active: 1, history: 1 });
    load('active', 1);
    load('history', 1);
  }, [visible, load]);

  const renderList = (status) => {
    const { items, total } = cache[status];
    const p = page[status];
    const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

    if (!items.length) {
      return (
        <Text type='tertiary' size='small'>
          {status === 'active' ? t('暂无可用积分') : t('暂无历史记录')}
        </Text>
      );
    }

    return (
      <div className='flex flex-col gap-2'>
        {items.map((it, idx) => (
          <div
            key={ulid(status, `${it.id}-${idx}`)}
            className='flex flex-wrap items-center justify-between gap-2 border-b pb-2'
            style={{ borderColor: 'var(--semi-color-fill-0)' }}
          >
            <div className='flex items-center gap-2'>
              <Tag size='small'>
                {t(LOT_SOURCE_LABEL[it.source] || it.source)}
              </Tag>
              <Text size='small'>
                {quotaToPoints(Number(it.remaining_quota) || 0)} {t('积分')}
              </Text>
              <Text type='tertiary' size='small'>
                （{t('发放')} {quotaToPoints(Number(it.amount_quota) || 0)}）
              </Text>
            </div>
            <div className='flex items-center gap-2'>
              <Text type='tertiary' size='small'>
                {formatPointsExpiryFullDate(it.expires_at) || t('永不过期')}
              </Text>
              {it.status === 'expired' && (
                <Tag size='small' color='red'>
                  {t('已过期')}
                </Tag>
              )}
              {it.status === 'exhausted' && (
                <Tag size='small' color='grey'>
                  {t('已用完')}
                </Tag>
              )}
            </div>
          </div>
        ))}
        {totalPages > 1 && (
          <div className='flex items-center justify-end gap-2'>
            <Text type='tertiary' size='small'>
              {p} / {totalPages}
            </Text>
            <a
              role='button'
              onClick={() => {
                if (p <= 1) return;
                setPage((prev) => ({ ...prev, [status]: p - 1 }));
                load(status, p - 1);
              }}
              style={{ cursor: p <= 1 ? 'not-allowed' : 'pointer' }}
            >
              {t('上一页')}
            </a>
            <a
              role='button'
              onClick={() => {
                if (p >= totalPages) return;
                setPage((prev) => ({ ...prev, [status]: p + 1 }));
                load(status, p + 1);
              }}
              style={{ cursor: p >= totalPages ? 'not-allowed' : 'pointer' }}
            >
              {t('下一页')}
            </a>
          </div>
        )}
      </div>
    );
  };

  return (
    <Modal
      centered
      visible={visible}
      onCancel={onClose}
      footer={null}
      title={t('积分明细')}
      style={{ maxWidth: 560 }}
    >
      {/* 规则说明放在最上面：用户在查明细时最需要知道「为什么会少」 */}
      <Banner
        type='info'
        closeIcon={null}
        style={{ marginBottom: 12 }}
        description={t(
          '积分按获得批次计算有效期，优先抵扣最早到期的积分；到期未使用部分自动作废。',
        )}
      />
      <Tabs type='line' activeKey={tab} onChange={setTab}>
        <TabPane tab={t('可用')} itemKey='active'>
          {renderList('active')}
        </TabPane>
        <TabPane tab={t('历史')} itemKey='history'>
          {renderList('history')}
        </TabPane>
      </Tabs>
      {loading && (
        <Text type='tertiary' size='small'>
          {t('加载中…')}
        </Text>
      )}
    </Modal>
  );
}
