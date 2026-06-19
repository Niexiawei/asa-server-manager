<template>
  <div class="instance-detail">
    <t-card class="detail-card" :bordered="false" headerBordered>
      <template #title>
        <div class="detail-header">
          <t-button
              variant="text"
              size="large"
              @click="backMainPage"
          >
            <template #icon>
              <chevron-left-icon/>
            </template>
          </t-button>
          <span class="instance-name">{{ instanceName }}</span>
          <t-tag :theme="statusTagTheme(instanceStatus)">
            {{ statusLabel(instanceStatus) }}
          </t-tag>
        </div>
      </template>
      <template #actions>
        <t-space breakLine>
          <t-button
              @click="startInstance"
              :disabled="!isCleanStopped"
              :loading="instanceStatus === 'starting' || instanceStatus === 'start_initialization'"
              theme="primary"
          >
            启动
          </t-button>
          <t-button
              @click="stopInstance"
              :disabled="instanceStatus !== 'started'"
              :loading="instanceStatus === 'stopping'"
              theme="warning"
          >
            停止
          </t-button>
          <t-button
              @click="restartInstance"
              :disabled="instanceStatus !== 'started'"
              :loading="instanceStatus === 'restarting'"
              theme="success"
          >
            重启
          </t-button>
          <t-divider layout="vertical" style="height: 100%"/>
          <t-button
              @click="rconFloatingVisible = true"
              :disabled="instanceStatus !== 'started'"
              theme="primary"
          >
            RCON 终端
          </t-button>
          <t-button
              @click="forceStopInstance"
              :disabled="instanceStatus === 'stopped' || instanceStatus === ''"
              theme="danger"
          >
            强制停止
          </t-button>
        </t-space>
      </template>
      <t-alert v-if="error" theme="error" :message="`错误: ${error}`" close/>
      <div v-else class="config-container">
        <!-- 服务器配置与资源监控并排布局 -->
        <div class="config-resource-row">
          <t-card headerBordered class="config-section server-config">
            <template #title>
              <div class="config-card-title">
                <span>服务器配置</span>
              </div>
            </template>
            <template #actions>
              <t-button
                  theme="primary"
                  size="small"
                  @click="openConfigEditModal"
                  style="margin-left: 12px"
                  :disabled="instanceData?.running"
              >
                编辑
              </t-button>
            </template>
            <div class="config-grid">
              <div v-for="item in getAllConfigItems()" :key="item.label" class="config-grid-item"
                   :class="{ 'full-width': item.label === '自定义启动参数', 'modid-item':item.label === 'Mod'}">
                <div class="config-item">
                  <div class="config-item-label">{{ item.label }}</div>
                  <div class="config-item-content">
                    <div v-if="(!item.type || item.type === 'text') && item.label === 'Mod'"
                         class="config-item-value">
                      <div v-if="item.value && item.value !== '-'">
                        <div class="mod-container">
                          <t-tag
                              v-for="(modId, index) in item.value.split(',')" :key="modId"
                              class="mod-tag"
                              theme="primary"
                              @click="copyModId(modId.trim())"
                          >
                            {{ getModNameById(modId.trim()) || modId.trim() }}
                            <file-copy-icon :style="{fontSize: '12px'}"/>
                          </t-tag>
                          <div class="break"></div>
                          <t-tag
                              @click="copyAllModIds(item.value)"
                              class="copy-all-btn"
                          >
                            <file-copy-icon/>
                            复制全部
                          </t-tag>
                        </div>
                      </div>
                      <div v-else>
                        {{ item.value }}
                      </div>
                    </div>
                    <div v-if="(!item.type || item.type === 'text') && item.label !== 'Mod'"
                         class="config-item-value">
                      {{ item.value }}
                    </div>
                    <div v-if="item.type === 'boolean'" class="config-item-value">
                      <t-tag :theme="item.value === '是' ? 'success' : 'default'">{{ item.value }}</t-tag>
                    </div>
                    <div v-if="item.type === 'password'" class="password-wrapper">
                        <span class="config-item-value">
                          {{
                            item.label === '服务器密码' && showServerPassword ? item.value : (item.label === '管理员密码' && showAdminPassword ? item.value : (item.hasPassword ? '●●●●●●' : item.value))
                          }}
                        </span>
                      <t-button
                          v-if="item.hasPassword"
                          variant="text"
                          size="small"
                          @click="item.label === '服务器密码' ? (showServerPassword = !showServerPassword) : (showAdminPassword = !showAdminPassword)"
                      >
                        <template #icon>
                          <component
                              :is="item.label === '服务器密码' ? (showServerPassword ? BrowseOffIcon : BrowseIcon) : (showAdminPassword ? BrowseOffIcon : BrowseIcon)"/>
                        </template>
                      </t-button>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </t-card>
          <div class="info-right" v-if="baseLoadedSuccessfully">
            <!-- 资源监控组件 -->
            <t-card class="config-section resource-monitor-card" headerBordered>
              <template #title>
                <div class="config-card-title">
                  <span>资源占用</span>
                </div>
              </template>
              <resource-monitor
                  :show-title-div="false"
                  :instance-name="instanceName"
              />
            </t-card>
            <!-- 实例历史状态组件 -->
            <t-card class="config-section status-history-card" headerBordered>
              <template #title>
                <div class="config-card-title">
                  <span>实例历史状态</span>
                </div>
              </template>
              <instance-status-history
                  :instance-name="instanceName"
              />
            </t-card>
          </div>
          <t-card class="info-right" v-else>
            <t-skeleton :animation="true" theme="paragraph"></t-skeleton>
            <t-skeleton :animation="true" theme="paragraph"></t-skeleton>
            <t-skeleton :animation="true" theme="paragraph"></t-skeleton>
            <t-skeleton :animation="true" theme="paragraph"></t-skeleton>
          </t-card>
        </div>

        <!-- 配置文件区域 -->
        <t-collapse v-model:value="activeCollapseKeys" class="config-files-collapse">
          <t-collapse-panel value="config-files" header="实例配置文件">
            <div class="config-files-row">
              <!-- Game.ini 配置 -->
              <t-card title="Game.ini 配置" class="config-section config-file-card">
                <t-space style="margin-bottom: 15px">
                  <t-button
                      @click="triggerGameIniUpload"
                      theme="success"
                      :disabled="instanceData?.running"
                  >
                    上传文件
                  </t-button>
                  <t-button
                      @click="gameIniEditModalVisible = true"
                      theme="primary"
                      :disabled="instanceData?.running"
                  >
                    编辑文件
                  </t-button>
                  <t-button
                      @click="compareGameIni"
                      :loading="loadingDiffContent"
                      :disabled="instanceData?.running"
                  >
                    对比配置
                  </t-button>
                </t-space>

                <div class="config-viewer-wrapper">
                  <config-file-viewer
                      :content="gameIniContent"
                      language="ini"
                  />
                </div>
              </t-card>

              <!-- GameUserSettings.ini 配置 -->
              <t-card title="GameUserSettings.ini 配置" class="config-section config-file-card">
                <t-space style="margin-bottom: 15px">
                  <t-button
                      @click="triggerGameUserSettingsUpload"
                      theme="success"
                      :disabled="instanceData?.running"
                  >
                    上传文件
                  </t-button>
                  <t-button
                      @click="gameUserSettingsEditModalVisible = true"
                      theme="primary"
                      :disabled="instanceData?.running"
                  >
                    编辑文件
                  </t-button>
                  <t-button
                      @click="compareGameUserSettings"
                      :loading="loadingDiffContent"
                      :disabled="instanceData?.running"
                  >
                    对比配置
                  </t-button>
                </t-space>

                <div class="config-viewer-wrapper">
                  <config-file-viewer
                      :content="gameUserSettingsContent"
                      language="ini"
                  />
                </div>
              </t-card>
            </div>
          </t-collapse-panel>
        </t-collapse>

        <t-card title="实时日志" class="config-section log-viewer-card" headerBordered>
          <log-viewer ref="logViewerRef" :instance-name="instanceName"/>
        </t-card>
      </div>
    </t-card>

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
    <t-dialog
        v-model:visible="rconFloatingVisible"
        header="RCON 交互式终端"
        draggable
        :width="1000"
        :modal="false"
        destroy-on-close
        :footer="false"
        class="rcon-modal"
    >
      <div class="rcon-modal-content" v-if="rconFloatingVisible">
        <rcon-terminal :headerDisable="true"
                       :instance-name="instanceName" :instance-running="instanceData?.running || false"/>
      </div>
    </t-dialog>

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
import {computed, onMounted, onUnmounted, ref, watch} from 'vue'
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
  getGameIni,
  getGameUserSettings,
  getInstanceConfig,
  getModInfo,
  getServerConfigs,
  restartServer,
  startServer,
  stopServer,
  forceStopServer,
  updateGameIni,
  updateGameUserSettings,
  updateInstanceConfig,
  uploadGameIniFile,
  uploadGameUserSettingsFile
} from '@/apis/api.js'
import {getInstanceStatus, initServer} from '@/store/serverStore.js'
import {FileCopyIcon, BrowseIcon, BrowseOffIcon, ChevronLeftIcon} from 'tdesign-icons-vue-next'
import {MessagePlugin, DialogPlugin, NotifyPlugin} from 'tdesign-vue-next'
import {useClipboard} from "@vueuse/core";

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

