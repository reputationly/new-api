// 积分到期日一律按**北京时间**展示，与端口径一致。
//
// 到期时刻固定落在北京时间 23:59:59。用浏览器时区格式化时，UTC+8 以东的浏览器
// （如东京 UTC+9）会把它显示成第二天——用户看到的「过期日」比实际晚一天；而
// 明细里展示的若是发放日，西半球浏览器又会早一天。服务端所有到期判定都按北京时间，
// 展示必须跟着走，否则用户会拿着错一天的日期来对账。
const ymdFmt = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
});

function partsOf(ts) {
  const out = {};
  for (const p of ymdFmt.formatToParts(new Date(ts * 1000))) {
    if (p.type !== 'literal') out[p.type] = p.value;
  }
  return out;
}

// formatPointsExpiryDate 返回「MM-DD」；ts <= 0 表示永不过期，返回空串。
export function formatPointsExpiryDate(ts) {
  if (!ts || ts <= 0) return '';
  const p = partsOf(ts);
  return `${p.month}-${p.day}`;
}

// formatPointsExpiryFullDate 返回「YYYY-MM-DD」，批次明细里用（含年份更清楚）。
export function formatPointsExpiryFullDate(ts) {
  if (!ts || ts <= 0) return '';
  const p = partsOf(ts);
  return `${p.year}-${p.month}-${p.day}`;
}

// pickExpiringHighlights 从 lots/summary 里取出展示需要的三样东西。
//
// **Tag 上的数量与日期必须取自最近的一个到期日**（v2.5 §10.2）：
// expiring.quota 是 30 天内多个到期日的合计，配上最早日期会读成「这些全在那天过期」。
// 例：12-20 到期 10、01-15 到期 500 —— 标签必须是「10 积分 12-20 过期」，
// 不能是「510 积分 12-20 过期」；510 只在 Tooltip 里作为「30 天内共 510」出现。
export function pickExpiringHighlights(summary, maxLines = 3) {
  const next = Array.isArray(summary?.nextExpiries) ? summary.nextExpiries : [];
  const sorted = next
    .filter((e) => e && Number(e.expires_at) > 0)
    .slice()
    .sort((a, b) => Number(a.expires_at) - Number(b.expires_at));
  return {
    nearest: sorted[0] || null,
    lines: sorted.slice(0, Math.max(1, maxLines)),
    total30d: Number(summary?.expiring?.quota) || 0,
  };
}
