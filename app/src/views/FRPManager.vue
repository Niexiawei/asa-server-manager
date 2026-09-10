<template>
  <t-card class="frp-card layout-card" :bordered="false">
    <template #title>
      <div class="frp-header">
        <div class="header-left">
          <span class="page-title">端口映射(Frp)</span>
          <check-icon v-if="frpStatus === 'running'"
                      style="color: #22c55e; font-size: 18px;"/>
          <close-icon v-else style="color: #ef4444; font-size: 18px;"/>
          <span v-if="statusMessage" class="status-message" :title="statusMessage">
            {{ statusMessage }}
          </span>
        </div>
        <t-space size="small">
          <t-button size="small" theme="primary" @click="startFRPAction" :disabled="frpStatus === 'running'">启动
          </t-button>
          <t-button size="small" theme="danger" @click="stopFRPAction" :disabled="frpStatus === 'stopped'">停止
          </t-button>
          <t-button size="small" theme="warning" @click="restartFRPAction">重启</t-button>
        </t-space>
      </div>
    </template>

    <div class="frp-container">
      <div class="config-panel">
        <div class="panel-header">
          <h3>连接配置</h3>
          <t-space>
            <t-button theme="primary" size="small" @click="saveConfig" :loading="saving">保存并应用</t-button>
            <t-button size="small" @click="loadConfig" variant="outline">重置</t-button>
          </t-space>
        </div>

        <div class="config-body">
          <t-form label-width="100px" :data="form">
            <t-form-item label="服务器地址">
              <t-input v-model="form.server_addr" placeholder="47.97.22.91 或 47.97.22.91:7000"/>
            </t-form-item>
            <t-form-item label="服务端口">
              <t-input-number v-model="form.server_port" :min="1" :max="65535"
                              theme="column" placeholder="7000" style="width: 160px"/>
            </t-form-item>
            <t-form-item label="验证密钥">
              <t-input v-model="form.token" :type="showToken ? 'text' : 'password'"
                       placeholder="frps 的 auth.token，未开鉴权可留空">
                <template #suffixIcon>
                  <browse-icon v-if="showToken" class="token-toggle" @click="showToken = false"/>
                  <browse-off-icon v-else class="token-toggle" @click="showToken = true"/>
                </template>
              </t-input>
            </t-form-item>
          </t-form>
          <t-divider>端口映射</t-divider>
          <div class="section-header">
            <div class="section-actions">
              <span class="proxy-count" :class="{ 'over-limit': overLimit }">
                共 {{ proxyCount }} 条代理{{ overLimit ? `（超出上限 ${MAX_PROXIES}）` : '' }}
              </span>
              <t-button size="small" variant="outline" @click="addRule">添加规则</t-button>
            </div>
          </div>

          <div class="rule-list">
            <div class="rule-head">
              <span>起始端口</span><span>结束端口</span><span>协议</span><span>备注</span><span></span>
            </div>
            <div v-for="(rule, idx) in form.rules" :key="idx" class="rule-row">
              <t-input-number v-model="rule.start" :min="1" :max="65535" theme="normal"/>
              <t-input-number v-model="rule.end" :min="1" :max="65535" theme="normal"/>
              <t-select v-model="rule.protocol">
                <t-option value="udp" label="UDP"/>
                <t-option value="tcp" label="TCP"/>
                <t-option value="tcp+udp" label="TCP+UDP"/>
              </t-select>
              <t-input v-model="rule.remark" placeholder="备注"/>
              <t-button size="small" theme="danger" variant="text" @click="removeRule(idx)">删除</t-button>
            </div>
            <div v-if="form.rules.length === 0" class="rule-empty">
              还没有端口映射规则。ARK 通常需要游戏端口（UDP）与 RCON 端口（TCP）。
            </div>
          </div>

          <div v-if="proxies.length" class="proxy-status">
            <t-divider>代理状态</t-divider>
            <div class="section-header">
              <span class="proxy-summary">
                {{ healthyCount }} 条正常<template v-if="unhealthy.length">，
                  <span class="bad">{{ unhealthy.length }} 条异常</span>
                </template>
              </span>
            </div>
            <div class="proxy-scroll">
              <div class="proxy-list">
                <div v-for="p in proxies" :key="p.name" class="proxy-row"
                     :class="{ bad: p.phase !== 'running' }">
                  <div class="dot">
                    <TaskChecked1Icon size="20" v-if="p.phase === 'running'" :fill-color='["transparent","transparent"]'
                                      :stroke-color='["currentColor","#0262f8"]' :stroke-width="2"/>
                    <CloseOctagonIcon size="20" v-else :fill-color='["transparent","transparent"]'
                                      :stroke-color='["currentColor","rgba(227, 77, 89, 1)"]' :stroke-width="2"/>
                  </div>
                  <div class="proxy-port">{{ p.type }} {{ p.local_port }}</div>
                  <ArrowRightIcon size="20" :fill-color='["transparent","transparent"]'
                                  :stroke-color='["currentColor","#0262f8"]' :stroke-width="2"/>
                  <div class="proxy-detail">
                    {{ p.phase === 'running' ? (p.remote_addr || '已连接') : (p.err || p.phase) }}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="log-panel">
        <div class="log-panel-header">
          <t-space class="log-controls" align="center" size="small">
            <t-button
                @click="startLogStream"
                theme="primary"
                :disabled="isStreaming"
            >
              {{ isStreaming ? '监听中...' : '开始监听' }}
            </t-button>
            <t-button
                @click="stopLogStream"
                theme="warning"
                :disabled="!isStreaming"
            >
              停止监听
            </t-button>
            <t-button
                @click="clearLogs"
                :disabled="(vllRef?.itemCount ?? 0) === 0"
            >
              清空日志
            </t-button>
            <t-divider layout="vertical"/>
            <t-tag :theme="isStreaming ? 'success' : ''"

            >{{ isStreaming ? '监听中' : '已停止' }}
            </t-tag>
          </t-space>
          <div class="log-count">日志行数: {{ vllRef?.itemCount ?? 0 }}</div>
        </div>

        <div class="log-viewer">
          <VirtualLogList
              ref="vllRef"
              class="log-vll"
              :estimated-item-height="28"
              :buffer="400"
          >
            <template #item="{ item, index }">
              <div class="log-line" :class="`log-level-${item.level}`">
                <span class="log-number">{{ index + 1 }}</span>
                <span class="log-time">{{ item.time }}</span>
                <span class="log-level" :class="`level-${item.level}`">{{ item.level }}</span>
                <span class="log-text">{{ item.msg }}</span>
              </div>
            </template>
            <template #empty>
              <div class="log-empty">
                暂无日志。{{ isStreaming ? '' : '点击"开始监听"按钮开始实时查看系统日志。' }}
              </div>
            </template>
          </VirtualLogList>
        </div>
      </div>
    </div>
  </t-card>
