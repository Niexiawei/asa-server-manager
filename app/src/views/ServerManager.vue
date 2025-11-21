<template>
  <div class="server-manager">
    <!-- WebSocket 连接状态指示器 -->
    <WSStatusIndicator/>

    <!-- 实例列表 -->
    <a-card :bordered="false" class="main-card">
      <template #extra>
        <a-button type="primary" @click="showCreateModal = true">新建实例</a-button>
      </template>
      <a-spin :loading="loading" style="width: 100%;height: 100%">
        <a-empty v-if="instances.length === 0" description="暂无实例，请创建新实例"/>
        <div v-else class="instance-list">
          <a-row :gutter="20">
            <a-col :span="12" v-for="instance in instances" :key="instance.name">
              <a-card
                  class="instance-item"
                  :bordered="true"
                  :class="['instance-card', { running: instance.running }]"
                  :title="instance.name"
              >
                <template #extra>
                  <a-link @click="viewInstanceDetail(instance.name)">查看详情</a-link>
                </template>
                <a-card-meta>
                  <template #description>
                    <div class="instance-info">
                      <div class="info-item" v-if="instance.config?.ServerName">
                        <span class="label">服务器名称:</span>
                        <span class="value">{{ instance.config.ServerName }}</span>
                      </div>
                      <div class="info-item">
                        <span class="label">状态:</span>
                        <a-tag :color="instance.running ? 'green' : 'gray'">{{
                            instance.running ? '运行中' : '已停止'
                          }}
                        </a-tag>
                      </div>
                      <div class="info-item" v-if="instance.config?.MapName">
                        <span class="label">地图:</span>
                        <span class="value">{{ instance.config.MapName }}</span>
                      </div>
                      <div class="info-item" v-if="instance.config?.Port">
                        <span class="label">端口:</span>
                        <span class="value">{{ instance.config.Port }}</span>
                      </div>
                      <div class="info-item" v-if="instance.config?.RCONPort">
                        <span class="label">RCON端口:</span>
                        <span class="value">{{ instance.config.RCONPort }}</span>
                      </div>
                      <div class="info-item" v-if="instance.config?.QueryPort">
                        <span class="label">查询端口:</span>
                        <span class="value">{{ instance.config.QueryPort }}</span>
                      </div>
                      <div class="info-item">
                        <span class="label">Mod ID:</span>
                        <span class="value">{{ instance.config?.ModIDs || '-' }}</span>
                      </div>
                      <div class="info-item" v-if="instance.config?.CustomStartParameters">
                        <span class="label">自定义参数:</span>
                        <span class="value">{{ instance.config.CustomStartParameters }}</span>
                      </div>
                    </div>
                  </template>
                </a-card-meta>
                <template #actions>
                  <a-button
                      @click="startInstance(instance.name)"
                      :disabled="instance.running"
                      type="primary"
                      size="small"
                  >
                    启动
                  </a-button>
                  <a-button
                      @click="stopInstance(instance.name)"
                      :disabled="!instance.running"
                      status="warning"
                      size="small"
                  >
                    停止
                  </a-button>
                  <a-button
                      @click="restartInstance(instance.name)"
                      :disabled="!instance.running"
                      status="success"
                      size="small"
                  >
                    重启
                  </a-button>
                  <a-button
                      @click="deleteInstanceHandler(instance.name)"
                      status="danger"
                      size="small"
                  >
                    删除
                  </a-button>
                  <a-button
                      @click="viewInstanceLogs(instance.name)"
                      type="primary"
                      size="small"
                  >
                    查看日志
                  </a-button>
                </template>
              </a-card>
            </a-col>
          </a-row>
        </div>
      </a-spin>
    </a-card>

    <!-- 日志查看弹窗 -->
    <a-modal
        v-model:visible="logModalVisible"
        :title="`${selectedInstanceName} - 实时日志`"
        width="1000px"
        :body-style="{height: '600px'}"
        @cancel="selectedInstanceName = ''"
    >
      <div style="height: 100%; display: flex; flex-direction: column;">
        <log-viewer 
            v-if="selectedInstanceName"
            :instance-name="selectedInstanceName"
            style="flex: 1;"
        />
      </div>
    </a-modal>
    <a-modal
        v-model:visible="showCreateModal"
        title="创建新实例"
        @ok="createInstanceHandler"
        @cancel="showCreateModal = false"
    >
      <a-form :model="form">
        <a-form-item field="instanceName" label="实例名称">
          <a-input
              v-model="form.instanceName"
              placeholder="输入实例名称"
          />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>

<script setup>
import {ref, reactive, onMounted, onUnmounted, watch, computed} from 'vue'
import {useRouter} from 'vue-router'
import {listInstances, createInstance, startServer, stopServer, restartServer, restartServerSSE, deleteInstance} from '@/apis/api.js'
import {Modal, Button} from '@arco-design/web-vue';
import {serverStore, updateInstancesInStore} from '@/store/serverStore.js'
import WSStatusIndicator from '@/components/WSStatusIndicator.vue'
import LogViewer from '@/views/ServerController/components/LogViewer.vue'

// 状态管理
const router = useRouter()
const instances = ref([])
const loading = ref(false)
const showCreateModal = ref(false)
const logModalVisible = ref(false)
const selectedInstanceName = ref('')
const form = reactive({
  instanceName: ''
})

// 获取实例列表
const fetchInstances = async () => {
  loading.value = true
  try {
    const data = await listInstances()
    if (data.success) {
      instances.value = data.data.instances
      // 同时更新全局状态存储
      updateInstancesInStore(instances.value)
    } else {
      console.error('获取实例列表失败:', data.error)
    }
  } catch (error) {
    console.error('获取实例列表失败:', error)
  } finally {
    loading.value = false
  }
}

