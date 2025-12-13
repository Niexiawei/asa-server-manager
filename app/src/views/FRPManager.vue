<template>
  <a-card class="frp-card" :bordered="false">
    <template #title>
      <div class="frp-header">
        <div class="header-left">
          <span class="page-title">FRP 管理</span>
          <IconCheck v-if="frpStatus === 'running'"
                     style="color: #22c55e; font-size: 18px;"/>
          <IconClose v-else style="color: #ef4444; font-size: 18px;"/>
        </div>
        <a-space>
          <a-button size="small" type="primary" @click="startFRP" :disabled="frpStatus === 'running'">启动</a-button>
          <a-button size="small" status="danger" @click="stopFRP" :disabled="frpStatus === 'stopped'">停止</a-button>
          <a-button size="small" status="warning" @click="restartFRP">重启</a-button>
        </a-space>
      </div>
    </template>

    <div class="frp-container">
      <div class="config-panel">
        <div class="panel-header">
          <h3>配置文件编辑</h3>
          <a-space>
            <a-button type="primary" size="small" @click="saveFRPConfig" :loading="saving">保存</a-button>
            <a-button size="small" @click="reloadFRPConfig" type="outline">重新加载</a-button>
          </a-space>
        </div>
        <div ref="editorContainer" class="editor-container"></div>
      </div>

      <div class="log-panel">
        <a-space class="log-controls">
          <a-button
              @click="startLogStream"
              type="primary"
              :disabled="isStreaming"
          >
            {{ isStreaming ? '监听中...' : '开始监听' }}
          </a-button>
          <a-button
              @click="stopLogStream"
              status="warning"
              :disabled="!isStreaming"
          >
            停止监听
          </a-button>
          <a-button
              @click="clearLogs"
              :disabled="systemLogs.length === 0"
          >
            清空日志
          </a-button>
          <a-divider direction="vertical"/>
          <a-badge :color="isStreaming ? 'green' : 'gray'"
                   :text="isStreaming ? '监听中' : '已停止'"
          />
          <span class="log-count">日志行数: {{ systemLogs.length }}</span>
        </a-space>

        <div class="log-viewer">
          <div class="log-container" ref="logContainer">
            <div
                v-for="(log, index) in systemLogs"
                :key="index"
                class="log-line"
            >
              <span class="log-number">{{ index + 1 }}</span>
              <span class="log-text">{{ log }}</span>
            </div>
            <div v-if="systemLogs.length === 0" class="log-empty">
              暂无日志。点击"开始监听"按钮开始实时查看系统日志。
            </div>
          </div>
        </div>
      </div>
    </div>
  </a-card>
</template>

<script setup>
import * as api from '@/apis/api.js'
import * as monaco from 'monaco-editor'
import {ref, onMounted, onBeforeUnmount, nextTick, shallowRef} from 'vue'
import {IconCheck, IconClose} from "@arco-design/web-vue/es/icon";

const frpStatus = ref('stopped')
const frpConfig = ref('')
const saving = ref(false)
const systemLogs = ref([])
const isStreaming = ref(false)
const statusCheckInterval = ref(null)
const statusStreamStop = ref(null)
const stopStreamFn = ref(null)
const editor = shallowRef(null)
const editorContainer = ref(null)
const logContainer = ref(null)

const initializeFRP = async () => {
  try {
    await loadFRPConfig()
    initEditor()
    startStatusStream()
  } catch (error) {
    console.error('Failed to initialize FRP manager:', error)
  }
}

const checkFRPStatus = async () => {
  try {
    const response = await api.getFRPStatus()
    if (response.success) {
      frpStatus.value = response.data.status
    }
  } catch (error) {
    console.error('Failed to check FRP status:', error)
  }
}

const startStatusStream = () => {
  if (statusStreamStop.value) return
  
  statusStreamStop.value = api.streamFRPStatus(
    (status) => {
      frpStatus.value = status
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

const loadFRPConfig = async () => {
  try {
    const response = await api.getFRPConfig()
    if (response.success) {
      frpConfig.value = response.data
      if (editor.value) {
        editor.value.setValue(frpConfig.value)
      }
    }
  } catch (error) {
    console.error('Failed to load FRP config:', error)
  }
}

const initEditor = () => {
  if (!editorContainer.value || editor.value) return

  editor.value = monaco.editor.create(editorContainer.value, {
    value: frpConfig.value,
    language: 'toml',
    theme: 'vs-light',
    automaticLayout: true,
    minimap: {enabled: true},
    fontSize: 13,
    lineNumbers: 'on',
    scrollBeyondLastLine: false,
    wordWrap: 'on'
  })
}

const saveFRPConfig = async () => {
  if (!editor.value) return

  saving.value = true
  try {
    const content = editor.value.getValue()
    const response = await api.updateFRPConfig(content)
    if (response.success) {
      frpConfig.value = content
      console.log('FRP config saved successfully')
    }
  } catch (error) {
    console.error('Failed to save FRP config:', error)
  } finally {
    saving.value = false
  }
}

const reloadFRPConfig = async () => {
  await loadFRPConfig()
}

const startFRP = async () => {
  try {
    const response = await api.startFRP()
    if (response.success) {
      frpStatus.value = 'running'
      console.log('FRP started successfully')
    }
  } catch (error) {
    console.error('Failed to start FRP:', error)
  }
}

const stopFRP = async () => {
  try {
    const response = await api.stopFRP()
    if (response.success) {
      frpStatus.value = 'stopped'
      console.log('FRP stopped successfully')
    }
  } catch (error) {
    console.error('Failed to stop FRP:', error)
  }
}

const restartFRP = async () => {
  try {
    const response = await api.restartFRP()
    if (response.success) {
      frpStatus.value = 'running'
      console.log('FRP restarted successfully')
    }
  } catch (error) {
    console.error('Failed to restart FRP:', error)
  }
}

const startLogStream = () => {
  if (isStreaming.value) return
  isStreaming.value = true
  stopStreamFn.value = api.streamSystemLogs(
      (log) => {
        systemLogs.value.push(log)
        nextTick(() => {
          if (logContainer.value) {
            logContainer.value.scrollTop = logContainer.value.scrollHeight
          }
        })
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
  if (editor.value) {
    editor.value.dispose()
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
}

:deep(.arco-card-body) {
  flex: 1;
  display: flex;
  flex-direction: column;
  padding: 16px;
  overflow: hidden;
}

:deep(.arco-card-header-title) {
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

.frp-container {
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

:deep(.arco-badge-status-text) {
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

.log-text {
  flex: 1;
  color: #e0e0e0;
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
