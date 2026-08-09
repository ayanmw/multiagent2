import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import DefaultLayout from '@/layouts/DefaultLayout.vue'
import { useAuthStore } from '@/stores/auth'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { public: true },
  },
  {
    path: '/register',
    name: 'register',
    component: () => import('@/views/RegisterView.vue'),
    meta: { public: true },
  },
  {
    path: '/',
    component: DefaultLayout,
    meta: { requiresAuth: true },
    children: [
      {
        path: '',
        name: 'chat',
        component: () => import('@/views/ChatView.vue'),
        meta: { title: '对话工作台' },
      },
      {
        path: 'providers',
        name: 'providers',
        component: () => import('@/views/ProvidersView.vue'),
        meta: { title: 'Provider 管理', desc: 'Provider 增删改、连接测试与模型自动拉取' },
      },
      {
        path: 'models',
        name: 'models',
        component: () => import('@/views/ModelsView.vue'),
        meta: { title: 'Model 管理', desc: '按 Provider 分组展示模型，手动刷新并启用/禁用' },
      },
      {
        path: 'workspaces',
        name: 'workspaces',
        component: () => import('@/views/WorkspacesView.vue'),
        meta: { title: '工作区管理', desc: '绑定代码目录，CodeAct 工具在该目录执行' },
      },
      {
        path: 'mcp',
        name: 'mcp',
        component: () => import('@/views/McpView.vue'),
        meta: { title: 'MCP 管理', desc: '配置 MCP 服务器，按需装载工具' },
      },
      {
        path: 'skills',
        name: 'skills',
        component: () => import('@/views/SkillsView.vue'),
        meta: { title: '技能仓库', desc: '浏览与编辑技能（warm-start 注入）' },
      },
      {
        path: 'tasks',
        name: 'tasks',
        component: () => import('@/views/TaskCenterView.vue'),
        meta: { title: '任务中心', desc: '后台任务列表、状态与 transcript' },
      },
      {
        path: 'audit',
        name: 'audit',
        component: () => import('@/views/AuditView.vue'),
        meta: { title: '审计日志', desc: '命令执行审计：按用户/决策/时间筛选' },
      },
      {
        path: 'usage',
        name: 'usage',
        component: () => import('@/views/UsageView.vue'),
        meta: { title: '用量统计', desc: 'Token 计量：按用户/模型/时间聚合成本' },
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('@/views/SettingsView.vue'),
        meta: { title: '设置', desc: 'API Key、角色权限与运行模式' },
      },
    ],
  },
  // 兜底：未知路径回到首页（由守卫决定是否需要登录）。
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

// 全局前置守卫：未登录访问受保护路由 → 跳转登录页（携带 redirect）；
// 已登录却访问登录/注册页 → 跳回首页。
router.beforeEach((to) => {
  const auth = useAuthStore()
  const requiresAuth = to.matched.some((r) => r.meta.requiresAuth)

  if (requiresAuth && !auth.isAuthenticated) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if ((to.name === 'login' || to.name === 'register') && auth.isAuthenticated) {
    return { name: 'chat' }
  }
  return true
})

export default router
