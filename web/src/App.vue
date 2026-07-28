<script setup lang="ts">
import { NConfigProvider, NMessageProvider, NDialogProvider, NGlobalStyle } from 'naive-ui'
import DefaultLayout from './layouts/DefaultLayout.vue'
</script>

<template>
  <n-config-provider>
    <n-global-style />
    <n-message-provider>
      <n-dialog-provider>
        <default-layout />
      </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>
