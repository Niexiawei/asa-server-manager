<template>
  <!-- 服务器控制 -->
  <t-card title="全局服务器控制" :bordered="false">
    <t-space>
      <t-button @click="startAllServersHandler" theme="primary">启动所有服务器</t-button>
      <t-button @click="stopAllServersHandler" theme="warning">停止所有服务器</t-button>
      <t-button @click="restartAllServersHandler" theme="success">重启所有服务器</t-button>
      <t-button @click="updateServerHandler" theme="default">更新服务器</t-button>
    </t-space>
  </t-card>

  <!-- 启动所有服务器日志 -->
  <t-dialog
      v-model:visible="startModalVisible"
      :header="startingServers ? '启动中...':'启动所有服务器'"
      :width="800"
      :footer="false"
      :close-on-overlay-click="!startingServers"
      :close-btn="!startingServers"
  >
    <div class="update-log-container">
      <div id="startLogContainer" class="update-log">
        <div
            v-for="(log, index) in startLogs"
            :key="index"
            class="log-line"
        >
          {{ log }}
        </div>
      </div>
    </div>
  </t-dialog>

  <!-- 停止所有服务器日志 -->
  <t-dialog
      v-model:visible="stopModalVisible"
      :header="stoppingServers ? '停止中...':'停止所有服务器'"
      :width="800"
      :footer="false"
      :close-on-overlay-click="!stoppingServers"
      :close-btn="!stoppingServers"
  >
    <div class="update-log-container">
      <div id="stopLogContainer" class="update-log">
        <div
            v-for="(log, index) in stopLogs"
            :key="index"
            class="log-line"
        >
          {{ log }}
        </div>
      </div>
    </div>
  </t-dialog>

  <!-- 重启所有服务器日志 -->
  <t-dialog
      v-model:visible="restartModalVisible"
      :header="restartingServers ? '重启中...':'重启所有服务器'"
      :width="800"
      :footer="false"
      :close-on-overlay-click="!restartingServers"
      :close-btn="!restartingServers"
  >
    <div class="update-log-container">
      <div id="restartLogContainer" class="update-log">
        <div
            v-for="(log, index) in restartLogs"
            :key="index"
            class="log-line"
        >
          {{ log }}
        </div>
      </div>
    </div>
  </t-dialog>

  <!-- 服务器更新日志 -->
  <t-dialog
      v-model:visible="updateModalVisible"
      :header="updating ? '服务器更新中...':'服务器更新'"
      :width="800"
      :footer="false"
      :close-on-overlay-click="!updating"
      :close-btn="!updating"
  >
    <div class="update-log-container">
      <div id="updateLogContainer" class="update-log">
        <div
            v-for="(log, index) in updateLogs"
            :key="index"
            class="log-line"
        >
          {{ log }}
        </div>
      </div>
    </div>
  </t-dialog>
</template>

<script setup>
import {ref} from 'vue'
import {
  startAllServers,
  stopAllServers,
  restartAllServers,
  updateServer,
  listInstances
} from '@/apis/api.js'
import {MessagePlugin, DialogPlugin} from 'tdesign-vue-next'

const props = defineProps({
  instances: {
    type: Array,
    default: () => []
  }
})

const emits = defineEmits(['refresh'])

// 状态管理 - 启动
const startModalVisible = ref(false)
const startingServers = ref(false)
const startLogs = ref([])

// 状态管理 - 停止
const stopModalVisible = ref(false)
const stoppingServers = ref(false)
const stopLogs = ref([])

// 状态管理 - 重启
const restartModalVisible = ref(false)
const restartingServers = ref(false)
const restartLogs = ref([])

// 状态管理 - 更新
const updateModalVisible = ref(false)
const updating = ref(false)
const updateLogs = ref([])
let updateAbortController = null
const updateModalName = ref("")

// 启动所有服务器
const startAllServersHandler = async () => {
  DialogPlugin.confirm({
    header: '确认',
    body: '确定要启动所有服务器吗？',
    confirmBtn: '确定',
    cancelBtn: '取消',
    onConfirm: async () => {
      startModalVisible.value = true
      startingServers.value = true
      startLogs.value = []

      await startAllServers(
          (message) => {
            startLogs.value.push(message)
            // 自动滚动到下方
            setTimeout(() => {
              const container = document.getElementById('startLogContainer')
              if (container) {
                container.scrollTop = container.scrollHeight
              }
            }, 0)
          },
          (error) => {
            console.error('启动服务器错误:', error)
            startLogs.value.push(`错误: ${error.message}`)
            startingServers.value = false
          },
          () => {
            startingServers.value = false
            startLogs.value.push('\n业务处理完成')
            MessagePlugin.success('所有服务器已启动')
            emits('refresh')
          }
      )
    }
  })
}

