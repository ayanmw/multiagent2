// 统一 HTTP 客户端：封装 /api 基础路径、自动附加 JWT、统一 JSON 与错误处理。
const TOKEN_KEY = 'gm_agent_token'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token)
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY)
}

// ApiError 携带 HTTP 状态码，便于上层按 401/403 等做差异化处理。
export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

interface RequestOptions {
  method?: string
  body?: unknown
  // 是否附加 JWT，默认 true；登录/注册等匿名接口需显式传 false。
  auth?: boolean
}

export async function request<T = unknown>(
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  const { method = 'GET', body, auth = true } = options
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }
  if (auth) {
    const token = getToken()
    if (token) {
      headers['Authorization'] = `Bearer ${token}`
    }
  }

  const res = await fetch(`/api${path}`, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })

  const text = await res.text()
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = text
    }
  }

  if (!res.ok) {
    const msg =
      data && typeof data === 'object' && 'error' in data
        ? String((data as Record<string, unknown>).error)
        : `请求失败 (${res.status})`
    // 全局 401 拦截：令牌失效/未登录时清理本地态并跳回登录页，避免各页各自报错。
    if (res.status === 401) {
      clearToken()
      if (!window.location.pathname.startsWith('/login')) {
        window.location.href = '/login'
      }
    }
    throw new ApiError(res.status, msg)
  }

  return data as T
}
