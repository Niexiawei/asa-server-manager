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
| `ServerPassword` | 服务器密码 | string | 无 | ✅ |
| `serverPVE` | 启用 PvE 模式（禁用 PvP） | boolean | False | ✅ |
| `ServerCrosshair` | 显示准星 | boolean | True | ✅ |
| `ServerHardcore` | 启用硬核模式（死亡后角色重置为 1 级） | boolean | False | ✅ |
| `SessionName` | 服务器名称 | string | ARK #123456 | ✅ |
| `RCONEnabled` | 启用 RCON | boolean | False | ⚠️ |
| `RCONPort` | RCON 端口 | integer | 27020 | ✅ |
| `RCONServerGameLogBuffer` | RCON 游戏日志缓冲区行数 | float | 600.0 | ✅ |
| `BanListURL` | 全局封禁列表 URL | string | 无 | ✅ |
| `AdminListURL` | 管理员列表 URL | string | 无 | ✅ |
| `AutoRestartIntervalSeconds` | 自动重启间隔（秒） | float | 未知 | ✅ |
| `UpdateAllowedCheatersInterval` | 远程管理员列表更新间隔（秒） | float | 600.0 | ✅ |
| `UseCharacterTracker` | 启用角色追踪 | boolean | False | ✅ |
| `SpectatorPassword` | 观战者密码 | string | 无 | ⚠️ |
| `UseExclusiveList` | 使用独占列表 | boolean | False | ⚠️ |
| `ListenServerTetherDistanceMultiplier` | 监听服务器系绳距离倍率 | float | 1.0 | ⚠️ |

