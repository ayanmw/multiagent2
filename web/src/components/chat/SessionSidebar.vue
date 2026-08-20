<script setup lang="ts">
import {
  NButton,
} from 'naive-ui/es/button'
import {
  NEmpty,
} from 'naive-ui/es/empty'
import {
  NPopconfirm,
} from 'naive-ui/es/popconfirm'
import {
  NScrollbar,
} from 'naive-ui/es/scrollbar'
import type { SessionView } from '@/api/session'

// 左侧会话列表侧栏：新建 / 选择 / 重命名 / 删除（纯展示 + 轻交互，数据流由父组件编排）。
defineProps<{
  sessions: SessionView[]
  activeKey: string | null
}>()

const emit = defineEmits<{
  (e: 'select', key: string): void
  (e: 'create'): void
  (e: 'rename', payload: { key: string; title: string }): void
  (e: 'delete', key: string): void
}>()

// 重命名：window.prompt 收集新标题后交父组件处理（父负责 API 调用与列表更新）。
function onRename(key: string, current: string) {
  const title = window.prompt('重命名会话', current)
  if (title == null) return
  const t = title.trim()
  if (!t || t === current) return
  emit('rename', { key, title: t })
}
</script>

<template>
  <aside
    class="w-64 shrink-0 flex flex-col border-r border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800"
  >
    <div class="p-3 border-b border-gray-200 dark:border-gray-700">
      <n-button block type="primary" @click="emit('create')">+ 新建对话</n-button>
    </div>
    <n-scrollbar class="flex-1">
      <div v-if="sessions.length === 0" class="p-4">
        <n-empty description="还没有对话" size="small" />
      </div>
      <ul class="py-2">
        <li
          v-for="s in sessions"
          :key="s.session_key"
          @click="emit('select', s.session_key)"
          class="group px-3 py-2 cursor-pointer text-sm truncate border-l-2"
          :class="
            s.session_key === activeKey
              ? 'border-blue-500 bg-blue-50 dark:bg-blue-900/30 text-blue-600 dark:text-blue-300'
              : 'border-transparent hover:bg-gray-100 dark:hover:bg-gray-700/50'
          "
        >
          <div class="flex items-center gap-1">
            <span class="flex-1 truncate">{{ s.title }}</span>
            <span class="hidden group-hover:flex items-center gap-1 shrink-0" @click.stop>
              <n-button
                size="tiny"
                quaternary
                title="重命名"
                @click="onRename(s.session_key, s.title)"
                >✎</n-button
              >
              <n-popconfirm @positive-click="emit('delete', s.session_key)">
                <template #trigger>
                  <n-button size="tiny" quaternary type="error" title="删除">🗑</n-button>
                </template>
                确认删除该会话？此操作不可撤销。
              </n-popconfirm>
            </span>
          </div>
        </li>
      </ul>
    </n-scrollbar>
  </aside>
</template>
