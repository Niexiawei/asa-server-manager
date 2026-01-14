<template>
  <div class="status-history-content" ref="statusContainerRef">
    <div v-if="loading" class="loading-container">
      <a-spin/>
    </div>
    <div v-else-if="statusHistory.length === 0" class="no-data">
      暂无历史状态数据
    </div>
    <a-list
        v-else
        :data="statusHistory"
        :bordered="false"
        :virtualListProps="{
          height: height,
          itemHeight: 46,
          overscanCount: 5
        }"
    >
      <template #item="{ item,index }">
        <a-list-item :key="index">
          <a-list-item-meta>
            <template #avatar>
              <a-tag class="status-tag" :color="getTagColor(item.status)">
                {{ getStatusText(item.status) }}
              </a-tag>
            </template>
            <template #description>
              <div class="status-description">
                <div v-if="item.error_message" class="status-error">
                  {{ item.error_message }}
                </div>
                <div class="status-time">
                  {{ formatTime(item.operation_time) }}
                </div>
              </div>
            </template>
            <template #title>
              {{ getStatusText(item.status) }}
            </template>
          </a-list-item-meta>
        </a-list-item>
      </template>
    </a-list>
  </div>
</template>

<script setup>
import {ref, onMounted, watch, useTemplateRef, computed, onUnmounted} from 'vue';
import {getInstanceStatus} from '@/apis/api';
import {useElementSize} from "@vueuse/core";
import {onAnyServerEvent} from '@/utils/wsManager';
import {serverStore} from '@/store/serverStore';

const statusHistory = ref([]);
const loading = ref(false);
let unlistenServerEvent = null;

const props = defineProps({
  instanceName: {
    type: String,
    required: true
  }
});

const el = useTemplateRef('statusContainerRef')
const {width, height} = useElementSize(el)

const getStatusText = (status) => {
  const statusMap = {
    'start_initialization': '初始化',
    'starting': '正在启动',
    'started': '已启动',
    'stopping': '正在停止',
    'stopped': '已停止',
    'start_failed': '启动失败',
    'stop_failed': '停止失败',
    'restart_failed': '重启失败',
    'restart': '重启中',
    'start_initialization_successful':'初始化成功'
  };
  return statusMap[status] || status;
};

const getTagColor = (status) => {
  const statusColorMap = {
    'start_initialization': 'blue',
    'starting': 'green',
    'started': 'green',
    'stopping': 'orange',
    'stopped': '#86909c',
    'start_failed': 'red',
    'stop_failed': 'red',
    'restart_failed': 'red',
    'restart': 'purple'
  };
  return statusColorMap[status] || 'gray';
};

const isFailedStatus = (status) => {
  return status.includes('_failed');
};

const formatTime = (timeString) => {
  try {
    const date = new Date(timeString);
    return date.toLocaleString('zh-CN');
  } catch (e) {
    return timeString;
  }
};

const loadStatusHistory = async () => {
  if (!props.instanceName) return;

  loading.value = true;
  try {
    const response = await getInstanceStatus(props.instanceName);
    if (response.success && response.data && response.data.status_history) {
      statusHistory.value = response.data.status_history;
    } else {
      statusHistory.value = [];
    }
  } catch (error) {
    console.error('获取实例历史状态失败:', error);
    statusHistory.value = [];
  } finally {
    loading.value = false;
  }
};

// 监听 WebSocket 事件，自动刷新历史状态
const handleServerEvent = (event) => {
  const {event_type, instance_name} = event;
  
  // 只有实例相关的状态变化事件才需要刷新列表
  const statusChangeEvents = [
    'server_starting',
    'server_started',
    'server_stopping',
    'server_stopped',
    'server_start_failed',
    'server_stop_failed',
    'server_restart_failed',
    'restart'
  ];
  
  // 如果是目标实例的状态变化事件，刷新历史列表
  if (instance_name === props.instanceName && statusChangeEvents.includes(event_type)) {
    loadStatusHistory();
  }
};

watch(() => props.instanceName, () => {
  loadStatusHistory();
}, {immediate: true});

onMounted(() => {
  loadStatusHistory();
  // 监听 WebSocket 事件
  unlistenServerEvent = onAnyServerEvent(handleServerEvent);
});

onUnmounted(() => {
  // 移除事件监听
  if (unlistenServerEvent && typeof unlistenServerEvent === 'function') {
    unlistenServerEvent();
  }
});

</script>

<style scoped lang="less">
.status-history-title {
  font-weight: 600;
  font-size: 14px;
  color: #333;
}

.status-history-content {
  height: 100%;

  .status-tag {
    width: 50px;
    height: 50px;
    border-radius: 8px;
    white-space: wrap;
    line-height: 1;
    text-align: center;
  }
}

.loading-container {
  display: flex;
  justify-content: center;
  align-items: center;
  height: 150px;
}

.no-data {
  text-align: center;
  color: #999;
  padding: 20px;
  font-style: italic;
}

.status-description {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.status-time {
  font-size: 12px;
  color: #666;
}

.status-error {
  font-size: 12px;
  color: #f5222d;
  word-break: break-word;
}

:deep(.arco-list-item) {
  padding: 8px 12px !important;
}

:deep(.arco-list-item-meta) {
  align-items: flex-start;
}

:deep(.arco-list-item-meta-content) {
  flex: 1;
}

:deep(.arco-list-item-meta-description) {
  margin-top: 4px;
}

</style>