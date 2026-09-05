// Resource Monitor Worker —— 专用（dedicated）Worker，每个标签页一个。
//
// 曾用 SharedWorker，想让「整个浏览器只有一条 /api/server/all-info SSE」。放弃它的原因：
//   1. SharedWorker 的 console.* 不进页面 devtools，要单独开 chrome://inspect/#workers
//      才看得到 —— 线上排障时等于瞎子（本次「连不上又没有任何日志」就是踩这个）。
//   2. SharedWorker 实例跨页面刷新存活：首个 INIT 的 SSE_URL 会被永久钉死，一旦
//      wedge（切了部署地址 / 子路径 / 环境），刷新救不回来，只能关掉所有标签页。
//   3. 少数浏览器（部分移动端、某些隔离/策略环境）根本没有 SharedWorker，且无降级路径。
//
// 代价：明文 HTTP + 多标签页时，每个标签页各一条 all-info（HTTP/1.1 下 6 连接上限更紧张）。
// 但默认部署是 HTTPS + HTTP/2，多路复用下这个上限本就不适用；单标签页场景零回归。
// 结构与 wsWorker.js 对齐：self.onmessage / postMessage，不再有 ports/onconnect。
//
// 详见 docs/RESOURCE_RATE_CHART_PLAN.md §10。

// SSE 重连：指数退避 + 全抖动，而不是固定间隔。固定间隔会造成**相位锁定**——
// 会话同时过期的客户端会同时按同一节奏重连，之后永远同步，每隔 N 秒给服务端一次并发脉冲。
const RECONNECT_BASE_DELAY = 1000;
const RECONNECT_MAX_DELAY = 30000;
let reconnectAttempt = 0;

// 鉴权失效后停止重连，等主线程登录成功再说。EventSource 拿不到 HTTP 状态码，
// 所以由主线程去问 /api/auth/state 来判定，再通过 AUTH_BLOCKED / AUTH_RESUMED 告知本 Worker。
let sseAuthBlocked = false;

function nextReconnectDelay() {
  const cap = Math.min(RECONNECT_MAX_DELAY, RECONNECT_BASE_DELAY * Math.pow(2, reconnectAttempt));
  reconnectAttempt++;
  return Math.random() * cap;
}

// 单页面单消费者：只需要记住「订了哪些实例」「订没订整机」，据此过滤要不要 postMessage。
const subscribedInstances = new Set();
let hostSubscribed = false;

let eventSource = null;
// 完整 SSE URL 由主线程用 buildEventSourceUrl() 拼好后传进来，Worker 不自己拼路径
// （曾因 base 带尾斜杠 + 模板字符串拼接产生 `//api/...` 而 404）。
let SSE_URL = '';

let isReconnecting = false;
let reconnectTimer = null;

self.onmessage = ({ data }) => {
  const { type, instanceId, payload } = data || {};

  switch (type) {
    case 'INIT':
      // URL 变了要重连 —— 切环境 / 子路径时旧 URL 会一直连不上。
      if (payload && payload.sseUrl && payload.sseUrl !== SSE_URL) {
        SSE_URL = payload.sseUrl;
        console.log('[ResourceWorker] INIT，SSE URL =', SSE_URL);
        closeSSEConnection();
        stopReconnect();
        reconnectAttempt = 0;
        startSSEConnection();
      } else if (!eventSource && !isReconnecting) {
        startSSEConnection();
      }
      break;

    case 'SUBSCRIBE':
      if (instanceId) subscribedInstances.add(instanceId);
      break;

    case 'UNSUBSCRIBE':
      if (instanceId) subscribedInstances.delete(instanceId);
      break;

    case 'SUBSCRIBE_HOST':
      hostSubscribed = true;
      break;

    case 'UNSUBSCRIBE_HOST':
      hostSubscribed = false;
      break;

    // 主线程确认过鉴权状态后告诉 Worker 该停还是该继续。
    case 'AUTH_BLOCKED':
      sseAuthBlocked = true;
      stopReconnect();
      break;

    case 'AUTH_RESUMED':
      sseAuthBlocked = false;
      reconnectAttempt = 0;
      // 之前因为鉴权停掉的话，恢复后要主动拉一次
      if (!eventSource && !isReconnecting) startSSEConnection();
      break;

    case 'CLOSE':
      closeAll();
      break;
  }
};

