<template>
  <div class="ws-notification" style="margin-left: auto; padding-right: 20px;">
    <a-popover position="br" trigger="click">
      <template #content>
        <div class="event-popover-content">
          <div class="popover-header">
            <span class="popover-title">事件 ({{ wsEvents.length }})</span>
            <a-button type="text" size="small" @click="clearEvents">清空</a-button>
          </div>
          <div class="event-list">
            <div v-if="wsEvents.length === 0" class="empty-state">
              暂无事件消息
            </div>
            <div v-for="(event, index) in wsEvents" :key="index" class="event-item">
              <span class="event-time">{{ formatTime(event.timestamp) }} -></span>
              <span class="event-type" :class="getEventClass(event.event_type)">{{ event.event_type }}</span>
              <span class="event-data">{{ event.instance_name || '' }} - {{ event.message || '' }}</span>
            </div>
          </div>
        </div>
      </template>
      <a-badge :count="wsEvents.length" :max-count="99" class="bell-icon">
        <a-button type="text" size="large" class="notification-btn">
          <template #icon>
            <icon-notification/>
          </template>
        </a-button>
      </a-badge>
    </a-popover>
  </div>
</template>

<script setup>
import {ref, onMounted, onUnmounted} from 'vue';
import {onAnyServerEvent} from '@/apis/api.js';
import {IconNotification} from '@arco-design/web-vue/es/icon';
import dayjs from "dayjs";

const wsEvents = ref([])
let unlistenEvent = null

// 从 localStorage 加载事件
function loadEventsFromStorage() {
  try {
    const stored = localStorage.getItem('ws_events')
    if (stored) {
      wsEvents.value = JSON.parse(stored)
    }
  } catch (err) {
    console.error('Failed to load events from localStorage:', err)
  }
}

// 保存事件到 localStorage
function saveEventsToStorage() {
  try {
    // 只保存最近 100 条事件
    const eventsToSave = wsEvents.value.slice(-100)
    localStorage.setItem('ws_events', JSON.stringify(eventsToSave))
  } catch (err) {
    console.error('Failed to save events to localStorage:', err)
  }
}

// 添加事件
function addEvent(event) {
  // 过滤掉 ping 事件和非服务器事件
  if (event?.event_type === 'pong' || !event?.event_type?.startsWith('server_')) {
    return
  }

  const eventData = {
    event_type: event.event_type,
    instance_name: event.instance_name || '',
    message: event.message || '',
    timestamp: Date.now()
  }
  wsEvents.value.push(eventData)

  // 只保留最近 200 条事件
  if (wsEvents.value.length > 200) {
    wsEvents.value.shift()
  }

  saveEventsToStorage()
}

// 格式化时间
function formatTime(timestamp) {
  return dayjs(timestamp).format('YY-MM-DD HH:mm')
}

// 获取事件类型的样式类
function getEventClass(eventType) {
  if (eventType.includes('started')) return 'event-success'
  if (eventType.includes('stopped')) return 'event-warning'
  if (eventType.includes('failed')) return 'event-error'
  if (eventType.includes('starting')) return 'event-info'
  return 'event-default'
}

// 清空事件
function clearEvents() {
  wsEvents.value = []
  localStorage.removeItem('ws_events')
}

onMounted(() => {
  // 加载历史事件
  loadEventsFromStorage()

  // 监听 WebSocket 事件
  unlistenEvent = onAnyServerEvent((event) => {
    console.log('WebSocket event received:', event)
    addEvent(event)
  })
})

onUnmounted(() => {
  // 清理监听
  if (unlistenEvent) {
    unlistenEvent()
  }
})
</script>

<style lang="less" scoped>
// WebSocket 通知区域
.ws-notification {
  display: flex;
  align-items: center;
  background-color: #fff;

  .notification-btn {
    color: #4e5969;
    font-size: 20px;
    padding: 0 8px;

    &:hover {
      color: #000;
    }
  }

  :deep(.arco-badge-count) {
    background-color: #f53f3f;
    color: #ffffff;
    font-size: 12px;
  }
}

// 气泡卡片内容样式
.event-popover-content {
  width: 600px;
  max-height: 600px;
  display: flex;
  flex-direction: column;

  .popover-header {
    display: flex;
    justify-content: space-between;
    align-items: center;

    .popover-title {
      font-weight: 500;
      color: #262626;
      font-size: 14px;
    }
  }

  .event-list {
    flex: 1;
    overflow-y: auto;
    color: #fff;

    .empty-state {
      padding: 30px 20px;
      text-align: center;
      color: #8a8a8a;
      font-size: 14px;
    }

    .event-item {
      padding: 10px 16px;
      font-size: 13px;
      display: flex;
      gap: 12px;
      align-items: center;
      background-color: #fafafa;
      transition: background-color 0.2s ease;
      border-radius: 8px;
      margin-top: 4px;

      &:first-child {
        margin-top: 0;
      }

      .event-time {
        color: #8a8a8a;
        min-width: 65px;
        font-family: monospace;
        font-size: 12px;
      }

      .event-type {
        min-width: 75px;
        padding: 2px 8px;
        border-radius: 2px;
        font-weight: 500;
        text-align: center;
        font-size: 12px;
        white-space: nowrap;

        &.event-success {
          background-color: #d4edda;
          color: #155724;
        }

        &.event-error {
          background-color: #f8d7da;
          color: #721c24;
        }

        &.event-warning {
          background-color: #fff3cd;
          color: #856404;
        }

        &.event-info {
          background-color: #d1ecf1;
          color: #0c5460;
        }

        &.event-default {
          background-color: #e2e3e5;
          color: #383d41;
        }
      }

      .event-data {
        color: #262626;
        flex: 1;
        white-space: nowrap;
        overflow: hidden;
        text-overflow: ellipsis;
        font-size: 13px;
      }
    }
  }
}
</style>
