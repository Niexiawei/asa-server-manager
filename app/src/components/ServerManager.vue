<template>
  <div class="server-manager">
    <a-card title="服务器实例管理" :bordered="false" class="main-card">
      <!-- 实例列表 -->
      <a-card title="现有实例" :bordered="false">
        <template #extra>
          <a-button type="primary" @click="showCreateModal = true">新建实例</a-button>
        </template>
        <a-spin :loading="loading" style="width: 100%;">
          <a-empty v-if="instances.length === 0" description="暂无实例，请创建新实例"/>
          <a-row :gutter="20" v-else>
            <a-col :span="12" v-for="instance in instances" :key="instance.name">
              <a-card
                  class="instance-item"
                  :bordered="true"
                  :class="['instance-card', { running: instance.running }]"
              >
                <a-card-meta :title="instance.name">
                  <template #description>
                    <p>状态: {{ instance.running ? '运行中' : '已停止' }}</p>
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
                      @click="deleteInstanceHandler(instance.name)"
                      status="danger"
                      size="small"
                  >
                    删除
                  </a-button>
                </template>
              </a-card>
            </a-col>
          </a-row>
        </a-spin>
      </a-card>
    </a-card>

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
import {ref, reactive, onMounted} from 'vue'
import {listInstances, createInstance, startServer, stopServer, deleteInstance} from '../apis/api.js'

// 状态管理
const instances = ref([])
const loading = ref(false)
const showCreateModal = ref(false)
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

// 停止实例
const stopInstance = async (name) => {
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

// 删除实例
const deleteInstanceHandler = async (name) => {
  // 使用 arco-design 的确认对话框
  $dialog.confirm({
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

// 组件挂载时获取实例列表
onMounted(() => {
  fetchInstances()
})
</script>

<style scoped>
.instance-item {
  border-radius: 8px;
  box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1);
}

.server-manager {
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

.instance-card {
  margin-bottom: 20px;
}

.instance-card.running {
  border-color: #42b883;
  border-width: 2px;
}

:deep(.arco-card-header) {
  border-bottom: 1px solid #eee;
}

:deep(.arco-card-actions) {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>