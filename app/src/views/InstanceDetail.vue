<template>
  <div class="instance-detail">
    <!-- WebSocket 连接状态指示器 -->
    <WSStatusIndicator/>

    <a-card class="detail-card" :bordered="false">
      <template #title>
        <div class="detail-header">
          <a-button
              type="text"
              size="large"
              @click="$router.back()"
          >
            <template #icon>
              <icon-left/>
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
            <a-button
                @click="restartInstance"
                :disabled="!instanceData?.running"
                status="success"
                size="small"
            >
              重启
            </a-button>
          </a-space>
        </div>
      </template>

      <a-spin :loading="loading" style="width: 100%">
        <a-alert v-if="error" type="error" :title="`错误: ${error}`" closable/>

        <div v-else class="config-container">
          <a-card title="服务器配置" class="config-section">
            <template #title>
              <div class="config-card-title">
                <span>服务器配置</span>
                <a-button
                    type="primary"
                    size="small"
                    @click="openConfigEditModal"
                    style="margin-left: 12px"
                    :disabled="instanceData?.running"
                >
                  编辑
                </a-button>
              </div>
            </template>
            <div class="config-grid">
              <div v-for="item in getAllConfigItems()" :key="item.label" class="config-grid-item"
                   :class="{ 'full-width': item.label === '自定义启动参数' }">
                <div class="config-item">
                  <div class="config-item-label">{{ item.label }}</div>
                  <div class="config-item-content">
                    <div v-if="!item.type || item.type === 'text'" class="config-item-value">
                      {{ item.value }}
                    </div>
                    <div v-else-if="item.type === 'boolean'" class="config-item-value">
                      <a-tag :color="item.value === '是' ? 'green' : 'gray'">{{ item.value }}</a-tag>
                    </div>
                    <div v-else-if="item.type === 'password'" class="password-wrapper">
                      <span class="config-item-value">
                        {{
                          item.label === '服务器密码' && showServerPassword ? item.value : (item.label === '管理员密码' && showAdminPassword ? item.value : (item.hasPassword ? '●●●●●●' : item.value))
                        }}
                      </span>
                      <a-button
                          v-if="item.hasPassword"
                          type="text"
                          size="small"
                          :icon="item.label === '服务器密码' ? (showServerPassword ? 'icon-eye-invisible' : 'icon-eye') : (showAdminPassword ? 'icon-eye-invisible' : 'icon-eye')"
                          @click="item.label === '服务器密码' ? (showServerPassword = !showServerPassword) : (showAdminPassword = !showAdminPassword)"
                      >
                        <template #icon>
                          <component
                              :is="item.label === '服务器密码' ? (showServerPassword ? IconEyeInvisible : IconEye) : (showAdminPassword ? IconEyeInvisible : IconEye)"/>
                        </template>
                      </a-button>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </a-card>

          <!-- 配置文件区域 -->
          <div class="config-files-row">
            <!-- Game.ini 配置 -->
            <a-card title="Game.ini 配置" class="config-section config-file-card">
              <a-space style="margin-bottom: 15px">
                <a-button
                    @click="loadGameIni"
                    type="primary"
                    :loading="loadingGameIni"
                >
                  加载文件
                </a-button>
                <a-button
                    @click="triggerGameIniUpload"
                    status="success"
                >
                  上传文件
                </a-button>
                <a-button
                    @click="gameIniEditModalVisible = true"
                    type="primary"
                >
                  编辑文件
                </a-button>
              </a-space>

              <div class="config-viewer-wrapper">
                <config-file-viewer
                    :content="gameIniContent"
                    language="ini"
                />
              </div>
            </a-card>

            <!-- GameUserSettings.ini 配置 -->
            <a-card title="GameUserSettings.ini 配置" class="config-section config-file-card">
              <a-space style="margin-bottom: 15px">
                <a-button
                    @click="loadGameUserSettings"
                    type="primary"
                    :loading="loadingGameUserSettings"
                >
                  加载文件
                </a-button>
                <a-button
                    @click="triggerGameUserSettingsUpload"
                    status="success"
                >
                  上传文件
                </a-button>
                <a-button
                    @click="gameUserSettingsEditModalVisible = true"
                    type="primary"
                >
                  编辑文件
                </a-button>
              </a-space>

              <div class="config-viewer-wrapper">
                <config-file-viewer
                    :content="gameUserSettingsContent"
                    language="ini"
                />
              </div>
            </a-card>
          </div>

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
                <a-badge :color="isStreaming ? 'green' : 'gray'"/>
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
                  <a-empty description="暂无日志"/>
                </div>
              </div>
              <div ref="logEndRef"></div>
            </div>
          </a-card>
        </div>
      </a-spin>
    </a-card>

    <!-- 配置编辑弹出框 -->
    <config-edit-modal
        :visible="configEditModalVisible"
        :config="instanceData?.config || {}"
        :saving="savingConfig"
        @update:visible="configEditModalVisible = $event"
        @save="saveConfig"
    />

    <!-- Game.ini 编辑模态框 -->
    <config-editor
        :visible="gameIniEditModalVisible"
        title="编辑 Game.ini 配置"
        :content="gameIniContent"
        language="ini"
        :saving="savingGameIni"
        @update:visible="gameIniEditModalVisible = $event"
        @save="saveGameIni"
        @cancel="gameIniEditModalVisible = false"
    />

    <!-- GameUserSettings.ini 编辑模态框 -->
    <config-editor
        :visible="gameUserSettingsEditModalVisible"
        title="编辑 GameUserSettings.ini 配置"
        :content="gameUserSettingsContent"
        language="ini"
        :saving="savingGameUserSettings"
        @update:visible="gameUserSettingsEditModalVisible = $event"
        @save="saveGameUserSettings"
        @cancel="gameUserSettingsEditModalVisible = false"
    />

    <!-- 隐藏的文件输入框 -->
    <input
        ref="gameIniFileInput"
        type="file"
        accept=".ini"
        style="display: none"
        @change="handleGameIniFileSelected"
    />
    <input
        ref="gameUserSettingsFileInput"
        type="file"
        accept=".ini"
        style="display: none"
        @change="handleGameUserSettingsFileSelected"
    />
  </div>
