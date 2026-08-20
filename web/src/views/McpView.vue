<script setup lang="ts">
import { h, ref, onMounted, reactive } from 'vue'
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
  NForm,
  NFormItem,
} from 'naive-ui/es/form'
import {
  NInput,
} from 'naive-ui/es/input'
import {
  NSelect,
} from 'naive-ui/es/select'
import {
  NSwitch,
} from 'naive-ui/es/switch'
import {
  NPopconfirm,
} from 'naive-ui/es/popconfirm'
import {
  NTag,
} from 'naive-ui/es/tag'
import {
  NSpace,
} from 'naive-ui/es/space'
import {
  NText,
} from 'naive-ui/es/typography'
import {
  NList,
  NListItem,
} from 'naive-ui/es/list'
import {
  NDrawer,
  NDrawerContent,
} from 'naive-ui/es/drawer'
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
  listMCPServers,
  createMCPServer,
  updateMCPServer,
  deleteMCPServer,
  testMCPServer,
  listMCPTemplates,
  importMCPTemplate,
  type MCPServer,
  type MCPServerPayload,
  type MCPTransport,
  type MCPTestResult,
  type MCPTemplate,
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
// 编辑态下已配置的密钥键名（仅键名，值永不下发），用于表单提示。
const editingEnvKeys = ref<string[]>([])
const editingHeaderKeys = ref<string[]>([])

function secretHint(keys: string[], has: boolean) {
  if (!has) return '当前未配置'
  return `已配置 ${keys.length} 项（${keys.join(', ')}），值已加密不回显；留空不修改，填 {} 清空`
}

