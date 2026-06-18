<template>
  <!-- 服务器更新弹窗 -->
  <t-dialog
    :visible="visible"
    header="服务器更新"
    :width="800"
    :close-on-overlay-click="!updating"
    :close-btn="!updating"
    @close="onDialogClose"
    @opened="onDialogOpened"
    destroy-on-close
    top="5vh"
    draggable
  >
    <!-- 更新状态提示 -->
    <t-alert v-if="updating" theme="info" message="服务器更新进行中，请勿关闭页面..." class="mb-3" />
    <t-alert v-if="updateCompleted" theme="success" message="服务器更新已完成" class="mb-3" />
    <t-alert v-if="updateCancelled" theme="warning" message="服务器更新已取消" class="mb-3" />

    <!-- 日志区域 -->
    <div class="update-log-container">
      <div id="updateLogContainer" class="update-log">
        <div v-if="updateLogs.length === 0 && !updating" class="log-placeholder">
          点击"开始更新"按钮启动服务器文件更新
        </div>
        <div
          v-for="(log, index) in updateLogs"
          :key="index"
          class="log-line"
          :class="{
            'log-error': log.startsWith('Error:') || log.startsWith('错误:'),
            'log-success': log.includes('[COMPLETED]'),
            'log-cancelled': log.includes('[CANCELLED]'),
          }"
        >
          {{ log }}
        </div>
      </div>
    </div>

    <template #footer>
      <div class="action-bar">
        <t-button
          v-if="updating"
          theme="danger"
          variant="outline"
          @click="handleCancelUpdate"
        >取消更新</t-button>
        <t-button
          v-else
          theme="primary"
          @click="handleStartUpdate"
          :loading="startingUpdate"
        >开始更新</t-button>
      </div>
    </template>
  </t-dialog>
</template>

<script setup>
import {ref, onMounted, onBeforeUnmount, nextTick, watch} from 'vue'
import {getUpdateStatus, cancelUpdate, listInstances} from '@/apis/api.js'
import {updateServer as updateServerSSE} from '@/apis/sseApi.js'
import {MessagePlugin, DialogPlugin} from 'tdesign-vue-next'

const props = defineProps({
  visible: {
    type: Boolean,
    default: false
  },
  instances: {
    type: Array,
    default: () => []
  }
})

const emits = defineEmits(['update:visible', 'refresh'])

// ========== 更新状态 ==========
const updating = ref(false)
const updateLogs = ref([])
const startingUpdate = ref(false)
const updateCompleted = ref(false)
const updateCancelled = ref(false)
let statusPollTimer = null

// ========== 弹窗关闭 ==========
function onDialogClose() {
  if (!updating.value) {
    emits('update:visible', false)
  }
}

// ========== 弹窗打开时检查状态 ==========
async function onDialogOpened() {
  updateCompleted.value = false
  updateCancelled.value = false
  await pollUpdateStatus()
  // 启动轮询
  if (!statusPollTimer) {
    statusPollTimer = setInterval(pollUpdateStatus, 5000)
  }
}

// ========== 轮询更新状态 ==========
async function pollUpdateStatus() {
  try {
    const res = await getUpdateStatus()
    const running = res.data?.running
    if (running && !updating.value) {
      // 后端正在更新但前端不知道（如刷新页面后）
      updating.value = true
      updateLogs.value = []
      startSSESubscription()
    } else if (!running && updating.value) {
      // 更新完成
      onUpdatingFinished()
    }
  } catch (e) {
    // ignore
  }
}

