<template>
  <div class="section">
    <p class="section-tip">
      自定义玩家 / 驯养生物的最大等级与每级累计所需经验值，以及每个玩家等级获得的印痕点数。
      下标 0 对应 1 级。玩家曲线写在前、驯养曲线写在后（ARK 按出现顺序区分）。
    </p>

    <t-tabs v-model="tab">
      <t-tab-panel value="player" label="玩家经验曲线"/>
      <t-tab-panel value="dino" label="驯养经验曲线"/>
      <t-tab-panel value="engram" label="玩家印痕点数"/>
    </t-tabs>

    <div class="resize-bar">
      <span class="only-tamed-hint">等级数量</span>
      <t-input-number v-model="resizeTo" :min="0" :max="1000" :step="1" size="small" style="width: 120px"/>
      <t-button size="small" theme="primary" variant="outline" @click="applyResize">应用</t-button>
      <span class="only-tamed-hint">当前 {{ activeArr.length }} 级</span>
    </div>

    <div v-if="activeArr.length === 0" class="empty">暂无配置，设置「等级数量」后点击「应用」生成</div>
    <div v-else class="level-grid">
      <div v-for="(v, i) in activeArr" :key="i" class="level-item">
        <span class="level-no">Lv {{ i + 1 }}</span>
        <t-input-number :model-value="activeArr[i]" :min="0" :step="1" theme="normal" align="right" size="small"
                        @change="(val) => (activeArr[i] = val ?? 0)"/>
      </div>
    </div>
  </div>
</template>

<script setup>
import {computed, ref} from 'vue'

const props = defineProps({
  player: {type: Array, required: true},
  dino: {type: Array, required: true},
  engramPoints: {type: Array, required: true},
})

const tab = ref('player')
const resizeTo = ref(0)

const activeArr = computed(() => {
  if (tab.value === 'dino') return props.dino
  if (tab.value === 'engram') return props.engramPoints
  return props.player
})

const applyResize = () => {
  const arr = activeArr.value
  const n = Math.max(0, Math.floor(resizeTo.value || 0))
  if (n < arr.length) {
    arr.splice(n)
  } else {
    const last = arr.length ? arr[arr.length - 1] : 0
    while (arr.length < n) arr.push(last)
  }
}
</script>

<style scoped src="./section.css"></style>
<style scoped>
.resize-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.level-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(170px, 1fr));
  gap: 8px;
  max-height: 360px;
  overflow-y: auto;
  padding: 4px;
}

.level-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 5px 10px;
  background: var(--td-bg-color-container, #fff);
  border: 1px solid var(--td-component-border, #e7e7e7);
  border-radius: 7px;
  transition: border-color 0.18s ease, background 0.18s ease;
}

.level-item:hover {
  border-color: var(--td-brand-color-hover, #618dfa);
  background: var(--td-brand-color-light, #f5f7ff);
}

.level-no {
  font-size: 12px;
  color: var(--td-text-color-secondary, #666);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

@media (prefers-reduced-motion: reduce) {
  .level-item {
    transition: none;
  }
}

.level-item :deep(.t-input-number) {
  width: 100px;
}
</style>
