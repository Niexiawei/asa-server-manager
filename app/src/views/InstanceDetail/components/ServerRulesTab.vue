<template>
  <div class="rules-tab">
    <div class="rules-toolbar">
      <div class="rules-hero">
        <info-circle-icon class="rules-hero-icon"/>
        <span>
          仅接管下列配置项（按需写入 <code>Game.ini</code> 与 <code>GameUserSettings.ini</code> 的对应节），
          文件中的注释、其它设置与未识别内容会被<strong>完整保留</strong>。
        </span>
      </div>
      <div class="rules-toolbar-right">
        <span class="rules-stat"><b>{{ totalConfigured }}</b> 项已配置</span>
        <span v-if="running" class="toolbar-hint">运行中不可保存</span>
        <span v-else-if="dirty" class="toolbar-hint dirty">有未保存的修改</span>
        <t-button variant="outline" :disabled="!dirty" @click="reset">重置</t-button>
        <t-button theme="primary" :loading="saving" :disabled="running" @click="onSave">保存</t-button>
      </div>
    </div>

    <t-tabs v-model="area" :addable="false" class="rules-area-tabs">
      <t-tab-panel v-for="p in RULES_PANELS" :key="p.value" :value="p.value">
        <template #label>
          <span class="area-label">
            {{ p.title }}
            <t-tag v-if="counts[p.value]" theme="primary" variant="light" size="small">
              {{ counts[p.value] }}
            </t-tag>
          </span>
        </template>

        <div class="area-body">
          <p class="area-sub">{{ p.sub }}</p>
          <basic-rules-section v-if="p.value === 'basic'" :model="simpleModel"/>
          <world-section v-else-if="p.value === 'world'" :model="simpleModel"/>
          <tribe-section v-else-if="p.value === 'tribe'" :model="simpleModel"/>
          <dino-multipliers-section
              v-else-if="p.value === 'dino'"
              :model="model.classMultipliers"
              :simple-model="simpleModel"
              :cave-flyers="caveFlyers"
              @update:cave-flyers="caveFlyers = $event"
          />
          <engram-overrides-section
              v-else-if="p.value === 'engram'"
              :model="model.engrams"
              :auto-unlocks="model.autoUnlocks"
          />
          <crafting-costs-section v-else-if="p.value === 'crafting'" :model="model.craftingCosts"/>
          <item-max-quantity-section
              v-else-if="p.value === 'maxqty'"
              :model="model.maxQuantity"
              :simple-model="simpleModel"
          />
          <level-overrides-section
              v-else-if="p.value === 'levels'"
              :player="model.levels.player"
              :dino="model.levels.dino"
              :engram-points="model.engramPoints"
          />
          <stats-multipliers-section v-else-if="p.value === 'stats'" :model="model.stats"/>
        </div>
      </t-tab-panel>
    </t-tabs>
  </div>
</template>

<script setup>
import {ref, toRef, watch} from 'vue'
import {InfoCircleIcon} from 'tdesign-icons-vue-next'
import {RULES_PANELS, useArkRulesModel} from '@/composables/useArkRulesModel.js'
import BasicRulesSection from '@/components/ark/sections/BasicRulesSection.vue'
import WorldSection from '@/components/ark/sections/WorldSection.vue'
import TribeSection from '@/components/ark/sections/TribeSection.vue'
import DinoMultipliersSection from '@/components/ark/sections/DinoMultipliersSection.vue'
import EngramOverridesSection from '@/components/ark/sections/EngramOverridesSection.vue'
import CraftingCostsSection from '@/components/ark/sections/CraftingCostsSection.vue'
import ItemMaxQuantitySection from '@/components/ark/sections/ItemMaxQuantitySection.vue'
import LevelOverridesSection from '@/components/ark/sections/LevelOverridesSection.vue'
import StatsMultipliersSection from '@/components/ark/sections/StatsMultipliersSection.vue'

const props = defineProps({
  gameIniContent: {type: String, default: ''},
  gameUserSettingsContent: {type: String, default: ''},
  customStartParameters: {type: String, default: ''},
  saving: {type: Boolean, default: false},
  running: {type: Boolean, default: false},
})
const emit = defineEmits(['save', 'update:dirty'])

const area = ref('basic')

const {model, simpleModel, caveFlyers, counts, totalConfigured, dirty, reset, buildPayload} =
    useArkRulesModel({
      gameIniContent: toRef(props, 'gameIniContent'),
      gameUserSettingsContent: toRef(props, 'gameUserSettingsContent'),
      customStartParameters: toRef(props, 'customStartParameters'),
    })

watch(dirty, (v) => emit('update:dirty', v), {immediate: true})

const onSave = () => emit('save', buildPayload())
</script>

<style scoped lang="less">
.rules-tab {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.rules-toolbar {
  position: sticky;
  top: 0;
  z-index: 2;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 16px;
  border: 1px solid var(--td-component-border, #dcdcdc);
  border-left: 3px solid var(--td-brand-color, #0052d9);
  border-radius: 10px;
  background: var(--td-bg-color-container, #fff);
}

.rules-hero {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--td-text-color-secondary, #5a5a5a);

  code {
    background: var(--td-bg-color-component, #f3f3f3);
    padding: 1px 6px;
    border-radius: 4px;
    font-size: 12px;
  }

  strong {
    color: var(--td-brand-color, #0052d9);
  }
}

.rules-hero-icon {
  flex: 0 0 auto;
  margin-top: 2px;
  font-size: 16px;
  color: var(--td-brand-color, #0052d9);
}

.rules-toolbar-right {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 10px;
}

.rules-stat {
  font-size: 12px;
  color: var(--td-text-color-secondary, #888);

  b {
    font-size: 18px;
    color: var(--td-brand-color, #0052d9);
    font-variant-numeric: tabular-nums;
  }
}

.toolbar-hint {
  font-size: 12px;
  color: var(--td-text-color-placeholder, #999);

  &.dirty {
    color: var(--td-warning-color, #e37318);
  }
}

.area-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.area-body {
  padding-top: 8px;
}

.area-sub {
  margin: 0 0 12px;
  font-size: 12px;
  color: var(--td-text-color-placeholder, #999);
}
</style>
