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
  NScrollbar,
} from 'naive-ui/es/scrollbar'
import {
  NEmpty,
} from 'naive-ui/es/empty'
import {
  useMessage,
} from 'naive-ui/es/message'
import {
  listProviders,
  createProvider,
  updateProvider,
  deleteProvider,
  fetchProviderModels,
  type Provider,
  type ProviderPayload,
  type Protocol,
  type ProviderModel,
} from '@/api/provider'

const message = useMessage()

const loading = ref(false)
const providers = ref<Provider[]>([])

async function load() {
  loading.value = true
  try {
    providers.value = await listProviders()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loading.value = false
  }
}

onMounted(load)

// ---- 新建 / 编辑对话框 ----
const showModal = ref(false)
const editingId = ref<number | null>(null)
const submitting = ref(false)
const form = reactive<ProviderPayload>({
  name: '',
  protocol: 'openai',
  base_url: '',
  api_key: '',
  description: '',
})

function openCreate() {
  editingId.value = null
  form.name = ''
  form.protocol = 'openai'
  form.base_url = ''
  form.api_key = ''
  form.description = ''
  showModal.value = true
}

function openEdit(p: Provider) {
  editingId.value = p.id
  form.name = p.name
  form.protocol = p.protocol
  form.base_url = p.base_url
  form.api_key = '' // 编辑时留空表示不修改密钥
  form.description = p.description
  showModal.value = true
}

const protocolOptions = [
  { label: 'OpenAI (兼容)', value: 'openai' },
  { label: 'Anthropic', value: 'anthropic' },
  { label: 'Gemini', value: 'gemini' },
]

async function submit() {
  if (!form.name.trim()) {
    message.warning('请填写名称')
    return
  }
  submitting.value = true
  try {
    const payload: ProviderPayload = {
      name: form.name.trim(),
      protocol: form.protocol as Protocol,
      base_url: form.base_url?.trim() ?? '',
      description: form.description?.trim() ?? '',
    }
    // 仅当用户填写了 api_key 才上传，避免把空串覆盖掉已有密钥。
    const apiKey = form.api_key?.trim()
    if (apiKey) payload.api_key = apiKey

    if (editingId.value === null) {
      await createProvider(payload)
      message.success('Provider 创建成功')
    } else {
      await updateProvider(editingId.value, payload)
      message.success('Provider 更新成功')
    }
    showModal.value = false
    await load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    submitting.value = false
  }
}

async function handleDelete(p: Provider) {
  try {
    await deleteProvider(p.id)
    message.success('已删除')
    await load()
  } catch (e) {
    message.error((e as Error).message)
  }
}

// ---- 测试连接 / 模型列表（复用模型发现端点，成功即代表连接可达）----
const showModels = ref(false)
const modelsProviderName = ref('')
const modelsList = ref<ProviderModel[]>([])
const modelsLoading = ref(false)
const modelsError = ref('')
const modelsCached = ref(false)

// 模型表格列
const modelColumns: DataTableColumns<ProviderModel> = [
  { title: '模型 ID', key: 'id', minWidth: 200, ellipsis: { tooltip: true } },
  { title: '归属', key: 'owned_by', width: 160, render(row) {
    return h(NText, { depth: 3 }, { default: () => row.owned_by ?? '-' })
  } },
]

async function handleTest(p: Provider) {
  modelsLoading.value = true
  modelsError.value = ''
  modelsList.value = []
  showModels.value = true
  modelsProviderName.value = p.name
  try {
    const data = await fetchProviderModels(p.id)
    modelsList.value = data.models ?? []
    modelsCached.value = data.cached
    if (modelsList.value.length === 0) {
      message.warning('连接成功，但未发现模型')
    } else {
      message.success(`连接成功，发现 ${modelsList.value.length} 个模型`)
    }
  } catch (e) {
    modelsError.value = (e as Error).message
    message.error('连接失败：' + (e as Error).message)
  } finally {
    modelsLoading.value = false
  }
}

