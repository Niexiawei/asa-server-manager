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
          <masonry-wall
            :items="instances"
            :ssr-columns="2"
            :column-width="800"
            :gap="10"
          >
            <template #default="{ item: instance }">
              <a-card
                  class="instance-item"
                  :bordered="true"
                  :class="'instance-card'"
                  :title="renderInstanceTitle(instance)"
              >
                <template #extra>
                  <a-link @click="viewInstanceDetail(instance.name)">查看详情</a-link>
                </template>
                <a-card-meta>
                  <template #description>
                    <!-- 左右布局：服务器配置参数 | 资源占用 -->
                    <div class="instance-content">
                      <!-- 左侧：服务器配置参数 -->
                      <div class="instance-info">
                        <div class="section-title">服务器配置</div>
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
                        <template v-if="instance.config?.ModIDs && instance.config?.ModIDs.length > 0">
                          <a-tooltip :content="instance.config?.ModIDs">
                            <div class="info-item info-item-modid">
                              <span class="label">Mod ID:</span>
                              <span class="value">{{ instance.config?.ModIDs || '-' }}</span>
                            </div>
                          </a-tooltip>
                        </template>
                        <div v-else class="info-item info-item-modid">
                          <span class="label">Mod ID:</span>
                          <span class="value">{{ instance.config?.ModIDs || '-' }}</span>
                        </div>
                        <div class="info-item info-item-custom" v-if="instance.config?.CustomStartParameters">
                          <span class="label">自定义参数:</span>
                          <span class="value">{{ instance.config.CustomStartParameters }}</span>
                        </div>
                      </div>

                      <!-- 右侧：资源占用 -->
                      <resource-monitor
                          class="resource-info"
                          :instance-name="instance.name"
                          :is-running="instance.isStartingOrRunning || false"
                      />
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
                  <a-dropdown-button
                      @click="viewInstanceLogs(instance.name)"
                      type="primary"
                      size="small"
                  >
                    查看日志
                    <template #content>
                      <a-doption @click="restartInstance(instance.name)" :disabled="!instance.running">
                        重启
                      </a-doption>
                      <a-doption @click="deleteInstanceHandler(instance.name)" :disabled="instance.running">
                        删除
                      </a-doption>
                      <a-doption @click="openSyncModal(instance.name)">
                        同步配置
                      </a-doption>
                    </template>
                  </a-dropdown-button>
                </template>
              </a-card>
            </template>
          </masonry-wall>
        </div>
      </a-spin>
    </a-card>

    <!-- 日志查看弹窗 -->
    <a-modal
        v-model:visible="logModalVisible"
        :title="`${selectedInstanceName} - 实时日志`"
        width="1000px"
        :body-style="{height: '600px'}"
        @cancel="logViewerClose"
        :footer="false"
    >
      <div style="height: 100%; display: flex; flex-direction: column;">
        <log-viewer
            ref="logViewerRef"
            v-if="selectedInstanceName"
            :instance-name="selectedInstanceName"
            style="flex: 1;"
        />
      </div>
    </a-modal>

    <!-- 配置同步弹窗 -->
    <sync-config-modal
        :visible="syncModalVisible"
        :instances="instances"
        :source-instance="selectedSourceInstance"
        @update:visible="syncModalVisible = $event"
        @sync-complete="handleSyncComplete"
    />

    <!-- 创建实例弹窗 -->
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
import {ref, reactive, onMounted, onUnmounted, watch, computed, nextTick, h} from 'vue'
import {useRouter} from 'vue-router'
import {
  listInstances,
  createInstance,
  startServer,
  stopServer,
  restartServer,
  restartServerSSE,
  deleteInstance
} from '@/apis/api.js'
import {Modal, Button, Message} from '@arco-design/web-vue';
import {IconCheck, IconClose} from '@arco-design/web-vue/es/icon';
import {serverStore, updateInstancesInStore} from '@/store/serverStore.js'
import WSStatusIndicator from '@/components/WSStatusIndicator.vue'
import LogViewer from '@/components/LogViewer.vue'
import SyncConfigModal from '@/components/SyncConfigModal.vue'
import ResourceMonitor from '@/components/ResourceMonitor.vue'
import MasonryWall from '@yeger/vue-masonry-wall'

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

