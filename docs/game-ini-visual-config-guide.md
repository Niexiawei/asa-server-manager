# 服务器可视化配置 —— 开发对接指南

> **用途**: 记录「服务器可视化配置」前端功能的架构与约定，作为后续**新增其它配置项**的对接手册。
> **范围**: 纯前端解析/编辑/合并，复用现有整文件 PUT 接口，后端零改动。涵盖两类项：
> - **数组/嵌套项**（Game.ini）→ `arkGameIni.js`，见第 3–5 节；
> - **简单 key=value 项**（可跨 GameUserSettings.ini `[ServerSettings]` 与 Game.ini）→ `arkSimpleSettings.js`，见第 6 节。
> **相关文档**: [`asa-item-ids.md`](./asa-item-ids.md) · [`asa-creature-ids.md`](./asa-creature-ids.md) · [`asa-server-configuration.md`](./asa-server-configuration.md)

---

## 1. 整体架构与数据流

所有受管配置项都位于 Game.ini 的 `[/script/shootergame.shootergamemode]` 节。

```
读取: getGameIni(instance)  ──►  Game.ini 原始文本
          │
          ▼  parseGameIni(text)          (app/src/utils/arkGameIni.js)
   { model, meta }
   model = 结构化可编辑数据（各分区一块）
   meta  = 原始行 / 行尾 / 被吸收行下标 / 目标节范围（用于无损合并）
          │
          ▼  各 section 组件直接修改 model（push / splice / delete，Vue 响应式）
          │
          ▼  mergeGameIni(meta, model)    ──►  新整文件文本
          │   仅替换受管行；注释、其它节、未识别键逐字保留
          ▼
保存: updateGameIni(instance, mergedText)   （复用 InstanceDetail.saveGameIni → 整文件 PUT）
```

**核心原则**: 工具只接管「受管键」，其余文件内容（注释 / 未识别键 / 其它节）原样保留。解析失败的单行降级为"保留 + 不结构化"，绝不吞内容。

---

## 2. 文件清单与职责

| 文件 | 职责 |
|------|------|
| `app/src/utils/arkGameIni.js` | **核心(数组/嵌套)**。Game.ini 受管键登记、解析/序列化/合并、INI 字符串工具、属性索引表 |
| `app/src/utils/arkSimpleSettings.js` | **核心(简单项)**。跨两文件的 `key=value` 注册表 + 通用节内解析/序列化/合并（见第 6 节） |
| `app/scripts/gen-ark-data.mjs` | 构建期脚本：解析 docs 的 md 表格 → 生成数据集（`npm run gen:data`） |
| `app/src/data/ark-items.json` / `ark-creatures.json` | 生成产物（入库），下拉用动态 `import()` 懒加载 |
| `app/src/components/ark/ArkClassSelect.vue` | 通用下拉：虚拟滚动 + 可搜索（名称/ClassName）+ 可输入自定义（Mod 兼容） |
| `app/src/components/ark/CreatureSelect.vue` / `ItemSelect.vue` | `ArkClassSelect` 的薄封装（生物/物品数据集） |
| `app/src/components/ark/AdvancedGameConfigDialog.vue` | 全屏弹窗外壳：打开时 parse（两类）、确认时 merge 出两文件、按分区渲染折叠面板 |
| `app/src/components/ark/sections/*.vue` | 配置分区组件（数组分区 + `BasicRulesSection.vue` 基础规则 + `WorldSection.vue` 环境配置 + `TribeSection.vue` 部落设置 + `DinoMultipliersSection.vue` 生物设置，复合组件：上半部渲染 `dino_*` 简单项，下半部保留 per-class tab 编辑器） |
| `app/src/components/ark/sections/section.css` | 分区共享样式（行/卡片/空状态/网格，TDesign 令牌 + hover/focus） |
| `app/src/views/InstanceDetail.vue` | 接入点：「服务器配置」卡片操作区「服务器规则配置」按钮 + 挂载弹窗 + `saveAdvancedConfig`（按需保存两文件） |

---

## 3. 数据模型 (model) 结构

`createEmptyModel()` 返回，`parseGameIni()` 填充：

