<template>
  <!-- 服务器更新弹窗 -->
  <t-dialog
      :visible="visible"
      header="服务器更新"
      :width="800"
      :close-on-overlay-click="footerMode !== 'exit'"
      :close-btn="footerMode !== 'exit'"
      @close="onDialogClose"
      @opened="onDialogOpened"
      destroy-on-close
      top="5vh"
      attach="body"
  >
    <!-- 更新状态提示 -->
    <t-alert v-if="footerMode === 'exit'" theme="info" message="服务器更新进行中，请勿关闭页面..." class="mb-3"/>
    <t-alert v-if="updateCompleted" theme="success" message="服务器更新已完成" class="mb-3"/>
    <t-alert v-if="updateCancelled" theme="warning" message="服务器更新已取消" class="mb-3"/>
    <t-alert v-if="updateFailed" theme="error" message="服务器更新失败" class="mb-3"/>

    <!-- 日志区域 -->
    <div class="update-log-container">
      <div id="updateLogContainer" class="update-log">
        <div v-if="updateLogs.length === 0 && footerMode === 'start'" class="log-placeholder">
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
            v-if="footerMode === 'exit'"
            theme="danger"
            variant="outline"
            @click="handleCancelUpdate"
        >退出
        </t-button>
        <t-button
            v-else-if="footerMode === 'close'"
            theme="primary"
            @click="handleCloseDialog"
        >关闭
        </t-button>
        <t-button
            v-else
            theme="primary"
            @click="handleStartUpdate"
            :loading="startingUpdate"
        >开始更新
        </t-button>
      </div>
    </template>
  </t-dialog>
</template>

<script setup>
import {ref, computed, onMounted, onBeforeUnmount, nextTick} from 'vue'
import {getUpdateStatus, cancelUpdate, listInstances} from '@/apis/api.js'
import {updateServer as updateServerSSE} from '@/apis/sseApi.js'
import {MessagePlugin, DialogPlugin} from 'tdesign-vue-next'
import {serverStore} from '@/store/serverStore.js'

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

// 视为「实例仍占用着服务端文件」的状态，取值见后端 state.InstanceStatus
const ACTIVE_STATUSES = [
  'start_initialization',
  'start_initialization_successful',
  'starting',
  'started',
  'stopping',
  'restarting',
  'restarted',
]

// ========== 更新状态 ==========
const updating = ref(false)
const updateLogs = ref([])
const startingUpdate = ref(false)
const updateCompleted = ref(false)
const updateCancelled = ref(false)
const updateFailed = ref(false)
let sseCloseFn = null
let updateCallbackUnreg = null

const footerMode = computed(() => {
  if (updateCompleted.value || updateCancelled.value || updateFailed.value) return 'close'
  if (updating.value) return 'exit'
  return 'start'
})

// ========== 弹窗关闭 ==========
function onDialogClose() {
  if (footerMode.value !== 'exit') {
    emits('update:visible', false)
  }
}

function handleCloseDialog() {
  emits('update:visible', false)
  emits('refresh')
  updateLogs.value = []
  updateCompleted.value = false
  updateCancelled.value = false
  updateFailed.value = false
}

// ========== 弹窗打开时重置 UI 标记 ==========
function onDialogOpened() {
  if (!updating.value) {
    updateCompleted.value = false
    updateCancelled.value = false
    updateFailed.value = false
  }
}

function scrollLogToBottom() {
  nextTick(() => {
    const logContainer = document.getElementById('updateLogContainer')
    if (logContainer) {
      logContainer.scrollTop = logContainer.scrollHeight
    }
  })
}

function handleUpdateLogMessage(message) {
  updateLogs.value.push(message)
  if (message.includes('[COMPLETED]')) {
    updateCompleted.value = true
    updateCancelled.value = false
    updateFailed.value = false
    updating.value = false
  } else if (message.includes('[CANCELLED]')) {
    updateCancelled.value = true
    updateCompleted.value = false
    updateFailed.value = false
    updating.value = false
  } else if (message.startsWith('Error:') || message.startsWith('错误:')) {
    updateFailed.value = true
    updateCompleted.value = false
    updateCancelled.value = false
    updating.value = false
  }
  scrollLogToBottom()
}

