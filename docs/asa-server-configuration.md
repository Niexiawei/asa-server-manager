# ARK: Survival Ascended - 服务器配置项完整参考

> **来源**: [ARK Wiki](https://ark.wiki.gg/wiki/Server_configuration) | **日期**: 2026-06-19
> **仅 ASA 兼容项** | 共 209 项配置 | 用于前端可视化配置基础资料

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
- [2. GameUserSettings.ini](#2-gameusersettingsini) — [ServerSettings](#21-serversettings)(140) · [SessionSettings](#22-sessionsettings)(4) · [GameSession](#23-scriptenginegamesession)(1) · [MOTD](#24-messageoftheday)(2)
- [3. Game.ini](#3-gameini)(62)
- [4. 高级配置详解](#4-高级配置详解)
  - [4.1 生物生成配置](#41-生物生成配置)
  - [4.2 生物属性配置](#42-生物属性配置)
  - [4.3 印痕条目配置](#43-印痕条目配置)
  - [4.4 物品配置](#44-物品配置)
  - [4.5 等级经验覆盖](#45-等级经验覆盖)
  - [4.6 属性配置](#46-属性配置)
  - [4.7 单人设置](#47-单人设置)
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
| `AllowHideDamageSourceFromLogs` | 允许隐藏日志伤害源 | True | boolean | GameUserSettings.ini + 命令行 | False, shows the damage sources in tribe logs. | False, shows the damage sources in tribe logs. |
| `AllowHitMarkers` | 允许命中标记 | True | boolean | GameUserSettings.ini + 命令行 | False, disables optional markers for ranged attacks. | False, disables optional markers for ranged attacks. |
| `AllowMultipleAttachedC4` | 允许多个C4附着 | False | boolean | GameUserSettings.ini + 命令行 | True, allows to attach more than one C4 per creature. | True, allows to attach more than one C4 per creature. |
| `CustomLiveTuningUrl` | 自定义实时调优URL | - | string | GameUserSettings.ini + 命令行 | with a URLDirect link to the live tuning file. For more information on how to use this system check out the official announcement: https://survivetheark.com/index.php?/forums/topic/569366-server-configuration-live-tuning-system.  ARK: Survival Evolve | with a URLDirect link to the live tuning file. |
| `DestroyTamesOverTheSoftTameLimit` | Destroy驯养sOverTheSoftTameLimit | False | boolean | GameUserSettings.ini + 命令行 | above the Soft Tame Server Limit will be marked “For Cryo” and display an icon and a timer indicating how soon they need to be cryopodded before they are automatically destroyed. Dinos marked and dinos destroyed by this system will be logged in the t | above the Soft Tame Server Limit will be marked “For Cryo” and display an icon and a timer indicating how soon they need to be cryopodded before they are automatically destroyed. |
| `DifficultyOffset` | 难度偏移值 | 1.0 | float | GameUserSettings.ini + 命令行 | the difficulty level. | the difficulty level. |
| `DisableWeatherFog` | 禁用天气雾 | False | boolean | GameUserSettings.ini + 命令行 | True, disables fog. | True, disables fog. |
| `globalVoiceChat` | globalVoiceChat | False | boolean | GameUserSettings.ini + 命令行 | True, voice chat turns global. | True, voice chat turns global. |
| `MaxTrainCars` | MaxTrainCars | 8 | integer | GameUserSettings.ini + 命令行 | the maximum amount of carts a train cave have. | the maximum amount of carts a train cave have. |
| `NonPermanentDiseases` | 疾病非永久化 | False | boolean | GameUserSettings.ini + 命令行 | True, makes permanent diseases not permanent. Players will lose them if on re-spawn. | True, makes permanent diseases not permanent. |
| `OxygenSwimSpeedStatMultiplier` | OxygenSwim速度Stat倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | this to set how swim speed is multiplied by level spent in oxygen. The value was reduced by 80% in 256.0. | this to set how swim speed is multiplied by level spent in oxygen. |
| `PlatformSaddleBuildAreaBoundsMultiplier` | PlatformSaddleBuildAreaBounds倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the number allows structures being placed further away from the platform. | the number allows structures being placed further away from the platform. |
| `PreventDiseases` | PreventDiseases | False | boolean | GameUserSettings.ini + 命令行 | True, completely diseases on the server. Thus far just Swamp Fever. | True, completely diseases on the server. |
| `ProximityChat` | ProximityChat | False | boolean | GameUserSettings.ini + 命令行 | True, only players near each other can see their chat messages | True, only players near each other can see their chat messages |
| `RCONPort` | RCONPort | 27020 | integer | GameUserSettings.ini + 命令行 | the optional TCP RCON Port. See Dedicated server setup | the optional TCP RCON Port. |
| `RCONServerGameLogBuffer` | RCONServerGameLogBuffer | 600.0 | float | GameUserSettings.ini + 命令行 | how many lines of game logs are send over the RCON. Note: despite being coded as a float it's suggested to treat it as integer. | how many lines of game logs are send over the RCON. |
| `ServerCrosshair` | ServerCrosshair | True | boolean | GameUserSettings.ini + 命令行 | False, disables the Crosshair on your server. | False, disables the Crosshair on your server. |
| `ServerForceNoHUD` | ServerForceNoHUD | False | boolean | GameUserSettings.ini + 命令行 | True, HUD is always disabled for non-tribe owned NPCs. | True, HUD is always disabled for non-tribe owned NPCs. |
| `ServerHardcore` | ServerHardcore | False | boolean | GameUserSettings.ini + 命令行 | True, enables Hardcore mode (player characters revert to level 1 upon death) | True, enables Hardcore mode (player characters revert to level 1 upon death) |
| `ShowFloatingDamageText` | ShowFloating伤害Text | False | boolean | GameUserSettings.ini + 命令行 | True, enables RPG-style popup damage text mode. | True, enables RPG-style popup damage text mode. |
| `UseAstraeosTraversalBuff` | UseAstraeosTraversalBuff | True | boolean | GameUserSettings.ini | True, enables the biome teleport in Astraeos when holding .mw-parser-output .key{display:inline-block;white-space:nowrap}.mw-parser-output .key kbd{padding:0.1em 0.6em 0.1em 0.6em;margin-right:2px;font-size:85%;font-family:inherit;font-style:normal;b | True, enables the biome teleport in Astraeos when holding .mw-parser-output .key{display:inline-block;white-space:nowrap}.mw-parser-output .key kbd{padding:0.1em 0.6em 0.1em 0.6em;margin-right:2px;fon |
| `YoungIceFoxDeathCooldown` | YoungIceFoxDeathCooldown | 3600 | float | GameUserSettings.ini + 命令行 | the cooldown for Veilwyn to reappear after taking fatal damage (in seconds), default is set to 1 hour. Must be greater than 0. | the cooldown for Veilwyn to reappear after taking fatal damage (in seconds), default is set to 1 hour. |
| `noTributeDownloads` | noTributeDownloads | False | boolean | GameUserSettings.ini + 命令行 | True, prevents CrossArk-data downloads inCross-ARK Data Transfer. | True, prevents CrossArk-data downloads inCross-ARK Data Transfer. |
| `PreventDownloadSurvivors` | PreventDownloadSurvivors | False | boolean | GameUserSettings.ini + 命令行 | True, prevents survivors download from ARK Data in Cross-ARK Data Transfer. | True, prevents survivors download from ARK Data in Cross-ARK Data Transfer. |
| `PreventUploadSurvivors` | PreventUploadSurvivors | False | boolean | GameUserSettings.ini + 命令行 | True, prevents survivors upload to ARK Data in Cross-ARK Data Transfer. | True, prevents survivors upload to ARK Data in Cross-ARK Data Transfer. |
| `BadWordListURL` | BadWordListURL | : "http://arkdedicated.com/badwords.txt"  : "http://cdn2.arkdedicated.com/asa/badwords.txt" | string | GameUserSettings.ini | with a URLAdd the URL to hosting your own bad words list. Note: on  ARK: Survival Evolved servers only the HTTP protocol is supported (an HTTPS URL will not work). | with a URLAdd the URL to hosting your own bad words list. |
| `BadWordWhiteListURL` | BadWordWhiteListURL | : "http://arkdedicated.com/goodwords.txt"  : "http://cdn2.arkdedicated.com/asa/goodwords.txt" | string | GameUserSettings.ini | with a URLAdd the URL to hosting your own good words list. Note: on  ARK: Survival Evolved servers only the HTTP protocol is supported (an HTTPS URL will not work). | with a URLAdd the URL to hosting your own good words list. |
| `BloodforgeReinforceExtraDurability` | BloodforgeReinforceExtraDurability | 0.3 | float | GameUserSettings.ini | Default value: 0.3Value type: float | 默认: 0.3类型: float |
| `BloodforgeReinforceSpeedMultiplier` | BloodforgeReinforce速度倍率 | 0.1 | float | GameUserSettings.ini | Default value: 0.1Value type: float | 默认: 0.1类型: float |
| `MaxActiveOutposts` | MaxActiveOutposts | - | integer | GameUserSettings.ini | Value type: integer | 类型: integer |
| `MaxActiveCityOutposts` | MaxActiveCityOutposts | - | integer | GameUserSettings.ini | Value type: integer | 类型: integer |
| `OutpostSigilRewardMultiplier` | OutpostSigilReward倍率 | 1.0 | float | GameUserSettings.ini | the scaling factor for sigil rewards from outpost missions. Higher values increase the number of sigils rewarded. | the scaling factor for sigil rewards from outpost missions. |
| `AutoRestartIntervalSeconds` | AutoRestart间隔Seconds | Unknown | float | GameUserSettings.ini + 命令行 | the time (in seconds) after which the server will automatically restart. Undocumented by Wildcard. (Appears to shut off the server instead of restarting properly) | the time (in seconds) after which the server will automatically restart. |
| `UseCharacterTracker` | UseCharacterTracker | False | boolean | GameUserSettings.ini + 命令行 | to enable character tracking. Alternatively, this option can be configured with -disableCharacterTracker argument in the command line (note that the argument from command line has priority over the value set in GameUserSettings.ini). Undocumented by  | to enable character tracking. |

#### 生物设置 (24项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowRaidDinoFeeding` | AllowRaid恐龙Feeding | False | boolean | GameUserSettings.ini + 命令行 | True, allows Titanosaurs to be permanently tamed (namely allow them to be fed). Note: in The Island only spawns a maximum of 3 Titanosaurs, so 3 tamed ones should ultimately block any more ones from spawning. | True, allows Titanosaurs to be permanently tamed (namely allow them to be fed). |
| `DinoCharacterFoodDrainMultiplier` | 恐龙CharacterFoodDrain倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for creatures' food consumption. Higher values increase food consumption (creatures get hungry faster). It also affects the taming-times. | the scaling factor for creatures' food consumption. |
| `DinoCharacterHealthRecoveryMultiplier` | 恐龙CharacterHealthRecovery倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for creatures' health recovery. Higher values increase the recovery rate (creatures heal faster). | the scaling factor for creatures' health recovery. |
| `DinoCharacterStaminaDrainMultiplier` | 恐龙CharacterStaminaDrain倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for creatures' stamina consumption. Higher values increase stamina consumption (creatures get tired faster). | the scaling factor for creatures' stamina consumption. |
| `DinoDamageMultiplier` | 恐龙伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the damage wild creatures deal with their attacks. The default value 1 provides normal damage. Higher values increase damage. Lower values decrease it. | the scaling factor for the damage wild creatures deal with their attacks. |
| `DinoResistanceMultiplier` | 恐龙抗性倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the resistance to damage wild creatures receive when attacked. The default value 1 provides normal damage. Higher values decrease resistance, increasing damage per attack. Lower values increase it, reducing damage per attack. A | the scaling factor for the resistance to damage wild creatures receive when attacked. |
| `DisableDinoDecayPvE` | Disable恐龙DecayPvE | False | boolean | GameUserSettings.ini + 命令行 | True, disables the creature decay in PvE mode. Note: after patch 273.691, in PvE mode the creature auto-unclaim after decay period has been disabled. | True, disables the creature decay in PvE mode. |
| `MaxPersonalTamedDinos` | MaxPersonal驯养d恐龙s | 0 | integer | GameUserSettings.ini + 命令行 | a per-tribe creature tame limit (500 on official PvE servers, 300 in official PvP servers). The default value of 0 disables such limit. | a per-tribe creature tame limit (500 on official PvE servers, 300 in official PvP servers). |
| `MaxTamedDinos` | Max驯养d恐龙s | 5000.0 | float | GameUserSettings.ini + 命令行 | the maximum number of tame creatures on a server, this is a global cap. Note: although at code level it is defined as a floating-point number, it is suggested to use an integer instead. | the maximum number of tame creatures on a server, this is a global cap. |
| `MaxTamedDinos_SoftTameLimit` | Max驯养d恐龙s_SoftTameLimit | 5000 | integer | GameUserSettings.ini + 命令行 | the server-wide soft tame limit. See DestroyTamesOverTheSoftTameLimit for more info. | the server-wide soft tame limit. |
| `MaxTamedDinos_SoftTameLimit_CountdownForDeletionDuration` | Max驯养d恐龙s_SoftTameLimit_CountdownForDeletionDuration | 604800 | integer | GameUserSettings.ini + 命令行 | the time (in seconds) for tame to get destroyed. See DestroyTamesOverTheSoftTameLimit for more info. | the time (in seconds) for tame to get destroyed. |
| `MaxTributeDinos` | MaxTribute恐龙s | 20 | integer | GameUserSettings.ini + 命令行 | for uploaded creatures. Any value less than default will be reverted. Note: Some player claimed maximum 273 to be safe cap and more will corrupt profile/cluster and lead to lose of all stored creatures but it need to be checked | for uploaded creatures. |
| `PreventSpawnAnimations` | 禁用重生动画 | False | boolean | GameUserSettings.ini + 命令行 | True, player characters (re)spawn without the wake-up animation. | True, player characters (re)spawn without the wake-up animation. |
| `PvEDinoDecayPeriodMultiplier` | PvE恐龙衰减周期倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | PvE auto-decay time multiplier. Requires DisableDinoDecayPvE=false in GameUserSettings.ini or ?DisableDinoDecayPvE=false in command line to work. | PvE auto-decay time multiplier. |
| `PvPDinoDecay` | PvP恐龙衰减 | False | boolean | GameUserSettings.ini + 命令行 | True, enables creatures' decay in PvP while the Offline Raid Prevention is active. | True, enables creatures' decay in PvP while the Offline Raid Prevention is active. |
| `RaidDinoCharacterFoodDrainMultiplier` | 突袭恐龙食物消耗倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | how quickly the food drains on such "raid dinos" (e.g.: Titanosaurus) | how quickly the food drains on such "raid dinos" (e.g.: Titanosaurus) |
| `ResourcesRespawnPeriodMultiplier` | 资源重生周期倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the re-spawn rate for resource nodes (trees, rocks, bushes, etc.). Lower values cause nodes to re-spawn more frequently. | the scaling factor for the re-spawn rate for resource nodes (trees, rocks, bushes, etc.). |
| `CrossARKAllowForeignDinoDownloads` | 跨服允许下载外来恐龙 | False | boolean | GameUserSettings.ini + 命令行 | True, enables non-native creatures tribute download on Aberration. | True, enables non-native creatures tribute download on Aberration. |
| `PreventDownloadDinos` | 禁止下载恐龙 | False | boolean | GameUserSettings.ini + 命令行 | True, prevents creatures download from ARK Data in Cross-ARK Data Transfer. | True, prevents creatures download from ARK Data in Cross-ARK Data Transfer. |
| `PreventUploadDinos` | 禁止上传恐龙 | False | boolean | GameUserSettings.ini + 命令行 | True, prevents creatures upload to ARK Data in Cross-ARK Data Transfer. | True, prevents creatures upload to ARK Data in Cross-ARK Data Transfer. |
| `AllowRidingDinosInsideBunkers` | AllowRiding恐龙sInsideBunkers | True | boolean | GameUserSettings.ini | Default value: TrueValue type: boolean | 默认: True类型: boolean |
| `AllowDinoAIInsideBunkers` | Allow恐龙AIInsideBunkers | True | boolean | GameUserSettings.ini | Default value: TrueValue type: boolean | 默认: True类型: boolean |
| `CryoHospitalHoursToRegenFood` | CryoHospitalHoursToRegenFood | 24.0 | float | GameUserSettings.ini | Default value: 24.0Value type: float | 默认: 24.0类型: float |
| `CryoHospitalHoursToDrainTorpor` | CryoHospitalHoursToDrainTorpor | 1.0 | float | GameUserSettings.ini | Default value: 1.0Value type: float | 默认: 1.0类型: float |

#### ASA新增功能 (15项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowCryoFridgeOnSaddle` | 允许冷冰箱放鞍上 | False | boolean | GameUserSettings.ini + 命令行 | True, allows cryofridges to be built on platform saddles and rafts. | True, allows cryofridges to be built on platform saddles and rafts. |
| `ArmadoggoDeathCooldown` | Armadoggo死亡冷却(秒) | 3600 | float | GameUserSettings.ini + 命令行 | the cooldown for Armadoggo to reappear after taking fatal damage (in seconds), default is set to 1 hour. Must be greater than 0. | the cooldown for Armadoggo to reappear after taking fatal damage (in seconds), default is set to 1 hour. |
| `CosmoWeaponAmmoReloadAmount` | Cosmo武器弹药装填量 | 1 | float | GameUserSettings.ini + 命令行 | how much ammo is given as the Cosmo's webslinger reloads over time. | how much ammo is given as the Cosmo's webslinger reloads over time. |
| `DisableCryopodEnemyCheck` | 禁用低温舱敌人检测 | False | boolean | GameUserSettings.ini + 命令行 | True, allows cryopods to be used while enemies are nearby. | True, allows cryopods to be used while enemies are nearby. |
| `DisableCryopodFridgeRequirement` | 禁用低温舱冰箱需求 | False | boolean | GameUserSettings.ini + 命令行 | True, allows cryopods to be used without needing to be in range of a powered cryofridge. | True, allows cryopods to be used without needing to be in range of a powered cryofridge. |
| `ForceGachaUnhappyInCaves` | 强制嘎查在洞穴中不高兴 | True | boolean | GameUserSettings.ini + 命令行 | True, Gachas will become unhappy within caves. | True, Gachas will become unhappy within caves. |
| `ImplantSuicideCD` | 植入体自杀冷却(秒) | 28800 | float | GameUserSettings.ini | the time (in seconds) a player must wait between 2 uses of the implant's "Respawn" feature. | the time (in seconds) a player must wait between 2 uses of the implant's "Respawn" feature. |
| `MaxCosmoWeaponAmmo` | MaxCosmoWeaponAmmo | -1 | float | GameUserSettings.ini + 命令行 | will make the maximum ammo amount for the Cosmo's webslinger to a set number instead of it scaling with the Cosmo's level. The default of -1 will enable scaling with level. | will make the maximum ammo amount for the Cosmo's webslinger to a set number instead of it scaling with the Cosmo's level. |
| `AllowBunkersInPreventionZones` | AllowBunkersInPreventionZones | False | boolean | GameUserSettings.ini | Default value: FalseValue type: boolean | 默认: False类型: boolean |
| `MinDistanceBetweenBunkers` | MinDistanceBetweenBunkers | 3000.0 | float | GameUserSettings.ini | Default value: 3000.0Value type: float | 默认: 3000.0类型: float |
| `EnemyAccessBunkerHPThreshold` | EnemyAccessBunkerHPThreshold | 0.25 | float | GameUserSettings.ini | Default value: 0.25Value type: float | 默认: 0.25类型: float |
| `BunkerUnderHPThresholdDmgMultiplier` | BunkerUnderHPThresholdDmg倍率 | 0.05 | float | GameUserSettings.ini | Default value: 0.05Value type: float | 默认: 0.05类型: float |
| `CryoHospitalHoursToRegenHP` | CryoHospitalHoursToRegenHP | 1.0 | float | GameUserSettings.ini | Default value: 1.0Value type: float | 默认: 1.0类型: float |
| `CryoHospitalMatingCooldownReduction` | CryoHospitalMatingCooldownReduction | 2.0 | float | GameUserSettings.ini | Default value: 2.0Value type: float | 默认: 2.0类型: float |
| `UpdateAllowedCheatersInterval` | UpdateAllowedCheaters间隔 | 600.0 | float | GameUserSettings.ini + 命令行 | in seconds at which the remote admin list linked by AllowedCheatersURL is queried for updates. Any value less than 3.0 will be reverted to 3.0. Undocumented by Wildcard. | in seconds at which the remote admin list linked by AllowedCheatersURL is queried for updates. |

#### 繁殖与成长 (13项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowAnyoneBabyImprintCuddle` | 允许任何人照料婴儿 | False | boolean | GameUserSettings.ini + 命令行 | True, allows anyone to "take care" of a baby creatures (cuddle etc.), not just whomever imprinted on it. | True, allows anyone to "take care" of a baby creatures (cuddle etc.), not just whomever imprinted on it. |
| `AllowThirdPersonPlayer` | 允许第三人称视角 | True | boolean | GameUserSettings.ini + 命令行 | False, disables third person camera allowed by default on all dedicated servers. | False, disables third person camera allowed by default on all dedicated servers. |
| `DisableImprintDinoBuff` | DisableImprint恐龙Buff | False | boolean | GameUserSettings.ini + 命令行 | True, disables the creature imprinting player Stat Bonus. Where whomever specifically imprinted on the creature, and raised it to have an Imprinting Quality, gets extra Damage/Resistance buff. | True, disables the creature imprinting player Stat Bonus. |
| `DontAlwaysNotifyPlayerJoined` | DontAlwaysNotify玩家Joined | False | boolean | GameUserSettings.ini + 命令行 | True, globally disables player joins notifications. | True, globally disables player joins notifications. |
| `KickIdlePlayersPeriod` | KickIdle玩家sPeriod | 3600.0 | float | GameUserSettings.ini + 命令行 | in seconds after which characters that have not moved or interacted will be kicked (if -EnableIdlePlayerKick as command line parameter is set). Note: although at code level it is defined as a floating-point number, it is suggested to use an integer i | in seconds after which characters that have not moved or interacted will be kicked (if -EnableIdlePlayerKick as command line parameter is set). |
| `PlayerCharacterFoodDrainMultiplier` | 玩家CharacterFoodDrain倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for player characters' food consumption. Higher values increase food consumption (player characters get hungry faster). | the scaling factor for player characters' food consumption. |
| `PlayerCharacterHealthRecoveryMultiplier` | 玩家CharacterHealthRecovery倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for player characters' health recovery. Higher values increase the recovery rate (player characters heal faster). | the scaling factor for player characters' health recovery. |
| `PlayerCharacterStaminaDrainMultiplier` | 玩家CharacterStaminaDrain倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for player characters' stamina consumption. Higher values increase stamina consumption (player characters get tired faster). | the scaling factor for player characters' stamina consumption. |
| `PlayerCharacterWaterDrainMultiplier` | 玩家CharacterWaterDrain倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for player characters' water consumption. Higher values increase water consumption (player characters get thirsty faster). | the scaling factor for player characters' water consumption. |
| `PlayerDamageMultiplier` | 玩家伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the damage players deal with their attacks. The default value 1 provides normal damage. Higher values increase damage. Lower values decrease it. | the scaling factor for the damage players deal with their attacks. |
| `PlayerResistanceMultiplier` | 玩家受到伤害倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the resistance to damage players receive when attacked. The default value 1 provides normal damage. Higher values decrease resistance, increasing damage per attack. Lower values increase it, reducing damage per attack. A value  | the scaling factor for the resistance to damage players receive when attacked. |
| `PreventMateBoost` | PreventMateBoost | False | boolean | GameUserSettings.ini + 命令行 | True, disables creature mate boosting. | True, disables creature mate boosting. |
| `ShowMapPlayerLocation` | 地图显示玩家位置 | True | boolean | GameUserSettings.ini + 命令行 | False, hides each player their own precise position when they view their map. | False, hides each player their own precise position when they view their map. |

#### 建筑与防御 (13项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowCaveBuildingPvE` | PvE允许洞穴建造 | False | boolean | GameUserSettings.ini | True, allows building in caves when PvE mode is also enabled. Note: no more working in command-line options before patch 241.5. | True, allows building in caves when PvE mode is also enabled. |
| `AllowCaveBuildingPvP` | PvP允许洞穴建造 | True | boolean | GameUserSettings.ini | False, prevents building in caves when PvP mode is also enabled. | False, prevents building in caves when PvP mode is also enabled. |
| `AlwaysAllowStructurePickup` | AlwaysAllow建筑Pickup | False | boolean | GameUserSettings.ini + 命令行 | True disables the timer on the quick pick-up system. | True disables the timer on the quick pick-up system. |
| `DisableStructureDecayPvE` | Disable建筑DecayPvE | False | boolean | GameUserSettings.ini + 命令行 | True, disables the gradual auto-decay of player structures. | True, disables the gradual auto-decay of player structures. |
| `EnableExtraStructurePreventionVolumes` | EnableExtra建筑PreventionVolumes | False | boolean | GameUserSettings.ini + 命令行 | True, disables building in specific resource-rich areas, in particular setup on The Island around the major mountains. | True, disables building in specific resource-rich areas, in particular setup on The Island around the major mountains. |
| `ForceAllStructureLocking` | ForceAll建筑Locking | False | boolean | GameUserSettings.ini + 命令行 | True, will default lock all structures. | True, will default lock all structures. |
| `IgnoreLimitMaxStructuresInRangeTypeFlag` | IgnoreLimitMax建筑sInRangeTypeFlag | False | boolean | GameUserSettings.ini | True, removes the limit of 150 decorative structures (flags, signs, dermis etc.). | True, removes the limit of 150 decorative structures (flags, signs, dermis etc.). |
| `OverrideStructurePlatformPrevention` | 覆盖建筑PlatformPrevention | False | boolean | GameUserSettings.ini + 命令行 | True, turrets becomes be buildable and functional on platform saddles. Since 247.999 applies on spike structure too. Note: despite patch notes, in ShooterGameServer it's coded OverrideStructurePlatformPrevention with two r. | True, turrets becomes be buildable and functional on platform saddles. |
| `PerPlatformMaxStructuresMultiplier` | PerPlatformMax建筑s倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | value increases (from a percentage scale) max number of items place-able on saddles and rafts. | value increases (from a percentage scale) max number of items place-able on saddles and rafts. |
| `PvEAllowStructuresAtSupplyDrops` | PvEAllow建筑sAtSupplyDrops | False | boolean | GameUserSettings.ini + 命令行 | True, allows building near supply drop points in PvE mode. | True, allows building near supply drop points in PvE mode. |
| `StructurePickupHoldDuration` | 建筑PickupHoldDuration | 0.5 | float | GameUserSettings.ini + 命令行 | the quick pick-up hold duration, a value of 0 results in instant pick-up. | the quick pick-up hold duration, a value of 0 results in instant pick-up. |
| `StructureResistanceMultiplier` | 建筑抗性倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the resistance to damage structures receive when attacked. The default value 1 provides normal damage. Higher values decrease resistance, increasing damage per attack. Lower values increase it, reducing damage per attack. A val | the scaling factor for the resistance to damage structures receive when attacked. |
| `TheMaxStructuresInRange` | TheMax建筑sInRange | 10500 | integer | GameUserSettings.ini + 命令行 | the maximum number of structures that can be constructed within a certain (currently hard-coded) range. Replaces the old value NewMaxStructuresInRange | the maximum number of structures that can be constructed within a certain (currently hard-coded) range. |

#### PvP与部落 (10项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AllowFlyerCarryPvE` | PvE允许飞行抓取 | False | boolean | GameUserSettings.ini + 命令行 | True, allows flying creatures to pick up wild creatures in PvE. | True, allows flying creatures to pick up wild creatures in PvE. |
| `DisablePvEGamma` | DisablePvEGamma | False | boolean | GameUserSettings.ini + 命令行 | True, prevents use of console command "gamma" in PvE mode. | True, prevents use of console command "gamma" in PvE mode. |
| `EnablePvPGamma` | EnablePvPGamma | False | boolean | GameUserSettings.ini + 命令行 | True, allows use of console command "gamma" in PvP mode. | True, allows use of console command "gamma" in PvP mode. |
| `PreventOfflinePvP` | PreventOfflinePvP | False | boolean | GameUserSettings.ini + 命令行 | True, enables the Offline Raiding Prevention (ORP). When all tribe members are logged off, tribe characters, creature and structures become invulnerable. Creature starvation still applies, moreover, characters and creature can still die if drowned. D | True, enables the Offline Raiding Prevention (ORP). |
| `PreventOfflinePvPInterval` | PreventOfflinePvP间隔 | 0.0 | float | GameUserSettings.ini + 命令行 | to wait before a ORP becomes active for tribe/players and relative creatures/structures (10 seconds in official PvE servers). Note: although at code level it is defined as a floating-point number, it is suggested to use an integer instead. | to wait before a ORP becomes active for tribe/players and relative creatures/structures (10 seconds in official PvE servers). |
| `PreventTribeAlliances` | PreventTribeAlliances | False | boolean | GameUserSettings.ini + 命令行 | True, prevents tribes from creating Alliances. | True, prevents tribes from creating Alliances. |
| `serverPVE` | serverPVE | False | boolean | GameUserSettings.ini + 命令行 | True, disables PvP and enables PvE | True, disables PvP and enables PvE |
| `TribeNameChangeCooldown` | TribeNameChangeCooldown | 15.0 | float | GameUserSettings.ini + 命令行 | in minutes, in between tribe name changes. Official server use a value of 172800.0 (2 days). | in minutes, in between tribe name changes. |
| `LimitBunkersPerTribe` | LimitBunkersPerTribe | True | boolean | GameUserSettings.ini | Default value: TrueValue type: boolean | 默认: True类型: boolean |
| `LimitBunkersPerTribeNum` | LimitBunkersPerTribeNum | 3 | integer | GameUserSettings.ini | Default value: 3Value type: integer | 默认: 3类型: integer |

#### 时间与存档 (7项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AutoSavePeriodMinutes` | 自动保存间隔(分钟) | 15.0 | float | GameUserSettings.ini + 命令行 | interval for automatic saves. Setting this to 0 will cause constant saving. | interval for automatic saves. |
| `ClampItemSpoilingTimes` | ClampItemSpoilingTimes | False | boolean | GameUserSettings.ini + 命令行 | True, clamps all spoiling times to the items' maximum spoiling times. Useful if any infinite-spoiling exploits were used on the server and you wish to clean them up. Could potentially cause issues with mods that alter spoiling time, hence it is an op | True, clamps all spoiling times to the items' maximum spoiling times. |
| `DayCycleSpeedScale` | 昼夜循环速度 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the passage of time in the ARK, controlling how often day changes to night and night changes to day. The default value 1 provides the same cycle speed as the single player experience (and the official public servers). Values lo | the scaling factor for the passage of time in the ARK, controlling how often day changes to night and night changes to day. |
| `DayTimeSpeedScale` | 白天时间速度 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the passage of time in the ARK during the day. This value determines the length of each day, relative to the length of each night (as specified by NightTimeSpeedScale). Lowering this value increases the length of each day. | the scaling factor for the passage of time in the ARK during the day. |
| `DisableBurrowDecayTimers` | DisableBurrowDecayTimers | False | boolean | GameUserSettings.ini + 命令行 | True, turns off entirely the Burrowbuck's burrow decay timers. | True, turns off entirely the Burrowbuck's burrow decay timers. |
| `NightTimeSpeedScale` | 夜晚时间速度 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the passage of time in the ARK during night time. This value determines the length of each night, relative to the length of each day (as specified by DayTimeSpeedScale) Lowering this value increases the length of each night. | the scaling factor for the passage of time in the ARK during night time. |
| `StructurePickupTimeAfterPlacement` | 建筑PickupTimeAfterPlacement | 30.0 | float | GameUserSettings.ini + 命令行 | of time in seconds after placement that quick pick-up is available. | of time in seconds after placement that quick pick-up is available. |

#### 采集与资源 (6项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `ClampResourceHarvestDamage` | ClampResource采集伤害 | False | boolean | GameUserSettings.ini + 命令行 | True, limit the damage caused by a tame to a resource on harvesting based on resource remaining health.  Note: enabling this setting may result in sensible resource harvesting reduction using high damage tools or creatures. | True, limit the damage caused by a tame to a resource on harvesting based on resource remaining health. |
| `HarvestAmountMultiplier` | 采集产量倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for yields from all harvesting activities (chopping down trees, picking berries, carving carcasses, mining rocks, etc.). Higher values increase the amount of materials harvested with each strike. | the scaling factor for yields from all harvesting activities (chopping down trees, picking berries, carving carcasses, mining rocks, etc.). |
| `HarvestHealthMultiplier` | 可采集物生命值倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the "health" of items that can be harvested (trees, rocks, carcasses, etc.). Higher values increase the amount of damage (i.e., "number of strikes") such objects can withstand before being destroyed, which results in higher ove | the scaling factor for the "health" of items that can be harvested (trees, rocks, carcasses, etc.). |
| `StructurePreventResourceRadiusMultiplier` | 建筑PreventResourceRadius倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | as ResourceNoReplenishRadiusStructures in Game.ini. If both settings are set both multiplier will be applied. Can be useful when cannot change the Game.ini file as it works as a command line option too. | as ResourceNoReplenishRadiusStructures in Game.ini. |
| `BloodforgeReinforceResourceCostMultiplier` | BloodforgeReinforceResourceCost倍率 | 3.0 | float | GameUserSettings.ini | Default value: 3.0Value type: float | 默认: 3.0类型: float |
| `MaxActiveResourceCaches` | MaxActiveResourceCaches | - | integer | GameUserSettings.ini | Value type: integer | 类型: integer |

#### 管理员与安全 (5项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `AdminLogging` | 管理员命令日志 | False | boolean | GameUserSettings.ini + 命令行 | True, logs all admin commands to in-game chat. | True, logs all admin commands to in-game chat. |
| `BanListURL` | 封禁列表URL | - | string | GameUserSettings.ini + 命令行 | with a URLSets the global ban list. Must be enclosed in double quotes. The list is fetched every 10 minutes (to check if there are new banned IDs).  ARK: Survival Evolved: Official ban list URL is http://arkdedicated.com/banlist.txt (before 279.233 t | with a URLSets the global ban list. |
| `ServerAdminPassword` | 管理员密码 | - | string | GameUserSettings.ini + 命令行 | specified, players must provide this password (via the in-game console) to gain access to administrator commands on the server. Note: no quotes are used. | specified, players must provide this password (via the in-game console) to gain access to administrator commands on the server. |
| `ServerPassword` | 服务器连接密码 | - | string | GameUserSettings.ini + 命令行 | specified, players must provide this password to join the server. Note: no quotes are used. | specified, players must provide this password to join the server. |
| `AdminListURL` | AdminListURL | N/A | string | GameUserSettings.ini + 命令行 | with a URLAlternative to AllowedCheaterAccountIDs.txt (see Administrator Whitelisting) using a web resource. The interval at which the server queries the resource to check for admin list update is defined by UpdateAllowedCheatersInterval. Undocumente | with a URLAlternative to AllowedCheaterAccountIDs.txt (see Administrator Whitelisting) using a web resource. |

#### 物品 (5项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `ItemStackSizeMultiplier` | ItemStackSize倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | increasing or decreasing global item stack size, this means all default stack sizes will be multiplied by the value given (excluding items that have a stack size of 1 by default). | increasing or decreasing global item stack size, this means all default stack sizes will be multiplied by the value given (excluding items that have a stack size of 1 by default). |
| `MaxTributeItems` | MaxTributeItems | 50 | integer | GameUserSettings.ini + 命令行 | for uploaded items and resources. Any value less than default will be reverted. Note: Some player claimed maximum 154 to be safe cap and more will corrupt profile/cluster and lead to lose of all stored items and resources but it need to be checked | for uploaded items and resources. |
| `RandomSupplyCratePoints` | RandomSupplyCratePoints | False | boolean | GameUserSettings.ini + 命令行 | True, supply drops are in random locations. Note: This setting is known to cause artifacts becoming inaccessible on Ragnarok if active. | True, supply drops are in random locations. |
| `PreventDownloadItems` | PreventDownloadItems | False | boolean | GameUserSettings.ini + 命令行 | True, prevents items download from ARK Data in Cross-ARK Data Transfer. | True, prevents items download from ARK Data in Cross-ARK Data Transfer. |
| `PreventUploadItems` | PreventUploadItems | False | boolean | GameUserSettings.ini + 命令行 | True, prevents items upload to ARK Data in Cross-ARK Data Transfer. | True, prevents items upload to ARK Data in Cross-ARK Data Transfer. |

#### Mod与地图 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `ActiveMods` | 启用模组列表 | - | list | GameUserSettings.ini | of mod IDs, comma-separated with no spaces, in a single line (for example: ModID1,ModID2,ModID3)Specifies the order and which mods are loaded. ModIDs are comma separated and in one line. Priority is in descending order (the left-most ModID hast the h | of mod IDs, comma-separated with no spaces, in a single line (for example: ModID1,ModID2,ModID3)Specifies the order and which mods are loaded. |
| `ActiveMapMod` | 启用地图模组 | - | mod | GameUserSettings.ini | ID for currently active mod mapSpecifies which mod map is loaded. | ID for currently active mod mapSpecifies which mod map is loaded. |
| `AllowBunkerModulesAboveGround` | AllowBunkerModulesAboveGround | False | boolean | GameUserSettings.ini | Default value: FalseValue type: boolean | 默认: False类型: boolean |
| `AllowBunkerModulesInPreventionZones` | AllowBunkerModulesInPreventionZones | False | boolean | GameUserSettings.ini | Default value: FalseValue type: boolean | 默认: False类型: boolean |

#### 印痕与等级 (2项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `CosmeticWhitelistOverride` | CosmeticWhitelist覆盖 | - | string | GameUserSettings.ini + 命令行 | with a URLURL to a comma-separated list of whitelisted custom cosmetics, in this format: Mod ID/Enable Dynamic Download (0/1)/Allow non-dataonly blueprints(0/1). See this post for details (note: CRC is not required and it's not used by the game anymo | with a URLURL to a comma-separated list of whitelisted custom cosmetics, in this format: Mod ID/Enable Dynamic Download (0/1)/Allow non-dataonly blueprints(0/1). |
| `OverrideOfficialDifficulty` | 覆盖OfficialDifficulty | 0.0 | float | GameUserSettings.ini + 命令行 | you to override the default server difficulty level of 4 with 5 to match the new official server difficulty level. Default value of 0.0 disables the override. A value of 5.0 will allow common creatures to spawn up to level 150. Originally (247.95) av | you to override the default server difficulty level of 4 with 5 to match the new official server difficulty level. |

#### 驯养设置 (1项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `TamingSpeedMultiplier` | 驯服速度倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for creature taming speed. Higher values make taming faster. | the scaling factor for creature taming speed. |

#### 经验值倍率 (1项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `XPMultiplier` | 全局经验倍率 | 1.0 | float | GameUserSettings.ini + 命令行 | the scaling factor for the experience received by players, tribes and tames for various actions. The default value 1 provides the same amounts of experience as in the single player experience (and official public servers). Higher values increase XP a | the scaling factor for the experience received by players, tribes and tames for various actions. |



### 2.2 [SessionSettings]

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `MultiHome` | 多宿主IP地址 | N/A | IP_ADDRESSSpecifies | GameUserSettings.ini | MultiHome IP Address. Boolean Multihome option must be set to True as well (command line or [MultiHome] section). Leave it empty if not using multihoming. Can be specified in command line too. | MultiHome IP Address. |
| `Port` | 游戏端口 | 7777 | integer | GameUserSettings.ini | the UDP Game Port. See Dedicated server setupNote: command line append syntax is not supported by  ARK: Survival Ascended | the UDP Game Port. |
| `QueryPort` | Steam查询端口 | 27015 | integer | GameUserSettings.ini | the UDP Steam Query Port. See Dedicated server setup | the UDP Steam Query Port. |
| `SessionName` | 服务器显示名称 | ARK #123456 | string | GameUserSettings.ini | the Server name advertised in the Game Server Browser as well in Steam Server browser. If no name is provide, the default name will be ARK # followed by a random 6 digit number. Note: Name must not be typed between quotes unless it is launched from c | the Server name advertised in the Game Server Browser as well in Steam Server browser. |


### 2.3 [/Script/Engine.GameSession]

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `MaxPlayers` | 最大玩家数 | 70 | integer | GameUserSettings.ini | the maximum number of players that can play on the server simultaneously. ASA: This setting is replaced with -WinLiveMaxPlayers in the command line options, as otherwise, it will force it back to the default value. | the maximum number of players that can play on the server simultaneously. |


### 2.4 [MessageOfTheDay]

```ini
[MessageOfTheDay]
Message=欢迎来到服务器！请遵守规则。
Duration=30
```

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `Duration` | 显示持续时间(秒) | 20 | integer | GameUserSettings.ini | in seconds the duration of the displayed message on player log-in. | in seconds the duration of the displayed message on player log-in. |
| `Message` | 消息内容 | N/A | string | GameUserSettings.ini | single line string for a message displayed to played once logged-in. No quotes needed. Use \n to start a new line in the message. | single line string for a message displayed to played once logged-in. |


---

## 3. Game.ini

**路径**: `ShooterGame/Saved/Config/WindowsServer/Game.ini`

> 直接在文件中添加配置项，无需 Section 头

#### 繁殖与成长 (14项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `BabyCuddleGracePeriodMultiplier` | 婴儿照料宽限期倍率 | 1.0 | float | Game.ini | how long after delaying cuddling with the Baby before Imprinting Quality starts to decrease. | how long after delaying cuddling with the Baby before Imprinting Quality starts to decrease. |
| `BabyCuddleIntervalMultiplier` | 婴儿照料间隔倍率 | 1.0 | float | Game.ini | how often babies needs attention for imprinting. More often means you'll need to cuddle with them more frequently to gain Imprinting Quality. Scales always according to default BabyMatureSpeedMultiplier value: set at 1.0 the imprint request is every  | how often babies needs attention for imprinting. |
| `BabyCuddleLoseImprintQualitySpeedMultiplier` | 婴儿印记品质下降速度倍率 | 1.0 | float | Game.ini | how fast Imprinting Quality decreases after the grace period if you haven't yet cuddled with the Baby. | how fast Imprinting Quality decreases after the grace period if you haven't yet cuddled with the Baby. |
| `BabyFoodConsumptionSpeedMultiplier` | 婴儿食物消耗速度倍率 | 1.0 | float | Game.ini | the speed that baby tames eat their food. A lower value decreases (by percentage) the food eaten by babies. | the speed that baby tames eat their food. |
| `BabyImprintAmountMultiplier` | 印记量倍率 | 1.0 | float | Game.ini | the percentage each imprint provides. A higher value, will rise the amount of imprinting % at each baby care/cuddle, a lower value will decrease it. This multiplier is global, meaning it will affect the imprinting progression of every species. See al | the percentage each imprint provides. |
| `BabyImprintingStatScaleMultiplier` | 印记属性缩放倍率 | 1.0 | float | Game.ini | how much of an effect on stats the Imprinting Quality has. Set it to 0 to effectively disable the system. | how much of an effect on stats the Imprinting Quality has. |
| `BabyMatureSpeedMultiplier` | 婴儿成长速度倍率 | 1.0 | float | Game.ini | the maturation speed of babies. A higher number decreases (by percentage) time needed for baby tames to mature. See Times for Breeding tables for values at 1.0, see The Imprinting formula how it affects the imprinting amount at each baby care/cuddle. | the maturation speed of babies. |
| `bDisableWirelessCraftingForPlayers` | 禁用玩家无线制作 | false | boolean | Game.ini | True, prevents wireless crafting from Tek Dedicated Storage when crafting in the player inventory. | True, prevents wireless crafting from Tek Dedicated Storage when crafting in the player inventory. |
| `bUseSingleplayerSettings` | 启用单人游戏设置 | False | boolean | Game.ini | True, all game settings will be more balanced for an individual player experience. Useful for dedicated server with a very small amount of players. See Single Player Settings section for more details. | True, all game settings will be more balanced for an individual player experience. |
| `EggHatchSpeedMultiplier` | 蛋孵化速度倍率 | 1.0 | float | Game.ini | the time needed for a fertilised egg to hatch. A higher value decreases (by percentage) that time. | the time needed for a fertilised egg to hatch. |
| `LayEggIntervalMultiplier` | 产蛋间隔倍率 | 1.0 | float | Game.ini | the time between eggs are spawning / being laid. Higher number increases it (by percentage). | the time between eggs are spawning / being laid. |
| `PerLevelStatsMultiplier_Player[<integer>]` | PerLevelStatsMultiplier_玩家[<integer>] | N/A | float | Game.ini | Player stats. See Level stats related section for more detail. | Player stats. |
| `PreventBreedingForClassNames` | 禁止指定物种繁殖 | N/A | "<string>"Prevents | Game.ini | breeding of specific creatures via classname. E.g. PreventBreedingForClassNames="Argent_Character_BP_C". Creature classnames can be found on the Creature IDs page. | breeding of specific creatures via classname. |
| `ResourceNoReplenishRadiusPlayers` | 玩家周围资源不刷新半径 | 1.0 | float | Game.ini | how resources regrow closer or farther away from players. Values higher than 1.0 increase the distance around players where resources are not allowed to grow back. Values between 0 and 1.0 will reduce it. | how resources regrow closer or farther away from players. |

#### 通用设置 (10项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bAllowUnlimitedRespecs` | 允许无限洗点 | False | boolean | Game.ini | True, allows more than one usage of Mindwipe Tonic without 24 hours cooldown. | True, allows more than one usage of Mindwipe Tonic without 24 hours cooldown. |
| `CustomRecipeEffectivenessMultiplier` | 自定义配方效果倍率 | 1.0 | float | Game.ini | the effectiveness of custom recipes. A higher value increases (by percentage) their effectiveness. | the effectiveness of custom recipes. |
| `CustomRecipeSkillMultiplier` | 自定义配方技能倍率 | 1.0 | float | Game.ini | the effect of the players crafting speed level that is used as a base for the formula in creating a custom recipe. A higher number increases (by percentage) the effect. | the effect of the players crafting speed level that is used as a base for the formula in creating a custom recipe. |
| `LimitGeneratorsNum` | 发电机数量上限 | 3 | integer | Game.ini | the number of generators in the area defined by LimitGeneratorsRange. Official servers have it set to 3. | the number of generators in the area defined by LimitGeneratorsRange. |
| `LimitGeneratorsRange` | 发电机限制范围 | 15000 | integer | Game.ini | the area range (in Unreal Units) in which the option LimitGeneratorsNum applies. Official servers have it set to 15000. | the area range (in Unreal Units) in which the option LimitGeneratorsNum applies. |
| `HairGrowthSpeedMultiplier` | 毛发生长速度倍率 | 1.0 (ASE), 0 (ASA) | float | Game.ini | the hair growth. Higher values increase speed of growth. | the hair growth. |
| `MatingIntervalMultiplier` | 交配间隔倍率 | 1.0 | float | Game.ini | the interval between tames can mate. A lower value decreases it (on a percentage scale). Example: a value of 0.5 would allow tames to mate 50% sooner. | the interval between tames can mate. |
| `MatingSpeedMultiplier` | 交配速度倍率 | 1.0 | float | Game.ini | the speed at which tames mate with each other. A higher value increases it (by percentage). Example: MatingSpeedMultiplier=2.0 would cause tames to complete mating in half the normal time. | the speed at which tames mate with each other. |
| `MaxFallSpeedMultiplier` | 最大坠落速度倍率 | 1.0 | float | Game.ini | the falling speed multiplier at which players starts taking fall damage. The falling speed is based on the time players spent in the air while having a negated Z axis velocity meaning that the higher this setting is, the longer players can fall witho | the falling speed multiplier at which players starts taking fall damage. |
| `PoopIntervalMultiplier` | 排便间隔倍率 | 1.0 | float | Game.ini | how frequently survivors can poop. Higher value decreases it (by percentage) | how frequently survivors can poop. |

#### ASA新增功能 (8项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bAllowFlyerSpeedLeveling` | 允许飞行生物升级速度 | False | boolean | Game.ini | whether flyer creatures can have their Movement Speed levelled up. In ARK: Survival Ascended, setting this to True only works if bAllowSpeedLeveling is also True. | whether flyer creatures can have their Movement Speed levelled up. |
| `bAllowSpeedLeveling` | 允许升级移动速度 | False | boolean | Game.ini | whether players and non-flyer creatures can have their Movement Speed levelled up. | whether players and non-flyer creatures can have their Movement Speed levelled up. |
| `bDisableWirelessCrafting` | 禁用无线制作 | false | boolean | Game.ini | True, prevents wireless crafting from Tek Dedicated Storage. | True, prevents wireless crafting from Tek Dedicated Storage. |
| `CheatTeleportLocations=(TeleportName="<string>",TeleportLocation=(X=<float>,Y=-<float>,Z=<float>))` | CheatTeleportLocations=(TeleportName="<string>",TeleportLocation=(X=<float>,Y=-<float>,Z=<float>)) | - | (...)Creates | Game.ini | a named teleport location that can be used with the TP command. The coordinates must be listed in Unreal units, not in-game gps coordinates. Example:  CheatTeleportLocations=(TeleportName="Hightower",TeleportLocation=(X=467967.0,Y=-359082.0,Z=6879.0) | a named teleport location that can be used with the TP command. |
| `WirelessCraftingRangeOverride` | 无线制作范围覆盖 | 3000 | integer | Game.ini | the wireless crafting range (in Unreal Units) on Tek Dedicated Storage. | the wireless crafting range (in Unreal Units) on Tek Dedicated Storage. |
| `ValgueroMemorialEntries` | 瓦尔盖罗纪念碑名列表 | N/A | list | Game.ini | of player names, semicolon-separated with no spaces, in a single line (for example: Name1;Name2;Name3;)The Valguero Memorial is now interactable, honouring those who have ascended by displaying their names. Server owners can customize the list of nam | of player names, semicolon-separated with no spaces, in a single line (for example: Name1;Name2;Name3;)The Valguero Memorial is now interactable, honouring those who have ascended by displaying their names. |
| `BaseHexagonRewardMultiplier` | 六角币奖励基础倍率 | 1.0 | float | Game.ini | the missions score hexagon rewards. Also scales token rewards in Club Ark (ASA). | the missions score hexagon rewards. |
| `HexagonCostMultiplier` | 六角币消耗倍率 | 1.0 | float | Game.ini | the hexagon cost of items in the Hexagon store. Also scales token cost of items in Club Ark (ASA). | the hexagon cost of items in the Hexagon store. |

#### 经验值倍率 (5项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `CraftXPMultiplier` | 制作经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for crafting. | the amount of XP earned for crafting. |
| `GenericXPMultiplier` | 通用经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for generic XP (automatic over time). | the amount of XP earned for generic XP (automatic over time). |
| `HarvestXPMultiplier` | 采集经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for harvesting. | the amount of XP earned for harvesting. |
| `KillXPMultiplier` | 击杀经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for a kill. | the amount of XP earned for a kill. |
| `SpecialXPMultiplier` | 特殊事件经验倍率 | 1.0 | float | Game.ini | the amount of XP earned for SpecialEvent. | the amount of XP earned for SpecialEvent. |

#### PvP与部落 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisableFriendlyFire` | 禁用友军伤害 | False | boolean | Game.ini | True, prevents Friendly-Fire (among tribe mates/tames/structures). | True, prevents Friendly-Fire (among tribe mates/tames/structures). |
| `bPvEDisableFriendlyFire` | PvE禁用友军伤害 | False | boolean | Game.ini | True, disabled Friendly-Fire (among tribe mates/tames/structures) in PvE servers. | True, disabled Friendly-Fire (among tribe mates/tames/structures) in PvE servers. |
| `IgnorePVPMountedWeaponryRestrictions` | IgnorePVPMountedWeaponryRestrictions | False | boolean | Game.ini | further information has been added about this variable. If you know anything, please consider creating an account and contributing. | further information has been added about this variable. |
| `TribeTowerBonusMultiplier` | 部落之塔奖励倍率 | 2.0 | float | Game.ini | for Tribe Tower bonus. | for Tribe Tower bonus. |

#### 生物设置 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisableWirelessCraftingForDinos` | 禁用恐龙无线制作 | false | boolean | Game.ini | True, prevents wireless crafting from Tek Dedicated Storage when crafting in dino inventories. | True, prevents wireless crafting from Tek Dedicated Storage when crafting in dino inventories. |
| `bUseDinoLevelUpAnimations` | 使用恐龙升级动画 | True | boolean | Game.ini | False, tame creatures on level-up will not perform the related animation. | False, tame creatures on level-up will not perform the related animation. |
| `ConfigAddNPCSpawnEntriesContainer` | 添加NPC生成区域配置 | N/A | (...)Adds | Game.ini | specific creatures in spawn areas. See Creature Spawn related section for more detail. | specific creatures in spawn areas. |
| `WildDinoCharacterFoodDrainMultiplier` | 野生恐龙食物消耗倍率 | 1.0 | float | Game.ini | how fast wild creatures consume food. | how fast wild creatures consume food. |

#### 采集与资源 (4项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `CropDecaySpeedMultiplier` | 作物腐烂速度倍率 | 1.0 | float | Game.ini | the speed of crop decay in plots. A higher value decrease (by percentage) speed of crop decay in plots. | the speed of crop decay in plots. |
| `CropGrowthSpeedMultiplier` | 作物生长速度倍率 | 1.0 | float | Game.ini | the speed of crop growth in plots. A higher value increases (by percentage) speed of crop growth. | the speed of crop growth in plots. |
| `HarvestResourceItemAmountClassMultipliers` | 资源采集量分类倍率 | N/A | (...)Scales | Game.ini | on a per-resource type basis, the amount of resources harvested. See Items related section for more details. | on a per-resource type basis, the amount of resources harvested. |
| `ResourceNoReplenishRadiusStructures` | 建筑周围资源不刷新半径 | 1.0 | float | Game.ini | how resources regrow closer or farther away from structures Values higher than 1.0 increase the distance around structures where resources are not allowed to grow back. Values between 0 and 1.0 will reduce it. | how resources regrow closer or farther away from structures Values higher than 1.0 increase the distance around structures where resources are not allowed to grow back. |

#### Mod与地图 (3项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisablePhotoMode` | 禁用拍照模式 | False | boolean | Game.ini | if photo mode is allowed (False) or not (True). | if photo mode is allowed (False) or not (True). |
| `bShowCreativeMode` | 显示创造模式 | False | boolean | Game.ini | True, adds a button to the pause menu to enable/disable creative mode. | True, adds a button to the pause menu to enable/disable creative mode. |
| `PhotoModeRangeLimit` | 拍照模式最大距离 | 3000 | integer | Game.ini | the maximum distance between photo mode camera position and player position. | the maximum distance between photo mode camera position and player position. |

#### 建筑与防御 (3项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `bDisableStructurePlacementCollision` | 禁用建筑放置碰撞 | False | boolean | Game.ini | True, allows for structures to clip into the terrain. | True, allows for structures to clip into the terrain. |
| `bDisableWirelessCraftingForStructures` | 禁用建筑无线制作 | false | boolean | Game.ini | True, prevents wireless crafting from Tek Dedicated Storage when crafting in structure inventories. | True, prevents wireless crafting from Tek Dedicated Storage when crafting in structure inventories. |
| `bIgnoreStructuresPreventionVolumes` | 忽略建筑禁止区域 | False | boolean | Game.ini | True, enables building areas where normally it's not allowed, such around some maps' Obelisks, in the Aberration Portal and in Mission Volumes areas on Genesis: Part 1. Note: in Genesis: Part 1 this settings is enabled by default and there is an ad h | True, enables building areas where normally it's not allowed, such around some maps' Obelisks, in the Aberration Portal and in Mission Volumes areas on Genesis: Part 1. |

#### 印痕与等级 (3项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `DestroyTamesOverLevelClamp` | 销毁超过等级上限的驯养 | 0 | integer | Game.ini | that exceed that level will be deleted on server start. Official servers have it set to 450. | that exceed that level will be deleted on server start. |
| `LevelExperienceRampOverrides` | 等级经验曲线覆盖 | N/A | (...)Configures | Game.ini | the total number of levels available to players and tame creatures and the experience points required to reach each level. See Players and tames levels override section for more details. | the total number of levels available to players and tame creatures and the experience points required to reach each level. |
| `OverrideNamedEngramEntries` | 覆盖命名印痕条目 | N/A | (...)Configures | Game.ini | the status and requirements for learning an engram, specified by its name. See Engram Entries related section for more detail. | the status and requirements for learning an engram, specified by its name. |

#### 物品 (2项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `CraftingSkillBonusMultiplier` | 制作技能奖励倍率 | 1.0 | float | Game.ini | the bonus received from upgrading the Crafting Skill. | the bonus received from upgrading the Crafting Skill. |
| `ExcludeItemIndices` | 排除物品索引 | N/A | integer | Game.ini | an item from supply crates specifying its Item ID. You can have multiple lines of this option. | an item from supply crates specifying its Item ID. |

#### 时间与存档 (2项)

| 配置项 | 中文名 | 默认值 | 类型 | 配置方式 | 说明 (EN) | 说明 (中文) |
|--------|--------|--------|------|----------|-----------|-------------|
| `GlobalItemDecompositionTimeMultiplier` | 全局物品分解时间倍率 | 1.0 | float | Game.ini | the decomposition time of dropped items, loot bags etc. globally. Higher values prolong the time. | the decomposition time of dropped items, loot bags etc. |
| `GlobalSpoilingTimeMultiplier` | 全局腐烂时间倍率 | 1.0 | float | Game.ini | the spoiling time of perishables globally. Higher values prolong the time. | the spoiling time of perishables globally. |



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