```js
{
  classMultipliers: {            // 生物倍率，6 个键各一个数组
    DinoClassDamageMultipliers: [{ className, multiplier }],
    TamedDinoClassDamageMultipliers: [...],
    DinoClassResistanceMultipliers: [...],
    TamedDinoClassResistanceMultipliers: [...],
    TamedDinoClassSpeedMultipliers: [...],
    TamedDinoClassStaminaMultipliers: [...],
  },
  engrams: [{ kind:'index'|'named', engramClassName, engramIndex,
              engramHidden, engramPointsCost, engramLevelRequirement, removeEngramPreReq }],
  autoUnlocks: [{ engramClassName, levelToAutoUnlock }],   // EngramEntryAutoUnlocks，0=始终解锁
  craftingCosts: [{ itemClassString,
                    resources: [{ resourceItemTypeString, baseResourceRequirement, requireExactType }] }],
  maxQuantity: [{ itemClassString, maxItemQuantity, ignoreMultiplier }],
  levels: { player: [xp...], dino: [xp...] },   // 顺序敏感：player 行在前，dino 行在后
  engramPoints: [points...],                     // 每个玩家等级一行
  stats: { Player:{}, DinoWild:{}, DinoTamed:{}, DinoTamed_Add:{}, DinoTamed_Affinity:{} },
                                                 // 每组为 { statIndex(0~11): 倍率 } 稀疏对象
}
```

数值字段：空值用 `''`（不输出该字段）；布尔默认 `false`。

---

## 4. 核心工具 arkGameIni.js

### 4.1 导出

| 导出 | 说明 |
|------|------|
| `parseGameIni(text)` → `{ model, meta }` | 解析整文件，得到可编辑 model + 合并所需 meta |
| `mergeGameIni(meta, model)` → `string` | 合并回整文件文本（无损保留未受管内容） |
| `createEmptyModel()` | 生成空 model |
| `STAT_INDICES` | 12 项属性索引表 `[{ index, cn, en }]`（0=生命…11=制作速度） |
| `STAT_GROUPS` | 属性分组 `[{ key, label }]`（Player / DinoWild / DinoTamed / DinoTamed_Add / DinoTamed_Affinity） |
| `CLASS_MULTIPLIER_KEYS` | 生物倍率 6 键的元数据 `[{ key, label, tamed, group }]` |

### 4.2 合并契约（meta）

`meta = { lines, eol, absorbed, target }`：
- `lines`: 原始行数组（已按 `\r?\n` 拆分）；`eol`: 探测到的行尾（保留 `\r\n` 或 `\n`）
- `absorbed`: `Set<行下标>` —— 被解析进 model 的受管行；合并时**只删除这些行**，序列化块插回首个被删行处
- `target`: 目标节范围 `{ headerIndex, endLine }`；节不存在则在文件末尾新建

### 4.3 内部 INI 字符串工具（新增解析器时复用）

| 函数 | 作用 |
|------|------|
| `splitTopLevel(str, sep)` | 按 sep 拆分，忽略括号/引号内的分隔符 |
| `stripOuterParens(s)` | 去掉**整体包裹**的一对最外层括号（带配平校验，`(a),(b)` 不会被误剥） |
| `parseKVs(inner)` | `Key=Value,Key=Value` → 对象（值保留原样含引号/括号） |
| `unquote(s)` / `toNum(s)` / `toBool(s)` | 去引号 / 转数字 / `True`→true |
| `fmtNum(n)` / `fmtBool(b)` | 数字格式化 / 布尔输出 `True`\|`False` |

---

## 5. 如何新增数组/嵌套配置项（分步）

> 简单 `key=value` 项请走第 6 节（注册表，更省事）。本节针对 Game.ini 的数组/嵌套结构。

以新增 **`HarvestResourceItemAmountClassMultipliers`**（采集产出倍率，格式同生物倍率：`(ClassName="...",Multiplier=<float>)`）为例。

### 步骤 1 —— 确认 INI 格式归类

| 格式 | 示例 | 处理方式 |
|------|------|----------|
| A. 重复结构行 | `Key=(FieldA="x",FieldB=2.0)` | `parseKVs(stripOuterParens(value))`，model 存数组 |
| B. 嵌套结构 | `Key=(Outer="x",Inner=((..),(..)))` | A 之上再 `stripOuterParens`+`splitTopLevel` 拆内层 |
| C. 索引/标量行 | `Key[3]=1.5` / 重复 `Key=5` | 正则取下标 / 按出现顺序收集为数组 |

本例为 **格式 A**。

### 步骤 2 —— `arkGameIni.js`：加模型字段

```js
// createEmptyModel() 内
return {
  ...,
  harvestItemMultipliers: [],   // 新增
}
```

