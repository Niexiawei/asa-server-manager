<template>
  <t-dialog
      v-model:visible="visible"
      header="有实例在定时更新后未恢复启动"
      :confirm-btn="{ content: '恢复启动', theme: 'primary', loading: confirming }"
      :cancel-btn="{ content: '忽略', theme: 'default', variant: 'outline' }"
      :close-btn="false"
      :close-on-esc-keydown="false"
      :close-on-overlay-click="false"
      :on-confirm="onConfirm"
      :on-cancel="onIgnore"
      width="520px"
      attach="body"
  >
    <div class="pending-body">
      <p class="pending-meta">
        任务「{{ pending?.task_name || '未知任务' }}」于 {{ formatTime(pending?.created_at) }} 停止了以下实例，
        之后没有把它们恢复启动。
      </p>
      <p class="pending-reason" v-if="pending?.reason">原因：{{ pending.reason }}</p>

      <div class="pending-instances">
        <t-tag v-for="name in pending?.instances || []" :key="name" theme="warning" variant="light">
          {{ name }}
        </t-tag>
      </div>
    </div>
  </t-dialog>
</template>

<script setup>
import {ref, onMounted, onUnmounted} from 'vue'
import {MessagePlugin, DialogPlugin} from 'tdesign-vue-next'
import {getPendingRestore, confirmPendingRestore, ignorePendingRestore} from '@/apis/api.js'
import {serverStore} from '@/store/serverStore.js'

const visible = ref(false)
const confirming = ref(false)
const pending = ref(null)

function formatTime(value) {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

async function refresh() {
  try {
    const {data} = await getPendingRestore()
    pending.value = data?.pending ?? null
    visible.value = !!pending.value
  } catch (e) {
    // 查询失败不弹错误：这只是个提示性功能，静默重试即可，不该在页面刚打开就报错
    console.error('Failed to fetch pending restore state:', e)
  }
}

async function onConfirm() {
  confirming.value = true
  try {
    await confirmPendingRestore()
    MessagePlugin.success('已开始恢复启动，可在批量操作面板查看进度')
    visible.value = false
  } catch (e) {
    MessagePlugin.error(e?.message || '恢复启动未能发起')
  } finally {
    confirming.value = false
  }
  // 返回 false 会阻止对话框自动关闭；这里手动控制 visible，所以固定阻止默认关闭行为
  return false
}

// 不可撤销的操作不能一键完成：忽略之后这些实例会一直保持停止状态，
// 需要用户明确知道后果
function onIgnore() {
  const confirmDialog = DialogPlugin.confirm({
    header: '确认忽略？',
    body: '忽略后不再提示，这些实例将保持停止状态，需要时请手动启动。',
    confirmBtn: {content: '确认忽略', theme: 'danger'},
    cancelBtn: '再想想',
    onConfirm: async () => {
      confirmDialog.destroy()
      try {
        await ignorePendingRestore()
        visible.value = false
      } catch (e) {
        MessagePlugin.error(e?.message || '忽略失败')
      }
    },
  })
  // 阻止外层 t-dialog 因点了「忽略」按钮而自动关闭——是否关闭由上面的二次确认决定
  return false
}

function onPendingRestoreEvent(type) {
  if (type !== 'pending_restore') return
  // 不直接信 WS 里的 data，以服务端文件为准，避免两条信息源打架
  refresh()
}

onMounted(() => {
  refresh()
  serverStore.pendingRestoreCallbacks.push(onPendingRestoreEvent)
})

onUnmounted(() => {
  const idx = serverStore.pendingRestoreCallbacks.indexOf(onPendingRestoreEvent)
  if (idx !== -1) {
    serverStore.pendingRestoreCallbacks.splice(idx, 1)
  }
})
</script>

<style scoped lang="less">
.pending-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.pending-meta {
  margin: 0;
}

.pending-reason {
  margin: 0;
  color: var(--td-text-color-secondary, #666);
}

.pending-instances {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 4px;
}
</style>