</template>

<script setup>
import {
  streamFRPStatus,
  getFRPConfig,
  updateFRPConfig,
  streamSystemLogs,
  startFRP,
  stopFRP,
  restartFRP
} from '@/apis/api.js'
import dayjs from 'dayjs'
import {ref, reactive, computed, onMounted, onBeforeUnmount, nextTick} from 'vue'
import {
  CheckIcon,
  CloseIcon,
  BrowseIcon,
  BrowseOffIcon,
  TaskChecked1Icon,
  CloseOctagonIcon, ArrowLeftIcon, ArrowRightIcon
} from 'tdesign-icons-vue-next'
import {MessagePlugin, NotifyPlugin} from 'tdesign-vue-next'
import VirtualLogList from '@/components/VirtualLogList.vue'

// 与后端 internal/frpmanage/config.go 的上限保持一致：每个端口都是一条独立的
// frp 代理注册，范围写大了会向 frps 发起海量注册。
const MAX_PROXIES = 128
const MAX_PORTS_PER_RULE = 64

const frpStatus = ref('stopped')
const statusMessage = ref('')
const proxies = ref([])
const saving = ref(false)
const showToken = ref(false)
const isStreaming = ref(false)
const statusStreamStop = ref(null)
const stopStreamFn = ref(null)
const vllRef = ref(null)

const form = reactive({
  server_addr: '',
  server_port: 7000,
  token: '',
  rules: []
})

const proxyCount = computed(() =>
    form.rules.reduce((sum, r) => {
      if (!r.start || !r.end || r.end < r.start) return sum
      return sum + (r.end - r.start + 1) * (r.protocol === 'tcp+udp' ? 2 : 1)
    }, 0)
)
const overLimit = computed(() => proxyCount.value > MAX_PROXIES)
const unhealthy = computed(() => proxies.value.filter(p => p.phase !== 'running'))
const healthyCount = computed(() => proxies.value.length - unhealthy.value.length)

