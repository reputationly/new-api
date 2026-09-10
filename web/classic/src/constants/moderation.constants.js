// 内容审核记录页的枚举。见 docs/content-moderation-design.md §10。

/** 处置动作。value 与 service/moderation 的 Action* 一致。 */
export const MODERATION_ACTIONS = [
  { value: 'block', label: '拦截', color: 'red' },
  { value: 'pass', label: '放行', color: 'green' },
  { value: 'review', label: '待复核', color: 'orange' },
  { value: 'mask', label: '替换', color: 'blue' },
  { value: 'error', label: '审核异常', color: 'grey' },
];

/** 判定来源。upstream 表示是上游供应商拒的，不是本站拦的。 */
export const MODERATION_SOURCES = [
  { value: 'self', label: '本站审核' },
  { value: 'upstream', label: '上游拒绝' },
];

/**
 * 风险类别。必须与 service/moderation/reason.go 的 categoryLabels 逐条对应：
 * 这里改了那边没改，页面和用户收到的拒绝文案会对同一条记录给出两种说法。
 */
export const MODERATION_CATEGORIES = [
  { value: 'keyword', label: '敏感词' },
  { value: 'sexual', label: '色情内容' },
  { value: 'illegal', label: '违法违规内容' },
  { value: 'political', label: '政治敏感内容' },
  { value: 'jailbreak', label: '越狱指令' },
  { value: 'violent', label: '暴力内容' },
  { value: 'self_harm', label: '自伤自杀内容' },
  { value: 'unethical', label: '违背公序良俗的内容' },
  { value: 'pii', label: '个人隐私信息' },
  { value: 'copyright', label: '版权风险内容' },
];

/** words 列的分隔符，与 model.ModerationWordsSep 一致（词条本身可能含逗号）。 */
export const MODERATION_WORDS_SEP = '\n';

/**
 * 策略里可配置的类别（不含 keyword —— L0 没有类别概念，命中恒为拒绝，
 * 想放宽只能改词库）。顺序与 system_setting.AllCategories 一致。
 */
export const MODERATION_POLICY_CATEGORIES = MODERATION_CATEGORIES.filter(
  (c) => c.value !== 'keyword',
);

/**
 * 图片/视频判定实际能产出的类别，与 system_setting.ImageCoveredCategories 一致。
 *
 * ShieldGemma 2 只有三条固定策略，是训练时定死的。这个集合必须在界面上标出来：
 * 不标的话，配了「政治 → 直接拒绝」的人会以为涉政图片被拦住了，而图片侧对涉政
 * 一点覆盖都没有——连 L0 关键词那样的兜底都没有（AC 自动机扫不了图）。
 */
export const MODERATION_IMAGE_COVERED = new Set([
  'sexual',
  'illegal',
  'violent',
]);

/** 类别处置。value 与 system_setting.CategoryAction* 一致。 */
export const MODERATION_CATEGORY_ACTIONS = [
  { value: 'block', label: '直接拒绝', color: 'red' },
  { value: 'log', label: '仅记录', color: 'orange' },
  { value: 'ignore', label: '不处理', color: 'grey' },
];

/**
 * 判定严格度。它**只影响两件事**，文案必须说清楚，否则会被当成一个全局松紧旋钮：
 * 文本侧决定模型判「有争议」那一档怎么算，图片侧决定 P(Yes) 的阈值。
 * 对「明确违规」和「明确安全」的判定毫无影响。
 */
export const MODERATION_STRICTNESS = [
  {
    value: 'loose',
    label: '宽松',
    desc: '有争议的内容放行；图片需 P(Yes) > 0.8 才算违规',
  },
  {
    value: 'standard',
    label: '标准',
    desc: '有争议的记录但不拦；图片阈值 0.5，与模型自身切点一致',
  },
  {
    value: 'strict',
    label: '严格',
    desc: '有争议的按违规处置；图片阈值降到 0.2，召回高但误杀也多',
  },
];

/** 分组可选的运行模式。空值 = 跟随全局。 */
export const MODERATION_GROUP_MODES = [
  { value: '', label: '跟随全局' },
  { value: 'off', label: '关闭' },
  { value: 'observe', label: '仅观察' },
  { value: 'blocking', label: '拦截' },
];

export const moderationCategoryActionLabel = (v) =>
  findLabel(MODERATION_CATEGORY_ACTIONS, v);

function findLabel(list, value) {
  const hit = list.find((i) => i.value === value);
  // 找不到就原样显示，不吞掉——吞了就看不出后端新加了取值。
  return hit ? hit.label : value;
}

export const moderationActionLabel = (v) => findLabel(MODERATION_ACTIONS, v);
export const moderationSourceLabel = (v) => findLabel(MODERATION_SOURCES, v);
export const moderationCategoryLabel = (v) =>
  findLabel(MODERATION_CATEGORIES, v);

export const moderationActionColor = (v) => {
  const hit = MODERATION_ACTIONS.find((i) => i.value === v);
  return hit ? hit.color : 'grey';
};