function openCreate() {
  editingId.value = null
  resetForm()
  editingEnvKeys.value = []
  editingHeaderKeys.value = []
  showModal.value = true
}
function openEdit(s: MCPServer) {
  editingId.value = s.id
  form.name = s.name
  form.transport = s.transport
  form.command = s.command
  form.argsText = (s.args ?? []).join('\n')
  form.url = s.url
  // M3-07：env / headers 明文不再下发，编辑时一律留空（留空 = 不修改）。
  form.envText = ''
  form.headersText = ''
  editingEnvKeys.value = s.env_keys ?? []
  editingHeaderKeys.value = s.header_keys ?? []
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
  // M3-07：env / headers 留空即不提交 → 后端保持原密文；填 {} 才是清空。
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

// MX-02：测试连接/装载校验——实际调后端 toolsearch 连接并预取工具列表。
const testingId = ref<number | null>(null)
const showTestResult = ref(false)
const testTargetName = ref('')
const testResult = ref<MCPTestResult | null>(null)

async function handleTest(s: MCPServer) {
  testingId.value = s.id
  testTargetName.value = s.name
  try {
    const res = await testMCPServer(s.id)
    testResult.value = res
    showTestResult.value = true
    if (res.ok) {
      message.success(`连接成功，发现 ${res.count} 个工具`)
    } else {
      message.warning('连接失败，请检查配置')
    }
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    testingId.value = null
  }
}

// ---------- 连接器市场（M8-08）：预置 MCP 模板 + 一键导入 ----------
const showMarket = ref(false)
const templates = ref<MCPTemplate[]>([])
const loadingTemplates = ref(false)

async function openMarket() {
  showMarket.value = true
  if (templates.value.length) return
  loadingTemplates.value = true
  try {
    templates.value = await listMCPTemplates()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loadingTemplates.value = false
  }
}

// 导入弹窗状态
const showImport = ref(false)
const importingTemplate = ref<MCPTemplate | null>(null)
const importing = ref(false)
const importForm = reactive({
  name: '',
  enabled: true,
  secrets: {} as Record<string, string>,
})

function openImport(tmpl: MCPTemplate) {
  importingTemplate.value = tmpl
  importForm.name = tmpl.default_name
  importForm.enabled = tmpl.default_enabled
  importForm.secrets = {}
  showImport.value = true
}

// 密钥字段名转可读标签（GITHUB_TOKEN → GITHUB TOKEN 的展示优化）。
function secretLabel(field: string) {
  return field.replace(/_/g, ' ')
}

function categoryTagType(cat: string) {
  switch (cat) {
    case '代码托管':
      return 'info'
    case '团队协作':
      return 'warning'
    case '数据与存储':
      return 'error'
    default:
      return 'success'
  }
}

async function submitImport() {
  const tmpl = importingTemplate.value
  if (!tmpl) return
  if (!importForm.name.trim()) {
    message.warning('请填写配置名称')
    return
  }
  importing.value = true
  try {
    // 密钥值统一走 env 提交（后端 env ∪ headers 合并查找占位符，M8-08）。
    await importMCPTemplate(tmpl.id, {
      name: importForm.name.trim(),
      enabled: importForm.enabled,
      env: importForm.secrets,
    })
    message.success(`已导入「${importForm.name.trim()}」，可在列表中测试连接`)
    showImport.value = false
    await load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    importing.value = false
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
  {
    title: '密钥',
    key: 'secrets',
    width: 150,
    render(row) {
      const parts: string[] = []
      if (row.has_env) parts.push(`env ×${(row.env_keys ?? []).length}`)
      if (row.has_headers) parts.push(`headers ×${(row.header_keys ?? []).length}`)
      if (!parts.length) return h(NText, { depth: 3 }, { default: () => '—' })
      return h(NSpace, { size: 4, wrap: false }, {
        default: () =>
          parts.map((p) =>
            h(NTag, { size: 'small', bordered: false, type: 'warning' }, { default: () => p }),
          ),
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
          h(
            NButton,
            {
              size: 'small',
              tertiary: true,
              loading: testingId.value === row.id,
              disabled: testingId.value !== null,
              onClick: () => handleTest(row),
            },
            { default: () => '测试连接' },
          ),
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
        <n-text depth="3" class="text-sm">
          配置 MCP 服务器（仅管理面 + 校验，工具由对话按需装载）；env / headers 加密存储，不回显明文
        </n-text>
      </div>
      <n-space class="ml-auto" :size="8">
        <n-button secondary type="primary" @click="openMarket">连接器市场</n-button>
        <n-button type="primary" @click="openCreate">新建 MCP</n-button>
      </n-space>
    </div>

    <n-data-table
      :columns="columns"
      :data="servers"
      :loading="loading"
      :scroll-x="1150"
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
          <n-space vertical :size="4" class="w-full">
            <n-input v-model:value="form.envText" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" placeholder='{"KEY":"value"}' />
            <n-text v-if="editingId !== null" depth="3" class="text-xs">
              {{ secretHint(editingEnvKeys, editingEnvKeys.length > 0) }}
            </n-text>
          </n-space>
        </n-form-item>
        <n-form-item label="请求头 headers（JSON，可选）">
          <n-space vertical :size="4" class="w-full">
            <n-input v-model:value="form.headersText" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" placeholder='{"Authorization":"Bearer ..."}' />
            <n-text v-if="editingId !== null" depth="3" class="text-xs">
              {{ secretHint(editingHeaderKeys, editingHeaderKeys.length > 0) }}
            </n-text>
          </n-space>
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

    <!-- MX-02：测试连接结果 -->
    <n-modal
      v-model:show="showTestResult"
      :title="`测试连接 · ${testTargetName}`"
      preset="card"
      style="width: 560px; max-width: 94vw"
    >
      <template v-if="testResult">
        <n-alert
          v-if="testResult.ok"
          type="success"
          :title="`连接成功，发现 ${testResult.count} 个工具`"
          class="mb-3"
        />
        <n-alert v-else type="error" :title="'连接失败'" class="mb-3">
          {{ testResult.error }}
        </n-alert>
        <template v-if="testResult.ok">
          <n-text depth="3" class="text-sm">实际装载的工具（命名空间 + 描述）：</n-text>
          <n-list class="mt-2" v-if="testResult.tools.length">
            <n-list-item v-for="t in testResult.tools" :key="t.name">
              <div class="font-mono text-xs">{{ t.name }}</div>
              <n-text depth="3" class="text-xs">{{ t.description || '（无描述）' }}</n-text>
            </n-list-item>
          </n-list>
          <n-text v-else depth="3" class="text-sm">该服务器连接成功，但未暴露任何工具。</n-text>
        </template>
      </template>
    </n-modal>

    <!-- M8-08：连接器市场（预置 MCP 模板，一键导入） -->
    <n-drawer v-model:show="showMarket" :width="720">
      <n-drawer-content title="连接器市场" closable>
        <template #header-extra>
          <n-text depth="3" class="text-xs">
            预置常用 MCP 模板，一键导入为你的配置；密钥加密落库，不回调明文
          </n-text>
        </template>
        <n-spin :show="loadingTemplates">
          <n-empty
            v-if="!loadingTemplates && !templates.length"
            description="暂无可用模板"
            class="py-12"
          />
          <n-space vertical :size="12">
            <n-card v-for="tmpl in templates" :key="tmpl.id" size="small">
              <div class="flex items-center gap-2 mb-1">
                <n-tag size="small" :type="categoryTagType(tmpl.category)" :bordered="false">
                  {{ tmpl.category }}
                </n-tag>
                <span class="font-semibold">{{ tmpl.name }}</span>
                <n-tag size="small" :bordered="false" class="font-mono">{{ tmpl.transport }}</n-tag>
                <n-button size="small" type="primary" class="ml-auto" @click="openImport(tmpl)">
                  一键导入
                </n-button>
              </div>
              <n-text depth="3" class="text-sm block">{{ tmpl.description }}</n-text>
              <div class="mt-2 flex items-center gap-1 flex-wrap">
                <template v-if="tmpl.secret_fields.length">
                  <n-tag
                    v-for="f in tmpl.secret_fields"
                    :key="f"
                    size="small"
                    type="warning"
                    :bordered="false"
                  >
                    需提供 {{ secretLabel(f) }}
                  </n-tag>
                </template>
                <n-text v-else depth="3" class="text-xs">无需密钥，导入即用</n-text>
              </div>
            </n-card>
          </n-space>
        </n-spin>
      </n-drawer-content>
    </n-drawer>

    <!-- M8-08：模板导入表单 -->
    <n-modal
      v-model:show="showImport"
      :title="`导入连接器 · ${importingTemplate?.name ?? ''}`"
      preset="card"
      style="width: 480px; max-width: 94vw"
    >
      <n-form label-placement="top">
        <n-form-item label="配置名称" required>
          <n-input v-model:value="importForm.name" placeholder="导入后的配置名称" />
        </n-form-item>
        <template v-for="f in importingTemplate?.secret_fields ?? []" :key="f">
          <n-form-item :label="secretLabel(f)" required>
            <n-input
              v-model:value="importForm.secrets[f]"
              type="password"
              show-password-on="click"
              :placeholder="`请输入 ${secretLabel(f)}（加密存储）`"
            />
          </n-form-item>
        </template>
        <n-form-item label="启用">
          <n-switch v-model:value="importForm.enabled" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showImport = false">取消</n-button>
          <n-button type="primary" :loading="importing" @click="submitImport">导入</n-button>
        </n-space>
      </template>
    </n-modal>
  </n-card>
</template>
