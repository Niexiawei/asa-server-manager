import {computed, ref, watch} from 'vue'
import {createEmptyModel, mergeGameIni, parseGameIni} from '@/utils/arkGameIni.js'
import {
  applySettings,
  createEmptyUiModel,
  defaultUiValue,
  parseSettings,
  SETTING_GROUPS,
  SETTINGS_REGISTRY,
} from '@/utils/arkSimpleSettings.js'

// 从 AdvancedGameConfigDialog.vue 的 <script setup> 抽取：解析两份 INI + 简单键，
// 计算每个分区「已配置项数」，合并回文件并产出 { gameIni, gameUserSettings, customStartParameters }。
// 输入是三个 ref（详情页持有的最新文件内容）；组件只负责渲染 sections 与保存按钮。

const CAVE_FLYERS_FLAG = '-ForceAllowCaveFlyers'

const keySetForPanel = (panel) =>
    new Set(
        SETTING_GROUPS.filter((g) => g.panel === panel).flatMap((g) =>
            SETTINGS_REGISTRY.filter((r) => r.group === g.key).map((r) => r.key),
        ),
    )

const dinoKeys = keySetForPanel('dino')
const basicKeys = keySetForPanel('basic')
const worldKeys = keySetForPanel('world')
const tribeKeys = keySetForPanel('tribe')
const maxqtyKeys = keySetForPanel('maxqty')

const simpleCountFor = (keySet, m) =>
    SETTINGS_REGISTRY.filter((r) => keySet.has(r.key)).reduce(
        (s, reg) => s + (m[reg.key] !== defaultUiValue(reg) ? 1 : 0), 0,
    )

export const RULES_PANELS = [
  {value: 'basic', no: 1, title: '基础规则设置', sub: 'PvP / PvE 规则、建造限制、补给箱 / 钓鱼（GameUserSettings.ini + Game.ini）'},
  {value: 'world', no: 2, title: '环境配置', sub: '难度、昼夜、玩家属性、采集、建筑数量与拾取等全局倍率'},
  {value: 'tribe', no: 3, title: '部落设置', sub: '离线突袭保护（ORP）、部落规则与限制'},
  {value: 'dino', no: 4, title: '生物设置', sub: '数量上限、全局倍率、行为规则，以及按生物类名精细配置'},
  {value: 'engram', no: 5, title: '印痕条目覆盖', sub: '隐藏 / 消耗点数 / 等级需求 / 前置（Engram Entries）'},
  {value: 'crafting', no: 6, title: '物品制作消耗', sub: '自定义配方资源（ConfigOverrideItemCraftingCosts）'},
  {value: 'maxqty', no: 7, title: '物品最大堆叠', sub: '单物品堆叠上限（ConfigOverrideItemMaxQuantity）'},
  {value: 'levels', no: 8, title: '玩家与驯养等级覆盖', sub: '最大等级 / 经验曲线 / 印痕点数'},
  {value: 'stats', no: 9, title: '属性倍率', sub: '每级属性点倍率（PerLevelStatsMultiplier）'},
]

export function useArkRulesModel({gameIniContent, gameUserSettingsContent, customStartParameters}) {
  const model = ref(createEmptyModel())         // Game.ini 数组/嵌套
  const simpleModel = ref(createEmptyUiModel())  // 跨两文件的简单 key=value
  const caveFlyers = ref(false)
  let meta = null
  let baseline = ''

  const snapshot = () =>
      JSON.stringify({m: model.value, s: simpleModel.value, c: caveFlyers.value})

  // 基于最新文件内容重新解析，并把当前状态作为脏检测基线
  const reparse = () => {
    const parsed = parseGameIni(gameIniContent.value || '')
    model.value = parsed.model
    meta = parsed.meta
    simpleModel.value = parseSettings(gameIniContent.value || '', gameUserSettingsContent.value || '').model
    caveFlyers.value = new RegExp(`\\${CAVE_FLYERS_FLAG}\\b`, 'i').test(customStartParameters.value || '')
    baseline = snapshot()
  }

  // 文件内容变化（首次挂载 / 详情页保存后刷新）时重新解析
  watch(
      [gameIniContent, gameUserSettingsContent, customStartParameters],
      reparse,
      {immediate: true},
  )

  const counts = computed(() => {
    const m = model.value
    const sm = simpleModel.value
    return {
      basic: simpleCountFor(basicKeys, sm),
      world: simpleCountFor(worldKeys, sm),
      tribe: simpleCountFor(tribeKeys, sm),
      dino: Object.values(m.classMultipliers).reduce((s, a) => s + a.length, 0)
          + simpleCountFor(dinoKeys, sm) + (caveFlyers.value ? 1 : 0),
      engram: m.engrams.length + m.autoUnlocks.length,
      crafting: m.craftingCosts.length,
      maxqty: m.maxQuantity.length + simpleCountFor(maxqtyKeys, sm),
      levels: m.levels.player.length + m.levels.dino.length + m.engramPoints.length,
      stats: Object.values(m.stats).reduce((s, o) => s + Object.keys(o).length, 0),
    }
  })

  const totalConfigured = computed(() =>
      Object.values(counts.value).reduce((a, b) => a + b, 0),
  )

  const dirty = ref(false)
  watch([model, simpleModel, caveFlyers], () => {
    dirty.value = snapshot() !== baseline
  }, {deep: true})

  const reset = () => reparse()

  // 先合并 Game.ini 数组键，再把跨文件简单键合并进两文件（键互不相交，顺序安全）
  const buildPayload = () => {
    if (!meta) meta = parseGameIni(gameIniContent.value || '').meta
    const mergedGameArrays = mergeGameIni(meta, model.value)
    const {gameIni, gameUserSettings} = applySettings(
        mergedGameArrays,
        gameUserSettingsContent.value || '',
        simpleModel.value,
    )
    let params = (customStartParameters.value || '')
        .replace(new RegExp(`\\s*\\${CAVE_FLYERS_FLAG}\\b`, 'gi'), '')
        .trimStart()
    if (caveFlyers.value) params = params ? `${params} ${CAVE_FLYERS_FLAG}` : CAVE_FLYERS_FLAG
    return {gameIni, gameUserSettings, customStartParameters: params}
  }

  return {model, simpleModel, caveFlyers, counts, totalConfigured, dirty, reset, reparse, buildPayload}
}