#### 游戏玩法设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `AllowAnyoneBabyImprintCuddle` | 允许任何人照顾幼崽 | boolean | False | ✅ |
| `AllowCaveBuildingPvE` | PvE 模式下允许在洞穴中建造 | boolean | False | ✅ |
| `AllowCaveBuildingPvP` | PvP 模式下允许在洞穴中建造 | boolean | True | ✅ |
| `AllowCrateSpawnsOnTopOfStructures` | 允许在建筑顶部刷新补给箱 | boolean | False | ⚠️ |
| `AllowCryoFridgeOnSaddle` | 允许在平台鞍和木筏上放置低温冰箱 | boolean | False | ✅ |
| `AllowFlyerCarryPvE` | PvE 模式下允许飞行生物抓取野生生物 | boolean | False | ✅ |
| `AllowFlyingStaminaRecovery` | 允许飞行中恢复耐力 | boolean | False | ⚠️ |
| `AllowHideDamageSourceFromLogs` | 在部落日志中隐藏伤害来源 | boolean | True | ✅ |
| `AllowHitMarkers` | 显示远程攻击命中标记 | boolean | True | ✅ |
| `AllowIntegratedSPlusStructures` | 允许集成 S+ 建筑 | boolean | False | ⚠️ |
| `AllowMultipleAttachedC4` | 允许在一个生物上附着多个 C4 | boolean | False | ✅ |
| `AllowRaidDinoFeeding` | 允许永久驯服泰坦龙 | boolean | False | ✅ |
| `AllowSharedConnections` | 允许共享连接 | boolean | False | ⚠️ |
| `AllowTekSuitPowersInGenesis` | 在创世纪中允许泰克套装能力 | boolean | False | ⚠️ |
| `AllowThirdPersonPlayer` | 允许第三人称视角 | boolean | True | ✅ |
| `AlwaysAllowStructurePickup` | 禁用快速拾取建筑的计时器 | boolean | False | ✅ |
| `AlwaysNotifyPlayerLeft` | 总是通知玩家离开 | boolean | False | ⚠️ |
| `ArmadoggoDeathCooldown` | Armadoggo 受致命伤害后重生冷却时间（秒） | float | 3600 | ✅ |
| `AutoDestroyDecayedDinos` | 自动销毁衰减的生物 | boolean | False | ⚠️ |
| `AutoDestroyOldStructuresMultiplier` | 旧建筑自动销毁倍率 | float | 1.0 | ⚠️ |
| `AutoSavePeriodMinutes` | 自动保存间隔（分钟） | float | 15.0 | ✅ |
| `bForceCanRideFliers` | 强制允许骑乘飞行生物 | boolean | False | ⚠️ |
| `ClampItemSpoilingTimes` | 将所有腐烂时间限制为物品最大腐烂时间 | boolean | False | ✅ |
| `ClampItemStats` | 限制物品属性 | boolean | False | ⚠️ |
| `ClampResourceHarvestDamage` | 限制驯服生物对资源的采集伤害 | boolean | False | ✅ |
| `CosmeticWhitelistOverride` | 自定义装饰白名单 URL | string | 无 | ✅ |
| `CosmoWeaponAmmoReloadAmount` | Cosmo 蛛网发射器每次装填弹药量 | float | 1 | ✅ |
| `MaxCosmoWeaponAmmo` | Cosmo 蛛网发射器最大弹药量（-1 为随等级缩放） | float | -1 | ✅ |
| `CustomDynamicConfigUrl` | 自定义动态配置 URL | string | 无 | ⚠️ |
| `CustomLiveTuningUrl` | 实时调优文件 URL | string | 无 | ✅ |
| `DestroyTamesOverTheSoftTameLimit` | 超过软驯服上限的生物标记并销毁 | boolean | False | ✅ |
| `DifficultyOffset` | 难度等级 | float | 1.0 | ✅ |
| `DinoCountMultiplier` | 生物数量倍率 | float | 1.0 | ⚠️ |
| `DisableBurrowDecayTimers` | 禁用 Burrowbuck 的洞穴衰减计时器 | boolean | False | ✅ |
| `DisableCryopodEnemyCheck` | 允许在敌人附近使用低温舱 | boolean | False | ✅ |
| `DisableCryopodFridgeRequirement` | 无需低温冰箱即可使用低温舱 | boolean | False | ✅ |
| `DisableDinoDecayPvE` | 禁用 PvE 模式下的生物衰减 | boolean | False | ✅ |
| `DisableImprintDinoBuff` | 禁用印记生物的玩家属性加成 | boolean | False | ✅ |
| `DisablePvEGamma` | 禁止 PvE 模式下使用 gamma 控制台命令 | boolean | False | ✅ |
| `DisableStructureDecayPvE` | 禁用 PvE 模式下的建筑自动衰减 | boolean | False | ✅ |
| `DisableWeatherFog` | 禁用雾天 | boolean | False | ✅ |
| `DontAlwaysNotifyPlayerJoined` | 禁用玩家加入通知 | boolean | False | ✅ |
| `EnableExtraStructurePreventionVolumes` | 在特定资源丰富区域禁用建造 | boolean | False | ✅ |
| `EnablePvPGamma` | 允许 PvP 模式下使用 gamma 控制台命令 | boolean | False | ✅ |
| `ExtinctionEventTimeInterval` | 灭绝事件时间间隔 | float | 0 | ⚠️ |
| `FastDecayUnsnappedCoreStructures` | 快速衰减未连接的核心建筑 | boolean | False | ⚠️ |
| `ForceAllStructureLocking` | 默认锁定所有建筑 | boolean | False | ✅ |
| `ForceGachaUnhappyInCaves` | Gacha 在洞穴中变为不快乐状态 | boolean | True | ✅ |
| `globalVoiceChat` | 启用全局语音聊天 | boolean | False | ✅ |
| `NonPermanentDiseases` | 使永久疾病不再永久 | boolean | False | ✅ |
| `OverrideStructurePlatformPrevention` | 允许在平台鞍上建造和使用炮塔 | boolean | False | ✅ |
| `PreventDiseases` | 完全阻止疾病 | boolean | False | ✅ |
| `PreventMateBoost` | 禁用生物配偶加成 | boolean | False | ✅ |
| `PreventOfflinePvP` | 启用离线突袭防护（ORP） | boolean | False | ✅ |
| `PreventOfflinePvPInterval` | ORP 激活前等待时间（秒） | float | 0.0 | ✅ |
| `PreventSpawnAnimations` | 禁用重生动画 | boolean | False | ✅ |
| `PreventTribeAlliances` | 阻止部落创建联盟 | boolean | False | ✅ |
| `ProximityChat` | 启用近距离聊天 | boolean | False | ✅ |
| `RandomSupplyCratePoints` | 补给箱随机位置 | boolean | False | ✅ |
| `ShowFloatingDamageText` | 显示浮动伤害数字 | boolean | False | ✅ |
| `ShowMapPlayerLocation` | 在地图上显示玩家位置 | boolean | True | ✅ |
| `UseAstraeosTraversalBuff` | 启用 Astraeos 的生物群落传送 | boolean | True | ✅ |
| `UseFjordurTraversalBuff` | 启用 Fjordur 的生物群落传送 | boolean | True | ⚠️ |
| `UseOptimizedHarvestingHealth` | 使用优化的采集生命值 | boolean | False | ⚠️ |
| `PvEAllowStructuresAtSupplyDrops` | PvE 模式下允许在补给点附近建造 | boolean | False | ✅ |
| `PvEStructureDecayPeriodMultiplier` | PvE 建筑衰减周期倍率 | float | 1.0 | ⚠️ |
| `PvPDinoDecay` | PvP 模式下启用生物衰减 | boolean | False | ✅ |
| `PvPStructureDecay` | PvP 模式下启用建筑衰减 | boolean | False | ⚠️ |
| `RaidDinoCharacterFoodDrainMultiplier` | 突袭生物食物消耗倍率 | float | 1.0 | ✅ |
| `StructureDamageMultiplier` | 建筑伤害倍率 | float | 1.0 | ⚠️ |
| `TamedDinoDamageMultiplier` | 驯服生物伤害倍率 | float | 1.0 | ⚠️ |
| `TamedDinoResistanceMultiplier` | 驯服生物抗性倍率 | float | 1.0 | ⚠️ |
| `TribeLogDestroyedEnemyStructures` | 在部落日志中记录摧毁敌方建筑 | boolean | False | ⚠️ |

