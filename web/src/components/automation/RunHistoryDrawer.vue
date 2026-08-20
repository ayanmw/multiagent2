<script setup lang="ts">
import { h, ref, watch } from 'vue'
import {
  NDataTable,
  type DataTableColumns,
} from 'naive-ui/es/data-table'
import {
  NDrawer,
  NDrawerContent,
} from 'naive-ui/es/drawer'
import {
  NEmpty,
} from 'naive-ui/es/empty'
import {
  NTag,
} from 'naive-ui/es/tag'
import {
  NText,
} from 'naive-ui/es/typography'
import {
  listAutomationRuns,
  runChannelLabel,
  runStatusLabel,
  type AutomationRun,
} from '@/api/automation'

// 自动化运行历史抽屉：内部自行加载该自动化的运行记录并渲染表格。
const props = defineProps<{
  show: boolean
  automationName: string
  automationId: number | null
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
}>()

const runsLoading = ref(false)
const runs = ref<AutomationRun[]>([])

function fmt(t?: string | null): string {
  if (!t) return '—'
  return t.replace('T', ' ').slice(0, 16)
}

const runColumns: DataTableColumns<AutomationRun> = [
  { title: 'ID', key: 'id', width: 70 },
  {
    title: '渠道',
    key: 'channel',
    width: 120,
    render(row) {
      return h(NTag, { size: 'small', bordered: false }, { default: () => runChannelLabel(row.channel) })
    },
  },
  {
    title: '状态',
    key: 'status',
    width: 100,
    render(row) {
      const type = row.status === 'done' ? 'success' : row.status === 'failed' ? 'error' : 'warning'
      return h(NTag, { size: 'small', bordered: false, type }, { default: () => runStatusLabel(row.status) })
    },
  },
  {
    title: '会话',
    key: 'session_key',
    minWidth: 140,
    ellipsis: { tooltip: true },
    render(row) {
      return h(NText, { class: 'font-mono text-xs', depth: 3 }, { default: () => row.session_key })
    },
  },
  { title: '重试', key: 'attempts', width: 70 },
  {
    title: '错误',
    key: 'error',
    minWidth: 160,
    ellipsis: { tooltip: true },
    render(row) {
      if (!row.error) return h(NText, { depth: 3 }, { default: () => '—' })
      return h(NText, { type: 'error', class: 'text-xs' }, { default: () => row.error })
    },
  },
  {
    title: '创建于',
    key: 'created_at',
    width: 150,
    render(row) {
      return h(NText, { depth: 3, class: 'text-xs' }, { default: () => fmt(row.created_at) })
    },
  },
]

// 打开抽屉时加载对应自动化的运行记录。
watch(
  () => props.show,
  async (v) => {
    if (!v || props.automationId === null) return
    runsLoading.value = true
    runs.value = []
    try {
      const res = await listAutomationRuns(props.automationId)
      runs.value = res.runs
    } catch (e) {
      runs.value = []
    } finally {
      runsLoading.value = false
    }
  },
)
</script>

<template>
  <n-drawer :show="show" :width="620" @update:show="(v: boolean) => emit('update:show', v)">
    <n-drawer-content :title="`运行历史 · ${automationName}`" closable>
      <n-data-table
        :columns="runColumns"
        :data="runs"
        :loading="runsLoading"
        :row-key="(row: AutomationRun) => row.id"
        size="small"
        :max-height="640"
      />
      <n-empty v-if="!runsLoading && !runs.length" description="暂无运行记录" class="py-8" />
    </n-drawer-content>
  </n-drawer>
</template>
