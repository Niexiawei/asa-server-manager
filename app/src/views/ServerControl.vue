<template>
  <div class="server-control">
    <a-card title="服务器控制面板" :bordered="false" class="main-card">
      <!-- 服务器控制 -->
      <a-card title="全局服务器控制" :bordered="false">
        <a-space>
          <a-button @click="startAllServersHandler" type="primary">启动所有服务器</a-button>
          <a-button @click="stopAllServersHandler" status="warning">停止所有服务器</a-button>
          <a-button @click="updateServerHandler" status="normal">更新服务器</a-button>
        </a-space>
      </a-card>
      
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
      
      <!-- RCON 命令 -->
      <a-card title="RCON 命令" :bordered="false" class="section-card">
        <a-form :model="rconForm" layout="vertical">
          <a-row :gutter="20">
            <a-col :span="12">
              <a-form-item field="instance" label="选择实例">
                <a-select 
                  v-model="rconForm.instance" 
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
              <a-form-item field="command" label="RCON 命令">
                <a-input 
                  v-model="rconForm.command" 
                  placeholder="输入 RCON 命令"
                  @press-enter="sendRconCommandHandler"
                />
              </a-form-item>
            </a-col>
          </a-row>
          
          <a-form-item>
            <a-button 
              @click="sendRconCommandHandler" 
              type="primary"
              :disabled="!rconForm.instance || !rconForm.command"
            >
              发送命令
            </a-button>
          </a-form-item>
        </a-form>
        
        <div class="rcon-response" v-if="rconResponse">
          <h4>响应:</h4>
          <a-alert type="info">
            <pre>{{ rconResponse }}</pre>
          </a-alert>
        </div>
      </a-card>
    </a-card>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { 
  startAllServers, 
  stopAllServers, 
  updateServer,
  createBackup,
  listBackups,
  restoreBackup,
  sendRCONCommand
} from '../apis/api.js'

// 状态管理
const instances = ref([])
const backups = ref([])
const rconResponse = ref('')

const backupForm = reactive({
  instance: '',
  worldFolder: ''
})

const rconForm = reactive({
  instance: '',
  command: ''
})

// 获取实例列表
const fetchInstances = async () => {
  try {
    // 这里应该从 ServerManager 组件获取实例列表
    // 暂时使用模拟数据
    instances.value = [
      { name: 'TheIsland', running: true },
      { name: 'Ragnarok', running: false }
    ]
  } catch (error) {
    console.error('获取实例列表失败:', error)
  }
}

// 获取备份列表
const fetchBackups = async () => {
  try {
    const data = await listBackups()
    if (data.success) {
      backups.value = data.data.backups
    } else {
      console.error('获取备份列表失败:', data.error)
    }
  } catch (error) {
    console.error('获取备份列表失败:', error)
  }
}

// 启动所有服务器
const startAllServersHandler = async () => {
  // 使用 arco-design 的确认对话框
  $dialog.confirm({
    title: '确认',
    content: '确定要启动所有服务器吗？',
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      try {
        const data = await startAllServers()
        if (data.success) {
          $message.success('所有服务器已启动')
        } else {
          console.error('启动所有服务器失败:', data.error)
          $message.error('启动所有服务器失败: ' + data.error)
        }
      } catch (error) {
        console.error('启动所有服务器失败:', error)
        $message.error('启动所有服务器失败: ' + error.message)
      }
    }
  })
}

// 停止所有服务器
const stopAllServersHandler = async () => {
  // 使用 arco-design 的确认对话框
  $dialog.confirm({
    title: '确认',
    content: '确定要停止所有服务器吗？',
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      try {
        const data = await stopAllServers()
        if (data.success) {
          $message.success('所有服务器已停止')
        } else {
          console.error('停止所有服务器失败:', data.error)
          $message.error('停止所有服务器失败: ' + data.error)
        }
      } catch (error) {
        console.error('停止所有服务器失败:', error)
        $message.error('停止所有服务器失败: ' + error.message)
      }
    }
  })
}

// 更新服务器
const updateServerHandler = async () => {
  // 使用 arco-design 的确认对话框
  $dialog.confirm({
    title: '确认',
    content: '确定要更新服务器吗？这可能需要一些时间。',
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      try {
        const data = await updateServer()
        if (data.success) {
          $message.success('服务器更新成功')
        } else {
          console.error('更新服务器失败:', data.error)
          $message.error('更新服务器失败: ' + data.error)
        }
      } catch (error) {
        console.error('更新服务器失败:', error)
        $message.error('更新服务器失败: ' + error.message)
      }
    }
  })
}

// 创建备份
const createBackupHandler = async () => {
  if (!backupForm.instance || !backupForm.worldFolder) return
  
  try {
    const data = await createBackup(backupForm.instance, backupForm.worldFolder)
    if (data.success) {
      $message.success('备份创建成功')
      // 刷新备份列表
      await fetchBackups()
      // 清空输入
      backupForm.worldFolder = ''
    } else {
      console.error('创建备份失败:', data.error)
      $message.error('创建备份失败: ' + data.error)
    }
  } catch (error) {
    console.error('创建备份失败:', error)
    $message.error('创建备份失败: ' + error.message)
  }
}

// 恢复备份
const restoreBackupHandler = async (backupFile) => {
  if (!backupForm.instance) {
    $message.warning('请先选择要恢复备份的实例')
    return
  }
  
  // 使用 arco-design 的确认对话框
  $dialog.confirm({
    title: '确认',
    content: `确定要将备份 "${backupFile}" 恢复到实例 "${backupForm.instance}" 吗？`,
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      try {
        const data = await restoreBackup(backupForm.instance, backupFile)
        if (data.success) {
          $message.success('备份恢复成功')
        } else {
          console.error('恢复备份失败:', data.error)
          $message.error('恢复备份失败: ' + data.error)
        }
      } catch (error) {
        console.error('恢复备份失败:', error)
        $message.error('恢复备份失败: ' + error.message)
      }
    }
  })
}

// 发送 RCON 命令
const sendRconCommandHandler = async () => {
  if (!rconForm.instance || !rconForm.command) return
  
  try {
    const data = await sendRCONCommand(rconForm.instance, rconForm.command)
    if (data.success) {
      rconResponse.value = data.data.response
      $message.success('命令发送成功')
    } else {
      console.error('发送 RCON 命令失败:', data.error)
      rconResponse.value = `错误: ${data.error}`
      $message.error('发送 RCON 命令失败: ' + data.error)
    }
  } catch (error) {
    console.error('发送 RCON 命令失败:', error)
    rconResponse.value = `错误: ${error.message}`
    $message.error('发送 RCON 命令失败: ' + error.message)
  }
}

// 组件挂载时获取数据
onMounted(() => {
  fetchInstances()
  fetchBackups()
})
</script>

<style scoped>
.server-control {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.main-card {
  flex: 1;
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
  overflow: auto;
}

.section-card {
  margin-top: 20px;
}

.backup-list h4 {
  margin: 20px 0 10px 0;
}

.rcon-response h4 {
  margin: 20px 0 10px 0;
}

:deep(.arco-alert-info) {
  background-color: #f0f9ff;
  border-color: #337ecc;
}

:deep(pre) {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>