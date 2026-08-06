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
  if (/^4k$/i.test(s)) return 2160;
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
 * 把视频计费矩阵的所有格子摊平成 [{resolution, column, priceUSD}]，并乘上分组倍率。
 *
 * 列的语义随 mode 变：token 模式是「输入是否含视频」，per_call 模式是秒数。
 */
export const flattenVideoMatrix = (videoPricing, groupRatio) => {
  if (!videoPricing?.mode) return [];
  const isToken = videoPricing.mode === 'token';
  const table = (isToken ? videoPricing.token : videoPricing.per_call) || {};

  // 0 是合法倍率（免费分组），不能用 `|| 1` 兜底——那会把免费分组显示成原价。
  // 只有 undefined / 非数才回退 1（详情页展示基础单价时就不传）。
  const raw = Number(groupRatio);
  const gr = Number.isFinite(raw) && raw >= 0 ? raw : 1;

  const rows = [];
  Object.entries(table).forEach(([resolution, cols]) => {
    Object.entries(cols || {}).forEach(([column, usd]) => {
      const base = Number(usd);
      // 过滤的是「未配置的格子」，判据必须用**折算前**的原始价：
      // 折算后判的话，免费分组会让所有格子变 0 而被整表滤空。
      if (!Number.isFinite(base) || base <= 0) return;
      rows.push({ resolution, column, priceUSD: base * gr });
    });
  });

  // 分辨率按短边升序，同分辨率内「不含视频」在前——与配置页和供应商价目表同序，
  // 便于逐格对照。
  const colOrder = (c) =>
    isToken ? (c === 'without_video' ? 0 : 1) : videoSecondsRank(c);
  rows.sort(
    (a, b) =>
      videoResolutionRank(a.resolution) - videoResolutionRank(b.resolution) ||
      colOrder(a.column) - colOrder(b.column),
  );
  return rows;
};
