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
  // 中性：用于「该时段更贵」的提示。青色在本页面通篇表示优惠，
  // 拿它去标一个涨价档等于在推销涨价。
  neutral: { bg: '#f5f5f5', fg: '#595959' },
};

/**
 * 把后端下发的时段折扣数据翻译成展示信息。
 *
 * 入参是 /api/pricing 的 `group_time_ratio[group][model]`，形如：
 *   { active, best_ratio, best_label, normal_ratio, label, until,
 *     windows: [{label,start,end,days,tz,value,ratio,active}] }
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
 *   color: string,    // DISCOUNT_HEX 的键：比常规便宜用 cyan，更贵用 neutral
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

  // 参照物是**常规倍率**（未命中任何时段时的价），不是「此刻的最终倍率」。
  //
  // 角标要回答的是「这个时段比平时贵还是便宜」。拿此刻的最终倍率做参照，在命中
  // 时段时它就等于那个时段自己的倍率——一个档跟它自己比，恒不成立，于是
  // 「常规 0.35、高峰 0.5」这种配置在高峰时段内会渲染成青色促销角标，
  // 而用户此刻付的比平时贵。
  const normal = Number(entry.normal_ratio);
  const isCheaper = (ratio) =>
    !Number.isFinite(normal) || Number(ratio) < normal;

  if (entry.active) {
    // 命中档的倍率：拿它跟常规比，才知道此刻是优惠还是溢价
    const activeRatio = entry.windows.find((w) => w.active)?.ratio;
    return {
      active: true,
      text: entry.label ? `${entry.label}进行中` : '空闲时段进行中',
      color: isCheaper(activeRatio) ? 'cyan' : 'neutral',
      until: entry.until || '',
      windows: entry.windows,
    };
  }

  // 档位名取自后端（best_label），不能硬编码「空闲时段」：管理员完全可能只配一个
  // **更贵**的高峰档（常规倍率 0.35、高峰 0.5），硬编码会显示「空闲时段 5折」
  // ——标签指向错的时段，数字也让人以为非高峰时反而只有 5 折。
  //
  // 同价的时段可能不止一个（「上午工作时间」与「下午工作时间」都配 0.5）。
  // 只点其中一个名字会让用户以为另一段不享受——best_label 取的是第一个命中最优值的
  // 档位，另一个同价档被静默略过。多于一个时改说「等 N 个时段」。
  const tiedCount = entry.windows.filter(
    (w) => Number(w?.ratio) === Number(entry.best_ratio),
  ).length;
  const label = entry.best_label || '空闲时段';
  return {
    active: false,
    // 绝对折扣，不说「再」：时段倍率取代模型倍率，不是在它之上再打一次
    text:
      tiedCount > 1
        ? `${label}等${tiedCount}个时段 ${bestInfo.text}`
        : `${label} ${bestInfo.text}`,
    color: isCheaper(entry.best_ratio) ? 'cyan' : 'neutral',
    until: entry.until || '',
    windows: entry.windows,
  };
};

// 与后端 setting/ratio_setting/group_time_ratio.go 的 defaultTimeRatioZone 一致。
// 只用于「要不要在界面上标出时区」，不参与任何时间计算。
export const DEFAULT_TIME_ZONE = 'Asia/Shanghai';

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
  const range = crossesMidnight(win)
    ? `${win.start}-次日${win.end}`
    : `${win.start}-${win.end}`;
  // 非默认时区必须标出来：窗口可以配 per-window 时区，而「09:00-12:00」不说是
  // 哪个时区的，用户没法判断它对自己意味着几点。默认时区不标，免得每行都挂一个
  // 对所有人都一样的尾巴。
  const tz = String(win.tz ?? '').trim();
  return tz && tz !== DEFAULT_TIME_ZONE ? `${range}(${tz})` : range;
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

/**
 * 构造「分时定价」表的行。classic 与 mobile 共用同一份——两端各写一份必然漂移，
 * 而漂移的表现是同一个模型在手机和电脑上看到不同的分时价目。
 *
 * **刻意不走 getTimeDiscountInfo**：那个函数回答的是「值不值得出一个角标」，
 * 在所有档位都不打折（best_ratio >= 1）时返回 null。拿它做入口判空，会让
 * 「窗口全是 ×1 原价档、模型折扣 0.35」这种配置整张表消失，连「其余时段 3.5折」
 * 那一行也没了——正是那一行要解决的问题。
 *
 * 逐分组列是必要的：时段规则按使用分组配，同一个模型在 default 有夜间折扣、
 * 在 premium 没有，是完全正常的配置。
 */
export const buildTimeWindowRows = ({
  usableGroup,
  modelEnableGroups,
  groupTimeRatio,
  modelName,
  normalLabel,
}) => {
  const groups = Object.keys(usableGroup || {})
    .filter((g) => g !== '' && g !== 'auto')
    .filter((g) => (modelEnableGroups || []).includes(g));

  const rows = [];
  groups.forEach((group) => {
    const entry = groupTimeRatio?.[group]?.[modelName];
    const windows = Array.isArray(entry?.windows) ? entry.windows : [];
    if (windows.length === 0) return;

    let anyActive = false;
    windows.forEach((w, idx) => {
      // 生效档由后端标（TimeWindowView.Active，与 pickTimeRule 同口径），
      // 不按 label 反推：label 互为子串时（「深夜」与「深夜加强」）字符串匹配
      // 会把两档都标成生效。也不与 entry.active 取交集——那个字段答的是
      // 「有没有优惠」，命中 ×1 原价档时为假，会让当前行整个丢失。
      const active = Boolean(w.active);
      if (active) anyActive = true;
      rows.push({
        key: `${group}-${idx}`,
        group,
        // 带上档位名：角标说的是「上午工作时间等2个时段」，详情表只列时间区间的话
        // 用户对不上号——他看到的那个名字在表里找不到。
        label: w.label || '',
        range: [w.label, formatWindowDays(w.days), formatWindowRange(w)]
          .filter(Boolean)
          .join(' '),
        // 展示该时段的**最终倍率**对应的折扣，而不是配置倍率：用户要知道的是
        // 「这个时段几折」，配置值是管理端的事
        discount: getGroupDiscountInfo(w.ratio),
        ratio: Number(w.ratio),
        active,
        until: entry.until || '',
      });
    });

    // 「其余时段」这一行是必须的：只列配了规则的时段，表里两行 5 折，其余时间是
    // 3.5 折还是 8 折用户完全看不出来，也就说不出自己此刻按哪一档付钱。
    // 没有任何窗口生效时它就是当前档。
    if (Number.isFinite(Number(entry.normal_ratio))) {
      rows.push({
        key: `${group}-normal`,
        group,
        label: normalLabel,
        range: normalLabel,
        discount: getGroupDiscountInfo(entry.normal_ratio),
        ratio: Number(entry.normal_ratio),
        active: !anyActive,
        until: entry.until || '',
      });
    }
  });
  return rows;
};
