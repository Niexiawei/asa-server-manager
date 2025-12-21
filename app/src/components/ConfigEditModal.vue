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
    <a-form :model="editingConfig" layout="vertical">
      <!-- 第一行 -->
      <a-row :gutter="16">
        <a-col :span="6">
          <a-form-item label="服务器名称" :rules="[{ required: true, message: '服务器名称为必填项' }]">
            <a-input
                v-model="editingConfig.ServerName"
                placeholder="输入服务器名称"
            />
          </a-form-item>
        </a-col>
        <a-col :span="6">
          <a-form-item label="最大玩家数" :rules="[{ required: true, message: '最大玩家数为必填项' }]">
            <a-input-number
                v-model="editingConfig.MaxPlayers"
                :min="1"
                placeholder="输入最大玩家数"
            />
          </a-form-item>
        </a-col>
        <a-col :span="6">
          <a-form-item label="游戏端口" :rules="[{ required: true, message: '游戏端口为必填项' }]">
            <a-input-number
                v-model="editingConfig.Port"
                :min="1"
                :max="65535"
                placeholder="输入游戏端口"
            />
          </a-form-item>
        </a-col>
        <a-col :span="6">
          <a-form-item label="RCON端口" :rules="[{ required: true, message: 'RCON端口为必填项' }]">
            <a-input-number
                v-model="editingConfig.RCONPort"
                :min="1"
                :max="65535"
                placeholder="输入RCON端口"
            />
          </a-form-item>
        </a-col>
      </a-row>

      <!-- 第二行 -->
      <a-row :gutter="16">
        <a-col :span="6">
          <a-form-item label="查询端口" :rules="[{ required: true, message: '查询端口为必填项' }]">
            <a-input-number
                v-model="editingConfig.QueryPort"
                :min="1"
                :max="65535"
                placeholder="输入查询端口"
            />
          </a-form-item>
        </a-col>
        <a-col :span="6">
          <a-form-item label="绑定域名">
            <a-input
                v-model="editingConfig.BindDomain"
                placeholder="请输入绑定域名"
            />
          </a-form-item>
        </a-col>
        <a-col :span="6">
          <a-form-item label="地图名称" :rules="[{ required: true, message: '地图名称为必填项' }]">
            <a-input
                v-model="editingConfig.MapName"
                placeholder="输入地图名称"
            />
          </a-form-item>
        </a-col>
        <a-col :span="6">
          <a-form-item label="集群ID" :rules="[{ required: true, message: '集群ID为必填项' }]">
            <a-input
                v-model="editingConfig.ClusterID"
                placeholder="输入集群ID"
            />
          </a-form-item>
        </a-col>
      </a-row>

      <!-- 第三行 -->
      <a-row :gutter="16">
        <a-col :span="6">
          <a-form-item label="存档目录" :rules="[{ required: true, message: '存档目录为必填项' }]">
            <a-input
                v-model="editingConfig.SaveDir"
                placeholder="输入存档目录"
            />
          </a-form-item>
        </a-col>
        <a-col :span="6">
          <a-form-item label="服务器密码">
            <a-input-password
                v-model="editingConfig.ServerPassword"
                placeholder="输入服务器密码"
            />
          </a-form-item>
        </a-col>
        <a-col :span="6">
          <a-form-item label="管理员密码" :rules="[{ required: true, message: '管理员密码为必填项' }]">
            <a-input-password
                v-model="editingConfig.ServerAdminPassword"
                placeholder="输入管理员密码"
            />
          </a-form-item>
        </a-col>
        <a-col :span="6">
          <a-form-item label="启用ASA插件">
            <a-switch
                v-model="editingConfig.EnableAsaPlugin"
                size="large"
            />
          </a-form-item>
        </a-col>
      </a-row>

      <!-- 第四行 -->
      <a-row :gutter="16">
        <a-col :span="24">
          <a-form-item label="Mod IDs">
            <a-textarea
                v-model="editingConfig.ModIDs"
                placeholder="输入Mod IDs（逗号分隔）"
                :rows="2"
            />
          </a-form-item>
        </a-col>
      </a-row>

      <!-- 第五行 -->
      <a-row :gutter="16">
        <a-col :span="24">
          <a-form-item label="自定义启动参数">
            <a-textarea
                v-model="editingConfig.CustomStartParameters"
                placeholder="输入自定义启动参数"
                :rows="2"
            />
          </a-form-item>
        </a-col>
      </a-row>
    </a-form>
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
  CustomStartParameters: '',
  EnableAsaPlugin: false,
  BindDomain: ''
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
    QueryPort: props.config?.QueryPort || '',
    ModIDs: props.config?.ModIDs || '',
    SaveDir: props.config?.SaveDir || '',
    ClusterID: props.config?.ClusterID || '',
    CustomStartParameters: props.config?.CustomStartParameters || '',
    EnableAsaPlugin: props.config?.EnableAsaPlugin || false,
    BindDomain: props.config?.BindDomain || ''
  }
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

<style scoped lang="less">
/* 使用 arco design 的内置样式，无需额外定义 */
</style>
