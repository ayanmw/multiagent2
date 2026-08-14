<script setup lang="ts">
import { ref, computed, onMounted, nextTick, watch } from 'vue'
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
  deleteSession,
  renameSession,
  bindSessionWorkspace,
  type SessionView,
  type MessageView,
} from '@/api/session'
import {
  listEnabledModels,
  streamChat,
  getSessionState,
  type EnabledModel,
  type AGUIEvent,
  type SessionState,
} from '@/api/chat'
import {
  fetchCommands,
  resolveSlashCommand,
  renderCommandPrompt,
  type Command,
} from '@/api/command'
import { listWorkspaces, type Workspace } from '@/api/workspace'
import { renderMarkdown } from '@/utils/markdown'

// 一条前端对话消息（id 仅本地用于渲染 key）。
interface ChatMsg {
  id: string
  role: 'user' | 'assistant'
  content: string
  streaming?: boolean
  error?: boolean
  // 本轮 Agent 调用过的工具（后端以 SSE 事件流推送，此前被前端丢弃，现补全展示）。
  toolCalls?: ToolCall[]
}

// 工具调用片段：name 为工具名（如 shell/file_write/git_commit），args 为入参原文。
interface ToolCall {
  id: string
  name: string
  args: string
  done: boolean
}

const message = useMessage()

const sessions = ref<SessionView[]>([])
const activeKey = ref<string | null>(null)
const messages = ref<ChatMsg[]>([])
const input = ref('')
const models = ref<EnabledModel[]>([])
const selectedModelId = ref<number | null>(null)
const selectedWorkspaceKey = ref<string | null>(null)
// 当前用户的工作区列表（MX-01：对话页下拉选择器数据源）。
const workspaces = ref<Workspace[]>([])
const streaming = ref(false)
const abortController = ref<AbortController | null>(null)
const scrollRef = ref<HTMLElement | null>(null)
const inputRef = ref<InstanceType<typeof NInput> | null>(null)

// 斜杠命令注册表（M1-15，来自后端 GET /api/commands）。
const commands = ref<Command[]>([])
// 命令浮层是否临时关闭（用户按 Esc 后，重新清空/输入 / 再开启）。
const dismissPalette = ref(false)
// 浮层中高亮项索引。
const highlightIndex = ref(0)

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
// 工具条展示的已绑定工作区标签（优先显示名称，回退到 key）。
const currentWorkspaceLabel = computed(() => {
  if (!selectedWorkspaceKey.value) return ''
  const w = workspaces.value.find((x) => x.key === selectedWorkspaceKey.value)
  return w ? (w.name ? `${w.name} · ${w.key}` : w.key) : selectedWorkspaceKey.value
})
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

// 命令浮层可见条件：输入框以 / 开头且仍处于「命令名输入阶段」（尚未输入空格进入填参）。
const showPalette = computed(() => {
  if (dismissPalette.value) return false
  const t = input.value
  if (!t.startsWith('/')) return false
  return !t.slice(1).includes(' ')
})
// 根据已输入的命令名前缀过滤命令。
const filteredCommands = computed(() => {
  const t = input.value
  if (!t.startsWith('/')) return commands.value
  const q = t.slice(1).toLowerCase()
  if (!q) return commands.value
  return commands.value.filter((c) => c.name.startsWith(q))
})
// 命令分类着色（与后端 CategorySystem/Workspace/Agent 对齐）。
function categoryType(c: Command): 'default' | 'warning' | 'success' {
  if (c.category === 'workspace') return 'warning'
  if (c.category === 'agent') return 'success'
  return 'default'
}

// 拉取会话列表与可用模型与命令注册表。
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
async function loadCommands() {
  try {
    commands.value = await fetchCommands()
  } catch (e) {
    message.error(`命令列表加载失败：${(e as Error).message}`)
  }
}
async function loadWorkspaces() {
  try {
    workspaces.value = await listWorkspaces()
  } catch (e) {
    message.error(`工作区列表加载失败：${(e as Error).message}`)
  }
}

// ---- 会话「运行状态」外置文件（PLAN/PROGRESS/LEARNINGS，M1-16）查看 ----
const showState = ref(false)
const stateLoading = ref(false)
const sessionState = ref<SessionState>({ exists: false })

async function loadSessionState() {
  if (!activeKey.value) {
    message.warning('请先选择一个会话')
    return
  }
  stateLoading.value = true
  showState.value = true
  try {
    sessionState.value = await getSessionState(activeKey.value)
    if (!sessionState.value.exists) {
      message.info('该会话暂未产生可续跑的工作状态文件')
    }
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    stateLoading.value = false
  }
}

