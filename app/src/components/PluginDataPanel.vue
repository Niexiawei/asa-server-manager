<template>
  <div class="plugin-data-panel">
    <div class="core-card">
      <div class="core-row">
        <span class="core-title">ArkApi 主程序</span>
        <span class="muted small">全局一份，所有实例共用</span>
        <template v-if="coreLoaded">
          <t-tag v-if="core.installed" theme="success" variant="light" size="small">已安装</t-tag>
          <t-tag v-else variant="light" size="small">未安装</t-tag>
          <span v-if="core.installed">{{ core.version || '版本未知' }}</span>
          <span class="muted small">{{ coreSourceText }}</span>
        </template>
        <span class="spacer"/>
        <template v-if="canManage && coreLoaded">
          <t-button
              size="small"
              theme="primary"
              variant="outline"
              :disabled="core.busy"
              @click="coreDialogVisible = true"
          >
            {{ core.installed ? '上传更新' : '上传主程序' }}
          </t-button>
          <t-button
              v-if="core.installed || core.managed"
              size="small"
              theme="danger"
              variant="outline"
              :disabled="core.busy"
              :loading="uninstallingCore"
              @click="confirmUninstallCore"
          >
            卸载
          </t-button>
        </template>
      </div>
      <div v-if="core.busy" class="core-note">
        server-files 正在被改写（Steam 更新或另一个主程序操作），完成之后才能操作主程序。
      </div>
      <div v-if="coreChangedText" class="core-note">{{ coreChangedText }}</div>
    </div>

    <div class="instance-settings">
      <div class="setting-row">
        <span class="label">启用ASA插件</span>
        <t-switch
            :value="enableAsaPlugin"
            :loading="savingEnable"
            @change="(v) => emit('update:enableAsaPlugin', v)"
        />
        <t-tooltip content="开启后该实例经 AsaApiLoader.exe 启动并加载 ArkApi 插件；关闭则以原版服务端启动。">
          <HelpCircleIcon class="hint-icon"/>
        </t-tooltip>
      </div>

      <div class="setting-row">
        <span class="label">数据库在线快照周期</span>
        <t-input-number
            v-model="snapshotInterval"
            :min="-1"
            :max="1440"
            theme="column"
            style="width: 140px"
            @change="emitIntervalChange"
        />
        <span class="unit">分钟</span>
        <t-tooltip
            content="定时为插件的 SQLite 数据库做一份一致的在线副本，插件出错或断电把库写坏时可用它恢复。0 = 默认 5 分钟，-1 = 关闭。"
        >
          <HelpCircleIcon class="hint-icon"/>
        </t-tooltip>
      </div>

      <div v-if="running" class="running-hint">
        实例运行中：以上设置和插件的启用开关在下次启动时生效；安装、更新、卸载插件需要先停止实例。
      </div>
    </div>

    <t-alert
        v-if="loaded && !arkApiInstalled && enableAsaPlugin"
        theme="warning"
        message="本实例已开启「启用ASA插件」，但没有安装 ArkApi 主程序（server-files 中找不到 AsaApiLoader.exe），实例将以原版服务端启动。"
    />

    <t-alert
        v-if="loaded && layout === 'legacy'"
        theme="warning"
        message="该实例仍在使用旧的全局插件（升级时它正在运行）。停止后，下次启动前会自动迁移为本实例独立的插件目录；在此之前这里只读，显示的是全局插件。"
    />

    <div v-if="loaded" class="toolbar">
      <span class="title">本实例的插件（{{ plugins.length }}）</span>
      <t-tooltip v-if="canManage" :content="uploadDisabledReason" :disabled="!uploadDisabledReason">
        <span>
          <t-button size="small" theme="primary" :disabled="!!uploadDisabledReason" @click="openInstall('')">
            上传插件
          </t-button>
        </span>
      </t-tooltip>
    </div>

    <t-alert
        v-if="loaded && arkApiInstalled && plugins.length === 0"
        theme="info"
        :message="emptyMessage"
    />

    <template v-if="plugins.length > 0">
      <t-alert v-if="layout === 'instance'" theme="info" class="panel-hint">
        <template #message>
          插件文件、配置与运行期数据都存放在本实例的独立目录（{{ pluginsDir }}），各实例互不影响。
          <strong>在这里保存的配置会在下次启动该实例时生效。</strong>
        </template>
      </t-alert>

      <t-table
          :data="plugins"
          :columns="columns"
          row-key="name"
          size="small"
          :loading="loading"
          bordered
      >
        <template #name="{ row }">
          <div class="plugin-name">
            <span>{{ row.name }}</span>
            <t-tooltip
                v-if="row.dll_missing"
                :content="`插件目录里没有 ${row.name}.dll，ArkApi 不会加载这个插件`"
            >
              <t-tag theme="danger" variant="light" size="small">文件不完整</t-tag>
            </t-tooltip>
          </div>
          <div v-if="row.full_name && row.full_name !== row.name" class="full-name">{{ row.full_name }}</div>
        </template>

        <template #version="{ row }">
          <span v-if="row.version">{{ row.version }}</span>
          <span v-else class="muted">—</span>
        </template>

        <template #description="{ row }">
          <span v-if="row.description">{{ row.description }}</span>
          <span v-else class="muted">—</span>
        </template>

        <template #enabled="{ row }">
          <div class="enabled-cell">
            <t-switch
                size="small"
                :value="row.enabled"
                :loading="togglingPlugin === row.name"
                :disabled="layout !== 'instance'"
                @change="(v) => toggleEnabled(row, v)"
            />
            <t-tooltip v-if="row.pending" content="实例运行中改的，下次启动该实例时生效">
              <t-tag theme="warning" variant="light" size="small">待生效</t-tag>
            </t-tooltip>
          </div>
        </template>

        <template #data_files="{ row }">
          <span v-if="row.external_db_path" class="muted">—</span>
          <span v-else-if="!row.data_files.length" class="muted">无</span>
          <t-tooltip v-else :content="fileListText(row.data_files)">
            <span>{{ row.data_files.length }} 个 / {{ formatSize(totalSize(row.data_files)) }}</span>
          </t-tooltip>
        </template>

        <template #snapshots="{ row }">
          <span v-if="!row.snapshots.length" class="muted">无</span>
          <t-tooltip v-else :content="fileListText(row.snapshots)">
            <span>{{ formatTime(latestTime(row.snapshots)) }}</span>
          </t-tooltip>
        </template>

        <template #op="{ row }">
          <t-button
              size="small"
              variant="text"
              theme="primary"
              :disabled="!row.has_config"
              :loading="openingPlugin === row.name"
              @click="openConfig(row)"
          >
            编辑配置
          </t-button>
          <template v-if="canManage">
            <t-tooltip :content="writeDisabledReason" :disabled="!writeDisabledReason">
              <span>
                <t-button
                    size="small"
                    variant="text"
                    theme="primary"
                    :disabled="!!writeDisabledReason"
                    @click="openInstall(row.name)"
                >
                  更新
                </t-button>
              </span>
            </t-tooltip>
            <t-tooltip :content="writeDisabledReason" :disabled="!writeDisabledReason">
              <span>
                <t-button
                    size="small"
                    variant="text"
                    theme="danger"
                    :disabled="!!writeDisabledReason"
                    @click="openUninstall(row)"
                >
                  卸载
                </t-button>
              </span>
            </t-tooltip>
          </template>
        </template>
      </t-table>

      <t-alert
          v-for="row in externalPlugins"
          :key="row.name"
          theme="warning"
          class="panel-hint"
          :message="`${row.name} 的数据库路径已由你手工设为 ${row.external_db_path}，它不在插件目录里，管理器不再为它做快照。`"
      />
    </template>

    <config-editor
        v-model:visible="editorVisible"
        :title="`${editingPlugin} — config.json`"
        :content="editingContent"
        language="json"
        :saving="saving"
        mode="modal"
        width="65vw"
        @save="saveConfig"
    />

    <plugin-install-dialog
        v-model:visible="installVisible"
        :instance-name="instanceName"
        :expect="installExpect"
        @done="load"
    />

    <plugin-uninstall-dialog
        v-model:visible="uninstallVisible"
        :instance-name="instanceName"
        :plugin="uninstallTarget"
        @done="load"
    />

    <ark-api-core-dialog v-model:visible="coreDialogVisible" :current="core" @done="refreshAll"/>
  </div>
