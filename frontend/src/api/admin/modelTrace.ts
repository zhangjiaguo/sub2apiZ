/**
 * Admin Codex 降智检测（modeltrace 数字分布指纹）API
 */

import { apiClient } from '../client'

/** 降智检测配置 */
export interface ModelTraceSettings {
  models: string[]
  account_ids: number[]
  bank_repeats: number
  detect_repeats: number
  concurrency: number
  probe_timeout_secs: number
  request_gap_ms: number
}

/** 任务状态 */
export interface ModelTraceTask {
  id: string
  kind: 'bank' | 'detect'
  started_at: string
  total: number
  done: number
  finished: boolean
  last_error?: string
}

/** 指纹库内单模型档案 */
export interface ModelTraceBankModel {
  model: string
  samples: number
}

/** 聚合状态 */
export interface ModelTraceStatus {
  task?: ModelTraceTask | null
  bank?: {
    built_at: string
    models: ModelTraceBankModel[]
    calibration: Record<string, number>
    ordered_weight: number
  }
  sample_counts?: Record<string, number>
}

/** 检测结果（每 账号×模型 一行） */
export interface ModelTraceResult {
  id: number
  task_id: string
  kind: 'bank' | 'detect'
  account_id: number
  model: string
  verdict: string
  probability: number
  match: boolean
  top_hits: number
  valid_runs: number
  failures: number
  avg_latency_ms: number
  reasons: string
  created_at: string
}

/** 建库样本 */
export interface ModelTraceSample {
  id: number
  model: string
  account_id: number
  expected_count: number
  numbers: number[]
  numbers_count: number
  latency_ms: number
  created_at: string
}

export async function getConfig(): Promise<ModelTraceSettings> {
  const { data } = await apiClient.get<ModelTraceSettings>('/admin/openai/modeltrace/config')
  return data
}

export async function updateConfig(settings: ModelTraceSettings): Promise<ModelTraceSettings> {
  const { data } = await apiClient.put<ModelTraceSettings>('/admin/openai/modeltrace/config', settings)
  return data
}

export async function getStatus(): Promise<ModelTraceStatus> {
  const { data } = await apiClient.get<ModelTraceStatus>('/admin/openai/modeltrace/status')
  return data
}

export async function runTask(kind: 'bank' | 'detect', models?: string[], accountIds?: number[]): Promise<ModelTraceTask> {
  const { data } = await apiClient.post<ModelTraceTask>('/admin/openai/modeltrace/run', {
    kind,
    models,
    account_ids: accountIds
  })
  return data
}

export async function getResults(taskId?: string, limit = 100, offset = 0): Promise<ModelTraceResult[]> {
  const params: Record<string, string | number> = { limit, offset }
  if (taskId) params.task_id = taskId
  const { data } = await apiClient.get<ModelTraceResult[]>('/admin/openai/modeltrace/results', { params })
  return data
}

export async function getSamples(model?: string, limit = 50): Promise<ModelTraceSample[]> {
  const params: Record<string, string | number> = { limit }
  if (model) params.model = model
  const { data } = await apiClient.get<ModelTraceSample[]>('/admin/openai/modeltrace/samples', { params })
  return data
}

export async function deleteSamples(model?: string): Promise<{ deleted: number }> {
  const params: Record<string, string> = {}
  if (model) params.model = model
  const { data } = await apiClient.delete<{ deleted: number }>('/admin/openai/modeltrace/samples', { params })
  return data
}

const modelTraceAPI = {
  getConfig,
  updateConfig,
  getStatus,
  runTask,
  getResults,
  getSamples,
  deleteSamples
}

export default modelTraceAPI
