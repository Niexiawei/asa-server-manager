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
            <a-divider direction="vertical" :margin="8" style="height: 24px"/>
            <a-button
                @click="rconFloatingVisible = true"
                :disabled="!instanceData?.running"
                type="primary"
                size="small"
            >
              RCON 终端
            </a-button>
          </a-space>
        </div>
      </template>

      <a-spin :loading="loading" style="width: 100%">
        <a-alert v-if="error" type="error" :title="`错误: ${error}`" closable/>

        <div v-else class="config-container">
          <a-card title="服务器配置" class="config-section server-config">
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
          <a-collapse v-model:active-key="activeCollapseKeys" class="config-files-collapse">
            <a-collapse-item key="config-files" header="实例配置文件">
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
            </a-collapse-item>
          </a-collapse>

          <a-card title="实时日志" class="config-section log-viewer-card">
            <log-viewer ref="logViewerRef" :instance-name="instanceName"/>
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

    <!-- RCON 浮窗 Modal -->
    <a-modal
        v-model:visible="rconFloatingVisible"
        title="RCON 交互式终端"
        draggable
        :width="1000"
        height="60vh"
        :mask="false"
        unmountOnClose
        :footer="false"
        class="rcon-modal"
    >
      <div class="rcon-modal-content">
        <rcon-terminal :headerDisable="true"
                       :instance-name="instanceName" :instance-running="instanceData?.running || false"/>
      </div>
    </a-modal>

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
import LogViewer from '@/components/LogViewer.vue'
import RconTerminal from '@/components/RconTerminal.vue'
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
import {IconLeft, IconEyeInvisible, IconEye, IconClose, IconMinus, IconPlus} from '@arco-design/web-vue/es/icon'
import {Modal, Message} from '@arco-design/web-vue'
import VueWebTerminal from 'vue-web-terminal'

// Monaco Editor 引用 - 已移至 ConfigEditor 组件
const loading = ref(true)
const error = ref(null)
const instanceData = ref(null)

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

// 日志查看器引用
const logViewerRef = ref(null)

// 配置文件折叠面板状态
const activeCollapseKeys = ref([])

// RCON 浮窗相关
const rconFloatingVisible = ref(false)

// 监听 WebSocket 事件，实时更新实例运行状态
watch(
    () => getInstanceStatus(instanceName),
    (newStatus) => {
      if (newStatus) {
        // 实时更新实例运行状态
        if (instanceData.value) {
          instanceData.value.running = newStatus.running
        }
      }
    }
)

// 监听 server_starting 事件，自动开启日志获取
let unlistenServerStarting = null

// 监听 server_stopped 事件，自动关闭日志获取
let unlistenServerStopped = null

// ... existing code ...

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

  if (instanceData.value?.running) {
    setTimeout(() => {
      logViewerRef.value.startLogStream()
    }, 500)
  }

  // 监听 server_starting 事件，自动开启日志获取
  unlistenServerStarting = onServerEvent('server_starting', (event) => {
    if (event.instance_name === instanceName) {
      // 提前启动日志监听，无需等待完全启动
      if (logViewerRef.value && !logViewerRef.value.isStreaming) {
        nextTick(() => {
          setTimeout(() => {
            logViewerRef.value.startLogStream()
          }, 500)
        })
      }
    }
  })

  // 监听 server_stopped 事件，自动关闭日志获取
  unlistenServerStopped = onServerEvent('server_stopped', (event) => {
    if (event.instance_name === instanceName) {
      // 停止日志监听
      if (logViewerRef.value && logViewerRef.value.isStreaming) {
        logViewerRef.value.stopLogStream()
      }
    }
  })
})

onUnmounted(() => {
  // 移除事件监听
  if (unlistenServerStarting) {
    unlistenServerStarting()
  }
  if (unlistenServerStopped) {
    unlistenServerStopped()
  }
  // 停止日志监听
  if (logViewerRef.value && logViewerRef.value.isStreaming) {
    logViewerRef.value.stopLogStream()
  }
})
</script>

<style scoped lang="less">
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

.server-config {
  height: auto !important;

  :deep(.arco-card-body) {
    height: auto !important;
    box-sizing: border-box;
  }
}

.config-section {
  border-radius: 6px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.08);
  height: 700px;

  :deep(.arco-card-body) {
    height: calc(100% - 45.5px);
    box-sizing: border-box;
  }
}

/* 配置一縎标题样式 */
.config-card-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
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

/* 配置文件行布局 */
.config-files-row {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 5px;
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
  height: 450px !important;
}

.config-viewer-wrapper {
  flex: 1;
  height: calc(100% - 47px);
  display: flex;
  flex-direction: column;
  width: 100%;
  box-sizing: border-box;
  overflow: hidden;
}

.log-viewer-card {
  height: 65vh !important;
}

.config-files-collapse {
  :deep(.arco-collapse-item-content) {
    padding-left: 13px !important;
  }
}

/* RCON Modal 调整 */
.rcon-modal-content {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
}


</style>
