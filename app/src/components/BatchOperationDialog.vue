<template>
  <!-- 批量操作主弹窗 -->
  <t-dialog
      :visible="visible"
      header="服务器批量操作"
      :width="900"
      :close-on-overlay-click="!batchRunning"
      :close-btn="!batchRunning"
      @close="onDialogClose"
      destroy-on-close
      top="5vh"
      draggable
  >
    <!-- 批量操作状态提示 -->
    <t-alert v-if="batchRunning" theme="info" :message="batchStatusText" class="mb-3"/>

    <!-- 全选 + 已选计数 -->
    <div class="select-header">
      <t-checkbox
          v-model="selectAll"
          :indeterminate="selectIndeterminate"
          :disabled="batchRunning"
          @change="onSelectAllChange"
      >
        全选 / 取消全选
      </t-checkbox>
      <span class="select-count">已选 {{ selectedInstances.length }} / {{ instances.length }}</span>
    </div>

    <!-- 实例竖向列表 -->
    <t-checkbox-group v-model="selectedInstances" :disabled="batchRunning" class="instance-list-wrap">
      <div
          v-for="inst in instances"
          :key="inst.name"
          class="instance-row"
          :class="{ 'is-disabled': batchRunning || !isInstanceSelectable(inst) }"
      >
        <t-checkbox
            :value="inst.name"
            :disabled="batchRunning || !isInstanceSelectable(inst)"
            class="instance-checkbox"
        >
          <template #label>
            <div class="instance-detail">
              <span class="inst-name">{{ inst.name }}</span>
              <t-tag size="small" :theme="statusTagTheme(inst.status)" variant="light" class="inst-status">
                {{ statusLabel(inst.status) }}
              </t-tag>
              <span class="inst-field" title="服务器名称">
                <span class="field-label">名称：</span>{{ inst.config?.ServerName || '-' }}
              </span>
              <span class="inst-field" title="端口">
                <span class="field-label">端口：</span>{{ inst.config?.Port || '-' }}
              </span>
              <span class="inst-field" title="地图">
                <span class="field-label">地图：</span>{{ inst.config?.MapName || '-' }}
              </span>
              <span class="inst-field inst-mods" title="Mod" v-if="inst.config?.ModIDs">
                <span class="field-label">Mod：</span>
                <t-tag
                    v-for="modId in inst.config.ModIDs.split(',').map(m => m.trim()).filter(Boolean)"
                    :key="modId"
                    size="small"
                    theme="primary"
                    variant="light"
                    class="mod-tag"
                >{{ getModName(modId) || modId }}</t-tag>
              </span>
            </div>
          </template>
        </t-checkbox>
      </div>
      <t-empty v-if="instances.length === 0" description="暂无实例"/>
    </t-checkbox-group>

    <!-- 延迟配置 -->
    <div class="delay-area">
      <t-space align="center">
        <span>实例间延迟：</span>
        <t-input-number
            v-model="delaySeconds"
            :min="0"
            :max="300"
            :step="5"
            :disabled="batchRunning"
            size="small"
            style="width: 120px"
        />
        <span>秒</span>
      </t-space>
    </div>

    <template #footer>
      <!-- 操作按钮区 -->
      <div class="action-bar">
        <t-button
            @click="batchOperationHandler('start')"
            theme="primary"
            :disabled="batchRunning || selectedInstances.length === 0"
        >启动选中
        </t-button>
        <t-button
            @click="batchOperationHandler('stop')"
            theme="warning"
            :disabled="batchRunning || selectedInstances.length === 0"
        >停止选中
        </t-button>
        <t-button
            @click="batchOperationHandler('restart')"
            theme="success"
            :disabled="batchRunning || selectedInstances.length === 0"
        >重启选中
        </t-button>
      </div>
    </template>
  </t-dialog>

  <!-- 批量操作日志弹窗（嵌套） -->
  <t-dialog
      v-model:visible="batchLogVisible"
      :header="batchLogHeader"
      :width="800"
      :footer="false"
      :close-on-overlay-click="!batchRunning"
      :close-btn="!batchRunning"
      @close="onBatchLogClose"
  >
    <div class="update-log-container">
      <div id="batchLogContainer" class="update-log">
        <div
            v-for="(log, index) in batchLogs"
            :key="index"
            class="log-line"
            :class="'log-' + log.level"
        >
          <span class="log-time">{{ formatLogTime(log.timestamp) }}</span>
          <span>{{ log.message }}</span>
          <t-button
              v-if="log.instance_name && log.level === 'info' && isInstancePending(log.instance_name)"
              size="small"
              variant="outline"
              theme="warning"
              @click="handleSkipInstance(log.instance_name)"
              class="skip-btn"
          >跳过
          </t-button>
        </div>
      </div>
    </div>
    <div class="batch-log-actions">
      <t-button
          v-if="batchRunning"
          theme="danger"
          variant="outline"
          @click="handleCancelBatch"
          size="small"
      >取消全部
      </t-button>
      <t-space>
        <span v-if="batchProgress" class="progress-text">
          进度：{{ batchProgress.done }} / {{ batchProgress.total }}
        </span>
      </t-space>
    </div>
  </t-dialog>

