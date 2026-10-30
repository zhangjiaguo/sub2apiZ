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

export async function groupUsageRanking(
  groupId: number | null,
  days = 7,
  viewer = false,
  pagination: { page: number; page_size: number } = { page: 1, page_size: 20 }
): Promise<PaginatedResponse<GroupUsageRankingRow>> {
  const { data } = await apiClient.get<PaginatedResponse<GroupUsageRankingRow>>(
    `${viewer ? VIEWER_BASE : ADMIN_BASE}/usage-summary`,
    { params: { ...(groupId === null ? {} : { group_id: groupId }), days, ...pagination } }
  )
  return data
}

export async function exportGroupUsageReport(groupId: number | null, days = 7, viewer = false): Promise<{ blob: Blob; filename: string }> {
  const { data, headers } = await apiClient.get<Blob>(`${viewer ? VIEWER_BASE : ADMIN_BASE}/usage-summary/export`, {
    params: { ...(groupId === null ? {} : { group_id: groupId }), days },
    responseType: 'blob',
  })
  const disposition = String(headers['content-disposition'] ?? '')
  const encoded = /filename\*=utf-8''([^;]+)/i.exec(disposition)
  const plain = /filename="([^"]+)"/i.exec(disposition)
  let filename = `用户用量_${groupId === null ? '全部分组' : `分组${groupId}`}_近${days}天.csv`
  try {
    filename = encoded ? decodeURIComponent(encoded[1]) : plain?.[1] || filename
  } catch {
    // 无效文件名头沿用本地生成的名称，不影响完整报表下载。
  }
  return { blob: data, filename }
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
  exportGroupUsageReport,
  listUserMessageViewers,
  grantUserMessageViewer,
  revokeUserMessageViewer,
}

export default userMessageLogAPI
