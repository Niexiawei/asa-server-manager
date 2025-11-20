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
      
      <!-- 服务器更新日志 -->
      <a-modal
        v-model:visible="updateModalVisible"
        title="服务器更新"
        :width="800"
        :footer="false"
        @cancel="handleCancelUpdate"
      >
        <div class="update-log-container">
          <div class="update-log">
            <div 
              v-for="(log, index) in updateLogs" 
              :key="index"
              class="log-line"
            >
              {{ log }}
            </div>
          </div>
          <a-spin v-if="updating" :size="32" class="update-spinner" />
        </div>
      </a-modal>

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
  listInstances,
  startAllServers, 
  stopAllServers, 
  updateServer,
  createBackup,
  listBackups,
  restoreBackup,
  sendRCONCommand
} from '../apis/api.js'
import { Message, Modal } from '@arco-design/web-vue'

// 状态管理
const instances = ref([])
const backups = ref([])
const rconResponse = ref('')
const updateModalVisible = ref(false)
const updating = ref(false)
const updateLogs = ref([])
let updateAbortController = null

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
    const data = await listInstances()
    if (data.success) {
      instances.value = data.data.instances
    } else {
      console.error('获取实例列表失败:', data.error)
      Message.error('获取实例列表失败: ' + (data.error || '未知错误'))
    }
  } catch (error) {
    console.error('获取实例列表失败:', error)
    Message.error('获取实例列表失败: ' + error.message)
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
  Modal.confirm({
    title: '确认',
    content: '确定要启动所有服务器吗？',
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      updateModalVisible.value = true
      updating.value = true
      updateLogs.value = []

      await startAllServers(
        (message) => {
          updateLogs.value.push(message)
          // 自动滚动到下方
          setTimeout(() => {
            const logContainer = document.querySelector('.update-log')
            if (logContainer) {
              logContainer.scrollTop = logContainer.scrollHeight
            }
          }, 0)
        },
        (error) => {
          console.error('启动服务器错误:', error)
          updateLogs.value.push(`错误: ${error.message}`)
          updating.value = false
        },
        () => {
          updating.value = false
          updateLogs.value.push('\n业务处理完成')
          Message.success('所有服务器已启动')
          fetchInstances()
        }
      )
    }
  })
}

// 停止所有服务器
const stopAllServersHandler = async () => {
  Modal.confirm({
    title: '确认',
    content: '确定要停止所有服务器吗？',
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      updateModalVisible.value = true
      updating.value = true
      updateLogs.value = []

      await stopAllServers(
        (message) => {
          updateLogs.value.push(message)
          // 自动滚动到下方
          setTimeout(() => {
            const logContainer = document.querySelector('.update-log')
            if (logContainer) {
              logContainer.scrollTop = logContainer.scrollHeight
            }
          }, 0)
        },
        (error) => {
          console.error('停止服务器错误:', error)
          updateLogs.value.push(`错误: ${error.message}`)
          updating.value = false
        },
        () => {
          updating.value = false
          updateLogs.value.push('\n业务处理完成')
          Message.success('所有服务器已停止')
          fetchInstances()
        }
      )
    }
  })
}

// 更新服务器
const updateServerHandler = async () => {
  // 检查是否有实例正在运行
  const runningInstances = instances.value.filter(i => i.running)
  if (runningInstances.length > 0) {
    Modal.confirm({
      title: '无法更新',
      content: `程序检测到以下实例正在运行：${runningInstances.map(i => i.name).join('、')}。\n\n请先关闭all所有实例后再试`,
      okText: '关闭',
      cancelText: '取消',
      onOk: async () => {
        // 打开日志面板展示停止过程
        updateModalVisible.value = true
        updating.value = true
        updateLogs.value = []

        await stopAllServers(
          (message) => {
            updateLogs.value.push(message)
            setTimeout(() => {
              const logContainer = document.querySelector('.update-log')
              if (logContainer) {
                logContainer.scrollTop = logContainer.scrollHeight
              }
            }, 0)
          },
          (error) => {
            console.error('停止服务器错误:', error)
            updateLogs.value.push(`错误: ${error.message}`)
            updating.value = false
          },
          () => {
            updating.value = false
            updateLogs.value.push('\n所有服务器已停止，现在可以更新')
            updateModalVisible.value = false
          }
        )
      }
    })
    return
  }

  Modal.confirm({
    title: '确认',
    content: '确定要更新服务器吗？这可能需要一些时间。',
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      updateModalVisible.value = true
      updating.value = true
      updateLogs.value = []
      updateAbortController = new AbortController()

      await updateServer(
        (message) => {
          // onMessage callback
          updateLogs.value.push(message)
          // 自动滚动到下方
          setTimeout(() => {
            const logContainer = document.querySelector('.update-log')
            if (logContainer) {
              logContainer.scrollTop = logContainer.scrollHeight
            }
          }, 0)
        },
        (error) => {
          // onError callback
          console.error('更新日志错误:', error)
          updateLogs.value.push(`错误: ${error.message}`)
          updating.value = false
        },
        () => {
          // onComplete callback
          updating.value = false
          updateLogs.value.push('\n更新流程完成1')
          Message.success('服务器更新成功')
          fetchInstances() // 刷新实例点状况
        }
      )
    }
  })
}

// 取消更新
const handleCancelUpdate = () => {
  if (updating.value) {
    Modal.confirm({
      title: '是否中止更新？',
      content: '正在更新中，中止可能会导致服务器状态不一致。',
      okText: '是',
      cancelText: '否',
      onOk: () => {
        if (updateAbortController) {
          updateAbortController.abort()
        }
        updating.value = false
        updateModalVisible.value = false
      }
    })
  } else {
    updateModalVisible.value = false
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

// 发送 RCON 命令
const sendRconCommandHandler = async () => {
  if (!rconForm.instance || !rconForm.command) return
  
  try {
    const data = await sendRCONCommand(rconForm.instance, rconForm.command)
    if (data.success) {
      rconResponse.value = data.data.response
      Message.success('命令发送成功')
    } else {
      console.error('发送 RCON 命令失败:', data.error)
      rconResponse.value = `错误: ${data.error}`
      Message.error('发送 RCON 命令失败: ' + (data.error || '未知错误'))
    }
  } catch (error) {
    console.error('发送 RCON 命令失败:', error)
    rconResponse.value = `错误: ${error.message}`
    Message.error('发送 RCON 命令失败: ' + error.message)
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

.update-log-container {
  position: relative;
  height: 400px;
}

.update-log {
  width: 100%;
  height: 100%;
  border: 1px solid #e5e7eb;
  border-radius: 4px;
  padding: 12px;
  background-color: #fafafa;
  overflow-y: auto;
  font-family: 'Monaco', 'Courier New', monospace;
  font-size: 12px;
  line-height: 1.6;
}

.log-line {
  color: #333;
  word-break: break-all;
  white-space: pre-wrap;
}

.update-spinner {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
}
</style>