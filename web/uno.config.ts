import { defineConfig, presetUno } from 'unocss'

export default defineConfig({
  presets: [
    // 开启基于 .dark class 的暗色模式，使 dark: 工具类生效（与 ui store 切换 <html class="dark"> 对应）。
    presetUno({ dark: 'class' }),
  ],
})
