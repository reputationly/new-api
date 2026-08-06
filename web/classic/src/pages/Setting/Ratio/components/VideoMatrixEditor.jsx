import React from 'react';
import { Input, Select, Table, Typography } from '@douyinfe/semi-ui';
import {
  NUMERIC_INPUT_REGEX,
  VIDEO_MODE_TOKEN,
  VIDEO_MODE_PER_CALL,
  VIDEO_COL_WITH_VIDEO,
  VIDEO_COL_WITHOUT_VIDEO,
  VIDEO_DEFAULT_RESOLUTIONS,
  VIDEO_DEFAULT_SECONDS,
  videoCellKey,
} from '../hooks/useModelPricingEditorState';

const { Text } = Typography;

/**
 * 视频计费矩阵编辑器。受控组件——不自带保存按钮，由 ModelPricingEditor 的
 * handleSubmit 统一写 VideoPricingConfig，保证矩阵与预扣锚点一次保存、一起校验。
 *
 * allowTokenMode=false 时禁用 token 选项——对应「缺预扣锚点，且界面上补不了」的模型
 * （tiered_expr 不渲染价格输入框）。判据是 canUseTokenVideoMatrix，不要在这里重新
 * 推导。用 disabled 而不是从列表里摘掉，是为了让历史数据里已经是 token 的配置仍然
 * 显示得出来，只是切不回去。完整状态空间见
 * useModelPricingEditorState.js 的 isVideoMatrixMissingAnchor 上方。
 */
export default function VideoMatrixEditor({
  value,
  onChange,
  allowTokenMode = true,
  t,
}) {
  const patch = (next) => onChange({ ...value, ...next });

  // 与 PriceInput / handleNumericFieldChange 同一条过滤规则：非法字符根本进不了
  // state。不拦的话 serializeVideoMatrix 会把 Number() 解析不出的格子静默丢掉
  // （"1,2" / "abc" 都会），保存照样成功，运营以为价格存进去了，实际那一格
  // 压根没写进 VideoPricingConfig。
  const updateCell = (resolution, col, cellValue) => {
    if (!NUMERIC_INPUT_REGEX.test(cellValue)) {
      return;
    }
    onChange({
      ...value,
      cells: { ...value.cells, [videoCellKey(resolution, col)]: cellValue },
    });
  };

  const cols =
    value.mode === VIDEO_MODE_TOKEN
      ? [
          { key: VIDEO_COL_WITHOUT_VIDEO, label: t('输入不含视频') },
          { key: VIDEO_COL_WITH_VIDEO, label: t('输入包含视频') },
        ]
      : (value.seconds || []).map((s) => ({
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
          value={value.cells?.[videoCellKey(record.resolution, col.key)] ?? ''}
          onChange={(v) => updateCell(record.resolution, col.key, v)}
          placeholder={t('留空即不配置')}
          prefix='¥'
          size='small'
        />
      ),
    })),
  ];

  return (
    <div>
      <div
        style={{
          display: 'flex',
          gap: 8,
          alignItems: 'center',
          marginBottom: 12,
          flexWrap: 'wrap',
        }}
      >
        <Select
          value={value.mode}
          onChange={(v) => patch({ mode: v })}
          optionList={[
            {
              label: t('按 Token'),
              value: VIDEO_MODE_TOKEN,
              disabled: !allowTokenMode,
            },
            { label: t('按次'), value: VIDEO_MODE_PER_CALL },
          ]}
          style={{ width: 130, flexShrink: 0 }}
        />
        <Select
          multiple
          filter
          allowCreate
          value={value.resolutions}
          optionList={VIDEO_DEFAULT_RESOLUTIONS.map((r) => ({
            label: r,
            value: r,
          }))}
          onChange={(v) => patch({ resolutions: v })}
          placeholder={t('分辨率，如 720p')}
          style={{ flex: 1, minWidth: 200 }}
        />
        {value.mode === VIDEO_MODE_PER_CALL && (
          <Select
            multiple
            filter
            allowCreate
            value={value.seconds}
            optionList={VIDEO_DEFAULT_SECONDS.map((s) => ({
              label: s,
              value: s,
            }))}
            onChange={(v) => patch({ seconds: v })}
            placeholder={t('秒数，如 5')}
            style={{ flex: 1, minWidth: 160 }}
          />
        )}
      </div>

      <Text type='tertiary' size='small'>
        {value.mode === VIDEO_MODE_TOKEN
          ? t('单位：¥ / 百万 tokens')
          : t('单位：¥ / 次')}
      </Text>

      <div style={{ marginTop: 8 }}>
        <Table
          size='small'
          pagination={false}
          columns={columns}
          dataSource={(value.resolutions || []).map((r) => ({
            key: r,
            resolution: r,
          }))}
          empty={<Text type='tertiary'>{t('请先添加分辨率')}</Text>}
        />
      </div>
    </div>
  );
}
