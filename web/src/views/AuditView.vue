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
  NSelect,
  type SelectOption,
} from 'naive-ui/es/select'
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
  NModal,
} from 'naive-ui/es/modal'
import {
  NScrollbar,
} from 'naive-ui/es/scrollbar'
import {
  NForm,
  NFormItem,
} from 'naive-ui/es/form'
import {
  NGrid,
  NGridItem,
} from 'naive-ui/es/grid'
import {
  useMessage,
} from 'naive-ui/es/message'
import { listAuditLogs, type AuditLog, type AuditDecisionFilter } from '@/api/audit'
import { useAuthStore } from '@/stores/auth'

const message = useMessage()
const auth = useAuthStore()

// admin/developer 看全员，可经 user_id 收敛；viewer 仅看自己（前端隐藏该筛选项，后端也强制忽略）。
const canSeeAll = computed(() => ['admin', 'developer'].includes(auth.user?.role ?? ''))

// 筛选条件（前端暂存，点「查询」后下发）。
const filters = reactive({
  decision: 'all' as AuditDecisionFilter,
  command: '',
  user_id: null as number | null,
  // 时间范围：NDatePicker daterange 返回 [start, end] 毫秒时间戳（或 null / 单值）。
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
const logs = ref<AuditLog[]>([])
const lastScope = ref<'' | 'all' | 'self'>('')

function fmtTime(iso?: string): string {
  if (!iso) return '-'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

function decisionType(d: string): 'success' | 'error' | 'warning' | 'default' {
  switch (d) {
    case 'allow':
      return 'success'
    case 'deny':
      return 'error'
    case 'ask':
      return 'warning'
    default:
      return 'default'
  }
}

const decisionOptions: SelectOption[] = [
  { label: '全部决策', value: 'all' },
  { label: '允许 (allow)', value: 'allow' },
  { label: '拒绝 (deny)', value: 'deny' },
  { label: '需确认 (ask)', value: 'ask' },
]

const columns: DataTableColumns<AuditLog> = [
  { title: 'ID', key: 'id', width: 72, fixed: 'left' },
  { title: '用户', key: 'user_id', width: 84 },
  {
    title: '命令',
    key: 'command',
    minWidth: 240,
    ellipsis: { tooltip: true },
    render(row) {
      return h('code', { class: 'font-mono text-xs' }, row.command || '-')
    },
  },
  {
    title: '工作目录',
    key: 'workdir',
    width: 180,
    ellipsis: { tooltip: true },
  },
  {
    title: '决策',
    key: 'decision',
    width: 110,
    render(row) {
      return h(NTag, { type: decisionType(row.decision), size: 'small', bordered: false }, { default: () => row.decision || '-' })
    },
  },
  {
    title: '执行',
    key: 'allowed',
    width: 90,
    render(row) {
      return h(
        NTag,
        { type: row.allowed ? 'success' : 'default', size: 'small', bordered: false },
        { default: () => (row.allowed ? '允许' : '拒绝') },
      )
    },
  },
  {
    title: '原因',
    key: 'reason',
    minWidth: 160,
    ellipsis: { tooltip: true },
  },
  {
    title: '备注',
    key: 'note',
    minWidth: 120,
    ellipsis: { tooltip: true },
  },
  {
    title: '时间',
    key: 'created_at',
    width: 190,
    render(row) {
      return h('span', { class: 'text-xs text-gray-500 dark:text-gray-400' }, fmtTime(row.created_at))
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 96,
    fixed: 'right',
    render(row) {
      return h(
        NButton,
        { size: 'small', tertiary: true, onClick: () => openDetail(row) },
        { default: () => '详情' },
      )
    },
  },
]

async function load() {
  loading.value = true
  try {
    const params: Parameters<typeof listAuditLogs>[0] = {
      decision: filters.decision,
      command: filters.command,
      limit: pagination.pageSize,
      offset: (pagination.page - 1) * pagination.pageSize,
    }
    if (canSeeAll.value && filters.user_id && filters.user_id > 0) {
      params.user_id = filters.user_id
    }
    if (Array.isArray(filters.range) && filters.range.length === 2) {
      params.start = filters.range[0]
      // 日期范围末值补齐到当天 23:59:59.999，确保包含整日。
      params.end = filters.range[1] + 24 * 60 * 60 * 1000 - 1
    }
    const res = await listAuditLogs(params)
    logs.value = res.audit_logs ?? []
    pagination.itemCount = res.total ?? 0
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
  filters.decision = 'all'
  filters.command = ''
  filters.user_id = null
  filters.range = null
  pagination.page = 1
  load()
}

// 详情弹窗。
const showDetail = ref(false)
const detailRow = ref<AuditLog | null>(null)
function openDetail(row: AuditLog) {
  detailRow.value = row
  showDetail.value = true
}

onMounted(load)
</script>

<template>
  <n-card :bordered="false" class="h-full" content-style="display:flex;flex-direction:column;height:100%">
    <div class="flex items-center mb-3">
      <div>
        <h2 class="text-lg font-semibold m-0">审计日志</h2>
        <span class="text-sm text-gray-500 dark:text-gray-400">
          命令执行审计（M3-02）·
          <template v-if="lastScope === 'self'">仅显示本人记录</template>
          <template v-else-if="lastScope === 'all'">显示全员记录</template>
        </span>
      </div>
      <n-button class="ml-auto" :loading="loading" @click="load">刷新</n-button>
    </div>

    <!-- 筛选区 -->
    <n-form inline label-placement="left" :show-feedback="false" class="mb-3">
      <n-grid :cols="24" :x-gap="12" responsive="screen" item-responsive>
        <n-form-item-gi :span="6" label="决策">
          <n-select v-model:value="filters.decision" :options="decisionOptions" style="width: 100%" />
        </n-form-item-gi>
        <n-form-item-gi :span="6" label="命令">
          <n-input v-model:value="filters.command" placeholder="关键词模糊匹配" clearable @keyup.enter="onSearch" />
        </n-form-item-gi>
        <n-form-item-gi v-if="canSeeAll" :span="5" label="用户 ID">
          <n-input-number v-model:value="filters.user_id" :min="1" placeholder="全员" style="width: 100%" clearable />
        </n-form-item-gi>
        <n-form-item-gi :span="7" label="时间范围">
          <n-date-picker v-model:value="filters.range" type="daterange" clearable style="width: 100%" />
        </n-form-item-gi>
        <n-form-item-gi :span="24 - (canSeeAll ? 24 : 18)" :offset="canSeeAll ? 0 : 18">
          <n-space>
            <n-button type="primary" :loading="loading" @click="onSearch">查询</n-button>
            <n-button @click="onReset">重置</n-button>
          </n-space>
        </n-form-item-gi>
      </n-grid>
    </n-form>

    <n-data-table
      :columns="columns"
      :data="logs"
      :loading="loading"
      :scroll-x="1300"
      :row-key="(row: AuditLog) => row.id"
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

    <n-modal
      v-model:show="showDetail"
      title="审计详情"
      preset="card"
      style="width: 720px; max-width: 94vw"
    >
      <n-empty v-if="!detailRow" description="无数据" />
      <template v-else>
        <n-descriptions :column="1" bordered size="small" label-placement="left">
          <n-descriptions-item label="ID">{{ detailRow.id }}</n-descriptions-item>
          <n-descriptions-item label="用户 ID">{{ detailRow.user_id }}</n-descriptions-item>
          <n-descriptions-item label="决策">
            <n-tag :type="decisionType(detailRow.decision)" size="small" :bordered="false">{{ detailRow.decision }}</n-tag>
          </n-descriptions-item>
          <n-descriptions-item label="执行">
            {{ detailRow.allowed ? '允许' : '拒绝' }}
          </n-descriptions-item>
          <n-descriptions-item label="命令">
            <code class="font-mono text-xs break-all">{{ detailRow.command }}</code>
          </n-descriptions-item>
          <n-descriptions-item label="工作目录">
            <code class="font-mono text-xs break-all">{{ detailRow.workdir }}</code>
          </n-descriptions-item>
          <n-descriptions-item label="原因">{{ detailRow.reason || '-' }}</n-descriptions-item>
          <n-descriptions-item label="备注">{{ detailRow.note || '-' }}</n-descriptions-item>
          <n-descriptions-item label="时间">{{ fmtTime(detailRow.created_at) }}</n-descriptions-item>
        </n-descriptions>
      </template>
    </n-modal>
  </n-card>
</template>
