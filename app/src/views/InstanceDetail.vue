<template>
  <div class="instance-detail">
    <a-card class="detail-card" :bordered="false">
      <template #title>
        <div class="detail-header">
          <a-button
              type="text"
              size="large"
              @click="backMainPage"
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
                :disabled="instanceData?.running || instanceStartLoading"
                :loading="instanceStartLoading"
                type="primary"
                size="small"
            >
              启动
            </a-button>
            <a-button
                @click="stopInstance"
                :disabled="!instanceData?.running || instanceStopLoading"
                :loading="instanceStopLoading"
                status="warning"
                size="small"
            >
              停止
            </a-button>
            <a-button
                @click="restartInstance"
                :disabled="!instanceData?.running || instanceRestartLoading"
                :loading="instanceRestartLoading"
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
          <!-- 服务器配置与资源监控并排布局 -->
          <div class="config-resource-row">
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
                     :class="{ 'full-width': item.label === '自定义启动参数', 'modid-item':item.label === 'Mod'}">
                  <div class="config-item">
                    <div class="config-item-label">{{ item.label }}</div>
                    <div class="config-item-content">
                      <div v-if="!item.type || item.type === 'text'" class="config-item-value">
                        <template v-if="item.label === 'Mod'">
                          <template v-if="item.value && item.value !== '-'">
                            <div class="mod-container">
                              <template v-for="(modId, index) in item.value.split(',')" :key="modId">
                                <a-tag 
                                    class="mod-tag" 
                                    v-if="modId.trim()" 
                                    color="arcoblue"
                                    @click="copyModId(modId.trim())"
                                    style="cursor: pointer; display: flex; align-items: center; gap: 4px;"
                                >
                                  {{ getModNameById(modId.trim()) || modId.trim() }}
                                  <icon-copy :style="{fontSize: '12px'}"/>
                                </a-tag>
                              </template>
                            </div>
                            <a-button 
                                type="text" 
                                size="mini"
                                @click="copyAllModIds(item.value)"
                                class="copy-all-btn"
                            >
                              <icon-copy /> 复制全部
                            </a-button>
                          </template>
                          <template v-else>
                            {{ item.value }}
                          </template>
                        </template>
                        <template v-else>
                          {{ item.value }}
                        </template>
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
            <div class="info-right">
              <!-- 资源监控组件 -->
              <a-card class="config-section resource-monitor-card">
                <template #title>
                  <div class="config-card-title">
                    <span>资源占用</span>
                  </div>
                </template>
                <resource-monitor
                    :show-title-div="false"
                    :instance-name="instanceName"
                />
              </a-card>
              <!-- 实例历史状态组件 -->
              <a-card class="config-section status-history-card">
                <template #title>
                  <div class="config-card-title">
                    <span>实例历史状态</span>
                  </div>
                </template>
                <instance-status-history
                    :instance-name="instanceName"
                />
              </a-card>
            </div>
          </div>

          <!-- 配置文件区域 -->
          <a-collapse v-model:active-key="activeCollapseKeys" class="config-files-collapse">
            <a-collapse-item key="config-files" header="实例配置文件">
              <div class="config-files-row">
                <!-- Game.ini 配置 -->
                <a-card title="Game.ini 配置" class="config-section config-file-card">
                  <a-space style="margin-bottom: 15px">
                    <a-button
                        @click="triggerGameIniUpload"
                        status="success"
                        :disabled="instanceData?.running"
                    >
                      上传文件
                    </a-button>
                    <a-button
                        @click="gameIniEditModalVisible = true"
                        type="primary"
                        :disabled="instanceData?.running"
                    >
                      编辑文件
                    </a-button>
                    <a-button
                        @click="compareGameIni"
                        :loading="loadingDiffContent"
                        :disabled="instanceData?.running"
                    >
                      对比配置
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
                        @click="triggerGameUserSettingsUpload"
                        status="success"
                        :disabled="instanceData?.running"
                    >
                      上传文件
                    </a-button>
                    <a-button
                        @click="gameUserSettingsEditModalVisible = true"
                        type="primary"
                        :disabled="instanceData?.running"
                    >
                      编辑文件
                    </a-button>
                    <a-button
                        @click="compareGameUserSettings"
                        :loading="loadingDiffContent"
                        :disabled="instanceData?.running"
                    >
                      对比配置
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
        :mask="false"
        unmountOnClose
        :footer="false"
        class="rcon-modal"
    >
      <div class="rcon-modal-content" v-if="rconFloatingVisible">
        <rcon-terminal :headerDisable="true"
                       :instance-name="instanceName" :instance-running="instanceData?.running || false"/>
      </div>
    </a-modal>

    <!-- 配置文件对比 Modal 组件 -->
    <config-diff-modal
        v-model:visible="diffModalVisible"
        :diff-type="diffType"
        :game-ini-content="gameIniContent"
        :game-user-settings-content="gameUserSettingsContent"
        :server-game-ini-content="serverGameIniContent"
        :server-game-user-settings-content="serverGameUserSettingsContent"
        v-model:dataLoading="loadingDiffContent"
        :editable="true"
        v-model:saving-loading="diffSaveLoading"
        @save="handleDiffSave"
    />

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
import {useRoute, useRouter} from 'vue-router'
import ConfigEditor from '@/components/ConfigEditor.vue'
import ConfigFileViewer from '@/components/ConfigFileViewer.vue'
import ConfigDiffModal from '@/components/ConfigDiffModal.vue'
import ConfigEditModal from '@/components/ConfigEditModal.vue'
import LogViewer from '@/components/LogViewer.vue'
import RconTerminal from '@/components/RconTerminal.vue'
import ResourceMonitor from '@/components/ResourceMonitor.vue'
import InstanceStatusHistory from '@/components/InstanceStatusHistory.vue'
import {
  getInstanceConfig,
  startServer,
  stopServer,
  restartServerSSE,
  getGameIni,
  getGameUserSettings,
  getServerConfigs,
  updateGameIni,
  updateGameUserSettings,
  uploadGameIniFile,
  uploadGameUserSettingsFile,
  updateInstanceConfig,
  getModInfo
} from '@/apis/api.js'
import {serverStore, getInstanceStatus, initServer} from '@/store/serverStore.js'
import {onServerEvent} from '@/apis/api.js'
import {IconLeft, IconEyeInvisible, IconEye, IconClose, IconMinus, IconPlus, IconCopy} from '@arco-design/web-vue/es/icon'
import {Modal, Message, Notification} from '@arco-design/web-vue'