#### 倍率设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `DayCycleSpeedScale` | 昼夜循环速度倍率 | float | 1.0 | ✅ |
| `DayTimeSpeedScale` | 白天时间速度倍率 | float | 1.0 | ✅ |
| `NightTimeSpeedScale` | 夜晚时间速度倍率 | float | 1.0 | ✅ |
| `DifficultyOffset` | 难度等级 | float | 1.0 | ✅ |
| `OverrideOfficialDifficulty` | 覆盖官方难度（5.0 允许生物刷新到 150 级） | float | 0.0 | ✅ |
| `DinoDamageMultiplier` | 野生生物攻击伤害倍率 | float | 1.0 | ✅ |
| `DinoResistanceMultiplier` | 野生生物受伤抗性倍率 | float | 1.0 | ✅ |
| `DinoCharacterFoodDrainMultiplier` | 生物食物消耗倍率 | float | 1.0 | ✅ |
| `DinoCharacterHealthRecoveryMultiplier` | 生物生命恢复倍率 | float | 1.0 | ✅ |
| `DinoCharacterStaminaDrainMultiplier` | 生物耐力消耗倍率 | float | 1.0 | ✅ |
| `HarvestAmountMultiplier` | 采集产量倍率 | float | 1.0 | ✅ |
| `HarvestHealthMultiplier` | 可采集物品生命值倍率 | float | 1.0 | ✅ |
| `ItemStackSizeMultiplier` | 全局物品堆叠大小倍率 | float | 1.0 | ✅ |
| `PlayerDamageMultiplier` | 玩家攻击伤害倍率 | float | 1.0 | ✅ |
| `PlayerResistanceMultiplier` | 玩家受伤抗性倍率 | float | 1.0 | ✅ |
| `PlayerCharacterFoodDrainMultiplier` | 玩家食物消耗倍率 | float | 1.0 | ✅ |
| `PlayerCharacterHealthRecoveryMultiplier` | 玩家生命恢复倍率 | float | 1.0 | ✅ |
| `PlayerCharacterStaminaDrainMultiplier` | 玩家耐力消耗倍率 | float | 1.0 | ✅ |
| `PlayerCharacterWaterDrainMultiplier` | 玩家水分消耗倍率 | float | 1.0 | ✅ |
| `OxygenSwimSpeedStatMultiplier` | 氧气属性对游泳速度的倍率 | float | 1.0 | ✅ |
| `ResourcesRespawnPeriodMultiplier` | 资源重生速度倍率 | float | 1.0 | ✅ |
| `StructureResistanceMultiplier` | 建筑受伤抗性倍率 | float | 1.0 | ✅ |
| `StructurePreventResourceRadiusMultiplier` | 建筑周围资源禁生区域倍率 | float | 1.0 | ✅ |
| `TamingSpeedMultiplier` | 驯服速度倍率 | float | 1.0 | ✅ |
| `XPMultiplier` | 经验值倍率 | float | 1.0 | ✅ |
| `PvEDinoDecayPeriodMultiplier` | PvE 生物衰减时间倍率 | float | 1.0 | ✅ |
| `StructurePickupHoldDuration` | 快速拾取建筑按住时间（秒） | float | 0.5 | ✅ |
| `StructurePickupTimeAfterPlacement` | 放置后可快速拾取的时间（秒） | float | 30.0 | ✅ |
| `TheMaxStructuresInRange` | 特定范围内最大建筑数量 | integer | 10500 | ✅ |
| `PerPlatformMaxStructuresMultiplier` | 平台鞍/木筏上最大建筑数量倍率 | float | 1.0 | ✅ |
| `PlatformSaddleBuildAreaBoundsMultiplier` | 平台鞍建造区域范围倍率 | float | 1.0 | ✅ |
| `TribeNameChangeCooldown` | 部落名称更改冷却时间（分钟） | float | 15.0 | ✅ |
| `MaxTamedDinos` | 服务器最大驯服生物数量 | float | 5000.0 | ✅ |
| `MaxPersonalTamedDinos` | 每部落驯服生物上限（0 为禁用） | integer | 0 | ✅ |
| `MaxTamedDinos_SoftTameLimit` | 服务器软驯服上限 | integer | 5000 | ✅ |
| `MaxTamedDinos_SoftTameLimit_CountdownForDeletionDuration` | 超过软上限后生物销毁倒计时（秒） | integer | 604800 | ✅ |
| `MaxTrainCars` | 火车最大车厢数 | integer | 8 | ✅ |
| `MaxTributeDinos` | 上传生物槽位 | integer | 20 | ✅ |
| `MaxTributeItems` | 上传物品槽位 | integer | 50 | ✅ |
| `MaxTributeCharacters` | 上传角色槽位 | integer | 5 | ⚠️ |
| `MaxGateFrameOnSaddles` | 平台鞍上门框最大数量 | integer | 6 | ⚠️ |
| `MaxHexagonsPerCharacter` | 每角色最大六角币数量 | integer | 无 | ⚠️ |
| `MaxPlatformSaddleStructureLimit` | 平台鞍建筑限制 | integer | 无 | ⚠️ |
| `PersonalTamedDinosSaddleStructureCost` | 驯服生物鞍建筑成本 | integer | 无 | ⚠️ |
| `KickIdlePlayersPeriod` | 踢出空闲玩家的时间（秒） | float | 3600.0 | ✅ |
| `ImplantSuicideCD` | 植入物"重生"功能冷却时间（秒） | float | 28800 | ✅ |
| `IgnoreLimitMaxStructuresInRangeTypeFlag` | 移除装饰建筑限制 | boolean | False | ✅ |
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
| `CrossARKAllowForeignDinoDownloads` | 允许在畸变地图下载非本地生物 | boolean | False | ✅ |
| `noTributeDownloads` | 阻止跨服数据下载 | boolean | False | ✅ |
| `PreventDownloadDinos` | 阻止从 ARK 数据下载生物 | boolean | False | ✅ |
| `PreventDownloadItems` | 阻止从 ARK 数据下载物品 | boolean | False | ✅ |
| `PreventDownloadSurvivors` | 阻止从 ARK 数据下载幸存者 | boolean | False | ✅ |
| `PreventUploadDinos` | 阻止上传生物到 ARK 数据 | boolean | False | ✅ |
| `PreventUploadItems` | 阻止上传物品到 ARK 数据 | boolean | False | ✅ |
| `PreventUploadSurvivors` | 阻止上传幸存者到 ARK 数据 | boolean | False | ✅ |

