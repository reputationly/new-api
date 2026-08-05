import React, { useState, useEffect, useContext, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Banner,
  Button,
  Card,
  Empty,
  Form,
  Input,
  Select,
  Table,
  Typography,
} from '@douyinfe/semi-ui';
import { Plus, Trash2 } from 'lucide-react';
import { API, showSuccess, showError } from '../../../helpers';
import { StatusContext } from '../../../context/Status';
import {
  normalizeRate,
  formatDisplayPrice,
  toNormalizedNumber,
} from '../Ratio/hooks/useModelPricingEditorState';

const { Text } = Typography;

// 与后端 setting/ratio_setting/video_pricing.go 的常量一一对应。
const MODE_TOKEN = 'token';
const MODE_PER_CALL = 'per_call';
const COL_WITH_VIDEO = 'with_video';
const COL_WITHOUT_VIDEO = 'without_video';

const DEFAULT_RESOLUTIONS = ['480p', '720p', '1080p', '4k'];
const DEFAULT_SECONDS = ['5', '10'];

const emptyRow = () => ({
  model: '',
  mode: MODE_TOKEN,
  resolutions: [...DEFAULT_RESOLUTIONS],
  seconds: [...DEFAULT_SECONDS],
  // cells[分辨率][列] = 人民币价格字符串（列为 with_video/without_video 或秒数）
  cells: {},
});

const cellKey = (resolution, col) => `${resolution}||${col}`;

/**
 * 后端存的是美元，界面填的是人民币——货币边界只在这里。
 * 与 useModelPricingEditorState 的 ModelPrice 同口径：只除汇率，不再除 2
 * （那个 /2 是 ModelRatio 的基准价约定，价格字段没有）。
 */
const usdToCny = (usd, rate) => formatDisplayPrice(Number(usd) * rate);
const cnyToUsd = (cny, rate) => toNormalizedNumber(Number(cny) / rate);

function parseConfig(raw, rate) {
  if (!raw || !raw.trim()) return [];
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [];
  }
  if (!parsed || typeof parsed !== 'object') return [];

  return Object.entries(parsed).map(([model, entry]) => {
    const mode = entry?.mode === MODE_PER_CALL ? MODE_PER_CALL : MODE_TOKEN;
    const table = (mode === MODE_TOKEN ? entry?.token : entry?.per_call) || {};
    const cells = {};
    const cols = new Set();
    Object.entries(table).forEach(([resolution, row]) => {
      Object.entries(row || {}).forEach(([col, usd]) => {
        cells[cellKey(resolution, col)] = usdToCny(usd, rate);
        cols.add(col);
      });
    });
    const resolutions = Object.keys(table);
    return {
      model,
      mode,
      resolutions: resolutions.length ? resolutions : [...DEFAULT_RESOLUTIONS],
      seconds:
        mode === MODE_PER_CALL && cols.size
          ? [...cols].sort((a, b) => Number(a) - Number(b))
          : [...DEFAULT_SECONDS],
      cells,
    };
  });
}

function serializeRows(rows, rate) {
  const out = {};
  rows.forEach((row) => {
    const model = (row.model || '').trim();
    if (!model) return;
    const cols =
      row.mode === MODE_TOKEN
        ? [COL_WITH_VIDEO, COL_WITHOUT_VIDEO]
        : (row.seconds || []).map((s) => String(s).trim()).filter(Boolean);

    const table = {};
    (row.resolutions || []).forEach((resolution) => {
      const key = String(resolution).trim();
      if (!key) return;
      const bucket = {};
      cols.forEach((col) => {
        const raw = row.cells[cellKey(key, col)];
        // 留空 = 该格未配置，不写入。后端查不到会回退旧计费路径，
        // 写个 0 反而会被当成「未配置」的另一种形态，徒增歧义。
        if (raw === '' || raw === null || raw === undefined) return;
        const usd = cnyToUsd(raw, rate);
        if (usd === null || !(usd > 0)) return;
        bucket[col] = usd;
      });
      if (Object.keys(bucket).length) table[key] = bucket;
    });
    if (!Object.keys(table).length) return;

    out[model] =
      row.mode === MODE_TOKEN
        ? { mode: MODE_TOKEN, token: table }
        : { mode: MODE_PER_CALL, per_call: table };
  });
  return out;
}

