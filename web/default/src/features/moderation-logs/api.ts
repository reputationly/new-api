import { api } from '@/lib/api'
import type {
  GetModerationLogContentResponse,
  GetModerationLogsParams,
  GetModerationLogsResponse,
} from './types'

export async function getModerationLogs(
  params: GetModerationLogsParams
): Promise<GetModerationLogsResponse> {
  const res = await api.get('/api/moderation/logs', { params })
  return res.data
}

/**
 * 取被拦内容原文。后端会解密并写一条管理操作审计——
 * 每调一次就留一条痕，不要在列表渲染时预取。
 */
export async function getModerationLogContent(
  id: number
): Promise<GetModerationLogContentResponse> {
  const res = await api.get(`/api/moderation/logs/${id}/content`)
  return res.data
}