</template>

<script setup>
import {ref, onMounted, onUnmounted, nextTick, watch, computed} from 'vue'
import {useRoute} from 'vue-router'
import ConfigEditor from '@/components/ConfigEditor.vue'
import ConfigFileViewer from '@/components/ConfigFileViewer.vue'
import ConfigEditModal from '@/components/ConfigEditModal.vue'
import WSStatusIndicator from '@/components/WSStatusIndicator.vue'
import {
  getInstanceConfig,
  streamInstanceLogs,
  getRecentInstanceLogs,
  startServer,
  stopServer,
  restartServer,
  restartServerSSE,
  getGameIni,
  getGameUserSettings,
  updateGameIni,
  updateGameUserSettings,
  uploadGameIniFile,
  uploadGameUserSettingsFile,
  updateInstanceConfig
} from '@/apis/api.js'
import {serverStore, getInstanceStatus} from '@/store/serverStore.js'
import {onServerEvent} from '@/apis/api.js'
import {IconLeft, IconEyeInvisible, IconEye} from '@arco-design/web-vue/es/icon'
import {Modal, Message} from '@arco-design/web-vue'

// Monaco Editor 引用 - 已移至 ConfigEditor 组件
const loading = ref(true)
const error = ref(null)
const instanceData = ref(null)
const logEndRef = ref(null)
const logs = ref([])
const isStreaming = ref(false)
const loadingRecentLogs = ref(false)
let stopLogStream_func = null

const route = useRoute()
const instanceName = route.params.name

// 基本信息
const basicInfo = ref([])
// 网络配置
const networkConfig = ref([])
// 游戏配置
const gameConfig = ref([])
// 高级配置
const advancedConfig = ref([])
// 密码显示状态
const showServerPassword = ref(false)
const showAdminPassword = ref(false)

// Game.ini 相关
const gameIniContent = ref('')
const gameIniEditModalVisible = ref(false)
const gameIniEdited = ref(false)
const loadingGameIni = ref(false)
const savingGameIni = ref(false)
const uploadingGameIni = ref(false)

