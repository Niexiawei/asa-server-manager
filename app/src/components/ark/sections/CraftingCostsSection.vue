<template>
  <div class="section">
    <p class="section-tip">
      自定义物品的制作消耗。每个制作物品可配置多种资源消耗。注意：更改制作配方后，已学会的印痕需用遗忘汤重新学习。
    </p>

    <div class="toolbar">
      <span class="only-tamed-hint">共 {{ model.length }} 个物品</span>
      <t-button theme="primary" size="small" @click="addEntry">
        <template #icon>
          <add-icon/>
        </template>
        添加制作物品
      </t-button>
    </div>

    <div v-if="model.length === 0" class="empty">暂无配置，点击「添加制作物品」开始</div>
    <div v-else class="rows">
      <div v-for="(entry, ei) in model" :key="ei" class="sub-card">
        <div class="sub-card-head">
          <span class="cell-label" style="white-space: nowrap">制作物品</span>
          <div style="flex: 1 1 auto; min-width: 0">
            <item-select v-model="entry.itemClassString"/>
          </div>
          <t-button variant="outline" theme="danger" size="small" @click="removeEntry(ei)">
            <template #icon>
              <delete-icon/>
            </template>
            删除物品
          </t-button>
        </div>

        <div class="sub-rows">
          <div v-if="entry.resources.length === 0" class="empty" style="padding: 12px">未添加资源消耗</div>
          <div v-for="(res, ri) in entry.resources" :key="ri" class="row">
            <div class="cell grow">
              <span class="cell-label">资源名称</span>
              <item-select v-model="res.resourceItemTypeString"/>
            </div>
            <div class="cell">
              <span class="cell-label">精确数量</span>
              <t-input-number v-model="res.baseResourceRequirement" :min="0" :step="1" theme="column" align="right"
                              placeholder="数量"/>
            </div>
            <div class="cell switch-cell">
              <span class="cell-label">精确资源</span>
              <t-switch v-model="res.requireExactType"/>
            </div>
            <t-button variant="text" theme="danger" shape="square" @click="removeResource(entry, ri)">
              <template #icon>
                <delete-icon/>
              </template>
            </t-button>
          </div>
          <div>
            <t-button variant="text" theme="primary" size="small" @click="addResource(entry)">
              <template #icon>
                <add-icon/>
              </template>
              添加资源
            </t-button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import {AddIcon, DeleteIcon} from 'tdesign-icons-vue-next'
import ItemSelect from '../ItemSelect.vue'

const props = defineProps({model: {type: Array, required: true}})

const addEntry = () => props.model.push({itemClassString: '', resources: []})
const removeEntry = (i) => props.model.splice(i, 1)
const addResource = (entry) =>
    entry.resources.push({resourceItemTypeString: '', baseResourceRequirement: 1, requireExactType: false})
const removeResource = (entry, i) => entry.resources.splice(i, 1)
</script>

<style scoped src="./section.css"></style>
<style scoped>
.switch-cell {
  align-items: center;
}
</style>
