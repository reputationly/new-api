import React from 'react';
import { buildTimeWindowRows } from '../../../helpers';

// 「动态折扣 / 动态目录价」角标的悬浮内容：逐条列出时段规则，并标出此刻生效的那一档。
//
// 角标本身只有固定四个字（它与模型名共用卡片头部那一行的宽度，而运营的时段模板名
// 长度无上限），规则明细全部落在这里。
//
// 卡片视图与表格视图共用一个组件：两处各写一份必然漂移，而漂移的表现是同一个模型
// 在两个视图里显示不同的时段价目。行构造直接复用详情页那份 buildTimeWindowRows，
// 连「其余时段」那一行也一并带上——只列配了规则的时段，用户看不出未命中时按几折付。
//
// 两种价目模式（互斥，按窗口判定）：
//   catalog —— 动态目录价：行内列「已配置且改了价」的分量绝对价目，折数取
//              与时刻无关的固定折扣（discount_ratio）。w.ratio 是「组折扣 ×
//              价目比」的合成值，标成折会把价目调整误读成折扣力度。
//   factor  —— 动态折扣：维持「该时段最终几折」（w.ratio），那本来就是它的语义。
const TimeRulesTooltip = ({ group, modelName, groupTimeRatio, t }) => {
  const rows = buildTimeWindowRows({
    usableGroup: { [group]: group },
    modelEnableGroups: [group],
    groupTimeRatio,
    modelName,
    normalLabel: t('其余时段'),
  });
  if (rows.length === 0) return null;

  // 标题按窗口模式聚合：全部 catalog 才叫「动态目录价」；混配或全 factor
  // 回落「分时定价」。「其余时段」行没有 priceMode，不参与判定。
  const modes = new Set(
    rows.filter((r) => r.priceMode).map((r) => r.priceMode),
  );
  const allCatalog = modes.size === 1 && modes.has('catalog');

  // 每行不折行。Semi 的 .semi-tooltip-wrapper 硬编码 max-width:240px，
  // 「工作时间（上午） 工作日 09:00-12:00 · 5折」这种一行会被折成两行，
  // 读起来像两条规则。配合调用处的 maxWidth:'none'，由最长的一行决定浮层宽度。
  return (
    <div className='text-xs leading-5' style={{ whiteSpace: 'nowrap' }}>
      <div className='mb-1 opacity-80'>
        {allCatalog ? t('动态目录价') : t('分时定价')}
      </div>
      {rows.map((r) => {
        // catalog 行的折数就是「顶行已写的固定折扣」，再列一遍是重复；
        // 价目明细也不在这里铺——明细交给详情页（点开卡片即可）。
        // factor 行的折数是「该时段最终几折」，顶行没有这个信息，必须保留。
        const rowDiscount =
          r.priceMode === 'factor' && r.discount ? r.discount.text : '';
        return (
          <div key={r.key}>
            {r.range}
            {rowDiscount ? ' · ' + rowDiscount : ''}
            {r.active ? ' · ' + t('生效中') : ''}
          </div>
        );
      })}
      <div className='mt-1 opacity-70'>{t('点击查看详情')}</div>
    </div>
  );
};

export default TimeRulesTooltip;
