<script setup lang="ts">
import { h, ref, onMounted } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NSwitch,
  NTag,
  NSpace,
  NText,
  NEmpty,
  NScrollbar,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import {
  listProviders,
  type Provider,
  type Protocol,
} from '@/api/provider'
import {
  listManagedModels,
  syncProviderModels,
  updateModel,
  type ManagedModel,
} from '@/api/model'

const message = useMessage()

// 每个 Provider 一组：自带模型列表与各自的加载/同步状态。
interface ModelGroup {
  provider: Provider
  models: ManagedModel[]
  loading: boolean
  syncing: boolean
  error: string
  cached: boolean
  synced: number
}

const loading = ref(false)
const groups = ref<ModelGroup[]>([])

// 协议标签着色，与 Provider 管理页保持一致。
function protocolType(p: Protocol): 'info' | 'warning' | 'success' {
  return p === 'openai' ? 'info' : p === 'anthropic' ? 'warning' : 'success'
}

// 拉取某 Provider 已托管的模型列表（不触发上游重新发现）。
async function loadGroup(g: ModelGroup) {
  g.loading = true
  g.error = ''
  try {
    g.models = await listManagedModels(g.provider.id)
  } catch (e) {
    g.error = (e as Error).message
  } finally {
    g.loading = false
  }
}

// 「刷新」：触发从 Provider 上游发现模型并 upsert 落库，再刷新本组列表。
async function syncGroup(g: ModelGroup) {
  g.syncing = true
  g.error = ''
  try {
    const res = await syncProviderModels(g.provider.id)
    g.models = res.models
    g.cached = res.cached
    g.synced = res.synced
    if (res.models.length === 0) {
      message.warning('连接成功，但未发现模型')
    } else {
      message.success(`已同步 ${res.models.length} 个模型${res.cached ? '（缓存命中）' : ''}`)
    }
  } catch (e) {
    g.error = (e as Error).message
    message.error('同步失败：' + (e as Error).message)
  } finally {
    g.syncing = false
  }
}

// 切换启用/禁用；默认模型强制启用，故启用开关在 is_default 时锁定。
async function toggleEnabled(row: ManagedModel, val: boolean) {
  try {
    const updated = await updateModel(row.provider_id, row.id, { enabled: val })
    row.enabled = updated.enabled
    row.is_default = updated.is_default
    message.success(val ? '已启用' : '已禁用')
  } catch (e) {
    message.error((e as Error).message)
  }
}

// 切换默认模型：设为默认时一并启用（默认模型必须对 Agent 可用）；
// 同 Provider 仅一个默认由后端事务保证，故改完后重新加载本组以同步其他行。
async function toggleDefault(row: ManagedModel, val: boolean) {
  const g = groups.value.find((x) => x.provider.id === row.provider_id)
  if (!g) return
  try {
    await updateModel(
      row.provider_id,
      row.id,
      val ? { is_default: true, enabled: true } : { is_default: false },
    )
    await loadGroup(g)
    message.success(val ? '已设为默认模型' : '已取消默认')
  } catch (e) {
    message.error((e as Error).message)
  }
}

const columns: DataTableColumns<ManagedModel> = [
  { title: '模型 ID', key: 'model_id', minWidth: 200, ellipsis: { tooltip: true } },
  { title: '名称', key: 'name', minWidth: 140, ellipsis: { tooltip: true } },
  {
    title: '归属',
    key: 'owned_by',
    width: 140,
    render(row) {
      return h(NText, { depth: 3 }, { default: () => row.owned_by || '-' })
    },
  },
  {
    title: '启用',
    key: 'enabled',
    width: 90,
    render(row) {
      return h(NSwitch, {
        value: row.enabled,
        disabled: row.is_default,
        onUpdateValue: (v: boolean) => toggleEnabled(row, v),
      })
    },
  },
  {
    title: '默认',
    key: 'is_default',
    width: 90,
    render(row) {
      return h(NSwitch, {
        value: row.is_default,
        onUpdateValue: (v: boolean) => toggleDefault(row, v),
      })
    },
  },
]

onMounted(async () => {
  loading.value = true
  try {
    const ps = await listProviders()
    groups.value = ps.map((p) => ({
      provider: p,
      models: [],
      loading: false,
      syncing: false,
      error: '',
      cached: false,
      synced: 0,
    }))
    await Promise.all(groups.value.map(loadGroup))
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <n-card
    :bordered="false"
    class="h-full"
    content-style="display:flex;flex-direction:column;height:100%"
  >
    <div class="flex items-center mb-3">
      <div>
        <h2 class="text-lg font-semibold m-0">Model 管理</h2>
        <n-text depth="3" class="text-sm">
          按 Provider 分组，手动同步上游模型并启用/禁用（默认模型将对 Agent 可用）
        </n-text>
      </div>
    </div>

    <n-scrollbar v-if="!loading || groups.length" class="flex-1 -mr-3 pr-3">
      <n-space vertical :size="16">
        <n-card
          v-for="g in groups"
          :key="g.provider.id"
          size="small"
          :bordered="false"
          class="bg-gray-50 dark:bg-gray-800/40"
        >
          <div class="flex items-center mb-2">
            <n-tag :type="protocolType(g.provider.protocol)" size="small" :bordered="false">
              {{ g.provider.protocol }}
            </n-tag>
            <span class="font-medium ml-2">{{ g.provider.name }}</span>
            <n-space :size="6" class="ml-2">
              <n-tag v-if="g.cached" size="small" :bordered="false">缓存命中</n-tag>
              <n-tag v-if="g.synced" size="small" :bordered="false" type="success">
                已同步 {{ g.synced }}
              </n-tag>
            </n-space>
            <n-button
              size="small"
              class="ml-auto"
              :loading="g.syncing"
              @click="syncGroup(g)"
            >
              刷新模型
            </n-button>
          </div>

          <n-empty
            v-if="g.error"
            :description="`加载失败：${g.error}`"
            size="small"
            class="py-4"
          />
          <n-empty
            v-else-if="!g.loading && g.models.length === 0"
            description="暂无模型，点击「刷新模型」从 Provider 拉取"
            size="small"
            class="py-4"
          />
          <n-data-table
            v-else
            :columns="columns"
            :data="g.models"
            :loading="g.loading"
            :row-key="(row: ManagedModel) => row.id"
            size="small"
            :max-height="360"
          />
        </n-card>

        <n-empty v-if="!loading && groups.length === 0" description="你还没有任何 Provider，请先到 Provider 管理页创建" />
      </n-space>
    </n-scrollbar>
  </n-card>
</template>
