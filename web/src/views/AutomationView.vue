<script setup lang="ts">
import { h, ref, onMounted } from 'vue'
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
  NEmpty,
} from 'naive-ui/es/empty'
import {
  NPopconfirm,
} from 'naive-ui/es/popconfirm'
import {
  NSpace,
} from 'naive-ui/es/space'
import {
  NSwitch,
} from 'naive-ui/es/switch'
import {
  NTag,
} from 'naive-ui/es/tag'
import {
  NText,
} from 'naive-ui/es/typography'
import {
  useMessage,
} from 'naive-ui/es/message'
import {
  listAutomations,
  createAutomation,
  updateAutomation,
  deleteAutomation,
  triggerTypeLabel,
  type Automation,
} from '@/api/automation'
import {
  listCheckpoints,
  resolveCheckpoint,
  type Checkpoint,
} from '@/api/checkpoint'
import AutomationFormModal from '@/components/automation/AutomationFormModal.vue'
import RunHistoryDrawer from '@/components/automation/RunHistoryDrawer.vue'

const message = useMessage()
const loading = ref(false)
const automations = ref<Automation[]>([])

async function load() {
  loading.value = true
  try {
    const res = await listAutomations()
    automations.value = res.automations
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loading.value = false
  }
}
onMounted(load)

// ---- 创建 / 编辑（表单弹窗已抽为子组件，此处只留状态与提交处理）----
const showModal = ref(false)
const editTarget = ref<Automation | null>(null)
const submitting = ref(false)

function openCreate() {
  editTarget.value = null
  showModal.value = true
}

function openEdit(a: Automation) {
  editTarget.value = a
  showModal.value = true
}

async function onFormSubmit(payload: Record<string, unknown>) {
  submitting.value = true
  try {
    if (editTarget.value === null) {
      await createAutomation(payload as never)
      message.success('自动化创建成功')
    } else {
      await updateAutomation(editTarget.value.id, payload as never)
      message.success('自动化更新成功')
    }
    showModal.value = false
    await load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    submitting.value = false
  }
}

async function toggleEnabled(a: Automation) {
  try {
    await updateAutomation(a.id, { enabled: !a.enabled })
    a.enabled = !a.enabled
    message.success(a.enabled ? '已启用' : '已停用')
  } catch (e) {
    message.error((e as Error).message)
  }
}

async function handleDelete(a: Automation) {
  try {
    await deleteAutomation(a.id)
    message.success('已删除')
    await load()
  } catch (e) {
    message.error((e as Error).message)
  }
}

// ---- 运行历史（抽屉已抽为子组件，内部自加载）----
const showRuns = ref(false)
const runsForName = ref('')
const runsForId = ref<number | null>(null)

function openRuns(a: Automation) {
  runsForName.value = a.name
  runsForId.value = a.id
  showRuns.value = true
}

// ---- 待审批检查点（复用 M3-05 human-in-the-loop）----
const checkpoints = ref<Checkpoint[]>([])
const cpLoading = ref(false)
const resolving = ref<number | null>(null)

async function loadCheckpoints() {
  cpLoading.value = true
  try {
    const res = await listCheckpoints({ status: 'pending', limit: 50, offset: 0 })
    checkpoints.value = res.checkpoints.filter((c) => c.status === 'pending')
  } catch {
    checkpoints.value = []
  } finally {
    cpLoading.value = false
  }
}
onMounted(loadCheckpoints)

async function resolveCp(id: number, action: 'approve' | 'reject') {
  resolving.value = id
  try {
    const res = await resolveCheckpoint(id, action)
    if (!res.ok) {
      message.error('处置失败')
      return
    }
    message.success(action === 'approve' ? '已批准执行' : '已拒绝')
    await loadCheckpoints()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    resolving.value = null
  }
}

function fmt(t?: string | null): string {
  if (!t) return '—'
  return t.replace('T', ' ').slice(0, 16)
}

