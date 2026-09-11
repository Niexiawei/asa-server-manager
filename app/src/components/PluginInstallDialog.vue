<template>
  <t-dialog
      v-model:visible="visible"
      :header="expect ? `更新插件 ${expect}` : '上传插件'"
      width="820px"
      :close-on-overlay-click="false"
  >
    <input ref="fileInput" type="file" accept=".zip" class="hidden-input" @change="onFileChosen"/>

    <div v-if="phase === 'pick' || phase === 'uploading'" class="pick">
      <p class="muted">
        选择 ArkApi 插件的 .zip 包（不超过 256 MiB）。上传后先校验，确认目标实例之后才会安装。
        <template v-if="expect">包里的插件必须是 <strong>{{ expect }}</strong>。</template>
      </p>
      <t-button :loading="phase === 'uploading'" @click="fileInput?.click()">选择 .zip 文件</t-button>
      <t-progress v-if="phase === 'uploading'" :percentage="progress" class="progress"/>
    </div>

    <template v-else-if="phase === 'report' || phase === 'applying'">
      <t-alert v-if="report.errors?.length" theme="error" title="安装包未通过校验">
        <template #message>
          <ul class="msg-list">
            <li v-for="(e, i) in report.errors" :key="i">{{ e }}</li>
          </ul>
        </template>
      </t-alert>

      <div v-else class="report">
        <div class="meta">
          <div>
            <span class="k">插件</span>{{ report.name }}
            <span v-if="report.full_name && report.full_name !== report.name" class="muted">（{{ report.full_name }}）</span>
          </div>
          <div><span class="k">版本</span>{{ report.version || '—' }}</div>
          <div v-if="report.min_api_version"><span class="k">最低 API 版本</span>{{ report.min_api_version }}</div>
          <div v-if="report.description"><span class="k">描述</span>{{ report.description }}</div>
        </div>

        <t-alert v-if="report.warnings?.length" theme="warning">
          <template #message>
            <ul class="msg-list">
              <li v-for="(w, i) in report.warnings" :key="i">{{ w }}</li>
            </ul>
          </template>
        </t-alert>

        <t-collapse borderless>
          <t-collapse-panel :header="`包内文件（${report.files?.length ?? 0}）`">
            <div v-for="f in report.files" :key="f.path" class="file-row">
              <span>{{ f.path }}</span>
              <span class="muted">{{ formatSize(f.size) }}</span>
            </div>
          </t-collapse-panel>
        </t-collapse>

        <div class="section-title">
          目标实例
          <span class="muted">默认只勾选当前实例，其他实例需要手动勾选；装完之后各实例的插件彼此独立。</span>
        </div>
        <t-table :data="report.targets" :columns="targetColumns" row-key="instance" size="small" bordered>
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
          <template #version="{ row }">
            <span v-if="row.installed">{{ row.installed_version || '未知' }} → {{ report.version || '未知' }}</span>
            <span v-else class="muted">未安装</span>
          </template>
          <template #action="{ row }">
            <span v-if="row.blocked" class="blocked">{{ row.blocked }}</span>
            <template v-else>
              <t-tag size="small" variant="light" :theme="row.action === 'update' ? 'warning' : 'success'">
                {{ row.action === 'update' ? '更新' : '新装' }}
              </t-tag>
              <div v-for="(w, i) in row.warnings || []" :key="i" class="row-warn">{{ w }}</div>
            </template>
          </template>
        </t-table>

        <t-checkbox v-if="restorable" v-model="restoreFromBackup">
          对「新装」且有上次卸载留下的备份的实例，从备份恢复配置与数据
        </t-checkbox>
      </div>
    </template>

    <plugin-result-list v-else-if="phase === 'result'" :results="results"/>

    <template #footer>
      <template v-if="phase === 'result'">
        <t-button theme="primary" @click="visible = false">完成</t-button>
      </template>
      <template v-else-if="phase === 'report' && report.errors?.length">
        <t-button variant="outline" @click="fileInput?.click()">重新选择</t-button>
        <t-button theme="default" @click="visible = false">关闭</t-button>
      </template>
      <template v-else-if="phase === 'report' || phase === 'applying'">
        <t-button theme="default" :disabled="phase === 'applying'" @click="visible = false">取消</t-button>
        <t-button
            theme="primary"
            :disabled="selected.length === 0"
            :loading="phase === 'applying'"
            @click="apply"
        >
          确认安装（{{ selected.length }} 个实例）
        </t-button>
      </template>
      <template v-else>
        <t-button theme="default" @click="visible = false">取消</t-button>
      </template>
    </template>
  </t-dialog>