#### Bunker（地堡）设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `LimitBunkersPerTribe` | 限制每部落地堡数量 | boolean | True | ✅ |
| `LimitBunkersPerTribeNum` | 每部落最大地堡数 | integer | 3 | ✅ |
| `AllowBunkersInPreventionZones` | 允许在防护区域内建造地堡 | boolean | False | ✅ |
| `AllowRidingDinosInsideBunkers` | 允许在地堡内骑乘生物 | boolean | True | ✅ |
| `AllowBunkerModulesAboveGround` | 允许地堡模块在地面以上 | boolean | False | ✅ |
| `AllowDinoAIInsideBunkers` | 允许地堡内生物 AI | boolean | True | ✅ |
| `AllowBunkerModulesInPreventionZones` | 允许在防护区域内地堡模块 | boolean | False | ✅ |
| `MinDistanceBetweenBunkers` | 地堡之间最小距离 | float | 3000.0 | ✅ |
| `EnemyAccessBunkerHPThreshold` | 敌人可攻击地堡的血量阈值 | float | 0.25 | ✅ |
| `BunkerUnderHPThresholdDmgMultiplier` | 地堡低于血量阈值时的伤害倍率 | float | 0.05 | ✅ |

#### CryoHospital（低温医院）设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `CryoHospitalHoursToRegenHP` | 低温医院恢复生命值时间（小时） | float | 1.0 | ✅ |
| `CryoHospitalHoursToRegenFood` | 低温医院恢复食物时间（小时） | float | 24.0 | ✅ |
| `CryoHospitalHoursToDrainTorpor` | 低温医院消耗昏迷值时间（小时） | float | 1.0 | ✅ |
| `CryoHospitalMatingCooldownReduction` | 低温医院交配冷却减少量 | float | 2.0 | ✅ |

#### Bloodforge（血锻）设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `BloodforgeReinforceExtraDurability` | 血锻强化额外耐久度 | float | 0.3 | ✅ |
| `BloodforgeReinforceResourceCostMultiplier` | 血锻强化资源消耗倍率 | float | 3.0 | ✅ |
| `BloodforgeReinforceSpeedMultiplier` | 血锻强化速度倍率 | float | 0.1 | ✅ |

#### 前哨站设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `MaxActiveOutposts` | 最大活跃前哨站数 | integer | 无 | ✅ |
| `MaxActiveResourceCaches` | 最大活跃资源缓存数 | integer | 无 | ✅ |
| `MaxActiveCityOutposts` | 最大城市前哨站数 | integer | 无 | ✅ |
| `OutpostSigilRewardMultiplier` | 前哨站任务印章奖励倍率 | float | 1.0 | ✅ |

