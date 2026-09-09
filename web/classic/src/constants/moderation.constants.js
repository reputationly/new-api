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
