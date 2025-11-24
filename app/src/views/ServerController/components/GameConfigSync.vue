<template>
  <a-card title="服务器运行配置覆盖" :bordered="false" class="section-card">
    <a-form :model="syncForm" layout="vertical">
      <a-row :gutter="20">
        <a-col :span="24">
          <a-form-item label="选择目标实例（可多选）">
            <a-select 
              v-model="syncForm.targetInstances" 
              placeholder="请选择要覆盖配置的实例（Game.ini 和 GameUserSettings.ini）"
              multiple
            >
              <a-option 
                v-for="instance in instances" 
                :key="instance.name" 
                :value="instance.name"
              >
                {{ instance.name }} {{ instance.running ? '(运行中)' : '(已停止)' }}
              </a-option>
            </a-select>
          </a-form-item>
        </a-col>
      </a-row>

      <a-row :gutter="20">
        <a-col :span="24">
          <div class="info-box">
            <a-alert type="info" title="提示" closable>
              该操作将从基础服务器同步 Game.ini 和 GameUserSettings.ini 配置文件到选定的实例，覆盖现有配置。
            </a-alert>
          </div>
        </a-col>
      </a-row>
      
      <a-form-item>
        <a-button 
          @click="handleSyncGameConfig" 
          type="primary"
          :loading="syncing"
          :disabled="syncForm.targetInstances.length === 0"
        >
          {{ syncing ? '同步中...' : '开始覆盖配置' }}
        </a-button>
      </a-form-item>
    </a-form>

    <!-- 同步结果 -->
    <div v-if="syncResult" class="sync-result">
      <a-alert 
        :type="syncResult.success ? 'success' : 'warning'"
        :title="syncResult.message"
        closable
        @close="syncResult = null"
      >
        <div v-if="syncResult.data">
          <div v-if="syncResult.data.synced_instances && syncResult.data.synced_instances.length > 0">
            <p><strong>成功同步的实例：</strong></p>
            <a-tag v-for="instance in syncResult.data.synced_instances" :key="instance" color="green" class="tag-item">
              {{ instance }}
            </a-tag>
          </div>
        </div>
      </a-alert>
    </div>
  </a-card>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { syncGameConfig } from '@/apis/api.js'
import { Message, Modal } from '@arco-design/web-vue'

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
  targetInstances: []
})

// 同步配置
const handleSyncGameConfig = async () => {
  if (syncForm.targetInstances.length === 0) {
    Message.warning('请选择至少一个目标实例')
    return
  }

  Modal.confirm({
    title: '确认覆盖配置',
    content: `确定要将基础服务器的配置文件同步到 ${syncForm.targetInstances.length} 个实例吗？这将覆盖现有的 Game.ini 和 GameUserSettings.ini 文件。`,
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      await executeSyncGameConfig()
    }
  })
}

const executeSyncGameConfig = async () => {
  syncing.value = true
  syncResult.value = null

  try {
    const data = await syncGameConfig(syncForm.targetInstances)

    if (data.success) {
      Message.success('配置同步成功')
      syncResult.value = {
        success: true,
        message: data.message,
        data: data.data
      }
      // 清空表单
      syncForm.targetInstances = []
    } else {
      syncResult.value = {
        success: false,
        message: data.message || '配置同步失败',
        data: data.data
      }
      Message.error(data.message || '配置同步失败')
    }
  } catch (error) {
    console.error('配置同步失败:', error)
    syncResult.value = {
      success: false,
      message: `配置同步失败: ${error.message}`,
      data: null
    }
    Message.error(`配置同步失败: ${error.message}`)
  } finally {
    syncing.value = false
  }
}
</script>

<style scoped>
.section-card {
  margin-top: 20px;
}

.info-box {
  margin-bottom: 16px;
}

.sync-result {
  margin-top: 20px;
}

.tag-item {
  margin: 4px 4px 4px 0;
}
</style>
