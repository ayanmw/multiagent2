<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import {
  useMessage,
} from 'naive-ui/es/message'
import {
  NLayout,
  NLayoutContent,
} from 'naive-ui/es/layout'
import {
  NCard,
} from 'naive-ui/es/card'
import {
  NForm,
  NFormItem,
} from 'naive-ui/es/form'
import {
  NInput,
} from 'naive-ui/es/input'
import {
  NButton,
} from 'naive-ui/es/button'
import {
  NAlert,
} from 'naive-ui/es/alert'
import {
  NH2,
  NText,
} from 'naive-ui/es/typography'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const route = useRoute()
const message = useMessage()
const auth = useAuthStore()

const form = reactive({
  account: '',
  password: '',
})
const loading = ref(false)
const error = ref('')

async function onSubmit() {
  error.value = ''
  if (!form.account || !form.password) {
    error.value = '请输入账号和密码'
    return
  }
  loading.value = true
  try {
    await auth.login(form.account, form.password)
    message.success('登录成功')
    const redirect = (route.query.redirect as string) || '/'
    router.push(redirect)
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '登录失败'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <n-layout class="h-screen">
    <n-layout-content class="flex items-center justify-center bg-gray-50">
      <n-card class="w-96 shadow-sm" :bordered="true">
        <div class="mb-6 text-center">
          <n-h2 class="!mt-0">登录 GoMultiAgent</n-h2>
          <n-text depth="3">企业级多 Agent CodeAgent 平台</n-text>
        </div>
        <n-alert v-if="error" type="error" class="mb-4">{{ error }}</n-alert>
        <n-form :model="form" @submit.prevent="onSubmit">
          <n-form-item label="账号" path="account">
            <n-input
              v-model:value="form.account"
              placeholder="用户名或邮箱"
              clearable
            />
          </n-form-item>
          <n-form-item label="密码" path="password">
            <n-input
              v-model:value="form.password"
              type="password"
              placeholder="请输入密码"
              show-password-on="click"
            />
          </n-form-item>
          <n-button
            type="primary"
            block
            :loading="loading"
            attr-type="submit"
          >
            登录
          </n-button>
        </n-form>
        <div class="mt-4 text-center">
          <n-text depth="3">还没有账号？</n-text>
          <n-button text type="primary" @click="router.push({ name: 'register' })">
            立即注册
          </n-button>
        </div>
      </n-card>
    </n-layout-content>
  </n-layout>
</template>