#### 敏感词过滤

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `BadWordListURL` | 敏感词列表 URL | string | 官方默认列表 | ✅ |
| `BadWordWhiteListURL` | 白名单词列表 URL | string | 官方默认列表 | ✅ |
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
| `BabyCuddleGracePeriodMultiplier` | 延迟照顾幼崽后印记质量开始下降的宽限期倍率 | float | 1.0 | ✅ |
| `BabyCuddleIntervalMultiplier` | 幼崽需要照顾的频率倍率 | float | 1.0 | ✅ |
| `BabyCuddleLoseImprintQualitySpeedMultiplier` | 宽限期后印记质量下降速度倍率 | float | 1.0 | ✅ |
| `BabyFoodConsumptionSpeedMultiplier` | 幼崽食物消耗速度倍率 | float | 1.0 | ✅ |
| `BabyImprintAmountMultiplier` | 每次照顾提供的印记百分比倍率 | float | 1.0 | ✅ |
| `BabyImprintingStatScaleMultiplier` | 印记质量对属性的影响倍率（设为 0 禁用） | float | 1.0 | ✅ |
| `BabyMatureSpeedMultiplier` | 幼崽成熟速度倍率 | float | 1.0 | ✅ |
| `EggHatchSpeedMultiplier` | 受精蛋孵化速度倍率 | float | 1.0 | ✅ |
| `LayEggIntervalMultiplier` | 下蛋间隔倍率 | float | 1.0 | ✅ |
| `MatingIntervalMultiplier` | 交配间隔倍率 | float | 1.0 | ✅ |
| `MatingSpeedMultiplier` | 交配速度倍率 | float | 1.0 | ✅ |
| `PoopIntervalMultiplier` | 排便频率倍率 | float | 1.0 | ✅ |
| `WildDinoCharacterFoodDrainMultiplier` | 野生生物食物消耗速度倍率 | float | 1.0 | ✅ |
| `PreventBreedingForClassNames` | 阻止指定生物繁殖（通过类名） | string | 无 | ✅ |
| `bAllowUnclaimDinos` | 允许取消认领生物 | boolean | True | ⚠️ |
| `bAllowCustomRecipes` | 允许自定义配方 | boolean | True | ⚠️ |
| `bDisableDinoBreeding` | 禁用生物繁殖 | boolean | False | ⚠️ |
| `bDisableDinoRiding` | 禁用骑乘生物 | boolean | False | ⚠️ |
| `bDisableDinoTaming` | 禁用驯服生物 | boolean | False | ⚠️ |
| `DinoHarvestingDamageMultiplier` | 生物采集伤害倍率 | float | 3.2 | ⚠️ |
| `DinoTurretDamageMultiplier` | 炮塔对生物伤害倍率 | float | 1.0 | ⚠️ |
| `PassiveTameIntervalMultiplier` | 被动驯服请求间隔倍率 | float | 1.0 | ⚠️ |
| `TamedDinoCharacterFoodDrainMultiplier` | 驯服生物食物消耗速度倍率 | float | 1.0 | ⚠️ |
| `TamedDinoTorporDrainMultiplier` | 驯服生物昏迷值消耗速度倍率 | float | 1.0 | ⚠️ |
| `WildDinoTorporDrainMultiplier` | 野生生物昏迷值消耗速度倍率 | float | 1.0 | ⚠️ |
| `AdjustableMutagenSpawnDelayMultiplier` | Mutagen 刷新延迟倍率 | float | 1.0 | ⚠️ |
| `PreventDinoTameClassNames` | 阻止指定生物驯服（通过类名） | string | 无 | ⚠️ |

### 属性与等级设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `bAllowFlyerSpeedLeveling` | 允许飞行生物升级移动速度 | boolean | False | ✅ |
| `bAllowSpeedLeveling` | 允许玩家和非飞行生物升级移动速度 | boolean | False | ✅ |
| `bAllowUnlimitedRespecs` | 允许无限次使用洗点药水 | boolean | False | ✅ |
| `PerLevelStatsMultiplier_Player[<integer>]` | 玩家每级属性倍率（索引 0-11） | float | 无 | ✅ |
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
| `CraftXPMultiplier` | 制造经验倍率 | float | 1.0 | ✅ |
| `GenericXPMultiplier` | 通用经验倍率 | float | 1.0 | ✅ |
| `HarvestXPMultiplier` | 采集经验倍率 | float | 1.0 | ✅ |
| `KillXPMultiplier` | 击杀经验倍率 | float | 1.0 | ✅ |
| `SpecialXPMultiplier` | 特殊事件经验倍率 | float | 1.0 | ✅ |
| `CraftingSkillBonusMultiplier` | 制造技能加成倍率 | float | 1.0 | ✅ |