const columns: DataTableColumns<Automation> = [
  { title: '名称', key: 'name', minWidth: 130 },
  {
    title: '触发器',
    key: 'trigger_type',
    width: 110,
    render(row) {
      return h(
        NTag,
        { size: 'small', bordered: false, type: row.trigger_type === 'cron' ? 'info' : 'warning' },
        { default: () => triggerTypeLabel(row.trigger_type) },
      )
    },
  },
  {
    title: 'cron 表达式',
    key: 'cron_expr',
    minWidth: 130,
    ellipsis: { tooltip: true },
    render(row) {
      if (row.trigger_type !== 'cron') return h(NText, { depth: 3 }, { default: () => '—' })
      return h(NText, { class: 'font-mono text-xs' }, { default: () => row.cron_expr || '—' })
    },
  },
  {
    title: '目标提示词',
    key: 'goal_prompt',
    minWidth: 200,
    ellipsis: { tooltip: true },
    render(row) {
      return h(NText, { depth: 3 }, { default: () => row.goal_prompt })
    },
  },
  {
    title: '启用',
    key: 'enabled',
    width: 90,
    render(row) {
      return h(NSwitch, {
        value: row.enabled,
        size: 'small',
        onUpdateValue: () => toggleEnabled(row),
      })
    },
  },
  {
    title: '上次运行',
    key: 'last_run',
    width: 140,
    render(row) {
      return h(NText, { depth: 3, class: 'text-xs' }, { default: () => fmt(row.last_run) })
    },
  },
  {
    title: '下次运行',
    key: 'next_run',
    width: 140,
    render(row) {
      return h(NText, { depth: 3, class: 'text-xs' }, { default: () => fmt(row.next_run) })
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 230,
    fixed: 'right',
    render(row) {
      return h(NSpace, { size: 4, wrap: false }, {
        default: () => [
          h(NButton, { size: 'small', tertiary: true, onClick: () => openEdit(row) }, { default: () => '编辑' }),
          h(NButton, { size: 'small', tertiary: true, onClick: () => openRuns(row) }, { default: () => '运行历史' }),
          h(
            NPopconfirm,
            { onPositiveClick: () => handleDelete(row) },
            {
              default: () => '确认删除该自动化？关联的 webhook 令牌将失效。',
              trigger: () =>
                h(NButton, { size: 'small', type: 'error', tertiary: true }, { default: () => '删除' }),
            },
          ),
        ],
      })
    },
  },
]

const cpColumns: DataTableColumns<Checkpoint> = [
  {
    title: '命令',
    key: 'command',
    minWidth: 220,
    ellipsis: { tooltip: true },
    render(row) {
      return h(NText, { class: 'font-mono text-xs' }, { default: () => row.command })
    },
  },
  {
    title: '工作目录',
    key: 'workdir',
    minWidth: 160,
    ellipsis: { tooltip: true },
    render(row) {
      return h(NText, { depth: 3, class: 'text-xs' }, { default: () => row.workdir })
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 160,
    fixed: 'right',
    render(row) {
      return h(NSpace, { size: 4, wrap: false }, {
        default: () => [
          h(
            NButton,
            {
              size: 'small',
              type: 'primary',
              tertiary: true,
              loading: resolving.value === row.ID,
              disabled: resolving.value !== null,
              onClick: () => resolveCp(row.ID, 'approve'),
            },
            { default: () => '批准' },
          ),
          h(
            NButton,
            {
              size: 'small',
              type: 'error',
              tertiary: true,
              loading: resolving.value === row.ID,
              disabled: resolving.value !== null,
              onClick: () => resolveCp(row.ID, 'reject'),
            },
            { default: () => '拒绝' },
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
        <h2 class="text-lg font-semibold m-0">自动化管理</h2>
        <n-text depth="3" class="text-sm">
          定时（cron）/ 事件（webhook）触发的自主 Loop 任务：到点或收到外部事件即带着 Goal Prompt 自动推进到完成
        </n-text>
      </div>
      <n-button type="primary" class="ml-auto" @click="openCreate">新建自动化</n-button>
    </div>

    <!-- 待审批检查点（复用 M3-05 human-in-the-loop）：无人值守危险命令在此处置 -->
    <n-card size="small" class="mb-3" :bordered="true">
      <div class="flex items-center mb-2">
        <n-text strong>待审批检查点</n-text>
        <n-tag v-if="checkpoints.length" size="small" type="warning" class="ml-2" :bordered="false">
          {{ checkpoints.length }} 条待处置
        </n-tag>
        <n-button text size="small" class="ml-auto" :loading="cpLoading" @click="loadCheckpoints">
          刷新
        </n-button>
      </div>
      <n-data-table
        v-if="checkpoints.length"
        :columns="cpColumns"
        :data="checkpoints"
        :row-key="(row: Checkpoint) => row.ID"
        size="small"
        :max-height="220"
      />
      <n-empty v-else description="暂无待审批检查点" class="py-4" size="small" />
    </n-card>

    <n-data-table
      :columns="columns"
      :data="automations"
      :loading="loading"
      :scroll-x="1180"
      :row-key="(row: Automation) => row.id"
      flex-height
      class="flex-1"
    />

    <!-- 创建 / 编辑（表单弹窗子组件） -->
    <AutomationFormModal
      v-model:show="showModal"
      :edit="editTarget"
      :submitting="submitting"
      @submit="onFormSubmit"
    />

    <!-- 运行历史（抽屉子组件） -->
    <RunHistoryDrawer
      v-model:show="showRuns"
      :automation-name="runsForName"
      :automation-id="runsForId"
    />
  </n-card>
</template>
