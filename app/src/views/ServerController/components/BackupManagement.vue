<template>
  <!-- 备份管理 -->
  <t-card title="备份管理" :bordered="false" class="section-card">
    <t-form :data="backupForm" layout="vertical">
      <t-row :gutter="20">
        <t-col :span="12">
          <t-form-item name="instance" label="选择实例">
            <t-select
                v-model="backupForm.instance"
                placeholder="请选择实例"
            >
              <t-option value="">请选择实例</t-option>
              <t-option
                  v-for="instance in instances"
                  :key="instance.name"
                  :value="instance.name"
              >
                {{ instance.name }}
              </t-option>
            </t-select>
          </t-form-item>
        </t-col>
      </t-row>

      <t-form-item>
        <t-button
            @click="createBackupHandler"
            theme="primary"
            :disabled="!backupForm.instance"
            :loading="loading"
        >
          创建备份
        </t-button>
      </t-form-item>
    </t-form>

    <div class="backup-list" v-if="backups.length > 0">
      <h4>现有备份:</h4>
      <t-list :data="backups" :split="false">
        <template #item="{ item }">
          <div class="backup-item">
            <div class="backup-item-name">{{ item }}</div>
            <t-button @click="restoreBackupHandler(item)" size="small">恢复</t-button>
          </div>
        </template>
      </t-list>
    </div>
  </t-card>

  <!-- 恢复选项对话框 -->
  <t-dialog
      v-model:visible="restoreModal.visible"
      header="选择要恢复的内容"
      :confirm-btn="{ content: '恢复', loading: restoreModal.confirmLoading }"
      :cancel-btn="'取消'"
      width="760px"
      @confirm="confirmRestore"
  >
    <t-form layout="vertical">
      <t-form-item label="实例">
        <div style="font-size: 14px">{{ restoreModal.instanceName }}</div>
      </t-form-item>
      <t-form-item label="备份文件">
        <div style="font-size: 14px">{{ restoreModal.backupFile }}</div>
      </t-form-item>
      <t-form-item label="下列组件将被恢复：">
        <t-checkbox v-model="restoreModal.options.restoreWorldfile">
          worldfile (世界文件/SaveDir)
        </t-checkbox>
        <t-checkbox v-model="restoreModal.options.restoreInstanceConfig">
          instance_config.ini (实例配置)
        </t-checkbox>
        <t-checkbox v-model="restoreModal.options.restoreGameConfig">
          Config (游戏配置)
        </t-checkbox>
      </t-form-item>
    </t-form>
  </t-dialog>
</template>

<script setup>
import {ref, reactive, watch} from 'vue'
import {
  createBackup,
  listBackups,
  restoreBackup
} from '@/apis/api.js'
import {MessagePlugin, DialogPlugin} from 'tdesign-vue-next'

const props = defineProps({
  instances: {
    type: Array,
    default: () => []
  }
})

// 状态管理
const backups = ref([])
const backupForm = reactive({
  instance: ''
})
const loading = ref(false)

// 恢复选项模态框
const restoreModal = reactive({
  visible: false,
  backupFile: '',
  instanceName: '',
  confirmLoading: false,
  options: {
    restoreWorldfile: true,
    restoreInstanceConfig: true,
    restoreGameConfig: true
  }
})

// 获取备份列表
const fetchBackups = async () => {
  loading.value = true
  try {
    const data = await listBackups()
    if (data.success) {
      backups.value = data.data.backups || []
    } else {
      console.error('获取备份列表失败:', data.error)
      backups.value = []
    }
  } catch (error) {
    console.error('获取备份列表失败:', error)
    backups.value = []
  } finally {
    loading.value = false
  }
}

// 创建备份
const createBackupHandler = async () => {
  if (!backupForm.instance) return
  loading.value = true
  try {
    const data = await createBackup(backupForm.instance)
    if (data.success) {
      MessagePlugin.success('备份创建成功')
      // 刷新备份列表
      await fetchBackups()
    } else {
      console.error('创建备份失败:', data.error)
      MessagePlugin.error('创建备份失败: ' + (data.error || '未知错误'))
    }
  } catch (error) {
    console.error('创建备份失败:', error)
    MessagePlugin.error('创建备份失败: ' + error.message)
  } finally {
    loading.value = false
  }
}

// 恢复备份
const restoreBackupHandler = async (backupFile) => {
  if (!backupForm.instance) {
    MessagePlugin.warning('请先选择要恢复备份的实例')
    return
  }

  // 打开恢复选项对话框
  restoreModal.visible = true
  restoreModal.backupFile = backupFile
  restoreModal.instanceName = backupForm.instance
  restoreModal.options = {
    restoreWorldfile: true,
    restoreInstanceConfig: true,
    restoreGameConfig: true
  }
}

// 确认恢复备份
const confirmRestore = async () => {
  if (!restoreModal.instanceName || !restoreModal.backupFile) return
  
  // 检查是否至少选择了一个组件
  const { restoreWorldfile, restoreInstanceConfig, restoreGameConfig } = restoreModal.options
  if (!restoreWorldfile && !restoreInstanceConfig && !restoreGameConfig) {
    MessagePlugin.warning('请至少选择一个要恢复的组件')
    return
  }

  // 构建恢复内容描述
  const components = []
  if (restoreWorldfile) components.push('worldfile (世界文件/SaveDir)')
  if (restoreInstanceConfig) components.push('instance_config.ini (实例配置)')
  if (restoreGameConfig) components.push('Config (游戲配置)')
  const componentsList = components.map((c, i) => `${i + 1}. ${c}`).join('\n')

  // 二次确认
  DialogPlugin.confirm({
    header: '确认恢复备份',
    body: `确定要将以下内容从备份 "${restoreModal.backupFile}" 恢复到实例 "${restoreModal.instanceName}" 吗？

将恢复的内容：
${componentsList}

此操作不可撤销，请谨慎操作！`,
    confirmBtn: { content: '确认恢复', theme: 'danger' },
    cancelBtn: '取消',
    onConfirm: async () => {
      loading.value = true
      restoreModal.confirmLoading = true

      try {
        const data = await restoreBackup(
          restoreModal.instanceName,
          restoreModal.backupFile,
          restoreModal.options
        )
        if (data.success) {
          MessagePlugin.success('备份恢复成功')
          restoreModal.visible = false
          await fetchBackups()
        } else {
          console.error('恢复备份失败:', data.error)
          MessagePlugin.error('恢复备份失败: ' + (data.error || '未知错误'))
        }
      } catch (error) {
        console.error('恢复备份失败:', error)
        MessagePlugin.error('恢复备份失败: ' + error.message)
      } finally {
        loading.value = false
        restoreModal.confirmLoading = false
      }
    }
  })
}

// 初始化时获取备份列表
fetchBackups()

// 监听实例变化，当选中实例时重新获取备份
watch(() => backupForm.instance, () => {
  fetchBackups()
})
</script>

<style scoped>
.section-card {
  margin-top: 20px;
}

.backup-list h4 {
  margin: 20px 0 10px 0;
}

.backup-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 0;
}

.backup-item-name {
  color: var(--color-text-1);
}
</style>
