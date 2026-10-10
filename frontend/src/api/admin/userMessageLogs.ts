/**
 * Admin user message logs API.
 *
 * 明文留存所有进站消息（独立于提示词审计），仅管理员可查。
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

export async function listUserMessageLogs(params: UserMessageLogQuery): Promise<UserMessageLogListResponse> {
  const { data } = await apiClient.get<UserMessageLogListResponse>('/admin/user-message-logs', { params })
  return data
}

export async function getUserMessageLog(id: number): Promise<UserMessageLog> {
  const { data } = await apiClient.get<{ code: number; data: UserMessageLog }>(`/admin/user-message-logs/${id}`)
  return data.data
}

const userMessageLogAPI = {
  list: listUserMessageLogs,
  get: getUserMessageLog,
}

export default userMessageLogAPI
