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
  { value: 'cyber', label: '网络攻击内容' },
  { value: 'advice', label: '违规专业建议' },
  { value: 'minor', label: '危害未成年人的内容' },
  { value: 'terror', label: '暴恐极端内容' },
  { value: 'vulgar', label: '低俗不良内容' },
];

/**
 * **开箱默认**：内置策略与新建策略每一类该填什么。
 * 与 system_setting.defaultCategoryActions 逐条一致。
 *
 * 注意它**不是**「策略里缺这个键时怎么判」——后者见
 * moderationAbsentCategoryAction，覆盖面窄得多。把两者当成一回事会让界面
 * 显示出后端并不会执行的动作。
 */
export const MODERATION_CATEGORY_DEFAULTS = {
  sexual: 'block',
  illegal: 'block',
  political: 'block',
  jailbreak: 'block',
  violent: 'log',
  self_harm: 'log',
  unethical: 'log',
  pii: 'ignore',
  copyright: 'ignore',
  cyber: 'log',
  advice: 'ignore',
  minor: 'block',
  terror: 'block',
  vulgar: 'log',
};

/**
 * 策略里**缺这个键**时才用的兜底，只含接众森卫士时新增的五类。
 * 与 system_setting.newCategoryDefaults 逐条一致。
 *
 * 原来的九类刻意不在这里：它们的「缺键 = 直接拒绝」是既有契约（下面
 * updateCategory 的注释也说了，存量策略与手写 JSON 都不要求九类齐全）。
 * 拿开箱默认去兜全部类别会把存量策略静默放松，对「严格」这类策略尤其致命。
 */
const MODERATION_NEW_CATEGORY_DEFAULTS = {
  cyber: 'log',
  advice: 'ignore',
  minor: 'block',
  terror: 'block',
  vulgar: 'log',
};

/**
 * 策略里没配这一类时，后端**真会执行**的动作。
 * 界面显示的必须是这个值——显示成别的就是在骗人，而这一列的全部价值
 * 就是让运营据它决策。与 system_setting.AbsentCategoryAction 一致。
 */
export const moderationAbsentCategoryAction = (category) =>
  MODERATION_NEW_CATEGORY_DEFAULTS[category] ?? 'block';

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
 * 判定协议（dialect）。节点上部署的是哪个模型，决定了请求怎么发、输出怎么解析。
 * value 与 system_setting.Dialect* 一致。
 */
export const MODERATION_TEXT_DIALECTS = [
  {
    value: 'qwen3guard',
    label: 'Qwen3Guard',
    model: 'qwen3guard',
    inputLimit: 24000,
    desc: '九类判定，有「有争议」中间档（严格度对它生效）',
  },
  {
    value: 'zhongsen-text',
    label: '众森卫士 Text',
    model: 'Zhongsen-Text-8b',
    inputLimit: 1500,
    desc: '29 类细分标签 + 归因理由；二分判定，严格度对它无效',
  },
];

/**
 * 与 system_setting.ImageDialects 一致：这里列的是**已有解析实现、可以安全选中**的，
 * 不是「我们认识的名字」。
 *
 * 众森多模态（zsws-multimodal）故意不在这里——图片侧还没有 dialect 分派，
 * 选中它会给 ZSWS 发 ShieldGemma 的请求，每次判定都失败，而「审核失败时放行」
 * 默认开着，于是图片审核静默停摆而「测试连接」照样报绿。
 * 标一句「暂未实现」不是护栏：能被点到的选项就会被点。
 */
export const MODERATION_IMAGE_DIALECTS = [
  {
    value: 'shieldgemma2',
    label: 'ShieldGemma 2',
    model: 'shieldgemma2',
    desc: '三条固定策略，P(Yes) 连续分数（严格度阈值对它生效）',
  },
];

export const moderationDialectsFor = (modality) =>
  modality === 'image' ? MODERATION_IMAGE_DIALECTS : MODERATION_TEXT_DIALECTS;

export const moderationDialectDefault = (modality) =>
  modality === 'image' ? 'shieldgemma2' : 'qwen3guard';

/**
 * 每个 dialect 实际能产出的类别，与 system_setting.dialectCoveredCategories 一致。
 * 改这里必须同步改那边，否则界面上的覆盖标注会和实际判定能力对不上。
 *
 * 这个标注必须显示出来：不标的话，配了「政治 → 直接拒绝」的人会以为涉政图片被
 * 拦住了，而 ShieldGemma 对涉政一点覆盖都没有——连 L0 关键词那样的兜底都没有
 * （AC 自动机扫不了图）。「以为配了其实没有」正是这套系统最不能出的错。
 */
export const MODERATION_DIALECT_COVERED = {
  qwen3guard: new Set([
    'sexual',
    'illegal',
    'political',
    'jailbreak',
    'violent',
    'self_harm',
    'unethical',
    'pii',
    'copyright',
  ]),
  'zhongsen-text': new Set([
    'sexual',
    'illegal',
    'political',
    'violent',
    'self_harm',
    'unethical',
    'pii',
    'cyber',
    'advice',
    'minor',
    'terror',
  ]),
  shieldgemma2: new Set(['sexual', 'illegal', 'violent']),
  'zsws-multimodal': new Set([
    'political',
    'violent',
    'sexual',
    'vulgar',
    'illegal',
  ]),
};

export const moderationDialectCovers = (dialect, category) =>
  MODERATION_DIALECT_COVERED[dialect]?.has(category) ?? false;

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
 *
 * 还有一件更容易踩的：**它只对提供中间档的 dialect 有意义**。
 * 众森卫士 Text 是二分判定（安全 vs 28 个风险码），严格度对它完全无效，
 * 松紧只能靠下面的类别处置表调。dialect 不支持时界面上必须标出来。
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

/** 有「有争议」中间档、严格度真正生效的 dialect。 */
export const MODERATION_STRICTNESS_DIALECTS = new Set([
  'qwen3guard',
  'shieldgemma2',
]);

export const moderationStrictnessApplies = (dialect) =>
  MODERATION_STRICTNESS_DIALECTS.has(dialect);

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
