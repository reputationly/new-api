import React, { useState, useEffect, useMemo, useCallback } from 'react';
import {
  Banner,
  Button,
  Checkbox,
  Empty,
  Input,
  InputNumber,
  Modal,
  Space,
  Spin,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconSearch } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';

import { API, showError, showSuccess } from '../../../../helpers';
import CardTable from '../../../common/ui/CardTable';

const { Text } = Typography;

/**
 * 渠道成本配置。设计见 docs/user-tier-pricing-and-topup-package-design.md §5。
 *
 * 成本比 = 上游成本 / ModelRatio 目录价，即供应商给的那个折扣。它有两个消费方：
 * 对账（按 (channel, model) 与供应商账单比对）与选路（同优先级层内按成本收敛）。
 *
 * 录入形态刻意做成「一张完整报价单」而不是「逐条添加」：数据来源就是供应商的
 * 报价单，一个渠道几十上百个模型，逐条 add 的交互在这个量级下没法用。
 */

// mergeRows 把「渠道挂载的模型」与「已配的成本」合成一张表。
//
// 两边都要显示：挂载了没配的是待补录的（选路时按未知成本处理，管理页要能看见）；
// 配了但已不挂载的是孤儿配置（渠道摘掉模型后残留），留着会让对账多出几行。
export function mergeRows(mountedModels, costs) {
  const costMap = new Map();
  (costs || []).forEach((c) => costMap.set(c.model_name, c));

  const rows = (mountedModels || []).map((name) => ({
    model: name,
    ratio: costMap.has(name) ? costMap.get(name).cost_ratio : null,
    remark: costMap.get(name)?.remark || '',
    mounted: true,
  }));

  const mountedSet = new Set(mountedModels || []);
  (costs || []).forEach((c) => {
    if (!mountedSet.has(c.model_name)) {
      rows.push({
        model: c.model_name,
        ratio: c.cost_ratio,
        remark: c.remark || '',
        mounted: false,
      });
    }
  });

  return rows;
}

// toPayload 只提交填了值的行。
//
// null 表示「未配」，与「配成 0」是两件事：前者在选路时按未知成本处理（与最低档
// 并列，不因未知而被排斥），后者是明确的零成本。把 null 当 0 提交会让该渠道
// 静默抢走全部流量。
export function toPayload(rows) {
  return rows
    .filter((r) => r.ratio !== null && r.ratio !== undefined && r.ratio !== '')
    .map((r) => ({
      model_name: r.model,
      cost_ratio: Number(r.ratio),
      remark: r.remark || '',
    }));
}