### 步骤 3 —— `arkGameIni.js`：加解析分派

在 `parseGameIni` 的 `try{}` 分派链里加分支（放在合适位置）：

```js
} else if (key === 'HarvestResourceItemAmountClassMultipliers') {
  model.harvestItemMultipliers.push(parseClassMultiplier(value)) // 复用已有解析器
  absorbed.add(i)
}
```

> 若是全新格式，仿照 `parseCraftingCost` / `parseMaxQuantity` 写一个 `parseXxx(value)`，用 4.3 的工具函数实现。

### 步骤 4 —— `arkGameIni.js`：加序列化

在 `serializeManaged(model)` 里追加（注意稳定顺序、过滤空标识）：

```js
for (const row of model.harvestItemMultipliers || []) {
  if (!row.className) continue
  out.push(`HarvestResourceItemAmountClassMultipliers=(ClassName="${row.className}",Multiplier=${fmtNum(row.multiplier)})`)
}
```

### 步骤 5 —— 新建分区组件 `sections/HarvestMultipliersSection.vue`

复用 `section.css` 的 `.section/.toolbar/.empty/.row/.cell` 与下拉组件、TDesign 栅格：

```vue
<template>
  <div class="section">
    <p class="section-tip">按物品类名自定义采集产出倍率。</p>
    <div class="toolbar">
      <span class="only-tamed-hint">共 {{ model.length }} 项</span>
      <t-button theme="primary" size="small" @click="add"><template #icon><add-icon/></template>添加物品</t-button>
    </div>
    <div v-if="model.length === 0" class="empty">暂无配置</div>
    <t-row v-else :gutter="[12, 12]">
      <t-col v-for="(row, i) in model" :key="i" :xs="12" :md="6">
        <div class="row">
          <div class="cell grow"><item-select v-model="row.className"/></div>
          <div class="cell"><t-input-number v-model="row.multiplier" :min="0" :step="0.1" theme="column" align="right"/></div>
          <t-button variant="text" theme="danger" shape="square" @click="model.splice(i,1)">
            <template #icon><delete-icon/></template>
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
const add = () => props.model.push({className: '', multiplier: 1})
</script>
<style scoped src="./section.css"></style>
```

> **约定**: 分区组件通过 `props.model`（model 的某一块，引用传递）直接 `push/splice/delete` 修改，依赖 Vue 响应式；不要重新赋值整个 prop。

### 步骤 6 —— `AdvancedGameConfigDialog.vue`：注册分区

1. `import HarvestMultipliersSection from './sections/HarvestMultipliersSection.vue'`
2. `panels` 数组追加一项：`{ value: 'harvest', no: 7, title: '采集产出倍率', sub: '按物品类名（HarvestResourceItemAmountClassMultipliers）' }`
3. `counts` computed 追加：`harvest: m.harvestItemMultipliers.length`
4. 面板 body 的 `v-if` 链追加：
   ```vue
   <harvest-multipliers-section v-else-if="p.value === 'harvest'" :model="model.harvestItemMultipliers"/>
   ```

### 步骤 7 —— 数据集（如需新下拉）

物品/生物已有数据集，直接用 `ItemSelect` / `CreatureSelect`。
若需**新数据来源**：在 `gen-ark-data.mjs` 增加 `extract(...)` 输出新 JSON，加 `npm run gen:data` 重新生成，再在 `ArkClassSelect.loadDataset` 加分支。

### 步骤 8 —— 验证（见第 9 节）

---

## 6. 新增「简单设置」（key=value，注册表驱动，可跨两文件）

简单标量/布尔项（PvP/PvE 规则、各类开关与倍率）与第 5 节的数组项不同：它们是 `key=value`，可能落在 **GameUserSettings.ini `[ServerSettings]`** 或 **Game.ini `[/script/shootergame.shootergamemode]`**。这类项由 `app/src/utils/arkSimpleSettings.js` **注册表驱动**——新增一项只需加一行，UI/解析/序列化全自动。

### 6.1 核心：arkSimpleSettings.js

