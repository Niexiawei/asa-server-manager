<template>
  <t-drawer
      v-model:visible="localVisible"
      header="编辑服务器配置"
      :confirm-btn="{ content: '保存', loading: saving }"
      :cancel-btn="'取消'"
      @confirm="handleBeforeOk"
      @close="handleCancel"
      :size="1100"
  >
    <div class="config-edit-modal-body">
      <t-form ref="formRef" :data="editingConfig"
              label-width="120px" label-align="top"
      >
        <t-row :gutter="16">
          <t-col :span="6">
            <t-form-item name="ServerName" label="服务器名称"
                         :rules="[{ required: true, message: '服务器名称为必填项' }]">
              <t-input
                  v-model="editingConfig.ServerName"
                  placeholder="输入服务器名称"
              />
            </t-form-item>
          </t-col>
          <t-col :span="6">
            <t-form-item name="BindDomain" label="绑定域名">
              <t-input
                  v-model="editingConfig.BindDomain"
                  placeholder="请输入绑定域名"
              />
            </t-form-item>
          </t-col>
        </t-row>

        <t-row :gutter="16">
          <t-col :span="4">
            <t-form-item name="MaxPlayers" label="最大玩家数"
                         :rules="[{ required: true, message: '最大玩家数为必填项' }]">
              <t-input-number
                  v-model="editingConfig.MaxPlayers"
                  :min="1"
                  placeholder="输入最大玩家数"
              />
            </t-form-item>
          </t-col>
          <t-col :span="4">
            <t-form-item name="Port" label="游戏端口" :rules="[{ required: true, message: '游戏端口为必填项' }]">
              <t-input-number
                  v-model="editingConfig.Port"
                  :min="1"
                  :max="65535"
                  placeholder="输入游戏端口"
              />
            </t-form-item>
          </t-col>
          <t-col :span="4">
            <t-form-item name="RCONPort" label="RCON端口" :rules="[{ required: true, message: 'RCON端口为必填项' }]">
              <t-input-number
                  v-model="editingConfig.RCONPort"
                  :min="1"
                  :max="65535"
                  placeholder="输入RCON端口"
              />
            </t-form-item>
          </t-col>
        </t-row>
        <t-row :gutter="16">
          <t-col :span="6">
            <t-form-item name="MapName" label="地图名称" :rules="[{ required: true, message: '地图名称为必填项' }]">
              <t-select
                  v-model="editingConfig.MapName"
                  placeholder="选择地图"
                  clearable
              >
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
              <t-input
                  v-model="editingConfig.ClusterID"
                  placeholder="输入集群ID"
              />
            </t-form-item>
          </t-col>
        </t-row>

        <t-row :gutter="16">
          <t-col :span="6">
            <t-form-item name="SaveDir" label="存档目录" :rules="[{ required: true, message: '存档目录为必填项' }]">
              <t-input
                  v-model="editingConfig.SaveDir"
                  placeholder="输入存档目录"
              />
            </t-form-item>
          </t-col>
          <t-col :span="6">
            <t-form-item name="ServerPassword" label="服务器密码">
              <t-input
                  v-model="editingConfig.ServerPassword"
                  type="password"
                  placeholder="输入服务器密码"
              />
            </t-form-item>
          </t-col>
        </t-row>

        <t-row :gutter="16">
          <t-col :span="6">
            <t-form-item name="ServerAdminPassword" label="管理员密码"
                         :rules="[{ required: true, message: '管理员密码为必填项' }]">
              <t-input
                  v-model="editingConfig.ServerAdminPassword"
                  type="password"
                  placeholder="输入管理员密码"
              />
            </t-form-item>
          </t-col>
        </t-row>

        <t-row :gutter="16">
          <t-col :span="12">
            <t-form-item name="ModIDs" class="mod-edit-item" label="Mod IDs">
              <div style="margin-bottom: 8px;width: 100%">
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
                      :style="{ width: '120px'}"
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
              <t-textarea
                  v-model="editingConfig.MessageOfTheDay"
                  placeholder="输入公告"
                  :rows="5"
              />
            </t-form-item>
          </t-col>
        </t-row>
        <t-row :gutter="16">
          <t-col :span="3">
            <t-form-item name="MessageOfTheDayDuration" label="消息时长">
              <t-input-number
                  v-model="editingConfig.MessageOfTheDayDuration"
                  :min="1"
                  placeholder="输入消息时长"
              >
                <template #suffix>
                  秒
                </template>
              </t-input-number>
            </t-form-item>
          </t-col>
        </t-row>

        <t-divider>其他设置</t-divider>

        <t-row :gutter="8">
          <t-col :span="6">
            <t-form-item name="EnableAsaPlugin" label="启用ASA插件">
              <t-switch
                  v-model="editingConfig.EnableAsaPlugin"
              />
            </t-form-item>
          </t-col>
        </t-row>
      </t-form>
    </div>
  </t-drawer>
</template>

<script setup>
import {ref, watch, nextTick, onMounted, computed} from 'vue'
import {AddIcon} from 'tdesign-icons-vue-next'
import {getModInfo} from '@/apis/api.js'

