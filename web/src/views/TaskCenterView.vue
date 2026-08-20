<script setup lang="ts">
import { h, ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
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
  NModal,
} from 'naive-ui/es/modal'
import {
  NTag,
} from 'naive-ui/es/tag'
import {
  NSpace,
} from 'naive-ui/es/space'
import {
  NScrollbar,
} from 'naive-ui/es/scrollbar'
import {
  NEmpty,
} from 'naive-ui/es/empty'
import {
  useMessage,
} from 'naive-ui/es/message'
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
const cancellingId = ref<string | null>(null)
const transcriptBodyRef = ref<HTMLElement | null>(null)
let pollTimer: ReturnType<typeof setInterval> | null = null

async function openDetail(run: TaskRun) {
  stopPoll()
  detailRun.value = run
  transcript.value = null
  showDetail.value = true
  detailLoading.value = true
  try {
    detailRun.value = await getTaskRun(run.id)
    transcript.value = await getTaskRunTranscript(run.id)
    maybeScroll()
    startPollIfActive()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    detailLoading.value = false
  }
}

// 运行中轮询：详情打开且任务仍活跃时，每 2s 拉取一次 transcript，形成「流」体验。
async function refreshDetail() {
  if (!detailRun.value) return
  try {
    detailRun.value = await getTaskRun(detailRun.value.id)
    transcript.value = await getTaskRunTranscript(detailRun.value.id)
    maybeScroll()
    if (!isActive(detailRun.value.status)) stopPoll()
  } catch {
    // 轮询失败静默忽略，等待下一次或用户手动刷新
  }
}

function startPollIfActive() {
  stopPoll()
  if (detailRun.value && isActive(detailRun.value.status)) {
    pollTimer = setInterval(refreshDetail, 2000)
  }
}
function stopPoll() {
  if (pollTimer !== null) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}
watch(showDetail, (v) => {
  if (!v) stopPoll()
})
onUnmounted(stopPoll)

