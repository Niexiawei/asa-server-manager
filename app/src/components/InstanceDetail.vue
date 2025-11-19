<template>
  <div class="instance-detail">
    <a-card class="detail-card" :bordered="false">
      <template #title>
        <div class="detail-header">
          <a-button
              type="text"
              size="large"
              @click="$router.back()"
          >
            <template #icon>
              <icon-left />
            </template>
          </a-button>
          <span class="instance-name">{{ instanceName }}</span>
          <a-tag :color="instanceData?.running ? 'green' : 'gray'">
            {{ instanceData?.running ? '运行中' : '已停止' }}
          </a-tag>
          <a-space style="margin-left: 20px">
            <a-button
                @click="startInstance"
                :disabled="instanceData?.running"
                type="primary"
                size="small"
            >
              启动
            </a-button>
            <a-button
                @click="stopInstance"
                :disabled="!instanceData?.running"
                status="warning"
                size="small"
            >
              停止
            </a-button>
          </a-space>
        </div>
      </template>

      <a-spin :loading="loading" style="width: 100%">
        <a-alert v-if="error" type="error" :title="`错误: ${error}`" closable />

        <div v-else class="config-container">
          <a-card title="基本信息" class="config-section">
            <a-descriptions :data="basicInfo" />
          </a-card>

          <a-card title="网络配置" class="config-section">
            <a-descriptions :data="networkConfig" />
          </a-card>

          <a-card title="游戏配置" class="config-section">
            <a-descriptions :data="gameConfig" />
          </a-card>

          <a-card title="高级配置" class="config-section">
            <a-descriptions :data="advancedConfig" />
          </a-card>

          <!-- 实时日志查看 -->
          <a-card title="实时日志" class="config-section">
            <a-space style="margin-bottom: 15px">
              <a-button 
                @click="startLogStream" 
                type="primary"
                :disabled="!instanceData?.running || isStreaming"
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
                :disabled="logs.length === 0"
              >
                清空日志
              </a-button>
              <span>
                <a-badge :color="isStreaming ? 'green' : 'gray'" />
                {{ isStreaming ? '监听中' : '已停止' }}
              </span>
              <span>日志行数: {{ logs.length }}</span>
            </a-space>

            <div class="log-container">
              <div class="log-content">
                <div 
                  v-for="(log, index) in logs" 
                  :key="index"
                  class="log-line"
                >
                  <span class="log-number">{{ index + 1 }}</span>
                  <span class="log-text">{{ log }}</span>
                </div>
                <div v-if="logs.length === 0" class="empty-logs">
                  <a-empty description="暂无日志" />
                </div>
              </div>
              <div ref="logEndRef"></div>
            </div>
          </a-card>
        </div>
      </a-spin>
    </a-card>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import { getInstanceConfig, streamInstanceLogs, startServer, stopServer } from '../apis/api.js'
import { IconLeft } from '@arco-design/web-vue/es/icon'
import { Modal } from '@arco-design/web-vue'

const route = useRoute()
const instanceName = route.params.name
const loading = ref(true)
const error = ref(null)
const instanceData = ref(null)
const logEndRef = ref(null)
const logs = ref([])
const isStreaming = ref(false)
let stopLogStream_func = null

// 基本信息
const basicInfo = ref([])
// 网络配置
const networkConfig = ref([])
// 游戏配置
const gameConfig = ref([])
// 高级配置
const advancedConfig = ref([])

const fetchInstanceConfig = async () => {
  loading.value = true
  error.value = null
  try {
    const data = await getInstanceConfig(instanceName)
    if (data.success && data.data) {
      const instance = data.data
      instanceData.value = instance

      const config = instance.config || {}

      // 基本信息
      basicInfo.value = [
        {
          label: '实例名称',
          value: instanceName
        },
        {
          label: '服务器名称',
          value: config.ServerName || '-'
        },
        {
          label: '最大玩家数',
          value: config.MaxPlayers || '-'
        }
      ]

      // 网络配置
      networkConfig.value = [
        {
          label: '游戏端口',
          value: config.Port || '-'
        },
        {
          label: 'RCON端口',
          value: config.RCONPort || '-'
        },
        {
          label: '查询端口',
          value: config.QueryPort || '-'
        }
      ]

      // 游戏配置
      gameConfig.value = [
        {
          label: '地图名称',
          value: config.MapName || '-'
        },
        {
          label: 'Mod IDs',
          value: config.ModIDs || '-'
        },
        {
          label: '存档目录',
          value: config.SaveDir || '-'
        }
      ]

      // 高级配置
      advancedConfig.value = [
        {
          label: '集群ID',
          value: config.ClusterID || '-'
        },
        {
          label: '自定义启动参数',
          value: config.CustomStartParameters || '-'
        },
        {
          label: '服务器密码',
          value: config.ServerPassword ? '●●●●●●' : '-'
        },
        {
          label: '管理员密码',
          value: config.ServerAdminPassword ? '●●●●●●' : '-'
        }
      ]
    } else {
      error.value = data.error || '获取实例配置失败'
    }
  } catch (err) {
    error.value = err.message || '获取实例配置失败'
  } finally {
    loading.value = false
  }
}

