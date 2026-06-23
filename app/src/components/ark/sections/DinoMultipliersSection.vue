<template>
  <div class="section">
    <p class="section-tip">
      生物全局倍率与行为规则配置（均写入 GameUserSettings.ini）；下方「按生物类名自定义倍率」可对单个生物精细覆盖（写入 Game.ini）。
    </p>

    <!-- 简单标量设置 -->
    <div v-for="g in groupsWithItems" :key="g.key" class="rule-group">
      <div class="rule-group-title">{{ g.label }}</div>
      <t-row :gutter="[12, 12]">
        <t-col v-for="reg in g.items" :key="reg.key" :xs="12" :md="6">
          <div class="rule-item">
            <div class="rule-text">
              <span class="rule-label">{{ reg.label }}</span>
              <span class="rule-tip">{{ reg.tip }}</span>
            </div>
            <div class="rule-control">
              <t-switch v-if="reg.type === 'bool'" v-model="simpleModel[reg.key]"/>
              <t-input-number
                  v-else-if="reg.type === 'float'"
                  v-model="simpleModel[reg.key]"
                  :min="0"
                  :step="0.1"
                  :decimal-places="4"
                  size="small"
                  align="right"
                  style="width: 120px"
              />
              <t-input-number
                  v-else
                  v-model="simpleModel[reg.key]"
                  :min="0"
                  :step="1"
                  :decimal-places="0"
                  size="small"
                  align="right"
                  style="width: 120px"
              />
            </div>
          </div>
        </t-col>
      </t-row>
    </div>

    <t-divider style="margin: 16px 0 8px">按生物类名自定义倍率</t-divider>

    <t-tabs v-model="group">
      <t-tab-panel value="damage" label="伤害"/>
      <t-tab-panel value="resistance" label="抗性"/>
      <t-tab-panel value="speed" label="速度(仅驯养)"/>
      <t-tab-panel value="stamina" label="耐力(仅驯养)"/>
    </t-tabs>

    <div class="toolbar">
      <t-radio-group v-if="hasWild" v-model="tamed" variant="default-filled" size="small">
        <t-radio-button :value="false">野生</t-radio-button>
        <t-radio-button :value="true">驯养</t-radio-button>
      </t-radio-group>
      <span v-else class="only-tamed-hint">仅作用于驯养生物</span>
      <t-button theme="primary" size="small" @click="addRow">
        <template #icon><add-icon/></template>
        添加生物
      </t-button>
    </div>

    <div v-if="rows.length === 0" class="empty">暂无配置，点击「添加生物」开始</div>
    <t-row v-else :gutter="[12, 12]">
      <t-col v-for="(row, i) in rows" :key="i" :xs="12" :md="6">
        <div class="row">
          <div class="cell grow">
            <creature-select v-model="row.className"/>
          </div>
          <div class="cell">
            <t-input-number v-model="row.multiplier" :min="0" :step="0.1" theme="column" :decimal-places="4"
                            align="right" placeholder="倍率"/>
          </div>
          <t-button variant="text" theme="danger" shape="square" @click="removeRow(i)">
            <template #icon><delete-icon/></template>
          </t-button>
        </div>
      </t-col>
    </t-row>
  </div>
</template>

<script setup>
import {computed, ref} from 'vue'
import {AddIcon, DeleteIcon} from 'tdesign-icons-vue-next'
import CreatureSelect from '../CreatureSelect.vue'
import {SETTING_GROUPS, SETTINGS_REGISTRY} from '@/utils/arkSimpleSettings.js'

const props = defineProps({
  model: {type: Object, required: true},
  simpleModel: {type: Object, required: true},
})

const groupsWithItems = computed(() =>
    SETTING_GROUPS.filter((g) => g.panel === 'dino').map((g) => ({
      ...g,
      items: SETTINGS_REGISTRY.filter((r) => r.group === g.key),
    })).filter((g) => g.items.length),
)

const group = ref('damage')
const tamed = ref(false)

const KEY_MAP = {
  'damage|false': 'DinoClassDamageMultipliers',
  'damage|true': 'TamedDinoClassDamageMultipliers',
  'resistance|false': 'DinoClassResistanceMultipliers',
  'resistance|true': 'TamedDinoClassResistanceMultipliers',
  'speed|true': 'TamedDinoClassSpeedMultipliers',
  'stamina|true': 'TamedDinoClassStaminaMultipliers',
}

const hasWild = computed(() => group.value === 'damage' || group.value === 'resistance')
const activeKey = computed(() => {
  const t = hasWild.value ? tamed.value : true
  return KEY_MAP[`${group.value}|${t}`]
})
const rows = computed(() => props.model[activeKey.value] || [])

const addRow = () => props.model[activeKey.value].push({className: '', multiplier: 1})
const removeRow = (i) => props.model[activeKey.value].splice(i, 1)
</script>

<style scoped src="./section.css"></style>
<style scoped>
.rule-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.rule-group-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-secondary, #5a5a5a);
  padding-left: 8px;
  border-left: 3px solid var(--td-brand-color, #0052d9);
  line-height: 1.4;
}

.rule-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  box-sizing: border-box;
  padding: 10px 12px;
  background: var(--td-bg-color-container, #fff);
  border: 1px solid var(--td-component-border, #e7e7e7);
  border-radius: 8px;
  transition: border-color 0.18s ease, background 0.18s ease;
}

.rule-item:hover {
  border-color: var(--td-brand-color-hover, #618dfa);
  background: var(--td-brand-color-light, #f5f7ff);
}

.rule-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
  gap: 2px;
}

.rule-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary, #1f1f1f);
}

.rule-tip {
  font-size: 11px;
  line-height: 1.4;
  color: var(--td-text-color-placeholder, #999);
}

.rule-control {
  flex: 0 0 auto;
}

@media (prefers-reduced-motion: reduce) {
  .rule-item {
    transition: none;
  }
}
</style>
