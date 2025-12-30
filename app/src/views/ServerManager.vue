<template>
  <div class="server-manager">
    <!-- 实例列表 -->
    <a-card :bordered="false" class="main-card">
      <template #extra>
        <a-button type="primary" @click="showCreateModal = true">新建实例</a-button>
      </template>
      <a-spin :loading="loading" style="width: 100%;height: 100%">
        <a-empty v-if="instances.length === 0" description="暂无实例，请创建新实例"/>
        <div v-else class="instance-list scrollbar">
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
                        <div class="info-item info-item-custom info-item-modid">
                          <span class="label">Mod:</span>
                          <span class="value">
                            <template v-if="instance.config?.ModIDs">
                              <template v-for="(modId, index) in instance.config.ModIDs.split(',')" :key="modId">
                                <a-tag
                                    class="mod-tag"
                                    v-if="modId.trim()"
                                    color="arcoblue"
                                    @click="copyModId(modId.trim())"
                                    style="cursor: pointer; display: flex; align-items: center; gap: 4px;"
                                >
                                  {{ getModNameById(modId.trim()) || modId.trim() }}
                                  <icon-copy :style="{fontSize: '12px'}"/>
                                </a-tag>
                              </template>
                            </template>
                            <template v-else>
                              -
                            </template>
                          </span>
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
                      />
                    </div>
                  </template>
                </a-card-meta>
                <template #actions>
                  <a-button
                      @click="startInstance(instance.name)"
                      :disabled="instance.running || instanceLoadingMap.get(instance.name)"
                      :loading="instanceLoadingMap.get(instance.name)"
                      type="primary"
                      size="small"
                  >
                    启动
                  </a-button>
                  <a-button
                      @click="stopInstance(instance.name)"
                      :disabled="!instance.running"
                      :loading="operationLoadingMap.get(`${instance.name}-stop`)"
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
                      <a-doption @click="restartInstance(instance.name)"
                                 :disabled="!instance.running || operationLoadingMap.get(`${instance.name}-restart`)">
                        <span v-if="operationLoadingMap.get(`${instance.name}-restart`)">
                          <icon-loading spin/> 重启中...
                        </span>
                        <span v-else>重启</span>
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
        :unmountOnClose="true"
    >
      <div style="height: 100%; display: flex; flex-direction: column;">
        <log-viewer
            ref="logViewerRef"
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
defineOptions({
  name: 'ServerManager'
})
import {
  ref,
  reactive,
  onMounted,
  onUnmounted,
  watch,
  computed,
  nextTick,
  h,
  inject,
  onDeactivated,
  onActivated
} from 'vue'
import {useRouter} from 'vue-router'
import {
  listInstances,
  createInstance,
  startServer,
  stopServer,
  restartServer,
  restartServerSSE,
  deleteInstance,
  getModInfo
} from '@/apis/api.js'
import {Modal, Button, Message, Notification} from '@arco-design/web-vue';
import {IconCheck, IconClose, IconLoading, IconCopy} from '@arco-design/web-vue/es/icon';
import {initServer, serverStore, updateInstancesInStore} from '@/store/serverStore.js'
import {onServerEvent} from '@/utils/wsManager.js'
import LogViewer from '@/components/LogViewer.vue'
import SyncConfigModal from '@/components/SyncConfigModal.vue'
import ResourceMonitor from '@/components/ResourceMonitor.vue'
import MasonryWall from '@yeger/vue-masonry-wall'

const addTab = inject('addTab')
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

// Mod信息
const modInfo = ref([])
const modInfoLoading = ref(false)

// 实例的 loading 状态
const instanceLoadingMap = ref(new Map())
// 单独的操作 loading 状态
const operationLoadingMap = ref(new Map())

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
    instances.value = await initServer()
  } catch (error) {
    console.error('获取实例列表失败:', error)
    Message.error("获取实例列表失败:" + error)
  } finally {
    loading.value = false
  }
}

// 获取Mod信息
const fetchModInfo = async () => {
  modInfoLoading.value = true
  try {
    const data = await getModInfo()
    if (data.success) {
      modInfo.value = data.data || []
    } else {
      console.error('获取Mod信息失败:', data.error)
      modInfo.value = []
    }
  } catch (error) {
    console.error('获取Mod信息失败:', error)
    modInfo.value = []
  } finally {
    modInfoLoading.value = false
  }
}

// 复制 Mod ID 到剪切板
const copyModId = async (modId) => {
  try {
    await navigator.clipboard.writeText(modId)
    Message.success(`${getModNameById(modId)}:已复制到剪切板`)
  } catch (error) {
    console.error('复制失败:', error)
    Message.error('复制失败')
  }
}

