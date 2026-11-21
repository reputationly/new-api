import React, { useState } from 'react';
import { Button, Space, Typography } from '@douyinfe/semi-ui';
import { IconChevronDown, IconChevronRight } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import TimeSlotEditor from './TimeSlotEditor';

const { Text } = Typography;

// 页面顶部的「时段模板」折叠条。
//
// 模板是全局共用的（改一次时间，所有引用它的模型同步生效），但它**不是**日常操作：
// 建好之后运营主要是在模型里绑定。所以默认收起，只在标题上显示个数。
const TimeSlotTemplates = ({ value, onChange }) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);

  let count = 0;
  try {
    const parsed = JSON.parse(value || '{}');
    count = Object.keys(parsed?.windows || {}).length;
  } catch (e) {
    count = 0;
  }

  return (
    <div
      className='rounded-2xl'
      style={{
        border: '1px solid var(--semi-color-border)',
        marginBottom: 16,
        background: 'var(--semi-color-bg-1)',
      }}
    >
      <div className='flex items-center justify-between px-4 py-3'>
        <Space>
          <Button
            size='small'
            theme='borderless'
            icon={open ? <IconChevronDown /> : <IconChevronRight />}
            onClick={() => setOpen((v) => !v)}
          />
          <Text strong>{t('时段模板')}</Text>
          <Text type='tertiary' size='small'>
            {t('（{{n}} 个）', { n: count })}
          </Text>
          <Text type='tertiary' size='small'>
            {t('全局共用：在这里定义时间，去下面每个模型里绑定并填价')}
          </Text>
        </Space>
      </div>
      {open ? (
        <div className='px-4 pb-3'>
          <TimeSlotEditor value={value} onChange={onChange} />
        </div>
      ) : null}
    </div>
  );
};

export default TimeSlotTemplates;
