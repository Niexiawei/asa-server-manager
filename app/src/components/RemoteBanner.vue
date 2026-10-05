<template>
  <!-- 远程上下文的常驻提示条（docs/REMOTE_MANAGER_MESH_PLAN.md §12 P4-2、§9）：
       正在管理哪台、走的什么路径、对方版本是否与本机不同、是否经中转。 -->
  <div v-if="remote" class="remote-banner" :class="{error: !!error}">
    <span class="title">正在管理远程机器：<b>{{ label }}</b></span>
    <t-tag v-if="hello" size="small" :theme="hello.path === 'relay' ? 'warning' : 'success'" variant="light">
      {{ PATH_TEXT[hello.path] || hello.path }} · {{ hello.latency_ms }} ms
    </t-tag>
    <t-tag v-if="hello" size="small" variant="light">
      对方授予本机：{{ hello.granted_role === 'ROLE_ADMIN' ? '管理员' : '操作员' }}
    </t-tag>
    <span v-if="versionMismatch" class="warn">
      对方版本 {{ hello.version }}，与本机 {{ localVersion }} 不同，部分页面可能不可用
    </span>
    <span v-if="hello?.path === 'relay'" class="hint">经中转：大文件上传 / 下载较慢</span>
    <span v-if="error" class="warn">{{ error }}</span>
    <span class="spacer"/>
    <t-button size="small" variant="text" @click="check" :loading="checking">重新检测</t-button>
    <t-button size="small" theme="primary" variant="outline" @click="switchToLocal">回到本机</t-button>
  </div>
</template>

<script setup>
import {computed, onMounted, ref} from 'vue'
import {peerState, isRemote, switchToLocal} from '@/utils/peerContext.js'
import {getMeshStatus, helloPeer} from '@/apis/meshApi.js'

const PATH_TEXT = {lan: '内网直连', public: '公网直连', relay: '中转', punched: '打洞'}

const remote = isRemote()
const hello = ref(null)
const localVersion = ref('')
const error = ref('')
const checking = ref(false)

const label = computed(() => hello.value?.label || peerState.label || peerState.id.slice(0, 8))
const versionMismatch = computed(() =>
    hello.value && localVersion.value && hello.value.version && hello.value.version !== localVersion.value)

async function check() {
  checking.value = true
  error.value = ''
  try {
    const [res, st] = await Promise.all([helloPeer(peerState.id), getMeshStatus().catch(() => null)])
    hello.value = res
    localVersion.value = st?.version || ''
    if (res?.granted_role === 'ROLE_NONE') {
      error.value = '对方已不再授权本机'
    }
  } catch (e) {
    hello.value = null
    error.value = `无法连接：${e.message || e}`
  } finally {
    checking.value = false
  }
}

onMounted(() => {
  if (remote) check()
})
</script>

<style scoped lang="less">
.remote-banner {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 0 0 auto;
  padding: 6px 14px;
  background: #fff7e6;
  border-bottom: 1px solid #ffd591;
  font-size: 13px;
  color: #614700;

  &.error {
    background: #fff1f0;
    border-bottom-color: #ffa39e;
  }

  .warn {
    color: #d4380d;
  }

  .hint {
    color: #8c6d1f;
  }

  .spacer {
    flex: 1;
  }
}
</style>
