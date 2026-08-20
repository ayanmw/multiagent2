<script setup lang="ts">
import {
  NButton,
} from 'naive-ui/es/button'
import {
  NPopselect,
} from 'naive-ui/es/popselect'
import {
  NTag,
} from 'naive-ui/es/tag'
import type { SelectOption } from 'naive-ui/es/select'

// 对话页顶部工具条：会话标题 + 工作区 / 模型 / Provider 选择 + 运行状态 / 清空上下文。
// 纯展示组件，所有变更以事件上抛，由 ChatView 统一编排数据流。
defineProps<{
  title: string
  workspaceKey: string | null
  workspaceLabel: string
  workspaceOptions: SelectOption[]
  selectedModelId: number | null
  modelOptions: SelectOption[]
  modelsEmpty: boolean
  modelLabel: string
  providerName: string
}>()

const emit = defineEmits<{
  (e: 'workspace-change', value: string | null): void
  (e: 'model-change', value: number | null): void
  (e: 'open-state'): void
  (e: 'clear-context'): void
}>()
</script>

<template>
  <div>
    <!-- 顶部工具条：会话标题 -->
    <header
      class="flex items-center gap-3 px-4 py-2 border-b border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800"
    >
      <n-tag v-if="title" size="small" :bordered="false" type="info">
        {{ title }}
      </n-tag>
      <span v-if="workspaceLabel" class="text-xs text-amber-500">
        📁 {{ workspaceLabel }}
      </span>
      <span class="ml-auto text-xs text-gray-400">对话工作台 · 输入 / 唤起命令</span>
    </header>

    <!-- 对话工具栏：工作区 / 当前模型 / Provider 可点击切换 + 清空上下文（MX-01 工作区选择） -->
    <div
      class="flex items-center gap-3 px-4 py-2 border-b border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800/60 text-sm"
    >
      <span class="text-gray-500 dark:text-gray-400">工作区</span>
      <n-popselect
        :value="workspaceKey ?? ''"
        :options="workspaceOptions"
        trigger="click"
        size="small"
        placement="bottom-start"
        @update:value="(v: string | null) => emit('workspace-change', v)"
      >
        <n-tag :bordered="false" type="warning" class="cursor-pointer select-none">
          📁 {{ workspaceKey ? workspaceKey : '默认目录' }}
          <span class="ml-1 opacity-60">▾</span>
        </n-tag>
      </n-popselect>
      <span class="text-gray-500 dark:text-gray-400">当前模型</span>
      <n-popselect
        :value="selectedModelId ?? undefined"
        :options="modelOptions"
        :disabled="modelsEmpty"
        trigger="click"
        size="small"
        placement="bottom-start"
        @update:value="(v: number | null) => emit('model-change', v)"
      >
        <n-tag :bordered="false" type="success" class="cursor-pointer select-none">
          🤖 {{ modelLabel }}
          <span class="ml-1 opacity-60">▾</span>
        </n-tag>
      </n-popselect>
      <span class="text-gray-500 dark:text-gray-400">
        Provider:
        <span class="font-medium text-gray-700 dark:text-gray-200">{{ providerName }}</span>
      </span>
      <div class="ml-auto flex items-center gap-2">
        <n-button size="small" tertiary @click="emit('open-state')">运行状态</n-button>
        <n-button size="small" tertiary @click="emit('clear-context')">清空上下文</n-button>
      </div>
    </div>
  </div>
</template>