const initializeFRP = async () => {
  await loadConfig()
  startStatusStream()
}

// 上一次看到的状态，用来在「运行中 → 已停止且带原因」时弹一次提示。
// 登录失败（token 错、frps 不可达）是异步发生的，不主动弹的话用户只会看到
// 状态灯自己变红，原因埋在日志里。
let prevRunning = null

const startStatusStream = () => {
  if (statusStreamStop.value) return

  statusStreamStop.value = streamFRPStatus(
      (st) => {
        frpStatus.value = st.running ? 'running' : 'stopped'
        statusMessage.value = st.message || ''
        proxies.value = st.proxies || []

        if (prevRunning && !st.running && st.message) {
          NotifyPlugin.error({title: 'FRP 已停止', content: st.message, duration: 8000})
        }
        prevRunning = st.running
      },
      (error) => {
        console.error('Status stream error:', error)
        setTimeout(() => {
          statusStreamStop.value = null
          startStatusStream()
        }, 5000)
      }
  )
}

const stopStatusStream = () => {
  if (statusStreamStop.value) {
    statusStreamStop.value()
    statusStreamStop.value = null
  }
}

const loadConfig = async () => {
  try {
    const response = await getFRPConfig()
    if (response.success && response.data) {
      const cfg = response.data
      form.server_addr = cfg.server_addr || ''
      form.server_port = cfg.server_port || 7000
      form.token = cfg.token || ''
      form.rules = (cfg.rules || []).map(r => ({...r}))
    }
  } catch (error) {
    console.error('Failed to load FRP config:', error)
  }
}

const addRule = () => {
  form.rules.push({start: 7777, end: 7777, protocol: 'udp', remark: ''})
}

const removeRule = (idx) => {
  form.rules.splice(idx, 1)
}

// 前端先跑一遍与后端同规则的轻校验，让用户在点保存前就看到问题。
// 后端仍会完整校验一次 —— 这里只是快速反馈，不是信任边界。
const localValidate = () => {
  if (!form.server_addr.trim()) return '远程服务器地址不能为空'
  if (form.rules.length === 0) return '至少需要一条端口映射规则'
  for (let i = 0; i < form.rules.length; i++) {
    const r = form.rules[i]
    const n = i + 1
    if (!r.start || !r.end) return `第 ${n} 条规则：端口不能为空`
    if (r.start > r.end) return `第 ${n} 条规则：起始端口不能大于结束端口`
    if (r.end - r.start + 1 > MAX_PORTS_PER_RULE) {
      return `第 ${n} 条规则跨越 ${r.end - r.start + 1} 个端口，单条上限 ${MAX_PORTS_PER_RULE}`
    }
  }
  if (overLimit.value) return `端口映射共展开 ${proxyCount.value} 条代理，上限 ${MAX_PROXIES}`
  return ''
}

const saveConfig = async () => {
  const localErr = localValidate()
  if (localErr) {
    MessagePlugin.error(localErr)
    return
  }

  saving.value = true
  try {
    const response = await updateFRPConfig({
      server_addr: form.server_addr.trim(),
      server_port: form.server_port || 0,
      token: form.token,
      rules: form.rules.map(r => ({
        start: r.start,
        end: r.end,
        protocol: r.protocol,
        remark: r.remark || ''
      }))
    })
    if (response.success) {
      MessagePlugin.success(
          frpStatus.value === 'running' ? '已保存并应用' : '已保存，启动后生效'
      )
      const warnings = response.data?.warnings || []
      warnings.forEach(w => MessagePlugin.warning(w))
    } else {
      MessagePlugin.error(response.error || '保存失败')
    }
  } catch (error) {
    MessagePlugin.error(error?.response?.data?.error || '保存失败')
  } finally {
    saving.value = false
  }
}

const runAction = async (fn, okText) => {
  try {
    const response = await fn()
    if (response.success) {
      MessagePlugin.success(okText)
    } else {
      MessagePlugin.error(response.error || '操作失败')
    }
  } catch (error) {
    MessagePlugin.error(error?.response?.data?.error || '操作失败')
  }
}

// 状态一律由 SSE 推回来，这里不再手工乐观改 frpStatus ——
// 之前那样写会让「启动失败」在面板上先亮一下绿灯再变红。
const startFRPAction = () => runAction(startFRP, 'FRP 已启动')
const stopFRPAction = () => runAction(stopFRP, 'FRP 已停止')
const restartFRPAction = () => runAction(restartFRP, 'FRP 已重启')

