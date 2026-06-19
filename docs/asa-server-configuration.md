# ARK: Survival Ascended - 服务器配置项完整参考

> **来源**: [ARK Wiki](https://ark.wiki.gg/wiki/Server_configuration) | **日期**: 2026-06-19
> **仅 ASA 兼容项** | 共 369 项配置

---

## 配置文件说明

| 配置文件 | 路径 | 说明 |
|----------|------|------|
| GameUserSettings.ini | `ShooterGame/Saved/Config/WindowsServer/GameUserSettings.ini` | 服务器行为与游戏设置 |
| Game.ini | `ShooterGame/Saved/Config/WindowsServer/Game.ini` | 游戏逻辑与高级设置 |

### 配置方式

| 方式 | 格式 | 示例 |
|------|------|------|
| **INI 文件** | 在对应 [Section] 下添加 `变量名=值` | `TamingSpeedMultiplier=3.0` |
| **命令行** | 启动参数 `?变量名=值` | `?MaxPlayers=70` |
| **命令行标志** | 启动参数 `-FlagName` | `-NoBattlEye` |
| **DynamicConfig** | 运行时热重载配置 | 无需重启服务器 |

### 值类型说明

| 类型 | 说明 | 示例 |
|------|------|------|
| boolean | 布尔值 True/False | `AdminLogging=True` |
| float | 浮点数 | `TamingSpeedMultiplier=3.0` |
| integer | 整数 | `MaxPlayers=70` |
| string | 文本字符串 | `ServerPassword=mypass` |
| list | 逗号分隔列表 | `ActiveMods=123,456` |

---

## 目录

- [1. 命令行参数](#1-命令行参数)
- [2. GameUserSettings.ini](#2-gameusersettingsini) — [ServerSettings](#21-serversettings)(211) · [SessionSettings](#22-sessionsettings)(4) · [GameSession](#23-scriptenginegamesession)(1) · [MOTD](#24-messageoftheday)(2) · [Ragnarok](#25-ragnarok-仙境)(5)
- [3. Game.ini](#3-gameini)(146)
- [附录: 快速参考](#附录-快速参考)

---

## 1. 命令行参数

### 启动命令格式

```
ArkAscendedServer.exe <地图名> [?选项=值]... [-选项[=值]]
```

### Windows 启动示例

```bash
# 基本启动
ArkAscendedServer.exe TheIsland?listen?MaxPlayers=70?ServerPassword=mypass?Port=7777?QueryPort=27015

# 带模组
ArkAscendedServer.exe TheIsland?listen?ActiveMods=123456789,987654321

# 禁用 BattlEye
ArkAscendedServer.exe TheIsland?listen -NoBattlEye

# ASA 专用: 设置最大玩家数 (必须用命令行)
ArkAscendedServer.exe TheIsland?listen -WinLiveMaxPlayers=70
```

### ASA 可用地图

| 地图名称 | Level Name | 说明 |
|----------|-----------|------|
| The Island | TheIsland | 孤岛 - 原始地图 |
| Scorched Earth | ScorchedEarth_P | 焦土 - 沙漠主题 |
| Aberration | Aberration_P | 畸变 - 地下世界 |
| Extinction | Extinction_P | 灭绝 - 都市废墟 |
| Genesis: Part 1 | Genesis | 创世纪1 - 任务系统 |
| Genesis: Part 2 | Gen2 | 创世纪2 - 太空站 |
| Ragnarok | Ragnarok_P | 仙境 - 社区地图 |
| Valguero | Valguero_P | 瓦尔盖罗 - 社区地图 |
| Crystal Isles | CrystalIsles | 水晶岛 - 社区地图 |
| Lost Island | LostIsland | 失落岛 - 社区地图 |
| Fjordur | Fjordur_P | 菲约杜尔 - 社区地图 |
| The Center | TheCenter | 中心岛 - 社区地图 |

### 常用命令行选项

| 选项 | 默认值 | 类型 | 说明 |
|------|--------|------|------|
| `?Port=7777` | 7777 | integer | UDP 游戏通信端口 |
| `?QueryPort=27015` | 27015 | integer | Steam 服务器查询端口 |
| `?MaxPlayers=70` | 70 | integer | 最大玩家数量 (ASA建议用 -WinLiveMaxPlayers) |
| `?listen` | - | flag | 启用监听服务器模式 |
| `?ServerPassword=xxx` | 空 | string | 玩家连接需要的密码 |
| `?ServerAdminPassword=xxx` | 空 | string | 管理员登录密码 (enablecheats) |
| `?SessionName=xxx` | ARK #随机 | string | 服务器列表中显示的名称 |
| `?ActiveMods=ID1,ID2` | 空 | list | 要加载的模组ID列表 (逗号分隔) |
| `?MapModID=xxx` | 空 | string | 自定义地图模组ID |
| `-NoBattlEye` | - | flag | 禁用 BattlEye 反作弊系统 |
| `-server` | - | flag | 以专用服务器模式启动 |
| `-log` | - | flag | 启用日志文件输出 |
| `-NoTransferFromDownloading` | - | flag | 禁止通过数据传输下载角色/物品 |
| `-WinLiveMaxPlayers=N` | 70 | integer | ASA专用: 设置最大玩家数 (替代 INI) |
| `-ActiveEvent=<name>` | - | string | 启用指定的活动事件 |
| `-ForceAllowCaveFlyers` | - | flag | 允许飞行坐骑进入洞穴 |
| `-NoWildBabies` | - | flag | 禁止野生婴儿生物刷新 |
| `-nocombineclientfilters` | - | flag | 禁用客户端过滤器合并 |

---

## 2. GameUserSettings.ini

**文件路径**: `ShooterGame/Saved/Config/WindowsServer/GameUserSettings.ini`

**配置方法**: 在文件中找到或创建对应的 `[Section]`，在下方每行添加一个配置项

```ini
[ServerSettings]
TamingSpeedMultiplier=3.0
HarvestAmountMultiplier=2.0
```

### 2.1 [ServerSettings]

> 主要服务器设置区域，包含游戏玩法、倍率、开关等配置

#### 通用设置 (64项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowMultipleAttachedC4` | 允许MultipleAttachedC4 | False | boolean | GameUserSettings.ini + 命令行 | True, allows to attach more than one C4 per creature. | 允许to attach more than one C4 per creature。 |
| `AllowSharedConnections` | 允许SharedConnections | False | boolean | GameUserSettings.ini | True, allows family sharing players to connect to the server. | 允许family sharing players to connect to the server。 |
| `ArmadoggoDeathCooldown` | ArmadoggoDeathCooldown | 3600 | float | GameUserSettings.ini + 命令行 | the cooldown for Armadoggo to reappear after taking fatal damage (in seconds), default is set to 1 hour. Must be greater than 0. | the cooldown for Armadoggo to reappear after taking fatal damage (in seconds), default is set to 1 hour. |
| `AutoSavePeriodMinutes` | AutoSavePeriodMinutes | 15.0 | float | GameUserSettings.ini + 命令行 | interval for automatic saves. Setting this to 0 will cause constant saving. | interval for automatic saves. |
| `BanListURL` | BanListURL | - | string | GameUserSettings.ini + 命令行 | with a URLSets the global ban list. Must be enclosed in double quotes. The list is fetched every 10 minutes (to check if there are new banned IDs).  ARK: Survival Evolved: Official ban list URL is htt | 设置global ban list。 |
| `bForceCanRideFliers` | ForceCanRideFliers | - | boolean | GameUserSettings.ini | True, allows flyers to be used on maps where they normally are disabled. Note: if you set it to False it will disable flyers on any map. | 允许flyers to be used on maps where they normally are disabled。 |
| `CosmoWeaponAmmoReloadAmount` | CosmoWeaponAmmoReloadAmount | 1 | float | GameUserSettings.ini + 命令行 | how much ammo is given as the Cosmo's webslinger reloads over time. | how much ammo is given as the Cosmo's webslinger reloads over time. |
| `CustomDynamicConfigUrl` | CustomDynamicConfigUrl | - | string | GameUserSettings.ini + 命令行 | with a URLDirect link to a live dynamicconfig.ini file (http://arkdedicated.com/dynamicconfig.ini), allowing live changes of the supported options without the need of server restart, as well defining  | with a URLDirect link to a live dynamicconfig.ini file (http://arkdedicated.com/dynamicconfig.ini), allowing live changes of the supported options without the need of server restart, as well defining  |
| `CustomLiveTuningUrl` | CustomLiveTuningUrl | - | string | GameUserSettings.ini + 命令行 | with a URLDirect link to the live tuning file. For more information on how to use this system check out the official announcement: https://survivetheark.com/index.php?/forums/topic/569366-server-confi | with a URLDirect link to the live tuning file. |
| `DifficultyOffset` | 难度偏移值 | 1.0 | float | GameUserSettings.ini + 命令行 | the difficulty level. | the difficulty level. |
| `DisableCryopodEnemyCheck` | 禁用CryopodEnemyCheck | False | boolean | GameUserSettings.ini + 命令行 | True, allows cryopods to be used while enemies are nearby. | 允许cryopods to be used while enemies are nearby。 |
| `DisableCryopodFridgeRequirement` | 禁用CryopodFridgeRequirement | False | boolean | GameUserSettings.ini + 命令行 | True, allows cryopods to be used without needing to be in range of a powered cryofridge. | 允许cryopods to be used without needing to be in range of a powered cryofridge。 |
| `DisableWeatherFog` | 禁用WeatherFog | False | boolean | GameUserSettings.ini + 命令行 | True, disables fog. | True, disables fog. |
| `ForceGachaUnhappyInCaves` | ForceGachaUnhappyInCaves | True | boolean | GameUserSettings.ini + 命令行 | True, Gachas will become unhappy within caves. | True, Gachas will become unhappy within caves. |
| `globalVoiceChat` | globalVoiceChat | False | boolean | GameUserSettings.ini + 命令行 | True, voice chat turns global. | True, voice chat turns global. |
| `ImplantSuicideCD` | ImplantSuicideCD | 28800 | float | GameUserSettings.ini | the time (in seconds) a player must wait between 2 uses of the implant's "Respawn" feature. | the time (in seconds) a player must wait between 2 uses of the implant's "Respawn" feature. |
| `NonPermanentDiseases` | NonPermanentDiseases | False | boolean | GameUserSettings.ini + 命令行 | True, makes permanent diseases not permanent. Players will lose them if on re-spawn. | True, makes permanent diseases not permanent. |
| `OxygenSwimSpeedStatMultiplier` | OxygenSwim速度Stat倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | this to set how swim speed is multiplied by level spent in oxygen. The value was reduced by 80% in 256.0. | this to set how swim speed is multiplied by level spent in oxygen. |
| `PlatformSaddleBuildAreaBoundsMultiplier` | PlatformSaddleBuildAreaBounds倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the number allows structures being placed further away from the platform. | 允许structures being placed further away from the platform。 |
| `PreventDiseases` | PreventDiseases | False | boolean | GameUserSettings.ini + 命令行 | True, completely diseases on the server. Thus far just Swamp Fever. | True, completely diseases on the server. |
| `ProximityChat` | ProximityChat | False | boolean | GameUserSettings.ini + 命令行 | True, only players near each other can see their chat messages | True, only players near each other can see their chat messages |
| `RCONEnabled` | RCONEnabled | False | boolean | GameUserSettings.ini + 命令行 | True, enables RCON, needs RCONPort=<TCP_PORT> and ServerAdminPassword=<admin_password> to work. | 启用RCON, needs RCONPort=<TCP_PORT> and ServerAdminPassword=<admin_password> to work。 |
| `RCONPort` | RCONPort | 27020 | integer | GameUserSettings.ini + 命令行 | the optional TCP RCON Port. See Dedicated server setup | the optional TCP RCON Port. |
| `RCONServerGameLogBuffer` | RCONServerGameLogBuffer | 600.0 | float | GameUserSettings.ini + 命令行 | how many lines of game logs are send over the RCON. Note: despite being coded as a float it's suggested to treat it as integer. | how many lines of game logs are send over the RCON. |
| `ServerCrosshair` | ServerCrosshair | True | boolean | GameUserSettings.ini + 命令行 | False, disables the Crosshair on your server. | False, disables the Crosshair on your server. |
| `ServerForceNoHUD` | ServerForceNoHUD | False | boolean | GameUserSettings.ini + 命令行 | True, HUD is always disabled for non-tribe owned NPCs. | True, HUD is always disabled for non-tribe owned NPCs. |
| `ServerHardcore` | ServerHardcore | False | boolean | GameUserSettings.ini + 命令行 | True, enables Hardcore mode (player characters revert to level 1 upon death) | True, enables Hardcore mode (player characters revert to level 1 upon death) |
| `ShowFloatingDamageText` | ShowFloating伤害Text | False | boolean | GameUserSettings.ini + 命令行 | True, enables RPG-style popup damage text mode. | 启用RPG-style popup damage text mode。 |
| `UseAstraeosTraversalBuff` | UseAstraeosTraversalBuff | True | boolean | GameUserSettings.ini | True, enables the biome teleport in Astraeos when holding .mw-parser-output .key{display:inline-block;white-space:nowrap}.mw-parser-output .key kbd{padding:0.1em 0.6em 0.1em 0.6em;margin-right:2px;fon | 启用the biome teleport in Astraeos when holding。 |
| `UseFjordurTraversalBuff` | UseFjordurTraversalBuff | False | boolean | GameUserSettings.ini | True, enables the biome teleport in Fjordur when holding R (enabled in official PvE servers). | 启用the biome teleport in Fjordur when holding R (enabled in official PvE servers)。 |
| `YoungIceFoxDeathCooldown` | YoungIceFoxDeathCooldown | 3600 | float | GameUserSettings.ini + 命令行 | the cooldown for Veilwyn to reappear after taking fatal damage (in seconds), default is set to 1 hour. Must be greater than 0. | the cooldown for Veilwyn to reappear after taking fatal damage (in seconds), default is set to 1 hour. |
| `noTributeDownloads` | noTributeDownloads | False | boolean | GameUserSettings.ini + 命令行 | True, prevents CrossArk-data downloads inCross-ARK Data Transfer. | 阻止CrossArk-data downloads inCross-ARK Data Transfer。 |
| `PreventDownloadSurvivors` | PreventDownloadSurvivors | False | boolean | GameUserSettings.ini + 命令行 | True, prevents survivors download from ARK Data in Cross-ARK Data Transfer. | 阻止survivors download from ARK Data in Cross-ARK Data Transfer。 |
| `PreventUploadSurvivors` | PreventUploadSurvivors | False | boolean | GameUserSettings.ini + 命令行 | True, prevents survivors upload to ARK Data in Cross-ARK Data Transfer. | 阻止survivors upload to ARK Data in Cross-ARK Data Transfer。 |
| `TributeCharacterExpirationSeconds` | TributeCharacterExpirationSeconds | 0 | integer | GameUserSettings.ini + 命令行 | in seconds the expiration timer for uploaded survivors in ARK Data. With default or negative values there is no expiration time. Check Cross-ARK Data Transfer for more details. Warning: do not set thi | in seconds the expiration timer for uploaded survivors in ARK Data. |
| `CryopodNerfDamageMult` | CryopodNerf伤害Mult | 0.0099999998 | float | GameUserSettings.ini + 命令行 | the amount of damage dealt by the creature after it is deployed from the cryopod, as a percentage of total damage output, and for the length of time set by CryopodNerfDuration. CryopodNerfDuration nee | the amount of damage dealt by the creature after it is deployed from the cryopod, as a percentage of total damage output, and for the length of time set by CryopodNerfDuration. |
| `CryopodNerfDuration` | CryopodNerfDuration | 0.0 | integer | GameUserSettings.ini + 命令行 | of time, in seconds, Cryosickness lasts after deploying a creature from a Cryopod. Note: although at code level it is defined as a floating-point number, it is suggested to use an integer instead. On  | of time, in seconds, Cryosickness lasts after deploying a creature from a Cryopod. |
| `CryopodNerfIncomingDamageMultPercent` | CryopodNerfIncoming伤害MultPercent | 0.0 | float | GameUserSettings.ini | the amount of damage taken by the creature after it is deployed from the cryopod, as a percentage of total damage received, and for the length of time set by CryopodNerfDuration. CryopodNerfIncomingDa | the amount of damage taken by the creature after it is deployed from the cryopod, as a percentage of total damage received, and for the length of time set by CryopodNerfDuration. |
| `EnableCryopodNerf` | EnableCryopodNerf | False | boolean | GameUserSettings.ini + 命令行 | True, there is no Cryopod cooldown timer, and creatures do not become unconscious. If this option is set, than EnableCryopodNerf and CryopodNerfIncomingDamageMultPercent must be set as well or they wi | True, there is no Cryopod cooldown timer, and creatures do not become unconscious. |
| `BadWordListURL` | BadWordListURL | : "http://arkdedicated.com/badwords.txt"  : "http://cdn2.arkdedicated.com/asa/badwords.txt" | string | GameUserSettings.ini | with a URLAdd the URL to hosting your own bad words list. Note: on  ARK: Survival Evolved servers only the HTTP protocol is supported (an HTTPS URL will not work). | with a URLAdd the URL to hosting your own bad words list. |
| `BadWordWhiteListURL` | BadWordWhiteListURL | : "http://arkdedicated.com/goodwords.txt"  : "http://cdn2.arkdedicated.com/asa/goodwords.txt" | string | GameUserSettings.ini | with a URLAdd the URL to hosting your own good words list. Note: on  ARK: Survival Evolved servers only the HTTP protocol is supported (an HTTPS URL will not work). | with a URLAdd the URL to hosting your own good words list. |
| `bFilterCharacterNames` | FilterCharacterNames | False | boolean | GameUserSettings.ini | True, filters out character names based on the bad words/good words list. | True, filters out character names based on the bad words/good words list. |
| `bFilterChat` | FilterChat | False | boolean | GameUserSettings.ini | True, filters out character names based on the bad word/good words list. | True, filters out character names based on the bad word/good words list. |
| `AllowBunkersInPreventionZones` | 允许BunkersInPreventionZones | False | boolean | GameUserSettings.ini | Default value: FalseValue type: boolean | Default value: FalseValue type: boolean |
| `MinDistanceBetweenBunkers` | MinDistanceBetweenBunkers | 3000.0 | float | GameUserSettings.ini | Default value: 3000.0Value type: float | Default value: 3000.0Value type: float |
| `EnemyAccessBunkerHPThreshold` | EnemyAccessBunkerHPThreshold | 0.25 | float | GameUserSettings.ini | Default value: 0.25Value type: float | Default value: 0.25Value type: float |
| `BunkerUnderHPThresholdDmgMultiplier` | BunkerUnderHPThresholdDmg倍率 | 0.05 | float | GameUserSettings.ini | Default value: 0.05Value type: float | Default value: 0.05Value type: float |
| `CryoHospitalHoursToRegenHP` | CryoHospitalHoursToRegenHP | 1.0 | float | GameUserSettings.ini | Default value: 1.0Value type: float | Default value: 1.0Value type: float |
| `CryoHospitalMatingCooldownReduction` | CryoHospitalMatingCooldownReduction | 2.0 | float | GameUserSettings.ini | Default value: 2.0Value type: float | Default value: 2.0Value type: float |
| `BloodforgeReinforceExtraDurability` | BloodforgeReinforceExtraDurability | 0.3 | float | GameUserSettings.ini | Default value: 0.3Value type: float | Default value: 0.3Value type: float |
| `BloodforgeReinforceSpeedMultiplier` | BloodforgeReinforce速度倍率 | 0.1 | float | GameUserSettings.ini | Default value: 0.1Value type: float | Default value: 0.1Value type: float |
| `OutpostSigilRewardMultiplier` | OutpostSigilReward倍率 | 1.0 | float | GameUserSettings.ini | the scaling factor for sigil rewards from outpost missions. Higher values increase the number of sigils rewarded. | the scaling factor for sigil rewards from outpost missions. |
| `ForceFlyerExplosives` | ForceFlyerExplosives | False | boolean | GameUserSettings.ini | True, allowed flyers (except Quetzal and Wyvern) to fly with C4 attached to it. Deprecated since 253.95. | True, allowed flyers (except Quetzal and Wyvern) to fly with C4 attached to it. |
| `AutoRestartIntervalSeconds` | AutoRestart间隔Seconds | Unknown | float | GameUserSettings.ini + 命令行 | the time (in seconds) after which the server will automatically restart. Undocumented by Wildcard. (Appears to shut off the server instead of restarting properly) | the time (in seconds) after which the server will automatically restart. |
| `ChatLogFileSplitIntervalSeconds` | ChatLogFileSplit间隔Seconds | 86400 | integer | GameUserSettings.ini | how to split the chat log file related to time in seconds. Cannot be set to a value lower than 45 (will default to 45 if the value is lower). Set to 0 only in official. Undocumented by Wildcard. | how to split the chat log file related to time in seconds. |
| `ChatLogFlushIntervalSeconds` | ChatLogFlush间隔Seconds | 86400 | integer | GameUserSettings.ini | in how many second the chat log is flushed to log file. Cannot be set to a value lower than 15 (will default to 15 if the value is lower). Set to 0 only in official. Undocumented by Wildcard. | in how many second the chat log is flushed to log file. |
| `DontRestoreBackup` | DontRestoreBackup | False | boolean | GameUserSettings.ini + 命令行 | True and -DisableDupeLogDeletes is present, prevents the server to automatically restore a backup in case of corrupted save. Undocumented by Wildcard. | 阻止the server to automatically restore a backup in case of corrupted save。 |
| `EnableMeshBitingProtection` | EnableMeshBitingProtection | True | boolean | GameUserSettings.ini + 命令行 | False, disables mesh biting protection. Undocumented by Wildcard. | False, disables mesh biting protection. |
| `FreezeReaperPregnancy` | FreezeReaperPregnancy | False | boolean | GameUserSettings.ini | True, freezes the  Reaper King pregnancy timer and experience gain. Undocumented by Wildcard. | True, freezes the  Reaper King pregnancy timer and experience gain. |
| `LogChatMessages` | LogChatMessages | False | boolean | GameUserSettings.ini | True, enables advanced chat logging. Chat logs will be saved in ShooterGame/Saved/Logs/ChatLogs/<SessionName>/ in json format. Disabled on official. The file will be split according to ChatLogFileSpli | 启用advanced chat logging。 |
| `ServerEnableMeshChecking` | ServerEnableMeshChecking | False | boolean | GameUserSettings.ini + 命令行 | in foliage repopulation. Takes no effect if -forcedisablemeshchecking is set. Enabled on official. Undocumented. | in foliage repopulation. |
| `UseCharacterTracker` | UseCharacterTracker | False | boolean | GameUserSettings.ini + 命令行 | to enable character tracking. Alternatively, this option can be configured with -disableCharacterTracker argument in the command line (note that the argument from command line has priority over the va | to enable character tracking. |
| `UseExclusiveList` | UseExclusiveList | False | boolean | GameUserSettings.ini + 命令行 | True, allows same behaviour as -exclusivejoin. Undocumented by Wildcard. | 允许same behaviour as -exclusivejoin。 |
| `ListenServerTetherDistanceMultiplier` | ListenServerTetherDistance倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the tether distance between host and other players on non-dedicated sessions only. Note: despite being readable from command line, this option affects non-dedicated sessions only, thus it has to be se | the tether distance between host and other players on non-dedicated sessions only. |

#### 生物设置 (34项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowCrateSpawnsOnTopOfStructures` | 允许补给箱在建筑上生成 | False | boolean | GameUserSettings.ini + 命令行 | True, allows from-the-air Supply Crates to appear on top of Structures, rather than being prevented by Structures. | 允许from-the-air Supply Crates to appear on top of Structures, rather than being prevented by Structures。 |
| `AllowRaidDinoFeeding` | 允许Raid恐龙Feeding | False | boolean | GameUserSettings.ini + 命令行 | True, allows Titanosaurs to be permanently tamed (namely allow them to be fed). Note: in The Island only spawns a maximum of 3 Titanosaurs, so 3 tamed ones should ultimately block any more ones from s | 允许Titanosaurs to be permanently tamed (namely allow them to be fed)。 |
| `AutoDestroyDecayedDinos` | AutoDestroyDecayed恐龙s | False | boolean | GameUserSettings.ini + 命令行 | True, auto-destroys claimable decayed tames on load, rather than have them remain around as claimable. Note: after patch 273.691, in PvE mode the tame auto-unclaim after decay period has been disabled | True, auto-destroys claimable decayed tames on load, rather than have them remain around as claimable. |
| `DinoCharacterFoodDrainMultiplier` | 恐龙CharacterFoodDrain倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for creatures' food consumption. Higher values increase food consumption (creatures get hungry faster). It also affects the taming-times. | the scaling factor for creatures' food consumption. |
| `DinoCharacterHealthRecoveryMultiplier` | 恐龙CharacterHealthRecovery倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for creatures' health recovery. Higher values increase the recovery rate (creatures heal faster). | the scaling factor for creatures' health recovery. |
| `DinoCharacterStaminaDrainMultiplier` | 恐龙CharacterStaminaDrain倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for creatures' stamina consumption. Higher values increase stamina consumption (creatures get tired faster). | the scaling factor for creatures' stamina consumption. |
| `DinoCountMultiplier` | 恐龙数量倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for creature spawns. Higher values increase the number of creatures spawned throughout the ARK. | the scaling factor for creature spawns. |
| `DinoDamageMultiplier` | 恐龙伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the damage wild creatures deal with their attacks. The default value 1 provides normal damage. Higher values increase damage. Lower values decrease it. | the scaling factor for the damage wild creatures deal with their attacks. |
| `DinoResistanceMultiplier` | 恐龙抗性倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the resistance to damage wild creatures receive when attacked. The default value 1 provides normal damage. Higher values decrease resistance, increasing damage per attack. Lower | the scaling factor for the resistance to damage wild creatures receive when attacked. |
| `DisableDinoDecayPvE` | 禁用恐龙DecayPvE | False | boolean | GameUserSettings.ini + 命令行 | True, disables the creature decay in PvE mode. Note: after patch 273.691, in PvE mode the creature auto-unclaim after decay period has been disabled. | True, disables the creature decay in PvE mode. |
| `MaxPersonalTamedDinos` | 最大Personal驯养d恐龙s | 0 | integer | GameUserSettings.ini + 命令行 | a per-tribe creature tame limit (500 on official PvE servers, 300 in official PvP servers). The default value of 0 disables such limit. | a per-tribe creature tame limit (500 on official PvE servers, 300 in official PvP servers). |
| `MaxTamedDinos` | 最大驯养d恐龙s | 5000.0 | float | GameUserSettings.ini + 命令行 | the maximum number of tame creatures on a server, this is a global cap. Note: although at code level it is defined as a floating-point number, it is suggested to use an integer instead. | the maximum number of tame creatures on a server, this is a global cap. |
| `MaxTamedDinos_SoftTameLimit` | 最大驯养d恐龙s_SoftTameLimit | 5000 | integer | GameUserSettings.ini + 命令行 | the server-wide soft tame limit. See DestroyTamesOverTheSoftTameLimit for more info. | the server-wide soft tame limit. |
| `MaxTamedDinos_SoftTameLimit_CountdownForDeletionDuration` | 最大驯养d恐龙s_SoftTameLimit_CountdownForDeletionDuration | 604800 | integer | GameUserSettings.ini + 命令行 | the time (in seconds) for tame to get destroyed. See DestroyTamesOverTheSoftTameLimit for more info. | the time (in seconds) for tame to get destroyed. |
| `MaxTributeDinos` | 最大Tribute恐龙s | 20 | integer | GameUserSettings.ini + 命令行 | for uploaded creatures. Any value less than default will be reverted. Note: Some player claimed maximum 273 to be safe cap and more will corrupt profile/cluster and lead to lose of all stored creature | for uploaded creatures. |
| `NPCNetworkStasisRangeScalePercentEnd` | NPCNetworkStasisRangeScalePercentEnd | 0.55000001 | float | GameUserSettings.ini | Maximum scale percentage used when NPCNetworkStasisRangeScalePlayerCountEnd is reached (requires inputting into INI, not command line). Used to override the NPC Network Stasis Range Scale (to scale se | Maximum scale percentage used when NPCNetworkStasisRangeScalePlayerCountEnd is reached (requires inputting into INI, not command line). |
| `PersonalTamedDinosSaddleStructureCost` | Personal驯养d恐龙sSaddle建筑Cost | 0 | integer | GameUserSettings.ini + 命令行 | the amount of "tame creature slots" a platform saddle (with structures) will use towards the tribe tame creature limit. | the amount of "tame creature slots" a platform saddle (with structures) will use towards the tribe tame creature limit. |
| `PreventSpawnAnimations` | PreventSpawnAnimations | False | boolean | GameUserSettings.ini + 命令行 | True, player characters (re)spawn without the wake-up animation. | True, player characters (re)spawn without the wake-up animation. |
| `PvEDinoDecayPeriodMultiplier` | PvE恐龙DecayPeriod倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | PvE auto-decay time multiplier. Requires DisableDinoDecayPvE=false in GameUserSettings.ini or ?DisableDinoDecayPvE=false in command line to work. | PvE auto-decay time multiplier. |
| `PvPDinoDecay` | PvP恐龙Decay | False | boolean | GameUserSettings.ini + 命令行 | True, enables creatures' decay in PvP while the Offline Raid Prevention is active. | 启用creatures' decay in PvP while the Offline Raid Prevention is active。 |
| `RaidDinoCharacterFoodDrainMultiplier` | Raid恐龙CharacterFoodDrain倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | how quickly the food drains on such "raid dinos" (e.g.: Titanosaurus) | how quickly the food drains on such "raid dinos" (e.g.: Titanosaurus) |
| `ResourcesRespawnPeriodMultiplier` | 资源重生周期倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the re-spawn rate for resource nodes (trees, rocks, bushes, etc.). Lower values cause nodes to re-spawn more frequently. | the scaling factor for the re-spawn rate for resource nodes (trees, rocks, bushes, etc.). |
| `ServerAutoForceRespawnWildDinosInterval` | ServerAutoForceRespawn野生恐龙s间隔 | 0.0 | float | GameUserSettings.ini + 命令行 | re-spawn of all wild creatures on server restart afters the value set in seconds. Default value of 0.0 disables it. Useful to prevent certain creature species (like the Basilo and Spino) from becoming | re-spawn of all wild creatures on server restart afters the value set in seconds. |
| `TamedDinoDamageMultiplier` | 驯养d恐龙伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the damage tame creatures deal with their attacks. The default value 1 provides normal damage. Higher values increase damage. Lower values decrease it. | the scaling factor for the damage tame creatures deal with their attacks. |
| `TamedDinoResistanceMultiplier` | 驯养d恐龙抗性倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the resistance to damage tame creatures receive when attacked. The default value 1 provides normal damage. Higher values decrease resistance, increasing damage per attack. Lower | the scaling factor for the resistance to damage tame creatures receive when attacked. |
| `CrossARKAllowForeignDinoDownloads` | CrossARK允许Foreign恐龙Downloads | False | boolean | GameUserSettings.ini + 命令行 | True, enables non-native creatures tribute download on Aberration. | 启用non-native creatures tribute download on Aberration。 |
| `MinimumDinoReuploadInterval` | Minimum恐龙Reupload间隔 | 0.0 | float | GameUserSettings.ini + 命令行 | of seconds cool-down between allowed creature re-uploads (43200 on official Servers which is 12 hours). | of seconds cool-down between allowed creature re-uploads (43200 on official Servers which is 12 hours). |
| `PreventDownloadDinos` | PreventDownload恐龙s | False | boolean | GameUserSettings.ini + 命令行 | True, prevents creatures download from ARK Data in Cross-ARK Data Transfer. | 阻止creatures download from ARK Data in Cross-ARK Data Transfer。 |
| `PreventUploadDinos` | PreventUpload恐龙s | False | boolean | GameUserSettings.ini + 命令行 | True, prevents creatures upload to ARK Data in Cross-ARK Data Transfer. | 阻止creatures upload to ARK Data in Cross-ARK Data Transfer。 |
| `TributeDinoExpirationSeconds` | Tribute恐龙ExpirationSeconds | 86400 | integer | GameUserSettings.ini + 命令行 | in seconds the expiration timer for uploaded tames in ARK Data. If set to 0 or less will revert to default. Check Cross-ARK Data Transfer for more details. Warning: do not set this option to an insane | in seconds the expiration timer for uploaded tames in ARK Data. |
| `AllowRidingDinosInsideBunkers` | 允许Riding恐龙sInsideBunkers | True | boolean | GameUserSettings.ini | Default value: TrueValue type: boolean | Default value: TrueValue type: boolean |
| `AllowDinoAIInsideBunkers` | 允许恐龙AIInsideBunkers | True | boolean | GameUserSettings.ini | Default value: TrueValue type: boolean | Default value: TrueValue type: boolean |
| `CryoHospitalHoursToRegenFood` | CryoHospitalHoursToRegenFood | 24.0 | float | GameUserSettings.ini | Default value: 24.0Value type: float | Default value: 24.0Value type: float |
| `CryoHospitalHoursToDrainTorpor` | CryoHospitalHoursToDrainTorpor | 1.0 | float | GameUserSettings.ini | Default value: 1.0Value type: float | Default value: 1.0Value type: float |

#### 建筑与防御 (31项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowCaveBuildingPvE` | PvE允许洞穴建造 | False | boolean | GameUserSettings.ini | True, allows building in caves when PvE mode is also enabled. Note: no more working in command-line options before patch 241.5. | 允许building in caves when PvE mode is also enabled。 |
| `AllowCaveBuildingPvP` | PvP允许洞穴建造 | True | boolean | GameUserSettings.ini | False, prevents building in caves when PvP mode is also enabled. | 阻止building in caves when PvP mode is also enabled。 |
| `AllowIntegratedSPlusStructures` | 允许IntegratedSPlus建筑s | True | booleanif | GameUserSettings.ini + 命令行 | False, disables all of the new S+ structures (intended mainly for letting unofficial servers that want to keep using the S+ mod version to keep using that without a ton of extra duplicate structures). | False, disables all of the new S+ structures (intended mainly for letting unofficial servers that want to keep using the S+ mod version to keep using  |
| `AlwaysAllowStructurePickup` | Always允许建筑Pickup | False | boolean | GameUserSettings.ini + 命令行 | True disables the timer on the quick pick-up system. | True disables the timer on the quick pick-up system. |
| `AutoDestroyOldStructuresMultiplier` | AutoDestroyOld建筑s倍率 | 0.0 | float | GameUserSettings.ini + 命令行 | auto-destruction of structures only after sufficient "no nearby tribe" time has passed (defined as a multiplier of the Allow Claim period). To enable it, set it to 1.0. Useful for servers to clear off | auto-destruction of structures only after sufficient "no nearby tribe" time has passed (defined as a multiplier of the Allow Claim period). |
| `DisableStructureDecayPvE` | 禁用建筑DecayPvE | False | boolean | GameUserSettings.ini + 命令行 | True, disables the gradual auto-decay of player structures. | True, disables the gradual auto-decay of player structures. |
| `EnableExtraStructurePreventionVolumes` | EnableExtra建筑PreventionVolumes | False | boolean | GameUserSettings.ini + 命令行 | True, disables building in specific resource-rich areas, in particular setup on The Island around the major mountains. | True, disables building in specific resource-rich areas, in particular setup on The Island around the major mountains. |
| `FastDecayUnsnappedCoreStructures` | FastDecayUnsnappedCore建筑s | False | boolean | GameUserSettings.ini + 命令行 | True, unsnapped foundations/pillars/fences/Tek Dedicated Storage will decay after the time stated by FastDecayInterval in Game.ini (default is 12 hours). Before 259.0, it set the decay time for such s | True, unsnapped foundations/pillars/fences/Tek Dedicated Storage will decay after the time stated by FastDecayInterval in Game.ini (default is 12 hours). |
| `ForceAllStructureLocking` | ForceAll建筑Locking | False | boolean | GameUserSettings.ini + 命令行 | True, will default lock all structures. | True, will default lock all structures. |
| `IgnoreLimitMaxStructuresInRangeTypeFlag` | IgnoreLimit最大建筑sInRangeTypeFlag | False | boolean | GameUserSettings.ini | True, removes the limit of 150 decorative structures (flags, signs, dermis etc.). | True, removes the limit of 150 decorative structures (flags, signs, dermis etc.). |
| `MaxPlatformSaddleStructureLimit` | 最大PlatformSaddle建筑Limit | 75 | integer | GameUserSettings.ini + 命令行 | the maximum number of platformed-creatures/rafts allowed on the ARK (a potential performance cost). Example: MaxPlatformSaddleStructureLimit=10 would only allow 10 platform saddles/rafts across the en | the maximum number of platformed-creatures/rafts allowed on the ARK (a potential performance cost). |
| `OnlyAutoDestroyCoreStructures` | OnlyAutoDestroyCore建筑s | False | boolean | GameUserSettings.ini + 命令行 | True, prevents any non-core/non-foundation structures from auto-destroying (however they'll still get auto-destroyed if a floor that they're on gets auto-destroyed). Official PvE Servers used this opt | 阻止any non-core/non-foundation structures from auto-destroying (however they'll still get auto-destroyed if a floor that they're on gets auto-destroyed)。 |
| `OnlyDecayUnsnappedCoreStructures` | OnlyDecayUnsnappedCore建筑s | False | boolean | GameUserSettings.ini + 命令行 | True, only unsnapped core structures will decay. Useful for eliminating lone pillar/foundation spam. | True, only unsnapped core structures will decay. |
| `OverrideStructurePlatformPrevention` | 覆盖建筑PlatformPrevention | False | boolean | GameUserSettings.ini + 命令行 | True, turrets becomes be buildable and functional on platform saddles. Since 247.999 applies on spike structure too. Note: despite patch notes, in ShooterGameServer it's coded OverrideStructurePlatfor | True, turrets becomes be buildable and functional on platform saddles. |
| `PerPlatformMaxStructuresMultiplier` | PerPlatform最大建筑s倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | value increases (from a percentage scale) max number of items place-able on saddles and rafts. | value increases (from a percentage scale) max number of items place-able on saddles and rafts. |
| `PvEAllowStructuresAtSupplyDrops` | PvE允许建筑sAtSupplyDrops | False | boolean | GameUserSettings.ini + 命令行 | True, allows building near supply drop points in PvE mode. | 允许building near supply drop points in PvE mode。 |
| `PvEStructureDecayPeriodMultiplier` | PvE建筑DecayPeriod倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for  structures decay times, e.g.: setting it at 2.0 will double all structure decay times, while setting at 0.5 will halve the timers. Note: despite the name, works in both PvP and | the scaling factor for  structures decay times, e.g.: setting it at 2.0 will double all structure decay times, while setting at 0.5 will halve the timers. |
| `PvPStructureDecay` | PvP建筑Decay | False | boolean | GameUserSettings.ini + 命令行 | True, enables structures decay on PvP servers while the Offline Raid Prevention is active. | 启用structures decay on PvP servers while the Offline Raid Prevention is active。 |
| `StructureDamageMultiplier` | 建筑伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the damage structures deal with their attacks (i.e., spiked walls). Higher values increase damage. Lower values decrease it. | the scaling factor for the damage structures deal with their attacks (i.e., spiked walls). |
| `StructurePickupHoldDuration` | 建筑PickupHoldDuration | 0.5 | float | GameUserSettings.ini + 命令行 | the quick pick-up hold duration, a value of 0 results in instant pick-up. | the quick pick-up hold duration, a value of 0 results in instant pick-up. |
| `StructureResistanceMultiplier` | 建筑抗性倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the resistance to damage structures receive when attacked. The default value 1 provides normal damage. Higher values decrease resistance, increasing damage per attack. Lower val | the scaling factor for the resistance to damage structures receive when attacked. |
| `TheMaxStructuresInRange` | The最大建筑sInRange | 10500 | integer | GameUserSettings.ini + 命令行 | the maximum number of structures that can be constructed within a certain (currently hard-coded) range. Replaces the old value NewMaxStructuresInRange | the maximum number of structures that can be constructed within a certain (currently hard-coded) range. |
| `TribeLogDestroyedEnemyStructures` | TribeLogDestroyedEnemy建筑s | False | boolean | GameUserSettings.ini + 命令行 | default, enemy structure destruction (for the victim tribe) is not displayed in the tribe Logs, set this to true to enable it. | default, enemy structure destruction (for the victim tribe) is not displayed in the tribe Logs, set this to true to enable it. |
| `AllowDeprecatedStructures` | 允许Deprecated建筑s | False | boolean | GameUserSettings.ini + 命令行 | True, allows servers to keep the Halloween Structures for a while after event ends (had to be used before relaunching the server with 222.3 update). Since no more events are planned to be activated or | 允许servers to keep the Halloween Structures for a while after event ends (had to be used before relaunching the server with 222。 |
| `bDisableStructureDecayPvE` | 禁用建筑DecayPvE | False | boolean | GameUserSettings.ini | DisableStructureDecayPvE. | DisableStructureDecayPvE. |
| `MaxStructuresInRange` | 最大建筑sInRange | 1300 | integer | GameUserSettings.ini | the maximum number of structures that can be constructed within a certain (currently hard-coded) range. Deprecated with patch 188.0 by NewMaxStructuresInRange. | the maximum number of structures that can be constructed within a certain (currently hard-coded) range. |
| `NewMaxStructuresInRange` | New最大建筑sInRange | 6000 | integer | GameUserSettings.ini | the maximum number of structures that can be constructed within a certain (currently hard-coded) range. Deprecated with patch 252.1 by TheMaxStructuresInRange. | the maximum number of structures that can be constructed within a certain (currently hard-coded) range. |
| `PvEStructureDecayDestructionPeriod` | PvE建筑DecayDestructionPeriod | 0 | integer | GameUserSettings.ini | the time required for player structures to decay in PvE mode. Deprecated and no more present in executable bits with patch 180.0 where each type of structure has its own decay time, increasing with "t | the time required for player structures to decay in PvE mode. |
| `MaxStructuresInSmallRadius` | 最大建筑sInSmallRadius | 0 | integer | GameUserSettings.ini + 命令行 | the amount of max structures allowed to be placed in a RadiusStructuresInSmallRadius from player position. Official set it to 40. Undocumented by Wildcard. | the amount of max structures allowed to be placed in a RadiusStructuresInSmallRadius from player position. |
| `MaxStructuresToProcess` | 最大建筑sToProcess | 0 | integer | GameUserSettings.ini + 命令行 | the max batch size of structures to process (e.g.: culling, building graphs modifications, etc) at each server tick. Leaving at 0 (default behaviour) will force the server to process all structures in | the max batch size of structures to process (e.g.: culling, building graphs modifications, etc) at each server tick. |
| `RadiusStructuresInSmallRadius` | Radius建筑sInSmallRadius | 0.0 | float | GameUserSettings.ini + 命令行 | the small radius dimension (in Unreal Units) used by MaxStructuresInSmallRadius. Official set it to 225.0. Undocumented by Wildcard. | the small radius dimension (in Unreal Units) used by MaxStructuresInSmallRadius. |

#### 繁殖与成长 (17项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowAnyoneBabyImprintCuddle` | 允许任何人照料婴儿 | False | boolean | GameUserSettings.ini + 命令行 | True, allows anyone to "take care" of a baby creatures (cuddle etc.), not just whomever imprinted on it. | 允许anyone to "take care" of a baby creatures (cuddle etc。 |
| `AllowThirdPersonPlayer` | 允许第三人称视角 | True | boolean | GameUserSettings.ini + 命令行 | False, disables third person camera allowed by default on all dedicated servers. | False, disables third person camera allowed by default on all dedicated servers. |
| `AlwaysNotifyPlayerLeft` | 始终通知玩家离开 | False | boolean | GameUserSettings.ini + 命令行 | True, players will always get notified if someone leaves the server | True, players will always get notified if someone leaves the server |
| `DisableImprintDinoBuff` | 禁用Imprint恐龙Buff | False | boolean | GameUserSettings.ini + 命令行 | True, disables the creature imprinting player Stat Bonus. Where whomever specifically imprinted on the creature, and raised it to have an Imprinting Quality, gets extra Damage/Resistance buff. | True, disables the creature imprinting player Stat Bonus. |
| `DontAlwaysNotifyPlayerJoined` | DontAlwaysNotify玩家Joined | False | boolean | GameUserSettings.ini + 命令行 | True, globally disables player joins notifications. | True, globally disables player joins notifications. |
| `KickIdlePlayersPeriod` | KickIdle玩家sPeriod | 3600.0 | float | GameUserSettings.ini + 命令行 | in seconds after which characters that have not moved or interacted will be kicked (if -EnableIdlePlayerKick as command line parameter is set). Note: although at code level it is defined as a floating | in seconds after which characters that have not moved or interacted will be kicked (if -EnableIdlePlayerKick as command line parameter is set). |
| `NPCNetworkStasisRangeScalePlayerCountStart` | NPCNetworkStasisRangeScale玩家CountStart | 0 | integer | GameUserSettings.ini | number of online players when the NPC Network Stasis Range Scale override is enabled (requires inputting into INI, not command line). Used to override the NPC Network Stasis Range Scale (to scale serv | number of online players when the NPC Network Stasis Range Scale override is enabled (requires inputting into INI, not command line). |
| `NPCNetworkStasisRangeScalePlayerCountEnd` | NPCNetworkStasisRangeScale玩家CountEnd | 0 | integer | GameUserSettings.ini | number of online players when NPCNetworkStasisRangeScalePercentEnd is reached (requires inputting into INI, not command line). Used to override the NPC Network Stasis Range Scale (to scale server perf | number of online players when NPCNetworkStasisRangeScalePercentEnd is reached (requires inputting into INI, not command line). |
| `PlayerCharacterFoodDrainMultiplier` | 玩家CharacterFoodDrain倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for player characters' food consumption. Higher values increase food consumption (player characters get hungry faster). | the scaling factor for player characters' food consumption. |
| `PlayerCharacterHealthRecoveryMultiplier` | 玩家CharacterHealthRecovery倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for player characters' health recovery. Higher values increase the recovery rate (player characters heal faster). | the scaling factor for player characters' health recovery. |
| `PlayerCharacterStaminaDrainMultiplier` | 玩家CharacterStaminaDrain倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for player characters' stamina consumption. Higher values increase stamina consumption (player characters get tired faster). | the scaling factor for player characters' stamina consumption. |
| `PlayerCharacterWaterDrainMultiplier` | 玩家CharacterWaterDrain倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for player characters' water consumption. Higher values increase water consumption (player characters get thirsty faster). | the scaling factor for player characters' water consumption. |
| `PlayerDamageMultiplier` | 玩家伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the damage players deal with their attacks. The default value 1 provides normal damage. Higher values increase damage. Lower values decrease it. | the scaling factor for the damage players deal with their attacks. |
| `PlayerResistanceMultiplier` | 玩家受到伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the resistance to damage players receive when attacked. The default value 1 provides normal damage. Higher values decrease resistance, increasing damage per attack. Lower values | the scaling factor for the resistance to damage players receive when attacked. |
| `PreventMateBoost` | PreventMateBoost | False | boolean | GameUserSettings.ini + 命令行 | True, disables creature mate boosting. | True, disables creature mate boosting. |
| `ShowMapPlayerLocation` | 地图显示玩家位置 | True | boolean | GameUserSettings.ini + 命令行 | False, hides each player their own precise position when they view their map. | False, hides each player their own precise position when they view their map. |
| `EnableAFKKickPlayerCountPercent` | EnableAFKKick玩家CountPercent | 0.0 | float | GameUserSettings.ini | the idle timeout to be applied only if the amount of online players reaches percentage value related to MaxPlayers argument. The percentage is expressed as normalised value between 0 and 1.0, where 1. | the idle timeout to be applied only if the amount of online players reaches percentage value related to MaxPlayers argument. |

#### PvP与部落 (16项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowFlyerCarryPvE` | PvE允许飞行抓取 | False | boolean | GameUserSettings.ini + 命令行 | True, allows flying creatures to pick up wild creatures in PvE. | 允许flying creatures to pick up wild creatures in PvE。 |
| `DisablePvEGamma` | 禁用PvEGamma | False | boolean | GameUserSettings.ini + 命令行 | True, prevents use of console command "gamma" in PvE mode. | 阻止use of console command "gamma" in PvE mode。 |
| `EnablePvPGamma` | EnablePvPGamma | False | boolean | GameUserSettings.ini + 命令行 | True, allows use of console command "gamma" in PvP mode. | 允许use of console command "gamma" in PvP mode。 |
| `PreventOfflinePvP` | PreventOfflinePvP | False | boolean | GameUserSettings.ini + 命令行 | True, enables the Offline Raiding Prevention (ORP). When all tribe members are logged off, tribe characters, creature and structures become invulnerable. Creature starvation still applies, moreover, c | 启用the Offline Raiding Prevention (ORP)。 |
| `PreventOfflinePvPInterval` | PreventOfflinePvP间隔 | 0.0 | float | GameUserSettings.ini + 命令行 | to wait before a ORP becomes active for tribe/players and relative creatures/structures (10 seconds in official PvE servers). Note: although at code level it is defined as a floating-point number, it  | to wait before a ORP becomes active for tribe/players and relative creatures/structures (10 seconds in official PvE servers). |
| `PreventTribeAlliances` | PreventTribeAlliances | False | boolean | GameUserSettings.ini + 命令行 | True, prevents tribes from creating Alliances. | 阻止tribes from creating Alliances。 |
| `serverPVE` | serverPVE | False | boolean | GameUserSettings.ini + 命令行 | True, disables PvP and enables PvE | True, disables PvP and enables PvE |
| `TribeNameChangeCooldown` | TribeNameChangeCooldown | 15.0 | float | GameUserSettings.ini + 命令行 | in minutes, in between tribe name changes. Official server use a value of 172800.0 (2 days). | in minutes, in between tribe name changes. |
| `EnableCryoSicknessPVE` | EnableCryoSicknessPVE | False | boolean | GameUserSettings.ini | True, enables Cryopod cooldown timer when deploying a creature. | 启用Cryopod cooldown timer when deploying a creature。 |
| `bFilterTribeNames` | FilterTribeNames | False | boolean | GameUserSettings.ini | True, filters out tribe names based on the badwords/goodwords list. | True, filters out tribe names based on the badwords/goodwords list. |
| `LimitBunkersPerTribe` | LimitBunkersPerTribe | True | boolean | GameUserSettings.ini | Default value: TrueValue type: boolean | Default value: TrueValue type: boolean |
| `LimitBunkersPerTribeNum` | LimitBunkersPerTribeNum | 3 | integer | GameUserSettings.ini | Default value: 3Value type: integer | Default value: 3Value type: integer |
| `bAllowFlyerCarryPVE` | 允许FlyerCarryPVE | False | boolean | GameUserSettings.ini | AllowFlyerCarryPvE. | AllowFlyerCarryPvE. |
| `PreventOutOfTribePinCodeUse` | PreventOutOfTribePinCodeUse | False | boolean | GameUserSettings.ini + 命令行 | True, prevents out of tribe players to use pins on structures (doors, elevators, storage boxes, etc). Undocumented by Wildcard. | 阻止out of tribe players to use pins on structures (doors, elevators, storage boxes, etc)。 |
| `TribeMergeAllowed` | TribeMerge允许ed | True | boolean | GameUserSettings.ini | False, prevents tribe to merge. Undocumented by Wildcard. | 阻止tribe to merge。 |
| `TribeMergeCooldown` | TribeMergeCooldown | 0.0 | float | GameUserSettings.ini | merge cool-down in seconds. Official uses 86400.0. Undocumented by Wildcard. | merge cool-down in seconds. |

#### 时间与天气 (8项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `ClampItemSpoilingTimes` | ClampItemSpoilingTimes | False | boolean | GameUserSettings.ini + 命令行 | True, clamps all spoiling times to the items' maximum spoiling times. Useful if any infinite-spoiling exploits were used on the server and you wish to clean them up. Could potentially cause issues wit | True, clamps all spoiling times to the items' maximum spoiling times. |
| `DayCycleSpeedScale` | 昼夜循环速度倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the passage of time in the ARK, controlling how often day changes to night and night changes to day. The default value 1 provides the same cycle speed as the single player exper | the scaling factor for the passage of time in the ARK, controlling how often day changes to night and night changes to day. |
| `DayTimeSpeedScale` | 白天时间速度倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the passage of time in the ARK during the day. This value determines the length of each day, relative to the length of each night (as specified by NightTimeSpeedScale). Lowering | the scaling factor for the passage of time in the ARK during the day. |
| `DisableBurrowDecayTimers` | 禁用BurrowDecayTimers | False | boolean | GameUserSettings.ini + 命令行 | True, turns off entirely the Burrowbuck's burrow decay timers. | True, turns off entirely the Burrowbuck's burrow decay timers. |
| `ExtinctionEventTimeInterval` | ExtinctionEventTime间隔 | - | secondsUsed | GameUserSettings.ini + 命令行 | to enable the extinction mode (ARKpocalypse). The number is the time in seconds. Use 2592000 value for 30 days. | to enable the extinction mode (ARKpocalypse). |
| `NightTimeSpeedScale` | 夜晚时间速度倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the passage of time in the ARK during night time. This value determines the length of each night, relative to the length of each day (as specified by DayTimeSpeedScale) Lowering | the scaling factor for the passage of time in the ARK during night time. |
| `StructurePickupTimeAfterPlacement` | 建筑PickupTimeAfterPlacement | 30.0 | float | GameUserSettings.ini + 命令行 | of time in seconds after placement that quick pick-up is available. | of time in seconds after placement that quick pick-up is available. |
| `ChatLogMaxAgeInDays` | ChatLog最大AgeInDays | 5 | integer | GameUserSettings.ini | how many days the chat log is long. Set it to a negative value will result it to set at -1 (virtually infinite). Set to 0 only in official. Undocumented by Wildcard. | how many days the chat log is long. |

#### 物品与制作 (7项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `ClampItemStats` | ClampItemStats | False | boolean | GameUserSettings.ini + 命令行 | True, enables stats clamping for items. See ItemStatClamps for more info. | 启用stats clamping for items。 |
| `ItemStackSizeMultiplier` | ItemStackSize倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | increasing or decreasing global item stack size, this means all default stack sizes will be multiplied by the value given (excluding items that have a stack size of 1 by default). | increasing or decreasing global item stack size, this means all default stack sizes will be multiplied by the value given (excluding items that have a |
| `MaxTributeItems` | 最大TributeItems | 50 | integer | GameUserSettings.ini + 命令行 | for uploaded items and resources. Any value less than default will be reverted. Note: Some player claimed maximum 154 to be safe cap and more will corrupt profile/cluster and lead to lose of all store | for uploaded items and resources. |
| `RandomSupplyCratePoints` | RandomSupplyCratePoints | False | boolean | GameUserSettings.ini + 命令行 | True, supply drops are in random locations. Note: This setting is known to cause artifacts becoming inaccessible on Ragnarok if active. | True, supply drops are in random locations. |
| `PreventDownloadItems` | PreventDownloadItems | False | boolean | GameUserSettings.ini + 命令行 | True, prevents items download from ARK Data in Cross-ARK Data Transfer. | 阻止items download from ARK Data in Cross-ARK Data Transfer。 |
| `PreventUploadItems` | PreventUploadItems | False | boolean | GameUserSettings.ini + 命令行 | True, prevents items upload to ARK Data in Cross-ARK Data Transfer. | 阻止items upload to ARK Data in Cross-ARK Data Transfer。 |
| `TributeItemExpirationSeconds` | TributeItemExpirationSeconds | 86400 | integer | GameUserSettings.ini + 命令行 | in seconds the expiration timer for uploaded items in ARK Data. If set to 0 or less will revert to default. Check Cross-ARK Data Transfer for more details. Warning: do not set this option to an insane | in seconds the expiration timer for uploaded items in ARK Data. |

#### 采集与资源 (7项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `ClampResourceHarvestDamage` | Clamp资源采集伤害 | False | boolean | GameUserSettings.ini + 命令行 | True, limit the damage caused by a tame to a resource on harvesting based on resource remaining health.  Note: enabling this setting may result in sensible resource harvesting reduction using high dam | True, limit the damage caused by a tame to a resource on harvesting based on resource remaining health. |
| `HarvestAmountMultiplier` | 采集产量倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for yields from all harvesting activities (chopping down trees, picking berries, carving carcasses, mining rocks, etc.). Higher values increase the amount of materials harvested wit | the scaling factor for yields from all harvesting activities (chopping down trees, picking berries, carving carcasses, mining rocks, etc.). |
| `HarvestHealthMultiplier` | 可采集物生命值倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the "health" of items that can be harvested (trees, rocks, carcasses, etc.). Higher values increase the amount of damage (i.e., "number of strikes") such objects can withstand b | the scaling factor for the "health" of items that can be harvested (trees, rocks, carcasses, etc.). |
| `StructurePreventResourceRadiusMultiplier` | 建筑Prevent资源Radius倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | as ResourceNoReplenishRadiusStructures in Game.ini. If both settings are set both multiplier will be applied. Can be useful when cannot change the Game.ini file as it works as a command line option to | as ResourceNoReplenishRadiusStructures in Game.ini. |
| `UseOptimizedHarvestingHealth` | UseOptimized采集ingHealth | False | boolean | GameUserSettings.ini + 命令行 | True, enables a server harvesting optimization with high HarvestAmountMultiplier (but less rare items). Note: on  ARK: Survival Evolved it's suggested to enable this option if harvesting with Tek Stry | 启用a server harvesting optimization with high HarvestAmountMultiplier (but less rare items)。 |
| `BloodforgeReinforceResourceCostMultiplier` | BloodforgeReinforce资源Cost倍率 | 3.0 | float | GameUserSettings.ini | Default value: 3.0Value type: float | Default value: 3.0Value type: float |
| `MaxActiveResourceCaches` | 最大Active资源Caches | - | integer | GameUserSettings.ini | Value type: integer | Value type: integer |

#### 服务器与性能 (7项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `DestroyTamesOverTheSoftTameLimit` | Destroy驯养sOverTheSoftTameLimit | False | boolean | GameUserSettings.ini + 命令行 | above the Soft Tame Server Limit will be marked “For Cryo” and display an icon and a timer indicating how soon they need to be cryopodded before they are automatically destroyed. Dinos marked and dino | 允许new players to join servers and tame creatures above the 5000 limit, whereas previously, they would not have been able to tame or breed creatures at the server cap。 |
| `MaxCosmoWeaponAmmo` | 最大CosmoWeaponAmmo | -1 | float | GameUserSettings.ini + 命令行 | will make the maximum ammo amount for the Cosmo's webslinger to a set number instead of it scaling with the Cosmo's level. The default of -1 will enable scaling with level. | will make the maximum ammo amount for the Cosmo's webslinger to a set number instead of it scaling with the Cosmo's level. |
| `MaxGateFrameOnSaddles` | 最大GateFrameOnSaddles | 0 | integer | GameUserSettings.ini | the maximum amount of gateways allowed on platform saddles. A value of 2 would prevent players from placing more than 2 gateways on their platform saddles (used in Official PvP servers). This setting  | the maximum amount of gateways allowed on platform saddles. |
| `MaxTrainCars` | 最大TrainCars | 8 | integer | GameUserSettings.ini + 命令行 | the maximum amount of carts a train cave have. | the maximum amount of carts a train cave have. |
| `MaxTributeCharacters` | 最大TributeCharacters | 10 | integer | GameUserSettings.ini + 命令行 | for uploaded characters. Any value less than default will be reverted. Note: rising it may corrupt player/cluster data and lead to lose of all stored characters. | for uploaded characters. |
| `MaxActiveOutposts` | 最大ActiveOutposts | - | integer | GameUserSettings.ini | Value type: integer | Value type: integer |
| `MaxActiveCityOutposts` | 最大ActiveCityOutposts | - | integer | GameUserSettings.ini | Value type: integer | Value type: integer |

#### ASA新增功能 (6项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowCryoFridgeOnSaddle` | 允许冷 Cryo冰箱放在鞍上 | False | boolean | GameUserSettings.ini + 命令行 | True, allows cryofridges to be built on platform saddles and rafts. | 允许cryofridges to be built on platform saddles and rafts。 |
| `AllowFlyingStaminaRecovery` | 允许飞行耐力恢复 | False | boolean | GameUserSettings.ini + 命令行 | True, allows server to recover Stamina when standing on a Flyer. | 允许server to recover Stamina when standing on a Flyer。 |
| `AllowHideDamageSourceFromLogs` | 允许隐藏日志伤害来源 | True | boolean | GameUserSettings.ini + 命令行 | False, shows the damage sources in tribe logs. | False, shows the damage sources in tribe logs. |
| `AllowHitMarkers` | 允许命中标记 | True | boolean | GameUserSettings.ini + 命令行 | False, disables optional markers for ranged attacks. | False, disables optional markers for ranged attacks. |
| `MaxHexagonsPerCharacter` | 最大HexagonsPerCharacter | 2000000000 | integer | GameUserSettings.ini | the max amount of Hexagon a Character can accumulate. Official set it to 2500000. | the max amount of Hexagon a Character can accumulate. |
| `UpdateAllowedCheatersInterval` | Update允许edCheaters间隔 | 600.0 | float | GameUserSettings.ini + 命令行 | in seconds at which the remote admin list linked by AllowedCheatersURL is queried for updates. Any value less than 3.0 will be reverted to 3.0. Undocumented by Wildcard. | in seconds at which the remote admin list linked by AllowedCheatersURL is queried for updates. |

#### 管理员与安全 (5项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AdminLogging` | 管理员命令日志 | False | boolean | GameUserSettings.ini + 命令行 | True, logs all admin commands to in-game chat. | True, logs all admin commands to in-game chat. |
| `ServerAdminPassword` | 管理员密码 | - | string | GameUserSettings.ini + 命令行 | specified, players must provide this password (via the in-game console) to gain access to administrator commands on the server. Note: no quotes are used. | specified, players must provide this password (via the in-game console) to gain access to administrator commands on the server. |
| `ServerPassword` | 服务器连接密码 | - | string | GameUserSettings.ini + 命令行 | specified, players must provide this password to join the server. Note: no quotes are used. | specified, players must provide this password to join the server. |
| `SpectatorPassword` | SpectatorPassword | - | string | GameUserSettings.ini + 命令行 | use non-admin spectator, the server must specify a spectator password. Then any client can use these console commands: requestspectator <password> and stopspectating. Note: no quotes are used. | use non-admin spectator, the server must specify a spectator password. |
| `AdminListURL` | AdminListURL | N/A | string | GameUserSettings.ini + 命令行 | with a URLAlternative to AllowedCheaterAccountIDs.txt (see Administrator Whitelisting) using a web resource. The interval at which the server queries the resource to check for admin list update is def | with a URLAlternative to AllowedCheaterAccountIDs.txt (see Administrator Whitelisting) using a web resource. |

#### Mod与地图 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `ActiveMods` | 启用模组列表 | - | list | GameUserSettings.ini | of mod IDs, comma-separated with no spaces, in a single line (for example: ModID1,ModID2,ModID3)Specifies the order and which mods are loaded. ModIDs are comma separated and in one line. Priority is i | 指定order and which mods are loaded。 |
| `ActiveMapMod` | 启用地图模组 | - | mod | GameUserSettings.ini | ID for currently active mod mapSpecifies which mod map is loaded. | ID for currently active mod mapSpecifies which mod map is loaded. |
| `AllowBunkerModulesAboveGround` | 允许BunkerModulesAboveGround | False | boolean | GameUserSettings.ini | Default value: FalseValue type: boolean | Default value: FalseValue type: boolean |
| `AllowBunkerModulesInPreventionZones` | 允许BunkerModulesInPreventionZones | False | boolean | GameUserSettings.ini | Default value: FalseValue type: boolean | Default value: FalseValue type: boolean |

#### 印痕与等级 (2项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `CosmeticWhitelistOverride` | CosmeticWhitelist覆盖 | - | string | GameUserSettings.ini + 命令行 | with a URLURL to a comma-separated list of whitelisted custom cosmetics, in this format: Mod ID/Enable Dynamic Download (0/1)/Allow non-dataonly blueprints(0/1). See this post for details (note: CRC i | with a URLURL to a comma-separated list of whitelisted custom cosmetics, in this format: Mod ID/Enable Dynamic Download (0/1)/Allow non-dataonly blueprints(0/1). |
| `OverrideOfficialDifficulty` | 覆盖OfficialDifficulty | 0.0 | float | GameUserSettings.ini + 命令行 | you to override the default server difficulty level of 4 with 5 to match the new official server difficulty level. Default value of 0.0 disables the override. A value of 5.0 will allow common creature | you to override the default server difficulty level of 4 with 5 to match the new official server difficulty level. |

#### 创世纪与任务 (1项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowTekSuitPowersInGenesis` | 允许TekSuitPowersInGenesis | False | boolean | GameUserSettings.ini + 命令行 | True, enables TEK suit powers in Genesis: Part 1. | 启用TEK suit powers in Genesis: Part 1。 |

#### 驯养设置 (1项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `TamingSpeedMultiplier` | 驯服速度倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for creature taming speed. Higher values make taming faster. | the scaling factor for creature taming speed. |

#### 经验值倍率 (1项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `XPMultiplier` | 全局经验倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the experience received by players, tribes and tames for various actions. The default value 1 provides the same amounts of experience as in the single player experience (and off | the scaling factor for the experience received by players, tribes and tames for various actions. |



### 2.2 [SessionSettings]

> 会话级设置：端口、服务器名称等

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `MultiHome` | 多宿主IP地址 | N/A | IP_ADDRESSSpecifies | GameUserSettings.ini | MultiHome IP Address. Boolean Multihome option must be set to True as well (command line or [MultiHome] section). Leave it empty if not using multihoming. Can be specified in command line too. | MultiHome IP Address. |
| `Port` | 游戏端口 | 7777 | integer | GameUserSettings.ini | the UDP Game Port. See Dedicated server setupNote: command line append syntax is not supported by  ARK: Survival Ascended | the UDP Game Port. |
| `QueryPort` | Steam查询端口 | 27015 | integer | GameUserSettings.ini | the UDP Steam Query Port. See Dedicated server setup | the UDP Steam Query Port. |
| `SessionName` | 服务器显示名称 | ARK #123456 | string | GameUserSettings.ini | the Server name advertised in the Game Server Browser as well in Steam Server browser. If no name is provide, the default name will be ARK # followed by a random 6 digit number. Note: Name must not be | the Server name advertised in the Game Server Browser as well in Steam Server browser. |


### 2.3 [/Script/Engine.GameSession]

> 引擎级会话设置

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `MaxPlayers` | 服务器最大玩家数 | 70 | integer | GameUserSettings.ini | the maximum number of players that can play on the server simultaneously. ASA: This setting is replaced with -WinLiveMaxPlayers in the command line options, as otherwise, it will force it back to the  | the maximum number of players that can play on the server simultaneously. |


### 2.4 [MessageOfTheDay]

> 玩家登录时显示的每日消息

```ini
[MessageOfTheDay]
Message=欢迎来到服务器！请遵守规则。
Duration=30
```

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `Duration` | 显示持续时间(秒) | 20 | integer | GameUserSettings.ini | in seconds the duration of the displayed message on player log-in. | in seconds the duration of the displayed message on player log-in. |
| `Message` | 消息内容 | N/A | string | GameUserSettings.ini | single line string for a message displayed to played once logged-in. No quotes needed. Use \n to start a new line in the message. | single line string for a message displayed to played once logged-in. |


### 2.5 [Ragnarok] (仙境)

> 仙境地图专属设置

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowMultipleTamedUnicorns` | 允许多只驯服独角兽 | False | boolean | GameUserSettings.ini | = one unicorn on the map at a time, True = one wild and unlimited tamed Unicorns on the map. | = one unicorn on the map at a time, True = one wild and unlimited tamed Unicorns on the map. |
| `EnableVolcano` | 启用火山 | True | boolean | GameUserSettings.ini | = disabled (the volcano will not become active), True = enabled | = disabled (the volcano will not become active), True = enabled |
| `UnicornSpawnInterval` | 独角兽重生间隔(小时) | 24 | integer | GameUserSettings.ini | long in hours the game should wait before spawning a new Unicorn if the wild one is killed (or tamed, if AllowMultipleTamedUnicorns is enabled). This value sets the minimum amount of time (in hours),  | 设置minimum amount of time (in hours), and the maximum is equal to 2x this value。 |
| `VolcanoIntensity` | 火山喷发强度 | 1 | float | GameUserSettings.ini | lower the value, the more intense the volcano's eruption will be. Recommended to leave at 1. The minimum value is 0.25, and for multiplayer games, it should not go below 0.5. Very high numbers will ba | lower the value, the more intense the volcano's eruption will be. |
| `VolcanoInterval` | 火山喷发间隔 | 0 | integer0 | GameUserSettings.ini | = 5000 (min) - 15000 (max) seconds between instances of the volcano becoming active. Any number above 0 acts as a multiplier, with a minimum value of .1 | = 5000 (min) - 15000 (max) seconds between instances of the volcano becoming active. |


---

## 3. Game.ini

**文件路径**: `ShooterGame/Saved/Config/WindowsServer/Game.ini`

**配置方法**: 直接在文件中添加配置项，无需指定 Section

```ini
BabyMatureSpeedMultiplier=5.0
EggHatchSpeedMultiplier=5.0
```

#### 生物设置 (29项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bAllowUnclaimDinos` | 允许放弃恐龙所有权 | True | boolean | Game.ini | False, prevents players to unclaim tame creatures. | 阻止players to unclaim tame creatures。 |
| `bDisableDinoRiding` | 禁用骑乘恐龙 | False | boolean | Game.ini | True, prevents players to ride tames. | 阻止players to ride tames。 |
| `bDisableWirelessCraftingForDinos` | 禁用恐龙无线制作 | false | boolean | Game.ini | True, prevents wireless crafting from Tek Dedicated Storage when crafting in dino inventories. | 阻止wireless crafting from Tek Dedicated Storage when crafting in dino inventories。 |
| `bFlyerPlatformAllowUnalignedDinoBasing` | 飞行平台允许非盟友恐龙站立 | False | boolean | Game.ini | True, Quetz platforms will allow any non-allied tame to base on them when they are flying. | True, Quetz platforms will allow any non-allied tame to base on them when they are flying. |
| `bIncreasePvPRespawnInterval` | 增加PvP重生间隔 | True | boolean | Game.ini | False, disables PvP additional re-spawn time (IncreasePvPRespawnIntervalBaseAmount) that scales (IncreasePvPRespawnIntervalMultiplier) when a player is killed by a team within a certain amount of time | False, disables PvP additional re-spawn time (IncreasePvPRespawnIntervalBaseAmount) that scales (IncreasePvPRespawnIntervalMultiplier) when a player i |
| `bPassiveDefensesDamageRiderlessDinos` | 被动防御伤害无骑手恐龙 | False | boolean | Game.ini | True, allows spike walls to damage wild/riderless creatures. | 允许spike walls to damage wild/riderless creatures。 |
| `bUseDinoLevelUpAnimations` | 使用恐龙升级动画 | True | boolean | Game.ini | False, tame creatures on level-up will not perform the related animation. | False, tame creatures on level-up will not perform the related animation. |
| `ConfigAddNPCSpawnEntriesContainer` | 添加NPC生成区域配置 | N/A | (...)Adds | Game.ini | specific creatures in spawn areas. See Creature Spawn related section for more detail. | specific creatures in spawn areas. |
| `ConfigOverrideNPCSpawnEntriesContainer` | 覆盖NPC生成区域配置 | N/A | (...)Overrides | Game.ini | specific creatures in spawn areas. See Creature Spawn related section for more details. | specific creatures in spawn areas. |
| `ConfigSubtractNPCSpawnEntriesContainer` | 移除NPC生成区域配置 | N/A | (...)Removes | Game.ini | specific creatures in spawn areas. See Creature Spawn related section for more detail. | specific creatures in spawn areas. |
| `DinoClassDamageMultipliers` | 恐龙类别伤害倍率 | N/A | (...)Globally | Game.ini | overrides wild creatures damages. See Creature Stats related section for more detail. | overrides wild creatures damages. |
| `DinoClassResistanceMultipliers` | 恐龙类别抗性倍率 | N/A | (...)Globally | Game.ini | overrides wild creatures resistance. See Creature Stats related section for more detail. | overrides wild creatures resistance. |
| `DinoHarvestingDamageMultiplier` | 恐龙采集伤害倍率 | 3.2 | float | Game.ini | the damage done to a harvestable item/entity by a tame. A higher number increases (by percentage) the speed of harvesting. | the damage done to a harvestable item/entity by a tame. |
| `DinoSpawnWeightMultipliers` | 恐龙生成权重倍率 | N/A | (...)Globally | Game.ini | overrides creatures spawns likelihood. See Creature Spawn related section for more detail. | overrides creatures spawns likelihood. |
| `DinoTurretDamageMultiplier` | 恐龙受到炮台伤害倍率 | 1.0 | float | Game.ini | the damage done by Turrets towards a creature. A higher values increases it (by percentage). | the damage done by Turrets towards a creature. |
| `IncreasePvPRespawnIntervalBaseAmount` | PvP重生额外等待时间(秒) | 60.0 | float | Game.ini | bIncreasePvPRespawnInterval is True, sets the additional PvP re-spawn time in seconds that scales (IncreasePvPRespawnIntervalMultiplier) when a player is killed by a team within a certain amount of ti | 设置additional PvP re-spawn time in seconds that scales (IncreasePvPRespawnIntervalMultiplier) when a player is killed by a team within a certain amount of time (IncreasePvPRespawnIntervalCheckPeriod)。 |
| `IncreasePvPRespawnIntervalCheckPeriod` | PvP重生间隔检查周期(秒) | 300.0 | float | Game.ini | bIncreasePvPRespawnInterval is True, sets the amount of time in seconds within a player re-spawn time increases (IncreasePvPRespawnIntervalBaseAmount) and scales (IncreasePvPRespawnIntervalMultiplier) | 设置amount of time in seconds within a player re-spawn time increases (IncreasePvPRespawnIntervalBaseAmount) and scales (IncreasePvPRespawnIntervalMultiplier) when it is killed by a team in PvP。 |
| `IncreasePvPRespawnIntervalMultiplier` | PvP重生间隔倍率 | 2.0 | float | Game.ini | bIncreasePvPRespawnInterval is True, scales the PvP additional re-spawn time (IncreasePvPRespawnIntervalBaseAmount) when a player is killed by a team within a certain amount of time (IncreasePvPRespaw | 缩放time (IncreasePvPRespawnIntervalCheckPeriod)的PvP additional re-spawn time (IncreasePvPRespawnIntervalBaseAmount) when a player is killed by a team within a certain amount。 |
| `NPCReplacements` | NPC替换配置 | N/A | (...)Globally | Game.ini | replaces specific creatures with another using class names. See Creature Spawn related section for more detail. | replaces specific creatures with another using class names. |
| `OverrideMaxExperiencePointsDino` | 覆盖恐龙最大经验值 | N/A | integer | Game.ini | the max XP cap of tame characters by exact specified amount. | the max XP cap of tame characters by exact specified amount. |
| `PerLevelStatsMultiplier_DinoTamed<_type>[<integer>]` | PerLevelStatsMultiplier_恐龙驯养d<_type>[<integer>] | N/A | float | Game.ini | tamed creature stats. See Level stats related section for more detail. | tamed creature stats. |
| `PerLevelStatsMultiplier_DinoWild[<integer>]` | PerLevelStatsMultiplier_恐龙野生[<integer>] | N/A | float | Game.ini | wild creatures stats. See Level stats related section for more detail. | wild creatures stats. |
| `PreventDinoTameClassNames` | 禁止驯服指定物种 | N/A | "<string>"Prevents | Game.ini | taming of specific dinosaurs via classname. E.g. PreventDinoTameClassNames="Argent_Character_BP_C". Dino classnames can be found on the Creature IDs page. | taming of specific dinosaurs via classname. |
| `TamedDinoCharacterFoodDrainMultiplier` | 驯养恐龙食物消耗倍率 | 1.0 | float | Game.ini | how fast tame creatures consume food. | how fast tame creatures consume food. |
| `TamedDinoClassDamageMultipliers` | 驯养恐龙类别伤害倍率 | N/A | (...)Globally | Game.ini | overrides tamed creatures damages. See Creature Stats related section for more details. | overrides tamed creatures damages. |
| `TamedDinoClassResistanceMultipliers` | 驯养恐龙类别抗性倍率 | N/A | (...)Globally | Game.ini | overrides tamed creatures resistance. See Creature Stats related section for more details. | overrides tamed creatures resistance. |
| `TamedDinoTorporDrainMultiplier` | 驯养恐龙昏迷值消耗倍率 | 1.0 | float | Game.ini | how fast tamed creatures lose torpor. | how fast tamed creatures lose torpor. |
| `WildDinoCharacterFoodDrainMultiplier` | 野生恐龙食物消耗倍率 | 1.0 | float | Game.ini | how fast wild creatures consume food. | how fast wild creatures consume food. |
| `WildDinoTorporDrainMultiplier` | 野生恐龙昏迷值消耗倍率 | 1.0 | float | Game.ini | how fast wild creatures lose torpor. | how fast wild creatures lose torpor. |

#### 繁殖与成长 (23项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `BabyCuddleGracePeriodMultiplier` | 婴儿照料宽限期倍率 | 1.0 | float | Game.ini | how long after delaying cuddling with the Baby before Imprinting Quality starts to decrease. | how long after delaying cuddling with the Baby before Imprinting Quality starts to decrease. |
| `BabyCuddleIntervalMultiplier` | 婴儿照料间隔倍率 | 1.0 | float | Game.ini | how often babies needs attention for imprinting. More often means you'll need to cuddle with them more frequently to gain Imprinting Quality. Scales always according to default BabyMatureSpeedMultipli | how often babies needs attention for imprinting. |
| `BabyCuddleLoseImprintQualitySpeedMultiplier` | 婴儿印记品质下降速度倍率 | 1.0 | float | Game.ini | how fast Imprinting Quality decreases after the grace period if you haven't yet cuddled with the Baby. | how fast Imprinting Quality decreases after the grace period if you haven't yet cuddled with the Baby. |
| `BabyFoodConsumptionSpeedMultiplier` | 婴儿食物消耗速度倍率 | 1.0 | float | Game.ini | the speed that baby tames eat their food. A lower value decreases (by percentage) the food eaten by babies. | the speed that baby tames eat their food. |
| `BabyImprintAmountMultiplier` | 印记量倍率 | 1.0 | float | Game.ini | the percentage each imprint provides. A higher value, will rise the amount of imprinting % at each baby care/cuddle, a lower value will decrease it. This multiplier is global, meaning it will affect t | the percentage each imprint provides. |
| `BabyImprintingStatScaleMultiplier` | 印记属性缩放倍率 | 1.0 | float | Game.ini | how much of an effect on stats the Imprinting Quality has. Set it to 0 to effectively disable the system. | how much of an effect on stats the Imprinting Quality has. |
| `BabyMatureSpeedMultiplier` | 婴儿成长速度倍率 | 1.0 | float | Game.ini | the maturation speed of babies. A higher number decreases (by percentage) time needed for baby tames to mature. See Times for Breeding tables for values at 1.0, see The Imprinting formula how it affec | the maturation speed of babies. |
| `bDisableDinoBreeding` | 禁用恐龙繁殖 | False | boolean | Game.ini | True, prevents tames to be bred. | 阻止tames to be bred。 |
| `bDisableWirelessCraftingForPlayers` | 禁用玩家无线制作 | false | boolean | Game.ini | True, prevents wireless crafting from Tek Dedicated Storage when crafting in the player inventory. | 阻止wireless crafting from Tek Dedicated Storage when crafting in the player inventory。 |
| `bUseSingleplayerSettings` | 启用单人游戏设置 | False | boolean | Game.ini | True, all game settings will be more balanced for an individual player experience. Useful for dedicated server with a very small amount of players. See Single Player Settings section for more details. | True, all game settings will be more balanced for an individual player experience. |
| `EggHatchSpeedMultiplier` | 蛋孵化速度倍率 | 1.0 | float | Game.ini | the time needed for a fertilised egg to hatch. A higher value decreases (by percentage) that time. | the time needed for a fertilised egg to hatch. |
| `LayEggIntervalMultiplier` | 产蛋间隔倍率 | 1.0 | float | Game.ini | the time between eggs are spawning / being laid. Higher number increases it (by percentage). | the time between eggs are spawning / being laid. |
| `LimitNonPlayerDroppedItemsCount` | 非玩家掉落物数量上限 | 0 | integer | Game.ini | the number of dropped items in the area defined by LimitNonPlayerDroppedItemsRange. Official servers have it set to 600. | the number of dropped items in the area defined by LimitNonPlayerDroppedItemsRange. |
| `LimitNonPlayerDroppedItemsRange` | 非玩家掉落物范围 | 0 | integer | Game.ini | the area range (in Unreal Units) in which the option LimitNonPlayerDroppedItemsCount applies. Official servers have it set to 1600. | the area range (in Unreal Units) in which the option LimitNonPlayerDroppedItemsCount applies. |
| `MaxNumberOfPlayersInTribe` | 部落最大人数 | 0 | integer | Game.ini | the maximum survivors allowed in a tribe. A value of 1 effectively disables tribes. The default value of 0 means there is no limit about how many survivors can be in a tribe. | the maximum survivors allowed in a tribe. |
| `OverrideMaxExperiencePointsPlayer` | 覆盖玩家最大经验值 | N/A | integer | Game.ini | the max XP cap of players characters by exact specified amount. | the max XP cap of players characters by exact specified amount. |
| `OverridePlayerLevelEngramPoints` | 覆盖玩家等级印痕点数 | N/A | integer | Game.ini | the number of engram points granted to players for each level gained. This option must be repeated for each player level set on the server, e.g.: if there are 65 player levels available this option sh | the number of engram points granted to players for each level gained. |
| `PerLevelStatsMultiplier_Player[<integer>]` | PerLevelStatsMultiplier_玩家[<integer>] | N/A | float | Game.ini | Player stats. See Level stats related section for more detail. | Player stats. |
| `PlayerBaseStatMultipliers[<attribute>]` | 玩家BaseStatMultipliers[<attribute>] | N/A | multiplierChanges | Game.ini | the base stats of a player by multiplying with the default value. Meaning the start stats of a new spawned character. See Stats related section for more detail. | the base stats of a player by multiplying with the default value. |
| `PlayerHarvestingDamageMultiplier` | 玩家采集伤害倍率 | 1.0 | float | Game.ini | the damage done to a harvestable item/entity by a Player. A higher value increases it (by percentage): the higher number, the faster the survivors collects. | the damage done to a harvestable item/entity by a Player. |
| `PreventBreedingForClassNames` | 禁止指定物种繁殖 | N/A | "<string>"Prevents | Game.ini | breeding of specific creatures via classname. E.g. PreventBreedingForClassNames="Argent_Character_BP_C". Creature classnames can be found on the Creature IDs page. | breeding of specific creatures via classname. |
| `ResourceNoReplenishRadiusPlayers` | 玩家周围资源不刷新半径 | 1.0 | float | Game.ini | how resources regrow closer or farther away from players. Values higher than 1.0 increase the distance around players where resources are not allowed to grow back. Values between 0 and 1.0 will reduce | how resources regrow closer or farther away from players. |
| `AdjustableMutagenSpawnDelayMultiplier` | AdjustableMutagenSpawnDelay倍率 | 1.0 | float | Game.ini | the Mutagen spawn rates. By default, The game attempts to spawn them every 8 hours on dedicated servers, and every hour on non-dedicated servers and single-player. Rising this value will rise the re-s | the Mutagen spawn rates. |

#### 通用设置 (14项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bAllowCustomRecipes` | 允许自定义配方 | True | boolean | Game.ini | False, disabled custom RP-oriented Recipe/Cooking System (including Skill-Based results). | False, disabled custom RP-oriented Recipe/Cooking System (including Skill-Based results). |
| `bAllowPlatformSaddleMultiFloors` | 允许平台鞍多层地板 | False | boolean | Game.ini | True, allows multiple platform floors. | 允许multiple platform floors。 |
| `BaseTemperatureMultiplier` | 基础温度倍率 | 1.0 | float | Game.ini | the map base temperature scaling factor: lower value makes the environment colder, higher value makes the environment hotter. | the map base temperature scaling factor: lower value makes the environment colder, higher value makes the environment hotter. |
| `bUseCorpseLocator` | 使用尸体定位光束 | True | boolean | Game.ini | False, prevents survivors to see a green light beam at the location of their dead body. | 阻止survivors to see a green light beam at the location of their dead body。 |
| `CustomRecipeEffectivenessMultiplier` | 自定义配方效果倍率 | 1.0 | float | Game.ini | the effectiveness of custom recipes. A higher value increases (by percentage) their effectiveness. | the effectiveness of custom recipes. |
| `CustomRecipeSkillMultiplier` | 自定义配方技能倍率 | 1.0 | float | Game.ini | the effect of the players crafting speed level that is used as a base for the formula in creating a custom recipe. A higher number increases (by percentage) the effect. | the effect of the players crafting speed level that is used as a base for the formula in creating a custom recipe. |
| `GlobalPoweredBatteryDurabilityDecreasePerSecond` | 电池耐久每秒消耗 | 3.0 | float | Game.ini | the rate at which charge batteries are used in electrical objects. | the rate at which charge batteries are used in electrical objects. |
| `HairGrowthSpeedMultiplier` | 毛发生长速度倍率 | 1.0 (ASE), 0 (ASA) | float | Game.ini | the hair growth. Higher values increase speed of growth. | the hair growth. |
| `MatingIntervalMultiplier` | 交配间隔倍率 | 1.0 | float | Game.ini | the interval between tames can mate. A lower value decreases it (on a percentage scale). Example: a value of 0.5 would allow tames to mate 50% sooner. | the interval between tames can mate. |
| `MatingSpeedMultiplier` | 交配速度倍率 | 1.0 | float | Game.ini | the speed at which tames mate with each other. A higher value increases it (by percentage). Example: MatingSpeedMultiplier=2.0 would cause tames to complete mating in half the normal time. | the speed at which tames mate with each other. |
| `MaxFallSpeedMultiplier` | 最大坠落速度倍率 | 1.0 | float | Game.ini | the falling speed multiplier at which players starts taking fall damage. The falling speed is based on the time players spent in the air while having a negated Z axis velocity meaning that the higher  | the falling speed multiplier at which players starts taking fall damage. |
| `PassiveTameIntervalMultiplier` | 被动驯服喂食间隔倍率 | 1.0 | float | Game.ini | how often a survivor get tame requests for passive tame creatures. | how often a survivor get tame requests for passive tame creatures. |
| `PoopIntervalMultiplier` | 排便间隔倍率 | 1.0 | float | Game.ini | how frequently survivors can poop. Higher value decreases it (by percentage) | how frequently survivors can poop. |
| `UseCorpseLifeSpanMultiplier` | 尸体存留时间倍率 | 1.0 | float | Game.ini | corpse and dropped box lifespan. | corpse and dropped box lifespan. |

#### 建筑与防御 (14项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisableStructurePlacementCollision` | 禁用建筑放置碰撞检测 | False | boolean | Game.ini | True, allows for structures to clip into the terrain. | 允许for structures to clip into the terrain。 |
| `bDisableWirelessCraftingForStructures` | 禁用建筑无线制作 | false | boolean | Game.ini | True, prevents wireless crafting from Tek Dedicated Storage when crafting in structure inventories. | 阻止wireless crafting from Tek Dedicated Storage when crafting in structure inventories。 |
| `bIgnoreStructuresPreventionVolumes` | 忽略建筑禁止区域 | False | boolean | Game.ini | True, enables building areas where normally it's not allowed, such around some maps' Obelisks, in the Aberration Portal and in Mission Volumes areas on Genesis: Part 1. Note: in Genesis: Part 1 this s | 启用building areas where normally it's not allowed, such around some maps' Obelisks, in the Aberration Portal and in Mission Volumes areas on Genesis: Part 1。 |
| `bUseTameLimitForStructuresOnly` | 驯服限制仅适用于建筑平台 | False | boolean | Game.ini | True will make Tame Units only be applied and used for Platforms with Structures and Rafts effectively disabling Tame Units for tames without Platform Structures. | True will make Tame Units only be applied and used for Platforms with Structures and Rafts effectively disabling Tame Units for tames without Platform |
| `FastDecayInterval` | 快速腐烂间隔(秒) | 43200 | integer | Game.ini | the decay period for "Fast Decay" structures (such as pillars or lone foundations). Value is in seconds. FastDecayUnsnappedCoreStructures in GameUserSettings.ini must be set to True as well to take an | the decay period for "Fast Decay" structures (such as pillars or lone foundations). |
| `LimitGeneratorsNum` | 发电机数量上限 | 3 | integer | Game.ini | the number of generators in the area defined by LimitGeneratorsRange. Official servers have it set to 3. | the number of generators in the area defined by LimitGeneratorsRange. |
| `LimitGeneratorsRange` | 发电机限制范围 | 15000 | integer | Game.ini | the area range (in Unreal Units) in which the option LimitGeneratorsNum applies. Official servers have it set to 15000. | the area range (in Unreal Units) in which the option LimitGeneratorsNum applies. |
| `PvPZoneStructureDamageMultiplier` | PvP区域建筑伤害倍率 | 6.0 | float | Game.ini | the scaling factor for damage structures take within caves. The lower the value, the less damage the structure takes (i.e. setting to 1.0 will make structure built in or near a cave receive the same a | the scaling factor for damage structures take within caves. |
| `StructureDamageRepairCooldown` | 建筑被攻击后修理冷却(秒) | 180 | integer | Game.ini | for cooldown period on structure repair from the last time damaged. Set to 180 seconds by default, 0 to disable it. | for cooldown period on structure repair from the last time damaged. |
| `bHardLimitTurretsInRange` | 炮台硬性数量限制 | False | boolean | Game.ini | True, enables the retroactive turret hard limit (100 turrets within a 10k unit radius). | 启用the retroactive turret hard limit (100 turrets within a 10k unit radius)。 |
| `bLimitTurretsInRange` | 启用范围内炮台数量限制 | True | boolean | Game.ini | False, doesn't limit the maximum allowed automated turrets (including Plant Species X) in a certain range. | False, doesn't limit the maximum allowed automated turrets (including Plant Species X) in a certain range. |
| `LimitTurretsNum` | 区域内炮台数量上限 | 100 | integer | Game.ini | the maximum number of turrets that are allowed in the area. | the maximum number of turrets that are allowed in the area. |
| `LimitTurretsRange` | 炮台限制区域范围 | 10000.0 | float | Game.ini | the area in Unreal Unit in which turrets are added towards the limit. | the area in Unreal Unit in which turrets are added towards the limit. |
| `bGenesisUseStructuresPreventionVolumes` | 创世纪启用建筑禁止区域 | False | boolean | Game.ini | True, disables building in mission areas on Genesis: Part 1. | True, disables building in mission areas on Genesis: Part 1. |

#### 印痕与等级 (13项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bAutoUnlockAllEngrams` | 自动解锁所有印痕 | False | boolean | Game.ini | True, unlocks all Engrams available. Ignores OverrideEngramEntries and OverrideNamedEngramEntries entries. | True, unlocks all Engrams available. |
| `bOnlyAllowSpecifiedEngrams` | 仅允许指定印痕 | False | boolean | Game.ini | True, any Engram not explicitly specified by OverrideEngramEntries or OverrideNamedEngramEntries list will be hidden. All Items and Blueprints based on hidden Engrams will be removed. | True, any Engram not explicitly specified by OverrideEngramEntries or OverrideNamedEngramEntries list will be hidden. |
| `ConfigOverrideItemCraftingCosts` | 覆盖物品制作配方 | N/A | (...)Overrides | Game.ini | items crafting resource requirements. See Item related section for more details. | items crafting resource requirements. |
| `ConfigOverrideItemMaxQuantity` | 覆盖物品最大堆叠数量 | N/A | (...)Overrides | Game.ini | items stack size on a per-item basis. See Item related section for more details. | items stack size on a per-item basis. |
| `ConfigOverrideSupplyCrateItems` | 覆盖补给箱物品内容 | N/A | (...)Overrides | Game.ini | items contained in loot crates. See Items related section for more details. | items contained in loot crates. |
| `DestroyTamesOverLevelClamp` | 销毁超过等级上限的驯养生物 | 0 | integer | Game.ini | that exceed that level will be deleted on server start. Official servers have it set to 450. | that exceed that level will be deleted on server start. |
| `EngramEntryAutoUnlocks` | EngramEntryAutoUnlocks | N/A | (...)Automatically | Game.ini | unlocks the specified Engram when reaching the level specified. See Engram Entries related section for more detail. | unlocks the specified Engram when reaching the level specified. |
| `LevelExperienceRampOverrides` | 等级经验曲线覆盖 | N/A | (...)Configures | Game.ini | the total number of levels available to players and tame creatures and the experience points required to reach each level. See Players and tames levels override section for more details. | the total number of levels available to players and tame creatures and the experience points required to reach each level. |
| `OverrideEngramEntries` | 覆盖印痕条目 | N/A | (...)Configures | Game.ini | the status and requirements for learning an engram, specified by its index. See Engram Entries related section for more detail. | the status and requirements for learning an engram, specified by its index. |
| `OverrideNamedEngramEntries` | 覆盖命名印痕条目 | N/A | (...)Configures | Game.ini | the status and requirements for learning an engram, specified by its name. See Engram Entries related section for more detail. | the status and requirements for learning an engram, specified by its name. |
| `bHexStoreAllowOnlyEngramTradeOption` | 六角商店仅允许印痕交易 | False | boolean | Game.ini | True, allows only Engrams to be sold on the Hex Store, disables everything else. | 允许only Engrams to be sold on the Hex Store, disables everything else。 |
| `MutagenLevelBoost[<Stat_ID>]` | MutagenLevelBoost[<Stat_ID>] | N/A | integer | Game.ini | the number of levels  Mutagen adds to tames with wild ancestry. See Stats related section for more details. | the number of levels  Mutagen adds to tames with wild ancestry. |
| `MutagenLevelBoost_Bred[<Stat_ID>]` | MutagenLevelBoost_Bred[<Stat_ID>] | N/A | integer | Game.ini | as MutagenLevelBoost, but for bred tames. See Stats related section for more details. | as MutagenLevelBoost, but for bred tames. |

#### PvP与部落 (11项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisableFriendlyFire` | 禁用友军伤害 | False | boolean | Game.ini | True, prevents Friendly-Fire (among tribe mates/tames/structures). | 阻止Friendly-Fire (among tribe mates/tames/structures)。 |
| `bPvEAllowTribeWar` | PvE允许部落宣战 | True | boolean | Game.ini | False, disables capability for Tribes to officially declare war on each other for mutually-agreed-upon period of time. | False, disables capability for Tribes to officially declare war on each other for mutually-agreed-upon period of time. |
| `bPvEAllowTribeWarCancel` | PvE允许取消部落战争 | False | boolean | Game.ini | True, allows cancellation of an agreed-upon war before it has actually started. | 允许cancellation of an agreed-upon war before it has actually started。 |
| `bPvEDisableFriendlyFire` | PvE禁用友军伤害 | False | boolean | Game.ini | True, disabled Friendly-Fire (among tribe mates/tames/structures) in PvE servers. | True, disabled Friendly-Fire (among tribe mates/tames/structures) in PvE servers. |
| `IgnorePVPMountedWeaponryRestrictions` | 忽略PvP骑乘武器限制 | False | boolean | Game.ini | further information has been added about this variable. If you know anything, please consider creating an account and contributing. | further information has been added about this variable. |
| `MaxAlliancesPerTribe` | 每部落最大联盟数 | N/A | integer | Game.ini | set, defines the maximum alliances a tribe can form or be part of. | 定义maximum alliances a tribe can form or be part of。 |
| `MaxTribeLogs` | 部落日志最大条数 | 400 | integer | Game.ini | how many Tribe log entries are displayed for each tribe. | how many Tribe log entries are displayed for each tribe. |
| `MaxTribesPerAlliance` | 联盟最大部落数 | N/A | integer | Game.ini | set, defines the maximum of tribes in an alliance. | 定义maximum of tribes in an alliance。 |
| `PreventOfflinePvPConnectionInvincibleInterval` | 离线PvP上线无敌时间(秒) | 5.0 | float | Game.ini | the time in seconds a player cannot take damages after logged-in. | the time in seconds a player cannot take damages after logged-in. |
| `TribeTowerBonusMultiplier` | 部落之塔奖励倍率 | 2.0 | float | Game.ini | for Tribe Tower bonus. | for Tribe Tower bonus. |
| `TribeSlotReuseCooldown` | 部落槽位重用冷却(秒) | 0.0 | float | Game.ini | a tribe slot for the value in seconds, e.g.: a value of 3600 would mean that if a survivor leaves the tribe, their place cannot be taken by another survivor (or re-join) for 1 hour. Used on Official S | a tribe slot for the value in seconds, e.g.: a value of 3600 would mean that if a survivor leaves the tribe, their place cannot be taken by another survivor (or re-join) for 1 hour. |

#### ASA新增功能 (9项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bAllowFlyerSpeedLeveling` | 允许飞行生物升级速度 | False | boolean | Game.ini | whether flyer creatures can have their Movement Speed levelled up. In ARK: Survival Ascended, setting this to True only works if bAllowSpeedLeveling is also True. | whether flyer creatures can have their Movement Speed levelled up. |
| `bAllowSpeedLeveling` | 允许升级移动速度 | False | boolean | Game.ini | whether players and non-flyer creatures can have their Movement Speed levelled up. | whether players and non-flyer creatures can have their Movement Speed levelled up. |
| `bDisableWirelessCrafting` | 禁用无线制作 | false | boolean | Game.ini | True, prevents wireless crafting from Tek Dedicated Storage. | 阻止wireless crafting from Tek Dedicated Storage。 |
| `CheatTeleportLocations=(TeleportName="<string>",TeleportLocation=(X=<float>,Y=-<float>,Z=<float>))` | CheatTeleportLocations=(TeleportName="<string>",TeleportLocation=(X=<float>,Y=-<float>,Z=<float>)) | - | (...)Creates | Game.ini | a named teleport location that can be used with the TP command. The coordinates must be listed in Unreal units, not in-game gps coordinates. Example:  CheatTeleportLocations=(TeleportName="Hightower", | a named teleport location that can be used with the TP command. |
| `WirelessCraftingRangeOverride` | 无线制作范围覆盖值 | 3000 | integer | Game.ini | the wireless crafting range (in Unreal Units) on Tek Dedicated Storage. | the wireless crafting range (in Unreal Units) on Tek Dedicated Storage. |
| `ValgueroMemorialEntries` | 瓦尔盖罗纪念碑玩家名列表 | N/A | list | Game.ini | of player names, semicolon-separated with no spaces, in a single line (for example: Name1;Name2;Name3;)The Valguero Memorial is now interactable, honouring those who have ascended by displaying their  | of player names, semicolon-separated with no spaces, in a single line (for example: Name1;Name2;Name3;)The Valguero Memorial is now interactable, honouring those who have ascended by displaying their  |
| `BaseHexagonRewardMultiplier` | 六角币奖励基础倍率 | 1.0 | float | Game.ini | the missions score hexagon rewards. Also scales token rewards in Club Ark (ASA). | the missions score hexagon rewards. |
| `bDisableHexagonStore` | 禁用六角币商店 | False | boolean | Game.ini | True, disables the Hexagon store | True, disables the Hexagon store |
| `HexagonCostMultiplier` | 六角币消耗倍率 | 1.0 | float | Game.ini | the hexagon cost of items in the Hexagon store. Also scales token cost of items in Club Ark (ASA). | the hexagon cost of items in the Hexagon store. |

#### 时间与天气 (7项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AutoPvEStartTimeSeconds` | 自动PvE开始时间(秒) | 0.0 | float | Game.ini | when the PvE mode should start in a PvPvE server. Valid values are from 0 to 86400. Options bAutoPvETimer, bAutoPvEUseSystemTime and AutoPvEStopTimeSeconds must also be set. Note: although at code lev | when the PvE mode should start in a PvPvE server. |
| `AutoPvEStopTimeSeconds` | 自动PvE结束时间(秒) | 0.0 | float | Game.ini | when the PvE mode should end in a PvPvE server. Valid values are from 0 to 86400. Options bAutoPvETimer, bAutoPvEUseSystemTime and AutoPvEStopTimeSeconds must also be set. Note: although at code level | when the PvE mode should end in a PvPvE server. |
| `bAutoPvETimer` | 启用自动PvE计时器 | False | boolean | Game.ini | True, enabled PvE mode in a PvPvE server at pre-specified times. The option bAutoPvEUseSystemTime determinates what kind of time to use, while AutoPvEStartTimeSeconds and AutoPvEStopTimeSeconds set th | True, enabled PvE mode in a PvPvE server at pre-specified times. |
| `bAutoPvEUseSystemTime` | 自动PvE使用系统时间 | False | boolean | Game.ini | True, PvE mode begin and end times in a PvPvE server will refer to the server system time instead of in-game world time. Options bAutoPvETimer, AutoPvEStartTimeSeconds and AutoPvEStopTimeSeconds must  | True, PvE mode begin and end times in a PvPvE server will refer to the server system time instead of in-game world time. |
| `GlobalCorpseDecompositionTimeMultiplier` | 全局尸体分解时间倍率 | 1.0 | float | Game.ini | the decomposition time of corpses, (player and creature), globally. Higher values prolong the time. | the decomposition time of corpses, (player and creature), globally. |
| `GlobalItemDecompositionTimeMultiplier` | 全局物品分解时间倍率 | 1.0 | float | Game.ini | the decomposition time of dropped items, loot bags etc. globally. Higher values prolong the time. | the decomposition time of dropped items, loot bags etc. |
| `GlobalSpoilingTimeMultiplier` | 全局腐烂时间倍率 | 1.0 | float | Game.ini | the spoiling time of perishables globally. Higher values prolong the time. | the spoiling time of perishables globally. |

#### 物品与制作 (7项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisableLootCrates` | 禁用战利品箱刷新 | False | boolean | Game.ini | True, prevents spawning of Loot crates (artifact creates will still spawn). | 阻止spawning of Loot crates (artifact creates will still spawn)。 |
| `CraftingSkillBonusMultiplier` | 制作技能奖励倍率 | 1.0 | float | Game.ini | the bonus received from upgrading the Crafting Skill. | the bonus received from upgrading the Crafting Skill. |
| `ExcludeItemIndices` | 排除物品索引 | N/A | integer | Game.ini | an item from supply crates specifying its Item ID. You can have multiple lines of this option. | an item from supply crates specifying its Item ID. |
| `FishingLootQualityMultiplier` | 钓鱼战利品品质倍率 | 1.0 | float | Game.ini | the quality of items that have a quality when fishing. Valid values are from 1.0 to 5.0. | the quality of items that have a quality when fishing. |
| `ItemStatClamps[<attribute>]` | ItemStatClamps[<attribute>] | N/A | valueGlobally | Game.ini | clamps item stats. See Items related section for more details. Requires ?ClampItemStats=true option in the command line. | clamps item stats. |
| `SupplyCrateLootQualityMultiplier` | 补给箱战利品品质倍率 | 1.0 | float | Game.ini | the quality of items that have a quality in the supply crates. Valid values are from 1.0 to 5.0. The quality also depends on the Difficulty Offset. | the quality of items that have a quality in the supply crates. |
| `bDisableDefaultMapItemSets` | 禁用默认地图物品套装 | False | boolean | Game.ini | True, disables Genesis 2 Tek Suit on Spawn. | True, disables Genesis 2 Tek Suit on Spawn. |

#### 经验值倍率 (5项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `CraftXPMultiplier` | 制作经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for crafting. | the amount of XP earned for crafting. |
| `GenericXPMultiplier` | 通用经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for generic XP (automatic over time). | the amount of XP earned for generic XP (automatic over time). |
| `HarvestXPMultiplier` | 采集经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for harvesting. | the amount of XP earned for harvesting. |
| `KillXPMultiplier` | 击杀经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for a kill. | the amount of XP earned for a kill. |
| `SpecialXPMultiplier` | 特殊事件经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for SpecialEvent. | the amount of XP earned for SpecialEvent. |

#### 采集与资源 (5项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `CropDecaySpeedMultiplier` | 作物腐烂速度倍率 | 1.0 | float | Game.ini | the speed of crop decay in plots. A higher value decrease (by percentage) speed of crop decay in plots. | the speed of crop decay in plots. |
| `CropGrowthSpeedMultiplier` | 作物生长速度倍率 | 1.0 | float | Game.ini | the speed of crop growth in plots. A higher value increases (by percentage) speed of crop growth. | the speed of crop growth in plots. |
| `FuelConsumptionIntervalMultiplier` | 燃料消耗间隔倍率 | 1.0 | float | Game.ini | the interval of fuel consumption. | the interval of fuel consumption. |
| `HarvestResourceItemAmountClassMultipliers` | 资源采集量分类倍率 | N/A | (...)Scales | Game.ini | on a per-resource type basis, the amount of resources harvested. See Items related section for more details. | on a per-resource type basis, the amount of resources harvested. |
| `ResourceNoReplenishRadiusStructures` | 建筑周围资源不刷新半径 | 1.0 | float | Game.ini | how resources regrow closer or farther away from structures Values higher than 1.0 increase the distance around structures where resources are not allowed to grow back. Values between 0 and 1.0 will r | how resources regrow closer or farther away from structures Values higher than 1.0 increase the distance around structures where resources are not allowed to grow back. |

#### 创世纪与任务 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisableGenesisMissions` | 禁用创世纪任务 | False | boolean | Game.ini | True, disables missions on Genesis. | True, disables missions on Genesis. |
| `bDisableWorldBuffs` | 禁用世界增益效果 | False | boolean | Game.ini | True, disables world effects from Missions (Genesis: Part 2) altogether. To disable specific world buffs, see DisableWorldBuffs of #DynamicConfig. | True, disables world effects from Missions (Genesis: Part 2) altogether. |
| `bEnableWorldBuffScaling` | 启用世界增益缩放 | False | boolean | Game.ini | True, makes world effects from Missions (Genesis: Part 2) scale from server settings, rather than add/subtract a flat amount to the value at runtime. | True, makes world effects from Missions (Genesis: Part 2) scale from server settings, rather than add/subtract a flat amount to the value at runtime. |
| `WorldBuffScalingEfficacy` | 世界增益缩放效果倍率 | 1.0 | float | Game.ini | world effects from Missions (Genesis: Part 2) scaling more or less effective when setting bEnableWorldBuffScaling=True. 1 would be default, 0.5 would be 50% less effective, 100 would be 100x more effe | world effects from Missions (Genesis: Part 2) scaling more or less effective when setting bEnableWorldBuffScaling=True. |

#### Mod与地图 (3项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisablePhotoMode` | 禁用拍照模式 | False | boolean | Game.ini | if photo mode is allowed (False) or not (True). | if photo mode is allowed (False) or not (True). |
| `bShowCreativeMode` | 显示创造模式按钮 | False | boolean | Game.ini | True, adds a button to the pause menu to enable/disable creative mode. | True, adds a button to the pause menu to enable/disable creative mode. |
| `PhotoModeRangeLimit` | 拍照模式最大距离 | 3000 | integer | Game.ini | the maximum distance between photo mode camera position and player position. | the maximum distance between photo mode camera position and player position. |

#### 服务器与性能 (1项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bAllowUnlimitedRespecs` | 允许无限洗点 | False | boolean | Game.ini | True, allows more than one usage of Mindwipe Tonic without 24 hours cooldown. | 允许more than one usage of Mindwipe Tonic without 24 hours cooldown。 |

#### 驯养设置 (1项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisableDinoTaming` | 禁用驯服恐龙 | False | boolean | Game.ini | True, prevents players to tame wild creatures. | 阻止players to tame wild creatures。 |



---

## 附录: 快速参考

### 推荐倍率配置

| 用途 | 配置项 | 官方默认 | 休闲PVE推荐 | PvP推荐 |
|------|--------|----------|-----------|---------|
| 驯服速度 | TamingSpeedMultiplier | 1.0 | 3.0 ~ 5.0 | 1.0 ~ 2.0 |
| 采集产量 | HarvestAmountMultiplier | 1.0 | 3.0 ~ 5.0 | 1.0 ~ 2.0 |
| 经验倍率 | XPMultiplier | 1.0 | 2.0 ~ 3.0 | 1.0 ~ 2.0 |
| 孵化速度 | EggHatchSpeedMultiplier | 1.0 | 5.0 ~ 10.0 | 2.0 ~ 3.0 |
| 成长速度 | BabyMatureSpeedMultiplier | 1.0 | 5.0 ~ 10.0 | 2.0 ~ 3.0 |
| 交配间隔 | MatingIntervalMultiplier | 1.0 | 0.25 ~ 0.5 | 0.5 ~ 1.0 |
| 最大玩家 | -WinLiveMaxPlayers | 70 | 20 ~ 40 | 40 ~ 70 |
| 难度 | DifficultyOffset | 1.0 | 1.0 | 1.0 |

### 生效方式

| 修改内容 | 生效方式 | 备注 |
|----------|----------|------|
| 命令行参数 | 重启服务器 | 修改启动脚本 |
| GameUserSettings.ini | 重启服务器 | 保存文件后重启 |
| Game.ini | 重启服务器 | 保存文件后重启 |
| DynamicConfig | 自动热重载 | 无需重启 |

### 常用管理员命令

| 命令 | 说明 |
|------|------|
| `enablecheats <密码>` | 启用管理员权限 |
| `cheat GiveItem ...` | 给予物品 |
| `cheat Summon ...` | 生成生物 |
| `cheat Teleport ...` | 传送 |
| `cheat SaveWorld` | 保存世界 |
| `cheat DestroyWildDinos` | 销毁所有野生恐龙 |
| `cheat Broadcast <消息>` | 广播消息 |

---

> 📖 完整文档: https://ark.wiki.gg/wiki/Server_configuration
> 本文档由自动化脚本从 Wiki 页面提取生成，配置项说明为英文原文附中文翻译