// 开始监听日志
const startLogStream = () => {
  isStreaming.value = true
  logs.value = []

  stopLogStream_func = streamInstanceLogs(
    instanceName,
    // onLog 回调
    (line) => {
      logs.value.push(line)
      // 自动滚动到底部
      nextTick(() => {
        if (logEndRef.value) {
          logEndRef.value.scrollIntoView({ behavior: 'smooth' })
        }
      })
    },
    // onError 回调
    (error) => {
      console.error('日志流错误:', error)
    },
    // onClose 回调
    () => {
      isStreaming.value = false
    }
  )
}

// 停止监听日志
const stopLogStream = () => {
  if (stopLogStream_func) {
    stopLogStream_func()
    stopLogStream_func = null
  }
  isStreaming.value = false
}

// 清空日志
const clearLogs = () => {
  logs.value = []
}

// 启动实例
const startInstance = () => {
  Modal.confirm({
    title: '提示',
    content: `确定要启动实例 "${instanceName}" 吗？`,
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      try {
        const data = await startServer(instanceName)
        if (data.success) {
          // 更新实例运行状态
          if (instanceData.value) {
            instanceData.value.running = true
          }
        } else {
          console.error('启动实例失败:', data.error)
        }
      } catch (error) {
        console.error('启动实例失败:', error)
      }
    }
  })
}

// 停止实例
const stopInstance = () => {
  Modal.confirm({
    title: '提示',
    content: `确定要停止实例 "${instanceName}" 吗？`,
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      try {
        const data = await stopServer(instanceName)
        if (data.success) {
          // 更新实例运行状态
          if (instanceData.value) {
            instanceData.value.running = false
          }
          // 停止日志监听
          if (isStreaming.value) {
            stopLogStream()
          }
        } else {
          console.error('停止实例失败:', data.error)
        }
      } catch (error) {
        console.error('停止实例失败:', error)
      }
    }
  })
}

onMounted(() => {
  fetchInstanceConfig()
})

onUnmounted(() => {
  if (isStreaming.value) {
    stopLogStream()
  }
})
</script>

<style scoped>
.instance-detail {
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow-y: auto;
}

.detail-card {
  background-color: white;
  border-radius: 8px;
}

.detail-header {
  display: flex;
  align-items: center;
  gap: 16px;
}

.instance-name {
  font-size: 18px;
  font-weight: bold;
  color: #333;
  flex: 1;
}

.config-container {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.config-section {
  border-radius: 6px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.08);
}

:deep(.arco-descriptions-row) {
  padding: 12px 0;
}

:deep(.arco-descriptions-item-label) {
  font-weight: 600;
  color: #333;
  min-width: 120px;
}

:deep(.arco-descriptions-item-content) {
  color: #666;
  word-break: break-all;
}

/* 日志样式 */
.log-container {
  border: 1px solid #d9d9d9;
  border-radius: 4px;
  background-color: #fafafa;
  overflow: hidden;
  height: 400px;
  display: flex;
  flex-direction: column;
}

.log-content {
  flex: 1;
  overflow-y: auto;
  padding: 10px;
  font-family: 'Courier New', monospace;
  font-size: 12px;
  background-color: #1f1f1f;
  color: #e0e0e0;
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

.empty-logs {
  display: flex;
  justify-content: center;
  align-items: center;
  height: 100%;
  color: #999;
}

/* 滚动条样式 */
.log-content::-webkit-scrollbar {
  width: 8px;
}

.log-content::-webkit-scrollbar-track {
  background: #2a2a2a;
}

.log-content::-webkit-scrollbar-thumb {
  background: #555;
  border-radius: 4px;
}

.log-content::-webkit-scrollbar-thumb:hover {
  background: #777;
}
</style>
