<template>
  <t-card class="syncthing-card layout-card">
    <template #title>
      <div class="syncthing-header">
        <div class="header-left">
          <span class="page-title">Syncthing 管理</span>
          <check-icon v-if="syncthingStatus === 'running'"
                      style="color: #22c55e; font-size: 18px;"/>
          <close-icon v-else style="color: #ef4444; font-size: 18px;"/>
        </div>
        <t-space>
          <t-button size="small" theme="primary" @click="startSyncthing" :disabled="syncthingStatus === 'running'">
            启动
          </t-button>
          <t-button size="small" theme="danger" @click="stopSyncthing" :disabled="syncthingStatus === 'stopped'">停止
          </t-button>
          <t-button size="small" theme="warning" @click="restartSyncthing">重启</t-button>
        </t-space>
      </div>
    </template>

    <div class="syncthing-container">
      <div class="config-panel">
        <div class="panel-header">
          <h3>配置文件编辑</h3>
          <t-space>
            <t-button theme="primary" size="small" @click="saveSyncthingConfig" :loading="saving">保存</t-button>
            <t-button size="small" @click="reloadSyncthingConfig" variant="outline">重新加载</t-button>
          </t-space>
        </div>
        <div ref="editorContainer" class="editor-container"></div>
      </div>

      <div class="log-panel">
        <t-space class="log-controls">
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
              :disabled="systemLogs.length === 0"
          >
            清空日志
          </t-button>
          <t-divider layout="vertical"/>
          <t-badge :color="isStreaming ? 'green' : 'gray'"
                   :count="isStreaming ? '监听中' : '已停止'"
          />
          <span class="log-count">日志行数: {{ systemLogs.length }}</span>
        </t-space>

        <div class="log-viewer">
          <div class="log-container" ref="logContainer">
            <div
                v-for="(log, index) in systemLogs"
                :key="index"
                class="log-line"
                :class="`log-level-${log.level}`"
            >
              <span class="log-number">{{ index + 1 }}</span>
              <span class="log-time">{{ log.time }}</span>
              <span class="log-level" :class="`level-${log.level}`">{{ log.level }}</span>
              <span class="log-text">{{ log.msg }}</span>
            </div>
            <div v-if="systemLogs.length === 0" class="log-empty">
              暂无日志。{{ isStreaming ? "" : '点击"开始监听"按钮开始实时查看系统日志。' }}
            </div>
          </div>
        </div>
      </div>
    </div>
  </t-card>
</template>

<script setup>
import * as api from '@/apis/api.js'
import * as monaco from 'monaco-editor'
import dayjs from 'dayjs'
import {ref, onMounted, onBeforeUnmount, nextTick, shallowRef} from 'vue'
import {CheckIcon, CloseIcon} from 'tdesign-icons-vue-next';

const syncthingStatus = ref('stopped')
const syncthingConfig = ref('')
const saving = ref(false)
const systemLogs = ref([])
const isStreaming = ref(false)
const statusCheckInterval = ref(null)
const statusStreamStop = ref(null)
const stopStreamFn = ref(null)
const editor = shallowRef(null)
const editorContainer = ref(null)
const logContainer = ref(null)

const initializeSyncthing = async () => {
  try {
    await loadSyncthingConfig()
    initEditor()
    startStatusStream()
  } catch (error) {
    console.error('Failed to initialize Syncthing manager:', error)
  }
}

const checkSyncthingStatus = async () => {
  try {
    const response = await api.getSyncthingStatus()
    if (response.success) {
      syncthingStatus.value = response.data.status
    }
  } catch (error) {
    console.error('Failed to check Syncthing status:', error)
  }
}

