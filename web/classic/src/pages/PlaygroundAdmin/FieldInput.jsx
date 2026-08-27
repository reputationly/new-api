import React from 'react';
import { useTranslation } from 'react-i18next';
import {
  Input,
  InputNumber,
  Select,
  Switch,
  Tag,
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
//
// lock：该字段被引擎硬约束锁死（来自 tab.fieldLocks，形如 { value, reason }）。
// 渲染成只读的值 + 原因，不给编辑入口 —— 运营改了也不作数，给个能改的框只会让人
// 以为改生效了。体验区读同一份 lock.value，见 getTabFieldLock 的注释。
const FieldInput = ({ field, value, onChange, compact = false, lock }) => {
  const { t } = useTranslation();
  const meta = PLAYGROUND_FIELD_META[field];
  if (!meta) return null;

  if (lock) {
    const locked = (lock.value || []).join('、');
    if (compact) return <Text type='tertiary'>{locked}</Text>;
    return (
      <div>
        <Text strong className='text-sm'>
          {t(meta.label)}
        </Text>
        <div className='mt-1'>
          <Tag size='large' color='grey' shape='circle'>
            {locked}
          </Tag>
          <Text type='tertiary' size='small' className='ml-2'>
            {t('引擎固定，不可修改')}
          </Text>
        </div>
        {lock.reason && (
          <Text type='warning' size='small' className='block mt-1'>
            {t(lock.reason)}
          </Text>
        )}
      </div>
    );
  }

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
      // 只剩一个开关：翻译用哪个语言模型已统一到「通用设置」里的那一个（与「AI 优化
      // 提示词」共用），不再逐模型配 defaultModel。值仍存成对象而非布尔，是为了不动
      // 已有配置的形状（读侧只看 enabled）。
      case 'translation':
        return (
          <Switch
            size='small'
            checked={value?.enabled === true}
            onChange={(v) => onChange(v ? { enabled: true } : undefined)}
          />
        );
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