| 导出 | 说明 |
|------|------|
| `SETTINGS_REGISTRY` | 全部简单项声明 `[{ key, file:'game'\|'gus', type:'bool'\|'float'\|'int', default, inverse, group, label, tip }]` |
| `SETTING_GROUPS` | 分组 `[{ key, label, panel:'basic'\|'world'\|'tribe'\|'dino' }]`，决定 UI 小节顺序及所属面板 |
| `createEmptyUiModel()` | 全部项取默认 UI 值（key→bool/number） |
| `parseSettings(gameText, gusText)` → `{ model, ... }` | 从两文件解析出可编辑 UI 模型（应用 inverse） |
| `applySettings(gameText, gusText, model)` → `{ gameIni, gameUserSettings }` | 把 UI 模型合并回两文件（无损保留其余内容） |
| `defaultUiValue(reg)` | 该项默认 UI 值（用于「是否非默认」计数） |

- **inverse**：UI 开关与 INI 语义相反（如「开启 PvP」= `serverPVE=False`、「开启队友伤害」= `bDisableFriendlyFire=False`）。`default` 为 INI 原生默认值。
- **写盘策略**（`serializeScalars`）：仅写「非默认 **或** 原文件已存在」的键 —— 回默认且原无 → 不写；回默认但原有 → 写默认值（保留显式声明）。布尔输出 `True/False`，浮点用 `arkGameIni` 同款数字格式，整数用 `String(Math.round(n))`。
- **`panel` 字段**：`SETTING_GROUPS` 每项带 `panel: 'basic' | 'world' | 'tribe' | 'dino'`，用于将分组分派到不同面板组件。`BasicRulesSection.vue` 渲染 `panel === 'basic'` 的组；`WorldSection.vue` 渲染 `panel === 'world'` 的组；`TribeSection.vue` 渲染 `panel === 'tribe'` 的组；`DinoMultipliersSection.vue` 上半部渲染 `panel === 'dino'` 的组（下半部保留 per-class tab 编辑器）。`AdvancedGameConfigDialog.vue` 中 `basicCount` / `worldCount` / `tribeCount` / `dinoSimpleCount` 各自只统计本面板的非默认项数；`dino` 面板徽章 = `dinoSimpleCount + classMultipliers` 条目数之和。
- 合并语义与 `arkGameIni` 一致（`absorbed` 删旧行、块插回首个被删行处、节不存在则新建），见 4.2。

### 6.2 新增一个简单项（一行）

只在 `SETTINGS_REGISTRY` 加一行即可：

```js
// 布尔类
{ key: 'DisableStructureDecayPvE', file: 'gus', type: 'bool', default: false, inverse: false,
  group: 'build', label: '禁用 PvE 建筑衰减', tip: '关闭玩家建筑的自动衰减。' },
// 浮点倍率类
{ key: 'TamingSpeedMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false,
  group: 'misc', label: '驯服速度倍数', tip: '越大越快（默认 1.0）。' },
// 整数类（type: 'int'）
{ key: 'MaxTributeDinos', file: 'gus', type: 'int', default: 20, inverse: false,
  group: 'cross', label: '最大恐龙上传数量', tip: '同一时间最多可上传的恐龙数量，默认 20。' },
```

要点：
- `file`：`'gus'` → GameUserSettings.ini `[ServerSettings]`；`'game'` → Game.ini `[/script/shootergame.shootergamemode]`。
- `inverse: true` 仅用于「开启 X」但 INI 是「DisableX / 取反」语义的布尔。
- `type: 'int'` 用于只允许整数的字段（人数上限、秒数、数量上限等）；UI 控件显示步进 1、无小数，落盘用 `String(Math.round(n))`。
- 新增 `group` 需在 `SETTING_GROUPS` 也加一项（含 `panel` 字段，见下文）；沿用已有 group 则无需改 UI。
- 渲染组件（`BasicRulesSection.vue` / `TribeSection.vue`）完全由注册表生成（bool→`t-switch`、float→`t-input-number step=0.1`、int→`t-input-number step=1 decimal-places=0`），**通常无需改动**。

### 6.3 新增分组与面板

`SETTING_GROUPS` 每项必须带 `panel` 字段，指定该分组由哪个面板组件渲染：

```js
// 基础规则面板（BasicRulesSection.vue）
{ key: 'build', label: '建造限制', panel: 'basic' },

// 环境配置面板（WorldSection.vue）
{ key: 'diff',  label: '难度设置', panel: 'world' },
{ key: 'world', label: '环境配置', panel: 'world' },

// 部落面板（TribeSection.vue）
{ key: 'orp',   label: '离线突袭保护（ORP）', panel: 'tribe' },
{ key: 'tribe', label: '部落设置',            panel: 'tribe' },

// 生物设置面板（DinoMultipliersSection.vue 上半部）
{ key: 'dino_num',   label: '数量上限',   panel: 'dino' },
{ key: 'dino_mult',  label: '倍率设置',   panel: 'dino' },
{ key: 'dino_breed', label: '繁殖与幼崽', panel: 'dino' },
{ key: 'dino_rule',  label: '行为规则',   panel: 'dino' },
```

