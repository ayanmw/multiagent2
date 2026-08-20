<script setup lang="ts">
import { ref, h, onMounted, computed } from 'vue'
import {
  NCard,
} from 'naive-ui/es/card'
import {
  NButton,
} from 'naive-ui/es/button'
import {
  NDataTable,
} from 'naive-ui/es/data-table'
import {
  NTag,
} from 'naive-ui/es/tag'
import {
  NSpace,
} from 'naive-ui/es/space'
import {
  NDrawer,
  NDrawerContent,
} from 'naive-ui/es/drawer'
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
  NText,
} from 'naive-ui/es/typography'
import {
  useMessage,
} from 'naive-ui/es/message'
import {
  useDialog,
} from 'naive-ui/es/dialog'
import type { DataTableColumns } from 'naive-ui/es/data-table'
import type { SelectOption } from 'naive-ui/es/select'
import { useAuthStore } from '@/stores/auth'
import {
  listUsers,
  createUser,
  updateUser,
  disableUser,
  enableUser,
  resetPassword,
  roleLabel,
  statusLabel,
  type AdminUser,
  type UserRole,
  type UserStatus,
} from '@/api/admin'

const auth = useAuthStore()
const message = useMessage()
const dialog = useDialog()

const loading = ref(false)
const users = ref<AdminUser[]>([])
const total = ref(0)

const roleOptions: SelectOption[] = [
  { label: '管理员', value: 'admin' },
  { label: '开发者', value: 'developer' },
  { label: '只读', value: 'viewer' },
]

// 新建 / 编辑抽屉状态。
const drawerVisible = ref(false)
const editing = ref<AdminUser | null>(null)
const form = ref({
  username: '',
  email: '',
  display_name: '',
  password: '',
  role: 'developer' as UserRole,
  status: 'active' as UserStatus,
})

const isSelf = (u: AdminUser) => auth.user?.id === u.id

async function load() {
  loading.value = true
  try {
    const res = await listUsers()
    users.value = res.users
    total.value = res.total
  } catch (e: any) {
    message.error(e?.message || '加载用户列表失败')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  form.value = {
    username: '',
    email: '',
    display_name: '',
    password: '',
    role: 'developer',
    status: 'active',
  }
  drawerVisible.value = true
}

function openEdit(u: AdminUser) {
  editing.value = u
  form.value = {
    username: u.username,
    email: u.email,
    display_name: u.display_name,
    password: '',
    role: u.role,
    status: u.status,
  }
  drawerVisible.value = true
}

async function submitDrawer() {
  try {
    if (editing.value) {
      const payload: any = {}
      if (form.value.display_name) payload.display_name = form.value.display_name
      payload.role = form.value.role
      payload.status = form.value.status
      await updateUser(editing.value.id, payload)
      message.success('已更新用户')
    } else {
      if (!form.value.username || !form.value.email || !form.value.password) {
        message.warning('用户名、邮箱、密码均为必填')
        return
      }
      await createUser({
        username: form.value.username,
        email: form.value.email,
        password: form.value.password,
        display_name: form.value.display_name,
        role: form.value.role,
      })
      message.success('已创建用户')
    }
    drawerVisible.value = false
    await load()
  } catch (e: any) {
    message.error(e?.message || '操作失败')
  }
}

async function toggleStatus(u: AdminUser) {
  if (isSelf(u)) {
    message.warning('不能修改自己的启用状态')
    return
  }
  try {
    if (u.status === 'active') {
      await disableUser(u.id)
      message.success(`已禁用 ${u.username}`)
    } else {
      await enableUser(u.id)
      message.success(`已启用 ${u.username}`)
    }
    await load()
  } catch (e: any) {
    message.error(e?.message || '操作失败')
  }
}

function openReset(u: AdminUser) {
  let value = ''
  dialog.create({
    title: `重置密码 — ${u.username}`,
    content: () =>
      h(NInput, {
        type: 'password',
        placeholder: '请输入新密码（至少 6 位）',
        onUpdateValue: (v: string) => (value = v),
      }),
    positiveText: '确定',
    negativeText: '取消',
    onPositiveClick: async () => {
      if (!value || value.length < 6) {
        message.warning('密码至少 6 位')
        return
      }
      try {
        await resetPassword(u.id, { password: value })
        message.success('密码已重置')
      } catch (e: any) {
        message.error(e?.message || '重置失败')
      }
    },
  })
}

const columns = computed<DataTableColumns<AdminUser>>(() => [
  { title: '用户名', key: 'username', minWidth: 120 },
  { title: '邮箱', key: 'email', minWidth: 180, ellipsis: { tooltip: true } },
  { title: '显示名', key: 'display_name', minWidth: 120, ellipsis: { tooltip: true } },
  {
    title: '角色',
    key: 'role',
    width: 100,
    render: (row) => h(NTag, { type: row.role === 'admin' ? 'error' : row.role === 'developer' ? 'info' : 'default', size: 'small' }, { default: () => roleLabel(row.role) }),
  },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render: (row) =>
      h(
        NTag,
        { type: row.status === 'active' ? 'success' : 'warning', size: 'small' },
        { default: () => statusLabel(row.status) },
      ),
  },
  {
    title: '配额',
    key: 'quota',
    width: 160,
    render: (row) => {
      const q = row.quota
      return q
        ? h(NSpace, { vertical: true, size: 2 }, {
            default: () => [
              h(NText, { depth: 2, style: 'font-size:12px' }, { default: () => `上限 ${q.max_tokens} token` }),
              h(NText, { depth: 3, style: 'font-size:12px' }, { default: () => `${q.window}${q.is_global ? '（全局默认）' : '（用户特定）'}` }),
            ],
          })
        : h(NText, { depth: 3, style: 'font-size:12px' }, { default: () => '无策略' })
    },
  },
  { title: '创建时间', key: 'created_at', width: 160 },
  {
    title: '操作',
    key: 'actions',
    width: 220,
    fixed: 'right',
    render: (row) =>
      h(NSpace, { size: 4 }, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => openEdit(row) }, { default: () => '编辑' }),
          h(
            NButton,
            {
              size: 'small',
              type: row.status === 'active' ? 'warning' : 'primary',
              disabled: isSelf(row),
              onClick: () => toggleStatus(row),
            },
            { default: () => (row.status === 'active' ? '禁用' : '启用') },
          ),
          h(NButton, { size: 'small', onClick: () => openReset(row) }, { default: () => '重置密码' }),
        ],
      }),
  },
])