// ---- 会话管理：删除 / 重命名 ----
async function handleDeleteSession(key: string) {
  try {
    await deleteSession(key)
    sessions.value = sessions.value.filter((s) => s.session_key !== key)
    if (activeKey.value === key) {
      activeKey.value = null
      messages.value = []
    }
    message.success('会话已删除')
  } catch (e) {
    message.error((e as Error).message)
  }
}

async function handleRenameSession(key: string, current: string) {
  const title = window.prompt('重命名会话', current)
  if (title == null) return
  const t = title.trim()
  if (!t || t === current) return
  try {
    const updated = await renameSession(key, t)
    const idx = sessions.value.findIndex((s) => s.session_key === key)
    if (idx >= 0) sessions.value[idx] = { ...sessions.value[idx], title: updated.title }
    message.success('已重命名')
  } catch (e) {
    message.error((e as Error).message)
  }
}

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

// 切换会话：重置绑定状态并加载其历史消息（服务端已持久化）。
async function selectSession(key: string) {
  activeKey.value = key
  selectedWorkspaceKey.value = null
  messages.value = []
  try {
    const detail = await getSession(key)
    messages.value = detail.messages.map((m: MessageView, i) => ({
      id: `${key}-${i}-${m.id}`,
      role: m.role,
      content: m.content,
    }))
    // MX-01：还原该会话已绑定的工作区（刷新后仍保留，因后端 GET 详情已暴露 workspace_key）。
    selectedWorkspaceKey.value = detail.workspace_key ?? null
  } catch (e) {
    message.error((e as Error).message)
  }
  scrollToBottom()
}

// 工作区下拉选项：首项为「默认目录（不绑定）」，其后为当前用户的各 workspace。
const workspaceOptions = computed(() => [
  { label: '默认目录（不绑定）', value: '' },
  ...workspaces.value.map((w) => ({
    label: w.name ? `${w.name} · ${w.key}` : w.key,
    value: w.key,
  })),
])

// 切换工作区：写入本地状态并经后端 PATCH 持久化绑定（即使尚未发消息，刷新后仍保留）。
async function onWorkspaceChange(val: string | null) {
  selectedWorkspaceKey.value = val || null
  if (!activeKey.value) return
  try {
    await bindSessionWorkspace(activeKey.value, selectedWorkspaceKey.value)
  } catch (e) {
    message.error(`绑定工作区失败：${(e as Error).message}`)
  }
}

// 新建会话并立即选中。
async function newSession() {
  const s = await createSession()
  sessions.value = [s, ...sessions.value]
  await selectSession(s.session_key)
}