// 状态辅助
const instanceStatus = computed(() => getInstanceStatus(instanceName)?.status || 'stopped')
const isTransitional = computed(() =>
  ['start_initialization', 'start_initialization_successful',
   'starting', 'stopping', 'restarting'].includes(instanceStatus.value))
const isFailed = computed(() =>
  ['start_failed', 'stop_failed', 'restart_failed'].includes(instanceStatus.value))
const isCleanStopped = computed(() =>
  ['stopped', 'start_failed', 'stop_failed', 'restart_failed', ''].includes(instanceStatus.value))
const statusLabel = (status) => ({
  start_initialization: '初始化中',
  start_initialization_successful: '初始化完成',
  starting: '启动中', started: '运行中', stopping: '停止中',
  stopped: '已停止', restarting: '重启中', restarted: '运行中',
  start_failed: '启动失败', stop_failed: '停止失败', restart_failed: '重启失败'
}[status] || '已停止')
const statusTagTheme = (status) => ({
  start_initialization: 'primary',
  start_initialization_successful: 'primary',
  starting: 'warning', started: 'success', stopping: 'warning',
  stopped: 'default', restarting: 'warning', restarted: 'success',
  start_failed: 'danger', stop_failed: 'danger', restart_failed: 'danger'
}[status] || 'default')

