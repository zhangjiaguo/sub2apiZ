import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

type NavigationGuard = (
  to: Record<string, any>,
  from: Record<string, any>,
  next: ReturnType<typeof vi.fn>
) => Promise<void>

const routerHarness = vi.hoisted(() => ({
  guard: null as NavigationGuard | null,
  routes: [] as Array<{ path: string; meta?: Record<string, unknown> }>,
}))

const authStore = vi.hoisted(() => ({
  checkAuth: vi.fn(),
  refreshUser: vi.fn(),
  isAuthenticated: true,
  isAdmin: false,
  isSimpleMode: false,
  hasPendingAuthSession: false,
}))

const viewerGroups = vi.hoisted(() => vi.fn())
vi.mock('@/api/admin/userMessageLogs', () => ({ myViewerGroups: viewerGroups }))

const appStore = vi.hoisted(() => ({
  siteName: 'Sub2API',
  backendModeEnabled: false,
  publicSettingsLoaded: false,
  cachedPublicSettings: null as null | {
    payment_enabled?: boolean
    risk_control_enabled?: boolean
    subscription_enabled?: boolean
    custom_menu_items?: []
  },
  fetchPublicSettings: vi.fn(),
}))

vi.mock('vue-router', () => ({
  createWebHistory: vi.fn(() => ({})),
  createRouter: vi.fn((options) => {
    routerHarness.routes = options.routes
    return {
      beforeEach: vi.fn((guard: NavigationGuard) => {
        routerHarness.guard = guard
      }),
      afterEach: vi.fn(),
      onError: vi.fn(),
    }
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authStore,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => ({ customMenuItems: [] }),
}))

vi.mock('@/stores/adminCompliance', () => ({
  useAdminComplianceStore: () => ({
    initialized: true,
    fetchStatus: vi.fn(),
    requireAcknowledgement: vi.fn(),
  }),
}))

vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({
    startNavigation: vi.fn(),
    endNavigation: vi.fn(),
    isLoading: { value: false },
  }),
}))

vi.mock('@/composables/useRoutePrefetch', () => ({
  useRoutePrefetch: () => ({
    triggerPrefetch: vi.fn(),
    cancelPendingPrefetch: vi.fn(),
    resetPrefetchState: vi.fn(),
  }),
}))

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

function runGuard(meta: Record<string, unknown>, path: string) {
  if (!routerHarness.guard) {
    throw new Error('router guard was not registered')
  }

  const next = vi.fn()
  const navigation = routerHarness.guard(
    {
      path,
      fullPath: path,
      name: 'FeatureRoute',
      params: {},
      meta: { requiresAuth: true, ...meta },
    },
    {},
    next
  )
  return { navigation, next }
}

