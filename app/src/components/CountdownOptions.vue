<template>
  <div class="countdown-options">
    <t-form-item label="倒计时">
      <t-switch v-model="enabled"/>
      <span class="hint">开启后会先公告并等待，再执行操作</span>
    </t-form-item>

    <template v-if="enabled">
      <t-form-item label="总时长(秒)" :status="totalError ? 'error' : undefined" :tips="totalError">
        <t-input-number v-model="total" :min="MIN_TOTAL" :max="MAX_TOTAL" :step="30"/>
      </t-form-item>

      <t-form-item label="播报时间点" :status="pointsError ? 'error' : undefined"
                   :tips="pointsError || '剩余多少秒时播报一次，逗号分隔；留空则自动推导'">
        <t-input v-model="pointsText" placeholder="600,300,60,30,10"/>
      </t-form-item>

      <t-form-item label="公告内容">
        <t-input v-model="message" :placeholder="DEFAULT_TEMPLATE"/>
      </t-form-item>

      <t-form-item label="占位符">
        <span class="hint">
          <code>{time}</code> 剩余时间 ·
          <code>{action}</code> 停止/重启 ·
          <code>{instance}</code> 实例名
        </span>
      </t-form-item>

      <t-form-item label="公告方式">
        <t-select v-model="command">
          <t-option value="ServerChat" label="ServerChat（聊天框）"/>
          <t-option value="Broadcast" label="Broadcast（屏幕中央）"/>
        </t-select>
      </t-form-item>
    </template>
  </div>
</template>

<script setup>
import {ref, computed, watch} from 'vue'

// 与后端 instance/countdown.go 的约束保持一致
const MIN_TOTAL = 30
const MAX_TOTAL = 24 * 3600
const MAX_POINTS = 20
const DEFAULT_TEMPLATE = '服务器将在 {time} 后{action}，请及时下线'

const props = defineProps({
  modelValue: {
    type: Object,
    default: () => ({}),
  },
})
const emits = defineEmits(['update:modelValue', 'validity'])

const enabled = ref(Boolean(props.modelValue?.countdown))
const total = ref(props.modelValue?.countdown || 600)
const pointsText = ref((props.modelValue?.notify_points ?? []).join(','))
const message = ref(props.modelValue?.notify_message || '')
const command = ref(props.modelValue?.notify_command || 'ServerChat')

// 解析播报点位，返回 {points, error}
const parsed = computed(() => {
  const raw = pointsText.value.trim()
  if (!raw) return {points: [], error: ''}

  const points = []
  for (const part of raw.split(',')) {
    const piece = part.trim()
    if (!piece) continue

    const n = Number(piece)
    if (!Number.isInteger(n)) {
      return {points: [], error: `播报时间点必须是整数秒：${piece}`}
    }
    if (n <= 0) {
      return {points: [], error: '播报时间点必须大于 0'}
    }
    // 核心校验：点位不能超过总时长，否则永远触发不到
    if (n > total.value) {
      return {points: [], error: `播报时间点 ${n} 秒超过了倒计时总时长 ${total.value} 秒`}
    }
    points.push(n)
  }

  if (points.length > MAX_POINTS) {
    return {points: [], error: `播报时间点最多 ${MAX_POINTS} 个`}
  }

  return {points, error: ''}
})

const totalError = computed(() => {
  if (!enabled.value) return ''
  if (!Number.isInteger(total.value) || total.value < MIN_TOTAL) {
    return `倒计时最少 ${MIN_TOTAL} 秒`
  }
  if (total.value > MAX_TOTAL) return '倒计时最多 24 小时'
  return ''
})

const pointsError = computed(() => (enabled.value ? parsed.value.error : ''))

const valid = computed(() => !totalError.value && !pointsError.value)

// 向父组件同步值与校验结果
watch(
    [enabled, total, pointsText, message, command, valid],
    () => {
      emits('update:modelValue', enabled.value
          ? {
            countdown: total.value,
            notify_points: parsed.value.points,
            notify_message: message.value,
            notify_command: command.value,
          }
          : {countdown: 0})
      emits('validity', valid.value)
    },
    {immediate: true}
)

// 外部重置表单（例如打开新建弹窗）时同步回来
watch(
    () => props.modelValue,
    (val) => {
      enabled.value = Boolean(val?.countdown)
      total.value = val?.countdown || 600
      pointsText.value = (val?.notify_points ?? []).join(',')
      message.value = val?.notify_message || ''
      command.value = val?.notify_command || 'ServerChat'
    }
)

defineExpose({valid})
</script>

<style scoped lang="less">
.hint {
  color: var(--td-text-color-secondary, #888);
  font-size: 12px;
  margin-left: 8px;

  code {
    background: var(--td-bg-color-container-hover, #f3f3f3);
    padding: 0 4px;
    border-radius: 3px;
  }
}
</style>
