/**
 * Admin user message logs API + viewer (授权用户) endpoints.
 *
 * 明文留存所有进站消息（独立于提示词审计）。管理员走 /admin 前缀（全量）；
 * 被授权查看者走 /user-message-logs 前缀（服务端强制收敛到授权分组）。
 */

import { apiClient } from '../client'
import type { PaginatedResponse } from '@/types'

export interface UserMessageLog {
  id: number
  created_at: string
  request_id: string
  user_id?: number
  username: string
  user_email: string
  api_key_id?: number
  api_key_name: string
  group_id?: number
  group_name: string
  provider: string
  endpoint: string
  protocol: string
  model: string
  stream: boolean
  stage: string
  /** 列表接口只回前 300 字符预览；全文走 getUserMessageLog */
  content: string
  content_length: number
  truncated: boolean
}

export interface UserMessageLogQuery {
  page?: number
  page_size?: number
  user_id?: number
  group_id?: number
  model?: string
  q?: string
  start_time?: string
  end_time?: string
}

export type UserMessageLogListResponse = PaginatedResponse<UserMessageLog>

export interface GroupUsageRankingRow {
  user_id: number
  username: string
  user_email: string
  requests: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  total_cost: number
}

export interface UserMessageViewer {
  id: number
  user_id: number
  username: string
  user_email: string
  group_id: number
  group_name: string
  created_at: string
}

const ADMIN_BASE = '/admin/user-message-logs'
const VIEWER_BASE = '/user-message-logs'

export async function listUserMessageLogs(params: UserMessageLogQuery, viewer = false): Promise<UserMessageLogListResponse> {
  const { data } = await apiClient.get<UserMessageLogListResponse>(viewer ? VIEWER_BASE : ADMIN_BASE, { params })
  return data
}

export async function getUserMessageLog(id: number, viewer = false): Promise<UserMessageLog> {
  // apiClient 拦截器已解 {code,message,data} 信封，这里 data 即记录本体。
  const { data } = await apiClient.get<UserMessageLog>(`${viewer ? VIEWER_BASE : ADMIN_BASE}/detail/${id}`)
  return data
}

export async function myViewerGroups(): Promise<Array<{ id: number; name: string }>> {
  const { data } = await apiClient.get<Array<{ id: number; name: string }>>(`${VIEWER_BASE}/my-groups`)
  return data ?? []
}

export async function groupUsageRanking(groupId: number, days = 7, viewer = false): Promise<GroupUsageRankingRow[]> {
  const { data } = await apiClient.get<{ items: GroupUsageRankingRow[] }>(
    `${viewer ? VIEWER_BASE : ADMIN_BASE}/usage-summary`,
    { params: { group_id: groupId, days } }
  )
  return data.items ?? []
}

export async function listUserMessageViewers(): Promise<UserMessageViewer[]> {
  const { data } = await apiClient.get<UserMessageViewer[]>(`${ADMIN_BASE}/viewers`)
  return data
}

export async function grantUserMessageViewer(userId: number, groupId: number): Promise<void> {
  await apiClient.post(`${ADMIN_BASE}/viewers`, { user_id: userId, group_id: groupId })
}

export async function revokeUserMessageViewer(id: number): Promise<void> {
  await apiClient.delete(`${ADMIN_BASE}/viewers/${id}`)
}

const userMessageLogAPI = {
  list: listUserMessageLogs,
  get: getUserMessageLog,
  myViewerGroups,
  groupUsageRanking,
  listUserMessageViewers,
  grantUserMessageViewer,
  revokeUserMessageViewer,
}

export default userMessageLogAPI
