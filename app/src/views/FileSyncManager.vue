<template>
  <t-card class="fs-card layout-card" :bordered="false">
    <template #title>
      <div class="fs-header">
        <div class="header-left">
          <span class="page-title">集群同步</span>
          <t-tag :theme="stateTheme" variant="light">{{ stateText }}</t-tag>
          <span v-if="status.message" class="status-message" :title="status.message">{{ status.message }}</span>
        </div>
        <t-space size="small">
          <t-button size="small" theme="primary" @click="runAction(startFileSync, '集群同步已启动')"
                    :disabled="running || !status || status.state === 'not_configured'">启动
          </t-button>
          <t-button size="small" theme="danger" @click="runAction(stopFileSync, '集群同步已停止')"
                    :disabled="!running">停止
          </t-button>
          <t-button size="small" theme="warning" @click="runAction(restartFileSync, '集群同步已重启')"
                    :disabled="status.state === 'not_configured'">重启
          </t-button>
        </t-space>
      </div>
    </template>

    <div class="fs-container">
      <!-- 左：配置 -->
      <div class="panel">
        <div class="panel-header">
          <h3>接入配置</h3>
          <t-space>
            <t-button theme="primary" size="small" @click="saveConfig" :loading="saving">保存并应用</t-button>
            <t-button size="small" variant="outline" @click="loadConfig">重置</t-button>
          </t-space>
        </div>

        <div class="panel-body">
          <t-form label-width="110px" :data="form">
            <t-form-item label="启用同步">
              <t-switch v-model="form.enabled"/>
              <span class="hint">关闭后保留配置，本程序重启也不会自动连接</span>
            </t-form-item>

            <t-form-item label="接入方式">
              <t-radio-group v-model="mode" variant="default-filled">
                <t-radio-button value="blob">粘贴接入字符串</t-radio-button>
                <t-radio-button value="files">上传证书文件</t-radio-button>
              </t-radio-group>
            </t-form-item>

            <template v-if="mode === 'blob'">
              <t-form-item label="接入字符串">
                <div class="field-col">
                  <t-textarea v-model="form.joinBlob" :autosize="{ minRows: 3, maxRows: 6 }"
                              :placeholder="config.has_bootstrap ? '已配置，留空则不修改' : '在协调端运行 coordinator join-blob -c <配置文件>，把输出的 filesync-join:v1:... 整行粘贴到这里'"
                              @blur="inspectBlob"/>
                  <div v-if="blobPreview" class="preview ok">
                    将连接 <b>{{ blobPreview.address }}</b>；CA 指纹 <code>{{ shortFingerprint(blobPreview.ca_fingerprint) }}</code>；
                    引导证书 {{ formatDate(blobPreview.cert_not_after) }} 到期
                  </div>
                  <div v-else-if="blobError" class="preview bad">{{ blobError }}</div>
                </div>
              </t-form-item>
              <t-form-item v-if="form.address" label="协调端地址">
                <span class="readonly">{{ form.address }}</span>
                <span class="hint">取自接入字符串</span>
              </t-form-item>
            </template>

            <template v-else>
              <t-form-item label="协调端地址">
                <t-input v-model="form.address" placeholder="sync.example.com:7443"/>
              </t-form-item>
              <t-form-item v-for="f in pemFields" :key="f.key" :label="f.label">
                <div class="file-row">
                  <t-button size="small" variant="outline" @click="pickFile(f.key)">选择 {{ f.file }}</t-button>
                  <span v-if="pem[f.key].name" class="file-name">{{ pem[f.key].name }}</span>
                  <span v-else-if="f.configured()" class="hint">已配置，留空则不修改</span>
                  <span v-else class="hint">协调端 &lt;data_dir&gt;/client-bundle/{{ f.file }}</span>
                </div>
              </t-form-item>
              <input ref="fileInput" type="file" accept=".crt,.key,.pem" class="hidden-file" @change="onFilePicked"/>
            </template>

            <t-form-item label="显示名">
              <t-input v-model="form.label" placeholder="可选，只在协调端界面上用来认出这台机器"/>
            </t-form-item>

            <t-form-item label="同步的集群">
              <t-select v-model="form.clusters" multiple filterable creatable clearable
                        placeholder="从实例配置里的 ClusterID 中选择，也可以直接输入"
                        :options="clusterOptions"/>
            </t-form-item>

            <t-form-item label="上行限速">
              <t-input-number v-model="form.uploadLimit" :min="0" theme="normal" suffix="KB/s" style="width: 180px"/>
              <span class="hint">0 为不限；本机同时在跑游戏服务器</span>
            </t-form-item>
            <t-form-item label="下行限速">
              <t-input-number v-model="form.downloadLimit" :min="0" theme="normal" suffix="KB/s" style="width: 180px"/>
            </t-form-item>
          </t-form>

          <t-divider>本机身份</t-divider>
          <div class="identity">
            <div>
              <span class="k">接入状态</span>
              <t-tag v-if="status.enrolled" theme="success" variant="light">已接入</t-tag>
              <t-tag v-else theme="default" variant="light">未接入</t-tag>
            </div>
            <div v-if="status.node_id"><span class="k">节点 ID</span><code>{{ status.node_id }}</code></div>
            <div v-if="status.cert_not_after">
              <span class="k">{{ status.enrolled ? '节点证书' : '引导证书' }}</span>
              <span :class="{ bad: certDaysLeft !== null && certDaysLeft < 30 }">
                {{ formatDate(status.cert_not_after) }} 到期（剩余 {{ certDaysLeft }} 天）
              </span>
            </div>
            <div class="identity-actions">
              <t-popconfirm theme="warning" @confirm="resetIdentity"
                            content="将删除本机节点证书并用引导凭据重新接入。请先在协调端对本机执行 node reset，否则会被拒绝。继续？">
                <t-button size="small" variant="outline" theme="warning" :disabled="!status.enrolled">重置本机身份</t-button>
              </t-popconfirm>
            </div>
          </div>
        </div>
      </div>

      <!-- 右：状态与日志 -->
      <div class="panel right">
        <div class="panel-header">
          <h3>同步状态</h3>
          <span class="rates">↑ {{ formatRate(status.tx_bytes_per_second) }}　↓ {{ formatRate(status.rx_bytes_per_second) }}</span>
        </div>
        <div class="status-body">
          <div class="conn-line">
            <span v-if="status.remote_addr">已连到 {{ status.remote_addr }}</span>
            <span v-if="status.reconnects">，重连 {{ status.reconnects }} 次</span>
            <span v-if="status.last_connect_error" class="bad">　{{ status.last_connect_error }}</span>
          </div>

          <div v-if="status.clusters.length === 0" class="empty">还没有选择要同步的集群。</div>
          <div v-for="c in status.clusters" :key="c.cluster_id" class="cluster">
            <div class="cluster-head">
              <b>{{ c.cluster_id }}</b>
              <span class="muted">待上报 {{ c.pending_local }} · 传输中 {{ c.inflight_tasks }}
                <template v-if="c.failed_tasks"> · <span class="bad">失败 {{ c.failed_tasks }}</span></template>
              </span>
              <span class="muted right-align">{{ c.last_synced_at ? '最近同步 ' + formatTime(c.last_synced_at) : '尚未同步' }}</span>
            </div>
            <div v-if="c.error" class="bad small">{{ c.error }}</div>
            <div v-for="t in c.transfers" :key="t.direction + t.path" class="transfer">
              <span class="dir">{{ t.direction === 'upload' ? '↑' : '↓' }}</span>
              <span class="path" :title="t.path">{{ t.path }}</span>
              <t-progress class="bar" :percentage="t.size ? Math.floor(t.offset * 100 / t.size) : 0" size="small"/>
              <span class="muted">{{ formatRate(t.bytes_per_second) }}</span>
            </div>
          </div>

          <template v-if="status.recent.length">
            <t-divider>最近告警</t-divider>
            <div class="recent">
              <div v-for="(n, i) in recentReversed" :key="i" class="notice" :class="n.level">
                <span class="muted">{{ formatTime(n.time) }}</span>
                <span v-if="n.cluster_id" class="muted">[{{ n.cluster_id }}]</span>
                <span>{{ n.message }}</span>
                <span v-if="n.conflict_path" class="muted">→ {{ n.conflict_path }}</span>
              </div>
            </div>
          </template>
        </div>

        <div class="log-header">
          <span>同步日志</span>
          <t-space size="small">
            <t-tag :theme="isStreaming ? 'success' : ''">{{ isStreaming ? '监听中' : '已停止' }}</t-tag>
            <t-button size="small" @click="isStreaming ? stopLogStream() : startLogStream()">
              {{ isStreaming ? '停止监听' : '开始监听' }}
            </t-button>
            <t-button size="small" @click="vllRef?.clear()">清空</t-button>
          </t-space>
        </div>
        <div class="log-viewer">
          <VirtualLogList ref="vllRef" class="log-vll" :estimated-item-height="28" :buffer="400">
            <template #item="{ item, index }">
              <div class="log-line">
                <span class="log-number">{{ index + 1 }}</span>
                <span class="log-time">{{ item.time }}</span>
                <span class="log-level" :class="`level-${item.level}`">{{ item.level }}</span>
                <span class="log-text">{{ item.msg }}</span>
              </div>
            </template>
            <template #empty>
              <div class="log-empty">暂无同步日志。</div>
            </template>
          </VirtualLogList>
        </div>
      </div>
    </div>
  </t-card>