</template>

<script setup>
import {ref, computed, onMounted, onBeforeUnmount, nextTick, watch} from 'vue'
import {
  batchStartServers,
  batchStopServers,
  batchRestartServers,
  getBatchStatus,
  cancelBatch,
  skipBatchInstance,
} from '@/apis/api.js'
import {streamBatchLogs as streamBatchLogsSSE} from '@/apis/sseApi.js'
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
  },
  modInfo: {
    type: Array,
    default: () => []
  }
})

const emits = defineEmits(['update:visible', 'refresh'])

// ========== 批量操作状态 ==========
const batchRunning = ref(false)
const batchLogVisible = ref(false)
const batchLogs = ref([])
const batchProgress = ref(null)
const batchOpType = ref('')
const delaySeconds = ref(0)
const selectedInstances = ref([])
let batchLogCloseFn = null
let batchCallbackUnreg = null

// ========== 计算属性 ==========
const selectAll = ref(false)
const selectIndeterminate = computed(() => {
  return selectedInstances.value.length > 0 &&
      selectedInstances.value.length < selectableCount.value
})
const selectableCount = computed(() => {
  return props.instances.filter(i => isInstanceSelectable(i)).length
})
const batchStatusText = computed(() => {
  if (!batchRunning.value) return ''
  const typeMap = {start: '启动', stop: '停止', restart: '重启'}
  const text = typeMap[batchOpType.value] || batchOpType.value
  if (batchProgress.value) {
    return `批量${text}操作进行中... (${batchProgress.value.done}/${batchProgress.value.total})`
  }
  return `批量${text}操作进行中...`
})
const batchLogHeader = computed(() => {
  const typeMap = {start: '启动', stop: '停止', restart: '重启'}
  const text = typeMap[batchOpType.value] || batchOpType.value
  return batchRunning.value ? `批量${text}中...` : `批量${text}完成`
})

// ========== 状态中文映射 ==========
const statusLabel = (status) => ({
  start_initialization: '初始化中',
  start_initialization_successful: '初始化完成',
  starting: '启动中', started: '运行中', stopping: '停止中',
  stopped: '已停止', restarting: '重启中', restarted: '运行中',
  start_failed: '启动失败', stop_failed: '停止失败', restart_failed: '重启失败'
}[status] || '已停止')

const statusTagTheme = (status) => ({
  start_initialization: 'primary',
  start_initialization_successful: 'primary',
  starting: 'warning', started: 'success', stopping: 'warning',
  stopped: 'default', restarting: 'warning', restarted: 'success',
  start_failed: 'danger', stop_failed: 'danger', restart_failed: 'danger'
}[status] || 'default')

// ========== Mod 名称映射 ==========
function getModName(modId) {
  if (!modId) return null
  const mod = props.modInfo.find(m => m.id === modId)
  return mod ? mod.name : null
}