// GameUserSettings.ini 相关
const gameUserSettingsContent = ref('')
const gameUserSettingsEditModalVisible = ref(false)
const gameUserSettingsEdited = ref(false)
const loadingGameUserSettings = ref(false)
const savingGameUserSettings = ref(false)
const uploadingGameUserSettings = ref(false)

// 文件输入框引用
const gameIniFileInput = ref(null)
const gameUserSettingsFileInput = ref(null)

// 配置编辑弹出框相关
const configEditModalVisible = ref(false)
const savingConfig = ref(false)

// 监听 WebSocket 事件，实时更新实例运行状态
watch(
    () => getInstanceStatus(instanceName),
    (newStatus) => {
      if (newStatus) {
        // 实时更新实例运行状态
        if (instanceData.value) {
          instanceData.value.running = newStatus.running
        }

        // 如果实例启动，自动开始日志监听
        if (newStatus.running && !isStreaming.value) {
          // 延迟100ms以确保服务器完全启动
          setTimeout(() => {
            if (!isStreaming.value && instanceData.value?.running) {
              startLogStream()
            }
          }, 100)
        }

        // 如果实例被停止，自动停止日志监听
        if (!newStatus.running && isStreaming.value) {
          stopLogStream()
        }
      }
    }
)

// 监听 WebSocket server_started 事件，自动开始日志监听
let unlistenServerStarted = null
let unlistenServerStarting = null
let unlistenServerStopped = null

// 计算实例是否在启动或停止中
const instanceChanging = computed(() => {
  const status = getInstanceStatus(instanceName)
  return status && (status.status === 'starting' || status.status === 'stopping')
})

// 获取所有配置项
const getAllConfigItems = () => {
  return [
    ...basicInfo.value,
    ...networkConfig.value,
    ...gameConfig.value,
    ...advancedConfig.value
  ]
}

// 加载 Game.ini
const loadGameIni = async () => {
  loadingGameIni.value = true
  gameIniEdited.value = false
  try {
    const data = await getGameIni(instanceName)
    if (data.success && data.data) {
      gameIniContent.value = data.data.content || ''
    } else {
      Message.error(data.error || '加载 Game.ini 失败')
    }
  } catch (err) {
    Message.error(err.message || '加载 Game.ini 失败')
  } finally {
    loadingGameIni.value = false
  }
}

// 保存 Game.ini
const saveGameIni = async (content) => {
  savingGameIni.value = true
  try {
    const data = await updateGameIni(instanceName, content)
    if (data.success) {
      Message.success('Game.ini 已保存')
      gameIniEdited.value = false
    } else {
      Message.error(data.error || '保存 Game.ini 失败')
    }
  } catch (err) {
    Message.error(err.message || '保存 Game.ini 失败')
  } finally {
    savingGameIni.value = false
  }
}

// 打开 Game.ini 上传模态框
const triggerGameIniUpload = () => {
  if (gameIniFileInput.value) {
    gameIniFileInput.value.click()
  }
}

// 处理 Game.ini 文件选择
const handleGameIniFileSelected = async (event) => {
  const file = event.target.files?.[0]
  if (!file) return

  uploadingGameIni.value = true
  try {
    const data = await uploadGameIniFile(instanceName, file)
    if (data.success) {
      Message.success('Game.ini 已上传')
      loadGameIni()
    } else {
      Message.error(data.error || '上传 Game.ini 失败')
    }
  } catch (err) {
    Message.error(err.message || '上传 Game.ini 失败')
  } finally {
    uploadingGameIni.value = false
    // 重置文件输入
    if (gameIniFileInput.value) {
      gameIniFileInput.value.value = ''
    }
  }
}

// 加载 GameUserSettings.ini
const loadGameUserSettings = async () => {
  loadingGameUserSettings.value = true
  gameUserSettingsEdited.value = false
  try {
    const data = await getGameUserSettings(instanceName)
    if (data.success && data.data) {
      gameUserSettingsContent.value = data.data.content || ''
    } else {
      Message.error(data.error || '加载 GameUserSettings.ini 失败')
    }
  } catch (err) {
    Message.error(err.message || '加载 GameUserSettings.ini 失败')
  } finally {
    loadingGameUserSettings.value = false
  }
}