// 发送渲染后的提示词或普通文本（client 命令与正常消息共用此路径）。
async function sendMessage(text: string) {
  if (!activeKey.value || streaming.value) return
  if (models.value.length === 0) {
    message.warning('请先在「Model 管理」启用至少一个模型')
    return
  }

  messages.value.push({ id: `u-${Date.now()}`, role: 'user', content: text })
  const assistantId = `a-${Date.now()}`
  messages.value.push({ id: assistantId, role: 'assistant', content: '', streaming: true })
  input.value = ''
  dismissPalette.value = false
  scrollToBottom()

  streaming.value = true
  const ac = new AbortController()
  abortController.value = ac
  try {
    await streamChat({
      sessionKey: activeKey.value,
      message: text,
      modelId: selectedModelId.value ?? undefined,
      workspaceKey: selectedWorkspaceKey.value ?? undefined,
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

// 执行已解析的斜杠命令（三类 Kind 分流）。
function executeSlashCommand(cmd: Command, args: string) {
  switch (cmd.kind) {
    case 'client':
      if (cmd.name === 'clear') clearContext()
      else if (cmd.name === 'model') applyModelCommand(args)
      else if (cmd.name === 'workspace') applyWorkspaceCommand(args)
      else message.info(`命令 /${cmd.name} 暂未在前端实现`)
      break
    case 'prompt': {
      const prompt = renderCommandPrompt(cmd, args)
      if (!prompt) {
        message.warning(`命令 /${cmd.name} 无可用模板`)
        return
      }
      sendMessage(prompt)
      break
    }
    case 'endpoint':
      message.info(`命令 /${cmd.name} 暂未在前端实现`)
      break
    default:
      message.info(`未知命令类型：${cmd.kind}`)
  }
}

// /model <model_id>：解析并切换当前对话模型（本地状态）。
function applyModelCommand(args: string) {
  const key = args.trim()
  if (!key) {
    message.warning('用法：/model <model_id>')
    return
  }
  const m = models.value.find((x) => String(x.id) === key || x.model_id === key)
  if (!m) {
    message.warning(`未找到模型：${key}`)
    return
  }
  selectedModelId.value = m.id
  message.success(`已切换模型：${m.name}`)
}

// /workspace <key>：绑定/取消绑定当前对话工作区（本地状态，发送时透传后端）。
function applyWorkspaceCommand(args: string) {
  const key = args.trim()
  selectedWorkspaceKey.value = key || null
  message.success(key ? `已绑定工作区：${key}` : '已取消工作区绑定（回退默认目录）')
}

// 主发送入口：先尝试解析斜杠命令，否则按普通消息发送。
function send() {
  const text = input.value.trim()
  if (!text || !activeKey.value || streaming.value) return

  // 保底：命令表未加载时仍支持 /clear。
  if (text === '/clear') {
    input.value = ''
    clearContext()
    return
  }

  const parsed = resolveSlashCommand(text, commands.value)
  if (parsed) {
    input.value = ''
    dismissPalette.value = false
    executeSlashCommand(parsed.command, parsed.args)
    return
  }

  sendMessage(text)
}

// 命令浮层：选中某命令后的行为。
// 有参数的命令（run/plan/model/workspace）→ 把「/name 」填回输入框等用户填参；
// 无参数的命令（clear/review）→ 直接执行。
function applyCommand(cmd: Command) {
  const hasArgs = !!cmd.args && cmd.args.length > 0
  if (!hasArgs) {
    input.value = ''
    dismissPalette.value = false
    executeSlashCommand(cmd, '')
    return
  }
  input.value = `/${cmd.name} `
  dismissPalette.value = false
  highlightIndex.value = 0
  nextTick(() => inputRef.value?.focus())
}

// 输入框键盘事件：浮层打开时优先做命令导航，否则 Enter 发送。
function onKeydown(e: KeyboardEvent) {
  if (showPalette.value && filteredCommands.value.length > 0 && input.value.trim().length > 1) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      highlightIndex.value = (highlightIndex.value + 1) % filteredCommands.value.length
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      highlightIndex.value =
        (highlightIndex.value - 1 + filteredCommands.value.length) % filteredCommands.value.length
      return
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      const cmd = filteredCommands.value[highlightIndex.value]
      if (cmd) applyCommand(cmd)
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      dismissPalette.value = true
      return
    }
  }
  // 否则 Enter 发送，Shift+Enter 换行。
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    send()
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
    // 工具调用：把 Agent 实际执行的命令/文件操作等过程展示出来。
    case 'TOOL_CALL_START': {
      if (!am.toolCalls) am.toolCalls = []
      am.toolCalls.push({
        id: ev.toolCallId ?? '',
        name: ev.toolCallName ?? 'tool',
        args: '',
        done: false,
      })
      break
    }
    case 'TOOL_CALL_ARGS': {
      const tc = am.toolCalls?.find((t) => t.id === ev.toolCallId)
      if (tc) tc.args += ev.delta ?? ''
      break
    }
    case 'TOOL_CALL_END': {
      const tc = am.toolCalls?.find((t) => t.id === ev.toolCallId)
      if (tc) tc.done = true
      break
    }
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

// 滚动到底部（流式输出时持续跟随）。
function scrollToBottom() {
  nextTick(() => {
    const el = scrollRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

// 输入变化时重置高亮（前缀变了，候选列表变了）。
watch(input, () => {
  highlightIndex.value = 0
})

onMounted(async () => {
  await loadModels()
  await loadCommands()
  await loadWorkspaces()
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
                <n-button size="tiny" quaternary title="重命名" @click="handleRenameSession(s.session_key, s.title)">✎</n-button>
                <n-popconfirm @positive-click="handleDeleteSession(s.session_key)">
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

    <!-- 右侧对话区 -->
    <main class="flex-1 flex flex-col min-w-0">
      <!-- 顶部工具条：会话标题 -->
      <header
        class="flex items-center gap-3 px-4 py-2 border-b border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800"
      >
        <n-tag v-if="activeSession" size="small" :bordered="false" type="info">
          {{ activeSession.title }}
        </n-tag>
        <span v-if="currentWorkspaceLabel" class="text-xs text-amber-500">
          📁 {{ currentWorkspaceLabel }}
        </span>
        <span class="ml-auto text-xs text-gray-400">对话工作台 · 输入 / 唤起命令</span>
      </header>

      <!-- 对话工具栏：工作区 / 当前模型 / Provider 可点击切换 + 清空上下文（MX-01 工作区选择） -->
      <div
        class="flex items-center gap-3 px-4 py-2 border-b border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800/60 text-sm"
      >
        <span class="text-gray-500 dark:text-gray-400">工作区</span>
        <n-popselect
          :value="selectedWorkspaceKey ?? ''"
          :options="workspaceOptions"
          trigger="click"
          size="small"
          placement="bottom-start"
          @update:value="onWorkspaceChange"
        >
          <n-tag :bordered="false" type="warning" class="cursor-pointer select-none">
            📁 {{ selectedWorkspaceKey ? selectedWorkspaceKey : '默认目录' }}
            <span class="ml-1 opacity-60">▾</span>
          </n-tag>
        </n-popselect>
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
        <div class="ml-auto flex items-center gap-2">
          <n-button size="small" tertiary @click="loadSessionState">运行状态</n-button>
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
              <template v-else>
                <!-- Agent 实际调用的工具（命令/文件操作/git 等），此前被前端静默丢弃，现补全展示 -->
                <div v-if="m.toolCalls && m.toolCalls.length" class="flex flex-col gap-1.5 mb-2">
                  <details
                    v-for="tc in m.toolCalls"
                    :key="tc.id"
                    class="rounded bg-gray-50 dark:bg-gray-900/60 border border-gray-200 dark:border-gray-700"
                  >
                    <summary class="cursor-pointer px-2 py-1 text-xs flex items-center gap-1.5 select-none">
                      <span>🔧</span>
                      <code class="font-mono text-blue-600 dark:text-blue-300">{{ tc.name }}</code>
                      <span v-if="!tc.done" class="text-amber-500">执行中…</span>
                      <span v-else class="text-gray-400">✓</span>
                    </summary>
                    <pre class="text-[11px] leading-relaxed px-2 pb-2 overflow-auto max-h-60">{{ formatArgs(tc.args) }}</pre>
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

      <!-- 输入区 -->
      <footer
        class="px-4 py-3 border-t border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800"
      >
        <div class="max-w-3xl mx-auto relative flex items-end gap-2">
          <!-- 斜杠命令浮层（输入框以 / 开头时弹出） -->
          <div
            v-if="showPalette && filteredCommands.length"
            class="absolute bottom-full left-0 right-0 mb-2 rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 shadow-lg overflow-hidden z-10"
          >
            <div
              v-for="(cmd, i) in filteredCommands"
              :key="cmd.name"
              @click="applyCommand(cmd)"
              @mouseenter="highlightIndex = i"
              class="flex items-start gap-2 px-3 py-2 cursor-pointer border-b border-gray-100 dark:border-gray-700/60 last:border-b-0"
              :class="i === highlightIndex ? 'bg-blue-50 dark:bg-blue-900/30' : 'hover:bg-gray-50 dark:hover:bg-gray-700/40'"
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
            v-model:value="input"
            type="textarea"
            :autosize="{ minRows: 1, maxRows: 5 }"
            placeholder="输入消息，Enter 发送，Shift+Enter 换行；输入 / 唤起命令（/run /review /plan /clear /model /workspace）"
            @keydown="onKeydown"
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

    <!-- 会话「运行状态」外置文件（PLAN/PROGRESS/LEARNINGS）查看面板 -->
    <n-modal
      v-model:show="showState"
      title="运行状态（Agent 工作计划与进展）"
      preset="card"
      style="width: 760px; max-width: 94vw"
    >
      <n-empty v-if="stateLoading" description="加载中…" />
      <n-empty v-else-if="!sessionState.exists" description="该会话暂未产生可续跑的工作状态文件" />
      <template v-else>
        <n-space vertical :size="12">
          <div v-if="sessionState.plan">
            <div class="text-sm font-semibold mb-1">📋 PLAN.md（计划 / 目标）</div>
            <n-scrollbar style="max-height: 220px">
              <pre class="text-xs bg-gray-50 dark:bg-gray-900 rounded p-2 whitespace-pre-wrap">{{ sessionState.plan }}</pre>
            </n-scrollbar>
          </div>
          <div v-if="sessionState.progress">
            <div class="text-sm font-semibold mb-1">📈 PROGRESS.md（进展日志）</div>
            <n-scrollbar style="max-height: 220px">
              <pre class="text-xs bg-gray-50 dark:bg-gray-900 rounded p-2 whitespace-pre-wrap">{{ sessionState.progress }}</pre>
            </n-scrollbar>
          </div>
          <div v-if="sessionState.learnings">
            <div class="text-sm font-semibold mb-1">💡 LEARNINGS.md（踩坑与约定）</div>
            <n-scrollbar style="max-height: 220px">
              <pre class="text-xs bg-gray-50 dark:bg-gray-900 rounded p-2 whitespace-pre-wrap">{{ sessionState.learnings }}</pre>
            </n-scrollbar>
          </div>
        </n-space>
      </template>
    </n-modal>
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
