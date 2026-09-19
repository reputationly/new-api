/**
 * 分组折扣的展示口径。纯计算、不引 UI 依赖，手机端可直接 import
 * （同 priceFormat.js / videoMatrix.js 的约定）——两端各抄一份必然漂移，
 * 而这里一旦漂移，用户在两个端会看到不同的折扣数字。
 */

/**
 * 把最终倍率翻译成折扣标签。
 *
 * 入参是后端算好的 Final（分组倍率 × 模型折扣，见 controller/pricing.go 的
 * resolveGroupModelRatio），与计费、日志用的是同一个数，所以标签上的折扣就是
 * 用户真实付的折扣。
 *
 * 返回 null 表示不该显示标签：
 *   ratio >= 1  —— 没打折（新型号原价，标个「10折」是噪音）
 *   ratio < 0 / 非有限数 —— 倍率异常，宁可不显示也不能显示错的
 *
 * 配色按力度分三档，都用浅底：模型广场一屏几十个模型，实心色块会盖过模型名本身。
 */
export const getGroupDiscountInfo = (ratio) => {
  // 先挡 null / undefined / 空串：Number(null) 和 Number('') 都是 0，
  // 会一路走到下面的「免费」分支——倍率没下发却显示免费，是最坏的一种错。
  if (ratio === null || ratio === undefined || ratio === '') return null;
  const r = Number(ratio);
  if (!Number.isFinite(r) || r >= 1 || r < 0) return null;
  if (r === 0) {
    return { text: '免费', color: 'red', ratio: r };
  }
  // 0.85 -> 8.5折；0.5 -> 5折（整数不拖 .0）
  const tenths = Math.round(r * 1000) / 100;
  const text = `${Number.isInteger(tenths) ? tenths : tenths.toFixed(1)}折`;
  const color = r <= 0.7 ? 'red' : r <= 0.9 ? 'orange' : 'amber';
  return { text, color, ratio: r };
};

/**
 * 折扣标签的展示色 -> 手机端色值。
 *
 * antd-mobile 没有 Semi 那套 color token，两端各挑各的颜色会导致同一个折扣
 * 在两个端显示成不同颜色。这里把映射固定下来。
 */
export const DISCOUNT_HEX = {
  red: { bg: '#fff1f0', fg: '#cf1322' },
  orange: { bg: '#fff7e6', fg: '#d46b08' },
  amber: { bg: '#fffbe6', fg: '#ad8b00' },
  // 时段折扣单独一档冷色：它与上面三档（分组折扣力度）是两个维度，同一个模型
  // 可能两种标签并排，用同色系会让人以为是同一件事的两种说法。
  cyan: { bg: '#e6fffb', fg: '#08979c' },
};

/**
 * 把后端下发的时段折扣数据翻译成展示信息。
 *
 * 入参是 /api/pricing 的 `group_time_ratio[group][model]`，形如：
 *   { active, best_ratio, label, until, windows: [{label,start,end,days,value,ratio,active}] }
 *
 * `best_ratio` 与每档的 `ratio` 都是后端算好的**最终倍率**，与列表价读的
 * group_model_ratio 同口径。所以这里只做「倍率 → 折扣文案」的翻译，不做任何乘法：
 * 时段折扣取代模型折扣而非叠乘（见 ResolveGroupRatioAt 的 Layer 4），
 * 「常规 8 折 / 空闲时段 5.6 折」是两个能直接比较的绝对值，用户不必心算。
 *
 * **active / until 是后端算好的，这里一个时间判断都不做。** 客户端时钟和时区都不可信
 * ——用户把手机时区改成 UTC，前端自算就会显示错误的「空闲时段中」，而价格是后端算的，
 * 于是标签和价格当场互相矛盾。
 *
 * 返回 null 表示不显示任何时段相关 UI（没配规则、或配了但折扣无效）。
 *
 * 不返回「折前价」之类的数：列表价的划线原价由既有的 originalInputPrice /
 * originalPrice 负责，而它们划的是**完全不打折**的价。时段系数已经被后端折进
 * usedGroupRatio，那套逻辑不改就已经是对的——在这里再算一个折前价，等于给同一件事
 * 造第二个口径。
 *
 * @returns {null | {
 *   active: boolean,  // 此刻是否在优惠时段内
 *   text: string,     // 角标文案
 *   color: string,    // DISCOUNT_HEX 的键
 *   until: string,    // 当前状态结束时刻（RFC3339），可能为空
 *   windows: Array,   // 详情页分时价格表用
 * }}
 */
