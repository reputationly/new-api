import React from 'react';
import { useTranslation } from 'react-i18next';
import {
  Input,
  InputNumber,
  Select,
  Switch,
  Typography,
} from '@douyinfe/semi-ui';
import { PLAYGROUND_FIELD_META } from '../../constants/playgroundAdmin.constants';
import { VIDEO_ASPECT_RATIOS } from '../../constants/videoPlayground.constants';

const { Text } = Typography;

// 列表字段的候选项：宽高比是封闭集合，给一份现成的；尺寸/时长各家差异太大，只回显
// 已填的值，靠 allowCreate 手输。
const SUGGESTIONS = { aspectRatios: VIDEO_ASPECT_RATIOS };

// 按 PLAYGROUND_FIELD_META 的 type 渲染一个配置项。tab 配置、分类默认值共用，
// 保证同一个字段在两处的控件与语义一致（这正是把 fields 抽成 schema 的目的）。
// value=undefined 表示未配置，onChange(undefined) 表示清空回落兜底。
const FieldInput = ({ field, value, onChange, compact = false }) => {
  const { t } = useTranslation();
  const meta = PLAYGROUND_FIELD_META[field];
  if (!meta) return null;

  const control = (() => {
    switch (meta.type) {
      case 'list': {
        const list = Array.isArray(value) ? value : [];
        const options = SUGGESTIONS[field] || list;
        return (
          <Select
            multiple
            filter
            allowCreate
            value={list}
            optionList={options.map((s) => ({ label: s, value: s }))}
            onChange={(v) => onChange(v && v.length ? v : undefined)}
            placeholder={t(meta.placeholder || '输入后回车')}
            style={{ width: '100%' }}
          />
        );
      }
      case 'int':
        return (
          <InputNumber
            min={0}
            value={value == null ? undefined : value}
            onChange={(v) =>
              onChange(v === '' || v == null ? undefined : Number(v))
            }
            placeholder={t('留空 / 0 = 不限')}
            style={{ width: '100%' }}
          />
        );
      case 'translation': {
        const cfg = value || { enabled: false, defaultModel: '' };
        return (
          <div className='flex items-center gap-2'>
            <Switch
              size='small'
              checked={cfg.enabled === true}
              onChange={(v) =>
                onChange(v ? { ...cfg, enabled: true } : undefined)
              }
            />
            <Input
              value={cfg.defaultModel || ''}
              disabled={cfg.enabled !== true}
              onChange={(v) =>
                onChange({ ...cfg, enabled: true, defaultModel: v })
              }
              placeholder={t('翻译用的语言模型')}
              style={{ flex: 1, minWidth: 140 }}
            />
          </div>
        );
      }
      case 'bool':
        return (
          <Switch
            size='small'
            checked={value === true}
            onChange={(v) => onChange(v)}
          />
        );
      default:
        return null;
    }
  })();

  if (compact) return control;

  return (
    <div>
      <Text strong className='text-sm'>
        {t(meta.label)}
      </Text>
      <div className='mt-1'>{control}</div>
      {meta.help && (
        <Text type='tertiary' size='small' className='block mt-1'>
          {t(meta.help)}
        </Text>
      )}
    </div>
  );
};

export default FieldInput;
