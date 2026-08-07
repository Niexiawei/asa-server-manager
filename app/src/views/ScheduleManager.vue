<template>
  <t-card class="schedule-card layout-card" :bordered="false">
    <template #title>
      <div class="page-title">
        定时任务管理
      </div>
    </template>
    <template #actions>
      <t-space size="8px">
        <t-button variant="outline" @click="refreshAll" :loading="loading">刷新</t-button>
        <t-button theme="primary" @click="openCreate">新建任务</t-button>
      </t-space>
    </template>

    <div class="schedule-body">
      <!-- 左列：任务表格。八列合计约 1230px，窄屏时容器内部横向滚动，
           不会把整个页面撑宽（min-width: 0 是这一点的关键） -->
      <t-card class="t-card-flex-custom">
        <div class="schedule-table-pane">
          <t-table
              :data="tasks"
              :columns="columns"
              row-key="id"
              :loading="loading"
              class="schedule-table"
              bordered
          >
            <template #type="{ row }">
              <t-tag :theme="row.type === 'update' ? 'warning' : 'primary'" variant="light">
                {{ row.type === 'update' ? '更新服务器' : '重启实例' }}
              </t-tag>
            </template>

            <template #rule="{ row }">
              {{ ruleSummary(row) }}
            </template>

            <template #instances="{ row }">
              <span v-if="row.type === 'update'" class="muted">—</span>
              <span v-else-if="!row.instances || row.instances.length === 0">全部实例</span>
              <span v-else>{{ row.instances.join('、') }}</span>
            </template>

            <template #next_run_at="{ row }">
              <span v-if="row.enabled && row.next_run_at">{{ formatTime(row.next_run_at) }}</span>
              <span v-else class="muted">—</span>
            </template>

            <template #last_run="{ row }">
              <div v-if="row.last_run_at">
                <div>{{ formatTime(row.last_run_at) }}</div>
                <div :class="['last-result', row.last_result?.startsWith('失败') ? 'failed' : 'ok']">
                  {{ row.last_result }}
                </div>
              </div>
              <span v-else class="muted">从未执行</span>
            </template>

            <template #enabled="{ row }">
              <t-switch :value="row.enabled" @change="(v) => onToggle(row, v)"/>
            </template>

            <template #op="{ row }">
              <t-space v-if="runningTasks.has(row.id)" size="small" align="center">
                <t-tag theme="warning" variant="light" size="small">{{ phaseLabel(runningTasks.get(row.id).phase) }}</t-tag>
                <t-link theme="danger" @click="onCancelRun(row)">取消</t-link>
              </t-space>
              <t-space v-else size="small">
                <t-link theme="primary" @click="openEdit(row)">编辑</t-link>
                <t-link theme="warning" @click="onRunNow(row)">立即执行</t-link>
                <t-link theme="danger" @click="onDelete(row)">删除</t-link>
              </t-space>
            </template>
          </t-table>
        </div>
      </t-card>

      <t-card class="t-card-flex-custom">
        <!-- 右列：执行日志 -->
        <div class="schedule-log-pane">
          <ScheduleRunLog ref="runLogRef" :tasks="tasks"/>
        </div>
      </t-card>
    </div>

    <t-dialog
        v-model:visible="dialogVisible"
        :header="editingId ? '编辑定时任务' : '新建定时任务'"
        :on-confirm="onSubmit"
        :confirm-btn="{ content: '保存', loading: saving }"
        cancel-btn="取消"
        width="560px"
    >
      <t-form ref="formRef" :data="form" label-width="100px">
        <t-form-item label="任务名称" name="name" :rules="[{ required: true, message: '任务名称不能为空' }]">
          <t-input v-model="form.name" placeholder="例如：每日凌晨重启"/>
        </t-form-item>

        <t-form-item label="任务类型">
          <t-select v-model="form.type">
            <t-option value="restart" label="重启实例"/>
            <t-option value="update" label="更新服务器"/>
          </t-select>
        </t-form-item>

        <!-- 更新会改写 server-files，运行中的实例全靠符号链接指着它，
             所以更新任务必然作用于全部实例，没有「指定实例」的余地 -->
        <t-form-item v-if="form.type === 'update'" label="说明">
          <span class="hint">更新前会自动停止全部实例</span>
        </t-form-item>

        <t-form-item v-else label="作用实例">
          <t-select v-model="form.instances" multiple placeholder="留空表示全部实例" :min-collapsed-num="3">
            <t-option v-for="name in instanceNames" :key="name" :value="name" :label="name"/>
          </t-select>
        </t-form-item>

        <t-form-item label="触发规则">
          <t-select v-model="form.rule">
            <t-option value="daily" label="每天"/>
            <t-option value="interval_days" label="间隔天数"/>
            <t-option value="interval_hours" label="间隔小时"/>
          </t-select>
        </t-form-item>

        <t-form-item v-if="form.rule === 'interval_days'" label="间隔天数" name="every"
                     :rules="[{ required: true, message: '间隔天数不能为空' }, { validator: (val) => Number.isInteger(val) && val >= 1, message: '间隔天数必须大于 0' }]">
          <t-input-number v-model="form.every" :min="1" :max="365"/>
        </t-form-item>

        <t-form-item v-if="form.rule === 'interval_hours'" label="间隔小时" name="every"
                     :rules="[{ required: true, message: '间隔小时不能为空' }, { validator: (val) => Number.isInteger(val) && val >= 1, message: '间隔小时数必须大于 0' }]">
          <t-input-number v-model="form.every" :min="1" :max="8760"/>
        </t-form-item>

        <t-form-item v-if="form.rule !== 'interval_hours'" label="执行时刻" name="at"
                     :rules="[{ required: true, message: '执行时刻不能为空' }]">
          <t-time-picker v-model="form.at" format="HH:mm"/>
        </t-form-item>

        <t-form-item label="启用">
          <t-switch v-model="form.enabled"/>
        </t-form-item>

        <t-divider>停止/重启前的公告</t-divider>

        <!-- update 任务的倒计时作用于「更新前的批量停服」那一步 -->
        <CountdownOptions v-model="form.countdown" @validity="(v) => (countdownValid = v)"/>
      </t-form>
    </t-dialog>
  </t-card>
