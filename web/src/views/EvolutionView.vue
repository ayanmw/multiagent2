<script setup lang="ts">
import { h, ref, onMounted } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NDrawer,
  NDrawerContent,
  NTag,
  NSpace,
  NText,
  NSelect,
  NInput,
  NEmpty,
  useMessage,
  type DataTableColumns,
  type SelectOption,
} from 'naive-ui'
import {
  listSkillCandidates,
  scanSkillCandidates,
  resolveSkillCandidate,
  type SkillCandidate,
  type SkillCandidateStatus,
} from '@/api/evolution'
import { renderMarkdown } from '@/utils/markdown'

const message = useMessage()
const loading = ref(false)
const scanning = ref(false)
const candidates = ref<SkillCandidate[]>([])
const total = ref(0)

// 状态筛选：默认只看待审批（与飞轮「人工审批」主场景一致）。
const statusFilter = ref<string>('pending')
const statusOptions: SelectOption[] = [
  { label: '全部', value: '' },
  { label: '待审批', value: 'pending' },
  { label: '已批准', value: 'approved' },
  { label: '已拒绝', value: 'rejected' },
]

const pageSize = 200

async function load() {
  loading.value = true
  try {
    const res = await listSkillCandidates({
      status: statusFilter.value || undefined,
      limit: pageSize,
      offset: 0,
    })
    candidates.value = res.skill_candidates ?? []
    total.value = res.total ?? 0
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function handleScan() {
  scanning.value = true
  try {
    const res = await scanSkillCandidates()
    message.success(`扫描完成：评估 ${res.scanned} 个会话，新增 ${res.created} 条候选，跳过 ${res.skipped}，错误 ${res.errors}`)
    await load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    scanning.value = false
  }
}

function statusType(s: SkillCandidateStatus) {
  switch (s) {
    case 'pending':
      return 'warning'
    case 'approved':
      return 'success'
    case 'rejected':
      return 'error'
  }
}
function statusLabel(s: SkillCandidateStatus) {
  return s === 'pending' ? '待审批' : s === 'approved' ? '已批准' : '已拒绝'
}

// ---- 详情抽屉 ----
const showDetail = ref(false)
const detail = ref<SkillCandidate | null>(null)
const rejecting = ref(false)
const rejectReason = ref('')
const acting = ref(false)

function openDetail(row: SkillCandidate) {
  detail.value = row
  rejecting.value = false
  rejectReason.value = ''
  showDetail.value = true
}

async function handleApprove() {
  if (!detail.value) return
  acting.value = true
  try {
    const updated = await resolveSkillCandidate(detail.value.id, 'approve')
    message.success(`已批准并发布为托管技能「${updated.name}」，warm-start 将自动命中`)
    detail.value = updated
    await load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    acting.value = false
  }
}

async function handleReject() {
  if (!detail.value) return
  if (!rejectReason.value.trim()) {
    message.warning('请填写拒绝原因')
    return
  }
  acting.value = true
  try {
    const updated = await resolveSkillCandidate(detail.value.id, 'reject', rejectReason.value.trim())
    message.success('已拒绝该候选')
    detail.value = updated
    rejecting.value = false
    await load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    acting.value = false
  }
}

function fmtTime(s: string) {
  if (!s) return ''
  const d = new Date(s)
  return isNaN(d.getTime()) ? s : d.toLocaleString()
}

const columns: DataTableColumns<SkillCandidate> = [
  { title: '名称', key: 'name', minWidth: 150 },
  {
    title: '状态',
    key: 'status',
    width: 100,
    render(row) {
      return h(NTag, { type: statusType(row.status), size: 'small', bordered: false }, { default: () => statusLabel(row.status) })
    },
  },
  { title: '描述', key: 'description', minWidth: 220, ellipsis: { tooltip: true } },
  {
    title: '来源会话',
    key: 'source_session_key',
    width: 160,
    ellipsis: { tooltip: true },
    render(row) {
      return h(NText, { depth: 3 }, { default: () => row.source_session_key || '-' })
    },
  },
  {
    title: '质量建议',
    key: 'quality_notes',
    minWidth: 180,
    ellipsis: { tooltip: true },
    render(row) {
      return h(NText, { depth: 3 }, { default: () => row.quality_notes || '-' })
    },
  },
  {
    title: '创建时间',
    key: 'created_at',
    width: 170,
    render(row) {
      return h(NText, { depth: 3 }, { default: () => fmtTime(row.created_at) })
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 100,
    fixed: 'right',
    render(row) {
      return h(NButton, { size: 'small', tertiary: true, onClick: () => openDetail(row) }, { default: () => '查看' })
    },
  },
]
</script>

<template>
  <n-card :bordered="false" class="h-full" content-style="display:flex;flex-direction:column;height:100%">
    <div class="flex items-center mb-3">
      <div>
        <h2 class="text-lg font-semibold m-0">进化飞轮</h2>
        <n-text depth="3" class="text-sm">后台扫描会话 transcript 提取候选技能；人工审批后发布为托管技能进共享库，warm-start 自动复用</n-text>
      </div>
      <n-space class="ml-auto">
        <n-select
          v-model:value="statusFilter"
          :options="statusOptions"
          style="width: 120px"
          @update:value="load"
        />
        <n-button :loading="scanning" @click="handleScan">扫描提取</n-button>
      </n-space>
    </div>

    <n-data-table
      :columns="columns"
      :data="candidates"
      :loading="loading"
      :scroll-x="900"
      :row-key="(row: SkillCandidate) => row.id"
      flex-height
      class="flex-1"
    />
    <n-text v-if="total > 0" depth="3" class="text-xs mt-2">共 {{ total }} 条候选</n-text>

    <n-drawer v-model:show="showDetail" :width="560" placement="right">
      <n-drawer-content :title="`候选技能：${detail?.name ?? ''}`" closable>
        <n-empty v-if="!detail" description="无数据" />
        <n-space v-else vertical :size="10">
          <div>
            <n-text depth="3" class="text-xs">状态</n-text>
            <div class="mt-1">
              <n-tag :type="statusType(detail.status)" size="small" :bordered="false">{{ statusLabel(detail.status) }}</n-tag>
            </div>
          </div>
          <div>
            <n-text depth="3" class="text-xs">描述</n-text>
            <div class="mt-1">{{ detail.description || '-' }}</div>
          </div>
          <div>
            <n-text depth="3" class="text-xs">来源会话</n-text>
            <div class="mt-1 font-mono text-xs">{{ detail.source_session_key || '-' }}</div>
          </div>
          <div v-if="detail.quality_notes">
            <n-text depth="3" class="text-xs">质量建议</n-text>
            <div class="mt-1 text-sm">{{ detail.quality_notes }}</div>
          </div>
          <div v-if="detail.reject_reason">
            <n-text depth="3" class="text-xs">拒绝原因</n-text>
            <div class="mt-1 text-sm">{{ detail.reject_reason }}</div>
          </div>
          <div>
            <n-text depth="3" class="text-xs">创建时间</n-text>
            <div class="mt-1 text-xs">{{ fmtTime(detail.created_at) }}</div>
          </div>

          <div>
            <n-text depth="3" class="text-xs">SKILL.md 内容</n-text>
            <div
              class="mt-1 rounded border border-gray-200 dark:border-gray-700 p-3 text-sm overflow-auto max-h-[40vh] bg-gray-50 dark:bg-gray-900"
              v-html="renderMarkdown(detail.body)"
            />
          </div>
        </n-space>

        <template #footer>
          <n-space v-if="detail && detail.status === 'pending'" justify="end">
            <n-button v-if="!rejecting" tertiary type="error" :loading="acting" @click="rejecting = true">拒绝</n-button>
            <n-input
              v-else
              v-model:value="rejectReason"
              placeholder="填写拒绝原因"
              size="small"
              style="width: 180px"
            />
            <n-button v-if="rejecting" tertiary @click="rejecting = false">取消</n-button>
            <n-button v-if="rejecting" type="error" size="small" :loading="acting" @click="handleReject">确认拒绝</n-button>
            <n-button type="primary" :loading="acting" @click="handleApprove">批准发布</n-button>
          </n-space>
          <n-text v-else-if="detail" depth="3">该候选已处理（{{ statusLabel(detail.status) }}）</n-text>
        </template>
      </n-drawer-content>
    </n-drawer>
  </n-card>
</template>
