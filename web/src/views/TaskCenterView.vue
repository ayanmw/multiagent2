<script setup lang="ts">
import { h, ref, onMounted } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NModal,
  NTag,
  NSpace,
  NScrollbar,
  NEmpty,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import {
  listTaskRuns,
  getTaskRun,
  cancelTaskRun,
  getTaskRunTranscript,
  type TaskRun,
  type TaskRunTranscript,
} from '@/api/taskrun'

const message = useMessage()
const loading = ref(false)
const runs = ref<TaskRun[]>([])

async function load() {
  loading.value = true
  try {
    runs.value = await listTaskRuns()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loading.value = false
  }
}
onMounted(load)

const showDetail = ref(false)
const detailRun = ref<TaskRun | null>(null)
const detailLoading = ref(false)
const transcript = ref<TaskRunTranscript | null>(null)
const cancelling = ref(false)

async function openDetail(run: TaskRun) {
  detailRun.value = run
  transcript.value = null
  showDetail.value = true
  detailLoading.value = true
  try {
    detailRun.value = await getTaskRun(run.id)
    transcript.value = await getTaskRunTranscript(run.id)
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    detailLoading.value = false
  }
}

async function handleCancel(run: TaskRun) {
  cancelling.value = true
  try {
    await cancelTaskRun(run.id)
    message.success('已发送取消请求')
    await load()
    if (detailRun.value && detailRun.value.id === run.id) {
      detailRun.value = await getTaskRun(run.id)
    }
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    cancelling.value = false
  }
}

function statusType(s?: string): 'default' | 'success' | 'warning' | 'error' {
  switch (s) {
    case 'completed':
    case 'done':
      return 'success'
    case 'running':
    case 'active':
    case 'pending':
      return 'warning'
    case 'failed':
    case 'error':
    case 'cancelled':
      return 'error'
    default:
      return 'default'
  }
}

function isActive(s?: string): boolean {
  return s === 'running' || s === 'active' || s === 'pending'
}

const columns: DataTableColumns<TaskRun> = [
  {
    title: 'ID',
    key: 'id',
    minWidth: 160,
    render(row) {
      return h('code', { class: 'font-mono text-xs' }, row.id)
    },
  },
  {
    title: '状态',
    key: 'status',
    width: 120,
    render(row) {
      return h(NTag, { type: statusType(row.status), size: 'small', bordered: false }, { default: () => row.status ?? '-' })
    },
  },
  { title: 'App', key: 'app_name', width: 140, ellipsis: { tooltip: true } },
  {
    title: '子会话',
    key: 'child_session_id',
    minWidth: 160,
    ellipsis: { tooltip: true },
    render(row) {
      return h('code', { class: 'font-mono text-xs' }, row.child_session_id ?? '-')
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 180,
    fixed: 'right',
    render(row) {
      return h(NSpace, { size: 4, wrap: false }, {
        default: () => [
          h(NButton, { size: 'small', tertiary: true, onClick: () => openDetail(row) }, { default: () => '详情' }),
          h(
            NButton,
            { size: 'small', type: 'error', tertiary: true, disabled: !isActive(row.status), loading: cancelling && false, onClick: () => handleCancel(row) },
            { default: () => '取消' },
          ),
        ],
      })
    },
  },
]
</script>

<template>
  <n-card :bordered="false" class="h-full" content-style="display:flex;flex-direction:column;height:100%">
    <div class="flex items-center mb-3">
      <div>
        <h2 class="text-lg font-semibold m-0">任务中心</h2>
        <n-empty v-if="false" />
        <span class="text-sm text-gray-500 dark:text-gray-400">后台任务（taskrun）列表、状态与子任务 transcript（由 Agent 工具发起）</span>
      </div>
      <n-button class="ml-auto" :loading="loading" @click="load">刷新</n-button>
    </div>

    <n-data-table
      :columns="columns"
      :data="runs"
      :loading="loading"
      :scroll-x="900"
      :row-key="(row: TaskRun) => row.id"
      flex-height
      class="flex-1"
    />

    <n-modal
      v-model:show="showDetail"
      title="任务详情"
      preset="card"
      style="width: 760px; max-width: 94vw"
    >
      <n-empty v-if="detailLoading" description="加载中…" />
      <template v-else-if="detailRun">
        <div class="mb-3">
          <div class="text-sm font-semibold mb-1">运行记录</div>
          <n-scrollbar style="max-height: 200px">
            <pre class="text-xs bg-gray-50 dark:bg-gray-900 rounded p-2 whitespace-pre-wrap">{{ JSON.stringify(detailRun, null, 2) }}</pre>
          </n-scrollbar>
        </div>
        <div>
          <div class="text-sm font-semibold mb-1">
            子任务 Transcript（{{ transcript?.events?.length ?? 0 }} 条事件）
          </div>
          <n-empty v-if="!transcript || transcript.events.length === 0" description="暂无 transcript 事件" />
          <n-scrollbar v-else style="max-height: 320px">
            <div class="flex flex-col gap-1.5">
              <pre
                v-for="(ev, i) in transcript.events.slice(0, 200)"
                :key="i"
                class="text-[11px] leading-relaxed bg-gray-50 dark:bg-gray-900 rounded p-2 whitespace-pre-wrap"
              >{{ JSON.stringify(ev, null, 2) }}</pre>
            </div>
          </n-scrollbar>
        </div>
      </template>
    </n-modal>
  </n-card>
</template>
