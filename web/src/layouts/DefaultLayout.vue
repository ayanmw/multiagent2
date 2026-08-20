<script setup lang="ts">
import { h, ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import {
  NLayout,
  NLayoutHeader,
  NLayoutSider,
  NLayoutContent,
} from 'naive-ui/es/layout'
import {
  NMenu,
} from 'naive-ui/es/menu'
import {
  NText,
} from 'naive-ui/es/typography'
import {
  NButton,
} from 'naive-ui/es/button'
import {
  NIcon,
} from 'naive-ui/es/icon'
import {
  NBadge,
} from 'naive-ui/es/badge'
import type { MenuOption } from 'naive-ui/es/menu'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'
import { listNotifications } from '@/api/notification'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const ui = useUiStore()
const collapsed = ref(false)

// 内联 SVG 图标，避免引入额外图标依赖；统一渲染为 Naive UI 的 NIcon。
function svgIcon(path: string) {
  return () =>
    h(NIcon, null, {
      default: () =>
        h(
          'svg',
          { viewBox: '0 0 24 24', width: '1.2em', height: '1.2em', fill: 'currentColor' },
          [h('path', { d: path })],
        ),
    })
}

const menuOptions: MenuOption[] = [
  {
    label: '对话',
    key: 'chat',
    icon: svgIcon('M20 2H4c-1.1 0-2 .9-2 2v18l4-4h14c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2z'),
  },
  {
    label: 'Provider',
    key: 'providers',
    icon: svgIcon('M19 9h-4V3H9v6H5l7 7 7-7zM5 18v2h14v-2H5z'),
  },
  {
    label: 'Model',
    key: 'models',
    icon: svgIcon(
      'M4 4h4v4H4zm6 0h4v4h-4zm6 0h4v4h-4zM4 10h4v4H4zm6 0h4v4h-4zm6 0h4v4h-4zM4 16h4v4H4zm6 0h4v4h-4zm6 0h4v4h-4z',
    ),
  },
  {
    label: '工作区',
    key: 'workspaces',
    icon: svgIcon('M3 3h8v8H3zm10 0h8v8h-8zM3 13h8v8H3zm10 0h8v8h-8z'),
  },
  {
    label: 'MCP',
    key: 'mcp',
    icon: svgIcon('M4 4h6v6H4zm10 0h6v6h-6zM4 14h6v6H4zm10 0h6v6h-6zM4 10h6v4H4zm10-6h6v4h-6z'),
  },
  {
    label: '技能',
    key: 'skills',
    icon: svgIcon('M12 2l2.4 7.4H22l-6 4.4 2.3 7.2-6.3-4.6L5.7 21l2.3-7.2-6-4.4h7.6z'),
  },
  {
    label: '知识库',
    key: 'knowledge',
    icon: svgIcon('M12 3C6.48 3 2 6.02 2 9.65c0 2.3 1.37 4.34 3.5 5.62V21l3.2-1.76c.74.16 1.52.26 2.3.26.39 0 .77-.02 1.15-.06C13.16 20.9 18 18.2 18 13.2V9.65C18 6.02 13.52 3 12 3zm-1 11H8v-2h3v2zm5 0h-3v-2h3v2zm0-4H8V8h8v2z'),
  },
  {
    label: '任务中心',
    key: 'tasks',
    icon: svgIcon('M3 5h18v4H3zm0 6h18v4H3zm0 6h18v2H3z'),
  },
  {
    label: '人工检查点',
    key: 'checkpoints',
    icon: svgIcon(
      'M12 2a10 10 0 100 20 10 10 0 000-20zm-1 5h2v6h-2V7zm0 8h2v2h-2v-2z',
    ),
  },
  {
    label: '审计日志',
    key: 'audit',
    icon: svgIcon('M9 2h6l1 3h4v2H4V5h4l1-3zM5 8h14l-1 13H6L5 8zm5 2v9h1V10h-1zm3 0v9h1V10h-1z'),
  },
  {
    label: '用量统计',
    key: 'usage',
    icon: svgIcon('M4 20V10h4v10H4zm6 0V4h4v16h-4zm6 0v-7h4v7h-4z'),
  },
  {
    label: '运行监控',
    key: 'monitoring',
    icon: svgIcon('M3 13h2v8H3zm4-6h2v14H7zm4-4h2v18h-2zm4 8h2v10h-2zm4-12h2v22h-2z'),
  },
  {
    label: 'Artifact',
    key: 'artifacts',
    icon: svgIcon('M14 2H6c-1.1 0-2 .9-2 2v16c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V8l-6-6zm0 7h-5V4.5L14 9z'),
  },
  {
    label: '设置',
    key: 'settings',
    icon: svgIcon(
      'M19.14 12.94c.04-.3.06-.61.06-.94s-.02-.64-.07-.94l2.03-1.58c.18-.14.23-.41.12-.61l-1.92-3.32c-.12-.22-.37-.29-.59-.22l-2.39.96c-.5-.38-1.03-.7-1.62-.94l-.36-2.54c-.04-.24-.24-.41-.48-.41h-3.84c-.24 0-.43.17-.47.41l-.36 2.54c-.59.24-1.13.57-1.62.94l-2.39-.96c-.22-.08-.47 0-.59.22L2.74 8.87c-.12.21-.08.47.12.61l2.03 1.58c-.05.3-.09.63-.09.94s.02.64.07.94l-2.03 1.58c-.18.14-.23.41-.12.61l1.92 3.32c.12.22.37.29.59.22l2.39-.96c.5.38 1.03.7 1.62.94l.36 2.54c.05.24.24.41.48.41h3.84c.24 0 .44-.17.47-.41l.36-2.54c.59-.24 1.13-.56 1.62-.94l2.39.96c.22.08.47 0 .59-.22l1.92-3.32c.12-.22.07-.47-.12-.61l-2.01-1.58z',
    ),
  },
  {
    label: () => '通知' + (unreadCount.value > 0 ? ` (${unreadCount.value})` : ''),
    key: 'notifications',
    icon: svgIcon('M12 22c1.1 0 2-.9 2-2h-4c0 1.1.9 2 2 2zm6-6v-5c0-3.07-1.64-5.64-4.5-6.32V4c0-.83-.67-1.5-1.5-1.5S10.5 3.17 10.5 4v.68C7.63 5.36 6 7.93 6 11v5l-2 2v1h16v-1l-2-2z'),
  },
  {
    label: '自动化',
    key: 'automations',
    icon: svgIcon('M12 2l1 7h7l-5.5 4 2 7L12 15l-4.5 5 2-7L4 9h7z'),
  },
  {
    label: '进化飞轮',
    key: 'evolution',
    icon: svgIcon('M12 2a10 10 0 100 20 10 10 0 000-20zm0 4l2 5h5l-4 3 1.5 5L12 16l-4.5 3L9 14l-4-3h5z'),
  },
  {
    label: '评估回归',
    key: 'evaluation',
    icon: svgIcon('M3 3h18v4H3zm0 7h18v4H3zm0 7h12v4H3zm14 .5l3 3 3-3'),
  },
  {
    label: '用户管理',
    key: 'admin',
    icon: svgIcon('M12 12c2.21 0 4-1.79 4-4s-1.79-4-4-4-4 1.79-4 4 1.79 4 4 4zm0 2c-2.67 0-8 1.34-8 4v2h16v-2c0-2.66-5.33-4-8-4z'),
  },
]

// 非管理员隐藏「用户管理」菜单项（后端同样强制 RequireRole(admin)）。
const visibleMenuOptions = computed(() => {
  if (auth.user?.role === 'admin') return menuOptions
  return menuOptions.filter((o) => o.key !== 'admin')
})

// 主题切换按钮图标：深色显示「太阳」(切回浅色)，浅色显示「月亮」(切到深色)。
const sunIcon = svgIcon(
  'M12 7c-2.76 0-5 2.24-5 5s2.24 5 5 5 5-2.24 5-5-2.24-5-5-5zM2 13h2c.55 0 1-.45 1-1s-.45-1-1-1H2c-.55 0-1 .45-1 1s.45 1 1 1zm18 0h2c.55 0 1-.45 1-1s-.45-1-1-1h-2c-.55 0-1 .45-1 1s.45 1 1 1zM11 2v2c0 .55.45 1 1 1s1-.45 1-1V2c0-.55-.45-1-1-1s-1 .45-1 1zm0 18v2c0 .55.45 1 1 1s1-.45 1-1v-2c0-.55-.45-1-1-1s-1 .45-1 1zM5.99 4.58c-.39-.39-1.03-.39-1.41 0-.39.39-.39 1.03 0 1.41l1.06 1.06c.39.39 1.03.39 1.41 0s.39-1.03 0-1.41L5.99 4.58zm12.37 12.37c-.39-.39-1.03-.39-1.41 0-.39.39-.39 1.03 0 1.41l1.06 1.06c.39.39 1.03.39 1.41 0 .39-.39.39-1.03 0-1.41l-1.06-1.06zm1.06-10.96c.39-.39.39-1.03 0-1.41-.39-.39-1.03-.39-1.41 0l-1.06 1.06c-.39.39-.39 1.03 0 1.41s1.03.39 1.41 0l1.06-1.06zM7.05 18.36c.39-.39.39-1.03 0-1.41-.39-.39-1.03-.39-1.41 0l-1.06 1.06c-.39.39-.39 1.03 0 1.41s1.03.39 1.41 0l1.06-1.06z',
)
const moonIcon = svgIcon(
  'M12 3c-4.97 0-9 4.03-9 9s4.03 9 9 9 9-4.03 9-9c0-.46-.04-.92-.1-1.36-.98 1.37-2.58 2.26-4.4 2.26-2.98 0-5.4-2.42-5.4-5.4 0-1.81.89-3.42 2.26-4.4-.44-.06-.9-.1-1.36-.1z',
)
const themeIcon = computed(() => (ui.dark ? sunIcon : moonIcon))

// 当前激活菜单项跟随路由名（chat/providers/models/settings）。
const activeKey = computed(() => route.name as string)

// 顶栏通知红点：拉取未读计数，登录态下定时刷新（M4-07 通知中心）。
const unreadCount = ref(0)
async function refreshUnread() {
  if (!auth.isAuthenticated) {
    unreadCount.value = 0
    return
  }
  try {
    const res = await listNotifications({ limit: 1, offset: 0 })
    unreadCount.value = res.unread ?? 0
  } catch {
    unreadCount.value = 0
  }
}
let unreadTimer: ReturnType<typeof setInterval> | null = null
onMounted(() => {
  refreshUnread()
  unreadTimer = setInterval(refreshUnread, 30000) // 30s 轮询未读计数
})
onBeforeUnmount(() => {
  if (unreadTimer) clearInterval(unreadTimer)
})

function handleMenuClick(key: string) {
  router.push({ name: key })
}

function handleLogout() {
  auth.logout()
  unreadCount.value = 0
  router.push({ name: 'login' })
}
</script>

<template>
  <n-layout class="h-screen">
    <n-layout-header
      class="flex items-center px-4 bg-white dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700"
      style="height: 56px"
    >
      <div class="text-lg font-bold tracking-wide">GoMultiAgent</div>
      <div class="ml-auto flex items-center gap-3">
        <n-button quaternary circle title="通知中心" @click="router.push({ name: 'notifications' })">
          <n-badge :value="unreadCount" :max="99" :show-zero="false" type="error">
            <svg viewBox="0 0 24 24" width="1.3em" height="1.3em" fill="currentColor">
              <path d="M12 22c1.1 0 2-.9 2-2h-4c0 1.1.9 2 2 2zm6-6v-5c0-3.07-1.64-5.64-4.5-6.32V4c0-.83-.67-1.5-1.5-1.5S10.5 3.17 10.5 4v.68C7.63 5.36 6 7.93 6 11v5l-2 2v1h16v-1l-2-2z" />
            </svg>
          </n-badge>
        </n-button>
        <n-button quaternary circle title="切换深色/浅色主题" @click="ui.toggleDark()">
          <component :is="themeIcon" />
        </n-button>
        <n-text v-if="auth.isAuthenticated" depth="3">
          {{ auth.user?.display_name || auth.user?.username }}
        </n-text>
        <n-text v-else depth="3">未登录</n-text>
        <n-button quaternary size="small" @click="handleLogout">退出</n-button>
      </div>
    </n-layout-header>
    <n-layout has-sider class="h-[calc(100vh-56px)]">
      <n-layout-sider
        bordered
        collapse-mode="width"
        :collapsed-width="64"
        :width="220"
        :collapsed="collapsed"
        show-trigger
        class="bg-white dark:bg-gray-800"
        @collapse="collapsed = true"
        @expand="collapsed = false"
      >
        <n-menu :value="activeKey" :options="visibleMenuOptions" @update:value="handleMenuClick" />
      </n-layout-sider>
      <n-layout-content class="p-4 overflow-auto bg-gray-50 dark:bg-gray-900">
        <router-view />
      </n-layout-content>
    </n-layout>
  </n-layout>
</template>
