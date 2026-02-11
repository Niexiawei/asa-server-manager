<template>
  <t-dialog
      v-model:visible="visible"
      :header="`配置同步 - 源实例: ${selectedSourceInstance}`"
      width="800px"
      :confirm-btn="{ content: '开始同步', disabled: selectedTargetInstances.length === 0 || syncing, loading: syncing }"
      :cancel-btn="'取消'"
      @confirm="handleSyncConfig"
      @close="() => {
        selectedTargetInstances = []
        syncLogs = []
        syncCustomStartParameters = true
        syncEnableAsaPlugin = true
        visible = false
      }"
  >
    <t-space direction="vertical" style="width: 100%">
      <!-- 源实例信息 -->
      <div class="sync-section">
        <div class="section-title">源实例</div>
        <div class="source-instance-info">
          {{ selectedSourceInstance }}
        </div>
      </div>

      <!-- 同步选项 -->
      <div class="sync-section">
        <div class="section-title">同步选项</div>
        <div class="sync-options">
          <t-checkbox v-model="syncCustomStartParameters" class="option-item">
            同步自定义启动参数 (CustomStartParameters)
          </t-checkbox>
          <t-checkbox v-model="syncEnableAsaPlugin" class="option-item">
            同步启用ASA插件 (EnableAsaPlugin)
          </t-checkbox>
        </div>
      </div>

      <!-- 目标实例选择 -->
      <div class="sync-section">
        <div class="section-title">目标实例 (可选一个或多个)</div>
        <div class="target-instances-container">
          <div
              v-for="instance in availableInstances"
              :key="instance.name"
              class="target-instance-card"
              :class="{ selected: selectedTargetInstances.includes(instance.name) }"
              @click="toggleTargetInstance(instance.name)"
          >
            <div class="instance-select-checkbox">
              <t-checkbox :model-value="selectedTargetInstances.includes(instance.name)"/>
            </div>
            <div class="instance-select-info">
              <div class="instance-select-name">{{ instance.name }}</div>
              <div class="instance-select-status">
                <t-tag
                    :theme="instance.running ? 'success' : 'default'"
                >
                  {{ instance.running ? '运行中' : '已停止' }}
                </t-tag>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 同步日志 -->
      <div class="sync-section" v-if="syncLogs.length > 0">
        <div class="section-title">同步日志</div>
        <div class="sync-logs">
          <div v-for="(log, index) in syncLogs" :key="index" class="log-line">
            {{ log }}
          </div>
          <div v-if="syncing" class="log-line syncing">
            <t-loading size="small"/>
            同步中...
          </div>
        </div>
      </div>
    </t-space>
  </t-dialog>
</template>

<script setup>
import {ref, computed} from 'vue'
import {syncInstanceConfig} from '@/apis/api.js'

const visible = defineModel("visible")

const props = defineProps({
  instances: {
    type: Array,
    default: () => []
  },
  sourceInstance: {
    type: String,
    default: ''
  }
})

const emit = defineEmits(['sync-complete'])

const selectedTargetInstances = ref([])
const syncing = ref(false)
const syncLogs = ref([])
const syncCustomStartParameters = ref(true)
const syncEnableAsaPlugin = ref(true)

const selectedSourceInstance = computed(() => props.sourceInstance)

const availableInstances = computed(() => {
  return props.instances?.filter(i => i.name !== selectedSourceInstance.value) || []
})

// 切换目标实例选中状态
const toggleTargetInstance = (instanceName) => {
  const index = selectedTargetInstances.value.indexOf(instanceName)
  if (index > -1) {
    selectedTargetInstances.value.splice(index, 1)
  } else {
    selectedTargetInstances.value.push(instanceName)
  }
}

