<template>
  <div class="instance-detail">
    <t-card class="detail-card" ref="detailCardRef" :bordered="false" headerBordered>
      <template #title>
        <div class="detail-header">
          <t-button variant="text" @click="backMainPage">
            <template #icon>
              <chevron-left-icon size="18px"/>
            </template>
          </t-button>
          <span class="instance-name">{{ instanceName }}</span>
          <t-tag :theme="statusTagTheme(instanceStatus)">{{ statusLabel(instanceStatus) }}</t-tag>
          <template v-if="countdownText(instanceName)">
            <t-tag theme="warning" variant="light">{{ countdownText(instanceName) }}</t-tag>
            <t-link
                v-if="isCountingDown(instanceName)"
                theme="danger"
                size="small"
                @click="cancelCountdown"
            >取消
            </t-link>
          </template>
        </div>
      </template>
      <template #actions>
        <t-space breakLine>
          <t-button
              @click="startInstance"
              :disabled="!canStart(instanceStatus)"
              :loading="isStartLoading(instanceName, instanceStatus)"
              theme="primary"
          >
            启动
          </t-button>
          <t-button
              @click="stopInstance"
              :disabled="!canStop(instanceStatus)"
              :loading="isStopLoading(instanceStatus)"
              theme="warning"
          >
            停止
          </t-button>
          <t-button
              @click="restartInstance"
              :disabled="!canRestart(instanceStatus)"
              :loading="isRestartLoading(instanceName, instanceStatus)"
              theme="success"
          >
            重启
          </t-button>
          <t-divider layout="vertical" style="height: 100%"/>
          <t-button
              @click="rconFloatingVisible = true"
              :disabled="!canStop(instanceStatus)"
              theme="primary"
          >
            RCON 终端
          </t-button>
          <t-button
              @click="forceStopInstance"
              :disabled="!canForceStop(instanceStatus)"
              theme="danger"
          >
            强制停止
          </t-button>
        </t-space>
      </template>
      <t-alert v-if="error" theme="error" :message="`错误: ${error}`" close/>
      <div
          v-else
          class="tabs-container"
          :style="{ height: `calc(${detailCardHeight}px - 88px)` }"
      >
        <t-tabs :value="activeTab" @change="onTabChange" class="detail-tabs">
          <t-tab-panel value="overview" label="概览" lazy :destroy-on-hide="false">
            <div class="panel-scroll">
              <instance-overview-tab
                  :config="instanceData?.config || {}"
                  :asa-version="instanceData?.asaVersion || ''"
                  :instance-name="instanceName"
                  :mod-info="modInfo"
                  :base-loaded="baseLoadedSuccessfully"
              />
            </div>
          </t-tab-panel>

          <t-tab-panel value="basic" lazy :destroy-on-hide="false">
            <template #label>
              <span class="tab-label">基础配置<i v-if="dirtyMap.basic" class="dirty-dot"/></span>
            </template>
            <div class="panel-scroll">
              <instance-basic-config-tab
                  :key="`basic-${panelKey.basic}`"
                  :config="instanceData?.config || {}"
                  :saving="savingConfig"
                  :running="isRunning"
                  :mod-info="modInfo"
                  @save="saveConfig"
                  @update:dirty="dirtyMap.basic = $event"
              />
            </div>
          </t-tab-panel>

          <t-tab-panel value="rules" lazy :destroy-on-hide="false">
            <template #label>
              <span class="tab-label">服务器规则<i v-if="dirtyMap.rules" class="dirty-dot"/></span>
            </template>
            <div class="panel-scroll">
              <server-rules-tab
                  :key="`rules-${panelKey.rules}`"
                  :game-ini-content="gameIniContent"
                  :game-user-settings-content="gameUserSettingsContent"
                  :custom-start-parameters="instanceData?.config?.CustomStartParameters || ''"
                  :saving="savingGameIni || savingGameUserSettings"
                  :running="isRunning"
                  @save="saveAdvancedConfig"
                  @update:dirty="dirtyMap.rules = $event"
              />
            </div>
          </t-tab-panel>

          <t-tab-panel value="files" lazy :destroy-on-hide="false">
            <template #label>
              <span class="tab-label">配置文件<i v-if="dirtyMap.files" class="dirty-dot"/></span>
            </template>
            <instance-config-files-tab
                :key="`files-${panelKey.files}`"
                :game-ini-content="gameIniContent"
                :game-user-settings-content="gameUserSettingsContent"
                :running="isRunning"
                :saving-game-ini="savingGameIni"
                :saving-gus="savingGameUserSettings"
                @save-game-ini="saveGameIni"
                @save-gus="saveGameUserSettings"
                @update:dirty="dirtyMap.files = $event"
            />
          </t-tab-panel>

          <t-tab-panel value="plugin" label="插件配置" lazy :destroy-on-hide="false">
            <div class="panel-scroll">
              <plugin-data-panel
                  ref="pluginPanelRef"
                  :instance-name="instanceName"
                  :interval="instanceData?.config?.PluginSnapshotInterval || 0"
                  @update:interval="saveSnapshotInterval"
              />
            </div>
          </t-tab-panel>

          <t-tab-panel value="backup" label="存档备份" lazy :destroy-on-hide="false">
            <div class="panel-scroll">
              <instance-backup-tab :instance-name="instanceName"/>
            </div>
          </t-tab-panel>

          <t-tab-panel value="logs" label="实时日志" lazy :destroy-on-hide="false">
            <div class="panel-scroll">
              <t-card class="log-viewer-card" :bordered="false">
                <log-viewer ref="logViewerRef" :instance-name="instanceName"/>
              </t-card>
            </div>
          </t-tab-panel>
        </t-tabs>
      </div>
    </t-card>

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
        <rcon-terminal
            :headerDisable="true"
            :instance-name="instanceName"
            :instance-running="instanceData?.running || false"
        />
      </div>
    </t-dialog>

    <!-- 停止/重启确认弹窗（内含倒计时选项） -->
    <CountdownConfirmDialog ref="countdownDialogRef"/>
  </div>