async function handleCancel(run: TaskRun) {
  cancellingId.value = run.id
  try {
    await cancelTaskRun(run.id)
    message.success('已发送取消请求，后台任务将停止')
    await load()
    if (detailRun.value && detailRun.value.id === run.id) {
      detailRun.value = await getTaskRun(run.id)
      transcript.value = await getTaskRunTranscript(run.id)
      startPollIfActive()
    }
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    cancellingId.value = null
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

function agentTagType(author?: string): 'default' | 'info' | 'success' | 'warning' | 'error' {
  switch ((author || '').toLowerCase()) {
    case 'orchestrator':
      return 'warning'
    case 'coder':
      return 'info'
    case 'reviewer':
      return 'success'
    default:
      return 'default'
  }
}

// 把框架 session 事件解析为可读 transcript 行。
interface ParsedEvent {
  author: string
  text: string
  reasoning: string
  toolCalls: { name: string; args: string }[]
  toolResult: { name: string; content: string } | null
  error: string | null
  timestamp: string
}
function parseEvent(ev: Record<string, unknown>): ParsedEvent {
  const author = (ev.author as string) || ''
  const choices = (Array.isArray(ev.choices) ? (ev.choices as any[]) : []) || []
  const ch = choices[0] || {}
  const msg = ch.message || {}
  const delta = ch.delta || {}
  const text = (msg.content as string) || (delta.content as string) || ''
  const reasoning = (msg.reasoning_content as string) || (delta.reasoning_content as string) || ''
  const role = (msg.role as string) || (delta.role as string) || ''
  const toolCallsRaw: any[] = msg.tool_calls || delta.tool_calls || []
  const toolCalls = toolCallsRaw.map((tc) => {
    const fn = tc?.function || {}
    const argsRaw = fn.arguments
    const args = typeof argsRaw === 'string' ? argsRaw : argsRaw != null ? JSON.stringify(argsRaw) : ''
    return { name: fn.name || tc?.name || '', args }
  })
  let toolResult: { name: string; content: string } | null = null
  if (role === 'tool' || msg.tool_name) {
    toolResult = { name: (msg.tool_name as string) || '', content: text }
  }
  let error: string | null = null
  if (ev.error && typeof ev.error === 'object') {
    error = (ev.error as any).message || JSON.stringify(ev.error)
  } else if (typeof ev.error === 'string') {
    error = ev.error
  }
  return {
    author,
    text,
    reasoning,
    toolCalls,
    toolResult,
    error,
    timestamp: (ev.timestamp as string) || '',
  }
}

const parsedEvents = computed<ParsedEvent[]>(() => {
  if (!transcript.value?.events) return []
  return (transcript.value.events as Record<string, unknown>[]).map(parseEvent)
})

function maybeScroll() {
  nextTick(() => {
    const el = transcriptBodyRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
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
            {
              size: 'small',
              type: 'error',
              tertiary: true,
              disabled: !isActive(row.status) || cancellingId.value !== null,
              loading: cancellingId.value === row.id,
              onClick: () => handleCancel(row),
            },
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
      style="width: 820px; max-width: 94vw"
    >
      <n-empty v-if="detailLoading" description="加载中…" />
      <template v-else-if="detailRun">
        <div class="mb-3">
          <div class="flex items-center gap-2 mb-1">
            <div class="text-sm font-semibold">运行记录</div>
            <n-tag :type="statusType(detailRun.status)" size="small" :bordered="false">{{ detailRun.status ?? '-' }}</n-tag>
            <span v-if="pollTimer !== null" class="text-xs text-blue-500">• 实时刷新中</span>
          </div>
          <n-scrollbar style="max-height: 200px">
            <pre class="text-xs bg-gray-50 dark:bg-gray-900 rounded p-2 whitespace-pre-wrap">{{ JSON.stringify(detailRun, null, 2) }}</pre>
          </n-scrollbar>
        </div>
        <div>
          <div class="flex items-center gap-2 mb-1">
            <div class="text-sm font-semibold">子任务 Transcript</div>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ parsedEvents.length }} 条事件</span>
            <n-button class="ml-auto" size="tiny" tertiary @click="refreshDetail">手动刷新</n-button>
          </div>
          <n-empty v-if="parsedEvents.length === 0" description="暂无 transcript 事件" />
          <n-scrollbar v-else style="max-height: 360px">
            <div ref="transcriptBodyRef" class="flex flex-col gap-2">
              <div
                v-for="(pe, i) in parsedEvents"
                :key="i"
                class="rounded border border-gray-100 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 p-2"
              >
                <div class="flex items-start gap-2">
                  <n-tag :type="agentTagType(pe.author)" size="small" :bordered="false" class="shrink-0">
                    {{ pe.author || 'system' }}
                  </n-tag>
                  <div class="flex-1 min-w-0 text-xs">
                    <pre
                      v-if="pe.reasoning"
                      class="whitespace-pre-wrap break-words text-gray-500 dark:text-gray-400 italic border-l-2 border-gray-300 dark:border-gray-600 pl-2 mb-1"
                    >{{ pe.reasoning }}</pre>
                    <pre v-if="pe.text" class="whitespace-pre-wrap break-words">{{ pe.text }}</pre>
                    <div v-for="(tc, j) in pe.toolCalls" :key="j" class="mt-1">
                      <span class="text-blue-600 dark:text-blue-400">调用工具 </span>
                      <code class="font-mono">{{ tc.name }}</code>
                      <pre
                        v-if="tc.args"
                        class="text-[11px] bg-gray-100 dark:bg-gray-800 rounded p-1 mt-0.5 whitespace-pre-wrap break-words"
                      >{{ tc.args }}</pre>
                    </div>
                    <div v-if="pe.toolResult" class="mt-1 text-green-700 dark:text-green-400">
                      <span>工具结果{{ pe.toolResult.name ? ' (' + pe.toolResult.name + ')' : '' }}</span>
                      <pre
                        class="text-[11px] bg-gray-100 dark:bg-gray-800 rounded p-1 mt-0.5 whitespace-pre-wrap break-words"
                      >{{ pe.toolResult.content }}</pre>
                    </div>
                    <div v-if="pe.error" class="mt-1 text-red-600 dark:text-red-400">错误：{{ pe.error }}</div>
                  </div>
                </div>
              </div>
            </div>
          </n-scrollbar>
        </div>
      </template>
    </n-modal>
  </n-card>
</template>
