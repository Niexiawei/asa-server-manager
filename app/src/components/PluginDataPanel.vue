<template>
  <div class="plugin-data-panel">
    <t-alert
        v-if="!loading && plugins.length === 0"
        theme="info"
        message="未检测到 ArkApi 插件（server-files 下没有 ArkApi/Plugins 目录）。"
    />

    <template v-else>
      <t-alert theme="info" class="panel-hint">
        <template #message>
          插件的配置与运行期数据（如 Permissions 的权限库）按实例独立存放，
          启动前注入服务端目录、停止后收回。<strong>在这里保存的配置会在下次启动该实例时生效。</strong>
        </template>
      </t-alert>

      <div class="snapshot-interval-row">
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
            content="服务器崩溃或断电时回收来不及执行，快照把最坏损失收窄到一个周期。0 = 默认 5 分钟，-1 = 关闭。"
        >
          <HelpCircleIcon class="hint-icon"/>
        </t-tooltip>
      </div>

      <t-table
          :data="plugins"
          :columns="columns"
          row-key="name"
          size="small"
          :loading="loading"
          bordered
      >
        <template #isolated="{ row }">
          <t-tag v-if="row.external_db_path" theme="warning" variant="light">用户接管</t-tag>
          <t-tag v-else-if="row.isolated" theme="success" variant="light">已隔离</t-tag>
          <t-tag v-else theme="default" variant="light">尚未启动过</t-tag>
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
        </template>
      </t-table>

      <t-alert
          v-for="row in externalPlugins"
          :key="row.name"
          theme="warning"
          class="panel-hint"
          :message="`${row.name} 的数据库路径已由你手工设为 ${row.external_db_path}，管理器不再为它做隔离、回收与快照。`"
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
  </div>
</template>

<script setup>
import {computed, ref, watch} from 'vue'
import {MessagePlugin} from 'tdesign-vue-next'
import {HelpCircleIcon} from 'tdesign-icons-vue-next'
import ConfigEditor from '@/components/ConfigEditor.vue'
import {getPluginConfig, listInstancePlugins, updatePluginConfig} from '@/apis/api'

const props = defineProps({
  instanceName: {type: String, required: true},
  // 来自实例配置的 PluginSnapshotInterval（分钟）
  interval: {type: Number, default: 0}
})

const emit = defineEmits(['update:interval'])

const loading = ref(false)
const saving = ref(false)
const plugins = ref([])
const snapshotInterval = ref(props.interval)

const editorVisible = ref(false)
const editingPlugin = ref('')
const editingContent = ref('')
const editingSeeded = ref(true)
const openingPlugin = ref('')

watch(() => props.interval, (v) => {
  snapshotInterval.value = v
})

const columns = [
  {colKey: 'name', title: '插件', width: 200},
  {colKey: 'isolated', title: '状态', width: 130},
  {colKey: 'data_files', title: '实例数据', width: 160},
  {colKey: 'snapshots', title: '最近快照', width: 180},
  {colKey: 'op', title: '操作', width: 110}
]

const externalPlugins = computed(() => plugins.value.filter(p => p.external_db_path))

const load = async () => {
  if (!props.instanceName) return
  loading.value = true
  try {
    const res = await listInstancePlugins(props.instanceName)
    plugins.value = res.data?.plugins ?? []
  } catch (e) {
    MessagePlugin.error(`加载插件列表失败: ${e.message ?? e}`)
  } finally {
    loading.value = false
  }
}

watch(() => props.instanceName, () => load(), {immediate: true})

const openConfig = async (row) => {
  openingPlugin.value = row.name
  try {
    const res = await getPluginConfig(props.instanceName, row.name)
    editingContent.value = res.data?.content ?? ''
    editingSeeded.value = res.data?.seeded ?? true
    editingPlugin.value = row.name
    editorVisible.value = true
    if (!editingSeeded.value) {
      // 实例侧还没有独立配置，这里展示的是源服务端自带的默认值
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

.snapshot-interval-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.snapshot-interval-row .label {
  font-size: 13px;
}

.snapshot-interval-row .unit {
  font-size: 13px;
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
