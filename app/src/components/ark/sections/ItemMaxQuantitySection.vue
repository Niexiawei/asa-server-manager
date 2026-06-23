<template>
  <div class="section">
    <p class="section-tip">
      自定义物品的最大堆叠数量。开启「忽略倍率」后，该物品堆叠不受全局堆叠倍率影响。
    </p>

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
import {AddIcon, DeleteIcon} from 'tdesign-icons-vue-next'
import ItemSelect from '../ItemSelect.vue'

const props = defineProps({model: {type: Array, required: true}})

const addRow = () => props.model.push({itemClassString: '', maxItemQuantity: 100, ignoreMultiplier: false})
const removeRow = (i) => props.model.splice(i, 1)
</script>

<style scoped src="./section.css"></style>
<style scoped>
.switch-cell {
  align-items: center;
}
</style>
