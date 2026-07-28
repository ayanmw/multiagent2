<script setup lang="ts">
import { ref, computed, onMounted, nextTick } from 'vue'
import {
  NButton,
  NInput,
  NPopselect,
  NEmpty,
  NScrollbar,
  NTag,
  useMessage,
} from 'naive-ui'
import {
  listSessions,
  createSession,
  getSession,
  type SessionView,
  type MessageView,
} from '@/api/session'
import {
  listEnabledModels,
  streamChat,
  type EnabledModel,
  type AGUIEvent,
} from '@/api/chat'
import { renderMarkdown } from '@/utils/markdown'

// 一条前端对话消息（id 仅本地用于渲染 key）。
interface ChatMsg {
  id: string
  role: 'user' | 'assistant'
  content: string
  streaming?: boolean
  error?: boolean
}

const message = useMessage()

const sessions = ref<SessionView[]>([])
const activeKey = ref<string | null>(null)
const messages = ref<ChatMsg[]>([])
const input = ref('')
const models = ref<EnabledModel[]>([])
const selectedModelId = ref<number | null>(null)
const streaming = ref(false)
const abortController = ref<AbortController | null>(null)
const scrollRef = ref<HTMLElement | null>(null)

// 当前选中的会话对象（用于顶部标题显示）。
const activeSession = computed(
  () => sessions.value.find((s) => s.session_key === activeKey.value) ?? null,
)
// 模型选择器选项。
const modelOptions = computed(() =>
  models.value.map((m) => ({ label: `${m.name} · ${m.provider_name}`, value: m.id })),
)
// 当前选中的模型（选中项为空时走后端默认模型）。
const currentModel = computed(() => models.value.find((m) => m.id === selectedModelId.value) ?? null)
// 默认模型（后端标记 is_default），用于未显式选择时的 Provider 展示。
const defaultModel = computed(() => models.value.find((m) => m.is_default) ?? null)
// 工具条展示的模型标签。
const currentModelLabel = computed(() => {
  if (currentModel.value) return `${currentModel.value.name} · ${currentModel.value.provider_name}`
  return models.value.length ? '默认模型（自动选择）' : '无可用模型'
})
// 工具条展示的 Provider 名称（未显式选模型时取默认模型所属 Provider）。
const currentProviderName = computed(() => {
  if (currentModel.value) return currentModel.value.provider_name
  return defaultModel.value ? defaultModel.value.provider_name : '—'
})

// 拉取会话列表与可用模型。
async function loadSessions() {
  sessions.value = await listSessions()
}
async function loadModels() {
  try {
    models.value = await listEnabledModels()
  } catch (e) {
    message.error((e as Error).message)
  }
}

// 切换会话：加载其历史消息（服务端已持久化）。
async function selectSession(key: string) {
  activeKey.value = key
  messages.value = []
  try {
    const detail = await getSession(key)
    messages.value = detail.messages.map((m: MessageView, i) => ({
      id: `${key}-${i}-${m.id}`,
      role: m.role,
      content: m.content,
    }))
  } catch (e) {
    message.error((e as Error).message)
  }
  scrollToBottom()
}

// 新建会话并立即选中。
async function newSession() {
  const s = await createSession()
  sessions.value = [s, ...sessions.value]
  await selectSession(s.session_key)
}

// 发送消息：追加用户消息 + 助手占位，再消费 SSE 流式回复。
async function send() {
  const text = input.value.trim()
  if (!text || !activeKey.value || streaming.value) return
  // 支持 /clear 命令：清空当前会话上下文（不发送给模型）。
  if (text === '/clear') {
    input.value = ''
    clearContext()
    return
  }
  if (models.value.length === 0) {
    message.warning('请先在「Model 管理」启用至少一个模型')
    return
  }

  messages.value.push({ id: `u-${Date.now()}`, role: 'user', content: text })
  const assistantId = `a-${Date.now()}`
  messages.value.push({ id: assistantId, role: 'assistant', content: '', streaming: true })
  input.value = ''
  scrollToBottom()

  streaming.value = true
  const ac = new AbortController()
  abortController.value = ac
  try {
    await streamChat({
      sessionKey: activeKey.value,
      message: text,
      modelId: selectedModelId.value ?? undefined,
      signal: ac.signal,
      onEvent: (ev: AGUIEvent) => onEvent(ev, assistantId),
    })
  } catch (e) {
    const am = messages.value.find((m) => m.id === assistantId)
    if (am) {
      am.streaming = false
      am.error = true
      am.content += `\n\n⚠️ ${(e as Error).message}`
    }
  } finally {
    const am = messages.value.find((m) => m.id === assistantId)
    if (am) am.streaming = false
    streaming.value = false
    abortController.value = null
    scrollToBottom()
  }
}

// 处理单条 AG-UI 事件，更新助手消息内容。
function onEvent(ev: AGUIEvent, assistantId: string) {
  const am = messages.value.find((m) => m.id === assistantId)
  if (!am) return
  switch (ev.type) {
    case 'TEXT_MESSAGE_CONTENT':
      am.content += ev.delta ?? ''
      scrollToBottom()
      break
    case 'RUN_ERROR':
      am.error = true
      am.content += `\n\n⚠️ ${ev.message ?? '对话出错'}`
      break
    case 'RUN_FINISHED':
      am.streaming = false
      break
    default:
      break
  }
}