</template>

<script setup>
import {computed, ref, watch} from 'vue'
import {DialogPlugin, MessagePlugin} from 'tdesign-vue-next'
import {HelpCircleIcon} from 'tdesign-icons-vue-next'
import ConfigEditor from '@/components/ConfigEditor.vue'
import PluginInstallDialog from '@/components/PluginInstallDialog.vue'
import PluginUninstallDialog from '@/components/PluginUninstallDialog.vue'
import ArkApiCoreDialog from '@/components/ArkApiCoreDialog.vue'
import {
  getArkApiStatus,
  getPluginConfig,
  listInstancePlugins,
  setInstancePluginEnabled,
  uninstallArkApi,
  updatePluginConfig
} from '@/apis/api'
import {authState, isAdmin} from '@/store/authStore.js'

const props = defineProps({
  instanceName: {type: String, required: true},
  // 来自实例配置的 PluginSnapshotInterval（分钟）
  interval: {type: Number, default: 0},
  // 来自实例配置的 EnableAsaPlugin。开关是受控的：父组件保存成功后回写，
  // 这里才跟着变——保存失败时开关停在原位，不会显示一个没生效的状态。
  enableAsaPlugin: {type: Boolean, default: false},
  savingEnable: {type: Boolean, default: false},
  running: {type: Boolean, default: false}
})

