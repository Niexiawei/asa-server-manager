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
        
        <a-col :span="12">
          <a-form-item field="worldFolder" label="世界文件夹名称">
            <a-input 
              v-model="backupForm.worldFolder" 
              placeholder="输入世界文件夹名称"
            />
          </a-form-item>
        </a-col>
      </a-row>
      
      <a-form-item>
        <a-button 
          @click="createBackupHandler" 
          type="primary"
          :disabled="!backupForm.instance || !backupForm.worldFolder"
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
            <a-list-item-meta :description="item" />
            <template #actions>
              <a-button @click="restoreBackupHandler(item)" size="small">恢复</a-button>
            </template>
          </a-list-item>
        </template>
      </a-list>
    </div>
  </a-card>
</template>

<script setup>
import { ref, reactive, watch } from 'vue'
import { 
  createBackup,
  listBackups,
  restoreBackup
} from '@/apis/api.js'
import { Message, Modal } from '@arco-design/web-vue'

const props = defineProps({
  instances: {
    type: Array,
    default: () => []
  }
})

// 状态管理
const backups = ref([])
const backupForm = reactive({
  instance: '',
  worldFolder: ''
})

// 获取备份列表
const fetchBackups = async () => {
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
  }
}

// 创建备份
const createBackupHandler = async () => {
  if (!backupForm.instance || !backupForm.worldFolder) return
  
  try {
    const data = await createBackup(backupForm.instance, backupForm.worldFolder)
    if (data.success) {
      Message.success('备份创建成功')
      // 刷新备份列表
      await fetchBackups()
      // 清空输入
      backupForm.worldFolder = ''
    } else {
      console.error('创建备份失败:', data.error)
      Message.error('创建备份失败: ' + (data.error || '未知错误'))
    }
  } catch (error) {
    console.error('创建备份失败:', error)
    Message.error('创建备份失败: ' + error.message)
  }
}

// 恢复备份
const restoreBackupHandler = async (backupFile) => {
  if (!backupForm.instance) {
    Message.warning('请先选择要恢复备份的实例')
    return
  }
  
  Modal.confirm({
    title: '确认',
    content: `确定要将备份 "${backupFile}" 恢复到实例 "${backupForm.instance}" 吗？`,
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      try {
        const data = await restoreBackup(backupForm.instance, backupFile)
        if (data.success) {
          Message.success('备份恢复成功')
        } else {
          console.error('恢复备份失败:', data.error)
          Message.error('恢复备份失败: ' + (data.error || '未知错误'))
        }
      } catch (error) {
        console.error('恢复备份失败:', error)
        Message.error('恢复备份失败: ' + error.message)
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