const props = defineProps({
  visible: {
    type: Boolean,
    required: true
  },
  config: {
    type: Object,
    required: true
  },
  saving: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['update:visible', 'save'])

const localVisible = ref(props.visible)
const formRef = ref()
const editingConfig = ref({
  ServerName: '',
  ServerPassword: '',
  ServerAdminPassword: '',
  MaxPlayers: '',
  MapName: '',
  Port: '',
  RCONPort: '',
  ModIDs: '',
  SaveDir: '',
  ClusterID: '',
  CustomStartParameters: '',
  EnableAsaPlugin: false,
  BindDomain: '',
  MessageOfTheDay: '',
  MessageOfTheDayDuration: ''
})

// Mod 信息相关
const modInfo = ref([])
const modTags = ref([])
const showInput = ref(false)
const inputVal = ref('')
const inputRef = ref(null)

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
  {
    label: 'Club ARK',
    value: 'BobsMissions_WP',
    tips: 'Requires Club ARK mod 1005639',
  },
]

const mapOptions = computed(() => {
  const current = editingConfig.value.MapName
  if (current && !MAP_OPTIONS.some((m) => m.value === current)) {
    return [...MAP_OPTIONS, {label: current, value: current}]
  }
  return MAP_OPTIONS
})

// 获取Mod信息
const fetchModInfo = async () => {
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
  }
}

// 根据Mod ID获取Mod名称
const getModNameById = (modId) => {
  if (!modId) return null
  const mod = modInfo.value.find(m => m.id === modId.toString())
  return mod ? mod.name : null
}

// 处理标签编辑
const handleEdit = () => {
  showInput.value = true
  nextTick(() => {
    if (inputRef.value) {
      inputRef.value.focus()
    }
  })
}

// 添加标签
const handleAdd = () => {
  if (inputVal.value && !modTags.value.includes(inputVal.value)) {
    modTags.value.push(inputVal.value)
    syncModTagsToInput()
    inputVal.value = ''
  }
  showInput.value = false
}

// 删除标签
const handleRemove = (tag) => {
  modTags.value = modTags.value.filter(t => t !== tag)
  syncModTagsToInput()
}

// 将标签同步到输入框
const syncModTagsToInput = () => {
  editingConfig.value.ModIDs = modTags.value.join(',')
}

// 将输入框同步到标签
const syncInputToModTags = () => {
  if (editingConfig.value.ModIDs) {
    modTags.value = editingConfig.value.ModIDs.split(',').filter(id => id.trim()).map(id => id.trim())
  } else {
    modTags.value = []
  }
}

// 监听 ModIDs 输入框变化，同步到标签
watch(() => editingConfig.value.ModIDs, (newVal) => {
  if (newVal !== modTags.value.join(',')) {
    syncInputToModTags()
  }
})

// 监听外部 visible 属性变化
watch(() => props.visible, (newVal) => {
  localVisible.value = newVal
  if (newVal) {
    initializeConfig()
  }
})

watch(localVisible, (val) => {
  if (val !== props.visible) {
    emit('update:visible', val)
  }
})

// 监听外部 config 属性变化
watch(() => props.config, (newConfig) => {
  if (localVisible.value) {
    initializeConfig()
  }
}, {deep: true})

// 初始化配置
const initializeConfig = () => {
  editingConfig.value = {
    ServerName: props.config?.ServerName || '',
    ServerPassword: props.config?.ServerPassword || '',
    ServerAdminPassword: props.config?.ServerAdminPassword || '',
    MaxPlayers: props.config?.MaxPlayers || '',
    MapName: props.config?.MapName || '',
    Port: props.config?.Port || '',
    RCONPort: props.config?.RCONPort || '',
    ModIDs: props.config?.ModIDs || '',
    SaveDir: props.config?.SaveDir || '',
    ClusterID: props.config?.ClusterID || '',
    CustomStartParameters: props.config?.CustomStartParameters || '',
    EnableAsaPlugin: props.config?.EnableAsaPlugin || false,
    BindDomain: props.config?.BindDomain || '',
    MessageOfTheDay: props.config?.MessageOfTheDay || '',
    MessageOfTheDayDuration: props.config?.MessageOfTheDayDuration || ''
  }
  // 同步 ModIDs 到标签
  syncInputToModTags()
}

// 组件挂载时获取 Mod 信息
onMounted(() => {
  fetchModInfo()
})

const toNumber = (value) => {
  if (value === '' || value === null || value === undefined) {
    return undefined
  }
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

const handleBeforeOk = async () => {
  const result = await formRef.value?.validate()
  if (result !== true) {
    return false
  }

  emit('save', buildPayload())
  return false
}

// 处理取消
const handleCancel = () => {
  emit('update:visible', false)
}
</script>

<style scoped lang="less">
.config-edit-modal-body {
  overflow-x: hidden;
}

:deep(.t-input-number) {
  width: 100%;
}

:deep(.t-select) {
  width: 100%;
}

:deep(.mod-edit-item) {
  .t-form__controls-content {
    flex-direction: column;
  }
}
</style>
