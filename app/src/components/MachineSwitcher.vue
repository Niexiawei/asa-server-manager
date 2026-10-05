<template>
  <!-- 顶栏「当前机器」选择器（docs/REMOTE_MANAGER_MESH_PLAN.md §12 P4-2）。
       只在本机启用了管理器互控、且当前用户能使用远程控制时出现；远程上下文里始终出现（要能切回本机）。 -->
  <t-dropdown v-if="visible" :options="options" trigger="click" :max-height="420" @click="onSelect"
              @visible-change="v => v && refresh()">
    <t-button variant="text" class="machine-button" :theme="remote ? 'warning' : 'default'">
      <template #icon>
        <desktop-icon/>
      </template>
      {{ remote ? (peerState.label || shortId(peerState.id)) : '本机' }}
      <template #suffix>▾</template>
    </t-button>
  </t-dropdown>
</template>

<script setup>
import {computed, onMounted, ref} from 'vue'
import {DesktopIcon} from 'tdesign-icons-vue-next'
import {authState} from '@/store/authStore.js'
import {peerState, isRemote, switchToPeer, switchToLocal} from '@/utils/peerContext.js'
import {getMeshStatus, listPeers} from '@/apis/meshApi.js'

const remote = isRemote()
const meshRunning = ref(false)
const peers = ref([])

const PATH_TEXT = {lan: '内网直连', public: '公网直连', relay: '中转', punched: '打洞'}

function shortId(id) {
  return (id || '').replace(/-/g, '').slice(0, 8)
}

// 能控制的 = 对方授予了本机角色（remote_role 是对方 Hello 回答的缓存）
const controllable = computed(() => peers.value.filter(p => p.remote_role))

const visible = computed(() => remote || (meshRunning.value && controllable.value.length > 0))

const options = computed(() => {
  const items = [{content: remote ? '回到本机' : '本机（当前）', value: '__local__', disabled: !remote}]
  for (const p of controllable.value) {
    const online = p.last_hello && !p.last_error
    const path = p.path ? PATH_TEXT[p.path] || p.path : ''
    const state = p.last_error ? '离线' : (online ? (path || '在线') : '未检测')
    items.push({
      content: `${p.display_name}　${state}　${p.remote_role === 'admin' ? '管理员' : '操作员'}`,
      value: p.node_id,
      disabled: p.node_id === peerState.id,
    })
  }
  return items
})

async function refresh() {
  // 远程上下文里也查的是本机（/api/mesh/* 不改写）
  try {
    const st = await getMeshStatus()
    meshRunning.value = !!st?.running
    peers.value = meshRunning.value ? (await listPeers()) || [] : []
  } catch (e) {
    // 403（本机用户不能使用远程控制）或未初始化：不显示选择器
    meshRunning.value = false
    peers.value = []
  }
}

function onSelect({value}) {
  if (value === '__local__') {
    switchToLocal()
    return
  }
  const p = peers.value.find(x => x.node_id === value)
  if (p) switchToPeer(p)
}

onMounted(() => {
  if (!authState.authEnabled || authState.bypassed || authState.user) refresh()
})
</script>

<style scoped lang="less">
.machine-button {
  padding: 0 8px !important;
}
</style>
