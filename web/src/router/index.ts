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
        component: () => import('@/views/PlaceholderView.vue'),
        meta: { title: '对话工作台', desc: 'M0-17 将在此实现 Session 列表、流式对话与 Markdown 渲染' },
      },
      {
        path: 'providers',
        name: 'providers',
        component: () => import('@/views/PlaceholderView.vue'),
        meta: { title: 'Provider 管理', desc: 'M0-15 将在此实现 Provider 增删改、连接测试与模型自动拉取' },
      },
      {
        path: 'models',
        name: 'models',
        component: () => import('@/views/PlaceholderView.vue'),
        meta: { title: 'Model 管理', desc: 'M0-16 将在此按 Provider 分组展示并启用/禁用模型' },
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('@/views/PlaceholderView.vue'),
        meta: { title: '设置', desc: '用户偏好、主题与账号相关设置' },
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