// Monaco Editor 引用 - 已移至 ConfigEditor 组件
const loading = ref(true)
const error = ref(null)
const instanceData = ref([])

// Mod信息
const modInfo = ref([])
const modInfoLoading = ref(false)

const route = useRoute()
const router = useRouter()
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

// 启动、停止、重启 按预的 loading 状态
const instanceStartLoading = ref(false)
const instanceStopLoading = ref(false)
const instanceRestartLoading = ref(false)

// 日志查看器引用
const logViewerRef = ref(null)

// 配置文件折叠面板状态
const activeCollapseKeys = ref([])

// RCON 浮窗相关
const rconFloatingVisible = ref(false)

// 配置文件对比相关
const diffModalVisible = ref(false)
const diffType = ref('game-ini')
const serverGameIniContent = ref('')
const serverGameUserSettingsContent = ref('')
const loadingDiffContent = ref(false)

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

function backMainPage() {
  router.replace({
    path: '/'
  })
}

// 保存 Game.ini
const saveGameIni = async (content) => {
  savingGameIni.value = true
  try {
    const data = await updateGameIni(instanceName, content)
    if (data.success) {
      Message.success('Game.ini 已保存')
      gameIniEdited.value = false
      await loadGameIni()
    } else {
      Message.error(data.error || '保存 Game.ini 失败')
    }
  } catch (err) {
    Message.error(err.message || '保存 Game.ini 失败')
    throw err
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
      await loadGameUserSettings()
    } else {
      Message.error(data.error || '保存 GameUserSettings.ini 失败')
    }
  } catch (err) {
    Message.error(err.message || '保存 GameUserSettings.ini 失败')
    throw err
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
      const instance = data.data || []
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
        },
        {
          label: '绑定域名',
          value: config.BindDomain || '-'
        }
      ]

      // 游戏配置
      gameConfig.value = [
        {
          label: '地图名称',
          value: config.MapName || '-'
        },
        {
          label: 'Mod',
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
      instanceStartLoading.value = true
      try {
        // 使用 SSE 方式调用启动
        await startServer(
            instanceName,
            // onMessage 回调 - 接收实时进度消息
            (message) => {
              console.log('Start progress:', message)
              // 检查启动失败的条件
            },
            // onError 回调 - 处理错误
            (error) => {
              // 启动失败：设置实例状态为未启动
              if (instanceData.value) {
                instanceData.value.running = false
              }

              Notification.error({
                title: `实例 "${instanceName}" 启动失败`,
                content: error.message || `实例 "${instanceName}" 启动失败`,
                duration: 0, // 0 表示不自动隐藏
                closable: true
              })

              console.error('启动实例失败:', error)
            },
            // onComplete 回调 - 启动完成
            () => {
              Message.success(`实例 "${instanceName}" 启动成功`)
              // 更新实例运行状态
              if (instanceData.value) {
                instanceData.value.running = true
              }
            }
        )
      } catch (error) {
        Message.error(`启动实例失败: ${error.message}`)
        console.error('启动实例失败:', error)
      } finally {
        instanceStartLoading.value = false
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
      instanceStopLoading.value = true
      try {
        const data = await stopServer(instanceName)
        if (data.success) {
          Message.success(data.message || `实例 "${instanceName}" 停止成功`)
          // 更新实例运行状态
          if (instanceData.value) {
            instanceData.value.running = false
          }
        } else {
          Message.error(data.error || `实例 "${instanceName}" 停止失败`)
          console.error('停止实例失败:', data.error)
        }
      } catch (error) {
        Message.error(`停止实例失败: ${error.message}`)
        console.error('停止实例失败:', error)
      } finally {
        instanceStopLoading.value = false
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
      instanceRestartLoading.value = true
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
      } finally {
        instanceRestartLoading.value = false
      }
    }
  })
}

