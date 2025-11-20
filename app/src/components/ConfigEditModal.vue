<template>
  <a-modal
      v-model:visible="localVisible"
      title="编辑服务器配置"
      width="900px"
      :ok-loading="saving"
      :ok-button-props="{ disabled: !isConfigFormValid() }"
      ok-text="保存"
      cancel-text="取消"
      @ok="handleSave"
      @cancel="handleCancel"
  >
    <div class="config-edit-grid">
      <!-- 第一行 -->
      <div class="config-edit-row">
        <div class="config-edit-col">
          <label>
            服务器名称
            <span class="required-mark">*</span>
          </label>
          <a-input
              v-model="editingConfig.ServerName"
              placeholder="输入服务器名称"
              :error="!editingConfig.ServerName && formSubmitted"
          />
          <span v-if="!editingConfig.ServerName && formSubmitted" class="error-text">服务器名称为必填项</span>
        </div>
        <div class="config-edit-col">
          <label>
            最大玩家数
            <span class="required-mark">*</span>
          </label>
          <a-input-number
              v-model="editingConfig.MaxPlayers"
              :min="1"
              placeholder="输入最大玩家数"
              :error="!editingConfig.MaxPlayers && formSubmitted"
          />
          <span v-if="!editingConfig.MaxPlayers && formSubmitted" class="error-text">最大玩家数为必填项</span>
        </div>
        <div class="config-edit-col">
          <label>
            游戏端口
            <span class="required-mark">*</span>
          </label>
          <a-input-number
              v-model="editingConfig.Port"
              :min="1"
              :max="65535"
              placeholder="输入游戏端口"
              :error="!editingConfig.Port && formSubmitted"
          />
          <span v-if="!editingConfig.Port && formSubmitted" class="error-text">游戏端口为必填项</span>
        </div>
        <div class="config-edit-col">
          <label>
            RCON端口
            <span class="required-mark">*</span>
          </label>
          <a-input-number
              v-model="editingConfig.RCONPort"
              :min="1"
              :max="65535"
              placeholder="输入RCON端口"
              :error="!editingConfig.RCONPort && formSubmitted"
          />
          <span v-if="!editingConfig.RCONPort && formSubmitted" class="error-text">RCON端口为必填项</span>
        </div>
      </div>

      <!-- 第二行 -->
      <div class="config-edit-row">
        <div class="config-edit-col">
          <label>
            查询端口
            <span class="required-mark">*</span>
          </label>
          <a-input-number
              v-model="editingConfig.QueryPort"
              :min="1"
              :max="65535"
              placeholder="输入查询端口"
              :error="!editingConfig.QueryPort && formSubmitted"
          />
          <span v-if="!editingConfig.QueryPort && formSubmitted" class="error-text">查询端口为必填项</span>
        </div>
        <div class="config-edit-col">
          <label>
            地图名称
            <span class="required-mark">*</span>
          </label>
          <a-input
              v-model="editingConfig.MapName"
              placeholder="输入地图名称"
              :error="!editingConfig.MapName && formSubmitted"
          />
          <span v-if="!editingConfig.MapName && formSubmitted" class="error-text">地图名称为必填项</span>
        </div>
        <div class="config-edit-col">
          <label>
            集群ID
            <span class="required-mark">*</span>
          </label>
          <a-input
              v-model="editingConfig.ClusterID"
              placeholder="输入集群ID"
              :error="!editingConfig.ClusterID && formSubmitted"
          />
          <span v-if="!editingConfig.ClusterID && formSubmitted" class="error-text">集群ID为必填项</span>
        </div>
        <div class="config-edit-col">
          <label>
            存档目录
            <span class="required-mark">*</span>
          </label>
          <a-input
              v-model="editingConfig.SaveDir"
              placeholder="输入存档目录"
              :error="!editingConfig.SaveDir && formSubmitted"
          />
          <span v-if="!editingConfig.SaveDir && formSubmitted" class="error-text">存档目录为必填项</span>
        </div>
      </div>

      <!-- 第三行 -->
      <div class="config-edit-row">
        <div class="config-edit-col">
          <label>服务器密码</label>
          <a-input-password
              v-model="editingConfig.ServerPassword"
              placeholder="输入服务器密码"
          />
        </div>
        <div class="config-edit-col">
          <label>
            管理员密码
            <span class="required-mark">*</span>
          </label>
          <a-input-password
              v-model="editingConfig.ServerAdminPassword"
              placeholder="输入管理员密码"
              :error="!editingConfig.ServerAdminPassword && formSubmitted"
          />
          <span v-if="!editingConfig.ServerAdminPassword && formSubmitted"
                class="error-text">管理员密码为必填项</span>
        </div>
      </div>

      <!-- 第四行 -->
      <div class="config-edit-full-row">
        <label>Mod IDs</label>
        <a-textarea
            v-model="editingConfig.ModIDs"
            placeholder="输入Mod IDs（逗号分隔）"
            :rows="2"
        />
      </div>

      <!-- 第五行 -->
      <div class="config-edit-full-row">
        <label>自定义启动参数</label>
        <a-textarea
            v-model="editingConfig.CustomStartParameters"
            placeholder="输入自定义启动参数"
            :rows="2"
        />
      </div>
    </div>
  </a-modal>
</template>

<script setup>
import {ref, watch} from 'vue'

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
const formSubmitted = ref(false)
const editingConfig = ref({
  ServerName: '',
  ServerPassword: '',
  ServerAdminPassword: '',
  MaxPlayers: '',
  MapName: '',
  Port: '',
  RCONPort: '',
  QueryPort: '',
  ModIDs: '',
  SaveDir: '',
  ClusterID: '',
  CustomStartParameters: ''
})

// 监听外部 visible 属性变化
watch(() => props.visible, (newVal) => {
  localVisible.value = newVal
  if (newVal) {
    // 打开模态框时，初始化编辑配置
    initializeConfig()
  }
})

// 监听外部 config 属性变化
watch(() => props.config, (newConfig) => {
  if (localVisible.value) {
    initializeConfig()
  }
}, { deep: true })

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
    QueryPort: props.config?.QueryPort || '',
    ModIDs: props.config?.ModIDs || '',
    SaveDir: props.config?.SaveDir || '',
    ClusterID: props.config?.ClusterID || '',
    CustomStartParameters: props.config?.CustomStartParameters || ''
  }
  formSubmitted.value = false
}

// 验证配置表单是否有效
const isConfigFormValid = () => {
  return (
      editingConfig.value.ServerName &&
      editingConfig.value.MaxPlayers &&
      editingConfig.value.Port &&
      editingConfig.value.RCONPort &&
      editingConfig.value.QueryPort &&
      editingConfig.value.MapName &&
      editingConfig.value.ClusterID &&
      editingConfig.value.SaveDir &&
      editingConfig.value.ServerAdminPassword
  )
}

// 处理保存
const handleSave = () => {
  formSubmitted.value = true
  
  if (!isConfigFormValid()) {
    return
  }
  
  emit('save', editingConfig.value)
}

// 处理取消
const handleCancel = () => {
  emit('update:visible', false)
}
</script>

<style scoped>
.config-edit-grid {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.config-edit-row {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
}

.config-edit-full-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.config-edit-col {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.config-edit-col label,
.config-edit-full-row label {
  font-weight: 500;
  font-size: 14px;
  color: #333;
}

/* 必填标记 */
.required-mark {
  color: #f5222d;
  margin-left: 4px;
}

/* 错误文本 */
.error-text {
  color: #f5222d;
  font-size: 12px;
  margin-top: 2px;
  display: block;
  height: 18px;
  line-height: 18px;
}
</style>
