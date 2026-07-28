<script setup lang="ts">
import { ref } from 'vue'
import {
  NLayout,
  NLayoutHeader,
  NLayoutSider,
  NLayoutContent,
  NMenu,
  NText,
  NButton,
} from 'naive-ui'
import type { MenuOption } from 'naive-ui'
import { useRouter } from 'vue-router'

const router = useRouter()
const collapsed = ref(false)

const menuOptions: MenuOption[] = [
  { label: '首页', key: 'home' },
  { label: '关于', key: 'about' },
]

function handleMenuClick(key: string) {
  router.push({ name: key })
}
</script>

<template>
  <n-layout class="h-screen">
    <n-layout-header
      class="flex items-center px-4 border-b border-gray-200"
      style="height: 56px"
    >
      <div class="text-lg font-bold tracking-wide">GoMultiAgent</div>
      <div class="ml-auto flex items-center gap-3">
        <n-text depth="3">未登录</n-text>
        <n-button quaternary size="small">退出</n-button>
      </div>
    </n-layout-header>
    <n-layout has-sider class="h-[calc(100vh-56px)]">
      <n-layout-sider
        bordered
        collapse-mode="width"
        :collapsed-width="64"
        :width="220"
        :collapsed="collapsed"
        show-trigger
        @collapse="collapsed = true"
        @expand="collapsed = false"
      >
        <n-menu :options="menuOptions" @update:value="handleMenuClick" />
      </n-layout-sider>
      <n-layout-content class="p-4 overflow-auto">
        <router-view />
      </n-layout-content>
    </n-layout>
  </n-layout>
</template>
