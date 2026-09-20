import React from 'react';
import { buildTimeWindowRows } from '../../../helpers';

// 「动态折扣」角标的悬浮内容：逐条列出时段规则，并标出此刻生效的那一档。
//
// 角标本身只有固定四个字（它与模型名共用卡片头部那一行的宽度，而运营的时段模板名
// 长度无上限），规则明细全部落在这里。
//
// 卡片视图与表格视图共用一个组件：两处各写一份必然漂移，而漂移的表现是同一个模型
// 在两个视图里显示不同的时段价目。行构造直接复用详情页那份 buildTimeWindowRows，
// 连「其余时段」那一行也一并带上——只列配了规则的时段，用户看不出未命中时按几折付。
const TimeRulesTooltip = ({ group, modelName, groupTimeRatio, t }) => {
  const rows = buildTimeWindowRows({
    usableGroup: { [group]: group },
    modelEnableGroups: [group],
    groupTimeRatio,
    modelName,
    normalLabel: t('其余时段'),
  });
  if (rows.length === 0) return null;

  return (
    <div className='text-xs leading-5'>
      <div className='mb-1 opacity-80'>{t('分时定价')}</div>
      {rows.map((r) => (
        <div key={r.key}>
          {r.range} · {r.discount ? r.discount.text : `${r.ratio}x`}
          {r.active ? ` · ${t('生效中')}` : ''}
        </div>
      ))}
    </div>
  );
};

export default TimeRulesTooltip;
