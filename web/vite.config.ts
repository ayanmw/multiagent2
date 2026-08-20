import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import UnoCSS from 'unocss/vite'
import Components from 'unplugin-vue-components/vite'
import { fileURLToPath, URL } from 'node:url'

// ---- naive-ui 按需引入（M8-06）----
// 自定义 resolver：模板中的 n-* 组件解析为「组件级子路径」导入（naive-ui/es/<dir>），
// 替代官方 NaiveUiResolver（其 from 为根入口 'naive-ui'，会带副作用全量打包）。
// 特例映射：naive-ui 部分组件所在目录 ≠ PascalCase 直转 kebab（如 NText→typography、
// NGi→grid、NFormItem→form、NDrawerContent→drawer、NLayoutHeader→layout 等）。
const NAIVE_DIR_OVERRIDES: Record<string, string> = {
  Text: 'typography',
  H1: 'typography',
  H2: 'typography',
  H3: 'typography',
  H4: 'typography',
  H5: 'typography',
  H6: 'typography',
  P: 'typography',
  Ul: 'typography',
  Ol: 'typography',
  Li: 'typography',
  Blockquote: 'typography',
  A: 'typography',
  Strong: 'typography',
  Em: 'typography',
  Gi: 'grid',
  GridItem: 'grid',
  FormItem: 'form',
  FormItemGi: 'form',
  DescriptionsItem: 'descriptions',
  RadioGroup: 'radio',
  RadioButton: 'radio',
  DrawerContent: 'drawer',
  LayoutHeader: 'layout',
  LayoutSider: 'layout',
  LayoutContent: 'layout',
  InputGroup: 'input',
  MessageProvider: 'message',
  DialogProvider: 'dialog',
  ListItem: 'list',
}

function toPascal(kebab: string): string {
  return kebab
    .split('-')
    .map((s) => s.charAt(0).toUpperCase() + s.slice(1))
    .join('')
}

function naiveUiResolver(name: string) {
  if (!/^(?:N[A-Z]|n-[a-z])/.test(name)) return undefined
  const comp = name.startsWith('n-') ? toPascal(name) : name
  const base = comp.slice(1)
  const dir =
    NAIVE_DIR_OVERRIDES[base] ??
    base.replace(/([a-z0-9])([A-Z])/g, '$1-$2').toLowerCase()
  return { name, from: `naive-ui/es/${dir}` }
}

export default defineConfig({
  plugins: [
    vue(),
    UnoCSS(),
    Components({
      resolvers: [naiveUiResolver],
      dts: false,
    }),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    rollupOptions: {
      output: {
        // 手动分包（M8-06）：框架生态与 naive-ui 依赖拆为独立 chunk，
        // 利用浏览器长期缓存 + 与业务代码分离；naive-ui 按需引入后
        // 该 chunk 仅含实际用到的组件模块（id 含 naive-ui 及其基础库）。
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined
          if (
            /naive-ui|vueuc|seemly|vdirs|vooks|treemate|css-render|evtd|date-fns/.test(id)
          ) {
            return 'naive-ui'
          }
          if (/vue|vue-router|pinia|@vue/.test(id)) {
            return 'vue-vendor'
          }
          return undefined
        },
      },
    },
  },
})
