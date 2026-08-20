<script setup lang="ts">
import { h, ref, reactive, computed, onMounted } from 'vue'
import {
  NButton,
} from 'naive-ui/es/button'
import {
  NCard,
} from 'naive-ui/es/card'
import {
  NDataTable,
  type DataTableColumns,
} from 'naive-ui/es/data-table'
import {
  NInput,
} from 'naive-ui/es/input'
import {
  NInputNumber,
} from 'naive-ui/es/input-number'
import {
  NDatePicker,
} from 'naive-ui/es/date-picker'
import {
  NTag,
} from 'naive-ui/es/tag'
import {
  NSpace,
} from 'naive-ui/es/space'
import {
  NText,
} from 'naive-ui/es/typography'
import {
  NEmpty,
} from 'naive-ui/es/empty'
import {
  NGrid,
  NGridItem,
} from 'naive-ui/es/grid'
import {
  NStatistic,
} from 'naive-ui/es/statistic'
import {
  NForm,
  NFormItem,
} from 'naive-ui/es/form'
import {
  useMessage,
} from 'naive-ui/es/message'
import { listUsage, type UsageRecord } from '@/api/usage'
import { useAuthStore } from '@/stores/auth'

const message = useMessage()
const auth = useAuthStore()

// admin/developer 看全员，可经 user_id 收敛；viewer 仅看自己（前端隐藏该筛选项，后端也强制忽略）。
const canSeeAll = computed(() => ['admin', 'developer'].includes(auth.user?.role ?? ''))

// 筛选条件。
const filters = reactive({
  user_id: null as number | null,
  provider_id: null as number | null,
  model_id: null as number | null,
  session_key: '',
  range: null as number | [number, number] | null,
})

// 分页状态（服务端分页）。
const pagination = reactive({
  page: 1,
  pageSize: 50,
  itemCount: 0,
  showSizePicker: true,
  pageSizes: [20, 50, 100, 200],
})

const loading = ref(false)
const records = ref<UsageRecord[]>([])
const totals = reactive({ prompt_tokens: 0, completion_tokens: 0, total_tokens: 0, records: 0 })
const lastScope = ref<'' | 'all' | 'self'>('')