目前已有分组及所属面板：

| group | label | panel | 说明 |
|-------|-------|-------|------|
| `diff` | 难度设置 | `world` | DifficultyOffset、OverrideOfficialDifficulty；后者 > 0 时自动将前者设为 1.0 |
| `world` | 环境配置 | `world` | 昼夜、玩家属性消耗/倍率、采集、建筑数量与拾取等 23 项 |
| `pvp` | PvP / PvE 规则 | `basic` | |
| `build` | 建造限制 | `basic` | |
| `loot` | 补给箱 / 钓鱼 | `basic` | |
| `cryo` | 低温舱（Cryopod） | `basic` | |
| `cross` | 跨服传输（Cross-ARK） | `basic` | |
| `tame` | 生物管理 | `basic` | |
| `env` | 环境（其他） | `basic` | 尸体/电池/燃料/物品属性上限等杂项；`OxygenSwimSpeedStatMultiplier` 已迁至 `world` |
| `orp` | 离线突袭保护（ORP） | `tribe` | |
| `tribe` | 部落设置 | `tribe` | |
| `dino_num` | 数量上限 | `dino` | |
| `dino_mult` | 倍率设置 | `dino` | 含新增 `TamingSpeedMultiplier`（驯服速度倍率，排在被动驯服间隔之后） |
| `dino_breed` | 繁殖与幼崽 | `dino` | 交配/下蛋/孵化/幼崽成熟/印记等 11 项，全部写入 Game.ini |
| `dino_rule` | 行为规则 | `dino` | |

> **迁移记录**：
> - `MaxPersonalTamedDinos` / `AllowRaidDinoFeeding` / `RaidDinoCharacterFoodDrainMultiplier`：`tribe` → `dino_num` / `dino_rule` / `dino_mult`
> - `OxygenSwimSpeedStatMultiplier`：`env` → `world`

新增 `panel: 'basic'` 的分组：自动出现在「基础规则设置」折叠面板中，`basicCount` 统计计数自动包含。  
新增 `panel: 'world'` 的分组：自动出现在「环境配置」折叠面板中，`worldCount` 统计计数自动包含。  
新增 `panel: 'tribe'` 的分组：自动出现在「部落设置」折叠面板中，`tribeCount` 统计计数自动包含。  
新增 `panel: 'dino'` 的分组：自动出现在「生物设置」折叠面板上半部的简单项网格中，`dinoSimpleCount` 统计计数自动包含。  
若需增加新面板，新建 `XxxSection.vue`（复制 `WorldSection.vue` 改 `panel === 'xxx'`），并在 `AdvancedGameConfigDialog.vue` 的 `panels` 数组、`xxxKeys` Set、`xxxCount` 函数及 `counts` computed 中追加即可。

### 6.4 弹窗与保存（已接好，新增简单项无需改）

`AdvancedGameConfigDialog.vue` 打开时 `parseSettings` 填充 `simpleModel`；确认时**先** `mergeGameIni`（数组键）**再** `applySettings`（标量键，产出两文件），`emit('save', { gameIni, gameUserSettings })`。`InstanceDetail.saveAdvancedConfig` 仅对**发生变化的文件**调用 `saveGameIni` / `saveGameUserSettings`。数组键与标量键互不相交，顺序合成安全（已验证无 clobber、无重复行）。

---

## 7. 分区组件规范

- 共享样式类（`section.css`）：`.section`（外层 flex 列）、`.section-tip`（说明块）、`.toolbar`、`.empty`（空状态）、`.row`/`.cell`/`.cell.grow`/`.cell-label`、`.sub-card`/`.sub-rows`（主从）、`.stat-grid`/`.stat-item`。
- **两列布局**：用 TDesign 栅格 `<t-row :gutter="[12, 12]"><t-col :xs="12" :md="6">`。`gutter` 数组形式 `[水平, 垂直]`（水平=负 margin，垂直=`row-gap`，TDesign `calcRowStyle` 支持）。窄行分区（生物倍率 / 最大堆叠）用两列；字段多/主从结构（印痕 / 制作消耗）保持单列。
- 下拉：`ItemSelect`（物品）、`CreatureSelect`（生物），`v-model` 绑 ClassName 字符串，支持搜索与自定义输入。
- 数字用 `t-input-number`（倍率 `:step="0.1"`、整数 `:step="1"`）；布尔用 `t-switch`。

