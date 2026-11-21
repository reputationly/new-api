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

// catalog 模式（动态目录价）时段的价目摘要：「输入¥1 输出¥4 缓存¥0.02」。
// 分量顺序固定（输入/输出/缓存命中优先），最多列 max 个，超出补「…」——
// tooltip 一行不折行，分量全列会把浮层撑得很宽。
const TIME_PRICE_COMPONENTS = [
  ['input', '输入'],
  ['output', '输出'],
  ['cache', '缓存命中'],
  ['create_cache', '缓存创建'],
  ['image', '图片'],
  ['audio_input', '音频输入'],
  ['audio_output', '音频输出'],
  ['per_call', '按次'],
];

const formatTimePriceValue = (v) => {
  const n = Number(v);
  if (!Number.isFinite(n)) return '';
  return `¥${Number.isInteger(n) ? n : Number(n.toFixed(4))}`;
};

export const formatTimePrices = (prices, t, max = 3) => {
  if (!prices || typeof prices !== 'object') return '';
  const tr = typeof t === 'function' ? t : (s) => s;
  const parts = [];
  for (const [comp, label] of TIME_PRICE_COMPONENTS) {
    const price = Number(prices[comp]);
    if (!Number.isFinite(price) || price <= 0) continue;
    parts.push(`${tr(label)}${formatTimePriceValue(price)}`);
    if (parts.length >= max) break;
  }
  const total = TIME_PRICE_COMPONENTS.filter(
    ([comp]) => Number(prices[comp]) > 0,
  ).length;
  return parts.join(' ') + (total > max ? ' …' : '');
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

// 窗口的区间列表：优先后端下发的 segments（多段/补集展开后的形态），
// start/end/days 旧形状兜底。段本身不带时区——tz 是窗口级字段，这里补回去，
// 否则非默认时区窗口的 (tz) 标注会在多段渲染时丢掉。
const windowSegments = (win) => {
  const segs =
    Array.isArray(win?.segments) && win.segments.length > 0
      ? win.segments
      : win?.start && win?.end
        ? [{ start: win.start, end: win.end, days: win.days }]
        : [];
  return segs.map((s) => ({ ...s, tz: s.tz ?? win?.tz }));
};

/**
 * 多段窗口的区间文案。天数相同的段合并成「工作日 09:00-12:00、14:00-18:00」；
 * 天数不同的逐段标（「周六 10:00-12:00」），否则「周末+工作日」混排的段会
 * 让用户把周六的时段读成工作日的。
 */
export const formatWindowSegments = (win) => {
  // 补集窗口（「其余时间」）：后端把它展开成的多段只是「除 X 以外」的
  // 实现细节，渲染出来既长又读不出配置意图，统一收成一行参照描述。
  if (win?.complement_of) {
    return `除「${win.complement_of}」以外`;
  }
  const segs = windowSegments(win);
  if (segs.length === 0) return '';
  const dayKey = (s) =>
    JSON.stringify([...(s.days || [])].sort((a, b) => a - b));
  const ranges = segs.map((s) => formatWindowRange(s));
  if (new Set(segs.map(dayKey)).size === 1) {
    return [formatWindowDays(segs[0].days), ranges.join('、')]
      .filter(Boolean)
      .join(' ');
  }
  return segs
    .map((s) =>
      [formatWindowDays(s.days), formatWindowRange(s)]
        .filter(Boolean)
        .join(' '),
    )
    .join('、');
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
        range: [w.label, formatWindowSegments(w)].filter(Boolean).join(' '),
        // 价目模式：catalog = 动态目录价（绝对价目，分组折扣照常叠加），
        // factor/缺省 = 动态折扣（系数乘常规价）。同模型不同窗口可混配。
        priceMode: w.price_mode === 'catalog' ? 'catalog' : 'factor',
        // catalog 模式下「实际改了价」的分量绝对价目（后端已过滤系数≈1 的分量）。
        prices: w.prices && typeof w.prices === 'object' ? w.prices : null,
        // 折数口径按模式分：
        //   catalog —— 用 discount_ratio（与时刻无关的固定折扣）。w.ratio 是
        //     「组折扣 × 价目比」的合成值，标成折会把价目调整误读成折扣力度；
        //   factor  —— 保持 w.ratio（该时段最终倍率），「这个时段几折」成立。
        discount:
          w.price_mode === 'catalog'
            ? getGroupDiscountInfo(
                Number(w.discount_ratio) > 0 ? w.discount_ratio : null,
              )
            : getGroupDiscountInfo(w.ratio),
        ratio: Number(w.ratio),
        // 该时段的目录倍率（不含折扣）与固定折扣：详情页要同时列「目录价 / 折后价」，
        // 而广场卡片只显示折后价。
        inDir: Number(w.input_directory_ratio) || 0,
        outDir: Number(w.output_directory_ratio) || 0,
        fixedDiscount: Number(entry.discount_ratio) || 0,
        // 相对常规价：>1 是紧缺时段加价、<1 是空闲优惠。折扣角标讲的是合同折扣，
        // 这一列讲的是「这个时段比常规贵/便宜多少」，两者不要混在一个数字里。
        relative: Number(
          (Number(w.ratio) / Number(entry.normal_ratio)).toFixed(4),
        ),
        active,
      });
    });

    // 「其余时段」这一行是必须的：只列配了规则的时段，表里两行 5 折，其余时间是
    // 3.5 折还是 8 折用户完全看不出来，也就说不出自己此刻按哪一档付钱。
    // 没有任何窗口生效时它就是当前档。
    // 例外：后端判定已配窗口并集覆盖整周（normal_applies=false）时常规价
    // 永远不会命中，那一行是死信息——高峰+空闲这种「两个时段组成全集」的
    // 配置下用户只想看到自己配的两档。字段缺省（旧后端）按 true 处理。
    if (
      Number.isFinite(Number(entry.normal_ratio)) &&
      entry.normal_applies !== false
    ) {
      rows.push({
        key: `${group}-normal`,
        group,
        label: normalLabel,
        range: normalLabel,
        discount: getGroupDiscountInfo(entry.normal_ratio),
        ratio: Number(entry.normal_ratio),
        // 其余时段 = 常规价：目录倍率相对值 1/1
        inDir: 1,
        outDir: 1,
        fixedDiscount: Number(entry.discount_ratio) || 0,
        relative: 1,
        active: !anyActive,
      });
    }
  });
  return rows;
};

