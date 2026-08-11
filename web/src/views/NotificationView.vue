<script setup lang="ts">
import { h, ref, reactive, computed, onMounted } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NSelect,
  NSpace,
  NTag,
  NEmpty,
  NAlert,
  NPopconfirm,
  useMessage,
  type DataTableColumns,
  type SelectOption,
} from 'naive-ui'
import {
  listNotifications,
  markNotificationRead,
  markAllNotificationsRead,
  notificationTypeLabel,
  type Notification,
  type NotificationType,
} from '@/api/notification'
import { useAuthStore } from '@/stores/auth'

const message = useMessage()
const auth = useAuthStore()

// 通知中心：本页仅需 notifications:read（所有登录角色都有）；写操作（标记已读）需 notifications:write。
const canWrite = computed(() => ['admin', 'developer'].includes(auth.user?.role ?? ''))

const filters = reactive({
  type: 'all' as 'all' | NotificationType,
})

const pagination = reactive({
  page: 1,
  pageSize: 20,
  itemCount: 0,
  showSizePicker: true,
  pageSizes: [20, 50, 100],
})

const loading = ref(false)
const rows = ref<Notification[]>([])
const unread = ref(0)
const total = ref(0)

function fmtTime(iso?: string): string {
  if (!iso) return '-'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

// 类型 → 配色（与后端 three 类语义对应）。
function typeType(t: NotificationType): 'success' | 'error' | 'warning' | 'default' {
  switch (t) {
    case 'success':
      return 'success'
    case 'failure':
      return 'error'
    case 'checkpoint':
      return 'warning'
    default:
      return 'default'
  }
}

// 前端按类型过滤（后端未提供 type 筛选参数，列表规模可控，前端过滤足够；
// 同时保留未读优先排序：未读在前、已读在后，创建时间倒序）。
const typeOptions: SelectOption[] = [
  { label: '全部类型', value: 'all' },
  { label: '成功', value: 'success' },
  { label: '失败', value: 'failure' },
  { label: '待审批', value: 'checkpoint' },
]

const columns: DataTableColumns<Notification> = [
  {
    title: '状态',
    key: 'read',
    width: 80,
    fixed: 'left',
    render(row) {
      return row.read
        ? h(NTag, { size: 'small', type: 'default', bordered: false }, { default: () => '已读' })
        : h(NTag, { size: 'small', type: 'info', bordered: false }, { default: () => '未读' })
    },
  },
  {
    title: '类型',
    key: 'type',
    width: 96,
    render(row) {
      return h(
        NTag,
        { size: 'small', type: typeType(row.type), bordered: false },
        { default: () => notificationTypeLabel(row.type) },
      )
    },
  },
  { title: '标题', key: 'title', minWidth: 180, ellipsis: { tooltip: true } },
  {
    title: '内容',
    key: 'message',
    minWidth: 260,
    ellipsis: { tooltip: true },
  },
  {
    title: '关联',
    key: 'ref',
    width: 150,
    render(row) {
      if (!row.ref_kind && !row.ref_id) return h('span', { class: 'text-gray-400' }, '-')
      return h('span', { class: 'font-mono text-xs' }, `${row.ref_kind}:${row.ref_id}`)
    },
  },
  {
    title: '时间',
    key: 'created_at',
    width: 180,
    render(row) {
      return h('span', { class: 'text-xs text-gray-500 dark:text-gray-400' }, fmtTime(row.created_at))
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 110,
    fixed: 'right',
    render(row) {
      if (row.read || !canWrite.value) return h('span', { class: 'text-gray-400' }, '-')
      return h(
        NButton,
        { size: 'small', tertiary: true, disabled: marking.value, onClick: () => markOne(row) },
        { default: () => '标记已读' },
      )
    },
  },
]

const filteredRows = computed(() => {
  if (filters.type === 'all') return rows.value
  return rows.value.filter((r) => r.type === filters.type)
})

async function load() {
  loading.value = true
  try {
    const res = await listNotifications({
      limit: pagination.pageSize,
      offset: (pagination.page - 1) * pagination.pageSize,
    })
    // 未读优先排序：未读在前、同组内创建时间倒序（接口返回本身已是创建倒序）。
    rows.value = [...(res.notifications ?? [])].sort((a, b) => Number(a.read) - Number(b.read))
    pagination.itemCount = res.total ?? 0
    total.value = res.total ?? 0
    unread.value = res.unread ?? 0
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
function onTypeChange() {
  pagination.page = 1
  load()
}

const marking = ref(false)
async function markOne(row: Notification) {
  marking.value = true
  try {
    await markNotificationRead(row.id)
    message.success('已标记为已读')
    load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    marking.value = false
  }
}

async function markAll() {
  marking.value = true
  try {
    const res = await markAllNotificationsRead()
    message.success(`已将 ${res.affected ?? 0} 条通知标记为已读`)
    load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    marking.value = false
  }
}

onMounted(load)
</script>

<template>
  <n-card :bordered="false" class="h-full" content-style="display:flex;flex-direction:column;height:100%">
    <div class="flex items-center mb-3">
      <div>
        <h2 class="text-lg font-semibold m-0">
          通知中心
          <n-tag v-if="unread > 0" type="error" size="small" :bordered="false" class="ml-2">
            未读 {{ unread }}
          </n-tag>
        </h2>
        <span class="text-sm text-gray-500 dark:text-gray-400">
          主动 Loop 的运行结果回发（M4-07）· 完成 / 失败 / 需人工检查点 都会在此沉淀
        </span>
      </div>
      <n-popconfirm v-if="canWrite" :disabled="unread === 0" @positive-click="markAll">
        <template #trigger>
          <n-button class="ml-auto" type="primary" ghost :disabled="unread === 0 || marking">
            全部已读
          </n-button>
        </template>
        确认将全部未读通知标记为已读？
      </n-popconfirm>
      <n-button v-else class="ml-auto" :loading="loading" @click="load">刷新</n-button>
    </div>

    <n-alert v-if="!canWrite" type="info" :bordered="false" class="mb-3">
      当前角色只读：标记已读需要 <code>notifications:write</code> 权限（admin / developer）。
    </n-alert>

    <n-space class="mb-3" :size="12">
      <n-select
        v-model:value="filters.type"
        :options="typeOptions"
        style="width: 200px"
        @update:value="onTypeChange"
      />
      <n-button :loading="loading" @click="load">刷新</n-button>
    </n-space>

    <n-data-table
      :columns="columns"
      :data="filteredRows"
      :loading="loading"
      :scroll-x="1100"
      :row-key="(row: Notification) => row.id"
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
    >
      <template #empty>
        <n-empty description="暂无通知" />
      </template>
    </n-data-table>
  </n-card>
</template>
