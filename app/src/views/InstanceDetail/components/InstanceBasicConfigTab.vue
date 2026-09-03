<template>
  <div class="basic-config-tab">
    <div class="basic-toolbar">
      <div class="rules-hero">
        <info-circle-icon class="rules-hero-icon"/>
        <span v-if="running" class="toolbar-hint">服务器运行中，停止后方可保存修改。</span>
        <span v-else-if="dirty" class="toolbar-hint dirty">有未保存的修改</span>
      </div>
      <div class="toolbar-actions">
        <t-button variant="outline" :disabled="!dirty" @click="resetForm">重置</t-button>
        <t-button theme="primary" :loading="saving" :disabled="running" @click="handleSave">
          保存
        </t-button>
      </div>
    </div>

    <t-form
        ref="formRef"
        :data="editingConfig"
        :disabled="running"
        label-width="120px"
        label-align="top"
        class="basic-form"
    >
      <t-row :gutter="16">
        <t-col :span="6">
          <t-form-item name="ServerName" label="服务器名称"
                       :rules="[{ required: true, message: '服务器名称为必填项' }]">
            <t-input v-model="editingConfig.ServerName" placeholder="输入服务器名称"/>
          </t-form-item>
        </t-col>
        <t-col :span="6">
          <t-form-item name="BindDomain" label="绑定域名">
            <t-input v-model="editingConfig.BindDomain" placeholder="请输入绑定域名"/>
          </t-form-item>
        </t-col>
      </t-row>

      <t-row :gutter="16">
        <t-col :span="4">
          <t-form-item name="MaxPlayers" label="最大玩家数"
                       :rules="[{ required: true, message: '最大玩家数为必填项' }]">
            <t-input-number v-model="editingConfig.MaxPlayers" :min="1" placeholder="输入最大玩家数"/>
          </t-form-item>
        </t-col>
        <t-col :span="4">
          <t-form-item name="Port" label="游戏端口" :rules="[{ required: true, message: '游戏端口为必填项' }]">
            <t-input-number v-model="editingConfig.Port" :min="1" :max="65535" placeholder="输入游戏端口"/>
          </t-form-item>
        </t-col>
        <t-col :span="4">
          <t-form-item name="RCONPort" label="RCON端口" :rules="[{ required: true, message: 'RCON端口为必填项' }]">
            <t-input-number v-model="editingConfig.RCONPort" :min="1" :max="65535" placeholder="输入RCON端口"/>
          </t-form-item>
        </t-col>
      </t-row>

      <t-row :gutter="16">
        <t-col :span="6">
          <t-form-item name="MapName" label="地图名称" :rules="[{ required: true, message: '地图名称为必填项' }]">
            <t-select v-model="editingConfig.MapName" placeholder="选择地图" clearable>
              <t-option
                  v-for="map in mapOptions"
                  :key="map.value"
                  :value="map.value"
                  :label="map.label"
              >
                <t-tooltip v-if="map.tips" :content="map.tips" placement="right">
                  <span>{{ map.label }}</span>
                </t-tooltip>
                <span v-else>{{ map.label }}</span>
              </t-option>
            </t-select>
          </t-form-item>
        </t-col>
        <t-col :span="6">
          <t-form-item name="ClusterID" label="集群ID" :rules="[{ required: true, message: '集群ID为必填项' }]">
            <t-input v-model="editingConfig.ClusterID" placeholder="输入集群ID"/>
          </t-form-item>
        </t-col>
      </t-row>

      <t-row :gutter="16">
        <t-col :span="6">
          <t-form-item name="SaveDir" label="存档目录" :rules="[{ required: true, message: '存档目录为必填项' }]">
            <t-input v-model="editingConfig.SaveDir" placeholder="输入存档目录"/>
          </t-form-item>
        </t-col>
        <t-col :span="6">
          <t-form-item name="ServerPassword" label="服务器密码">
            <t-input v-model="editingConfig.ServerPassword" type="password" placeholder="输入服务器密码"/>
          </t-form-item>
        </t-col>
      </t-row>

      <t-row :gutter="16">
        <t-col :span="6">
          <t-form-item name="ServerAdminPassword" label="管理员密码"
                       :rules="[{ required: true, message: '管理员密码为必填项' }]">
            <t-input v-model="editingConfig.ServerAdminPassword" type="password" placeholder="输入管理员密码"/>
          </t-form-item>
        </t-col>
      </t-row>

      <t-row :gutter="16">
        <t-col :span="12">
          <t-form-item name="ModIDs" class="mod-edit-item" label="Mod IDs">
            <div style="margin-bottom: 8px; width: 100%">
              <t-space break-line :size="4">
                <t-tag
                    v-for="tag in modTags"
                    :key="tag"
                    closable
                    theme="primary"
                    @close="handleRemove(tag)"
                >
                  {{ getModNameById(tag) || tag }}
                </t-tag>
                <t-input
                    v-if="showInput"
                    ref="inputRef"
                    :style="{ width: '120px' }"
                    size="small"
                    v-model.trim="inputVal"
                    placeholder="Mod ID"
                    @enter="handleAdd"
                    @blur="handleAdd"
                />
                <t-tag
                    v-else
                    :style="{
                      width: '120px',
                      backgroundColor: 'var(--color-fill-2)',
                      border: '1px dashed var(--color-fill-3)',
                      cursor: 'pointer',
                    }"
                    @click="handleEdit"
                >
                  <template #icon>
                    <add-icon/>
                  </template>
                  添加 Mod ID
                </t-tag>
              </t-space>
            </div>
            <t-textarea
                v-model="editingConfig.ModIDs"
                placeholder="输入Mod IDs（逗号分隔）"
                :rows="2"
            />
          </t-form-item>
        </t-col>
      </t-row>

      <t-row :gutter="16">
        <t-col :span="12">
          <t-form-item name="CustomStartParameters" label="自定义启动参数">
            <t-textarea
                v-model="editingConfig.CustomStartParameters"
                placeholder="输入自定义启动参数"
                :rows="2"
            />
          </t-form-item>
        </t-col>
      </t-row>

      <t-divider>服务器公告</t-divider>

      <t-row :gutter="16">
        <t-col :span="12">
          <t-form-item name="MessageOfTheDay" label="公告">
            <t-textarea v-model="editingConfig.MessageOfTheDay" placeholder="输入公告" :rows="5"/>
          </t-form-item>
        </t-col>
      </t-row>
      <t-row :gutter="16">
        <t-col :span="3">
          <t-form-item name="MessageOfTheDayDuration" label="消息时长">
            <t-input-number v-model="editingConfig.MessageOfTheDayDuration" :min="1" placeholder="输入消息时长">
              <template #suffix>秒</template>
            </t-input-number>
          </t-form-item>
        </t-col>
      </t-row>

      <t-divider>其他设置</t-divider>

      <t-row :gutter="8">
        <t-col :span="6">
          <t-form-item name="EnableAsaPlugin" label="启用ASA插件">
            <t-switch v-model="editingConfig.EnableAsaPlugin"/>
          </t-form-item>
        </t-col>
      </t-row>
    </t-form>
  </div>
