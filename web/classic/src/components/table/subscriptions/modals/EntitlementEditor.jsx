import React, { useMemo } from 'react';
import {
  Banner,
  Button,
  Card,
  Col,
  InputNumber,
  Row,
  Select,
  Space,
  Switch,
  TagInput,
  Tooltip,
  Typography,
} from '@douyinfe/semi-ui';
import {
  IconArrowDown,
  IconArrowUp,
  IconCopy,
  IconDelete,
  IconPlus,
} from '@douyinfe/semi-icons';
import {
  findEntitlementOverlaps,
  splitModels,
} from '../../../../helpers/entitlementOverlap';

const { Text } = Typography;

const entitlementResetOptions = [
  { value: 'never', label: '不重置' },
  { value: 'daily', label: '每天' },
  { value: 'weekly', label: '每周' },
  { value: 'monthly', label: '每月' },
  { value: 'custom', label: '跟随套餐自定义周期' },
];

export const emptyEntitlement = () => ({
  id: 0,
  models: '',
  channel_ids: '',
  consume_points: true,
  consume_discount: 1,
  limit_count: 0,
  reset_period: 'never',
  rate_limit_rpm: 0,
});

/**
 * 把后端返回的权益列表转成编辑态。新建套餐时后端没有这个字段，给空数组。
 */
export const toEditableEntitlements = (list) =>
  (Array.isArray(list) ? list : []).map((e) => ({
    id: e.id || 0,
    models: e.models || '',
    channel_ids: e.channel_ids || '',
    // 后端用不带 default 标签的布尔存这个开关，false 是有意义的值，
    // 不能用 || 兜底，否则「不消耗算力点」会被改回 true。
    consume_points: e.consume_points !== false,
    consume_discount: Number(e.consume_discount ?? 1),
    limit_count: Number(e.limit_count || 0),
    reset_period: e.reset_period || 'never',
    rate_limit_rpm: Number(e.rate_limit_rpm || 0),
  }));

/**
 * 数字字段归一。InputNumber 被清空时给出的可能是 undefined / null / ''，
 * 直接 Number() 会得到 NaN，而 NaN 的任何比较都是 false——校验会被静默跳过，
 * 表现为「明明没填 RPM 却放行了」。空值一律当 0 处理，让比较回到有意义的分支。
 */
const toNumber = (v) => {
  const n = Number(v);
  return Number.isFinite(n) ? n : 0;
};

/**
 * 保存前的前端校验，规则与后端 ValidateEntitlement 一致。
 * 前端先拦一道只是为了少一次往返，后端那道才是真正的把关。
 */
export const validateEntitlements = (list, t) => {
  const items = Array.isArray(list) ? list : [];
  for (let i = 0; i < items.length; i++) {
    const e = items[i];
    if (splitModels(e.models).length === 0) {
      return t('权益 {{n}} 的模型范围不能为空').replace('{{n}}', i + 1);
    }
    if (toNumber(e.consume_discount) <= 0) {
      return t('权益 {{n}} 的折扣系数必须大于 0').replace('{{n}}', i + 1);
    }
    if (
      (toNumber(e.limit_count) <= 0 || !e.consume_points) &&
      toNumber(e.rate_limit_rpm) <= 0
    ) {
      return t('权益 {{n}} 不限次或不消耗算力点，必须设置速率限制').replace(
        '{{n}}',
        i + 1,
      );
    }
  }
  return '';
};