const logViewerRef = ref()
const syncModalVisible = ref(false)
const selectedSourceInstance = ref('')

// 渲染实例标题，在名称前添加状态图标
const renderInstanceTitle = (instance) => {
  return h('div', {style: {display: 'flex', alignItems: 'center', gap: '8px'}}, [
    h(instance.running ? IconCheck : IconClose, {style: {color: instance.running ? '#00b42a' : '#f53f3f'}}),
    h('span', instance.name)
  ])
}

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

function logViewerClose() {
  selectedInstanceName.value = ''
  if (logViewerRef && logViewerRef.value && logViewerRef.value.isStreaming) {
    logViewerRef.value.stopLogStream()
  }
}

onUnmounted(() => {
  // 停止日志监听
  if (logViewerRef.value && logViewerRef.value.isStreaming) {
    logViewerRef.value.stopLogStream()
  }
})

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
          Message.success(data.message || `实例 "${name}" 启动成功`)
          // 更新本地状态
          const instance = instances.value.find(inst => inst.name === name)
          if (instance) {
            instance.running = true
          }
        } else {
          Message.error(data.error || `实例 "${name}" 启动失败`)
          console.error('启动实例失败:', data.error)
        }
      } catch (error) {
        Message.error(`启动实例失败: ${error.message}`)
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
          Message.success(data.message || `实例 "${name}" 停止成功`)
          // 更新本地状态
          const instance = instances.value.find(inst => inst.name === name)
          if (instance) {
            instance.running = false
          }
        } else {
          Message.error(data.error || `实例 "${name}" 停止失败`)
          console.error('停止实例失败:', data.error)
        }
      } catch (error) {
        Message.error(`停止实例失败: ${error.message}`)
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
              Message.error('重启实例失败')
            },
            // onComplete 回调 - 重启完成
            () => {
              console.log('Server restart completed')
              Message.success('实例重启成功')
            }
        )
      } catch (error) {
        console.error('重启实例失败:', error)
        Message.error('重启实例失败')
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
  let instance = instances.value.find(item => item.name == name)
  if (instance?.running) {
    nextTick(() => {
      logViewerRef.value.startLogStream()
    })
  }
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

// 打开配置同步弹窗
const openSyncModal = (instanceName) => {
  selectedSourceInstance.value = instanceName
  syncModalVisible.value = true
}

// 处理同步完成事件
const handleSyncComplete = (result) => {
  // 可在此添加配置同步完成后的处理逻辑
  console.log('Config sync completed:', result)
  fetchInstances()
}

</script>

<style scoped lang="less">
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

.instance-content {
  display: flex;
  gap: 24px;
  justify-content: center;

  .instance-info {
    width: 60%;
  }

  .resource-info {
    width: 40%;
  }
}

.instance-info {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.server-manager {
  height: 100%;
  display: flex;
  flex-direction: column;
}

:deep(.main-card) {
  .arco-card-body {
    height: calc(100% - 78px) !important;
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

}

// 移除绿色边框样式，改为使用图标标记状态

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

.instance-content {
  display: flex;
  gap: 24px;
}

.instance-info {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.section-title {
  font-weight: 700;
  color: #1d39c4;
  font-size: 16px;
  margin-bottom: 8px;
  padding-bottom: 8px;
  border-bottom: 2px solid #1d39c4;
}

.info-item-custom {
  height: auto !important;
  align-items: start !important;

  .label {
    padding: 12px 0;
    box-sizing: border-box;
  }

  .value {
    line-height: 20px;
    padding: 12px 0;
    box-sizing: border-box;
  }
}

.info-item {
  display: flex;
  align-items: center;
  height: 32px;
  padding: 0 8px;
  background-color: #f5f5f5;
  border-radius: 4px;
}

.info-item-modid {
  .value {
    white-space: nowrap;
    text-overflow: ellipsis;
    overflow: hidden;
  }
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

.checkbox-label {
  display: flex;
  align-items: center;
  font-size: 14px;
}
</style>