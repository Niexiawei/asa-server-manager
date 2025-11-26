<template>
  <!-- 备份管理 -->
  <a-card title="备份管理" :bordered="false" class="section-card">
    <a-form :model="backupForm" layout="vertical">
      <a-row :gutter="20">
        <a-col :span="12">
          <a-form-item field="instance" label="选择实例">
            <a-select
                v-model="backupForm.instance"
                placeholder="请选择实例"
            >
              <a-option value="">请选择实例</a-option>
              <a-option
                  v-for="instance in instances"
                  :key="instance.name"
                  :value="instance.name"
              >
                {{ instance.name }}
              </a-option>
            </a-select>
          </a-form-item>
        </a-col>
      </a-row>

      <a-form-item>
        <a-button
            @click="createBackupHandler"
            type="primary"
            :disabled="!backupForm.instance"
            :loading="loading"
        >
          创建备份
        </a-button>
      </a-form-item>
    </a-form>

    <div class="backup-list" v-if="backups.length > 0">
      <h4>现有备份:</h4>
      <a-list :data="backups" :bordered="false">
        <template #item="{ item }">
          <a-list-item>
            <a-list-item-meta :description="item"/>
            <template #actions>
              <a-button @click="restoreBackupHandler(item)" size="small">恢复</a-button>
            </template>
          </a-list-item>
        </template>
      </a-list>
    </div>
  </a-card>

  <!-- 恢复选项对话框 -->
  <a-modal
      v-model:visible="restoreModal.visible"
      title="选择要恢复的内容"
      ok-text="恢复"
      cancel-text="取消"
      width="760px"
      @ok="confirmRestore"
      :confirm-loading="restoreModal.confirmLoading"
  >
    <a-form layout="vertical">
      <a-form-item label="实例">
        <div style="font-size: 14px">{{ restoreModal.instanceName }}</div>
      </a-form-item>
      <a-form-item label="备份文件">
        <div style="font-size: 14px">{{ restoreModal.backupFile }}</div>
      </a-form-item>
      <a-form-item label="下列组件将被恢复：">
        <a-checkbox v-model="restoreModal.options.restoreWorldfile">
          worldfile (世界文件/SaveDir)
        </a-checkbox>
        <a-checkbox v-model="restoreModal.options.restoreInstanceConfig">
          instance_config.ini (实例配置)
        </a-checkbox>
        <a-checkbox v-model="restoreModal.options.restoreGameConfig">
          Config (游戏配置)
        </a-checkbox>
      </a-form-item>
    </a-form>
  </a-modal>
</template>

<script setup>
import {ref, reactive, watch} from 'vue'
import {
  createBackup,
  listBackups,
  restoreBackup
} from '@/apis/api.js'
import {Message, Modal} from '@arco-design/web-vue'

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
      Message.success('备份创建成功')
      // 刷新备份列表
      await fetchBackups()
    } else {
      console.error('创建备份失败:', data.error)
      Message.error('创建备份失败: ' + (data.error || '未知错误'))
    }
  } catch (error) {
    console.error('创建备份失败:', error)
    Message.error('创建备份失败: ' + error.message)
  } finally {
    loading.value = false
  }
}

// 恢复备份
const restoreBackupHandler = async (backupFile) => {
  if (!backupForm.instance) {
    Message.warning('请先选择要恢复备份的实例')
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
    Message.warning('请至少选择一个要恢复的组件')
    return
  }

  // 构建恢复内容描述
  const components = []
  if (restoreWorldfile) components.push('worldfile (世界文件/SaveDir)')
  if (restoreInstanceConfig) components.push('instance_config.ini (实例配置)')
  if (restoreGameConfig) components.push('Config (游戲配置)')
  const componentsList = components.map((c, i) => `${i + 1}. ${c}`).join('\n')

  // 二次确认
  Modal.confirm({
    title: '确认恢复备份',
    content: `确定要将以下内容从备份 "${restoreModal.backupFile}" 恢复到实例 "${restoreModal.instanceName}" 吗？

将恢复的内容：
${componentsList}

此操作不可撤销，请谨慎操作！`,
    okText: '确认恢复',
    cancelText: '取消',
    okButtonProps: { status: 'danger' },
    onOk: async () => {
      loading.value = true
      restoreModal.confirmLoading = true
      
      try {
        const data = await restoreBackup(
          restoreModal.instanceName,
          restoreModal.backupFile,
          restoreModal.options
        )
        if (data.success) {
          Message.success('备份恢复成功')
          restoreModal.visible = false
        } else {
          console.error('恢复备份失败:', data.error)
          Message.error('恢复备份失败: ' + (data.error || '未知错误'))
        }
      } catch (error) {
        console.error('恢复备份失败:', error)
        Message.error('恢复备份失败: ' + error.message)
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
</style>
