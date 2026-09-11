<template>
  <div class="plugin-data-panel">
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
        v-if="loaded && !arkApiInstalled"
        :theme="enableAsaPlugin ? 'warning' : 'info'"
        :message="notInstalledMessage"
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
  </div>
</template>

<script setup>
import {computed, ref, watch} from 'vue'
import {MessagePlugin} from 'tdesign-vue-next'
import {HelpCircleIcon} from 'tdesign-icons-vue-next'
import ConfigEditor from '@/components/ConfigEditor.vue'
import PluginInstallDialog from '@/components/PluginInstallDialog.vue'
import PluginUninstallDialog from '@/components/PluginUninstallDialog.vue'
import {getPluginConfig, listInstancePlugins, setInstancePluginEnabled, updatePluginConfig} from '@/apis/api'
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

const notInstalledMessage = computed(() => (props.enableAsaPlugin
    ? '本实例已开启「启用ASA插件」，但 server-files 中没有安装 ArkApi 主程序（找不到 AsaApiLoader.exe），实例将以原版服务端启动。'
    : '未安装 ArkApi 主程序（server-files 中找不到 AsaApiLoader.exe）。'))

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

watch(() => props.instanceName, () => load(), {immediate: true})

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

defineExpose({reload: load})
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
