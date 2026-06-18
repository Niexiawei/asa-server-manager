<template>
  <!-- 配置同步 -->
  <t-card title="配置同步" :bordered="false" class="section-card">
    <t-form :data="syncForm" layout="vertical">
      <t-row :gutter="20">
        <t-col :span="12">
          <t-form-item name="sourceInstance" label="源实例">
            <t-select 
              v-model="syncForm.sourceInstance" 
              placeholder="请选择源实例"
              @change="onSourceInstanceChange"
            >
              <t-option value="">请选择源实例</t-option>
              <t-option 
                v-for="instance in instances" 
                :key="instance.name" 
                :value="instance.name"
              >
                {{ instance.name }} {{ instance.running ? '(运行中)' : '(已停止)' }}
              </t-option>
            </t-select>
          </t-form-item>
        </t-col>
        
        <t-col :span="12">
          <t-form-item label="目标实例">
            <t-select 
              v-model="syncForm.targetInstances" 
              placeholder="请选择目标实例（可多选）"
              multiple
            >
              <t-option 
                v-for="instance in targetInstanceOptions" 
                :key="instance.name" 
                :value="instance.name"
              >
                {{ instance.name }} {{ instance.running ? '(运行中)' : '(已停止)' }}
              </t-option>
            </t-select>
          </t-form-item>
        </t-col>
      </t-row>

      <!-- 同步选项 -->
      <t-row v-if="syncForm.sourceInstance" :gutter="20">
        <t-col :span="24">
          <div class="sync-options-section">
            <div class="sync-options-title">同步选项：</div>
            <t-checkbox v-model="syncForm.syncCustomStartParameters" class="option-item">
              同步自定义启动参数 (CustomStartParameters)
            </t-checkbox>
            <t-checkbox v-model="syncForm.syncEnableAsaPlugin" class="option-item">
              同步启用ASA插件 (EnableAsaPlugin)
            </t-checkbox>
          </div>
        </t-col>
      </t-row>
      
      <t-form-item>
        <t-button 
          @click="handleSyncConfig" 
          theme="primary"
          :loading="syncing"
          :disabled="!syncForm.sourceInstance || syncForm.targetInstances.length === 0"
        >
          {{ syncing ? '同步中...' : '开始同步' }}
        </t-button>
      </t-form-item>
    </t-form>

    <!-- 同步结果 -->
    <div v-if="syncResult" class="sync-result">
      <t-alert 
        :theme="syncResult.success ? 'success' : 'warning'"
        :message="syncResult.message"
        close
        @close="syncResult = null"
      >
        <div v-if="syncResult.data">
          <div v-if="syncResult.data.synced_instances">
            <p><strong>成功同步的实例：</strong></p>
            <t-tag v-for="instance in syncResult.data.synced_instances" :key="instance" theme="success" class="tag-item">
              {{ instance }}
            </t-tag>
          </div>
          <div v-if="syncResult.data.failed_instances && syncResult.data.failed_instances.length > 0">
            <p><strong style="color: red;">失败的实例：</strong></p>
            <div v-for="failed in syncResult.data.failed_instances" :key="failed.instance" class="failed-item">
              <strong>{{ failed.instance }}:</strong> {{ failed.error }}
            </div>
          </div>
        </div>
      </t-alert>
    </div>
  </t-card>
</template>

<script setup>
import { ref, reactive, computed } from 'vue'
import { syncInstanceConfig } from '@/apis/api.js'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'

const props = defineProps({
  instances: {
    type: Array,
    default: () => []
  }
})

// 状态管理
const syncing = ref(false)
const syncResult = ref(null)
const syncForm = reactive({
  sourceInstance: '',
  targetInstances: [],
  syncCustomStartParameters: true,
  syncEnableAsaPlugin: true
})

// 计算目标实例选项（排除源实例）
const targetInstanceOptions = computed(() => {
  return props.instances?.filter(instance => instance.name !== syncForm.sourceInstance) || []
})

// 源实例变化时清空目标实例
const onSourceInstanceChange = () => {
  syncForm.targetInstances = []
}

// 同步配置
const handleSyncConfig = async () => {
  if (!syncForm.sourceInstance || syncForm.targetInstances.length === 0) {
    MessagePlugin.warning('请选择源实例和目标实例')
    return
  }

  let syncDialog = DialogPlugin.confirm({
    header: '确认',
    body: `确定要将 "${syncForm.sourceInstance}" 的配置同步到 ${syncForm.targetInstances.length} 个目标实例吗？`,
    confirmBtn: '确定',
    cancelBtn: '取消',
    onConfirm: async () => {
      syncDialog.hide()
      await executeSyncConfig()
    }
  })
}

const executeSyncConfig = async () => {
  syncing.value = true
  syncResult.value = null

  try {
    const data = await syncInstanceConfig(
      syncForm.sourceInstance,
      syncForm.targetInstances,
      syncForm.syncCustomStartParameters,
      syncForm.syncEnableAsaPlugin
    )

    if (data.success) {
      MessagePlugin.success('配置同步成功')
      syncResult.value = {
        success: true,
        message: data.message,
        data: data.data
      }
      // 清空表单
      syncForm.sourceInstance = ''
      syncForm.targetInstances = []
      syncForm.syncCustomStartParameters = true
      syncForm.syncEnableAsaPlugin = true
    } else {
      syncResult.value = {
        success: false,
        message: data.message || '配置同步失败',
        data: data.data
      }
      MessagePlugin.error(data.message || '配置同步失败')
    }
  } catch (error) {
    console.error('配置同步失败:', error)
    syncResult.value = {
      success: false,
      message: `配置同步失败: ${error.message}`,
      data: null
    }
    MessagePlugin.error(`配置同步失败: ${error.message}`)
  } finally {
    syncing.value = false
  }
}
</script>

<style scoped>
.section-card {
  margin-top: 20px;
}

.sync-options-section {
  padding: 12px;
  background-color: #fafafa;
  border-radius: 4px;
  border: 1px solid #e5e7eb;
}

.sync-options-title {
  font-weight: 500;
  margin-bottom: 12px;
  font-size: 14px;
}

.option-item {
  display: block;
  margin-bottom: 8px;
  padding: 4px 0;
}

.sync-result {
  margin-top: 20px;
}

.tag-item {
  margin: 4px 4px 4px 0;
}

.failed-item {
  padding: 8px;
  background-color: #fef2f0;
  border-radius: 4px;
  margin-bottom: 8px;
  border-left: 3px solid #ff4d4f;
  color: #ff4d4f;
}
</style>