const columns: DataTableColumns<Provider> = [
  { title: '名称', key: 'name', minWidth: 120 },
  {
    title: '协议',
    key: 'protocol',
    width: 120,
    render(row) {
      const type =
        row.protocol === 'openai' ? 'info' : row.protocol === 'anthropic' ? 'warning' : 'success'
      return h(NTag, { type, size: 'small', bordered: false }, { default: () => row.protocol })
    },
  },
  { title: '地址', key: 'base_url', minWidth: 160, ellipsis: { tooltip: true } },
  {
    title: 'API Key',
    key: 'has_api_key',
    width: 100,
    render(row) {
      return h(
        NTag,
        { type: row.has_api_key ? 'success' : 'default', size: 'small', bordered: false },
        { default: () => (row.has_api_key ? '已配置' : '未配置') },
      )
    },
  },
  { title: '描述', key: 'description', minWidth: 140, ellipsis: { tooltip: true } },
  {
    title: '操作',
    key: 'actions',
    width: 260,
    fixed: 'right',
    render(row) {
      return h(NSpace, { size: 4, wrap: false }, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => handleTest(row) }, { default: () => '测试连接' }),
          h(
            NButton,
            { size: 'small', tertiary: true, onClick: () => openEdit(row) },
            { default: () => '编辑' },
          ),
          h(
            NPopconfirm,
            { onPositiveClick: () => handleDelete(row) },
            {
              default: () => '确认删除该 Provider？此操作不可撤销。',
              trigger: () =>
                h(
                  NButton,
                  { size: 'small', type: 'error', tertiary: true },
                  { default: () => '删除' },
                ),
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
        <h2 class="text-lg font-semibold m-0">Provider 管理</h2>
        <n-text depth="3" class="text-sm">管理你的 LLM 接入配置（BYOK，按用户隔离）</n-text>
      </div>
      <n-button type="primary" class="ml-auto" @click="openCreate">新建 Provider</n-button>
    </div>

    <n-data-table
      :columns="columns"
      :data="providers"
      :loading="loading"
      :scroll-x="920"
      :row-key="(row: Provider) => row.id"
      flex-height
      class="flex-1"
    />

    <!-- 新建 / 编辑对话框 -->
    <n-modal
      v-model:show="showModal"
      :title="editingId === null ? '新建 Provider' : '编辑 Provider'"
      preset="card"
      style="width: 520px; max-width: 92vw"
    >
      <n-form :model="form" label-placement="top">
        <n-form-item label="名称" required>
          <n-input v-model:value="form.name" placeholder="例如：我的 OpenAI" />
        </n-form-item>
        <n-form-item label="协议">
          <n-select v-model:value="form.protocol" :options="protocolOptions" />
        </n-form-item>
        <n-form-item label="Base URL">
          <n-input
            v-model:value="form.base_url"
            placeholder="OpenAI 兼容端点需含 /v1，如 http://localhost:8080/v1"
          />
        </n-form-item>
        <n-form-item :label="editingId === null ? 'API Key' : 'API Key（留空则不修改）'">
          <n-input
            v-model:value="form.api_key"
            type="password"
            show-password-on="click"
            placeholder="明文仅在创建/更新时传入，不回显"
          />
        </n-form-item>
        <n-form-item label="描述">
          <n-input
            v-model:value="form.description"
            type="textarea"
            :autosize="{ minRows: 2, maxRows: 4 }"
            placeholder="可选"
          />
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

    <!-- 测试连接结果 / 模型列表 -->
    <n-modal
      v-model:show="showModels"
      :title="`测试连接 — ${modelsProviderName}`"
      preset="card"
      style="width: 640px; max-width: 94vw"
    >
      <n-empty v-if="modelsError" :description="`连接失败：${modelsError}`" />
      <template v-else>
        <n-space vertical :size="8">
          <n-space align="center" :size="8">
            <n-tag v-if="modelsList.length" type="success" size="small" :bordered="false">
              发现 {{ modelsList.length }} 个模型
            </n-tag>
            <n-tag v-else type="warning" size="small" :bordered="false">连接成功，但未发现模型</n-tag>
            <n-tag v-if="modelsCached" size="small" :bordered="false">缓存命中</n-tag>
          </n-space>
          <n-scrollbar style="max-height: 320px">
            <n-data-table
              :columns="modelColumns"
              :data="modelsList"
              :loading="modelsLoading"
              :row-key="(row: ProviderModel) => row.id"
              size="small"
            />
          </n-scrollbar>
        </n-space>
      </template>
    </n-modal>
  </n-card>
</template>
