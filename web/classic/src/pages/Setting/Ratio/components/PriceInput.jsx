import React, { useState } from 'react';
import { Input } from '@douyinfe/semi-ui';
import {
  PRICE_SUFFIX,
  formatDisplayPrice,
} from '../hooks/useModelPricingEditorState';

// 美元录入模式（usdRate 非空）只改**输入框这一层**：state 与后端存储始终是人民币，
// 显示值与键入值按汇率折算。运营商照抄供应商的美元价目表时不用自己乘，
// 序列化链路（ModelRatio = ¥/百万 tokens、ModelPrice = ¥/次）一个字都不用动。
// toFixed 不会输出科学计数法，但 parseFloat+String 会（2e-8）；这里统一保留小数串，
// 避免极低单价（如 $0.00000002）在往返里被压成 0。
const trimTrailingZeros = (text) =>
  text.includes('.') ? text.replace(/0+$/, '').replace(/\.$/, '') : text;

const toFixedString = (num, digits) => {
  if (!Number.isFinite(num)) {
    return '';
  }
  return trimTrailingZeros(num.toFixed(digits));
};

const toUSDDisplay = (value, rate) => {
  if (!hasNumber(value)) {
    return '';
  }
  // 10 位足够：再细的价格也没有实际意义，但 6 位会把 1e-7 量级直接抹成 0。
  return toFixedString(Number(value) / rate, 10);
};

// 展示用：不足 2 位补 0，超过则保留原精度（不走 formatDisplayPrice：
// 它把 2e-8 这类型值圆成 "0.00"，低价模型的数字会凭空消失）。
const formatUSDDisplay = (value, rate) => {
  const raw = toUSDDisplay(value, rate);
  if (raw === '') {
    return '';
  }
  const [intPart, fracPart = ''] = raw.split('.');
  return `${intPart}.${fracPart.length < 2 ? fracPart.padEnd(2, '0') : fracPart}`;
};

const toCNYValue = (draft, rate) => {
  const raw = String(draft ?? '').trim();
  const num = Number(raw);
  if (raw === '' || !Number.isFinite(num)) {
    // 空串 / 半截输入（"."、"1." 由父级正则决定收不收）原样透传，保持既有手感。
    return draft;
  }
  // 12 位：后端按 6 位存（roundTo6），但这里必须比存储精度更细，
  // 否则 $0.00000002 × 7.3 会在换算这一步就变成 0。
  return toFixedString(num * rate, 12);
};

const hasNumber = (value) =>
  value !== '' && value !== null && value !== undefined;

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
  // 非空即进入美元录入模式（值为 1 美元折多少人民币）。null/0 = 原来的纯人民币输入。
  usdRate = null,
  sourceCurrency = 'CNY',
}) => {
  const [focused, setFocused] = useState(false);
  const [draft, setDraft] = useState('');
  const rate = Number(usdRate);
  const useUSD = Number.isFinite(rate) && rate > 0;
  const displayValue = focused
    ? draft
    : useUSD
      ? formatUSDDisplay(value, rate)
      : formatDisplayPrice(value);
  // 单位后缀随模式换符号，"/1M tokens" 与 "/次" 两种都覆盖到。
  const effectiveSuffix =
    useUSD || sourceCurrency === 'USD'
      ? String(suffix).replace('¥', '$')
      : suffix;
  // 美元模式下必须让人看见落库的人民币值，否则存进去多少是个惊喜。
  const unit = String(suffix).replace(/[¥$]/g, '');
  const cnyHint =
    useUSD && Number(value) > 0 ? `= ¥${formatDisplayPrice(value)}${unit}` : '';

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
            setDraft(useUSD ? toUSDDisplay(value, rate) : (value ?? ''));
            setFocused(true);
          }}
          onBlur={() => setFocused(false)}
          onChange={(v) => {
            setDraft(v);
            onChange(useUSD ? toCNYValue(v, rate) : v);
          }}
          suffix={effectiveSuffix}
          disabled={disabled}
        />
      ) : null}
      {cnyHint ? (
        <div className='mt-1 text-xs text-gray-400'>{cnyHint}</div>
      ) : null}
      {extraText ? (
        <div className='mt-1 text-xs text-gray-500'>{extraText}</div>
      ) : null}
    </div>
  );
};

export default PriceInput;