// 停止当前生成。
function stopStreaming() {
  abortController.value?.abort()
}

// 清空当前会话的本地上下文（前端展示重置）。
// 说明：引擎每次请求新建 Runner（内存会话不跨请求保留），故清空展示即等价于上下文重置；
// 后续消息不会携带历史，模型视角已无上下文。
function clearContext() {
  messages.value = []
  message.success('上下文已清空')
}

// Enter 发送，Shift+Enter 换行。
function onEnter(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    send()
  }
}

// 滚动到底部（流式输出时持续跟随）。
function scrollToBottom() {
  nextTick(() => {
    const el = scrollRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

onMounted(async () => {
  await loadModels()
  await loadSessions()
  if (sessions.value.length > 0) {
    await selectSession(sessions.value[0].session_key)
  }
})
</script>

<template>
  <div class="h-full -m-4 flex bg-gray-50 dark:bg-gray-900">
    <!-- 左侧会话列表 -->
    <aside
      class="w-64 shrink-0 flex flex-col border-r border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800"
    >
      <div class="p-3 border-b border-gray-200 dark:border-gray-700">
        <n-button block type="primary" @click="newSession">+ 新建对话</n-button>
      </div>
      <n-scrollbar class="flex-1">
        <div v-if="sessions.length === 0" class="p-4">
          <n-empty description="还没有对话" size="small" />
        </div>
        <ul class="py-2">
          <li
            v-for="s in sessions"
            :key="s.session_key"
            @click="selectSession(s.session_key)"
            class="px-3 py-2 cursor-pointer text-sm truncate border-l-2"
            :class="
              s.session_key === activeKey
                ? 'border-blue-500 bg-blue-50 dark:bg-blue-900/30 text-blue-600 dark:text-blue-300'
                : 'border-transparent hover:bg-gray-100 dark:hover:bg-gray-700/50'
            "
          >
            {{ s.title }}
          </li>
        </ul>
      </n-scrollbar>
    </aside>

    <!-- 右侧对话区 -->
    <main class="flex-1 flex flex-col min-w-0">
      <!-- 顶部工具条：会话标题 -->
      <header
        class="flex items-center gap-3 px-4 py-2 border-b border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800"
      >
        <n-tag v-if="activeSession" size="small" :bordered="false" type="info">
          {{ activeSession.title }}
        </n-tag>
        <span class="ml-auto text-xs text-gray-400">对话工作台 · 输入 /clear 可重置上下文</span>
      </header>

      <!-- 对话工具栏：当前模型/Provider 可点击切换 + 清空上下文 -->
      <div
        class="flex items-center gap-3 px-4 py-2 border-b border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800/60 text-sm"
      >
        <span class="text-gray-500 dark:text-gray-400">当前模型</span>
        <n-popselect
          v-model:value="selectedModelId"
          :options="modelOptions"
          :disabled="models.length === 0"
          trigger="click"
          size="small"
          placement="bottom-start"
        >
          <n-tag :bordered="false" type="success" class="cursor-pointer select-none">
            🤖 {{ currentModelLabel }}
            <span class="ml-1 opacity-60">▾</span>
          </n-tag>
        </n-popselect>
        <span class="text-gray-500 dark:text-gray-400">
          Provider:
          <span class="font-medium text-gray-700 dark:text-gray-200">{{ currentProviderName }}</span>
        </span>
        <div class="ml-auto">
          <n-button size="small" tertiary @click="clearContext">清空上下文</n-button>
        </div>
      </div>

      <!-- 消息区 -->
      <div ref="scrollRef" class="flex-1 overflow-auto px-4 py-4">
        <div v-if="messages.length === 0" class="h-full flex items-center justify-center">
          <n-empty :description="activeKey ? '开始你的第一条消息吧' : '点击「新建对话」开始'" />
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
              <div
                v-else-if="m.content"
                class="md-content"
                v-html="renderMarkdown(m.content)"
              ></div>
              <div v-else class="flex items-center gap-1 text-gray-400">
                <span class="inline-block w-2 h-2 rounded-full bg-gray-400 animate-pulse"></span>
                正在思考…
              </div>
              <div v-if="m.error" class="text-red-500 text-xs mt-1">
                ⚠️ 生成出错，结果可能不完整
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 输入区 -->
      <footer
        class="px-4 py-3 border-t border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800"
      >
        <div class="max-w-3xl mx-auto flex items-end gap-2">
          <n-input
            v-model:value="input"
            type="textarea"
            :autosize="{ minRows: 1, maxRows: 5 }"
            placeholder="输入消息，Enter 发送，Shift+Enter 换行；输入 /clear 清空上下文"
            @keydown="onEnter"
          />
          <n-button
            v-if="!streaming"
            type="primary"
            :disabled="!input.trim() || !activeKey"
            @click="send"
            >发送</n-button
          >
          <n-button v-else type="error" @click="stopStreaming">停止</n-button>
        </div>
      </footer>
    </main>
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
