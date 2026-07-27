<template>
  <!-- 批量操作主弹窗 -->
  <t-dialog
      :visible="visible"
      header="服务器批量操作"
      width="85vw"
      @close="onDialogClose"
      destroy-on-close
      top="5vh"
      draggable
      attach="body"
      :footer="false"
  >
    <!-- 批量操作状态提示 -->
    <t-alert v-if="batchRunning" theme="info" :message="batchStatusText" class="mb-3"/>

    <div class="batch-operation-body">
      <div class="left-box">
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
              :class="{ 'is-disabled': batchRunning }"
          >
            <t-checkbox
                :value="inst.name"
                class="instance-checkbox"
            >
              <template #label>
                <div class="instance-detail">
                  <span class="inst-name">{{ inst.name }}</span>
                  <t-tag size="small" :theme="statusTagTheme(inst.status)" variant="light" class="inst-status">
                    {{ statusLabel(inst.status) }}
                  </t-tag>
                  <t-tag
                      v-if="batchStatusTag(inst.name)"
                      size="small"
                      :theme="batchStatusTag(inst.name).theme"
                      variant="light"
                      class="inst-queued"
                  >{{ batchStatusTag(inst.name).label }}
                  </t-tag>
                  <t-tag
                      v-if="countdownText(inst.name)"
                      size="small"
                      theme="warning"
                      variant="light"
                      class="inst-countdown"
                  >{{ countdownText(inst.name) }}
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
                <t-popup trigger="hover" placement="right-bottom"
                         overlayClassName="inst-mods-popup"
                         showArrow
                >
                  <span
                      class="field-label">Mod:
                  <t-link theme="primary">({{
                      inst.config.ModIDs.split(',').map(m => m.trim()).filter(Boolean).length
                    }})</t-link>
                </span>
                  <template #content>
                    <div class="mod-list">
                      <t-tag
                          v-for="modId in inst.config.ModIDs.split(',').map(m => m.trim()).filter(Boolean)"
                          :key="modId"
                          size="small"
                          theme="primary"
                          variant="light"
                          class="mod-tag"
                      >{{ getModName(modId) || modId }}</t-tag>
                    </div>
                  </template>
                </t-popup>
              </span>
                </div>
              </template>
            </t-checkbox>
            <t-button
                v-if="isInstancePending(inst.name)"
                size="small"
                variant="outline"
                theme="warning"
                class="skip-btn"
                @click="handleSkipInstance(inst.name)"
            >跳过
            </t-button>
          </div>
          <t-empty v-if="instances.length === 0" description="暂无实例"/>
        </t-checkbox-group>

        <!-- 延迟配置 -->
        <div class="footer-area">
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
        </div>
      </div>
      <div class="right-box">
        <div class="select-header">批量操作日志</div>
        <div class="update-log-container">
          <div id="batchLogContainer" class="update-log">
            <div v-if="batchLogs.length === 0" class="log-line">
              暂无操作日志...
            </div>
            <div
                v-for="(log, index) in batchLogs"
                :key="index"
                class="log-line"
                :class="'log-' + log.level"
            >
              <span class="log-time">{{ formatLogTime(log.timestamp) }}</span>
              <span>{{ log.message }}</span>
            </div>
          </div>
        </div>
        <div class="batch-log-actions">
          <t-button
              theme="danger"
              @click="handleCancelBatch"
              :disabled="!batchRunning"
          >取消全部
          </t-button>
          <span v-if="batchProgress" class="progress-text">
            进度：{{ batchProgress.done }} / {{ batchProgress.total }}
          </span>
        </div>
      </div>
    </div>
  </t-dialog>

  <!-- 批量停止/重启确认弹窗（内含倒计时选项） -->
  <CountdownConfirmDialog ref="countdownDialogRef"/>
</template>

<script setup>
import {ref, computed, onMounted, onBeforeUnmount, onDeactivated, nextTick, watch} from 'vue'
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
import {statusLabel, statusTagTheme, countdownText} from '@/composables/useInstanceState.js'
import CountdownConfirmDialog from '@/components/CountdownConfirmDialog.vue'

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

const OP_TYPE_LABELS = {start: '启动', stop: '停止', restart: '重启'}

