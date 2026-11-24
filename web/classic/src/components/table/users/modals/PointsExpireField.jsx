import React from 'react';
import {
  DatePicker,
  InputNumber,
  Radio,
  RadioGroup,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;

// 积分有效期上限（天），与后端 setting/operation_setting.MaxPointsExpireDays 一致。
export const POINTS_EXPIRE_DAYS_MAX = 3650;

// 有效期是**必选**的：漏设就变成永久有效，而「赠送多久」正是运营在这笔操作里必须
// 显式决定的事。前端不给默认选中，逼着人点一下。
const MODES = [
  { value: 'never', label: '永不过期' },
  { value: 'days', label: 'N 天后' },
  { value: 'date', label: '指定日期' },
];

// validatePointsExpire 返回错误文案（已翻译），通过时返回 null。
export function validatePointsExpire(form) {
  const mode = form?.points_expire_mode;
  if (!mode) return '请选择积分有效期';
  if (mode === 'days') {
    const days = Number(form.points_expire_days);
    if (!(days >= 1 && days <= POINTS_EXPIRE_DAYS_MAX)) {
      return `有效期需在 1~${POINTS_EXPIRE_DAYS_MAX} 天之间`;
    }
  }
  if (mode === 'date' && !form.points_expire_date) {
    return '请选择过期日期';
  }
  return null;
}

// pointsExpirePayload 把表单状态摊平成接口字段。到期时刻一律由服务端按北京时间计算，
// 这里只提交「模式 + 天数/日期」，不提交算好的时间戳（浏览器时区不可信）。
export function pointsExpirePayload(form) {
  const mode = form?.points_expire_mode;
  return {
    points_expire_mode: mode || '',
    points_expire_days:
      mode === 'days' ? Number(form.points_expire_days) || 0 : 0,
    points_expire_date: mode === 'date' ? form.points_expire_date || '' : '',
  };
}

/**
 * PointsExpireField 积分有效期选择器（三种模式 + 对应输入）。
 *
 * @param form     当前表单状态对象
 * @param onChange (field, value) => void，由父组件写回自己的表单状态
 * @param hint     可选的补充说明（例如签到「下周日过期」之类的口径提示）
 */
export default function PointsExpireField({ form, onChange, hint }) {
  const { t } = useTranslation();
  const mode = form?.points_expire_mode;

  return (
    <div className='mb-3'>
      <div className='mb-1 flex items-center gap-1'>
        <Text size='small'>{t('积分有效期')}</Text>
        <Text type='danger' size='small'>
          *
        </Text>
      </div>
      <RadioGroup
        type='button'
        value={mode}
        onChange={(e) => onChange('points_expire_mode', e.target.value)}
        style={{ width: '100%' }}
      >
        {MODES.map((m) => (
          <Radio key={m.value} value={m.value}>
            {t(m.label)}
          </Radio>
        ))}
      </RadioGroup>

      {mode === 'days' && (
        <div className='mt-2'>
          <InputNumber
            placeholder={t('天数，1~{{max}}', { max: POINTS_EXPIRE_DAYS_MAX })}
            value={form.points_expire_days}
            min={1}
            max={POINTS_EXPIRE_DAYS_MAX}
            precision={0}
            onChange={(val) =>
              onChange(
                'points_expire_days',
                val === '' || val == null ? '' : val,
              )
            }
            style={{ width: '100%' }}
            suffix={t('天')}
          />
        </div>
      )}

      {mode === 'date' && (
        <div className='mt-2'>
          <DatePicker
            type='date'
            format='yyyy-MM-dd'
            value={form.points_expire_date || undefined}
            onChange={(_, dateStr) =>
              onChange('points_expire_date', dateStr || '')
            }
            style={{ width: '100%' }}
          />
        </div>
      )}

      <Text type='tertiary' size='small' className='block mt-1'>
        {hint || t('到期未用完的部分自动作废；消费时优先扣除最早到期的积分。')}
      </Text>
    </div>
  );
}
