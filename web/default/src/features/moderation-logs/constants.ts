import type { StatusVariant } from '@/components/status-badge'

/** 审核动作。value 与后端 model.ModerationAction* 一致，label 走 i18n。 */
export const MODERATION_ACTIONS = [
  { value: 'block', label: 'Blocked' },
  { value: 'pass', label: 'Passed' },
  { value: 'review', label: 'Pending Review' },
  { value: 'mask', label: 'Masked' },
  { value: 'error', label: 'Moderation Error' },
] as const

export const MODERATION_ACTION_VARIANTS: Record<string, StatusVariant> = {
  block: 'red',
  pass: 'green',
  review: 'orange',
  mask: 'blue',
  error: 'grey',
}

/** 判定来源。upstream 表示是上游供应商拒的，不是本站拦的。 */
export const MODERATION_SOURCES = [
  { value: 'self', label: 'Moderated by this site' },
  { value: 'upstream', label: 'Rejected by upstream' },
] as const

/**
 * 类别。与 service/moderation/reason.go 的 categoryLabels 一一对应。
 * 两处必须同步：这里改了那里没改，页面和拒绝文案会对同一条记录给出两种说法。
 */
export const MODERATION_CATEGORIES = [
  { value: 'keyword', label: 'Sensitive Word' },
  { value: 'sexual', label: 'Sexual Content' },
  { value: 'illegal', label: 'Illegal Content' },
  { value: 'political', label: 'Politically Sensitive' },
  { value: 'jailbreak', label: 'Jailbreak Prompt' },
  { value: 'violent', label: 'Violent Content' },
  { value: 'self_harm', label: 'Self-Harm Content' },
  { value: 'unethical', label: 'Unethical Content' },
  { value: 'pii', label: 'Personal Information' },
  { value: 'copyright', label: 'Copyright Risk' },
] as const

function toLabelMap(
  items: readonly { value: string; label: string }[]
): Record<string, string> {
  return Object.fromEntries(items.map((i) => [i.value, i.label]))
}

const categoryLabelMap = toLabelMap(MODERATION_CATEGORIES)
const actionLabelMap = toLabelMap(MODERATION_ACTIONS)
const sourceLabelMap = toLabelMap(MODERATION_SOURCES)

type Translate = (key: string) => string

/** 未登记的取值原样显示，不吞掉——吞了就看不出后端新加了类别。 */
export function categoryLabel(t: Translate, value: string): string {
  const label = categoryLabelMap[value]
  return label ? t(label) : value
}

export function actionLabel(t: Translate, value: string): string {
  const label = actionLabelMap[value]
  return label ? t(label) : value
}

export function sourceLabel(t: Translate, value: string): string {
  const label = sourceLabelMap[value]
  return label ? t(label) : value
}
