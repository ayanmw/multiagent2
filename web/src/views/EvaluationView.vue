<script setup lang="ts">
import { h, ref, computed, onMounted, watch } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NModal,
  NForm,
  NFormItem,
  NInput,
  NSelect,
  NSpace,
  NText,
  NTag,
  NInputNumber,
  NEmpty,
  NSpin,
  useMessage,
  useDialog,
  type DataTableColumns,
  type SelectOption,
} from 'naive-ui'
import {
  listEvalDatasets,
  createEvalDataset,
  updateEvalDataset,
  deleteEvalDataset,
  listEvalCases,
  createEvalCase,
  updateEvalCase,
  deleteEvalCase,
  listEvalRuns,
  getEvalRun,
  runEval,
  listEvalResults,
  type EvalDataset,
  type EvalCase,
  type EvalRun,
  type EvalResult,
  type GraderType,
} from '@/api/eval'

const message = useMessage()
const dialog = useDialog()

const datasets = ref<EvalDataset[]>([])
const selectedDatasetId = ref<number | null>(null)
const loadingDatasets = ref(false)

const cases = ref<EvalCase[]>([])
const loadingCases = ref(false)

const runs = ref<EvalRun[]>([])
const loadingRuns = ref(false)

const selectedRunId = ref<number | null>(null)
const results = ref<EvalResult[]>([])
const loadingResults = ref(false)

const graderOptions: SelectOption[] = [
  { label: '精确匹配 exact', value: 'exact' },
  { label: '包含匹配 contains', value: 'contains' },
  { label: 'LLM 裁判 llm', value: 'llm' },
]

const datasetOptions = computed<SelectOption[]>(
  () => datasets.value.map((d) => ({ label: d.name, value: d.id })),
)

// ---- 评估集 Dataset ----

async function loadDatasets() {
  loadingDatasets.value = true
  try {
    const res = await listEvalDatasets()
    datasets.value = res.datasets ?? []
    if (selectedDatasetId.value == null && datasets.value.length) {
      selectedDatasetId.value = datasets.value[0].id
    } else if (!datasets.value.some((d) => d.id === selectedDatasetId.value)) {
      selectedDatasetId.value = datasets.value[0]?.id ?? null
    }
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loadingDatasets.value = false
  }
}

async function selectDataset(id: number | null) {
  selectedDatasetId.value = id
  selectedRunId.value = null
  results.value = []
  if (id != null) {
    await Promise.all([loadCases(id), loadRuns(id)])
  } else {
    cases.value = []
    runs.value = []
  }
}

watch(selectedDatasetId, (id) => selectDataset(id))

// ---- 用例 Case ----

async function loadCases(datasetId: number) {
  loadingCases.value = true
  try {
    const res = await listEvalCases(datasetId)
    cases.value = res.cases ?? []
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loadingCases.value = false
  }
}

// ---- 运行 Run ----

async function loadRuns(datasetId: number) {
  loadingRuns.value = true
  try {
    const res = await listEvalRuns(datasetId)
    runs.value = res.runs ?? []
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loadingRuns.value = false
  }
}

const runModel = ref('')
const runGrader = ref<GraderType | null>(null)
const runRepeats = ref(1)
const running = ref(false)

async function handleRun() {
  if (selectedDatasetId.value == null) return
  running.value = true
  try {
    const run = await runEval(selectedDatasetId.value, {
      model: runModel.value.trim() || undefined,
      grader: runGrader.value ?? undefined,
      repeats: runRepeats.value,
    })
    message.success('已触发回归运行（异步执行，完成后可查分数）')
    await loadRuns(selectedDatasetId.value)
    // 轮询直到收敛（done / failed）。
    pollRun(run.id)
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    running.value = false
  }
}

let pollTimer: ReturnType<typeof setInterval> | null = null
function pollRun(runId: number) {
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = setInterval(async () => {
    try {
      const run = await getEvalRun(runId)
      const idx = runs.value.findIndex((r) => r.id === runId)
      if (idx >= 0) runs.value[idx] = run
      if (run.status !== 'running') {
        if (pollTimer) clearInterval(pollTimer)
        pollTimer = null
        if (run.status === 'done') {
          selectedRunId.value = runId
          await loadResults(runId)
          message.success(`回归完成：平均分 ${run.score_avg.toFixed(2)}，通过率 ${(run.pass_rate * 100).toFixed(0)}%`)
        } else if (run.status === 'failed') {
          message.error(`运行失败：${run.error || '未知错误'}`)
        }
      }
    } catch {
      if (pollTimer) clearInterval(pollTimer)
      pollTimer = null
    }
  }, 1500)
}

// ---- 结果 Result ----

