<script setup lang="ts">
import { h, ref, onMounted } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NModal,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NPopconfirm,
  NTag,
  NSpace,
  NText,
  NAlert,
  NScrollbar,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import {
  listAPIKeys,
  createAPIKey,
  revokeAPIKey,
  type APIKey,
  type CreateAPIKeyResult,
} from '@/api/apikey'
import { request } from '@/api/client'

const message = useMessage()

// ---- API Keys ----
const loadingKeys = ref(false)
const apikeys = ref<APIKey[]>([])
async function loadKeys() {
  loadingKeys.value = true
  try {
    apikeys.value = await listAPIKeys()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loadingKeys.value = false
  }
}

const showCreate = ref(false)
const createName = ref('')
const createExpires = ref<number | null>(null)
const createSubmitting = ref(false)
const createdSecret = ref('')

async function submitCreate() {
  if (!createName.value.trim()) {
    message.warning('请填写名称')
    return
  }
  createSubmitting.value = true
  createdSecret.value = ''
  try {
    const res: CreateAPIKeyResult = await createAPIKey(
      createName.value.trim(),
      createExpires.value ?? undefined,
    )
    createdSecret.value = res.api_key
    message.success('API Key 创建成功（明文仅显示一次）')
    await loadKeys()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    createSubmitting.value = false
  }
}

async function handleRevoke(k: APIKey) {
  try {
    await revokeAPIKey(k.id)
    message.success('已撤销')
    await loadKeys()
  } catch (e) {
    message.error((e as Error).message)
  }
}

const keyColumns: DataTableColumns<APIKey> = [
  { title: '名称', key: 'name', minWidth: 120 },
  { title: '前缀', key: 'prefix', width: 130, render(row) {
    return h('code', { class: 'font-mono text-xs' }, row.prefix)
  } },
  {
    title: '状态',
    key: 'status',
    width: 100,
    render(row) {
      return h(NTag, { type: row.status === 'active' ? 'success' : 'default', size: 'small', bordered: false }, { default: () => row.status })
    },
  },
  { title: '过期时间', key: 'expires_at', width: 180, render(row) {
    return h(NText, { depth: 3, class: 'text-xs' }, { default: () => row.expires_at ?? '永不过期' })
  } },
  { title: '创建时间', key: 'created_at', width: 180, render(row) {
    return h(NText, { depth: 3, class: 'text-xs' }, { default: () => row.created_at })
  } },
  {
    title: '操作',
    key: 'actions',
    width: 120,
    fixed: 'right',
    render(row) {
      return h(
        NPopconfirm,
        { onPositiveClick: () => handleRevoke(row) },
        {
          default: () => '确认撤销该 API Key？',
          trigger: () =>
            h(NButton, { size: 'small', type: 'error', tertiary: true }, { default: () => '撤销' }),
        },
      )
    },
  },
]

// ---- 角色 / 权限（仅管理员可见）----
interface Role {
  id: number
  name: string
  permissions: string[]
  description?: string
}
const roles = ref<Role[]>([])
const rolesError = ref('')
async function loadRoles() {
  rolesError.value = ''
  try {
    const data = await request<{ roles: Role[] }>('/admin/roles')
    roles.value = data.roles ?? []
  } catch (e) {
    rolesError.value = (e as Error).message
  }
}

// ---- 运行模式（服务端配置，只读）----
const teamFeatures = [
  'AGENT_MODE=team 时，根 Agent 切换为 Orchestrator，代码落地委托 Coder 子代理',
  'EnableReviewer：默认加入只读 Reviewer，形成「实现 → 审阅 → 修复」回环',
  'EnableGoal（目标契约）：Orchestrator 须把目标推进到 complete/blocked 才允许结束',
  'EnablePlan（Plan-Execute）：先建计划、逐项执行完毕才允许结束',
  'Guardrail（护栏熔断）：预算/调用次数硬顶，熔断时优雅终止并保留部分结果',
  'StateExternalization（状态外置）：PLAN/PROGRESS/LEARNINGS 落盘，支持跨重启续跑',
  'ToolSearch（延迟工具箱）：MCP 工具默认不暴露，由 tool_search/call_tool 按需调用',
]

onMounted(() => {
  loadKeys()
  loadRoles()
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <!-- API Key 管理 -->
    <n-card :bordered="false" title="API Key 管理">
      <div class="flex items-center mb-3">
        <n-text depth="3" class="text-sm">用于 CLI / 第三方接入的令牌（需 apikeys:write 权限）</n-text>
        <n-button class="ml-auto" type="primary" size="small" @click="showCreate = true">新建 Key</n-button>
      </div>
      <n-data-table
        :columns="keyColumns"
        :data="apikeys"
        :loading="loadingKeys"
        :scroll-x="820"
        :row-key="(row: APIKey) => row.id"
        size="small"
      />
    </n-card>

    <!-- 角色 / 权限 -->
    <n-card :bordered="false" title="角色与权限">
      <n-empty v-if="rolesError" :description="`无法加载：${rolesError}（仅管理员可见）`" />
      <n-empty v-else-if="roles.length === 0" description="暂无角色数据" />
      <n-scrollbar v-else style="max-height: 280px">
        <div v-for="r in roles" :key="r.id" class="mb-3">
          <div class="flex items-center gap-2 mb-1">
            <n-tag size="small" :bordered="false" type="info">{{ r.name }}</n-tag>
            <n-text depth="3" class="text-xs">{{ r.description }}</n-text>
          </div>
          <div class="flex flex-wrap gap-1">
            <n-tag v-for="p in r.permissions" :key="p" size="tiny" :bordered="false">{{ p }}</n-tag>
          </div>
        </div>
      </n-scrollbar>
    </n-card>

    <!-- 运行模式（只读） -->
    <n-card :bordered="false" title="运行模式（服务端配置 · 只读）">
      <n-text depth="3" class="text-sm">以下能力由后端环境变量决定，前端仅作展示：</n-text>
      <ul class="mt-2 pl-5 list-disc text-sm text-gray-700 dark:text-gray-200">
        <li v-for="(f, i) in teamFeatures" :key="i">{{ f }}</li>
      </ul>
    </n-card>

    <!-- 新建 Key 弹窗 -->
    <n-modal
      v-model:show="showCreate"
      title="新建 API Key"
      preset="card"
      style="width: 480px; max-width: 92vw"
    >
      <n-form :model="{ name: createName, expires: createExpires }" label-placement="top">
        <n-form-item label="名称" required>
          <n-input v-model:value="createName" placeholder="例如：我的 CLI" />
        </n-form-item>
        <n-form-item label="有效期（天，留空为永不过期）">
          <n-input-number v-model:value="createExpires" :min="1" :max="3650" clearable />
        </n-form-item>
      </n-form>
      <n-alert v-if="createdSecret" type="success" :title="'已创建，明文密钥（仅显示一次）'" class="mb-2">
        <n-input :value="createdSecret" readonly class="font-mono text-xs" />
      </n-alert>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showCreate = false">关闭</n-button>
          <n-button type="primary" :loading="createSubmitting" @click="submitCreate">创建</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>
