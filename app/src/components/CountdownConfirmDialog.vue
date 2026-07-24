<template>
  <t-dialog
      v-model:visible="visible"
      :header="header"
      :confirm-btn="{ content: confirmText, theme: action === 'stop' ? 'danger' : 'warning' }"
      cancel-btn="取消"
      :on-confirm="onConfirm"
      @close="onClose"
      width="520px"
      attach="body"
  >
    <p class="prompt">{{ prompt }}</p>

    <t-form label-width="100px">
      <CountdownOptions v-model="countdown" @validity="(v) => (valid = v)"/>
    </t-form>
  </t-dialog>
</template>

<script setup>
import {ref, computed} from 'vue'
import {MessagePlugin} from 'tdesign-vue-next'
import CountdownOptions from '@/components/CountdownOptions.vue'

const visible = ref(false)
const instanceName = ref('')
const action = ref('stop')
const countdown = ref({countdown: 0})
const valid = ref(true)
// 批量场景下由调用方给出文案，留空则用下面的单实例默认措辞
const headerOverride = ref('')
const promptOverride = ref('')

let resolver = null

const actionLabel = computed(() => (action.value === 'stop' ? '停止' : '重启'))
const header = computed(() => headerOverride.value || `${actionLabel.value}实例`)
const confirmText = computed(() => `确定${actionLabel.value}`)
const prompt = computed(
    () => promptOverride.value || `确定要${actionLabel.value}实例 "${instanceName.value}" 吗？`
)

// open('meijue', 'stop') → Promise<countdownConfig | null>
// open('', 'stop', {header, prompt}) → 批量场景，自定义文案
// null 表示用户取消
function open(name, act, overrides = {}) {
  instanceName.value = name
  action.value = act
  headerOverride.value = overrides.header || ''
  promptOverride.value = overrides.prompt || ''
  countdown.value = {countdown: 0}
  valid.value = true
  visible.value = true

  return new Promise((resolve) => {
    resolver = resolve
  })
}

function onConfirm() {
  // 后端也会校验并返回 400，这里拦一道是为了让用户当场看到哪里不对
  if (!valid.value) {
    MessagePlugin.error('倒计时配置有误，请检查')
    return false
  }

  visible.value = false
  resolver?.(countdown.value)
  resolver = null
}

// 关闭弹窗（点 X 或取消）时也要兑现 Promise，否则调用方会永久挂起。
// onConfirm 已经先 resolve 并把 resolver 置空，所以这里的二次调用是空操作。
function onClose() {
  resolver?.(null)
  resolver = null
}

defineExpose({open})
</script>

<style scoped lang="less">
.prompt {
  margin-bottom: 16px;
}
</style>