</template>

<script setup>
import {computed, ref, watch} from 'vue'
import {MessagePlugin} from 'tdesign-vue-next'
import PluginResultList from '@/components/PluginResultList.vue'
import {applyArkApiPackage, discardArkApiPackage, uploadArkApiPackage} from '@/apis/api'

// 插件包的两段式安装：选择 zip → 上传并校验 → 看报告、勾选目标实例 → 确认。
// 目标实例只默认勾选当前实例（docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §1.1），其他实例要手动勾选。
const visible = defineModel('visible', {type: Boolean, default: false})

const props = defineProps({
  // 当前实例：目标表里唯一默认勾选的一行
  instanceName: {type: String, required: true},
  // 从某一行点「更新」时的插件名，服务端要求包里的插件与之相同
  expect: {type: String, default: ''}
})

const emit = defineEmits(['done'])

const fileInput = ref(null)
// pick → uploading → report → applying → result
const phase = ref('pick')
const progress = ref(0)
const report = ref({})
const selected = ref([])
const restoreFromBackup = ref(true)
const results = ref([])
// 已 apply 过的 token 服务端已经删掉了，关闭时不必再丢弃
let applied = false

const targetColumns = [
  {colKey: 'pick', title: '', width: 48},
  {colKey: 'instance', title: '实例', width: 180},
  {colKey: 'version', title: '版本'},
  {colKey: 'action', title: '动作'}
]

const restorable = computed(() => (report.value.targets ?? []).some(
    t => selected.value.includes(t.instance) && t.action === 'install' && t.backup_available))

const reset = () => {
  phase.value = 'pick'
  progress.value = 0
  report.value = {}
  selected.value = []
  restoreFromBackup.value = true
  results.value = []
  applied = false
}

watch(visible, (v) => {
  if (v) {
    reset()
    return
  }
  if (report.value.token && !applied) {
    discardArkApiPackage(report.value.token).catch(() => {
    })
  }
  if (applied) emit('done')
})

const onFileChosen = async (e) => {
  const file = e.target.files?.[0]
  e.target.value = '' // 允许重复选同一个文件
  if (!file) return
  if (report.value.token) {
    discardArkApiPackage(report.value.token).catch(() => {
    })
  }
  report.value = {}
  phase.value = 'uploading'
  progress.value = 0
  try {
    const res = await uploadArkApiPackage('plugin', file, {
      expect: props.expect || undefined,
      onProgress: (p) => (progress.value = p)
    })
    report.value = res.data ?? {}
    selected.value = (report.value.targets ?? [])
        .filter(t => t.instance === props.instanceName && !t.blocked)
        .map(t => t.instance)
    phase.value = 'report'
  } catch (err) {
    if (err.data) {
      // 422：包没通过校验，data 是逐条错误
      report.value = err.data
      phase.value = 'report'
    } else {
      MessagePlugin.error(`上传失败: ${err.message ?? err}`)
      phase.value = 'pick'
    }
  }
}

const toggle = (instance, checked) => {
  selected.value = checked
      ? [...selected.value, instance]
      : selected.value.filter(n => n !== instance)
}

const apply = async () => {
  phase.value = 'applying'
  try {
    const res = await applyArkApiPackage(report.value.token, {
      targets: selected.value,
      restore_from_backup: restorable.value && restoreFromBackup.value
    })
    applied = true
    results.value = res.data?.results ?? []
    phase.value = 'result'
    if (res.success) {
      MessagePlugin.success(res.message || '安装完成')
    } else {
      MessagePlugin.warning(res.message || '部分实例安装失败')
    }
  } catch (err) {
    // 典型是暂存过期：token 已经没了，只能重新上传
    MessagePlugin.error(`安装失败: ${err.message ?? err}`)
    report.value = {}
    phase.value = 'pick'
  }
}

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
</script>

<style scoped>
.hidden-input {
  display: none;
}

.pick {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 12px;
}

.progress {
  width: 100%;
}

.report {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.meta {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.meta .k {
  display: inline-block;
  min-width: 96px;
  color: var(--td-text-color-secondary);
}

.msg-list {
  margin: 0;
  padding-left: 18px;
}

.file-row {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
  font-family: monospace;
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

.row-warn {
  font-size: 12px;
  color: var(--td-warning-color);
}
</style>
