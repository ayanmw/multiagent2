<script setup lang="ts">
import { computed } from 'vue'
import {
  NConfigProvider,
  NMessageProvider,
  NDialogProvider,
  NGlobalStyle,
  darkTheme,
} from 'naive-ui'
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