// 同步配置
const handleSyncConfig = async () => {
  if (!selectedSourceInstance.value || selectedTargetInstances.value.length === 0) {
    return
  }

  syncing.value = true
  syncLogs.value = []

  try {
    syncLogs.value.push(`开始从 "${selectedSourceInstance.value}" 同步配置到 ${selectedTargetInstances.value.length} 个实例...`)

    const data = await syncInstanceConfig(selectedSourceInstance.value, selectedTargetInstances.value, syncCustomStartParameters.value, syncEnableAsaPlugin.value)

    if (data.success) {
      syncLogs.value.push('✓ 配置同步成功！')
      syncLogs.value.push(`同步了 ${data.data.count} 个实例`)
      data.data.synced_instances.forEach(instance => {
        syncLogs.value.push(`  ✓ ${instance}`)
      })

      // 同步完成后触发事件
      setTimeout(() => {
        emit('sync-complete', {
          success: true,
          syncedInstances: data.data.synced_instances
        })
      }, 1000)
    } else {
      syncLogs.value.push('✗ 配置同步出现错误')
      syncLogs.value.push(`错误: ${data.error}`)
      syncLogs.value.push(`成功: ${data.data.success_count} 个实例`)

      if (data.data.synced_instances && data.data.synced_instances.length > 0) {
        syncLogs.value.push('成功的实例:')
        data.data.synced_instances.forEach(instance => {
          syncLogs.value.push(`  ✓ ${instance}`)
        })
      }

      if (data.data.failed_instances && data.data.failed_instances.length > 0) {
        syncLogs.value.push('失败的实例:')
        data.data.failed_instances.forEach(item => {
          syncLogs.value.push(`  ✗ ${item.instance}: ${item.error}`)
        })
      }

      emit('sync-complete', {
        success: false,
        syncedInstances: data.data.synced_instances || [],
        failedInstances: data.data.failed_instances || []
      })
    }
  } catch (error) {
    syncLogs.value.push(`✗ 同步过程中发生错误: ${error.message}`)
    emit('sync-complete', {
      success: false,
      error: error.message
    })
  } finally {
    syncing.value = false
  }
}

</script>

<style scoped>
/* 配置同步弹窗样式 */
.sync-section {
  padding: 12px 0;
  border-bottom: 1px solid #f0f0f0;
}

.sync-section:last-child {
  border-bottom: none;
}

.section-title {
  font-weight: 600;
  color: #333;
  margin-bottom: 12px;
  font-size: 14px;
}

.source-instance-info {
  padding: 12px;
  background-color: #f5f5f5;
  border-radius: 4px;
  border-left: 3px solid #165dff;
  font-weight: 500;
  color: #333;
  font-size: 14px;
}

/* 目标实例卡片样式 */
.target-instances-container {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(250px, 1fr));
  gap: 12px;
  padding: 8px 0;
}

.target-instance-card {
  display: flex;
  align-items: center;
  padding: 12px 16px;
  background-color: #ffffff;
  border: 2px solid #e5e7eb;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.3s ease;
  gap: 12px;
}

.target-instance-card:hover {
  border-color: #165dff;
  background-color: #f6f8ff;
  box-shadow: 0 2px 8px rgba(22, 93, 255, 0.15);
}

.target-instance-card.selected {
  border-color: #165dff;
  background-color: #f6f8ff;
  box-shadow: 0 4px 12px rgba(22, 93, 255, 0.2);
  border-width: 2px;
}

.instance-select-checkbox {
  display: flex;
  align-items: center;
  flex-shrink: 0;
}

.instance-select-info {
  display: flex;
  flex-direction: column;
  gap: 6px;
  flex: 1;
}

.instance-select-name {
  font-weight: 600;
  color: #333;
  font-size: 14px;
}

.instance-select-status {
  display: flex;
  align-items: center;
  gap: 8px;
}

.sync-logs {
  background-color: #f5f5f5;
  border-radius: 4px;
  padding: 12px;
  max-height: 300px;
  overflow-y: auto;
  font-family: 'Monaco', 'Courier New', monospace;
  font-size: 12px;
  line-height: 1.5;
}

.log-line {
  color: #333;
  padding: 4px 0;
  word-break: break-all;
}

.log-line.syncing {
  display: flex;
  align-items: center;
  gap: 8px;
  color: #165dff;
  font-style: italic;
}

.sync-options {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.option-item {
  padding: 8px 0;
  font-size: 14px;
}
</style>