// 创建实例
const createInstanceHandler = async ({values, errors}) => {
  if (errors || !form.instanceName.trim()) return
  try {
    const data = await createInstance(form.instanceName)
    if (data.success) {
      // 清空输入
      form.instanceName = ''
      // 关闭弹窗
      showCreateModal.value = false
      // 重新获取实例列表
      await fetchInstances()
    } else {
      console.error('创建实例失败:', data.error)
    }
  } catch (error) {
    console.error('创建实例失败:', error)
  }
}

// 启动实例
const startInstance = async (name) => {
  Modal.confirm({
    title: '提示',
    content: `确定要启动实例 "${name}" 吗？`,
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      try {
        const data = await startServer(name)
        if (data.success) {
          // 更新本地状态
          const instance = instances.value.find(inst => inst.name === name)
          if (instance) {
            instance.running = true
          }
        } else {
          console.error('启动实例失败:', data.error)
        }
      } catch (error) {
        console.error('启动实例失败:', error)
      }
    }
  })
}

// 停止实例
const stopInstance = async (name) => {
  Modal.confirm({
    title: '提示',
    content: `确定要停止实例 "${name}" 吗？`,
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      try {
        const data = await stopServer(name)
        if (data.success) {
          // 更新本地状态
          const instance = instances.value.find(inst => inst.name === name)
          if (instance) {
            instance.running = false
          }
        } else {
          console.error('停止实例失败:', data.error)
        }
      } catch (error) {
        console.error('停止实例失败:', error)
      }
    }
  })
}

// 重启实例
const restartInstance = async (name) => {
  Modal.confirm({
    title: '提示',
    content: `确定要重启实例 "${name}" 吗？`,
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      try {
        // 使用 SSE 方式调用重启
        restartServerSSE(
          name,
          // onMessage 回调 - 接收实时进度消息
          (message) => {
            console.log('Restart progress:', message)
            // 可选：在 UI 中显示重启进度
          },
          // onError 回调 - 处理错误
          (error) => {
            console.error('重启实例失败:', error)
          },
          // onComplete 回调 - 重启完成
          () => {
            console.log('Server restart completed')
            // 重启不改变本地状态，等待后续自动更新
          }
        )
      } catch (error) {
        console.error('重启实例失败:', error)
      }
    }
  })
}

// 删除实例
const deleteInstanceHandler = async (name) => {
  // 使用 arco-design 的确认对话框
  Modal.confirm({
    title: '确认',
    content: `确定要删除实例 "${name}" 吗？`,
    okText: '确定',
    cancelText: '取消',
    onOk: async () => {
      try {
        const data = await deleteInstance(name)
        if (data.success) {
          // 从本地列表移除
          instances.value = instances.value.filter(inst => inst.name !== name)
        } else {
          console.error('删除实例失败:', data.error)
        }
      } catch (error) {
        console.error('删除实例失败:', error)
      }
    }
  })
}

// 查看实例日志
const viewInstanceLogs = (name) => {
  selectedInstanceName.value = name
  logModalVisible.value = true
}

// 监听全局服务器状态变化
watch(
    () => serverStore.instances,
    (newInstances) => {
      // 当 WebSocket 接收到事件更新状态时，同步更新本地 instances 数组
      const updatedInstances = Array.from(newInstances.values())
      instances.value = updatedInstances
    },
    {deep: true}
)

// 组件挂载时获取实例列表
onMounted(() => {
  fetchInstances()
})

// 组件卸载时清理
onUnmounted(() => {
  // WebSocket 会保持连接，其他组件可能需要它
})

// 查看实例详情
const viewInstanceDetail = (name) => {
  router.push({
    name: 'InstanceDetail',
    params: {name}
  })
}

// 计算 WebSocket 连接状态显示
const wsStatusText = computed(() => {
  return serverStore.connected ? '实时同步中' : '连接中...'
})
</script>

<style scoped>
.instance-item {
  border-radius: 8px;
  box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1);
}

.instance-list {
  height: 100%;
  overflow-y: auto;
  overflow-x: hidden;

  :deep(.arco-card-header-title) {
    font-size: 24px !important;
    font-weight: bold;
  }
}

.server-manager {
  height: 100%;
  display: flex;
  flex-direction: column;
}

:deep(.main-card) {
  .arco-card-body {
    height: calc(100% - 58px) !important;
  }
}

.main-card {
  flex: 1;
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
  overflow: hidden;
  height: calc(100% - 40px);


}

.instance-card {
  margin-bottom: 20px;
}

.instance-card.running {
  border-color: #42b883;
  border-width: 2px;
}

.ws-status-bar {
  margin-bottom: 16px;
}

.ws-connected-indicator {
  margin-bottom: 12px;
  padding: 8px 16px;
  background-color: #f6ffed;
  border-radius: 4px;
  border-left: 4px solid #52c41a;
}

:deep(.arco-card-header) {
  border-bottom: 1px solid #eee;
}

:deep(.arco-card-actions) {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.instance-info {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.info-item {
  display: flex;
  align-items: center;
  height: 32px;
  padding: 0 8px;
  background-color: #f5f5f5;
  border-radius: 4px;
}

.info-item .label {
  font-weight: 600;
  color: #333;
  min-width: 100px;
  display: inline-block;
  font-size: 14px;
}

.info-item .value {
  color: #666;
  font-size: 14px;
  word-break: break-all;
  flex: 1;
}
</style>