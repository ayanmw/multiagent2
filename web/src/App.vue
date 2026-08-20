<script setup lang="ts">
import { computed } from 'vue'
import { NConfigProvider } from 'naive-ui/es/config-provider'
import { NMessageProvider } from 'naive-ui/es/message'
import { NDialogProvider } from 'naive-ui/es/dialog'
import { NGlobalStyle } from 'naive-ui/es/global-style'
import { darkTheme } from 'naive-ui/es/themes'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
// 根据 UI store 的深色偏好切换 Naive UI 主题（null 即浅色默认主题）。
const theme = computed(() => (ui.dark ? darkTheme : null))
</script>

<template>
  <n-config-provider :theme="theme">
    <n-global-style />
    <n-message-provider>
      <n-dialog-provider>
        <router-view />
      </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>