</template>

<script setup>
import {
  getFileSyncConfig,
  updateFileSyncConfig,
  inspectJoinBlob,
  getFileSyncClusters,
  startFileSync,
  stopFileSync,
  restartFileSync,
  resetFileSyncIdentity,
  streamFileSyncStatus,
  streamSystemLogs
} from '@/apis/api.js'
import dayjs from 'dayjs'
import {ref, reactive, computed, onMounted, onBeforeUnmount} from 'vue'
import {MessagePlugin, NotifyPlugin} from 'tdesign-vue-next'
import VirtualLogList from '@/components/VirtualLogList.vue'

// 与后端 internal/filesyncmanage 的日志前缀一致
const LOG_PREFIX = '[filesync]'

const STATE_TEXT = {
  not_configured: '未配置',
  disabled: '已关闭',
  stopped: '已停止',
  connecting: '连接中',
  connected: '已连接',
  failed: '已停止（出错）'
}
const STATE_THEME = {
  connected: 'success',
  connecting: 'warning',
  failed: 'danger'
}

const config = ref({})
const status = ref({state: 'not_configured', clusters: [], recent: []})
const clusterOptions = ref([])
const saving = ref(false)
const mode = ref('blob')
const blobPreview = ref(null)
const blobError = ref('')
const fileInput = ref(null)
const pickingFor = ref('')
const vllRef = ref(null)
const isStreaming = ref(false)
let stopStatus = null
let stopLogs = null