const getModNameById = (modId) => {
  if (!modId) return null

  const mod = modInfo.value.find(m => m.id === modId)
  return mod ? mod.name : null
}

function logViewerClose() {
  selectedInstanceName.value = ''
  // 日志监听已通过 LogViewer 组件内部 watch 自动管理
  // 关闭日志窗布84LogViewer会自动取消订阅
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
      // 设置 loading 状态
      instanceLoadingMap.value.set(name, true)

      try {
        // 使用 SSE 方式调用启动
        await startServer(
            name,
            // onMessage 回调 - 接收实时进度消息
            (message) => {
              //console.log('Start progress:', message)
            },
            // onError 回调 - 处理错误
            (error) => {
              // 启动失败：设置实例状态为未启动
              const instance = instances.value.find(inst => inst.name === name)
              if (instance) {
                instance.running = false
              }

              Notification.error({
                title: `实例 "${name}" 启动失败`,
                content: error.message || `实例 "${name}" 启动失败`,
                duration: 0, // 0 表示不自动隐藏
                closable: true
              })

              console.error('启动实例失败:', error)
            },
            // onComplete 回调 - 启动完成
            () => {
              Message.success(`实例 "${name}" 启动成功`)
              // 更新本地状态
              const instance = instances.value.find(inst => inst.name === name)
              if (instance) {
                instance.running = true
              }
            }
        )
      } catch (error) {
        Message.error(`启动实例失败: ${error.message}`)
        console.error('启动实例失败:', error)
      } finally {
        // 清除 loading 状态
        instanceLoadingMap.value.set(name, false)
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
      // 设置停止操作 loading 状态
      operationLoadingMap.value.set(`${name}-stop`, true)

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
      } finally {
        // 清除停止操作 loading 状态
        operationLoadingMap.value.set(`${name}-stop`, false)
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
      // 设置重启操作 loading 状态
      operationLoadingMap.value.set(`${name}-restart`, true)

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
              // 清除重启操作 loading 状态
              operationLoadingMap.value.set(`${name}-restart`, false)
            },
            // onComplete 回调 - 重启完成
            () => {
              console.log('Server restart completed')
              Message.success('实例重启成功')
              // 清除重启操作 loading 状态
              operationLoadingMap.value.set(`${name}-restart`, false)
            }
        )
      } catch (error) {
        console.error('重启实例失败:', error)
        Message.error('重启实例失败')
        // 清除重启操作 loading 状态
        operationLoadingMap.value.set(`${name}-restart`, false)
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
  // 日志监听已通过 LogViewer 组件内部 watch 自动管理
}

// 监听全局服务器状态变化
watch(
    () => serverStore.instances,
    (newInstances) => {
      // 更新现有实例对象，避免数组引用变化导致重绘
      newInstances.forEach((storeInstance, instanceName) => {
        const localInstance = instances.value.find(inst => inst.name === instanceName)
        if (localInstance) {
          // 只更新状态字段，保持数组引用不变
          Object.assign(localInstance, {
            running: storeInstance.running,
            status: storeInstance.status,
            isStartingOrRunning: storeInstance.isStartingOrRunning,
            message: storeInstance.message,
            error: storeInstance.error
          })
        }
      })
    },
    {deep: true}
)

// 监听游戏日志路径事件已移转到 LogViewer 组件内部管理
// LogViewer 会辅地根据实例是否运行来自动开启/停止监听

// 组件挂载时获取实例列表
onActivated(async () => {
  await fetchInstances()
  await fetchModInfo()
})

// 组件卸载时清理
onDeactivated(() => {
  // WebSocket 会保持连接，其他组件可能需要它
})

// 窗口打开时， LogViewer 组件会自动根据实例状态开启/停止监听
watch(
    () => logModalVisible.value,
    (visible) => {
      // LogViewer 组件会自动处理
    }
)

// 查看实例详情
const viewInstanceDetail = (name) => {
  const title = name
  const path = `/instance/${name}`
  let params = {
    name
  }
  if (addTab) {
    addTab(title, path, "InstanceDetail", params)
  } else {
    router.push({
      name: 'InstanceDetail',
      params: params
    })
  }
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
  //box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1);
  box-shadow: 0 2px 4px rgba(0, 0, 0, .12), 0 0 6px rgba(0, 0, 0, .04)
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
  min-height: 32px;

  .label {
    height: 32px;
    padding: 0 !important;
    line-height: 32px;
  }

  .value {
    display: flex;
    flex-wrap: wrap;
    padding: 6px 0 !important;
    box-sizing: border-box;

    :deep(.arco-tag) {
      padding: 0 3px;
    }

    .mod-tag {
      margin: 2px;
    }
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