// ========== 批量操作状态 ==========
const batchRunning = ref(false)
const batchLogs = ref([])
// 日志拉取中：清空后到第一条到达之间为 true，用于抑制「暂无日志」占位闪烁
const batchProgress = ref(null)
const batchOpType = ref('')
const delaySeconds = ref(0)
const selectedInstances = ref([])
const countdownDialogRef = ref(null)
// 本次批量操作中每个实例的执行状态：pending / running / success / failed / skipped / cancelled
const instanceOpStatus = ref(new Map())
let batchLogCloseFn = null
let logSubscriptionSeq = 0
let batchStoreCallback = null

// ========== 计算属性 ==========
const selectAll = ref(false)
const selectIndeterminate = computed(() => {
  return selectedInstances.value.length > 0 &&
      selectedInstances.value.length < props.instances.length
})
const batchStatusText = computed(() => {
  if (!batchRunning.value) return ''
  const text = OP_TYPE_LABELS[batchOpType.value] || batchOpType.value
  if (batchProgress.value) {
    return `批量${text}操作进行中... (${batchProgress.value.done}/${batchProgress.value.total})`
  }
  return `批量${text}操作进行中...`
})

// ========== Mod 名称映射 ==========
function getModName(modId) {
  if (!modId) return null
  const mod = props.modInfo.find(m => m.id === modId)
  return mod ? mod.name : null
}

// ========== 实例排队状态 ==========
// 只标注「待处理」的两个状态；实例自身生命周期已由 statusLabel(inst.status) 表达
const BATCH_STATUS_TAG = {
  pending: {label: '排队中', theme: 'warning'},
  skip_requested: {label: '待跳过', theme: 'default'}
}

function batchStatusTag(instanceName) {
  if (!batchRunning.value) return null
  return BATCH_STATUS_TAG[instanceOpStatus.value.get(instanceName)] || null
}

// 后端只在轮到某个实例之前检查一次 skip 信号，一旦开始执行就跳不掉了，
// 因此只有仍处于 pending 的实例才允许跳过
function isInstancePending(instanceName) {
  return batchRunning.value && instanceOpStatus.value.get(instanceName) === 'pending'
}

function setInstanceOpStatus(instanceName, status) {
  const next = new Map(instanceOpStatus.value)
  next.set(instanceName, status)
  instanceOpStatus.value = next
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
})

// ========== 弹窗开关 ==========
// 允许在批量运行中关闭：日志流跟着弹窗走，关闭即断开。
// 关闭期间不会丢东西——运行态与进度由 WebSocket 经 serverStore 推送，与这条 SSE 无关；
// 后端保留最近 500 条日志，重开时会完整回放
function onDialogClose() {
  emits('update:visible', false)
}

watch(() => props.visible, (visible) => {
  if (!visible) {
    stopLogSubscription()
    return
  }
  // 重开时与后端同步一次，兜底关闭期间的状态漂移（如别处发起的批量）
  hydrateBatchStatus()
  ensureLogSubscription()
  // destroy-on-close 重建了日志容器，滚动位置需要重新贴底
  scrollLogToBottom()
})

// ========== 批量操作 ==========
// 启动不提供倒计时：服务器是关着的，没有玩家可公告，
// 而后端 runCountdownPhase 也只按 stop/restart 区分文案
function batchOperationHandler(opType) {
  if (opType === 'start') {
    confirmPlainBatch(opType)
  } else {
    confirmBatchWithCountdown(opType)
  }
}

function confirmPlainBatch(opType) {
  const text = OP_TYPE_LABELS[opType]
  const confirmDialog = DialogPlugin.confirm({
    header: '确认',
    body: `确定要${text}选中的 ${selectedInstances.value.length} 个服务器实例吗？`,
    confirmBtn: '确定',
    cancelBtn: '取消',
    onConfirm: () => {
      confirmDialog.destroy()
      submitBatch(opType)
    }
  })
}

async function confirmBatchWithCountdown(opType) {
  const text = OP_TYPE_LABELS[opType]
  const countdown = await countdownDialogRef.value?.open('', opType, {
    header: `批量${text}`,
    prompt: `确定要${text}选中的 ${selectedInstances.value.length} 个服务器实例吗？`
  })
  if (!countdown) return

  submitBatch(opType, countdown)
}

async function submitBatch(opType, countdown) {
  const instances = [...selectedInstances.value]
  try {
    let res
    if (opType === 'start') {
      res = await batchStartServers(instances, delaySeconds.value)
    } else if (opType === 'stop') {
      res = await batchStopServers(instances, delaySeconds.value, countdown)
    } else {
      res = await batchRestartServers(instances, delaySeconds.value, countdown)
    }

    batchRunning.value = true
    batchOpType.value = opType
    batchProgress.value = {done: 0, total: res.data?.total || instances.length}
    instanceOpStatus.value = new Map(instances.map(name => [name, 'pending']))

    // 弹窗此刻必然开着；已有连接就复用，新日志会顺着同一条流推过来
    ensureLogSubscription()
  } catch (error) {
    MessagePlugin.error(error.message || `${OP_TYPE_LABELS[opType]}操作失败`)
  }
}