const form = reactive({
  enabled: true,
  address: '',
  label: '',
  joinBlob: '',
  clusters: [],
  uploadLimit: 0,
  downloadLimit: 0
})

// 三个文件的读取结果；name 为空表示本次没有选择（保存时不发送 = 不修改）。
const pem = reactive({
  ca: {name: '', text: ''},
  cert: {name: '', text: ''},
  key: {name: '', text: ''}
})
const pemFields = [
  {key: 'ca', label: 'CA 证书', file: 'ca.crt', configured: () => config.value.has_ca},
  {key: 'cert', label: '引导证书', file: 'client.crt', configured: () => config.value.has_bootstrap},
  {key: 'key', label: '引导私钥', file: 'client.key', configured: () => config.value.has_bootstrap}
]

const running = computed(() => ['connecting', 'connected'].includes(status.value.state))
const stateText = computed(() => STATE_TEXT[status.value.state] || status.value.state)
const stateTheme = computed(() => STATE_THEME[status.value.state] || 'default')
const recentReversed = computed(() => [...(status.value.recent || [])].reverse())
const certDaysLeft = computed(() => {
  if (!status.value.cert_not_after) return null
  return Math.max(0, dayjs(status.value.cert_not_after).diff(dayjs(), 'day'))
})

const formatDate = (t) => (t ? dayjs(t).format('YYYY-MM-DD') : '')
const formatTime = (t) => (t ? dayjs(t).format('MM-DD HH:mm:ss') : '')
const shortFingerprint = (fp) => (fp ? fp.split(':').slice(0, 8).join(':') + '…' : '')
const formatRate = (bps) => {
  if (!bps) return '0 B/s'
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s']
  let v = bps, i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`
}
const errorText = (error, fallback) => error?.response?.data?.error || error?.message || fallback

const loadConfig = async () => {
  try {
    const response = await getFileSyncConfig()
    if (!response.success) return
    const cfg = response.data || {}
    config.value = cfg
    form.enabled = cfg.configured ? cfg.enabled : true
    form.address = cfg.address || ''
    form.label = cfg.label || ''
    form.clusters = (cfg.clusters || []).map(c => c.cluster_id)
    form.uploadLimit = cfg.upload_limit_kbps || 0
    form.downloadLimit = cfg.download_limit_kbps || 0
    form.joinBlob = ''
    blobPreview.value = null
    blobError.value = ''
    for (const k of Object.keys(pem)) pem[k] = {name: '', text: ''}
  } catch (error) {
    console.error('Failed to load filesync config:', error)
  }
}

const loadClusters = async () => {
  try {
    const response = await getFileSyncClusters()
    if (response.success) {
      clusterOptions.value = (response.data || []).map(o => ({
        value: o.cluster_id,
        label: `${o.cluster_id}（${o.instances.join('、')}）`
      }))
    }
  } catch (error) {
    console.error('Failed to load cluster ids:', error)
  }
}

const inspectBlob = async () => {
  const blob = form.joinBlob.trim()
  blobPreview.value = null
  blobError.value = ''
  if (!blob) return
  try {
    const response = await inspectJoinBlob(blob)
    if (response.success) {
      blobPreview.value = response.data
      form.address = response.data.address
    } else {
      blobError.value = response.error || '无法解析接入字符串'
    }
  } catch (error) {
    blobError.value = errorText(error, '无法解析接入字符串')
  }
}

const pickFile = (key) => {
  pickingFor.value = key
  fileInput.value.value = ''
  fileInput.value.click()
}

const onFilePicked = async (event) => {
  const file = event.target.files?.[0]
  if (!file || !pickingFor.value) return
  if (file.size > 64 * 1024) {
    MessagePlugin.error('这不像是一个证书文件（超过 64 KB）')
    return
  }
  const text = await file.text()
  if (!text.includes('-----BEGIN ')) {
    MessagePlugin.error(`${file.name} 不是 PEM 格式的证书或私钥`)
    return
  }
  pem[pickingFor.value] = {name: file.name, text}
}

const buildPayload = () => {
  const payload = {
    enabled: form.enabled,
    address: form.address.trim(),
    label: form.label.trim(),
    clusters: form.clusters.map(id => ({cluster_id: String(id).trim()})).filter(c => c.cluster_id),
    upload_limit_kbps: form.uploadLimit || 0,
    download_limit_kbps: form.downloadLimit || 0
  }
  if (mode.value === 'blob') {
    if (form.joinBlob.trim()) payload.join_blob = form.joinBlob.trim()
  } else {
    if (pem.ca.text) payload.ca_pem = pem.ca.text
    if (pem.cert.text) payload.bootstrap_cert_pem = pem.cert.text
    if (pem.key.text) payload.bootstrap_key_pem = pem.key.text
  }
  return payload
}

// 轻校验只为快速反馈，后端会完整校验一次。
const localValidate = (payload) => {
  if (mode.value === 'blob') {
    if (!payload.join_blob && !config.value.has_ca) return '请粘贴接入字符串'
    if (blobError.value) return blobError.value
  } else {
    if (!payload.address) return '协调端地址不能为空'
    if (!payload.ca_pem && !config.value.has_ca) return '请选择 ca.crt'
    if (!!payload.bootstrap_cert_pem !== !!payload.bootstrap_key_pem) return '引导证书与私钥要一起选择'
  }
  return ''
}

const saveConfig = async () => {
  const payload = buildPayload()
  const localErr = localValidate(payload)
  if (localErr) {
    MessagePlugin.error(localErr)
    return
  }
  saving.value = true
  try {
    const response = await updateFileSyncConfig(payload)
    if (response.success) {
      MessagePlugin.success(payload.enabled ? '已保存并应用' : '已保存，同步已关闭')
      await loadConfig()
    } else {
      MessagePlugin.error(response.error || '保存失败')
    }
  } catch (error) {
    MessagePlugin.error(errorText(error, '保存失败'))
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
    MessagePlugin.error(errorText(error, '操作失败'))
  }
}

const resetIdentity = () => runAction(resetFileSyncIdentity, '已删除本机节点证书，正在重新接入')

// 只对新出现的错误级告警弹通知；首帧里的历史告警不弹。
let lastNoticeTime = null
const notifyNewErrors = (recent) => {
  const fresh = lastNoticeTime === null ? [] : recent.filter(n => n.time > lastNoticeTime)
  if (recent.length) lastNoticeTime = recent[recent.length - 1].time
  else if (lastNoticeTime === null) lastNoticeTime = ''
  fresh.filter(n => n.level === 'error').forEach(n =>
      NotifyPlugin.error({title: '集群同步', content: n.message, duration: 8000}))
}

const startStatusStream = () => {
  if (stopStatus) return
  stopStatus = streamFileSyncStatus(
      (st) => {
        st.clusters = st.clusters || []
        st.recent = st.recent || []
        status.value = st
        notifyNewErrors(st.recent)
      },
      () => {
        setTimeout(() => {
          stopStatus = null
          startStatusStream()
        }, 5000)
      }
  )
}

const parseLogLine = (line) => {
  try {
    const log = JSON.parse(line)
    return {
      time: log.ts ? dayjs(log.ts).format('YYYY-MM-DD HH:mm:ss') : '',
      level: (log.level || 'INFO').toUpperCase(),
      msg: log.msg || line
    }
  } catch {
    return {time: dayjs().format('YYYY-MM-DD HH:mm:ss'), level: 'INFO', msg: line}
  }
}

const startLogStream = () => {
  if (isStreaming.value) return
  isStreaming.value = true
  stopLogs = streamSystemLogs(
      (line) => {
        const parsed = parseLogLine(line)
        if (parsed.msg.includes(LOG_PREFIX)) vllRef.value?.push(parsed)
      },
      () => { isStreaming.value = false },
      () => { isStreaming.value = false }
  )
}

const stopLogStream = () => {
  if (stopLogs) {
    stopLogs()
    stopLogs = null
  }
  isStreaming.value = false
}

onMounted(async () => {
  await Promise.all([loadConfig(), loadClusters()])
  startStatusStream()
  startLogStream()
})

onBeforeUnmount(() => {
  if (stopStatus) stopStatus()
  stopLogStream()
})
</script>

<style scoped lang="less">
.fs-card {
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

.fs-header {
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
}

.page-title {
  font-size: 18px;
  font-weight: 500;
}

.status-message {
  font-size: 12px;
  color: #ef4444;
  max-width: 480px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fs-container {
  display: grid;
  gap: 20px;
  height: 100%;
  min-height: 0;
  grid-template-columns: 1fr 1fr;
}

.panel {
  height: 100%;
  min-height: 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
  background: white;
  border: 1px solid rgb(229, 231, 235);
  border-radius: 4px;
}

.panel-header,
.log-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  border-bottom: 1px solid rgb(229, 231, 235);
  background: rgb(249, 250, 251);
  flex: 0 0 auto;

  h3 {
    margin: 0;
    font-size: 14px;
    font-weight: 500;
  }
}

.log-header {
  border-top: 1px solid rgb(229, 231, 235);
}

.panel-body {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  padding: 16px;
}

.hint {
  margin-left: 8px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.readonly {
  font-family: monospace;
}

.field-col {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.preview {
  font-size: 12px;
  line-height: 1.6;

  &.ok {
    color: var(--td-text-color-secondary);
  }

  &.bad {
    color: #ef4444;
  }
}

.file-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.file-name {
  font-family: monospace;
  font-size: 12px;
}

.hidden-file {
  display: none;
}

.identity {
  display: flex;
  flex-direction: column;
  gap: 8px;
  font-size: 13px;

  .k {
    display: inline-block;
    width: 110px;
    color: var(--td-text-color-secondary);
  }
}

.identity-actions {
  margin-top: 4px;
}

.bad {
  color: #ef4444;
}

.muted {
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.small {
  font-size: 12px;
}

.rates {
  font-size: 12px;
  font-family: monospace;
}

.status-body {
  flex: 0 1 auto;
  max-height: 45%;
  overflow-y: auto;
  padding: 12px 16px;
}

.conn-line {
  font-size: 12px;
  margin-bottom: 8px;
  color: var(--td-text-color-secondary);
}

.empty {
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.cluster {
  padding: 8px 0;
  border-bottom: 1px dashed rgb(229, 231, 235);
}

.cluster-head {
  display: flex;
  gap: 12px;
  align-items: baseline;

  .right-align {
    margin-left: auto;
  }
}

.transfer {
  display: grid;
  grid-template-columns: 16px minmax(0, 1fr) 120px 80px;
  gap: 8px;
  align-items: center;
  font-size: 12px;
  margin-top: 4px;

  .path {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: monospace;
  }
}

.recent .notice {
  font-size: 12px;
  display: flex;
  gap: 6px;
  padding: 2px 0;

  &.error {
    color: #ef4444;
  }

  &.warning {
    color: #d97706;
  }
}

.log-viewer {
  flex: 1 1 auto;
  min-height: 160px;
  background-color: #1a1a1a;
}

.log-vll {
  font-family: 'Courier New', monospace;
  font-size: 13px;
}

.log-line {
  display: flex;
  padding: 2px 12px;
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.5;
}

.log-number {
  min-width: 40px;
  margin-right: 8px;
  color: #888;
  user-select: none;
  flex-shrink: 0;
}

.log-time {
  min-width: 160px;
  margin-right: 10px;
  color: #a0aec0;
  flex-shrink: 0;
}

.log-level {
  min-width: 56px;
  margin-right: 10px;
  font-weight: 600;
  text-align: center;
  border-radius: 3px;
  flex-shrink: 0;
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
}

.log-empty {
  text-align: center;
  color: #666;
  padding: 32px;
  font-size: 13px;
}

@media (max-width: 1200px) {
  .fs-container {
    grid-template-columns: 1fr;
  }

  .panel {
    min-height: 360px;
  }
}
</style>
