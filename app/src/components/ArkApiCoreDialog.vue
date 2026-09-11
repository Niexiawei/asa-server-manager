<template>
  <t-dialog
      v-model:visible="visible"
      :header="installed ? '更新 ArkApi 主程序' : '安装 ArkApi 主程序'"
      width="860px"
      :close-on-overlay-click="false"
  >
    <input ref="fileInput" type="file" accept=".zip" class="hidden-input" @change="onFileChosen"/>

    <div v-if="phase === 'pick' || phase === 'uploading'" class="pick">
      <p class="muted">
        选择 ArkApi 主程序的 .zip 包（例如 AsaApi_2.03.zip，不超过 256 MiB）。上传后先校验，确认之后才会安装。
      </p>
      <t-alert theme="warning" :message="scopeNotice"/>
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
            <span class="k">动作</span>{{ report.action === 'update' ? '更新' : '新装' }}
            <span v-if="report.action === 'update'" class="muted">（当前：{{ currentText }}）</span>
          </div>
          <div class="version-row">
            <span class="k">版本号</span>
            <t-input
                v-model="version"
                size="small"
                class="version-input"
                placeholder="未知"
                :maxlength="64"
                :disabled="phase === 'applying'"
            />
            <span class="muted">从文件名提取，可以修改。主程序文件里没有版本信息，这里填的就是面板上显示的版本。</span>
          </div>
        </div>

        <t-alert theme="warning" :message="scopeNotice"/>

        <t-alert v-if="report.overwrites?.length" theme="info">
          <template #message>
            以下游戏自带的文件会被覆盖：<strong>{{ report.overwrites.join('、') }}</strong>。
            原件会先备份，卸载主程序时还原。
          </template>
        </t-alert>

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

        <template v-if="report.bundled?.length">
          <div class="section-title">
            附带插件
            <span class="muted">
              附带插件不会装进 server-files。需要的话，选择要装进哪些实例（默认都不装）；装完之后各实例的插件彼此独立。
            </span>
          </div>
          <t-table :data="report.bundled" :columns="bundledColumns" row-key="name" size="small" bordered>
            <template #name="{ row }">
              <div>{{ row.name }} <span class="muted">{{ row.version || '' }}</span></div>
              <div v-if="row.full_name && row.full_name !== row.name" class="muted small">{{ row.full_name }}</div>
            </template>
            <template #targets="{ row }">
              <div v-if="row.errors?.length" class="blocked">未通过校验：{{ row.errors[0] }}</div>
              <t-select
                  v-else
                  v-model="bundledSel[row.name]"
                  :options="targetOptions(row.name)"
                  multiple
                  clearable
                  size="small"
                  placeholder="不安装"
                  :min-collapsed-num="3"
                  :disabled="phase === 'applying'"
              />
            </template>
          </t-table>
          <t-checkbox v-if="restorable" v-model="restoreFromBackup">
            对「新装」且有上次卸载留下的备份的实例，从备份恢复配置与数据
          </t-checkbox>
        </template>
      </div>
    </template>

    <div v-else-if="phase === 'result'" class="report">
      <t-alert :theme="resultOk ? 'success' : 'warning'" :message="resultMessage"/>
      <t-alert v-if="result.warnings?.length" theme="warning">
        <template #message>
          <ul class="msg-list">
            <li v-for="(w, i) in result.warnings" :key="i">{{ w }}</li>
          </ul>
        </template>
      </t-alert>
      <div v-if="result.backup" class="muted small">被换下来的旧文件在：{{ result.backup }}</div>
      <template v-if="result.results?.length">
        <div class="section-title">附带插件</div>
        <plugin-result-list :results="result.results"/>
      </template>
    </div>

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
        <t-button theme="primary" :loading="phase === 'applying'" @click="apply">
          确认{{ report.action === 'update' ? '更新' : '安装' }}主程序
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

// ArkApi 主程序的两段式安装 / 更新：选择 zip → 上传并校验 → 看报告、改版本号、挑附带插件的目标实例 → 确认。
// 主程序全局一份（docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §6.1）；附带插件默认一个实例都不装。
const visible = defineModel('visible', {type: Boolean, default: false})

const props = defineProps({
  // 当前的主程序状态（GET /api/arkapi），决定标题与「当前版本」
  current: {type: Object, default: () => ({})}
})

const emit = defineEmits(['done'])

const scopeNotice = '主程序全局一份、所有实例共用：影响所有开启了「启用ASA插件」的实例。运行中的实例不受影响，下次启动时生效。'

const fileInput = ref(null)
// pick → uploading → report → applying → result
const phase = ref('pick')
const progress = ref(0)
const report = ref({})
const version = ref('')
// 插件名 → 选中的实例
const bundledSel = ref({})
const restoreFromBackup = ref(true)
const result = ref({})
const resultOk = ref(true)
const resultMessage = ref('')
// 已 apply 过的 token 服务端已经删掉了，关闭时不必再丢弃
let applied = false

const installed = computed(() => !!props.current?.installed)

const currentText = computed(() => {
  const c = props.current ?? {}
  if (!c.installed) return '未安装'
  if (!c.managed) return '手工安装，版本未知'
  return c.version || '版本未知'
})

const bundledColumns = [
  {colKey: 'name', title: '插件', width: 220},
  {colKey: 'targets', title: '装进这些实例'}
]

const actionText = (t) => (t.action === 'update' ? `更新 ${t.installed_version || ''}`.trim() : '新装')

const targetOptions = (plugin) => (report.value.bundled_targets?.[plugin] ?? []).map(t => ({
  value: t.instance,
  label: t.blocked ? `${t.instance}（${t.blocked}）` : `${t.instance}（${actionText(t)}）`,
  disabled: !!t.blocked
}))

const restorable = computed(() => Object.entries(bundledSel.value).some(([plugin, picked]) =>
    (report.value.bundled_targets?.[plugin] ?? []).some(
        t => picked.includes(t.instance) && t.action === 'install' && t.backup_available)))

const reset = () => {
  phase.value = 'pick'
  progress.value = 0
  report.value = {}
  version.value = ''
  bundledSel.value = {}
  restoreFromBackup.value = true
  result.value = {}
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
    const res = await uploadArkApiPackage('core', file, {onProgress: (p) => (progress.value = p)})
    report.value = res.data ?? {}
    version.value = report.value.version ?? ''
    bundledSel.value = Object.fromEntries((report.value.bundled ?? []).map(b => [b.name, []]))
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

const apply = async () => {
  phase.value = 'applying'
  const bundled = Object.fromEntries(Object.entries(bundledSel.value).filter(([, picked]) => picked.length > 0))
  try {
    const res = await applyArkApiPackage(report.value.token, {
      version: version.value,
      bundled,
      restore_from_backup: restorable.value && restoreFromBackup.value
    })
    applied = true
    result.value = res.data ?? {}
    resultOk.value = !!res.success
    resultMessage.value = res.message || '完成'
    phase.value = 'result'
  } catch (err) {
    // 附带插件的选择被拒、server-files 正忙时暂存包还在，可以改了再确认；其他情况（典型是过期）只能重新上传
    MessagePlugin.error(`安装失败: ${err.message ?? err}`)
    phase.value = 'report'
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
  gap: 6px;
}

.meta .k {
  display: inline-block;
  min-width: 72px;
  color: var(--td-text-color-secondary);
}

.version-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.version-input {
  width: 140px;
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

.small {
  font-size: 12px;
}

.blocked {
  color: var(--td-error-color);
  font-size: 12px;
}
</style>