### 建筑与资源设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `bDisableStructurePlacementCollision` | 允许建筑与地形重叠 | boolean | False | ✅ |
| `bIgnoreStructuresPreventionVolumes` | 允许在禁止建造区域建造 | boolean | False | ✅ |
| `bAllowPlatformSaddleMultiFloors` | 允许多层平台鞍 | boolean | False | ⚠️ |
| `bHardLimitTurretsInRange` | 启用炮塔硬限制（10k 范围内 100 个） | boolean | False | ⚠️ |
| `bLimitTurretsInRange` | 限制范围内炮塔数量 | boolean | True | ⚠️ |
| `LimitTurretsNum` | 范围内最大炮塔数量 | integer | 100 | ⚠️ |
| `LimitTurretsRange` | 炮塔限制范围（UE 单位） | float | 10000.0 | ⚠️ |
| `ResourceNoReplenishRadiusPlayers` | 玩家周围资源禁生区域倍率 | float | 1.0 | ✅ |
| `ResourceNoReplenishRadiusStructures` | 建筑周围资源禁生区域倍率 | float | 1.0 | ✅ |
| `CropDecaySpeedMultiplier` | 作物腐烂速度倍率 | float | 1.0 | ✅ |
| `CropGrowthSpeedMultiplier` | 作物生长速度倍率 | float | 1.0 | ✅ |
| `LimitGeneratorsNum` | 区域内发电机数量限制 | integer | 3 | ✅ |
| `LimitGeneratorsRange` | 发电机限制区域范围（UE 单位） | integer | 15000 | ✅ |
| `MaxFallSpeedMultiplier` | 开始受到坠落伤害的坠落速度倍率 | float | 1.0 | ✅ |
| `LimitNonPlayerDroppedItemsCount` | 非玩家掉落物品数量限制 | integer | 0 | ⚠️ |
| `LimitNonPlayerDroppedItemsRange` | 非玩家掉落物品范围限制 | integer | 0 | ⚠️ |
| `FastDecayInterval` | 快速衰减间隔（秒） | integer | 43200 | ⚠️ |
| `FishingLootQualityMultiplier` | 钓鱼战利品质量倍率 | float | 1.0 | ⚠️ |
| `FuelConsumptionIntervalMultiplier` | 燃料消耗间隔倍率 | float | 1.0 | ⚠️ |
| `GlobalCorpseDecompositionTimeMultiplier` | 全局尸体分解时间倍率 | float | 1.0 | ⚠️ |
| `GlobalPoweredBatteryDurabilityDecreasePerSecond` | 全局电池耐久消耗速率 | float | 3.0 | ⚠️ |
| `StructureDamageRepairCooldown` | 建筑伤害修复冷却时间（秒） | integer | 180 | ⚠️ |
| `SupplyCrateLootQualityMultiplier` | 补给箱战利品质量倍率 | float | 1.0 | ⚠️ |
| `PvPZoneStructureDamageMultiplier` | PvP 区域建筑伤害倍率 | float | 6.0 | ⚠️ |
| `IncreasePvPRespawnIntervalBaseAmount` | PvP 重生间隔基础增加量（秒） | float | 60.0 | ⚠️ |
| `IncreasePvPRespawnIntervalCheckPeriod` | PvP 重生间隔检查周期（秒） | float | 300.0 | ⚠️ |
| `IncreasePvPRespawnIntervalMultiplier` | PvP 重生间隔倍率 | float | 2.0 | ⚠️ |
| `bIncreasePvPRespawnInterval` | 启用 PvP 重生间隔增加 | boolean | True | ⚠️ |
| `PreventOfflinePvPConnectionInvincibleInterval` | 登录后无敌时间（秒） | float | 5.0 | ⚠️ |
| `UseCorpseLifeSpanMultiplier` | 尸体和掉落箱寿命倍率 | float | 1.0 | ⚠️ |
| `BaseTemperatureMultiplier` | 基础温度倍率 | float | 1.0 | ⚠️ |
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
| `ItemStatClamps[<attribute>]` | 全局限制物品属性 | struct | 无 | ⚠️ |
| `OverrideEngramEntries` | 按索引配置印痕状态 | struct | 无 | ⚠️ |
| `OverridePlayerLevelEngramPoints` | 覆盖每级印痕点数 | integer | 无 | ⚠️ |
| `EngramEntryAutoUnlocks` | 自动解锁指定印痕 | struct | 无 | ⚠️ |

### 制造与配方设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `CustomRecipeEffectivenessMultiplier` | 自定义配方效果倍率 | float | 1.0 | ✅ |
| `CustomRecipeSkillMultiplier` | 自定义配方制造速度技能效果倍率 | float | 1.0 | ✅ |
| `bDisableWirelessCrafting` | 禁用 Tek 专用存储的无线制造 | boolean | False | ✅ |
| `bDisableWirelessCraftingForDinos` | 禁用在生物背包中使用无线制造 | boolean | False | ✅ |
| `bDisableWirelessCraftingForPlayers` | 禁用在玩家背包中使用无线制造 | boolean | False | ✅ |
| `bDisableWirelessCraftingForStructures` | 禁用在建筑背包中使用无线制造 | boolean | False | ✅ |
| `WirelessCraftingRangeOverride` | 无线制造范围（UE 单位） | integer | 3000 | ✅ |

