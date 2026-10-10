<template>
  <AppLayout>
    <TablePageLayout>
      <!-- Filters -->
      <template #filters>
        <div class="card p-4 sm:p-6">
          <div class="flex flex-1 flex-wrap items-end gap-4">
            <div class="w-full sm:w-auto sm:min-w-[240px]">
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

            <div class="w-full sm:w-auto sm:min-w-[180px]">
              <label class="input-label">{{ t('admin.userMessages.filters.model') }}</label>
              <input
                v-model.trim="filters.model"
                type="text"
                class="input"
                placeholder="gpt-6-sol"
                @keyup.enter="search"
              />
            </div>

            <div class="w-full sm:w-auto sm:min-w-[120px]">
              <label class="input-label">{{ t('admin.userMessages.filters.groupId') }}</label>
              <input
                v-model.trim="filters.group_id"
                type="text"
                inputmode="numeric"
                class="input"
                @keyup.enter="search"
              />
            </div>

            <div class="flex gap-2">
              <button type="button" class="btn btn-primary" :disabled="loading" @click="search">
                {{ t('admin.userMessages.filters.search') }}
              </button>
              <button type="button" class="btn btn-secondary" @click="reset">
                {{ t('admin.userMessages.filters.reset') }}
              </button>
            </div>
          </div>
        </div>
      </template>

      <!-- Table -->
      <template #table>
        <div class="card p-4 sm:p-6">
          <DataTable :columns="columns" :data="logs" :loading="loading" row-key="id">
            <template #cell-created_at="{ value }">
              <span class="whitespace-nowrap font-mono text-xs text-gray-600 dark:text-gray-300">
                {{ formatTime(value) }}
              </span>
            </template>

            <template #cell-user="{ row }">
              <div class="min-w-0 max-w-[220px]">
                <div class="truncate text-sm text-gray-800 dark:text-gray-200">
                  {{ row.username || row.user_email || `#${row.user_id || '—'}` }}
                </div>
                <div class="mt-0.5 truncate font-mono text-xs text-gray-400" :title="row.user_email">
                  {{ row.user_email || '—' }}
                </div>
              </div>
            </template>

            <template #cell-model="{ row }">
              <div class="min-w-0 max-w-[180px]">
                <div class="truncate font-mono text-sm text-gray-800 dark:text-gray-200" :title="row.model">
                  {{ row.model || '—' }}
                </div>
                <div class="mt-0.5 truncate font-mono text-xs text-gray-400">
                  {{ row.group_name || '—' }}{{ row.stream ? ' · SSE' : '' }}
                </div>
              </div>
            </template>

            <template #cell-endpoint="{ value, row }">
              <span class="whitespace-nowrap font-mono text-xs text-gray-500 dark:text-gray-400" :title="`${row.protocol} · ${row.stage}`">
                {{ shortEndpoint(value) }}{{ row.stage === 'http' ? '' : `(${row.stage})` }}
              </span>
            </template>

            <template #cell-content="{ row }">
              <div class="max-w-[420px]">
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

          <Pagination
            v-if="total > 0"
            v-model:page="page"
            :total="total"
            :page-size="pageSize"
            @update:page="fetchLogs"
          />
        </div>
      </template>
    </TablePageLayout>

    <!-- Full-text dialog -->
    <BaseDialog :show="detailVisible" :title="t('admin.userMessages.detail.title')" wide @close="detailVisible = false">
      <div v-if="detail" class="space-y-4">
        <div class="grid grid-cols-2 gap-x-6 gap-y-2 rounded-lg bg-gray-50 p-4 text-sm dark:bg-gray-800/60 sm:grid-cols-3">
          <div><span class="text-gray-400">用户</span><div class="font-medium">{{ detail.username || detail.user_email || `#${detail.user_id}` }}</div></div>
          <div><span class="text-gray-400">邮箱</span><div class="truncate font-mono text-xs">{{ detail.user_email || '—' }}</div></div>
          <div><span class="text-gray-400">Key</span><div class="truncate">{{ detail.api_key_name || '—' }}</div></div>
          <div><span class="text-gray-400">分组</span><div>{{ detail.group_name || '—' }}</div></div>
          <div><span class="text-gray-400">模型</span><div class="font-mono">{{ detail.model || '—' }}</div></div>
          <div><span class="text-gray-400">时间</span><div class="font-mono text-xs">{{ formatTime(detail.created_at) }}</div></div>
          <div><span class="text-gray-400">入口</span><div class="font-mono text-xs">{{ detail.endpoint }}</div></div>
          <div><span class="text-gray-400">长度</span><div class="font-mono">{{ formatLength(detail.content_length) }}{{ detail.truncated ? '+' : '' }}</div></div>
          <div class="col-span-2 sm:col-span-1">
            <span class="text-gray-400">{{ t('admin.userMessages.detail.requestId') }}</span>
            <div class="truncate font-mono text-xs" :title="detail.request_id">{{ detail.request_id || '—' }}</div>
          </div>
        </div>

        <div v-if="detail.truncated" class="rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-900/30 dark:text-amber-300">
          {{ t('admin.userMessages.detail.truncated', { n: detail.content_length.toLocaleString() }) }}
        </div>

        <div class="flex items-center justify-between">
          <span class="text-sm font-medium text-gray-500">{{ t('admin.userMessages.detail.content') }}</span>
          <button type="button" class="btn btn-secondary !py-1 !text-xs" @click="copyContent">
            {{ copied ? t('admin.userMessages.detail.copied') : t('admin.userMessages.detail.copy') }}
          </button>
        </div>
        <pre class="max-h-[55vh] overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-900 p-4 font-mono text-xs leading-relaxed text-gray-100">{{ detail.content || '—' }}</pre>
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getUserMessageLog, listUserMessageLogs, type UserMessageLog } from '@/api/admin'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import type { Column } from '@/components/common/types'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()

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

const detailVisible = ref(false)
const detail = ref<UserMessageLog | null>(null)
const copied = ref(false)

const columns: Column[] = [
  { key: 'created_at', label: t('admin.userMessages.columns.time') },
  { key: 'user', label: t('admin.userMessages.columns.user') },
  { key: 'model', label: t('admin.userMessages.columns.model') },
  { key: 'endpoint', label: t('admin.userMessages.columns.endpoint') },
  { key: 'content', label: t('admin.userMessages.columns.content') },
  { key: 'content_length', label: t('admin.userMessages.columns.size') },
  { key: 'actions', label: '' },
]

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
    const res = await listUserMessageLogs(params)
    logs.value = res.items ?? []
    total.value = res.total ?? 0
  } catch (err) {
    console.error(t('admin.userMessages.loadFailed'), err)
    logs.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function search() {
  page.value = 1
  fetchLogs()
}

function reset() {
  filters.q = ''
  filters.user_id = ''
  filters.model = ''
  filters.group_id = ''
  search()
}

async function openDetail(id: number) {
  detailVisible.value = true
  detail.value = null
  copied.value = false
  try {
    detail.value = await getUserMessageLog(id)
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

function shortEndpoint(value: string): string {
  if (!value) return '—'
  return value.replace('/v1/', '').replace('/backend-api/codex/', 'codex:')
}

onMounted(fetchLogs)
</script>
