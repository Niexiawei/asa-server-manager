<template>
  <div class="section">
    <p class="section-tip">
      自定义物品的最大堆叠数量。开启「忽略倍率」后，该物品堆叠不受全局堆叠倍率影响。
    </p>

    <!-- 全局设置：ItemStackSizeMultiplier + ClampItemSpoilingTimes -->
    <div class="rule-group">
      <div class="rule-group-title">全局设置</div>
      <t-row :gutter="[12, 12]">
        <t-col v-for="reg in globalItems" :key="reg.key" :xs="12" :md="6">
          <div class="rule-item">
            <div class="rule-text">
              <span class="rule-label">{{ reg.label }}</span>
              <t-typography-paragraph
                  class="rule-tip"
                  :ellipsis="{ row: 1, tooltipProps: { placement: 'top-left',content:reg.tip } }"
              >{{ reg.tip }}
              </t-typography-paragraph>
            </div>
            <div class="rule-control">
              <t-switch v-if="reg.type === 'bool'" v-model="simpleModel[reg.key]"/>
              <t-input-number
                  v-else
                  v-model="simpleModel[reg.key]"
                  :min="0"
                  :step="0.1"
                  :decimal-places="4"
                  size="small"
                  align="right"
                  style="width: 120px"
              />
            </div>
          </div>
        </t-col>
      </t-row>
    </div>

    <t-divider/>

    <div class="toolbar">
      <span class="only-tamed-hint">共 {{ model.length }} 条</span>
      <t-button theme="primary" size="small" @click="addRow">
        <template #icon>
          <add-icon/>
        </template>
        添加物品
      </t-button>
    </div>

    <div v-if="model.length === 0" class="empty">暂无配置，点击「添加物品」开始</div>
    <t-row v-else :gutter="[12, 12]">
      <t-col v-for="(row, i) in model" :key="i" :xs="12" :md="6">
        <div class="row">
          <div class="cell grow">
            <span class="cell-label">物品名称</span>
            <item-select v-model="row.itemClassString"/>
          </div>
          <div class="cell">
            <span class="cell-label">最大数量</span>
            <t-input-number v-model="row.maxItemQuantity" :min="0" :step="1" theme="column" align="right"
                            placeholder="数量"/>
          </div>
          <div class="cell switch-cell">
            <span class="cell-label">忽略倍率</span>
            <t-switch v-model="row.ignoreMultiplier"/>
          </div>
          <t-button variant="text" theme="danger" shape="square" @click="removeRow(i)">
            <template #icon>
              <delete-icon/>
            </template>
          </t-button>
        </div>
      </t-col>
    </t-row>
  </div>
</template>

<script setup>
import {computed} from 'vue'
import {AddIcon, DeleteIcon} from 'tdesign-icons-vue-next'
import ItemSelect from '../ItemSelect.vue'
import {SETTINGS_REGISTRY} from '@/utils/arkSimpleSettings.js'

const props = defineProps({
  model: {type: Array, required: true},
  simpleModel: {type: Object, required: true},
})

const globalItems = computed(() => SETTINGS_REGISTRY.filter((r) => r.group === 'item_global'))

const addRow = () => props.model.push({itemClassString: '', maxItemQuantity: 100, ignoreMultiplier: false})
const removeRow = (i) => props.model.splice(i, 1)
</script>

<style scoped src="./section.css"></style>
<style scoped>
.switch-cell {
  align-items: center;
}

.rule-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 4px;
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
  /* flex: 1 1 0 让文本区占满剩余空间，不挤压右侧 rule-control */
  flex: 1 1 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
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
  cursor: default;
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
