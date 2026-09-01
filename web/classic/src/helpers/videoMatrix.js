// 视频计费矩阵的纯计算部分（docs/video-billing-matrix-design.md）。
//
// 单独成模块、不引任何 UI 依赖，是为了让手机端能**直接 import 而不是抄一份**：
// helpers/utils.jsx 会传染 Semi 等桌面依赖，被 mobile 的 vite 配置整模块换成
// src/shims/classic-utils.jsx，那条路子只能靠手抄同步。排序权重那个 bug
// （4k 排到 480p 前面、'5s' 秒数塌成 0）就是因为同一段逻辑被抄了三份、
// 修一份漏两份。放这里就只有一份。

/**
 * 分辨率的排序权重。
 *
 * 不能直接 parseInt——`parseInt('4k', 10)` 得到 4，是个有限数，会让 4k 排到
 * 480p 前面，而「先判有限数再兜底 4k」的写法里那个兜底分支永远走不到。
 */
export const videoResolutionRank = (r) => {
  const s = String(r ?? '').trim();
  // 兜底行永远排最后：它是「其余所有分辨率」，放在具体档位之间会割裂表格
  if (s === '*') return Number.MAX_SAFE_INTEGER;
  // k 档按 n × 1080 折算成序数。写死 /^4k$/ 会让 LTX-2.5 的 2K 落进下面的
  // \d+p 分支——它匹配不上，返回 0，于是 2K 排到 480p 前面。取值只用于排序，
  // 单调即可，不必是真实短边（2K 的真实短边随部署而变：LTX 是 1408）。
  const k = s.match(/^(\d+)k$/i);
  if (k) return Number(k[1]) * 1080;
  const m = s.match(/^(\d+)\s*p?$/i);
  return m ? Number(m[1]) : 0;
};

/**
 * 秒数列的排序权重。
 *
 * 后端的校验与查表用的都是「前导整数」（setting/ratio_setting/video_pricing.go
 * 的 leadingDigits），所以 '5s' / '5秒' 都是合法列名。前端若用 Number() 解析，
 * 它们会变成 NaN 或被 `|| 0` 塌成 0，同一批列彼此无序。必须和后端同一套解析。
 */
export const videoSecondsRank = (c) => {
  const m = String(c ?? '')
    .trim()
    .match(/^\d+/);
  return m ? Number(m[0]) : 0;
};

/**
 * per_second 一维表升二维时用的虚拟列名。
 *
 * 必须是哨兵值而不是「秒」这类可读文本：渲染端普遍写着 `${column} 秒`
 * （per_call 的列名就是纯数字），拿可读文本去拼会得到「秒 秒」。哨兵值逼着
 * 渲染端为 per_second 显式分支，而不是让它悄悄落进 per_call 的模板。
 */
export const VIDEO_PER_SECOND_COLUMN = '__per_second__';

/**
 * 把视频计费矩阵的所有格子摊平成 [{resolution, column, priceUSD, originalPriceUSD}]。
 *
 * 列的语义随 mode 变：token 是「输入是否含视频」，per_call 是秒数，
 * per_second 只有一列（每秒单价），由本函数把一维表升成二维。
 *
 * originalPriceUSD 是**折前价**（少乘一个分组倍率），口径与按量/按次两条路的
 * originalInputPrice / originalPrice 一致：仅在 0 <= 倍率 < 1 时给，其余为 null。
 * 倍率 > 1 是涨价，划线会显示成「原价更便宜」，比不显示更糟。
 */
export const flattenVideoMatrix = (videoPricing, groupRatio) => {
  if (!videoPricing?.mode) return [];
  const isToken = videoPricing.mode === 'token';
  const isPerSecond = videoPricing.mode === 'per_second';
  // per_second 的后端表是一维的 { 分辨率: $/秒 }，先升成二维走同一套摊平逻辑
  let table;
  if (isToken) table = videoPricing.token || {};
  else if (isPerSecond) {
    table = {};
    Object.entries(videoPricing.per_second || {}).forEach(([res, usd]) => {
      table[res] = { [VIDEO_PER_SECOND_COLUMN]: usd };
    });
  } else table = videoPricing.per_call || {};

  // 0 是合法倍率（免费分组），不能用 `|| 1` 兜底——那会把免费分组显示成原价。
  // 只有 undefined / 非数才回退 1（详情页展示基础单价时就不传）。
  const raw = Number(groupRatio);
  const gr = Number.isFinite(raw) && raw >= 0 ? raw : 1;
  const discounted = gr < 1;

  const rows = [];
  Object.entries(table).forEach(([resolution, cols]) => {
    Object.entries(cols || {}).forEach(([column, usd]) => {
      const base = Number(usd);
      // 过滤的是「未配置的格子」，判据必须用**折算前**的原始价：
      // 折算后判的话，免费分组会让所有格子变 0 而被整表滤空。
      if (!Number.isFinite(base) || base <= 0) return;
      rows.push({
        resolution,
        column,
        priceUSD: base * gr,
        originalPriceUSD: discounted ? base : null,
      });
    });
  });

  // 分辨率按短边升序，同分辨率内「不含视频」在前——与配置页和供应商价目表同序，
  // 便于逐格对照。
  const colOrder = (c) =>
    isToken ? (c === 'without_video' ? 0 : 1) : videoSecondsRank(c);
  // per_second 每行只有一列，列序无意义；分辨率序仍然要对
  rows.sort(
    (a, b) =>
      videoResolutionRank(a.resolution) - videoResolutionRank(b.resolution) ||
      colOrder(a.column) - colOrder(b.column),
  );
  return rows;
};
