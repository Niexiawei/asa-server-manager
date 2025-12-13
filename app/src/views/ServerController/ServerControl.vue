<template>
  <div class="server-control">
    <a-card title="服务器控制面板" :bordered="false" class="main-card">
      <GlobalServerControl
          :instances="instances"
          @refresh="fetchInstances"
      />
      <BackupManagement
          :instances="instances"
      />
      <RconCommand
          :instances="instances"
      />
      <GameConfigSync
          :instances="instances"
      />
      <SyncConfigManagement
          :instances="instances"
      />
    </a-card>
  </div>
</template>

<script setup>
import {ref, onMounted} from 'vue'
import {listInstances} from '@/apis/api.js'
import {Message} from '@arco-design/web-vue'
import GlobalServerControl from '@/views/ServerController/components/GlobalServerControl.vue'
import BackupManagement from '@/views/ServerController/components/BackupManagement.vue'
import RconCommand from '@/views/ServerController/components/RconCommand.vue'
import GameConfigSync from '@/views/ServerController/components/GameConfigSync.vue'
import SyncConfigManagement from '@/views/ServerController/components/SyncConfigManagement.vue'

// 状态管理
const instances = ref([])

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

// 组件挂载时获取数据
onMounted(() => {
  fetchInstances()
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
  border-radius: var(--border-radius-large);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
  overflow: auto;
}
</style>