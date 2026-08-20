import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import 'virtual:uno.css'

// naive-ui 组件改为按需引入：模板中的 n-* 组件由 unplugin-vue-components
// + NaiveUiResolver 自动解析（vite.config.ts），不再全量 app.use(naive)，
// 首屏 bundle 显著减小（M8-06）。
const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