// 格式化时间戳为 yyyy-mm-dd HH:mm:ss
const formatTimestamp = (ts) => {
  if (!ts) return ''
  return dayjs(ts).format('YYYY-MM-DD HH:mm:ss')
}

// 解析日志字符串为结构化对象
const parseLogLine = (logStr) => {
  // 日志格式: JSON 字符串
  // 例如: {"ts": "2025-12-13T16:54:27.275+0800", "level": "INFO", "msg": "Server started successfully"}
  try {
    const log = JSON.parse(logStr)
    return {
      time: formatTimestamp(log.ts),
      level: (log.level || 'INFO').toUpperCase(),
      msg: log.msg || logStr
    }
  } catch (e) {
    // 如果解析失败，返回原始字符串
    return {
      time: dayjs().format('YYYY-MM-DD HH:mm:ss'),
      level: 'INFO',
      msg: logStr
    }
  }
}

// 检查日志是否包含 frpc 相关内容
const isFRPCLog = (msg) => {
  return msg.includes('[frpc]')
}

const startLogStream = () => {
  if (isStreaming.value) return
  isStreaming.value = true
  stopStreamFn.value = streamSystemLogs(
      (log) => {
        // 解析日志为结构化格式
        const parsedLog = parseLogLine(log)
        // 只显示包含 [frpc] 的日志
        if (isFRPCLog(parsedLog.msg)) {
          vllRef.value?.push(parsedLog)
        }
      },
      (error) => {
        console.error('日志流错误:', error)
        isStreaming.value = false
      },
      () => {
        console.log('日志流已关闭')
        isStreaming.value = false
      }
  )
}

const stopLogStream = () => {
  if (stopStreamFn.value) {
    stopStreamFn.value()
    stopStreamFn.value = null
  }
  isStreaming.value = false
}

const clearLogs = () => {
  vllRef.value?.clear()
}

onMounted(() => {
  initializeFRP()
  nextTick(() => {
    setTimeout(() => {
      startLogStream()
    }, 500)
  })
})

onBeforeUnmount(() => {
  stopStatusStream()
  if (stopStreamFn.value) {
    stopStreamFn.value()
  }
})
</script>

<style scoped lang="less">
.frp-card {
  height: 100%;
  width: 100%;
  display: flex;
  flex-direction: column;
  border-radius: var(--border-radius-large);
  overflow: hidden;

  :deep(.t-divider) {
    margin: 12px 0;
  }
}

:deep(.t-card__body) {
  flex: 1;
  display: flex;
  flex-direction: column;
  padding: 16px;
  overflow: hidden;
}

:deep(.t-card__title) {
  width: 100%;
}

.frp-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
  gap: 16px;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 8px;
  line-height: 18px;
}

.page-title {
  font-size: 18px;
  font-weight: 500;
}

.status-message {
  font-size: 12px;
  color: #ef4444;
  max-width: 420px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.frp-container {
  display: grid;
  gap: 20px;
  height: 100%;
  grid-template-columns: 1fr 1fr;
}

.config-panel,
.log-panel {
  height: 100%;
  min-height: 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
  background: white;
  border: 1px solid rgb(229, 231, 235);
  border-radius: 4px;

  .log-panel-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 16px;
    border-bottom: 1px solid rgb(229, 231, 235);
    background: rgb(249, 250, 251);
    margin: 0;
    width: 100%;
    box-sizing: border-box;
    flex: 0 0 auto;
  }
}

.log-count {
  font-size: 16px;
}

:deep(.t-badge__text) {
  line-height: 16px !important;
  font-size: 16px;
  color: var(--color-text-2);
}

.panel-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  border-bottom: 1px solid rgb(229, 231, 235);
  background: rgb(249, 250, 251);
  flex: 0 0 auto;
}

.panel-header h3 {
  margin: 0;
  font-size: 14px;
  font-weight: 500;
}

.config-body {
  flex: 1 1 auto;
  min-height: 0;
  padding: 16px;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;

  > div {
    flex: 0 0 auto;
  }

  .t-form {
    flex: 0 0 auto;
  }

  .proxy-status {
    flex: 1 1 auto;
    display: flex;
    flex-direction: column;
    min-height: 0;

    .section-header {
      flex: 0 0 auto;
    }

    .proxy-scroll {
      flex: 1 1 auto;
      min-height: 0;
      padding: 10px;
      box-sizing: border-box;
    }
  }
}