const emit = defineEmits(['update:interval', 'update:enableAsaPlugin'])

const loading = ref(false)
// 首次加载成功之前不下「没装主程序 / 没有插件」的结论，免得一闪而过的误报
const loaded = ref(false)
const saving = ref(false)
const plugins = ref([])
const arkApiInstalled = ref(true)
// instance：本实例独立的插件目录；legacy：升级时实例正在运行、尚未迁移，列的是全局插件
const layout = ref('instance')
const pluginsDir = ref('')
const snapshotInterval = ref(props.interval)

const editorVisible = ref(false)
const editingPlugin = ref('')
const editingContent = ref('')
const editingSeeded = ref(true)
const openingPlugin = ref('')
const togglingPlugin = ref('')

const installVisible = ref(false)
const installExpect = ref('')
const uninstallVisible = ref(false)
const uninstallTarget = ref('')

// ArkApi 主程序（全局）的状态，来自 GET /api/arkapi
const core = ref({})
const coreLoaded = ref(false)
const coreDialogVisible = ref(false)
const uninstallingCore = ref(false)

watch(() => props.interval, (v) => {
  snapshotInterval.value = v
})

// 往服务器上放 dll 等同于在服务器上执行代码，安装/更新/卸载只给管理员（服务端同样校验）。
// 没开鉴权时服务端不拦，这里也不隐藏。
const canManage = computed(() => !authState.authEnabled || isAdmin.value)

const uploadDisabledReason = computed(() => {
  if (!arkApiInstalled.value) return '安装 ArkApi 主程序后才能添加插件'
  if (layout.value !== 'instance') return '该实例尚未迁移到独立插件目录，停止后下次启动时会自动迁移'
  return ''
})

// 运行中的 ArkApi 占着插件 dll，覆盖、删除都会失败（方案 D2）
const writeDisabledReason = computed(() => {
  if (layout.value !== 'instance') return '该实例尚未迁移到独立插件目录'
  if (props.running) return '实例运行中，请先停止实例'
  return ''
})

