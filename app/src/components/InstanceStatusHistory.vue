<template>
  <div class="status-history-content" ref="statusContainerRef">
    <div v-if="loading" class="loading-container">
      <t-loading size="small"/>
    </div>
    <div v-else-if="statusHistory.length === 0" class="no-data">
      暂无历史状态数据
    </div>
    <t-list
        v-else
        :data="statusHistory"
        :split="false"
        :style="{
          height: `${height}px`
        }"
        :scroll="{
          type: 'virtual',
          rowHeight: 46,
          bufferSize: 10,
          threshold: 10
        }"
    >
      <t-list-item v-for="(item,index) in statusHistory" :key="index">
        <div class="status-item" :key="index">
          <t-tag class="status-tag" :theme="getTagTheme(item.status)">
            {{ getStatusText(item.status) }}
          </t-tag>
          <div class="status-content">
            <div class="status-title">
              {{ getStatusText(item.status) }}
            </div>
            <div class="status-description">
              <div v-if="item.error_message" class="status-error">
                {{ item.error_message }}
              </div>
              <div class="status-time">
                {{ formatTime(item.operation_time) }}
              </div>
            </div>
          </div>
        </div>
      </t-list-item>
    </t-list>
  </div>
</template>

<script setup lang="jsx">
import {ref, onMounted, watch, useTemplateRef, computed, onUnmounted} from 'vue';
import {getInstanceStatus} from '@/apis/api';
import {useElementSize} from "@vueuse/core";
import {onAnyServerEvent} from '@/utils/wsManager';

const ellipsisState = {
  row: 1,
  suffix: <ChevronDownIcon />,
  expandable: true,
  collapsible: true,
};

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
    'restarting': '重启中',
    'restarted': '运行中',
    'start_initialization_successful': '初始化成功'
  };
  return statusMap[status] || status;
};

const getTagTheme = (status) => {
  const statusThemeMap = {
    'start_initialization': 'primary',
    'starting': 'success',
    'started': 'success',
    'stopping': 'warning',
    'stopped': 'default',
    'start_failed': 'danger',
    'stop_failed': 'danger',
    'restart_failed': 'danger',
    'restarting': 'warning',
    'restarted': 'success'
  };
  return statusThemeMap[status] || 'default';
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
  console.log(event)
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
    'server_restarting',
    'server_restarted'
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
    flex-shrink: 0;
  }
}

.status-item {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 8px 0;
}

.status-content {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.status-title {
  font-weight: 600;
  color: #333;
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


</style>
