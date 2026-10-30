import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import UserMessagesView from '../UserMessagesView.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'

const { listUserMessageLogs, groupUsageRanking, exportGroupUsageReport, saveAs, myViewerGroups, listUserMessageViewers, getGroups } = vi.hoisted(() => ({
  listUserMessageLogs: vi.fn(),
  groupUsageRanking: vi.fn(),
  exportGroupUsageReport: vi.fn(),
  saveAs: vi.fn(),
  myViewerGroups: vi.fn(),
  listUserMessageViewers: vi.fn(),
  getGroups: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: { getAll: getGroups },
  },
  listUserMessageLogs,
  groupUsageRanking,
  exportGroupUsageReport,
  myViewerGroups,
  listUserMessageViewers,
  grantUserMessageViewer: vi.fn(),
  revokeUserMessageViewer: vi.fn(),
}))

vi.mock('file-saver', () => ({ saveAs }))

vi.mock('@/api/client', () => ({
  apiClient: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({ isAdmin: true, user: null }),
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {} }),
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({ global: { t: (k: string) => k } }),
  useI18n: () => ({
    t: (key: string, params?: Record<string, string | number>) =>
      params ? `${key}:${JSON.stringify(params)}` : key,
  }),
}))

function fakeLogs(count: number) {
  return Array.from({ length: count }, (_, i) => ({
    id: i + 1,
    created_at: '2026-10-10T12:00:00Z',
    request_id: `req-${i}`,
    user_id: 1,
    username: 'u',
    user_email: 'u@x.com',
    api_key_id: 2,
    api_key_name: 'k',
    group_id: 20,
    group_name: 'g',
    provider: 'openai',
    endpoint: '/v1/responses',
    protocol: 'openai_responses',
    model: 'gpt-5.6-sol',
    stream: true,
    stage: 'http',
    content: 'hello world',
    content_length: 11,
    truncated: false,
  }))
}

