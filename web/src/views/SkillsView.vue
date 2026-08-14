<script setup lang="ts">
import { h, ref, onMounted } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NModal,
  NInput,
  NPopconfirm,
  NTag,
  NSpace,
  NText,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import {
  listSkills,
  getSkill,
  createSkill,
  updateSkill,
  deleteSkill,
  type Skill,
  type SkillDetail,
} from '@/api/skill'

const message = useMessage()
const loading = ref(false)
const skills = ref<Skill[]>([])

async function load() {
  loading.value = true
  try {
    skills.value = await listSkills()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loading.value = false
  }
}
onMounted(load)

// ---- 详情 / 编辑 ----
const showDetail = ref(false)
const detailName = ref('')
const detailBody = ref('')
const detailReadOnly = ref(false)
const detailLoading = ref(false)
const detailSaving = ref(false)

async function openDetail(s: Skill) {
  detailName.value = s.name
  detailReadOnly.value = s.read_only
  detailBody.value = ''
  showDetail.value = true
  detailLoading.value = true
  try {
    const d: SkillDetail = await getSkill(s.name)
    detailBody.value = d.body ?? ''
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    detailLoading.value = false
  }
}

async function saveDetail() {
  detailSaving.value = true
  try {
    await updateSkill(detailName.value, detailBody.value)
    message.success('已保存')
    showDetail.value = false
    await load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    detailSaving.value = false
  }
}

// ---- 新建 ----
const showCreate = ref(false)
const createName = ref('')
const createBody = ref('')
const createSubmitting = ref(false)

function applyTemplate() {
  const nm = createName.value.trim() || 'my-skill'
  createBody.value = `---
name: ${nm}
description: 用一句话描述这个技能解决的问题
---

# 适用场景
（何时应该使用这个技能）

# 步骤
1. ...
2. ...

# 注意事项
- 关键约束与易错点
`
  message.success('已插入 SKILL.md 模板')
}

async function submitCreate() {
  if (!createName.value.trim()) {
    message.warning('请填写技能名（仅字母数字与 - _）')
    return
  }
  createSubmitting.value = true
  try {
    await createSkill(createName.value.trim(), createBody.value)
    message.success('技能创建成功')
    showCreate.value = false
    createName.value = ''
    createBody.value = ''
    await load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    createSubmitting.value = false
  }
}

async function handleDelete(s: Skill) {
  try {
    await deleteSkill(s.name)
    message.success('已删除')
    await load()
  } catch (e) {
    message.error((e as Error).message)
  }
}

const columns: DataTableColumns<Skill> = [
  { title: '名称', key: 'name', minWidth: 140 },
  {
    title: '范围',
    key: 'scope',
    width: 110,
    render(row) {
      const type = row.scope === 'shared' ? 'warning' : 'info'
      return h(NTag, { type, size: 'small', bordered: false }, { default: () => row.scope })
    },
  },
  {
    title: '只读',
    key: 'read_only',
    width: 90,
    render(row) {
      return h(
        NTag,
        { type: row.read_only ? 'default' : 'success', size: 'small', bordered: false },
        { default: () => (row.read_only ? '只读' : '可写') },
      )
    },
  },
  { title: '描述', key: 'description', minWidth: 200, ellipsis: { tooltip: true } },
  {
    title: '操作',
    key: 'actions',
    width: 200,
    fixed: 'right',
    render(row) {
      return h(NSpace, { size: 4, wrap: false }, {
        default: () => [
          h(NButton, { size: 'small', tertiary: true, onClick: () => openDetail(row) }, { default: () => '查看' }),
          h(
            NPopconfirm,
            { onPositiveClick: () => handleDelete(row) },
            {
              default: () => '确认删除该技能？（共享技能不可删）',
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
        <h2 class="text-lg font-semibold m-0">技能仓库</h2>
        <n-text depth="3" class="text-sm">共享技能只读，私有技能可编辑（warm-start 会在对话开始时注入相关 SKILL.md）</n-text>
      </div>
      <n-button type="primary" class="ml-auto" @click="showCreate = true">新建技能</n-button>
    </div>

    <n-data-table
      :columns="columns"
      :data="skills"
      :loading="loading"
      :scroll-x="900"
      :row-key="(row: Skill) => row.name"
      flex-height
      class="flex-1"
    />

    <!-- 详情 / 编辑 -->
    <n-modal
      v-model:show="showDetail"
      :title="`技能：${detailName}`"
      preset="card"
      style="width: 680px; max-width: 94vw"
    >
      <n-text v-if="detailReadOnly" depth="3" class="text-xs">共享技能（只读）</n-text>
      <n-input
        v-model:value="detailBody"
        type="textarea"
        :autosize="{ minRows: 12, maxRows: 28 }"
        :readonly="detailReadOnly"
        placeholder="SKILL.md 内容"
        class="mt-2 font-mono text-xs"
      />
      <template #footer>
        <n-space justify="end">
          <n-button @click="showDetail = false">关闭</n-button>
          <n-button v-if="!detailReadOnly" type="primary" :loading="detailSaving" @click="saveDetail">保存</n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 新建 -->
    <n-modal
      v-model:show="showCreate"
      title="新建技能"
      preset="card"
      style="width: 600px; max-width: 94vw"
    >
      <n-input v-model:value="createName" placeholder="技能名（仅字母数字与 - _）" class="mb-2" />
      <n-space class="mb-2">
        <n-button size="small" tertiary @click="applyTemplate">插入 SKILL.md 模板</n-button>
        <n-text depth="3" class="text-xs">模板含 frontmatter(name/description)+正文，warm-start 会自动命中私有技能</n-text>
      </n-space>
      <n-input
        v-model:value="createBody"
        type="textarea"
        :autosize="{ minRows: 10, maxRows: 24 }"
        placeholder="SKILL.md 内容（frontmatter + 正文）"
        class="font-mono text-xs"
      />
      <template #footer>
        <n-space justify="end">
          <n-button @click="showCreate = false">取消</n-button>
          <n-button type="primary" :loading="createSubmitting" @click="submitCreate">创建</n-button>
        </n-space>
      </template>
    </n-modal>
  </n-card>
</template>
