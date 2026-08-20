<script setup lang="ts">
// 租户管理（M8-09 多租户隔离，admin 专属）。
// 租户 = 一组用户的配额边界：租户内用户共享租户级预算上限（scope=tenant），
// 租户 A 超配额不影响 B。本页支持：创建/启停用/删除租户、成员加入/移出、
// 租户预算设置（budgets scope=tenant）。
import { ref, h, onMounted, computed } from 'vue'
import { NCard } from 'naive-ui/es/card'
import { NButton } from 'naive-ui/es/button'
import { NDataTable } from 'naive-ui/es/data-table'
import { NTag } from 'naive-ui/es/tag'
import { NSpace } from 'naive-ui/es/space'
import { NModal } from 'naive-ui/es/modal'
import { NDrawer, NDrawerContent } from 'naive-ui/es/drawer'
import { NForm, NFormItem } from 'naive-ui/es/form'
import { NInput } from 'naive-ui/es/input'
import { NSelect } from 'naive-ui/es/select'
import { NInputNumber } from 'naive-ui/es/input-number'
import { NText } from 'naive-ui/es/typography'
import { useMessage } from 'naive-ui/es/message'
import { useDialog } from 'naive-ui/es/dialog'
import type { DataTableColumns } from 'naive-ui/es/data-table'
import type { SelectOption } from 'naive-ui/es/select'
import {
  listTenants,
  createTenant,
  updateTenant,
  deleteTenant,
  addTenantMember,
  removeTenantMember,
  listBudgets,
  upsertBudget,
  type Tenant,
} from '@/api/tenant'
import { listUsers, type AdminUser } from '@/api/admin'

const message = useMessage()
const dialog = useDialog()

const tenants = ref<Tenant[]>([])
const loading = ref(false)

// ---- 创建弹窗 ----
const showCreate = ref(false)
const createForm = ref({ name: '', description: '' })
const creating = ref(false)

// ---- 成员管理抽屉 ----
const showMembers = ref(false)
const activeTenant = ref<Tenant | null>(null)
const users = ref<AdminUser[]>([])
const memberUsers = computed(() =>
  users.value.filter((u) => u.tenant_id === activeTenant.value?.id),
)
const freeUsers = computed(() =>
  users.value.filter((u) => !u.tenant_id || u.tenant_id === 0),
)
const selectedUserId = ref<number | null>(null)

// ---- 预算弹窗 ----
const showBudget = ref(false)
const budgetForm = ref({ max_tokens: 100000, window: 'daily' as 'daily' | 'total' })

async function loadTenants() {
  loading.value = true
  try {
    const r = await listTenants()
    tenants.value = r.tenants
  } catch (e) {
    message.error((e as Error).message || '加载租户失败')
  } finally {
    loading.value = false
  }
}

async function loadUsers() {
  try {
    const r = await listUsers()
    users.value = r.users
  } catch {
    // 成员管理打开时才会用到用户列表，失败静默（抽屉内重试）。
  }
}

onMounted(loadTenants)

function statusTag(s: Tenant['status']) {
  return h(
    NTag,
    { size: 'small', type: s === 'active' ? 'success' : 'warning' },
    { default: () => (s === 'active' ? '启用' : '停用') },
  )
}

const columns: DataTableColumns<Tenant> = [
  {
    title: '租户名',
    key: 'name',
    render: (row) => h(NText, { strong: true }, { default: () => row.name }),
  },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render: (row) => statusTag(row.status),
  },
  {
    title: '成员数',
    key: 'member_count',
    width: 90,
  },
  {
    title: '描述',
    key: 'description',
    ellipsis: { tooltip: true },
  },
  {
    title: '创建时间',
    key: 'created_at',
    width: 170,
  },
  {
    title: '操作',
    key: 'actions',
    width: 320,
    render: (row) =>
      h(NSpace, { size: 4 }, {
        default: () => [
          h(
            NButton,
            { size: 'small', onClick: () => openMembers(row) },
            { default: () => '成员' },
          ),
          h(
            NButton,
            { size: 'small', onClick: () => openBudget(row) },
            { default: () => '预算' },
          ),
          h(
            NButton,
            {
              size: 'small',
              type: row.status === 'active' ? 'warning' : 'success',
              onClick: () => toggleStatus(row),
            },
            { default: () => (row.status === 'active' ? '停用' : '启用') },
          ),
          h(
            NButton,
            {
              size: 'small',
              type: 'error',
              onClick: () => confirmDelete(row),
            },
            { default: () => '删除' },
          ),
        ],
      }),
  },
]

async function submitCreate() {
  if (!createForm.value.name.trim()) {
    message.warning('请输入租户名')
    return
  }
  creating.value = true
  try {
    await createTenant({
      name: createForm.value.name.trim(),
      description: createForm.value.description.trim() || undefined,
    })
    message.success('租户已创建')
    showCreate.value = false
    createForm.value = { name: '', description: '' }
    await loadTenants()
  } catch (e) {
    message.error((e as Error).message || '创建失败')
  } finally {
    creating.value = false
  }
}

async function toggleStatus(row: Tenant) {
  try {
    await updateTenant(row.id, { status: row.status === 'active' ? 'disabled' : 'active' })
    message.success(row.status === 'active' ? '已停用（不可再加入成员）' : '已启用')
    await loadTenants()
  } catch (e) {
    message.error((e as Error).message || '操作失败')
  }
}

