<template>
  <div class="status-history-content">
    <div v-if="loading" class="loading-container">
      <a-spin/>
    </div>
    <div v-else-if="statusHistory.length === 0" class="no-data">
      暂无历史状态数据
    </div>
    <div v-else class="history-list">
      <div
          v-for="(item, index) in statusHistory"
          :key="index"
          class="status-item"
          :class="{ 'status-item-running': item.status === 'started', 'status-item-stopped': item.status === 'stopped' }"
      >
        <div class="status-time">{{ formatTime(item.operation_time) }}</div>
        <div class="status-value">
            <span
                class="status-badge"
                :class="getStatusClass(item.status)"
            >
              {{ getStatusText(item.status) }}
            </span>
        </div>
        <div v-if="item.error_message" class="status-error">
          {{ item.error_message }}
        </div>
      </div>
    </div>
  </div>
</template>

<script>
import {ref, onMounted, watch} from 'vue';
import {getInstanceStatus} from '../apis/api';

export default {
  name: 'InstanceStatusHistory',
  props: {
    instanceName: {
      type: String,
      required: true
    }
  },
  setup(props) {
    const statusHistory = ref([]);
    const loading = ref(false);

    const getStatusText = (status) => {
      const statusMap = {
        'start_initialization': '启动初始化',
        'starting': '正在启动',
        'started': '已启动',
        'stopping': '正在停止',
        'stopped': '已停止',
        'start_failed': '启动失败',
        'stop_failed': '停止失败',
        'restart_failed': '重启失败',
        'restart': '重启中'
      };
      return statusMap[status] || status;
    };

    const getStatusClass = (status) => {
      const statusClassMap = {
        'start_initialization': 'status-initializing',
        'starting': 'status-starting',
        'started': 'status-running',
        'stopping': 'status-stopping',
        'stopped': 'status-stopped',
        'start_failed': 'status-error',
        'stop_failed': 'status-error',
        'restart_failed': 'status-error',
        'restart': 'status-restarting'
      };
      return statusClassMap[status] || 'status-unknown';
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

    watch(() => props.instanceName, () => {
      loadStatusHistory();
    }, {immediate: true});

    onMounted(() => {
      loadStatusHistory();
    });

    return {
      statusHistory,
      loading,
      getStatusText,
      getStatusClass,
      formatTime
    };
  }
};
</script>

<style scoped lang="less">
.status-history-title {
  font-weight: 600;
  font-size: 14px;
  color: #333;
}

.status-history-content {
  height: 100%;
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

.history-list {
  height: 100%;
  overflow-y: auto;
}

.status-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 0;
  border-bottom: 1px solid #f0f0f0;
}

.status-item:last-child {
  border-bottom: none;
}

.status-time {
  flex: 1;
  font-size: 12px;
  color: #666;
  min-width: 120px;
}

.status-value {
  flex: 1;
  text-align: center;
}

.status-error {
  flex: 2;
  font-size: 12px;
  color: #f5222d;
  text-align: right;
  word-break: break-word;
  padding-left: 10px;
}

.status-badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 12px;
  font-size: 12px;
  font-weight: normal;
  color: #fff;
}

.status-initializing {
  background-color: #1890ff;
}

.status-starting {
  background-color: #52c41a;
}

.status-running {
  background-color: #52c41a;
}

.status-stopping {
  background-color: #faad14;
}

.status-stopped {
  background-color: #bfbfbf;
}

.status-error {
  background-color: #f5222d;
}

.status-restarting {
  background-color: #722ed1;
}

.status-unknown {
  background-color: #8c8c8c;
}
</style>