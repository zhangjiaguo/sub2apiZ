import { beforeEach, describe, expect, it, vi } from 'vitest'
import { exportGroupUsageReport } from '@/api/admin/userMessageLogs'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get } }))

describe('用户用量完整报表下载', () => {
  beforeEach(() => vi.clearAllMocks())

  it('不发送当前分页，读取 UTF-8 中文文件名', async () => {
    const blob = new Blob(['报表'])
    const filename = '用户用量_分组20_近7天.csv'
    get.mockResolvedValue({ data: blob, headers: { 'content-disposition': `attachment; filename*=utf-8''${encodeURIComponent(filename)}` } })
    expect(await exportGroupUsageReport(20, 7, false)).toEqual({ blob, filename })
    expect(get).toHaveBeenCalledWith('/admin/user-message-logs/usage-summary/export', { params: { group_id: 20, days: 7, format: 'csv' }, responseType: 'blob' })
  })

  it('查看者请求保持分组范围，无效文件名头安全回退', async () => {
    const blob = new Blob(['报表'])
    get.mockResolvedValue({ data: blob, headers: { 'content-disposition': "attachment; filename*=utf-8''%INVALID" } })
    const report = await exportGroupUsageReport(20, 30, true)
    expect(report.filename).toBe('用户用量_分组20_近30天.csv')
    expect(get).toHaveBeenCalledWith('/user-message-logs/usage-summary/export', { params: { group_id: 20, days: 30, format: 'csv' }, responseType: 'blob' })
  })
})