// ========== 实例可选性判断 ==========
function isInstanceSelectable(inst) {
  return true
}

function isInstancePending(instanceName) {
  if (!batchRunning.value) return false
  return true
}

// ========== 全选逻辑 ==========
function onSelectAllChange(val) {
  if (val) {
    selectedInstances.value = props.instances.map(i => i.name)
  } else {
    selectedInstances.value = []
  }
}

watch(selectedInstances, (val) => {
  selectAll.value = val.length === props.instances.length && props.instances.length > 0
}, {deep: true})

// ========== 弹窗关闭 ==========
function onDialogClose() {
  if (!batchRunning.value) {
    emits('update:visible', false)
  }
}

// ========== 批量操作 ==========
function batchOperationHandler(opType) {
  const typeMap = {start: '启动', stop: '停止', restart: '重启'}
  const text = typeMap[opType]

  let confirmDialog = DialogPlugin.confirm({
    header: '确认',
    body: `确定要${text}选中的 ${selectedInstances.value.length} 个服务器实例吗？`,
    confirmBtn: '确定',
    cancelBtn: '取消',
    onConfirm: async () => {
      confirmDialog.hide()
      try {
        const apiMap = {
          start: batchStartServers,
          stop: batchStopServers,
          restart: batchRestartServers
        }
        const res = await apiMap[opType](selectedInstances.value, delaySeconds.value)

        batchRunning.value = true
        batchOpType.value = opType
        batchLogs.value = []
        batchProgress.value = {done: 0, total: res.data?.total || selectedInstances.value.length}
        batchLogVisible.value = true

        startLogSubscription()
      } catch (error) {
        MessagePlugin.error(error.message || `${text}操作失败`)
      }
    }
  })
}

// ========== 日志订阅 ==========
function startLogSubscription() {
  if (batchLogCloseFn) {
    batchLogCloseFn()
    batchLogCloseFn = null
  }

  batchLogCloseFn = streamBatchLogsSSE(
      (entry) => {
        batchLogs.value.push(entry)
        if (entry.level === 'success' || entry.level === 'error' ||
            entry.level === 'warning' || entry.level === 'completed') {
          updateProgressFromLog()
        }
        nextTick(() => {
          const container = document.getElementById('batchLogContainer')
          if (container) {
            container.scrollTop = container.scrollHeight
          }
        })
        if (entry.level === 'completed') {
          onBatchCompleted()
        }
      },
      (error) => {
        console.error('Batch log SSE error:', error)
      },
      () => {
        onBatchCompleted()
      }
  )
}

function updateProgressFromLog() {
  if (!batchProgress.value) return
  let done = 0
  for (const log of batchLogs.value) {
    if (log.level === 'success' || log.level === 'error') {
      done++
    }
  }
  batchProgress.value = {...batchProgress.value, done}
}

function onBatchCompleted() {
  batchRunning.value = false
  if (batchLogCloseFn) {
    batchLogCloseFn()
    batchLogCloseFn = null
  }
  emits('refresh')
  selectedInstances.value = []
}

function onBatchLogClose() {
  if (batchLogCloseFn) {
    batchLogCloseFn()
    batchLogCloseFn = null
  }
}

// ========== 取消/跳过 ==========
async function handleCancelBatch() {
  try {
    await cancelBatch()
    MessagePlugin.info('已发送取消指令')
  } catch (error) {
    MessagePlugin.error('取消失败')
  }
}

async function handleSkipInstance(instanceName) {
  try {
    await skipBatchInstance(instanceName)
  } catch (error) {
    MessagePlugin.error('跳过失败')
  }
}

// ========== 批量状态恢复 ==========
async function hydrateBatchStatus() {
  try {
    const res = await getBatchStatus()
    if (res.data?.active) {
      batchRunning.value = true
      batchOpType.value = res.data.type || ''
      if (res.data.progress) {
        batchProgress.value = res.data.progress
      }
      if (!batchLogCloseFn) {
        startLogSubscription()
      }
    } else if (serverStore.batchRunning) {
      batchRunning.value = true
      batchOpType.value = serverStore.batchOpType
      if (serverStore.batchProgress) {
        batchProgress.value = {...serverStore.batchProgress}
      }
    }
  } catch (e) {
    // ignore
  }
}