// 保存 GameUserSettings.ini
const saveGameUserSettings = async (content) => {
  savingGameUserSettings.value = true
  try {
    const data = await updateGameUserSettings(instanceName, content)
    if (data.success) {
      Message.success('GameUserSettings.ini 已保存')
      gameUserSettingsEdited.value = false
    } else {
      Message.error(data.error || '保存 GameUserSettings.ini 失败')
    }
  } catch (err) {
    Message.error(err.message || '保存 GameUserSettings.ini 失败')
  } finally {
    savingGameUserSettings.value = false
  }
}

// 打开 GameUserSettings.ini 上传模态框
const triggerGameUserSettingsUpload = () => {
  if (gameUserSettingsFileInput.value) {
    gameUserSettingsFileInput.value.click()
  }
}

// 处理 GameUserSettings.ini 文件选择
const handleGameUserSettingsFileSelected = async (event) => {
  const file = event.target.files?.[0]
  if (!file) return

  uploadingGameUserSettings.value = true
  try {
    const data = await uploadGameUserSettingsFile(instanceName, file)
    if (data.success) {
      Message.success('GameUserSettings.ini 已上传')
      loadGameUserSettings()
    } else {
      Message.error(data.error || '上传 GameUserSettings.ini 失败')
    }
  } catch (err) {
    Message.error(err.message || '上传 GameUserSettings.ini 失败')
  } finally {
    uploadingGameUserSettings.value = false
    // 重置文件输入
    if (gameUserSettingsFileInput.value) {
      gameUserSettingsFileInput.value.value = ''
    }
  }
}

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
        },
        {
          label: '启用ASA插件',
          value: config.EnableAsaPlugin ? '是' : '否',
          type: 'boolean'
        }
      ]

      // 高级配置 - 保存原始密码值用于显示
      advancedConfig.value = [
        {
          label: '集群ID',
          value: config.ClusterID || '-',
          type: 'text'
        },
        {
          label: '自定义启动参数',
          value: config.CustomStartParameters || '-',
          type: 'text'
        },
        {
          label: '服务器密码',
          value: config.ServerPassword || '-',
          type: 'password',
          hasPassword: !!config.ServerPassword
        },
        {
          label: '管理员密码',
          value: config.ServerAdminPassword || '-',
          type: 'password',
          hasPassword: !!config.ServerAdminPassword
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
// 打开配置编辑弹出框
const openConfigEditModal = () => {
  configEditModalVisible.value = true
}

// 保存配置
const saveConfig = async (config) => {
  savingConfig.value = true
  try {
    const data = await updateInstanceConfig(instanceName, config)
    if (data.success) {
      Message.success('配置已保存')
      configEditModalVisible.value = false
      // 刷新实例配置
      await fetchInstanceConfig()
    } else {
      Message.error(data.error || '保存配置失败')
    }
  } catch (err) {
    Message.error(err.message || '保存配置失败')
  } finally {
    savingConfig.value = false
  }
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

// 重启实例
const restartInstance = () => {
  Modal.confirm({
    title: '提示',
    content: `确定要重启实例 "${instanceName}" 吗？`,
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      try {
        // 停止日志监听（重启过程中会断开连接）
        if (isStreaming.value) {
          stopLogStream()
        }
        
        // 使用 SSE 方式调用重启
        restartServerSSE(
          instanceName,
          // onMessage 回调 - 接收实时进度消息
          (message) => {
            console.log('Restart progress:', message)
            // 可选：在 UI 中显示重启进度
          },
          // onError 回调 - 处理错误
          (error) => {
            console.error('重启实例失败:', error)
            Message.error('重启实例失败')
          },
          // onComplete 回调 - 重启完成
          () => {
            console.log('Server restart completed')
            Message.success('实例重启成功')
          }
        )
      } catch (error) {
        console.error('重启实例失败:', error)
        Message.error('重启实例失败')
      }
    }
  })
}

onMounted(async () => {
  await fetchInstanceConfig()
  loadGameIni()
  loadGameUserSettings()

  // 如果已有缓存的实例状态，使用它
  const cachedStatus = getInstanceStatus(instanceName)
  if (cachedStatus) {
    instanceData.value = cachedStatus
  }

  // 如果进入页面时服务器已经在运行，自动启动日志监听
  if (instanceData.value?.running && !isStreaming.value) {
    console.log('Server is already running when entering the page, auto-starting log stream')
    setTimeout(() => {
      if (!isStreaming.value && instanceData.value?.running) {
        startLogStream()
      }
    }, 100)
  }

  // 监听当前实例的 server_starting 事件（实例开始启动时）
  unlistenServerStarting = onServerEvent('server_starting', (event) => {
    if (event.instance_name === instanceName) {
      console.log('Server starting event received, preparing for log stream')
      // Instance is starting, will auto-start log streaming when server_started event is received

      setTimeout(() => {
        if (!isStreaming.value && instanceData.value?.running) {
          startLogStream()
        }
      }, 2000)
    }
  })

  // 监听当前实例的 server_started 事件
  unlistenServerStarted = onServerEvent('server_started', (event) => {
    if (event.instance_name === instanceName) {
      console.log('Server started event received, auto-starting log stream')
      // 延迟100ms确保服务器完全启动
    }
  })

  // 监听当前实例的 server_stopped 事件
  unlistenServerStopped = onServerEvent('server_stopped', (event) => {
    if (event.instance_name === instanceName) {
      console.log('Server stopped event received, auto-stopping log stream')
      if (isStreaming.value) {
        stopLogStream()
      }
    }
  })
})


onUnmounted(() => {
  // 停止日志监听
  if (isStreaming.value) {
    stopLogStream()
  }

  // 取消 WebSocket 事件监听
  if (unlistenServerStarting) {
    unlistenServerStarting()
  }
  if (unlistenServerStarted) {
    unlistenServerStarted()
  }
  if (unlistenServerStopped) {
    unlistenServerStopped()
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

/* 配置一縎标题样式 */
.config-card-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
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
  height: 60vh;
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

/* 配置文件编辑器样式 */
.config-editor-container {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.config-editor-toolbar {
  padding: 10px 0;
  border-bottom: 1px solid #d9d9d9;
}

.config-textarea {
  min-height: 500px;
  font-family: 'Courier New', monospace;
  font-size: 13px;
  line-height: 1.5;
}

/* 高级配置项样式 */
.advanced-config-items {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.config-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 32px;
  background-color: #f5f5f5;
  border-radius: 4px;
  padding: 0 16px;
}

/* Grid 串串源 */
.config-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
}

.config-grid-item {
  display: flex;
  flex-direction: column;
}

.config-grid-item.full-width {
  grid-column: 1 / -1;
}

.config-grid-item .config-item {
  height: auto;
  padding: 12px 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.config-item-label {
  font-weight: 600;
  color: #333;
  min-width: 120px;
  font-size: 14px;
}

.config-item-content {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  justify-content: flex-end;
}

.config-item-value {
  color: #666;
  font-size: 14px;
  word-break: break-all;
}

.password-wrapper {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* 配置文件查看器样式 */
.config-file-viewer {
  border: 1px solid #d9d9d9;
  border-radius: 4px;
  background-color: #fafafa;
  overflow: hidden;
  min-height: 200px;
  max-height: 600px;
  display: flex;
  flex-direction: column;
}

.file-content {
  flex: 1;
  overflow-y: auto;
  padding: 12px 16px;
  font-family: 'Courier New', monospace;
  font-size: 12px;
  white-space: pre-wrap;
  word-wrap: break-word;
  line-height: 1.6;
  color: #333;
  background-color: #f5f5f5;
}

/* 配置文件行布局 */
.config-files-row {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 20px;
  width: 100%;
  box-sizing: border-box;
  overflow: hidden;
}

.config-file-card {
  display: flex;
  flex-direction: column;
  width: 100%;
  box-sizing: border-box;
  min-width: 0;
}

.config-viewer-wrapper {
  flex: 1;
  height: 500px;
  display: flex;
  flex-direction: column;
  width: 100%;
  box-sizing: border-box;
  overflow: hidden;
}

/* 配置编辑潤弹框样式 */
</style>
