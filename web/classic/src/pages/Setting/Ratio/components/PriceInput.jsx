import React, { useState } from 'react';
import { Input } from '@douyinfe/semi-ui';
import {
  PRICE_SUFFIX,
  formatDisplayPrice,
} from '../hooks/useModelPricingEditorState';

// 显示固定 2 位（¥4.00），编辑时才展开原始精度串；失焦不回写圆整值，
// 故 model state 始终保持高精度，序列化不受 2 位显示影响（见设计 §6.1.1）。
// 基础价格与分时价分量价共用同一行式：标签在上、输入通栏、右侧可挂徽标，
// 两处各写一份必然在单位后缀与精度上漂移。
const PriceInput = ({
  label,
  value,
  placeholder,
  onChange,
  suffix = PRICE_SUFFIX,
  disabled = false,
  extraText = '',
  headerAction = null,
  hidden = false,
}) => {
  const [focused, setFocused] = useState(false);
  const [draft, setDraft] = useState('');
  const displayValue = focused ? draft : formatDisplayPrice(value);
  return (
    <div style={{ marginBottom: 16 }}>
      <div className='mb-1 font-medium text-gray-700 flex items-center justify-between gap-3'>
        <span>{label}</span>
        {headerAction}
      </div>
      {!hidden ? (
        <Input
          value={displayValue}
          placeholder={placeholder}
          onFocus={() => {
            // 编辑起点用原始高精度串，避免廉价模型（如 ¥0.0146）被 2 位显示圆整后丢精度。
            setDraft(value ?? '');
            setFocused(true);
          }}
          onBlur={() => setFocused(false)}
          onChange={(v) => {
            setDraft(v);
            onChange(v);
          }}
          suffix={suffix}
          disabled={disabled}
        />
      ) : null}
      {extraText ? (
        <div className='mt-1 text-xs text-gray-500'>{extraText}</div>
      ) : null}
    </div>
  );
};

export default PriceInput;
