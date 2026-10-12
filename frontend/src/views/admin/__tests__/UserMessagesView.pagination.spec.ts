import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import UserMessagesView from '../UserMessagesView.vue'

const { listUserMessageLogs, groupUsageRanking, myViewerGroups, listUserMessageViewers, getGroups } = vi.hoisted(() => ({
  listUserMessageLogs: vi.fn(),
  groupUsageRanking: vi.fn(),
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
  myViewerGroups,
  listUserMessageViewers,
  grantUserMessageViewer: vi.fn(),
  revokeUserMessageViewer: vi.fn(),
}))

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
    groupUsageRanking.mockResolvedValue([])
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
})
