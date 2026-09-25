import React from 'react';
import { Banner, Tag } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';

const SOURCE_LABEL = {
  auto_groups: '自动分组顺序',
  group_group_ratio: '按线路覆盖',
  special_usable: '可用线路规则',
  topup_ratio: '充值倍率',
  rate_limit: '限流',
  points: '积分白名单',
};

/**
 * 悬空引用提示条：配置里引用了一个不存在的线路名。
 *
 * 与 MismatchBanner 是反方向的失配：那边是「渠道挂了、配置没有」，这边是
 * 「配置引用了、线路没有」。后者不会报错，只会静默不生效——auto 池里一个不存在的
 * 名字永远选不中，一条指向已删线路的可用规则永远不命中。数据来自
 * /api/group/overview 的 dangling 字段。
 */
export default function DanglingBanner({ dangling = [] }) {
  const { t } = useTranslation();
  if (!dangling.length) return null;

  return (
    <Banner
      type='warning'
      closeIcon={null}
      className='mb-3'
      description={
        <div className='text-sm leading-6'>
          <div>{t('以下配置引用了不存在的线路，当前静默不生效：')}</div>
          <div className='mt-1 flex flex-wrap items-center gap-2'>
            {dangling.map((item, idx) => (
              <Tag
                key={`${item.source}_${item.owner}_${item.name}_${idx}`}
                color='orange'
                shape='circle'
              >
                {t(SOURCE_LABEL[item.source] || item.source)}
                {item.owner ? ` · ${item.owner}` : ''}
                {' → '}
                {item.name}
              </Tag>
            ))}
          </div>
        </div>
      }
    />
  );
}
