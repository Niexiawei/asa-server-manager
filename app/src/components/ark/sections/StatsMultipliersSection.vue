<template>
  <div class="section">
    <p class="section-tip">
      每级属性点倍率（PerLevelStatsMultiplier）。留空表示不覆盖，使用游戏默认值。Add / Affinity 为驯服时的加成项。
    </p>

    <t-tabs v-model="group">
      <t-tab-panel v-for="g in STAT_GROUPS" :key="g.key" :value="g.key" :label="g.label"/>
    </t-tabs>

    <div class="toolbar">
      <span class="only-tamed-hint">分组：{{ groupLabel }}（共 12 项属性）</span>
      <t-button size="small" variant="text" theme="danger" @click="clearGroup">清空本组</t-button>
    </div>

    <t-row :gutter="[10, 10]">
      <t-col v-for="s in STAT_INDICES" :key="s.index" :xs="12" :md="6" :lg="4">
        <div class="stat-item">
          <div class="stat-top">
            <span class="stat-name">{{ s.cn }}</span>
            <t-input-number
                :model-value="getStat(s.index)"
                :min="0"
                :step="currentGroup.type === 'int' ? 1 : 0.1"
                :decimal-places="currentGroup.type === 'int' ? 0 : 4"
                size="small"
                align="right"
                placeholder="默认"
                @change="(v) => setStat(s.index, v)"
            />
          </div>
          <span class="stat-en">{{ s.en }}</span>
        </div>
      </t-col>
    </t-row>
  </div>
</template>

<script setup>
import {computed, ref} from 'vue'
import {STAT_GROUPS, STAT_INDICES} from '@/utils/arkGameIni.js'

const props = defineProps({model: {type: Object, required: true}})

const group = ref(STAT_GROUPS[0].key)
const currentGroup = computed(() => STAT_GROUPS.find((g) => g.key === group.value) ?? STAT_GROUPS[0])
const groupLabel = computed(() => currentGroup.value.label)

const getStat = (i) => {
  const v = props.model[group.value]?.[i]
  return v === '' || v == null ? undefined : v
}
const setStat = (i, v) => {
  const bucket = props.model[group.value]
  if (v === '' || v == null || Number.isNaN(Number(v))) {
    delete bucket[i]
  } else {
    bucket[i] = Number(v)
  }
}
const clearGroup = () => {
  const bucket = props.model[group.value]
  for (const k of Object.keys(bucket)) delete bucket[k]
}
</script>

<style scoped src="./section.css"></style>
