<script setup lang="ts">
import {
  NButton,
} from 'naive-ui/es/button'
import {
  NInput,
} from 'naive-ui/es/input'
import {
  NTag,
} from 'naive-ui/es/tag'
import { nextTick, ref, watch } from 'vue'
import type { Command } from '@/api/command'

// 对话输入区：斜杠命令浮层 + 多行输入 + 发送/停止。
// 职责：纯 UI 交互（命令浮层渲染、键盘导航、Enter 发送）；命令解析与数据流由父组件编排。
const props = defineProps<{
  modelValue: string
  streaming: boolean
  disabled: boolean
  commands: Command[]
  filteredCommands: Command[]
  showPalette: boolean
  highlightIndex: number
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'send'): void
  (e: 'stop'): void
  (e: 'nav', delta: number): void
  (e: 'select-command', index: number): void
  (e: 'dismiss'): void
}>()

const inputRef = ref<InstanceType<typeof NInput> | null>(null)

// 行为保真（原 ChatView）：选中「带参命令」（父回填 `/name `）后自动聚焦输入框继续填参。
watch(
  () => props.modelValue,
  (v, prev) => {
    if (v.startsWith('/') && v.endsWith(' ') && v !== prev) {
      nextTick(() => inputRef.value?.focus())
    }
  },
)

// 命令分类着色（与后端 CategorySystem/Workspace/Agent 对齐）。
function categoryType(c: Command): 'default' | 'warning' | 'success' {
  if (c.category === 'workspace') return 'warning'
  if (c.category === 'agent') return 'success'
  return 'default'
}

// 输入框键盘事件：浮层打开时优先做命令导航，否则 Enter 发送。
function onKeydown(e: KeyboardEvent) {
  const paletteOpen =
    props.showPalette && props.filteredCommands.length > 0 && props.modelValue.trim().length > 1
  if (paletteOpen) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      emit('nav', 1)
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      emit('nav', -1)
      return
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      emit('select-command', props.highlightIndex)
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      emit('dismiss')
      return
    }
  }
  // 否则 Enter 发送，Shift+Enter 换行。
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    emit('send')
  }
}
</script>

<template>
  <footer class="px-4 py-3 border-t border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800">
    <div class="max-w-3xl mx-auto relative flex items-end gap-2">
      <!-- 斜杠命令浮层（输入框以 / 开头时弹出） -->
      <div
        v-if="showPalette && filteredCommands.length"
        class="absolute bottom-full left-0 right-0 mb-2 rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 shadow-lg overflow-hidden z-10"
      >
        <div
          v-for="(cmd, i) in filteredCommands"
          :key="cmd.name"
          @click="emit('select-command', i)"
          @mouseenter="emit('nav', i - highlightIndex)"
          class="flex items-start gap-2 px-3 py-2 cursor-pointer border-b border-gray-100 dark:border-gray-700/60 last:border-b-0"
          :class="
            i === highlightIndex ? 'bg-blue-50 dark:bg-blue-900/30' : 'hover:bg-gray-50 dark:hover:bg-gray-700/40'
          "
        >
          <n-tag size="small" :bordered="false" :type="categoryType(cmd)" class="mt-0.5 shrink-0">
            {{ cmd.usage }}
          </n-tag>
          <div class="min-w-0">
            <div class="text-xs text-gray-500 dark:text-gray-400 truncate">
              {{ cmd.description }}
            </div>
            <div v-if="cmd.args && cmd.args.length" class="text-[11px] text-gray-400 mt-0.5">
              参数：<code class="font-mono">{{ cmd.args.map((a) => a.name).join(' ') }}</code>
            </div>
          </div>
        </div>
      </div>

      <n-input
        ref="inputRef"
        :value="modelValue"
        type="textarea"
        :autosize="{ minRows: 1, maxRows: 5 }"
        placeholder="输入消息，Enter 发送，Shift+Enter 换行；输入 / 唤起命令（/run /review /plan /clear /model /workspace）"
        @update:value="(v: string) => emit('update:modelValue', v)"
        @keydown="onKeydown"
      />
      <n-button v-if="!streaming" type="primary" :disabled="disabled || !modelValue.trim()" @click="emit('send')"
        >发送</n-button
      >
      <n-button v-else type="error" @click="emit('stop')">停止</n-button>
    </div>
  </footer>
</template>