.token-toggle {
  cursor: pointer;
  color: var(--color-text-3, #888);
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: end;
  margin: 8px 0 10px;
}

.section-title {
  font-size: 14px;
  font-weight: 500;
}

.section-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.proxy-count {
  font-size: 12px;
  color: var(--color-text-3, #888);

  &.over-limit {
    color: #ef4444;
    font-weight: 500;
  }
}

.rule-head,
.rule-row {
  display: grid;
  grid-template-columns: 1fr 1fr 1fr 3fr 56px;
  gap: 8px;
  align-items: center;
}

.rule-head {
  font-size: 12px;
  color: var(--color-text-3, #888);
  margin-bottom: 6px;
}

.rule-row {
  margin-bottom: 8px;
}

.rule-empty {
  font-size: 12px;
  color: var(--color-text-3, #888);
  padding: 12px 0;
}

.proxy-summary {
  font-size: 12px;
  color: var(--color-text-3, #888);

  .bad {
    color: #ef4444;
  }
}

.proxy-scroll {
  overflow-y: auto;
  .custom-scrollbar-style();
}


@media (max-width: 1880px) {
  .proxy-list {
    grid-template-columns: repeat(3, 1fr) !important;
  }
}

.proxy-list {
  font-size: 16px;
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 8px;
  grid-auto-rows: 40px;
  align-content: start;
  width: 100%;
}

.proxy-row {
  display: grid;
  grid-template-columns: 30px 2fr 30px 1fr;
  align-items: center;
  box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1);
  border-radius: 8px;
  border: 1px solid #e5e7eb;
  padding: 0 6px;
  box-sizing: border-box;
  align-content: center;

  > div {
    text-align: center;
  }

  .proxy-port {
    color: var(--color-text-1, #333);
  }

  .proxy-detail {
    color: var(--color-text-3, #888);
    word-break: break-all;
  }
}

.log-viewer {
  position: relative;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  background-color: #1a1a1a;
  flex: 1 1 auto;
  min-height: 0;

  :deep(.vll-viewport::-webkit-scrollbar) {
    width: 8px;
  }

  :deep(.vll-viewport::-webkit-scrollbar-track) {
    background: #2a2a2a;
  }

  :deep(.vll-viewport::-webkit-scrollbar-thumb) {
    background: #555;
    border-radius: 4px;

    &:hover {
      background: #777;
    }
  }
}

.log-vll {
  font-family: 'Courier New', monospace;
  font-size: 14px;
  color: var(--color-white);
}

.log-line {
  display: flex;
  padding: 2px 15px;
  margin-bottom: 2px;
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.5;
}

.log-number {
  display: inline-block;
  min-width: 50px;
  margin-right: 10px;
  color: #888;
  user-select: none;
  flex-shrink: 0;
}

.log-time {
  display: inline-block;
  min-width: 170px;
  margin-right: 12px;
  color: #a0aec0;
  font-weight: 400;
  flex-shrink: 0;
  font-family: 'Courier New', monospace;
  font-size: 13px;
}

.log-level {
  display: inline-block;
  min-width: 70px;
  margin-right: 12px;
  font-weight: 600;
  text-align: center;
  border-radius: 3px;
  padding: 0 6px;
  flex-shrink: 0;
  height: 20px;
  line-height: 20px;
  white-space: nowrap;
}

.level-INFO {
  color: #4fc3f7;
  background-color: rgba(79, 195, 247, 0.15);
}

.level-WARN {
  color: #ffb74d;
  background-color: rgba(255, 183, 77, 0.15);
}

.level-ERROR {
  color: #ef5350;
  background-color: rgba(239, 83, 80, 0.15);
}

.level-DEBUG {
  color: #81c784;
  background-color: rgba(129, 199, 132, 0.15);
}

.log-text {
  flex: 1;
  color: #e0e0e0;
  word-break: break-word;
}

.log-empty {
  text-align: center;
  color: #666;
  padding: 40px;
  font-size: 14px;
}

@media (max-width: 1200px) {
  .frp-container {
    flex-direction: column;
    gap: 15px;
  }

  .config-panel,
  .log-panel {
    min-height: 300px;
  }

  .frp-header {
    flex-direction: column;
    align-items: flex-start;
  }
}
</style>