function handleBatchStoreEvent(eventType, event) {
  if (eventType === 'batch_started') {
    batchRunning.value = true
    batchOpType.value = event.data?.type || event.status || ''
    if (event.data?.total != null) {
      batchProgress.value = {done: 0, total: event.data.total}
    }
  } else if (eventType === 'batch_progress') {
    if (event.data?.done != null && event.data?.total != null) {
      batchProgress.value = {done: event.data.done, total: event.data.total}
    }
  } else if (eventType === 'batch_completed') {
    batchRunning.value = false
    batchProgress.value = null
    emits('refresh')
  }
}

// ========== 格式化 ==========
function formatLogTime(ts) {
  if (!ts) return ''
  const d = new Date(ts)
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:${String(d.getSeconds()).padStart(2, '0')}`
}

// ========== 生命周期 ==========
onMounted(() => {
  hydrateBatchStatus()
  batchCallbackUnreg = (eventType, event) => handleBatchStoreEvent(eventType, event)
  serverStore.batchCallbacks.push(batchCallbackUnreg)
  if (serverStore.batchRunning) {
    batchRunning.value = true
    batchOpType.value = serverStore.batchOpType
    if (serverStore.batchProgress) {
      batchProgress.value = {...serverStore.batchProgress}
    }
  }
})

onBeforeUnmount(() => {
  if (batchLogCloseFn) {
    batchLogCloseFn()
  }
  if (batchCallbackUnreg) {
    const idx = serverStore.batchCallbacks.indexOf(batchCallbackUnreg)
    if (idx > -1) {
      serverStore.batchCallbacks.splice(idx, 1)
    }
  }
})
</script>

<style scoped lang="less">
.mb-3 {
  margin-bottom: 12px;
}

.select-header {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 10px;
}

.select-count {
  color: #888;
  font-size: 13px;
}

.instance-list-wrap {
  height: 500px;
  overflow-y: auto;
  overflow-x: hidden;
  border: 1px solid #e5e7eb;
  border-radius: 6px;
  background: #fafafa;
  padding: 8px;
  margin-bottom: 16px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  flex-wrap: nowrap;
}

.instance-row {
  border-bottom: 1px solid #f0f0f0;
  padding: 8px 4px;
  border-radius: 4px;
  transition: background 0.15s;

  &:hover {
    background: #f0f7ff;
  }

  &:last-child {
    border-bottom: none;
  }

  &.is-disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
}

.instance-checkbox {
  width: 100%;
}

.instance-detail {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  font-size: 13px;
}

.inst-name {
  font-weight: 600;
  font-size: 14px;
  min-width: 100px;
  color: #1a1a1a;
}

.inst-status {
  flex-shrink: 0;
}

.inst-field {
  color: #555;
  display: flex;
  align-items: center;
  gap: 2px;

  .field-label {
    color: #999;
    font-size: 12px;
  }
}

.inst-mods {
  flex-wrap: wrap;
  gap: 4px;

  .mod-tag {
    margin: 1px 2px;
  }
}

.delay-area {
  margin-bottom: 16px;
}

.action-bar {
  display: flex;
  justify-content: end;
  gap: 10px;
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

.log-line {
  color: #d4d4d4;
  word-break: break-all;
  white-space: pre-wrap;
  display: flex;
  align-items: center;
  gap: 6px;
}

.log-time {
  color: #6a9955;
  flex-shrink: 0;
}

.log-success {
  color: #4ec9b0;
}

.log-error {
  color: #f44747;
}

.log-warning {
  color: #dcdcaa;
}

.log-completed {
  color: #569cd6;
  font-weight: bold;
}

.log-info {
  color: #d4d4d4;
}

.skip-btn {
  margin-left: auto;
  flex-shrink: 0;
}

.batch-log-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 12px;
}

.progress-text {
  color: #888;
  font-size: 13px;
}
</style>