// ========== 日志订阅 ==========
// sseApi 返回的关闭函数一定会回调 onClose，因此用代次令牌让旧订阅的回调失效，
// 否则重新订阅会把仍在运行的批量误判为已完成
function stopLogSubscription() {
  if (!batchLogCloseFn) return
  const closeFn = batchLogCloseFn
  batchLogCloseFn = null
  logSubscriptionSeq++
  closeFn()
}

// ensureLogSubscription 是唯一该被外部调用的订阅入口。
//
// 只在弹窗打开时才连日志流：本组件挂在 ServerManager 上、无 v-if，
// 而 ServerManager 又被 KeepAlive 缓存，不设这道闸的话首页一加载就会开一条流并一直挂着。
// 已有连接就复用——后端是长连接，跨多轮批量都不用重连。
function ensureLogSubscription() {
  if (!props.visible) return
  if (batchLogCloseFn) return
  startLogSubscription()
}

function startLogSubscription() {
  stopLogSubscription()
  const seq = ++logSubscriptionSeq
  const isCurrent = () => seq === logSubscriptionSeq

  // 后端订阅时会回放完整历史日志，必须先清空，否则重连会产生重复行。
  // 清空到第一条日志到达之间不显示「暂无日志」，免得闪一下
  batchLogs.value = []

  batchLogCloseFn = streamBatchLogsSSE(
      (entry) => {
        if (!isCurrent()) return
        batchLogs.value.push(entry)
        applyLogToInstanceStatus(entry)
        if (entry.level === 'success' || entry.level === 'error' ||
            entry.level === 'warning' || entry.level === 'completed') {
          updateProgressFromLog()
        }
        scrollLogToBottom()
        if (entry.level === 'completed') {
          onBatchCompleted()
        }
      },
      (error) => {
        if (!isCurrent()) return
        console.error('Batch log SSE error:', error)
      },
      () => {
        if (!isCurrent()) return
        // 走到这里说明连接是真的断了（服务端退出或致命错误）——主动关闭有 isCurrent 守卫拦着。
        // 句柄要清掉，否则 ensureLogSubscription 会被僵尸句柄挡住，重开弹窗收不到日志
        batchLogCloseFn = null
        onBatchCompleted()
      }
  )
}

function scrollLogToBottom() {
  nextTick(() => {
    const logContainer = document.getElementById('batchLogContainer')
    if (logContainer) {
      logContainer.scrollTop = logContainer.scrollHeight
    }
  })
}

// 后端日志中带 instance_name 的条目即该实例的状态推进
// （不带实例名的 warning 属于整体消息，如 "Batch operation cancelled"）
const LOG_LEVEL_TO_OP_STATUS = {
  info: 'running',
  success: 'success',
  error: 'failed',
  warning: 'skipped'
}

