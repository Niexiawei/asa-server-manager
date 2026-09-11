<template>
  <t-dialog
      v-model:visible="visible"
      :header="`卸载插件 ${plugin}`"
      width="720px"
      :close-on-overlay-click="false"
  >
    <plugin-result-list v-if="phase === 'result'" :results="results"/>

    <div v-else class="body">
      <p class="muted">
        卸载会把插件目录整个移入该实例的 ArkApi/Backups/，配置与数据随之保留，以后重新安装时可以从备份恢复。
      </p>
      <div class="section-title">
        装有该插件的实例
        <span class="muted">默认只勾选当前实例，其他实例需要手动勾选。</span>
      </div>
      <t-table
          :data="instances"
          :columns="columns"
          :loading="loading"
          row-key="instance"
          size="small"
          bordered
      >
        <template #pick="{ row }">
          <t-checkbox
              :checked="selected.includes(row.instance)"
              :disabled="!!row.blocked || phase === 'applying'"
              @change="(v) => toggle(row.instance, v)"
          />
        </template>
        <template #instance="{ row }">
          {{ row.instance }}
          <t-tag v-if="row.instance === instanceName" size="small" variant="light" theme="primary">当前</t-tag>
        </template>
        <template #version="{ row }">{{ row.installed_version || '未知' }}</template>
        <template #state="{ row }">
          <span v-if="row.blocked" class="blocked">{{ row.blocked }}</span>
          <span v-else>{{ row.enabled ? '启用' : '已禁用' }}</span>
        </template>
      </t-table>
    </div>

    <template #footer>
      <t-button v-if="phase === 'result'" theme="primary" @click="visible = false">完成</t-button>
      <template v-else>
        <t-button theme="default" :disabled="phase === 'applying'" @click="visible = false">取消</t-button>
        <t-button
            theme="danger"
            :disabled="selected.length === 0"
            :loading="phase === 'applying'"
            @click="confirm"
        >
          确认卸载（{{ selected.length }} 个实例）
        </t-button>
      </template>
    </template>
  </t-dialog>
</template>

<script setup>
import {ref, watch} from 'vue'
import {MessagePlugin} from 'tdesign-vue-next'
import PluginResultList from '@/components/PluginResultList.vue'
import {getPluginInstances, uninstallPlugin} from '@/apis/api'

// 从当前实例 + 手动勾选的其他实例卸载一个插件（docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §6.4）。
const visible = defineModel('visible', {type: Boolean, default: false})

const props = defineProps({
  instanceName: {type: String, required: true},
  plugin: {type: String, default: ''}
})

const emit = defineEmits(['done'])

// pick → applying → result
const phase = ref('pick')
const loading = ref(false)
const instances = ref([])
const selected = ref([])
const results = ref([])

const columns = [
  {colKey: 'pick', title: '', width: 48},
  {colKey: 'instance', title: '实例', width: 200},
  {colKey: 'version', title: '已装版本', width: 110},
  {colKey: 'state', title: '状态'}
]

watch(visible, async (v) => {
  if (!v) {
    if (phase.value === 'result') emit('done')
    return
  }
  phase.value = 'pick'
  instances.value = []
  selected.value = []
  results.value = []
  loading.value = true
  try {
    const res = await getPluginInstances(props.plugin)
    instances.value = res.data?.instances ?? []
    selected.value = instances.value
        .filter(t => t.instance === props.instanceName && !t.blocked)
        .map(t => t.instance)
  } catch (err) {
    MessagePlugin.error(`读取插件安装情况失败: ${err.message ?? err}`)
  } finally {
    loading.value = false
  }
})

const toggle = (instance, checked) => {
  selected.value = checked
      ? [...selected.value, instance]
      : selected.value.filter(n => n !== instance)
}

const confirm = async () => {
  phase.value = 'applying'
  try {
    const res = await uninstallPlugin(props.plugin, selected.value)
    results.value = res.data?.results ?? []
    phase.value = 'result'
    if (res.success) {
      MessagePlugin.success(res.message || '卸载完成')
    } else {
      MessagePlugin.warning(res.message || '部分实例卸载失败')
    }
  } catch (err) {
    MessagePlugin.error(`卸载失败: ${err.message ?? err}`)
    phase.value = 'pick'
  }
}
</script>

<style scoped>
.body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.section-title {
  font-weight: 500;
}

.section-title .muted {
  font-weight: normal;
  font-size: 12px;
  margin-left: 8px;
}

.muted {
  color: var(--td-text-color-secondary);
}

.blocked {
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}
</style>