// 加载服务器配置
const loadServerConfigs = async () => {
  loadingDiffContent.value = true
  try {
    const data = await getServerConfigs()
    if (data.success && data.data) {
      serverGameIniContent.value = data.data.game_ini?.content || ''
      serverGameUserSettingsContent.value = data.data.game_user_settings?.content || ''
    } else {
      Message.error('加载服务器配置失败')
    }
  } catch (err) {
    Message.error('加载服务器配置失败: ' + err.message)
  } finally {
    loadingDiffContent.value = false
  }
}

// 获取Mod信息
const fetchModInfo = async () => {
  modInfoLoading.value = true
  try {
    const data = await getModInfo()
    if (data.success) {
      modInfo.value = data.data || []
    } else {
      console.error('获取Mod信息失败:', data.error)
      modInfo.value = []
    }
  } catch (error) {
    console.error('获取Mod信息失败:', error)
    modInfo.value = []
  } finally {
    modInfoLoading.value = false
  }
}

// 根据Mod ID获取Mod名称
const getModNameById = (modId) => {
  if (!modId) return null

  const mod = modInfo.value.find(m => m.id === modId)
  return mod ? mod.name : null
}

// 复制单个 Mod ID 到剪切板
const copyModId = async (modId) => {
  try {
    await navigator.clipboard.writeText(modId)
    Message.success(`${getModNameById(modId)}:已复制到剪切板`)
  } catch (error) {
    console.error('复制失败:', error)
    Message.error('复制失败')
  }
}

// 复制全部 Mod ID 到剪切板
const copyAllModIds = async (modIds) => {
  try {
    const ids = modIds.split(',').map(id => id.trim()).filter(id => id).join(',')
    await navigator.clipboard.writeText(ids)
    Message.success('已复制所有Mod ID到剪切板')
  } catch (error) {
    console.error('复制失败:', error)
    Message.error('复制失败')
  }
}

// 打开 Game.ini 对比
const compareGameIni = async () => {
  diffType.value = 'game-ini'
  diffModalVisible.value = true
  await loadServerConfigs()
}