const EntitlementEditor = ({
  value = [],
  onChange,
  channelOptions = [],
  t,
}) => {
  const overlaps = useMemo(() => findEntitlementOverlaps(value), [value]);

  const update = (index, patch) => {
    const next = value.map((item, i) =>
      i === index ? { ...item, ...patch } : item,
    );
    onChange(next);
  };

  const add = () => onChange([...value, emptyEntitlement()]);

  const remove = (index) => onChange(value.filter((_, i) => i !== index));

  // 复制时必须把 id 清零：沿用原 id 会被后端当成「更新同一条」，
  // 结果是复制出来的那份把原件覆盖掉，而不是新增一条。
  const duplicate = (index) =>
    onChange([
      ...value.slice(0, index + 1),
      { ...value[index], id: 0 },
      ...value.slice(index + 1),
    ]);

  const move = (index, delta) => {
    const target = index + delta;
    if (target < 0 || target >= value.length) return;
    const next = [...value];
    [next[index], next[target]] = [next[target], next[index]];
    onChange(next);
  };

  return (
    <div>
      <div className='flex items-center justify-between mb-2'>
        <Text type='tertiary' size='small'>
          {t(
            '顺序即匹配优先级，靠前的先命中；次数与算力点叠加生效，任一用尽即降级扣钱包余额',
          )}
        </Text>
        <Button size='small' icon={<IconPlus />} onClick={add}>
          {t('添加权益')}
        </Button>
      </div>

      {overlaps.map((o) => (
        <Banner
          key={`${o.first}-${o.second}`}
          type='warning'
          className='mb-2'
          closeIcon={null}
          description={t(
            '权益 {{a}} 与权益 {{b}} 模型范围重叠：{{models}}。重叠部分只会命中权益 {{a}}，如不符合预期请调整顺序。',
          )
            .replace(/\{\{a\}\}/g, String(o.first + 1))
            .replace(/\{\{b\}\}/g, String(o.second + 1))
            .replace('{{models}}', o.models.join('、'))}
        />
      ))}

      {value.length === 0 && (
        <Card className='!rounded-xl border-0 shadow-sm'>
          <Text type='tertiary'>
            {t(
              '未配置权益。不配权益时套餐仅按总额度计费，与现有套餐行为一致。',
            )}
          </Text>
        </Card>
      )}

      {value.map((ent, index) => {
        const unlimited = Number(ent.limit_count) <= 0;
        const rpmRequired = unlimited || !ent.consume_points;
        const rpmMissing = rpmRequired && Number(ent.rate_limit_rpm) <= 0;
        return (
          <Card
            key={index}
            className='!rounded-xl border-0 shadow-sm mb-3'
            title={`${t('权益')} ${index + 1}`}
            headerExtraContent={
              <Space>
                <Tooltip content={t('上移')}>
                  <Button
                    size='small'
                    theme='borderless'
                    icon={<IconArrowUp />}
                    disabled={index === 0}
                    onClick={() => move(index, -1)}
                  />
                </Tooltip>
                <Tooltip content={t('下移')}>
                  <Button
                    size='small'
                    theme='borderless'
                    icon={<IconArrowDown />}
                    disabled={index === value.length - 1}
                    onClick={() => move(index, 1)}
                  />
                </Tooltip>
                <Tooltip content={t('复制')}>
                  <Button
                    size='small'
                    theme='borderless'
                    icon={<IconCopy />}
                    onClick={() => duplicate(index)}
                  />
                </Tooltip>
                <Tooltip content={t('删除')}>
                  <Button
                    size='small'
                    theme='borderless'
                    type='danger'
                    icon={<IconDelete />}
                    onClick={() => remove(index)}
                  />
                </Tooltip>
              </Space>
            }
          >
            <Row gutter={12}>
              <Col span={24}>
                <Text size='small' type='secondary'>
                  {t('模型范围')}
                </Text>
                <TagInput
                  value={splitModels(ent.models)}
                  onChange={(tags) => update(index, { models: tags.join(',') })}
                  placeholder={t(
                    '输入模型名后回车，支持通配符如 claude-opus-*',
                  )}
                  style={{ width: '100%' }}
                  className='mt-1'
                />
              </Col>

              <Col span={24} className='mt-3'>
                <Text size='small' type='secondary'>
                  {t('渠道限定')}
                </Text>
                <Select
                  multiple
                  filter
                  value={splitModels(ent.channel_ids)}
                  onChange={(ids) =>
                    update(index, { channel_ids: (ids || []).join(',') })
                  }
                  placeholder={t('留空 = 不限渠道')}
                  style={{ width: '100%' }}
                  className='mt-1'
                  optionList={channelOptions}
                />
              </Col>

              <Col span={12} className='mt-3'>
                <Text size='small' type='secondary'>
                  {t('消耗算力点')}
                </Text>
                <div className='mt-1 flex items-center gap-2'>
                  <Switch
                    checked={ent.consume_points}
                    onChange={(checked) =>
                      update(index, { consume_points: checked })
                    }
                  />
                  <Text size='small' type='tertiary'>
                    {ent.consume_points
                      ? t('按量扣点')
                      : t('不计费（无限制模型）')}
                  </Text>
                </div>
              </Col>

              <Col span={12} className='mt-3'>
                <Text size='small' type='secondary'>
                  {t('折扣系数')}
                </Text>
                <InputNumber
                  value={ent.consume_discount}
                  onChange={(v) => update(index, { consume_discount: v })}
                  min={0.0001}
                  step={0.1}
                  precision={4}
                  disabled={!ent.consume_points}
                  style={{ width: '100%' }}
                  className='mt-1'
                />
              </Col>

              <Col span={12} className='mt-3'>
                <Text size='small' type='secondary'>
                  {t('次数上限')}
                </Text>
                <InputNumber
                  value={ent.limit_count}
                  onChange={(v) => update(index, { limit_count: v })}
                  min={0}
                  precision={0}
                  style={{ width: '100%' }}
                  className='mt-1'
                />
                <Text size='small' type='tertiary'>
                  {unlimited ? t('0 = 不限次') : t('外采模型建议必配')}
                </Text>
              </Col>

              <Col span={12} className='mt-3'>
                <Text size='small' type='secondary'>
                  {t('次数重置周期')}
                </Text>
                <Select
                  value={ent.reset_period}
                  onChange={(v) => update(index, { reset_period: v })}
                  disabled={unlimited}
                  style={{ width: '100%' }}
                  className='mt-1'
                  optionList={entitlementResetOptions.map((o) => ({
                    value: o.value,
                    label: t(o.label),
                  }))}
                />
              </Col>

              <Col span={24} className='mt-3'>
                <Text size='small' type='secondary'>
                  {t('速率限制 RPM')}
                </Text>
                <InputNumber
                  value={ent.rate_limit_rpm}
                  onChange={(v) => update(index, { rate_limit_rpm: v })}
                  min={0}
                  precision={0}
                  style={{ width: '100%' }}
                  className='mt-1'
                />
                {rpmMissing ? (
                  <Text size='small' type='danger'>
                    {t(
                      '不限次或不消耗算力点时必填：这两种情况下它是唯一的成本闸门',
                    )}
                  </Text>
                ) : (
                  <Text size='small' type='tertiary'>
                    {t('0 = 不限速')}
                  </Text>
                )}
              </Col>
            </Row>
          </Card>
        );
      })}
    </div>
  );
};

export default EntitlementEditor;
