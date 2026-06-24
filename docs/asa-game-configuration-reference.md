# ARK: Survival Ascended 游戏配置参考手册

> 本文档基于 [ARK Wiki Server Configuration](https://ark.wiki.gg/wiki/Server_configuration) 页面生成，仅包含 ASA (ARK: Survival Ascended) 支持的配置项。
>
> **兼容性标记说明：**
> - ✅ 已确认兼容 - Wiki 已验证可在 ASA 中使用
> - ⚠️ 未知状态 - Wiki 未验证，可能可用
> - ❌ 已废弃 - 在 ASA 中不可用，已标注替代方案

---

## 目录

- [1. 命令行参数 (Command Line)](#1-命令行参数-command-line)
- [2. GameUserSettings.ini 配置](#2-gameusersettingsini-配置)
  - [2.1 [ServerSettings] 服务器设置](#21-serversettings-服务器设置)
  - [2.2 [SessionSettings] 会话设置](#22-sessionsettings-会话设置)
  - [2.3 [MessageOfTheDay] 每日消息](#23-messageoftheday-每日消息)
- [3. Game.ini 配置](#3-gameini-配置)
  - [3.1 复杂配置项详细格式](#31-复杂配置项详细格式)
- [4. DynamicConfig 动态配置](#4-dynamicconfig-动态配置)
- [5. Ragnarok 地图特殊设置](#5-ragnarok-地图特殊设置)
- [6. 已废弃配置及替代方案](#6-已废弃配置及替代方案)

---

## 1. 命令行参数 (Command Line)

启动服务器时通过命令行指定的参数。

| 参数 | 说明 | 兼容性 |
|------|------|--------|
| `-ActiveEvent=<eventname>` | 启用指定活动或活动颜色调色板。可选值: `ark7th`, `ARKaeology`, `birthday`, `Easter`, `FearEvolved`, `ExtinctionChronicles`, `PAX`, `Summer`, `TurkeyTrial`, `vday`, `WinterWonderland`, `None`。设置后服务器会加载对应活动的特殊生物颜色、装饰和事件内容，设为 `None` 可清除活动状态 | ✅ |
| `?AltSaveDirectoryName=<savedir_name>` | 自定义服务器世界存档目录名，通常用于集群管理。ASA 中仍使用 `<savedir_name>/<map_name>` 目录结构。在多服务器集群环境中，通过统一目录命名可简化存档备份和跨服数据管理 | ✅ |
| `-AlwaysTickDedicatedSkeletalMeshes` | 禁用空闲/非活跃生物动画的动态优化（仅在服务器低 FPS 时生效）。默认情况下服务器会跳过远处或非活跃生物的动画计算以节省性能，启用此参数可确保所有生物动画始终更新，可能导致性能下降 | ✅ |
| `-AutoDestroyStructures` | 启用旧建筑自动销毁。计时器可通过 `AutoDestroyOldStructuresMultiplier` 调整。当玩家长期不登录时，其建筑会在一定时间后自动清除，有助于保持服务器性能和地图整洁，适合 PvE 服务器使用 | ✅ |
| `-culture=<lang_code>` | 覆盖服务器输出语言。支持: `ca`, `cs`, `da`, `de`, `en`, `es`, `eu`, `fi`, `fr`, `hu`, `it`, `ja`, `ka`, `ko`, `nl`, `pl`, `pt_BR`, `ru`, `sv`, `th`, `tr`, `zh`, `zh-Hans-CN`, `zh-TW`。主要影响服务器日志和控制台输出的语言，不影响游戏内客户端语言设置 | ✅ |
| `-DoCustomCosmeticValidation` | 启用自定义装饰模组验证。服务器会检查玩家使用的自定义装饰物品是否来自合法来源，防止使用未经授权或修改的装饰内容，提升服务器安全性 | ✅ |
| `-DisableCustomCosmetics` | 禁用自定义装饰系统。启用后玩家无法使用自定义皮肤、服装等装饰物品，适用于需要严格控制服务器内容或减少潜在兼容性问题的场景 | ✅ |
| `-disabledinonetrangescaling` | 禁用生物网络复制范围优化。默认情况下服务器会根据玩家数量动态调整生物的网络同步范围以节省带宽，禁用后所有生物始终保持完整的同步范围，可能增加网络负载 | ✅ |
| `-DisableCustomFoldersInTributeInventories` | 禁用贡品背包中的自定义文件夹 | ⚠️ |
| `-DisableRailgunPVP` | 禁用 PVP 中的电磁步枪 | ⚠️ |
| `-EasterColors` | 生物刷新时有概率获得复活节颜色。启用后新刷新的野生生物会随机获得复活节活动的特殊颜色配色，无需启用完整复活节活动即可使用 | ✅ |
| `-EnableIdlePlayerKick` | 踢出在 `KickIdlePlayersPeriod` 时间内未移动或交互的角色。需要配合 `KickIdlePlayersPeriod` 参数设置超时时间，适用于防止玩家挂机占用服务器槽位 | ✅ |
| `?EventColorsChanceOverride=<float>` | 覆盖活动颜色出现概率 | ⚠️ |
| `-exclusivejoin` | 某人名单模式，仅允许加入白名单的玩家连接服务器。启用后需要在 `ExclusiveJoinList.txt` 文件中添加允许的 Steam ID，未在列表中的玩家将无法连接 | ✅ |
| `-ForceAllowCaveFlyers` | 强制允许飞行生物进入洞穴。默认情况下洞穴区域禁止骑乘飞行生物，启用此参数可移除此限制，让玩家骑乘飞龙等生物直接进入洞穴探索 | ✅ |
| `-ForceRespawnDinos` | 启动时销毁所有野生生物（不会销毁已驯服的生物）。用于在服务器更新或模组更改后强制刷新生物列表，确保新生物类型或属性变更生效，建议配合定期重启使用 | ✅ |
| `-GBUsageToForceRestart=<value>` | 内存保护：服务器内存达到指定 GB 数时强制保存并重启。设为 0 禁用。当服务器因内存泄漏等问题导致内存持续增长时，此参数可自动触发重启以防止崩溃，建议根据服务器配置设置合理阈值 | ✅ |
| `-imprintlimit=101` | 设置印记上限 | ⚠️ |
| `-MaxNumOfSaveBackups=<integer>` | 设置存档备份数量上限 | ⚠️ |
| `?MapPlayerLocation=false` | 隐藏玩家在地图上的位置 | ⚠️ |
| `-MinimumTimeBetweenInventoryRetrieval=<seconds>` | 设置物品取回的最小间隔时间（秒） | ⚠️ |
| `-mods=<ModId1>[,<ModId2>[...]]` | 指定 CurseForge 模组 ID，启动时自动更新。多个模组用逗号分隔，服务器启动时会自动检查并下载最新版本，模组加载顺序从左到右 | ✅ |
| `-MULTIHOME` | 启用多主页，需在 `GameUserSettings.ini` 的 `[SessionSettings]` 中指定 `MULTIHOME=<IP_ADDRESS>`。用于服务器绑定多个网络接口时指定对外暴露的 IP 地址，适用于多网卡环境 | ✅ |
| `-noantispeedhack` | 禁用反加速作弊 | ⚠️ |
| `-NoBattlEye` | 不使用 BattleEye 反作弊。禁用后服务器将不运行 BattleEye 进程，可减少系统资源占用，但会降低反作弊能力，仅建议在受信任的私有服务器中使用 | ✅ |
| `-nocombineclientmoves` | 禁用客户端移动合并 | ⚠️ |
| `-NoDinos` | 阻止野生生物刷新。启用后 `-NoDinosExcept*` 系列参数不可用。适用于需要完全无野生生物的纯净服务器环境，如纯建筑或测试服务器 | ✅ |
| `-NoHangDetection` | 禁用服务器挂起检测 | ⚠️ |
| `-NotifyAdminCommandsInChat` | 在聊天中通知管理员命令 | ⚠️ |
| `-noundermeshchecking` | 禁用地下网格检查 | ⚠️ |
| `-noundermeshkilling` | 禁用地下网格杀死 | ⚠️ |
| `-NoWildBabies` | 禁用野生幼崽刷新。启用后地图上不再刷新野生幼崽生物，可减少服务器生物数量以提升性能，或用于不需要幼崽机制的游戏模式 | ✅ |
| `-passivemods=<ModId1>[,<ModId2>[...]]` | 禁用模组功能但保留其数据。用于安全移除模组时保留已有的模组物品和数据，避免因直接移除模组导致存档损坏 | ✅ |
| `-port=<Server Port>` | 服务器端口（默认 7777）。指定 UDP 游戏端口，玩家通过此端口连接服务器。同一台机器上运行多个服务器实例时需要使用不同端口 | ✅ |
| `-pvedisallowtribewar` | PvE 模式下禁止部落战争 | ⚠️ |
| `-SecureSendArKPayload` | 安全发送 ARK 负载 | ⚠️ |
| `-servergamelog` | 启用服务器管理员日志（支持 RCON）。启用后管理员可通过 RCON 连接查看服务器实时日志，包括玩家活动、建筑放置、生物驯服等重要事件记录 | ✅ |
| `-servergamelogincludetribelogs` | 在普通服务器日志中包含部落日志。启用后部落成员的活动（如加入、离开、建筑操作等）会记录到服务器主日志文件中，便于管理员追踪部落活动 | ✅ |
| `-ServerRCONOutputTribeLogs` | 在 RCON 输出中包含部落日志。启用后通过 RCON 查询日志时会显示部落相关的事件，如部落战争、成员变动等信息 | ✅ |
| `-ServerUseEventColors` | 启用活动颜色。允许生物在刷新时使用当前活动的特殊颜色配色，需要配合 `-ActiveEvent` 参数使用以指定具体活动 | ✅ |
| `-speedhackbias=<value>` | 设置反加速作弊偏置值 | ⚠️ |
| `-StasisKeepControllers` | 保持休眠生物的 AI 控制器在内存中。默认情况下休眠生物的 AI 会被卸载以节省内存，启用此参数可确保生物在苏醒后立即恢复完整 AI 行为，但会增加内存占用 | ✅ |
| `-structurememopts` | 建筑内存优化选项 | ⚠️ |
| `-TotalConversionMod=<ModID>` | 指定全面转换模组 | ⚠️ |
| `-UseDynamicConfig` | 启用动态配置。允许通过远程 URL 加载配置文件，可在不重启服务器的情况下修改游戏参数，配置文件格式与 GameUserSettings.ini 一致 | ✅ |
| `-UseItemDupeCheck` | 启用物品复制检查 | ⚠️ |
| `-UseSecureSpawnRules` | 使用安全刷新规则 | ⚠️ |
| `-usestore` | 使用官方服务器的存档行为。启用后服务器采用与官方相同的存档格式和保存策略，适合需要与官方服务器保持一致存档管理方式的社区服务器 | ✅ |
| `-UseStructureStasisGrid` | 使用建筑休眠网格 | ⚠️ |
| `-webalarm` | 启用 Web 报警 | ⚠️ |
| `-WinLiveMaxPlayers=<integer>` | 设置服务器最大玩家数（替代 `MaxPlayers`）。直接在命令行中指定服务器可容纳的最大玩家数量，优先级高于 GameUserSettings.ini 中的配置 | ✅ |
| `-ClusterDirOverride=<PATH>` | 指定跨服数据传输存储路径。用于自定义集群数据的存储位置，默认路径可能不适合所有服务器配置，通过此参数可将集群数据指向更快的存储设备或网络共享目录 | ✅ |
| `-clusterid=<CLUSTER_NAME>` | 指定集群 ID 以允许跨服数据传输。只有使用相同集群 ID 的服务器之间才能进行玩家、生物和物品的跨服传输，集群 ID 区分大小写 | ✅ |
| `-NoTransferFromFiltering` | 阻止单人服务器与无集群 ID 服务器之间的数据传输。启用后可防止玩家从单人游戏或无集群的服务器向集群服务器传输数据，保护集群服务器的经济平衡 | ✅ |
| `-ClearOldItems` | 清除旧物品 | ⚠️ |
| `-inlinesaveload` | 内联存档加载 | ⚠️ |
| `-NoBiomeWalls` | 禁用生物群落墙壁 | ⚠️ |
| `-nofishloot` | 禁用鱼类战利品 | ⚠️ |
| `-noninlinesaveload` | 非内联存档加载 | ⚠️ |
| `-oldsaveformat` | 使用旧存档格式 | ⚠️ |
| `-PVPDisablePenetratingHits` | 禁用 PVP 穿透命中 | ⚠️ |
| `-StructureDestructionTag=<newBiomesStructuresZones>` | 建筑销毁标签 | ⚠️ |
| `-usecache` | 使用缓存 | ⚠️ |
| `-vday` | 启用情人节活动 | ⚠️ |
| `-AllowChatSpam` | 允许聊天刷屏 | ⚠️ |
| `-BackupTransferPlayerDatas` | 备份传输玩家数据 | ⚠️ |
| `-BattlEyeServerRecheck` | BattleEye 服务器重新检查 | ⚠️ |
| `-converttostore` | 将非存储格式转换为存储格式。一次性参数，用于将旧的存档格式转换为新的存储格式，转换完成后下次启动时应移除此参数 | ✅ |
| `-CustomAdminCommandTrackingURL=<URL>` | 自定义管理员命令追踪 URL | ⚠️ |
| `-CustomMerticsURL=<URL>` | 自定义指标 URL | ⚠️ |
| `-CustomNotificationURL=<URL>` | 自定义服务器通知广播 URL（仅支持 HTTP）。服务器会将重要事件（如玩家加入、服务器重启等）以 POST 请求发送到指定 URL，可用于集成外部通知系统如 Discord Webhook | ✅ |
| `-dedihibernation` | 专用服务器休眠 | ⚠️ |
| `-disableCharacterTracker` | 禁用角色追踪。关闭服务器对玩家角色位置的追踪功能，可用于减少服务器计算负载，但会影响依赖角色追踪的功能如地图显示 | ✅ |
| `-DisableDupeLogDeletes` | 阻止 `-ForceDupeLog` 生效。当同时使用 `-ForceDupeLog` 时，此参数可防止复制日志被自动删除，保留完整的复制检测记录用于后续分析 | ✅ |
| `-DormancyNetMultiplier=<float>` | 休眠网络倍率 | ⚠️ |
| `-EnableOfficialOnlyVersioningCode` | 启用官方专用版本代码 | ⚠️ |
| `-EnableSteelShield` | 启用 SteelShield DDoS 防护（仅 Nitrado 服务器）。激活 Nitrado 专有的 DDoS 防护服务，可有效抵御大规模流量攻击，仅在 Nitrado 托管的服务器上可用 | ✅ |
| `-EnableVictoryCoreDupeCheck` | 启用 VictoryCore 复制检查 | ⚠️ |
| `-forcedisablemeshchecking` | 强制禁用网格检查 | ⚠️ |
| `-ForceDupeLog` | 强制记录复制日志。启用后服务器会记录所有疑似物品复制的操作日志，用于检测和调查作弊行为，日志文件会占用额外磁盘空间 | ✅ |
| `-forceuseperfthreads` | 强制使用性能线程。强制服务器使用专为性能优化的线程模型，可提升服务器在高负载下的处理能力，建议在多核服务器上使用 | ✅ |
| `-ignoredupeditems` | 检测到复制物品时忽略而非移除。与 `-ForceDupeLog` 配合使用时，复制物品不会被删除而是保留，适合测试环境或需要保留物品的场景 | ✅ |
| `-ip=<ipv4_address>` | 绑定 IP 地址。指定服务器监听的网络接口 IP，适用于多网卡环境，确保服务器只在指定网络接口上接受连接 | ✅ |
| `-MaxConnectionsPerIP=<integer>` | 每 IP 最大连接数 | ⚠️ |
| `-NitradoQueryPort` | Nitrado 查询端口。Nitrado 托管服务器专用的 Steam 查询端口配置参数，用于服务器列表显示和查询功能 | ✅ |
| `-nitradotest2` | Nitrado 测试选项 | ⚠️ |
| `-NoAI` | 禁用生物 AI 控制器。所有生物将停止自主行为（如巡逻、觅食、攻击等），变成静态对象，可大幅提升服务器性能，但会严重影响游戏体验 | ✅ |
| `-NoDinosExceptForcedSpawn` | 阻止野生生物刷新（强制刷新除外）。与 `-NoDinos` 类似但保留了通过代码强制触发的刷新点，适用于需要精控制生物生成的服务器 | ✅ |
| `-NoDinosExceptStreamingSpawn` | 阻止野生生物刷新（流式刷新除外）。保留了流式加载区域的生物刷新，适用于需要在玩家附近保持生物但移除远处生物的场景 | ✅ |
| `-NoDinosExceptManualSpawn` | 阻止野生生物刷新（手动刷新除外）。仅保留管理员手动刷出的生物，自然刷新完全停止，适用于活动服务器或需要精确控制生物出现的场景 | ✅ |
| `-NoDinosExceptWaterSpawn` | 阻止野生生物刷新（水中刷新除外）。仅保留水中的生物刷新，陆地和空中生物不再生成，适用于以海洋探索为主题的服务器 | ✅ |
| `-nodormancythrottling` | 禁用休眠节流 | ⚠️ |
| `-noperfthreads` | 禁用性能线程。不使用性能优化线程模型，适用于调试或在特定硬件环境下出现兼容性问题时使用，通常不建议在生产服务器上启用 | ✅ |
| `-nosound` | 禁用声音以提升性能。服务器端不需要音频输出，禁用声音可减少系统资源占用，建议在所有专用服务器上使用 | ✅ |
| `-onethread` | 禁用多线程。强制服务器使用单线程运行，仅用于调试多线程相关问题，会严重降低服务器性能，不建议在正式环境中使用 | ✅ |
| `-parseservertojson` | 将服务器解析为 JSON | ⚠️ |
| `-pauseonddos` | DDoS 时暂停 | ⚠️ |
| `-PreventTotalConversionSaveDir` | 阻止全面转换存档目录 | ⚠️ |
| `-pveallowtribewar` | PvE 模式下允许部落战争 | ⚠️ |
| `-ReloadedForBackup` | 为备份重新加载 | ⚠️ |
| `-ServerIP=<ipv4_address>` | 服务器 IP 地址。与 `-ip` 参数功能相同，指定服务器绑定的网络接口 IP 地址，适用于需要明确指定监听地址的多网卡环境 | ✅ |
| `-ServerPlatform=<plat1>[+<plat2>[...]]` | 允许连接的平台。可选: `PC`(Steam), `PS5`, `XSX`(XBOX), `WINGDK`(Microsoft Store), `ALL`(跨平台)。用于控制哪些平台的玩家可以连接服务器，多个平台用 `+` 连接 | ✅ |
| `-UnstasisDinoObstructionCheck` | 防止生物在重新渲染时穿模。当休眠生物被唤醒时，服务器会检查其位置是否与建筑或其他物体重叠，并尝试调整位置，避免生物卡在建筑内部 | ✅ |
| `-UseTameEffectivenessClamp` | 使用驯服效果上限 | ⚠️ |
| `-UseServerNetSpeedCheck` | 防止单个 tick 积累过多移动数据。启用后服务器会监控每个 tick 的网络数据量，防止单个玩家发送过多移动数据导致服务器卡顿或崩溃 | ✅ |
| `-ForceIgnoreSingleplayerSpawnRangeCheck` | 单人/非专用服务器忽略刷新范围检查。允许生物在玩家附近更近的位置刷新，适用于单人游戏或小型非专用服务器，可增加遇到生物的频率 | ✅ |
| `-d3d11 -dx11 -sm5` | 强制使用 DirectX 11 Shader Model 5。适用于需要兼容旧显卡或测试特定渲染路径的场景，ASA 默认使用 DirectX 12 | ✅ |
| `-game` | Unreal Engine 编辑器选项（无实际用途）。引擎内部参数，对 ARK 专用服务器的功能没有任何影响，可安全忽略 | ✅ |
| `-LANPLAY` | Unreal Engine LAN 选项（对 ARK 服务器无效）。引擎级别的局域网参数，ARK 服务器使用自己的网络系统，此参数不会产生任何效果 | ✅ |
| `?listen` | Unreal Engine 非专用服务器选项（无实际用途）。引擎内部参数，ARK 使用自己的服务器架构，此参数不会影响服务器行为 | ✅ |
| `-log` | Unreal Engine 日志窗口选项（无实际用途）。控制引擎日志窗口的显示，ARK 服务器有自己的日志系统，此参数通常不起作用 | ✅ |
| `-lowmemory` | 低内存模式 | ⚠️ |
| `-nomansky` | 禁用天空效果 | ⚠️ |
| `-nomemorybias` | 禁用内存偏置 | ⚠️ |
| `-norhithread` | 禁用 RHI 线程 | ⚠️ |
| `-opengl -opengl3 -opengl4` | 强制使用 OpenGL 渲染 | ⚠️ |
| `-PreventHibernation` | 阻止服务器休眠 | ⚠️ |
| `-server` | Unreal Engine 专用服务器选项（无实际用途）。引擎级别的服务器模式标记，ARK 服务器启动时已自动设置相关参数，此参数不会产生额外效果 | ✅ |
| `-vulkan` | 强制使用 Vulkan 渲染（ASA 不支持，会导致崩溃）。ASA 目前不支持 Vulkan 渲染 API，使用此参数会导致服务器启动失败或运行时崩溃，切勿使用 | ✅ |

---

## 2. GameUserSettings.ini 配置

### 2.1 [ServerSettings] 服务器设置

#### 基础设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `ActiveMods` | 模组 ID 列表（逗号分隔，无空格）。优先级从左到右递减。服务器启动时按顺序加载模组，靠前的模组优先级更高，当多个模组修改相同内容时以先加载的为准 | string | 无 | ✅ |
| `ActiveMapMod` | 当前活跃的地图模组 ID。指定用于自定义地图的模组 ID，服务器会加载该模组提供的地图而非默认地图 | string | 无 | ✅ |
| `AdminLogging` | 启用后管理员命令将记录到游戏内聊天。方便所有玩家看到管理员执行的操作，提升服务器管理透明度，适用于需要公开管理行为的社区服务器 | boolean | False | ✅ |
| `ServerAdminPassword` | 管理员密码。玩家在游戏内输入 `enablecheats <密码>` 后可获得管理员权限，使用各种作弊命令。密码应足够复杂以防被猜解，建议定期更换 | string | 无 | ✅ |
| `ServerPassword` | 服务器密码。设置后玩家必须输入正确密码才能连接服务器，留空则为公开服务器。用于限制只有知道密码的玩家才能加入 | string | 无 | ✅ |
| `serverPVE` | 启用 PvE 模式（禁用 PvP）。启用后玩家之间无法造成伤害，适合以合作生存为主的游戏风格，同时会启用建筑衰减等 PvE 专属机制 | boolean | False | ✅ |
| `ServerCrosshair` | 显示准星。启用后屏幕中心会显示十字准星，方便瞄准远程武器，禁用后需要依靠武器自带的瞄准方式 | boolean | True | ✅ |
| `ServerHardcore` | 启用硬核模式（死亡后角色重置为 1 级）。角色死亡后所有等级、印痕和属性都会重置，提供极高难度的生存体验，适合追求挑战的玩家 | boolean | False | ✅ |
| `SessionName` | 服务器名称。显示在服务器列表中的名称，玩家通过此名称识别和搜索服务器，建议使用简洁明了的名称便于玩家查找 | string | ARK #123456 | ✅ |
| `RCONEnabled` | 启用 RCON | boolean | False | ⚠️ |
| `RCONPort` | RCON 端口。远程控制台连接端口，管理员通过 RCON 客户端连接此端口可远程执行命令和查看日志，需要配合 `RCONEnabled=True` 使用 | integer | 27020 | ✅ |
| `RCONServerGameLogBuffer` | RCON 游戏日志缓冲区行数。设置通过 RCON 可查看的历史日志行数，较大的值会占用更多内存但能提供更长的日志历史 | float | 600.0 | ✅ |
| `BanListURL` | 全局封禁列表 URL。指定外部封禁列表文件的 URL，服务器会定期从该 URL 加载被封禁的玩家列表，适用于使用共享封禁名单的服务器集群 | string | 无 | ✅ |
| `AdminListURL` | 管理员列表 URL。指定远程管理员列表文件的 URL，服务器会定期加载列表中的 Steam ID 并赋予管理员权限，便于多人协作管理服务器 | string | 无 | ✅ |
| `AutoRestartIntervalSeconds` | 自动重启间隔（秒）。服务器会在指定时间间隔后自动执行保存和重启操作，有助于清理内存泄漏和保持服务器稳定运行，设为 0 禁用 | float | 未知 | ✅ |
| `UpdateAllowedCheatersInterval` | 远程管理员列表更新间隔（秒）。控制从 `AdminListURL` 重新加载管理员列表的频率，较短的间隔可更快响应管理员变更，但会增加网络请求 | float | 600.0 | ✅ |
| `UseCharacterTracker` | 启用角色追踪。允许服务器记录和追踪玩家角色的位置信息，可用于管理工具查看玩家分布，但会增加少量服务器计算开销 | boolean | False | ✅ |
| `SpectatorPassword` | 观战者密码 | string | 无 | ⚠️ |
| `UseExclusiveList` | 使用独占列表 | boolean | False | ⚠️ |
| `ListenServerTetherDistanceMultiplier` | 监听服务器系绳距离倍率 | float | 1.0 | ⚠️ |

#### 游戏玩法设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `AllowAnyoneBabyImprintCuddle` | 允许任何人照顾幼崽。默认只有幼崽的认领者才能执行照顾操作获得印记，启用后任何部落成员都可以照顾幼崽，方便部落协作养育 | boolean | False | ✅ |
| `AllowCaveBuildingPvE` | PvE 模式下允许在洞穴中建造。默认 PvE 模式禁止洞穴建造以保护资源点，启用后玩家可以在洞穴内放置建筑，但可能影响洞穴资源刷新 | boolean | False | ✅ |
| `AllowCaveBuildingPvP` | PvP 模式下允许在洞穴中建造。PvP 模式默认允许洞穴建造，禁用后可防止玩家在洞穴中建造防御工事封锁资源点或矿洞入口 | boolean | True | ✅ |
| `AllowCrateSpawnsOnTopOfStructures` | 允许在建筑顶部刷新补给箱 | boolean | False | ⚠️ |
| `AllowCryoFridgeOnSaddle` | 允许在平台鞍和木筏上放置低温冰箱。启用后玩家可以在移动平台上使用低温冰箱存储生物，提升移动基地的功能性 | boolean | False | ✅ |
| `AllowFlyerCarryPvE` | PvE 模式下允许飞行生物抓取野生生物。默认 PvE 模式禁止抓取，启用后玩家可以使用阿根廷巨鹰等飞行生物抓取野生生物进行运输或驯服辅助 | boolean | False | ✅ |
| `AllowFlyingStaminaRecovery` | 允许飞行中恢复耐力 | boolean | False | ⚠️ |
| `AllowHideDamageSourceFromLogs` | 在部落日志中隐藏伤害来源。启用后部落日志只记录生物或建筑被破坏，不显示是谁造成的伤害，可用于保护 PvP 服务器中的战术隐私 | boolean | True | ✅ |
| `AllowHitMarkers` | 显示远程攻击命中标记。启用后使用远程武器命中目标时会在准星处显示命中标记反馈，帮助玩家确认攻击是否命中 | boolean | True | ✅ |
| `AllowIntegratedSPlusStructures` | 允许集成 S+ 建筑 | boolean | False | ⚠️ |
| `AllowMultipleAttachedC4` | 允许在一个生物上附着多个 C4。默认每个生物只能附着一个 C4 炸药，启用后可以在单个生物上放置多个 C4，增加战术多样性 | boolean | False | ✅ |
| `AllowRaidDinoFeeding` | 允许永久驯服泰坦龙。默认泰坦龙在驯服后 16-20 小时会自动放走，启用此选项后可以永久保留泰坦龙，大幅改变后期游戏平衡 | boolean | False | ✅ |
| `AllowSharedConnections` | 允许共享连接 | boolean | False | ⚠️ |
| `AllowTekSuitPowersInGenesis` | 在创世纪中允许泰克套装能力 | boolean | False | ⚠️ |
| `AllowThirdPersonPlayer` | 允许第三人称视角。启用后玩家可以切换到第三人称视角进行游戏，提供更广阔的视野范围，禁用后强制使用第一人称视角 | boolean | True | ✅ |
| `AlwaysAllowStructurePickup` | 禁用快速拾取建筑的计时器。启用后玩家可以随时拾取已放置的建筑而不限于放置后的 30 秒窗口，方便建筑调整和重新布局 | boolean | False | ✅ |
| `AlwaysNotifyPlayerLeft` | 总是通知玩家离开 | boolean | False | ⚠️ |
| `ArmadoggoDeathCooldown` | Armadoggo 受致命伤害后重生冷却时间（秒）。控制 Armadoggo 生物在受到致命伤害后需要等待多长时间才能重新刷新，较长的冷却时间可降低该生物的可用频率 | float | 3600 | ✅ |
| `AutoDestroyDecayedDinos` | 自动销毁衰减的生物 | boolean | False | ⚠️ |
| `AutoDestroyOldStructuresMultiplier` | 旧建筑自动销毁倍率 | float | 1.0 | ⚠️ |
| `AutoSavePeriodMinutes` | 自动保存间隔（分钟）。服务器会按指定间隔自动保存世界数据，较短的间隔可减少崩溃时的数据丢失，但会增加磁盘写入频率影响性能 | float | 15.0 | ✅ |
| `bForceCanRideFliers` | 强制允许骑乘飞行生物 | boolean | False | ⚠️ |
| `ClampItemSpoilingTimes` | 将所有腐烂时间限制为物品最大腐烂时间。启用后不会因为堆叠数量增加而延长腐烂时间，所有同类物品统一使用最大腐烂时间，简化物品管理 | boolean | False | ✅ |
| `ClampItemStats` | 限制物品属性 | boolean | False | ⚠️ |
| `ClampResourceHarvestDamage` | 限制驯服生物对资源的采集伤害。启用后生物的采集伤害会被限制在合理范围内，防止高攻击力生物一击摧毁整个资源点，保护资源刷新平衡 | boolean | False | ✅ |
| `CosmeticWhitelistOverride` | 自定义装饰白名单 URL。指定允许使用的自定义装饰物品列表 URL，服务器会验证玩家使用的装饰是否在白名单中，用于控制可用的装饰内容 | string | 无 | ✅ |
| `CosmoWeaponAmmoReloadAmount` | Cosmo 蛛网发射器每次装填弹药量。控制每次装填操作恢复的弹药数量，较高的值可减少装填频率，提升战斗中的持续输出能力 | float | 1 | ✅ |
| `MaxCosmoWeaponAmmo` | Cosmo 蛛网发射器最大弹药量（-1 为随等级缩放）。设置武器的最大弹药容量，设为 -1 时弹药上限会根据武器等级自动缩放，提供更灵活的武器成长体验 | float | -1 | ✅ |
| `CustomDynamicConfigUrl` | 自定义动态配置 URL | string | 无 | ⚠️ |
| `CustomLiveTuningUrl` | 实时调优文件 URL。指定远程实时调优数据文件的 URL，服务器会加载该文件中的参数来动态调整游戏平衡数值，无需重启服务器即可微调游戏体验 | string | 无 | ✅ |
| `DestroyTamesOverTheSoftTameLimit` | 超过软驯服上限的生物标记并销毁。启用后当服务器驯服生物总数超过 `MaxTamedDinos_SoftTameLimit` 时，超出的生物会被标记并在倒计时结束后自动销毁 | boolean | False | ✅ |
| `DifficultyOffset` | 难度等级。控制野生生物的最大等级和战利品质量，值为 1.0 时对应官方默认难度（最大等级 150），配合 `OverrideOfficialDifficulty` 可进一步调整 | float | 1.0 | ✅ |
| `DinoCountMultiplier` | 生物数量倍率 | float | 1.0 | ⚠️ |
| `DisableBurrowDecayTimers` | 禁用 Burrowbuck 的洞穴衰减计时器。启用后 Burrowbuck 生物的洞穴不会随时间衰减消失，适合需要保留这些资源点的服务器 | boolean | False | ✅ |
| `DisableCryopodEnemyCheck` | 允许在敌人附近使用低温舱。默认在敌对玩家附近无法部署低温舱，禁用此检查后可在任何位置使用低温舱，增加战术灵活性 | boolean | False | ✅ |
| `DisableCryopodFridgeRequirement` | 无需低温冰箱即可使用低温舱。默认低温舱需要在低温冰箱附近才能部署生物，启用后可在任何位置自由部署，大幅提升低温舱的便利性 | boolean | False | ✅ |
| `DisableDinoDecayPvE` | 禁用 PvE 模式下的生物衰减。默认 PvE 模式中长期未接触的驯服生物会逐渐衰减，禁用后生物永远不会因衰减而死亡，适合活跃的 PvE 社区 | boolean | False | ✅ |
| `DisableImprintDinoBuff` | 禁用印记生物的玩家属性加成。默认骑乘 100% 印记的生物会给玩家提供属性加成，禁用后印记仅影响生物本身的属性，不传递给骑手 | boolean | False | ✅ |
| `DisablePvEGamma` | 禁止 PvE 模式下使用 gamma 控制台命令。启用后玩家无法通过 gamma 命令调整亮度，保持游戏的原生光照效果，增加夜晚和洞穴的挑战性 | boolean | False | ✅ |
| `DisableStructureDecayPvE` | 禁用 PvE 模式下的建筑自动衰减。默认 PvE 模式中离开玩家的建筑会随时间衰减，禁用后建筑永不衰减，但可能导致废弃建筑堆积影响服务器性能 | boolean | False | ✅ |
| `DisableWeatherFog` | 禁用雾天。启用后地图上不会出现雾天效果，提升能见度和游戏体验，同时可减少因雾天导致的渲染性能开销 | boolean | False | ✅ |
| `DontAlwaysNotifyPlayerJoined` | 禁用玩家加入通知。启用后玩家加入服务器时不会在所有玩家屏幕上显示通知，减少对其他玩家的干扰 | boolean | False | ✅ |
| `EnableExtraStructurePreventionVolumes` | 在特定资源丰富区域禁用建造。启用后会在重要的资源点、矿脉等区域设置禁止建造区域，防止玩家封锁资源点影响其他玩家获取资源 | boolean | False | ✅ |
| `EnablePvPGamma` | 允许 PvP 模式下使用 gamma 控制台命令。启用后 PvP 玩家可以调整游戏亮度，在夜间或洞穴中获得更好的视野，但可能影响游戏公平性 | boolean | False | ✅ |
| `ExtinctionEventTimeInterval` | 灭绝事件时间间隔 | float | 0 | ⚠️ |
| `FastDecayUnsnappedCoreStructures` | 快速衰减未连接的核心建筑 | boolean | False | ⚠️ |
| `ForceAllStructureLocking` | 默认锁定所有建筑。启用后所有新放置的建筑自动进入锁定状态，其他玩家无法打开门、箱子等，需要手动解锁才能共享访问权限 | boolean | False | ✅ |
| `ForceGachaUnhappyInCaves` | Gacha 在洞穴中变为不快乐状态。启用后 Gacha 生物在洞穴内会降低产出效率，防止玩家利用洞穴环境无限刷取资源 | boolean | True | ✅ |
| `globalVoiceChat` | 启用全局语音聊天。启用后所有在线玩家都可以通过语音聊天交流，不受距离限制，禁用后仅附近玩家可以听到语音 | boolean | False | ✅ |
| `NonPermanentDiseases` | 使永久疾病不再永久。启用后原本永久的疾病会随时间自动痊愈，降低疾病对玩家的惩罚力度，适合休闲游戏模式 | boolean | False | ✅ |
| `OverrideStructurePlatformPrevention` | 允许在平台鞍上建造和使用炮塔。默认平台鞍上有建造和炮塔限制，启用后可在平台鞍上自由建造并放置自动炮塔，增强移动基地的战斗力 | boolean | False | ✅ |
| `PreventDiseases` | 完全阻止疾病。启用后玩家不会感染任何疾病，移除疾病机制带来的生存压力，适合轻松休闲的游戏模式 | boolean | False | ✅ |
| `PreventMateBoost` | 禁用生物配偶加成。默认成对的异性生物会提供配偶加成效果（如伤害提升、减伤等），禁用后移除此机制，简化生物管理 | boolean | False | ✅ |
| `PreventOfflinePvP` | 启用离线突袭防护（ORP）。当部落所有成员都离线后，其建筑和生物会获得伤害减免保护，防止被在线玩家趁虚攻击，保护离线玩家的劳动成果 | boolean | False | ✅ |
| `PreventOfflinePvPInterval` | ORP 激活前等待时间（秒）。设置部落所有成员离线后需要等待多长时间才激活离线保护，防止玩家短暂下线就获得保护 | float | 0.0 | ✅ |
| `PreventSpawnAnimations` | 禁用重生动画。启用后玩家重生时跳过动画直接进入游戏，减少等待时间，提升游戏流畅度 | boolean | False | ✅ |
| `PreventTribeAlliances` | 阻止部落创建联盟。启用后部落之间无法建立联盟关系，限制大型势力的形成，促进部落间的独立竞争 | boolean | False | ✅ |
| `ProximityChat` | 启用近距离聊天。启用后聊天消息只有附近的玩家才能看到，模拟真实的对话距离限制，增加沉浸感，适合 RP 服务器 | boolean | False | ✅ |
| `RandomSupplyCratePoints` | 补给箱随机位置。启用后补给箱的刷新位置会在预设点之间随机选择，增加寻找补给箱的不确定性和探索乐趣 | boolean | False | ✅ |
| `ShowFloatingDamageText` | 显示浮动伤害数字。启用后攻击目标时会在屏幕上显示造成的伤害数值，方便玩家直观了解攻击效果和生物血量变化 | boolean | False | ✅ |
| `ShowMapPlayerLocation` | 在地图上显示玩家位置。启用后地图上会标记当前玩家的位置，方便导航和定位，禁用后需要依靠地标和指南针辨认方向 | boolean | True | ✅ |
| `UseAstraeosTraversalBuff` | 启用 Astraeos 的生物群落传送。在 Astraeos 地图上，玩家骑乘生物穿越不同生物群落时会获得临时增益效果，提升探索体验 | boolean | True | ✅ |
| `UseFjordurTraversalBuff` | 启用 Fjordur 的生物群落传送 | boolean | True | ⚠️ |
| `UseOptimizedHarvestingHealth` | 使用优化的采集生命值 | boolean | False | ⚠️ |
| `PvEAllowStructuresAtSupplyDrops` | PvE 模式下允许在补给点附近建造。默认 PvE 模式禁止在补给箱刷新点附近建造，启用后玩家可以在补给点周围放置建筑 | boolean | False | ✅ |
| `PvEStructureDecayPeriodMultiplier` | PvE 建筑衰减周期倍率 | float | 1.0 | ⚠️ |
| `PvPDinoDecay` | PvP 模式下启用生物衰减。启用后长期未接触的驯服生物会逐渐衰减，防止玩家通过大量囤积生物来获得不公平优势 | boolean | False | ✅ |
| `PvPStructureDecay` | PvP 模式下启用建筑衰减 | boolean | False | ⚠️ |
| `RaidDinoCharacterFoodDrainMultiplier` | 突袭生物食物消耗倍率。影响大型突袭生物（如泰坦龙、飞龙等）的食物消耗速度，较低的值减少喂食频率 | float | 1.0 | ✅ |
| `StructureDamageMultiplier` | 建筑伤害倍率 | float | 1.0 | ⚠️ |
| `TamedDinoDamageMultiplier` | 驯服生物伤害倍率 | float | 1.0 | ⚠️ |
| `TamedDinoResistanceMultiplier` | 驯服生物抗性倍率 | float | 1.0 | ⚠️ |
| `TribeLogDestroyedEnemyStructures` | 在部落日志中记录摧毁敌方建筑 | boolean | False | ⚠️ |

#### 倍率设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `DayCycleSpeedScale` | 昼夜循环速度倍率。控制 ARK 中时间流逝的速度，决定白天变为夜晚和夜晚变为白天的频率。默认值 1 提供与单人游戏和官方服务器相同的循环速度。值低于 1 会减慢循环，高于 1 会加速循环。当值为 1 时，1 分钟现实时间约等于 28 分钟游戏时间，因此要实现约 24 小时的昼夜循环，需要设置为 0.025 | float | 1.0 | ✅ |
| `DayTimeSpeedScale` | 白天时间速度倍率。指定 ARK 中白天时间流逝的缩放因子。此值决定每个白天的长度，相对于夜晚长度（由 NightTimeSpeedScale 指定）。降低此值会增加每个白天的长度 | float | 1.0 | ✅ |
| `NightTimeSpeedScale` | 夜晚时间速度倍率。指定 ARK 中夜晚时间流逝的缩放因子。此值决定每个夜晚的长度，相对于白天长度（由 DayTimeSpeedScale 指定）。降低此值会增加每个夜晚的长度 | float | 1.0 | ✅ |
| `DifficultyOffset` | 难度等级。指定服务器的难度级别，影响野生生物的最高等级和资源品质。默认值 1.0 对应官方难度 4，值为 1.2 时对应难度 5（生物最高 150 级） | float | 1.0 | ✅ |
| `OverrideOfficialDifficulty` | 覆盖官方难度。允许将默认难度等级 4 覆盖为 5 以匹配新的官方服务器难度。默认值 0.0 禁用覆盖。设为 5.0 时普通生物最高刷新到 150 级，设为更高值可出现更高等级生物 | float | 0.0 | ✅ |
| `DinoDamageMultiplier` | 野生生物攻击伤害倍率。影响所有野生生物造成的伤害，值为 2.0 时伤害翻倍，用于调整 PvE 难度 | float | 1.0 | ✅ |
| `DinoResistanceMultiplier` | 野生生物受伤抗性倍率。影响野生生物受到的伤害，值越小生物越难被杀死，设为 0.5 则野生生物受到的伤害减半 | float | 1.0 | ✅ |
| `DinoCharacterFoodDrainMultiplier` | 生物食物消耗倍率。影响所有生物的食物消耗速度，较低的值使生物需要更少食物，减少喂食频率 | float | 1.0 | ✅ |
| `DinoCharacterHealthRecoveryMultiplier` | 生物生命恢复倍率。影响生物的生命值恢复速度，较高的值使生物更快回血，适合需要快速恢复的游戏节奏 | float | 1.0 | ✅ |
| `DinoCharacterStaminaDrainMultiplier` | 生物耐力消耗倍率。影响生物的耐力消耗速度，较低的值使生物可以更持久地奔跑、飞行或游泳 | float | 1.0 | ✅ |
| `HarvestAmountMultiplier` | 采集产量倍率。影响所有资源采集的产出数量，值为 3.0 时每次采集获得 3 倍资源，适合加速游戏进度 | float | 1.0 | ✅ |
| `HarvestHealthMultiplier` | 可采集物品生命值倍率。影响树木、石头等资源点的血量，较高的值需要更多次采集才能摧毁资源点，增加采集的持续性 | float | 1.0 | ✅ |
| `ItemStackSizeMultiplier` | 全局物品堆叠大小倍率。影响所有可堆叠物品的最大堆叠数量，值为 2.0 时堆叠上限翻倍，减少背包空间压力 | float | 1.0 | ✅ |
| `PlayerDamageMultiplier` | 玩家攻击伤害倍率。影响玩家使用武器和工具造成的伤害，值为 2.0 时伤害翻倍，用于调整玩家的战斗能力 | float | 1.0 | ✅ |
| `PlayerResistanceMultiplier` | 玩家受伤抗性倍率。影响玩家受到的伤害，值越小玩家越难被杀死，设为 0.5 则受到的伤害减半 | float | 1.0 | ✅ |
| `PlayerCharacterFoodDrainMultiplier` | 玩家食物消耗倍率。影响玩家的饥饿速度，较低的值使玩家需要更少食物，减少生存压力 | float | 1.0 | ✅ |
| `PlayerCharacterHealthRecoveryMultiplier` | 玩家生命恢复倍率。影响玩家的生命值恢复速度，较高的值使玩家更快回血，减少对药品的依赖 | float | 1.0 | ✅ |
| `PlayerCharacterStaminaDrainMultiplier` | 玩家耐力消耗倍率。影响玩家的耐力消耗速度，较低的值使玩家可以更持久地奔跑和使用工具 | float | 1.0 | ✅ |
| `PlayerCharacterWaterDrainMultiplier` | 玩家水分消耗倍率。影响玩家的口渴速度，较低的值使玩家需要更少饮水，减少寻找水源的频率 | float | 1.0 | ✅ |
| `OxygenSwimSpeedStatMultiplier` | 氧气属性对游泳速度的倍率。影响氧气属性点对游泳速度的加成效果，较高的值使高氧气角色游得更快 | float | 1.0 | ✅ |
| `ResourcesRespawnPeriodMultiplier` | 资源重生速度倍率。影响被采集资源的重生时间，较低的值使资源更快重生，适合需要大量资源的服务器 | float | 1.0 | ✅ |
| `StructureResistanceMultiplier` | 建筑受伤抗性倍率。影响建筑受到的伤害，值越小建筑越难被破坏，设为 0.5 则建筑受到的伤害减半 | float | 1.0 | ✅ |
| `StructurePreventResourceRadiusMultiplier` | 建筑周围资源禁生区域倍率。影响建筑周围禁止资源刷新的范围，较大的值使建筑周围更大范围内不刷新资源 | float | 1.0 | ✅ |
| `TamingSpeedMultiplier` | 驯服速度倍率。影响驯服生物所需的时间和食物，值为 3.0 时驯服速度变为 3 倍，大幅缩短驯服过程 | float | 1.0 | ✅ |
| `XPMultiplier` | 经验值倍率。影响所有经验获取的倍率，值为 2.0 时获得双倍经验，加速角色和生物的升级过程 | float | 1.0 | ✅ |
| `PvEDinoDecayPeriodMultiplier` | PvE 生物衰减时间倍率。影响 PvE 模式下驯服生物的衰减周期，较长的衰减时间给予玩家更多时间回来照顾生物 | float | 1.0 | ✅ |
| `StructurePickupHoldDuration` | 快速拾取建筑按住时间（秒）。设置拾取建筑需要长按的时间，较短的时间使拾取操作更快响应，但可能增加误操作风险 | float | 0.5 | ✅ |
| `StructurePickupTimeAfterPlacement` | 放置后可快速拾取的时间（秒）。设置建筑放置后允许快速拾取的时间窗口，超时后只能通过拆除来移除建筑，设为 0 则禁用快速拾取 | float | 30.0 | ✅ |
| `TheMaxStructuresInRange` | 特定范围内最大建筑数量。限制在一定区域内可以放置的建筑总数，防止玩家在小范围内堆积大量建筑导致服务器性能下降 | integer | 10500 | ✅ |
| `PerPlatformMaxStructuresMultiplier` | 平台鞍/木筏上最大建筑数量倍率。影响平台鞍和木筏上可以放置的建筑数量，较高的值允许在移动平台上建造更多建筑 | float | 1.0 | ✅ |
| `PlatformSaddleBuildAreaBoundsMultiplier` | 平台鞍建造区域范围倍率。影响平台鞍上可建造区域的大小，较大的值扩展建造边界，允许在更大范围内放置建筑 | float | 1.0 | ✅ |
| `TribeNameChangeCooldown` | 部落名称更改冷却时间（分钟）。设置更改部落名称后需要等待的时间才能再次更改，防止频繁改名造成混淆 | float | 15.0 | ✅ |
| `MaxTamedDinos` | 服务器最大驯服生物数量。限制整个服务器可容纳的驯服生物总数，达到上限后无法继续驯服新生物，用于控制服务器性能 | float | 5000.0 | ✅ |
| `MaxPersonalTamedDinos` | 每部落驯服生物上限（0 为禁用）。限制每个部落可拥有的驯服生物数量，设为 0 则不限制，用于平衡部落间的资源分配 | integer | 0 | ✅ |
| `MaxTamedDinos_SoftTameLimit` | 服务器软驯服上限。当驯服生物数量超过此值时会触发销毁倒计时机制，给予玩家时间处理多余的生物，而非立即阻止驯服 | integer | 5000 | ✅ |
| `MaxTamedDinos_SoftTameLimit_CountdownForDeletionDuration` | 超过软上限后生物销毁倒计时（秒）。设置超过软驯服上限后生物被标记为待销毁的等待时间，给予玩家足够时间决定保留哪些生物 | integer | 604800 | ✅ |
| `MaxTrainCars` | 火车最大车厢数。限制火车可以连接的最大车厢数量，较少的车厢数可减少火车相关的物理计算负载 | integer | 8 | ✅ |
| `MaxTributeDinos` | 上传生物槽位。限制玩家在跨服传输终端可同时上传的生物数量，较小的值限制跨服转移能力 | integer | 20 | ✅ |
| `MaxTributeItems` | 上传物品槽位。限制玩家在跨服传输终端可同时上传的物品数量，控制跨服物品转移的规模 | integer | 50 | ✅ |
| `MaxTributeCharacters` | 上传角色槽位 | integer | 5 | ⚠️ |
| `MaxGateFrameOnSaddles` | 平台鞍上门框最大数量 | integer | 6 | ⚠️ |
| `MaxHexagonsPerCharacter` | 每角色最大六角币数量 | integer | 无 | ⚠️ |
| `MaxPlatformSaddleStructureLimit` | 平台鞍建筑限制 | integer | 无 | ⚠️ |
| `PersonalTamedDinosSaddleStructureCost` | 驯服生物鞍建筑成本 | integer | 无 | ⚠️ |
| `KickIdlePlayersPeriod` | 踢出空闲玩家的时间（秒）。需要配合 `-EnableIdlePlayerKick` 命令行参数使用，设置玩家未操作多长时间后被踢出服务器 | float | 3600.0 | ✅ |
| `ImplantSuicideCD` | 植入物"重生"功能冷却时间（秒）。设置使用植入物自杀重生后需要等待的冷却时间，防止玩家频繁利用自杀来快速传送或恢复状态 | float | 28800 | ✅ |
| `IgnoreLimitMaxStructuresInRangeTypeFlag` | 移除装饰建筑限制。启用后装饰性建筑（如旗帜、火把等）不再计入区域建筑数量限制，允许玩家更自由地装饰基地 | boolean | False | ✅ |
| `NPCNetworkStasisRangeScalePlayerCountStart` | NPC 网络休眠范围缩放玩家数量起始值 | integer | 0 | ⚠️ |
| `NPCNetworkStasisRangeScalePlayerCountEnd` | NPC 网络休眠范围缩放玩家数量结束值 | integer | 0 | ⚠️ |
| `NPCNetworkStasisRangeScalePercentEnd` | NPC 网络休眠范围缩放百分比结束值 | float | 0 | ⚠️ |
| `OnlyAutoDestroyCoreStructures` | 仅自动销毁核心建筑 | boolean | False | ⚠️ |
| `OnlyDecayUnsnappedCoreStructures` | 仅衰减未连接的核心建筑 | boolean | False | ⚠️ |
| `ServerAutoForceRespawnWildDinosInterval` | 服务器自动强制刷新野生生物间隔 | float | 0 | ⚠️ |
| `MinimumDinoReuploadInterval` | 最小生物重新上传间隔 | float | 0 | ⚠️ |
| `TributeCharacterExpirationSeconds` | 上传角色过期时间（秒） | integer | 86400 | ⚠️ |
| `TributeDinoExpirationSeconds` | 上传生物过期时间（秒） | integer | 86400 | ⚠️ |
| `TributeItemExpirationSeconds` | 上传物品过期时间（秒） | integer | 86400 | ⚠️ |

#### Cryopod（低温舱）设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `CryopodNerfDamageMult` | 低温舱削弱伤害倍率 | float | 0 | ⚠️ |
| `CryopodNerfDuration` | 低温舱削弱持续时间 | float | 0 | ⚠️ |
| `CryopodNerfIncomingDamageMultPercent` | 低温舱削弱受到伤害倍率百分比 | float | 0 | ⚠️ |
| `EnableCryopodNerf` | 启用低温舱削弱 | boolean | False | ⚠️ |
| `EnableCryoSicknessPVE` | 启用 PvE 低温舱疾病 | boolean | False | ⚠️ |

#### 跨服传输设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `CrossARKAllowForeignDinoDownloads` | 允许在畸变地图下载非本地生物。默认畸变地图只允许本地生物下载，启用后可从其他地图传输非本地生物进入畸变 | boolean | False | ✅ |
| `noTributeDownloads` | 阻止跨服数据下载。启用后玩家无法从其他服务器下载之前上传的生物、物品或角色数据，完全切断跨服数据传输 | boolean | False | ✅ |
| `PreventDownloadDinos` | 阻止从 ARK 数据下载生物。单独控制生物的跨服下载，允许玩家上传但不能下载，用于限制外部生物进入服务器 | boolean | False | ✅ |
| `PreventDownloadItems` | 阻止从 ARK 数据下载物品。单独控制物品的跨服下载，防止外部物品进入服务器经济系统，保护本地游戏平衡 | boolean | False | ✅ |
| `PreventDownloadSurvivors` | 阻止从 ARK 数据下载幸存者。防止玩家从其他服务器带入高级角色，确保所有玩家从同一起点开始 | boolean | False | ✅ |
| `PreventUploadDinos` | 阻止上传生物到 ARK 数据。禁止玩家将生物上传到跨服数据，防止生物被转移到其他服务器，保留服务器生态完整性 | boolean | False | ✅ |
| `PreventUploadItems` | 阻止上传物品到 ARK 数据。禁止玩家将物品上传到跨服数据，防止资源和装备通过跨服转移流失 | boolean | False | ✅ |
| `PreventUploadSurvivors` | 阻止上传幸存者到 ARK 数据。禁止玩家将角色数据上传到跨服系统，确保玩家角色只能在当前服务器内活动 | boolean | False | ✅ |

#### Bunker（地堡）设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `LimitBunkersPerTribe` | 限制每部落地堡数量。启用后每个部落只能拥有有限数量的地堡建筑，防止部落过度使用地堡获得不公平优势 | boolean | True | ✅ |
| `LimitBunkersPerTribeNum` | 每部落最大地堡数。设置每个部落可以拥有的地堡数量上限，配合 `LimitBunkersPerTribe` 使用，控制地堡的使用规模 | integer | 3 | ✅ |
| `AllowBunkersInPreventionZones` | 允许在防护区域内建造地堡。默认防护区域禁止建造，启用后可在特定防护区域内放置地堡，扩大地堡的可用范围 | boolean | False | ✅ |
| `AllowRidingDinosInsideBunkers` | 允许在地堡内骑乘生物。启用后玩家可以在地堡内部骑乘生物移动，提升地堡的机动性和战术价值 | boolean | True | ✅ |
| `AllowBunkerModulesAboveGround` | 允许地堡模块在地面以上。默认地堡模块只能在地下建造，启用后可在地面以上放置地堡模块，扩展建筑可能性 | boolean | False | ✅ |
| `AllowDinoAIInsideBunkers` | 允许地堡内生物 AI。启用后地堡内的生物可以正常执行 AI 行为（如巡逻、攻击等），禁用后地堡内生物变为被动状态 | boolean | True | ✅ |
| `AllowBunkerModulesInPreventionZones` | 允许在防护区域内地堡模块。启用后可在防护区域内建造地堡模块，配合 `AllowBunkersInPreventionZones` 使用 | boolean | False | ✅ |
| `MinDistanceBetweenBunkers` | 地堡之间最小距离。设置两个地堡之间的最小间隔距离，防止玩家在小范围内密集建造多个地堡 | float | 3000.0 | ✅ |
| `EnemyAccessBunkerHPThreshold` | 敌人可攻击地堡的血量阈值。当敌方地堡血量低于此百分比时才可被攻击，防止新放置的地堡立即被摧毁 | float | 0.25 | ✅ |
| `BunkerUnderHPThresholdDmgMultiplier` | 地堡低于血量阈值时的伤害倍率。当敌方地堡血量低于阈值后，受到的伤害会按此倍率增加，加速低血量地堡的摧毁过程 | float | 0.05 | ✅ |

#### CryoHospital（低温医院）设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `CryoHospitalHoursToRegenHP` | 低温医院恢复生命值时间（小时）。设置生物在低温医院中完全恢复生命值所需的小时数，较短的时间加速治疗过程 | float | 1.0 | ✅ |
| `CryoHospitalHoursToRegenFood` | 低温医院恢复食物时间（小时）。设置生物在低温医院中完全恢复食物值所需的小时数，较长的时间模拟缓慢的营养补充过程 | float | 24.0 | ✅ |
| `CryoHospitalHoursToDrainTorpor` | 低温医院消耗昏迷值时间（小时）。设置生物在低温医院中完全清除昏迷值所需的小时数，帮助受伤或被麻醉的生物恢复清醒 | float | 1.0 | ✅ |
| `CryoHospitalMatingCooldownReduction` | 低温医院交配冷却减少量。生物在低温医院中停留时可缩短交配冷却时间，加速繁殖周期，适合需要快速繁殖的服务器 | float | 2.0 | ✅ |

#### Bloodforge（血锻）设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `BloodforgeReinforceExtraDurability` | 血锻强化额外耐久度。设置通过血锻强化装备时获得的额外耐久度百分比，较高的值使强化后的装备更持久 | float | 0.3 | ✅ |
| `BloodforgeReinforceResourceCostMultiplier` | 血锻强化资源消耗倍率。影响血锻强化所需的资源数量，较低的值减少材料消耗，降低强化门槛 | float | 3.0 | ✅ |
| `BloodforgeReinforceSpeedMultiplier` | 血锻强化速度倍率。影响血锻强化的完成速度，较高的值缩短强化时间，加快装备升级节奏 | float | 0.1 | ✅ |

#### 前哨站设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `MaxActiveOutposts` | 最大活跃前哨站数。限制服务器中同时存在的前哨站数量，防止过多前哨站影响服务器性能 | integer | 无 | ✅ |
| `MaxActiveResourceCaches` | 最大活跃资源缓存数。限制服务器中同时存在的资源缓存数量，控制资源点的分布密度 | integer | 无 | ✅ |
| `MaxActiveCityOutposts` | 最大城市前哨站数。限制服务器中同时存在的城市前哨站数量，与普通前哨站分开计算 | integer | 无 | ✅ |
| `OutpostSigilRewardMultiplier` | 前哨站任务印章奖励倍率。影响完成前哨站任务获得的印章数量，较高的值加速印章积累，加快商店购买进度 | float | 1.0 | ✅ |

#### 敏感词过滤

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `BadWordListURL` | 敏感词列表 URL。指定敏感词过滤列表的 URL，服务器会从该地址加载需要过滤的词汇，用于自动屏蔽不当内容 | string | 官方默认列表 | ✅ |
| `BadWordWhiteListURL` | 白名单词列表 URL。指定豁免词汇列表的 URL，列表中的词汇即使包含敏感词也不会被过滤，用于避免误杀正常用语 | string | 官方默认列表 | ✅ |
| `bFilterCharacterNames` | 过滤角色名称 | boolean | True | ⚠️ |
| `bFilterChat` | 过滤聊天内容 | boolean | True | ⚠️ |
| `bFilterTribeNames` | 过滤部落名称 | boolean | True | ⚠️ |

#### 已废弃/重命名配置

| 配置项 | 说明 | 兼容性 |
|--------|------|--------|
| `AllowDeprecatedStructures` | 允许已废弃建筑 | ⚠️ |
| `bAllowFlyerCarryPVE` | 已废弃，使用 `AllowFlyerCarryPvE` | ⚠️ |
| `bDisableStructureDecayPvE` | 已废弃，使用 `DisableStructureDecayPvE` | ⚠️ |
| `ForceFlyerExplosives` | 强制飞行爆炸物 | ⚠️ |
| `MaxStructuresInRange` | 已废弃，使用 `TheMaxStructuresInRange` | ⚠️ |
| `NewMaxStructuresInRange` | 已废弃，使用 `TheMaxStructuresInRange` | ⚠️ |
| `PvEStructureDecayDestructionPeriod` | PvE 建筑衰减销毁周期 | ⚠️ |

#### 日志与调试

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `ChatLogFileSplitIntervalSeconds` | 聊天日志文件分割间隔（秒） | integer | 无 | ⚠️ |
| `ChatLogFlushIntervalSeconds` | 聊天日志刷新间隔（秒） | integer | 无 | ⚠️ |
| `ChatLogMaxAgeInDays` | 聊天日志最大保留天数 | integer | 无 | ⚠️ |
| `DontRestoreBackup` | 不恢复备份 | boolean | False | ⚠️ |
| `EnableAFKKickPlayerCountPercent` | 启用 AFK 踢出玩家数量百分比 | float | 0 | ⚠️ |
| `EnableMeshBitingProtection` | 启用网格咬合保护 | boolean | False | ⚠️ |
| `FreezeReaperPregnancy` | 冻结 Reaper 孕育 | boolean | False | ⚠️ |
| `LogChatMessages` | 记录聊天消息 | boolean | False | ⚠️ |
| `MaxStructuresInSmallRadius` | 小范围内最大建筑数量 | integer | 无 | ⚠️ |
| `MaxStructuresToProcess` | 处理建筑的最大数量 | integer | 无 | ⚠️ |
| `PreventOutOfTribePinCodeUse` | 阻止部落外 PIN 码使用 | boolean | False | ⚠️ |
| `RadiusStructuresInSmallRadius` | 小范围内建筑的半径 | float | 无 | ⚠️ |
| `ServerEnableMeshChecking` | 启用服务器网格检查 | boolean | False | ⚠️ |
| `TribeMergeAllowed` | 允许部落合并 | boolean | True | ⚠️ |
| `TribeMergeCooldown` | 部落合并冷却时间 | float | 无 | ⚠️ |

---

### 2.2 [SessionSettings] 会话设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `Port` | UDP 游戏端口 | integer | 7777 | ✅ |
| `SessionName` | 服务器名称 | string | ARK #123456 | ✅ |
| `MultiHome` | MultiHome IP 地址。需同时设置 `MULTIHOME=<boolean>` 为 True | string | 无 | ⚠️ |
| `QueryPort` | UDP Steam 查询端口 | integer | 27015 | ⚠️ |

---

### 2.3 [MessageOfTheDay] 每日消息

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `Duration` | 登录时消息显示持续时间（秒） | integer | 20 | ✅ |
| `Message` | 登录时显示的消息。使用 `\n` 换行 | string | 无 | ✅ |

---

## 3. Game.ini 配置

### 繁殖与幼崽设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `BabyCuddleGracePeriodMultiplier` | 幼崽照顾宽限期倍率。当幼崽需要照顾时，玩家在宽限期内完成照顾不会影响印记质量。增大此值可给予玩家更多时间响应照顾请求，适合无法长时间在线的玩家 | float | 1.0 | ✅ |
| `BabyCuddleIntervalMultiplier` | 幼崽照顾频率倍率。控制幼崽发出照顾请求的时间间隔，值越大间隔越长。降低此值可减少玩家需要频繁登录照顾幼崽的负担，适合休闲服务器 | float | 1.0 | ✅ |
| `BabyCuddleLoseImprintQualitySpeedMultiplier` | 宽限期后印记质量下降速度倍率。当玩家错过幼崽的照顾请求后，印记质量会逐渐下降。增大此值会加速下降过程，减小则给予玩家更多补救机会 | float | 1.0 | ✅ |
| `BabyFoodConsumptionSpeedMultiplier` | 幼崽食物消耗速度倍率。影响幼崽阶段生物的食物消耗速率，值越大消耗越快。降低此值可减少幼崽期间需要准备的食物量，降低养育难度 | float | 1.0 | ✅ |
| `BabyImprintAmountMultiplier` | 每次照顾提供的印记百分比倍率。增大此值可使每次照顾操作获得更高的印记百分比，减少达到 100% 印记所需的照顾次数，适合加速繁殖流程的服务器 | float | 1.0 | ✅ |
| `BabyImprintingStatScaleMultiplier` | 印记质量对生物属性的影响倍率。控制 100% 印记给生物带来的属性加成强度，设为 0 则完全禁用印记属性加成。高倍率可使满印记生物获得更显著的属性提升 | float | 1.0 | ✅ |
| `BabyMatureSpeedMultiplier` | 幼崽成熟速度倍率。控制幼崽成长为成年生物的速度，值越大成熟越快。增大此值可大幅缩短从幼崽到成年的时间，减少玩家的等待和照顾负担 | float | 1.0 | ✅ |
| `EggHatchSpeedMultiplier` | 受精蛋孵化速度倍率。影响受精蛋从开始孵化到破壳所需的时间，值越大孵化越快。适合需要加速繁殖周期的服务器，可配合幼崽成熟速度一起调整 | float | 1.0 | ✅ |
| `LayEggIntervalMultiplier` | 生物下蛋间隔倍率。控制雌性生物产卵的时间间隔，值越大间隔越长。降低此值可增加产卵频率，方便需要大量蛋类资源（如制作饲料）的玩家 | float | 1.0 | ✅ |
| `MatingIntervalMultiplier` | 生物交配间隔倍率。控制雌性生物两次交配之间的冷却时间，值越大间隔越长。降低此值可缩短繁殖冷却，加速生物种群的扩充速度 | float | 1.0 | ✅ |
| `MatingSpeedMultiplier` | 生物交配速度倍率。控制两只异性生物完成交配过程的速度，值越大交配越快。增大此值可减少生物需要待在一起的时间，提高繁殖效率 | float | 1.0 | ✅ |
| `PoopIntervalMultiplier` | 生物排便频率倍率。控制所有生物排便的时间间隔，值越大间隔越长。降低此值可增加排便频率，粪便是制作化肥和种植的重要原材料 | float | 1.0 | ✅ |
| `WildDinoCharacterFoodDrainMultiplier` | 野生生物食物消耗速度倍率。影响野生生物的饥饿速率，值越大饥饿越快。调整此值可间接影响被动驯服的难度，因为饥饿的生物更容易接受喂食 | float | 1.0 | ✅ |
| `PreventBreedingForClassNames` | 阻止指定类名的生物参与繁殖。通过填入生物类名（逗号分隔）来禁止特定生物繁殖后代，可用于限制某些强力生物的数量增长或防止特定生物繁殖带来的性能问题 | string | 无 | ✅ |
| `bAllowUnclaimDinos` | 允许取消认领生物 | boolean | True | ⚠️ |
| `bAllowCustomRecipes` | 允许自定义配方 | boolean | True | ⚠️ |
| `bDisableDinoBreeding` | 禁用生物繁殖 | boolean | False | ⚠️ |
| `bDisableDinoRiding` | 禁用骑乘生物 | boolean | False | ⚠️ |
| `bDisableDinoTaming` | 禁用驯服生物 | boolean | False | ⚠️ |
| `DinoHarvestingDamageMultiplier` | 生物采集伤害倍率 | float | 3.2 | ⚠️ |
| `DinoTurretDamageMultiplier` | 炮塔对生物伤害倍率 | float | 1.0 | ⚠️ |
| `PassiveTameIntervalMultiplier` | 被动驯服请求间隔倍率 | float | 1.0 | ⚠️ |
| `TamedDinoCharacterFoodDrainMultiplier` | 驯服生物食物消耗速度倍率。影响驯服生物的食物消耗速度，较低的值使驯服生物需要更少食物，减少喂食频率和食物管理压力。设为 0.5 则食物消耗速度减半 | float | 1.0 | ⚠️ |
| `TamedDinoTorporDrainMultiplier` | 驯服生物昏迷值消耗速度倍率。影响驯服生物从昏迷状态恢复的速度，较低的值使生物昏迷时间更长，适合需要长时间麻醉的场景 | float | 1.0 | ⚠️ |
| `WildDinoTorporDrainMultiplier` | 野生生物昏迷值消耗速度倍率。影响野生生物从昏迷状态恢复的速度，较低的值使野生生物昏迷时间更长，便于玩家进行驯服操作 | float | 1.0 | ⚠️ |
| `AdjustableMutagenSpawnDelayMultiplier` | Mutagen 刷新延迟倍率 | float | 1.0 | ⚠️ |
| `PreventDinoTameClassNames` | 阻止指定生物驯服（通过类名） | string | 无 | ⚠️ |

### 属性与等级设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `bAllowFlyerSpeedLeveling` | 允许飞行生物升级移动速度。默认情况下飞行生物的移动速度属性不可通过升级点数提升，启用后玩家可以像其他属性一样分配升级点数到速度上，显著增强飞行生物的机动性 | boolean | False | ✅ |
| `bAllowSpeedLeveling` | 允许玩家和非飞行生物升级移动速度。默认情况下移动速度属性不可升级，启用后玩家和陆地生物都可以通过分配升级点数来提升移动速度，改变游戏的战斗和探索平衡 | boolean | False | ✅ |
| `bAllowUnlimitedRespecs` | 允许无限次使用洗点药水（Mindwipe Tonic）。默认每个角色在一定等级后只能使用一次洗点药水重置属性和印痕点数，启用后可无限制地反复洗点，方便玩家根据不同场景灵活调整角色配置 | boolean | False | ✅ |
| `PerLevelStatsMultiplier_Player[<integer>]` | 玩家每级属性倍率（索引 0-11）。控制玩家每次升级时各属性的成长幅度，索引对应不同属性（0=生命值、1=耐力、7=负重等）。值为 2.0 时该属性每级获得双倍加点效果，可精细调整角色成长曲线 | float | 无 | ✅ |
| `PerLevelStatsMultiplier_DinoTamed<_type>[<integer>]` | 驯服生物每级属性倍率 | float | 无 | ⚠️ |
| `PerLevelStatsMultiplier_DinoWild[<integer>]` | 野生生物每级属性倍率 | float | 无 | ⚠️ |
| `PlayerBaseStatMultipliers[<attribute>]` | 玩家基础属性倍率 | float | 无 | ⚠️ |
| `PlayerHarvestingDamageMultiplier` | 玩家采集伤害倍率 | float | 1.0 | ⚠️ |
| `OverrideMaxExperiencePointsDino` | 覆盖生物最大经验值上限 | integer | 无 | ⚠️ |
| `OverrideMaxExperiencePointsPlayer` | 覆盖玩家最大经验值上限 | integer | 无 | ⚠️ |
| `MutagenLevelBoost[<Stat_ID>]` | Mutagen 对驯服生物的等级提升 | integer | 无 | ⚠️ |
| `MutagenLevelBoost_Bred[<Stat_ID>]` | Mutagen 对繁殖生物的等级提升 | integer | 无 | ⚠️ |

### 经验值设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `CraftXPMultiplier` | 制造经验倍率。影响通过制作物品获得的经验值，值为 2.0 时制造获得双倍经验。适合需要加速升级的服务器，鼓励玩家通过制造来提升等级 | float | 1.0 | ✅ |
| `GenericXPMultiplier` | 通用经验倍率。影响通过探索、发现地点、解锁印痕等通用途径获得的经验值。作为基础经验倍率，调整此值可整体加速或减慢玩家的升级进度 | float | 1.0 | ✅ |
| `HarvestXPMultiplier` | 采集经验倍率。影响通过采集资源（砍树、挖矿、采集浆果等）获得的经验值。增大此值可让玩家在日常采集资源的同时获得更多经验，适合资源采集密集的服务器 | float | 1.0 | ✅ |
| `KillXPMultiplier` | 击杀经验倍率。影响通过击杀野生生物获得的经验值，值为 2.0 时击杀获得双倍经验。增大此值可鼓励玩家参与战斗，加快通过战斗升级的速度 | float | 1.0 | ✅ |
| `SpecialXPMultiplier` | 特殊事件经验倍率。影响通过特殊活动（如节日事件、成就完成等）获得的经验值。可在活动期间临时增大此值以激励玩家参与特殊事件 | float | 1.0 | ✅ |
| `CraftingSkillBonusMultiplier` | 制造技能加成倍率。影响制造技能属性对制作物品品质的加成效果，值越大高制造技能角色制作出高品质物品的概率越高。调整此值可改变制造专精角色的优势程度 | float | 1.0 | ✅ |

### 建筑与资源设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `bDisableStructurePlacementCollision` | 允许建筑与地形重叠放置。默认情况下建筑不能与地形或其他物体碰撞，启用后建筑可以穿透地形放置，适合在不平坦的地形上建造基地，但可能导致建筑嵌入山体或地面 | boolean | False | ✅ |
| `bIgnoreStructuresPreventionVolumes` | 允许在禁止建造区域内建造。默认某些区域（如矿洞入口、重要资源点）被标记为禁止建造区域，启用后可无视这些限制在任意位置建造，但可能影响其他玩家的游戏体验 | boolean | False | ✅ |
| `ResourceNoReplenishRadiusPlayers` | 玩家周围资源禁生区域倍率。控制玩家附近禁止资源刷新的范围大小，值越大玩家周围越大的区域内不会刷新资源。增大此值可防止资源在玩家附近刷新造成干扰 | float | 1.0 | ✅ |
| `ResourceNoReplenishRadiusStructures` | 建筑周围资源禁生区域倍率。控制建筑周围禁止资源刷新的范围大小，值越大建筑周围越大的区域内不刷新资源。用于防止树木和石头在基地内部刷新穿模 | float | 1.0 | ✅ |
| `CropDecaySpeedMultiplier` | 作物腐烂速度倍率。影响种植在耕地上的作物腐烂速度，值越大腐烂越快。降低此值可延长作物存活时间，减少玩家需要频繁浇水施肥的负担 | float | 1.0 | ✅ |
| `CropGrowthSpeedMultiplier` | 作物生长速度倍率。影响作物从种子到成熟的速度，值越大生长越快。增大此值可加速作物成熟，缩短从播种到收获的等待时间，适合需要大量农业产出的服务器 | float | 1.0 | ✅ |
| `LimitGeneratorsNum` | 区域内发电机数量限制。限制在指定范围内可以放置的发电机数量，防止玩家密集放置大量发电机导致服务器性能下降和游戏平衡问题 | integer | 3 | ✅ |
| `LimitGeneratorsRange` | 发电机限制区域范围（UE 单位）。配合 `LimitGeneratorsNum` 使用，设置发电机数量限制的检测范围。在此范围内的发电机数量不能超过设定上限 | integer | 15000 | ✅ |
| `MaxFallSpeedMultiplier` | 开始受到坠落伤害的坠落速度倍率。控制角色从高处坠落时开始计算伤害的速度阈值，值越大需要更高的坠落速度才会受伤。增大此值可减少坠落伤害的发生频率 | float | 1.0 | ✅ |
| `LimitNonPlayerDroppedItemsCount` | 非玩家掉落物品数量限制 | integer | 0 | ⚠️ |
| `LimitNonPlayerDroppedItemsRange` | 非玩家掉落物品范围限制 | integer | 0 | ⚠️ |
| `FastDecayInterval` | 快速衰减间隔（秒） | integer | 43200 | ⚠️ |
| `FishingLootQualityMultiplier` | 钓鱼战利品质量倍率。影响钓鱼获得物品的品质，值越大品质越高。有效值范围 1.0 到 5.0 | float | 1.0 | ⚠️ |
| `FuelConsumptionIntervalMultiplier` | 燃料消耗间隔倍率。影响发电机等设备消耗燃料的速度，较低的值使燃料消耗更慢，延长设备运行时间 | float | 1.0 | ⚠️ |
| `GlobalCorpseDecompositionTimeMultiplier` | 全局尸体分解时间倍率。影响玩家和生物尸体消失所需的时间，值越大尸体存在越久。增大此值可给予玩家更多时间找回死亡掉落的物品 | float | 1.0 | ⚠️ |
| `GlobalPoweredBatteryDurabilityDecreasePerSecond` | 全局电池耐久消耗速率 | float | 3.0 | ⚠️ |
| `StructureDamageRepairCooldown` | 建筑伤害修复冷却时间（秒） | integer | 180 | ⚠️ |
| `SupplyCrateLootQualityMultiplier` | 补给箱战利品质量倍率。影响补给箱中物品的品质，值越大品质越高。品质也受难度偏移影响 | float | 1.0 | ⚠️ |
| `PvPZoneStructureDamageMultiplier` | PvP 区域建筑伤害倍率。影响洞穴内建筑受到的伤害，较低的值使建筑更难被破坏。设为 1.0 则洞穴内外建筑受到相同伤害 | float | 6.0 | ⚠️ |
| `IncreasePvPRespawnIntervalBaseAmount` | PvP 重生间隔基础增加量（秒） | float | 60.0 | ⚠️ |
| `IncreasePvPRespawnIntervalCheckPeriod` | PvP 重生间隔检查周期（秒） | float | 300.0 | ⚠️ |
| `IncreasePvPRespawnIntervalMultiplier` | PvP 重生间隔倍率 | float | 2.0 | ⚠️ |
| `bIncreasePvPRespawnInterval` | 启用 PvP 重生间隔增加 | boolean | True | ⚠️ |
| `PreventOfflinePvPConnectionInvincibleInterval` | 登录后无敌时间（秒） | float | 5.0 | ⚠️ |
| `UseCorpseLifeSpanMultiplier` | 尸体和掉落箱寿命倍率 | float | 1.0 | ⚠️ |
| `BaseTemperatureMultiplier` | 基础温度倍率。指定地图基础温度的缩放因子：较低的值使环境更冷，较高的值使环境更热。可用于调整服务器的整体温度难度 | float | 1.0 | ⚠️ |
| `bPassiveDefensesDamageRiderlessDinos` | 被动防御伤害无骑手生物 | boolean | False | ⚠️ |
| `TribeSlotReuseCooldown` | 部落槽位重用冷却时间（秒） | float | 0.0 | ⚠️ |
| `MaxAlliancesPerTribe` | 每部落最大联盟数 | integer | 无 | ⚠️ |
| `MaxTribesPerAlliance` | 每联盟最大部落数 | integer | 无 | ⚠️ |
| `MaxNumberOfPlayersInTribe` | 每部落最大玩家数（0 为无限制） | integer | 0 | ⚠️ |
| `MaxTribeLogs` | 部落日志最大条目数 | integer | 400 | ⚠️ |
| `bUseCorpseLocator` | 显示死亡位置绿光束 | boolean | True | ⚠️ |
| `bUseTameLimitForStructuresOnly` | 仅对有建筑的平台应用驯服限制 | boolean | False | ⚠️ |
| `bFlyerPlatformAllowUnalignedDinoBasing` | 飞行平台允许非盟友生物停留 | boolean | False | ⚠️ |
| `bAutoPvETimer` | 启用 PvE 定时器 | boolean | False | ⚠️ |
| `bAutoPvEUseSystemTime` | 使用系统时间进行 PvE 定时 | boolean | False | ⚠️ |
| `AutoPvEStartTimeSeconds` | PvE 模式开始时间（秒） | float | 0.0 | ⚠️ |
| `AutoPvEStopTimeSeconds` | PvE 模式结束时间（秒） | float | 0.0 | ⚠️ |
| `bAutoUnlockAllEngrams` | 自动解锁所有印痕 | boolean | False | ⚠️ |
| `bDisableLootCrates` | 禁用战利品箱刷新 | boolean | False | ⚠️ |
| `bPvEAllowTribeWar` | PvE 模式下允许部落战争 | boolean | True | ⚠️ |
| `bPvEAllowTribeWarCancel` | 允许取消部落战争 | boolean | False | ⚠️ |
| `bOnlyAllowSpecifiedEngrams` | 仅允许指定印痕 | boolean | False | ⚠️ |
| `DinoClassDamageMultipliers` | 全局覆盖野生生物伤害 | struct | 无 | ⚠️ |
| `DinoClassResistanceMultipliers` | 全局覆盖野生生物抗性 | struct | 无 | ⚠️ |
| `DinoSpawnWeightMultipliers` | 全局覆盖生物刷新权重 | struct | 无 | ⚠️ |
| `NPCReplacements` | 全局替换指定生物 | struct | 无 | ⚠️ |
| `ConfigOverrideNPCSpawnEntriesContainer` | 覆盖刷新区域中的生物 | struct | 无 | ⚠️ |
| `ConfigSubtractNPCSpawnEntriesContainer` | 从刷新区域移除生物 | struct | 无 | ⚠️ |
| `ConfigOverrideItemCraftingCosts` | 覆盖物品制造成本 | struct | 无 | ⚠️ |
| `ConfigOverrideItemMaxQuantity` | 覆盖物品堆叠上限 | struct | 无 | ⚠️ |
| `ConfigOverrideSupplyCrateItems` | 覆盖补给箱物品 | struct | 无 | ⚠️ |
| `TamedDinoClassDamageMultipliers` | 全局覆盖驯服生物伤害 | struct | 无 | ⚠️ |
| `TamedDinoClassResistanceMultipliers` | 全局覆盖驯服生物抗性 | struct | 无 | ⚠️ |
| `TamedDinoClassSpeedMultipliers` | 全局覆盖驯服生物速度 | struct | 无 | ⚠️ |
| `TamedDinoClassStaminaMultipliers` | 全局覆盖驯服生物耐力 | struct | 无 | ⚠️ |
| `ItemStatClamps[<attribute>]` | 全局限制物品属性 | struct | 无 | ⚠️ |
| `OverrideEngramEntries` | 按索引配置印痕状态 | struct | 无 | ⚠️ |
| `OverridePlayerLevelEngramPoints` | 覆盖每级印痕点数 | integer | 无 | ⚠️ |
| `EngramEntryAutoUnlocks` | 自动解锁指定印痕 | struct | 无 | ⚠️ |

### 制造与配方设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `CustomRecipeEffectivenessMultiplier` | 自定义配方效果倍率。影响玩家通过烹饪锅制作的自定义配方（如炖菜、药汤等）的效果强度，值越大配方产出的物品效果越强。适合需要强化自定义烹饪玩法的服务器 | float | 1.0 | ✅ |
| `CustomRecipeSkillMultiplier` | 自定义配方制造技能加成倍率。影响制造技能属性对自定义配方品质的加成效果，值越大高制造技能角色制作出高品质配方的概率越高，鼓励玩家专精制造路线 | float | 1.0 | ✅ |
| `bDisableWirelessCrafting` | 禁用 Tek 专用存储的无线制造功能。默认 Tek 专用存储可以无线提供制造材料，启用此选项后所有无线制造功能被禁用，玩家需要将材料放入背包才能制造 | boolean | False | ✅ |
| `bDisableWirelessCraftingForDinos` | 禁用在生物背包中使用无线制造。单独控制生物是否能使用 Tek 专用存储的无线制造材料，禁用后生物身上的物品不能远程调用 Tek 存储中的资源进行制造 | boolean | False | ✅ |
| `bDisableWirelessCraftingForPlayers` | 禁用在玩家背包中使用无线制造。单独控制玩家是否能使用 Tek 专用存储的无线制造材料，禁用后玩家必须将材料手动放入背包才能进行制造操作 | boolean | False | ✅ |
| `bDisableWirelessCraftingForStructures` | 禁用在建筑背包中使用无线制造。单独控制建筑（如工作台、熔炉等）是否能使用 Tek 专用存储的无线制造材料，禁用后建筑需要直接存放材料才能运作 | boolean | False | ✅ |
| `WirelessCraftingRangeOverride` | 无线制造范围覆盖（UE 单位）。设置 Tek 专用存储无线制造功能的有效距离，玩家在此范围内才能远程调用存储中的材料。增大此值可扩大无线制造的便利范围 | integer | 3000 | ✅ |

### 其他设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `bDisableFriendlyFire` | 禁用友军伤害。启用后同部落成员之间无法互相造成伤害，防止队友间的误伤，适合 PvE 和合作型服务器，减少因意外攻击队友导致的损失 | boolean | False | ✅ |
| `bPvEDisableFriendlyFire` | PvE 模式下禁用友军伤害。仅在 PvE 模式下生效，阻止同部落成员之间的伤害。与 `bDisableFriendlyFire` 不同，此选项仅影响 PvE 模式，PvP 模式下仍保留友军伤害 | boolean | False | ✅ |
| `bDisablePhotoMode` | 禁用拍照模式。启用后玩家无法使用游戏内置的拍照模式功能，可减少因拍照模式带来的潜在作弊风险（如透视查看周围环境），适合竞技型服务器 | boolean | False | ✅ |
| `bShowCreativeMode` | 在暂停菜单中显示创意模式按钮。启用后拥有管理员权限的玩家可在暂停菜单中切换创意模式，创意模式提供无限资源和无碰撞建造，适合建筑创作和服务器调试 | boolean | False | ✅ |
| `bUseDinoLevelUpAnimations` | 生物升级时播放动画。启用后生物每次升级会播放短暂的升级动画效果，禁用可跳过动画加快操作节奏，适合需要快速批量升级生物的服务器 | boolean | True | ✅ |
| `bUseSingleplayerSettings` | 启用单人游戏平衡设置。启用后自动应用一系列单人游戏优化参数（如更高的驯服速度、更短的幼崽成长时间等），适合单人或小型合作游戏，大幅降低单人游玩的时间成本 | boolean | False | ✅ |
| `HairGrowthSpeedMultiplier` | 头发生长速度倍率。控制角色头发和胡须的生长速度，值越大生长越快。设为 0 禁用头发生长，玩家需要通过理发来改变发型。ASA 中默认禁用头发生长 | float | 0 (ASA) | ✅ |
| `GlobalItemDecompositionTimeMultiplier` | 掉落物品分解时间倍率。影响地上掉落物品（如死亡掉落的背包）消失所需的时间，值越大物品存在越久。增大此值可给予玩家更多时间找回死亡掉落的物品 | float | 1.0 | ✅ |
| `GlobalSpoilingTimeMultiplier` | 全局腐烂时间倍率。影响所有可腐烂物品（如生肉、浆果等）的腐烂速度，值越大腐烂越慢。增大此值可延长食物保质期，减少食物管理的压力 | float | 1.0 | ✅ |
| `DestroyTamesOverLevelClamp` | 超过指定等级的驯服生物在服务器启动时自动删除。设为 0 禁用此功能。当服务器更新或模组变更导致生物等级异常时，可通过此设置自动清理超高等级生物，维护游戏平衡 | integer | 0 | ✅ |
| `PhotoModeRangeLimit` | 拍照模式相机与玩家的最大距离（UE 单位）。限制拍照模式下相机可以离开角色的最远距离，防止玩家利用拍照模式进行远距离侦察。较小的值限制更严格 | integer | 3000 | ✅ |
| `IgnorePVPMountedWeaponryRestrictions` | 忽略 PvP 模式下的骑乘武器限制。默认 PvP 模式下某些武器在骑乘状态时受到限制，启用后可移除这些限制，允许玩家在骑乘时使用所有武器，增加战斗多样性 | boolean | False | ✅ |
| `TribeTowerBonusMultiplier` | 部落塔加成倍率。影响部落塔（Tribe Tower）提供的增益效果强度，值越大加成越强。部落塔是 ASA 中的部落协作建筑，此倍率可调整其对部落成员的属性加成幅度 | float | 2.0 | ✅ |
| `BaseHexagonRewardMultiplier` | 任务六角币奖励倍率。影响完成任务（如创世纪任务）获得的基础六角币数量，值为 2.0 时获得双倍六角币。适合加速六角币积累，让玩家更快购买商店物品 | float | 1.0 | ✅ |
| `HexagonCostMultiplier` | 六角币商店物品价格倍率。影响六角币商店中所有物品的购买价格，值越大价格越高。降低此值可使商店物品更便宜，提升六角币的购买力 | float | 1.0 | ✅ |
| `ExcludeItemIndices` | 从补给箱中排除指定物品 ID。通过填入物品索引 ID 来阻止特定物品出现在补给箱战利品池中，可用于移除不想要的物品或调整战利品组成 | integer | 无 | ✅ |
| `HarvestResourceItemAmountClassMultipliers` | 按资源类型设置采集产量倍率。针对特定资源类名设置独立的采集产量倍率，可精细控制每种资源的采集效率，而不影响其他资源。格式为类名和倍率的配对列表 | struct | 无 | ✅ |
| `LevelExperienceRampOverrides` | 配置玩家和生物的等级上限及每级所需经验值。第一次出现配置玩家等级曲线，第二次出现配置驯服生物等级曲线。可自定义最高等级和升级所需经验，实现自定义等级体系 | struct | 无 | ✅ |
| `OverrideNamedEngramEntries` | 按名称配置印痕状态和需求。通过印痕类名来精确控制每个印痕的可见性、学习成本、等级需求和前置条件，比按索引配置更直观且不易因游戏更新而失效 | struct | 无 | ✅ |
| `ConfigAddNPCSpawnEntriesContainer` | 在指定刷新区域添加新的生物刷新条目。可在现有的生物刷新组中注入自定义的生物，指定刷新权重和数量限制，用于在特定区域增加新生物种类 | struct | 无 | ✅ |
| `CheatTeleportLocations` | 创建命名传送点。定义一组可命名的传送坐标点，管理员可通过命令快速传送到这些预设位置，方便服务器管理和巡查各区域 | struct | 无 | ✅ |
| `ValgueroMemorialEntries` | Valguero 纪念碑名称列表（分号分隔）。用于自定义 Valguero 地图上纪念碑显示的名称，多个名称用分号分隔。通常用于纪念对服务器有贡献的玩家或特殊事件 | string | 无 | ✅ |
| `bDisableHexagonStore` | 禁用六角币商店 | boolean | False | ⚠️ |
| `bDisableDefaultMapItemSets` | 禁用默认地图物品套装 | boolean | False | ⚠️ |
| `bDisableGenesisMissions` | 禁用创世纪任务 | boolean | False | ⚠️ |
| `bDisableWorldBuffs` | 禁用世界增益效果 | boolean | False | ⚠️ |
| `bEnableWorldBuffScaling` | 启用世界增益效果缩放 | boolean | False | ⚠️ |
| `bGenesisUseStructuresPreventionVolumes` | 创世纪中启用建筑禁止区域 | boolean | False | ⚠️ |
| `bHexStoreAllowOnlyEngramTradeOption` | 六角币商店仅允许印痕交易 | boolean | False | ⚠️ |
| `WorldBuffScalingEfficacy` | 世界增益效果缩放效率 | float | 1.0 | ⚠️ |

### 3.1 复杂配置项详细格式

以下配置项需要特殊格式，每个条目必须在配置文件中写在一行内（示例中的换行仅用于可读性）。

#### OverrideEngramEntries / OverrideNamedEngramEntries

按索引或名称配置印痕状态和需求。

```
OverrideEngramEntries=(EngramIndex=<index>[,EngramHidden=<hidden>][,EngramPointsCost=<cost>][,EngramLevelRequirement=<level>][,RemoveEngramPreReq=<remove_prereq>])

OverrideNamedEngramEntries=(EngramClassName="<class_name>"[,EngramHidden=<hidden>][,EngramPointsCost=<cost>][,EngramLevelRequirement=<level>][,RemoveEngramPreReq=<remove_prereq>])
```

**参数说明：**
- `index` (integer) - 印痕索引
- `class_name` (string) - 印痕类名
- `hidden` (boolean) - 是否隐藏印痕
- `cost` (integer) - 学习所需印痕点数
- `level` (integer) - 学习所需最低等级
- `remove_prereq` (boolean) - 是否移除前置印痕需求

**示例：**
```ini
OverrideEngramEntries=(EngramIndex=0,EngramHidden=False)
OverrideEngramEntries=(EngramIndex=1,EngramHidden=False,EngramPointsCost=3,EngramLevelRequirement=3,RemoveEngramPreReq=True)
OverrideNamedEngramEntries=(EngramClassName="EngramEntry_Campfire_C",EngramHidden=False)
```

#### EngramEntryAutoUnlocks

自动解锁指定印痕。

```
EngramEntryAutoUnlocks=(EngramClassName="<string>",LevelToAutoUnlock=<integer>)
```

**示例：**
```ini
EngramEntryAutoUnlocks=(EngramClassName="EngramEntry_TekTeleporter_C",LevelToAutoUnlock=0)
```

#### ConfigAddNPCSpawnEntriesContainer

在刷新区域添加指定生物。

```
ConfigAddNPCSpawnEntriesContainer=(NPCSpawnEntriesContainerClassString="<spawn_class>",NPCSpawnEntries=((AnEntryName="<spawn_name>",EntryWeight=<factor>,NPCsToSpawnStrings=("<entity_id>"))),NPCSpawnLimits=((NPCClassString="<entity_id>",MaxPercentageOfDesiredNumToAllow=<percentage>)))
```

**参数说明：**
- `spawn_class` (string) - 刷新组容器类名
- `spawn_name` (string) - 刷新名称
- `factor` (float) - 权重因子
- `entity_id` (string) - 生物实体 ID
- `percentage` (float) - 最大允许百分比

**示例：**
```ini
ConfigAddNPCSpawnEntriesContainer=(NPCSpawnEntriesContainerClassString="DinoSpawnEntriesBeach_C",NPCSpawnEntries=((AnEntryName="GigaSpawner",EntryWeight=1.0,NPCsToSpawnStrings=("Gigant_Character_BP_C"))),NPCSpawnLimits=((NPCClassString="Gigant_Character_BP_C",MaxPercentageOfDesiredNumToAllow=0.01)))
```

#### ConfigSubtractNPCSpawnEntriesContainer

从刷新区域移除指定生物。

```
ConfigSubtractNPCSpawnEntriesContainer=(NPCSpawnEntriesContainerClassString="<spawn_class>",NPCSpawnEntries=((AnEntryName="<spawn_name>",NPCsToSpawnStrings=("<entity_id>"))))
```

#### ConfigOverrideNPCSpawnEntriesContainer

覆盖刷新区域中的生物。

```
ConfigOverrideNPCSpawnEntriesContainer=(NPCSpawnEntriesContainerClassString="<spawn_class>",NPCSpawnEntries=((AnEntryName="<spawn_name>",EntryWeight=<factor>,NPCsToSpawnStrings=("<entity_id>"))),NPCSpawnLimits=((NPCClassString="<entity_id>",MaxPercentageOfDesiredNumToAllow=<percentage>)))
```

#### DinoSpawnWeightMultipliers

自定义生物刷新权重。

```
DinoSpawnWeightMultipliers=(DinoNameTag=<tag>[,SpawnWeightMultiplier=<factor>][,OverrideSpawnLimitPercentage=<override>][,SpawnLimitPercentage=<limit>])
```

**参数说明：**
- `tag` (string) - 生物类型标签
- `factor` (float) - 权重因子
- `override` (boolean) - 是否使用指定的 SpawnLimitPercentage
- `limit` (float) - 最大刷新百分比（0.25 = 25%）

**示例：**
```ini
DinoSpawnWeightMultipliers=(DinoNameTag=Bronto,SpawnWeightMultiplier=10.0,OverrideSpawnLimitPercentage=True,SpawnLimitPercentage=0.5)
```

#### NPCReplacements

全局替换指定生物。

```
NPCReplacements=(FromClassName="<classname>",ToClassName="<classname>")
```

**示例：**
```ini
NPCReplacements=(FromClassName="MegaRaptor_Character_BP_C",ToClassName="Dodo_Character_BP_C")
```

**DynamicConfig 语法（注意额外括号）：**
```ini
NPCReplacements=((FromClassName="MegaRaptor_Character_BP_C",ToClassName="Dodo_Character_BP_C"))
```

#### DinoClassDamageMultipliers / TamedDinoClassDamageMultipliers

按类名覆盖特定生物的伤害倍率。`DinoClassDamageMultipliers` 用于野生生物，`TamedDinoClassDamageMultipliers` 用于驯服生物。值越大伤害越高。可以指定多个条目，但每个类名只能出现一次。生物类名可在 Creature IDs 页面找到。

```
DinoClassDamageMultipliers=(ClassName="<string>",Multiplier=<float>)
TamedDinoClassDamageMultipliers=(ClassName="<string>",Multiplier=<float>)
```

**参数说明：**
- `ClassName` (string) - 生物类名
- `Multiplier` (float) - 伤害倍率，默认 1.0

**示例：**
```ini
DinoClassDamageMultipliers=(ClassName="MegaRex_Character_BP_C",Multiplier=0.1)
TamedDinoClassDamageMultipliers=(ClassName="Rex_Character_BP_C",Multiplier=10.0)
```

#### DinoClassResistanceMultipliers / TamedDinoClassResistanceMultipliers

按类名覆盖特定生物的抗性倍率。`DinoClassResistanceMultipliers` 用于野生生物，`TamedDinoClassResistanceMultipliers` 用于驯服生物。值越大受到的伤害越少。可以指定多个条目，但每个类名只能出现一次。生物类名可在 Creature IDs 页面找到。

```
DinoClassResistanceMultipliers=(ClassName="<string>",Multiplier=<float>)
TamedDinoClassResistanceMultipliers=(ClassName="<string>",Multiplier=<float>)
```

**参数说明：**
- `ClassName` (string) - 生物类名
- `Multiplier` (float) - 抗性倍率，默认 1.0

**示例：**
```ini
DinoClassResistanceMultipliers=(ClassName="MegaRex_Character_BP_C",Multiplier=0.1)
TamedDinoClassResistanceMultipliers=(ClassName="Rex_Character_BP_C",Multiplier=10.0)
```

#### TamedDinoClassSpeedMultipliers

按类名覆盖驯服生物的基础速度倍率。值越大速度越快。可以指定多个条目，但每个类名只能出现一次。生物类名可在 Creature IDs 页面找到。

```
TamedDinoClassSpeedMultipliers=(ClassName="<string>",Multiplier=<float>)
```

**参数说明：**
- `ClassName` (string) - 生物类名
- `Multiplier` (float) - 速度倍率，默认 1.0

**示例：**
```ini
TamedDinoClassSpeedMultipliers=(ClassName="Argent_Character_BP_C",Multiplier=2.0)
```

#### TamedDinoClassStaminaMultipliers

按类名覆盖驯服生物的耐力倍率。值越大耐力越高。可以指定多个条目，但每个类名只能出现一次。生物类名可在 Creature IDs 页面找到。

```
TamedDinoClassStaminaMultipliers=(ClassName="<string>",Multiplier=<float>)
```

**参数说明：**
- `ClassName` (string) - 生物类名
- `Multiplier` (float) - 耐力倍率，默认 1.0

**示例：**
```ini
TamedDinoClassStaminaMultipliers=(ClassName="Ptero_Character_BP_C",Multiplier=5.0)
```

#### ConfigOverrideItemCraftingCosts

覆盖物品制造成本。

```
ConfigOverrideItemCraftingCosts=(ItemClassString="<string>",BaseCraftingResourceRequirements=((ResourceItemTypeString="<string>",BaseResourceRequirement=<float>,bCraftingRequireExactResourceType=<boolean>)))
```

**示例：**
```ini
ConfigOverrideItemCraftingCosts=(ItemClassString="PrimalItem_WeaponStoneHatchet_C",BaseCraftingResourceRequirements=((ResourceItemTypeString="PrimalItemResource_Thatch_C",BaseResourceRequirement=1.0,bCraftingRequireExactResourceType=False),(ResourceItemTypeString="PrimalItemAmmo_ArrowStone_C",BaseResourceRequirement=2.0,bCraftingRequireExactResourceType=False)))
```

#### ConfigOverrideItemMaxQuantity

覆盖物品堆叠上限。

```
ConfigOverrideItemMaxQuantity=(ItemClassString="<string>",Quantity=(MaxItemQuantity=<integer>,bIgnoreMultiplier=<boolean>))
```

**参数说明：**
- `MaxItemQuantity` (integer) - 新堆叠上限
- `bIgnoreMultiplier` (boolean) - False 时实际堆叠 = ItemStackSizeMultiplier * MaxItemQuantity；True 时直接使用 MaxItemQuantity

#### ConfigOverrideSupplyCrateItems

覆盖补给箱物品。允许手动覆盖战利品箱中的物品。每个战利品箱可以有一个或多个物品集，每个物品集可以有一个或多个物品条目。

```
ConfigOverrideSupplyCrateItems=(
  SupplyCrateClassString="<string>",
  MinItemSets=<integer>,
  MaxItemSets=<integer>,
  NumItemSetsPower=<float>,
  bSetsRandomWithoutReplacement=<boolean>
  [,bAppendItemSets=<boolean>]
  [,bAppendPreventIncreasingMinMaxItemSets=<boolean>],
  ItemSets=(
    [SetName="<string>",]
    MinNumItems=<integer>,
    MaxNumItems=<integer>,
    NumItemsPower=<float>,
    SetWeight=<float>,
    bItemsRandomWithoutReplacement=<boolean>,
    ItemEntries=(
      [ItemEntryName="<string>",]
      EntryWeight=<float>,
      ItemClassStrings=("<string>"[,...n]),
      ItemsWeights=(<float>[,...n]),
      MinQuantity=<float>,
      MaxQuantity=<float>,
      MinQuality=<float>,
      MaxQuality=<float>,
      bForceBlueprint=<boolean>,
      ChanceToBeBlueprintOverride=<float>
      [,bForcePreventGrinding=<boolean>]
      [,ItemStatClampsMultiplier=<float>]
    )
  )[,...m]
)
```

**箱子选项参数：**
- `SupplyCrateClassString` (string) - 要覆盖的战利品箱类名
- `MinItemSets` (integer) - 最小物品集数量
- `MaxItemSets` (integer) - 最大物品集数量
- `NumItemSetsPower` (float) - 所有物品集的品质，默认 1.0
- `bSetsRandomWithoutReplacement` (boolean) - True 时确保物品不会重复出现
- `bAppendItemSets` (boolean) - True 时追加物品集而非完全替换，默认 False
- `bAppendPreventIncreasingMinMaxItemSets` (boolean) - True 时动态增加掉落物品数量（需 bAppendItemSets=True），默认 False

**物品集选项参数：**
- `SetName` (string) - 物品集名称（可选）
- `MinNumItems` (integer) - 此集中最小物品数量
- `MaxNumItems` (integer) - 此集中最大物品数量
- `NumItemsPower` (float) - 此集的品质倍率
- `SetWeight` (float) - 此集的权重因子，默认 1.0
- `bItemsRandomWithoutReplacement` (boolean) - True 时确保此集中物品不会重复

**物品条目选项参数：**
- `ItemEntryName` (string) - 物品条目名称（可选）
- `EntryWeight` (float) - 此条目的权重因子，默认 1.0
- `ItemClassStrings` (string) - 物品类名列表（逗号分隔）
- `ItemsWeights` (float) - 各物品的权重列表
- `MinQuantity` (float) - 最小数量
- `MaxQuantity` (float) - 最大数量
- `MinQuality` (float) - 最小品质
- `MaxQuality` (float) - 最大品质
- `bForceBlueprint` (boolean) - 强制为蓝图
- `ChanceToBeBlueprintOverride` (float) - 蓝图覆盖概率
- `bForcePreventGrinding` (boolean) - 强制防止分解（可选）
- `ItemStatClampsMultiplier` (float) - 物品属性限制倍率（可选）

**注意：** 每个条目必须在配置文件中写在一行内。自 273.7 补丁后，SupplyCrateClassString 支持使用简写形式。

#### HarvestResourceItemAmountClassMultipliers

按资源类型设置采集产量倍率。与全局设置 `HarvestAmountMultiplier` 类似，但仅对指定的资源类型生效。可以添加多行来分别设置不同资源（如木材、石头等）的采集倍率。值越大每次采集获得的资源越多。

```
HarvestResourceItemAmountClassMultipliers=(ClassName="<string>",Multiplier=<float>)
```

**参数说明：**
- `ClassName` (string) - 资源类名，可在 Item IDs 页面找到
- `Multiplier` (float) - 采集倍率，默认 1.0

**示例（采集茅草时获得 2 倍产量）：**
```ini
HarvestResourceItemAmountClassMultipliers=(ClassName="PrimalItemResource_Thatch_C",Multiplier=2.0)
```

**常见资源类名：**
- `PrimalItemResource_Thatch_C` - 茅草
- `PrimalItemResource_Wood_C` - 木材
- `PrimalItemResource_Stone_C` - 石头
- `PrimalItemResource_Metal_C` - 金属
- `PrimalItemResource_Fiber_C` - 纤维
- `PrimalItemResource_Hide_C` - 兽皮

#### ItemStatClamps

全局限制物品属性。需要命令行参数 `ClampItemStats=true`。

```
ItemStatClamps[<attribute>]=<value>
```

**属性索引：**
- 0: 通用品质
- 1: 护甲
- 2: 最大耐久
- 3: 武器伤害百分比
- 4: 武器弹夹弹药
- 5: 低温抗性
- 6: 重量
- 7: 高温抗性

**示例（官方服务器值）：**
```ini
ItemStatClamps[1]=19800
ItemStatClamps[3]=19800
```

#### PerLevelStatsMultiplier

每级属性倍率配置。允许更改每升一级获得的属性值。

```
PerLevelStatsMultiplier_Player[<attribute>]=<multiplier>
PerLevelStatsMultiplier_DinoTamed[<attribute>]=<multiplier>
PerLevelStatsMultiplier_DinoWild[<attribute>]=<multiplier>
PerLevelStatsMultiplier_DinoTamed_Add[<attribute>]=<multiplier>
PerLevelStatsMultiplier_DinoTamed_Affinity[<attribute>]=<multiplier>
```

**配置类型说明：**
- `PerLevelStatsMultiplier_Player` - 玩家每级属性倍率
- `PerLevelStatsMultiplier_DinoWild` - 野生生物每级属性倍率
- `PerLevelStatsMultiplier_DinoTamed` - 驯服生物每级属性倍率（升级点）
- `PerLevelStatsMultiplier_DinoTamed_Add` - 驯服生物属性倍率（驯服时立即加成）
- `PerLevelStatsMultiplier_DinoTamed_Affinity` - 驯服生物属性倍率（亲和度加成）

**参数说明：**
- `attribute` (integer) - 属性索引，见属性索引表
- `multiplier` (float) - 倍率，默认 1.0。设为 0.01 可几乎禁用属性增长（设为 0 会恢复默认值 1.0）

**默认值表：**

| 属性 | Wild | Tamed | Tamed_Add | Tamed_Affinity |
|------|------|-------|-----------|----------------|
| 0 Health | 1 | 0.2 | 0.14 | 0.44 |
| 1 Stamina | 1 | 1 | 1 | 1 |
| 2 Torpidity | 1 | 1 | 1 | 1 |
| 3 Oxygen | 1 | 1 | 1 | 1 |
| 4 Food | 1 | 1 | 1 | 1 |
| 5 Water | 1 | 1 | 1 | 1 |
| 6 Temperature | 1 | 1 | 1 | 1 |
| 7 Weight | 1 | 1 | 1 | 1 |
| 8 Melee Damage | 1 | 0.17 | 0.14 | 0.44 |
| 9 Movement Speed | 1 | 1 | 1 | 1 |
| 10 Fortitude | 1 | 1 | 1 | 1 |
| 11 Crafting Skill | 1 | 1 | 1 | 1 |

**示例：**
```ini
PerLevelStatsMultiplier_Player[7]=2.0
PerLevelStatsMultiplier_DinoWild[0]=1.0
PerLevelStatsMultiplier_DinoTamed[0]=1.0
PerLevelStatsMultiplier_DinoTamed_Add[0]=1.0
PerLevelStatsMultiplier_DinoTamed_Affinity[0]=1.0
```

#### MutagenLevelBoost / MutagenLevelBoost_Bred

Mutagen 对生物的等级提升。`MutagenLevelBoost` 用于有野生血统的驯服生物，`MutagenLevelBoost_Bred` 用于繁殖生物。

```
MutagenLevelBoost[<Stat_ID>]=<integer>
MutagenLevelBoost_Bred[<Stat_ID>]=<integer>
```

**参数说明：**
- `Stat_ID` (integer) - 属性索引，见属性索引表
- `integer` - 等级点数

**MutagenLevelBoost 默认值：** 5, 5, 0, 0, 0, 0, 0, 5, 5, 0, 0, 0
**MutagenLevelBoost_Bred 默认值：** 1, 1, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0

**示例（将生命和伤害提升加倍，移除耐力和负重提升）：**
```ini
MutagenLevelBoost[0]=10
MutagenLevelBoost[1]=0
MutagenLevelBoost[7]=0
MutagenLevelBoost[8]=10

MutagenLevelBoost_Bred[0]=2
MutagenLevelBoost_Bred[1]=0
MutagenLevelBoost_Bred[7]=0
MutagenLevelBoost_Bred[8]=2
```

#### PlayerBaseStatMultipliers

玩家基础属性倍率。通过乘以默认值来改变玩家的初始属性，即新生成角色的起始属性。

```
PlayerBaseStatMultipliers[<attribute>]=<multiplier>
```

**参数说明：**
- `attribute` (integer) - 属性索引，见属性索引表
- `multiplier` (float) - 倍率，默认值见下表

**默认值表：**

| 属性 | 默认值 | 输出 | 说明 |
|------|--------|------|------|
| 0 Health | 1.0 | 100.0 | 生命值 |
| 1 Stamina | 1.0 | 100.0 | 耐力 |
| 2 Torpidity | 1.0 | 200.0 | 昏迷值（超过 50 仍会昏迷） |
| 3 Oxygen | 1.0 | 100.0 | 氧气 |
| 4 Food | 1.0 | 100.0 | 食物 |
| 5 Water | 1.0 | 100.0 | 水分 |
| 6 Temperature | 0.0 | 0.0 | 温度（未使用属性） |
| 7 Weight | 1.0 | 100.0 | 负重 |
| 8 MeleeDamageMultiplier | 0.0 | 100% | 近战伤害（基础值无法增加） |
| 9 SpeedMultiplier | 0.0 | 100% | 移动速度（基础值无法增加） |
| 10 TemperatureFortitude | 0.0 | 0 | 恒温抗性（基础值无法增加） |
| 11 CraftingSpeedMultiplier | 0.0 | 100% | 制造速度（基础值无法增加） |

**注意：** 属性 6、8、9、10、11 的默认值为 0.0，这些属性的基础值无法通过此配置增加。

#### LevelExperienceRampOverrides

配置玩家和生物等级及经验需求。此指令在配置文件中可以出现两次：
- 第一次出现时，配置玩家等级
- 第二次出现时，配置驯服生物等级

每次使用时，必须列出玩家/生物可以达到的所有等级。每个等级都需要一个 `ExperiencePointsForLevel` 参数。`<n>` 的值必须从 0 开始顺序递增。

```
LevelExperienceRampOverrides=(ExperiencePointsForLevel[0]=<points>,ExperiencePointsForLevel[1]=<points>,...,ExperiencePointsForLevel[n]=<points>)
```

**参数说明：**
- `n` (integer) - 等级编号（从 0 开始）
- `points` (integer) - 达到该等级所需的经验值

**重要注意事项：**
- 最后 100 级用于飞升、迷你恐龙经验、探索者笔记和符文奖励，需要额外添加 100 级
- 每个条目必须在配置文件中写在一行内
- 如果只想修改最高等级，需要重新定义所有等级的经验值

**示例（50 个玩家等级 + 15 个飞升等级）：**
```ini
LevelExperienceRampOverrides=(ExperiencePointsForLevel[0]=1,ExperiencePointsForLevel[1]=5,...,ExperiencePointsForLevel[64]=1000)
```

**示例（35 个驯服生物等级，放在玩家等级配置之后）：**
```ini
LevelExperienceRampOverrides=(ExperiencePointsForLevel[0]=1,ExperiencePointsForLevel[1]=5,...,ExperiencePointsForLevel[34]=1000)
```

---

## 4. DynamicConfig 动态配置

通过 `-UseDynamicConfig` 命令行参数启用。可在不重启服务器的情况下修改。

### 已确认配置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `BabyCuddleIntervalMultiplier` | 同 Game.ini 中的 `BabyCuddleIntervalMultiplier`。动态调整幼崽需要照顾的频率，可在不重启服务器的情况下修改 | float | 1.0 | ✅ |
| `BabyImprintAmountMultiplier` | 同 Game.ini 中的 `BabyImprintAmountMultiplier`。动态调整每次照顾提供的印记百分比，可在运行时微调印记难度 | float | 1.0 | ✅ |
| `BabyMatureSpeedMultiplier` | 同 Game.ini 中的 `BabyMatureSpeedMultiplier`。动态调整幼崽成长速度，适合在活动期间临时加速繁殖体验 | float | 1.0 | ✅ |
| `EggHatchSpeedMultiplier` | 同 Game.ini 中的 `EggHatchSpeedMultiplier`。动态调整受精蛋孵化速度，可在不重启服务器的情况下加速或减慢孵化过程 | float | 1.0 | ✅ |
| `HarvestAmountMultiplier` | 同 GameUserSettings.ini 中的 `HarvestAmountMultiplier`。动态调整采集产量，适合在活动期间临时提高资源产出倍率 | float | 1.0 | ✅ |
| `HexagonRewardMultiplier` | 同 Game.ini 中的六角币奖励倍率。动态调整任务六角币奖励数量，可用于活动期间提升六角币获取速度 | float | 1.0 | ✅ |
| `MatingIntervalMultiplier` | 同 Game.ini 中的 `MatingIntervalMultiplier`。动态调整生物交配间隔，可在活动期间临时缩短繁殖冷却时间 | float | 1.0 | ✅ |
| `XPMultiplier` | 同 Game.ini 中的经验倍率。动态调整全局经验获取倍率，适合在活动期间临时提高经验获取速度 | float | 1.0 | ✅ |
| `DynamicColorset` | 自定义颜色列表（逗号分隔，需 `ActiveEventColors=custom`）。指定自定义的生物颜色配色方案，用于创建独特的服务器视觉风格 | string | 无 | ✅ |
| `DynamicColorsetChanceOverride` | 动态颜色应用概率（0.0-1.0）。控制自定义颜色在生物刷新时的应用概率，值为 0.5 表示 50% 的生物会获得自定义颜色 | float | 0.25 | ✅ |

### 未知状态配置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `BabyFoodConsumptionSpeedMultiplier` | 同 Game.ini 中的 `BabyFoodConsumptionSpeedMultiplier` | float | 1.0 | ⚠️ |
| `bDisableDinoDecayPvE` | 同 GameUserSettings.ini 中的 `DisableDinoDecayPvE` | boolean | False | ⚠️ |
| `bDisableStructureDecayPvE` | 同 GameUserSettings.ini 中的 `DisableStructureDecayPvE` | boolean | False | ⚠️ |
| `bPvPDinoDecay` | 同 GameUserSettings.ini 中的 `PvPDinoDecay` | boolean | False | ⚠️ |
| `bPvPStructureDecay` | 同 GameUserSettings.ini 中的 `PvPStructureDecay` | boolean | False | ⚠️ |
| `bUseAlarmNotifications` | 切换 Web 报警通知 | boolean | False | ⚠️ |
| `CropGrowthSpeedMultiplier` | 同 Game.ini 中的 `CropGrowthSpeedMultiplier` | float | 1.0 | ⚠️ |
| `CustomRecipeEffectivenessMultiplier` | 同 Game.ini 中的 `CustomRecipeEffectivenessMultiplier` | float | 1.0 | ⚠️ |
| `DinoCharacterFoodDrainMultiplier` | 同 GameUserSettings.ini 中的 `DinoCharacterFoodDrainMultiplier` | float | 1.0 | ⚠️ |
| `DisableTimestampVerification` | 禁用时间戳验证 | boolean | False | ⚠️ |
| `DisableWorldBuffs` | 禁用特定世界增益效果（创世纪 Part 2） | string | 无 | ⚠️ |
| `DynamicUndermeshRegions` | 强制动态地下网格区域更新 | string | 无 | ⚠️ |
| `EnableFullDump` | 服务器崩溃时强制完整内存转储 | boolean | False | ⚠️ |
| `EnableWorldBuffScaling` | 同 Game.ini 中的 `bEnableWorldBuffScaling` | boolean | False | ⚠️ |
| `GMaxFlameThrowerServerTicksPerFrame` | 控制火焰喷射器每服务器 tick 的速率 | integer | 5 | ⚠️ |
| `GlobalSpoilingTimeMultiplier` | 同 Game.ini 中的 `GlobalSpoilingTimeMultiplier` | float | 1.0 | ⚠️ |
| `GUseServerNetSpeedCheck` | 同命令行 `-UseServerNetSpeedCheck` | boolean | False | ⚠️ |
| `MatingSpeedMultiplier` | 同 Game.ini 中的 `MatingSpeedMultiplier` | float | 1.0 | ⚠️ |
| `NPCReplacements` | 全局替换指定生物（同 Game.ini） | string | 无 | ⚠️ |
| `PvEDinoDecayPeriodMultiplier` | 同 GameUserSettings.ini 中的 `PvEDinoDecayPeriodMultiplier` | float | 1.0 | ⚠️ |
| `PvEStructureDecayPeriodMultiplier` | 同 GameUserSettings.ini 中的 `PvEStructureDecayPeriodMultiplier` | float | 1.0 | ⚠️ |
| `StructureDamageMultiplier` | 同 GameUserSettings.ini 中的 `StructureDamageMultiplier` | float | 1.0 | ⚠️ |
| `TamingSpeedMultiplier` | 同 GameUserSettings.ini 中的 `TamingSpeedMultiplier` | float | 1.0 | ⚠️ |
| `TributeDinoExpirationSeconds` | 同 GameUserSettings.ini 中的 `TributeDinoExpirationSeconds` | integer | 86400 | ⚠️ |
| `TributeItemExpirationSeconds` | 同 GameUserSettings.ini 中的 `TributeItemExpirationSeconds` | integer | 86400 | ⚠️ |
| `WorldBuffScalingEfficacy` | 同 Game.ini 中的 `WorldBuffScalingEfficacy` | float | 1.0 | ⚠️ |

---

## 5. Ragnarok 地图特殊设置

这些配置项仅适用于 Ragnarok 地图，需要在 GameUserSettings.ini 的 `[Ragnarok]` 部分设置。

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `AllowMultipleTamedUnicorns` | 允许地图上同时存在多只独角兽。False = 仅一只野生独角兽，True = 一只野生 + 无限驯服 | boolean | False | ⚠️ |
| `EnableVolcano` | 启用火山活动。False = 火山不活跃，True = 启用火山 | boolean | True | ⚠️ |
| `UnicornSpawnInterval` | 新独角兽刷新间隔（小时）。最小值，最大值为 2 倍 | integer | 24 | ⚠️ |
| `VolcanoIntensity` | 火山喷发强度。值越低喷发越剧烈。最小值 0.25，多人游戏建议不低于 0.5 | float | 1 | ⚠️ |
| `VolcanoInterval` | 火山活动间隔。0 = 5000-15000 秒，其他值为倍率 | integer | 0 | ⚠️ |

---

## 6. 已废弃配置及替代方案

以下配置在 ASA 中已废弃，请使用替代方案。

### 命令行参数

| 废弃参数 | 替代方案 | 说明 |
|----------|----------|------|
| `-AllowFlyerSpeedLeveling` | Game.ini: `bAllowFlyerSpeedLeveling=True` | 启用飞行生物移动速度升级 |
| `-automanagedmods` | `-mods=<ModId1>[,<ModId2>[...]]` | 自动模组管理（Steam 专用） |
| `-crossplay` | `-ServerPlatform=<plat1>[+<plat2>[...]]` | 启用跨平台 |
| `-epiconly` | 无（ASA 不在 Epic 平台） | Epic 独占服务器 |
| `?GameModIds=<ModID1>[,<ModID2>[...]]` | `ActiveMods` 或 `-mods` | 指定模组列表 |
| `-insecure` | 无 | 禁用 VAC |
| `-MapModID=<ModID>` | `ActiveMapMod` | 指定地图模组 |
| `-newsaveformat` | `-usestore` | 新存档格式 |
| `-PublicIPForEpic=<IPAddress>` | 无（ASA 不在 Epic 平台） | Epic 公共 IP |
| `-ServerAllowAnsel` | 无 | 允许 NVIDIA Ansel |
| `-UseVivox` | 无 | 启用 Vivox 语音 |
| `?bRawSockets` | 无 | 直接 UDP 连接 |
| `-forcenetthreading` | 无 | 强制网络线程 |
| `-nonetthreading` | 无 | 单线程网络 |
| `-allowansel` | 无 | NVIDIA Ansel 支持 |
| `-d3d10 -dx10 -sm4` | 无 | DirectX 10（ASA 不支持） |
| `-d3d12 -dx12` | 无（ASA 默认 DirectX 12） | DirectX 12 |
| `-noaafonts` | 无 | 禁用字体抗锯齿 |
| `-nosteamclient` | 无 | Steam 客户端启动 |
| `-USEALLAVAILABLECORES` | 无 | 使用所有核心（仅 DevKit） |

### GameUserSettings.ini

| 废弃配置 | 替代方案 | 说明 |
|----------|----------|------|
| `ActiveTotalConversion` | `-mods` 参数 | 全面转换模组 |
| `DestroyUnconnectedWaterPipes` | 无 | 自动销毁未连接水管 |
| `AllowedCheatersURL` | `AdminListURL` | 管理员列表 URL |
| `MaxPlayers` | `-WinLiveMaxPlayers=<integer>` | 最大玩家数 |

### Game.ini

| 废弃配置 | 替代方案 | 说明 |
|----------|----------|------|
| `PreventTransferForClassNames` | 无 | 阻止指定生物传输 |

### DynamicConfig

| 废弃配置 | 替代方案 | 说明 |
|----------|----------|------|
| `ActiveEventColors` | `-ActiveEvent=<eventname>` 命令行参数 | 活动颜色 |

---

## 属性索引表

用于 `PerLevelStatsMultiplier` 等配置的属性索引：

| 索引 | 属性 |
|------|------|
| 0 | 生命值 (Health) |
| 1 | 耐力 / 充能容量 (Stamina / Charge Capacity) |
| 2 | 昏迷值 (Torpidity) |
| 3 | 氧气 / 充能恢复 (Oxygen / Charge Regeneration) |
| 4 | 食物 (Food) |
| 5 | 水分 (Water) |
| 6 | 温度抗性 (Temperature) |
| 7 | 负重 (Weight) |
| 8 | 近战伤害 (Melee Damage) |
| 9 | 移动速度 (Movement Speed) |
| 10 | 恒温抗性 (Fortitude) |
| 11 | 制造技能 (Crafting Skill) |

---

## 地图名称参考

| 地图 | ASA 地图名称 |
|------|-------------|
| The Island | TheIsland_WP |
| The Center | TheCenter_WP |
| Scorched Earth | ScorchedEarth_WP |
| Ragnarok | Ragnarok_WP |
| Aberration | Aberration_WP |
| Extinction | Extinction_WP |
| Valguero | Valguero_WP |
| Astraeos | Astraeos_WP |
| Lost Colony | LostColony_WP |
| Club ARK | BobsMissions_WP |

---

> **数据来源**: [ARK Wiki - Server Configuration](https://ark.wiki.gg/wiki/Server_configuration)
>
> **最后更新**: 2026-06-24