</template>

<script setup>
import {computed, nextTick, ref, watch} from 'vue'
import {AddIcon, InfoCircleIcon} from 'tdesign-icons-vue-next'

const props = defineProps({
  config: {type: Object, default: () => ({})},
  saving: {type: Boolean, default: false},
  running: {type: Boolean, default: false},
  modInfo: {type: Array, default: () => []},
})
const emit = defineEmits(['save', 'update:dirty'])

const formRef = ref()

const FIELDS = [
  'ServerName', 'ServerPassword', 'ServerAdminPassword', 'MaxPlayers', 'MapName',
  'Port', 'RCONPort', 'ModIDs', 'SaveDir', 'ClusterID', 'CustomStartParameters',
  'EnableAsaPlugin', 'BindDomain', 'MessageOfTheDay', 'MessageOfTheDayDuration',
]

const projectConfig = (src) => {
  const src0 = src || {}
  const out = {}
  for (const k of FIELDS) {
    if (k === 'EnableAsaPlugin') out[k] = src0[k] || false
    else out[k] = src0[k] ?? ''
  }
  return out
}

const editingConfig = ref(projectConfig(props.config))

const MAP_OPTIONS = [
  {label: 'The Island', value: 'TheIsland_WP'},
  {label: 'The Center', value: 'TheCenter_WP'},
  {label: 'Scorched Earth', value: 'ScorchedEarth_WP'},
  {label: 'Ragnarok', value: 'Ragnarok_WP'},
  {label: 'Aberration', value: 'Aberration_WP'},
  {label: 'Extinction', value: 'Extinction_WP'},
  {label: 'Valguero', value: 'Valguero_WP'},
  {label: 'Astraeos', value: 'Astraeos_WP'},
  {label: 'Lost Colony', value: 'LostColony_WP'},
  {label: 'Club ARK', value: 'BobsMissions_WP', tips: 'Requires Club ARK mod 1005639'},
]