</template>

<script setup>
import {ref, onMounted, onUnmounted} from 'vue'
import {MessagePlugin, DialogPlugin} from 'tdesign-vue-next'
import {
  listScheduleTasks,
  createScheduleTask,
  updateScheduleTask,
  deleteScheduleTask,
  toggleScheduleTask,
  runScheduleTaskNow,
  listScheduleRuns,
  cancelScheduleRun,
  listInstances,
} from '@/apis/api.js'
import CountdownOptions from '@/components/CountdownOptions.vue'
import ScheduleRunLog from '@/components/ScheduleRunLog.vue'
import {serverStore} from '@/store/serverStore.js'

const PHASE_LABELS = {
  countdown: '倒计时中',
  stopping: '停服中',
  updating: '更新中',
  restoring: '恢复启动中',
  restarting: '重启中',
}

function phaseLabel(phase) {
  return PHASE_LABELS[phase] || '执行中'
}

const columns = [
  {colKey: 'name', title: '名称', width: 160},
  {colKey: 'type', title: '类型', width: 90, align: "center"},
  {colKey: 'rule', title: '规则', width: 140},
  {colKey: 'instances', title: '作用实例', width: 180},
  {colKey: 'next_run_at', title: '下次执行', width: 140},
  {colKey: 'last_run', title: '上次执行', width: 140},
  {colKey: 'enabled', title: '启用', width: 60, align: "center"},
  {colKey: 'op', title: '操作', width: 120},
]

const tasks = ref([])
const instanceNames = ref([])
const loading = ref(false)
const saving = ref(false)
const runLogRef = ref(null)

const dialogVisible = ref(false)
const editingId = ref(null)
const formRef = ref(null)

// taskId -> { runId, phase }。用于渲染「取消」按钮与执行阶段标签。
// 依赖 WS 的 schedule_run_started / schedule_run 维持，进页面时先拉一次兜底
// （任务可能在页面没开着的时候就开始了）
const runningTasks = ref(new Map())
let scheduleRunsPollTimer = null
let onScheduleStoreEvent = null

const countdownValid = ref(true)

const emptyForm = () => ({
  name: '',
  type: 'restart',
  enabled: true,
  rule: 'daily',
  every: 1,
  at: '04:00',
  instances: [],
  countdown: {countdown: 0},
})
const form = ref(emptyForm())

function ruleSummary(row) {
  switch (row.rule) {
    case 'interval_hours':
      return `每 ${row.every} 小时`
    case 'interval_days':
      return `每 ${row.every} 天 ${row.at}`
    case 'daily':
      return `每天 ${row.at}`
    default:
      return row.rule
  }
}