### 其他设置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `bDisableFriendlyFire` | 禁用友军伤害 | boolean | False | ✅ |
| `bPvEDisableFriendlyFire` | PvE 模式下禁用友军伤害 | boolean | False | ✅ |
| `bDisablePhotoMode` | 禁用拍照模式 | boolean | False | ✅ |
| `bShowCreativeMode` | 在暂停菜单中显示创意模式按钮 | boolean | False | ✅ |
| `bUseDinoLevelUpAnimations` | 生物升级时播放动画 | boolean | True | ✅ |
| `bUseSingleplayerSettings` | 启用单人游戏平衡设置 | boolean | False | ✅ |
| `HairGrowthSpeedMultiplier` | 头发生长速度倍率 | float | 0 (ASA) | ✅ |
| `GlobalItemDecompositionTimeMultiplier` | 掉落物品分解时间倍率 | float | 1.0 | ✅ |
| `GlobalSpoilingTimeMultiplier` | 全局腐烂时间倍率 | float | 1.0 | ✅ |
| `DestroyTamesOverLevelClamp` | 超过指定等级的驯服生物在启动时删除 | integer | 0 | ✅ |
| `PhotoModeRangeLimit` | 拍照模式相机与玩家最大距离 | integer | 3000 | ✅ |
| `IgnorePVPMountedWeaponryRestrictions` | 忽略 PvP 骑乘武器限制 | boolean | False | ✅ |
| `TribeTowerBonusMultiplier` | 部落塔加成倍率 | float | 2.0 | ✅ |
| `BaseHexagonRewardMultiplier` | 任务六角币奖励倍率 | float | 1.0 | ✅ |
| `HexagonCostMultiplier` | 六角币商店物品价格倍率 | float | 1.0 | ✅ |
| `ExcludeItemIndices` | 从补给箱中排除指定物品 ID | integer | 无 | ✅ |
| `HarvestResourceItemAmountClassMultipliers` | 按资源类型设置采集产量倍率 | struct | 无 | ✅ |
| `LevelExperienceRampOverrides` | 配置玩家和生物等级及经验需求 | struct | 无 | ✅ |
| `OverrideNamedEngramEntries` | 按名称配置印痕状态和需求 | struct | 无 | ✅ |
| `ConfigAddNPCSpawnEntriesContainer` | 在刷新区域添加指定生物 | struct | 无 | ✅ |
| `CheatTeleportLocations` | 创建命名传送点 | struct | 无 | ✅ |
| `ValgueroMemorialEntries` | Valguero 纪念碑名称列表（分号分隔） | string | 无 | ✅ |
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

覆盖生物伤害。

```
DinoClassDamageMultipliers=(ClassName="<string>",Multiplier=<float>)
TamedDinoClassDamageMultipliers=(ClassName="<string>",Multiplier=<float>)
```

**示例：**
```ini
DinoClassDamageMultipliers=(ClassName="MegaRex_Character_BP_C",Multiplier=0.1)
TamedDinoClassDamageMultipliers=(ClassName="Rex_Character_BP_C",Multiplier=10.0)
```

#### DinoClassResistanceMultipliers / TamedDinoClassResistanceMultipliers

覆盖生物抗性。

```
DinoClassResistanceMultipliers=(ClassName="<string>",Multiplier=<float>)
TamedDinoClassResistanceMultipliers=(ClassName="<string>",Multiplier=<float>)
```

**示例：**
```ini
DinoClassResistanceMultipliers=(ClassName="MegaRex_Character_BP_C",Multiplier=0.1)
TamedDinoClassResistanceMultipliers=(ClassName="Rex_Character_BP_C",Multiplier=10.0)
```

#### TamedDinoClassSpeedMultipliers

覆盖驯服生物速度。

```
TamedDinoClassSpeedMultipliers=(ClassName="<string>",Multiplier=<float>)
```

