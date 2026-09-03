# 实例详情页 Tab 化拆分方案

> 目标文件：`app/src/views/InstanceDetail.vue` → 重命名为 `app/src/views/InstanceDetail/index.vue`（容器），子组件入 `app/src/views/InstanceDetail/components/`
> 关联组件（**不改动**，仅决定是否继续引用）：`app/src/components/ark/AdvancedGameConfigDialog.vue`
> 约束：**已有组件一律不动**；凡涉及行为差异的地方，**新增组件 / composable**，旧文件原样保留。
> 已定决策见 §15。

---

## 1. 背景与问题

当前 `InstanceDetail.vue`（约 1550 行）把一个实例的所有能力堆在同一个滚动长页里：

1. **服务器基本信息编辑**走 `ConfigEditModal.vue`（`t-drawer`，宽 1100）——点击遮罩 / ESC 容易误关，未保存内容直接丢失。
2. **服务器规则配置**走 `AdvancedGameConfigDialog.vue`（`t-drawer`，size 80%）——同样是抽屉，9 大配置区域挤在一个内嵌 `t-collapse` 里，误关风险 + 纵向嵌套滚动。
3. 其余能力（实例配置文件原始预览、ArkApi 插件配置、游戏存档备份、实时日志）全部平铺在同一页的 `t-collapse` / `t-card` 中，页面过长、首屏加载重（Monaco ×2 + 日志 SSE + 插件表 + 资源监控 SSE 同时挂载）。

### 诉求

- 用 **Tab** 把详情页拆成多个模块，各模块独立。
- **基本信息编辑** 与 **服务器规则配置** 从抽屉改为 Tab 内的**常驻编辑区**，配显式「保存 / 重置」与「未保存」保护，消除误关。
- **服务器规则配置**按配置区域（基础规则 / 环境 / 部落 / 生物 / 印痕 / 制作消耗 / 堆叠 / 等级 / 属性倍率）分块，纳入主页面的 Tab 切换体系，而非内嵌折叠面板。

---

## 2. 目标与非目标

### 目标
- `InstanceDetail.vue` 收敛为「页头 + 操作栏 + `<t-tabs>` 容器 + 全局 Modal」，业务内容全部下沉到按模块划分的新子组件。
- 抽屉式表单编辑 → Tab 内常驻表单，显式保存、可重置、离开有拦截。
- 规则 9 区域改为二级 Tab（主 Tab「服务器规则」下），复用现有 `components/ark/sections/*.vue`，零改动。
- 首屏只挂载「概览」Tab，其余 Tab 懒挂载。

### 非目标
- 不改动任何现有可复用组件（`AdvancedGameConfigDialog`、`ConfigEditModal`、`sections/*`、`ConfigEditor`、`ConfigFileViewer`、`ConfigDiffModal`、`PluginDataPanel`、`LogViewer`、`ResourceMonitor`、`InstanceStatusHistory`、`RconTerminal`、`CountdownConfirmDialog`）。重构后 `ConfigEditModal` / `AdvancedGameConfigDialog` / `ConfigEditor` / `ConfigFileViewer` / `ConfigDiffModal` 都不再被 `InstanceDetail.vue` 引用，但**文件保留**。
- 不改 `utils/arkGameIni.js` / `utils/arkSimpleSettings.js`。
- 后端 API：本次前端改造会让「配置文件上传」「配置文件对比」两组接口失去调用方，**前端不再引用**；对应后端接口的删除由后端另行处理，清单见 §5.5。
- 不改路由结构（仍是 `/instance/:name` 单路由；Tab 状态用 query 参数，见 §9）。
- 不动 RCON 交互终端的实现（仍为非模态浮窗，从操作栏触发）。

### 本轮补充需求（在 §1 基础上）
- **配置文件**：Game.ini / GameUserSettings.ini 的「预览」与「编辑」合并——Tab 内直接是**可编辑的 Monaco 编辑器**，每个文件只保留一个「保存」按钮，不再有独立的编辑弹窗。
- **移除**「配置文件对比」（`ConfigDiffModal` + `getServerConfigs`）与「配置文件上传」（`uploadGameIniFile` / `uploadGameUserSettingsFile`）。
- **保存按钮禁用规则（所有可编辑 Tab 统一）**：仅当服务器**已停止**时可保存，即 `:disabled="instanceData?.running"`（运行中禁用）。

---

## 3. 现状内容盘点