export default function SettingsVideoPricing(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [statusState, statusDispatch] = useContext(StatusContext);
  const [rows, setRows] = useState([]);

  const rate = useMemo(
    () =>
      normalizeRate(
        props.options?.USDExchangeRate ??
          statusState?.status?.usd_exchange_rate,
      ),
    [props.options, statusState],
  );

  useEffect(() => {
    setRows(parseConfig(props.options?.VideoPricingConfig, rate));
  }, [props.options, rate]);

  const updateRow = (idx, patch) =>
    setRows((prev) => prev.map((r, i) => (i === idx ? { ...r, ...patch } : r)));
  const removeRow = (idx) =>
    setRows((prev) => prev.filter((_, i) => i !== idx));
  const updateCell = (idx, resolution, col, value) =>
    setRows((prev) =>
      prev.map((r, i) =>
        i === idx
          ? { ...r, cells: { ...r.cells, [cellKey(resolution, col)]: value } }
          : r,
      ),
    );

  const onSubmit = async () => {
    setLoading(true);
    try {
      const value = JSON.stringify(serializeRows(rows, rate));
      const res = await API.put('/api/option/', {
        key: 'VideoPricingConfig',
        value,
      });
      if (res.data.success) {
        showSuccess(t('保存成功'));
        statusDispatch({
          type: 'set',
          payload: { ...statusState.status, VideoPricingConfig: value },
        });
        if (props.refresh) await props.refresh();
      } else {
        showError(res.data.message);
      }
    } catch (e) {
      showError(t('保存失败，请重试'));
    } finally {
      setLoading(false);
    }
  };

  const renderMatrix = (row, idx) => {
    const cols =
      row.mode === MODE_TOKEN
        ? [
            { key: COL_WITHOUT_VIDEO, label: t('输入不含视频') },
            { key: COL_WITH_VIDEO, label: t('输入包含视频') },
          ]
        : (row.seconds || []).map((s) => ({
            key: String(s),
            label: `${s} ${t('秒')}`,
          }));

    const columns = [
      {
        title: t('分辨率'),
        dataIndex: 'resolution',
        width: 110,
        render: (v) => <Text strong>{v}</Text>,
      },
      ...cols.map((col) => ({
        title: col.label,
        dataIndex: col.key,
        render: (_, record) => (
          <Input
            value={row.cells[cellKey(record.resolution, col.key)] ?? ''}
            onChange={(v) => updateCell(idx, record.resolution, col.key, v)}
            placeholder={t('留空即不配置')}
            prefix='¥'
            size='small'
          />
        ),
      })),
    ];

    return (
      <Table
        size='small'
        pagination={false}
        columns={columns}
        dataSource={(row.resolutions || []).map((r) => ({
          key: r,
          resolution: r,
        }))}
        empty={<Text type='tertiary'>{t('请先添加分辨率')}</Text>}
      />
    );
  };

  return (
    <Card>
      <Form.Section
        text={t('视频计费配置')}
        extraText={
          t(
            '按「分辨率 × 输入是否含视频」或「分辨率 × 秒数」为视频模型定价，与供应商价目表逐格对应，可直接对抄。未配置的模型完全走原有的模型倍率/固定价格路径，不受影响。',
          ) +
          t(
            '「按 Token」用于上游会返回用量的模型（如 Seedance）：填每百万 tokens 的价格，结算时按上游实际返回的 token 数计费。注意秒数已隐含在 token 里，无需也不应再按秒数缩放。「按次」用于不返回 token 的模型（如 Kling）：填单次生成的价格，提交时即定价。',
          )
        }
      >
        <Banner
          type='info'
          closeIcon={null}
          description={t(
            '价格单位为人民币（¥），按运营设置的「美元兑人民币汇率」{{rate}} 折算为美元存储；显示保留 2 位小数，存储仍为高精度，计费不受影响。若日志按人民币展示，同一汇率下账单金额与此处填的价格逐格对应。',
            { rate },
          )}
          style={{ marginBottom: 16 }}
        />

        {rows.length === 0 ? (
          <Empty
            description={
              <Text type='tertiary'>{t('暂无视频计费配置，请添加')}</Text>
            }
            style={{ padding: '16px 0' }}
          />
        ) : (
          rows.map((row, idx) => (
            <div
              key={idx}
              style={{
                border: '1px solid var(--semi-color-border)',
                borderRadius: 6,
                padding: 12,
                marginBottom: 12,
              }}
            >
              <div
                style={{
                  display: 'flex',
                  gap: 8,
                  alignItems: 'center',
                  marginBottom: 12,
                  flexWrap: 'wrap',
                }}
              >
                <Input
                  value={row.model}
                  onChange={(v) => updateRow(idx, { model: v })}
                  placeholder={t('模型名称')}
                  style={{ width: 260, flexShrink: 0 }}
                />
                <Select
                  value={row.mode}
                  onChange={(v) => updateRow(idx, { mode: v })}
                  optionList={[
                    { label: t('按 Token'), value: MODE_TOKEN },
                    { label: t('按次'), value: MODE_PER_CALL },
                  ]}
                  style={{ width: 130, flexShrink: 0 }}
                />
                <Select
                  multiple
                  filter
                  allowCreate
                  value={row.resolutions}
                  optionList={DEFAULT_RESOLUTIONS.map((r) => ({
                    label: r,
                    value: r,
                  }))}
                  onChange={(v) => updateRow(idx, { resolutions: v })}
                  placeholder={t('分辨率，如 720p')}
                  style={{ flex: 1, minWidth: 200 }}
                />
                {row.mode === MODE_PER_CALL && (
                  <Select
                    multiple
                    filter
                    allowCreate
                    value={row.seconds}
                    optionList={DEFAULT_SECONDS.map((s) => ({
                      label: s,
                      value: s,
                    }))}
                    onChange={(v) => updateRow(idx, { seconds: v })}
                    placeholder={t('秒数，如 5')}
                    style={{ flex: 1, minWidth: 160 }}
                  />
                )}
                <Button
                  type='danger'
                  theme='borderless'
                  icon={<Trash2 size={16} />}
                  onClick={() => removeRow(idx)}
                />
              </div>
              <Text type='tertiary' size='small'>
                {row.mode === MODE_TOKEN
                  ? t('单位：¥ / 百万 tokens')
                  : t('单位：¥ / 次')}
              </Text>
              <div style={{ marginTop: 8 }}>{renderMatrix(row, idx)}</div>
            </div>
          ))
        )}

        <Button
          theme='outline'
          type='tertiary'
          icon={<Plus size={16} />}
          onClick={() => setRows((prev) => [...prev, emptyRow()])}
          style={{ marginTop: 4 }}
        >
          {t('添加模型')}
        </Button>

        <div style={{ marginTop: 24 }}>
          <Button type='primary' onClick={onSubmit} loading={loading}>
            {t('保存设置')}
          </Button>
        </div>
      </Form.Section>
    </Card>
  );
}