---

## 8. 样式约定

- **设计风格**：Data-Dense Dashboard（数据密集型）—— 最小内边距、白底卡片、hover 高亮、计数指示。
- **颜色**：统一用 TDesign 令牌（`--td-brand-color` / `--td-component-border` / `--td-text-color-*` / `--td-bg-color-*` / `--td-brand-color-light`），均带回退值，保证暗色模式与全站一致。
- **弹窗布局**：信息 band 钉顶（`flex:0 0 auto`），内容区 `.adv-scroll` 内部滚动（`overflow-y:auto`），`.t-dialog__body` 设 `overflow:hidden` —— 滚动只发生在 body 内部，不整体滚动。
- **可访问性**：过渡 150–300ms、`@media (prefers-reduced-motion: reduce)` 关动效、数字用 `font-variant-numeric: tabular-nums`、`<768px` 响应式降列/隐藏副标题。

---

## 9. 验证清单

1. **数据集**（如改动）：`cd app && npm run gen:data`，核对条目数与 className 无反引号。
2. **往返保真（重点）**：临时 node 脚本 `import { parseGameIni, mergeGameIni } from '../src/utils/arkGameIni.js'`，喂入含注释 + 未受管键 + 已有受管键的样本：
   - 不改动 → `mergeGameIni` 输出应保留注释/未识别键/其它节；
   - 二次往返 `merge(parse(merge).meta, parse(merge).model)` 应**幂等**。
3. **嵌套结构**：重点测多/单子项的 `(...)` 嵌套往返。
4. **简单项（arkSimpleSettings）**：临时 node 脚本验证 `parseSettings` → 改 `model` → `applySettings`：inverse 解析正确、默认值不写、回默认但原有则写、跨两文件保留注释/未识别键、幂等；并测「数组合并 + 标量合并」在同一 Game.ini 合成无 clobber、无重复行。
5. **构建**：`cd app && npm run build` 通过。
6. **联调**：`npm run dev`（代理 :19193）→ 实例详情 → 「服务器配置」卡片 →「服务器规则配置」按钮 → 各分区（1 基础规则设置 / 2 环境配置 / 3 部落设置 / 4 生物设置 / 5 印痕 / 6 制作消耗 / 7 最大堆叠 / 8 等级 / 9 属性倍率，共 **9 个**）增删改保存 → Monaco 查看器 + 真实文件 `{Game,GameUserSettings}.ini` 核对 → 重新打开确认回显无丢失。重点验证：
   - **环境配置**面板：「难度设置」2 项（修改 `OverrideOfficialDifficulty > 0` 应自动将 `DifficultyOffset` 置为 1.0）、「环境配置」23 项（含 `OxygenSwimSpeedStatMultiplier`）正确显示；
   - **生物设置**面板：上半部 4 个子分组（数量上限 3 项、倍率设置含 `TamingSpeedMultiplier` 共 16 项、繁殖与幼崽 11 项、行为规则 10 项）正确渲染，下半部 per-class tab 正常；「洞穴飞行」开关显示于行为规则组末尾；
   - **印痕**面板：顶部「自动解锁印痕」区块（`EngramEntryAutoUnlocks`）和底部「印痕条目覆盖」区块均可增删，计数徽章合计两者之和；
   - **部落设置**面板中 `MaxPersonalTamedDinos` / `AllowRaidDinoFeeding` / `RaidDinoCharacterFoodDrainMultiplier` 已不再显示，`OxygenSwimSpeedStatMultiplier` 也已不再显示。

---

## 10. 已知限制

- 印痕（Engram）无清单数据，手动填写 `EngramClassName` / `EngramIndex`。
- 下拉暂不显示图标（避免依赖 wiki CDN 外网）。
- `LevelExperienceRampOverrides` 顺序敏感：UI 玩家曲线在前、驯养在后；若只填驯养未填玩家，单行会被 ARK 当作玩家曲线。
- 简单项「反向布尔」仅影响展示，落盘按真实 INI 语义；写盘策略见 6.1。
- 仅接管已登记的受管键，文件其余内容逐字保留。
