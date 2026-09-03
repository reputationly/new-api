import React from 'react';
import { useTranslation } from 'react-i18next';
import {
  Checkbox,
  CheckboxGroup,
  Input,
  InputNumber,
  Select,
  Switch,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { PLAYGROUND_FIELD_META } from '../../constants/playgroundAdmin.constants';
import { VIDEO_ASPECT_RATIOS } from '../../constants/videoPlayground.constants';
import {
  IMAGE_QUALITY_TIERS,
  imageTierLabel,
} from '../../constants/imagePlayground.constants';

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
      // 画质档：**只出档名，不出数字**。存进配置的仍是面积基准（computeImageSize 要
      // 它算像素），但那是实现细节 —— 运营要回答的只有「这个模型对外提供到哪一档」。
      // 让运营去填 "2048" 既没有意义（它既不是宽也不是高），填错了还会静默出错档。
      //
      // 三档都可自由勾选（标准也能取消）：只给高清以上、或只给标准，都是合法的产品
      // 决策。全不勾 = 不展示画质选择器，回到"只由宽高比定画幅"。
      //
      // 顺序按面积基准升序，不跟着勾选顺序走 —— 让"超清"排在"标准"前面纯属噪声。
      case 'tiers': {
        const picked = Array.isArray(value) ? value.map(String) : [];
        // 老配置里可能有**不在标准阶梯上的值**：这个字段原来是自由文本列表，运营手填
        // 过什么都有可能，4096（极清）更是被文档明确提过。这些值必须一起渲染成勾选项，
        // 否则它们会显示成"一个都没勾"——运营随手点一下就把配置覆盖掉了，而体验区那边
        // 还在按旧值出图，两处说法不一致、且全程没有任何报错。
        //
        // 渲染出来之后它们是普通勾选项：留着就保住，主动取消才移除，两种意图都能表达。
        const known = IMAGE_QUALITY_TIERS.map((x) => x.base);
        const options = [
          ...IMAGE_QUALITY_TIERS,
          ...picked
            .filter((b) => !known.includes(b))
            .map((b) => ({ base: b, label: imageTierLabel(b) })),
        ].sort((a, b) => Number(a.base) - Number(b.base));
        return (
          <CheckboxGroup
            direction='horizontal'
            value={options
              .filter((x) => picked.includes(x.base))
              .map((x) => x.base)}
            onChange={(v) => {
              const next = options
                .filter((x) => (v || []).includes(x.base))
                .map((x) => x.base);
              onChange(next.length ? next : undefined);
            }}
          >
            {options.map((x) => (
              <Checkbox key={x.base} value={x.base}>
                {t(x.label)}
              </Checkbox>
            ))}
          </CheckboxGroup>
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
