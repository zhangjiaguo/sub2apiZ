<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- 头部：返回 + 用户身份 -->
      <div class="card p-6">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="flex items-center gap-4">
            <button
              type="button"
              class="btn btn-ghost btn-sm"
              @click="router.push('/admin/users')"
            >
              <Icon name="arrowLeft" size="sm" />
            </button>
            <div
              class="flex h-14 w-14 items-center justify-center rounded-full bg-primary-100 text-xl font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
            >
              {{ avatarChar }}
            </div>
            <div>
              <div class="flex flex-wrap items-center gap-2">
                <h1 class="text-lg font-semibold text-gray-900 dark:text-white">{{ user?.email }}</h1>
                <span :class="statusBadgeClass">{{ statusLabel }}</span>
                <span v-if="user?.role" class="rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-600 dark:bg-gray-800 dark:text-gray-300">{{ user.role }}</span>
              </div>
              <div class="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-gray-400">
                <span>#{{ userId }}</span>
                <span v-if="user?.username">{{ user.username }}</span>
                <span v-if="userWechat">WeChat: {{ userWechat }}</span>
                <span v-if="user?.created_at">注册 {{ formatDateTime(user.created_at) }}</span>
                <span v-if="user?.last_active_at">{{ t('admin.userDetail.lastActive') }} {{ formatDateTime(user.last_active_at) }}</span>
              </div>
            </div>
          </div>
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary" @click="openBalance">
              {{ t('admin.userDetail.adjustBalance') }}
            </button>
            <button
              v-if="user"
              type="button"
              class="btn"
              :class="user.status === 'active' ? 'btn-danger' : 'btn-primary'"
              @click="toggleStatus"
            >
              {{ user.status === 'active' ? t('admin.userDetail.disable') : t('admin.userDetail.enable') }}
            </button>
            <button type="button" class="btn btn-secondary" @click="goMessages">
              {{ t('admin.userDetail.viewMessages') }}
            </button>
          </div>
        </div>
      </div>

      <!-- 统计卡片 -->
      <div class="grid grid-cols-2 gap-4 lg:grid-cols-5">
        <div class="card p-4">
          <div class="text-xs text-gray-400">{{ t('admin.userDetail.stats.balance') }}</div>
          <div class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">${{ (user?.balance ?? 0).toFixed(2) }}</div>
        </div>
        <div class="card p-4">
          <div class="text-xs text-gray-400">{{ t('admin.userDetail.stats.frozen') }}</div>
          <div class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">${{ (user?.frozen_balance ?? 0).toFixed(2) }}</div>
        </div>
        <div class="card p-4">
          <div class="text-xs text-gray-400">{{ t('admin.userDetail.stats.recharged') }}</div>
          <div class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">${{ totalRecharged.toFixed(2) }}</div>
        </div>
        <div class="card p-4">
          <div class="text-xs text-gray-400">{{ t('admin.userDetail.stats.requests') }}</div>
          <div class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">{{ usage?.total_requests?.toLocaleString() ?? '—' }}</div>
        </div>
        <div class="card p-4">
          <div class="text-xs text-gray-400">{{ t('admin.userDetail.stats.cost') }}</div>
          <div class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">${{ usage?.total_cost?.toFixed(2) ?? '—' }}</div>
        </div>
      </div>

      <!-- Tabs：余额记录 / 用量周期 -->
      <div class="card p-6">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div class="inline-flex rounded-lg border border-gray-200 bg-gray-50 p-1 dark:border-gray-700 dark:bg-gray-800">
            <button
              type="button"
              class="rounded-md px-4 py-1.5 text-sm font-medium transition-colors"
              :class="activeTab === 'history'
                ? 'bg-white text-primary-600 shadow-sm dark:bg-gray-900 dark:text-primary-400'
                : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'"
              @click="activeTab = 'history'"
            >
              {{ t('admin.userDetail.tabs.history') }}
            </button>
            <button
              type="button"
              class="rounded-md px-4 py-1.5 text-sm font-medium transition-colors"
              :class="activeTab === 'usage'
                ? 'bg-white text-primary-600 shadow-sm dark:bg-gray-900 dark:text-primary-400'
                : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'"
              @click="activeTab = 'usage'"
            >
              {{ t('admin.userDetail.tabs.usage') }}
            </button>
          </div>
          <div v-if="activeTab === 'usage'" class="w-36">
            <Select v-model="usagePeriod" :options="periodOptions" @change="fetchUsage" />
          </div>
        </div>

        <!-- 余额记录 -->
        <div v-if="activeTab === 'history'">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-gray-200 text-left text-xs text-gray-400 dark:border-gray-700">
                <th class="py-2 pr-3">{{ t('admin.userDetail.history.time') }}</th>
                <th class="py-2 pr-3">{{ t('admin.userDetail.history.type') }}</th>
                <th class="py-2 pr-3 text-right">{{ t('admin.userDetail.history.value') }}</th>
                <th class="py-2 pr-3">{{ t('admin.userDetail.history.notes') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in history" :key="item.id" class="border-b border-gray-100 last:border-0 dark:border-gray-800">
                <td class="py-2 pr-3 font-mono text-xs">{{ formatDateTime(item.created_at) }}</td>
                <td class="py-2 pr-3 text-gray-600 dark:text-gray-300">{{ item.type }}</td>
                <td class="py-2 pr-3 text-right font-mono" :class="item.value >= 0 ? 'text-green-600 dark:text-green-400' : 'text-red-500'">
                  {{ item.value >= 0 ? '+' : '' }}${{ Number(item.value).toFixed(2) }}
                </td>
                <td class="py-2 pr-3 text-xs text-gray-400">{{ item.notes || '—' }}</td>
              </tr>
              <tr v-if="history.length === 0 && !historyLoading">
                <td colspan="4" class="py-8 text-center text-xs text-gray-400">{{ t('admin.userDetail.history.empty') }}</td>
              </tr>
            </tbody>
          </table>
          <Pagination
            v-if="historyTotal > 0"
            :total="historyTotal"
            :page="historyPage"
            :page-size="historyPageSize"
            @update:page="onHistoryPage"
          />
        </div>

        <!-- 用量周期 -->
        <div v-if="activeTab === 'usage'" class="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div class="rounded-lg bg-gray-50 p-4 dark:bg-gray-800/60">
            <div class="text-xs text-gray-400">{{ t('admin.userDetail.stats.requests') }}</div>
            <div class="mt-1 text-2xl font-semibold">{{ usage?.total_requests?.toLocaleString() ?? '—' }}</div>
          </div>
          <div class="rounded-lg bg-gray-50 p-4 dark:bg-gray-800/60">
            <div class="text-xs text-gray-400">{{ t('admin.userDetail.stats.tokens') }}</div>
            <div class="mt-1 text-2xl font-semibold">{{ fmtTokens(usage?.total_tokens) }}</div>
          </div>
          <div class="rounded-lg bg-gray-50 p-4 dark:bg-gray-800/60">
            <div class="text-xs text-gray-400">{{ t('admin.userDetail.stats.cost') }}</div>
            <div class="mt-1 text-2xl font-semibold">${{ usage?.total_cost?.toFixed(2) ?? '—' }}</div>
          </div>
        </div>
      </div>
    </div>

    <!-- 调整余额 -->
    <BaseDialog :show="balanceVisible" :title="t('admin.userDetail.adjustBalance')" @close="balanceVisible = false">
      <div class="space-y-4">
        <div>
          <label class="input-label">{{ t('admin.userDetail.balance.operation') }}</label>
          <Select v-model="balanceForm.operation" :options="operationOptions" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.userDetail.balance.amount') }}</label>
          <input v-model.trim="balanceForm.amount" type="text" inputmode="decimal" class="input" placeholder="10.00" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.userDetail.balance.notes') }}</label>
          <input v-model.trim="balanceForm.notes" type="text" class="input" :placeholder="t('admin.userDetail.balance.notesPlaceholder')" />
        </div>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" @click="balanceVisible = false">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-primary" :disabled="submitting" @click="submitBalance">
            {{ t('common.confirm') }}
          </button>
        </div>
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  getById,
  getUserBalanceHistory,
  getUserUsageStats,
  update,
  updateBalance,
  type BalanceHistoryItem,
} from '@/api/admin/users'
import type { AdminUser } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const userId = computed(() => Number(route.params.id))
const user = ref<AdminUser | null>(null)
const usage = ref<{ total_requests: number; total_cost: number; total_tokens: number } | null>(null)
const usagePeriod = ref('month')

const activeTab = ref<'history' | 'usage'>('history')
const history = ref<BalanceHistoryItem[]>([])
const historyTotal = ref(0)
const historyPage = ref(1)
const historyPageSize = ref(20)
const historyLoading = ref(false)

const balanceVisible = ref(false)
const submitting = ref(false)
const balanceForm = reactive({ operation: 'add', amount: '', notes: '' })

const periodOptions = [
  { value: 'day', label: t('admin.userDetail.period.day') },
  { value: 'week', label: t('admin.userDetail.period.week') },
  { value: 'month', label: t('admin.userDetail.period.month') },
]
const operationOptions = [
  { value: 'add', label: t('admin.userDetail.balance.add') },
  { value: 'subtract', label: t('admin.userDetail.balance.subtract') },
  { value: 'set', label: t('admin.userDetail.balance.set') },
]

const avatarChar = computed(() => (user.value?.email ?? '?').charAt(0).toUpperCase())
const totalRecharged = computed(() => Number((user.value as { total_recharged?: number } | null)?.total_recharged ?? 0))
const userWechat = computed(() => (user.value as { wechat?: string } | null)?.wechat || '')
const statusLabel = computed(() =>
  user.value?.status === 'active' ? t('admin.userDetail.statusActive') : user.value?.status ?? '—'
)
const statusBadgeClass = computed(() =>
  user.value?.status === 'active'
    ? 'rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-300'
    : 'rounded-full bg-gray-200 px-2 py-0.5 text-xs font-medium text-gray-600 dark:bg-gray-700 dark:text-gray-300'
)

async function fetchUser() {
  try {
    user.value = await getById(userId.value)
  } catch (err) {
    console.error('load user failed', err)
  }
}

async function fetchUsage() {
  try {
    usage.value = await getUserUsageStats(userId.value, usagePeriod.value)
  } catch (err) {
    console.error('load usage failed', err)
    usage.value = null
  }
}

async function fetchHistory() {
  historyLoading.value = true
  try {
    const res = await getUserBalanceHistory(userId.value, historyPage.value, historyPageSize.value)
    history.value = res.items ?? []
    historyTotal.value = res.total ?? 0
  } catch (err) {
    console.error('load history failed', err)
    history.value = []
  } finally {
    historyLoading.value = false
  }
}

function onHistoryPage(next: number) {
  historyPage.value = next
  fetchHistory()
}

function openBalance() {
  balanceForm.operation = 'add'
  balanceForm.amount = ''
  balanceForm.notes = ''
  balanceVisible.value = true
}

async function submitBalance() {
  const amount = Number(balanceForm.amount)
  if (!/^\d+(\.\d{1,4})?$/.test(balanceForm.amount) || isNaN(amount)) {
    alert(t('admin.userDetail.balance.invalid'))
    return
  }
  submitting.value = true
  try {
    user.value = await updateBalance(
      userId.value,
      amount,
      balanceForm.operation as 'set' | 'add' | 'subtract',
      balanceForm.notes
    )
    balanceVisible.value = false
    fetchHistory()
  } catch (err) {
    console.error(err)
    alert(t('admin.userDetail.balance.failed'))
  } finally {
    submitting.value = false
  }
}

async function toggleStatus() {
  if (!user.value) return
  const next = user.value.status === 'active' ? 'disabled' : 'active'
  try {
    user.value = await update(userId.value, { status: next })
  } catch (err) {
    console.error(err)
  }
}

function goMessages() {
  router.push({ path: '/admin/user-messages', query: { user_id: String(userId.value) } })
}

function formatDateTime(value?: string | null): string {
  if (!value) return '—'
  const d = new Date(value)
  if (isNaN(d.getTime())) return value
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

function fmtTokens(n?: number): string {
  if (n == null) return '—'
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`
  return String(n)
}

onMounted(() => {
  fetchUser()
  fetchHistory()
  fetchUsage()
})
</script>