// ========== 开始更新 ==========
async function handleStartUpdate() {
  try {
    const {data: {instances}} = await listInstances()
    // running 来自后端的端口监听检查，实例处于启动中（进程已起、端口未绑定）时为 false，
    // 所以额外把这些过渡状态也算作正在运行，与后端的存活判据保持一致
    const runningInstances = instances?.filter(
        i => i.running || ACTIVE_STATUSES.includes(i.status)
    ) || []
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
      updateFailed.value = false
      updateLogs.value = []
      updating.value = true

      await updateServerSSE(
          handleUpdateLogMessage,
          (error) => {
            console.error('更新日志错误:', error)
            handleUpdateLogMessage(error.startsWith('Error:') || error.startsWith('错误:') ? error : `错误: ${error}`)
          },
          () => {
            onUpdatingFinished()
            if (updateCompleted.value) {
              MessagePlugin.success('服务器更新已完成')
            }
            emits('refresh')
          }
      )

      startingUpdate.value = false
    }
  })
}

// ========== 状态恢复 ==========
async function hydrateUpdateStatus() {
  try {
    const res = await getUpdateStatus()
    if (res.data?.running) {
      updating.value = true
      if (updateLogs.value.length === 0) {
        startSSESubscription()
      }
    } else if (serverStore.updateRunning) {
      updating.value = true
      if (updateLogs.value.length === 0) {
        startSSESubscription()
      }
    }
  } catch (e) {
    // ignore
  }
}

function handleUpdateStoreEvent(eventType) {
  if (eventType === 'update_started') {
    updating.value = true
    updateCompleted.value = false
    updateCancelled.value = false
    updateFailed.value = false
  } else if (eventType === 'update_completed') {
    onUpdatingFinished(false)
    emits('refresh')
  } else if (eventType === 'update_cancelled') {
    onUpdatingFinished(true)
    emits('refresh')
  }
}

function startSSESubscription() {
  if (sseCloseFn) {
    sseCloseFn()
    sseCloseFn = null
  }

  updating.value = true

  sseCloseFn = updateServerSSE(
      handleUpdateLogMessage,
      (error) => {
        console.error('更新日志 SSE 错误:', error)
        handleUpdateLogMessage(error.startsWith('Error:') || error.startsWith('错误:') ? error : `错误: ${error}`)
      },
      () => {
        onUpdatingFinished()
      }
  )
}

// ========== 更新结束处理 ==========
function onUpdatingFinished(cancelled = null) {
  updating.value = false

  if (typeof sseCloseFn === 'function') {
    sseCloseFn()
    sseCloseFn = null
  }

  if (updateCompleted.value || updateCancelled.value || updateFailed.value) {
    return
  }

  if (cancelled === true) {
    updateCancelled.value = true
    updateCompleted.value = false
    updateFailed.value = false
    return
  }
  if (cancelled === false) {
    updateCompleted.value = true
    updateCancelled.value = false
    updateFailed.value = false
    return
  }

  const lastLog = updateLogs.value[updateLogs.value.length - 1] || ''
  if (lastLog.includes('[COMPLETED]')) {
    updateCompleted.value = true
    updateCancelled.value = false
    updateFailed.value = false
  } else if (lastLog.includes('[CANCELLED]')) {
    updateCancelled.value = true
    updateCompleted.value = false
    updateFailed.value = false
  } else if (lastLog.startsWith('Error:') || lastLog.startsWith('错误:')) {
    updateFailed.value = true
    updateCompleted.value = false
    updateCancelled.value = false
  }
}

// ========== 退出并取消更新 ==========
function handleCancelUpdate() {
  let confirmDialog = DialogPlugin.confirm({
    header: '确认退出',
    body: '确定要退出并取消服务器更新吗？取消后可能需要重新更新才能使用服务器。',
    theme: 'danger',
    confirmBtn: '确定退出',
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
  hydrateUpdateStatus()
  updateCallbackUnreg = (eventType) => handleUpdateStoreEvent(eventType)
  serverStore.updateCallbacks.push(updateCallbackUnreg)
  if (serverStore.updateRunning) {
    updating.value = true
  }
})

onBeforeUnmount(() => {
  if (typeof sseCloseFn === 'function') {
    sseCloseFn()
    sseCloseFn = null
  }
  if (updateCallbackUnreg) {
    const idx = serverStore.updateCallbacks.indexOf(updateCallbackUnreg)
    if (idx > -1) {
      serverStore.updateCallbacks.splice(idx, 1)
    }
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