const columns = [
  {colKey: 'name', title: '插件', width: 200},
  {colKey: 'version', title: '版本', width: 80},
  {colKey: 'description', title: '描述', ellipsis: true},
  {colKey: 'enabled', title: '启用', width: 110},
  {colKey: 'data_files', title: '实例数据', width: 130},
  {colKey: 'snapshots', title: '最近快照', width: 160},
  {colKey: 'op', title: '操作', width: 210}
]

const externalPlugins = computed(() => plugins.value.filter(p => p.external_db_path))

const coreSourceText = computed(() => {
  const c = core.value
  if (c.installed && c.managed) return `本程序安装 · ${formatTime(c.installed_at)}`
  if (c.installed) return '手工安装（不是通过本面板安装的，版本未知）'
  if (c.managed) return '安装记录还在，但 server-files 中找不到 AsaApiLoader.exe'
  return 'server-files 中没有 AsaApiLoader.exe'
})

// 清单里、却在安装之后被外部改动或删掉的文件（例如 Steam 校验换回了游戏自带的 msvcp140.dll）
const coreChangedText = computed(() => {
  const modified = core.value.modified_files ?? []
  const missing = core.value.missing_files ?? []
  if (!modified.length && !missing.length) return ''
  const parts = []
  if (modified.length) parts.push(`被改动：${modified.join('、')}`)
  if (missing.length) parts.push(`被删掉：${missing.join('、')}`)
  return `主程序安装之后有文件被外部改动过（${parts.join('；')}）。重新上传同一个主程序包即可恢复。`
})

const emptyMessage = computed(() => {
  if (layout.value !== 'instance') return '未检测到 ArkApi 插件（ArkApi/Plugins 目录下没有插件）。'
  const how = canManage.value ? '点击「上传插件」添加，或' : ''
  return `本实例还没有 ArkApi 插件。${how}手工把插件目录放进 ${pluginsDir.value}。`
})

const load = async () => {
  if (!props.instanceName) return
  loading.value = true
  try {
    const res = await listInstancePlugins(props.instanceName)
    plugins.value = res.data?.plugins ?? []
    arkApiInstalled.value = res.data?.arkapi_installed ?? true
    layout.value = res.data?.layout ?? 'instance'
    pluginsDir.value = res.data?.plugins_dir ?? ''
    loaded.value = true
  } catch (e) {
    MessagePlugin.error(`加载插件列表失败: ${e.message ?? e}`)
  } finally {
    loading.value = false
  }
}

const loadCore = async () => {
  try {
    const res = await getArkApiStatus()
    core.value = res.data ?? {}
    coreLoaded.value = true
  } catch (e) {
    MessagePlugin.error(`读取 ArkApi 主程序状态失败: ${e.message ?? e}`)
  }
}

// 主程序装卸之后两边都要刷新：插件列表里的 arkapi_installed 决定能不能上传插件
const refreshAll = () => Promise.all([load(), loadCore()])

watch(() => props.instanceName, () => refreshAll(), {immediate: true})

// 启用/禁用只作用于本实例（方案 §1.1）。运行中只改配置，列表里会标「待生效」
const toggleEnabled = async (row, enabled) => {
  togglingPlugin.value = row.name
  try {
    const res = await setInstancePluginEnabled(props.instanceName, row.name, enabled)
    MessagePlugin.success(res.message || '已保存')
    await load()
  } catch (e) {
    MessagePlugin.error(`切换插件状态失败: ${e.message ?? e}`)
  } finally {
    togglingPlugin.value = ''
  }
}

const openInstall = (expect) => {
  installExpect.value = expect
  installVisible.value = true
}

const openUninstall = (row) => {
  uninstallTarget.value = row.name
  uninstallVisible.value = true
}

