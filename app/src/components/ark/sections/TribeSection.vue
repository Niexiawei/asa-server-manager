<template>
  <div class="section">
    <p class="section-tip">
      离线突袭保护（ORP）与部落规则设置。开关按「开启」语义展示，保存时自动转换为对应 INI 值
      （全部写入 GameUserSettings.ini，部分例外字段写入 Game.ini）。
    </p>

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
import {computed} from 'vue'
import {SETTING_GROUPS, SETTINGS_REGISTRY} from '@/utils/arkSimpleSettings.js'

defineProps({model: {type: Object, required: true}})

const groupsWithItems = computed(() =>
    SETTING_GROUPS.filter((g) => g.panel === 'tribe').map((g) => ({
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
