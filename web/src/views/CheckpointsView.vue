<script setup lang="ts">
import { h, ref, reactive, computed, onMounted } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NSelect,
  NSpace,
  NTag,
  NModal,
  NInput,
  NForm,
  NGrid,
  NEmpty,
  NAlert,
  useMessage,
  useDialog,
  type DataTableColumns,
  type SelectOption,
} from 'naive-ui'
import {
  listCheckpoints,
  resolveCheckpoint,
  checkpointDisplayID,
  type Checkpoint,
  type CheckpointStatusFilter,
} from '@/api/checkpoint'
import { useAuthStore } from '@/stores/auth'

const message = useMessage()
const dialog = useDialog()
const auth = useAuthStore()

// admin/developer 可见/可处置全员检查点；viewer 仅本人（后端强制，此处仅做提示）。
const canSeeAll = computed(() => ['admin', 'developer'].includes(auth.user?.role ?? ''))
// 审批需 checkpoints:write：viewer 只读（后端 RBAC 会拒绝，前端提前置灰避免误操作）。
const canResolve = computed(() => ['admin', 'developer'].includes(auth.user?.role ?? ''))

const filters = reactive({
  status: 'pending' as CheckpointStatusFilter, // 默认只看待审批，符合「审批台」语义
})

const pagination = reactive({
  page: 1,
  pageSize: 20,
  itemCount: 0,
  showSizePicker: true,
  pageSizes: [20, 50, 100],
})

const loading = ref(false)
const rows = ref<Checkpoint[]>([])
const lastScope = ref<'' | 'all' | 'self'>('')
const pendingCount = ref(0)

