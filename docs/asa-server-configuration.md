# ARK: Survival Ascended - 服务器配置项完整参考

> **来源**: [ARK Wiki](https://ark.wiki.gg/wiki/Server_configuration) | **日期**: 2026-06-23
> **仅 ASA 兼容项** | 共 328+ 项配置 | 用于前端可视化配置基础资料

---

## 配置文件说明

| 配置文件 | 路径 | 配置方式 | 说明 |
|----------|------|----------|------|
| GameUserSettings.ini | `ShooterGame/Saved/Config/WindowsServer/GameUserSettings.ini` | INI 节 + 键值对 | 服务器行为、倍率、开关等 |
| Game.ini | `ShooterGame/Saved/Config/WindowsServer/Game.ini` | 直接键值对 | 高级游戏逻辑、生物配置等 |
| 命令行 | 启动脚本 | `?key=value` 或 `-flag` | 部分设置仅命令行生效 |

### 配置文件写入格式

```ini
; GameUserSettings.ini 示例
[ServerSettings]
TamingSpeedMultiplier=3.0
HarvestAmountMultiplier=2.0
ServerPassword=mypass
ServerAdminPassword=admin123

[SessionSettings]
Port=7777
QueryPort=27015
SessionName=My ASA Server

[MessageOfTheDay]
Message=欢迎来到服务器！
Duration=30

; Game.ini 示例 (无需 Section)
BabyMatureSpeedMultiplier=5.0
EggHatchSpeedMultiplier=5.0
bAllowSpeedLeveling=True
```

### 值类型说明

| 类型 | 格式 | 示例 |
|------|------|------|
| boolean | True 或 False | `AdminLogging=True` |
| float | 小数 | `TamingSpeedMultiplier=3.0` |
| integer | 整数 | `MaxPlayers=70` |
| string | 文本 (无需引号) | `ServerPassword=mypass` |
| list | 逗号分隔 | `ActiveMods=123,456` |
| (...) | 结构化配置 | 见相关章节详细说明 |

### 配置生效方式

| 方式 | 生效时机 | 说明 |
|------|----------|------|
| 命令行参数 | 启动时 | 修改启动脚本后重启 |
| INI 文件 | 启动时 | 保存文件后重启服务器 |
| DynamicConfig | 运行时热重载 | 无需重启 (通过 URL 或内置) |

---

## 目录

- [1. 命令行参数](#1-命令行参数)
  - [1.1 语法](#11-语法)
  - [1.2 地图](#12-地图)
- [2. 命令行选项](#2-命令行选项)
- [3. 配置文件](#3-配置文件)
  - [3.1 GameUserSettings.ini](#31-gameusersettingsini)
    - [3.1.1 [ServerSettings]](#311-serversettings)
    - [3.1.2 [SessionSettings]](#312-sessionsettings)
    - [3.1.3 [MultiHome]](#313-multihome)
    - [3.1.4 [/Script/Engine.GameSession]](#314-scriptenginegamesession)
    - [3.1.5 [MessageOfTheDay]](#315-messageoftheday)
  - [3.2 Game.ini](#32-gameini)
    - [3.2.1 繁殖与成长](#321-繁殖与成长)
    - [3.2.2 通用设置](#322-通用设置)
    - [3.2.3 ASA新增功能](#323-asa新增功能)
    - [3.2.4 经验值倍率](#324-经验值倍率)
    - [3.2.5 PvP与部落](#325-pvp与部落)
    - [3.2.6 生物设置](#326-生物设置)
    - [3.2.7 采集与资源](#327-采集与资源)
    - [3.2.8 Mod与地图](#328-mod与地图)
    - [3.2.9 建筑与防御](#329-建筑与防御)
    - [3.2.10 印痕与等级](#3210-印痕与等级)
    - [3.2.11 物品](#3211-物品)
    - [3.2.12 时间与存档](#3212-时间与存档)
- [4. 高级配置详解](#4-高级配置详解)
  - [4.1 生物生成配置](#41-生物生成配置)
  - [4.2 生物属性配置](#42-生物属性配置)
  - [4.3 印痕条目配置](#43-印痕条目配置)
  - [4.4 物品配置](#44-物品配置)
  - [4.5 等级经验覆盖](#45-等级经验覆盖)
  - [4.6 属性配置](#46-属性配置)
  - [4.7 单人设置](#47-单人设置)
- [5. 管理员白名单](#5-管理员白名单)
- [6. 玩家白名单](#6-玩家白名单)
- [7. 跨服数据传输](#7-跨服数据传输)
  - [7.1 ARK数据设置](#71-ark数据设置)
  - [7.2 集群文件与多服务器](#72-集群文件与多服务器)
- [8. 动态配置 (DynamicConfig)](#8-动态配置-dynamicconfig)
- [附录: 快速参考](#附录-快速参考)

---

## 1. 命令行参数

### 启动命令格式

```
ArkAscendedServer.exe <地图名> [?选项=值]... [-选项[=值]]
```

### Windows 启动示例

```bash
ArkAscendedServer.exe TheIsland_WP?listen?MaxPlayers=70?ServerPassword=mypass?Port=7777?QueryPort=27015
ArkAscendedServer.exe TheIsland_WP?listen?ActiveMods=123456789,987654321
ArkAscendedServer.exe TheIsland_WP?listen -NoBattlEye -WinLiveMaxPlayers=70
```

### ASA 可用地图 (Level Name)

| 地图名称 | Level Name |
|----------|-----------|
| The Island | `TheIsland_WP` |
| The Center | `TheCenter_WP` |
| Scorched Earth | `ScorchedEarth_WP` |
| Ragnarok | `Ragnarok_WP` |
| Aberration | `Aberration_WP` |
| Extinction | `Extinction_WP` |
| Valguero | `Valguero_WP` |
| Astraeos | `Astraeos_WP` |
| Lost Colony | `LostColony_WP` |
| Club ARK | `BobsMissions_WP` |

### 常用命令行选项

| 选项 | 默认值 | 类型 | 说明 |
|------|--------|------|------|
| `?Port=7777` | 7777 | integer | UDP 游戏通信端口 |
| `?QueryPort=27015` | 27015 | integer | Steam 服务器查询端口 |
| `?listen` | - | flag | 启用监听服务器模式 |
| `?ServerPassword=xxx` | 空 | string | 玩家连接密码 |
| `?ServerAdminPassword=xxx` | 空 | string | 管理员密码 (enablecheats) |
| `?SessionName=xxx` | ARK #随机 | string | 服务器显示名称 |
| `?ActiveMods=ID1,ID2` | 空 | list | 模组ID列表 (逗号分隔) |
| `-NoBattlEye` | - | flag | 禁用 BattlEye |
| `-WinLiveMaxPlayers=N` | 70 | integer | ASA专用: 最大玩家数 |
| `-server` | - | flag | 专用服务器模式 |
| `-log` | - | flag | 启用日志输出 |
| `-NoTransferFromDownloading` | - | flag | 禁止数据传输下载 |
| `-ActiveEvent=<name>` | - | string | 启用活动事件 |
| `-ForceAllowCaveFlyers` | - | flag | 允许洞穴飞行 |
| `-NoWildBabies` | - | flag | 禁止野生婴儿刷新 |

---

## 2. GameUserSettings.ini

**路径**: `ShooterGame/Saved/Config/WindowsServer/GameUserSettings.ini`

### 2.1 [ServerSettings]

> 主要服务器设置，包含游戏玩法倍率、功能开关等

#### 通用设置 (34项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowHideDamageSourceFromLogs` | 允许隐藏日志中的伤害来源 | True | boolean | GameUserSettings.ini + 命令行 | False, shows the damage sources in tribe logs. | False，显示部落日志中的伤害来源。 |
| `AllowHitMarkers` | 允许命中标记 | True | boolean | GameUserSettings.ini + 命令行 | False, disables optional markers for ranged attacks. | False，禁用远程攻击的可选标记。 |
| `AllowMultipleAttachedC4` | 允许多个C4附着 | False | boolean | GameUserSettings.ini + 命令行 | True, allows to attach more than one C4 per creature. | 允许每个生物附加多个C4。 |
| `CustomLiveTuningUrl` | 自定义实时调优URL | - | string | GameUserSettings.ini + 命令行 | with a URLDirect link to the live tuning file. | 直接链接到实时调优文件的URL。 |
| `DestroyTamesOverTheSoftTameLimit` | 超过软驯服限制时销毁 | False | boolean | GameUserSettings.ini + 命令行 | above the Soft Tame Server Limit will be marked “For Cryo” and display an icon and a timer indicating how soon they need to be cryopodded before they are automatically destroyed. Dinos marked and dinos destroyed by this system will be logged in the t | above the Soft Tame Server Limit will be marked “For Cryo” and display an icon and a timer indicating how soon they need to be cryopodded before they are automatically destroyed. |
| `DifficultyOffset` | 难度偏移值 | 1.0 | float | GameUserSettings.ini + 命令行 | the difficulty level. | 难度等级。 |
| `DisableWeatherFog` | 禁用天气雾 | False | boolean | GameUserSettings.ini + 命令行 | True, disables fog. | True，禁用雾效果。 |
| `globalVoiceChat` | 全局语音聊天 | False | boolean | GameUserSettings.ini + 命令行 | True, voice chat turns global. | True，语音聊天变为全局。 |
| `MaxTrainCars` | 最大火车车厢数 | 8 | integer | GameUserSettings.ini + 命令行 | the maximum amount of carts a train cave have. | 火车可拥有的最大车厢数量。 |
| `NonPermanentDiseases` | 疾病非永久化 | False | boolean | GameUserSettings.ini + 命令行 | True, makes permanent diseases not permanent. Players will lose them if on re-spawn. | True，使永久性疾病变为非永久。玩家重生后将失去疾病。 |
| `OxygenSwimSpeedStatMultiplier` | 氧气游泳速度属性倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | this to set how swim speed is multiplied by level spent in oxygen. The value was reduced by 80% in 256.0. | 设置游泳速度如何乘以氧气等级。在256.0版本中数值降低了80%。 |
| `PlatformSaddleBuildAreaBoundsMultiplier` | 平台鞍建造区域范围倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the number allows structures being placed further away from the platform. | 允许将结构放置在距离平台鞍更远的位置。 |
| `PreventDiseases` | 预防疾病 | False | boolean | GameUserSettings.ini + 命令行 | True, completely diseases on the server. Thus far just Swamp Fever. | True，完全预防服务器上的疾病。目前仅限沼泽热。 |
| `ProximityChat` | 近距离聊天 | False | boolean | GameUserSettings.ini + 命令行 | True, only players near each other can see their chat messages | True，只有彼此靠近的玩家才能看到聊天消息。 |
| `RCONPort` | RCON端口 | 27020 | integer | GameUserSettings.ini + 命令行 | the optional TCP RCON Port. See Dedicated server setup | 可选的TCP RCON端口。 |
| `RCONServerGameLogBuffer` | RCON服务器游戏日志缓冲区 | 600.0 | float | GameUserSettings.ini + 命令行 | how many lines of game logs are send over the RCON. Note: despite being coded as a float it's suggested to treat it as integer. | 通过RCON发送的游戏日志行数。建议视为整数处理。 |
| `ServerCrosshair` | 服务器十字准线 | True | boolean | GameUserSettings.ini + 命令行 | False, disables the Crosshair on your server. | False，禁用服务器上的十字准线。 |
| `ServerForceNoHUD` | 服务器强制无HUD | False | boolean | GameUserSettings.ini + 命令行 | True, HUD is always disabled for non-tribe owned NPCs. | True，非部落拥有的NPC的HUD始终禁用。 |
| `ServerHardcore` | 服务器硬核模式 | False | boolean | GameUserSettings.ini + 命令行 | True, enables Hardcore mode (player characters revert to level 1 upon death) | True，启用硬核模式（玩家角色死亡后恢复到1级）。 |
| `ShowFloatingDamageText` | 显示浮动伤害文本 | False | boolean | GameUserSettings.ini + 命令行 | True, enables RPG-style popup damage text mode. | True，启用RPG风格的弹出伤害文本模式。 |
| `UseAstraeosTraversalBuff` | 使用Astraeos传送Buff | True | boolean | GameUserSettings.ini | True, enables the biome teleport in Astraeos when holding .mw-parser-output .key{display:inline-block;white-space:nowrap}.mw-parser-output .key kbd{padding:0.1em 0.6em 0.1em 0.6em;margin-right:2px;font-size:85%;font-family:inherit;font-style:normal;b | True，启用Astraeos中的生物群落传送。 |
| `YoungIceFoxDeathCooldown` | 幼年冰狐死亡冷却时间 | 3600 | float | GameUserSettings.ini + 命令行 | the cooldown for Veilwyn to reappear after taking fatal damage (in seconds), default is set to 1 hour. Must be greater than 0. | Veilwyn受到致命伤害后重新出现的冷却时间（秒），默认1小时。必须大于0。 |
| `noTributeDownloads` | 禁止贡品下载 | False | boolean | GameUserSettings.ini + 命令行 | True, prevents CrossArk-data downloads inCross-ARK Data Transfer. | True，禁止跨ARK数据传输中的数据下载。 |
| `PreventDownloadSurvivors` | 禁止下载幸存者 | False | boolean | GameUserSettings.ini + 命令行 | True, prevents survivors download from ARK Data in Cross-ARK Data Transfer. | True，禁止从ARK数据下载幸存者。 |
| `PreventUploadSurvivors` | 禁止上传幸存者 | False | boolean | GameUserSettings.ini + 命令行 | True, prevents survivors upload to ARK Data in Cross-ARK Data Transfer. | True，禁止向ARK数据上传幸存者。 |
| `BadWordListURL` | 违禁词列表URL | : "http://arkdedicated.com/badwords.txt"  : "http://cdn2.arkdedicated.com/asa/badwords.txt" | string | GameUserSettings.ini | with a URLAdd the URL to hosting your own bad words list. Note: on  ARK: Survival Evolved servers only the HTTP protocol is supported (an HTTPS URL will not work). | 托管自定义违禁词列表的URL。注意：ARK服务器仅支持HTTP协议。 |
| `BadWordWhiteListURL` | 违禁词白名单URL | : "http://arkdedicated.com/goodwords.txt"  : "http://cdn2.arkdedicated.com/asa/goodwords.txt" | string | GameUserSettings.ini | with a URLAdd the URL to hosting your own good words list. Note: on  ARK: Survival Evolved servers only the HTTP protocol is supported (an HTTPS URL will not work). | 托管自定义白名单词汇列表的URL。注意：ARK服务器仅支持HTTP协议。 |
| `BloodforgeReinforceExtraDurability` | 血锻强化额外耐久度 | 0.3 | float | GameUserSettings.ini | Default value: 0.3Value type: float | 默认: 0.3类型: float |
| `BloodforgeReinforceSpeedMultiplier` | 血锻强化速度倍率 | 0.1 | float | GameUserSettings.ini | Default value: 0.1Value type: float | 默认: 0.1类型: float |
| `MaxActiveOutposts` | 最大活跃前哨站数 | - | integer | GameUserSettings.ini | Value type: integer | 类型: integer |
| `MaxActiveCityOutposts` | 最大城市前哨站数 | - | integer | GameUserSettings.ini | Value type: integer | 类型: integer |
| `OutpostSigilRewardMultiplier` | 前哨站印记奖励倍率 | 1.0 | float | GameUserSettings.ini | the scaling factor for sigil rewards from outpost missions. Higher values increase the number of sigils rewarded. | 前哨站任务印记奖励的缩放因子。数值越高，奖励印记越多。 |
| `AutoRestartIntervalSeconds` | 自动重启间隔(秒) | Unknown | float | GameUserSettings.ini + 命令行 | the time (in seconds) after which the server will automatically restart. Undocumented by Wildcard. (Appears to shut off the server instead of restarting properly) | 服务器自动重启的时间（秒）。未被Wildcard文档记录。 |
| `UseCharacterTracker` | 启用角色追踪器 | False | boolean | GameUserSettings.ini + 命令行 | to enable character tracking. Alternatively, this option can be configured with -disableCharacterTracker argument in the command line (note that the argument from command line has priority over the value set in GameUserSettings.ini). Undocumented by  | 启用角色追踪。可通过命令行-disableCharacterTracker参数配置。 |

#### 服务器管理与安全 (22项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `RCONEnabled` | 启用RCON | False | boolean | GameUserSettings.ini + 命令行 | True, enables the RCON protocol support. | 启用RCON协议支持。 |
| `SpectatorPassword` | 观察者密码 | - | string | GameUserSettings.ini + 命令行 | the password to join as spectator. | 以观察者身份加入的密码。 |
| `AllowedCheatersURL` | 允许作弊者URL | - | string | GameUserSettings.ini | URL to a list of allowed admin accounts. | 允许的管理员账户列表URL。 |
| `LogChatMessages` | 记录聊天消息 | False | boolean | GameUserSettings.ini + 命令行 | True, logs chat messages to a file. | 将聊天消息记录到文件。 |
| `ChatLogFileSplitIntervalSeconds` | 聊天日志分割间隔(秒) | 86400 | integer | GameUserSettings.ini | interval in seconds for splitting chat log files. | 聊天日志文件分割间隔（秒）。 |
| `ChatLogFlushIntervalSeconds` | 聊天日志刷新间隔(秒) | 86400 | integer | GameUserSettings.ini | interval in seconds for flushing chat log to disk. | 聊天日志刷新到磁盘的间隔（秒）。 |
| `ChatLogMaxAgeInDays` | 聊天日志最大保存天数 | 5 | integer | GameUserSettings.ini | maximum age of chat log files in days. | 聊天日志文件最大保存天数。 |
| `EnableFullDump` | 启用完整转储 | False | boolean | GameUserSettings.ini + 命令行 | True, enables full memory dump on crash. | 崩溃时启用完整内存转储。 |
| `DisableTimestampVerification` | 禁用时间戳验证 | False | boolean | GameUserSettings.ini + 命令行 | True, disables timestamp verification. | 禁用时间戳验证。 |
| `ServerEnableMeshChecking` | 启用Mesh检测 | False | boolean | GameUserSettings.ini + 命令行 | True, enables mesh detection system. | 启用Mesh检测系统。 |
| `EnableMeshBitingProtection` | 启用Mesh咬伤保护 | True | boolean | GameUserSettings.ini + 命令行 | True, enables protection against mesh biting exploits. | 启用防止Mesh咬伤漏洞的保护。 |
| `DontRestoreBackup` | 不恢复备份 | False | boolean | GameUserSettings.ini + 命令行 | True, prevents automatic backup restoration. | 防止自动恢复备份。 |
| `AllowSharedConnections` | 允许共享连接 | False | boolean | GameUserSettings.ini + 命令行 | True, allows shared connections. | 允许共享连接。 |
| `UseExclusiveList` | 使用独占列表 | False | boolean | GameUserSettings.ini + 命令行 | True, uses exclusive join list. | 使用独占加入列表。 |
| `AlwaysNotifyPlayerLeft` | 总是通知玩家离开 | False | boolean | GameUserSettings.ini + 命令行 | True, always notifies when a player leaves. | 玩家离开时总是通知。 |
| `ListenServerTetherDistanceMultiplier` | 监听服务器束缚距离倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | multiplier for tether distance in non-dedicated servers. | 非专用服务器束缚距离倍率。 |
| `EnableAFKKickPlayerCountPercent` | 启用AFK踢出玩家百分比 | 0.0 | float | GameUserSettings.ini + 命令行 | percentage of players that triggers AFK kick. 0 disables. | 触发AFK踢出的玩家百分比，0禁用。 |
| `MaxHexagonsPerCharacter` | 每角色最大六角币 | 2000000000 | integer | GameUserSettings.ini + 命令行 | maximum hexagons per character. | 每个角色最大六角币数量。 |
| `HexagonRewardMultiplier` | 六角币奖励倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | multiplier for hexagon rewards from missions. | 任务六角币奖励倍率。 |
| `AllowTekSuitPowersInGenesis` | 允许Genesis中使用泰克套装 | False | boolean | GameUserSettings.ini + 命令行 | True, allows tek suit powers in Genesis maps. | 允许在Genesis地图中使用泰克套装能力。 |
| `UseOptimizedHarvestingHealth` | 使用优化采集生命值 | False | boolean | GameUserSettings.ini + 命令行 | True, uses optimized harvesting health system. | 使用优化的采集生命值系统。 |
| `AllowIntegratedSPlusStructures` | 允许集成S+建筑 | True | boolean | GameUserSettings.ini + 命令行 | True, allows integrated S+ structures. | 允许集成Structures Plus建筑。 |
| `AllowMultipleTamedUnicorns` | 允许多个驯服独角兽 | False | boolean | GameUserSettings.ini + 命令行 | True, allows multiple tamed unicorns. | 允许驯服多个独角兽。 |
| `AllowFlyingStaminaRecovery` | 允许飞行耐力恢复 | False | boolean | GameUserSettings.ini + 命令行 | True, allows stamina recovery while flying. | 允许飞行时恢复耐力。 |
| `AdjustableMutagenSpawnDelayMultiplier` | 可调节诱变剂生成延迟倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the multiplier for adjustable mutagen spawn delay. | 可调节诱变剂生成延迟倍率。 |
| `BaseTemperatureMultiplier` | 基础温度倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the multiplier for base temperature. | 基础温度倍率。 |
| `CustomDynamicConfigUrl` | 自定义动态配置URL | - | string | GameUserSettings.ini + 命令行 | URL to the dynamic configuration file. | 动态配置文件的URL。 |
| `ForceFlyerExplosives` | 强制飞行爆炸物 | False | boolean | GameUserSettings.ini + 命令行 | True, allows flyers to carry explosives. | 允许飞行生物携带爆炸物。 |
| `UseFjordurTraversalBuff` | 使用Fjordur传送Buff | False | boolean | GameUserSettings.ini + 命令行 | True, enables the biome teleport in Fjordur. | 启用Fjordur生物群落传送。 |
| `GMaxFlameThrowerServerTicksPerFrame` | 火焰喷射器每帧Tick | 5 | integer | GameUserSettings.ini + 命令行 | controls the tick rate of Flamethrower per server tick. Higher values may cause performance issues. Undocumented. | 控制火焰喷射器每服务器Tick的Tick速率，更高值可能导致性能问题。 |
| `GUseServerNetSpeedCheck` | 使用服务器网络速度检查 | False | boolean | GameUserSettings.ini + 命令行 | prevents players from accumulating too much movements data per server tick. Enabled on official clusters. Undocumented. | 防止玩家每Tick累积过多移动数据，官方集群启用。 |
| `OverrideSpawnLimitPercentage` | 覆盖生成限制百分比 | N/A | float | GameUserSettings.ini + 命令行 | overrides the spawn limit percentage for creatures. | 覆盖生物生成限制百分比。 |
| `PreventDinoTameClassNames` | 防止指定类名驯服 | N/A | string | GameUserSettings.ini + 命令行 | prevents taming of specific creatures by classname. | 防止驯服指定类名的生物。 |

#### PvE自动切换 (2项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AutoPvEStartTimeSeconds` | 自动PvE开始时间(秒) | 0.0 | float | GameUserSettings.ini + 命令行 | the time in seconds for auto PvE to start. Requires AutoPvEStopTimeSeconds. | 自动PvE开始时间（秒），需要设置AutoPvEStopTimeSeconds。 |
| `AutoPvEStopTimeSeconds` | 自动PvE停止时间(秒) | 0.0 | float | GameUserSettings.ini + 命令行 | the time in seconds for auto PvE to stop. Requires AutoPvEStartTimeSeconds. | 自动PvE停止时间（秒），需要设置AutoPvEStartTimeSeconds。 |

#### 服务器重启与自动维护 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `ServerAutoForceRespawnWildDinosInterval` | 自动刷新野生恐龙间隔 | 0.0 | float | GameUserSettings.ini + 命令行 | interval in seconds for auto force respawning wild dinos. 0 disables. | 自动刷新野生恐龙的间隔（秒），0禁用。 |
| `AutoDestroyOldStructuresMultiplier` | 自动销毁旧建筑倍率 | 0.0 | float | GameUserSettings.ini + 命令行 | multiplier for auto destroying old structures. | 自动销毁旧建筑的倍率。 |
| `AutoDestroyDecayedDinos` | 自动销毁腐烂恐龙 | False | boolean | GameUserSettings.ini + 命令行 | True, auto destroys decayed dinos. | 自动销毁腐烂的恐龙。 |
| `DestroyUnconnectedWaterPipes` | 销毁未连接水管 | False | boolean | GameUserSettings.ini + 命令行 | True, destroys unconnected water pipes. | 销毁未连接的水管。 |

#### 生物设置 (37项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowRaidDinoFeeding` | 允许突袭恐龙喂食 | False | boolean | GameUserSettings.ini + 命令行 | True, allows Titanosaurs to be permanently tamed (namely allow them to be fed). Note: in The Island only spawns a maximum of 3 Titanosaurs, so 3 tamed ones should ultimately block any more ones from spawning. | True，允许泰坦龙被永久驯服（即允许喂食）。 |
| `DinoCharacterFoodDrainMultiplier` | 恐龙食物消耗倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for creatures' food consumption. Higher values increase food consumption (creatures get hungry faster). It also affects the taming-times. | 生物食物消耗的缩放因子。数值越高，食物消耗越快（生物更快饥饿）。也影响驯服时间。 |
| `DinoCharacterHealthRecoveryMultiplier` | 恐龙生命恢复倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for creatures' health recovery. Higher values increase the recovery rate (creatures heal faster). | 生物生命恢复的缩放因子。数值越高，恢复速度越快。 |
| `DinoCharacterStaminaDrainMultiplier` | 恐龙耐力消耗倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for creatures' stamina consumption. Higher values increase stamina consumption (creatures get tired faster). | 生物耐力消耗的缩放因子。数值越高，耐力消耗越快。 |
| `DinoDamageMultiplier` | 恐龙伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the damage wild creatures deal with their attacks. The default value 1 provides normal damage. Higher values increase damage. Lower values decrease it. | 野生生物攻击伤害的缩放因子。默认值1为正常伤害。 |
| `DinoResistanceMultiplier` | 恐龙抗性倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the resistance to damage wild creatures receive when attacked. The default value 1 provides normal damage. Higher values decrease resistance, increasing damage per attack. Lower values increase it, reducing damage per attack. A | 野生生物受到伤害时的抗性缩放因子。默认值1为正常伤害。数值越高抗性越低。 |
| `DisableDinoDecayPvE` | 禁用PvE恐龙衰减 | False | boolean | GameUserSettings.ini + 命令行 | True, disables the creature decay in PvE mode. Note: after patch 273.691, in PvE mode the creature auto-unclaim after decay period has been disabled. | True，禁用PvE模式下的生物衰减。 |
| `MaxPersonalTamedDinos` | 每部落驯服恐龙上限 | 0 | integer | GameUserSettings.ini + 命令行 | a per-tribe creature tame limit (500 on official PvE servers, 300 in official PvP servers). The default value of 0 disables such limit. | 每部落生物驯服上限（官方PvE服500，PvP服300）。默认值0表示无限制。 |
| `MaxTamedDinos` | 服务器驯服恐龙上限 | 5000.0 | float | GameUserSettings.ini + 命令行 | the maximum number of tame creatures on a server, this is a global cap. Note: although at code level it is defined as a floating-point number, it is suggested to use an integer instead. | 服务器上驯服生物的最大数量（全局上限）。建议使用整数。 |
| `MaxTamedDinos_SoftTameLimit` | 服务器软驯服限制 | 5000 | integer | GameUserSettings.ini + 命令行 | the server-wide soft tame limit. See DestroyTamesOverTheSoftTameLimit for more info. | 服务器范围的软驯服限制。 |
| `MaxTamedDinos_SoftTameLimit_CountdownForDeletionDuration` | 软驯服限制销毁倒计时(秒) | 604800 | integer | GameUserSettings.ini + 命令行 | the time (in seconds) for tame to get destroyed. See DestroyTamesOverTheSoftTameLimit for more info. | 超出软驯服限制后生物被销毁的时间（秒）。 |
| `MaxTributeDinos` | 最大上传恐龙数 | 20 | integer | GameUserSettings.ini + 命令行 | for uploaded creatures. Any value less than default will be reverted. Note: Some player claimed maximum 273 to be safe cap and more will corrupt profile/cluster and lead to lose of all stored creatures but it need to be checked | 上传生物的最大数量。低于默认值将被恢复。 |
| `PreventSpawnAnimations` | 禁用重生动画 | False | boolean | GameUserSettings.ini + 命令行 | True, player characters (re)spawn without the wake-up animation. | True，玩家角色重生时无唤醒动画。 |
| `PvEDinoDecayPeriodMultiplier` | PvE恐龙衰减周期倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | PvE auto-decay time multiplier. Requires DisableDinoDecayPvE=false in GameUserSettings.ini or ?DisableDinoDecayPvE=false in command line to work. | PvE生物自动衰减时间倍率。需要DisableDinoDecayPvE=false。 |
| `PvPDinoDecay` | PvP恐龙衰减 | False | boolean | GameUserSettings.ini + 命令行 | True, enables creatures' decay in PvP while the Offline Raid Prevention is active. | True，启用PvP模式下离线突袭保护期间的生物衰减。 |
| `RaidDinoCharacterFoodDrainMultiplier` | 突袭恐龙食物消耗倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | how quickly the food drains on such "raid dinos" (e.g.: Titanosaurus) | 突袭恐龙（如泰坦龙）的食物消耗速度。 |
| `ResourcesRespawnPeriodMultiplier` | 资源重生周期倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the re-spawn rate for resource nodes (trees, rocks, bushes, etc.). Lower values cause nodes to re-spawn more frequently. | 资源节点（树木、岩石、灌木等）重生速率的缩放因子。数值越低重生越频繁。 |
| `CrossARKAllowForeignDinoDownloads` | 跨服允许下载外来恐龙 | False | boolean | GameUserSettings.ini + 命令行 | True, enables non-native creatures tribute download on Aberration. | True，启用在畸变地图下载非本地生物。 |
| `PreventDownloadDinos` | 禁止下载恐龙 | False | boolean | GameUserSettings.ini + 命令行 | True, prevents creatures download from ARK Data in Cross-ARK Data Transfer. | True，禁止从ARK数据下载生物。 |
| `PreventUploadDinos` | 禁止上传恐龙 | False | boolean | GameUserSettings.ini + 命令行 | True, prevents creatures upload to ARK Data in Cross-ARK Data Transfer. | True，禁止向ARK数据上传生物。 |
| `AllowRidingDinosInsideBunkers` | 允许在地堡内骑乘恐龙 | True | boolean | GameUserSettings.ini | Default value: TrueValue type: boolean | 默认: True，允许在地堡内骑乘恐龙。 |
| `AllowDinoAIInsideBunkers` | 允许地堡内恐龙AI | True | boolean | GameUserSettings.ini | Default value: TrueValue type: boolean | 默认: True，允许地堡内恐龙AI运作。 |
| `CryoHospitalHoursToRegenFood` | 低温舱医院食物恢复时间(小时) | 24.0 | float | GameUserSettings.ini | Default value: 24.0Value type: float | 默认: 24.0，低温舱医院食物恢复所需小时数。 |
| `CryoHospitalHoursToDrainTorpor` | 低温舱医院昏迷恢复时间(小时) | 1.0 | float | GameUserSettings.ini | Default value: 1.0Value type: float | 默认: 1.0，低温舱医院昏迷值恢复所需小时数。 |
| `TamedDinoCharacterFoodDrainMultiplier` | 驯服恐龙食物消耗倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for tamed creatures' food consumption. | 驯服生物食物消耗倍率。 |
| `TamedDinoDamageMultiplier` | 驯服恐龙伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for tamed creatures' damage. | 驯服生物伤害倍率。 |
| `TamedDinoResistanceMultiplier` | 驯服恐龙抗性倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for tamed creatures' resistance. | 驯服生物抗性倍率。 |
| `TamedDinoTorporDrainMultiplier` | 驯服恐龙昏迷消耗倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for tamed creatures' torpor drain. | 驯服生物昏迷值消耗倍率。 |
| `WildDinoTorporDrainMultiplier` | 野生恐龙昏迷消耗倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for wild creatures' torpor drain. | 野生生物昏迷值消耗倍率。 |
| `DinoHarvestingDamageMultiplier` | 恐龙采集伤害倍率 | 3.2 | float | GameUserSettings.ini + 命令行 | the scaling factor for dino harvesting damage. | 恐龙采集伤害倍率。 |
| `DinoTurretDamageMultiplier` | 恐龙炮塔伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for dino turret damage. | 恐龙炮塔伤害倍率。 |
| `DinoCountMultiplier` | 恐龙数量倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for wild dino count. | 野生恐龙数量倍率。 |
| `PassiveTameIntervalMultiplier` | 被动驯服间隔倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the interval multiplier for passive taming. | 被动驯服间隔倍率。 |
| `UnicornSpawnInterval` | 独角兽生成间隔 | 24 | integer | GameUserSettings.ini + 命令行 | the interval in hours for unicorn spawning. | 独角兽生成间隔（小时）。 |
| `FreezeReaperPregnancy` | 冻结Reaper怀孕 | False | boolean | GameUserSettings.ini + 命令行 | True, freezes Reaper pregnancy. | 冻结Reaper怀孕状态。 |

#### ASA新增功能 (15项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowCryoFridgeOnSaddle` | 允许冷冰箱放鞍上 | False | boolean | GameUserSettings.ini + 命令行 | True, allows cryofridges to be built on platform saddles and rafts. | True，允许在平台鞍和木筏上放置低温冰箱。 |
| `ArmadoggoDeathCooldown` | Armadoggo死亡冷却(秒) | 3600 | float | GameUserSettings.ini + 命令行 | the cooldown for Armadoggo to reappear after taking fatal damage (in seconds), default is set to 1 hour. Must be greater than 0. | Armadoggo受到致命伤害后重新出现的冷却时间（秒），默认1小时。 |
| `CosmoWeaponAmmoReloadAmount` | Cosmo武器弹药装填量 | 1 | float | GameUserSettings.ini + 命令行 | how much ammo is given as the Cosmo's webslinger reloads over time. | Cosmo的网枪随时间装填的弹药量。 |
| `DisableCryopodEnemyCheck` | 禁用低温舱敌人检测 | False | boolean | GameUserSettings.ini + 命令行 | True, allows cryopods to be used while enemies are nearby. | True，允许在敌人附近使用低温舱。 |
| `DisableCryopodFridgeRequirement` | 禁用低温舱冰箱需求 | False | boolean | GameUserSettings.ini + 命令行 | True, allows cryopods to be used without needing to be in range of a powered cryofridge. | True，允许无需在低温冰箱附近即可使用低温舱。 |
| `ForceGachaUnhappyInCaves` | 强制嘎查在洞穴中不高兴 | True | boolean | GameUserSettings.ini + 命令行 | True, Gachas will become unhappy within caves. | True，嘎查在洞穴内会变得不高兴。 |
| `ImplantSuicideCD` | 植入体自杀冷却(秒) | 28800 | float | GameUserSettings.ini | the time (in seconds) a player must wait between 2 uses of the implant's "Respawn" feature. | 玩家两次使用植入体"重生"功能之间的等待时间（秒）。 |
| `MaxCosmoWeaponAmmo` | Cosmo最大武器弹药量 | -1 | float | GameUserSettings.ini + 命令行 | will make the maximum ammo amount for the Cosmo's webslinger to a set number instead of it scaling with the Cosmo's level. The default of -1 will enable scaling with level. | 设置Cosmo网枪的最大弹药量，-1为随等级缩放。 |
| `AllowBunkersInPreventionZones` | 允许在防护区建造地堡 | False | boolean | GameUserSettings.ini | Default value: FalseValue type: boolean | 默认: False，允许在防护区域建造地堡。 |
| `MinDistanceBetweenBunkers` | 地堡最小间距 | 3000.0 | float | GameUserSettings.ini | Default value: 3000.0Value type: float | 默认: 3000.0，地堡之间的最小距离。 |
| `EnemyAccessBunkerHPThreshold` | 敌人进入地堡生命值阈值 | 0.25 | float | GameUserSettings.ini | Default value: 0.25Value type: float | 默认: 0.25，敌人可进入地堡的生命值阈值。 |
| `BunkerUnderHPThresholdDmgMultiplier` | 地堡低于阈值伤害倍率 | 0.05 | float | GameUserSettings.ini | Default value: 0.05Value type: float | 默认: 0.05，地堡生命值低于阈值时的伤害倍率。 |
| `CryoHospitalHoursToRegenHP` | 低温舱医院生命恢复时间(小时) | 1.0 | float | GameUserSettings.ini | Default value: 1.0Value type: float | 默认: 1.0，低温舱医院生命值恢复所需小时数。 |
| `CryoHospitalMatingCooldownReduction` | 低温舱医院交配冷却减少 | 2.0 | float | GameUserSettings.ini | Default value: 2.0Value type: float | 默认: 2.0，低温舱医院减少的交配冷却时间。 |
| `UpdateAllowedCheatersInterval` | 更新允许作弊者列表间隔(秒) | 600.0 | float | GameUserSettings.ini + 命令行 | in seconds at which the remote admin list linked by AllowedCheatersURL is queried for updates. Any value less than 3.0 will be reverted to 3.0. Undocumented by Wildcard. | AllowedCheatersURL远程管理员列表查询更新的间隔（秒）。低于3.0将被恢复。 |

#### 低温舱削弱设置 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `EnableCryopodNerf` | 启用低温舱削弱 | False | boolean | GameUserSettings.ini + 命令行 | True, enables cryopod nerf system. | 启用低温舱削弱系统。 |
| `CryopodNerfDamageMult` | 低温舱削弱伤害倍率 | 0.01 | float | GameUserSettings.ini + 命令行 | the damage multiplier applied to cryoed dinos after uncryoing. 0.01 means 99% damage removed. | 低温舱恐龙解除后的伤害倍率，0.01表示移除99%伤害。 |
| `CryopodNerfDuration` | 低温舱削弱持续时间 | 0.0 | float | GameUserSettings.ini + 命令行 | the duration in seconds for cryopod nerf effect. | 低温舱削弱效果持续时间（秒）。 |
| `CryopodNerfIncomingDamageMultPercent` | 低温舱削弱受到伤害百分比 | 0.0 | float | GameUserSettings.ini + 命令行 | the incoming damage multiplier percentage for cryoed dinos. | 低温舱恐龙受到伤害的倍率百分比。 |
| `EnableCryoSicknessPVE` | 启用PvE低温舱疾病 | False | boolean | GameUserSettings.ini + 命令行 | True, enables cryo sickness in PvE mode. | 在PvE模式中启用低温舱疾病。 |

#### 火山与环境事件 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `EnableVolcano` | 启用火山 | True | boolean | GameUserSettings.ini + 命令行 | True, enables volcano events. | 启用火山事件。 |
| `VolcanoIntensity` | 火山强度 | 1 | integer | GameUserSettings.ini + 命令行 | the intensity of volcano eruptions. | 火山喷发强度。 |
| `VolcanoInterval` | 火山间隔 | 0 | integer | GameUserSettings.ini + 命令行 | the interval between volcano events. 0 uses default. | 火山事件间隔，0使用默认值。 |
| `ExtinctionEventTimeInterval` | 灭绝事件时间间隔 | - | integer | GameUserSettings.ini + 命令行 | the time interval for extinction events in seconds. | 灭绝事件时间间隔（秒）。 |

#### 繁殖与成长 (14项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowAnyoneBabyImprintCuddle` | 允许任何人照料婴儿 | False | boolean | GameUserSettings.ini + 命令行 | True, allows anyone to "take care" of a baby creatures (cuddle etc.), not just whomever imprinted on it. | True，允许任何人照料婴儿生物（拥抱等），不仅限于印记绑定者。 |
| `AllowThirdPersonPlayer` | 允许第三人称视角 | True | boolean | GameUserSettings.ini + 命令行 | False, disables third person camera allowed by default on all dedicated servers. | False，禁用专用服务器默认的第三人称视角。 |
| `DisableImprintDinoBuff` | 禁用印记恐龙加成 | False | boolean | GameUserSettings.ini + 命令行 | True, disables the creature imprinting player Stat Bonus. Where whomever specifically imprinted on the creature, and raised it to have an Imprinting Quality, gets extra Damage/Resistance buff. | True，禁用生物印记玩家属性加成。 |
| `DontAlwaysNotifyPlayerJoined` | 不通知玩家加入 | False | boolean | GameUserSettings.ini + 命令行 | True, globally disables player joins notifications. | True，全局禁用玩家加入通知。 |
| `KickIdlePlayersPeriod` | 踢出挂机玩家时间(秒) | 3600.0 | float | GameUserSettings.ini + 命令行 | in seconds after which characters that have not moved or interacted will be kicked (if -EnableIdlePlayerKick as command line parameter is set). Note: although at code level it is defined as a floating-point number, it is suggested to use an integer i | 未移动或交互的角色被踢出的等待时间（秒）。需要-EnableIdlePlayerKick参数。 |
| `PlayerCharacterFoodDrainMultiplier` | 玩家食物消耗倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for player characters' food consumption. Higher values increase food consumption (player characters get hungry faster). | 玩家食物消耗的缩放因子。数值越高，食物消耗越快。 |
| `PlayerCharacterHealthRecoveryMultiplier` | 玩家生命恢复倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for player characters' health recovery. Higher values increase the recovery rate (player characters heal faster). | 玩家生命恢复的缩放因子。数值越高，恢复速度越快。 |
| `PlayerCharacterStaminaDrainMultiplier` | 玩家耐力消耗倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for player characters' stamina consumption. Higher values increase stamina consumption (player characters get tired faster). | 玩家耐力消耗的缩放因子。数值越高，耐力消耗越快。 |
| `PlayerCharacterWaterDrainMultiplier` | 玩家水分消耗倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for player characters' water consumption. Higher values increase water consumption (player characters get thirsty faster). | 玩家水分消耗的缩放因子。数值越高，水分消耗越快。 |
| `PlayerDamageMultiplier` | 玩家伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the damage players deal with their attacks. The default value 1 provides normal damage. Higher values increase damage. Lower values decrease it. | 玩家攻击伤害的缩放因子。默认值1为正常伤害。 |
| `PlayerResistanceMultiplier` | 玩家抗性倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the resistance to damage players receive when attacked. The default value 1 provides normal damage. Higher values decrease resistance, increasing damage per attack. Lower values increase it, reducing damage per attack. A value  | 玩家受到伤害时的抗性缩放因子。默认值1为正常伤害。 |
| `PreventMateBoost` | 禁用配偶加成 | False | boolean | GameUserSettings.ini + 命令行 | True, disables creature mate boosting. | True，禁用生物配偶加成。 |
| `ShowMapPlayerLocation` | 地图显示玩家位置 | True | boolean | GameUserSettings.ini + 命令行 | False, hides each player their own precise position when they view their map. | False，隐藏玩家在地图上的精确位置。 |
| `PlayerHarvestingDamageMultiplier` | 玩家采集伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for player harvesting damage. | 玩家采集伤害倍率。 |
| `UseCorpseLifeSpanMultiplier` | 尸体寿命倍率 | 6.0 | float | GameUserSettings.ini + 命令行 | the multiplier for corpse lifespan. | 尸体寿命倍率。 |

#### 建筑与防御 (32项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowCaveBuildingPvE` | PvE允许洞穴建造 | False | boolean | GameUserSettings.ini | True, allows building in caves when PvE mode is also enabled. Note: no more working in command-line options before patch 241.5. | True，允许在PvE模式下的洞穴中建造。 |
| `AllowCaveBuildingPvP` | PvP允许洞穴建造 | True | boolean | GameUserSettings.ini | False, prevents building in caves when PvP mode is also enabled. | False，禁止在PvP模式下的洞穴中建造。 |
| `AlwaysAllowStructurePickup` | 始终允许建筑拾取 | False | boolean | GameUserSettings.ini + 命令行 | True disables the timer on the quick pick-up system. | True，禁用快速拾取系统的计时器。 |
| `DisableStructureDecayPvE` | 禁用PvE建筑衰减 | False | boolean | GameUserSettings.ini + 命令行 | True, disables the gradual auto-decay of player structures. | True，禁用玩家建筑的自动衰减。 |
| `EnableExtraStructurePreventionVolumes` | 启用额外建筑防护区域 | False | boolean | GameUserSettings.ini + 命令行 | True, disables building in specific resource-rich areas, in particular setup on The Island around the major mountains. | True，禁止在特定资源丰富区域建造。 |
| `ForceAllStructureLocking` | 强制所有建筑锁定 | False | boolean | GameUserSettings.ini + 命令行 | True, will default lock all structures. | True，默认锁定所有建筑。 |
| `IgnoreLimitMaxStructuresInRangeTypeFlag` | 忽略范围内最大建筑限制 | False | boolean | GameUserSettings.ini | True, removes the limit of 150 decorative structures (flags, signs, dermis etc.). | True，移除150个装饰性建筑的限制。 |
| `OverrideStructurePlatformPrevention` | 覆盖建筑平台限制 | False | boolean | GameUserSettings.ini + 命令行 | True, turrets becomes be buildable and functional on platform saddles. Since 247.999 applies on spike structure too. Note: despite patch notes, in ShooterGameServer it's coded OverrideStructurePlatformPrevention with two r. | True，允许在平台鞍上建造和使用炮塔。 |
| `PerPlatformMaxStructuresMultiplier` | 平台最大建筑数倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | value increases (from a percentage scale) max number of items place-able on saddles and rafts. | 增加鞍和木筏上可放置物品的最大数量。 |
| `PvEAllowStructuresAtSupplyDrops` | PvE允许在补给点建造 | False | boolean | GameUserSettings.ini + 命令行 | True, allows building near supply drop points in PvE mode. | True，允许在PvE模式下的补给点附近建造。 |
| `StructurePickupHoldDuration` | 建筑拾取长按时间 | 0.5 | float | GameUserSettings.ini + 命令行 | the quick pick-up hold duration, a value of 0 results in instant pick-up. | 快速拾取的长按持续时间，0为即时拾取。 |
| `StructureResistanceMultiplier` | 建筑抗性倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the resistance to damage structures receive when attacked. The default value 1 provides normal damage. Higher values decrease resistance, increasing damage per attack. Lower values increase it, reducing damage per attack. A val | 建筑受到伤害时的抗性缩放因子。默认值1为正常伤害。 |
| `TheMaxStructuresInRange` | 范围内最大建筑数 | 10500 | integer | GameUserSettings.ini + 命令行 | the maximum number of structures that can be constructed within a certain (currently hard-coded) range. Replaces the old value NewMaxStructuresInRange | 某范围内可建造的最大建筑数量。 |
| `MaxStructuresInRange` | 范围内最大建筑数 | 10500 | integer | GameUserSettings.ini + 命令行 | the maximum number of structures in a certain range. | 某范围内最大建筑数量。 |
| `MaxStructuresInSmallRadius` | 小范围内最大建筑数 | 0 | integer | GameUserSettings.ini + 命令行 | the maximum number of structures in a small radius. 0 disables. | 小范围内最大建筑数量，0禁用。 |
| `MaxStructuresToProcess` | 处理建筑最大数量 | 0 | integer | GameUserSettings.ini + 命令行 | the maximum number of structures to process. 0 disables. | 处理建筑最大数量，0禁用。 |
| `NewMaxStructuresInRange` | 新范围内最大建筑数 | 10500 | integer | GameUserSettings.ini + 命令行 | deprecated, use TheMaxStructuresInRange instead. | 已弃用，请使用TheMaxStructuresInRange。 |
| `MaxPlatformSaddleStructureLimit` | 平台鞍建筑限制 | 75 | integer | GameUserSettings.ini + 命令行 | the maximum number of structures on platform saddles. | 平台鞍上最大建筑数量。 |
| `MaxGateFrameOnSaddles` | 鞍上门框最大数量 | 0 | integer | GameUserSettings.ini + 命令行 | the maximum number of gate frames on saddles. 0 disables. | 鞍上门框最大数量，0禁用。 |
| `AllowDeprecatedStructures` | 允许弃用建筑 | False | boolean | GameUserSettings.ini + 命令行 | True, allows deprecated structures to be placed. | 允许放置已弃用的建筑。 |
| `AllowCrateSpawnsOnTopOfStructures` | 允许补给箱在建筑上生成 | False | boolean | GameUserSettings.ini + 命令行 | True, allows supply crates to spawn on top of structures. | 允许补给箱在建筑顶部生成。 |
| `StructureDamageMultiplier` | 建筑伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for structure damage. | 建筑伤害倍率。 |
| `StructureDamageRepairCooldown` | 建筑伤害修复冷却 | 180 | integer | GameUserSettings.ini + 命令行 | the cooldown in seconds before damaged structures can be repaired. | 受损建筑修复前的冷却时间（秒）。 |
| `LimitTurretsNum` | 炮塔数量限制 | 100 | integer | GameUserSettings.ini + 命令行 | the maximum number of turrets in the area defined by LimitTurretsRange. | LimitTurretsRange范围内最大炮塔数量。 |
| `LimitTurretsRange` | 炮塔限制范围 | 10000.0 | float | GameUserSettings.ini + 命令行 | the area range in which LimitTurretsNum applies. | 炮塔数量限制的范围。 |
| `LimitNonPlayerDroppedItemsCount` | 非玩家掉落物品数量限制 | 0 | integer | GameUserSettings.ini + 命令行 | the maximum number of non-player dropped items. 0 disables. | 非玩家掉落物品最大数量，0禁用。 |
| `LimitNonPlayerDroppedItemsRange` | 非玩家掉落物品范围限制 | 600 | integer | GameUserSettings.ini + 命令行 | the range for non-player dropped items limit. | 非玩家掉落物品限制范围。 |
| `PvEStructureDecayDestructionPeriod` | PvE建筑腐烂销毁周期 | 0 | integer | GameUserSettings.ini + 命令行 | the period for PvE structure decay destruction. 0 disables. | PvE建筑腐烂销毁周期，0禁用。 |
| `PvEStructureDecayPeriodMultiplier` | PvE建筑腐烂周期倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the multiplier for PvE structure decay period. | PvE建筑腐烂周期倍率。 |
| `FastDecayInterval` | 快速腐烂间隔 | 43200 | integer | Game.ini | the interval in seconds for fast decay of unconnected structures. Default is 12 hours. | 未连接建筑快速腐烂间隔（秒），默认12小时。 |
| `FastDecayUnsnappedCoreStructures` | 快速腐烂未连接核心建筑 | False | boolean | GameUserSettings.ini + 命令行 | True, enables fast decay for unsnapped core structures. | 启用未连接核心建筑的快速腐烂。 |
| `OnlyAutoDestroyCoreStructures` | 仅自动销毁核心建筑 | False | boolean | GameUserSettings.ini + 命令行 | True, only auto destroys core structures. | 仅自动销毁核心建筑。 |
| `OnlyDecayUnsnappedCoreStructures` | 仅腐烂未连接核心建筑 | False | boolean | GameUserSettings.ini + 命令行 | True, only decays unsnapped core structures. | 仅腐烂未连接的核心建筑。 |

#### PvP与部落 (22项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowFlyerCarryPvE` | PvE允许飞行抓取 | False | boolean | GameUserSettings.ini + 命令行 | True, allows flying creatures to pick up wild creatures in PvE. | True，允许飞行生物在PvE中抓取野生生物。 |
| `DisablePvEGamma` | 禁用PvE伽马值 | False | boolean | GameUserSettings.ini + 命令行 | True, prevents use of console command "gamma" in PvE mode. | True，禁止在PvE模式下使用gamma控制台命令。 |
| `EnablePvPGamma` | 启用PvP伽马值 | False | boolean | GameUserSettings.ini + 命令行 | True, allows use of console command "gamma" in PvP mode. | True，允许在PvP模式下使用gamma控制台命令。 |
| `PreventOfflinePvP` | 启用离线突袭防护 | False | boolean | GameUserSettings.ini + 命令行 | True, enables the Offline Raiding Prevention (ORP). When all tribe members are logged off, tribe characters, creature and structures become invulnerable. Creature starvation still applies, moreover, characters and creature can still die if drowned. D | True，启用离线突袭防护（ORP）。所有部落成员离线后，角色、生物和建筑变为无敌。 |
| `PreventOfflinePvPInterval` | 离线突袭防护生效延迟(秒) | 0.0 | float | GameUserSettings.ini + 命令行 | to wait before a ORP becomes active for tribe/players and relative creatures/structures (10 seconds in official PvE servers). Note: although at code level it is defined as a floating-point number, it is suggested to use an integer instead. | ORP生效前的等待时间（秒）。 |
| `PreventTribeAlliances` | 禁止部落联盟 | False | boolean | GameUserSettings.ini + 命令行 | True, prevents tribes from creating Alliances. | True，禁止部落创建联盟。 |
| `serverPVE` | 服务器PvE模式 | False | boolean | GameUserSettings.ini + 命令行 | True, disables PvP and enables PvE | True，禁用PvP并启用PvE。 |
| `TribeNameChangeCooldown` | 部落改名冷却时间(分钟) | 15.0 | float | GameUserSettings.ini + 命令行 | in minutes, in between tribe name changes. Official server use a value of 172800.0 (2 days). | 部落名称更改之间的冷却时间（分钟）。 |
| `LimitBunkersPerTribe` | 限制每部落地堡数 | True | boolean | GameUserSettings.ini | Default value: TrueValue type: boolean | 默认: True，限制每部落可建造的地堡数量。 |
| `LimitBunkersPerTribeNum` | 每部落地堡数量上限 | 3 | integer | GameUserSettings.ini | Default value: 3Value type: integer | 默认: 3，每部落可建造的地堡最大数量。 |
| `IncreasePvPRespawnIntervalBaseAmount` | 增加PvP重生间隔基础值 | 0.0 | float | GameUserSettings.ini + 命令行 | the base amount added to PvP respawn interval. | PvP重生间隔增加的基础值。 |
| `IncreasePvPRespawnIntervalCheckPeriod` | 增加PvP重生间隔检查周期 | 0.0 | float | GameUserSettings.ini + 命令行 | the time period to check for repeated PvP deaths. | 检查重复PvP死亡的时间周期。 |
| `IncreasePvPRespawnIntervalMultiplier` | 增加PvP重生间隔倍率 | 0.0 | float | GameUserSettings.ini + 命令行 | the multiplier for increased PvP respawn interval. | PvP重生间隔增加的倍率。 |
| `PreventOfflinePvPConnectionInvincibleInterval` | 离线PvP连接无敌间隔 | 5.0 | float | GameUserSettings.ini + 命令行 | the interval in seconds of invincibility after connecting in offline PvP. | 离线PvP连接后的无敌间隔（秒）。 |
| `PreventOutOfTribePinCodeUse` | 防止部落外PIN码使用 | False | boolean | GameUserSettings.ini + 命令行 | True, prevents out-of-tribe pin code use. | 防止部落外成员使用PIN码。 |
| `PvPStructureDecay` | PvP建筑腐烂 | False | boolean | GameUserSettings.ini + 命令行 | True, enables structure decay in PvP mode. | 在PvP模式中启用建筑腐烂。 |
| `PvPZoneStructureDamageMultiplier` | PvP区域建筑伤害倍率 | 6.0 | float | GameUserSettings.ini + 命令行 | the structure damage multiplier in PvP zones. | PvP区域建筑伤害倍率。 |
| `TribeMergeAllowed` | 允许部落合并 | True | boolean | GameUserSettings.ini + 命令行 | True, allows tribe merging. | 允许部落合并。 |
| `TribeMergeCooldown` | 部落合并冷却 | 0.0 | float | GameUserSettings.ini + 命令行 | the cooldown in seconds between tribe merges. | 部落合并之间的冷却时间（秒）。 |
| `TribeSlotReuseCooldown` | 部落槽位重用冷却 | 0.0 | float | GameUserSettings.ini + 命令行 | the cooldown in seconds for tribe slot reuse. | 部落槽位重用冷却时间（秒）。 |
| `TribeLogDestroyedEnemyStructures` | 部落日志记录销毁敌方建筑 | False | boolean | GameUserSettings.ini + 命令行 | True, logs destroyed enemy structures in tribe log. | 在部落日志中记录销毁的敌方建筑。 |
| `MaxAlliancesPerTribe` | 每部落最大联盟数 | N/A | integer | GameUserSettings.ini + 命令行 | the maximum number of alliances per tribe. | 每个部落最大联盟数量。 |
| `MaxTribesPerAlliance` | 每联盟最大部落数 | N/A | integer | GameUserSettings.ini + 命令行 | the maximum number of tribes per alliance. | 每个联盟最大部落数量。 |
| `MaxNumberOfPlayersInTribe` | 部落最大玩家数 | 0 | integer | GameUserSettings.ini + 命令行 | the maximum number of players in a tribe. 0 disables. | 部落最大玩家数量，0禁用。 |
| `MaxTribeLogs` | 部落日志最大数量 | 400 | integer | GameUserSettings.ini + 命令行 | the maximum number of tribe log entries. | 部落日志最大条目数量。 |

#### 时间与存档 (7项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AutoSavePeriodMinutes` | 自动保存间隔(分钟) | 15.0 | float | GameUserSettings.ini + 命令行 | interval for automatic saves. Setting this to 0 will cause constant saving. | 自动保存间隔（分钟）。设为0将导致持续保存。 |
| `ClampItemSpoilingTimes` | 限制物品腐烂时间 | False | boolean | GameUserSettings.ini + 命令行 | True, clamps all spoiling times to the items' maximum spoiling times. Useful if any infinite-spoiling exploits were used on the server and you wish to clean them up. Could potentially cause issues with mods that alter spoiling time, hence it is an op | True，将所有腐烂时间限制在物品最大腐烂时间内。 |
| `DayCycleSpeedScale` | 昼夜循环速度 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the passage of time in the ARK, controlling how often day changes to night and night changes to day. The default value 1 provides the same cycle speed as the single player experience (and the official public servers). Values lo | ARK中时间流逝的缩放因子，控制昼夜交替速度。默认值1为正常速度。 |
| `DayTimeSpeedScale` | 白天时间速度 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the passage of time in the ARK during the day. This value determines the length of each day, relative to the length of each night (as specified by NightTimeSpeedScale). Lowering this value increases the length of each day. | ARK白天时间流逝的缩放因子。降低此值增加白天时长。 |
| `DisableBurrowDecayTimers` | 禁用地穴衰减计时器 | False | boolean | GameUserSettings.ini + 命令行 | True, turns off entirely the Burrowbuck's burrow decay timers. | True，完全关闭地穴的衰减计时器。 |
| `NightTimeSpeedScale` | 夜晚时间速度 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the passage of time in the ARK during night time. This value determines the length of each night, relative to the length of each day (as specified by DayTimeSpeedScale) Lowering this value increases the length of each night. | ARK夜晚时间流逝的缩放因子。降低此值增加夜晚时长。 |
| `StructurePickupTimeAfterPlacement` | 建筑放置后可拾取时间(秒) | 30.0 | float | GameUserSettings.ini + 命令行 | of time in seconds after placement that quick pick-up is available. | 建筑放置后可快速拾取的时间窗口（秒）。 |

#### 采集与资源 (6项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `ClampResourceHarvestDamage` | 限制资源采集伤害 | False | boolean | GameUserSettings.ini + 命令行 | True, limit the damage caused by a tame to a resource on harvesting based on resource remaining health.  Note: enabling this setting may result in sensible resource harvesting reduction using high damage tools or creatures. | True，限制驯服生物对资源的采集伤害。 |
| `HarvestAmountMultiplier` | 采集产量倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for yields from all harvesting activities (chopping down trees, picking berries, carving carcasses, mining rocks, etc.). Higher values increase the amount of materials harvested with each strike. | 所有采集活动产出的缩放因子。数值越高，每次采集获得的材料越多。 |
| `HarvestHealthMultiplier` | 可采集物生命值倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the "health" of items that can be harvested (trees, rocks, carcasses, etc.). Higher values increase the amount of damage (i.e., "number of strikes") such objects can withstand before being destroyed, which results in higher ove | 可采集物品（树木、岩石等）生命值的缩放因子。数值越高，物品越耐采集。 |
| `StructurePreventResourceRadiusMultiplier` | 建筑资源阻止半径倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | as ResourceNoReplenishRadiusStructures in Game.ini. If both settings are set both multiplier will be applied. Can be useful when cannot change the Game.ini file as it works as a command line option too. | 等同于Game.ini中的ResourceNoReplenishRadiusStructures。 |
| `BloodforgeReinforceResourceCostMultiplier` | 血锻强化资源消耗倍率 | 3.0 | float | GameUserSettings.ini | Default value: 3.0Value type: float | 默认: 3.0类型: float |
| `MaxActiveResourceCaches` | 最大活跃资源缓存数 | - | integer | GameUserSettings.ini | Value type: integer | 类型: integer |

#### 管理员与安全 (5项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AdminLogging` | 管理员命令日志 | False | boolean | GameUserSettings.ini + 命令行 | True, logs all admin commands to in-game chat. | True，将所有管理员命令记录到游戏聊天中。 |
| `BanListURL` | 封禁列表URL | - | string | GameUserSettings.ini + 命令行 | with a URLSets the global ban list. Must be enclosed in double quotes. The list is fetched every 10 minutes (to check if there are new banned IDs).  ARK: Survival Evolved: Official ban list URL is http://arkdedicated.com/banlist.txt (before 279.233 t | 设置全局封禁列表URL。每10分钟更新一次。 |
| `ServerAdminPassword` | 管理员密码 | - | string | GameUserSettings.ini + 命令行 | specified, players must provide this password (via the in-game console) to gain access to administrator commands on the server. Note: no quotes are used. | 玩家需通过游戏控制台输入此密码以获得管理员权限。 |
| `ServerPassword` | 服务器连接密码 | - | string | GameUserSettings.ini + 命令行 | specified, players must provide this password to join the server. Note: no quotes are used. | 玩家加入服务器时需要输入的密码。 |
| `AdminListURL` | 管理员列表URL | N/A | string | GameUserSettings.ini + 命令行 | with a URLAlternative to AllowedCheaterAccountIDs.txt (see Administrator Whitelisting) using a web resource. The interval at which the server queries the resource to check for admin list update is defined by UpdateAllowedCheatersInterval. Undocumente | AllowedCheaterAccountIDs.txt的替代方案，使用Web资源的管理员白名单URL。 |

#### 物品 (5项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `ItemStackSizeMultiplier` | 物品堆叠大小倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | increasing or decreasing global item stack size, this means all default stack sizes will be multiplied by the value given (excluding items that have a stack size of 1 by default). | 全局物品堆叠大小的缩放因子。所有默认堆叠大小将乘以此值。 |
| `MaxTributeItems` | 最大上传物品数 | 50 | integer | GameUserSettings.ini + 命令行 | for uploaded items and resources. Any value less than default will be reverted. Note: Some player claimed maximum 154 to be safe cap and more will corrupt profile/cluster and lead to lose of all stored items and resources but it need to be checked | 上传物品和资源的最大数量。低于默认值将被恢复。 |
| `MaxTributeCharacters` | 最大贡品角色数 | 10 | integer | GameUserSettings.ini + 命令行 | the maximum number of tribute characters. | 最大贡品角色数量。 |
| `TributeCharacterExpirationSeconds` | 贡品角色过期时间(秒) | 0 | integer | GameUserSettings.ini + 命令行 | the expiration time in seconds for tribute characters. 0 disables. | 贡品角色过期时间（秒），0禁用。 |
| `TributeDinoExpirationSeconds` | 贡品恐龙过期时间(秒) | 86400 | integer | GameUserSettings.ini + 命令行 | the expiration time in seconds for tribute dinos. | 贡品恐龙过期时间（秒）。 |
| `TributeItemExpirationSeconds` | 贡品物品过期时间(秒) | 86400 | integer | GameUserSettings.ini + 命令行 | the expiration time in seconds for tribute items. | 贡品物品过期时间（秒）。 |
| `MinimumDinoReuploadInterval` | 最小恐龙重新上传间隔 | 0.0 | float | GameUserSettings.ini + 命令行 | the minimum interval in seconds for dino reupload. 0 disables. | 恐龙重新上传最小间隔（秒），0禁用。 |
| `PreventTransferForClassNames` | 防止指定类名传输 | N/A | string | GameUserSettings.ini + 命令行 | prevents transfer of specific creatures by classname. | 防止指定类名的生物传输。 |
| `PersonalTamedDinosSaddleStructureCost` | 个人驯服恐龙鞍建筑成本 | 0 | integer | GameUserSettings.ini + 命令行 | the saddle structure cost for personal tamed dinos. | 个人驯服恐龙鞍建筑成本。 |
| `RandomSupplyCratePoints` | 随机补给箱位置 | False | boolean | GameUserSettings.ini + 命令行 | True, supply drops are in random locations. Note: This setting is known to cause artifacts becoming inaccessible on Ragnarok if active. | True，补给箱出现在随机位置。 |
| `PreventDownloadItems` | 禁止下载物品 | False | boolean | GameUserSettings.ini + 命令行 | True, prevents items download from ARK Data in Cross-ARK Data Transfer. | True，禁止从ARK数据下载物品。 |
| `PreventUploadItems` | 禁止上传物品 | False | boolean | GameUserSettings.ini + 命令行 | True, prevents items upload to ARK Data in Cross-ARK Data Transfer. | True，禁止向ARK数据上传物品。 |
| `SupplyCrateLootQualityMultiplier` | 补给箱战利品品质倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the quality multiplier for supply crate loot. | 补给箱战利品品质倍率。 |
| `FishingLootQualityMultiplier` | 钓鱼战利品品质倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the quality multiplier for fishing loot. | 钓鱼战利品品质倍率。 |
| `ClampItemStats` | 限制物品属性 | False | boolean | GameUserSettings.ini + 命令行 | True, clamps item stats to maximum values. | 限制物品属性到最大值。 |
| `GlobalCorpseDecompositionTimeMultiplier` | 全局尸体分解时间倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the multiplier for corpse decomposition time. | 尸体分解时间倍率。 |
| `GlobalPoweredBatteryDurabilityDecreasePerSecond` | 全局电池耐久每秒减少 | 3.0 | float | GameUserSettings.ini + 命令行 | the durability decrease per second for powered batteries. | 供电电池每秒耐久度减少量。 |
| `FuelConsumptionIntervalMultiplier` | 燃料消耗间隔倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the multiplier for fuel consumption interval. | 燃料消耗间隔倍率。 |

#### Mod与地图 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `ActiveMods` | 启用模组列表 | - | list | GameUserSettings.ini | of mod IDs, comma-separated with no spaces, in a single line (for example: ModID1,ModID2,ModID3)Specifies the order and which mods are loaded. ModIDs are comma separated and in one line. Priority is in descending order (the left-most ModID hast the h | 模组ID列表，逗号分隔，单行排列。指定加载顺序，左侧优先级最高。 |
| `ActiveMapMod` | 启用地图模组 | - | mod | GameUserSettings.ini | ID for currently active mod mapSpecifies which mod map is loaded. | 当前活跃地图模组的ID，指定加载哪个模组地图。 |
| `AllowBunkerModulesAboveGround` | 允许地堡模块在地面 | False | boolean | GameUserSettings.ini | Default value: FalseValue type: boolean | 默认: False，允许地堡模块在地面使用。 |
| `AllowBunkerModulesInPreventionZones` | 允许防护区内地堡模块 | False | boolean | GameUserSettings.ini | Default value: FalseValue type: boolean | 默认: False，允许在防护区域内使用地堡模块。 |

#### 印痕与等级 (2项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `CosmeticWhitelistOverride` | 外观白名单覆盖 | - | string | GameUserSettings.ini + 命令行 | with a URLURL to a comma-separated list of whitelisted custom cosmetics, in this format: Mod ID/Enable Dynamic Download (0/1)/Allow non-dataonly blueprints(0/1). See this post for details (note: CRC is not required and it's not used by the game anymo | 白名单自定义外观的URL列表，格式：Mod ID/启用动态下载(0/1)/允许非纯数据蓝图(0/1)。 |
| `OverrideOfficialDifficulty` | 覆盖官方难度 | 0.0 | float | GameUserSettings.ini + 命令行 | you to override the default server difficulty level of 4 with 5 to match the new official server difficulty level. Default value of 0.0 disables the override. A value of 5.0 will allow common creatures to spawn up to level 150. Originally (247.95) av | 覆盖默认服务器难度等级。默认值0.0禁用覆盖，5.0允许普通生物最高150级。 |

#### 驯养设置 (1项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `TamingSpeedMultiplier` | 驯服速度倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for creature taming speed. Higher values make taming faster. | 生物驯服速度的缩放因子。数值越高，驯服越快。 |

#### 经验值倍率 (1项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `XPMultiplier` | 全局经验倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the experience received by players, tribes and tames for various actions. The default value 1 provides the same amounts of experience as in the single player experience (and official public servers). Higher values increase XP a | 玩家、部落和驯服生物获得经验的缩放因子。默认值1为正常经验。 |



### 2.2 [SessionSettings]

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `MultiHome` | 多宿主IP地址 | N/A | IP_ADDRESSSpecifies | GameUserSettings.ini | MultiHome IP Address. Boolean Multihome option must be set to True as well (command line or [MultiHome] section). Leave it empty if not using multihoming. Can be specified in command line too. | 多宿主IP地址。需要同时设置MultiHome选项为True。 |
| `Port` | 游戏端口 | 7777 | integer | GameUserSettings.ini | the UDP Game Port. See Dedicated server setupNote: command line append syntax is not supported by  ARK: Survival Ascended | UDP游戏端口。 |
| `QueryPort` | Steam查询端口 | 27015 | integer | GameUserSettings.ini | the UDP Steam Query Port. See Dedicated server setup | UDP Steam查询端口。 |
| `SessionName` | 服务器显示名称 | ARK #123456 | string | GameUserSettings.ini | the Server name advertised in the Game Server Browser as well in Steam Server browser. If no name is provide, the default name will be ARK # followed by a random 6 digit number. Note: Name must not be typed between quotes unless it is launched from c | 在游戏服务器浏览器和Steam服务器浏览器中显示的服务器名称。 |


### 2.3 [/Script/Engine.GameSession]

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `MaxPlayers` | 最大玩家数 | 70 | integer | GameUserSettings.ini | the maximum number of players that can play on the server simultaneously. ASA: This setting is replaced with -WinLiveMaxPlayers in the command line options, as otherwise, it will force it back to the default value. | 服务器同时在线的最大玩家数量。ASA中使用命令行-WinLiveMaxPlayers替代。 |


### 2.4 [MessageOfTheDay]

```ini
[MessageOfTheDay]
Message=欢迎来到服务器！请遵守规则。
Duration=30
```

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `Duration` | 显示持续时间(秒) | 20 | integer | GameUserSettings.ini | in seconds the duration of the displayed message on player log-in. | 玩家登录时显示消息的持续时间（秒）。 |
| `Message` | 消息内容 | N/A | string | GameUserSettings.ini | single line string for a message displayed to played once logged-in. No quotes needed. Use \n to start a new line in the message. | 玩家登录后显示的单行消息。使用\n换行。 |


---

## 3. Game.ini

**路径**: `ShooterGame/Saved/Config/WindowsServer/Game.ini`

> 直接在文件中添加配置项，无需 Section 头

#### 繁殖与成长 (14项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `BabyCuddleGracePeriodMultiplier` | 婴儿照料宽限期倍率 | 1.0 | float | Game.ini | how long after delaying cuddling with the Baby before Imprinting Quality starts to decrease. | 延迟照料婴儿后，印记品质开始下降前的宽限期。 |
| `BabyCuddleIntervalMultiplier` | 婴儿照料间隔倍率 | 1.0 | float | Game.ini | how often babies needs attention for imprinting. More often means you'll need to cuddle with them more frequently to gain Imprinting Quality. Scales always according to default BabyMatureSpeedMultiplier value: set at 1.0 the imprint request is every  | 婴儿需要照料的频率。数值越高，照料间隔越短。 |
| `BabyCuddleLoseImprintQualitySpeedMultiplier` | 婴儿印记品质下降速度倍率 | 1.0 | float | Game.ini | how fast Imprinting Quality decreases after the grace period if you haven't yet cuddled with the Baby. | 宽限期后印记品质下降的速度。 |
| `BabyFoodConsumptionSpeedMultiplier` | 婴儿食物消耗速度倍率 | 1.0 | float | Game.ini | the speed that baby tames eat their food. A lower value decreases (by percentage) the food eaten by babies. | 婴儿驯服生物的食物消耗速度。数值越低，消耗越少。 |
| `BabyImprintAmountMultiplier` | 印记量倍率 | 1.0 | float | Game.ini | the percentage each imprint provides. A higher value, will rise the amount of imprinting % at each baby care/cuddle, a lower value will decrease it. This multiplier is global, meaning it will affect the imprinting progression of every species. See al | 每次照料提供的印记百分比。数值越高，每次照料获得的印记越多。 |
| `BabyImprintingStatScaleMultiplier` | 印记属性缩放倍率 | 1.0 | float | Game.ini | how much of an effect on stats the Imprinting Quality has. Set it to 0 to effectively disable the system. | 印记品质对属性加成的影响程度。设为0可禁用此系统。 |
| `BabyMatureSpeedMultiplier` | 婴儿成长速度倍率 | 1.0 | float | Game.ini | the maturation speed of babies. A higher number decreases (by percentage) time needed for baby tames to mature. See Times for Breeding tables for values at 1.0, see The Imprinting formula how it affects the imprinting amount at each baby care/cuddle. | 婴儿成长速度。数值越高，成长所需时间越短。 |
| `bDisableWirelessCraftingForPlayers` | 禁用玩家无线制作 | false | boolean | Game.ini | True, prevents wireless crafting from Tek Dedicated Storage when crafting in the player inventory. | True，禁止玩家背包从泰克专用存储无线制作。 |
| `bUseSingleplayerSettings` | 启用单人游戏设置 | False | boolean | Game.ini | True, all game settings will be more balanced for an individual player experience. Useful for dedicated server with a very small amount of players. See Single Player Settings section for more details. | True，所有游戏设置将更适合单人体验。 |
| `EggHatchSpeedMultiplier` | 蛋孵化速度倍率 | 1.0 | float | Game.ini | the time needed for a fertilised egg to hatch. A higher value decreases (by percentage) that time. | 受精蛋孵化所需时间。数值越高，孵化越快。 |
| `LayEggIntervalMultiplier` | 产蛋间隔倍率 | 1.0 | float | Game.ini | the time between eggs are spawning / being laid. Higher number increases it (by percentage). | 产蛋间隔时间。数值越高，间隔越长。 |
| `PerLevelStatsMultiplier_Player[<integer>]` | 玩家每级属性倍率[<整数>] | N/A | float | Game.ini | Player stats. See Level stats related section for more detail. | 玩家属性倍率。 |
| `PreventBreedingForClassNames` | 禁止指定物种繁殖 | N/A | "<string>"Prevents | Game.ini | breeding of specific creatures via classname. E.g. PreventBreedingForClassNames="Argent_Character_BP_C". Creature classnames can be found on the Creature IDs page. | 通过类名禁止特定生物繁殖。 |
| `ResourceNoReplenishRadiusPlayers` | 玩家周围资源不刷新半径 | 1.0 | float | Game.ini | how resources regrow closer or farther away from players. Values higher than 1.0 increase the distance around players where resources are not allowed to grow back. Values between 0 and 1.0 will reduce it. | 玩家周围资源不刷新的半径。数值越高，禁止刷新范围越大。 |

#### 通用设置 (10项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bAllowUnlimitedRespecs` | 允许无限洗点 | False | boolean | Game.ini | True, allows more than one usage of Mindwipe Tonic without 24 hours cooldown. | True，允许无限制使用洗点药水，无需24小时冷却。 |
| `CustomRecipeEffectivenessMultiplier` | 自定义配方效果倍率 | 1.0 | float | Game.ini | the effectiveness of custom recipes. A higher value increases (by percentage) their effectiveness. | 自定义配方的效果倍率。数值越高，效果越好。 |
| `CustomRecipeSkillMultiplier` | 自定义配方技能倍率 | 1.0 | float | Game.ini | the effect of the players crafting speed level that is used as a base for the formula in creating a custom recipe. A higher number increases (by percentage) the effect. | 玩家制作速度等级对自定义配方的影响。 |
| `LimitGeneratorsNum` | 发电机数量上限 | 3 | integer | Game.ini | the number of generators in the area defined by LimitGeneratorsRange. Official servers have it set to 3. | LimitGeneratorsRange范围内发电机的最大数量。 |
| `LimitGeneratorsRange` | 发电机限制范围 | 15000 | integer | Game.ini | the area range (in Unreal Units) in which the option LimitGeneratorsNum applies. Official servers have it set to 15000. | LimitGeneratorsNum适用的区域范围（虚幻单位）。 |
| `HairGrowthSpeedMultiplier` | 毛发生长速度倍率 | 1.0 (ASE), 0 (ASA) | float | Game.ini | the hair growth. Higher values increase speed of growth. | 毛发生长速度。数值越高，生长越快。 |
| `MatingIntervalMultiplier` | 交配间隔倍率 | 1.0 | float | Game.ini | the interval between tames can mate. A lower value decreases it (on a percentage scale). Example: a value of 0.5 would allow tames to mate 50% sooner. | 驯服生物可交配的间隔时间。数值越低，间隔越短。 |
| `MatingSpeedMultiplier` | 交配速度倍率 | 1.0 | float | Game.ini | the speed at which tames mate with each other. A higher value increases it (by percentage). Example: MatingSpeedMultiplier=2.0 would cause tames to complete mating in half the normal time. | 驯服生物的交配速度。数值越高，交配越快。 |
| `MaxFallSpeedMultiplier` | 最大坠落速度倍率 | 1.0 | float | Game.ini | the falling speed multiplier at which players starts taking fall damage. The falling speed is based on the time players spent in the air while having a negated Z axis velocity meaning that the higher this setting is, the longer players can fall witho | 玩家开始受到坠落伤害的坠落速度倍率。数值越高，可坠落时间越长。 |
| `PoopIntervalMultiplier` | 排便间隔倍率 | 1.0 | float | Game.ini | how frequently survivors can poop. Higher value decreases it (by percentage) | 幸存者排便频率。数值越高，排便越频繁。 |

#### ASA新增功能 (8项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bAllowFlyerSpeedLeveling` | 允许飞行生物升级速度 | False | boolean | Game.ini | whether flyer creatures can have their Movement Speed levelled up. In ARK: Survival Ascended, setting this to True only works if bAllowSpeedLeveling is also True. | 允许飞行生物升级移动速度。需要bAllowSpeedLeveling也为True。 |
| `bAllowSpeedLeveling` | 允许升级移动速度 | False | boolean | Game.ini | whether players and non-flyer creatures can have their Movement Speed levelled up. | 允许玩家和非飞行生物升级移动速度。 |
| `bDisableWirelessCrafting` | 禁用无线制作 | false | boolean | Game.ini | True, prevents wireless crafting from Tek Dedicated Storage. | True，禁止从泰克专用存储无线制作。 |
| `CheatTeleportLocations=(TeleportName="<string>",TeleportLocation=(X=<float>,Y=-<float>,Z=<float>))` | 自定义传送点位置 | - | (...)Creates | Game.ini | a named teleport location that can be used with the TP command. The coordinates must be listed in Unreal units, not in-game gps coordinates. Example:  CheatTeleportLocations=(TeleportName="Hightower",TeleportLocation=(X=467967.0,Y=-359082.0,Z=6879.0) | 创建可用于TP命令的命名传送点。坐标必须使用虚幻单位。 |
| `WirelessCraftingRangeOverride` | 无线制作范围覆盖 | 3000 | integer | Game.ini | the wireless crafting range (in Unreal Units) on Tek Dedicated Storage. | 泰克专用存储的无线制作范围（虚幻单位）。 |
| `ValgueroMemorialEntries` | 瓦尔盖罗纪念碑名列表 | N/A | list | Game.ini | of player names, semicolon-separated with no spaces, in a single line (for example: Name1;Name2;Name3;)The Valguero Memorial is now interactable, honouring those who have ascended by displaying their names. Server owners can customize the list of nam | 玩家名称列表，分号分隔。自定义纪念碑上显示的飞升者名称。 |
| `BaseHexagonRewardMultiplier` | 六角币奖励基础倍率 | 1.0 | float | Game.ini | the missions score hexagon rewards. Also scales token rewards in Club Ark (ASA). | 任务六角币奖励的基础倍率。也影响Club ARK的代币奖励。 |
| `HexagonCostMultiplier` | 六角币消耗倍率 | 1.0 | float | Game.ini | the hexagon cost of items in the Hexagon store. Also scales token cost of items in Club Ark (ASA). | 六角币商店物品的消耗倍率。也影响Club ARK的代币消耗。 |

#### 经验值倍率 (5项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `CraftXPMultiplier` | 制作经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for crafting. | 制作获得的经验值倍率。 |
| `GenericXPMultiplier` | 通用经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for generic XP (automatic over time). | 自动获得的通用经验值倍率。 |
| `HarvestXPMultiplier` | 采集经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for harvesting. | 采集获得的经验值倍率。 |
| `KillXPMultiplier` | 击杀经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for a kill. | 击杀获得的经验值倍率。 |
| `SpecialXPMultiplier` | 特殊事件经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for SpecialEvent. | 特殊事件获得的经验值倍率。 |

#### PvP与部落 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisableFriendlyFire` | 禁用友军伤害 | False | boolean | Game.ini | True, prevents Friendly-Fire (among tribe mates/tames/structures). | True，禁止友军伤害（部落成员/驯服生物/建筑之间）。 |
| `bPvEDisableFriendlyFire` | PvE禁用友军伤害 | False | boolean | Game.ini | True, disabled Friendly-Fire (among tribe mates/tames/structures) in PvE servers. | True，在PvE服务器中禁用友军伤害。 |
| `IgnorePVPMountedWeaponryRestrictions` | 忽略PvP骑乘武器限制 | False | boolean | Game.ini | further information has been added about this variable. If you know anything, please consider creating an account and contributing. | 忽略PvP模式下的骑乘武器限制。 |
| `TribeTowerBonusMultiplier` | 部落之塔奖励倍率 | 2.0 | float | Game.ini | for Tribe Tower bonus. | 部落之塔奖励倍率。 |

#### 生物设置 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisableWirelessCraftingForDinos` | 禁用恐龙无线制作 | false | boolean | Game.ini | True, prevents wireless crafting from Tek Dedicated Storage when crafting in dino inventories. | True，禁止恐龙背包从泰克专用存储无线制作。 |
| `bUseDinoLevelUpAnimations` | 使用恐龙升级动画 | True | boolean | Game.ini | False, tame creatures on level-up will not perform the related animation. | False，驯服生物升级时不播放动画。 |
| `ConfigAddNPCSpawnEntriesContainer` | 添加NPC生成区域配置 | N/A | (...)Adds | Game.ini | specific creatures in spawn areas. See Creature Spawn related section for more detail. | 在生成区域添加特定生物。 |
| `WildDinoCharacterFoodDrainMultiplier` | 野生恐龙食物消耗倍率 | 1.0 | float | Game.ini | how fast wild creatures consume food. | 野生生物的食物消耗速度。 |

#### 生物等级与经验 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `EnemyLevelsMin` | 敌人等级最小值 | N/A | float | Game.ini | the minimum enemy level as a float array. | 敌人等级最小值（浮点数组）。 |
| `EnemyLevelsMax` | 敌人等级最大值 | N/A | float | Game.ini | the maximum enemy level as a float array. | 敌人等级最大值（浮点数组）。 |
| `GameDifficulties` | 游戏难度 | N/A | float | Game.ini | the game difficulties as a float array. | 游戏难度（浮点数组）。 |
| `OverrideMaxExperiencePointsDino` | 覆盖恐龙最大经验值 | N/A | integer | Game.ini | overrides the maximum experience points for dinos. | 覆盖恐龙最大经验值。 |
| `OverrideMaxExperiencePointsPlayer` | 覆盖玩家最大经验值 | N/A | integer | Game.ini | overrides the maximum experience points for players. | 覆盖玩家最大经验值。 |
| `OverridePlayerLevelEngramPoints` | 覆盖玩家等级印痕点数 | N/A | integer | Game.ini | overrides the engram points per player level. | 覆盖每级玩家印痕点数。 |

#### 世界Buff与事件 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `WorldBuffScalingEfficacy` | 世界Buff缩放效果 | 1.0 | float | Game.ini | the efficacy of world buff scaling. | 世界Buff缩放效果。 |
| `DisableWorldBuffs` | 禁用世界Buff | False | boolean | Game.ini | True, disables all world buffs. | 禁用所有世界Buff。 |
| `EnableWorldBuffScaling` | 启用世界Buff缩放 | False | boolean | Game.ini | True, enables world buff scaling. | 启用世界Buff缩放。 |
| `DynamicUndermeshRegions` | 动态地下区域 | 1.0 | float | Game.ini | the multiplier for dynamic undermesh regions. | 动态地下区域倍率。 |
| `DormancyNetMultiplier` | 休眠网络倍率 | 1.0 | float | Game.ini | the multiplier for dormancy rate. Requires -nodormancythrottling if <= 1.0. Undocumented. | 休眠速率倍率，小于等于1.0需要-nodormancythrottling参数。 |

#### 采集与资源 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `CropDecaySpeedMultiplier` | 作物腐烂速度倍率 | 1.0 | float | Game.ini | the speed of crop decay in plots. A higher value decrease (by percentage) speed of crop decay in plots. | 农田中作物的腐烂速度。数值越高，腐烂越慢。 |
| `CropGrowthSpeedMultiplier` | 作物生长速度倍率 | 1.0 | float | Game.ini | the speed of crop growth in plots. A higher value increases (by percentage) speed of crop growth. | 农田中作物的生长速度。数值越高，生长越快。 |
| `HarvestResourceItemAmountClassMultipliers` | 资源采集量分类倍率 | N/A | (...)Scales | Game.ini | on a per-resource type basis, the amount of resources harvested. See Items related section for more details. | 按资源类型缩放的采集产出量。 |
| `ResourceNoReplenishRadiusStructures` | 建筑周围资源不刷新半径 | 1.0 | float | Game.ini | how resources regrow closer or farther away from structures Values higher than 1.0 increase the distance around structures where resources are not allowed to grow back. Values between 0 and 1.0 will reduce it. | 建筑周围资源不刷新的半径。数值越高，禁止刷新范围越大。 |

#### Mod与地图 (3项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisablePhotoMode` | 禁用拍照模式 | False | boolean | Game.ini | if photo mode is allowed (False) or not (True). | True，禁用拍照模式。 |
| `bShowCreativeMode` | 显示创造模式 | False | boolean | Game.ini | True, adds a button to the pause menu to enable/disable creative mode. | True，在暂停菜单中添加启用/禁用创造模式的按钮。 |
| `PhotoModeRangeLimit` | 拍照模式最大距离 | 3000 | integer | Game.ini | the maximum distance between photo mode camera position and player position. | 拍照模式相机与玩家位置之间的最大距离。 |

#### 建筑与防御 (3项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisableStructurePlacementCollision` | 禁用建筑放置碰撞 | False | boolean | Game.ini | True, allows for structures to clip into the terrain. | True，允许建筑穿入地形。 |
| `bDisableWirelessCraftingForStructures` | 禁用建筑无线制作 | false | boolean | Game.ini | True, prevents wireless crafting from Tek Dedicated Storage when crafting in structure inventories. | True，禁止建筑背包从泰克专用存储无线制作。 |
| `bIgnoreStructuresPreventionVolumes` | 忽略建筑禁止区域 | False | boolean | Game.ini | True, enables building areas where normally it's not allowed, such around some maps' Obelisks, in the Aberration Portal and in Mission Volumes areas on Genesis: Part 1. Note: in Genesis: Part 1 this settings is enabled by default and there is an ad h | True，允许在通常禁止建造的区域建造。 |

#### 印痕与等级 (3项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `DestroyTamesOverLevelClamp` | 销毁超过等级上限的驯养 | 0 | integer | Game.ini | that exceed that level will be deleted on server start. Official servers have it set to 450. | 超过此等级的驯服生物将在服务器启动时被删除。官方服务器设为450。 |
| `LevelExperienceRampOverrides` | 等级经验曲线覆盖 | N/A | (...)Configures | Game.ini | the total number of levels available to players and tame creatures and the experience points required to reach each level. See Players and tames levels override section for more details. | 配置玩家和驯服生物的可用等级总数及每级所需经验值。 |
| `OverrideNamedEngramEntries` | 覆盖命名印痕条目 | N/A | (...)Configures | Game.ini | the status and requirements for learning an engram, specified by its name. See Engram Entries related section for more detail. | 按名称配置印痕的学习状态和要求。 |

#### 物品 (2项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `CraftingSkillBonusMultiplier` | 制作技能奖励倍率 | 1.0 | float | Game.ini | the bonus received from upgrading the Crafting Skill. | 升级制作技能获得的奖励倍率。 |
| `ExcludeItemIndices` | 排除物品索引 | N/A | integer | Game.ini | an item from supply crates specifying its Item ID. You can have multiple lines of this option. | 通过物品ID从补给箱中排除物品。 |

#### 时间与存档 (2项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `GlobalItemDecompositionTimeMultiplier` | 全局物品分解时间倍率 | 1.0 | float | Game.ini | the decomposition time of dropped items, loot bags etc. globally. Higher values prolong the time. | 掉落物品、战利品袋等的全局分解时间。数值越高，时间越长。 |
| `GlobalSpoilingTimeMultiplier` | 全局腐烂时间倍率 | 1.0 | float | Game.ini | the spoiling time of perishables globally. Higher values prolong the time. | 易腐物品的全局腐烂时间。数值越高，腐烂越慢。 |



---

## 4. 高级配置详解

> 以下章节详细解释各类结构化配置(...)的语法和用法

### 生物生成配置 (Creature Spawn)

> 用于自定义特定区域的生物生成，可添加、移除或覆盖默认生成规则。配置写入 Game.ini。

ConfigAddNPCSpawnEntriesContainer


**`ConfigAddNPCSpawnEntriesContainer=(`**

```ini
ConfigAddNPCSpawnEntriesContainer=([NPCSpawnEntriesContainerClassString="<spawn_class>"],[NPCSpawnEntries=(([AnEntryName="<spawn_name>"],[EntryWeight=<factor>],[NPCsToSpawnStrings=("<entity_id>")
```

**`ConfigAddNPCSpawnEntriesContainer=(`**

```ini
ConfigAddNPCSpawnEntriesContainer=(  NPCSpawnEntriesContainerClassString="DinoSpawnEntriesBeach_C",  NPCSpawnEntries=((AnEntryName="GigaSpawner", EntryWeight=1.0, NPCsToSpawnStrings=("Gigant_Character_BP_C")
```

**`ConfigAddNPCSpawnEntriesContainer=(`**

```ini
ConfigAddNPCSpawnEntriesContainer=(  NPCSpawnEntriesContainerClassString="DinoSpawnEntriesDamiensAtoll_C",  NPCSpawnEntries=(    (AnEntryName="Dodos (2)
```

### 生物属性配置 (Creature Stats)

> 用于自定义特定生物的伤害和抗性倍率，按生物类名(Classname)配置。配置写入 Game.ini。

**`DinoClassDamageMultipliers`**

```ini
DinoClassDamageMultipliers=(ClassName="<string>",Multiplier=<float>)
TamedDinoClassDamageMultipliers=(ClassName="<string>",Multiplier=<float>)
DinoClassDamageMultipliers=(  ClassName="MegaRex_Character_BP_C",  Multiplier=0.1)
TamedDinoClassDamageMultipliers=(  ClassName="Rex_Character_BP_C",  Multiplier=10.0)
DinoClassResistanceMultipliers=(ClassName="<string>",Multiplier=<float>)
TamedDinoClassResistanceMultipliers=(ClassName="<string>",Multiplier=<float>)
DinoClassResistanceMultipliers=(  ClassName="MegaRex_Character_BP_C",  Multiplier=0.1)
TamedDinoClassResistanceMultipliers=(  ClassName="Rex_Character_BP_C",  Multiplier=10.0)
```

### 印痕条目配置 (Engram Entries)

> 用于自定义印痕的学习条件、消耗点数、等级需求，或隐藏/显示特定印痕。配置写入 Game.ini。

**`OverrideEngramEntries and OverrideNamedEngramEntries`**

```ini
OverrideEngramEntries=(EngramIndex=<index>[,EngramHidden=<hidden>][,EngramPointsCost=<cost>][,EngramLevelRequirement=<level>][,RemoveEngramPreReq=<remove_prereq>])
OverrideNamedEngramEntries=(EngramClassName="<class_name>"[,EngramHidden=<hidden>][,EngramPointsCost=<cost>][,EngramLevelRequirement=<level>][,RemoveEngramPreReq=<remove_prereq>])
OverrideEngramEntries=(EngramIndex=0,EngramHidden=False)
OverrideEngramEntries=(EngramIndex=1,EngramHidden=False,EngramPointsCost=3,EngramLevelRequirement=3,RemoveEngramPreReq=True)
OverrideNamedEngramEntries=(EngramClassName="EngramEntry_Campfire_C",EngramHidden=False)
```

### 物品配置 (Items)

> 用于自定义物品制作配方、堆叠数量、补给箱内容、采集产出等。配置写入 Game.ini。

Every Item Class String can be found in the Dev Kit. Currently doesn't change the repair cost and demolish refund of edited structures. This can result in potential exploit for lowered crafting costs and may make structures unrepairable.
Note: if using stack mods, refer to the mod new resources instead of vanilla ones (i.e.: PrimalItemResource_Electronics_Child_C instead of PrimalItemResource_Electronics_C).



ConfigOverrideItemCraftingCosts
This is an example of how to make the Hatchet require 1 thatch and 2 stone arrows to craft.



**`ConfigOverrideItemCraftingCosts=(`**

```ini
ConfigOverrideItemCraftingCosts=(  ItemClassString="PrimalItem_WeaponStoneHatchet_C",  BaseCraftingResourceRequirements=(    (ResourceItemTypeString="PrimalItemResource_Thatch_C",     BaseResourceRequirement=1.0,     bCraftingRequireExactResourceType=False)
```

**`ConfigOverrideItemCraftingCosts=(`**

```ini
ConfigOverrideItemCraftingCosts=(  ItemClassString="PrimalItem_WeaponTorch_C",  BaseCraftingResourceRequirements=(    (ResourceItemTypeString="PrimalItemConsumable_RawMeat_C",     BaseResourceRequirement=3.0,     bCraftingRequireExactResourceType=False)
```

ConfigOverrideItemMaxQuantity


**`ConfigOverrideItemMaxQuantity=(ItemClassString="<string>",Quantity=(MaxItemQuantity=<integer>, bIgnoreMultiplier=<boolean>))`**

```ini
ConfigOverrideItemMaxQuantity=(ItemClassString="<string>",Quantity=(MaxItemQuantity=<integer>, bIgnoreMultiplier=<boolean>)
```

### 等级经验曲线覆盖 (Level Override)

> 用于自定义玩家和驯养生物的最大等级及每级所需经验值。配置写入 Game.ini。

**`LevelExperienceRampOverrides=(ExperiencePointsForLevel[<n>]=<points>,[ExperiencePointsForLevel[<n>]=<points>],...,[ExperiencePointsForLevel[<n>]=<points>])`**

```ini
LevelExperienceRampOverrides=(ExperiencePointsForLevel[<n>]=<points>,[ExperiencePointsForLevel[<n>]=<points>],...,[ExperiencePointsForLevel[<n>]=<points>])
LevelExperienceRampOverrides=(ExperiencePointsForLevel[0]=1,ExperiencePointsForLevel[1]=5,...ExperiencePointsForLevel[64]=1000)
LevelExperienceRampOverrides=(ExperiencePointsForLevel[0]=1,ExperiencePointsForLevel[1]=5,...ExperiencePointsForLevel[34]=1000)
```

### 属性配置 (Stats)

> 用于自定义玩家基础属性、每级属性倍率、诱变剂加成等。包含属性索引表(0=生命, 1=耐力, 3=氧气, 4=食物, 7=负重, 8=近战, 9=移速, 10=坚毅, 11=制作速度)。配置写入 Game.ini。

Attribute index table
This table shows relationships between each stats attribute and its coded index




Index
Stats


0
 Health


1
 Stamina /  Charge Capacity


2
 Torpidity


3
 Oxygen /  Charge Regeneration


4
 Food


5
 Water


6
Temperature


7
 Weight


8
 Melee Damage /  Charge Emission Range


9
 Movement Speed /  Maewing's Nursing effectiveness


10
 Fortitude


11
 Crafting Speed

MutagenLevelBoost
MutagenLevelBoost[<Stat_ID>]=<integer>




Stat_ID
Index of the attribute to override. See the Attribute index table.


integer
Level points. Default values: 5, 5, 0, 0, 0, 0, 0, 5, 5, 0, 0, 0

Number of levels  Mutagen adds to tames with wild ancestry.
The example provided doubles the amounts of level points a mutagen adds on Health and Damage stats but removes the extra level gain

### 单人游戏设置 (Single Player)

> 启用 bUseSingleplayerSettings=True 后自动应用的额外倍率调整，使单人/少量玩家的游戏体验更平衡。

If bUseSingleplayerSettings=True than the following options are applied additionally to the configured (or default) values:




Option
Base value
Additional multiplier


BabyCuddleIntervalMultiplier
ini
x 0.17


BabyMatureSpeedMultiplier
ini
x 35.0


AllowRaidDinoFeeding
True
N/A


bAllowUnlimitedRespecs
True
N/A


CropGrowthSpeedMultiplier
ini
x 4.0


EggHatchSpeedMultiplier
ini
x 9.0


HairGrowthSpeedMultiplier
ini
x 0.69999999


MatingIntervalMultiplier
ini
x 0.15000001


PerLevelStatsMultiplier_DinoTamed_Add[0]
ini
x 3.5714285


PerLevelStatsMultiplier_DinoTamed_Add[8]
ini
x 3.5714285


PerLevelStatsMultiplier_DinoTamed_Affinity[0]
ini
x 2.2727273


PerLevelStatsMultiplier_DinoTamed_Affinity[8]
ini
x 2.2727273


PerLevelStatsMultiplier_DinoTamed[0]
ini
x 2.125


PerLevelStatsMultiplier



---

## 5. 管理员白名单

> 通过白名单文件自动授予玩家管理员权限，无需输入密码

### ARK: Survival Ascended

在 ASA 中，玩家可以通过 EOS ID（32位字母数字字符串）被白名单为管理员。

**配置方法：**
1. 创建文件 `ShooterGame/Saved/AllowedCheaterAccountIDs.txt`
2. 在文件中列出每个玩家的 EOS ID，每行一个

```
EOS_ID_1
EOS_ID_2
```

### ARK: Survival Evolved

在 ASE 中，玩家可以通过 Steam ID（17位数字字符串）被白名单为管理员。

**配置方法：**
1. 创建文件 `ShooterGame/Saved/AllowedCheaterSteamIDs.txt`
2. 在文件中列出每个玩家的 Steam ID，每行一个

```
76561198012345678
76561198087654321
```

**说明：**
- 使用此方法时，不需要指定服务器管理员密码
- 仍可指定密码，非白名单玩家可通过密码获得管理员权限
- 白名单玩家会自动获得管理员权限

---

## 6. 玩家白名单

> 控制哪些玩家可以加入服务器

### 独占加入白名单

当命令行添加 `-exclusivejoin` 或 GameUserSettings.ini 中设置 `UseExclusiveList=true` 时，只有白名单中的玩家可以加入服务器。

**配置方法：**
1. 创建文件 `ShooterGame/Binaries/<PLATFORM>/PlayersExclusiveJoinList.txt`
2. 在文件中列出每个玩家的 ARK ID，每行一个
3. `<PLATFORM>` 为 `Linux` 或 `Win64`，取决于操作系统

```
ARK_ID_1
ARK_ID_2
```

**说明：**
- 启用后，普通服务器密码可以移除
- 如果保留密码，白名单玩家仍需输入密码
- 修改文件后需要重启服务器

### 无检查白名单

此白名单允许玩家绕过服务器最大人数限制。

**配置方法：**
1. 创建文件 `ShooterGame/Binaries/<PLATFORM>/PlayersJoinNoCheckList.txt`
2. 在文件中列出每个玩家的 ARK ID，每行一个

**管理命令：**
- `Cheat AllowPlayerToJoinNoCheck <ARK_ID>` - 无需重启服务器即可添加玩家

---

## 7. 跨服数据传输

> 配置服务器之间的数据传输和集群

### 基本配置

在官方服务器中，玩家可以在任何补给箱、终端、方舟和 Tek 传送器处上传/传输角色。

对于非官方服务器，要允许动态跨服旅行，需要运行至少两个具有相同集群 ID 和集群目录的服务器：

```bash
ShooterGameServer TheIsland?SessionName=MySession1 -clusterid=<CLUSTER_NAME> -ClusterDirOverride="<PATH>"
ShooterGameServer ScorchedEarth_P?SessionName=MySession2 -clusterid=<CLUSTER_NAME> -ClusterDirOverride="<PATH>"
```

**关键参数：**
- `-clusterid=<CLUSTER_NAME>` - 集群ID，必须相同才能互相看到
- `-ClusterDirOverride=<PATH>` - 跨服存储位置

**路径示例：**
- Linux: `-ClusterDirOverride="/MyARKClusterStorage"`
- Windows: `-ClusterDirOverride="C:\MyARKClusterStorage"`

如果不指定 `-ClusterDirOverride`，服务器默认使用 `ShooterGame/Saved/clusters`，这会阻止同一时间运行的服务器互相看到。

### 7.1 ARK数据设置

以下选项可控制上传/下载权限（命令行或 GameUserSettings.ini）：

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `noTributeDownloads` | False | 禁止所有数据下载 |
| `PreventDownloadSurvivors` | False | 禁止下载角色 |
| `PreventDownloadItems` | False | 禁止下载物品 |
| `PreventDownloadDinos` | False | 禁止下载恐龙 |
| `PreventUploadSurvivors` | False | 禁止上传角色 |
| `PreventUploadItems` | False | 禁止上传物品 |
| `PreventUploadDinos` | False | 禁止上传恐龙 |

**上传计时器控制：**

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `MinimumDinoReuploadInterval` | 0.0 | 恐龙重新上传最小间隔（秒） |
| `TributeCharacterExpirationSeconds` | 0 | 上传角色过期时间（秒），0=不过期 |
| `TributeDinoExpirationSeconds` | 86400 | 上传恐龙过期时间（秒），默认24小时 |
| `TributeItemExpirationSeconds` | 86400 | 上传物品过期时间（秒），默认24小时 |

### 7.2 集群文件与多服务器

**集群目录结构：**
```
<ClusterDirOverride>/
├── <cluster_id>/
│   ├── ... (集群共享数据)
```

**注意事项：**
- 所有服务器必须使用相同的 `clusterid`
- 所有服务器必须指向相同的 `ClusterDirOverride` 路径
- 不同路径的服务器无法互相看到

---

## 8. 动态配置 (DynamicConfig)

> 无需重启服务器即可更改部分设置

### 概述

DynamicConfig 允许在不重启服务器的情况下更改一些服务器设置。该文件必须托管在 Web 服务器上（如 Windows 上的 IIS、Apache 等），使用 HTTP 协议（不支持 HTTPS），并且 .ini 文件扩展名必须作为 `text/plain` MIME 类型暴露。不能直接使用系统路径链接。

### 配置方法

1. 创建一个 .ini 文件并托管到 Web 服务器
2. 在命令行或 GameUserSettings.ini 中设置：
   - `-UseDynamicConfig` - 启用动态配置
   - `CustomDynamicConfigUrl=<URL>` - 指定配置文件 URL

**示例：**
```bash
ShooterGameServer TheIsland?listen -UseDynamicConfig -CustomDynamicConfigUrl="http://example.com/arkconfig.ini"
```

### 更新机制

- 配置在每次世界自动保存时检查
- 管理员可使用 `ForceUpdateDynamicConfig` 命令强制立即更新
- 配置文件中的行会被读取并应用；省略的行不会更新

### 可用的动态配置选项

以下设置可通过 DynamicConfig 动态配置：

| 配置项 | 默认值 | 类型 | 说明 |
|--------|--------|------|------|
| `ActiveEventColors` | N/A | string | 激活事件颜色 |
| `BabyCuddleIntervalMultiplier` | 1.0 | float | 婴儿照料间隔倍率 |
| `BabyFoodConsumptionSpeedMultiplier` | 1.0 | float | 婴儿食物消耗速度倍率 |
| `BabyImprintAmountMultiplier` | 1.0 | float | 印记量倍率 |
| `BabyMatureSpeedMultiplier` | 1.0 | float | 婴儿成长速度倍率 |
| `bDisableDinoDecayPvE` | False | boolean | 禁用PvE恐龙衰减 |
| `bDisableStructureDecayPvE` | False | boolean | 禁用PvE建筑衰减 |
| `bPvPDinoDecay` | False | boolean | PvP恐龙衰减 |
| `bPvPStructureDecay` | False | boolean | PvP建筑衰减 |
| `CropGrowthSpeedMultiplier` | 1.0 | float | 作物生长速度倍率 |
| `CustomRecipeEffectivenessMultiplier` | 1.0 | float | 自定义配方效果倍率 |
| `DinoCharacterFoodDrainMultiplier` | 1.0 | float | 恐龙食物消耗倍率 |
| `DisableWorldBuffs` | N/A | string | 禁用特定世界Buff |
| `DynamicColorset` | N/A | string | 动态颜色集（逗号分隔） |
| `DynamicColorsetChanceOverride` | N/A | float | 动态颜色集概率覆盖 |
| `EggHatchSpeedMultiplier` | 1.0 | float | 蛋孵化速度倍率 |
| `EnableFullDump` | False | boolean | 启用完整内存转储 |
| `EnableWorldBuffScaling` | False | boolean | 启用世界Buff缩放 |
| `GlobalSpoilingTimeMultiplier` | 1.0 | float | 全局腐烂时间倍率 |
| `HarvestAmountMultiplier` | 1.0 | float | 采集产量倍率 |
| `HexagonRewardMultiplier` | 1.0 | float | 六角币奖励倍率 |
| `TamingSpeedMultiplier` | 1.0 | float | 驯服速度倍率 |
| `XPMultiplier` | 1.0 | float | 经验倍率 |
| `bUseAlarmNotifications` | N/A | boolean | 切换Web警报通知。Undocumented。 |
| `DisableTimestampVerification` | False | boolean | 禁用时间戳验证。Undocumented。 |
| `GMaxFlameThrowerServerTicksPerFrame` | 5 | integer | 控制火焰喷射器每服务器Tick的Tick速率，更高值可能导致性能问题。Undocumented。 |
| `GUseServerNetSpeedCheck` | False | boolean | 防止玩家每Tick累积过多移动数据，官方集群启用。Undocumented。 |

### 事件颜色名称

| 事件名称 | 说明 |
|----------|------|
| `easter` | ARK: Eggcellent Adventure |
| `FearEvolved` | ARK: Fear Evolved |
| `PAX` | ARK: PAX Party |
| `Summer` | ARK: Summer EVO |
| `TurkeyTrial` | ARK: Turkey Trial |
| `vday` | Valentine's EVO Event |
| `WinterWonderland` | ARK: Winter Wonderland |
| `custom` | 自定义颜色（需设置 DynamicColorset） |

---

## 附录: 快速参考

### 推荐倍率配置

| 用途 | 配置项 | 官方默认 | 休闲PVE | PvP |
|------|--------|----------|---------|-----|
| 驯服速度 | TamingSpeedMultiplier | 1.0 | 3~5 | 1~2 |
| 采集产量 | HarvestAmountMultiplier | 1.0 | 3~5 | 1~2 |
| 经验倍率 | XPMultiplier | 1.0 | 2~3 | 1~2 |
| 孵化速度 | EggHatchSpeedMultiplier | 1.0 | 5~10 | 2~3 |
| 成长速度 | BabyMatureSpeedMultiplier | 1.0 | 5~10 | 2~3 |
| 交配间隔 | MatingIntervalMultiplier | 1.0 | 0.25~0.5 | 0.5~1 |
| 最大玩家 | -WinLiveMaxPlayers | 70 | 20~40 | 40~70 |

### 属性索引表

| 索引 | 属性 | 中文 |
|------|------|------|
| 0 | Health | 生命值 |
| 1 | Stamina | 耐力 |
| 2 | Torpidity | 昏迷值 |
| 3 | Oxygen | 氧气 |
| 4 | Food | 食物 |
| 5 | Water | 水分 |
| 6 | Temperature | 温度 |
| 7 | Weight | 负重 |
| 8 | Melee Damage | 近战伤害 |
| 9 | Movement Speed | 移动速度 |
| 10 | Fortitude | 坚毅 |
| 11 | Crafting Speed | 制作速度 |

### 常用管理员命令

| 命令 | 说明 |
|------|------|
| `enablecheats <密码>` | 启用管理员权限 |
| `cheat SaveWorld` | 保存世界 |
| `cheat DestroyWildDinos` | 销毁所有野生恐龙重新刷新 |
| `cheat Broadcast <消息>` | 广播消息 |
| `cheat GiveItemNumToPlayer <ID> <物品ID> <数量> <品质> <蓝图>` | 给予物品 |
| `cheat Summon <生物类名>` | 生成生物 |
| `cheat Teleport` | 传送到准心位置 |
| `cheat god` | 无敌模式 |

---

> 📖 完整英文文档: https://ark.wiki.gg/wiki/Server_configuration
> 本文档仅包含 ASA (ARK: Survival Ascended) 兼容的配置项
