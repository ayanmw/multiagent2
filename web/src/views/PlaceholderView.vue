<script setup lang="ts">
import { useRoute } from 'vue-router'
import { NCard, NSpace, NEmpty, NP, NText } from 'naive-ui'

const route = useRoute()
const title = (route.meta.title as string) || '页面'
const desc = (route.meta.desc as string) || ''
</script>

<template>
  <n-card :title="title" :bordered="false" class="h-full">
    <n-space vertical :size="16" align="center" class="py-16">
      <n-empty :description="`「${title}」功能建设中`" />
      <n-p class="text-center">
        <n-text depth="3">{{ desc }}</n-text>
      </n-p>
    </n-space>
  </n-card>
</template>
