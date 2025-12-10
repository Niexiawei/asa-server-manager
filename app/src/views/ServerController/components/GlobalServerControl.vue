<template>
  <!-- 服务器控制 -->
  <a-card title="全局服务器控制" :bordered="false">
    <a-space>
      <a-button @click="startAllServersHandler" type="primary">启动所有服务器</a-button>
      <a-button @click="stopAllServersHandler" status="warning">停止所有服务器</a-button>
      <a-button @click="restartAllServersHandler" status="success">重启所有服务器</a-button>
      <a-button @click="updateServerHandler" status="normal">更新服务器</a-button>
    </a-space>
  </a-card>

  <!-- 启动所有服务器日志 -->
  <a-modal
      v-model:visible="startModalVisible"
      :title="startingServers ? '启动中...':'启动所有服务器'"
      :width="800"
      :footer="false"
      @before-close="handleBeforeCloseStart"
  >
    <div class="update-log-container">
      <div class="update-log">
        <div
            v-for="(log, index) in startLogs"
            :key="index"
            class="log-line"
        >
          {{ log }}
        </div>
      </div>
    </div>
  </a-modal>

  <!-- 停止所有服务器日志 -->
  <a-modal
      v-model:visible="stopModalVisible"
      :title="stoppingServers ? '停止中...':'停止所有服务器'"
      :width="800"
      :footer="false"
      @before-close="handleBeforeCloseStop"
  >
    <div class="update-log-container">
      <div class="update-log">
        <div
            v-for="(log, index) in stopLogs"
            :key="index"
            class="log-line"
        >
          {{ log }}
        </div>
      </div>
    </div>
  </a-modal>

  <!-- 重启所有服务器日志 -->
  <a-modal
      v-model:visible="restartModalVisible"
      :title="restartingServers ? '重启中...':'重启所有服务器'"
      :width="800"
      :footer="false"
      @before-close="handleBeforeCloseRestart"
  >
    <div class="update-log-container">
      <div class="update-log">
        <div
            v-for="(log, index) in restartLogs"
            :key="index"
            class="log-line"
        >
          {{ log }}
        </div>
      </div>
    </div>
  </a-modal>

  <!-- 服务器更新日志 -->
  <a-modal
      v-model:visible="updateModalVisible"
      :title="updating ? '服务器更新中...':'服务器更新'"
      :width="800"
      :footer="false"
      @before-close="handleBeforeCloseUpdate"
  >
    <div class="update-log-container">
      <div class="update-log">
        <div
            v-for="(log, index) in updateLogs"
            :key="index"
            class="log-line"
        >
          {{ log }}
        </div>
      </div>
    </div>
  </a-modal>
</template>

<script setup>
import {ref} from 'vue'
import {
  startAllServers,
  stopAllServers,
  restartAllServers,
  updateServer
} from '@/apis/api.js'
import {Message, Modal} from '@arco-design/web-vue'

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
  Modal.confirm({
    title: '确认',
    content: '确定要启动所有服务器吗？',
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      startModalVisible.value = true
      startingServers.value = true
      startLogs.value = []

      await startAllServers(
          (message) => {
            startLogs.value.push(message)
            // 自动滚动到下方
            setTimeout(() => {
              const containers = document.querySelectorAll('.update-log')
              if (containers.length > 0) {
                containers[0].scrollTop = containers[0].scrollHeight
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
            Message.success('所有服务器已启动')
            emits('refresh')
          }
      )
    }
  })
}

// 停止所有服务器
const stopAllServersHandler = async () => {
  Modal.confirm({
    title: '确认',
    content: '确定要停止所有服务器吗？',
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      stopModalVisible.value = true
      stoppingServers.value = true
      stopLogs.value = []

      await stopAllServers(
          (message) => {
            stopLogs.value.push(message)
            // 自动滚动到下方
            setTimeout(() => {
              const containers = document.querySelectorAll('.update-log')
              if (containers.length > 1) {
                containers[1].scrollTop = containers[1].scrollHeight
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
            Message.success('所有服务器已停止')
            emits('refresh')
          }
      )
    }
  })
}

// 重启所有服务器
const restartAllServersHandler = async () => {
  Modal.confirm({
    title: '确认',
    content: '确定要重启所有服务器吗？',
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      restartModalVisible.value = true
      restartingServers.value = true
      restartLogs.value = []

      await restartAllServers(
          (message) => {
            restartLogs.value.push(message)
            // 自动滚动到下方
            setTimeout(() => {
              const containers = document.querySelectorAll('.update-log')
              if (containers.length > 2) {
                containers[2].scrollTop = containers[2].scrollHeight
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
            Message.success('所有服务器已重启')
            emits('refresh')
          }
      )
    }
  })
}

// 更新服务器
const updateServerHandler = async () => {
  // 检查是否有实例正在运行
  const runningInstances = props?.instances?.filter(i => i.running) || []
  if (runningInstances.length > 0) {
    Modal.confirm({
      title: '无法更新',
      content: `程序检测到以下实例正在运行：${runningInstances.map(i => i.name).join('、')}。\n\n请先关闭all所有实例后再试`,
      okText: '关闭',
      cancelText: '取消',
      onOk: async () => {
        // 打开日志面板展示停止过程
        updateModalVisible.value = true
        updating.value = true
        updateLogs.value = []

        await stopAllServers(
            (message) => {
              updateLogs.value.push(message)
              setTimeout(() => {
                const logContainer = document.querySelector('.update-log')
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

  Modal.confirm({
    title: '确认',
    content: '确定要更新服务器吗？这可能需要一些时间。',
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
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
              const logContainer = document.querySelector('.update-log')
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
            Message.success('服务器更新成功')
            emits('refresh')
          }
      )
    }
  })
}

// 取消启动
const handleBeforeCloseStart = async () => {
  return await new Promise(resolve => {
    Modal.confirm({
      title: '是否中止启动？',
      content: '正在启动中，中止可能会导致服务器状态不一致。',
      okText: '是',
      cancelText: '否',
      onOk: () => {
        startingServers.value = false
        startModalVisible.value = false
        resolve(true)
      },
      onCancel: () => {
        resolve(false)
      }
    })
  })
}

// 取消停止
const handleBeforeCloseStop = async () => {
  return await new Promise(resolve => {
    Modal.confirm({
      title: '是否中止停止？',
      content: '正在停止中，中止可能会导致服务器状态不一致。',
      okText: '是',
      cancelText: '否',
      onOk: () => {
        stoppingServers.value = false
        stopModalVisible.value = false
        resolve(true)
      },
      onCancel: () => {
        resolve(false)
      }
    })
  })
}

// 取消重启
const handleBeforeCloseRestart = async () => {
  return await new Promise(resolve => {
    Modal.confirm({
      title: '是否中止重启？',
      content: '正在重启中，中止可能会导致服务器状态不一致。',
      okText: '是',
      cancelText: '否',
      onOk: () => {
        restartingServers.value = false
        restartModalVisible.value = false
        resolve(true)
      },
      onCancel: () => {
        resolve(false)
      }
    })
  })
}

// 取消更新
const handleBeforeCloseUpdate = async () => {
  return await new Promise(resolve => {
    Modal.confirm({
      title: '是否中止更新？',
      content: '正在更新中，中止可能会导致服务器状态不一致。',
      okText: '是',
      cancelText: '否',
      onOk: () => {
        if (updateAbortController) {
          updateAbortController.abort()
        }
        updating.value = false
        updateModalVisible.value = false
        resolve(true)
      },
      onCancel: () => {
        resolve(false)
      }
    })
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