export const getTimeDiscountInfo = (entry) => {
  if (!entry || !Array.isArray(entry.windows) || entry.windows.length === 0) {
    return null;
  }
  // best_ratio >= 1 表示全天各档都不算折扣——配了规则但没有优惠，标个角标只是噪音
  const bestInfo = getGroupDiscountInfo(entry.best_ratio);
  if (!bestInfo) return null;

  if (entry.active) {
    return {
      active: true,
      text: entry.label ? `${entry.label}进行中` : '空闲时段进行中',
      color: 'cyan',
      until: entry.until || '',
      windows: entry.windows,
    };
  }

  return {
    active: false,
    // 绝对折扣，不说「再」：时段折扣取代模型折扣，不是在它之上再打一次
    text: `空闲时段 ${bestInfo.text}`,
    color: 'cyan',
    until: entry.until || '',
    windows: entry.windows,
  };
};

const WEEKDAY_LABELS = ['日', '一', '二', '三', '四', '五', '六'];

/**
 * 把时段窗口的生效日翻译成可读文案。
 *
 * 与管理端的预设是同一套口径（空=每天、1-5=工作日、0&6=周末），两端各写一份的话，
 * 管理员按「工作日」预设配完，用户端可能显示成「周一/周二/周三/周四/周五」。
 */
export const formatWindowDays = (days) => {
  if (!Array.isArray(days) || days.length === 0) return '每天';
  const set = [...new Set(days)].sort((a, b) => a - b);
  const key = set.join(',');
  if (key === '1,2,3,4,5') return '工作日';
  if (key === '0,6') return '周末';
  if (key === '0,1,2,3,4,5,6') return '每天';
  return set.map((d) => `周${WEEKDAY_LABELS[d] ?? d}`).join('、');
};

/**
 * 从后端下发的 RFC3339 时刻里取出「HH:MM」。
 *
 * **直接截字符串，不经过 Date。** 后端给的时刻已经带了时段自己的时区偏移
 * （如 2026-09-19T08:00:00+08:00），用 `new Date(...).getHours()` 会按**浏览器**
 * 时区重新渲染——用户手机设成 UTC 时，「至 08:00」会显示成「至 00:00」，而那个
 * 08:00 才是规则里写的那个数。
 */
export const formatTimeUntil = (until) => {
  if (typeof until !== 'string') return '';
  const m = until.match(/T(\d{2}):(\d{2})/);
  return m ? `${m[1]}:${m[2]}` : '';
};

const clockMinutes = (s) => {
  const m = /^(\d{1,2}):(\d{2})$/.exec(String(s ?? '').trim());
  return m ? Number(m[1]) * 60 + Number(m[2]) : null;
};

/**
 * 窗口是否跨午夜。
 *
 * **按分钟数比较，不按字符串。** `'9:00' > '18:00'` 在字符串比较下为真（'9' > '1'），
 * 会把一个普通白天时段渲染成「9:00-次日18:00」。后端 parseClock 已经收严为必须
 * 补零，这里仍按数值算：这是个被 classic 与 mobile 共用的纯函数，不该依赖一个
 * 它自己看不见的上游不变量——而这次的 bug 恰恰就是那个不变量没兜住。
 *
 * 导出是给管理端编辑器复用的：它原本自己写了一份字符串比较，两份判定必然分叉。
 */
export const crossesMidnight = (win) => {
  const start = clockMinutes(win?.start);
  const end = clockMinutes(win?.end);
  return start !== null && end !== null && start > end;
};

/**
 * 时段窗口的区间文案。跨午夜时显式标出来——「22:00-06:00」不标的话，
 * 会被读成「每天只有那 8 小时里的某一段」还是「跨到第二天」全凭猜。
 */
export const formatWindowRange = (win) => {
  if (!win) return '';
  return crossesMidnight(win)
    ? `${win.start}-次日${win.end}`
    : `${win.start}-${win.end}`;
};

/**
 * 时段档位的一行文案，供角标 tooltip 使用。
 *
 * 折扣取 `w.ratio`（最终倍率）而不是 `w.value`（配置系数）：两者在分组基础倍率
 * 不为 1、或该用户有档位折扣时会分叉，于是 tooltip 说「×0.56」而紧挨着的角标
 * 说「5.0折」。角标与 tooltip 是同一次悬停里前后脚看到的两个数，必须同口径。
 */
export const formatWindowLine = (w) => {
  const d = getGroupDiscountInfo(w?.ratio);
  const discount = d ? d.text : `${Number(Number(w?.ratio ?? 1).toFixed(4))}x`;
  return `${formatWindowDays(w?.days)} ${formatWindowRange(w)} ${discount}`;
};
