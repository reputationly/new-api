import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Empty,
  Form,
  Modal,
  Popconfirm,
  Space,
  Spin,
  Switch,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconDelete, IconPlus } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';

import { API, showError, showSuccess } from '../../helpers';
import CardTable from '../../components/common/ui/CardTable';

const { Text } = Typography;

/**
 * 充值套餐管理。设计见 docs/user-tier-pricing-and-topup-package-design.md §6。
 *
 * 套餐**只送积分**：1:1 到账，不送额度、不附带档位折扣。原设计是三重让利，
 * 被否决的原因是第三重（永久档位折扣）取决于用户未来消费多少，运营在配置界面上
 * 根本算不出来——到账 ¥1250 按 7 折能买 ¥1786 的服务，成本率高于 56% 即净亏，
 * 而界面上只显示「送 20% + 5000 积分」。
 *
 * 只送积分之后让利可精确计算且封顶在电费：积分只能用于自建模型（积分白名单）。
 */

const emptyForm = {
  title: '',
  subtitle: '',
  price_amount: 100,
  grant_points: 0,
  max_purchase_per_user: 0,
  sort_order: 0,
  enabled: true,
};

export default function TopupPackagePage() {
  const { t } = useTranslation();
  const [packages, setPackages] = useState([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState(null); // null = 弹窗关闭
  const [formValues, setFormValues] = useState(emptyForm);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await API.get('/api/topup_package/');
      if (res.data?.success) {
        setPackages(res.data.data || []);
      } else {
        showError(res.data?.message);
      }
    } catch (e) {
      showError(t('加载充值套餐失败'));
    }
    setLoading(false);
  }, [t]);

  useEffect(() => {
    load();
  }, [load]);

  const openCreate = () => {
    setFormValues({ ...emptyForm });
    setEditing({ id: 0 });
  };

  const openEdit = (pkg) => {
    setFormValues({ ...pkg });
    setEditing(pkg);
  };

  const save = async () => {
    const payload = {
      ...formValues,
      price_amount: Number(formValues.price_amount) || 0,
      grant_points: Number(formValues.grant_points) || 0,
      max_purchase_per_user: Number(formValues.max_purchase_per_user) || 0,
      sort_order: Number(formValues.sort_order) || 0,
    };
    if (!payload.title?.trim()) {
      showError(t('套餐名称不能为空'));
      return;
    }
    if (payload.price_amount < 0.01) {
      showError(t('售价必须大于 0'));
      return;
    }
    setSaving(true);
    try {
      const isEdit = editing?.id > 0;
      if (isEdit) payload.id = editing.id;
      const res = isEdit
        ? await API.put('/api/topup_package/', payload)
        : await API.post('/api/topup_package/', payload);
      if (res.data?.success) {
        showSuccess(isEdit ? t('套餐已更新') : t('套餐已创建'));
        setEditing(null);
        await load();
      } else {
        showError(res.data?.message);
      }
    } catch (e) {
      showError(t('保存失败'));
    }
    setSaving(false);
  };

  const remove = async (id) => {
    try {
      const res = await API.delete(`/api/topup_package/${id}`);
      if (res.data?.success) {
        showSuccess(t('套餐已删除'));
        await load();
      } else {
        showError(res.data?.message);
      }
    } catch (e) {
      showError(t('删除失败'));
    }
  };

  const columns = useMemo(
    () => [
      {
        title: t('套餐'),
        dataIndex: 'title',
        render: (text, record) => (
          <div>
            <Text strong>{text}</Text>
            {record.subtitle && (
              <Text type='tertiary' size='small' className='block'>
                {record.subtitle}
              </Text>
            )}
          </div>
        ),
      },
      {
        title: t('售价'),
        dataIndex: 'price_amount',
        width: 120,
        render: (v) => <Text>¥{Number(v).toFixed(2)}</Text>,
      },
      {
        title: t('赠送积分'),
        dataIndex: 'grant_points',
        width: 120,
        render: (v) =>
          v > 0 ? <Tag color='green'>{v}</Tag> : <Text type='tertiary'>—</Text>,
      },
      {
        title: t('限购'),
        dataIndex: 'max_purchase_per_user',
        width: 100,
        render: (v) =>
          v > 0 ? (
            <Text>
              {v} {t('次')}
            </Text>
          ) : (
            <Text type='tertiary'>{t('不限')}</Text>
          ),
      },
      {
        title: t('状态'),
        dataIndex: 'enabled',
        width: 100,
        render: (v) =>
          v ? (
            <Tag color='blue'>{t('已上架')}</Tag>
          ) : (
            <Tag color='grey'>{t('已下架')}</Tag>
          ),
      },
      {
        title: t('操作'),
        key: 'ops',
        width: 140,
        render: (_, record) => (
          <Space>
            <Button size='small' onClick={() => openEdit(record)}>
              {t('编辑')}
            </Button>
            <Popconfirm
              title={t('确定删除该套餐？')}
              content={t(
                '已购买的订单不受影响，但未支付的订单到账时将不再赠送积分',
              )}
              onConfirm={() => remove(record.id)}
            >
              <Button size='small' type='danger' icon={<IconDelete />} />
            </Popconfirm>
          </Space>
        ),
      },
    ],
    [t],
  );

  const setField = (key, value) =>
    setFormValues((prev) => ({ ...prev, [key]: value }));

  return (
    <div className='mt-[60px] px-2'>
      <Card className='!rounded-2xl'>
        <div className='flex items-center justify-between mb-3'>
          <div>
            <Text strong style={{ fontSize: 18 }}>
              {t('充值套餐')}
            </Text>
            <Text type='tertiary' size='small' className='block'>
              {t(
                '套餐 1:1 到账并额外赠送积分。售价不受「充值金额折扣」与「充值分组倍率」影响，所见即所得。',
              )}
            </Text>
          </div>
          <Button theme='solid' icon={<IconPlus />} onClick={openCreate}>
            {t('新建套餐')}
          </Button>
        </div>

        <Banner
          type='info'
          closeIcon={null}
          className='mb-3'
          description={
            <div className='text-xs leading-6'>
              <div>
                {t(
                  '赠送的积分只能用于「积分白名单」里的模型。请确保白名单里没有外采渠道的模型——否则送出去的不是电费而是供应商账单，成本会放大数倍。',
                )}
              </div>
              <div>
                {t(
                  '套餐购买目前只支持自研支付宝 / 微信直连；其他支付方式不会出现在套餐购买入口。',
                )}
              </div>
            </div>
          }
        />

        <Spin spinning={loading}>
          {packages.length === 0 && !loading ? (
            <Empty description={t('还没有配置任何充值套餐')} />
          ) : (
            <CardTable
              columns={columns}
              dataSource={packages}
              rowKey='id'
              pagination={false}
              size='small'
            />
          )}
        </Spin>
      </Card>

      <Modal
        title={editing?.id > 0 ? t('编辑套餐') : t('新建套餐')}
        visible={editing !== null}
        onCancel={() => setEditing(null)}
        onOk={save}
        confirmLoading={saving}
        width={520}
      >
        <Form labelPosition='top'>
          <Form.Input
            field='title'
            label={t('套餐名称')}
            value={formValues.title}
            onChange={(v) => setField('title', v)}
            placeholder={t('如：入门包')}
          />
          <Form.Input
            field='subtitle'
            label={t('副标题')}
            value={formValues.subtitle}
            onChange={(v) => setField('subtitle', v)}
            placeholder={t('可选，展示在套餐名下方')}
          />
          <Form.InputNumber
            field='price_amount'
            label={t('售价（元）')}
            min={0.01}
            step={10}
            precision={2}
            value={formValues.price_amount}
            onChange={(v) => setField('price_amount', v)}
            extraText={t('到账额度与售价 1:1')}
            style={{ width: '100%' }}
          />
          <Form.InputNumber
            field='grant_points'
            label={t('赠送积分')}
            min={0}
            step={100}
            value={formValues.grant_points}
            onChange={(v) => setField('grant_points', v)}
            extraText={t('额外赠送，只能用于积分白名单里的模型')}
            style={{ width: '100%' }}
          />
          <Form.InputNumber
            field='max_purchase_per_user'
            label={t('每人限购次数')}
            min={0}
            value={formValues.max_purchase_per_user}
            onChange={(v) => setField('max_purchase_per_user', v)}
            extraText={t('0 = 不限。只统计已支付成功的订单')}
            style={{ width: '100%' }}
          />
          <Form.InputNumber
            field='sort_order'
            label={t('排序')}
            value={formValues.sort_order}
            onChange={(v) => setField('sort_order', v)}
            extraText={t('数字越小越靠前')}
            style={{ width: '100%' }}
          />
          <div className='mt-3'>
            <Text>{t('上架')}</Text>
            <Switch
              className='ml-2'
              checked={formValues.enabled}
              onChange={(v) => setField('enabled', v)}
            />
            <Text type='tertiary' size='small' className='block mt-1'>
              {t('下架后用户侧不再显示，已购买的订单不受影响')}
            </Text>
          </div>
        </Form>
      </Modal>
    </div>
  );
}