function formatTime(value) {
  if (!value) return '—'
  const d = new Date(value)
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

async function loadTasks() {
  loading.value = true
  try {
    const {data} = await listScheduleTasks()
    tasks.value = data?.tasks ?? []
  } catch (e) {
    MessagePlugin.error(e?.message || '获取定时任务失败')
  } finally {
    loading.value = false
  }
}

// 刷新按钮同时刷新左列任务与右列日志
async function refreshAll() {
  await Promise.all([loadTasks(), runLogRef.value?.loadLogs(), loadRuns()])
}

// 拉一次正在执行的任务列表。作为 WS 事件的兜底：任务可能在页面没开着的时候
// 就开始了，光靠 schedule_run_started 事件不够及时
async function loadRuns() {
  try {
    const {data} = await listScheduleRuns()
    const next = new Map()
    for (const run of data?.runs ?? []) {
      next.set(run.task_id, {runId: run.run_id, phase: run.phase})
    }
    runningTasks.value = next
  } catch (e) {
    console.error('获取正在执行的任务失败:', e)
  }
}

function onScheduleEvent(type, event) {
  if (type === 'schedule_run_started') {
    const d = event?.data
    if (!d?.task_id) return
    runningTasks.value.set(d.task_id, {runId: d.run_id, phase: 'countdown'})
  } else if (type === 'schedule_run') {
    const d = event?.data
    if (d?.task_id) {
      runningTasks.value.delete(d.task_id)
    }
  }
}

function onCancelRun(row) {
  const running = runningTasks.value.get(row.id)
  if (!running) return

  const extraWarning = running.phase === 'updating'
      ? '当前正在更新服务端文件，取消会中断更新，服务端文件可能不完整，建议之后重新执行一次更新。'
      : ''

  const dialog = DialogPlugin.confirm({
    header: '确认取消',
    body: `取消后将停止本次任务，并把执行前正在运行的实例重新启动。${extraWarning}`,
    confirmBtn: {content: '确认取消', theme: 'danger'},
    cancelBtn: '再想想',
    onConfirm: async () => {
      dialog.hide()
      try {
        await cancelScheduleRun(running.runId)
        MessagePlugin.info('已发送取消指令，正在回滚实例状态')
      } catch (e) {
        MessagePlugin.error(e?.message || '取消失败')
      }
    },
  })
}

async function loadInstances() {
  try {
    const {data} = await listInstances()
    instanceNames.value = (data?.instances ?? []).map((i) => i.name)
  } catch (e) {
    // 实例列表只用于下拉候选，取不到不影响其它功能
    console.error('获取实例列表失败:', e)
  }
}

function openCreate() {
  editingId.value = null
  form.value = emptyForm()
  dialogVisible.value = true
}

function openEdit(row) {
  editingId.value = row.id
  form.value = {
    name: row.name,
    type: row.type,
    enabled: row.enabled,
    rule: row.rule,
    every: row.every || 1,
    at: row.at || '04:00',
    instances: row.instances ? [...row.instances] : [],
    countdown: {
      countdown: row.countdown || 0,
      notify_points: row.notify_points ? [...row.notify_points] : [],
      notify_message: row.notify_message || '',
      notify_command: row.notify_command || 'ServerChat',
    },
  }
  dialogVisible.value = true
}

function buildPayload() {
  const payload = {
    name: form.value.name,
    type: form.value.type,
    enabled: form.value.enabled,
    rule: form.value.rule,
    every: form.value.rule === 'daily' ? 1 : Number(form.value.every),
    at: form.value.rule === 'interval_hours' ? '' : form.value.at,
    // 后端拒绝给更新任务指定实例，这里直接清空而不是依赖用户没选过
    instances: form.value.type === 'update' ? [] : form.value.instances,
    countdown: form.value.countdown?.countdown || 0,
    notify_points: form.value.countdown?.notify_points ?? [],
    notify_message: form.value.countdown?.notify_message || '',
    notify_command: form.value.countdown?.notify_command || '',
  }
  return payload
}

async function onSubmit() {
  // 表单字段（名称/间隔/执行时刻）先过一遍客户端校验，与 schedule.Task.Validate() 对齐
  const result = await formRef.value?.validate()
  if (result !== true) {
    return false
  }

  // 后端也会校验，这里拦一道是为了让用户当场看到是哪个字段不对
  if (!countdownValid.value) {
    MessagePlugin.error('倒计时配置有误，请检查')
    return false
  }

  saving.value = true
  try {
    if (editingId.value) {
      await updateScheduleTask(editingId.value, buildPayload())
      MessagePlugin.success('定时任务已更新')
    } else {
      await createScheduleTask(buildPayload())
      MessagePlugin.success('定时任务已创建')
    }
    dialogVisible.value = false
    await loadTasks()
  } catch (e) {
    // http.js 拦截器已经把后端 error 字段提取进了 e.message，不是 e.response.data.error
    MessagePlugin.error(e?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function onToggle(row, enabled) {
  try {
    await toggleScheduleTask(row.id, enabled)
    await loadTasks()
  } catch (e) {
    MessagePlugin.error(e?.message || '操作失败')
    await loadTasks()
  }
}

function onDelete(row) {
  const dialog = DialogPlugin.confirm({
    header: '确认删除',
    body: `确定要删除定时任务「${row.name}」吗？`,
    confirmBtn: {content: '删除', theme: 'danger'},
    cancelBtn: '取消',
    onConfirm: async () => {
      dialog.hide()
      try {
        await deleteScheduleTask(row.id)
        MessagePlugin.success('已删除')
        await loadTasks()
      } catch (e) {
        MessagePlugin.error(e?.message || '删除失败')
      }
    },
  })
}

function onRunNow(row) {
  const body = row.type === 'update'
      ? '将立即停止全部实例并开始更新，确定吗？'
      : `将立即重启${row.instances?.length ? row.instances.join('、') : '全部实例'}，确定吗？`

  const dialog = DialogPlugin.confirm({
    header: '确认执行',
    body,
    confirmBtn: '执行',
    cancelBtn: '取消',
    onConfirm: async () => {
      dialog.hide()
      try {
        await runScheduleTaskNow(row.id)
        // 执行结果会通过 WS 推到右侧日志面板，这里不用轮询
        MessagePlugin.success('已触发执行，结果将出现在右侧执行日志')
      } catch (e) {
        MessagePlugin.error(e?.message || '触发失败')
      }
    },
  })
}

onMounted(() => {
  loadTasks()
  loadInstances()
  loadRuns()
  onScheduleStoreEvent = onScheduleEvent
  serverStore.scheduleCallbacks.push(onScheduleStoreEvent)
  // 15s 兜底轮询：WS 事件是主要信号，轮询只是防它偶尔漏一条
  scheduleRunsPollTimer = setInterval(loadRuns, 15000)
})

onUnmounted(() => {
  if (scheduleRunsPollTimer) {
    clearInterval(scheduleRunsPollTimer)
    scheduleRunsPollTimer = null
  }
  if (onScheduleStoreEvent) {
    const idx = serverStore.scheduleCallbacks.indexOf(onScheduleStoreEvent)
    if (idx !== -1) {
      serverStore.scheduleCallbacks.splice(idx, 1)
    }
  }
})
</script>

<style scoped lang="less">
.schedule-card {
  height: 100%;
  width: 100%;
  display: flex;
  flex-direction: column;
  border-radius: var(--border-radius-large);
  overflow: hidden;

  .schedule-table {
    padding: 0 !important;
  }
}

// 左右两列：左列吃掉剩余宽度，右列固定。
// 日志条目是定长文本块，给它弹性宽度只会让行长忽宽忽窄；
// 需要吃掉剩余空间的是表格。
.schedule-body {
  display: flex;
  align-items: stretch;
  height: 100%;
  min-height: 0;
  gap: 8px;
}

.schedule-table-pane {
  flex: 1;
  // min-width: 0 是关键：没有它，八列表格的固有宽度会把 flex 容器撑开，
  // 横向滚动条跑到整个页面上而不是表格内部
  min-width: 0;
  overflow-x: auto;
  overflow-y: auto;
}

.schedule-log-pane {
  flex: 0 0 380px;
  width: 380px;
  height: 100%;
}

// 窄屏折叠到下方，而不是隐藏或收进抽屉：
// 用户不需要额外操作就能看到日志，只是位置变了
@media (max-width: 1280px) {
  .schedule-body {
    flex-direction: column;
  }

  .schedule-log-pane {
    flex: 0 0 320px;
    width: 100%;
    height: 320px;
    border-left: none;
    border-top: 1px solid var(--td-component-stroke, #e7e7e7);
  }
}

.schedule-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.page-title {
  font-size: 18px;
}

.muted {
  color: var(--td-text-color-placeholder, #999);
}

.hint {
  color: var(--td-text-color-secondary, #888);
  font-size: 13px;
}

.last-result {
  font-size: 12px;

  &.ok {
    color: #22c55e;
  }

  &.failed {
    color: #ef4444;
  }
}
</style>