describe('UserMessagesView pagination', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getGroups.mockResolvedValue([{ id: 20, name: 'G' }])
    groupUsageRanking.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
    listUserMessageViewers.mockResolvedValue([])
    listUserMessageLogs.mockResolvedValue({
      items: fakeLogs(20),
      total: 917,
      page: 1,
      page_size: 20,
      pages: 46,
    })
  })

  it('total > 0 时渲染 Pagination 且带总条数', async () => {
    const wrapper = mount(UserMessagesView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="actions" /><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
          teleport: true,
        },
      },
    })
    await flushPromises()

    // 找到 Pagination 渲染产物（“显示 x 到 y，共 total 条”的信息行）
    const html = wrapper.html()
    expect(html).toContain('pagination.showing')
    expect(html).toContain('917')
    // 翻页按钮存在
    const buttons = wrapper.findAll('button')
    expect(buttons.length).toBeGreaterThan(3)
  })

  it('total = 0 时不渲染分页（空态）', async () => {
    listUserMessageLogs.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
    const wrapper = mount(UserMessagesView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="actions" /><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
          teleport: true,
        },
      },
    })
    await flushPromises()
    expect(wrapper.html()).not.toContain('pagination.showing')
  })

  it('管理员默认分页展示全部用户，翻页后排名继续累计', async () => {
    const usage = (id: number) => ({ user_id: id, username: `用户${id}`, user_email: '', requests: 0, input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, total_cost: 0 })
    groupUsageRanking.mockResolvedValue({ items: [usage(1)], total: 106, page: 1, page_size: 20, pages: 6 })
    const wrapper = mount(UserMessagesView, { global: { stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      TablePageLayout: { template: '<div><slot name="actions" /><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
      teleport: true,
    } } })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'admin.userMessages.tabs.usage')!.trigger('click')
    await flushPromises()
    expect(groupUsageRanking).toHaveBeenLastCalledWith(null, 7, false, { page: 1, page_size: 20 })
    expect(wrapper.findComponent(Pagination).props('total')).toBe(106)
    expect(wrapper.text()).toContain('用户1')

    groupUsageRanking.mockResolvedValue({ items: [usage(21)], total: 106, page: 2, page_size: 20, pages: 6 })
    wrapper.findComponent(Pagination).vm.$emit('update:page', 2)
    await flushPromises()
    expect(groupUsageRanking).toHaveBeenLastCalledWith(null, 7, false, { page: 2, page_size: 20 })
    expect(wrapper.find('tbody tr td').text()).toBe('21')
    expect(wrapper.text()).toContain('用户21')

    await wrapper.findAll('button').find(button => button.text() === 'admin.userMessages.tabs.messages')!.trigger('click')
    expect(wrapper.findComponent(Pagination).props('page')).toBe(1)
    expect(wrapper.findComponent(Pagination).props('total')).toBe(917)
    await wrapper.findAll('button').find(button => button.text() === 'admin.userMessages.tabs.usage')!.trigger('click')
    await flushPromises()

    groupUsageRanking.mockResolvedValue({ items: [usage(1)], total: 9, page: 1, page_size: 20, pages: 1 })
    const groupSelect = wrapper.findAllComponents(Select).find(select => select.props('options').some((option: { value: string }) => option.value === '20'))!
    groupSelect.vm.$emit('update:modelValue', '20')
    groupSelect.vm.$emit('change', '20')
    await flushPromises()
    expect(groupUsageRanking).toHaveBeenLastCalledWith(20, 7, false, { page: 1, page_size: 20 })
    expect(wrapper.findComponent(Pagination).props('page')).toBe(1)
    groupUsageRanking.mockResolvedValue({ items: [usage(1)], total: 9, page: 1, page_size: 50, pages: 1 })
    wrapper.findComponent(Pagination).vm.$emit('update:pageSize', 50)
    await flushPromises()
    expect(groupUsageRanking).toHaveBeenLastCalledWith(20, 7, false, { page: 1, page_size: 50 })
    const daysSelect = wrapper.findAllComponents(Select).find(select => select.props('options').some((option: { value: string }) => option.value === '14'))!
    daysSelect.vm.$emit('update:modelValue', '30')
    daysSelect.vm.$emit('change', '30')
    await flushPromises()
    expect(groupUsageRanking).toHaveBeenLastCalledWith(20, 30, false, { page: 1, page_size: 50 })
    wrapper.unmount()
  })

  it('较早的用量请求不能覆盖后来选择的分组结果', async () => {
    let resolveOld: (value: unknown) => void = () => {}
    groupUsageRanking.mockImplementation(() => new Promise(resolve => { resolveOld = resolve }))
    const wrapper = mount(UserMessagesView, { global: { stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      TablePageLayout: { template: '<div><slot name="actions" /><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
      teleport: true,
    } } })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'admin.userMessages.tabs.usage')!.trigger('click')
    const oldResponse = resolveOld
    groupUsageRanking.mockResolvedValue({ items: [{ user_id: 9, username: '当前分组用户', user_email: '', requests: 1, input_tokens: 1, output_tokens: 1, cache_read_tokens: 0, total_cost: 1 }], total: 1, page: 1, page_size: 20, pages: 1 })
    const groupSelect = wrapper.findAllComponents(Select).find(select => select.props('options').some((option: { value: string }) => option.value === '20'))!
    groupSelect.vm.$emit('update:modelValue', '20')
    groupSelect.vm.$emit('change', '20')
    await flushPromises()
    oldResponse({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
    await flushPromises()
    expect(wrapper.text()).toContain('当前分组用户')
    expect(wrapper.findComponent(Pagination).props('total')).toBe(1)
    wrapper.unmount()
  })

  it('在第二页一键导出全部筛选结果，不改变当前分页', async () => {
    const row = { user_id: 21, username: '用户21', user_email: '', requests: 0, input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, total_cost: 0 }
    groupUsageRanking.mockResolvedValue({ items: [row], total: 92, page: 1, page_size: 20, pages: 5 })
    const report = { blob: new Blob(['完整报表']), filename: '用户用量_全部分组_近7天.csv' }
    exportGroupUsageReport.mockResolvedValue(report)
    const wrapper = mount(UserMessagesView, { global: { stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      TablePageLayout: { template: '<div><slot name="actions" /><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
      teleport: true,
    } } })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'admin.userMessages.tabs.usage')!.trigger('click')
    await flushPromises()
    groupUsageRanking.mockResolvedValue({ items: [row], total: 92, page: 2, page_size: 20, pages: 5 })
    wrapper.findComponent(Pagination).vm.$emit('update:page', 2)
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'admin.userMessages.ranking.exportReport')!.trigger('click')
    await flushPromises()
    expect(exportGroupUsageReport).toHaveBeenCalledWith(null, 7, false)
    expect(saveAs).toHaveBeenCalledWith(report.blob, report.filename)
    expect(wrapper.findComponent(Pagination).props('page')).toBe(2)
    exportGroupUsageReport.mockRejectedValue(new Error('连接失败'))
    await wrapper.findAll('button').find(button => button.text() === 'admin.userMessages.ranking.exportReport')!.trigger('click')
    await flushPromises()
    expect(saveAs).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('admin.userMessages.ranking.exportFailed')
    wrapper.unmount()
  })
})
