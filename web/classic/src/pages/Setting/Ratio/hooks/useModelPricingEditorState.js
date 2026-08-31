/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useEffect, useMemo, useState } from 'react';
import { API, showError, showSuccess } from '../../../../helpers';
import {
  combineBillingExpr,
  splitBillingExprAndRequestRules,
} from '../components/requestRuleExpr';

export const PAGE_SIZE = 10;
export const PRICE_SUFFIX = '¥/1M tokens';
export const DEFAULT_USD2RMB_RATE = 7.3;
const EMPTY_CANDIDATE_MODEL_NAMES = [];

// 汇率兜底：无效（0 / 空 / 非有限）时回退常量，避免 ÷0 导致价格爆炸。
export const normalizeRate = (rate) => {
  const num = Number(rate);
  return Number.isFinite(num) && num > 0 ? num : DEFAULT_USD2RMB_RATE;
};

// 显示精度：按实际数字精度展示，小数不足 2 位时补 0 到 2 位（¥4 → 4.00、
// ¥0.876 → 0.876、¥0.0146 → 0.0146）。先收敛到 10 位去掉往返浮点漂移尾巴
// （¥4 重载得到 4.000000000004 → 4.00）。仅用于展示，绝不回写存储（见设计 §6.1.1）。
export const formatDisplayPrice = (value) => {
  if (!hasValue(value) && value !== 0) {
    return '';
  }
  const num = Number(value);
  if (!Number.isFinite(num)) {
    return '';
  }
  const trimmed = parseFloat(num.toFixed(10));
  const decimals = (String(trimmed).split('.')[1] || '').length;
  return trimmed.toFixed(Math.max(2, decimals));
};

const EMPTY_MODEL = {
  name: '',
  billingMode: 'per-token',
  fixedPrice: '',
  inputPrice: '',
  completionPrice: '',
  lockedCompletionRatio: '',
  completionRatioLocked: false,
  cachePrice: '',
  createCachePrice: '',
  imagePrice: '',
  audioInputPrice: '',
  audioOutputPrice: '',
  billingExpr: '',
  requestRuleExpr: '',
  // null = 未启用视频计费矩阵；对象 = 已启用（形状见 emptyVideoMatrix）
  videoMatrix: null,
  rawRatios: {
    modelRatio: '',
    completionRatio: '',
    cacheRatio: '',
    createCacheRatio: '',
    imageRatio: '',
    audioRatio: '',
    audioCompletionRatio: '',
  },
  hasConflict: false,
};

// 导出供 VideoMatrixEditor 复用：矩阵格子和 PriceInput 必须是同一条过滤规则，
// 两边分叉就会出现「这个框能输入的字符，那个框存不进去」。
export const NUMERIC_INPUT_REGEX = /^(\d+(\.\d*)?|\.\d*)?$/;

export const hasValue = (value) =>
  value !== '' && value !== null && value !== undefined && value !== false;

const toNumericString = (value) => {
  if (!hasValue(value) && value !== 0) {
    return '';
  }
  const num = Number(value);
  return Number.isFinite(num) ? String(num) : '';
};

const toNumberOrNull = (value) => {
  if (!hasValue(value) && value !== 0) {
    return null;
  }
  const num = Number(value);
  return Number.isFinite(num) ? num : null;
};

// 存储精度：保留 12 位有效，去掉浮点尾巴。与 formatDisplayPrice（屏幕 2 位）
// 是两套独立精度，显示截断绝不回流到存储（见设计 §6.1.1）。
export const formatNumber = (value) => {
  const num = toNumberOrNull(value);
  if (num === null) {
    return '';
  }
  return parseFloat(num.toFixed(12)).toString();
};

export const toNormalizedNumber = (value) => {
  const formatted = formatNumber(value);
  return formatted === '' ? null : Number(formatted);
};

