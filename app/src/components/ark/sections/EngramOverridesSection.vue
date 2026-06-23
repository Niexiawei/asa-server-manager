<template>
  <div class="section">
    <p class="section-tip">
      覆盖印痕的隐藏状态、消耗点数、等级需求或前置要求。无印痕清单数据，需手动填写 EngramIndex 或 EngramClassName
      (如 <code>EngramEntry_Campfire_C</code>)。
    </p>

    <div class="toolbar">
      <span class="only-tamed-hint">共 {{ model.length }} 条</span>
      <t-button theme="primary" size="small" @click="addRow">
        <template #icon><add-icon/></template>
        添加印痕
      </t-button>
    </div>

    <div v-if="model.length === 0" class="empty">暂无配置，点击「添加印痕」开始</div>
    <div v-else class="rows">
      <div v-for="(row, i) in model" :key="i" class="row engram-row">
        <div class="cell">
          <span class="cell-label">类型</span>
          <t-select v-model="row.kind" style="width: 110px">
            <t-option value="index" label="按索引"/>
            <t-option value="named" label="按类名"/>
          </t-select>
        </div>
        <div class="cell grow">
          <span class="cell-label">{{ row.kind === 'named' ? 'EngramClassName' : 'EngramIndex' }}</span>
          <t-input v-if="row.kind === 'named'" v-model="row.engramClassName" placeholder="EngramEntry_XXX_C"/>
          <t-input-number v-else v-model="row.engramIndex" :min="0" :step="1" theme="column" align="right"
                          placeholder="索引"/>
        </div>
        <div class="cell">
          <span class="cell-label">消耗点数</span>
          <t-input-number v-model="row.engramPointsCost" :min="0" :step="1" theme="normal" align="right"
                          placeholder="默认"/>
        </div>
        <div class="cell">
          <span class="cell-label">等级需求</span>
          <t-input-number v-model="row.engramLevelRequirement" :min="0" :step="1" theme="normal" align="right"
                          placeholder="默认"/>
        </div>
        <div class="cell switch-cell">
          <span class="cell-label">隐藏</span>
          <t-switch v-model="row.engramHidden"/>
        </div>
        <div class="cell switch-cell">
          <span class="cell-label">移除前置</span>
          <t-switch v-model="row.removeEngramPreReq"/>
        </div>
        <t-button variant="text" theme="danger" shape="square" @click="removeRow(i)">
          <template #icon><delete-icon/></template>
        </t-button>
      </div>
    </div>
  </div>
</template>

<script setup>
import {AddIcon, DeleteIcon} from 'tdesign-icons-vue-next'

const props = defineProps({model: {type: Array, required: true}})

const addRow = () =>
    props.model.push({
      kind: 'index',
      engramClassName: '',
      engramIndex: '',
      engramHidden: false,
      engramPointsCost: '',
      engramLevelRequirement: '',
      removeEngramPreReq: false,
    })
const removeRow = (i) => props.model.splice(i, 1)
</script>

<style scoped src="./section.css"></style>
<style scoped>
.engram-row {
  flex-wrap: wrap;
}

.switch-cell {
  align-items: center;
}

.engram-row :deep(.t-input-number) {
  width: 96px;
}

code {
  background: #f0f0f0;
  padding: 0 4px;
  border-radius: 3px;
}
</style>