function applyLogToInstanceStatus(entry) {
  if (!entry.instance_name) return
  const status = LOG_LEVEL_TO_OP_STATUS[entry.level]
  if (status) {
    setInstanceOpStatus(entry.instance_name, status)
  }
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
  if (!batchRunning.value) return
  batchRunning.value = false
  // 不断开日志流：它是跨操作的长连接，只跟着弹窗的开关走。
  // 这里断开的话，紧接着再发起一轮批量就得重连，又回到「频繁建连」的老毛病
  emits('refresh')
  selectedInstances.value = []
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
  // 乐观更新，按钮立即消失
  setInstanceOpStatus(instanceName, 'skip_requested')
  try {
    await skipBatchInstance(instanceName)
    MessagePlugin.success(`已请求跳过 ${instanceName}`)
  } catch (error) {
    MessagePlugin.error(error.message || '跳过失败')
  } finally {
    // 以服务端为准：成功时确认，失败时回滚乐观更新
    hydrateBatchStatus()
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
      if (Array.isArray(res.data.instances)) {
        instanceOpStatus.value = new Map(
            res.data.instances.map(r => [r.instance_name, r.status])
        )
      }
      ensureLogSubscription()
      return
    }

    if (serverStore.batchRunning) {
      batchRunning.value = true
      batchOpType.value = serverStore.batchOpType
      if (serverStore.batchProgress) {
        batchProgress.value = {...serverStore.batchProgress}
      }
      return
    }

    if (batchRunning.value) {
      // 后端已无进行中的批量，纠正本地残留的运行态
      // （例如弹窗关闭期间批量结束，而 WS 完成事件恰好丢失）
      onBatchCompleted()
    }

    // 没有批量在跑：连上去把上一轮的日志拉回来，并继续挂着等新日志。
    // 后端是长连接，回放完历史不会关流，之后新发起的批量也走这同一条
    ensureLogSubscription()
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
    // 事件本身不带实例名，回查 /batch/status 补齐实例状态并订阅日志，
    // 使别处（其它标签页 / GUI）发起的批量在本页也能看到日志与跳过按钮
    hydrateBatchStatus()
  } else if (eventType === 'batch_progress') {
    if (event.data?.done != null && event.data?.total != null) {
      batchProgress.value = {done: event.data.done, total: event.data.total}
    }
  } else if (eventType === 'batch_instance_skipped') {
    if (event.instance_name) {
      setInstanceOpStatus(event.instance_name, 'skip_requested')
    }
  } else if (eventType === 'batch_completed') {
    // 复用统一收尾逻辑，确保 SSE 连接被关闭
    onBatchCompleted()
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
  // 只同步状态，不连日志流——挂载时弹窗通常是关着的，ensureLogSubscription 会自行拦下
  hydrateBatchStatus()
  batchStoreCallback = (eventType, event) => handleBatchStoreEvent(eventType, event)
  serverStore.batchCallbacks.push(batchStoreCallback)
})

// ServerManager 被 KeepAlive 缓存，切到别的页面时只 deactivate 不 unmount，
// 只靠 onBeforeUnmount 的话这条流会在后台一直挂着
onDeactivated(() => {
  stopLogSubscription()
})

onBeforeUnmount(() => {
  stopLogSubscription()
  if (batchStoreCallback) {
    const idx = serverStore.batchCallbacks.indexOf(batchStoreCallback)
    if (idx > -1) {
      serverStore.batchCallbacks.splice(idx, 1)
    }
  }
})
</script>

<style scoped lang="less">
.batch-operation-body {
  display: flex;
  align-items: start;
  gap: 8px;
  width: 100%;

  > div {
    width: 50%;
    height: 600px;
  }
}

.mb-3 {
  margin-bottom: 12px;
}

.mod-list {
  display: flex;
  max-width: 600px;
  flex-wrap: wrap;
  gap: 6px;
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

.instance-row {
  border-bottom: 1px solid #f0f0f0;
  padding: 8px 4px;
  border-radius: 4px;
  transition: background 0.15s;
  display: flex;
  align-items: center;
  gap: 8px;

  &:hover {
    background: #f0f7ff;
  }

  &:last-child {
    border-bottom: none;
  }

  // 批量运行时只置灰勾选区域，跳过按钮仍需保持可点
  &.is-disabled .instance-checkbox {
    opacity: 0.6;
    cursor: not-allowed;
  }
}

.instance-checkbox {
  flex: 1 1 auto;
  min-width: 0;
}

.inst-queued,
.inst-countdown {
  flex-shrink: 0;
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

.action-bar {
  display: flex;
  justify-content: end;
  gap: 10px;
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
  // 日志区高度由 .right-box 撑开，用相对值而不是写死的 padding
  padding-top: 40%;
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
  flex-shrink: 0;
}

.left-box {
  display: flex;
  flex-direction: column;

  .select-header {
    flex: 0 0 auto;
  }

  .instance-list-wrap {
    overflow-y: auto;
    overflow-x: hidden;
    border: 1px solid #e5e7eb;
    border-radius: 6px;
    background: #fafafa;
    padding: 8px;
    display: flex;
    flex-direction: column;
    gap: 4px;
    flex-wrap: nowrap;
    flex: 1 1 auto;
    min-height: 0;
  }

  .footer-area {
    margin-top: 12px;
    flex: 0 0 auto;
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
}

.right-box {
  display: flex;
  flex-direction: column;

  .select-header {
    flex: 0 0 auto;
  }

  .batch-log-actions {
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: end;
    gap: 12px;
    margin-top: 12px;
    flex: 0 0 auto;
  }

  .update-log-container {
    position: relative;
    overflow: hidden;
    flex: 1 1 auto;
    min-height: 0;
  }
}

.progress-text {
  color: #888;
  font-size: 13px;
}
</style>
