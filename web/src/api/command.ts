// 斜杠命令注册表前端封装（M1-15）。
// 元数据由后端 GET /api/commands 下发（单一事实源，见 server/internal/command），
// 前端只负责渲染命令浮层、解析用户输入、以及把 prompt 类命令渲染成提示词。
import { request } from './client'

// 命令参数说明（用于前端填参表单）。
export interface CommandArg {
  name: string
  description: string
  required: boolean
}

// 命令元数据（JSON 字段与后端 server/internal/command.Command 对齐，勿改名）。
export interface Command {
  name: string
  description: string
  usage: string
  category: string
  args?: CommandArg[]
  kind: string
  template?: string
  endpoint?: string
}

// 拉取后端命令注册表（受保护路由，无需 DB）。
export async function fetchCommands(): Promise<Command[]> {
  const data = await request<{ commands: Command[] }>('/commands')
  return data.commands ?? []
}

export interface ResolvedSlash {
  command: Command
  args: string
}

// 解析用户输入是否为斜杠命令。
// 规则：以 / 开头；第一个空格前为命令名，与注册表精确匹配才视为命令（避免误吞普通文本）；
// 其余整行作为参数（如 /run ls -la → name=run, args="ls -la"）。
// 未匹配到命令名时返回 null（按普通消息发送）。
export function resolveSlashCommand(text: string, commands: Command[]): ResolvedSlash | null {
  const t = text.trim()
  if (!t.startsWith('/')) return null
  const withoutSlash = t.slice(1)
  const sp = withoutSlash.indexOf(' ')
  const name = sp === -1 ? withoutSlash : withoutSlash.slice(0, sp)
  const args = sp === -1 ? '' : withoutSlash.slice(sp + 1).trim()
  const cmd = commands.find((c) => c.name === name)
  if (!cmd) return null
  return { command: cmd, args }
}

// 前端渲染 prompt 类命令模板（与后端 command.RenderPrompt 对齐）。
// 模板中的 {{args}} 占位符替换为用户输入的参数。
export function renderCommandPrompt(cmd: Command, args: string): string {
  if (cmd.kind !== 'prompt' || !cmd.template) return ''
  if (!cmd.template.includes('{{args}}')) return cmd.template
  return cmd.template.replaceAll('{{args}}', args)
}