</template>

<script setup>
import {computed, onMounted, reactive, ref, watch} from 'vue'
import {useRoute, useRouter} from 'vue-router'
import PluginDataPanel from '@/components/PluginDataPanel.vue'
import LogViewer from '@/components/LogViewer.vue'
import RconTerminal from '@/components/RconTerminal.vue'
import CountdownConfirmDialog from '@/components/CountdownConfirmDialog.vue'
import InstanceOverviewTab from './components/InstanceOverviewTab.vue'
import InstanceBasicConfigTab from './components/InstanceBasicConfigTab.vue'
import ServerRulesTab from './components/ServerRulesTab.vue'
import InstanceConfigFilesTab from './components/InstanceConfigFilesTab.vue'
import InstanceBackupTab from './components/InstanceBackupTab.vue'
import {
  getGameIni,
  getGameUserSettings,
  getInstanceConfig,
  getModInfo,
  restartServer,
  startServer,
  stopServer,
  forceStopServer,
  cancelServerCountdown,
  updateGameIni,
  updateGameUserSettings,
  updateInstanceConfig,
} from '@/apis/api.js'
import {getInstanceStatus, initServer, addRestartPending} from '@/store/serverStore.js'
import {
  canForceStop,
  canStart,
  canStop,
  canRestart,
  isStartLoading,
  isStopLoading,
  isRestartLoading,
  statusLabel,
  statusTagTheme,
  countdownText,
  isCountingDown,
} from '@/composables/useInstanceState.js'
import {useUnsavedGuard} from '@/composables/useUnsavedGuard.js'
import {ChevronLeftIcon} from 'tdesign-icons-vue-next'
import {MessagePlugin, DialogPlugin, NotifyPlugin} from 'tdesign-vue-next'
import {useElementBounding} from '@vueuse/core'

const route = useRoute()
const router = useRouter()
const instanceName = route.params.name

const error = ref(null)
const instanceData = ref({})

const detailCardRef = ref()
const countdownDialogRef = ref(null)
const {height: detailCardHeight} = useElementBounding(detailCardRef)

const modInfo = ref([])
const baseLoadedSuccessfully = ref(false)

const gameIniContent = ref('')
const gameUserSettingsContent = ref('')
const savingGameIni = ref(false)
const savingGameUserSettings = ref(false)
const savingConfig = ref(false)

const rconFloatingVisible = ref(false)
const pluginPanelRef = ref(null)
const logViewerRef = ref(null)

const instanceStatus = computed(() => getInstanceStatus(instanceName)?.status || 'stopped')
const isRunning = computed(() => !!instanceData.value?.running)

// ---- Tab 状态 + 未保存保护 ----
const TAB_KEYS = ['overview', 'basic', 'rules', 'files', 'plugin', 'backup', 'logs']
const EDITABLE_TABS = ['basic', 'rules', 'files']

const validTab = (t) => (TAB_KEYS.includes(t) ? t : '')
const activeTab = ref(validTab(route.query.tab) || 'overview')

const dirtyMap = reactive({basic: false, rules: false, files: false})
const panelKey = reactive({basic: 0, rules: 0, files: 0})