// 建立唯一的 SSE 连接
function startSSEConnection() {
  if (eventSource) {
    console.warn('[ResourceWorker] SSE 连接已存在，跳过');
    return;
  }
  if (!SSE_URL) {
    console.warn('[ResourceWorker] 尚未收到 SSE URL，等待 INIT');
    return;
  }
  if (sseAuthBlocked) {
    console.log('[ResourceWorker] 鉴权阻断中，跳过连接');
    return;
  }

  try {
    console.log('[ResourceWorker] 建立 SSE 连接:', SSE_URL);
    eventSource = new EventSource(SSE_URL);

    eventSource.onopen = () => {
      // 连接成功必须重置退避指数，否则下次断线会直接从上次的最大延迟起步
      reconnectAttempt = 0;
      stopReconnect();
      console.log('[ResourceWorker] SSE 已连接');
      postMessage({ type: 'SSE_CONNECTED' });
    };

    eventSource.onmessage = (event) => {
      let data;
      try {
        data = JSON.parse(event.data);
      } catch (error) {
        console.error('[ResourceWorker] 解析 SSE 数据失败:', error);
        postMessage({ type: 'ERROR', error: '资源数据解析失败' });
        return;
      }

      // 宿主机指标：每 tick 都发，不做变化门限 —— 趋势图要求「每个 tick 都有点」
      if (data.host && hostSubscribed) {
        postMessage({
          type: 'HOST_UPDATE',
          data: {
            timestamp: data.timestamp,
            cpu_cores: data.cpu_cores,
            running_count: data.running_count,
            host: data.host,
          },
        });
      }

      if (Array.isArray(data.instances)) {
        data.instances.forEach((instanceData) => {
          const id = instanceData.instance;
          if (!subscribedInstances.has(id)) return;
          postMessage({
            type: 'RESOURCE_UPDATE',
            instanceId: id,
            data: formatInstanceData(instanceData, data),
          });
        });
      }
    };

    eventSource.onerror = () => {
      if (eventSource && eventSource.readyState === EventSource.CLOSED) {
        console.error('[ResourceWorker] SSE 连接关闭，准备重连');
        closeSSEConnection();
        postMessage({ type: 'ERROR', error: 'SSE 连接错误' });
        // CLOSED 说明浏览器已放弃（服务端返回非 200，比如 401）。
        // 请主线程确认是不是鉴权失效 —— EventSource 拿不到状态码。
        postMessage({ type: 'SSE_CHECK_AUTH' });
        startReconnect();
      }
      // readyState === CONNECTING 说明浏览器在自行重试，不用管
    };
  } catch (error) {
    console.error('[ResourceWorker] 建立 SSE 连接失败:', error);
    postMessage({ type: 'ERROR', error: '资源监控启动失败' });
    startReconnect();
  }
}

function startReconnect() {
  if (isReconnecting) return;
  // 鉴权失效时不要重连：服务端只会一路 401，而每次尝试都是一条新连接。
  if (sseAuthBlocked) {
    console.log('[ResourceWorker] 重连已暂停：需要重新登录');
    return;
  }
  isReconnecting = true;
  scheduleReconnect();
}

// 用递归 setTimeout 而不是 setInterval：setInterval 在单次尝试耗时超过间隔时会**堆积**回调，
// 网络差的时候反而制造出更多并发连接。
function scheduleReconnect() {
  clearTimeout(reconnectTimer);
  const delay = nextReconnectDelay();
  console.log('[ResourceWorker] 第 ' + reconnectAttempt + ' 次重连将在 ' + Math.round(delay) + 'ms 后进行');
  reconnectTimer = setTimeout(() => {
    if (!isReconnecting || sseAuthBlocked) return;
    if (!eventSource) {
      console.log('[ResourceWorker] 尝试重连…');
      startSSEConnection();
      if (isReconnecting) scheduleReconnect();
    }
  }, delay);
}