const baseLoadedSuccessfully = ref(false)

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
      if (newStatus && instanceData.value) {
        instanceData.value.running = newStatus.running
        instanceData.value.status = newStatus.status
      }
    }
)
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
      MessagePlugin.error(data.error || '加载 Game.ini 失败')
    }
  } catch (err) {
    MessagePlugin.error(err.message || '加载 Game.ini 失败')
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
      MessagePlugin.success('Game.ini 已保存')
      gameIniEdited.value = false
      await loadGameIni()
    } else {
      MessagePlugin.error(data.error || '保存 Game.ini 失败')
    }
  } catch (err) {
    MessagePlugin.error(err.message || '保存 Game.ini 失败')
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
      MessagePlugin.success('Game.ini 已上传')
      loadGameIni()
    } else {
      MessagePlugin.error(data.error || '上传 Game.ini 失败')
    }
  } catch (err) {
    MessagePlugin.error(err.message || '上传 Game.ini 失败')
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
      MessagePlugin.error(data.error || '加载 GameUserSettings.ini 失败')
    }
  } catch (err) {
    MessagePlugin.error(err.message || '加载 GameUserSettings.ini 失败')
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
      MessagePlugin.success('GameUserSettings.ini 已保存')
      gameUserSettingsEdited.value = false
      await loadGameUserSettings()
    } else {
      MessagePlugin.error(data.error || '保存 GameUserSettings.ini 失败')
    }
  } catch (err) {
    MessagePlugin.error(err.message || '保存 GameUserSettings.ini 失败')
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
      MessagePlugin.success('GameUserSettings.ini 已上传')
      loadGameUserSettings()
    } else {
      MessagePlugin.error(data.error || '上传 GameUserSettings.ini 失败')
    }
  } catch (err) {
    MessagePlugin.error(err.message || '上传 GameUserSettings.ini 失败')
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
          label: 'Message Of The Day',
          value: config.MessageOfTheDay || '-',
          type: 'text'
        },
        {
          label: '消息时长',
          value: config.MessageOfTheDayDuration || '-',
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
        },
        {
          label: '自定义启动参数',
          value: config.CustomStartParameters || '-',
          type: 'text'
        },
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
      MessagePlugin.success('配置已保存')
      configEditModalVisible.value = false
      await fetchInstanceConfig()
      await loadGameUserSettings()
    } else {
      MessagePlugin.error(data.error || '保存配置失败')
    }
  } catch (err) {
    MessagePlugin.error(err.message || '保存配置失败')
  } finally {
    savingConfig.value = false
  }
}

// 启动实例
const startInstance = () => {
  let startDialog = DialogPlugin.confirm({
    header: '提示',
    body: `确定要启动实例 "${instanceName}" 吗？`,
    confirmBtn: '确定',
    cancelBtn: '取消',
    onConfirm: async () => {
      startDialog.hide()
      try {
        const data = await startServer(instanceName)
        if (data.success) {
          MessagePlugin.success(data.message || `实例 "${instanceName}" 正在启动`)
        } else {
          NotifyPlugin.error({
            title: `实例 "${instanceName}" 启动失败`,
            content: data.error || `实例 "${instanceName}" 启动失败`
          })
        }
      } catch (error) {
        MessagePlugin.error(`启动实例失败: ${error.message}`)
        console.error('启动实例失败:', error)
      }
    }
  })
}