const anyDirty = () => EDITABLE_TABS.some((t) => dirtyMap[t])
const guard = useUnsavedGuard(anyDirty)

const onTabChange = async (next) => {
  const cur = activeTab.value
  if (cur === next) return
  if (EDITABLE_TABS.includes(cur) && dirtyMap[cur]) {
    const ok = await guard.promptDiscard(true)
    if (!ok) return
    // 放弃修改：重挂该面板，让它从 props 重新初始化
    dirtyMap[cur] = false
    panelKey[cur]++
  }
  activeTab.value = next
}

// activeTab <-> ?tab= 双向同步
watch(activeTab, (t) => {
  if (route.query.tab !== t) {
    router.replace({query: {...route.query, tab: t}}).catch(() => {
    })
  }
  if (t === 'plugin') pluginPanelRef.value?.reload?.()
})
watch(
    () => route.query.tab,
    (t) => {
      const v = validTab(t) || 'overview'
      if (v !== activeTab.value && !anyDirty()) activeTab.value = v
    },
)

// 监听 WebSocket 事件，实时更新实例运行状态
watch(
    () => getInstanceStatus(instanceName),
    (newStatus) => {
      if (newStatus && instanceData.value) {
        instanceData.value.running = newStatus.running
        instanceData.value.status = newStatus.status
      }
    },
)

function backMainPage() {
  router.replace({path: '/'})
}

// ---- 配置文件 ----
const loadGameIni = async () => {
  try {
    const data = await getGameIni(instanceName)
    if (data.success && data.data) {
      gameIniContent.value = data.data.content || ''
    } else {
      if (data.error.include('Game.ini not found for')) {
        return
      }
      MessagePlugin.error(data.error || '加载 Game.ini 失败')
    }
  } catch (err) {
    if (err.message.include('Game.ini not found for')) {
      return
    }
    MessagePlugin.error(err.message || '加载 Game.ini 失败')
  }
}

const loadGameUserSettings = async () => {
  try {
    const data = await getGameUserSettings(instanceName)
    if (data.success && data.data) {
      gameUserSettingsContent.value = data.data.content || ''
    } else {
      MessagePlugin.error(data.error || '加载 GameUserSettings.ini 失败')
    }
  } catch (err) {
    MessagePlugin.error(err.message || '加载 GameUserSettings.ini 失败')
  }
}

const saveGameIni = async (content) => {
  savingGameIni.value = true
  try {
    const data = await updateGameIni(instanceName, content)
    if (data.success) {
      MessagePlugin.success('Game.ini 已保存')
      await loadGameIni()
    } else {
      MessagePlugin.error(data.error || '保存 Game.ini 失败')
      throw new Error(data.error || '保存 Game.ini 失败')
    }
  } catch (err) {
    MessagePlugin.error(err.message || '保存 Game.ini 失败')
    throw err
  } finally {
    savingGameIni.value = false
  }
}

const saveGameUserSettings = async (content) => {
  savingGameUserSettings.value = true
  try {
    const data = await updateGameUserSettings(instanceName, content)
    if (data.success) {
      MessagePlugin.success('GameUserSettings.ini 已保存')
      await loadGameUserSettings()
    } else {
      MessagePlugin.error(data.error || '保存 GameUserSettings.ini 失败')
      throw new Error(data.error || '保存 GameUserSettings.ini 失败')
    }
  } catch (err) {
    MessagePlugin.error(err.message || '保存 GameUserSettings.ini 失败')
    throw err
  } finally {
    savingGameUserSettings.value = false
  }
}

// 服务器规则可视化配置：仅对发生变化的文件/字段写入
const saveAdvancedConfig = async ({gameIni, gameUserSettings, customStartParameters}) => {
  try {
    if (gameIni != null && gameIni !== gameIniContent.value) {
      await saveGameIni(gameIni)
    }
    if (gameUserSettings != null && gameUserSettings !== gameUserSettingsContent.value) {
      await saveGameUserSettings(gameUserSettings)
    }
    const currentParams = instanceData.value?.config?.CustomStartParameters || ''
    if (customStartParameters != null && customStartParameters !== currentParams) {
      const data = await updateInstanceConfig(instanceName, {CustomStartParameters: customStartParameters})
      if (data?.success && instanceData.value?.config) {
        instanceData.value.config.CustomStartParameters = customStartParameters
      }
    }
  } catch (err) {
    // 各 save 函数内已提示错误
  }
}

// 插件数据库在线快照周期（分钟）
const saveSnapshotInterval = async (minutes) => {
  try {
    const data = await updateInstanceConfig(instanceName, {PluginSnapshotInterval: minutes})
    if (data?.success && instanceData.value?.config) {
      instanceData.value.config.PluginSnapshotInterval = minutes
    }
  } catch (err) {
    MessagePlugin.error(`保存快照周期失败: ${err.message ?? err}`)
  }
}

