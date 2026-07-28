<script setup lang="ts">
import { NCard, NText, NH1, NP } from 'naive-ui'
</script>

<template>
  <n-card title="欢迎使用 GoMultiAgent" :bordered="false">
    <n-h1>企业级多 Agent CodeAgent 平台</n-h1>
    <n-p>
      这是 M0 阶段的前端骨架。左侧导航用于切换页面，顶部为全局 Header，
      中间区域为路由出口（router-view）。
    </n-p>
    <n-text depth="3">
      后端地址：http://localhost:8080 · 后续任务将在此接入登录、Provider/Model 管理与流式对话。
    </n-text>
  </n-card>
</template>