const currentRole = computed(() => auth.user?.role)

onMounted(load)
</script>

<template>
  <div class="h-full flex flex-col gap-3">
    <n-card :bordered="false" size="small">
      <div class="flex items-center justify-between">
        <div>
          <h2 class="text-lg font-semibold m-0">用户管理</h2>
          <n-text depth="3" style="font-size: 13px">创建 / 禁用 / 重置平台用户，查看配额。仅管理员可见。</n-text>
        </div>
        <n-button v-if="currentRole === 'admin'" type="primary" @click="openCreate">新建用户</n-button>
      </div>
    </n-card>

    <n-card :bordered="false" size="small" class="flex-1" content-style="display:flex;flex-direction:column">
      <n-data-table
        :columns="columns"
        :data="users"
        :loading="loading"
        :row-key="(row: AdminUser) => row.id"
        :scroll-x="1100"
        flex-height
        class="flex-1"
      />
      <n-text depth="3" style="font-size: 12px; margin-top: 8px">共 {{ total }} 名用户</n-text>
    </n-card>

    <n-drawer v-model:show="drawerVisible" :width="420" placement="right">
      <n-drawer-content :title="editing ? '编辑用户' : '新建用户'" :native-scrollbar="false">
        <n-form :model="form" label-placement="top">
          <n-form-item label="用户名">
            <n-input v-model:value="form.username" :disabled="!!editing" placeholder="3-64 位" />
          </n-form-item>
          <n-form-item label="邮箱">
            <n-input v-model:value="form.email" :disabled="!!editing" placeholder="user@example.com" />
          </n-form-item>
          <n-form-item label="显示名">
            <n-input v-model:value="form.display_name" placeholder="可选，默认同用户名" />
          </n-form-item>
          <n-form-item v-if="!editing" label="密码">
            <n-input v-model:value="form.password" type="password" placeholder="至少 6 位" />
          </n-form-item>
          <n-form-item label="角色">
            <n-select
              v-model:value="form.role"
              :options="roleOptions"
              :disabled="!!(editing && isSelf(editing))"
              placeholder="选择角色"
            />
          </n-form-item>
          <n-form-item label="状态">
            <n-space align="center">
              <n-switch
                v-model:value="form.status"
                :checked-value="'active'"
                :unchecked-value="'disabled'"
                :disabled="!!(editing && isSelf(editing))"
              />
              <n-text depth="3" style="font-size: 13px">{{ statusLabel(form.status) }}</n-text>
            </n-space>
          </n-form-item>
        </n-form>
        <template #footer>
          <n-space justify="end">
            <n-button @click="drawerVisible = false">取消</n-button>
            <n-button type="primary" @click="submitDrawer">保存</n-button>
          </n-space>
        </template>
      </n-drawer-content>
    </n-drawer>
  </div>
</template>
