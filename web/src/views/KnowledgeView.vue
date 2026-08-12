<script setup lang="ts">
import { h, ref, reactive, computed, onMounted } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NInput,
  NInputGroup,
  NSelect,
  NSpace,
  NText,
  NEmpty,
  NModal,
  NForm,
  NFormItem,
  NTag,
  NScrollbar,
  useMessage,
  useDialog,
  type DataTableColumns,
} from 'naive-ui'
import {
  listKnowledgeBases,
  createKnowledgeBase,
  updateKnowledgeBase,
  deleteKnowledgeBase,
  listDocuments,
  indexDocument,
  deleteDocument,
  searchKnowledge,
  type KnowledgeBase,
  type DocInfo,
  type SearchHit,
} from '@/api/knowledge'

const message = useMessage()
const dialog = useDialog()

const loading = ref(false)
const bases = ref<KnowledgeBase[]>([])

// 选中的知识库（主从布局：左侧列表，右侧详情）。
const selected = ref<KnowledgeBase | null>(null)
const docs = ref<DocInfo[]>([])
const docsLoading = ref(false)

// 新建 / 编辑弹窗。
const showEdit = ref(false)
const editing = ref<KnowledgeBase | null>(null)
const form = reactive({ name: '', description: '' })
const saving = ref(false)

// 添加文档抽屉（用弹窗实现）。
const showDoc = ref(false)
const docForm = reactive({ name: '', content: '', content_type: 'text' })
const docSaving = ref(false)

// 检索。
const searchQuery = ref('')
const searchTopK = ref(3)
const hits = ref<SearchHit[]>([])
const searching = ref(false)

const contentTypeOptions = [
  { label: '文本 (text)', value: 'text' },
  { label: '代码 (code)', value: 'code' },
]

async function load() {
  loading.value = true
  try {
    bases.value = await listKnowledgeBases()
    if (selected.value) {
      const still = bases.value.find((b) => b.id === selected.value!.id)
      selected.value = still ?? null
      if (still) await loadDocs(still.id)
    }
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loading.value = false
  }
}

async function loadDocs(id: number) {
  docsLoading.value = true
  try {
    docs.value = await listDocuments(id)
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    docsLoading.value = false
  }
}

async function selectBase(b: KnowledgeBase) {
  selected.value = b
  hits.value = []
  searchQuery.value = ''
  await loadDocs(b.id)
}

function openCreate() {
  editing.value = null
  form.name = ''
  form.description = ''
  showEdit.value = true
}

function openEdit(b: KnowledgeBase) {
  editing.value = b
  form.name = b.name
  form.description = b.description
  showEdit.value = true
}

async function saveBase() {
  if (!form.name.trim()) {
    message.warning('请填写知识库名称')
    return
  }
  saving.value = true
  try {
    if (editing.value) {
      await updateKnowledgeBase(editing.value.id, form.name.trim(), form.description.trim())
      message.success('已更新知识库')
    } else {
      const created = await createKnowledgeBase(form.name.trim(), form.description.trim())
      message.success('已创建知识库')
      await load()
      await selectBase(created)
    }
    showEdit.value = false
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    saving.value = false
  }
}