function fmtTime(iso?: string): string {
  if (!iso) return '-'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

const columns: DataTableColumns<UsageRecord> = [
  { title: 'ID', key: 'id', width: 72, fixed: 'left' },
  { title: '用户', key: 'user_id', width: 84 },
  {
    title: '模型',
    key: 'model_name',
    width: 160,
    ellipsis: { tooltip: true },
  },
  {
    title: '会话',
    key: 'session_key',
    width: 150,
    ellipsis: { tooltip: true },
  },
  {
    title: '提示',
    key: 'prompt_tokens',
    width: 96,
    render(row) {
      return h('span', { class: 'text-xs tabular-nums' }, String(row.prompt_tokens))
    },
  },
  {
    title: '补全',
    key: 'completion_tokens',
    width: 96,
    render(row) {
      return h('span', { class: 'text-xs tabular-nums' }, String(row.completion_tokens))
    },
  },
  {
    title: '合计',
    key: 'total_tokens',
    width: 110,
    render(row) {
      return h('span', { class: 'text-xs tabular-nums font-semibold' }, String(row.total_tokens))
    },
  },
  {
    title: '估算',
    key: 'estimated',
    width: 90,
    render(row) {
      return row.estimated
        ? h(NTag, { type: 'warning', size: 'small', bordered: false }, { default: () => '估算' })
        : h(NTag, { type: 'success', size: 'small', bordered: false }, { default: () => '上游' })
    },
  },
  {
    title: '时间',
    key: 'created_at',
    width: 190,
    render(row) {
      return h('span', { class: 'text-xs text-gray-500 dark:text-gray-400' }, fmtTime(row.created_at))
    },
  },
]

async function load() {
  loading.value = true
  try {
    const params: Parameters<typeof listUsage>[0] = {
      limit: pagination.pageSize,
      offset: (pagination.page - 1) * pagination.pageSize,
    }
    if (canSeeAll.value && filters.user_id && filters.user_id > 0) params.user_id = filters.user_id
    if (filters.provider_id && filters.provider_id > 0) params.provider_id = filters.provider_id
    if (filters.model_id && filters.model_id > 0) params.model_id = filters.model_id
    if (filters.session_key && filters.session_key.trim()) params.session_key = filters.session_key
    if (Array.isArray(filters.range) && filters.range.length === 2) {
      params.start = filters.range[0]
      // 日期范围末值补齐到当天 23:59:59.999，确保包含整日。
      params.end = filters.range[1] + 24 * 60 * 60 * 1000 - 1
    }
    const res = await listUsage(params)
    records.value = res.usage_records ?? []
    pagination.itemCount = res.total ?? 0
    Object.assign(totals, res.totals ?? { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0, records: 0 })
    lastScope.value = res.scope ?? ''
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loading.value = false
  }
}

function onPageChange(page: number) {
  pagination.page = page
  load()
}
function onPageSizeChange(size: number) {
  pagination.pageSize = size
  pagination.page = 1
  load()
}
function onSearch() {
  pagination.page = 1
  load()
}
function onReset() {
  filters.user_id = null
  filters.provider_id = null
  filters.model_id = null
  filters.session_key = ''
  filters.range = null
  pagination.page = 1
  load()
}

onMounted(load)
</script>

<template>
  <n-card :bordered="false" class="h-full" content-style="display:flex;flex-direction:column;height:100%">
    <div class="flex items-center mb-3">
      <div>
        <h2 class="text-lg font-semibold m-0">用量统计</h2>
        <span class="text-sm text-gray-500 dark:text-gray-400">
          Token 计量（M3-03）·
          <template v-if="lastScope === 'self'">仅显示本人记录</template>
          <template v-else-if="lastScope === 'all'">显示全员记录</template>
        </span>
      </div>
      <n-button class="ml-auto" :loading="loading" @click="load">刷新</n-button>
    </div>

    <!-- 累计概览卡片 -->
    <n-grid :cols="4" :x-gap="12" class="mb-3">
      <n-gi>
        <n-card :bordered="true" size="small">
          <n-statistic label="合计 Token" :value="totals.total_tokens" />
        </n-card>
      </n-gi>
      <n-gi>
        <n-card :bordered="true" size="small">
          <n-statistic label="提示 Token" :value="totals.prompt_tokens" />
        </n-card>
      </n-gi>
      <n-gi>
        <n-card :bordered="true" size="small">
          <n-statistic label="补全 Token" :value="totals.completion_tokens" />
        </n-card>
      </n-gi>
      <n-gi>
        <n-card :bordered="true" size="small">
          <n-statistic label="记录数" :value="totals.records" />
        </n-card>
      </n-gi>
    </n-grid>

    <!-- 筛选区 -->
    <n-form inline label-placement="left" :show-feedback="false" class="mb-3">
      <n-grid :cols="24" :x-gap="12" responsive="screen" item-responsive>
        <n-form-item-gi v-if="canSeeAll" :span="5" label="用户 ID">
          <n-input-number v-model:value="filters.user_id" :min="1" placeholder="全员" style="width: 100%" clearable />
        </n-form-item-gi>
        <n-form-item-gi :span="5" label="Provider">
          <n-input-number v-model:value="filters.provider_id" :min="1" placeholder="全部" style="width: 100%" clearable />
        </n-form-item-gi>
        <n-form-item-gi :span="5" label="Model">
          <n-input-number v-model:value="filters.model_id" :min="1" placeholder="全部" style="width: 100%" clearable />
        </n-form-item-gi>
        <n-form-item-gi :span="5" label="会话">
          <n-input v-model:value="filters.session_key" placeholder="会话 key" clearable @keyup.enter="onSearch" />
        </n-form-item-gi>
        <n-form-item-gi :span="4" label="时间">
          <n-date-picker v-model:value="filters.range" type="daterange" clearable style="width: 100%" />
        </n-form-item-gi>
        <n-form-item-gi :span="24 - (canSeeAll ? 19 : 19)" :offset="canSeeAll ? 0 : 19">
          <n-space>
            <n-button type="primary" :loading="loading" @click="onSearch">查询</n-button>
            <n-button @click="onReset">重置</n-button>
          </n-space>
        </n-form-item-gi>
      </n-grid>
    </n-form>

    <n-data-table
      :columns="columns"
      :data="records"
      :loading="loading"
      :scroll-x="1100"
      :row-key="(row: UsageRecord) => row.id"
      :pagination="{
        page: pagination.page,
        pageSize: pagination.pageSize,
        itemCount: pagination.itemCount,
        showSizePicker: pagination.showSizePicker,
        pageSizes: pagination.pageSizes,
        prefix: (info) => `共 ${info.itemCount ?? 0} 条`,
      }"
      :on-update:page="onPageChange"
      :on-update:page-size="onPageSizeChange"
      flex-height
      class="flex-1"
    />
    <n-empty v-if="!loading && records.length === 0" description="暂无用量记录，发起一次对话后即可看到" class="mt-6" />
  </n-card>
</template>
