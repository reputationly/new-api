import React, { useEffect, useMemo, useState } from 'react';
import {
  Modal,
  InputNumber,
  Table,
  Typography,
  Tag,
  Button,
  Space,
} from '@douyinfe/semi-ui';

const { Text } = Typography;

// 可以按时段变价的分量。顺序刻意按"最常改"排：输入、输出、缓存，其余在后。
// 后端白名单与之对应（setting/ratio_setting/group_time_ratio.go: timeFactorComponents）。
const COMPONENTS = [
  { key: 'input', label: '输入', unit: '¥/1M' },
  { key: 'output', label: '输出', unit: '¥/1M' },
  { key: 'cache', label: '缓存命中', unit: '¥/1M' },
  { key: 'create_cache', label: '缓存创建', unit: '¥/1M' },
  { key: 'image', label: '图片输入', unit: '¥/1M' },
  { key: 'audio_input', label: '音频输入', unit: '¥/1M' },
  { key: 'audio_output', label: '音频输出', unit: '¥/1M' },
  { key: 'per_call', label: '按次价', unit: '¥/次' },
];

const MAX_FACTOR = 20;

/**
 * 单条时段价的编辑器：把「分量 × 该时段目录价」摊成一张表。
 *
 * 运营填的是**价格**（¥），存的是**系数**——系数让时段价随常规目录价一起变，
 * 不必因为改基础价回来重配每一条时段。
 *
 * 留空 = 该分量不随时段变（后端 factors 里不出现即视为不变）。
 * 「整价系数」是快捷入口：一次写满所有分量，覆盖"整价统一打折"这个主流场景。
 */
const TimePriceRuleEditor = ({ t, visible, rule, base, onCancel, onOk }) => {
  // draft: component -> 价格（¥，null 表示不随时段变）
  const [draft, setDraft] = useState({});

  useEffect(() => {
    if (!visible) return;
    const next = {};
    const factors = rule?.factors || {};
    COMPONENTS.forEach(({ key }) => {
      const f = Number(factors[key]);
      const b = Number(base?.[key]);
      next[key] = f > 0 && b > 0 ? Number((b * f).toFixed(4)) : null;
    });
    setDraft(next);
  }, [visible, rule, base]);

  const rows = useMemo(
    () =>
      COMPONENTS.filter(({ key }) => Number(base?.[key]) > 0).map(
        ({ key, label, unit }) => {
          const b = Number(base[key]);
          const v = draft[key];
          return {
            key,
            label,
            unit,
            base: b,
            price: v,
            factor: v > 0 ? v / b : null,
          };
        },
      ),
    [base, draft],
  );

  // 整价系数：一次把所有分量按同一比例改
  const applyUniform = (f) => {
    if (!(f > 0)) return;
    const next = {};
    COMPONENTS.forEach(({ key }) => {
      const b = Number(base?.[key]);
      next[key] = b > 0 ? Number((b * f).toFixed(4)) : null;
    });
    setDraft(next);
  };

  const submit = () => {
    const factors = {};
    let bad = false;
    COMPONENTS.forEach(({ key }) => {
      const b = Number(base?.[key]);
      const v = draft[key];
      if (!(v > 0)) return; // 留空 = 不随时段变
      const f = v / b;
      if (!(f > 0 && f <= MAX_FACTOR)) bad = true;
      factors[key] = Number(f.toFixed(6));
    });
    if (bad) {
      // 交给外层统一提示（与面板其它校验同一套文案）
      onOk(null, t('有分量的折算系数超出 (0, 20]，请检查目录价'));
      return;
    }
    onOk(factors);
  };

  const columns = [
    { title: t('分量'), dataIndex: 'label', width: 110 },
    {
      title: t('常规目录价'),
      dataIndex: 'base',
      width: 120,
      render: (b, r) => (
        <Text type='tertiary' size='small'>
          {Number(b.toFixed(4))} {r.unit}
        </Text>
      ),
    },
    {
      title: t('该时段目录价'),
      dataIndex: 'price',
      render: (v, r) => (
        <InputNumber
          size='small'
          min={0}
          step={0.1}
          precision={4}
          value={v}
          placeholder={t('不随时段变')}
          style={{ width: 150 }}
          onChange={(val) =>
            setDraft((prev) => ({
              ...prev,
              [r.key]: typeof val === 'number' && val > 0 ? val : null,
            }))
          }
        />
      ),
    },
    {
      title: t('折算'),
      dataIndex: 'factor',
      width: 100,
      render: (f) =>
        f === null ? (
          <Text type='tertiary' size='small'>
            {t('不变')}
          </Text>
        ) : (
          <Tag color={f > 1 ? 'orange' : 'cyan'} shape='circle' size='small'>
            ×{Number(f.toFixed(3))}
          </Tag>
        ),
    },
  ];

  return (
    <Modal
      title={t('该时段的价目')}
      visible={visible}
      onCancel={onCancel}
      footer={
        <Space>
          <Button onClick={onCancel}>{t('取消')}</Button>
          <Button theme='solid' onClick={submit}>
            {t('确定')}
          </Button>
        </Space>
      }
      width={720}
    >
      <div className='mb-3 flex items-center gap-2 flex-wrap'>
        <Text type='tertiary' size='small'>
          {t('整价系数')}
        </Text>
        {[0.5, 0.6, 0.8, 1].map((f) => (
          <Button key={f} size='small' onClick={() => applyUniform(f)}>
            ×{f}
          </Button>
        ))}
        <Text type='tertiary' size='small'>
          {t('一键按比例写满所有分量')}
        </Text>
      </div>
      <Table
        size='small'
        bordered={false}
        pagination={false}
        dataSource={rows}
        columns={columns}
        rowKey='key'
      />
      <Text type='tertiary' size='small' className='mt-2 block'>
        {t(
          '留空 = 该分量不随时段变。填 ¥ 存系数，改常规目录价时各时段自动跟随。',
        )}
      </Text>
    </Modal>
  );
};

export default TimePriceRuleEditor;