export default function ChannelCostModal({ visible, channel, onClose }) {
  const { t } = useTranslation();
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [selected, setSelected] = useState([]);
  const [batchValue, setBatchValue] = useState(1);

  const load = useCallback(async () => {
    if (!channel?.id) return;
    setLoading(true);
    try {
      const res = await API.get(`/api/channel/${channel.id}/cost`);
      const { success, message, data } = res.data;
      if (success) {
        setRows(mergeRows(data.mounted_models, data.costs));
      } else {
        showError(message);
      }
    } catch (e) {
      showError(t('加载渠道成本失败'));
    }
    setLoading(false);
  }, [channel?.id, t]);

  useEffect(() => {
    if (visible) {
      setKeyword('');
      setSelected([]);
      load();
    }
  }, [visible, load]);

  const missingCount = useMemo(
    () => rows.filter((r) => r.mounted && r.ratio === null).length,
    [rows],
  );

  const filtered = useMemo(() => {
    const kw = keyword.trim().toLowerCase();
    if (!kw) return rows;
    return rows.filter((r) => r.model.toLowerCase().includes(kw));
  }, [rows, keyword]);

  const updateRow = useCallback((modelName, field, val) => {
    setRows((prev) =>
      prev.map((r) => (r.model === modelName ? { ...r, [field]: val } : r)),
    );
  }, []);

  const applyBatch = useCallback(() => {
    if (selected.length === 0) return;
    const picked = new Set(selected);
    setRows((prev) =>
      prev.map((r) => (picked.has(r.model) ? { ...r, ratio: batchValue } : r)),
    );
    setSelected([]);
  }, [selected, batchValue]);

  // 未配的一次性按 1.0 填满：多数供应商只对部分模型给折扣，其余按目录价结算。
  // 先填满再改少数几个，比逐个填快得多。
  const fillMissing = useCallback(() => {
    setRows((prev) =>
      prev.map((r) => (r.mounted && r.ratio === null ? { ...r, ratio: 1 } : r)),
    );
  }, []);

  const save = useCallback(async () => {
    const invalid = rows.find(
      (r) => r.ratio !== null && (Number.isNaN(Number(r.ratio)) || r.ratio < 0),
    );
    if (invalid) {
      showError(t('模型 {{m}} 的成本比不合法', { m: invalid.model }));
      return;
    }
    setSaving(true);
    try {
      const res = await API.put(`/api/channel/${channel.id}/cost`, {
        costs: toPayload(rows),
      });
      const { success, message } = res.data;
      if (success) {
        showSuccess(t('成本配置已保存'));
        onClose?.(true);
      } else {
        showError(message);
      }
    } catch (e) {
      showError(t('保存渠道成本失败'));
    }
    setSaving(false);
  }, [rows, channel?.id, onClose, t]);

  const columns = useMemo(
    () => [
      {
        title: '',
        key: 'select',
        width: 40,
        render: (_, record) => (
          <Checkbox
            checked={selected.includes(record.model)}
            onChange={(e) =>
              setSelected((prev) =>
                e.target.checked
                  ? [...prev, record.model]
                  : prev.filter((m) => m !== record.model),
              )
            }
          />
        ),
      },
      {
        title: t('模型'),
        dataIndex: 'model',
        render: (text, record) => (
          <Space spacing={4}>
            <Text>{text}</Text>
            {!record.mounted && (
              <Tag size='small' color='orange'>
                {t('已不挂载')}
              </Tag>
            )}
          </Space>
        ),
      },
      {
        title: t('成本比'),
        dataIndex: 'ratio',
        width: 150,
        render: (_, record) => (
          <InputNumber
            size='small'
            min={0}
            step={0.05}
            precision={4}
            style={{ width: '100%' }}
            placeholder={t('未配置')}
            value={record.ratio}
            onChange={(v) =>
              updateRow(record.model, 'ratio', v === '' ? null : v)
            }
          />
        ),
      },
      {
        title: t('备注'),
        dataIndex: 'remark',
        render: (_, record) => (
          <Input
            size='small'
            placeholder={t('如：并行科技 2026-08 报价')}
            value={record.remark}
            onChange={(v) => updateRow(record.model, 'remark', v)}
          />
        ),
      },
    ],
    [t, selected, updateRow],
  );

  return (
    <Modal
      title={t('成本配置') + (channel?.name ? ` · ${channel.name}` : '')}
      visible={visible}
      onCancel={() => onClose?.(false)}
      width={860}
      footer={
        <Space>
          <Button onClick={() => onClose?.(false)}>{t('取消')}</Button>
          <Button theme='solid' loading={saving} onClick={save}>
            {t('保存')}
          </Button>
        </Space>
      }
    >
      <Spin spinning={loading}>
        <Banner
          type='info'
          closeIcon={null}
          description={
            <div className='text-xs leading-6'>
              <div>
                {t(
                  '成本比 = 上游成本 ÷ 目录价。0.62 表示这个渠道该模型的成本是目录价的 62%，即供应商给的折扣。',
                )}
              </div>
              <div>
                {t(
                  '留空 = 未配置。未配置的模型在选路时与最低成本并列（不因成本未知而被排斥），但对账会回退到旧口径，存在偏差。',
                )}
              </div>
            </div>
          }
        />

        {missingCount > 0 && (
          <Banner
            type='warning'
            closeIcon={null}
            className='mt-2'
            description={
              <Space>
                <Text>
                  {t('{{n}} 个已挂载模型未配成本', { n: missingCount })}
                </Text>
                <Button size='small' onClick={fillMissing}>
                  {t('全部按 1.0 填充')}
                </Button>
              </Space>
            }
          />
        )}

        <div className='mt-3 mb-2 flex flex-wrap items-center gap-2'>
          <Input
            prefix={<IconSearch />}
            size='small'
            style={{ width: 200 }}
            placeholder={t('搜索模型')}
            value={keyword}
            onChange={setKeyword}
            showClear
          />
          <InputNumber
            size='small'
            min={0}
            step={0.05}
            precision={4}
            style={{ width: 110 }}
            value={batchValue}
            onChange={(v) => setBatchValue(v ?? 0)}
          />
          <Button
            size='small'
            disabled={selected.length === 0}
            onClick={applyBatch}
          >
            {t('应用到选中')} ({selected.length})
          </Button>
        </div>

        {rows.length === 0 && !loading ? (
          <Empty description={t('该渠道未挂载任何模型')} />
        ) : (
          <CardTable
            columns={columns}
            dataSource={filtered}
            rowKey='model'
            pagination={false}
            size='small'
            scroll={{ y: 380 }}
          />
        )}
      </Spin>
    </Modal>
  );
}
