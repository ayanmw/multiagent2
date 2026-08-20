<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import {
  NEmpty,
} from 'naive-ui/es/empty'
import { renderMarkdown } from '@/utils/markdown'

// 前端对话消息（与 ChatView 共享的消息结构）。
export interface ChatMsg {
  id: string
  role: 'user' | 'assistant'
  content: string
  streaming?: boolean
  error?: boolean
  toolCalls?: ToolCall[]
}

// 工具调用片段：name 为工具名（如 shell/file_write/git_commit），args 为入参原文。
export interface ToolCall {
  id: string
  name: string
  args: string
  done: boolean
}

// 消息区：纯展示组件（含空态 / 气泡 / 工具调用折叠 / Markdown 渲染 / 错误提示）。
const props = defineProps<{
  messages: ChatMsg[]
  hasSession: boolean
}>()

const scrollRef = ref<HTMLElement | null>(null)

// 消息流式输出时持续跟随滚动到底部（纯 UI 行为，无需父组件参与）。
watch(
  () => props.messages,
  () => {
    nextTick(() => {
      const el = scrollRef.value
      if (el) el.scrollTop = el.scrollHeight
    })
  },
  { deep: true },
)

// 工具入参美化：尝试按 JSON 格式化，失败则原样返回。
function formatArgs(raw: string): string {
  const s = raw.trim()
  if (!s) return ''
  try {
    return JSON.stringify(JSON.parse(s), null, 2)
  } catch {
    return s
  }
}
</script>

<template>
  <div ref="scrollRef" class="flex-1 overflow-auto px-4 py-4">
    <div v-if="messages.length === 0" class="h-full flex items-center justify-center">
      <n-empty :description="hasSession ? '开始你的第一条消息吧' : '点击「新建对话」开始'" />
    </div>
    <div v-else class="flex flex-col gap-4 max-w-3xl mx-auto">
      <div
        v-for="m in messages"
        :key="m.id"
        class="flex"
        :class="m.role === 'user' ? 'justify-end' : 'justify-start'"
      >
        <div
          class="max-w-[75%] rounded-lg px-3 py-2 text-sm break-words"
          :class="
            m.role === 'user'
              ? 'bg-blue-500 text-white'
              : 'bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700'
          "
        >
          <div v-if="m.role === 'user'" class="whitespace-pre-wrap">{{ m.content }}</div>
          <template v-else>
            <!-- Agent 实际调用的工具（命令/文件操作/git 等），以可折叠 details 展示 -->
            <div v-if="m.toolCalls && m.toolCalls.length" class="flex flex-col gap-1.5 mb-2">
              <details
                v-for="tc in m.toolCalls"
                :key="tc.id"
                class="rounded bg-gray-50 dark:bg-gray-900/60 border border-gray-200 dark:border-gray-700"
              >
                <summary
                  class="cursor-pointer px-2 py-1 text-xs flex items-center gap-1.5 select-none"
                >
                  <span>🔧</span>
                  <code class="font-mono text-blue-600 dark:text-blue-300">{{ tc.name }}</code>
                  <span v-if="!tc.done" class="text-amber-500">执行中…</span>
                  <span v-else class="text-gray-400">✓</span>
                </summary>
                <pre
                  class="text-[11px] leading-relaxed px-2 pb-2 overflow-auto max-h-60"
                  >{{ formatArgs(tc.args) }}</pre
                >
              </details>
            </div>
            <div v-if="m.content" class="md-content" v-html="renderMarkdown(m.content)"></div>
            <div v-else class="flex items-center gap-1 text-gray-400">
              <span class="inline-block w-2 h-2 rounded-full bg-gray-400 animate-pulse"></span>
              正在思考…
            </div>
          </template>
          <div v-if="m.error" class="text-red-500 text-xs mt-1">
            ⚠️ 生成出错，结果可能不完整
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.md-content :deep(p) {
  margin: 0 0 0.5rem;
}
.md-content :deep(p:last-child) {
  margin-bottom: 0;
}
.md-content :deep(pre) {
  background: #1e1e1e;
  color: #d4d4d4;
  padding: 0.75rem 1rem;
  border-radius: 0.5rem;
  overflow: auto;
  margin: 0.5rem 0;
}
.md-content :deep(code) {
  font-family: 'JetBrains Mono', Consolas, Monaco, monospace;
  font-size: 0.85em;
}
.md-content :deep(:not(pre) > code) {
  background: rgba(127, 127, 127, 0.18);
  padding: 0.1em 0.35em;
  border-radius: 0.3em;
}
.md-content :deep(ul),
.md-content :deep(ol) {
  padding-left: 1.25rem;
  margin: 0.5rem 0;
}
.md-content :deep(li) {
  margin: 0.15rem 0;
}
.md-content :deep(h1),
.md-content :deep(h2),
.md-content :deep(h3) {
  margin: 0.6rem 0 0.4rem;
  font-weight: 600;
}
.md-content :deep(a) {
  color: #3b82f6;
  text-decoration: underline;
}
.md-content :deep(table) {
  border-collapse: collapse;
  margin: 0.5rem 0;
}
.md-content :deep(th),
.md-content :deep(td) {
  border: 1px solid #888;
  padding: 0.3rem 0.5rem;
}
</style>