function fmtTime(iso?: string): string {
  if (!iso) return '-'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

function statusType(s: string): 'warning' | 'success' | 'error' | 'default' {
  switch (s) {
    case 'pending':
      return 'warning'
    case 'approved':
      return 'success'
    case 'rejected':
      return 'error'
    default:
      return 'default'
  }
}

function statusLabel(s: string): string {
  switch (s) {
    case 'pending':
      return '待审批'
    case 'approved':
      return '已批准'
    case 'rejected':
      return '已拒绝'
    default:
      return s || '-'
  }
}

const statusOptions: SelectOption[] = [
  { label: '待审批 (pending)', value: 'pending' },
  { label: '已批准 (approved)', value: 'approved' },
  { label: '已拒绝 (rejected)', value: 'rejected' },
  { label: '全部状态', value: 'all' },
]

const columns: DataTableColumns<Checkpoint> = [
  {
    title: '编号',
    key: 'ID',
    width: 92,
    fixed: 'left',
    render(row) {
      return h('span', { class: 'font-mono text-xs' }, checkpointDisplayID(row))
    },
  },
  { title: '用户', key: 'user_id', width: 76 },
  {
    title: '命令',
    key: 'command',
    minWidth: 240,
    ellipsis: { tooltip: true },
    render(row) {
      return h('code', { class: 'font-mono text-xs' }, row.command || '-')
    },
  },
  { title: '工作目录', key: 'workdir', width: 180, ellipsis: { tooltip: true } },
  { title: '命中原因', key: 'reason', minWidth: 160, ellipsis: { tooltip: true } },
  {
    title: '状态',
    key: 'status',
    width: 100,
    render(row) {
      return h(
        NTag,
        { type: statusType(row.status), size: 'small', bordered: false },
        { default: () => statusLabel(row.status) },
      )
    },
  },
  {
    title: '创建时间',
    key: 'CreatedAt',
    width: 180,
    render(row) {
      return h('span', { class: 'text-xs text-gray-500 dark:text-gray-400' }, fmtTime(row.CreatedAt))
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 210,
    fixed: 'right',
    render(row) {
      const detailBtn = h(
        NButton,
        { size: 'small', tertiary: true, onClick: () => openDetail(row) },
        { default: () => '详情' },
      )
      if (row.status !== 'pending') {
        return h(NSpace, { size: 6 }, { default: () => [detailBtn] })
      }
      return h(
        NSpace,
        { size: 6 },
        {
          default: () => [
            detailBtn,
            h(
              NButton,
              {
                size: 'small',
                type: 'primary',
                disabled: !canResolve.value || resolving.value,
                onClick: () => openResolve(row, 'approve'),
              },
              { default: () => '批准' },
            ),
            h(
              NButton,
              {
                size: 'small',
                type: 'error',
                ghost: true,
                disabled: !canResolve.value || resolving.value,
                onClick: () => openResolve(row, 'reject'),
              },
              { default: () => '拒绝' },
            ),
          ],
        },
      )
    },
  },
]

async function load() {
  loading.value = true
  try {
    const res = await listCheckpoints({
      status: filters.status,
      limit: pagination.pageSize,
      offset: (pagination.page - 1) * pagination.pageSize,
    })
    rows.value = res.checkpoints ?? []
    pagination.itemCount = res.total ?? 0
    lastScope.value = res.scope ?? ''
    if (filters.status === 'pending') {
      pendingCount.value = res.total ?? 0
    }
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
function onStatusChange() {
  pagination.page = 1
  load()
}

// ---- 审批弹窗 ----
const showResolve = ref(false)
const resolving = ref(false)
const resolveAction = ref<'approve' | 'reject'>('approve')
const resolveRow = ref<Checkpoint | null>(null)
const resolveComment = ref('')

function openResolve(row: Checkpoint, action: 'approve' | 'reject') {
  resolveRow.value = row
  resolveAction.value = action
  resolveComment.value = ''
  showResolve.value = true
}

async function submitResolve() {
  const row = resolveRow.value
  if (!row) return
  resolving.value = true
  try {
    const res = await resolveCheckpoint(row.ID, resolveAction.value, resolveComment.value.trim())
    showResolve.value = false
    if (resolveAction.value === 'approve') {
      message.success(`${res.display_id} 已批准并执行`)
      // 批准会真正执行命令，把结果直接摊开给人看（失败与否一目了然）。
      if (res.result) {
        dialog.info({
          title: `${res.display_id} 执行结果`,
          content: () => h('pre', { class: 'font-mono text-xs whitespace-pre-wrap break-all m-0' }, res.result ?? ''),
          positiveText: '知道了',
          style: 'width: 720px; max-width: 94vw',
        })
      }
    } else {
      message.success(`${res.display_id} 已拒绝，命令不会执行`)
    }
    load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    resolving.value = false
  }
}

// ---- 详情弹窗 ----
const showDetail = ref(false)
const detailRow = ref<Checkpoint | null>(null)
function openDetail(row: Checkpoint) {
  detailRow.value = row
  showDetail.value = true
}

onMounted(load)
</script>

<template>
  <n-card :bordered="false" class="h-full" content-style="display:flex;flex-direction:column;height:100%">
    <div class="flex items-center mb-3">
      <div>
        <h2 class="text-lg font-semibold m-0">
          人工检查点
          <n-tag v-if="pendingCount > 0" type="warning" size="small" :bordered="false" class="ml-2">
            待审批 {{ pendingCount }}
          </n-tag>
        </h2>
        <span class="text-sm text-gray-500 dark:text-gray-400">
          无人值守下的危险命令审批（M3-05）·
          <template v-if="lastScope === 'self'">仅显示本人记录</template>
          <template v-else-if="lastScope === 'all'">显示全员记录</template>
        </span>
      </div>
      <n-button class="ml-auto" :loading="loading" @click="load">刷新</n-button>
    </div>

    <n-alert v-if="!canResolve" type="info" :bordered="false" class="mb-3">
      当前角色只读：审批需要 <code>checkpoints:write</code> 权限（admin / developer）。
    </n-alert>

    <n-form inline label-placement="left" :show-feedback="false" class="mb-3">
      <n-grid :cols="24" :x-gap="12" responsive="screen" item-responsive>
        <n-form-item-gi :span="8" label="状态">
          <n-select
            v-model:value="filters.status"
            :options="statusOptions"
            style="width: 100%"
            @update:value="onStatusChange"
          />
        </n-form-item-gi>
        <n-form-item-gi :span="16">
          <span class="text-sm text-gray-500 dark:text-gray-400">
            待审批命令在 Agent 侧处于暂停状态：批准后由后端在原工作目录执行并回填结果，拒绝则永不执行。
            <template v-if="canSeeAll">（当前角色可处置全员检查点）</template>
          </span>
        </n-form-item-gi>
      </n-grid>
    </n-form>

    <n-data-table
      :columns="columns"
      :data="rows"
      :loading="loading"
      :scroll-x="1300"
      :row-key="(row: Checkpoint) => row.ID"
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

    <!-- 审批确认弹窗 -->
    <n-modal
      v-model:show="showResolve"
      :title="resolveAction === 'approve' ? '批准并执行' : '拒绝该命令'"
      preset="card"
      style="width: 640px; max-width: 94vw"
    >
      <template v-if="resolveRow">
        <n-alert :type="resolveAction === 'approve' ? 'warning' : 'info'" :bordered="false" class="mb-3">
          <template v-if="resolveAction === 'approve'">
            批准后将在 <code class="break-all">{{ resolveRow.workdir || '(默认工作目录)' }}</code>
            实际执行该命令（绕过危险命令策略），请确认命令内容无误。
          </template>
          <template v-else>拒绝后该命令不会被执行，Agent 侧任务将按中止处理。</template>
        </n-alert>
        <div class="mb-2 text-xs text-gray-500 dark:text-gray-400">命令</div>
        <code class="font-mono text-xs break-all block mb-3">{{ resolveRow.command }}</code>
        <div class="mb-2 text-xs text-gray-500 dark:text-gray-400">命中原因</div>
        <div class="text-sm mb-3">{{ resolveRow.reason || '-' }}</div>
        <n-input
          v-model:value="resolveComment"
          type="textarea"
          :rows="3"
          placeholder="审批意见（可选，会写入检查点记录）"
        />
      </template>
      <template #footer>
        <n-space justify="end">
          <n-button :disabled="resolving" @click="showResolve = false">取消</n-button>
          <n-button
            :type="resolveAction === 'approve' ? 'primary' : 'error'"
            :loading="resolving"
            @click="submitResolve"
          >
            {{ resolveAction === 'approve' ? '确认批准并执行' : '确认拒绝' }}
          </n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 详情弹窗 -->
    <n-modal v-model:show="showDetail" title="检查点详情" preset="card" style="width: 760px; max-width: 94vw">
      <n-empty v-if="!detailRow" description="无数据" />
      <template v-else>
        <n-descriptions :column="1" bordered size="small" label-placement="left">
          <n-descriptions-item label="编号">{{ checkpointDisplayID(detailRow) }}</n-descriptions-item>
          <n-descriptions-item label="状态">
            <n-tag :type="statusType(detailRow.status)" size="small" :bordered="false">
              {{ statusLabel(detailRow.status) }}
            </n-tag>
          </n-descriptions-item>
          <n-descriptions-item label="用户 ID">{{ detailRow.user_id }}</n-descriptions-item>
          <n-descriptions-item label="会话">
            <code class="font-mono text-xs break-all">{{ detailRow.session_id || '-' }}</code>
          </n-descriptions-item>
          <n-descriptions-item label="命令">
            <code class="font-mono text-xs break-all">{{ detailRow.command }}</code>
          </n-descriptions-item>
          <n-descriptions-item label="工作目录">
            <code class="font-mono text-xs break-all">{{ detailRow.workdir || '-' }}</code>
          </n-descriptions-item>
          <n-descriptions-item label="命中原因">{{ detailRow.reason || '-' }}</n-descriptions-item>
          <n-descriptions-item label="上下文">{{ detailRow.context || '-' }}</n-descriptions-item>
          <n-descriptions-item label="审批人">{{ detailRow.resolved_by || '-' }}</n-descriptions-item>
          <n-descriptions-item label="审批意见">{{ detailRow.comment || '-' }}</n-descriptions-item>
          <n-descriptions-item label="执行结果">
            <pre class="font-mono text-xs whitespace-pre-wrap break-all m-0">{{ detailRow.result || '-' }}</pre>
          </n-descriptions-item>
          <n-descriptions-item label="创建时间">{{ fmtTime(detailRow.CreatedAt) }}</n-descriptions-item>
          <n-descriptions-item label="更新时间">{{ fmtTime(detailRow.UpdatedAt) }}</n-descriptions-item>
        </n-descriptions>
      </template>
    </n-modal>
  </n-card>
</template>