function confirmDelete(row: Tenant) {
  dialog.warning({
    title: '删除租户',
    content: `确定删除租户「${row.name}」？有成员的租户无法删除（需先迁移成员）。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteTenant(row.id)
        message.success('租户已删除')
        await loadTenants()
      } catch (e) {
        message.error((e as Error).message || '删除失败（有成员时需先移出全部成员）')
      }
    },
  })
}

// ---- 成员管理 ----
async function openMembers(row: Tenant) {
  activeTenant.value = row
  showMembers.value = true
  selectedUserId.value = null
  await loadUsers()
}

async function addMember() {
  if (!selectedUserId.value || !activeTenant.value) return
  try {
    await addTenantMember(activeTenant.value.id, selectedUserId.value)
    message.success('成员已加入')
    await loadUsers()
    await loadTenants()
  } catch (e) {
    message.error((e as Error).message || '加入失败')
  }
}

async function removeMember(userId: number) {
  if (!activeTenant.value) return
  try {
    await removeTenantMember(activeTenant.value.id, userId)
    message.success('成员已移出（恢复独立用户）')
    await loadUsers()
    await loadTenants()
  } catch (e) {
    message.error((e as Error).message || '移出失败')
  }
}

const freeUserOptions = computed<SelectOption[]>(() =>
  freeUsers.value.map((u) => ({
    label: `${u.username}（${u.email}）`,
    value: u.id,
  })),
)

// ---- 租户预算 ----
async function openBudget(row: Tenant) {
  activeTenant.value = row
  budgetForm.value = { max_tokens: 100000, window: 'daily' }
  try {
    const policies = await listBudgets()
    const mine = policies.find(
      (p) => p.scope === 'tenant' && String(p.scope_key) === String(row.id),
    )
    if (mine) {
      budgetForm.value = { max_tokens: mine.max_tokens, window: mine.window }
    }
  } catch {
    // 读预算失败不阻断弹窗，按默认值编辑。
  }
  showBudget.value = true
}

async function submitBudget() {
  if (!activeTenant.value) return
  try {
    await upsertBudget({
      scope: 'tenant',
      scope_key: String(activeTenant.value.id),
      max_tokens: budgetForm.value.max_tokens,
      window: budgetForm.value.window,
    })
    message.success('租户预算已更新（租户超限只拦本租户用户）')
    showBudget.value = false
  } catch (e) {
    message.error((e as Error).message || '保存预算失败')
  }
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <n-card title="租户管理（多租户隔离）" size="small">
      <template #header-extra>
        <n-button type="primary" size="small" @click="showCreate = true">新建租户</n-button>
      </template>
      <template #default>
        <n-alert type="info" class="mb-3" title="什么是租户？">
          租户是一组用户的配额边界：租户内用户共享「租户级预算上限」（budgets scope=tenant）。
          租户 A 超配额只会拦截 A 内用户的后续 LLM 调用，不影响租户 B 与独立用户。
          租户预算请在「预算」入口设置；用户归属可在「成员」中管理。
        </n-alert>
        <n-data-table
          :columns="columns"
          :data="tenants"
          :loading="loading"
          :row-key="(r: Tenant) => r.id"
          :scroll-x="900"
        />
      </template>
    </n-card>

    <!-- 创建租户弹窗 -->
    <n-modal
      v-model:show="showCreate"
      preset="card"
      title="新建租户"
      style="width: 480px"
    >
      <n-form label-placement="top">
        <n-form-item label="租户名（唯一）" required>
          <n-input v-model:value="createForm.name" placeholder="如：研发一部" />
        </n-form-item>
        <n-form-item label="描述">
          <n-input
            v-model:value="createForm.description"
            type="textarea"
            placeholder="可选：租户用途说明"
          />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showCreate = false">取消</n-button>
          <n-button type="primary" :loading="creating" @click="submitCreate">创建</n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 成员管理抽屉 -->
    <n-drawer v-model:show="showMembers" :width="480">
      <n-drawer-content :title="`成员管理 · ${activeTenant?.name ?? ''}`">
        <n-alert type="info" class="mb-3">
          租户内用户共享租户级预算；移出后恢复为独立用户（不再参与本租户预算聚合）。
        </n-alert>
        <n-form-item label="添加成员">
          <n-space>
            <n-select
              v-model:value="selectedUserId"
              :options="freeUserOptions"
              placeholder="选择未归属租户的用户"
              style="width: 300px"
              clearable
            />
            <n-button type="primary" :disabled="!selectedUserId" @click="addMember">
              加入
            </n-button>
          </n-space>
        </n-form-item>
        <n-space vertical>
          <n-text depth="3">当前成员（{{ memberUsers.length }}）</n-text>
          <div
            v-for="u in memberUsers"
            :key="u.id"
            class="flex items-center justify-between border-b border-gray-200 pb-2"
          >
            <div>
              <n-text>{{ u.username }}</n-text>
              <n-text depth="3" class="ml-2">{{ u.email }}</n-text>
            </div>
            <n-button size="small" type="error" ghost @click="removeMember(u.id)">
              移出
            </n-button>
          </div>
          <n-text v-if="memberUsers.length === 0" depth="3">暂无成员</n-text>
        </n-space>
      </n-drawer-content>
    </n-drawer>

    <!-- 租户预算弹窗 -->
    <n-modal
      v-model:show="showBudget"
      preset="card"
      :title="`租户预算 · ${activeTenant?.name ?? ''}`"
      style="width: 440px"
    >
      <n-form label-placement="top">
        <n-form-item label="Token 上限（max_tokens）" required>
          <n-input-number
            v-model:value="budgetForm.max_tokens"
            :min="1"
            style="width: 100%"
          />
        </n-form-item>
        <n-form-item label="统计窗口">
          <n-select
            v-model:value="budgetForm.window"
            :options="[
              { label: '每日（natural day 重置）', value: 'daily' },
              { label: '全周期（不重置）', value: 'total' },
            ]"
          />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showBudget = false">取消</n-button>
          <n-button type="primary" @click="submitBudget">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>