const startStatusStream = () => {
  if (statusStreamStop.value) return

  statusStreamStop.value = api.streamSyncthingStatus(
      (status) => {
        syncthingStatus.value = status
      },
      (error) => {
        console.error('Status stream error:', error)
        // 错误发生时，对5秒后重新连接
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

const loadSyncthingConfig = async () => {
  try {
    const response = await api.getSyncthingConfig()
    if (response.success) {
      syncthingConfig.value = response.data
      if (editor.value) {
        editor.value.setValue(syncthingConfig.value)
      }
    }
  } catch (error) {
    console.error('Failed to load Syncthing config:', error)
  }
}

const initEditor = () => {
  if (!editorContainer.value || editor.value) return

  editor.value = monaco.editor.create(editorContainer.value, {
    value: syncthingConfig.value,
    language: 'xml',
    theme: 'vs-light',
    automaticLayout: true,
    minimap: {enabled: true},
    fontSize: 13,
    lineNumbers: 'on',
    scrollBeyondLastLine: false,
    wordWrap: 'on'
  })
}

const saveSyncthingConfig = async () => {
  if (!editor.value) return

  saving.value = true
  try {
    const content = editor.value.getValue()
    const response = await api.updateSyncthingConfig(content)
    if (response.success) {
      syncthingConfig.value = content
      console.log('Syncthing config saved successfully')
    }
  } catch (error) {
    console.error('Failed to save Syncthing config:', error)
  } finally {
    saving.value = false
  }
}

const reloadSyncthingConfig = async () => {
  await loadSyncthingConfig()
}

const startSyncthing = async () => {
  try {
    const response = await api.startSyncthing()
    if (response.success) {
      syncthingStatus.value = 'running'
      console.log('Syncthing started successfully')
    }
  } catch (error) {
    console.error('Failed to start Syncthing:', error)
  }
}

const stopSyncthing = async () => {
  try {
    const response = await api.stopSyncthing()
    if (response.success) {
      syncthingStatus.value = 'stopped'
      console.log('Syncthing stopped successfully')
    }
  } catch (error) {
    console.error('Failed to stop Syncthing:', error)
  }
}

const restartSyncthing = async () => {
  try {
    const response = await api.restartSyncthing()
    if (response.success) {
      syncthingStatus.value = 'running'
      console.log('Syncthing restarted successfully')
    }
  } catch (error) {
    console.error('Failed to restart Syncthing:', error)
  }
}

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

// 检查日志是否包含 syncthing 相关内容
const isSyncthingLog = (msg) => {
  return msg.includes('[syncthing]')
}

const startLogStream = () => {
  if (isStreaming.value) return
  isStreaming.value = true
  stopStreamFn.value = api.streamSystemLogs(
      (log) => {
        // 解析日志为结构化格式
        const parsedLog = parseLogLine(log)
        // 只显示包含 [syncthing] 的日志
        if (isSyncthingLog(parsedLog.msg)) {
          systemLogs.value.push(parsedLog)
          nextTick(() => {
            if (logContainer.value) {
              logContainer.value.scrollTop = logContainer.value.scrollHeight
            }
          })
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
  systemLogs.value = []
}

onMounted(() => {
  initializeSyncthing()
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
  if (editor.value) {
    editor.value.dispose()
  }
})
</script>

<style scoped lang="less">
.syncthing-card {
  height: 100%;
  width: 100%;
  display: flex;
  flex-direction: column;
  border-radius: var(--border-radius-large);
  overflow: hidden;
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

.syncthing-header {
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

.syncthing-container {
  display: flex;
  flex: 1;
  gap: 20px;
  overflow: hidden;
}

.config-panel,
.log-panel {
  flex: 1;
  display: flex;
  flex-direction: column;
  background: white;
  border: 1px solid rgb(229, 231, 235);
  border-radius: 4px;
  overflow: hidden;
}

.log-controls {
  padding: 12px 16px;
  border-bottom: 1px solid rgb(229, 231, 235);
  background: rgb(249, 250, 251);
  margin: 0;
  width: 100%;
  box-sizing: border-box;
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
}

.panel-header h3 {
  margin: 0;
  font-size: 14px;
  font-weight: 500;
}

.editor-container {
  flex: 1;
  width: 100%;
  overflow: hidden;
}

.log-viewer {
  position: relative;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  background-color: #1a1a1a;
  height: calc(100% - 62px);
  flex: 1;
  overflow: hidden;
}

.log-container {
  height: 100%;
  overflow-y: auto;
  padding: 15px;
  box-sizing: border-box;
  font-family: 'Courier New', monospace;
  font-size: 14px;
  color: var(--color-white);
  white-space: pre-wrap;
  word-wrap: break-word;
  line-height: 1.5;
}

.log-line {
  display: flex;
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

/* 滚动条样式 */
.log-container::-webkit-scrollbar {
  width: 8px;
}

.log-container::-webkit-scrollbar-track {
  background: #2a2a2a;
}

.log-container::-webkit-scrollbar-thumb {
  background: #555;
  border-radius: 4px;
}

.log-container::-webkit-scrollbar-thumb:hover {
  background: #777;
}

@media (max-width: 1200px) {
  .syncthing-container {
    flex-direction: column;
    gap: 15px;
  }

  .config-panel,
  .log-panel {
    min-height: 300px;
  }

  .syncthing-header {
    flex-direction: column;
    align-items: flex-start;
  }
}
</style>
