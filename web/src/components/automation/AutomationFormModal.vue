<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import {
  NButton,
} from 'naive-ui/es/button'
import {
  NForm,
  NFormItem,
} from 'naive-ui/es/form'
import {
  NInput,
} from 'naive-ui/es/input'
import {
  NModal,
} from 'naive-ui/es/modal'
import {
  NRadioButton,
  NRadioGroup,
} from 'naive-ui/es/radio'
import {
  NSpace,
} from 'naive-ui/es/space'
import {
  NSwitch,
} from 'naive-ui/es/switch'
import {
  NText,
} from 'naive-ui/es/typography'
import {
  useMessage,
} from 'naive-ui/es/message'
import type { Automation, AutomationTriggerType } from '@/api/automation'

// 自动化创建/编辑表单弹窗：内部持有表单状态与校验，提交时把 payload 上抛父组件
// （父负责 create/update API 调用、消息提示与列表刷新）。
const props = defineProps<{
  show: boolean
  // 非空 = 编辑目标；null = 新建。
  edit: Automation | null
  submitting: boolean
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
  (e: 'submit', payload: Record<string, unknown>): void
}>()

const message = useMessage()

const form = reactive({
  name: '',
  trigger_type: 'cron' as AutomationTriggerType,
  cron_expr: '',
  goal_prompt: '',
  enabled: true,
})

const triggerOptions = [
  { label: '定时（cron）', value: 'cron' },
  { label: '事件（webhook）', value: 'webhook' },
]

function resetForm() {
  form.name = ''
  form.trigger_type = 'cron'
  form.cron_expr = ''
  form.goal_prompt = ''
  form.enabled = true
}

function fillForm(a: Automation) {
  form.name = a.name
  form.trigger_type = a.trigger_type
  form.cron_expr = a.cron_expr
  form.goal_prompt = a.goal_prompt
  form.enabled = a.enabled
}

// 打开弹窗时按编辑目标初始化表单。
watch(
  () => props.show,
  (v) => {
    if (!v) return
    if (props.edit) fillForm(props.edit)
    else resetForm()
  },
)

function onSubmit() {
  if (!form.name.trim()) {
    message.warning('请填写名称')
    return
  }
  if (!form.goal_prompt.trim()) {
    message.warning('请填写目标提示词（Goal Prompt）')
    return
  }
  if (form.trigger_type === 'cron' && !form.cron_expr.trim()) {
    message.warning('定时触发器需要填写 cron 表达式')
    return
  }
  const payload = {
    name: form.name.trim(),
    trigger_type: form.trigger_type,
    goal_prompt: form.goal_prompt.trim(),
    enabled: form.enabled,
  } as Record<string, unknown>
  if (form.trigger_type === 'cron') {
    payload.cron_expr = form.cron_expr.trim()
  }
  emit('submit', payload)
}
</script>

<template>
  <n-modal
    :show="show"
    :title="edit === null ? '新建自动化' : '编辑自动化'"
    preset="card"
    style="width: 600px; max-width: 94vw"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <n-form :model="form" label-placement="top">
      <n-form-item label="名称" required>
        <n-input v-model:value="form.name" placeholder="例如：每小时推进需求文档" />
      </n-form-item>
      <n-form-item label="触发器">
        <n-radio-group v-model:value="form.trigger_type">
          <n-radio-button v-for="o in triggerOptions" :key="o.value" :value="o.value">
            {{ o.label }}
          </n-radio-button>
        </n-radio-group>
      </n-form-item>
      <n-form-item v-if="form.trigger_type === 'cron'" label="cron 表达式" required>
        <n-input
          v-model:value="form.cron_expr"
          placeholder="例如：*/1 * * * *（每分钟）；0 9 * * *（每天 9 点）"
        />
        <template #feedback>
          <span class="text-xs text-gray-400">标准 5 段 cron；调度器据此计算「下次运行」时间</span>
        </template>
      </n-form-item>
      <n-form-item v-else label="事件入口说明">
        <n-text depth="3" class="text-xs">
          保存后后端自动生成 32B webhook 令牌；外部系统向
          <code class="font-mono">POST /api/webhooks/&lt;token&gt;</code>
          即可触发本自动化的 Loop（令牌不在此回显，可于后端日志/数据库查询）。
        </n-text>
      </n-form-item>
      <n-form-item label="目标提示词（Goal Prompt）" required>
        <n-input
          v-model:value="form.goal_prompt"
          type="textarea"
          :autosize="{ minRows: 3, maxRows: 8 }"
          placeholder="描述这个自动化要自主完成的目标，例如：阅读 docs/loop/PLAN.md，挑选第一个 ○ 任务实现并验证后提交。"
        />
      </n-form-item>
      <n-form-item label="启用">
        <n-switch v-model:value="form.enabled" />
      </n-form-item>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button @click="emit('update:show', false)">取消</n-button>
        <n-button type="primary" :loading="submitting" @click="onSubmit">
          {{ edit === null ? '创建' : '保存' }}
        </n-button>
      </n-space>
    </template>
  </n-modal>
</template>
