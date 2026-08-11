<script setup lang="ts">
import { h, ref, reactive, onMounted } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NDrawer,
  NDrawerContent,
  NModal,
  NForm,
  NFormItem,
  NInput,
  NRadioGroup,
  NRadioButton,
  NSwitch,
  NPopconfirm,
  NTag,
  NSpace,
  NText,
  NEmpty,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import {
  listAutomations,
  createAutomation,
  updateAutomation,
  deleteAutomation,
  listAutomationRuns,
  triggerTypeLabel,
  runChannelLabel,
  runStatusLabel,
  type Automation,
  type AutomationRun,
  type AutomationTriggerType,
} from '@/api/automation'
import {
  listCheckpoints,
  resolveCheckpoint,
  type Checkpoint,
} from '@/api/checkpoint'

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

// ---- 创建 / 编辑 ----
const showModal = ref(false)
const editingId = ref<number | null>(null)
const submitting = ref(false)
const form = reactive({
  name: '',
  trigger_type: 'cron' as AutomationTriggerType,
  cron_expr: '',
  goal_prompt: '',
  enabled: true,
})

function resetForm() {
  form.name = ''
  form.trigger_type = 'cron'
  form.cron_expr = ''
  form.goal_prompt = ''
  form.enabled = true
}

function openCreate() {
  editingId.value = null
  resetForm()
  showModal.value = true
}

function openEdit(a: Automation) {
  editingId.value = a.id
  form.name = a.name
  form.trigger_type = a.trigger_type
  form.cron_expr = a.cron_expr
  form.goal_prompt = a.goal_prompt
  form.enabled = a.enabled
  showModal.value = true
}

async function submit() {
  if (!form.name.trim()) {
    message.warning('请填写名称')
    return
  }
  if (!form.goal_prompt.trim()) {
    message.warning('请填写目标提示词（Goal Prompt）')
    return
  }
  if (form.trigger_type === 'cron' && !form.cron_expr.trim()) {
    message.warning('定时触发器需要填写 cron 表达式')
    return
  }
  const payload = {
    name: form.name.trim(),
    trigger_type: form.trigger_type,
    goal_prompt: form.goal_prompt.trim(),
    enabled: form.enabled,
  } as Record<string, unknown>
  if (form.trigger_type === 'cron') {
    payload.cron_expr = form.cron_expr.trim()
  }
  submitting.value = true
  try {
    if (editingId.value === null) {
      await createAutomation(payload as never)
      message.success('自动化创建成功')
    } else {
      await updateAutomation(editingId.value, payload as never)
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

// ---- 运行历史 ----
const showRuns = ref(false)
const runsLoading = ref(false)
const runs = ref<AutomationRun[]>([])
const runsFor = ref('')

async function openRuns(a: Automation) {
  runsFor.value = a.name
  showRuns.value = true
  runsLoading.value = true
  try {
    const res = await listAutomationRuns(a.id)
    runs.value = res.runs
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    runsLoading.value = false
  }
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

const triggerOptions = [
  { label: '定时（cron）', value: 'cron' },
  { label: '事件（webhook）', value: 'webhook' },
]

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
  { title: '上次运行', key: 'last_run', width: 140, render(row) { return h(NText, { depth: 3, class: 'text-xs' }, { default: () => fmt(row.last_run) }) } },
  { title: '下次运行', key: 'next_run', width: 140, render(row) { return h(NText, { depth: 3, class: 'text-xs' }, { default: () => fmt(row.next_run) }) } },
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
  { title: '会话', key: 'session_key', minWidth: 140, ellipsis: { tooltip: true }, render(row) { return h(NText, { class: 'font-mono text-xs', depth: 3 }, { default: () => row.session_key }) } },
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
  { title: '创建于', key: 'created_at', width: 150, render(row) { return h(NText, { depth: 3, class: 'text-xs' }, { default: () => fmt(row.created_at) }) } },
]

const cpColumns: DataTableColumns<Checkpoint> = [
  {
    title: '命令',
    key: 'command',
    minWidth: 220,
    ellipsis: { tooltip: true },
    render(row) { return h(NText, { class: 'font-mono text-xs' }, { default: () => row.command }) },
  },
  {
    title: '工作目录',
    key: 'workdir',
    minWidth: 160,
    ellipsis: { tooltip: true },
    render(row) { return h(NText, { depth: 3, class: 'text-xs' }, { default: () => row.workdir }) },
  },
  {
    title: '操作',
    key: 'actions',
    width: 160,
    fixed: 'right',
    render(row) {
      return h(NSpace, { size: 4, wrap: false }, {
        default: () => [
          h(NButton, { size: 'small', type: 'primary', tertiary: true, loading: resolving.value === row.ID, disabled: resolving.value !== null, onClick: () => resolveCp(row.ID, 'approve') }, { default: () => '批准' }),
          h(NButton, { size: 'small', type: 'error', tertiary: true, loading: resolving.value === row.ID, disabled: resolving.value !== null, onClick: () => resolveCp(row.ID, 'reject') }, { default: () => '拒绝' }),
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
        <n-button text size="small" class="ml-auto" :loading="cpLoading" @click="loadCheckpoints">刷新</n-button>
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

    <!-- 创建 / 编辑 -->
    <n-modal
      v-model:show="showModal"
      :title="editingId === null ? '新建自动化' : '编辑自动化'"
      preset="card"
      style="width: 600px; max-width: 94vw"
    >
      <n-form :model="form" label-placement="top">
        <n-form-item label="名称" required>
          <n-input v-model:value="form.name" placeholder="例如：每小时推进需求文档" />
        </n-form-item>
        <n-form-item label="触发器">
          <n-radio-group v-model:value="form.trigger_type">
            <n-radio-button v-for="o in triggerOptions" :key="o.value" :value="o.value">{{ o.label }}</n-radio-button>
          </n-radio-group>
        </n-form-item>
        <n-form-item v-if="form.trigger_type === 'cron'" label="cron 表达式" required>
          <n-input v-model:value="form.cron_expr" placeholder="例如：*/1 * * * *（每分钟）；0 9 * * *（每天 9 点）" />
          <template #feedback>
            <span class="text-xs text-gray-400">标准 5 段 cron；调度器据此计算「下次运行」时间</span>
          </template>
        </n-form-item>
        <n-form-item v-else label="事件入口说明">
          <n-text depth="3" class="text-xs">
            保存后后端自动生成 32B webhook 令牌；外部系统向
            <code class="font-mono">POST /api/webhooks/&lt;token&gt;</code>
            即可触发本自动化的 Loop（令牌不在此回显，可于后端日志/数据库查询）。
          </n-text>
        </n-form-item>
        <n-form-item label="目标提示词（Goal Prompt）" required>
          <n-input
            v-model:value="form.goal_prompt"
            type="textarea"
            :autosize="{ minRows: 3, maxRows: 8 }"
            placeholder="描述这个自动化要自主完成的目标，例如：阅读 docs/loop/PLAN.md，挑选第一个 ○ 任务实现并验证后提交。"
          />
        </n-form-item>
        <n-form-item label="启用">
          <n-switch v-model:value="form.enabled" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showModal = false">取消</n-button>
          <n-button type="primary" :loading="submitting" @click="submit">
            {{ editingId === null ? '创建' : '保存' }}
          </n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 运行历史 -->
    <n-drawer v-model:show="showRuns" :width="620">
      <n-drawer-content :title="`运行历史 · ${runsFor}`" closable>
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
  </n-card>
</template>
