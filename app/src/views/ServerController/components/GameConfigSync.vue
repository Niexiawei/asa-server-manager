<template>
  <t-card title="服务器运行配置覆盖" :bordered="false" class="section-card">
    <t-form :data="syncForm" layout="vertical">
      <t-row :gutter="20">
        <t-col :span="24">
          <t-form-item label="选择目标实例（可多选）">
            <t-select 
              v-model="syncForm.targetInstances" 
              placeholder="请选择要覆盖配置的实例（Game.ini 和 GameUserSettings.ini）"
              multiple
            >
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
      </t-row>

      <t-row :gutter="20">
        <t-col :span="24">
          <div class="info-box">
            <t-alert theme="info" title="提示" close>
              该操作将从基础服务器同步 Game.ini 和 GameUserSettings.ini 配置文件到选定的实例，覆盖现有配置。
            </t-alert>
          </div>
        </t-col>
      </t-row>
      
      <t-form-item>
        <t-button 
          @click="handleSyncGameConfig" 
          theme="primary"
          :loading="syncing"
          :disabled="syncForm.targetInstances.length === 0"
        >
          {{ syncing ? '同步中...' : '开始覆盖配置' }}
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
          <div v-if="syncResult.data.synced_instances && syncResult.data.synced_instances.length > 0">
            <p><strong>成功同步的实例：</strong></p>
            <t-tag v-for="instance in syncResult.data.synced_instances" :key="instance" theme="success" class="tag-item">
              {{ instance }}
            </t-tag>
          </div>
        </div>
      </t-alert>
    </div>
  </t-card>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { syncGameConfig } from '@/apis/api.js'
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
  targetInstances: []
})

// 同步配置
const handleSyncGameConfig = async () => {
  if (syncForm.targetInstances.length === 0) {
    MessagePlugin.warning('请选择至少一个目标实例')
    return
  }

  DialogPlugin.confirm({
    header: '确认覆盖配置',
    body: `确定要将基础服务器的配置文件同步到 ${syncForm.targetInstances.length} 个实例吗？这将覆盖现有的 Game.ini 和 GameUserSettings.ini 文件。`,
    confirmBtn: '确定',
    cancelBtn: '取消',
    onConfirm: async () => {
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
      MessagePlugin.success('配置同步成功')
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
