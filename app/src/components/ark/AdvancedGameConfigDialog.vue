<template>
  <t-dialog
      :visible="visible"
      header="服务器规则配置"
      mode="full-screen"
      :confirm-btn="{ content: '保存到 Game.ini', loading: saving, theme: 'primary' }"
      cancel-btn="取消"
      @confirm="handleConfirm"
      @close="handleClose"
      dialogClassName="server-cfg-edit-dialog"
  >
    <div class="adv-body">
      <!-- 信息band：保留说明 + 已配置统计 -->
      <div class="adv-hero">
        <div class="adv-hero-info">
          <info-circle-icon class="adv-hero-icon"/>
          <span>
            仅接管下列配置项（按需写入 <code>Game.ini</code> 与 <code>GameUserSettings.ini</code> 的对应节），
            文件中的注释、其它设置与未识别内容会被<strong>完整保留</strong>。
          </span>
        </div>
        <div class="adv-hero-stat">
          <span class="adv-hero-stat-num">{{ totalConfigured }}</span>
          <span class="adv-hero-stat-label">项已配置</span>
        </div>
      </div>

      <div class="adv-scroll">
        <t-collapse v-model="activePanels" :expand-mutex="true" class="adv-collapse">
          <t-collapse-panel
              v-for="p in panels"
              :key="p.value"
              :value="p.value"
          >
            <template #header>
              <div class="panel-head">
                <span class="panel-no">{{ p.no }}</span>
                <div class="panel-titles">
                  <span class="panel-title">{{ p.title }}</span>
                  <span class="panel-sub">{{ p.sub }}</span>
                </div>
                <t-tag
                    v-if="counts[p.value]"
                    theme="primary"
                    variant="light"
                    size="small"
                    class="panel-count"
                >
                  {{ counts[p.value] }} 项
                </t-tag>
              </div>
            </template>

            <div class="panel-body">
              <basic-rules-section v-if="p.value === 'basic'" :model="simpleModel"/>
              <world-section v-else-if="p.value === 'world'" :model="simpleModel"/>
              <tribe-section v-else-if="p.value === 'tribe'" :model="simpleModel"/>
              <dino-multipliers-section v-else-if="p.value === 'dino'" :model="model.classMultipliers" :simple-model="simpleModel" :cave-flyers="caveFlyers" @update:cave-flyers="caveFlyers = $event"/>
              <engram-overrides-section v-else-if="p.value === 'engram'" :model="model.engrams" :auto-unlocks="model.autoUnlocks"/>
              <crafting-costs-section v-else-if="p.value === 'crafting'" :model="model.craftingCosts"/>
              <item-max-quantity-section v-else-if="p.value === 'maxqty'" :model="model.maxQuantity" :simple-model="simpleModel"/>
              <level-overrides-section
                  v-else-if="p.value === 'levels'"
                  :player="model.levels.player"
                  :dino="model.levels.dino"
                  :engram-points="model.engramPoints"
              />
              <stats-multipliers-section v-else-if="p.value === 'stats'" :model="model.stats"/>
            </div>
          </t-collapse-panel>
        </t-collapse>
      </div>
    </div>
  </t-dialog>
</template>

<script setup>
import {computed, ref, watch} from 'vue'
import {InfoCircleIcon} from 'tdesign-icons-vue-next'
import {createEmptyModel, mergeGameIni, parseGameIni} from '@/utils/arkGameIni.js'
import {
  applySettings,
  createEmptyUiModel,
  defaultUiValue,
  parseSettings,
  SETTING_GROUPS,
  SETTINGS_REGISTRY,
} from '@/utils/arkSimpleSettings.js'
import BasicRulesSection from './sections/BasicRulesSection.vue'
import WorldSection from './sections/WorldSection.vue'
import TribeSection from './sections/TribeSection.vue'
import DinoMultipliersSection from './sections/DinoMultipliersSection.vue'
import EngramOverridesSection from './sections/EngramOverridesSection.vue'
import CraftingCostsSection from './sections/CraftingCostsSection.vue'
import ItemMaxQuantitySection from './sections/ItemMaxQuantitySection.vue'
import LevelOverridesSection from './sections/LevelOverridesSection.vue'
import StatsMultipliersSection from './sections/StatsMultipliersSection.vue'

const props = defineProps({
  visible: {type: Boolean, default: false},
  gameIniContent: {type: String, default: ''},
  gameUserSettingsContent: {type: String, default: ''},
  customStartParameters: {type: String, default: ''},
  saving: {type: Boolean, default: false},
})
const emit = defineEmits(['update:visible', 'save'])

const model = ref(createEmptyModel())          // Game.ini 数组/嵌套
const simpleModel = ref(createEmptyUiModel())   // 跨两文件的简单 key=value
const caveFlyers = ref(false)
let meta = null
const activePanels = ref([])

