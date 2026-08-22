import React from 'react';
import { Banner, Button, Tag } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';

/**
 * 失配提示条：被渠道挂载、但分组配置里没有的分组名。
 *
 * 这类渠道当前**完全不可用**——middleware/auth.go 判「分组已被弃用」直接拒绝，
 * 而在改造前没有任何页面能发现，只能等用户报错反推。现网大概率已经存在这类失配。
 */
export default function MismatchBanner({ unconfigured = [], onCreateMissing }) {
  const { t } = useTranslation();
  if (!unconfigured.length) return null;

  return (
    <Banner
      type='warning'
      closeIcon={null}
      className='mb-3'
      description={
        <div className='text-sm leading-6'>
          <div>
            {t(
              '检测到 {{n}} 个分组被渠道引用但未配置，这些渠道当前完全不可用：',
              {
                n: unconfigured.length,
              },
            )}
          </div>
          <div className='mt-1 flex flex-wrap items-center gap-2'>
            {unconfigured.map((item) => (
              <Tag key={item.name} color='orange' shape='circle'>
                {item.name}
                {item.channel_count > 0
                  ? `（${t('{{n}} 个渠道', { n: item.channel_count })}）`
                  : ''}
              </Tag>
            ))}
            <Button
              size='small'
              theme='solid'
              onClick={() => onCreateMissing?.(unconfigured.map((x) => x.name))}
            >
              {t('一键补建')}
            </Button>
          </div>
        </div>
      }
    />
  );
}