// ========== 开始更新 ==========
async function handleStartUpdate() {
  // 检查是否有运行中的实例
  try {
    const {data: {instances}} = await listInstances()
    const runningInstances = instances?.filter(i => i.running) || []
    if (runningInstances.length > 0) {
      MessagePlugin.warning(`以下实例正在运行：${runningInstances.map(i => i.name).join('、')}，请先关闭所有实例`)
      return
    }
  } catch (error) {
    MessagePlugin.error('获取服务器状态失败')
    return
  }

  let confirmDialog = DialogPlugin.confirm({
    header: '确认',
    body: '确定要更新服务器吗？这可能需要一些时间。',
    confirmBtn: '确定',
    cancelBtn: '取消',
    onConfirm: async () => {
      confirmDialog.hide()
      startingUpdate.value = true
      updateCompleted.value = false
      updateCancelled.value = false
      updateLogs.value = []

      // 调用 SSE 接口启动更新
      startSSESubscription()

      await updateServerSSE(
        (message) => {
          updateLogs.value.push(message)
          nextTick(() => {
            const logContainer = document.getElementById('updateLogContainer')
            if (logContainer) {
              logContainer.scrollTop = logContainer.scrollHeight
            }
          })
        },
        (error) => {
          console.error('更新日志错误:', error)
          updateLogs.value.push(`错误: ${error}`)
        },
        () => {
          onUpdatingFinished()
          MessagePlugin.success('服务器更新流程已结束')
          emits('refresh')
        }
      )

      startingUpdate.value = false
    }
  })
}

// ========== SSE 订阅（当后端已在运行时） ==========
let sseCloseFn = null

function startSSESubscription() {
  if (sseCloseFn) {
    sseCloseFn()
    sseCloseFn = null
  }

  updating.value = true

  sseCloseFn = updateServerSSE(
    (message) => {
      updateLogs.value.push(message)
      nextTick(() => {
        const logContainer = document.getElementById('updateLogContainer')
        if (logContainer) {
          logContainer.scrollTop = logContainer.scrollHeight
        }
      })
    },
    (error) => {
      console.error('更新日志 SSE 错误:', error)
    },
    () => {
      onUpdatingFinished()
    }
  )
}

// ========== 更新结束处理 ==========
function onUpdatingFinished() {
  const wasUpdating = updating.value
  updating.value = false

  if (sseCloseFn) {
    sseCloseFn()
    sseCloseFn = null
  }

  // 检查最后一条日志判断是完成还是取消
  const lastLog = updateLogs.value[updateLogs.value.length - 1] || ''
  if (lastLog.includes('[COMPLETED]')) {
    updateCompleted.value = true
    updateCancelled.value = false
  } else if (lastLog.includes('[CANCELLED]')) {
    updateCancelled.value = true
    updateCompleted.value = false
  } else if (wasUpdating) {
    // 没有明确标记，默认为完成
    updateCompleted.value = true
  }
}

// ========== 取消更新 ==========
function handleCancelUpdate() {
  let confirmDialog = DialogPlugin.confirm({
    header: '确认取消',
    body: '确定要取消服务器文件更新吗？取消后可能需要重新更新才能使用服务器。',
    theme: 'danger',
    confirmBtn: '确定取消',
    cancelBtn: '返回',
    onConfirm: async () => {
      confirmDialog.hide()
      try {
        await cancelUpdate()
        MessagePlugin.info('已发送取消指令，正在停止更新...')
      } catch (error) {
        MessagePlugin.error('取消失败: ' + (error.message || '未知错误'))
      }
    }
  })
}

// ========== 生命周期 ==========
onMounted(() => {
  // 初始状态检查在 onDialogOpened 中执行
})

onBeforeUnmount(() => {
  if (statusPollTimer) {
    clearInterval(statusPollTimer)
    statusPollTimer = null
  }
  if (sseCloseFn) {
    sseCloseFn()
    sseCloseFn = null
  }
})
</script>

<style scoped lang="less">
.mb-3 {
  margin-bottom: 12px;
}

.update-log-container {
  position: relative;
  height: 400px;
  overflow: hidden;
}

.update-log {
  width: 100%;
  height: 100%;
  border: 1px solid #e5e7eb;
  border-radius: 4px;
  padding: 12px;
  box-sizing: border-box;
  background-color: #1e1e1e;
  overflow-y: auto;
  font-family: 'Monaco', 'Courier New', monospace;
  font-size: 12px;
  line-height: 1.6;
}

.log-placeholder {
  color: #666;
  text-align: center;
  padding-top: 180px;
}

.log-line {
  color: #d4d4d4;
  word-break: break-all;
  white-space: pre-wrap;
}

.log-error {
  color: #f44747;
}

.log-success {
  color: #4ec9b0;
  font-weight: bold;
}

.log-cancelled {
  color: #dcdcaa;
  font-weight: bold;
}

.action-bar {
  display: flex;
  justify-content: end;
  gap: 10px;
}
</style>