const fetchInstanceConfig = async () => {
  error.value = null
  try {
    const data = await getInstanceConfig(instanceName)
    if (data.success && data.data) {
      instanceData.value = data.data || {}
    } else {
      error.value = data.error || '获取实例配置失败'
    }
  } catch (err) {
    error.value = err.message || '获取实例配置失败'
  }
}

const saveConfig = async (config) => {
  savingConfig.value = true
  try {
    const data = await updateInstanceConfig(instanceName, config)
    if (data.success) {
      MessagePlugin.success('配置已保存')
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

// ---- 生命周期操作 ----
const startInstance = () => {
  const startDialog = DialogPlugin.confirm({
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
            content: data.error || `实例 "${instanceName}" 启动失败`,
          })
        }
      } catch (err) {
        MessagePlugin.error(`启动实例失败: ${err.message}`)
      }
    },
  })
}

const stopInstance = async () => {
  const countdown = await countdownDialogRef.value?.open(instanceName, 'stop')
  if (!countdown) return
  try {
    const data = await stopServer(instanceName, countdown)
    if (data.success) {
      MessagePlugin.success(data.message || `实例 "${instanceName}" 正在停止`)
    } else {
      MessagePlugin.error(data.error || `实例 "${instanceName}" 停止失败`)
    }
  } catch (err) {
    MessagePlugin.error(`停止实例失败: ${err.message}`)
  }
}

const restartInstance = async () => {
  const countdown = await countdownDialogRef.value?.open(instanceName, 'restart')
  if (!countdown) return
  addRestartPending(instanceName)
  try {
    const data = await restartServer(instanceName, countdown)
    if (data.success) {
      MessagePlugin.success(data.message || '实例正在重启')
    } else {
      MessagePlugin.error(data.error || '重启实例失败')
    }
  } catch (err) {
    MessagePlugin.error('重启实例失败')
  }
}

const cancelCountdown = async () => {
  try {
    const data = await cancelServerCountdown(instanceName)
    if (data.success) {
      MessagePlugin.success(data.message || '倒计时已取消')
    } else {
      MessagePlugin.error(data.error || '取消倒计时失败')
    }
  } catch (err) {
    MessagePlugin.error(`取消倒计时失败: ${err.message}`)
  }
}

const forceStopInstance = () => {
  const dialog = DialogPlugin.confirm({
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
      } catch (err) {
        MessagePlugin.error(`强制停止失败: ${err.message}`)
      }
    },
  })
}

const fetchModInfo = async () => {
  try {
    const data = await getModInfo()
    modInfo.value = data.success ? data.data || [] : []
  } catch (err) {
    modInfo.value = []
  }
}

const fetchInstances = async () => {
  try {
    await initServer()
  } catch (err) {
    MessagePlugin.error('获取实例列表失败:' + err)
  }
}

onMounted(async () => {
  await fetchInstanceConfig()
  await fetchInstances()
  baseLoadedSuccessfully.value = true

  loadGameIni()
  loadGameUserSettings()
  fetchModInfo()

  const cachedStatus = getInstanceStatus(instanceName)
  if (cachedStatus) {
    instanceData.value = {...instanceData.value, ...cachedStatus}
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
  height: 100%;
  display: flex;
  flex-direction: column;

  :deep(.t-card__header) {
    flex: 0 0 auto;
  }

  :deep(.t-loading__parent) {
    flex: 1 1 auto;
    min-height: 0;
  }

  :deep(.t-card__body) {
    padding: var(--td-comp-paddingTB-l) var(--td-comp-paddingLR-xl);
    box-sizing: border-box;
    height: 100%;
  }
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

.tabs-container {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.detail-tabs {
  height: 100%;
  display: flex;
  flex-direction: column;

  :deep(.t-tabs__content) {
    flex: 1;
    min-height: 0;
  }

  :deep(.t-tab-panel) {
    height: 100%;
  }
}

.panel-scroll {
  height: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  padding-top: 12px;
  box-sizing: border-box;
}

.tab-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.dirty-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--td-error-color, #d54941);
  display: inline-block;
}

.log-viewer-card {
  height: 100%;
  display: flex;

  :deep(.t-card__body) {
    padding: 0;
    width: 100%;
  }

  :deep(.t-loading__parent) {
    flex: 1;
    min-height: 0;
    display: flex;
  }
}

.rcon-modal-content {
  width: 100%;
  height: 60vh;
  display: flex;
  flex-direction: column;
}
</style>
