import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../../client'
import { exportGroupUsageReport } from '../userMessageLogs'

vi.mock('../../client', () => ({ apiClient: { get: vi.fn() } }))

describe('单分组用量报表导出', () => {
  beforeEach(() => vi.clearAllMocks())

  it.each([null, 0, -1, NaN])('拒绝无效分组 %s，不发送请求', async groupId => {
    await expect(exportGroupUsageReport(groupId, 7, false, 'xlsx')).rejects.toThrow('具体分组')
    expect(apiClient.get).not.toHaveBeenCalled()
  })

  it('查看者只导出指定分组和格式，并读取中文文件名', async () => {
    const blob = new Blob(['报告'], { type: 'text/html' })
    vi.mocked(apiClient.get).mockResolvedValue({ data: blob, headers: {
      'content-disposition': "attachment; filename*=utf-8''" + encodeURIComponent('分组20报告.html'),
    } })
    const report = await exportGroupUsageReport(20, 14, true, 'html')
    expect(apiClient.get).toHaveBeenCalledWith('/user-message-logs/usage-summary/export', {
      params: { group_id: 20, days: 14, format: 'html' }, responseType: 'blob',
    })
    expect(report).toEqual({ blob, filename: '分组20报告.html' })
  })
})
