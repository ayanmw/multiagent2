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
  listWorkspaces,
  createWorkspace,
  updateWorkspace,
  deleteWorkspace,
  type Workspace,
  type WorkspacePayload,
} from '@/api/workspace'

const message = useMessage()
const loading = ref(false)
const workspaces = ref<Workspace[]>([])

async function load() {
  loading.value = true
  try {
    workspaces.value = await listWorkspaces()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loading.value = false
  }
}
onMounted(load)

const showModal = ref(false)
const editingKey = ref<string | null>(null)
const submitting = ref(false)
const form = reactive<WorkspacePayload>({ name: '', git_remote: '', description: '' })

function openCreate() {
  editingKey.value = null
  form.name = ''
  form.git_remote = ''
  form.description = ''
  showModal.value = true
}
function openEdit(w: Workspace) {
  editingKey.value = w.key
  form.name = w.name
  form.git_remote = w.git_remote
  form.description = w.description
  showModal.value = true
}

async function submit() {
  if (!form.name.trim()) {
    message.warning('请填写名称')
    return
  }
  submitting.value = true
  try {
    const payload: WorkspacePayload = {
      name: form.name.trim(),
      git_remote: form.git_remote?.trim() ?? '',
      description: form.description?.trim() ?? '',
    }
    if (editingKey.value === null) {
      await createWorkspace(payload)
      message.success('工作区创建成功')
    } else {
      await updateWorkspace(editingKey.value, payload)
      message.success('工作区更新成功')
    }
    showModal.value = false
    await load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    submitting.value = false
  }
}

async function handleDelete(w: Workspace) {
  try {
    await deleteWorkspace(w.key)
    message.success('已删除（磁盘目录保留）')
    await load()
  } catch (e) {
    message.error((e as Error).message)
  }
}

const columns: DataTableColumns<Workspace> = [
  { title: '名称', key: 'name', minWidth: 120 },
  { title: 'Key', key: 'key', width: 130, render(row) {
    return h(NText, { depth: 3, class: 'font-mono text-xs' }, { default: () => row.key })
  } },
  { title: '本地路径', key: 'local_path', minWidth: 160, ellipsis: { tooltip: true } },
  { title: 'Git Remote', key: 'git_remote', minWidth: 120, ellipsis: { tooltip: true } },
  {
    title: '状态',
    key: 'status',
    width: 100,
    render(row) {
      return h(
        NTag,
        { type: row.status === 'active' ? 'success' : 'default', size: 'small', bordered: false },
        { default: () => row.status },
      )
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
              default: () => '确认删除该工作区？磁盘目录会保留。',
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
        <h2 class="text-lg font-semibold m-0">工作区管理</h2>
        <n-text depth="3" class="text-sm">绑定代码目录，对话中的 CodeAct 工具在该目录执行（自动 git init）</n-text>
      </div>
      <n-button type="primary" class="ml-auto" @click="openCreate">新建工作区</n-button>
    </div>

    <n-data-table
      :columns="columns"
      :data="workspaces"
      :loading="loading"
      :scroll-x="1000"
      :row-key="(row: Workspace) => row.key"
      flex-height
      class="flex-1"
    />

    <n-modal
      v-model:show="showModal"
      :title="editingKey === null ? '新建工作区' : '编辑工作区'"
      preset="card"
      style="width: 520px; max-width: 92vw"
    >
      <n-form :model="form" label-placement="top">
        <n-form-item label="名称" required>
          <n-input v-model:value="form.name" placeholder="例如：我的项目" />
        </n-form-item>
        <n-form-item label="Git Remote（可选）">
          <n-input v-model:value="form.git_remote" placeholder="git@github.com:user/repo.git" />
        </n-form-item>
        <n-form-item label="描述">
          <n-input v-model:value="form.description" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showModal = false">取消</n-button>
          <n-button type="primary" :loading="submitting" @click="submit">
            {{ editingKey === null ? '创建' : '保存' }}
          </n-button>
        </n-space>
      </template>
    </n-modal>
  </n-card>
</template>
