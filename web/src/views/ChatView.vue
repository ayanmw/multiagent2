<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import {
  NButton,
} from 'naive-ui/es/button'
import {
  NEmpty,
} from 'naive-ui/es/empty'
import {
  NModal,
} from 'naive-ui/es/modal'
import {
  NScrollbar,
} from 'naive-ui/es/scrollbar'
import {
  NSpace,
} from 'naive-ui/es/space'
import {
  useMessage,
} from 'naive-ui/es/message'
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
import SessionSidebar from '@/components/chat/SessionSidebar.vue'
import ChatToolbar from '@/components/chat/ChatToolbar.vue'
import MessageList, { type ChatMsg } from '@/components/chat/MessageList.vue'
import ChatInput from '@/components/chat/ChatInput.vue'

// ChatMsg / ToolCall 类型已收敛到 MessageList 子组件（数据流仍由本视图编排）。

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
}

// 浮层导航：上下移动高亮（循环）。
function onNav(delta: number) {
  const len = filteredCommands.value.length
  if (!len) return
  highlightIndex.value = (highlightIndex.value + delta + len) % len
}

// 浮层选中：取当前高亮命令（带参命令回填输入框，无参命令直接执行）。
function onSelectCommand(index: number) {
  const cmd = filteredCommands.value[index]
  if (cmd) applyCommand(cmd)
}

// 处理单条 AG-UI 事件，更新助手消息内容。
function onEvent(ev: AGUIEvent, assistantId: string) {
  const am = messages.value.find((m) => m.id === assistantId)
  if (!am) return
  switch (ev.type) {
    case 'TEXT_MESSAGE_CONTENT':
      am.content += ev.delta ?? ''
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
    <SessionSidebar
      :sessions="sessions"
      :active-key="activeKey"
      @select="selectSession"
      @create="newSession"
      @rename="({ key, title }) => handleRenameSession(key, title)"
      @delete="handleDeleteSession"
    />

    <!-- 右侧对话区 -->
    <main class="flex-1 flex flex-col min-w-0">
      <!-- 顶部工具条：会话标题 + 工作区 / 模型 / Provider -->
      <ChatToolbar
        :title="activeSession?.title ?? ''"
        :workspace-key="selectedWorkspaceKey"
        :workspace-label="currentWorkspaceLabel"
        :workspace-options="workspaceOptions"
        :selected-model-id="selectedModelId"
        :model-options="modelOptions"
        :models-empty="models.length === 0"
        :model-label="currentModelLabel"
        :provider-name="currentProviderName"
        @workspace-change="onWorkspaceChange"
        @model-change="(v: number | null) => (selectedModelId = v)"
        @open-state="loadSessionState"
        @clear-context="clearContext"
      />

      <!-- 消息区 -->
      <MessageList :messages="messages" :has-session="!!activeKey" />

      <!-- 输入区（含斜杠命令浮层） -->
      <ChatInput
        v-model="input"
        :streaming="streaming"
        :disabled="!activeKey"
        :commands="commands"
        :filtered-commands="filteredCommands"
        :show-palette="showPalette"
        :highlight-index="highlightIndex"
        @send="send"
        @stop="stopStreaming"
        @nav="onNav"
        @select-command="onSelectCommand"
        @dismiss="dismissPalette = true"
      />
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