function confirmDelete(b: KnowledgeBase) {
  dialog.warning({
    title: '删除知识库',
    content: `确认删除「${b.name}」？该知识库的全部索引向量将一并清除，不可恢复。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteKnowledgeBase(b.id)
        message.success('已删除')
        if (selected.value?.id === b.id) selected.value = null
        await load()
      } catch (e) {
        message.error((e as Error).message)
      }
    },
  })
}

function openAddDoc() {
  if (!selected.value) return
  docForm.name = ''
  docForm.content = ''
  docForm.content_type = 'text'
  showDoc.value = true
}

async function saveDoc() {
  if (!selected.value) return
  if (!docForm.name.trim() || !docForm.content.trim()) {
    message.warning('请填写文档名称与内容')
    return
  }
  docSaving.value = true
  try {
    const res = await indexDocument(
      selected.value.id,
      docForm.name.trim(),
      docForm.content,
      docForm.content_type,
    )
    message.success(`已索引 ${res.indexed_chunks} 个切片`)
    showDoc.value = false
    await loadDocs(selected.value.id)
    await load()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    docSaving.value = false
  }
}

function confirmDeleteDoc(d: DocInfo) {
  if (!selected.value) return
  dialog.warning({
    title: '删除文档',
    content: `确认删除文档「${d.name}」（${d.chunk_count} 个切片）？`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteDocument(selected.value!.id, d.name)
        message.success('已删除文档')
        await loadDocs(selected.value!.id)
        await load()
      } catch (e) {
        message.error((e as Error).message)
      }
    },
  })
}

async function runSearch() {
  if (!selected.value) return
  if (!searchQuery.value.trim()) {
    message.warning('请输入检索内容')
    return
  }
  searching.value = true
  try {
    hits.value = await searchKnowledge(selected.value.id, searchQuery.value.trim(), searchTopK.value)
    if (hits.value.length === 0) message.info('未检索到相关内容')
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    searching.value = false
  }
}

const baseColumns: DataTableColumns<KnowledgeBase> = [
  {
    title: '名称',
    key: 'name',
    render(row) {
      return h('span', { class: 'font-medium' }, row.name)
    },
  },
  {
    title: '描述',
    key: 'description',
    ellipsis: { tooltip: true },
    render(row) {
      return h('span', { class: 'text-gray-500 dark:text-gray-400' }, row.description || '-')
    },
  },
  {
    title: '文档 / 切片',
    key: 'counts',
    width: 120,
    render(row) {
      return h('span', { class: 'tabular-nums' }, `${row.doc_count} / ${row.chunk_count}`)
    },
  },
  {
    title: '更新时间',
    key: 'updated_at',
    width: 180,
    render(row) {
      return h('span', { class: 'text-xs text-gray-500 dark:text-gray-400' }, row.updated_at)
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 150,
    render(row) {
      return h(NSpace, { size: 'small' }, () => [
        h(
          NButton,
          { size: 'small', type: 'primary', quaternary: true, onClick: () => openEdit(row) },
          { default: () => '编辑' },
        ),
        h(
          NButton,
          { size: 'small', type: 'error', quaternary: true, onClick: () => confirmDelete(row) },
          { default: () => '删除' },
        ),
      ])
    },
  },
]

const docColumns: DataTableColumns<DocInfo> = [
  {
    title: '文档（来源）',
    key: 'name',
    render(row) {
      return h('span', { class: 'font-medium' }, row.name)
    },
  },
  {
    title: '切片数',
    key: 'chunk_count',
    width: 100,
    render(row) {
      return h('span', { class: 'tabular-nums' }, String(row.chunk_count))
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 100,
    render(row) {
      return h(
        NButton,
        { size: 'small', type: 'error', quaternary: true, onClick: () => confirmDeleteDoc(row) },
        { default: () => '删除' },
      )
    },
  },
]

onMounted(load)
</script>

<template>
  <n-card :bordered="false" class="h-full" content-style="display:flex;gap:12px;height:100%">
    <!-- 左：知识库列表 -->
    <div class="flex flex-col w-72 shrink-0 border-r border-gray-200 dark:border-gray-700 pr-3">
      <div class="flex items-center mb-3">
        <h2 class="text-lg font-semibold m-0">知识库</h2>
        <n-button class="ml-auto" type="primary" size="small" @click="openCreate">新建</n-button>
      </div>
      <n-scrollbar class="flex-1">
        <div v-if="bases.length === 0 && !loading" class="text-sm text-gray-500 mt-4">
          还没有知识库，点击「新建」开始。
        </div>
        <div
          v-for="b in bases"
          :key="b.id"
          class="px-3 py-2 mb-2 rounded cursor-pointer border transition-colors"
          :class="
            selected && selected.id === b.id
              ? 'border-blue-500 bg-blue-50 dark:bg-blue-950'
              : 'border-gray-200 dark:border-gray-700 hover:border-blue-300'
          "
          @click="selectBase(b)"
        >
          <div class="font-medium truncate">{{ b.name }}</div>
          <div class="text-xs text-gray-500 dark:text-gray-400 truncate">
            {{ b.description || '无描述' }}
          </div>
          <div class="text-xs text-gray-400 mt-1 tabular-nums">
            文档 {{ b.doc_count }} · 切片 {{ b.chunk_count }}
          </div>
        </div>
      </n-scrollbar>
    </div>

    <!-- 右：选中知识库详情 -->
    <div class="flex-1 flex flex-col min-w-0">
      <template v-if="selected">
        <div class="flex items-center mb-3">
          <div>
            <h2 class="text-lg font-semibold m-0">{{ selected.name }}</h2>
            <n-text depth="3" class="text-xs">{{ selected.description || '无描述' }}</n-text>
          </div>
          <n-button class="ml-auto" type="primary" size="small" :loading="docsLoading" @click="openAddDoc">
            添加文档
          </n-button>
        </div>

        <!-- 检索 -->
        <n-card size="small" :bordered="true" class="mb-3">
          <div class="flex items-center gap-2">
            <n-input-group>
              <n-input
                v-model:value="searchQuery"
                placeholder="输入问题 / 关键词检索知识库内容"
                @keyup.enter="runSearch"
              />
              <n-select
                v-model:value="searchTopK"
                :options="[
                  { label: 'Top 3', value: 3 },
                  { label: 'Top 5', value: 5 },
                  { label: 'Top 10', value: 10 },
                ]"
                style="width: 96px"
              />
              <n-button type="primary" :loading="searching" @click="runSearch">检索</n-button>
            </n-input-group>
          </div>
          <div v-if="hits.length" class="mt-3 space-y-2">
            <div
              v-for="(hit, i) in hits"
              :key="i"
              class="p-2 rounded bg-gray-50 dark:bg-gray-800 text-sm"
            >
              <div class="flex items-center gap-2 mb-1">
                <n-tag size="small" :bordered="false" type="info">{{ hit.source }}</n-tag>
                <span class="text-xs text-gray-400">#{{ hit.chunk_index }}</span>
                <span class="text-xs text-gray-400 ml-auto tabular-nums">
                  相似度 {{ hit.score.toFixed(3) }}
                </span>
              </div>
              <pre class="whitespace-pre-wrap break-words m-0 font-sans text-gray-700 dark:text-gray-200">{{ hit.content }}</pre>
            </div>
          </div>
        </n-card>

        <!-- 文档列表 -->
        <n-data-table
          :columns="docColumns"
          :data="docs"
          :loading="docsLoading"
          :row-key="(row: DocInfo) => row.name"
          flex-height
          class="flex-1"
        />
        <n-empty
          v-if="!docsLoading && docs.length === 0"
          description="该知识库还没有文档，点击「添加文档」上传文本"
          class="mt-6"
        />
      </template>
      <n-empty v-else description="选择左侧知识库以查看文档与检索" class="m-auto" />
    </div>

    <!-- 新建 / 编辑知识库弹窗 -->
    <n-modal
      v-model:show="showEdit"
      :title="editing ? '编辑知识库' : '新建知识库'"
      preset="card"
      style="width: 480px"
    >
      <n-form :model="form" label-placement="top">
        <n-form-item label="名称">
          <n-input v-model:value="form.name" placeholder="如：Go 编码规范" />
        </n-form-item>
        <n-form-item label="描述">
          <n-input
            v-model:value="form.description"
            type="textarea"
            placeholder="这个知识库用来存放什么内容"
            :autosize="{ minRows: 2, maxRows: 4 }"
          />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showEdit = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="saveBase">保存</n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 添加文档弹窗 -->
    <n-modal v-model:show="showDoc" title="添加文档" preset="card" style="width: 640px">
      <n-form :model="docForm" label-placement="top">
        <n-form-item label="文档名称">
          <n-input v-model:value="docForm.name" placeholder="如：goroutine.md" />
        </n-form-item>
        <n-form-item label="类型">
          <n-select v-model:value="docForm.content_type" :options="contentTypeOptions" />
        </n-form-item>
        <n-form-item label="内容">
          <n-input
            v-model:value="docForm.content"
            type="textarea"
            placeholder="粘贴或输入文档文本内容，将自动切片并索引"
            :autosize="{ minRows: 8, maxRows: 16 }"
          />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showDoc = false">取消</n-button>
          <n-button type="primary" :loading="docSaving" @click="saveDoc">索引</n-button>
        </n-space>
      </template>
    </n-modal>
  </n-card>
</template>