const parseOptionJSON = (rawValue) => {
  if (!rawValue || rawValue.trim() === '') {
    return {};
  }
  try {
    const parsed = JSON.parse(rawValue);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch (error) {
    console.error('JSON解析错误:', error);
    return {};
  }
};

// ---------------------------------------------------------------------------
// 视频计费矩阵（docs/video-billing-matrix-design.md）
//
// 矩阵不是第四种「计费方式」，而是叠在按量/按次之上的一层：token 模式下上层管
// 提交时的预扣锚点、矩阵管完成后的差额结算，两者都要有。所以它在编辑器里是一个
// 开关，不是 billingMode 的一个取值。
// ---------------------------------------------------------------------------

// 与后端 setting/ratio_setting/video_pricing.go 的常量一一对应。
export const VIDEO_MODE_TOKEN = 'token';
export const VIDEO_MODE_PER_CALL = 'per_call';
export const VIDEO_COL_WITH_VIDEO = 'with_video';
export const VIDEO_COL_WITHOUT_VIDEO = 'without_video';

export const VIDEO_DEFAULT_RESOLUTIONS = ['480p', '720p', '1080p', '4k'];
export const VIDEO_DEFAULT_SECONDS = ['5', '10'];

export const videoCellKey = (resolution, col) => `${resolution}||${col}`;

export const emptyVideoMatrix = () => ({
  mode: VIDEO_MODE_TOKEN,
  resolutions: [...VIDEO_DEFAULT_RESOLUTIONS],
  seconds: [...VIDEO_DEFAULT_SECONDS],
  // cells[分辨率||列] = 人民币价格字符串（列为 with_video/without_video 或秒数）
  cells: {},
});

/**
 * 把后端的 VideoPriceEntry 解析成编辑器状态；entry 为空返回 null（= 未启用矩阵）。
 *
 * 货币边界：后端存美元，界面填人民币。与 ModelPrice 同口径——只乘汇率，不再乘 2
 * （那个 2 是 ModelRatio 的基准价约定，价格字段没有）。
 */
export const parseVideoMatrixEntry = (entry, rate) => {
  if (!entry || typeof entry !== 'object') return null;
  const usd2rmb = normalizeRate(rate);
  const mode =
    entry.mode === VIDEO_MODE_PER_CALL ? VIDEO_MODE_PER_CALL : VIDEO_MODE_TOKEN;
  const table =
    (mode === VIDEO_MODE_TOKEN ? entry.token : entry.per_call) || {};

  const cells = {};
  const cols = new Set();
  Object.entries(table).forEach(([resolution, row]) => {
    Object.entries(row || {}).forEach(([col, usd]) => {
      cells[videoCellKey(resolution, col)] = formatDisplayPrice(
        Number(usd) * usd2rmb,
      );
      cols.add(col);
    });
  });

  const resolutions = Object.keys(table);
  return {
    mode,
    resolutions: resolutions.length
      ? resolutions
      : [...VIDEO_DEFAULT_RESOLUTIONS],
    seconds:
      mode === VIDEO_MODE_PER_CALL && cols.size
        ? [...cols].sort((a, b) => Number(a) - Number(b))
        : [...VIDEO_DEFAULT_SECONDS],
    cells,
  };
};

/** 序列化回后端的 VideoPriceEntry；一格都没填时返回 null（不写进配置）。 */
export const serializeVideoMatrix = (matrix, rate) => {
  if (!matrix) return null;
  const usd2rmb = normalizeRate(rate);
  const cols =
    matrix.mode === VIDEO_MODE_TOKEN
      ? [VIDEO_COL_WITHOUT_VIDEO, VIDEO_COL_WITH_VIDEO]
      : (matrix.seconds || []).map((s) => String(s).trim()).filter(Boolean);

  const table = {};
  (matrix.resolutions || []).forEach((resolution) => {
    const key = String(resolution).trim();
    if (!key) return;
    const bucket = {};
    cols.forEach((col) => {
      const raw = matrix.cells?.[videoCellKey(key, col)];
      // 留空 = 该格未配置，不写入。后端查不到会回退旧计费路径，
      // 写个 0 反而会被当成「未配置」的另一种形态，徒增歧义。
      if (raw === '' || raw === null || raw === undefined) return;
      const usd = toNormalizedNumber(Number(raw) / usd2rmb);
      if (usd === null || !(usd > 0)) return;
      bucket[col] = usd;
    });
    if (Object.keys(bucket).length) table[key] = bucket;
  });
  if (!Object.keys(table).length) return null;

  return matrix.mode === VIDEO_MODE_TOKEN
    ? { mode: VIDEO_MODE_TOKEN, token: table }
    : { mode: VIDEO_MODE_PER_CALL, per_call: table };
};

// 货币边界：倍率 → 人民币输入价（倍率 × 基准价 2 × 汇率）。
const ratioToBasePrice = (ratio, rate) => {
  const num = toNumberOrNull(ratio);
  if (num === null) return '';
  return formatNumber(num * 2 * normalizeRate(rate));
};

const normalizeCompletionRatioMeta = (rawMeta) => {
  if (!rawMeta || typeof rawMeta !== 'object' || Array.isArray(rawMeta)) {
    return {
      locked: false,
      ratio: '',
    };
  }

  return {
    locked: Boolean(rawMeta.locked),
    ratio: toNumericString(rawMeta.ratio),
  };
};

const buildModelState = (name, sourceMaps, rate) => {
  const videoMatrix = parseVideoMatrixEntry(
    sourceMaps.VideoPricing?.[name],
    rate,
  );
  const billingMode = sourceMaps.ModelBillingMode?.[name];

  const modelRatio = toNumericString(sourceMaps.ModelRatio[name]);
  const completionRatio = toNumericString(sourceMaps.CompletionRatio[name]);
  const completionRatioMeta = normalizeCompletionRatioMeta(
    sourceMaps.CompletionRatioMeta?.[name],
  );
  const cacheRatio = toNumericString(sourceMaps.CacheRatio[name]);
  const createCacheRatio = toNumericString(sourceMaps.CreateCacheRatio[name]);
  const imageRatio = toNumericString(sourceMaps.ImageRatio[name]);
  const audioRatio = toNumericString(sourceMaps.AudioRatio[name]);
  const audioCompletionRatio = toNumericString(
    sourceMaps.AudioCompletionRatio[name],
  );
  // 货币边界：ModelPrice 是绝对美元金额，× 汇率得人民币按次价。
  const rawFixedPrice = toNumberOrNull(sourceMaps.ModelPrice[name]);
  const fixedPrice =
    rawFixedPrice === null
      ? ''
      : formatNumber(rawFixedPrice * normalizeRate(rate));
  const inputPrice = ratioToBasePrice(modelRatio, rate);
  const inputPriceNumber = toNumberOrNull(inputPrice);
  const audioInputPrice =
    inputPriceNumber !== null && hasValue(audioRatio)
      ? formatNumber(inputPriceNumber * Number(audioRatio))
      : '';

  const rawRatios = {
    modelRatio,
    completionRatio,
    cacheRatio,
    createCacheRatio,
    imageRatio,
    audioRatio,
    audioCompletionRatio,
  };

  if (billingMode === 'tiered_expr') {
    const fullBillingExpr = sourceMaps.ModelBillingExpr?.[name] || '';
    const { billingExpr, requestRuleExpr } =
      splitBillingExprAndRequestRules(fullBillingExpr);
    return {
      ...EMPTY_MODEL,
      name,
      billingMode: 'tiered_expr',
      billingExpr,
      requestRuleExpr,
      // 阶梯表达式只作用于同步请求（ModelPriceHelper），视频矩阵只作用于异步任务
      // （ModelPriceHelperPerCall → applyVideoPricing），两者可以共存且都生效。
      videoMatrix,
      // legacy 价格字段必须原样带过来。tiered 的界面不渲染价格输入框，编辑器改不了
      // 它们，但 handleSubmit 是全量重写——不带过来，serializeModel 就写不出这两个
      // 字段，DB 里的 ModelPrice/ModelRatio 会在每次从可视化编辑器保存时被清空。
      //
      // 这不只是丢数据：token 视频矩阵的预扣锚点正是这两个字段，清掉之后矩阵在
      // 运行时必被 model_price_error 拒掉。tiered 行的锚点能不能判定，全靠这里。
      //
      // 只带 fixedPrice 和 rawRatios，不带 inputPrice：留空才能让 serializeModel 走
      // 「inputPrice === null」那条分支、把 rawRatios 原值逐字回写，不经过
      // 倍率→价格→倍率的往返换算。
      fixedPrice,
      rawRatios,
      hasConflict: false,
    };
  }

  return {
    ...EMPTY_MODEL,
    name,
    billingMode: hasValue(fixedPrice) ? 'per-request' : 'per-token',
    fixedPrice,
    inputPrice,
    completionRatioLocked: completionRatioMeta.locked,
    lockedCompletionRatio: completionRatioMeta.ratio,
    completionPrice:
      inputPriceNumber !== null &&
      hasValue(
        completionRatioMeta.locked
          ? completionRatioMeta.ratio
          : completionRatio,
      )
        ? formatNumber(
            inputPriceNumber *
              Number(
                completionRatioMeta.locked
                  ? completionRatioMeta.ratio
                  : completionRatio,
              ),
          )
        : '',
    cachePrice:
      inputPriceNumber !== null && hasValue(cacheRatio)
        ? formatNumber(inputPriceNumber * Number(cacheRatio))
        : '',
    createCachePrice:
      inputPriceNumber !== null && hasValue(createCacheRatio)
        ? formatNumber(inputPriceNumber * Number(createCacheRatio))
        : '',
    imagePrice:
      inputPriceNumber !== null && hasValue(imageRatio)
        ? formatNumber(inputPriceNumber * Number(imageRatio))
        : '',
    audioInputPrice,
    audioOutputPrice:
      toNumberOrNull(audioInputPrice) !== null && hasValue(audioCompletionRatio)
        ? formatNumber(Number(audioInputPrice) * Number(audioCompletionRatio))
        : '',
    requestRuleExpr: '',
    videoMatrix,
    rawRatios,
    hasConflict:
      hasValue(fixedPrice) &&
      [
        modelRatio,
        completionRatio,
        cacheRatio,
        createCacheRatio,
        imageRatio,
        audioRatio,
        audioCompletionRatio,
      ].some(hasValue),
  };
};

const hasAnyVideoMatrixCell = (matrix) =>
  Object.values(matrix?.cells || {}).some((raw) => Number(raw) > 0);

// 填了格子的 per_call 矩阵自己就是完整定价（后端 videoPerCallPriceable 会为它放行，
// 不需要任何 legacy 价格），所以不该被列进「未设置价格模型」——否则运营会去补一个
// 用不上的 ModelPrice，正好制造出这次要消除的「两边都配」。
//
// 必须同时要求「至少一格有值」：空矩阵会被 serializeVideoMatrix 判为 null、根本不写进
// VideoPricingConfig，只看 mode 就等于把一个实际未定价的模型从列表里摘掉。
//
// token 矩阵反过来：它确实缺预扣锚点，留在这个列表里是对的。
export const isBasePricingUnset = (model) =>
  model.billingMode !== 'tiered_expr' &&
  !hasValue(model.fixedPrice) &&
  !hasValue(model.inputPrice) &&
  !(
    model.videoMatrix?.mode === VIDEO_MODE_PER_CALL &&
    hasAnyVideoMatrixCell(model.videoMatrix)
  );

/**
 * 「这个模型保存后，DB 里会不会留下预扣锚点字段（ModelPrice / ModelRatio）」。
 *
 * 纯数据判断，不含任何「要不要拦」的策略——策略在 isVideoMatrixMissingAnchor 和
 * 各转移守卫里。两者曾经糊在一起，是前几轮反复改错的根源之一。
 *
 * 判断源必须与 serializeModel 的写入点严格对应。锚点判定是 billingMode 的函数，
 * 不是一条扁平的 or：
 *
 * | billingMode  | serializeModel 写哪些锚点字段              | 本函数认什么                        |
 * |--------------|--------------------------------------------|-------------------------------------|
 * | per-request  | ModelPrice ← fixedPrice，随即早返回         | fixedPrice                          |
 * | per-token    | ModelRatio ← inputPrice 或 rawRatios       | inputPrice / rawRatios.modelRatio   |
 * | tiered_expr  | ModelPrice ← fixedPrice，**再继续**走倍率路 | 以上三者任一                        |
 *
 * 写成扁平 or 曾经直接造出一个 P1：per-request 早返回、rawRatios 一概不落库，扁平版
 * 却因为 rawRatios.modelRatio 还有残值而判「有锚点」放行保存——存完 DB 里 ModelPrice
 * 和 ModelRatio 都是空的，视频请求运行时 model_price_error。
 *
 * 机械检查：数 serializeModel 里写 ModelPrice / ModelRatio 的位置，本函数每个分支
 * 认的字段必须与该 billingMode 能到达的写入点完全一致。
 */
const hasAnchorFields = (model) => {
  switch (model?.billingMode) {
    // 只写 ModelPrice 就早返回，ModelRatio / rawRatios 一概不落库。
    case 'per-request':
      return hasValue(model.fixedPrice);
    // 两条路都走得到：fixedPrice 来自 buildModelState 原样带过来的 DB 值，
    // rawRatios 同理；inputPrice 只在「本次会话里从按量切过来」时非空。
    case 'tiered_expr':
      return (
        hasValue(model.fixedPrice) ||
        hasValue(model.inputPrice) ||
        hasValue(model.rawRatios?.modelRatio)
      );
    // per-token：rawRatios 不随编辑更新，运营清空「输入价格」后 inputPrice === ''，
    // 但 serializeModel 会从 rawRatios.modelRatio 把 ModelRatio 原样写回去，
    // DB 里锚点其实还在。少看这一项会拦下本该合法的保存。
    default:
      return (
        hasValue(model?.inputPrice) || hasValue(model?.rawRatios?.modelRatio)
      );
  }
};

/**
 * 视频矩阵可用性的完整状态空间 —— 这张表是所有矩阵门控的唯一依据：
 *
 * |                         | per_call 矩阵 | token 矩阵          |
 * |-------------------------|---------------|---------------------|
 * | per-token / per-request | ✅ 不需要锚点  | 需要锚点，可现场补   |
 * | tiered_expr             | ✅ 不需要锚点  | 需要锚点，只能靠既有 |
 *
 * 两条正交的约束交叉出这张表：
 *
 *  1. 矩阵模式维度：token 的单价要乘上游返回的 token 数，提交时无从预估，预扣只能
 *     靠按量/按次价格当锚点；缺了它 ModelPriceHelperPerCall 直接报 model_price_error，
 *     请求根本进不到矩阵那一步（docs/video-billing-matrix-design.md §2.6）。
 *     per_call 的格子里就是终价，videoPerCallPriceable 已为它放行，不需要任何 legacy 价格。
 *  2. 计费方式维度：tiered_expr 的界面不渲染价格输入框，所以那一行**补不了**锚点，
 *     只能沿用 DB 里已有的。注意「补不了」≠「判不了」——buildModelState 现在把
 *     fixedPrice / rawRatios 原样带进状态，锚点有没有是查得出来的。
 *
 * 曾经有整整六轮，右下角那格是「判不了」的：buildModelState 从 EMPTY_MODEL 重建、
 * 把 legacy 价格丢光，于是这里只能一律放行，放行又让坏配置存得下去。**那是数据层的
 * 缺陷，却一直被当成门控问题在反复调门控**——门控怎么画都有一边错。修掉数据层之后
 * 这一格降级成了普通的条件格，本函数因此对三行一视同仁，不再有 billingMode 豁免。
 *
 * 转移入口清单见 handleBillingModeChange 上方；别凭印象数「已经堵了几条」。
 */
export const isVideoMatrixMissingAnchor = (model) =>
  Boolean(model?.videoMatrix) &&
  model.videoMatrix.mode === VIDEO_MODE_TOKEN &&
  !hasAnchorFields(model);

/**
 * 能不能给这个模型新建/切到 token 矩阵——「缺锚点且补不了」时才不行。
 *
 * 与 isVideoMatrixMissingAnchor 的区别是**可补救性**，不是合法性：per-token /
 * per-request 缺锚点时界面上就有输入框，让它选 token 再出横幅提示即可；tiered 的
 * 界面没有价格输入框，选了就卡死，只能从源头禁掉。
 *
 * 单一来源：矩阵编辑器的 allowTokenMode 和 handleVideoMatrixToggle 的默认模式都读
 * 这一个函数。这两处曾各写一份 `billingMode === 'tiered_expr'`，等于把「tiered 一律
 * 不能用 token」这条过严的规则复制了两遍。
 */
export const canUseTokenVideoMatrix = (model) =>
  model?.billingMode !== 'tiered_expr' || hasAnchorFields(model);

const getVideoMatrixWarnings = (model, t) => {
  if (!isVideoMatrixMissingAnchor(model)) {
    return [];
  }
  // 同一个缺陷，两种补救路径：能现场填价格的说「去上面填」，填不了的说「改矩阵」。
  if (model.billingMode === 'tiered_expr') {
    return [
      t(
        '该模型使用表达式计费，界面上没有可填的基础价格，而「按 Token」的视频计费矩阵需要一个预扣锚点。请把矩阵改为「按次」或关闭矩阵，否则视频任务会被直接拒绝。',
      ),
    ];
  }
  return [
    t(
      '「按 Token」的视频计费矩阵需要上面的按量或按次价格作为提交时的预扣锚点，否则请求会被直接拒绝。建议标定在矩阵最高档单价附近。',
    ),
  ];
};

export const getModelWarnings = (model, t) => {
  if (!model) {
    return [];
  }
  const videoWarnings = getVideoMatrixWarnings(model, t);
  if (model.billingMode === 'tiered_expr') {
    return videoWarnings;
  }
  const warnings = [...videoWarnings];
  const hasDerivedPricing = [
    model.inputPrice,
    model.completionPrice,
    model.cachePrice,
    model.createCachePrice,
    model.imagePrice,
    model.audioInputPrice,
    model.audioOutputPrice,
  ].some(hasValue);

  if (model.hasConflict) {
    warnings.push(
      t('当前模型同时存在按次价格和倍率配置，保存时会按当前计费方式覆盖。'),
    );
  }

  if (
    !hasValue(model.inputPrice) &&
    [
      model.rawRatios.completionRatio,
      model.rawRatios.cacheRatio,
      model.rawRatios.createCacheRatio,
      model.rawRatios.imageRatio,
      model.rawRatios.audioRatio,
      model.rawRatios.audioCompletionRatio,
    ].some(hasValue)
  ) {
    warnings.push(
      t(
        '当前模型存在未显式设置输入倍率的扩展倍率；填写输入价格后会自动换算为价格字段。',
      ),
    );
  }

  if (
    model.billingMode === 'per-token' &&
    hasDerivedPricing &&
    !hasValue(model.inputPrice)
  ) {
    warnings.push(t('按量计费下需要先填写输入价格，才能保存其它价格项。'));
  }

  if (
    model.billingMode === 'per-token' &&
    hasValue(model.audioOutputPrice) &&
    !hasValue(model.audioInputPrice)
  ) {
    warnings.push(t('填写音频补全价格前，需要先填写音频输入价格。'));
  }

  return warnings;
};

export const buildSummaryText = (model, t) => {
  const requestRuleSuffix =
    (model.billingMode === 'tiered_expr' && model.requestRuleExpr
      ? `，${t('请求规则')}`
      : '') + (model.videoMatrix ? `，${t('视频矩阵')}` : '');
  if (model.billingMode === 'tiered_expr') {
    const expr = model.billingExpr;
    if (!expr) return `${t('表达式计费')}${requestRuleSuffix}`;
    const tierCount = (expr.match(/tier\(/g) || []).length;
    if (tierCount === 0) {
      return `${t('表达式计费')}${requestRuleSuffix}`;
    }
    return `${t('阶梯计费')} (${tierCount} ${t('档')})${requestRuleSuffix}`;
  }

  if (model.billingMode === 'per-request' && hasValue(model.fixedPrice)) {
    return `${t('按次')} ¥${formatDisplayPrice(model.fixedPrice)} / ${t('次')}${requestRuleSuffix}`;
  }

  if (hasValue(model.inputPrice)) {
    const extraCount = [
      model.completionPrice,
      model.cachePrice,
      model.createCachePrice,
      model.imagePrice,
      model.audioInputPrice,
      model.audioOutputPrice,
    ].filter(hasValue).length;
    const extraLabel =
      extraCount > 0 ? `，${t('额外价格项')} ${extraCount}` : '';
    return `${t('输入')} ¥${formatDisplayPrice(model.inputPrice)}${extraLabel}${requestRuleSuffix}`;
  }

  return `${t('未设置价格')}${requestRuleSuffix}`;
};

export const buildOptionalFieldToggles = (model) => ({
  completionPrice:
    model.completionRatioLocked || hasValue(model.completionPrice),
  cachePrice: hasValue(model.cachePrice),
  createCachePrice: hasValue(model.createCachePrice),
  imagePrice: hasValue(model.imagePrice),
  audioInputPrice: hasValue(model.audioInputPrice),
  audioOutputPrice: hasValue(model.audioOutputPrice),
});

const serializeModel = (model, t, rate) => {
  const usd2rmb = normalizeRate(rate);
  const result = {
    ModelPrice: null,
    ModelRatio: null,
    CompletionRatio: null,
    CacheRatio: null,
    CreateCacheRatio: null,
    ImageRatio: null,
    AudioRatio: null,
    AudioCompletionRatio: null,
  };

  // ModelPrice 只有两种 billingMode 会写：per-request（编辑器里填的按次价）和
  // tiered_expr（buildModelState 原样带过来的 DB 值，编辑器改不了）。per-token 不写，
  // 它的价格走 ModelRatio。
  if (
    model.billingMode === 'per-request' ||
    model.billingMode === 'tiered_expr'
  ) {
    if (hasValue(model.fixedPrice)) {
      // 人民币按次价 ÷ 汇率 = 后端存储的美元 ModelPrice。
      result.ModelPrice = toNormalizedNumber(
        Number(model.fixedPrice) / usd2rmb,
      );
    }
    // per-request 到此为止：这类模型按次定价，倍率无意义，不回写。
    // tiered_expr 不能停在这——它的 ModelRatio 也要原样保住，继续往下走。
    if (model.billingMode === 'per-request') {
      return result;
    }
  }

  const inputPrice = toNumberOrNull(model.inputPrice);
  const completionPrice = toNumberOrNull(model.completionPrice);
  const cachePrice = toNumberOrNull(model.cachePrice);
  const createCachePrice = toNumberOrNull(model.createCachePrice);
  const imagePrice = toNumberOrNull(model.imagePrice);
  const audioInputPrice = toNumberOrNull(model.audioInputPrice);
  const audioOutputPrice = toNumberOrNull(model.audioOutputPrice);

  const hasDependentPrice = [
    completionPrice,
    cachePrice,
    createCachePrice,
    imagePrice,
    audioInputPrice,
    audioOutputPrice,
  ].some((value) => value !== null);

  if (inputPrice === null) {
    if (hasDependentPrice) {
      throw new Error(
        t(
          '模型 {{name}} 缺少输入价格，无法计算补全/缓存/图片/音频价格对应的倍率',
          {
            name: model.name,
          },
        ),
      );
    }

    if (hasValue(model.rawRatios.modelRatio)) {
      result.ModelRatio = toNormalizedNumber(model.rawRatios.modelRatio);
    }
    if (hasValue(model.rawRatios.completionRatio)) {
      result.CompletionRatio = toNormalizedNumber(
        model.rawRatios.completionRatio,
      );
    }
    if (hasValue(model.rawRatios.cacheRatio)) {
      result.CacheRatio = toNormalizedNumber(model.rawRatios.cacheRatio);
    }
    if (hasValue(model.rawRatios.createCacheRatio)) {
      result.CreateCacheRatio = toNormalizedNumber(
        model.rawRatios.createCacheRatio,
      );
    }
    if (hasValue(model.rawRatios.imageRatio)) {
      result.ImageRatio = toNormalizedNumber(model.rawRatios.imageRatio);
    }
    if (hasValue(model.rawRatios.audioRatio)) {
      result.AudioRatio = toNormalizedNumber(model.rawRatios.audioRatio);
    }
    if (hasValue(model.rawRatios.audioCompletionRatio)) {
      result.AudioCompletionRatio = toNormalizedNumber(
        model.rawRatios.audioCompletionRatio,
      );
    }
    return result;
  }

  // 货币边界：人民币输入价 ÷ 汇率 ÷ 基准价 2 = 后端倍率。派生项为同币种比值，汇率自动约掉，不参与换算。
  result.ModelRatio = toNormalizedNumber(inputPrice / usd2rmb / 2);

  if (!model.completionRatioLocked && completionPrice !== null) {
    result.CompletionRatio = toNormalizedNumber(completionPrice / inputPrice);
  } else if (
    model.completionRatioLocked &&
    hasValue(model.rawRatios.completionRatio)
  ) {
    result.CompletionRatio = toNormalizedNumber(
      model.rawRatios.completionRatio,
    );
  }
  if (cachePrice !== null) {
    result.CacheRatio = toNormalizedNumber(cachePrice / inputPrice);
  }
  if (createCachePrice !== null) {
    result.CreateCacheRatio = toNormalizedNumber(createCachePrice / inputPrice);
  }
  if (imagePrice !== null) {
    result.ImageRatio = toNormalizedNumber(imagePrice / inputPrice);
  }
  if (audioInputPrice !== null) {
    result.AudioRatio = toNormalizedNumber(audioInputPrice / inputPrice);
  }
  if (audioOutputPrice !== null) {
    if (audioInputPrice === null || audioInputPrice === 0) {
      throw new Error(
        t('模型 {{name}} 缺少音频输入价格，无法计算音频补全倍率', {
          name: model.name,
        }),
      );
    }
    result.AudioCompletionRatio = toNormalizedNumber(
      audioOutputPrice / audioInputPrice,
    );
  }

  return result;
};

export const buildPreviewRows = (model, t, rate) => {
  if (!model) return [];
  const usd2rmb = normalizeRate(rate);
  const finalBillingExpr = combineBillingExpr(
    model.billingExpr,
    model.requestRuleExpr,
  );

  if (model.billingMode === 'tiered_expr') {
    const rows = [
      {
        key: 'BillingMode',
        label: 'ModelBillingMode',
        value: 'tiered_expr',
      },
    ];
    if (finalBillingExpr) {
      const tierCount = (model.billingExpr.match(/tier\(/g) || []).length;
      rows.push({
        key: 'BillingExpr',
        label: 'ModelBillingExpr',
        value:
          tierCount > 0
            ? `${tierCount} ${t('档')} — ${
                finalBillingExpr.length > 60
                  ? finalBillingExpr.slice(0, 60) + '...'
                  : finalBillingExpr
              }`
            : finalBillingExpr.length > 60
              ? finalBillingExpr.slice(0, 60) + '...'
              : finalBillingExpr,
      });
    }
    return rows;
  }

  if (model.billingMode === 'per-request') {
    const rows = [
      {
        key: 'ModelPrice',
        label: 'ModelPrice',
        value: hasValue(model.fixedPrice)
          ? formatNumber(Number(model.fixedPrice) / usd2rmb)
          : t('空'),
      },
    ];
    return rows;
  }

  const inputPrice = toNumberOrNull(model.inputPrice);
  if (inputPrice === null) {
    const rows = [
      {
        key: 'ModelRatio',
        label: 'ModelRatio',
        value: hasValue(model.rawRatios.modelRatio)
          ? model.rawRatios.modelRatio
          : t('空'),
      },
      {
        key: 'CompletionRatio',
        label: 'CompletionRatio',
        value: hasValue(model.rawRatios.completionRatio)
          ? model.rawRatios.completionRatio
          : t('空'),
      },
      {
        key: 'CacheRatio',
        label: 'CacheRatio',
        value: hasValue(model.rawRatios.cacheRatio)
          ? model.rawRatios.cacheRatio
          : t('空'),
      },
      {
        key: 'CreateCacheRatio',
        label: 'CreateCacheRatio',
        value: hasValue(model.rawRatios.createCacheRatio)
          ? model.rawRatios.createCacheRatio
          : t('空'),
      },
      {
        key: 'ImageRatio',
        label: 'ImageRatio',
        value: hasValue(model.rawRatios.imageRatio)
          ? model.rawRatios.imageRatio
          : t('空'),
      },
      {
        key: 'AudioRatio',
        label: 'AudioRatio',
        value: hasValue(model.rawRatios.audioRatio)
          ? model.rawRatios.audioRatio
          : t('空'),
      },
      {
        key: 'AudioCompletionRatio',
        label: 'AudioCompletionRatio',
        value: hasValue(model.rawRatios.audioCompletionRatio)
          ? model.rawRatios.audioCompletionRatio
          : t('空'),
      },
    ];
    return rows;
  }

  const completionPrice = toNumberOrNull(model.completionPrice);
  const cachePrice = toNumberOrNull(model.cachePrice);
  const createCachePrice = toNumberOrNull(model.createCachePrice);
  const imagePrice = toNumberOrNull(model.imagePrice);
  const audioInputPrice = toNumberOrNull(model.audioInputPrice);
  const audioOutputPrice = toNumberOrNull(model.audioOutputPrice);

  const rows = [
    {
      key: 'ModelRatio',
      label: 'ModelRatio',
      value: formatNumber(inputPrice / usd2rmb / 2),
    },
    {
      key: 'CompletionRatio',
      label: 'CompletionRatio',
      value: model.completionRatioLocked
        ? `${model.lockedCompletionRatio || t('空')} (${t('后端固定')})`
        : completionPrice !== null
          ? formatNumber(completionPrice / inputPrice)
          : t('空'),
    },
    {
      key: 'CacheRatio',
      label: 'CacheRatio',
      value:
        cachePrice !== null ? formatNumber(cachePrice / inputPrice) : t('空'),
    },
    {
      key: 'CreateCacheRatio',
      label: 'CreateCacheRatio',
      value:
        createCachePrice !== null
          ? formatNumber(createCachePrice / inputPrice)
          : t('空'),
    },
    {
      key: 'ImageRatio',
      label: 'ImageRatio',
      value:
        imagePrice !== null ? formatNumber(imagePrice / inputPrice) : t('空'),
    },
    {
      key: 'AudioRatio',
      label: 'AudioRatio',
      value:
        audioInputPrice !== null
          ? formatNumber(audioInputPrice / inputPrice)
          : t('空'),
    },
    {
      key: 'AudioCompletionRatio',
      label: 'AudioCompletionRatio',
      value:
        audioOutputPrice !== null &&
        audioInputPrice !== null &&
        audioInputPrice !== 0
          ? formatNumber(audioOutputPrice / audioInputPrice)
          : t('空'),
    },
  ];
  return rows;
};

export function useModelPricingEditorState({
  options,
  refresh,
  t,
  candidateModelNames = EMPTY_CANDIDATE_MODEL_NAMES,
  filterMode = 'all',
  rate = DEFAULT_USD2RMB_RATE,
}) {
  const [models, setModels] = useState([]);
  const [initialVisibleModelNames, setInitialVisibleModelNames] = useState([]);
  const [selectedModelName, setSelectedModelName] = useState('');
  const [selectedModelNames, setSelectedModelNames] = useState([]);
  const [searchText, setSearchText] = useState('');
  const [currentPage, setCurrentPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [conflictOnly, setConflictOnly] = useState(false);
  const [optionalFieldToggles, setOptionalFieldToggles] = useState({});

  useEffect(() => {
    const sourceMaps = {
      ModelPrice: parseOptionJSON(options.ModelPrice),
      ModelRatio: parseOptionJSON(options.ModelRatio),
      CompletionRatio: parseOptionJSON(options.CompletionRatio),
      CompletionRatioMeta: parseOptionJSON(options.CompletionRatioMeta),
      CacheRatio: parseOptionJSON(options.CacheRatio),
      CreateCacheRatio: parseOptionJSON(options.CreateCacheRatio),
      ImageRatio: parseOptionJSON(options.ImageRatio),
      AudioRatio: parseOptionJSON(options.AudioRatio),
      AudioCompletionRatio: parseOptionJSON(options.AudioCompletionRatio),
      ModelBillingMode: parseOptionJSON(
        options['billing_setting.billing_mode'],
      ),
      ModelBillingExpr: parseOptionJSON(
        options['billing_setting.billing_expr'],
      ),
      VideoPricing: parseOptionJSON(options.VideoPricingConfig),
    };

    const names = new Set([
      ...candidateModelNames,
      ...Object.keys(sourceMaps.ModelPrice),
      ...Object.keys(sourceMaps.ModelRatio),
      ...Object.keys(sourceMaps.CompletionRatio),
      ...Object.keys(sourceMaps.CompletionRatioMeta),
      ...Object.keys(sourceMaps.CacheRatio),
      ...Object.keys(sourceMaps.CreateCacheRatio),
      ...Object.keys(sourceMaps.ImageRatio),
      ...Object.keys(sourceMaps.AudioRatio),
      ...Object.keys(sourceMaps.AudioCompletionRatio),
      ...Object.keys(sourceMaps.ModelBillingMode),
      ...Object.keys(sourceMaps.ModelBillingExpr),
      // 视频矩阵是第四个定价源：不并进来的话，只靠矩阵定价的模型会从列表里消失
      // （同 relay/helper/price.go:234-239 登记 HasModelBillingConfig 的道理）。
      ...Object.keys(sourceMaps.VideoPricing),
    ]);

    const nextModels = Array.from(names)
      .map((name) => buildModelState(name, sourceMaps, rate))
      .sort((a, b) => a.name.localeCompare(b.name));

    setModels(nextModels);
    setInitialVisibleModelNames(
      filterMode === 'unset'
        ? nextModels
            .filter((model) => isBasePricingUnset(model))
            .map((model) => model.name)
        : nextModels.map((model) => model.name),
    );
    setOptionalFieldToggles(
      nextModels.reduce((acc, model) => {
        acc[model.name] = buildOptionalFieldToggles(model);
        return acc;
      }, {}),
    );
    setSelectedModelName((previous) => {
      if (previous && nextModels.some((model) => model.name === previous)) {
        return previous;
      }
      const nextVisibleModels =
        filterMode === 'unset'
          ? nextModels.filter((model) => isBasePricingUnset(model))
          : nextModels;
      return nextVisibleModels[0]?.name || '';
    });
  }, [candidateModelNames, filterMode, options, rate]);

  const visibleModels = useMemo(() => {
    return filterMode === 'unset'
      ? models.filter((model) => initialVisibleModelNames.includes(model.name))
      : models;
  }, [filterMode, initialVisibleModelNames, models]);

  const filteredModels = useMemo(() => {
    return visibleModels.filter((model) => {
      const keyword = searchText.trim().toLowerCase();
      const keywordMatch = keyword
        ? model.name.toLowerCase().includes(keyword)
        : true;
      const conflictMatch = conflictOnly ? model.hasConflict : true;
      return keywordMatch && conflictMatch;
    });
  }, [conflictOnly, searchText, visibleModels]);

  const pagedData = useMemo(() => {
    const start = (currentPage - 1) * PAGE_SIZE;
    return filteredModels.slice(start, start + PAGE_SIZE);
  }, [currentPage, filteredModels]);

  const selectedModel = useMemo(
    () =>
      visibleModels.find((model) => model.name === selectedModelName) || null,
    [selectedModelName, visibleModels],
  );

  const selectedWarnings = useMemo(
    () => getModelWarnings(selectedModel, t),
    [selectedModel, t],
  );

  const previewRows = useMemo(
    () => buildPreviewRows(selectedModel, t, rate),
    [selectedModel, t, rate],
  );

  useEffect(() => {
    setCurrentPage(1);
  }, [searchText, conflictOnly, filterMode, candidateModelNames]);

  useEffect(() => {
    setSelectedModelNames((previous) =>
      previous.filter((name) =>
        visibleModels.some((model) => model.name === name),
      ),
    );
  }, [visibleModels]);

  useEffect(() => {
    if (visibleModels.length === 0) {
      setSelectedModelName('');
      return;
    }
    if (!visibleModels.some((model) => model.name === selectedModelName)) {
      setSelectedModelName(visibleModels[0].name);
    }
  }, [selectedModelName, visibleModels]);

  const upsertModel = (name, updater) => {
    setModels((previous) =>
      previous.map((model) => {
        if (model.name !== name) return model;
        return typeof updater === 'function' ? updater(model) : updater;
      }),
    );
  };

  const isOptionalFieldEnabled = (model, field) => {
    if (!model) return false;
    const modelToggles = optionalFieldToggles[model.name];
    if (modelToggles && typeof modelToggles[field] === 'boolean') {
      return modelToggles[field];
    }
    return buildOptionalFieldToggles(model)[field];
  };

  const updateOptionalFieldToggle = (modelName, field, checked) => {
    setOptionalFieldToggles((prev) => ({
      ...prev,
      [modelName]: {
        ...(prev[modelName] || {}),
        [field]: checked,
      },
    }));
  };

  const handleOptionalFieldToggle = (field, checked) => {
    if (!selectedModel) return;

    updateOptionalFieldToggle(selectedModel.name, field, checked);

    if (checked) {
      return;
    }

    upsertModel(selectedModel.name, (model) => {
      const nextModel = { ...model, [field]: '' };

      if (field === 'audioInputPrice') {
        nextModel.audioOutputPrice = '';
        setOptionalFieldToggles((prev) => ({
          ...prev,
          [selectedModel.name]: {
            ...(prev[selectedModel.name] || {}),
            audioInputPrice: false,
            audioOutputPrice: false,
          },
        }));
      }

      return nextModel;
    });
  };

  const fillDerivedPricesFromBase = (model, nextInputPrice) => {
    const baseNumber = toNumberOrNull(nextInputPrice);
    if (baseNumber === null) {
      return model;
    }

    return {
      ...model,
      completionPrice:
        model.completionRatioLocked && hasValue(model.lockedCompletionRatio)
          ? formatNumber(baseNumber * Number(model.lockedCompletionRatio))
          : !hasValue(model.completionPrice) &&
              hasValue(model.rawRatios.completionRatio)
            ? formatNumber(baseNumber * Number(model.rawRatios.completionRatio))
            : model.completionPrice,
      cachePrice:
        !hasValue(model.cachePrice) && hasValue(model.rawRatios.cacheRatio)
          ? formatNumber(baseNumber * Number(model.rawRatios.cacheRatio))
          : model.cachePrice,
      createCachePrice:
        !hasValue(model.createCachePrice) &&
        hasValue(model.rawRatios.createCacheRatio)
          ? formatNumber(baseNumber * Number(model.rawRatios.createCacheRatio))
          : model.createCachePrice,
      imagePrice:
        !hasValue(model.imagePrice) && hasValue(model.rawRatios.imageRatio)
          ? formatNumber(baseNumber * Number(model.rawRatios.imageRatio))
          : model.imagePrice,
      audioInputPrice:
        !hasValue(model.audioInputPrice) && hasValue(model.rawRatios.audioRatio)
          ? formatNumber(baseNumber * Number(model.rawRatios.audioRatio))
          : model.audioInputPrice,
      audioOutputPrice:
        !hasValue(model.audioOutputPrice) &&
        hasValue(model.rawRatios.audioRatio) &&
        hasValue(model.rawRatios.audioCompletionRatio)
          ? formatNumber(
              baseNumber *
                Number(model.rawRatios.audioRatio) *
                Number(model.rawRatios.audioCompletionRatio),
            )
          : model.audioOutputPrice,
    };
  };

  const handleNumericFieldChange = (field, value) => {
    if (!selectedModel || !NUMERIC_INPUT_REGEX.test(value)) {
      return;
    }

    upsertModel(selectedModel.name, (model) => {
      const updatedModel = { ...model, [field]: value };

      if (field === 'inputPrice') {
        return fillDerivedPricesFromBase(updatedModel, value);
      }

      return updatedModel;
    });
  };

  /**
   * 「token 矩阵 + 无锚点」这个非法态（见 isVideoMatrixMissingAnchor 上方状态空间表）
   * 的**全部**转移入口，改门控时逐条对：
   *
   *  1. EMPTY_MODEL 新建           —— videoMatrix 为 null，进不了
   *  2. buildModelState 读历史数据 —— 挡不住，由 getVideoMatrixWarnings 出横幅提示
   *  3. 矩阵编辑器切模式           —— VideoMatrixEditor 的 allowTokenMode 禁用 token 项
   *  4. 打开矩阵开关               —— handleVideoMatrixToggle 无锚点时默认 per_call
   *  5. 切换计费方式到 tiered_expr —— 本函数，下面拦
   *  6. 批量应用 tiered 模板       —— applySelectedModelPricing，那边跳过
   *
   * 之前只堵了 3 和 4 就在注释里写「UI 已从源头挡住新建」，5 和 6 漏了整整一轮。
   * 判定的是一个多元状态（billingMode × 矩阵模式 × 锚点字段），入口就必须按写入点
   * 枚举（grep `billingMode:` 和 `videoMatrix:`），不能只数自己刚改过的那几个。
   *
   * 5 和 6 都只在**切过去会导致缺锚点**时才拦——模型本来就有 ModelPrice/ModelRatio 的，
   * serializeModel 会原样保住（见 buildModelState 的 tiered 分支），切换完全合法，
   * 拦下来只会逼运营白重写一遍矩阵。
   */
  const handleBillingModeChange = (value) => {
    if (!selectedModel) return;
    // 不做自动转换：token 矩阵的格子是 ¥/百万 tokens，per_call 是 ¥/次，
    // 数值含义完全不同，静默改 mode 会把单价直接错解释成次价。
    //
    // 用切换后的投影状态判断，而不是重新推导一遍条件——判定逻辑只有 hasAnchorFields
    // 一份，这里只负责把「切过去之后长什么样」算出来喂给它。
    if (
      value === 'tiered_expr' &&
      selectedModel.videoMatrix?.mode === VIDEO_MODE_TOKEN &&
      !hasAnchorFields({ ...selectedModel, billingMode: value })
    ) {
      showError(
        t(
          '该模型的视频计费矩阵为「按 Token」但没有预扣锚点，而表达式计费的界面没有可填的基础价格，切过去就再也补不上了。请先填好按量或按次价格，或把矩阵改为「按次」／关闭矩阵。',
        ),
      );
      return;
    }
    upsertModel(selectedModel.name, (model) => {
      const next = { ...model, billingMode: value };
      if (value === 'tiered_expr' && !model.billingExpr) {
        next.billingExpr = 'tier("base", p * 0 + c * 0)';
      }
      return next;
    });
  };

  const handleBillingExprChange = (newExpr) => {
    if (!selectedModel) return;
    upsertModel(selectedModel.name, (model) => ({
      ...model,
      billingExpr: newExpr,
    }));
  };

  const handleVideoMatrixChange = (nextMatrix) => {
    if (!selectedModel) return;
    upsertModel(selectedModel.name, (model) => ({
      ...model,
      videoMatrix: nextMatrix,
    }));
  };

  const handleVideoMatrixToggle = (checked) => {
    if (!checked) {
      handleVideoMatrixChange(null);
      return;
    }
    const matrix = emptyVideoMatrix();
    // 不改默认值的话，开关一打开就是个选不掉的非法态：token 选项在这种模型上是禁用的。
    if (!canUseTokenVideoMatrix(selectedModel)) {
      matrix.mode = VIDEO_MODE_PER_CALL;
    }
    handleVideoMatrixChange(matrix);
  };

  const handleRequestRuleExprChange = (newExpr) => {
    if (!selectedModel) return;
    upsertModel(selectedModel.name, (model) => ({
      ...model,
      requestRuleExpr: newExpr,
    }));
  };

  const addModel = (modelName) => {
    const trimmedName = modelName.trim();
    if (!trimmedName) {
      showError(t('请输入模型名称'));
      return false;
    }
    if (models.some((model) => model.name === trimmedName)) {
      showError(t('模型名称已存在'));
      return false;
    }

    const nextModel = {
      ...EMPTY_MODEL,
      name: trimmedName,
      rawRatios: { ...EMPTY_MODEL.rawRatios },
    };

    setModels((previous) => [nextModel, ...previous]);
    setOptionalFieldToggles((prev) => ({
      ...prev,
      [trimmedName]: buildOptionalFieldToggles(nextModel),
    }));
    setSelectedModelName(trimmedName);
    setCurrentPage(1);
    return true;
  };

  const deleteModel = (name) => {
    const nextModels = models.filter((model) => model.name !== name);
    setModels(nextModels);
    setOptionalFieldToggles((prev) => {
      const next = { ...prev };
      delete next[name];
      return next;
    });
    setSelectedModelNames((previous) =>
      previous.filter((item) => item !== name),
    );
    if (selectedModelName === name) {
      setSelectedModelName(nextModels[0]?.name || '');
    }
  };

  const applySelectedModelPricing = () => {
    if (!selectedModel) {
      showError(t('请先选择一个作为模板的模型'));
      return false;
    }
    if (selectedModelNames.length === 0) {
      showError(t('请先勾选需要批量设置的模型'));
      return false;
    }

    const sourceToggles = optionalFieldToggles[selectedModel.name] || {};

    // 批量应用后目标模型长什么样。跳过判断和实际写入共用这一个投影——两处各抄一份
    // 字段列表，往后加一个价格字段就会悄悄分叉，判断依据和真正写进去的东西不一致。
    // videoMatrix 刻意不在列表里：矩阵不参与批量，所以投影里它保持目标自己的值，
    // 下面的 isVideoMatrixMissingAnchor 才问得出「套用之后这张矩阵还成立吗」。
    const projectBatchTarget = (model) => {
      const next = {
        ...model,
        billingMode: selectedModel.billingMode,
        fixedPrice: selectedModel.fixedPrice,
        inputPrice: selectedModel.inputPrice,
        completionPrice: selectedModel.completionPrice,
        cachePrice: selectedModel.cachePrice,
        createCachePrice: selectedModel.createCachePrice,
        imagePrice: selectedModel.imagePrice,
        audioInputPrice: selectedModel.audioInputPrice,
        audioOutputPrice: selectedModel.audioOutputPrice,
        billingExpr: selectedModel.billingExpr || '',
        requestRuleExpr: selectedModel.requestRuleExpr || '',
      };

      if (
        next.billingMode === 'per-token' &&
        next.completionRatioLocked &&
        hasValue(next.inputPrice) &&
        hasValue(next.lockedCompletionRatio)
      ) {
        next.completionPrice = formatNumber(
          Number(next.inputPrice) * Number(next.lockedCompletionRatio),
        );
      }

      return next;
    };

    // 转移入口 6（清单见 handleBillingModeChange 上方）：模板的价格字段会覆盖目标的，
    // 于是一个原本有锚点的目标可能被刷成没锚点，而它的 token 矩阵还留在那儿——就地
    // 造出非法态。矩阵不在批量范围内，改不了它，只能整个跳过该目标。
    //
    // 判据直接套投影后的 isVideoMatrixMissingAnchor，不手写条件：手写版只想到了
    // 「模板是 tiered_expr」这一种，漏掉了「模板是留空的按量」等同样会抹掉锚点的情形。
    const skipped = models
      .filter((model) => selectedModelNames.includes(model.name))
      .filter((model) => isVideoMatrixMissingAnchor(projectBatchTarget(model)))
      .map((model) => model.name);
    const skippedSet = new Set(skipped);

    // 全部目标都被跳过时不能当成功：返回 false 让弹窗留在原地，勾选状态不丢，
    // 运营看完错误提示可以直接改，不用从头再勾一遍。
    if (skipped.length === selectedModelNames.length) {
      showError(
        t(
          '{{count}} 个模型都被跳过：套用这套价格后，它们的「按 Token」视频计费矩阵会失去预扣锚点，视频任务将被直接拒绝。请先给模板填好按量或按次价格，或把这些模型的矩阵改为「按次」／关闭矩阵。',
          { count: skipped.length },
        ),
      );
      return false;
    }

    setModels((previous) =>
      previous.map((model) => {
        if (
          !selectedModelNames.includes(model.name) ||
          skippedSet.has(model.name)
        ) {
          return model;
        }
        return projectBatchTarget(model);
      }),
    );

    setOptionalFieldToggles((previous) => {
      const next = { ...previous };
      selectedModelNames.forEach((modelName) => {
        if (skippedSet.has(modelName)) {
          return;
        }
        const targetModel = models.find((item) => item.name === modelName);
        next[modelName] = {
          completionPrice: targetModel?.completionRatioLocked
            ? true
            : Boolean(sourceToggles.completionPrice),
          cachePrice: Boolean(sourceToggles.cachePrice),
          createCachePrice: Boolean(sourceToggles.createCachePrice),
          imagePrice: Boolean(sourceToggles.imagePrice),
          audioInputPrice: Boolean(sourceToggles.audioInputPrice),
          audioOutputPrice:
            Boolean(sourceToggles.audioInputPrice) &&
            Boolean(sourceToggles.audioOutputPrice),
        };
      });
      return next;
    });

    // 走到这里 skipped 一定是真子集（全跳过的情况上面已经 return false），
    // 所以这条成功提示的计数必然 > 0。
    showSuccess(
      t('已将模型 {{name}} 的价格配置批量应用到 {{count}} 个模型', {
        name: selectedModel.name,
        count: selectedModelNames.length - skipped.length,
      }),
    );
    if (skipped.length > 0) {
      showError(
        t(
          '已跳过 {{count}} 个模型（{{names}}）：套用这套价格后，它们的「按 Token」视频计费矩阵会失去预扣锚点。请先把这些模型的矩阵改为「按次」或关闭矩阵。',
          { count: skipped.length, names: skipped.join('、') },
        ),
      );
    }
    return true;
  };

  const handleSubmit = async () => {
    setLoading(true);
    try {
      const output = {
        ModelPrice: {},
        ModelRatio: {},
        CompletionRatio: {},
        CacheRatio: {},
        CreateCacheRatio: {},
        ImageRatio: {},
        AudioRatio: {},
        AudioCompletionRatio: {},
      };

      const tieredOutput = {
        'billing_setting.billing_mode': {},
        'billing_setting.billing_expr': {},
      };

      // 全量重写，与上面的 output 同一套语义：关掉开关的模型自然从配置里消失，
      // 不需要单独的删除逻辑。
      const videoOutput = { VideoPricingConfig: {} };

      for (const model of models) {
        if (model.videoMatrix) {
          // 半张表生效造成的错账比保存失败糟得多——与后端
          // UpdateVideoPricingByJSONString 的整体校验同一个理由。
          if (isVideoMatrixMissingAnchor(model)) {
            throw new Error(
              t(
                '模型 {{name}} 启用了「按 Token」的视频计费矩阵，但没有配置按量或按次价格作为预扣锚点',
                { name: model.name },
              ),
            );
          }
          const entry = serializeVideoMatrix(model.videoMatrix, rate);
          if (entry) {
            videoOutput.VideoPricingConfig[model.name] = entry;
          }
        }

        if (model.billingMode === 'tiered_expr') {
          const finalBillingExpr = combineBillingExpr(
            model.billingExpr,
            model.requestRuleExpr,
          );
          if (finalBillingExpr) {
            tieredOutput['billing_setting.billing_mode'][model.name] =
              'tiered_expr';
            tieredOutput['billing_setting.billing_expr'][model.name] =
              finalBillingExpr;
          }
        }

        // Always serialize ratio/price values for all models (including
        // tiered_expr) so they serve as fallback during multi-instance sync
        // delay.  ModelPriceHelper checks billing_mode first, so these values
        // are only used when billing_setting hasn't propagated yet.
        const writeSerialized = (source) => {
          Object.entries(serializeModel(source, t, rate)).forEach(
            ([key, value]) => {
              if (value !== null) {
                output[key][model.name] = value;
              }
            },
          );
        };
        try {
          writeSerialized(model);
        } catch (e) {
          if (model.billingMode !== 'tiered_expr') {
            throw e;
          }
          // tiered 的界面不渲染价格输入框，所以转换失败只可能来自「本次会话里从按量
          // 切过来、留下半填价格」这一种状态。不能就此放过：handleSubmit 是全量重写，
          // 这一轮不写 ModelRatio 就等于把 DB 里的删掉。剥掉价格字段重来一次，让
          // serializeModel 走「inputPrice === null」那条分支，把 buildModelState
          // 原样带过来的 rawRatios 逐字回写——保住既有配置，只丢掉那半填的输入。
          writeSerialized({
            ...model,
            inputPrice: null,
            completionPrice: null,
            cachePrice: null,
            createCachePrice: null,
            imagePrice: null,
            audioInputPrice: null,
            audioOutputPrice: null,
          });
        }
      }

      const requestQueue = [
        ...Object.entries(output).map(([key, value]) =>
          API.put('/api/option/', {
            key,
            value: JSON.stringify(value, null, 2),
          }),
        ),
        ...Object.entries(tieredOutput).map(([key, value]) =>
          API.put('/api/option/', {
            key,
            value: JSON.stringify(value, null, 2),
          }),
        ),
        ...Object.entries(videoOutput).map(([key, value]) =>
          API.put('/api/option/', {
            key,
            value: JSON.stringify(value, null, 2),
          }),
        ),
      ];

      const results = await Promise.all(requestQueue);
      for (const res of results) {
        if (!res?.data?.success) {
          throw new Error(res?.data?.message || t('保存失败，请重试'));
        }
      }

      showSuccess(t('保存成功'));
      await refresh();
    } catch (error) {
      console.error('保存失败:', error);
      showError(error.message || t('保存失败，请重试'));
    } finally {
      setLoading(false);
    }
  };

  return {
    models,
    selectedModel,
    selectedModelName,
    selectedModelNames,
    setSelectedModelName,
    setSelectedModelNames,
    searchText,
    setSearchText,
    currentPage,
    setCurrentPage,
    loading,
    conflictOnly,
    setConflictOnly,
    filteredModels,
    pagedData,
    selectedWarnings,
    previewRows,
    isOptionalFieldEnabled,
    handleOptionalFieldToggle,
    handleNumericFieldChange,
    handleBillingModeChange,
    handleBillingExprChange,
    handleRequestRuleExprChange,
    handleVideoMatrixChange,
    handleVideoMatrixToggle,
    handleSubmit,
    addModel,
    deleteModel,
    applySelectedModelPricing,
  };
}
