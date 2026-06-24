<template>
  <div class="section">
    <!-- 自动解锁 -->
    <div class="sub-header">
      <span class="sub-title">自动解锁印痕</span>
      <span class="sub-desc">玩家达到指定等级时自动获得印痕，无需消耗点数（LevelToAutoUnlock=0 表示始终解锁）</span>
      <t-button theme="primary" size="small" @click="addAutoUnlock">
        <template #icon><add-icon/></template>
        添加
      </t-button>
    </div>

    <div v-if="autoUnlocks.length === 0" class="empty">暂无自动解锁配置</div>
    <div v-else class="rows">
      <div v-for="(row, i) in autoUnlocks" :key="i" class="row auto-unlock-row">
        <div class="cell grow">
          <span class="cell-label">EngramClassName</span>
          <engram-select v-model="row.engramClassName"/>
        </div>
        <div class="cell">
          <span class="cell-label">解锁等级</span>
          <t-input-number v-model="row.levelToAutoUnlock" :min="0" :step="1" theme="normal" align="right"
                          placeholder="0"/>
        </div>
        <t-button variant="text" theme="danger" shape="square" @click="removeAutoUnlock(i)">
          <template #icon><delete-icon/></template>
        </t-button>
      </div>
    </div>

    <t-divider style="margin: 16px 0 12px"/>

    <!-- 印痕条目覆盖 -->
    <div class="sub-header">
      <span class="sub-title">印痕条目覆盖</span>
      <span class="sub-desc">覆盖印痕的隐藏状态、消耗点数、等级需求或前置要求（OverrideEngramEntries / OverrideNamedEngramEntries）</span>
      <t-button theme="primary" size="small" @click="addRow">
        <template #icon><add-icon/></template>
        添加
      </t-button>
    </div>

    <div v-if="model.length === 0" class="empty">暂无覆盖配置</div>
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
          <engram-select v-if="row.kind === 'named'" v-model="row.engramClassName"/>
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
import EngramSelect from '../EngramSelect.vue'

const props = defineProps({
  model: {type: Array, required: true},
  autoUnlocks: {type: Array, required: true},
})

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

const addAutoUnlock = () => props.autoUnlocks.push({engramClassName: '', levelToAutoUnlock: 0})
const removeAutoUnlock = (i) => props.autoUnlocks.splice(i, 1)
</script>

<style scoped src="./section.css"></style>
<style scoped>
.sub-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.sub-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary, #1f1f1f);
  white-space: nowrap;
}

.sub-desc {
  flex: 1;
  font-size: 12px;
  color: var(--td-text-color-placeholder, #999);
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.auto-unlock-row :deep(.t-input-number) {
  width: 96px;
}

.engram-row {
  flex-wrap: wrap;
}

.switch-cell {
  align-items: center;
}

.engram-row :deep(.t-input-number) {
  width: 96px;
}
</style>