// 主程序全局一份：卸载影响所有实例，确认框里要写明（方案 §9.1）
const confirmUninstallCore = () => {
  const dialog = DialogPlugin.confirm({
    header: '卸载 ArkApi 主程序',
    body: '影响所有实例：开启了「启用ASA插件」的实例下次启动将以原版服务端启动，运行中的实例不受影响。' +
        '各实例的插件、配置与数据都不会改动，重新安装主程序后原样可用。被移除的文件进入备份，被覆盖过的游戏文件会还原。',
    confirmBtn: {content: '确认卸载', theme: 'danger'},
    cancelBtn: '取消',
    onConfirm: async () => {
      dialog.hide()
      uninstallingCore.value = true
      try {
        const res = await uninstallArkApi()
        MessagePlugin.success(res.message || 'ArkApi 主程序已卸载')
        for (const w of res.data?.warnings ?? []) {
          MessagePlugin.warning(w, 10000)
        }
      } catch (e) {
        MessagePlugin.error(`卸载主程序失败: ${e.message ?? e}`)
      } finally {
        uninstallingCore.value = false
        await refreshAll()
      }
    }
  })
}

const openConfig = async (row) => {
  openingPlugin.value = row.name
  try {
    const res = await getPluginConfig(props.instanceName, row.name)
    editingContent.value = res.data?.content ?? ''
    editingSeeded.value = res.data?.seeded ?? true
    editingPlugin.value = row.name
    editorVisible.value = true
    if (!editingSeeded.value) {
      // 旧布局下实例侧还没有独立配置，这里展示的是源服务端自带的默认值
      MessagePlugin.info('该实例还没有独立的插件配置，当前显示的是默认值，保存后才会成为本实例的配置')
    }
  } catch (e) {
    MessagePlugin.error(`读取插件配置失败: ${e.message ?? e}`)
  } finally {
    openingPlugin.value = ''
  }
}

const saveConfig = async (content) => {
  saving.value = true
  try {
    await updatePluginConfig(props.instanceName, editingPlugin.value, content)
    MessagePlugin.success('插件配置已保存，将在下次启动该实例时生效')
    await load()
  } catch (e) {
    MessagePlugin.error(`保存插件配置失败: ${e.message ?? e}`)
  } finally {
    saving.value = false
  }
}

const emitIntervalChange = (v) => {
  emit('update:interval', v ?? 0)
}

const totalSize = (files) => files.reduce((sum, f) => sum + (f.size ?? 0), 0)

const latestTime = (files) =>
    files.reduce((latest, f) => (!latest || f.modified > latest ? f.modified : latest), '')

const formatSize = (bytes) => {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let n = bytes
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

const formatTime = (iso) => (iso ? new Date(iso).toLocaleString() : '无')

const fileListText = (files) =>
    files.map(f => `${f.name} (${formatSize(f.size)})`).join('\n')

defineExpose({reload: refreshAll})
</script>

<style scoped>
.plugin-data-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.panel-hint {
  margin: 0;
}

.core-card {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 12px 16px;
  border: 1px solid var(--td-component-border, #dcdcdc);
  border-radius: 8px;
  background: var(--td-bg-color-container, #fff);
}

.core-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.core-title {
  font-weight: 500;
}

.core-row .spacer {
  flex: 1;
}

.core-note {
  font-size: 13px;
  color: var(--td-warning-color, #e37318);
}

.small {
  font-size: 12px;
}

.instance-settings {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px 16px;
  border: 1px solid var(--td-component-border, #dcdcdc);
  border-radius: 8px;
  background: var(--td-bg-color-container, #fff);
}

.setting-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.setting-row .label {
  min-width: 132px;
  font-size: 13px;
}

.setting-row .unit {
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.running-hint {
  font-size: 13px;
  color: var(--td-warning-color, #e37318);
}

.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.toolbar .title {
  font-weight: 500;
}

.plugin-name,
.enabled-cell {
  display: flex;
  align-items: center;
  gap: 6px;
}

.full-name {
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.hint-icon {
  cursor: help;
  color: var(--td-text-color-placeholder);
}

.muted {
  color: var(--td-text-color-placeholder);
}
</style>
