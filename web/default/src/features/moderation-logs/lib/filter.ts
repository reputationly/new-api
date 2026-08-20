import type { GetModerationLogsParams, ModerationLogFilters } from '../types'

/** 默认看今天：审核记录的典型用法是「刚有人反馈发不出去」，不是翻旧账。 */
export function defaultTimeRange(): ModerationLogFilters {
  const now = new Date()
  const startTime = new Date(now)
  startTime.setHours(0, 0, 0, 0)
  const endTime = new Date(now.getTime() + 3600 * 1000)
  return { startTime, endTime }
}

/** URL search 参数。时间用毫秒时间戳，转秒留到发请求时做。 */
export type ModerationLogsSearch = {
  page?: number
  pageSize?: number
  startTime?: number
  endTime?: number
  username?: string
  word?: string
  action?: string
  source?: string
  category?: string
  modelName?: string
  group?: string
  channelId?: string
  requestId?: string
}

/** 空值一律不写进 URL，否则地址栏会攒一堆 `&group=` 这种没有意义的参数。 */
function omitEmpty<T extends Record<string, unknown>>(obj: T): Partial<T> {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(obj)) {
    if (v !== undefined && v !== '' && v !== null) out[k] = v
  }
  return out as Partial<T>
}

export function filtersToSearch(
  filters: ModerationLogFilters
): ModerationLogsSearch {
  return omitEmpty({
    startTime: filters.startTime?.getTime(),
    endTime: filters.endTime?.getTime(),
    username: filters.username,
    word: filters.word,
    action: filters.action,
    source: filters.source,
    category: filters.category,
    modelName: filters.modelName,
    group: filters.group,
    channelId: filters.channelId,
    requestId: filters.requestId,
  })
}

export function searchToFilters(
  search: ModerationLogsSearch
): ModerationLogFilters {
  const fallback = defaultTimeRange()
  return {
    startTime: search.startTime
      ? new Date(search.startTime)
      : fallback.startTime,
    endTime: search.endTime ? new Date(search.endTime) : fallback.endTime,
    username: search.username,
    word: search.word,
    action: search.action,
    source: search.source,
    category: search.category,
    modelName: search.modelName,
    group: search.group,
    channelId: search.channelId,
    requestId: search.requestId,
  }
}

export function searchToParams(
  search: ModerationLogsSearch,
  page: number,
  pageSize: number
): GetModerationLogsParams {
  const fallback = defaultTimeRange()
  // 时间戳转秒：moderation_logs.created_at 存的是 Unix 秒（见 model/moderation_log.go）。
  const startMs = search.startTime ?? fallback.startTime.getTime()
  const endMs = search.endTime ?? fallback.endTime.getTime()

  return {
    p: page,
    page_size: pageSize,
    start_timestamp: Math.floor(startMs / 1000),
    end_timestamp: Math.floor(endMs / 1000),
    ...omitEmpty({
      username: search.username,
      word: search.word,
      action: search.action,
      source: search.source,
      category: search.category,
      model_name: search.modelName,
      group: search.group,
      channel_id: search.channelId,
      request_id: search.requestId,
    }),
  }
}