async function loadResults(runId: number) {
  loadingResults.value = true
  try {
    const res = await listEvalResults(runId)
    results.value = res.results ?? []
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loadingResults.value = false
  }
}

function onSelectRun(row: EvalRun) {
  selectedRunId.value = row.id
  loadResults(row.id)
}

// ---- 弹窗：评估集 ----

const showDatasetModal = ref(false)
const datasetModalMode = ref<'create' | 'edit'>('create')
const datasetForm = ref({ id: 0, name: '', description: '', default_grader: 'exact' as GraderType, default_model: '' })
const savingDataset = ref(false)

function openCreateDataset() {
  datasetModalMode.value = 'create'
  datasetForm.value = { id: 0, name: '', description: '', default_grader: 'exact', default_model: '' }
  showDatasetModal.value = true
}
function openEditDataset(row: EvalDataset) {
  datasetModalMode.value = 'edit'
  datasetForm.value = {
    id: row.id,
    name: row.name,
    description: row.description,
    default_grader: row.default_grader,
    default_model: row.default_model,
  }
  showDatasetModal.value = true
}
async function saveDataset() {
  if (!datasetForm.value.name.trim()) {
    message.warning('请填写评估集名称')
    return
  }
  savingDataset.value = true
  try {
    if (datasetModalMode.value === 'create') {
      await createEvalDataset({
        name: datasetForm.value.name.trim(),
        description: datasetForm.value.description.trim(),
        default_grader: datasetForm.value.default_grader,
        default_model: datasetForm.value.default_model.trim() || undefined,
      })
      message.success('评估集已创建')
    } else {
      await updateEvalDataset(datasetForm.value.id, {
        name: datasetForm.value.name.trim(),
        description: datasetForm.value.description.trim(),
        default_grader: datasetForm.value.default_grader,
        default_model: datasetForm.value.default_model.trim() || undefined,
      })
      message.success('评估集已更新')
    }
    showDatasetModal.value = false
    await loadDatasets()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    savingDataset.value = false
  }
}
function confirmDeleteDataset(row: EvalDataset) {
  dialog.warning({
    title: '删除评估集',
    content: `确认删除「${row.name}」？将级联删除其全部用例、运行与结果。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteEvalDataset(row.id)
        message.success('已删除')
        if (selectedDatasetId.value === row.id) selectedDatasetId.value = null
        await loadDatasets()
      } catch (e) {
        message.error((e as Error).message)
      }
    },
  })
}

// ---- 弹窗：用例 ----

const showCaseModal = ref(false)
const caseModalMode = ref<'create' | 'edit'>('create')
const caseForm = ref({ id: 0, dataset_id: 0, input: '', expected: '', grader: '' as GraderType | '', model: '' })
const savingCase = ref(false)

function openCreateCase() {
  if (selectedDatasetId.value == null) return
  caseModalMode.value = 'create'
  caseForm.value = { id: 0, dataset_id: selectedDatasetId.value, input: '', expected: '', grader: '', model: '' }
  showCaseModal.value = true
}
function openEditCase(row: EvalCase) {
  caseModalMode.value = 'edit'
  caseForm.value = {
    id: row.id,
    dataset_id: row.dataset_id,
    input: row.input,
    expected: row.expected,
    grader: row.grader,
    model: row.model,
  }
  showCaseModal.value = true
}
async function saveCase() {
  if (!caseForm.value.input.trim() || !caseForm.value.expected.trim()) {
    message.warning('请填写输入与期望输出')
    return
  }
  savingCase.value = true
  try {
    if (caseModalMode.value === 'create') {
      await createEvalCase(caseForm.value.dataset_id, {
        input: caseForm.value.input,
        expected: caseForm.value.expected,
        grader: caseForm.value.grader || undefined,
        model: caseForm.value.model.trim() || undefined,
      })
      message.success('用例已添加')
    } else {
      await updateEvalCase(caseForm.value.dataset_id, caseForm.value.id, {
        input: caseForm.value.input,
        expected: caseForm.value.expected,
        grader: caseForm.value.grader || undefined,
        model: caseForm.value.model.trim() || undefined,
      })
      message.success('用例已更新')
    }
    showCaseModal.value = false
    if (selectedDatasetId.value != null) await loadCases(selectedDatasetId.value)
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    savingCase.value = false
  }
}
function confirmDeleteCase(row: EvalCase) {
  dialog.warning({
    title: '删除用例',
    content: '确认删除该用例？',
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteEvalCase(row.dataset_id, row.id)
        message.success('已删除')
        if (selectedDatasetId.value != null) await loadCases(selectedDatasetId.value)
      } catch (e) {
        message.error((e as Error).message)
      }
    },
  })
}

function fmtTime(s: string | null) {
  if (!s) return '-'
  const d = new Date(s)
  return isNaN(d.getTime()) ? s : d.toLocaleString()
}
function graderLabel(g: GraderType) {
  return g === 'exact' ? '精确' : g === 'contains' ? '包含' : g === 'llm' ? 'LLM' : g
}
function runStatusType(s: EvalRun['status']) {
  return s === 'done' ? 'success' : s === 'failed' ? 'error' : 'warning'
}

// ---- 表格列 ----

const caseColumns: DataTableColumns<EvalCase> = [
  { title: '输入', key: 'input', minWidth: 200, ellipsis: { tooltip: true } },
  { title: '期望输出', key: 'expected', minWidth: 160, ellipsis: { tooltip: true } },
  {
    title: '评分器',
    key: 'grader',
    width: 90,
    render(row) {
      return row.grader ? h(NTag, { size: 'small', bordered: false }, { default: () => graderLabel(row.grader) }) : h(NText, { depth: 3 }, { default: () => '默认' })
    },
  },
  {
    title: '模型',
    key: 'model',
    width: 120,
    render(row) {
      return h(NText, { depth: 3 }, { default: () => row.model || '-' })
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 120,
    fixed: 'right',
    render(row) {
      return h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', tertiary: true, onClick: () => openEditCase(row) }, { default: () => '编辑' }),
          h(NButton, { size: 'small', tertiary: true, type: 'error', onClick: () => confirmDeleteCase(row) }, { default: () => '删除' }),
        ],
      })
    },
  },
]

const runColumns: DataTableColumns<EvalRun> = [
  {
    title: 'ID',
    key: 'id',
    width: 70,
    render(row) {
      return h(NButton, { size: 'tiny', text: true, type: 'primary', onClick: () => onSelectRun(row) }, { default: () => `#${row.id}` })
    },
  },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render(row) {
      return h(NTag, { size: 'small', type: runStatusType(row.status), bordered: false }, { default: () => row.status })
    },
  },
  { title: '模型', key: 'model', width: 120, render(row) { return h(NText, { depth: 3 }, { default: () => row.model || '-' }) } },
  { title: '评分器', key: 'grader', width: 90, render(row) { return h(NText, { depth: 3 }, { default: () => graderLabel(row.grader) }) } },
  { title: '重复', key: 'repeats', width: 70 },
  { title: '平均分', key: 'score_avg', width: 90, render(row) { return row.score_avg.toFixed(2) } },
  { title: '通过率', key: 'pass_rate', width: 90, render(row) { return `${(row.pass_rate * 100).toFixed(0)}%` } },
  { title: '用例/尝试', key: 'counts', width: 110, render(row) { return `${row.total_cases}/${row.total_attempts}` } },
  { title: '创建时间', key: 'created_at', minWidth: 160, render(row) { return h(NText, { depth: 3 }, { default: () => fmtTime(row.created_at) }) } },
]

