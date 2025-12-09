<template>
  <div class="ws-notification" style="margin-left: auto; padding-right: 20px;">
    <a-popover position="br" trigger="click" class="ws-notification-popover">
      <template #content>
        <a-card :bordered=false>
          <template #title>
            <div class="popover-header">
              <span class="popover-title">事件 ({{ wsEvents.length }})</span>
              <a-button type="outline" size="small" @click="clearEvents">清空</a-button>
            </div>
          </template>
          <div class="event-popover-content">
            <div class="event-list">
              <div v-if="wsEvents.length === 0" class="empty-state">
                暂无事件消息
              </div>
              <a-timeline v-else>
                <a-timeline-item v-for="(event, index) in [...wsEvents].reverse()" :key="index"
                                 :dot-color="getEventColor(event.event_type)">
                  <div class="timeline-content">
                    <div class="timeline-header">
                      <span class="event-time">{{ formatTime(event.timestamp) }}</span>
                      <span class="event-type" :class="getEventClass(event.event_type)">{{ event.event_type }}</span>
                    </div>
                    <div class="event-message">{{ event.instance_name || '' }} - {{ event.message || '' }}</div>
                  </div>
                </a-timeline-item>
              </a-timeline>
            </div>
          </div>
        </a-card>
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
import {Timeline, TimelineItem} from '@arco-design/web-vue';
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

// 获取时间轴点的颜色
function getEventColor(eventType) {
  if (eventType.includes('started')) return '#52c41a'
  if (eventType.includes('stopped')) return '#faad14'
  if (eventType.includes('failed')) return '#f5222d'
  if (eventType.includes('starting')) return '#1890ff'
  return '#8c8c8c'
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

// 气泡卡片内容样式
.event-popover-content {
  width: 450px;
  max-height: 600px;
  display: flex;
  flex-direction: column;
  overflow-y: auto;
  overflow-x: hidden;

  .event-list {
    flex: 1;
    width: 100%;
    padding: 15px;
    box-sizing: border-box;

    :deep(.arco-timeline-item-dot-content) {
      width: 13px;

      .arco-timeline-item-dot {
        width: 13px;
        height: 13px;
        border: 3px solid #fff;
        box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1);
      }
    }

    .empty-state {
      padding: 30px 20px;
      text-align: center;
      color: #8a8a8a;
      font-size: 14px;
    }

    :deep(.arco-timeline) {
      .arco-timeline-item {
        padding-bottom: 16px;
      }
    }

    .timeline-content {

      .timeline-header {
        display: flex;
        gap: 8px;
        align-items: center;
        margin-bottom: 6px;

        .event-time {
          color: #8a8a8a;
          font-family: monospace;
          font-size: 12px;
          min-width: 75px;
        }

        .event-type {
          padding: 2px 8px;
          border-radius: 2px;
          font-weight: 500;
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
      }

      .event-message {
        color: #262626;
        font-size: 13px;
        margin-left: 0;
      }
    }
  }

}
</style>

<style lang="less">
.ws-notification-popover {
  .arco-popover-popup-content {
    padding: 0 !important;
  }

  .arco-popover-content {
    border-radius: var(--border-radius-large);
    overflow: hidden;
    margin-top: 0 !important;
  }

  .arco-card-body {
    padding: 0 !important;
  }
}
</style>