// 停止所有服务器
const stopAllServersHandler = async () => {
  DialogPlugin.confirm({
    header: '确认',
    body: '确定要停止所有服务器吗？',
    confirmBtn: '确定',
    cancelBtn: '取消',
    onConfirm: async () => {
      stopModalVisible.value = true
      stoppingServers.value = true
      stopLogs.value = []

      await stopAllServers(
          (message) => {
            stopLogs.value.push(message)
            // 自动滚动到下方
            setTimeout(() => {
              const container = document.getElementById('stopLogContainer')
              if (container) {
                container.scrollTop = container.scrollHeight
              }
            }, 0)
          },
          (error) => {
            console.error('停止服务器错误:', error)
            stopLogs.value.push(`错误: ${error.message}`)
            stoppingServers.value = false
          },
          () => {
            stoppingServers.value = false
            stopLogs.value.push('\n业务处理完成')
            MessagePlugin.success('所有服务器已停止')
            emits('refresh')
          }
      )
    }
  })
}

// 重启所有服务器
const restartAllServersHandler = async () => {
  DialogPlugin.confirm({
    header: '确认',
    body: '确定要重启所有服务器吗？',
    confirmBtn: '确定',
    cancelBtn: '取消',
    onConfirm: async () => {
      restartModalVisible.value = true
      restartingServers.value = true
      restartLogs.value = []

      await restartAllServers(
          (message) => {
            restartLogs.value.push(message)
            // 自动滚动到下方
            setTimeout(() => {
              const container = document.getElementById('restartLogContainer')
              if (container) {
                container.scrollTop = container.scrollHeight
              }
            }, 0)
          },
          (error) => {
            console.error('重启服务器错误:', error)
            restartLogs.value.push(`错误: ${error.message}`)
            restartingServers.value = false
          },
          () => {
            restartingServers.value = false
            restartLogs.value.push('\n业务处理完成')
            MessagePlugin.success('所有服务器已重启')
            emits('refresh')
          }
      )
    }
  })
}

// 更新服务器
const updateServerHandler = async () => {
  try {
    // 通过接口获取最新的服务器运行状态
    const {data: {instances}} = await listInstances()
    console.log(instances)
    const runningInstances = instances?.filter(i => i.running) || []

    if (runningInstances.length > 0) {
      DialogPlugin.confirm({
        header: '无法更新',
        body: `程序检测到以下实例正在运行：${runningInstances.map(i => i.name).join('、')}。\n\n请先关闭所有实例后再试`,
        confirmBtn: '关闭',
        cancelBtn: '取消',
        onConfirm: async () => {
          // 打开日志面板展示停止过程
          updateModalVisible.value = true
          updating.value = true
          updateLogs.value = []

          await stopAllServers(
              (message) => {
                updateLogs.value.push(message)
                setTimeout(() => {
                  const logContainer = document.getElementById('updateLogContainer')
                  if (logContainer) {
                    logContainer.scrollTop = logContainer.scrollHeight
                  }
                }, 0)
              },
              (error) => {
                console.error('停止服务器错误:', error)
                updateLogs.value.push(`错误: ${error.message}`)
                updating.value = false
              },
              () => {
                updating.value = false
                updateLogs.value.push('\n所有服务器已停止，现在可以更新')
                updateModalVisible.value = false
              }
          )
        }
      })
      return
    }
  } catch (error) {
    console.error('获取服务器状态失败:', error)
    MessagePlugin.error('获取服务器状态失败，请重试')
    return
  }

  DialogPlugin.confirm({
    header: '确认',
    body: '确定要更新服务器吗？这可能需要一些时间。',
    confirmBtn: '确定',
    cancelBtn: '取消',
    onConfirm: async () => {
      updateModalVisible.value = true
      updating.value = true
      updateLogs.value = []
      updateAbortController = new AbortController()

      await updateServer(
          (message) => {
            // onMessage callback
            updateLogs.value.push(message)
            // 自动滚动到下方
            setTimeout(() => {
              const logContainer = document.getElementById('updateLogContainer')
              if (logContainer) {
                logContainer.scrollTop = logContainer.scrollHeight
              }
            }, 0)
          },
          (error) => {
            // onError callback
            console.error('更新日志错误:', error)
            updateLogs.value.push(`错误: ${error.message}`)
            updating.value = false
          },
          () => {
            // onComplete callback
            updating.value = false
            updateLogs.value.push('\n更新完成')
            MessagePlugin.success('服务器更新成功')
            emits('refresh')
          }
      )
    }
  })
}


</script>

<style scoped lang="less">
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
  background-color: #fafafa;
  overflow-y: auto;
  font-family: 'Monaco', 'Courier New', monospace;
  font-size: 12px;
  line-height: 1.6;
}

.log-line {
  color: #333;
  word-break: break-all;
  white-space: pre-wrap;
}
</style>
