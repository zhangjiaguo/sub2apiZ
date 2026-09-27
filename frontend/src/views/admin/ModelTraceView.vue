<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- 页头 -->
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.modelTrace.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.description') }}</p>
        </div>
        <span
          class="inline-flex items-center rounded-full px-3 py-1 text-xs font-medium"
          :class="status.bank
            ? 'bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-300'
            : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400'"
        >
          {{ status.bank
            ? `${t('admin.modelTrace.builtAt')} ${formatDateTime(status.bank.built_at)}`
            : t('admin.modelTrace.bankEmpty') }}
        </span>
      </div>

      <!-- 设置 -->
      <section class="rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <div class="border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.modelTrace.settings') }}</h2>
        </div>
        <div class="space-y-5 px-5 py-4">
          <!-- 模型多选 -->
          <div>
            <label class="input-label">{{ t('admin.modelTrace.models') }}</label>
            <p class="mb-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.modelsHelp') }}</p>
            <div class="flex flex-wrap gap-2">
              <button
                v-for="m in modelOptions"
                :key="m"
                type="button"
                class="rounded-md border px-3 py-1.5 font-mono text-xs transition-colors"
                :class="selectedModels.has(m)
                  ? 'border-primary-300 bg-primary-50 text-primary-700 dark:border-primary-700 dark:bg-primary-900/20 dark:text-primary-300'
                  : 'border-gray-100 text-gray-500 hover:bg-gray-50 dark:border-dark-700 dark:text-gray-400 dark:hover:bg-dark-700/50'"
                @click="toggleModel(m)"
              >{{ m }}</button>
            </div>
          </div>

          <!-- 数值参数 -->
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-5">
            <div>
              <label class="input-label">{{ t('admin.modelTrace.bankRepeats') }}</label>
              <input v-model.number="form.bank_repeats" type="number" min="8" max="40" class="input" />
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.bankRepeatsHelp') }}</p>
            </div>
            <div>
              <label class="input-label">{{ t('admin.modelTrace.detectRepeats') }}</label>
              <input v-model.number="form.detect_repeats" type="number" min="1" max="20" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.modelTrace.concurrency') }}</label>
              <input v-model.number="form.concurrency" type="number" min="1" max="8" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.modelTrace.probeTimeout') }}</label>
              <input v-model.number="form.probe_timeout_secs" type="number" min="30" max="600" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.modelTrace.requestGap') }}</label>
              <input v-model.number="form.request_gap_ms" type="number" min="0" max="60000" class="input" />
            </div>
          </div>

          <div class="flex justify-end">
            <button type="button" class="btn btn-primary" :disabled="saving" @click="saveSettings">
              {{ saving ? t('admin.modelTrace.saving') : t('admin.modelTrace.save') }}
            </button>
          </div>
        </div>
      </section>

      <!-- 账号选择 -->
      <section class="rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <div>
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.modelTrace.accounts') }}</h2>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.accountsHelp') }}</p>
          </div>
          <div class="flex items-center gap-3">
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.selectedCount', { count: selectedAccountIds.size }) }}</span>
            <div class="w-56">
              <Select v-model="groupFilter" :options="groupOptions" :placeholder="t('admin.modelTrace.selectGroup')" />
            </div>
          </div>
        </div>
        <div class="px-5 py-4">
          <div v-if="accountsLoading" class="flex items-center justify-center py-8">
            <div class="h-6 w-6 animate-spin rounded-full border-b-2 border-primary-600"></div>
          </div>
          <div v-else-if="filteredAccounts.length === 0" class="py-8 text-center text-sm text-gray-400">—</div>
          <div v-else class="grid grid-cols-1 gap-2 md:grid-cols-2 xl:grid-cols-3">
            <label
              v-for="acc in filteredAccounts"
              :key="acc.id"
              class="flex cursor-pointer items-center gap-3 rounded-md border px-3 py-2 transition-colors"
              :class="selectedAccountIds.has(acc.id)
                ? 'border-primary-300 bg-primary-50 dark:border-primary-700 dark:bg-primary-900/20'
                : 'border-gray-100 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-700/50'"
            >
              <input
                type="checkbox"
                class="checkbox"
                :checked="selectedAccountIds.has(acc.id)"
                @change="toggleAccount(acc.id)"
              />
              <div class="min-w-0 flex-1">
                <div class="truncate text-sm font-medium text-gray-800 dark:text-gray-200">{{ acc.name }}</div>
                <div class="mt-0.5 flex items-center gap-2 text-xs text-gray-400">
                  <span>#{{ acc.id }}</span>
                  <span
                    class="inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium"
                    :class="acc.status === 'active' ? 'bg-green-50 text-green-600 dark:bg-green-900/30 dark:text-green-300' : 'bg-gray-100 text-gray-500 dark:bg-dark-700'"
                  >{{ acc.status }}</span>
                </div>
              </div>
            </label>
          </div>
        </div>
      </section>

      <!-- 运行任务 -->
      <section class="rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <div class="border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.modelTrace.run') }}</h2>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.runHelp') }}</p>
        </div>
        <div class="space-y-4 px-5 py-4">
          <div class="flex flex-wrap items-center gap-3">
            <button
              type="button"
              class="btn btn-primary"
              :disabled="taskRunning || selectedAccountIds.size === 0 || selectedModels.size < 2"
              @click="runTask('bank')"
            >{{ t('admin.modelTrace.runBank') }}</button>
            <button
              type="button"
              class="btn btn-primary"
              :disabled="taskRunning || selectedAccountIds.size === 0 || selectedModels.size < 2 || !status.bank"
              :title="!status.bank ? t('admin.modelTrace.bankEmpty') : undefined"
              @click="runTask('detect')"
            >{{ t('admin.modelTrace.runDetect') }}</button>
            <span v-if="taskRunning" class="inline-flex items-center gap-1.5 text-xs text-primary-600 dark:text-primary-400">
              <span class="h-1.5 w-1.5 animate-pulse rounded-full bg-primary-500"></span>{{ t('admin.modelTrace.taskRunning') }}
            </span>
          </div>

          <!-- 任务进度 -->
          <div v-if="status.task" class="rounded-md border border-gray-100 bg-gray-50 px-4 py-3 dark:border-dark-700 dark:bg-dark-900/40">
            <div class="flex flex-wrap items-center justify-between gap-2 text-xs">
              <div class="flex items-center gap-2">
                <span class="rounded px-1.5 py-0.5 text-[10px] font-medium"
                  :class="status.task.kind === 'bank' ? 'bg-indigo-50 text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-300' : 'bg-teal-50 text-teal-700 dark:bg-teal-900/30 dark:text-teal-300'"
                >{{ status.task.kind === 'bank' ? t('admin.modelTrace.kindBank') : t('admin.modelTrace.kindDetect') }}</span>
                <span class="font-mono text-gray-500 dark:text-gray-400">{{ status.task.id }}</span>
              </div>
              <span class="text-gray-500 dark:text-gray-400">{{ status.task.done }} / {{ status.task.total }}</span>
            </div>
            <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
              <div class="h-full rounded-full transition-all" :class="status.task.finished ? 'bg-green-500' : 'bg-primary-500'"
                :style="{ width: `${taskPercent}%` }"></div>
            </div>
            <p v-if="status.task.last_error" class="mt-2 text-xs text-red-600 dark:text-red-400">{{ status.task.last_error }}</p>
          </div>
          <p v-else class="text-xs text-gray-400">{{ t('admin.modelTrace.noTask') }}</p>
        </div>
      </section>

      <!-- 指纹库状态 -->
      <section v-if="status.bank" class="rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <div class="border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.modelTrace.bankStatus') }}</h2>
        </div>
        <div class="grid grid-cols-1 gap-4 px-5 py-4 lg:grid-cols-3">
          <div class="lg:col-span-2">
            <div class="mb-2 text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('admin.modelTrace.bankModels') }}</div>
            <div class="overflow-x-auto rounded-md border border-gray-100 dark:border-dark-700">
              <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
                <thead class="bg-gray-50 dark:bg-dark-900/60">
                  <tr>
                    <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.model') }}</th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.samples') }}</th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.totalSamples') }}</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                  <tr v-for="m in status.bank.models" :key="m.model">
                    <td class="px-3 py-2 font-mono text-xs text-gray-700 dark:text-gray-300">{{ m.model }}</td>
                    <td class="px-3 py-2 text-xs text-gray-500 dark:text-gray-400">{{ m.samples }}</td>
                    <td class="px-3 py-2 text-xs text-gray-500 dark:text-gray-400">{{ status.sample_counts?.[m.model] ?? 0 }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
          <div class="space-y-3 text-xs">
            <div>
              <div class="text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.builtAt') }}</div>
              <div class="mt-0.5 text-gray-700 dark:text-gray-300">{{ formatDateTime(status.bank.built_at) }}</div>
            </div>
            <div>
              <div class="text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.calibration') }}</div>
              <div class="mt-0.5 font-mono text-gray-700 dark:text-gray-300">{{ calibrationText }}</div>
            </div>
            <div>
              <div class="text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.orderedWeight') }}</div>
              <div class="mt-0.5 font-mono text-gray-700 dark:text-gray-300">{{ status.bank.ordered_weight }}</div>
            </div>
          </div>
        </div>
      </section>

      <!-- 检测结果 -->
      <section class="rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <div class="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.modelTrace.results') }}</h2>
          <button type="button" class="btn btn-secondary" :disabled="resultsLoading" @click="loadResults">
            <Icon name="refresh" size="sm" :class="resultsLoading ? 'animate-spin' : ''" />
            {{ t('admin.modelTrace.refresh') }}
          </button>
        </div>
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
            <thead class="bg-gray-50 dark:bg-dark-900/60">
              <tr>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.time') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.kind') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.account') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.model') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.verdict') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.match') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.probability') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.validRuns') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.avgLatency') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.reasons') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-if="results.length === 0">
                <td colspan="10" class="px-4 py-8 text-center text-sm text-gray-400">{{ t('admin.modelTrace.noResults') }}</td>
              </tr>
              <tr
                v-for="res in results"
                :key="res.id"
                class="hover:bg-gray-50 dark:hover:bg-dark-700/40"
                :class="res.kind === 'detect' && !res.match ? 'bg-red-50/60 dark:bg-red-900/10' : ''"
              >
                <td class="whitespace-nowrap px-4 py-2.5 text-xs text-gray-500 dark:text-gray-400">{{ formatDateTime(res.created_at) }}</td>
                <td class="px-4 py-2.5">
                  <span class="rounded px-1.5 py-0.5 text-[10px] font-medium"
                    :class="res.kind === 'bank' ? 'bg-indigo-50 text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-300' : 'bg-teal-50 text-teal-700 dark:bg-teal-900/30 dark:text-teal-300'"
                  >{{ res.kind === 'bank' ? t('admin.modelTrace.kindBank') : t('admin.modelTrace.kindDetect') }}</span>
                </td>
                <td class="px-4 py-2.5 text-xs text-gray-700 dark:text-gray-300">{{ accountName(res.account_id) }}</td>
                <td class="px-4 py-2.5 font-mono text-xs text-gray-700 dark:text-gray-300">{{ res.model }}</td>
                <td class="px-4 py-2.5 font-mono text-xs text-gray-700 dark:text-gray-300">{{ res.verdict }}</td>
                <td class="px-4 py-2.5">
                  <span v-if="res.kind === 'detect'" class="text-xs font-medium"
                    :class="res.match ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'"
                  >{{ res.match ? '✓ ' + t('admin.modelTrace.matchOk') : '✗ ' + t('admin.modelTrace.mismatch') }}</span>
                  <span v-else class="text-xs"
                    :class="res.verdict === 'ok' ? 'text-green-600 dark:text-green-400' : 'text-amber-600 dark:text-amber-400'"
                  >{{ res.verdict }}</span>
                </td>
                <td class="px-4 py-2.5 text-xs text-gray-700 dark:text-gray-300">{{ res.probability > 0 ? pctText(res.probability) : '—' }}</td>
                <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-gray-400">{{ res.valid_runs }} / {{ res.failures }}</td>
                <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-gray-400">{{ res.avg_latency_ms ? `${res.avg_latency_ms}ms` : '—' }}</td>
                <td class="max-w-xs truncate px-4 py-2.5 text-xs text-gray-400" :title="res.reasons || undefined">{{ res.reasons || '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- 建库样本 -->
      <section class="rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.modelTrace.samplesCard') }}</h2>
          <div class="flex items-center gap-2">
            <div class="w-56">
              <Select v-model="sampleModelFilter" :options="sampleModelOptions" @change="loadSamples" />
            </div>
            <button
              v-if="sampleModelFilter"
              type="button"
              class="btn btn-secondary text-red-600 dark:text-red-400"
              @click="deleteSamples"
            >{{ t('admin.modelTrace.deleteSamples') }}</button>
            <button type="button" class="btn btn-secondary" :disabled="samplesLoading" @click="loadSamples">
              <Icon name="refresh" size="sm" :class="samplesLoading ? 'animate-spin' : ''" />
              {{ t('admin.modelTrace.refresh') }}
            </button>
          </div>
        </div>
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
            <thead class="bg-gray-50 dark:bg-dark-900/60">
              <tr>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.time') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.model') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.account') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.expectedCount') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.latency') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.numbers') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-if="samples.length === 0">
                <td colspan="6" class="px-4 py-8 text-center text-sm text-gray-400">{{ t('admin.modelTrace.noSamples') }}</td>
              </tr>
              <tr v-for="s in samples" :key="s.id" class="hover:bg-gray-50 dark:hover:bg-dark-700/40">
                <td class="whitespace-nowrap px-4 py-2.5 text-xs text-gray-500 dark:text-gray-400">{{ formatDateTime(s.created_at) }}</td>
                <td class="px-4 py-2.5 font-mono text-xs text-gray-700 dark:text-gray-300">{{ s.model }}</td>
                <td class="px-4 py-2.5 text-xs text-gray-700 dark:text-gray-300">{{ accountName(s.account_id) }}</td>
                <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-gray-400">{{ s.expected_count }} / {{ s.numbers_count }}</td>
                <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-gray-400">{{ s.latency_ms }}ms</td>
                <td class="max-w-xl truncate px-4 py-2.5 font-mono text-xs text-gray-400" :title="s.numbers.join(', ')">{{ numbersPreview(s) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { AccountListItem, AdminGroup } from '@/types'
import type { ModelTraceResult, ModelTraceSample, ModelTraceSettings, ModelTraceStatus } from '@/api/admin/modelTrace'
import AppLayout from '@/components/layout/AppLayout.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'

const { t } = useI18n()
const appStore = useAppStore()

// 默认 8 模型（与后端默认一致）
const PRESET_MODELS = [
  'gpt-5.4',
  'gpt-5.5',
  'gpt-5.6-sol',
  'gpt-5.6-terra',
  'gpt-5.6-luna',
  'gpt-6-astra',
  'gpt-6-sol',
  'gpt-6-luna'
]

const defaultSettings = (): ModelTraceSettings => ({
  models: [...PRESET_MODELS],
  account_ids: [],
  bank_repeats: 12,
  detect_repeats: 5,
  concurrency: 2,
  probe_timeout_secs: 180,
  request_gap_ms: 800
})

const form = reactive<ModelTraceSettings>(defaultSettings())
const selectedModels = ref(new Set<string>(PRESET_MODELS))
const saving = ref(false)

// 账号选择
const groups = ref<AdminGroup[]>([])
const accounts = ref<AccountListItem[]>([])
const accountsLoading = ref(false)
const groupFilter = ref<number | ''>('')
const selectedAccountIds = ref(new Set<number>())

// 状态与任务
const status = ref<ModelTraceStatus>({})
const taskWasRunning = ref(false)
let statusTimer: ReturnType<typeof setInterval> | null = null

// 结果 / 样本
const results = ref<ModelTraceResult[]>([])
const resultsLoading = ref(false)
const samples = ref<ModelTraceSample[]>([])
const samplesLoading = ref(false)
const sampleModelFilter = ref<string>('')

const taskRunning = computed(() => !!status.value.task && !status.value.task.finished)
const taskPercent = computed(() => {
  const task = status.value.task
  if (!task || task.total <= 0) return 0
  return Math.min(100, Math.round((task.done / task.total) * 100))
})

const groupOptions = computed(() => [
  { value: '', label: t('admin.modelTrace.allGroups') },
  ...groups.value.map((g) => ({ value: g.id, label: g.name }))
])

const filteredAccounts = computed(() => {
  if (groupFilter.value === '') return accounts.value
  const group = groups.value.find((g) => g.id === groupFilter.value)
  if (!group) return accounts.value
  return accounts.value.filter((a) => (a as AccountListItem & { groups?: { name?: string }[] }).groups?.some((g) => g.name === group.name) ?? false)
})

// 候选模型 = 预设 ∪ 配置中出现过的模型
const modelOptions = computed(() => {
  const set = new Set<string>(PRESET_MODELS)
  for (const m of form.models ?? []) set.add(m)
  return [...set].sort()
})

const calibrationText = computed(() => {
  const cal = status.value.bank?.calibration ?? {}
  return Object.entries(cal)
    .sort((a, b) => Number(a[0]) - Number(b[0]))
    .map(([g, beta]) => `${g}回=${Number(beta).toFixed(1)}`)
    .join(' · ')
})

const sampleModelOptions = computed(() => {
  const keys = new Set<string>()
  for (const m of status.value.bank?.models ?? []) keys.add(m.model)
  for (const key of Object.keys(status.value.sample_counts ?? {})) keys.add(key)
  return [
    { value: '', label: t('admin.modelTrace.allModels') },
    ...[...keys].sort().map((m) => ({ value: m, label: m }))
  ]
})

function accountName(accountId: number): string {
  const acc = accounts.value.find((a) => a.id === accountId)
  return acc?.name ?? `#${accountId}`
}

function toggleModel(model: string) {
  const next = new Set(selectedModels.value)
  if (next.has(model)) {
    next.delete(model)
  } else {
    next.add(model)
  }
  selectedModels.value = next
}

function toggleAccount(accountId: number) {
  const next = new Set(selectedAccountIds.value)
  if (next.has(accountId)) {
    next.delete(accountId)
  } else {
    next.add(accountId)
  }
  selectedAccountIds.value = next
}

function numbersPreview(s: ModelTraceSample): string {
  const head = s.numbers.slice(0, 48).join(', ')
  return s.numbers.length > 48 ? `${head}, …` : head
}

function pctText(p: number): string {
  return `${(p * 100).toFixed(1)}%`
}

async function loadConfig() {
  try {
    const config = await adminAPI.modelTrace.getConfig()
    Object.assign(form, defaultSettings(), config)
    selectedModels.value = new Set(form.models ?? [])
    selectedAccountIds.value = new Set(form.account_ids ?? [])
  } catch (e) {
    appStore.showError(String((e as Error)?.message ?? e))
  }
}

async function saveSettings() {
  if (selectedModels.value.size < 2) {
    appStore.showError(t('admin.modelTrace.needTwoModels'))
    return
  }
  saving.value = true
  try {
    const payload: ModelTraceSettings = {
      ...form,
      models: [...selectedModels.value].sort(),
      account_ids: [...selectedAccountIds.value].sort((a, b) => a - b)
    }
    const saved = await adminAPI.modelTrace.updateConfig(payload)
    Object.assign(form, defaultSettings(), saved)
    selectedModels.value = new Set(saved.models ?? [])
    selectedAccountIds.value = new Set(saved.account_ids ?? [])
    appStore.showSuccess(t('admin.modelTrace.save') + ' ✓')
  } catch (e) {
    appStore.showError(String((e as Error)?.message ?? e))
  } finally {
    saving.value = false
  }
}

async function loadGroupsAndAccounts() {
  accountsLoading.value = true
  try {
    const [allGroups, accountPage] = await Promise.all([
      adminAPI.groups.getAll('openai' as never).catch(() => adminAPI.groups.getAll()),
      adminAPI.accounts.list(1, 200, { platform: 'openai', type: 'oauth' })
    ])
    groups.value = allGroups.filter((g) => g.platform === 'openai')
    accounts.value = accountPage.items ?? []
  } catch (e) {
    appStore.showError(String((e as Error)?.message ?? e))
  } finally {
    accountsLoading.value = false
  }
}

async function loadStatus() {
  try {
    status.value = await adminAPI.modelTrace.getStatus()
    const task = status.value.task
    if (task && !task.finished) {
      taskWasRunning.value = true
    } else if (taskWasRunning.value) {
      // 任务刚结束：刷新结果与样本
      taskWasRunning.value = false
      await Promise.allSettled([loadResults(), loadSamples()])
    }
  } catch {
    // 静默
  }
}

async function runTask(kind: 'bank' | 'detect') {
  try {
    await adminAPI.modelTrace.runTask(kind)
    appStore.showSuccess(kind === 'bank' ? t('admin.modelTrace.runBank') + ' ✓' : t('admin.modelTrace.runDetect') + ' ✓')
    taskWasRunning.value = true
    await loadStatus()
  } catch (e) {
    appStore.showError(String((e as Error)?.message ?? e))
  }
}

async function loadResults() {
  resultsLoading.value = true
  try {
    results.value = await adminAPI.modelTrace.getResults(undefined, 200)
  } catch {
    // 静默
  } finally {
    resultsLoading.value = false
  }
}

async function loadSamples() {
  samplesLoading.value = true
  try {
    samples.value = await adminAPI.modelTrace.getSamples(sampleModelFilter.value || undefined, 100)
  } catch {
    // 静默
  } finally {
    samplesLoading.value = false
  }
}

async function deleteSamples() {
  const model = sampleModelFilter.value
  if (!model) return
  if (!window.confirm(t('admin.modelTrace.confirmDelete', { model }))) return
  try {
    const { deleted } = await adminAPI.modelTrace.deleteSamples(model)
    appStore.showSuccess(t('admin.modelTrace.deleted', { count: deleted }))
    await Promise.allSettled([loadSamples(), loadStatus()])
  } catch (e) {
    appStore.showError(String((e as Error)?.message ?? e))
  }
}

onMounted(async () => {
  await Promise.allSettled([loadConfig(), loadGroupsAndAccounts(), loadStatus(), loadResults(), loadSamples()])
  // 运行中每 3 秒轮询任务进度；空闲时低频刷新（可在别处触发的任务也能被发现）
  statusTimer = setInterval(loadStatus, 3000)
})

onUnmounted(() => {
  if (statusTimer) clearInterval(statusTimer)
})
</script>