// 打开 GameUserSettings.ini 对比
const compareGameUserSettings = async () => {
  diffType.value = 'game-user-settings'
  diffModalVisible.value = true
  await loadServerConfigs()
}

const diffSaveLoading = ref(false)

// 处理 Diff 保存事件
const handleDiffSave = async ({type, content}) => {
  diffSaveLoading.value = true
  try {
    if (type == "game-ini") {
      await saveGameIni(content)
    } else if (type == "game-user-settings") {
      await saveGameUserSettings(content)
    } else {
      Message.error("不支持的文件对比")
    }
  } finally {
    diffSaveLoading.value = false
  }
}

const fetchInstances = async () => {
  loading.value = true
  try {
    await initServer()
  } catch (error) {
    console.error('获取实例表失败:', error)
    Message.error("获取实列表失败:" + error)
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  await fetchInstances()
  await fetchInstanceConfig()
  loadGameIni()
  loadGameUserSettings()
  fetchModInfo()

  // 如果已有缓存的实例状态，使用它
  const cachedStatus = getInstanceStatus(instanceName)
  if (cachedStatus) {
    instanceData.value = cachedStatus
  }

  // 日志监听已通过 LogViewer 组件内部的 watch 自动管理，无需外部调用
})

onUnmounted(() => {
  // 日志监听已通过 LogViewer 组件内部的 watch 自动管理
  // 组件卸载时，LogViewer 会自动停止监听
})
</script>

<style scoped lang="less">
.instance-detail {
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow-y: auto;
}

.status-history-card {
  :deep(.arco-card-body) {
    height: calc(100% - 46px) !important;
  }
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

/* 服务器配置与资源监控、实例历史状态并排布局 */
.config-resource-row {
  display: grid;
  grid-template-columns: 3fr 1fr;
  gap: 15px;
  width: 100%;

  .resource-monitor-card{
    flex: 0 0 auto;
  }

  .status-history-card{
    flex: 1 1 0; /* 占剩余空间，可收缩 */
    min-width: 0; /* 关键：允许收缩，禁止 min-content 阻止收缩 */
  }

  .info-right {
    display: flex;
    flex-direction: column;
    height: 100%;
    gap: 15px;
  }
}

.server-config {
  height: auto !important;

  :deep(.arco-card-body) {
    height: auto !important;
    box-sizing: border-box;
  }
}

.resource-monitor-card {
  height: auto !important;

  :deep(.arco-card-body) {
    height: auto !important;
    box-sizing: border-box;
    padding: 15px !important;
  }
}

.config-section {
  border-radius: 6px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.08);

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
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.config-grid-item {
  display: flex;
  flex-direction: column;
}


.config-grid-item.modid-item {
  width: 100%;
  grid-column: 1 / -1;

  .config-item {
    width: calc(100% - 32px);
    height: auto !important;
    min-height: 32px;
    align-items: flex-start !important;
    flex-direction: column;
  }

  .config-item-label {
    flex: 0 0 auto; /* 不收缩到 0，不占剩余空间 */
    white-space: nowrap;
    padding: 12px 0 !important;
    box-sizing: border-box;
    height: auto !important;
    line-height: 20px;
  }

  .config-item-content {
    flex: 1 1 0; /* 占剩余空间，可收缩 */
    min-width: 0; /* 关键：允许收缩，禁止 min-content 阻止收缩 */
    height: auto !important;
    padding: 0 0 !important;
    box-sizing: border-box;

    .config-item-value {
      width: 100%;
      display: flex;
      flex-direction: column;
      gap: 8px;

      .mod-container {
        display: flex;
        flex-wrap: wrap;
        gap: 4px;
      }

      .copy-all-btn {
        align-self: flex-start;
        font-size: 12px;
        padding: 4px 8px;
        height: auto;
      }
    }
  }
}

.config-grid-item.full-width {
  grid-column: 1 / -1;
}

.config-grid-item .config-item {
  height: 26px;
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

.mod-tag {
  margin: 2px;
  padding: 0 3px;
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
  height: 650px !important;
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
  height: 60vh;
  display: flex;
  flex-direction: column;
}


</style>