**示例：**
```ini
TamedDinoClassSpeedMultipliers=(ClassName="Argent_Character_BP_C",Multiplier=2.0)
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

覆盖补给箱物品。

```
ConfigOverrideSupplyCrateItems=(SupplyCrateClassString="<string>",MinItemSets=<integer>,MaxItemSets=<integer>,NumItemSetsPower=<float>,bSetsRandomWithoutReplacement=<boolean>[,bAppendItemSets=<boolean>],ItemSets=((SetName="<string>",MinNumItems=<integer>,MaxNumItems=<integer>,NumItemsPower=<float>,SetWeight=<float>,bItemsRandomWithoutReplacement=<boolean>,ItemEntries=((ItemEntryName="<string>",EntryWeight=<float>,ItemClassStrings=("<string>"),ItemsWeights=(<float>),MinQuantity=<float>,MaxQuantity=<float>,MinQuality=<float>,MaxQuality=<float>,bForceBlueprint=<boolean>,ChanceToBeBlueprintOverride=<float>)))))
```

#### HarvestResourceItemAmountClassMultipliers

按资源类型设置采集产量倍率。

```
HarvestResourceItemAmountClassMultipliers=(ClassName="<string>",Multiplier=<float>)
```

**示例：**
```ini
HarvestResourceItemAmountClassMultipliers=(ClassName="PrimalItemResource_Thatch_C",Multiplier=2.0)
```

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

每级属性倍率配置。

```
PerLevelStatsMultiplier_Player[<attribute>]=<multiplier>
PerLevelStatsMultiplier_DinoTamed[<attribute>]=<multiplier>
PerLevelStatsMultiplier_DinoWild[<attribute>]=<multiplier>
PerLevelStatsMultiplier_DinoTamed_Add[<attribute>]=<multiplier>
PerLevelStatsMultiplier_DinoTamed_Affinity[<attribute>]=<multiplier>
```

**示例：**
```ini
PerLevelStatsMultiplier_Player[7]=2.0
PerLevelStatsMultiplier_DinoTamed[0]=1.0
PerLevelStatsMultiplier_DinoTamed_Add[0]=1.0
PerLevelStatsMultiplier_DinoTamed_Affinity[0]=1.0
```

#### MutagenLevelBoost / MutagenLevelBoost_Bred

Mutagen 对生物的等级提升。

```
MutagenLevelBoost[<Stat_ID>]=<integer>
MutagenLevelBoost_Bred[<Stat_ID>]=<integer>
```

**默认值：** 5, 5, 0, 0, 0, 0, 0, 5, 5, 0, 0, 0

**示例：**
```ini
MutagenLevelBoost[0]=10
MutagenLevelBoost[1]=0
MutagenLevelBoost[7]=0
MutagenLevelBoost[8]=10
```

#### PlayerBaseStatMultipliers

玩家基础属性倍率。

```
PlayerBaseStatMultipliers[<attribute>]=<multiplier>
```

**默认值：**
| 属性 | 默认值 | 输出 |
|------|--------|------|
| 0 Health | 1.0 | 100.0 |
| 1 Stamina | 1.0 | 100.0 |
| 2 Torpidity | 1.0 | 200.0 |
| 3 Oxygen | 1.0 | 100.0 |
| 4 Food | 1.0 | 100.0 |
| 5 Water | 1.0 | 100.0 |
| 6 Temperature | 1.0 | 100.0 |
| 7 Weight | 1.0 | 100.0 |
| 8 Damage | 1.0 | 100.0 |
| 9 Speed | 1.0 | 100.0 |
| 10 Fortitude | 1.0 | 0.0 |
| 11 Crafting Speed | 1.0 | 100.0 |

#### LevelExperienceRampOverrides

配置玩家和生物等级及经验需求。第一次出现配置玩家等级，第二次出现配置驯服生物等级。

```
LevelExperienceRampOverrides=(ExperiencePointsForLevel[0]=<points>,ExperiencePointsForLevel[1]=<points>,...,ExperiencePointsForLevel[n]=<points>)
```

**注意：** 最后 100 级用于飞升、迷你恐龙经验、探索者笔记和符文奖励，需要额外添加 100 级。

**示例：**
```ini
LevelExperienceRampOverrides=(ExperiencePointsForLevel[0]=1,ExperiencePointsForLevel[1]=5,...,ExperiencePointsForLevel[64]=1000)
```

---

## 4. DynamicConfig 动态配置

通过 `-UseDynamicConfig` 命令行参数启用。可在不重启服务器的情况下修改。

### 已确认配置

| 配置项 | 说明 | 类型 | 默认值 | 兼容性 |
|--------|------|------|--------|--------|
| `BabyCuddleIntervalMultiplier` | 同 Game.ini 中的 `BabyCuddleIntervalMultiplier` | float | 1.0 | ✅ |
| `BabyImprintAmountMultiplier` | 同 Game.ini 中的 `BabyImprintAmountMultiplier` | float | 1.0 | ✅ |
| `BabyMatureSpeedMultiplier` | 同 Game.ini 中的 `BabyMatureSpeedMultiplier` | float | 1.0 | ✅ |
| `EggHatchSpeedMultiplier` | 同 Game.ini 中的 `EggHatchSpeedMultiplier` | float | 1.0 | ✅ |
| `HarvestAmountMultiplier` | 同 GameUserSettings.ini 中的 `HarvestAmountMultiplier` | float | 1.0 | ✅ |
| `HexagonRewardMultiplier` | 同 Game.ini 中的六角币奖励倍率 | float | 1.0 | ✅ |
| `MatingIntervalMultiplier` | 同 Game.ini 中的 `MatingIntervalMultiplier` | float | 1.0 | ✅ |
| `XPMultiplier` | 同 Game.ini 中的经验倍率 | float | 1.0 | ✅ |
| `DynamicColorset` | 自定义颜色列表（逗号分隔，需 `ActiveEventColors=custom`） | string | 无 | ✅ |
| `DynamicColorsetChanceOverride` | 动态颜色应用概率（0.0-1.0） | float | 0.25 | ✅ |

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