// 停止实例
const stopInstance = () => {
  let stopDialog = DialogPlugin.confirm({
    header: '提示',
    body: `确定要停止实例 "${instanceName}" 吗？`,
    confirmBtn: '确定',
    cancelBtn: '取消',
    onConfirm: async () => {
      stopDialog.hide()
      try {
        const data = await stopServer(instanceName)
        if (data.success) {
          MessagePlugin.success(data.message || `实例 "${instanceName}" 正在停止`)
        } else {
          MessagePlugin.error(data.error || `实例 "${instanceName}" 停止失败`)
          console.error('停止实例失败:', data.error)
        }
      } catch (error) {
        MessagePlugin.error(`停止实例失败: ${error.message}`)
        console.error('停止实例失败:', error)
      }
    }
  })
}

// 重启实例
const restartInstance = () => {
  let restartDialog = DialogPlugin.confirm({
    header: '提示',
    body: `确定要重启实例 "${instanceName}" 吗？`,
    confirmBtn: '确定',
    cancelBtn: '取消',
    onConfirm: async () => {
      restartDialog.hide()
      try {
        const data = await restartServer(instanceName)
        if (data.success) {
          MessagePlugin.success(data.message || '实例正在重启')
        } else {
          MessagePlugin.error(data.error || '重启实例失败')
        }
      } catch (error) {
        console.error('重启实例失败:', error)
        MessagePlugin.error('重启实例失败')
      }
    }
  })
}

// 强制停止实例
const forceStopInstance = () => {
  let dialog = DialogPlugin.confirm({
    header: '警告',
    body: `确定要强制停止实例 "${instanceName}" 吗？这将直接终止进程并重置状态。`,
    theme: 'danger',
    confirmBtn: '确定',
    cancelBtn: '取消',
    onConfirm: async () => {
      dialog.hide()
      try {
        const data = await forceStopServer(instanceName)
        if (data.success) {
          MessagePlugin.success(data.message || `实例 "${instanceName}" 已强制停止`)
        } else {
          MessagePlugin.error(data.error || '强制停止失败')
        }
      } catch (error) {
        MessagePlugin.error(`强制停止失败: ${error.message}`)
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
      MessagePlugin.error('加载服务器配置失败')
    }
  } catch (err) {
    MessagePlugin.error('加载服务器配置失败: ' + err.message)
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


const {text, isSupported, copy} = useClipboard({
  legacy: true,
})

// 复制单个 Mod ID 到剪切板
const copyModId = async (modId) => {
  try {
    await copy(modId);
    MessagePlugin.success(`${getModNameById(modId)}:已复制到剪切板`)
  } catch (error) {
    console.error('复制失败:', error)
    MessagePlugin.error('复制失败:' + error)
  }
}

// 复制全部 Mod ID 到剪切板
const copyAllModIds = async (modIds) => {
  try {
    const ids = modIds.split(',').map(id => id.trim()).filter(id => id).join(',')
    await copy(ids)
    MessagePlugin.success('已复制所有Mod ID到剪切板')
  } catch (error) {
    console.error('复制失败:', error)
    MessagePlugin.error('复制失败:' + error)
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
      MessagePlugin.error("不支持的文件对比")
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
    MessagePlugin.error("获取实列表失败:" + error)
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  await fetchInstanceConfig()
  await fetchInstances()
  baseLoadedSuccessfully.value = true

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

.config-resource-row {
  display: grid;
  grid-template-columns: 3fr 1fr;
  gap: 15px;
  width: 100%;

  .resource-monitor-card {
    flex: 0 0 auto;
  }

  .status-history-card {
    flex: 1 1 0; /* 占剩余空间，可收缩 */
    min-height: 0; /* 关键：允许收缩，禁止 min-content 阻止收缩 */
  }

  .info-right {
    display: flex;
    flex-direction: column;
    height: 100%;
    gap: 15px;

    :deep(.t-skeleton) {
      padding-bottom: 12px;

      &:last-child {
        padding-bottom: 0;
      }
    }
  }
}

.server-config {
}

.resource-monitor-card {
  :deep(.t-card__body) {
    box-sizing: border-box;
    padding: 15px !important;
  }
}

.config-section {
  border-radius: 6px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.08);

  //:deep(.t-card__body) {
  //  height: 100%;
  //  box-sizing: border-box;
  //}
  //
  //.t-card__header {
  //  flex: 0 0 auto;
  //}
  //
  //.t-loading__parent {
  //  flex: 1 1 0; /* 占剩余空间，可收缩 */
  //  min-height: 0; /* 关键：允许收缩，禁止 min-content 阻止收缩 */
  //}


  :deep(.t-loading__parent) {
    height: calc(100% - 56px);
  }

  :deep(.t-card__body) {
    height: 100%;
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

        > .t-tag {
          cursor: pointer;
        }

        .break {
          flex: 0 0 100%;
          height: 0;
        }
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
  :deep(.t-collapse-panel__content) {
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
