<template>
  <div class="section">
    <p class="section-tip">
      难度、昼夜周期、玩家属性消耗、采集、建筑数量与拾取等全局环境倍率（均写入 GameUserSettings.ini）。
    </p>

    <div v-for="g in groupsWithItems" :key="g.key" class="rule-group">
      <div class="rule-group-title">{{ g.label }}</div>
      <t-row :gutter="[12, 12]">
        <t-col v-for="reg in g.items" :key="reg.key" :xs="12" :md="6">
          <div class="rule-item">
            <div class="rule-text">
              <span class="rule-label">{{ reg.label }}</span>
              <t-typography-paragraph
                  class="rule-tip"
                  :ellipsis="{ row: 1, tooltipProps: { placement: 'top-left', content: reg.tip } }"
              >{{ reg.tip }}</t-typography-paragraph>
            </div>
            <div class="rule-control">
              <t-switch v-if="reg.type === 'bool'" v-model="model[reg.key]"/>
              <t-input-number
                  v-else-if="reg.type === 'float'"
                  v-model="model[reg.key]"
                  :min="0"
                  :step="0.1"
                  :decimal-places="4"
                  size="small"
                  align="right"
                  style="width: 120px"
              />
              <t-input-number
                  v-else
                  v-model="model[reg.key]"
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
  </div>
</template>

<script setup>
import {computed, watch} from 'vue'
import {SETTING_GROUPS, SETTINGS_REGISTRY} from '@/utils/arkSimpleSettings.js'

const props = defineProps({model: {type: Object, required: true}})

// OverrideOfficialDifficulty 与 DifficultyOffset 冲突：前者生效时后者应为 1.0
watch(() => props.model.OverrideOfficialDifficulty, (val) => {
  if (val > 0 && props.model.DifficultyOffset !== 1.0) {
    props.model.DifficultyOffset = 1.0
  }
})

const groupsWithItems = computed(() =>
    SETTING_GROUPS.filter((g) => g.panel === 'world').map((g) => ({
      ...g,
      items: SETTINGS_REGISTRY.filter((r) => r.group === g.key),
    })).filter((g) => g.items.length),
)
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
  margin: 0;
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
