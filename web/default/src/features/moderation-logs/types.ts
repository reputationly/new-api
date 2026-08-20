export interface ModerationLog {
  id: number
  user_id: number
  token_id: number
  channel_id: number
  username: string
  group: string
  policy: string
  task_id: string
  request_id: string
  model_name: string
  source: string
  stage: string
  modality: string
  action: string
  /**
   * 该判定是否真的执行了。observe 模式下 action 仍是 block 但请求正常放行，
   * 判定列必须据此区分，否则「拦了多少」会把观察期的误杀一起算进去。
   */
  enforced: boolean
  /** 逗号分隔 */
  categories: string
  score: number
  provider: string
  /** 逗号分隔的命中关键词，值是配置页里的原文条目 */
  words: string
  object_key: string
  content_hash: string
  preview: string
  detail: string
  created_at: number
  /** 后端算好的「有无可解密原文」，前端拿不到密文本身 */
  has_content: boolean
}

export interface ModerationLogFilters {
  startTime: Date
  endTime: Date
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

export interface GetModerationLogsParams {
  p: number
  page_size: number
  start_timestamp?: number
  end_timestamp?: number
  username?: string
  word?: string
  action?: string
  source?: string
  category?: string
  model_name?: string
  group?: string
  channel_id?: string
  request_id?: string
}

export interface GetModerationLogsResponse {
  success: boolean
  message?: string
  data?: {
    items: ModerationLog[]
    total: number
    page: number
    page_size: number
  }
}

export interface GetModerationLogContentResponse {
  success: boolean
  message?: string
  data?: {
    content: string
  }
}