const resultColumns: DataTableColumns<EvalResult> = [
  { title: '用例', key: 'case_id', width: 80, render(row) { return `#${row.case_id}` } },
  { title: '尝试', key: 'attempt', width: 70 },
  {
    title: '评分器',
    key: 'grader',
    width: 80,
    render(row) {
      return h(NTag, { size: 'small', bordered: false }, { default: () => graderLabel(row.grader) })
    },
  },
  { title: '模型输出', key: 'output', minWidth: 220, ellipsis: { tooltip: true } },
  { title: '得分', key: 'score', width: 80, render(row) { return row.score.toFixed(2) } },
  {
    title: '通过',
    key: 'passed',
    width: 80,
    render(row) {
      return h(NTag, { size: 'small', type: row.passed ? 'success' : 'error', bordered: false }, { default: () => (row.passed ? '通过' : '未过') })
    },
  },
  { title: '延迟(ms)', key: 'latency_ms', width: 90 },
  { title: '错误', key: 'error', minWidth: 150, ellipsis: { tooltip: true }, render(row) { return h(NText, { depth: 3 }, { default: () => row.error || '-' }) } },
]

onMounted(loadDatasets)
</script>

<template>
  <n-card :bordered="false" class="h-full" content-style="display:flex;flex-direction:column;height:100%">
    <div class="flex items-center mb-3">
      <div>
        <h2 class="text-lg font-semibold m-0">评估回归</h2>
        <n-text depth="3" class="text-sm">建评估集 → 跑回归 → 出分数报告；模型 / Prompt 改动前后分数可对比</n-text>
      </div>
      <n-space class="ml-auto">
        <n-select
          v-model:value="selectedDatasetId"
          :options="datasetOptions"
          :loading="loadingDatasets"
          placeholder="选择评估集"
          style="width: 200px"
        />
        <n-button type="primary" @click="openCreateDataset">新建评估集</n-button>
      </n-space>
    </div>

    <div v-if="selectedDatasetId == null" class="flex-1 flex items-center justify-center">
      <n-empty description="请选择或新建一个评估集" />
    </div>

    <n-grid v-else :x-gap="16" :y-gap="16" cols="1 1100:2" responsive="screen" class="flex-1" item-responsive>
      <!-- 左：用例 -->
      <n-gi>
        <n-card title="用例" size="small" :bordered="true" class="h-full" content-style="display:flex;flex-direction:column">
          <template #header-extra>
            <n-button size="small" type="primary" @click="openCreateCase">+ 用例</n-button>
          </template>
          <n-data-table
            :columns="caseColumns"
            :data="cases"
            :loading="loadingCases"
            :scroll-x="620"
            :row-key="(row: EvalCase) => row.id"
            flex-height
            class="flex-1"
          />
        </n-card>
      </n-gi>

      <!-- 右：运行 + 结果 -->
      <n-gi>
        <n-space vertical :size="16" class="h-full">
          <n-card title="运行回归" size="small" :bordered="true">
            <n-space align="center" :wrap="true">
              <n-input v-model:value="runModel" placeholder="模型覆盖（留空用默认）" style="width: 180px" />
              <n-select v-model:value="runGrader" :options="graderOptions" placeholder="评分器覆盖" clearable style="width: 180px" />
              <n-input-number v-model:value="runRepeats" :min="1" :max="10" placeholder="重复次数" style="width: 120px" />
              <n-button type="primary" :loading="running" @click="handleRun">运行回归</n-button>
            </n-space>
          </n-card>

          <n-card title="运行历史" size="small" :bordered="true" content-style="display:flex;flex-direction:column">
            <n-data-table
              :columns="runColumns"
              :data="runs"
              :loading="loadingRuns"
              :scroll-x="900"
              :row-key="(row: EvalRun) => row.id"
              flex-height
              :max-height="220"
            />
          </n-card>

          <n-card :title="`结果${selectedRunId != null ? ' #' + selectedRunId : ''}`" size="small" :bordered="true" content-style="display:flex;flex-direction:column">
            <n-spin v-if="loadingResults" />
            <n-empty v-else-if="results.length === 0" description="运行后可查看逐条结果" />
            <n-data-table
              v-else
              :columns="resultColumns"
              :data="results"
              :scroll-x="900"
              :row-key="(row: EvalResult) => row.id"
              flex-height
              :max-height="260"
            />
          </n-card>
        </n-space>
      </n-gi>
    </n-grid>

    <!-- 评估集弹窗 -->
    <n-modal
      v-model:show="showDatasetModal"
      :title="datasetModalMode === 'create' ? '新建评估集' : '编辑评估集'"
      preset="card"
      style="width: 480px"
    >
      <n-form :model="datasetForm" label-placement="top">
        <n-form-item label="名称">
          <n-input v-model:value="datasetForm.name" placeholder="如 math-basics" />
        </n-form-item>
        <n-form-item label="描述">
          <n-input v-model:value="datasetForm.description" type="textarea" placeholder="评估集用途说明" />
        </n-form-item>
        <n-form-item label="默认评分器">
          <n-select v-model:value="datasetForm.default_grader" :options="graderOptions" />
        </n-form-item>
        <n-form-item label="默认模型">
          <n-input v-model:value="datasetForm.default_model" placeholder="留空则每次运行指定" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showDatasetModal = false">取消</n-button>
          <n-button type="primary" :loading="savingDataset" @click="saveDataset">保存</n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 用例弹窗 -->
    <n-modal
      v-model:show="showCaseModal"
      :title="caseModalMode === 'create' ? '新建用例' : '编辑用例'"
      preset="card"
      style="width: 520px"
    >
      <n-form :model="caseForm" label-placement="top">
        <n-form-item label="输入 / Prompt">
          <n-input v-model:value="caseForm.input" type="textarea" placeholder="发给模型的输入" />
        </n-form-item>
        <n-form-item label="期望输出">
          <n-input v-model:value="caseForm.expected" type="textarea" placeholder="用于评分的参考输出" />
        </n-form-item>
        <n-form-item label="评分器（留空用评估集默认）">
          <n-select v-model:value="caseForm.grader" :options="graderOptions" clearable />
        </n-form-item>
        <n-form-item label="模型覆盖（留空用默认）">
          <n-input v-model:value="caseForm.model" placeholder="指定模型 id" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showCaseModal = false">取消</n-button>
          <n-button type="primary" :loading="savingCase" @click="saveCase">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </n-card>
</template>
