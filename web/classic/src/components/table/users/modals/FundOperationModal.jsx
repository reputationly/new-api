import React, { useEffect, useState } from 'react';
import {
  Banner,
  Button,
  Divider,
  Form,
  Modal,
  Radio,
  RadioGroup,
  Space,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../../../helpers';
import {
  displayAmountToQuota,
  quotaToDisplayAmount,
  quotaToPoints,
} from '../../../../helpers/quota';

const { Text } = Typography;

// 管理员资金操作 —— 四个语义明确的动作，取代原先含糊的「改额度」。
//
// 每个动作都必须说明这笔钱的性质，后端据此落 fund_entries 流水。
// 记账铁律：只有 prepay 与 ar_settle 计入营收（CashFen > 0）。
//
// 刻意不提供「覆盖余额」：把余额从 A 改成 B，差额是收了钱还是送的事后无法判定，
// 是账本上唯一的黑洞。确需纠错请用旧的「调整额度」，那条路径会记为性质不明的
// adjust 流水，在报表里单列做异常监控。
const OPS = [
  { value: 'prepay', label: '真实转账入账' },
  { value: 'gift', label: '赠送' },
  { value: 'credit_grant', label: '调整授信额度' },
  { value: 'ar_settle', label: '信用回款核销' },
];

// 赠送事由固定枚举而非自由文本：对账时要按事由归集市场成本，自由文本归不了类。
const GIFT_REASONS = ['POC试用', '售后赔付', 'BD谈单', '活动奖励', '其他'];

const yuanToFen = (yuan) => Math.round((Number(yuan) || 0) * 100);

export default function FundOperationModal(props) {
  const { t } = useTranslation();
  const { visible, userId, username, onClose, refresh } = props;

  const [op, setOp] = useState('prepay');
  const [loading, setLoading] = useState(false);
  const [state, setState] = useState({
    quota: 0,
    points_balance: 0,
    credit_limit: 0,
    credit_used: 0,
  });
  // 各动作独立持有输入，切换动作时不互相污染（金额语义完全不同）
  const [form, setForm] = useState({});

  const loadState = async () => {
    if (!userId) return;
    const res = await API.get(`/api/user/${userId}`);
    if (res.data?.success) {
      const d = res.data.data;
      setState({
        quota: d.quota || 0,
        points_balance: d.points_balance || 0,
        credit_limit: d.credit_limit || 0,
        credit_used: d.credit_used || 0,
      });
    }
  };

  useEffect(() => {
    if (visible) {
      setOp('prepay');
      setForm({});
      loadState();
    }
  }, [visible, userId]);

  const setField = (k) => (v) => setForm((f) => ({ ...f, [k]: v }));

  const creditAvailable = state.credit_limit - state.credit_used;
  const hasDebt = state.credit_used > 0;

  const submit = async () => {
    const payload = { id: userId, action: 'fund_op', op };

    switch (op) {
      case 'prepay': {
        const received = Number(form.received) || 0;
        const credited = Number(form.credited ?? form.received) || 0;
        if (received <= 0) return showError(t('请填写实收金额'));
        if (credited <= 0) return showError(t('请填写到账额度'));
        if (!form.ref) return showError(t('请填写凭证号或合同号'));
        payload.value = displayAmountToQuota(credited);
        payload.cash_fen = yuanToFen(received);
        break;
      }
      case 'gift': {
        const amount = Number(form.amount) || 0;
        if (amount <= 0) return showError(t('请填写赠送金额'));
        if (!form.reason) return showError(t('请选择赠送事由'));
        payload.value = displayAmountToQuota(amount);
        payload.cash_fen = 0;
        break;
      }
      case 'credit_grant': {
        const limit = Number(form.limit);
        if (!(limit >= 0)) return showError(t('请填写授信上限'));
        if (!form.ref) return showError(t('请填写授信协议号'));
        payload.value = displayAmountToQuota(limit);
        payload.cash_fen = 0;
        break;
      }
      case 'ar_settle': {
        const received = Number(form.received) || 0;
        if (received <= 0) return showError(t('请填写回款金额'));
        if (!form.ref) return showError(t('请填写回款凭证'));
        payload.value = displayAmountToQuota(received);
        payload.cash_fen = yuanToFen(received);
        break;
      }
      default:
        return;
    }
    payload.ref = form.ref || '';
    payload.remark =
      op === 'gift'
        ? [form.reason, form.remark].filter(Boolean).join(' - ')
        : form.remark || '';

    // 赠送不计营收，选错一次账就脏一笔——这两个选项在界面上相邻，值得多一次确认
    if (op === 'gift') {
      const ok = await new Promise((resolve) => {
        Modal.confirm({
          title: t('确认是赠送而非收款？'),
          content: t(
            '此操作不计入营收，将记为市场成本。若客户实际付了钱，请改用「真实转账入账」。',
          ),
          onOk: () => resolve(true),
          onCancel: () => resolve(false),
        });
      });
      if (!ok) return;
    }

    setLoading(true);
    try {
      const res = await API.post('/api/user/manage', payload);
      if (res.data?.success) {
        showSuccess(t('操作成功'));
        // 刷新状态条但不关闭弹窗：运营常需连做两步（先核销回款、再调高授信）
        const d = res.data.data;
        if (d) setState((s) => ({ ...s, ...d }));
        else await loadState();
        setForm({});
        refresh && refresh();
      } else {
        showError(res.data?.message || t('操作失败'));
      }
    } catch (e) {
      showError(e.message);
    }
    setLoading(false);
  };

  const renderForm = () => {
    switch (op) {
      case 'prepay':
        return (
          <>
            <Form.InputNumber
              field='received'
              label={t('实收金额（元）')}
              placeholder={t('银行/微信/支付宝实际收到的金额')}
              min={0}
              precision={2}
              value={form.received}
              onChange={setField('received')}
              style={{ width: '100%' }}
            />
            <Form.InputNumber
              field='credited'
              label={t('到账额度（元）')}
              placeholder={t('留空则与实收金额相同')}
              min={0}
              precision={2}
              value={form.credited ?? form.received}
              onChange={setField('credited')}
              style={{ width: '100%' }}
              extraText={t(
                '与实收不同即为折扣：实收计入营收，到账额度是给客户的服务额度',
              )}
            />
            <Form.Input
              field='ref'
              label={t('凭证号 / 合同号')}
              placeholder={t('如 WX20260921143022')}
              value={form.ref}
              onChange={setField('ref')}
            />
          </>
        );
      case 'gift':
        return (
          <>
            <Form.InputNumber
              field='amount'
              label={t('赠送金额（元）')}
              min={0}
              precision={2}
              value={form.amount}
              onChange={setField('amount')}
              style={{ width: '100%' }}
              extraText={
                form.amount > 0
                  ? `${t('折合')} ${quotaToPoints(displayAmountToQuota(Number(form.amount)))} ${t('积分')}`
                  : t('赠送进积分池，不进现金池，不计入营收')
              }
            />
            <Form.Select
              field='reason'
              label={t('赠送事由')}
              placeholder={t('请选择')}
              optionList={GIFT_REASONS.map((r) => ({
                label: t(r),
                value: r,
              }))}
              value={form.reason}
              onChange={setField('reason')}
              style={{ width: '100%' }}
            />
          </>
        );
      case 'credit_grant':
        return (
          <>
            <Form.InputNumber
              field='limit'
              label={t('授信上限（元）')}
              min={0}
              precision={2}
              value={form.limit}
              onChange={setField('limit')}
              style={{ width: '100%' }}
              extraText={`${t('当前已用未结')} ${quotaToDisplayAmount(state.credit_used).toFixed(2)} ${t('元')}，${t('上限不得低于该值')}`}
            />
            <Form.Input
              field='ref'
              label={t('授信协议号')}
              placeholder={t('如 HT-2026-0921-007')}
              value={form.ref}
              onChange={setField('ref')}
            />
          </>
        );
      case 'ar_settle':
        return (
          <>
            {!hasDebt && (
              <Banner
                type='info'
                description={t('该用户无未结账款，无需核销')}
                closeIcon={null}
              />
            )}
            <Form.InputNumber
              field='received'
              label={t('回款金额（元）')}
              min={0}
              precision={2}
              value={form.received}
              onChange={setField('received')}
              disabled={!hasDebt}
              style={{ width: '100%' }}
              extraText={`${t('已用未结')} ${quotaToDisplayAmount(state.credit_used).toFixed(2)} ${t('元')}`}
            />
            <Form.Input
              field='ref'
              label={t('回款凭证')}
              placeholder={t('如 BANK-20260930-88')}
              value={form.ref}
              onChange={setField('ref')}
              disabled={!hasDebt}
            />
          </>
        );
      default:
        return null;
    }
  };

  return (
    <Modal
      title={`${t('资金操作')} · ${username || ''}`}
      visible={visible}
      onCancel={onClose}
      footer={
        <Space>
          <Button onClick={onClose}>{t('关闭')}</Button>
          <Button
            theme='solid'
            loading={loading}
            onClick={submit}
            disabled={op === 'ar_settle' && !hasDebt}
          >
            {t('确认')}
          </Button>
        </Space>
      }
      width={560}
    >
      <div className='mb-2'>
        <Space spacing={16} wrap>
          <Text type='tertiary'>
            {t('现金余额')}{' '}
            <Text strong>¥{quotaToDisplayAmount(state.quota).toFixed(2)}</Text>
          </Text>
          <Text type='tertiary'>
            {t('积分')}{' '}
            <Text strong>{quotaToPoints(state.points_balance)}</Text>
          </Text>
          <Text type='tertiary'>
            {t('信用')}{' '}
            <Text strong>
              ¥{quotaToDisplayAmount(state.credit_used).toFixed(2)} / ¥
              {quotaToDisplayAmount(state.credit_limit).toFixed(2)}
            </Text>
            {creditAvailable > 0 && state.credit_limit > 0 && (
              <Text type='tertiary'>
                {' '}
                （{t('可用')} ¥
                {quotaToDisplayAmount(creditAvailable).toFixed(2)}）
              </Text>
            )}
            {/* 结算时服务已交付，欠款可以超过上限（下一次请求被拒）——标出来，
                否则管理员只看到「已用 > 上限」而不知道是怎么回事 */}
            {creditAvailable < 0 && state.credit_limit > 0 && (
              <Text type='warning'>
                {' '}
                （{t('已超出')} ¥
                {quotaToDisplayAmount(-creditAvailable).toFixed(2)}，
                {t('新请求会被拒绝')}）
              </Text>
            )}
          </Text>
        </Space>
      </div>
      <Divider margin='12px' />
      <RadioGroup
        type='button'
        value={op}
        onChange={(e) => {
          setOp(e.target.value);
          setForm({});
        }}
        className='mb-3'
      >
        {OPS.map((o) => (
          <Radio key={o.value} value={o.value}>
            {t(o.label)}
          </Radio>
        ))}
      </RadioGroup>
      <Form key={op} labelPosition='top'>
        {renderForm()}
        <Form.TextArea
          field='remark'
          label={t('备注')}
          autosize
          rows={1}
          value={form.remark}
          onChange={setField('remark')}
        />
      </Form>
    </Modal>
  );
}
