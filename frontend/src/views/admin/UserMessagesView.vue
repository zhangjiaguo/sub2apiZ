<template>
  <AppLayout>
    <TablePageLayout>
      <!-- Tab 切换：用户消息 / 用户用量 -->
      <template #actions>
        <div class="inline-flex rounded-lg border border-gray-200 bg-gray-50 p-1 dark:border-gray-700 dark:bg-gray-800">
          <button
            v-for="tab in tabs"
            :key="tab.key"
            type="button"
            class="rounded-md px-4 py-1.5 text-sm font-medium transition-colors"
            :class="activeTab === tab.key
              ? 'bg-white text-primary-600 shadow-sm dark:bg-gray-900 dark:text-primary-400'
              : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'"
            @click="switchTab(tab.key)"
          >
            {{ tab.label }}
          </button>
        </div>
      </template>

      <!-- Filters -->
      <template #filters>
        <div v-if="activeTab === 'messages'" class="card p-4 sm:p-6">
          <div class="flex flex-1 flex-wrap items-end gap-4">
            <div class="w-full sm:w-auto sm:min-w-[220px]">
              <label class="input-label">{{ t('admin.userMessages.filters.q') }}</label>
              <div class="relative">
                <Icon
                  name="search"
                  size="md"
                  class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400"
                />
                <input
                  v-model.trim="filters.q"
                  type="text"
                  class="input pl-10"
                  :placeholder="t('admin.userMessages.filters.qPlaceholder')"
                  @keyup.enter="search"
                />
              </div>
            </div>

            <div class="w-full sm:w-auto sm:min-w-[120px]">
              <label class="input-label">{{ t('admin.userMessages.filters.userId') }}</label>
              <input
                v-model.trim="filters.user_id"
                type="text"
                inputmode="numeric"
                class="input"
                @keyup.enter="search"
              />
            </div>

            <div class="w-full sm:w-auto sm:min-w-[160px]">
              <label class="input-label">{{ t('admin.userMessages.filters.model') }}</label>
              <input
                v-model.trim="filters.model"
                type="text"
                class="input"
                placeholder="gpt-6-sol"
                @keyup.enter="search"
              />
            </div>

            <div class="w-full sm:w-auto sm:min-w-[200px]">
              <label class="input-label">{{ t('admin.userMessages.filters.groupId') }}</label>
              <Select v-model="filters.group_id" :options="groupOptions" @change="search" />
            </div>

            <div class="flex gap-2">
              <button type="button" class="btn btn-primary" :disabled="loading" @click="search">
                {{ t('admin.userMessages.filters.search') }}
              </button>
              <button type="button" class="btn btn-secondary" @click="reset">
                {{ t('admin.userMessages.filters.reset') }}
              </button>
              <button v-if="isAdmin" type="button" class="btn btn-secondary" @click="openViewers">
                {{ t('admin.userMessages.viewers.button') }}
              </button>
            </div>
          </div>
        </div>
      </template>

      <!-- Table -->
      <template #table>
        <!-- 用量排行（tab=usage） -->
        <div v-if="activeTab === 'usage'" class="card p-4 sm:p-6">
          <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
            <h3 class="text-sm font-semibold text-gray-800 dark:text-gray-100">
              {{ activeRankGroupName }}
            </h3>
            <div class="flex items-center gap-2">
              <div class="w-48">
                <Select v-model="filters.group_id" :options="groupOptions" @change="fetchRanking" />
              </div>
              <span class="text-xs text-gray-400">{{ t('admin.userMessages.ranking.days') }}</span>
              <div class="w-24">
                <Select v-model="rankDays" :options="daysOptions" @change="fetchRanking" />
              </div>
            </div>
          </div>
          <div v-if="!rankAvailable" class="py-10 text-center text-sm text-gray-400">
            {{ t('admin.userMessages.ranking.pickFirst') }}
          </div>
          <div v-else class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-gray-200 text-left text-xs text-gray-400 dark:border-gray-700">
                  <th class="py-2 pr-3">#</th>
                  <th class="py-2 pr-3">{{ t('admin.userMessages.ranking.user') }}</th>
                  <th class="py-2 pr-3 text-right">{{ t('admin.userMessages.ranking.requests') }}</th>
                  <th class="py-2 pr-3 text-right">{{ t('admin.userMessages.ranking.input') }}</th>
                  <th class="py-2 pr-3 text-right">{{ t('admin.userMessages.ranking.output') }}</th>
                  <th class="py-2 pr-3 text-right">{{ t('admin.userMessages.ranking.cache') }}</th>
                  <th class="py-2 text-right">{{ t('admin.userMessages.ranking.cost') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="(row, i) in ranking"
                  :key="row.user_id"
                  class="cursor-pointer border-b border-gray-100 transition-colors last:border-0 hover:bg-primary-50/50 dark:border-gray-800 dark:hover:bg-primary-900/10"
                  @click="drillUser(row.user_id)"
                >
                  <td class="py-2 pr-3 font-mono text-xs text-gray-400">{{ i + 1 }}</td>
                  <td class="py-2 pr-3">
                    <div class="font-medium text-gray-800 dark:text-gray-200">{{ row.username || row.user_email || `#${row.user_id}` }}</div>
                    <div class="font-mono text-xs text-gray-400">{{ row.user_email }}</div>
                  </td>
                  <td class="py-2 pr-3 text-right font-mono text-xs">{{ row.requests.toLocaleString() }}</td>
                  <td class="py-2 pr-3 text-right font-mono text-xs">{{ fmtTokens(row.input_tokens) }}</td>
                  <td class="py-2 pr-3 text-right font-mono text-xs">{{ fmtTokens(row.output_tokens) }}</td>
                  <td class="py-2 pr-3 text-right font-mono text-xs">{{ fmtTokens(row.cache_read_tokens) }}</td>
                  <td class="py-2 text-right font-mono text-xs">${{ row.total_cost.toFixed(2) }}</td>
                </tr>
                <tr v-if="ranking.length === 0">
                  <td colspan="7" class="py-6 text-center text-xs text-gray-400">{{ t('admin.userMessages.ranking.empty') }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <div v-if="activeTab === 'messages'" class="card p-4 sm:p-6">
          <DataTable :columns="columns" :data="logs" :loading="loading" row-key="id">
            <template #cell-created_at="{ value }">
              <span class="whitespace-nowrap font-mono text-xs text-gray-600 dark:text-gray-300">
                {{ formatTime(value) }}
              </span>
            </template>

            <template #cell-user="{ row }">
              <div class="min-w-0 max-w-[200px]">
                <div class="truncate text-sm text-gray-800 dark:text-gray-200">
                  {{ row.username || row.user_email || `#${row.user_id || '—'}` }}
                </div>
                <div class="mt-0.5 truncate font-mono text-xs text-gray-400" :title="row.user_email">
                  {{ row.user_email || '—' }}
                </div>
              </div>
            </template>

            <template #cell-username="{ value }">
              <span class="block max-w-[200px] truncate text-sm text-gray-700 dark:text-gray-300" :title="value">
                {{ value || '-' }}
              </span>
            </template>

            <template #cell-model="{ row }">
              <div class="min-w-0 max-w-[160px]">
                <div class="truncate font-mono text-sm text-gray-800 dark:text-gray-200" :title="row.model">
                  {{ row.model || '—' }}
                </div>
                <div class="mt-0.5 truncate font-mono text-xs text-gray-400">
                  {{ row.group_name || '—' }}{{ row.stream ? ' · SSE' : '' }}
                </div>
              </div>
            </template>

            <template #cell-endpoint="{ value, row }">
              <span
                class="whitespace-nowrap font-mono text-xs text-gray-500 dark:text-gray-400"
                :title="`${row.protocol} · ${row.stage}`"
              >
                {{ shortEndpoint(value) }}{{ row.stage === 'http' ? '' : `(${row.stage})` }}
              </span>
            </template>

            <template #cell-content="{ row }">
              <div class="max-w-[380px]">
                <div class="truncate text-sm text-gray-700 dark:text-gray-300" :title="row.content">
                  {{ row.content ? row.content.slice(0, 160) : '—' }}
                </div>
              </div>
            </template>

            <template #cell-content_length="{ row }">
              <span class="whitespace-nowrap font-mono text-xs text-gray-500 dark:text-gray-400">
                {{ formatLength(row.content_length) }}{{ row.truncated ? '+' : '' }}
              </span>
            </template>

            <template #cell-actions="{ row }">
              <button
                type="button"
                class="inline-flex items-center gap-1 font-medium text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
                @click="openDetail(row.id)"
              >
                <Icon name="eye" size="sm" />
                {{ t('admin.userMessages.columns.detail') }}
              </button>
            </template>
          </DataTable>

          <div v-if="!loading && logs.length === 0" class="py-12 text-center text-sm text-gray-400">
            {{ t('admin.userMessages.empty') }}
          </div>

        </div>
      </template>

      <!-- Pagination（专用插槽，消息 tab） -->
      <template #pagination>
        <Pagination
          v-if="activeTab === 'messages' && total > 0"
          :total="total"
          :page="page"
          :page-size="pageSize"
          @update:page="onPageChange"
          @update:pageSize="onPageSizeChange"
        />
      </template>
    </TablePageLayout>

    <!-- 全文弹框：左右两栏（左=请求信息，右=正文） -->
    <BaseDialog :show="detailVisible" :title="t('admin.userMessages.detail.title')" width="extra-wide" @close="detailVisible = false">
      <div v-if="detail" class="flex h-[68vh] gap-0 overflow-hidden rounded-lg border border-gray-200 dark:border-gray-700">
        <!-- 左栏：请求信息 -->
        <div class="w-64 shrink-0 overflow-y-auto border-r border-gray-200 bg-gray-50 p-4 dark:border-gray-700 dark:bg-gray-800/60">
          <div class="space-y-4 text-sm">
            <div v-for="item in detailMeta" :key="item.label">
              <div class="text-xs text-gray-400">{{ item.label }}</div>
              <div class="mt-0.5 break-words font-medium text-gray-800 dark:text-gray-200">{{ item.value || '—' }}</div>
            </div>
          </div>
          <div
            v-if="detail.truncated"
            class="mt-4 rounded-md bg-amber-50 px-2 py-1.5 text-xs text-amber-700 dark:bg-amber-900/30 dark:text-amber-300"
          >
            {{ t('admin.userMessages.detail.truncated', { n: detail.content_length.toLocaleString() }) }}
          </div>
          <button type="button" class="btn btn-secondary mt-4 w-full !py-1.5 !text-xs" @click="copyContent">
            {{ copied ? t('admin.userMessages.detail.copied') : t('admin.userMessages.detail.copy') }}
          </button>
        </div>
        <!-- 右栏：全文 -->
        <pre class="flex-1 overflow-auto whitespace-pre-wrap break-words p-4 font-mono text-xs leading-relaxed text-gray-800 dark:text-gray-200">{{ detail.content || '—' }}</pre>
      </div>
    </BaseDialog>

    <!-- 授权管理（仅管理员） -->
    <BaseDialog :show="viewersVisible" :title="t('admin.userMessages.viewers.title')" width="wide" @close="viewersVisible = false">
      <div class="space-y-4">
        <div class="flex flex-wrap items-end gap-3">
          <div class="w-40">
            <label class="input-label">{{ t('admin.userMessages.viewers.userId') }}</label>
            <input v-model.trim="grantUserId" type="text" inputmode="numeric" class="input" placeholder="3" />
          </div>
          <div class="w-56">
            <label class="input-label">{{ t('admin.userMessages.filters.groupId') }}</label>
            <Select v-model="grantGroupId" :options="grantGroupOptions" />
          </div>
          <button type="button" class="btn btn-primary" :disabled="granting || !grantUserId || !grantGroupId" @click="grant">
            {{ t('admin.userMessages.viewers.grant') }}
          </button>
        </div>

        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-gray-200 text-left text-xs text-gray-400 dark:border-gray-700">
              <th class="py-2 pr-3">{{ t('admin.userMessages.viewers.user') }}</th>
              <th class="py-2 pr-3">{{ t('admin.userMessages.filters.groupId') }}</th>
              <th class="py-2 pr-3">{{ t('admin.userMessages.viewers.grantedAt') }}</th>
              <th class="py-2"></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="v in viewers" :key="v.id" class="border-b border-gray-100 last:border-0 dark:border-gray-800">
              <td class="py-2 pr-3">
                <div class="font-medium text-gray-800 dark:text-gray-200">#{{ v.user_id }} {{ v.username || v.user_email }}</div>
              </td>
              <td class="py-2 pr-3 text-gray-600 dark:text-gray-300">{{ v.group_name }} (#{{ v.group_id }})</td>
              <td class="py-2 pr-3 font-mono text-xs text-gray-400">{{ formatTime(v.created_at) }}</td>
              <td class="py-2 text-right">
                <button
                  type="button"
                  class="text-xs font-medium text-red-500 hover:text-red-600"
                  @click="revoke(v.id)"
                >
                  {{ t('admin.userMessages.viewers.revoke') }}
                </button>
              </td>
            </tr>
            <tr v-if="viewers.length === 0">
              <td colspan="4" class="py-6 text-center text-xs text-gray-400">{{ t('admin.userMessages.viewers.empty') }}</td>
            </tr>
          </tbody>
        </table>
        <p class="text-xs text-gray-400">{{ t('admin.userMessages.viewers.hint') }}</p>
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  getUserMessageLog,
  grantUserMessageViewer,
  groupUsageRanking,
  listUserMessageLogs,
  listUserMessageViewers,
  myViewerGroups,
  revokeUserMessageViewer,
  type GroupUsageRankingRow,
  type UserMessageLog,
  type UserMessageViewer,
} from '@/api/admin'
import { adminAPI } from '@/api/admin'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import type { Column } from '@/components/common/types'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAuthStore } from '@/stores'

const { t } = useI18n()
const route = useRoute()
const authStore = useAuthStore()
const isAdmin = computed(() => authStore.isAdmin)

const loading = ref(false)
const logs = ref<UserMessageLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)

const filters = reactive({
  q: '',
  user_id: '',
  model: '',
  group_id: '',
})

const groups = ref<Array<{ id: number; name: string }>>([])
const groupOptions = computed(() => [
  { value: '', label: isAdmin.value ? t('admin.userMessages.filters.allGroups') : t('admin.userMessages.filters.pickGroup') },
  ...groups.value.map((g) => ({ value: String(g.id), label: `${g.name} (#${g.id})` })),
])
const grantGroupOptions = computed(() => groups.value.map((g) => ({ value: String(g.id), label: `${g.name} (#${g.id})` })))

const activeTab = ref<'messages' | 'usage'>('messages')
const tabs = computed(() => [
  { key: 'messages' as const, label: t('admin.userMessages.tabs.messages') },
  { key: 'usage' as const, label: t('admin.userMessages.tabs.usage') },
])

const ranking = ref<GroupUsageRankingRow[]>([])
const rankDays = ref('7')
const daysOptions = [
  { value: '1', label: '1' },
  { value: '7', label: '7' },
  { value: '14', label: '14' },
  { value: '30', label: '30' },
]

const detailVisible = ref(false)
const detail = ref<UserMessageLog | null>(null)
const copied = ref(false)

const viewersVisible = ref(false)
const viewers = ref<UserMessageViewer[]>([])
const grantUserId = ref('')
const grantGroupId = ref('')
const granting = ref(false)

const columns: Column[] = [
  { key: 'created_at', label: t('admin.userMessages.columns.time') },
  { key: 'user', label: t('admin.userMessages.columns.user') },
  { key: 'username', label: t('admin.users.columns.username') },
  { key: 'model', label: t('admin.userMessages.columns.model') },
  { key: 'endpoint', label: t('admin.userMessages.columns.endpoint') },
  { key: 'content', label: t('admin.userMessages.columns.content') },
  { key: 'content_length', label: t('admin.userMessages.columns.size') },
  { key: 'actions', label: '' },
]

const activeRankGroup = computed(() => {
  if (filters.group_id && /^\d+$/.test(filters.group_id)) return Number(filters.group_id)
  if (!isAdmin.value && groups.value.length === 1) return groups.value[0].id
  return null
})
const rankAvailable = computed(() => activeRankGroup.value !== null)
const activeRankGroupName = computed(() => {
  const g = groups.value.find((x) => String(x.id) === String(activeRankGroup.value))
  return g ? `${g.name} (#${g.id})` : `#${activeRankGroup.value}`
})

const detailMeta = computed(() => {
  if (!detail.value) return []
  const d = detail.value
  return [
    { label: t('admin.userMessages.columns.user'), value: d.username || `#${d.user_id}` },
    { label: '邮箱', value: d.user_email },
    { label: 'Key', value: d.api_key_name },
    { label: t('admin.userMessages.filters.groupId'), value: `${d.group_name} (#${d.group_id})` },
    { label: t('admin.userMessages.columns.model'), value: d.model },
    { label: t('admin.userMessages.columns.endpoint'), value: d.endpoint },
    { label: '协议 / 阶段', value: `${d.protocol} · ${d.stage}` },
    { label: t('admin.userMessages.columns.time'), value: formatTime(d.created_at) },
    { label: t('admin.userMessages.columns.size'), value: `${d.content_length.toLocaleString()}${d.truncated ? '+' : ''}` },
    { label: t('admin.userMessages.detail.requestId'), value: d.request_id },
  ]
})

async function fetchLogs() {
  loading.value = true
  try {
    const params: Record<string, unknown> = {
      page: page.value,
      page_size: pageSize.value,
    }
    if (filters.q) params.q = filters.q
    if (filters.user_id && /^\d+$/.test(filters.user_id)) params.user_id = Number(filters.user_id)
    if (filters.model) params.model = filters.model
    if (filters.group_id && /^\d+$/.test(filters.group_id)) params.group_id = Number(filters.group_id)
    const res = await listUserMessageLogs(params, !isAdmin.value)
    logs.value = res.items ?? []
    total.value = res.total ?? 0
  } catch (err: unknown) {
    const status = (err as { status?: number })?.status
    if (status === 403) {
      logs.value = []
      total.value = 0
      console.warn(t('admin.userMessages.noPermission'))
    } else {
      console.error(t('admin.userMessages.loadFailed'), err)
      logs.value = []
      total.value = 0
    }
  } finally {
    loading.value = false
  }
}

function search() {
  page.value = 1
  fetchLogs()
}

function switchTab(tab: 'messages' | 'usage') {
  activeTab.value = tab
  if (tab === 'usage') fetchRanking()
}

// 从排行钻取：跳到消息 tab 并锁定该用户+当前分组
function drillUser(userId: number) {
  filters.user_id = String(userId)
  page.value = 1
  activeTab.value = 'messages'
  fetchLogs()
}

function reset() {
  filters.q = ''
  filters.user_id = ''
  filters.model = ''
  filters.group_id = ''
  search()
}

function onPageChange(next: number) {
  page.value = next
  fetchLogs()
}

function onPageSizeChange(next: number) {
  pageSize.value = next
  page.value = 1
  fetchLogs()
}

async function fetchRanking() {
  const gid = activeRankGroup.value
  if (!gid) {
    ranking.value = []
    return
  }
  try {
    ranking.value = await groupUsageRanking(gid, Number(rankDays.value) || 7, !isAdmin.value)
  } catch {
    ranking.value = []
  }
}

async function openDetail(id: number) {
  detailVisible.value = true
  detail.value = null
  copied.value = false
  try {
    detail.value = await getUserMessageLog(id, !isAdmin.value)
  } catch (err) {
    console.error(t('admin.userMessages.detailFailed'), err)
    detailVisible.value = false
  }
}

async function copyContent() {
  if (!detail.value?.content) return
  try {
    await navigator.clipboard.writeText(detail.value.content)
    copied.value = true
    setTimeout(() => (copied.value = false), 2000)
  } catch {
    /* 剪贴板不可用时静默 */
  }
}

async function openViewers() {
  viewersVisible.value = true
  grantUserId.value = ''
  grantGroupId.value = groups.value.length ? String(groups.value[0].id) : ''
  await fetchViewers()
}

async function fetchViewers() {
  try {
    viewers.value = await listUserMessageViewers()
  } catch (err) {
    console.error(err)
    viewers.value = []
  }
}

async function grant() {
  if (!/^\d+$/.test(grantUserId.value) || !grantGroupId.value) return
  granting.value = true
  try {
    await grantUserMessageViewer(Number(grantUserId.value), Number(grantGroupId.value))
    grantUserId.value = ''
    await fetchViewers()
  } catch (err) {
    console.error(err)
  } finally {
    granting.value = false
  }
}

async function revoke(id: number) {
  try {
    await revokeUserMessageViewer(id)
    await fetchViewers()
  } catch (err) {
    console.error(err)
  }
}

function formatTime(value: string): string {
  if (!value) return '—'
  const d = new Date(value)
  if (isNaN(d.getTime())) return value
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

function formatLength(n: number): string {
  if (!n) return '—'
  if (n >= 10000) return `${(n / 1000).toFixed(1)}K`
  return String(n)
}

function fmtTokens(n: number): string {
  if (!n) return '0'
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`
  return String(n)
}

function shortEndpoint(value: string): string {
  if (!value) return '—'
  return value.replace('/v1/', '').replace('/backend-api/codex/', 'codex:')
}

onMounted(() => {
  // 支持从用户详情页跳转预设筛选（?user_id=）
  const presetUser = String(route.query.user_id ?? '')
  if (presetUser && /^\d+$/.test(presetUser)) {
    filters.user_id = presetUser
  }
  fetchLogs()
  if (isAdmin.value) {
    adminAPI.groups
      .getAll()
      .then((all) => {
        groups.value = (all ?? []).map((g) => ({ id: g.id, name: g.name }))
      })
      .catch(() => {})
  } else {
    myViewerGroups()
      .then((all: Array<{ id: number; name: string }>) => {
        groups.value = (all ?? []).map((g) => ({ id: g.id, name: g.name }))
        // 查看者默认锁定到唯一授权分组（多组时保持「选择分组」手动选）。
        if (groups.value.length === 1) filters.group_id = String(groups.value[0].id)
        search()
      })
      .catch(() => {})
  }
  fetchRanking()
})
</script>