const mapOptions = computed(() => {
  const current = editingConfig.value.MapName
  if (current && !MAP_OPTIONS.some((m) => m.value === current)) {
    return [...MAP_OPTIONS, {label: current, value: current}]
  }
  return MAP_OPTIONS
})

// Mod 标签
const modTags = ref([])
const showInput = ref(false)
const inputVal = ref('')
const inputRef = ref(null)

const getModNameById = (modId) => {
  if (!modId) return null
  const mod = props.modInfo.find((m) => m.id === modId.toString())
  return mod ? mod.name : null
}

const syncInputToModTags = () => {
  editingConfig.value.ModIDs
      ? (modTags.value = editingConfig.value.ModIDs.split(',').filter((id) => id.trim()).map((id) => id.trim()))
      : (modTags.value = [])
}
const syncModTagsToInput = () => {
  editingConfig.value.ModIDs = modTags.value.join(',')
}

const handleEdit = () => {
  showInput.value = true
  nextTick(() => inputRef.value?.focus())
}
const handleAdd = () => {
  if (inputVal.value && !modTags.value.includes(inputVal.value)) {
    modTags.value.push(inputVal.value)
    syncModTagsToInput()
    inputVal.value = ''
  }
  showInput.value = false
}
const handleRemove = (tag) => {
  modTags.value = modTags.value.filter((t) => t !== tag)
  syncModTagsToInput()
}

watch(
    () => editingConfig.value.ModIDs,
    (newVal) => {
      if (newVal !== modTags.value.join(',')) syncInputToModTags()
    },
)

// 用 props.config 重新初始化
const initFromProps = () => {
  editingConfig.value = projectConfig(props.config)
  syncInputToModTags()
}

watch(() => props.config, initFromProps, {deep: true})

// 脏检测：与 props.config 投影比较
const dirty = computed(
    () => JSON.stringify(editingConfig.value) !== JSON.stringify(projectConfig(props.config)),
)
watch(dirty, (v) => emit('update:dirty', v), {immediate: true})

const resetForm = () => initFromProps()

const toNumber = (value) => {
  if (value === '' || value === null || value === undefined) return undefined
  return Number(value)
}

const buildPayload = () => ({
  ServerName: editingConfig.value.ServerName,
  ServerPassword: editingConfig.value.ServerPassword,
  ServerAdminPassword: editingConfig.value.ServerAdminPassword,
  MaxPlayers: toNumber(editingConfig.value.MaxPlayers),
  MapName: editingConfig.value.MapName,
  Port: toNumber(editingConfig.value.Port),
  RCONPort: toNumber(editingConfig.value.RCONPort),
  ModIDs: editingConfig.value.ModIDs,
  SaveDir: editingConfig.value.SaveDir,
  ClusterID: editingConfig.value.ClusterID,
  CustomStartParameters: editingConfig.value.CustomStartParameters,
  EnableAsaPlugin: editingConfig.value.EnableAsaPlugin,
  BindDomain: editingConfig.value.BindDomain,
  MessageOfTheDay: editingConfig.value.MessageOfTheDay,
  MessageOfTheDayDuration: toNumber(editingConfig.value.MessageOfTheDayDuration),
})

const handleSave = async () => {
  const result = await formRef.value?.validate()
  if (result !== true) return
  emit('save', buildPayload())
}
</script>

<style scoped lang="less">
.basic-config-tab {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.basic-toolbar {
  position: sticky;
  top: 0;
  z-index: 2;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 16px;
  border: 1px solid var(--td-component-border, #dcdcdc);
  border-left: 3px solid var(--td-brand-color, #0052d9);
  border-radius: 10px;
  background: var(--td-bg-color-container, #fff);

  .rules-hero {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    font-size: 13px;
    line-height: 1.6;
    color: var(--td-text-color-secondary, #5a5a5a);

    .rules-hero-icon {
      flex: 0 0 auto;
      margin-top: 2px;
      font-size: 16px;
      color: var(--td-brand-color, #0052d9);
    }
  }
}

.toolbar-hint {
  font-size: 13px;
  color: var(--td-text-color-secondary, #5a5a5a);

  &.dirty {
    color: var(--td-warning-color, #e37318);
  }
}

.toolbar-actions {
  display: flex;
  gap: 8px;
  margin-left: auto;
}

.basic-form {
  :deep(.t-input-number) {
    width: 100%;
  }

  :deep(.t-select) {
    width: 100%;
  }

  :deep(.mod-edit-item) .t-form__controls-content {
    flex-direction: column;
  }
}
</style>
