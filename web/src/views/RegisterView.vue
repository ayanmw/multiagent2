<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
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
const message = useMessage()
const auth = useAuthStore()

const form = reactive({
  username: '',
  email: '',
  password: '',
  display_name: '',
})
const loading = ref(false)
const error = ref('')

function validate(): string | null {
  if (form.username.trim().length < 3) return '用户名至少 3 个字符'
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.email)) return '邮箱格式不正确'
  if (form.password.length < 6) return '密码至少 6 位'
  return null
}

async function onSubmit() {
  error.value = ''
  const err = validate()
  if (err) {
    error.value = err
    return
  }
  loading.value = true
  try {
    await auth.register({
      username: form.username.trim(),
      email: form.email.trim(),
      password: form.password,
      display_name: form.display_name.trim() || undefined,
    })
    message.success('注册成功，已自动登录')
    router.push('/')
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '注册失败'
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
          <n-h2 class="!mt-0">注册 GoMultiAgent</n-h2>
          <n-text depth="3">创建你的企业级多 Agent 工作台</n-text>
        </div>
        <n-alert v-if="error" type="error" class="mb-4">{{ error }}</n-alert>
        <n-form :model="form" @submit.prevent="onSubmit">
          <n-form-item label="用户名" path="username">
            <n-input v-model:value="form.username" placeholder="3-64 个字符" clearable />
          </n-form-item>
          <n-form-item label="邮箱" path="email">
            <n-input v-model:value="form.email" placeholder="用于登录与找回" clearable />
          </n-form-item>
          <n-form-item label="密码" path="password">
            <n-input
              v-model:value="form.password"
              type="password"
              placeholder="至少 6 位"
              show-password-on="click"
            />
          </n-form-item>
          <n-form-item label="显示名称" path="display_name">
            <n-input v-model:value="form.display_name" placeholder="可选，默认同用户名" clearable />
          </n-form-item>
          <n-button type="primary" block :loading="loading" attr-type="submit">
            注册
          </n-button>
        </n-form>
        <div class="mt-4 text-center">
          <n-text depth="3">已有账号？</n-text>
          <n-button text type="primary" @click="router.push({ name: 'login' })">
            去登录
          </n-button>
        </div>
      </n-card>
    </n-layout-content>
  </n-layout>
</template>