function stopReconnect() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  isReconnecting = false;
}

function closeSSEConnection() {
  if (eventSource) {
    eventSource.close();
    eventSource = null;
    console.log('[ResourceWorker] SSE 连接已关闭');
  }
}

function closeAll() {
  console.log('[ResourceWorker] 关闭全部连接');
  stopReconnect();
  closeSSEConnection();
  subscribedInstances.clear();
  hostSubscribed = false;
}

// ============ 以下为原 sharedResourceWorker.js 的格式化工具，原样搬运 ============

// Format instance data for sending to component
function formatInstanceData(instanceData, fullData) {
  return {
    timestamp: fullData.timestamp,
    cpu_cores: fullData.cpu_cores,
    memory: {
      total: fullData.memory.total,
      total_gb: fullData.memory.total_gb
    },
    pid: instanceData.pid,
    process: {
      name: instanceData.process_name,
      cpu_percent: instanceData.cpu_percent,
      cpu_total_percent: instanceData.cpu_total_percent,
      memory_used: instanceData.memory_used,
      memory_used_mb: instanceData.memory_used_mb,
      memory_used_gb: instanceData.memory_used_gb,
      memory_percent: instanceData.memory_percent
    },
    // 趋势图用的速率字段，采不到时后端给 null（与「速率为 0」是两回事），原样透传
    disk_io: instanceData.disk_io ?? null,
    net_io: instanceData.net_io ?? null,
    render: {
      cpu_percent_value: instanceData.cpu_percent.toFixed(1),
      cpu_percent_normalized: instanceData.cpu_percent / 100,
      cpu_percent_color: getProgressColor(instanceData.cpu_percent),

      cpu_total_percent_value: instanceData.cpu_total_percent.toFixed(1),
      cpu_total_percent_normalized: instanceData.cpu_total_percent / 100,
      cpu_total_percent_color: getProgressColor(instanceData.cpu_total_percent),

      memory_used_formatted: formatMemory(instanceData.memory_used),
      memory_total_formatted: formatMemory(fullData.memory.total),

      memory_percent_value: instanceData.memory_percent.toFixed(2),
      memory_percent_normalized: instanceData.memory_percent / 100,
      memory_percent_color: getProgressColor(instanceData.memory_percent)
    }
  };
}

// Format memory bytes to readable format
function formatMemory(bytes) {
  if (!bytes) return '-';

  // Helper function to format number to max 4 digits
  function formatToMax4Digits(value) {
    // First try the value as is (if integer)
    const intValue = Math.floor(value);
    if (intValue === value && intValue.toString().length <= 4) {
      return value.toString();
    }

    // Try with 2 decimal places
    let formatted = value.toFixed(2);
    // Check if the numeric part (without decimal point) has at most 4 digits
    if (formatted.replace('.', '').length <= 4) {
      return formatted;
    }

    // Try with 1 decimal place
    formatted = value.toFixed(1);
    if (formatted.replace('.', '').length <= 4) {
      return formatted;
    }

    // Use integer value if it has 4 or fewer digits
    if (intValue.toString().length <= 4) {
      return intValue.toString();
    }

    // If integer part has more than 4 digits, truncate to 4 digits
    if (intValue.toString().length > 4) {
      return intValue.toString().substring(0, 4);
    }

    // Fallback
    return value.toFixed(0);
  }

  const mb = bytes / (1024 * 1024);
  if (mb < 1024) {
    return `${formatToMax4Digits(mb)} MB`;
  }

  const gb = mb / 1024;
  return `${formatToMax4Digits(gb)} GB`;
}

// Get progress color based on percentage
function getProgressColor(percent) {
  if (percent < 50) {
    return '#00b42a'; // 绿色
  } else if (percent < 70) {
    return '#165dff'; // 蓝色
  } else if (percent < 90) {
    return '#ff7d00'; // 黄色
  } else {
    return '#f53f3f'; // 红色
  }
}
