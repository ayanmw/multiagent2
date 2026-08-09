<script setup lang="ts">
import { h, ref, onMounted, reactive } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NModal,
  NForm,
  NFormItem,
  NInput,
  NSelect,
  NSwitch,
  NPopconfirm,
  NTag,
  NSpace,
  NText,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import {
  listMCPServers,
  createMCPServer,
  updateMCPServer,
  deleteMCPServer,
  type MCPServer,
  type MCPServerPayload,
  type MCPTransport,
} from '@/api/mcp'

const message = useMessage()
const loading = ref(false)
const servers = ref<MCPServer[]>([])

async function load() {
  loading.value = true
  try {
    servers.value = await listMCPServers()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loading.value = false
  }
}
onMounted(load)

const showModal = ref(false)
const editingId = ref<number | null>(null)
const submitting = ref(false)
const form = reactive({
  name: '',
  transport: 'stdio' as MCPTransport,
  command: '',
  argsText: '',
  url: '',
  envText: '',
  headersText: '',
  enabled: true,
  description: '',
})

function resetForm() {
  form.name = ''
  form.transport = 'stdio'
  form.command = ''
  form.argsText = ''
  form.url = ''
  form.envText = ''
  form.headersText = ''
  form.enabled = true
  form.description = ''
}
function openCreate() {
  editingId.value = null
  resetForm()
  showModal.value = true
}
function openEdit(s: MCPServer) {
  editingId.value = s.id
  form.name = s.name
  form.transport = s.transport
  form.command = s.command
  form.argsText = (s.args ?? []).join('\n')
  form.url = s.url
  form.envText = s.env ? JSON.stringify(s.env, null, 2) : ''
  form.headersText = s.headers ? JSON.stringify(s.headers, null, 2) : ''
  form.enabled = s.enabled
  form.description = s.description
  showModal.value = true
}

async function submit() {
  if (!form.name.trim()) {
    message.warning('请填写名称')
    return
  }
  const payload: MCPServerPayload = {
    name: form.name.trim(),
    transport: form.transport,
    enabled: form.enabled,
    description: form.description?.trim() ?? '',
  }
  if (form.command.trim()) payload.command = form.command.trim()
  const args = form.argsText.split('\n').map((s) => s.trim()).filter(Boolean)
  if (args.length) payload.args = args
  if (form.url.trim()) payload.url = form.url.trim()
  if (form.envText.trim()) {
    try {
      payload.env = JSON.parse(form.envText)
    } catch {
      message.error('env 不是合法 JSON')
      return
    }
  }
  if (form.headersText.trim()) {
    try {
      payload.headers = JSON.parse(form.headersText)
    } catch {
      message.error('headers 不是合法 JSON')
      return
    }
  }
  submitting.value = true
  try {
    if (editingId.value === null) {
      await createMCPServer(payload)
      message.success('MCP 服务器创建成功')
    } else {
      await updateMCPServer(editingId.value, payload)
      message.success('MCP 服务器更新成功')
    }
    showModal.value = false
    await load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    submitting.value = false
  }
}

async function handleDelete(s: MCPServer) {
  try {
    await deleteMCPServer(s.id)
    message.success('已删除')
    await load()
  } catch (e) {
    message.error((e as Error).message)
  }
}

const transportOptions = [
  { label: 'stdio（本地进程）', value: 'stdio' },
  { label: 'sse（Server-Sent Events）', value: 'sse' },
  { label: 'streamable（Streamable HTTP）', value: 'streamable' },
]

const columns: DataTableColumns<MCPServer> = [
  { title: '名称', key: 'name', minWidth: 120 },
  {
    title: '传输',
    key: 'transport',
    width: 120,
    render(row) {
      return h(NTag, { size: 'small', bordered: false }, { default: () => row.transport })
    },
  },
  {
    title: '启用',
    key: 'enabled',
    width: 90,
    render(row) {
      return h(
        NTag,
        { type: row.enabled ? 'success' : 'default', size: 'small', bordered: false },
        { default: () => (row.enabled ? '是' : '否') },
      )
    },
  },
  { title: '命令 / 地址', key: 'endpoint', minWidth: 180, ellipsis: { tooltip: true },
    render(row) {
      return h(NText, { depth: 3, class: 'font-mono text-xs' }, {
        default: () => row.transport === 'stdio' ? row.command : row.url,
      })
    },
  },
  { title: '描述', key: 'description', minWidth: 140, ellipsis: { tooltip: true } },
  {
    title: '操作',
    key: 'actions',
    width: 200,
    fixed: 'right',
    render(row) {
      return h(NSpace, { size: 4, wrap: false }, {
        default: () => [
          h(NButton, { size: 'small', tertiary: true, onClick: () => openEdit(row) }, { default: () => '编辑' }),
          h(
            NPopconfirm,
            { onPositiveClick: () => handleDelete(row) },
            {
              default: () => '确认删除该 MCP 服务器？',
              trigger: () =>
                h(NButton, { size: 'small', type: 'error', tertiary: true }, { default: () => '删除' }),
            },
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
        <h2 class="text-lg font-semibold m-0">MCP 服务器管理</h2>
        <n-text depth="3" class="text-sm">配置 MCP 服务器（仅管理面 + 校验，工具由对话按需装载）</n-text>
      </div>
      <n-button type="primary" class="ml-auto" @click="openCreate">新建 MCP</n-button>
    </div>

    <n-data-table
      :columns="columns"
      :data="servers"
      :loading="loading"
      :scroll-x="1000"
      :row-key="(row: MCPServer) => row.id"
      flex-height
      class="flex-1"
    />

    <n-modal
      v-model:show="showModal"
      :title="editingId === null ? '新建 MCP 服务器' : '编辑 MCP 服务器'"
      preset="card"
      style="width: 560px; max-width: 94vw"
    >
      <n-form :model="form" label-placement="top">
        <n-form-item label="名称" required>
          <n-input v-model:value="form.name" placeholder="例如：我的工具箱" />
        </n-form-item>
        <n-form-item label="传输方式">
          <n-select v-model:value="form.transport" :options="transportOptions" />
        </n-form-item>
        <template v-if="form.transport === 'stdio'">
          <n-form-item label="启动命令">
            <n-input v-model:value="form.command" placeholder="例如：npx" />
          </n-form-item>
          <n-form-item label="参数（每行一个）">
            <n-input v-model:value="form.argsText" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" placeholder="-y&#10;@some/mcp" />
          </n-form-item>
        </template>
        <template v-else>
          <n-form-item label="URL">
            <n-input v-model:value="form.url" placeholder="https://.../mcp" />
          </n-form-item>
        </template>
        <n-form-item label="环境变量 env（JSON，可选）">
          <n-input v-model:value="form.envText" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" placeholder='{"KEY":"value"}' />
        </n-form-item>
        <n-form-item label="请求头 headers（JSON，可选）">
          <n-input v-model:value="form.headersText" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" placeholder='{"Authorization":"Bearer ..."}' />
        </n-form-item>
        <n-form-item label="描述">
          <n-input v-model:value="form.description" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" />
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
  </n-card>
</template>
