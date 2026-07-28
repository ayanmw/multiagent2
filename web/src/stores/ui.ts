// 全局 UI 状态仓库（Pinia）：管理深色主题偏好，持久化到 localStorage，
// 并通过切换 <html> 上的 `dark` 类驱动 UnoCSS 的 dark: 工具类与 Naive UI 主题。
import { defineStore } from 'pinia'
import { ref } from 'vue'

const THEME_KEY = 'gm_agent_theme'

// 将深色偏好同步到 <html> 根节点，供 UnoCSS dark: 变体生效。
function applyHtmlClass(dark: boolean) {
  if (typeof document !== 'undefined') {
    document.documentElement.classList.toggle('dark', dark)
  }
}

export const useUiStore = defineStore('ui', () => {
  const initial = (localStorage.getItem(THEME_KEY) ?? 'light') === 'dark'
  const dark = ref<boolean>(initial)
  applyHtmlClass(dark.value)

  function setDark(value: boolean) {
    dark.value = value
    localStorage.setItem(THEME_KEY, value ? 'dark' : 'light')
    applyHtmlClass(value)
  }

  function toggleDark() {
    setDark(!dark.value)
  }

  return { dark, setDark, toggleDark }
})
