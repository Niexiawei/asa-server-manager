<template>
  <div class="run-log">
    <div class="run-log-header">
      <div class="run-log-title">
        执行日志
        <span class="run-log-count" v-if="total > 0">共 {{ total }} 条</span>
      </div>
      <t-space size="4px">
        <t-select
            v-model="filterTaskId"
            size="small"
            class="run-log-filter"
            :popup-props="{ overlayInnerStyle: { width: '200px' } }"
            @change="loadLogs"
        >
          <t-option value="" label="全部任务"/>
          <t-option v-for="t in tasks" :key="t.id" :value="t.id" :label="t.name"/>
        </t-select>
        <t-button size="small" variant="text" shape="square" title="刷新" @click="loadLogs">
          <template #icon>
            <t-icon name="refresh"/>
          </template>
        </t-button>
        <t-popconfirm content="确定清空全部执行日志吗？此操作不可撤销。" @confirm="onClear">
          <t-button size="small" variant="text" shape="square" title="清空" :disabled="total === 0">
            <template #icon>
              <t-icon name="delete"/>
            </template>
          </t-button>
        </t-popconfirm>
      </t-space>
    </div>

    <div class="run-log-container">
      <div class="run-log-body" v-loading="loading">
        <div v-if="!loading && logs.length === 0" class="log-line">暂无执行记录...</div>

        <div
            v-for="log in logs"
            :key="log.id"
            :class="['log-entry', log.success ? 'ok' : 'failed']"
        >
          <div class="log-line">
            <span class="log-time">{{ formatTime(log.started_at) }}</span>
            <span class="log-trigger">[{{ log.trigger === 'manual' ? '手动' : '定时' }}]</span>
            <span class="log-task" :title="log.task_name">{{ log.task_name }}</span>
            <span :class="['log-status', log.success ? 'ok' : 'failed']">
              {{ log.success ? '成功' : '失败' }}
            </span>
            <span class="log-dur">{{ formatDuration(log.duration_ms) }}</span>
          </div>
          <div class="log-message" v-if="log.message">
            <span class="log-arrow">{{ log.success ? '›' : '✗' }}</span>
            <span>{{ log.message }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import {ref, onMounted, onUnmounted} from 'vue'
import {MessagePlugin} from 'tdesign-vue-next'
import {listScheduleLogs, clearScheduleLogs} from '@/apis/api.js'
import {serverStore} from '@/store/serverStore.js'

// tasks 供筛选下拉使用，由父组件传入，避免这里重复拉一次任务列表
const props = defineProps({
  tasks: {type: Array, default: () => []},
})

const logs = ref([])
const total = ref(0)
const loading = ref(false)
const filterTaskId = ref('')

// 与后端 maxRunRecords 对齐：请求满量，列表在前端本地过滤即可
const FETCH_LIMIT = 500

async function loadLogs() {
  loading.value = true
  try {
    const {data} = await listScheduleLogs(filterTaskId.value, FETCH_LIMIT)
    logs.value = data?.logs ?? []
    total.value = data?.total ?? 0
  } catch (e) {
    MessagePlugin.error('获取执行日志失败')
  } finally {
    loading.value = false
  }
}

async function onClear() {
  try {
    await clearScheduleLogs()
    MessagePlugin.success('执行日志已清空')
    await loadLogs()
  } catch (e) {
    MessagePlugin.error(e?.response?.data?.error || '清空失败')
  }
}

// WS 推来的新记录直接插到表头，省掉一次往返。
// 当前有筛选时，只接受属于该任务的记录。
function onScheduleEvent(type, event) {
  if (type !== 'schedule_run') return

  const record = event?.data
  if (!record?.id) return

  if (filterTaskId.value && record.task_id !== filterTaskId.value) return

  logs.value.unshift(record)
  total.value += 1

  // 后端是滚动窗口，前端也跟着裁，避免长期开着页面把列表撑爆
  if (logs.value.length > FETCH_LIMIT) {
    logs.value.length = FETCH_LIMIT
  }
}

function formatTime(value) {
  if (!value) return '—'
  // 后端推的是毫秒时间戳，REST 返回的是 RFC3339 字符串，两种都要认
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'

  const pad = (n) => String(n).padStart(2, '0')
  const sameYear = d.getFullYear() === new Date().getFullYear()
  const date = sameYear
      ? `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
      : `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  return `${date} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function formatDuration(ms) {
  if (!ms || ms < 0) return '0s'
  if (ms < 1000) return `${ms}ms`

  const totalSeconds = Math.round(ms / 1000)
  if (totalSeconds < 60) return `${totalSeconds}s`

  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  if (minutes < 60) return `${minutes}m${String(seconds).padStart(2, '0')}s`

  const hours = Math.floor(minutes / 60)
  return `${hours}h${String(minutes % 60).padStart(2, '0')}m`
}

onMounted(() => {
  loadLogs()
  serverStore.scheduleCallbacks.push(onScheduleEvent)
})

onUnmounted(() => {
  const idx = serverStore.scheduleCallbacks.indexOf(onScheduleEvent)
  if (idx !== -1) {
    serverStore.scheduleCallbacks.splice(idx, 1)
  }
})

defineExpose({loadLogs})
</script>

<style scoped lang="less">
// 不在这里画外边框：组件不知道自己被放在右侧还是下方，
// 边框由父容器的 pane 负责
.run-log {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.run-log-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 12px 12px 10px;
  border-bottom: 1px solid var(--td-component-stroke, #e7e7e7);
  flex-shrink: 0;
}

.run-log-title {
  font-size: 15px;
  font-weight: 500;
  white-space: nowrap;
}

.run-log-count {
  margin-left: 6px;
  font-size: 12px;
  font-weight: 400;
  color: var(--td-text-color-placeholder, #999);
}

.run-log-filter {
  width: 120px;
}

// 深色终端日志区，与 BatchOperationDialog 的黑色窗口保持一致的视觉语言
.run-log-container {
  flex: 1;
  min-height: 0;
  padding: 8px 0;
  box-sizing: border-box;
}

.run-log-body {
  width: 100%;
  height: 100%;
  box-sizing: border-box;
  overflow-y: auto;
  padding: 12px;
  border: 1px solid #e5e7eb;
  border-radius: 4px;
  background-color: #1e1e1e;
  font-family: 'Monaco', 'Courier New', monospace;
  font-size: 12px;
  line-height: 1.6;
}

.log-placeholder {
  color: #666;
  text-align: center;
  padding-top: 40%;
}

.log-entry {
  margin-bottom: 8px;
}

.log-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  color: #d4d4d4;
  word-break: break-all;
}

// VS Code 暗色语义配色，与 BatchOperationDialog 的 .log-* 对齐
.log-time {
  color: #6a9955; // 绿：时间
  flex-shrink: 0;
}

.log-trigger {
  color: #808080; // 灰：触发来源
  flex-shrink: 0;
}

.log-task {
  color: #569cd6; // 蓝：任务名
  font-weight: bold;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 160px;
}

.log-status {
  flex-shrink: 0;

  &.ok {
    color: #4ec9b0; // 青：成功
  }

  &.failed {
    color: #f44747; // 红：失败
  }
}

.log-dur {
  color: #808080;
  flex-shrink: 0;
  margin-left: auto;
}

.log-message {
  margin-top: 2px;
  padding-left: 4px;
  color: #d4d4d4;
  word-break: break-word;
  white-space: pre-wrap;
  display: flex;
  gap: 6px;
}

.log-arrow {
  flex-shrink: 0;
  color: #6a9955;
}

.log-entry.failed .log-message {
  color: #f44747;

  .log-arrow {
    color: #f44747;
  }
}
</style>