const panels = [
  {
    value: 'basic',
    no: 1,
    title: '基础规则设置',
    sub: 'PvP / PvE 规则、建造限制、补给箱 / 钓鱼（GameUserSettings.ini + Game.ini）'
  },
  {value: 'world', no: 2, title: '环境配置', sub: '难度、昼夜、玩家属性、采集、建筑数量与拾取等全局倍率'},
  {value: 'tribe', no: 3, title: '部落设置', sub: '离线突袭保护（ORP）、部落规则与限制'},
  {value: 'dino', no: 4, title: '生物设置', sub: '数量上限、全局倍率、行为规则，以及按生物类名精细配置'},
  {value: 'engram', no: 5, title: '印痕条目覆盖', sub: '隐藏 / 消耗点数 / 等级需求 / 前置（Engram Entries）'},
  {value: 'crafting', no: 6, title: '物品制作消耗', sub: '自定义配方资源（ConfigOverrideItemCraftingCosts）'},
  {value: 'maxqty', no: 7, title: '物品最大堆叠', sub: '单物品堆叠上限（ConfigOverrideItemMaxQuantity）'},
  {value: 'levels', no: 8, title: '玩家与驯养等级覆盖', sub: '最大等级 / 经验曲线 / 印痕点数'},
  {value: 'stats', no: 9, title: '属性倍率', sub: '每级属性点倍率（PerLevelStatsMultiplier）'},
]

const dinoKeys = new Set(
    SETTING_GROUPS.filter((g) => g.panel === 'dino').flatMap((g) =>
        SETTINGS_REGISTRY.filter((r) => r.group === g.key).map((r) => r.key),
    ),
)
const dinoSimpleCount = (m) =>
    SETTINGS_REGISTRY.filter((r) => dinoKeys.has(r.key)).reduce(
        (s, reg) => s + (m[reg.key] !== defaultUiValue(reg) ? 1 : 0), 0,
    )

const basicKeys = new Set(
    SETTING_GROUPS.filter((g) => g.panel === 'basic').flatMap((g) =>
        SETTINGS_REGISTRY.filter((r) => r.group === g.key).map((r) => r.key),
    ),
)
const worldKeys = new Set(
    SETTING_GROUPS.filter((g) => g.panel === 'world').flatMap((g) =>
        SETTINGS_REGISTRY.filter((r) => r.group === g.key).map((r) => r.key),
    ),
)
const tribeKeys = new Set(
    SETTING_GROUPS.filter((g) => g.panel === 'tribe').flatMap((g) =>
        SETTINGS_REGISTRY.filter((r) => r.group === g.key).map((r) => r.key),
    ),
)
const maxqtyKeys = new Set(
    SETTING_GROUPS.filter((g) => g.panel === 'maxqty').flatMap((g) =>
        SETTINGS_REGISTRY.filter((r) => r.group === g.key).map((r) => r.key),
    ),
)

const basicCount = (m) =>
    SETTINGS_REGISTRY.filter((r) => basicKeys.has(r.key)).reduce(
        (s, reg) => s + (m[reg.key] !== defaultUiValue(reg) ? 1 : 0), 0,
    )
const worldCount = (m) =>
    SETTINGS_REGISTRY.filter((r) => worldKeys.has(r.key)).reduce(
        (s, reg) => s + (m[reg.key] !== defaultUiValue(reg) ? 1 : 0), 0,
    )
const tribeCount = (m) =>
    SETTINGS_REGISTRY.filter((r) => tribeKeys.has(r.key)).reduce(
        (s, reg) => s + (m[reg.key] !== defaultUiValue(reg) ? 1 : 0), 0,
    )
const maxqtySimpleCount = (m) =>
    SETTINGS_REGISTRY.filter((r) => maxqtyKeys.has(r.key)).reduce(
        (s, reg) => s + (m[reg.key] !== defaultUiValue(reg) ? 1 : 0), 0,
    )

const counts = computed(() => {
  const m = model.value
  return {
    basic: basicCount(simpleModel.value),
    world: worldCount(simpleModel.value),
    tribe: tribeCount(simpleModel.value),
    dino: Object.values(m.classMultipliers).reduce((s, a) => s + a.length, 0) + dinoSimpleCount(simpleModel.value) + (caveFlyers.value ? 1 : 0),
    engram: m.engrams.length + m.autoUnlocks.length,
    crafting: m.craftingCosts.length,
    maxqty: m.maxQuantity.length + maxqtySimpleCount(simpleModel.value),
    levels: m.levels.player.length + m.levels.dino.length + m.engramPoints.length,
    stats: Object.values(m.stats).reduce((s, o) => s + Object.keys(o).length, 0),
  }
})
const totalConfigured = computed(() =>
    Object.values(counts.value).reduce((a, b) => a + b, 0),
)

