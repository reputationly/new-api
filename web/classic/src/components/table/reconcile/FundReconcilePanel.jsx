import React, { useCallback, useEffect, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  DatePicker,
  Descriptions,
  Popconfirm,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../../helpers';
import { quotaToDisplayAmount, quotaToPoints } from '../../../helpers/quota';

const { Text, Title } = Typography;

// 收入对账面板。设计见 docs/revenue-reconciliation-design.md §七。
//
// 三个维度：入账（收了多少）、消耗（花在哪）、余额（还欠客户多少）。
// 记账铁律：只有 prepay 与 ar_settle 计入营收；赠送与授信开额单独成栏但不计营收。

const KIND_LABEL = {
  prepay: '预付充值',
  gift: '赠送发放',
  credit_grant: '授信开额',
  ar_settle: '信用回款',
  refund: '退款',
  adjust: '差错调整',
  opening: '期初余额',
  closed: '账户注销冲销',
};

const SOURCE_LABEL = {
  online_pay: '在线支付',
  bank_transfer: '对公转账',
  subscription: '套餐订阅',
  admin_cash: '线下手工',
  admin_gift: '管理员赠送',
  admin_credit: '管理员授信',
  admin_adjust: '管理员调整',
  redemption: '兑换码',
  checkin: '签到',
  kyc: '实名奖励',
  invite: '邀请奖励',
  register: '注册礼',
  package_bonus: '套餐赠品',
};

const ACCOUNT_LABEL = {
  cash: '现金',
  points: '赠送积分',
  credit: '信用',
  subscription: '套餐',
};

const fen2yuan = (fen) => (Number(fen || 0) / 100).toFixed(2);
const quota2yuan = (q) => quotaToDisplayAmount(Number(q || 0)).toFixed(2);

export default function FundReconcilePanel() {
  const { t } = useTranslation();
  const now = Math.floor(Date.now() / 1000);

  const [range, setRange] = useState([
    new Date((now - 30 * 24 * 3600) * 1000),
    new Date(now * 1000),
  ]);
  const [loading, setLoading] = useState(false);
  const [summary, setSummary] = useState(null);
  const [consistency, setConsistency] = useState(null);
  const [entries, setEntries] = useState([]);
  const [entryTotal, setEntryTotal] = useState(0);
  const [page, setPage] = useState(1);
  const pageSize = 20;

  const startTs = Math.floor(range?.[0]?.getTime() / 1000) || 0;
  const endTs = Math.floor(range?.[1]?.getTime() / 1000) || 0;

  const loadSummary = useCallback(async () => {
    setLoading(true);
    try {
      const [sRes, cRes] = await Promise.all([
        API.get(
          `/api/reconcile/admin/fund/summary?start=${startTs}&end=${endTs}`,
        ),
        API.get('/api/reconcile/admin/fund/consistency'),
      ]);
      if (sRes.data?.success) setSummary(sRes.data.data);
      else showError(sRes.data?.message);
      if (cRes.data?.success) setConsistency(cRes.data.data);
    } catch (e) {
      showError(e.message);
    }
    setLoading(false);
  }, [startTs, endTs]);

  const loadEntries = useCallback(
    async (p) => {
      try {
        const res = await API.get(
          `/api/reconcile/admin/fund/entries?start=${startTs}&end=${endTs}&p=${p}&page_size=${pageSize}`,
        );
        if (res.data?.success) {
          setEntries(res.data.data?.items || []);
          setEntryTotal(res.data.data?.total || 0);
        }
      } catch (e) {
        showError(e.message);
      }
    },
    [startTs, endTs],
  );

  useEffect(() => {
    if (startTs && endTs) {
      loadSummary();
      setPage(1);
      loadEntries(1);
    }
  }, [startTs, endTs, loadSummary, loadEntries]);

  const initBaseline = async () => {
    try {
      const res = await API.post('/api/reconcile/admin/fund/baseline');
      if (res.data?.success) {
        showSuccess(
          `${t('已写入期初流水')} ${res.data.data?.created || 0} ${t('条')}`,
        );
        loadSummary();
      } else {
        showError(res.data?.message);
      }
    } catch (e) {
      showError(e.message);
    }
  };

  const exportCsv = () => {
    window.open(
      `/api/reconcile/admin/fund/export?start=${startTs}&end=${endTs}`,
      '_blank',
    );
  };

  // 入账按来源归集，用于「真实入账」卡片的下钻展示
  const inflowBySource = {};
  (summary?.inflow || []).forEach((r) => {
    if (r.kind === 'prepay' || r.kind === 'ar_settle') {
      inflowBySource[r.source] = (inflowBySource[r.source] || 0) + r.cash_total;
    }
  });

  const entryColumns = [
    {
      title: t('时间'),
      dataIndex: 'created_at',
      render: (v) => new Date(v * 1000).toLocaleString(),
    },
    { title: t('用户'), dataIndex: 'user_id' },
    {
      title: t('账户'),
      dataIndex: 'account',
      render: (v) => t(ACCOUNT_LABEL[v] || v),
    },
    {
      title: t('性质'),
      dataIndex: 'kind',
      render: (v) => {
        // 计营收的两种用主色，其余用中性色——一眼看出哪些是真钱
        const isRevenue = v === 'prepay' || v === 'ar_settle';
        return (
          <Tag color={isRevenue ? 'green' : 'grey'} shape='circle' size='small'>
            {t(KIND_LABEL[v] || v)}
          </Tag>
        );
      },
    },
    {
      title: t('来源'),
      dataIndex: 'source',
      render: (v) => t(SOURCE_LABEL[v] || v),
    },
    {
      title: t('额度变动'),
      dataIndex: 'quota_delta',
      render: (v, r) =>
        r.account === 'points'
          ? `${quotaToPoints(v)} ${t('积分')}`
          : `¥${quota2yuan(v)}`,
    },
    {
      title: t('实收'),
      dataIndex: 'cash_fen',
      render: (v) =>
        Number(v) > 0 ? (
          <Text strong>¥{fen2yuan(v)}</Text>
        ) : (
          <Text type='tertiary'>—</Text>
        ),
    },
    { title: t('操作人'), dataIndex: 'operator_id' },
    {
      title: t('单号'),
      dataIndex: 'ref_id',
      render: (v, r) => (
        <Text type='tertiary' size='small'>
          {r.ref_type}/{v}
        </Text>
      ),
    },
    { title: t('备注'), dataIndex: 'remark' },
  ];

  return (
    <Spin spinning={loading}>
      <div className='flex flex-col gap-3'>
        <Space wrap>
          <DatePicker
            type='dateTimeRange'
            value={range}
            onChange={(v) => setRange(v)}
            style={{ width: 380 }}
          />
          <Button onClick={loadSummary}>{t('刷新')}</Button>
          <Button onClick={exportCsv}>{t('导出 CSV')}</Button>
          {consistency && !consistency.has_baseline && (
            <Popconfirm
              title={t('初始化对账基线')}
              content={t(
                '把当前所有用户余额记为期初流水，作为自洽校验的起点。只需执行一次，重复执行不会重复记账。',
              )}
              onConfirm={initBaseline}
            >
              <Button theme='solid'>{t('初始化对账基线')}</Button>
            </Popconfirm>
          )}
        </Space>

        {/* 自洽校验：不平几乎不是算错，而是有代码绕过流水表改了余额 */}
        {consistency && !consistency.has_baseline && (
          <Banner
            type='warning'
            closeIcon={null}
            description={t(
              '尚未初始化对账基线。流水表是后加的，历史余额没有对应流水，此时无法做自洽校验。',
            )}
          />
        )}
        {consistency?.has_baseline && (
          <Banner
            type={consistency.all_ok ? 'success' : 'danger'}
            closeIcon={null}
            description={
              <Space wrap>
                {consistency.items.map((it) => (
                  <Text key={it.name}>
                    {it.ok ? '✅' : '❌'} {t(it.name)}
                    {!it.ok && (
                      <Text type='danger'>
                        {' '}
                        {t('差额')} ¥{quota2yuan(it.diff)}（{it.detail}）
                      </Text>
                    )}
                  </Text>
                ))}
                {!consistency.all_ok && (
                  <Text type='danger' strong>
                    {t('账不平通常意味着有代码绕过流水表直接改了余额')}
                  </Text>
                )}
              </Space>
            }
          />
        )}

        <div className='grid grid-cols-1 md:grid-cols-3 gap-3'>
          <Card title={t('真实入账')} bordered>
            <Title heading={3}>
              ¥{fen2yuan(summary?.revenue?.cash_fen_total)}
            </Title>
            <Descriptions
              align='left'
              size='small'
              data={Object.entries(inflowBySource).map(([k, v]) => ({
                key: t(SOURCE_LABEL[k] || k),
                value: `¥${fen2yuan(v)}`,
              }))}
            />
            <div className='mt-2'>
              <Text type='tertiary' size='small'>
                {t('不计营收')}：{t('赠送')} ¥
                {quota2yuan(summary?.revenue?.gift_quota)} · {t('授信开额')} ¥
                {quota2yuan(summary?.revenue?.credit_grant)}
              </Text>
            </div>
          </Card>

          <Card title={t('消耗')} bordered>
            <Descriptions
              align='left'
              size='small'
              data={[
                {
                  key: t('现金消耗'),
                  value: `¥${quota2yuan(summary?.consume?.cash_consumed)}`,
                },
                {
                  key: t('赠送消耗'),
                  value: `${quotaToPoints(summary?.consume?.points_consumed)} ${t('积分')}`,
                },
                {
                  key: t('消费总额'),
                  value: `¥${quota2yuan(summary?.consume?.total_quota)}`,
                },
              ]}
            />
          </Card>

          <Card title={t('余额（负债）')} bordered>
            <Descriptions
              align='left'
              size='small'
              data={[
                {
                  key: t('现金余额'),
                  value: `¥${quota2yuan(summary?.balance?.cash)}`,
                },
                {
                  key: t('赠送积分'),
                  value: `${quotaToPoints(summary?.balance?.points)} ${t('积分')}`,
                },
                {
                  key: t('应收账款'),
                  value: `¥${quota2yuan(summary?.balance?.credit_used)}`,
                },
                {
                  key: t('授信敞口'),
                  value: `¥${quota2yuan(summary?.balance?.credit_exposed)}`,
                },
              ]}
            />
          </Card>
        </div>

        {/* 先判有值再比较：Number(undefined) 是 NaN，而 NaN !== 0 恒真，
            不加这道判断首屏和加载失败时都会弹出一条「调整 ¥0.00」的假告警 */}
        {!!summary?.revenue?.adjust_quota &&
          Number(summary.revenue.adjust_quota) !== 0 && (
            <Banner
              type='warning'
              closeIcon={null}
              description={`${t('本期存在性质不明的调整')} ¥${quota2yuan(summary?.revenue?.adjust_quota)}${t('，它们来自旧的「调整额度」入口，不计入营收，建议改用「资金操作」的四个动作')}`}
            />
          )}

        <Card title={t('流水明细')} bordered>
          <Table
            columns={entryColumns}
            dataSource={entries}
            pagination={{
              currentPage: page,
              pageSize,
              total: entryTotal,
              onPageChange: (p) => {
                setPage(p);
                loadEntries(p);
              },
            }}
            size='small'
          />
        </Card>
      </div>
    </Spin>
  );
}