| 现状区块 | 承载组件 | 形态 | 去向 |
|---|---|---|---|
| 页头：实例名 / 状态 / 倒计时 | 内联 | 常驻 | **保留在页头**（Tab 之上） |
| 操作栏：启动 / 停止 / 重启 / RCON / 强制停止 | 内联 + `t-dialog`(RCON) | 常驻 | **保留在页头**；RCON 浮窗不变 |
| 服务器配置（只读栅格） | 内联 `getAllConfigItems()` | 常驻 | → Tab 1「概览」 |
| 资源占用 | `ResourceMonitor` | 常驻 | → Tab 1「概览」 |
| 实例历史状态 | `InstanceStatusHistory` | 常驻 | → Tab 1「概览」 |
| 编辑服务器配置 | `ConfigEditModal`（drawer） | 抽屉表单 | → Tab 2「基础配置」（常驻表单，**新组件**） |
| 服务器规则配置 | `AdvancedGameConfigDialog`（drawer） | 抽屉 + 内嵌 collapse | → Tab 3「服务器规则」（二级 Tab，**新组件** + 复用 sections/*） |
| 实例配置文件（Game.ini / GameUserSettings.ini 预览 + 编辑） | `ConfigFileViewer`（只读）+ `ConfigEditor`（drawer 编辑） | collapse + 抽屉 | → Tab 4「配置文件」**合并为 Tab 内可编辑 Monaco**（**新组件** `IniEditorPane`），每文件一个「保存」按钮 |
| 配置文件上传 | 隐藏 `<input type=file>` + `uploadGameIniFile` / `uploadGameUserSettingsFile` | 按钮 | **移除**（接口见 §5.5） |
| 配置文件对比 | `ConfigDiffModal` + `getServerConfigs` | Modal | **移除**（接口见 §5.5） |
| ArkApi 插件配置 | `PluginDataPanel` | collapse | → Tab 5「插件配置」（直接复用） |
| 游戏存档备份 | 内联备份列表逻辑 | collapse | → Tab 6「存档备份」（**新组件**） |
| 实时日志 | `LogViewer` | card | → Tab 7「实时日志」（直接复用） |

---

## 4. 目标信息架构（Tab 设计）

页头（常驻，所有 Tab 共享）：`← 返回 | 实例名 | 状态标签 | 倒计时标签/取消` + 操作栏 `启动 / 停止 / 重启 | RCON 终端 / 强制停止`。

主 Tab（`<t-tabs>` `theme="card"`，`v-model` 绑定 `activeTab`，值同步到 `?tab=`）：

| # | Tab key | 标题 | 内容 | 承载 | 可编辑 |
|---|---|---|---|---|---|
| 1 | `overview` | 概览 | 只读配置栅格 + 资源占用 + 历史状态 | `InstanceOverviewTab.vue`（新） | 否 |
| 2 | `basic` | 基础配置 | 服务器名/端口/地图/Mod/密码/公告/启动参数… 常驻表单 | `InstanceBasicConfigTab.vue`（新） | **是** |
| 3 | `rules` | 服务器规则 | 二级 Tab：基础规则 / 环境 / 部落 / 生物 / 印痕 / 制作消耗 / 堆叠 / 等级 / 属性倍率 | `ServerRulesTab.vue`（新）+ `sections/*`（复用） | **是** |
| 4 | `files` | 配置文件 | Game.ini + GameUserSettings.ini **可编辑 Monaco**（左右两栏或上下两块），每块一个「保存」按钮 | `InstanceConfigFilesTab.vue`（新）+ `IniEditorPane.vue`（新） | **是** |
| 5 | `plugin` | 插件配置 | `PluginDataPanel` | 直接复用 | 组件内自管 |
| 6 | `backup` | 存档备份 | 备份列表 / 创建 / 恢复 / 删除 | `InstanceBackupTab.vue`（新） | — |
| 7 | `logs` | 实时日志 | `LogViewer` | 直接复用 | — |

> Tab 2 / 3 / 4 在标题右侧显示「未保存」小红点（`t-badge` dot），有脏数据时点亮。
>
> **可编辑 Tab（2 基础配置 / 3 服务器规则 / 4 配置文件）的「保存」按钮统一 `:disabled="instanceData?.running"`** —— 运行中不可保存，与旧版「编辑」按钮的禁用取值一致。

### 关于「规则 9 区域」的归属（开放决策，见 §14）
- **推荐（方案 A）**：主 Tab「服务器规则」内用**二级 `<t-tabs>`** 承载 9 个区域。既满足「纳入 Tab 切换体系」，又不会让主 Tab 数量膨胀到 15+。
- 方案 B：9 个区域全部提升为主 Tab，与其它 6 个平级。语义最直白，但主 Tab 过多、且保存按钮要跨 9 个 Tab 共享一份 model，交互更绕。

---

## 5. 组件改造清单

### 5.1 复用、零改动
- **仍被引用**：`components/ark/sections/*.vue`、`PluginDataPanel.vue`、`LogViewer.vue`、`ResourceMonitor.vue`、`InstanceStatusHistory.vue`、`RconTerminal.vue`、`CountdownConfirmDialog.vue`、`utils/arkGameIni.js`、`utils/arkSimpleSettings.js`。
  - ⚠️ **例外（已实施）**：`PluginDataPanel.vue` 有一处既有 TDZ bug —— `watch(() => props.instanceName, () => load(), {immediate: true})` 写在 `const load` 定义之前，`immediate` 回调在 setup 阶段访问未初始化的 `load` 抛 `ReferenceError`。旧页面把它塞在**折叠面板**里、用户不展开就不挂载，于是长期未触发；Tab 化后首次进入「插件配置」即挂载并复现。修复是**纯行序调整**（把该 `watch` 移到 `load` 定义之后），零行为变化，无法用「包一层新组件」绕过（TDZ 在其自身 setup 内）。
- **重构后不再被 `InstanceDetail.vue` 引用，文件保留**（按约束不删不改）：`AdvancedGameConfigDialog.vue`、`ConfigEditModal.vue`、`ConfigEditor.vue`、`ConfigFileViewer.vue`、`ConfigDiffModal.vue`。
  - 其中 `ConfigDiffModal.vue` 全仓库仅 `InstanceDetail.vue` 引用，重构后成为**孤儿组件**（后端删接口后功能彻底下线，可择期清理）。

### 5.2 新增组件（`app/src/views/InstanceDetail/components/`）

| 文件 | 职责 | props（← 容器） | emits（→ 容器） |
|---|---|---|---|
| `InstanceOverviewTab.vue` | 只读配置栅格（含 Mod 复制、密码显隐）+ 内嵌 `ResourceMonitor` + `InstanceStatusHistory` | `configItems`、`instanceName`、`modInfo` | — |
| `InstanceBasicConfigTab.vue` | 常驻基础配置表单。**照搬 `ConfigEditModal.vue` 的字段与校验规则**，去掉 `t-drawer` 外壳，改为页内 `t-form` + 顶部「保存 / 重置」工具条（保存 `:disabled="running"`） | `config`、`saving`、`running`、`modInfo` | `save(payload)`、`update:dirty(bool)` |
| `ServerRulesTab.vue` | 二级 `<t-tabs>` 承载 9 区域，块内复用 `sections/*`；顶部「保存 / 重置 / 已配置 N 项」工具条（保存 `:disabled="running"`） | `gameIniContent`、`gameUserSettingsContent`、`customStartParameters`、`saving`、`running` | `save({gameIni, gameUserSettings, customStartParameters})`、`update:dirty(bool)` |
| `InstanceConfigFilesTab.vue` | 布局两块，各内嵌一个 `IniEditorPane`（Game.ini / GameUserSettings.ini）；每块顶部一个「保存」按钮（`:disabled="running"`）。无上传、无对比 | `gameIniContent`、`gameUserSettingsContent`、`running` | `save-game-ini(content)`、`save-gus(content)`、`update:dirty(bool)` |
| `IniEditorPane.vue` | Monaco 单文件编辑器（`ini` 语言、`automaticLayout:true`）。参照 `ConfigEditor.vue` 的 `monaco.editor.create` 初始化，但**不套 `t-dialog`**，内容变化 `emit('change')` + 暴露 `getValue()`；`readonly` 为真时 `editor.updateOptions({ readOnly: true })` | `modelValue`（初始内容）、`readonly`（默认 false） | `change(content)` |
| `InstanceBackupTab.vue` | 备份列表 / 创建 / 刷新 / 恢复 / 删除（把容器里 `fetchBackups`/`createBackupHandler`/`restoreBackupHandler`/`deleteBackupHandler` 迁进来，API 直调或经 emit） | `instanceName` | （可选）`changed` |

> 「插件配置」「实时日志」两个 Tab 直接在 `<t-tab-panel>` 内渲染 `PluginDataPanel` / `LogViewer`，**不额外包壳**（无行为差异）。
> `IniEditorPane` 独立于 `ConfigFileViewer`（后者只读、无法内联复用于编辑），是本次新增的**唯一** Monaco 相关组件。

### 5.3 新增 composable（`app/src/composables/`）

| 文件 | 职责 |
|---|---|
| `useArkRulesModel.js` | **从 `AdvancedGameConfigDialog.vue` 的 `<script setup>` 抽取**：`createEmptyModel` / `parseGameIni` / `mergeGameIni` / `parseSettings` / `applySettings` 的编排 + `counts` / `totalConfigured` 计算 + `caveFlyers` 启动参数处理。输入 `{gameIniContent, gameUserSettingsContent, customStartParameters}`（ref），输出 `{model, simpleModel, caveFlyers, counts, totalConfigured, reset(), buildPayload()}`。`buildPayload()` 产出 `{gameIni, gameUserSettings, customStartParameters}`，与容器现有 `saveAdvancedConfig` 入参**完全一致**。 |
| `useUnsavedGuard.js` | 通用未保存保护：登记若干 `dirty` 来源；提供 `confirmLeave()`（返回 Promise<boolean>，内部弹 `DialogPlugin.confirm`）；封装 `onBeforeRouteLeave` + 供 Tab 切换调用的拦截函数。 |

### 5.4 容器（`InstanceDetail/index.vue`）保留职责

- 状态所有权不变：`instanceData` / `gameIniContent` / `gameUserSettingsContent` / `modInfo` / `serverGameIniContent` 等仍由容器持有。
- 所有 API 调用保留在容器：`fetchInstanceConfig` / `loadGameIni` / `loadGameUserSettings` / `saveGameIni` / `saveGameUserSettings` / `saveConfig` / `saveAdvancedConfig` / `loadServerConfigs` / start/stop/restart/forceStop/cancelCountdown / 备份相关。
- 子组件为**受控展示层**：props 下发数据、emit 上报意图，容器执行副作用后刷新。
- 容器继续持有的 Modal：RCON `t-dialog`、`CountdownConfirmDialog`。（`ConfigEditor` ×2 / `ConfigDiffModal` / 隐藏 `<input type=file>` ×2 全部移除。）
- 移除：`ConfigEditModal`、`AdvancedGameConfigDialog`、`ConfigEditor`、`ConfigFileViewer`、`ConfigDiffModal` 的 import 与模板引用；隐藏 `<input type=file>` ×2；`activeCollapseKeys` 折叠逻辑；随之简化的 `watch`。

### 5.5 因本次改造而失去调用方的接口 / 代码（供后端移除）

前端在本方案落地后**不再调用**以下能力。后端接口是否删除由后端决定，这里给出完整清单：

| 层 | 符号 | 位置 | 说明 |
|---|---|---|---|
| 前端 API | `uploadGameIniFile` | `app/src/apis/api.js:146` | 上传 Game.ini，仅 `InstanceDetail.vue` 用 |
| 前端 API | `uploadGameUserSettingsFile` | `app/src/apis/api.js:153` | 上传 GameUserSettings.ini，仅 `InstanceDetail.vue` 用 |
| 前端 API | `getServerConfigs` | `app/src/apis/api.js:125` | 读基础服务器 Game.ini/GUS，仅用于 `InstanceDetail.vue` 的配置对比 |
| 前端 API | `getInstanceConfigs` | `app/src/apis/api.js:130` | **当前已无任何调用方**（全仓库仅定义、无引用），顺带一并移除 |
| 前端组件 | `ConfigDiffModal.vue` | `app/src/components/` | 重构后成孤儿组件 |
| **后端路由** | `POST /api/config/:name/game-ini` → `h.uploadGameIni` | `internal/webapi/configapi/configapi.go:26` | 上传（与 `PUT` 更新不同）。**注意保留 `PUT /:name/game-ini`（`updateGameIni`），编辑保存仍走它** |
| **后端路由** | `POST /api/config/:name/game-user-settings` → `h.uploadGameUserSettings` | `configapi.go:27` | 同上；**保留 `PUT` 版** |
| **后端路由** | `GET /api/config/server/configs` → `h.getServerConfigs` | `configapi.go:22` | 仅配置对比用 |
| **后端路由** | `GET /api/config/:name/configs` → `h.getInstanceConfigs` | `configapi.go:23` | 前端早已无调用方 |
| 后端处理器 | `Handler.uploadGameIni` / `Handler.uploadGameUserSettings` | `configapi.go:170` / `:232` | 随路由删除 |
| 后端处理器 | `Handler.getServerConfigs` / `Handler.getInstanceConfigs` | `configapi.go:46` / `:85` | 随路由删除 |
| 后端 helper | `cfgpkg.GetServerGameIniContent` / `GetServerGameUserSettingsContent` | `internal/config/config.go:420` / `:436` | **仅** `getServerConfigs` 调用，可一并清理（先确认无其他引用） |

**保留不动**：`GET /:name/game-ini`、`GET /:name/game-user-settings`、`PUT /:name/game-ini`、`PUT /:name/game-user-settings`、`POST /api/config/sync-instance`（配置同步是独立功能，`SyncConfigModal.vue` 仍在用）。

---

## 6. 「服务器规则」Tab 详细设计（`ServerRulesTab.vue`）

### 结构
```
<div class="rules-tab">
  <div class="rules-toolbar">           <!-- 常驻顶部：不随内容滚动 -->
    <span>已配置 {{ totalConfigured }} 项</span>
    <t-button @click="reset">重置</t-button>
    <!-- 运行中不可保存 -->
    <t-button theme="primary" :loading="saving" :disabled="running" @click="onSave">保存</t-button>
  </div>
  <t-tabs v-model="area" theme="normal" :addable="false">
    <t-tab-panel value="basic"    :label="`基础规则 ${badge('basic')}`">   <basic-rules-section :model="simpleModel" /> </t-tab-panel>
    <t-tab-panel value="world"    label="环境配置">        <world-section :model="simpleModel" /> </t-tab-panel>
    <t-tab-panel value="tribe"    label="部落设置">        <tribe-section :model="simpleModel" /> </t-tab-panel>
    <t-tab-panel value="dino"     label="生物设置">
      <dino-multipliers-section :model="model.classMultipliers" :simple-model="simpleModel"
        :cave-flyers="caveFlyers" @update:cave-flyers="caveFlyers = $event" />
    </t-tab-panel>
    <t-tab-panel value="engram"   label="印痕条目覆盖">
      <engram-overrides-section :model="model.engrams" :auto-unlocks="model.autoUnlocks" />
    </t-tab-panel>
    <t-tab-panel value="crafting" label="物品制作消耗">   <crafting-costs-section :model="model.craftingCosts" /> </t-tab-panel>
    <t-tab-panel value="maxqty"   label="物品最大堆叠">
      <item-max-quantity-section :model="model.maxQuantity" :simple-model="simpleModel" />
    </t-tab-panel>
    <t-tab-panel value="levels"   label="玩家与驯养等级覆盖">
      <level-overrides-section :player="model.levels.player" :dino="model.levels.dino"
        :engram-points="model.engramPoints" />
    </t-tab-panel>
    <t-tab-panel value="stats"    label="属性倍率">       <stats-multipliers-section :model="model.stats" /> </t-tab-panel>
  </t-tabs>
</div>
```
> 这些 `<xxx-section>` 的 props 结构**与 `AdvancedGameConfigDialog.vue` 模板中的用法逐字一致**，直接照抄即可，无需改 section 组件。

### 数据与保存流
1. `ServerRulesTab` 内 `const { model, simpleModel, caveFlyers, counts, totalConfigured, reset, buildPayload } = useArkRulesModel(toRefs(props))`。
2. composable 在 props 内容变化（首次进入 Tab / 容器刷新）时重新 `parseGameIni` + `parseSettings`，并快照初始值用于脏检测与 `reset()`。
3. 脏检测：`watch([model, simpleModel, caveFlyers], deep)` 与初始快照比较 → `emit('update:dirty', dirty)`。
4. 保存：`onSave()` → `emit('save', buildPayload())` → 容器现有 `saveAdvancedConfig(payload)` 原样处理（按需写 Game.ini / GameUserSettings.ini / CustomStartParameters）→ 成功后容器刷新 `gameIniContent` 等 → composable 重新解析、清脏。

### 二级 Tab 懒挂载
`<t-tabs>` 默认懒渲染未激活面板；`EngramOverridesSection` 等含大选择器（`EngramSelect` / `CreatureSelect`）的区域仅在切到时挂载。

---

## 7. 「基础配置」Tab 详细设计（`InstanceBasicConfigTab.vue`）

- 模板：把 `ConfigEditModal.vue` 的 `t-form`（`ServerName`/`BindDomain`/`MaxPlayers`/`Port`/`RCONPort`/`MapName`/`ClusterID`/`SaveDir`/`ServerPassword`/`ServerAdminPassword`/`ModIDs` 标签编辑器/`CustomStartParameters`/`MessageOfTheDay`/`MessageOfTheDayDuration`/`EnableAsaPlugin`）连同 `rules` 校验、`MAP_OPTIONS`、Mod 标签增删逻辑、`buildPayload()` / `toNumber()` **整体照搬**到新组件。
- 去掉 `t-drawer`，改为：顶部常驻工具条「重置 / 保存」+ 下方 `t-form`（`label-align="top"`，双列 `t-row`）。
- `running` 时整表单 `disabled`（与旧「运行中禁止编辑」一致）。
- 脏检测：`editingConfig` 与初始 `props.config` 投影比较 → `emit('update:dirty')`。
- 保存：校验通过 → `emit('save', buildPayload())` → 容器 `saveConfig(payload)`（已存在）→ 成功刷新 + 清脏。
- 「重置」：重新用 `props.config` 初始化 `editingConfig`。

---

## 8. 「配置文件」/「插件」/「备份」/「日志」Tab

### 配置文件（`InstanceConfigFilesTab.vue` + `IniEditorPane.vue`）
- 布局：两块 `t-card`（Game.ini / GameUserSettings.ini），**默认左右两栏**（`display:grid; grid-template-columns:1fr 1fr`）；`@media (max-width: ~1100px)` 塌成**上下两块**（`grid-template-columns:1fr`）。各块结构：
  ```
  <t-card title="Game.ini">
    <template #actions>
      <t-button theme="primary" :loading="savingGameIni" :disabled="running" @click="saveGameIni">保存</t-button>
    </template>
    <ini-editor-pane ref="gameIniPaneRef" :model-value="gameIniContent" :readonly="running" @change="onGameIniChange" />
  </t-card>
  ```
- `IniEditorPane` 是 Monaco 编辑器（新组件，见 §5.2），`readonly=false` 时可编辑；不再有「编辑文件」弹窗、不再有 `ConfigFileViewer` 只读态。
- 保存：`saveGameIni()` → `emit('save-game-ini', gameIniPaneRef.getValue())` → 容器现有 `saveGameIni(content)`（`PUT /:name/game-ini`，**不变**）→ 成功后容器刷新 `gameIniContent`，`IniEditorPane` `watch(modelValue)` 里 `editor.setValue()` 重置基线、清脏。
- **无上传、无对比**：移除按钮、移除隐藏 `<input type=file>`、移除 `compareGameIni` / `compareGameUserSettings` / `loadServerConfigs` / `handleDiffSave` / `diff*` 状态与 `ConfigDiffModal`。
- 脏检测：`onGameIniChange` 比对当前值与最后一次加载/保存的快照 → `emit('update:dirty', dirty)`。
- 运行中：`IniEditorPane` 传 `:readonly="running"`——服务器运行时编辑器**只读**（只能看），保存按钮一并 `:disabled="running"`。

### 插件配置
- `<t-tab-panel value="plugin">` 内直接 `<plugin-data-panel :instance-name :interval @update:interval="saveSnapshotInterval" ref="pluginPanelRef" />`。
- 切到该 Tab 时 `pluginPanelRef.reload()`（迁移现有 `watch(activeCollapseKeys)` 里的刷新逻辑到 `watch(activeTab)`）。

### 存档备份（`InstanceBackupTab.vue`）
- 迁入 `backups` / `backupLoading` / `backupListLoading` 与四个 handler。
- 首次切到该 Tab 懒加载 `fetchBackups()`（替代现有 `watch(activeCollapseKeys)` 逻辑）。

### 实时日志
- `<t-tab-panel value="logs">` 内直接 `<log-viewer ref="logViewerRef" :instance-name />`。
- 见 §10 关于 SSE 生命周期。

---

## 9. 未保存修改保护机制

三层：

1. **Tab 标题标记**：`basic` / `rules` / `files` Tab 有脏数据时标题右侧显示红点。
2. **Tab 切换拦截**：`<t-tabs>` `v-model` 改为「受控」——用 `:value` + `@change`，在 `onTabChange(next)` 里：若当前 Tab 脏 → `await confirmLeave()`（`DialogPlugin.confirm`：「有未保存修改，确定离开？放弃 / 继续编辑」）→ 确认则切并丢弃、取消则 `activeTab` 不变。
3. **路由离开拦截**：`onBeforeRouteLeave` → 任一 Tab 脏 → 同样 `confirmLeave()`。

统一封装在 `useUnsavedGuard.js`。容器持有 `dirtyMap = reactive({ basic:false, rules:false, files:false })`，子组件 `@update:dirty="dirtyMap.basic = $event"`。

> 「配置文件」Tab 现在是可编辑态，同样纳入脏保护——这是「合并预览+编辑、去掉编辑弹窗」后必须补的一环。

> 说明：这正是「抽屉误关丢数据」被替换掉的核心收益——常驻编辑区 + 显式保存 + 离开确认。

---

## 10. KeepAlive / 懒挂载 / SSE 生命周期

- **主 `<t-tabs>` 用 `:lazy`（或对每个 `t-tab-panel` `v-if="mounted[key]"` 首次激活置真）**：首屏只挂 `overview`。
- Monaco（`IniEditorPane` ×2）只在切到「配置文件」时才创建，缓解首屏卡顿。
- 「配置文件」Tab 首次挂载后建议 `v-show` 保活（不销毁 Monaco 实例），否则切走再切回会丢失未保存编辑且重建编辑器有成本；配合脏保护（切 Tab 前已弹确认）即可。
- **`LogViewer` 的 SSE**：`LogViewer` 内部按 `instance-name` 自管监听。若 Tab 面板被 `v-if` 卸载，切走即断流、切回重连。若希望后台持续收日志，则「日志」面板改用 `v-show` 常驻（首次访问后不再卸载）。**推荐：日志 Tab 首次激活后用 `v-show` 保活**，其余 Tab 用 `v-if` 懒挂。
- `ResourceMonitor` / `InstanceStatusHistory` 在 `overview`，随详情页整体存活；`overview` 常驻不卸载。
- 详情页整体仍不进 `App.vue` 的 `<KeepAlive include>`（保持现状：以 `route.fullPath` 为 key，切实例即重建）。

---

## 11. 状态与数据流（不变量）

```
InstanceDetail/index.vue (容器 / 单一数据源 / 全部 API)
├── 页头 + 操作栏（内联，调用 start/stop/restart/forceStop/cancelCountdown）
├── <t-tabs v-model=activeTab (受控, @change 拦截脏检查)>
│   ├── overview : InstanceOverviewTab      ← configItems, instanceName, modInfo
│   ├── basic    : InstanceBasicConfigTab   ← config, saving, modInfo   → save / update:dirty
│   ├── rules    : ServerRulesTab           ← gameIni, gus, customParams → save / update:dirty
│   ├── files    : InstanceConfigFilesTab   ← gameIni, gus, running     → save-game-ini / save-gus / update:dirty
│   ├── plugin   : PluginDataPanel (复用)
│   ├── backup   : InstanceBackupTab        ← instanceName
│   └── logs     : LogViewer (复用)
└── Modal 区（容器持有）: RCON t-dialog, CountdownConfirmDialog
   （移除：ConfigEditor×2 / ConfigDiffModal / <input type=file>×2）
```

`ServerRulesTab.save` payload 形状 = 容器 `saveAdvancedConfig` 现有入参，**无需改容器保存逻辑**。
`InstanceBasicConfigTab.save` payload 形状 = 容器 `saveConfig` 现有入参。

---

## 12. 样式与布局

- 页头 + 操作栏高度固定；`<t-tabs>` 下方内容区 `height: calc(详情卡高度 - 页头 - tab条)`，`overflow-y:auto`，滚动发生在**面板内部**。
- 复用 `AdvancedGameConfigDialog` 里的 `.adv-hero` 信息条样式思路，做成 `ServerRulesTab` 顶部工具条（新 scoped CSS，不引旧文件样式）。
- `sections/*` 自带 `section.css`，直接生效，无需处理。
- 「配置文件」两栏栅格沿用现有 `.config-files-row` / `.config-file-card` 思路（`grid` 双列 → 窄屏单列），迁到新组件 scoped 样式；每个 `IniEditorPane` 给固定高度（如 `calc(100% - 48px)` 或 `560px`），Monaco `automaticLayout` 兜底尺寸变化。

---

## 13. 分阶段实施步骤

> 每阶段结束都能 `npm run build` 通过、页面可用；便于回退。

1. **骨架 + 改名**：`git mv app/src/views/InstanceDetail.vue app/src/views/InstanceDetail/index.vue`，改 `router/index.js:3` 的 import 路径（路由 `name` 不变）；引入 `<t-tabs>`，把现有内容**原样搬进对应 `t-tab-panel`**（暂不拆组件、暂不动抽屉）。验证布局、滚动、懒挂载、`?tab=` 同步。
2. **概览 / 备份 抽组件**：新建 `InstanceOverviewTab` / `InstanceBackupTab`，容器改为传 props + 收 emit。行为不变。
3. **配置文件合并为可编辑**：新建 `IniEditorPane` + `InstanceConfigFilesTab`；接容器现有 `saveGameIni` / `saveGameUserSettings`（`PUT`，不变）；**移除**上传按钮 + `<input type=file>` ×2 + `uploadGameIniFile` / `uploadGameUserSettingsFile` 引用、对比按钮 + `ConfigDiffModal` + `getServerConfigs` + `compareGameIni` / `compareGameUserSettings` / `loadServerConfigs` / `handleDiffSave` / `diff*` / `serverGameIniContent` 等。保存按钮 `:disabled="running"`。加脏检测。
4. **基础配置去抽屉**：新建 `InstanceBasicConfigTab`（照搬 `ConfigEditModal` 表单），接到容器 `saveConfig`；移除 `ConfigEditModal` 引用。保存按钮 `:disabled="running"`。加脏检测。
5. **服务器规则去抽屉**：抽 `useArkRulesModel.js`（从 `AdvancedGameConfigDialog` 复制逻辑），新建 `ServerRulesTab` + 二级 Tab 复用 `sections/*`，接容器 `saveAdvancedConfig`；移除 `AdvancedGameConfigDialog` 引用。保存按钮 `:disabled="running"`。加脏检测。
6. **未保存保护**：`useUnsavedGuard.js` + Tab 切换拦截 + `onBeforeRouteLeave` + 标题红点（`basic` / `rules` / `files`）。
7. **懒挂载 / SSE 生命周期打磨**：`:lazy` / `v-if` / 日志与配置文件 Tab `v-show` 保活；`watch(activeTab)` 迁移插件刷新、备份懒加载。
8. **前端清理**：删除容器内失效的 `activeCollapseKeys`、冗余 `watch`、无用 import；从 `app/src/apis/api.js` 移除 `uploadGameIniFile` / `uploadGameUserSettingsFile` / `getServerConfigs` / `getInstanceConfigs`；`npm run build` + 人工回归 §14。
9. **（后端，独立）** 按 §5.5 清单移除对应路由 / 处理器 / helper。

---

## 14. 风险与回归点

| 风险 | 说明 | 应对 |
|---|---|---|
| section 组件 props 契约理解偏差 | 9 个 section 的 props 形状各异（`model` vs `simpleModel` vs 拆开的 `player/dino/engramPoints`） | 逐字对照 `AdvancedGameConfigDialog.vue` 模板第 54–67 行照抄 |
| `useArkRulesModel` 与原 dialog 行为漂移 | 原 `watch(visible)` 里有「每次打开重新解析 + 默认只展开 basic」等细节 | composable 用 `watch(源内容, ...)` 触发重解析；二级 Tab 默认 `area='basic'` |
| 保存后未清脏 / 反复提示 | 容器刷新 `gameIniContent` 后 composable 需重新快照 | 保存成功回调里显式 `reset` 快照基线 |
| 日志 SSE 频繁重连 | Tab 切换卸载 `LogViewer` | 日志面板首次激活后 `v-show` 保活 |
| Monaco 首次挂载在 0 尺寸容器里布局错乱 | 首次进入「配置文件」Tab 才 `v-if` 挂载，之后 `v-show` 保活；`IniEditorPane` 用 `automaticLayout:true` 兜底 | 组件 `onActivated` / `nextTick` 后 `editor.layout()` |
| 「配置文件」切走再切回丢失未保存编辑 | 若用 `v-if` 卸载则 Monaco 内容丢失 | 首次挂载后 `v-show` 保活 + 脏保护拦截 |
| 运行中禁用逻辑遗漏 | 旧代码多处 `:disabled="instanceData?.running"`；本次三处可编辑 Tab 的保存按钮都要带 | 新表单/按钮逐项对照迁移 |
| KeepAlive 缓存 Tab 状态串实例 | 详情页以 `route.fullPath` 为 key，切实例重建 | 不改现状即可 |
| `?tab=` 与浏览器前进后退 | query 变化不重建组件 | `watch(route.query.tab)` 同步 `activeTab` |

---

## 15. 已定决策

1. **规则 9 区域归属**：主 Tab「服务器规则」下用**二级 `<t-tabs>`**（方案 A）。
2. **配置文件 Tab 在服务器运行中**：编辑器**只读**——`IniEditorPane` 传 `:readonly="running"`，运行中只能看不能改（保存按钮同样禁用，但主判据是编辑器只读）。
3. **配置文件两块布局**：**左右两栏**；屏幕变窄后**塌成上下两块**（`@media` 断点，同 `InstanceDetail.vue` 现有 `.config-files-row` 思路）。
4. **RCON 终端**：**保持**操作栏触发的非模态浮窗（`t-dialog :modal="false"`），不做成 Tab。
5. **Tab 状态写入 URL**：`?tab=<key>`，刷新/前进后退可定位（`watch(route.query.tab)` ↔ `activeTab` 双向同步）。
6. **目录与文件命名**：
   - 现有 `app/src/views/InstanceDetail.vue` → **`app/src/views/InstanceDetail/index.vue`**（容器）。
   - 所有新子组件放 **`app/src/views/InstanceDetail/components/`**：`InstanceOverviewTab.vue`、`InstanceBasicConfigTab.vue`、`ServerRulesTab.vue`、`InstanceConfigFilesTab.vue`、`IniEditorPane.vue`、`InstanceBackupTab.vue`。
   - composable 仍放 `app/src/composables/`：`useArkRulesModel.js`、`useUnsavedGuard.js`。
   - **改动引用**：`app/src/router/index.js:3` 的 `import InstanceDetail from '@/views/InstanceDetail.vue'` → `'@/views/InstanceDetail/index.vue'`。`App.vue` / `ServerManager.vue` 只用路由 `name`（`'InstanceDetail'`），**无需改**。路由 `name` 保持 `'InstanceDetail'` 不变（`App.vue` 的 `KeepAlive` key、`ServerManager.vue` 跳转都依赖它）。

---

## 16. 验收清单

- [ ] 详情页首屏只挂载「概览」Tab，Monaco / 日志 SSE / 插件表未在首屏创建。
- [ ] 「基础配置」为页内常驻表单，改动后有「未保存」红点；点遮罩/ESC 不再存在（无抽屉）。
- [ ] 「基础配置」保存走原 `saveConfig`，字段、校验一致；保存按钮 `:disabled="instanceData?.running"`。
- [ ] 「服务器规则」9 区域以二级 Tab 呈现，section 组件零改动；保存走原 `saveAdvancedConfig`，仅写变更文件/字段；保存按钮 `:disabled="instanceData?.running"`。
- [ ] 规则里 `-ForceAllowCaveFlyers` 启动参数联动与旧 dialog 一致。
- [ ] 「配置文件」Tab 内 Game.ini / GameUserSettings.ini 为可编辑 Monaco（**默认左右两栏，窄屏塌成上下**），各自一个「保存」按钮，`:disabled="instanceData?.running"`；保存走原 `PUT /:name/game-ini` / `game-user-settings`。
- [ ] 服务器**运行中**时两个编辑器均为**只读**（`:readonly="running"`）。
- [ ] 「配置文件」Tab **无上传、无对比**入口；`ConfigDiffModal` / `<input type=file>` 已从详情页移除。
- [ ] `app/src/views/InstanceDetail.vue` 已改名为 `app/src/views/InstanceDetail/index.vue`，`router/index.js` import 路径已更新，路由 `name` 仍为 `'InstanceDetail'`，从主页跳转与 `KeepAlive` 行为不变。
- [ ] `app/src/apis/api.js` 已移除 `uploadGameIniFile` / `uploadGameUserSettingsFile` / `getServerConfigs` / `getInstanceConfigs`，全仓库无残留引用。
- [ ] 任一编辑 Tab（basic / rules / files）有脏数据时：切 Tab / 离开路由 均弹确认。
- [ ] 保存成功后脏状态清除、不再提示。
- [ ] 插件 Tab 切入时刷新；备份 Tab 首次切入时懒加载列表。
- [ ] 切换实例（不同 `:name`）时页面状态不串。
- [ ] `AdvancedGameConfigDialog.vue` / `ConfigEditModal.vue` / `ConfigEditor.vue` / `ConfigFileViewer.vue` / `ConfigDiffModal.vue` / `sections/*` 文件**无 diff**（仅取消引用）。
- [ ] `npm run build` 通过，无 console error。
- [ ] （后端跟进）§5.5 清单的路由 / 处理器 / helper 已移除，`go build` + `go vet` 通过。
