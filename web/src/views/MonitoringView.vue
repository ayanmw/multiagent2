<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import {
  NButton,
  NCard,
  NTag,
  NSpace,
  NGrid,
  NGridItem,
  NStatistic,
  NEmpty,
  NResult,
  NText,
  NBadge,
  useMessage,
} from 'naive-ui'
import { getMonitoringOverview, GRAFANA_URL, type MonitoringOverview } from '@/api/monitoring'

const message = useMessage()
const loading = ref(false)
const data = ref<MonitoringOverview | null>(null)

// 失败率：避免除零；百分比保留两位小数。
function pct(numerator: number, denominator: number): string {
  if (denominator <= 0) return '0.00%'
  return ((numerator / denominator) * 100).toFixed(2) + '%'
}

const llmFailRate = computed(() =>
  data.value ? pct(data.value.llm_errors, data.value.llm_calls) : '0.00%',
)
const toolFailRate = computed(() =>
  data.value ? pct(data.value.tool_errors, data.value.tool_calls) : '0.00%',
)
const loopFailRate = computed(() =>
  data.value ? pct(data.value.loop_failures, data.value.loop_runs) : '0.00%',
)
// 检查点堆积告警色：0 绿、>0 黄、>=10 红。
const checkpointType = computed(() => {
  const n = data.value?.pending_checkpoints ?? 0
  if (n >= 10) return 'error' as const
  if (n > 0) return 'warning' as const
  return 'success' as const
})
// 并发 Loop 告警色：0 绿、>=1 橙。
const activeLoopsType = computed(() => {
  const n = data.value?.active_loops ?? 0
  return n > 0 ? ('warning' as const) : ('success' as const)
})

async function load() {
  loading.value = true
  try {
    data.value = await getMonitoringOverview()
  } catch (e) {
    message.error((e as Error).message)
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <n-card :bordered="false" class="h-full" content-style="display:flex;flex-direction:column;height:100%">
    <div class="flex items-center mb-3">
      <div>
        <h2 class="text-lg font-semibold m-0">运行监控</h2>
        <span class="text-sm text-gray-500 dark:text-gray-400">
          OpenTelemetry 指标快照（M3-09 / M7-05）· 进程内累计，重启清零
        </span>
      </div>
      <n-space class="ml-auto">
        <n-button tag="a" :href="GRAFANA_URL" target="_blank" secondary>
          打开 Grafana 看板
        </n-button>
        <n-button :loading="loading" @click="load">刷新</n-button>
      </n-space>
    </div>

    <!-- 指标未启用：后端 METRICS_ENABLED=false 或尚未初始化 -->
    <n-result
      v-if="!loading && data && !data.enabled"
      status="info"
      title="可观测性未启用"
      description="后端 METRICS_ENABLED 为 false，或 metrics 子系统尚未初始化。将环境变量 METRICS_ENABLED 设为 true 并重启后端即可启用 /metrics 与运行监控。"
      class="m-auto"
    />

    <!-- 已启用但暂无数据 -->
    <n-empty
      v-else-if="!loading && data && data.enabled && data.llm_calls === 0 && data.tool_calls === 0 && data.token_total === 0"
      description="暂无指标，发起几次对话或执行命令后即可看到调用与失败率"
      class="mt-10"
    />

    <template v-else-if="data && data.enabled">
      <!-- 实时 gauge：Active Loops / 检查点堆积（M7-05） -->
      <n-grid :cols="4" :x-gap="12" class="mb-3">
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="并发自主 Loop" :value="data.active_loops">
              <template #prefix>
                <n-badge :type="activeLoopsType" dot />
              </template>
            </n-statistic>
          </n-card>
        </n-gi>
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="待审批检查点" :value="data.pending_checkpoints">
              <template #prefix>
                <n-badge :type="checkpointType" dot />
              </template>
            </n-statistic>
          </n-card>
        </n-gi>
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="自主 Loop 失败率" :value="loopFailRate" />
          </n-card>
        </n-gi>
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="预算耗尽拦截" :value="data.budget_exhausted" />
          </n-card>
        </n-gi>
      </n-grid>

      <!-- LLM 调用概览 -->
      <n-grid :cols="4" :x-gap="12" class="mb-3">
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="LLM 调用总数" :value="data.llm_calls" />
          </n-card>
        </n-gi>
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="LLM 失败数" :value="data.llm_errors" />
          </n-card>
        </n-gi>
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="LLM 失败率" :value="llmFailRate" />
          </n-card>
        </n-gi>
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="LLM 成功率" :value="(100 - parseFloat(llmFailRate)).toFixed(2) + '%'" />
          </n-card>
        </n-gi>
      </n-grid>

      <!-- 工具调用概览 -->
      <n-grid :cols="4" :x-gap="12" class="mb-3">
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="工具调用总数" :value="data.tool_calls" />
          </n-card>
        </n-gi>
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="工具失败数" :value="data.tool_errors" />
          </n-card>
        </n-gi>
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="工具失败率" :value="toolFailRate" />
          </n-card>
        </n-gi>
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="工具成功率" :value="(100 - parseFloat(toolFailRate)).toFixed(2) + '%'" />
          </n-card>
        </n-gi>
      </n-grid>

      <!-- Token 用量概览 -->
      <n-grid :cols="3" :x-gap="12" class="mb-3">
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="提示 Token" :value="data.token_prompt" />
          </n-card>
        </n-gi>
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="补全 Token" :value="data.token_completion" />
          </n-card>
        </n-gi>
        <n-gi>
          <n-card :bordered="true" size="small">
            <n-statistic label="Token 总计" :value="data.token_total" />
          </n-card>
        </n-gi>
      </n-grid>

      <n-space class="mt-1">
        <n-tag :bordered="false" type="success" size="small">
          LLM 失败 {{ data.llm_errors }} / {{ data.llm_calls }}
        </n-tag>
        <n-tag :bordered="false" type="warning" size="small">
          工具失败 {{ data.tool_errors }} / {{ data.tool_calls }}
        </n-tag>
        <n-text depth="3" class="text-xs">
          失败原因分类（allowed/denied/checkpoint/failed）见后端 /metrics 的 codeagent_tool_errors_total 标签；Grafana 看板含 P99 时延、Loop、预算与进程重启曲线。
        </n-text>
      </n-space>
    </template>
  </n-card>
</template>
