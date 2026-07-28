// 认证状态仓库（Pinia）：管理 JWT 与用户信息，并持久化到 localStorage。
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { User } from '@/api/auth'
import { login as apiLogin, register as apiRegister, me as apiMe } from '@/api/auth'
import { setToken, getToken, clearToken } from '@/api/client'

const USER_KEY = 'gm_agent_user'

function loadUser(): User | null {
  const raw = localStorage.getItem(USER_KEY)
  if (!raw) return null
  try {
    return JSON.parse(raw) as User
  } catch {
    return null
  }
}

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(getToken())
  const user = ref<User | null>(loadUser())

  const isAuthenticated = computed(() => !!token.value)

  // 登录态落地：写入内存 ref 与 localStorage。
  function persist(t: string, u: User) {
    setToken(t)
    token.value = t
    user.value = u
    localStorage.setItem(USER_KEY, JSON.stringify(u))
  }

  async function login(account: string, password: string) {
    const res = await apiLogin({ account, password })
    persist(res.token, res.user)
  }

  async function register(payload: {
    username: string
    email: string
    password: string
    display_name?: string
  }) {
    const res = await apiRegister(payload)
    persist(res.token, res.user)
  }

  // 用已存储的 token 拉取最新用户信息；token 失效则清除登录态。
  async function fetchMe() {
    if (!token.value) return
    try {
      const res = await apiMe()
      user.value = res.user
      localStorage.setItem(USER_KEY, JSON.stringify(res.user))
    } catch {
      logout()
    }
  }

  function logout() {
    clearToken()
    token.value = null
    user.value = null
    localStorage.removeItem(USER_KEY)
  }

  return { token, user, isAuthenticated, login, register, fetchMe, logout }
})
