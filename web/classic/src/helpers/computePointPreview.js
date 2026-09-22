import { computePointsToQuota, getQuotaPerUnit } from './quota';

/**
 * 算力点换算预览：本套餐每期发的算力点，换成某个模型能用多少。
 *
 * 运营定价时需要的是「客户拿到手的是什么体验」——50,000 点听不出多少，
 * 「≈ 1,250 张高清图」听得出来。模型由运营自选而不是写死几个名字：
 * 写死的那几个在具体部署上不一定存在，预览会莫名其妙空掉。
 *
 * 一律按**未打分组折扣的列表价**算。订阅者实际落在哪个分组取决于套餐的升级分组
 * 与站点配置，配置时把它折进来只会让这个数字更难解释；标注清楚口径即可。
 */

const USD_PER_MILLION_TOKENS_FACTOR = 2; // 与定价页一致：USD/1M tokens = model_ratio × 2

/** 取矩阵里最便宜的一格——预览要回答「最多能用多少」。 */
const minNested = (matrix) => {
  let best = null;
  Object.values(matrix || {}).forEach((row) => {
    Object.values(row || {}).forEach((v) => {
      const n = Number(v);
      if (Number.isFinite(n) && n > 0 && (best === null || n < best)) best = n;
    });
  });
  return best;
};

const minFlat = (obj) => {
  let best = null;
  Object.values(obj || {}).forEach((v) => {
    const n = Number(v);
    if (Number.isFinite(n) && n > 0 && (best === null || n < best)) best = n;
  });
  return best;
};

/**
 * @returns {{kind:string, amount:number, unit:string}|{kind:'unknown', reason:string}}
 */
export const previewComputePoints = (points, record) => {
  const p = Number(points || 0);
  if (!Number.isFinite(p) || p <= 0) {
    return { kind: 'unknown', reason: '未设置每期算力点' };
  }
  if (!record) {
    return { kind: 'unknown', reason: '未选择模型' };
  }

  const usd = computePointsToQuota(p) / getQuotaPerUnit();
  if (!Number.isFinite(usd) || usd <= 0) {
    return { kind: 'unknown', reason: '换算率异常' };
  }

  const mode = record.video_pricing?.mode;
  if (mode === 'per_second') {
    const unitPrice = minFlat(record.video_pricing.per_second);
    if (!unitPrice) return { kind: 'unknown', reason: '该模型未配置单价' };
    return { kind: 'seconds', amount: Math.floor(usd / unitPrice), unit: '秒' };
  }
  if (mode === 'per_call') {
    const unitPrice = minNested(record.video_pricing.per_call);
    if (!unitPrice) return { kind: 'unknown', reason: '该模型未配置单价' };
    return { kind: 'calls', amount: Math.floor(usd / unitPrice), unit: '次' };
  }
  if (mode) {
    return { kind: 'unknown', reason: '该模型按 token 查表计费，无法直接换算' };
  }

  if (Number(record.quota_type) === 1) {
    const unitPrice = Number(record.model_price);
    if (!Number.isFinite(unitPrice) || unitPrice <= 0) {
      return { kind: 'unknown', reason: '该模型未配置单价' };
    }
    return { kind: 'calls', amount: Math.floor(usd / unitPrice), unit: '次' };
  }

  const ratio = Number(record.model_ratio);
  if (!Number.isFinite(ratio) || ratio <= 0) {
    return { kind: 'unknown', reason: '该模型未配置倍率' };
  }
  const usdPerMillion = ratio * USD_PER_MILLION_TOKENS_FACTOR;
  const tokens = Math.floor((usd / usdPerMillion) * 1_000_000);
  return { kind: 'tokens', amount: tokens, unit: 'tokens' };
};

/** 千分位，避免「1250000 tokens」这种读不出量级的数字。 */
export const formatPreviewAmount = (n) => {
  const v = Number(n || 0);
  if (!Number.isFinite(v)) return '0';
  return v.toLocaleString('en-US');
};