describe('feature route guard', () => {
  beforeAll(async () => {
    await import('@/router')
  })

  beforeEach(() => {
    authStore.isAuthenticated = true
    authStore.isAdmin = false
    authStore.isSimpleMode = false
    appStore.publicSettingsLoaded = false
    appStore.cachedPublicSettings = null
    appStore.fetchPublicSettings.mockReset()
  })

  it('waits for the first public-settings request before deciding payment access', async () => {
    const deferred = createDeferred<{ payment_enabled: boolean }>()
    appStore.fetchPublicSettings.mockImplementation(async () => {
      const settings = await deferred.promise
      appStore.cachedPublicSettings = settings
      appStore.publicSettingsLoaded = true
      return settings
    })

    const { navigation, next } = runGuard({ requiresPayment: true }, '/purchase')

    await vi.waitFor(() => expect(appStore.fetchPublicSettings).toHaveBeenCalledTimes(1))
    expect(next).not.toHaveBeenCalled()

    deferred.resolve({ payment_enabled: true })
    await navigation
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it.each([
    ['payment', { requiresPayment: true }, '/purchase'],
    ['risk control', { requiresRiskControl: true }, '/admin/risk-control'],
    ['subscription', { requiresSubscription: true }, '/subscriptions'],
  ])('does not treat a failed %s settings load as explicitly disabled', async (_name, meta, path) => {
    authStore.isAdmin = meta.requiresRiskControl === true
    appStore.fetchPublicSettings.mockResolvedValue(null)

    const { navigation, next } = runGuard(meta, path)
    await navigation

    expect(appStore.publicSettingsLoaded).toBe(false)
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it.each([
    ['payment', { requiresPayment: true }, { payment_enabled: false }, '/dashboard'],
    [
      'risk control',
      { requiresRiskControl: true },
      { risk_control_enabled: false },
      '/admin/settings',
    ],
    ['subscription', { requiresSubscription: true }, { subscription_enabled: false }, '/dashboard'],
  ])('redirects when loaded settings explicitly disable %s', async (_name, meta, settings, target) => {
    authStore.isAdmin = meta.requiresRiskControl === true
    appStore.cachedPublicSettings = settings
    appStore.publicSettingsLoaded = true

    const { navigation, next } = runGuard(meta, '/feature')
    await navigation

    expect(appStore.fetchPublicSettings).not.toHaveBeenCalled()
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith(target)
  })
})

describe('用户消息页面访问保护', () => {
  beforeEach(() => {
    authStore.isAuthenticated = true
    authStore.isAdmin = false
    authStore.isSimpleMode = false
    authStore.refreshUser.mockReset().mockResolvedValue({ role: 'user' })
    viewerGroups.mockReset().mockResolvedValue([])
  })

  function visitMessages() {
    const route = routerHarness.routes.find((item) => item.path === '/admin/user-messages')
    expect(route?.meta?.requiresUserMessageAccess).toBe(true)
    return runGuard(route!.meta!, route!.path)
  }

  it('未登录时跳转登录页，不查询授权', async () => {
    authStore.isAuthenticated = false
    const { navigation, next } = visitMessages()
    await navigation
    expect(next).toHaveBeenCalledWith({ path: '/login', query: { redirect: '/admin/user-messages' } })
    expect(authStore.refreshUser).not.toHaveBeenCalled()
    expect(viewerGroups).not.toHaveBeenCalled()
  })

  it('确认当前管理员身份后允许进入', async () => {
    authStore.isAdmin = true
    authStore.refreshUser.mockResolvedValue({ role: 'admin' })
    const { navigation, next } = visitMessages()
    await navigation
    expect(authStore.refreshUser).toHaveBeenCalledOnce()
    expect(viewerGroups).not.toHaveBeenCalled()
    expect(next).toHaveBeenCalledWith()
  })

  it('等待服务器授权检查完成才允许普通用户进入', async () => {
    const deferred = createDeferred<Array<{ id: number; name: string }>>()
    viewerGroups.mockReturnValue(deferred.promise)
    const { navigation, next } = visitMessages()
    await vi.waitFor(() => expect(viewerGroups).toHaveBeenCalledOnce())
    expect(next).not.toHaveBeenCalled()
    deferred.resolve([{ id: 7, name: '授权分组' }])
    await navigation
    expect(next).toHaveBeenCalledWith()
  })

  it('未授权用户直接输入地址也无法进入', async () => {
    const { navigation, next } = visitMessages()
    await navigation
    expect(next).toHaveBeenCalledWith('/dashboard')
  })

  it('伪造或过期的管理员缓存不能绕过服务器授权', async () => {
    authStore.isAdmin = true
    const { navigation, next } = visitMessages()
    await navigation
    expect(viewerGroups).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith('/dashboard')
  })

  it('重新进入时再次校验，撤销授权后拒绝进入', async () => {
    viewerGroups.mockResolvedValueOnce([{ id: 7, name: '授权分组' }])
    const first = visitMessages()
    await first.navigation
    expect(first.next).toHaveBeenCalledWith()
    const second = visitMessages()
    await second.navigation
    expect(viewerGroups).toHaveBeenCalledTimes(2)
    expect(second.next).toHaveBeenCalledWith('/dashboard')
  })

  it.each(['身份', '分组'])('%s校验失败时拒绝进入', async (kind) => {
    if (kind === '身份') authStore.refreshUser.mockRejectedValue(new Error('网络异常'))
    else viewerGroups.mockRejectedValue({ status: 403 })
    const { navigation, next } = visitMessages()
    await navigation
    expect(next).toHaveBeenCalledWith('/dashboard')
  })

  it('会话失效时返回登录页', async () => {
    authStore.refreshUser.mockImplementation(async () => {
      authStore.isAuthenticated = false
      throw { status: 401 }
    })
    const { navigation, next } = visitMessages()
    await navigation
    expect(next).toHaveBeenCalledWith({ path: '/login', query: { redirect: '/admin/user-messages' } })
  })
})

describe('subscription route guard (opt-out flag)', () => {
  beforeEach(() => {
    authStore.isAuthenticated = true
    authStore.isAdmin = false
    authStore.isSimpleMode = false
    appStore.publicSettingsLoaded = true
    appStore.fetchPublicSettings.mockReset()
  })

  it.each([
    ['missing key', {}],
    ['explicit true', { subscription_enabled: true }],
  ])('lets /subscriptions through when the flag is %s', async (_name, settings) => {
    appStore.cachedPublicSettings = settings

    const { navigation, next } = runGuard({ requiresSubscription: true }, '/subscriptions')
    await navigation

    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it('sends admins to the admin dashboard when subscriptions are disabled', async () => {
    authStore.isAdmin = true
    appStore.cachedPublicSettings = { subscription_enabled: false }

    const { navigation, next } = runGuard({ requiresSubscription: true }, '/subscriptions')
    await navigation

    expect(next).toHaveBeenCalledWith('/admin/dashboard')
  })
})