/**
 * 取某个 (线路, 模型) 的**常规价终值**（未命中任何时段时的倍率）。
 *
 * 折扣角标必须用它而不是 usedGroupRatio：后者是「此刻价」，含时段系数，会让同一个客户
 * 同一个模型白天 9 折、紧缺时段 11 折——折扣率随时钟变，合同与报表都没法解释。
 * 常规价来自后端下发的 group_time_ratio[线路][模型].normal_ratio（终值口径，已含用户档
 * 乘数）；没有分时规则时回退到 usedGroupRatio。
 */
export const getNormalRatio = (groupTimeRatio, group, modelName, fallback) => {
  const v = groupTimeRatio?.[group]?.[modelName]?.normal_ratio;
  return Number.isFinite(Number(v)) ? Number(v) : fallback;
};

/**
 * 取某个 (线路, 模型) 的时段视图（后端算好的档位、此刻生效项与常规价）。
 * 前端不做任何时间判断：客户端时钟/时区都不可信，算出来只会与实扣打架。
 */
export const getTimeRatioView = (groupTimeRatio, group, modelName) =>
  groupTimeRatio?.[group]?.[modelName] || null;

/**
 * 价格列相对「常规价」的方向：更便宜（划线）、更贵（挂标签）、持平（什么都不做）。
 * 返回 null 表示没有分时规则、或此刻就是常规价。
 */
export const getTimePriceDelta = (
  groupTimeRatio,
  group,
  modelName,
  currentRatio,
) => {
  const view = getTimeRatioView(groupTimeRatio, group, modelName);
  if (!view) return null;
  const normal = Number(view.normal_ratio);
  const current = Number(currentRatio);
  if (!Number.isFinite(normal) || !Number.isFinite(current) || normal <= 0)
    return null;
  if (Math.abs(normal - current) < 1e-9) return null;
  return {
    normal,
    current,
    cheaper: current < normal,
    label: view.label || '',
    active: Boolean(view.active),
  };
};

/**
 * 时间档标签的显示文本：**限长**。
 *
 * 时段模板名是运营自己起的，可能很长（「工作时间（上午）」「夜间低谷·次日」）。
 * 模型广场卡片的右上角是「时段 + 折扣（+ 积分抵扣）」一组角标，名字那行是 truncate 的，
 * 角标不设上限就会把模型名压成「deepseek-v…」——这是本项目踩过一次的坑。
 * 这里只截显示文本，完整名字由调用方放进 tooltip。
 */
export const clampTimeLabel = (label, max = 4) => {
  const s = String(label || '').trim();
  if (!s) return '';
  return s.length > max ? s.slice(0, max) + '…' : s;
};