// 每次打开时基于最新文件内容重新解析，并自动展开有内容的分区
watch(
    () => props.visible,
    (v) => {
      if (!v) return
      const parsed = parseGameIni(props.gameIniContent || '')
      model.value = parsed.model
      meta = parsed.meta
      simpleModel.value = parseSettings(props.gameIniContent || '', props.gameUserSettingsContent || '').model
      caveFlyers.value = /-ForceAllowCaveFlyers\b/i.test(props.customStartParameters || '')
      const open = panels.filter((p) => counts.value[p.value]).map((p) => p.value)
      //activePanels.value = open.length ? open : ['basic']
      activePanels.value = ['basic']
    },
)

const handleConfirm = () => {
  if (!meta) meta = parseGameIni(props.gameIniContent || '').meta
  // 先合并 Game.ini 数组键，再把跨文件简单键合并进两文件（键互不相交，顺序安全）
  const mergedGameArrays = mergeGameIni(meta, model.value)
  const {gameIni, gameUserSettings} = applySettings(
      mergedGameArrays,
      props.gameUserSettingsContent || '',
      simpleModel.value,
  )
  const FLAG = '-ForceAllowCaveFlyers'
  let params = (props.customStartParameters || '').replace(/\s*-ForceAllowCaveFlyers\b/gi, '').trimStart()
  if (caveFlyers.value) params = params ? `${params} ${FLAG}` : FLAG
  emit('save', {gameIni, gameUserSettings, customStartParameters: params})
}

const handleClose = () => emit('update:visible', false)
</script>

<style scoped>
/* 让滚动发生在 body 内部：body 不滚，内部 .adv-scroll 滚 */
:deep(.t-dialog__body) {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.adv-body {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 4px;
  box-sizing: border-box;
  height: calc(100vh - 56px - 50px - 64px);
  overflow-y: auto;
}

.adv-scroll {

}

/* 信息 band（固定，不随内容滚动） */
.adv-hero {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 18px;
  border: 1px solid var(--td-component-border, #dcdcdc);
  border-left: 3px solid var(--td-brand-color, #0052d9);
  border-radius: 10px;
  background: var(--td-bg-color-container, #fff);
}

.adv-hero-info {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--td-text-color-secondary, #5a5a5a);
}

.adv-hero-icon {
  flex: 0 0 auto;
  margin-top: 2px;
  font-size: 16px;
  color: var(--td-brand-color, #0052d9);
}

.adv-hero-info code {
  background: var(--td-bg-color-component, #f3f3f3);
  padding: 1px 6px;
  border-radius: 4px;
  font-size: 12px;
  color: var(--td-text-color-primary, #222);
}

.adv-hero-info strong {
  color: var(--td-brand-color, #0052d9);
}

.adv-hero-stat {
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-width: 84px;
  padding: 6px 16px;
  border-radius: 10px;
  background: var(--td-brand-color-light, #f2f3ff);
}

.adv-hero-stat-num {
  font-size: 24px;
  font-weight: 700;
  line-height: 1.1;
  font-variant-numeric: tabular-nums;
  color: var(--td-brand-color, #0052d9);
}

.adv-hero-stat-label {
  font-size: 11px;
  color: var(--td-text-color-secondary, #888);
}

/* 折叠卡片 */
.adv-collapse {
  display: flex;
  flex-direction: column;
  gap: 12px;
  border: none;
  background: transparent;
}

.adv-collapse :deep(.t-collapse-panel) {
  border: 1px solid var(--td-component-border, #e3e3e3);
  border-radius: 10px;
  overflow: hidden;
  background: var(--td-bg-color-container, #fff);
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
}

.adv-collapse :deep(.t-collapse-panel:hover) {
  border-color: var(--td-brand-color-hover, #618dfa);
  box-shadow: 0 2px 10px rgba(0, 82, 217, 0.08);
}

.adv-collapse :deep(.t-collapse-panel__header) {
  padding: 14px 16px;
}

.adv-collapse :deep(.t-collapse-panel__body) {
  border-top: 1px solid var(--td-component-stroke, #f0f0f0);
  background: var(--td-bg-color-container, #fff);
}

.adv-collapse :deep(.t-collapse-panel__content) {
  padding: 16px;
}

/* 分区头部 */
.panel-head {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}

.panel-no {
  flex: 0 0 auto;
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 7px;
  background: var(--td-brand-color, #0052d9);
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.panel-titles {
  display: flex;
  flex-direction: column;
  min-width: 0;
  gap: 1px;
}

.panel-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--td-text-color-primary, #1f1f1f);
}

.panel-sub {
  font-size: 12px;
  color: var(--td-text-color-placeholder, #999);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.panel-count {
  margin-left: auto;
  flex: 0 0 auto;
  font-variant-numeric: tabular-nums;
}

@media (max-width: 768px) {
  .adv-hero {
    flex-direction: column;
    align-items: stretch;
  }

  .adv-hero-stat {
    flex-direction: row;
    gap: 8px;
    align-self: flex-start;
  }

  .panel-sub {
    display: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .adv-collapse :deep(.t-collapse-panel) {
    transition: none;
  }
}
</style>
