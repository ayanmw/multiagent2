<script setup lang="ts">
import { h, ref, computed, onMounted } from 'vue'
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
  NSpace,
} from 'naive-ui/es/space'
import {
  NTag,
} from 'naive-ui/es/tag'
import {
  NModal,
} from 'naive-ui/es/modal'
import {
  NAlert,
} from 'naive-ui/es/alert'
import {
  NEmpty,
} from 'naive-ui/es/empty'
import {
  NSpin,
} from 'naive-ui/es/spin'
import {
  useMessage,
} from 'naive-ui/es/message'
import {
  listArtifacts,
  getArtifact,
  downloadArtifact,
  type ArtifactEntry,
  type ArtifactContent,
} from '@/api/artifact'
import { listSessions, type SessionView } from '@/api/session'

const message = useMessage()

// ---- 会话选择（owner 隔离：只能选自己的会话，后端再兜底一次） ----
const sessions = ref<SessionView[]>([])
const sessionOptions = computed<SelectOption[]>(() =>
  sessions.value.map((s) => ({
    label: s.title || s.session_key,
    value: s.session_key,
  })),
)
const selectedKey = ref<string>('')

// ---- artifact 列表 ----
const loading = ref(false)
const rows = ref<ArtifactEntry[]>([])
const enabled = ref(true) // 状态外置是否启用（后端返回）

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
}
function fmtTime(iso?: string): string {
  if (!iso) return '-'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

const columns: DataTableColumns<ArtifactEntry> = [
  {
    title: '文件名',
    key: 'name',
    minWidth: 220,
    fixed: 'left',
    render(row) {
      return h(
        'code',
        { class: 'font-mono text-xs' },
        row.name,
      )
    },
  },
  {
    title: '类型',
    key: 'is_state',
    width: 110,
    render(row) {
      return h(
        NTag,
        { type: row.is_state ? 'info' : 'default', size: 'small', bordered: false },
        { default: () => (row.is_state ? '工作状态' : '产物') },
      )
    },
  },
  {
    title: '大小',
    key: 'size',
    width: 120,
    render(row) {
      return h('span', { class: 'text-xs text-gray-500 dark:text-gray-400' }, fmtSize(row.size))
    },
  },
  {
    title: '修改时间',
    key: 'modified_at',
    width: 190,
    render(row) {
      return h('span', { class: 'text-xs text-gray-500 dark:text-gray-400' }, fmtTime(row.modified_at))
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 160,
    fixed: 'right',
    render(row) {
      return h(
        NSpace,
        { size: 6 },
        {
          default: () => [
            h(
              NButton,
              { size: 'small', tertiary: true, onClick: () => openView(row) },
              { default: () => '查看' },
            ),
            h(
              NButton,
              { size: 'small', tertiary: true, onClick: () => doDownload(row) },
              { default: () => '下载' },
            ),
          ],
        },
      )
    },
  },
]

async function loadSessions() {
  try {
    sessions.value = await listSessions()
    if (!selectedKey.value && sessions.value.length > 0) {
      selectedKey.value = sessions.value[0].session_key
      await loadArtifacts()
    }
  } catch (e) {
    message.error((e as Error).message)
  }
}

async function loadArtifacts() {
  if (!selectedKey.value) {
    rows.value = []
    return
  }
  loading.value = true
  try {
    const res = await listArtifacts(selectedKey.value)
    enabled.value = res.enabled
    rows.value = res.artifacts ?? []
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loading.value = false
  }
}

function onSessionChange() {
  loadArtifacts()
}

// ---- 查看弹窗 ----
const showView = ref(false)
const viewing = ref<ArtifactEntry | null>(null)
const content = ref<ArtifactContent | null>(null)
const viewLoading = ref(false)

async function openView(row: ArtifactEntry) {
  viewing.value = row
  content.value = null
  showView.value = true
  viewLoading.value = true
  try {
    content.value = await getArtifact(selectedKey.value, row.name)
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    viewLoading.value = false
  }
}

// 二进制产物在查看弹窗里直接提供下载入口。
async function doDownload(row: ArtifactEntry) {
  try {
    await downloadArtifact(selectedKey.value, row.name)
    message.success(`已开始下载 ${row.name}`)
  } catch (e) {
    message.error((e as Error).message)
  }
}

onMounted(loadSessions)
</script>

<template>
  <n-card :bordered="false" class="h-full" content-style="display:flex;flex-direction:column;height:100%">
    <div class="flex items-center mb-3">
      <div>
        <h2 class="text-lg font-semibold m-0">Artifact 浏览器</h2>
        <span class="text-sm text-gray-500 dark:text-gray-400">
          浏览某会话下的全部产物（M3-06）· 与「运行状态面板」互补，这里看全部 artifact
        </span>
      </div>
      <n-button class="ml-auto" :loading="loading" :disabled="!selectedKey" @click="loadArtifacts">
        刷新
      </n-button>
    </div>

    <div class="flex items-center gap-3 mb-3">
      <span class="text-sm text-gray-500 dark:text-gray-400">会话</span>
      <n-select
        v-model:value="selectedKey"
        :options="sessionOptions"
        placeholder="选择要浏览的会话"
        style="width: 360px"
        @update:value="onSessionChange"
      />
    </div>

    <n-alert v-if="selectedKey && !enabled" type="warning" :bordered="false" class="mb-3">
      该会话未启用「工作状态外置」（STATE_ENABLED=false），暂无 artifact 产物。
    </n-alert>

    <n-data-table
      :columns="columns"
      :data="rows"
      :loading="loading"
      :row-key="(row: ArtifactEntry) => row.name"
      :scroll-x="800"
      flex-height
      class="flex-1"
    >
      <template #empty>
        <n-empty description="该会话暂无产物" />
      </template>
    </n-data-table>

    <!-- 查看弹窗 -->
    <n-modal v-model:show="showView" preset="card" style="width: 820px; max-width: 94vw" :title="viewing?.name">
      <n-empty v-if="!viewing" description="无数据" />
      <template v-else>
        <n-spin :show="viewLoading">
          <div v-if="content" class="space-y-2">
            <n-alert
              v-if="content.binary"
              type="info"
              :bordered="false"
              class="mb-2"
            >
              该产物为二进制文件，无法在浏览器内预览，请点击「下载」获取原始内容。
              <n-button size="small" tertiary class="mt-2" @click="doDownload(viewing)">
                下载 {{ viewing.name }}
              </n-button>
            </n-alert>
            <n-alert
              v-else-if="content.truncated"
              type="warning"
              :bordered="false"
              class="mb-2"
            >
              内容超过 256 KiB 已截断显示，完整内容请点「下载」。
              <n-button size="small" tertiary class="mt-2" @click="doDownload(viewing)">
                下载 {{ viewing.name }}
              </n-button>
            </n-alert>
            <n-tag v-if="content.is_state" size="small" type="info" :bordered="false" class="mb-2">
              工作状态文件（PLAN / PROGRESS / LEARNINGS）
            </n-tag>
            <pre
              v-if="!content.binary"
              class="font-mono text-xs whitespace-pre-wrap break-all m-0 p-3 rounded bg-gray-50 dark:bg-gray-800 max-h-[60vh] overflow-auto"
            >{{ content.content || '(空文件)' }}</pre>
          </div>
        </n-spin>
      </template>
    </n-modal>
  </n-card>
</template>
