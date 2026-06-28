// 服务器「简单 key=value 设置」的注册表 + 通用节内解析/序列化/合并。
//
// 与 arkGameIni.js（数组/嵌套）不同，这里处理跨两个文件的标量/布尔项：
//   - GameUserSettings.ini  [ServerSettings]
//   - Game.ini              [/script/shootergame.shootergamemode]
//
// 新增一个简单项 = 往 SETTINGS_REGISTRY 加一行。合并算法与 arkGameIni 一致：
// 只删受管行、块插回首个被删行处/节末/新建节，其余内容逐字保留。

export const GAME_SECTION = '/script/shootergame.shootergamemode'
export const GUS_SECTION = 'serversettings'
export const GAME_SECTION_HEADER = '[/script/shootergame.shootergamemode]'
export const GUS_SECTION_HEADER = '[ServerSettings]'

// inverse=true 表示 UI 开关与 INI 语义相反（如「开启 PvP」= serverPVE=False）。
// default 为 INI 原生（stored）默认值。
export const SETTINGS_REGISTRY = [
  // —— PvP / PvE 规则 ——
  {
    key: 'serverPVE', file: 'gus', type: 'bool', default: false, inverse: true, group: 'pvp',
    label: '开启 PvP 模式', tip: '关闭即为 PvE。对应 serverPVE：True 时禁用 PvP、启用 PvE。',
  },
  {
    key: 'bDisableFriendlyFire', file: 'game', type: 'bool', default: false, inverse: true, group: 'pvp',
    label: '开启 PvP 队友伤害', tip: '队友 / 驯养 / 建筑之间可造成伤害。对应 bDisableFriendlyFire=False。',
  },
  {
    key: 'bPvEDisableFriendlyFire', file: 'game', type: 'bool', default: false, inverse: true, group: 'pvp',
    label: '开启 PvE 队友伤害', tip: 'PvE 服务器队友伤害。对应 bPvEDisableFriendlyFire=False。',
  },
  {
    key: 'EnablePvPGamma', file: 'gus', type: 'bool', default: false, inverse: false, group: 'pvp',
    label: '允许 PvP 中使用 Gamma 命令', tip: 'True 时允许玩家在 PvP 模式中使用控制台 gamma 命令调整亮度。',
  },
  {
    key: 'AllowCaveBuildingPvP', file: 'gus', type: 'bool', default: true, inverse: false, group: 'pvp',
    label: 'PvP 允许洞穴建造', tip: 'False 时禁止在 PvP 模式下于洞穴内建造建筑。',
  },
  {
    key: 'PvPStructureDecay', file: 'gus', type: 'bool', default: false, inverse: false, group: 'pvp',
    label: '启用 PvP 建筑腐烂', tip: 'True 时在 PvP 模式中启用建筑自动腐烂。',
  },
  {
    key: 'PvPZoneStructureDamageMultiplier', file: 'gus', type: 'float', default: 6.0, inverse: false, group: 'pvp',
    label: 'PvP 区域建筑伤害倍率', tip: 'PvP 区域内对建筑造成伤害的倍率，默认 6。',
  },
  {
    key: 'IncreasePvPRespawnIntervalBaseAmount', file: 'gus', type: 'float', default: 0.0, inverse: false, group: 'pvp',
    label: 'PvP 重生间隔基础值（秒）', tip: '连续 PvP 死亡后额外增加的重生等待基础秒数。',
  },
  {
    key: 'IncreasePvPRespawnIntervalCheckPeriod', file: 'gus', type: 'float', default: 0.0, inverse: false, group: 'pvp',
    label: 'PvP 重生间隔检查周期（秒）', tip: '判定「连续 PvP 死亡」的时间窗口（秒）。',
  },
  {
    key: 'IncreasePvPRespawnIntervalMultiplier', file: 'gus', type: 'float', default: 0.0, inverse: false, group: 'pvp',
    label: 'PvP 重生间隔递增倍率', tip: '连续 PvP 死亡后重生等待时间的递增倍率。',
  },
  {
    key: 'IgnorePVPMountedWeaponryRestrictions', file: 'game', type: 'bool', default: false, inverse: false, group: 'pvp',
    label: '忽略 PvP 骑乘武器限制', tip: 'True 时忽略 PvP 模式下骑乘武器的默认限制。',
  },
  // —— 建造限制 ——
  {
    key: 'AllowCaveBuildingPvE', file: 'gus', type: 'bool', default: false, inverse: false, group: 'build',
    label: '开启 PvE 洞穴建筑', tip: 'PvE 模式下允许在洞穴内建造。',
  },
  {
    key: 'EnableExtraStructurePreventionVolumes', file: 'gus', type: 'bool', default: false, inverse: false, group: 'build',
    label: '防止资源丰富区建设', tip: '禁止在特定资源丰富区域建造（如主岛主要山脉周边）。',
  },
  // —— 补给箱 / 钓鱼 ——
  {
    key: 'bDisableLootCrates', file: 'game', type: 'bool', default: false, inverse: false, group: 'loot',
    label: '禁用补给箱', tip: '禁用地图上的补给箱（空投）。',
  },
  {
    key: 'SupplyCrateLootQualityMultiplier', file: 'game', type: 'float', default: 1.0, inverse: false, group: 'loot',
    label: '补给箱质量倍数', tip: '补给箱掉落物品质量倍率（默认 1.0）。',
  },
  {
    key: 'FishingLootQualityMultiplier', file: 'game', type: 'float', default: 1.0, inverse: false, group: 'loot',
    label: '钓鱼质量倍数', tip: '钓鱼掉落物品质量倍率（默认 1.0）。',
  },
  // —— 低温舱行为 ——
  {
    key: 'AllowCryoFridgeOnSaddle', file: 'gus', type: 'bool', default: false, inverse: false, group: 'cryo',
    label: '允许冷冻箱放在鞍座上', tip: '允许将冷冻箱建造在平台鞍座和木筏上。',
  },
  {
    key: 'DisableCryopodEnemyCheck', file: 'gus', type: 'bool', default: false, inverse: false, group: 'cryo',
    label: '禁用低温舱敌人检测', tip: 'True 时可在敌人附近使用低温舱。',
  },
  {
    key: 'DisableCryopodFridgeRequirement', file: 'gus', type: 'bool', default: false, inverse: false, group: 'cryo',
    label: '禁用低温舱冰箱需求', tip: 'True 时使用低温舱无需在通电冷冻箱范围内。',
  },
  {
    key: 'EnableCryoSicknessPVE', file: 'gus', type: 'bool', default: false, inverse: false, group: 'cryo',
    label: '启用 PvE 低温症', tip: 'PvE 部署生物时启用低温舱冷却计时（低温症）。',
  },
  // —— 低温舱削弱 ——
  {
    key: 'EnableCryopodNerf', file: 'gus', type: 'bool', default: false, inverse: false, group: 'cryo',
    label: '启用低温舱削弱', tip: '启用低温舱削弱系统（生物解除低温后暂时降低战斗力）。',
  },
  {
    key: 'CryopodNerfDamageMult', file: 'gus', type: 'float', default: 0.01, inverse: false, group: 'cryo',
    label: '削弱伤害倍率', tip: '解除低温后生物的伤害倍率，0.01 表示保留 1%（移除 99%）。',
  },
  {
    key: 'CryopodNerfDuration', file: 'gus', type: 'float', default: 0.0, inverse: false, group: 'cryo',
    label: '削弱持续时间（秒）', tip: '低温舱削弱效果持续时间（秒）。',
  },
  {
    key: 'CryopodNerfIncomingDamageMultPercent', file: 'gus', type: 'float', default: 0.0, inverse: false, group: 'cryo',
    label: '削弱受伤倍率 (%)', tip: '低温舱生物受到伤害的额外倍率百分比。',
  },
  // —— 低温医院 ——
  {
    key: 'CryoHospitalHoursToRegenFood', file: 'gus', type: 'float', default: 24.0, inverse: false, group: 'cryo',
    label: '食物恢复时长（小时）', tip: '低温医院恢复生物食物所需时间，默认 24 小时。',
  },
  {
    key: 'CryoHospitalHoursToDrainTorpor', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'cryo',
    label: '晕厥消耗时长（小时）', tip: '低温医院消耗晕厥值所需时间，默认 1 小时。',
  },
  {
    key: 'CryoHospitalHoursToRegenHP', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'cryo',
    label: 'HP 恢复时长（小时）', tip: '低温医院恢复 HP 所需时间，默认 1 小时。',
  },
  {
    key: 'CryoHospitalMatingCooldownReduction', file: 'gus', type: 'float', default: 2.0, inverse: false, group: 'cryo',
    label: '交配冷却缩减倍率', tip: '低温医院缩减交配冷却的倍率，默认 2。',
  },
  // —— 跨服传输（Cross-ARK）——
  {
    key: 'noTributeDownloads', file: 'gus', type: 'bool', default: false, inverse: false, group: 'cross',
    label: '禁止所有跨服数据下载', tip: 'True 时禁止所有 Cross-ARK 数据传入下载。',
  },
  {
    key: 'PreventDownloadSurvivors', file: 'gus', type: 'bool', default: false, inverse: false, group: 'cross',
    label: '禁用角色下载', tip: '禁止玩家从其他服务器下载角色。',
  },
  {
    key: 'PreventDownloadItems', file: 'gus', type: 'bool', default: false, inverse: false, group: 'cross',
    label: '禁用物品下载', tip: '禁止玩家从其他服务器下载物品。',
  },
  {
    key: 'PreventDownloadDinos', file: 'gus', type: 'bool', default: false, inverse: false, group: 'cross',
    label: '禁用恐龙下载', tip: '禁止玩家从其他服务器下载恐龙。',
  },
  {
    key: 'CrossARKAllowForeignDinoDownloads', file: 'gus', type: 'bool', default: false, inverse: false, group: 'cross',
    label: '允许外来恐龙下载', tip: 'True 时允许下载本地图非原生生物（如异常地图的生物）。',
  },
  {
    key: 'PreventUploadSurvivors', file: 'gus', type: 'bool', default: false, inverse: false, group: 'cross',
    label: '禁用角色上传', tip: '禁止玩家上传角色到其他服务器。',
  },
  {
    key: 'PreventUploadItems', file: 'gus', type: 'bool', default: false, inverse: false, group: 'cross',
    label: '禁用物品上传', tip: '禁止玩家上传物品到其他服务器。',
  },
  {
    key: 'PreventUploadDinos', file: 'gus', type: 'bool', default: false, inverse: false, group: 'cross',
    label: '禁用恐龙上传', tip: '禁止玩家上传恐龙到其他服务器。',
  },
  {
    key: 'MaxTributeDinos', file: 'gus', type: 'int', default: 20, inverse: false, group: 'cross',
    label: '最大恐龙上传数量', tip: '同一时间最多可上传的恐龙数量，默认 20。',
  },
  {
    key: 'MaxTributeItems', file: 'gus', type: 'int', default: 50, inverse: false, group: 'cross',
    label: '最大物品上传数量', tip: '同一时间最多可上传的物品数量，默认 50。',
  },
  {
    key: 'TributeCharacterExpirationSeconds', file: 'gus', type: 'int', default: 0, inverse: false, group: 'cross',
    label: '角色上传期限（秒）', tip: '上传角色的保留时间（秒），0 = 使用游戏默认值。',
  },
  {
    key: 'TributeItemExpirationSeconds', file: 'gus', type: 'int', default: 86400, inverse: false, group: 'cross',
    label: '物品上传期限（秒）', tip: '上传物品的保留时间（秒），默认 86400（24 小时）。',
  },
  {
    key: 'TributeDinoExpirationSeconds', file: 'gus', type: 'int', default: 86400, inverse: false, group: 'cross',
    label: '恐龙上传期限（秒）', tip: '上传恐龙的保留时间（秒），默认 86400（24 小时）。',
  },
  // —— 生物设置：数量上限 ——
  {
    key: 'MaxTamedDinos', file: 'gus', type: 'int', default: 5000, inverse: false, group: 'dino_num',
    label: '服务器驯养硬上限', tip: '全服最大驯养数量（全局硬上限），超过后无法继续驯服，默认 5000。建议使用整数。',
  },
  {
    key: 'MaxPersonalTamedDinos', file: 'gus', type: 'int', default: 0, inverse: false, group: 'dino_num',
    label: '每部落驯养上限（0=禁用）', tip: '每个部落的驯养生物总数上限（官服 PvE=500，PvP=300），0 禁用该限制。',
  },
  {
    key: 'MaxPlatformSaddleStructureLimit', file: 'gus', type: 'int', default: 75, inverse: false, group: 'dino_num',
    label: '平台鞍建筑数量上限', tip: '平台鞍（木筏/飞行坐骑）上允许放置的最大建筑数量，默认 75。',
  },
  // —— 生物设置：倍率 ——
  {
    key: 'DinoDamageMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '野生恐龙攻击伤害倍率', tip: '野生生物发动攻击时的伤害倍率，默认 1.0。',
  },
  {
    key: 'TamedDinoDamageMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '驯服恐龙攻击伤害倍率', tip: '驯服生物发动攻击时的伤害倍率，默认 1.0。',
  },
  {
    key: 'DinoResistanceMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '野生恐龙伤害抗性倍率', tip: '野生生物承受伤害的抗性倍率，值越大抗性越低（受到伤害越多），默认 1.0。',
  },
  {
    key: 'TamedDinoResistanceMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '驯服恐龙伤害抗性倍率', tip: '驯服生物承受伤害的抗性倍率，值越大抗性越低，默认 1.0。',
  },
  {
    key: 'DinoCharacterFoodDrainMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '野生恐龙食物消耗倍率', tip: '野生生物食物消耗速度的倍率，同时影响驯服时间，默认 1.0。',
  },
  {
    key: 'TamedDinoCharacterFoodDrainMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '驯服恐龙食物消耗倍率', tip: '驯服生物食物消耗速度倍率，默认 1.0。',
  },
  {
    key: 'WildDinoTorporDrainMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '野生恐龙眩晕值消耗倍率', tip: '野生生物眩晕值（Torpor）消耗速度倍率，值越大越难驯服，默认 1.0。',
  },
  {
    key: 'TamedDinoTorporDrainMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '驯服恐龙眩晕值消耗倍率', tip: '驯服生物眩晕值消耗速度倍率，默认 1.0。',
  },
  {
    key: 'DinoCharacterStaminaDrainMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '恐龙体力消耗倍率', tip: '生物体力消耗速度倍率，值越大越快疲劳，默认 1.0。',
  },
  {
    key: 'DinoCharacterHealthRecoveryMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '恐龙生命恢复倍率', tip: '生物生命值自然恢复速度倍率，值越大恢复越快，默认 1.0。',
  },
  {
    key: 'DinoHarvestingDamageMultiplier', file: 'gus', type: 'float', default: 3.2, inverse: false, group: 'dino_mult',
    label: '恐龙采集伤害倍率', tip: '恐龙采集资源时对节点造成的伤害倍率，影响采集效率，默认 3.2。',
  },
  {
    key: 'DinoTurretDamageMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '炮塔对恐龙伤害倍率', tip: '炮塔对恐龙造成伤害的倍率，默认 1.0。',
  },
  {
    key: 'PassiveTameIntervalMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '被动驯服间隔倍率', tip: '被动驯服（投食驯服）的等待间隔倍率，值越大间隔越长，默认 1.0。',
  },
  {
    key: 'TamingSpeedMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '驯服速度倍率', tip: '驯服生物所需时间的倍率，值越大驯服越快，默认 1.0。',
  },
  {
    key: 'PvEDinoDecayPeriodMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: 'PvE 恐龙自动放生时间倍率', tip: 'PvE 模式下驯服生物的自动衰减周期倍率，需 DisableDinoDecayPvE=False 才生效，默认 1.0。',
  },
  {
    key: 'RaidDinoCharacterFoodDrainMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'dino_mult',
    label: '突袭恐龙食物消耗倍率', tip: '突袭类恐龙（如泰坦龙）的食物消耗速度倍率，默认 1.0。',
  },
  // —— 生物设置：行为规则 ——
  {
    key: 'AllowRaidDinoFeeding', file: 'gus', type: 'bool', default: false, inverse: false, group: 'dino_rule',
    label: '允许喂食突袭恐龙', tip: 'True 时允许永久驯服泰坦龙（可以持续喂食）。',
  },
  {
    key: 'AllowFlyingStaminaRecovery', file: 'gus', type: 'bool', default: false, inverse: false, group: 'dino_rule',
    label: '允许飞行耐力恢复', tip: 'True 时飞行生物在飞行过程中可以自然回复体力，默认需落地才能恢复。',
  },
  {
    key: 'AllowMultipleAttachedC4', file: 'gus', type: 'bool', default: false, inverse: false, group: 'dino_rule',
    label: '允许恐龙贴多个 C4', tip: 'True 时允许在同一只生物上附着超过一个 C4 炸弹。',
  },
  {
    key: 'AllowFlyerCarryPvE', file: 'gus', type: 'bool', default: false, inverse: false, group: 'dino_rule',
    label: 'PvE 允许飞行生物拾取（野生）', tip: 'True 时 PvE 模式下飞行生物可以抓取野生生物。PvP 模式默认允许，无需手动开启。',
  },
  {
    key: 'DisableDinoDecayPvE', file: 'gus', type: 'bool', default: false, inverse: false, group: 'dino_rule',
    label: '禁用 PvE 恐龙自动放生', tip: 'True 时禁用 PvE 模式下驯服生物的自动衰减放生机制。',
  },
  {
    key: 'PvPDinoDecay', file: 'gus', type: 'bool', default: false, inverse: false, group: 'dino_rule',
    label: 'PvP 驯养衰减（ORP 期间）', tip: 'True 时在离线保护（ORP）激活期间驯养生物也会衰减。',
  },
  {
    key: 'AutoDestroyDecayedDinos', file: 'gus', type: 'bool', default: false, inverse: false, group: 'dino_rule',
    label: '自动销毁已腐烂恐龙', tip: 'True 时自动销毁已进入衰减（可被领养）状态的驯服生物。',
  },
  {
    key: 'bDisableDinoTaming', file: 'game', type: 'bool', default: false, inverse: false, group: 'dino_rule',
    label: '禁止驯服', tip: 'True 时阻止玩家驯服野生生物。',
  },
  {
    key: 'bDisableDinoRiding', file: 'game', type: 'bool', default: false, inverse: false, group: 'dino_rule',
    label: '禁止骑乘', tip: 'True 时阻止玩家骑乘已驯服生物。',
  },
  {
    key: 'bAllowSpeedLeveling', file: 'game', type: 'bool', default: false, inverse: false, group: 'dino_rule',
    label: '允许人物/陆行生物移速加点', tip: '允许玩家和非飞行生物将属性点分配到移动速度。',
  },
  {
    key: 'bAllowFlyerSpeedLeveling', file: 'game', type: 'bool', default: false, inverse: false, group: 'dino_rule',
    label: '允许飞行生物移速加点', tip: '允许飞行生物将属性点分配到移动速度。需先开启「允许人物/陆行生物移速加点（bAllowSpeedLeveling）」。',
  },
  // —— 生物设置：繁殖与幼崽 ——
  {
    key: 'MatingIntervalMultiplier', file: 'game', type: 'float', default: 1.0, inverse: false, group: 'dino_breed',
    label: '交配冷却倍率', tip: '交配冷却时间的倍率，值越小冷却越短（繁殖越频繁），默认 1.0。',
  },
  {
    key: 'MatingSpeedMultiplier', file: 'game', type: 'float', default: 1.0, inverse: false, group: 'dino_breed',
    label: '交配速度倍率', tip: '完成一次交配过程所需时间的倍率，值越大交配越慢，默认 1.0。',
  },
  {
    key: 'LayEggIntervalMultiplier', file: 'game', type: 'float', default: 1.0, inverse: false, group: 'dino_breed',
    label: '下蛋间隔倍率', tip: '产蛋间隔时间的倍率，值越小产蛋越频繁，默认 1.0。',
  },
  {
    key: 'EggHatchSpeedMultiplier', file: 'game', type: 'float', default: 1.0, inverse: false, group: 'dino_breed',
    label: '受精蛋孵化速度倍率', tip: '受精蛋孵化时间的倍率，值越大孵化越快，默认 1.0。',
  },
  {
    key: 'BabyMatureSpeedMultiplier', file: 'game', type: 'float', default: 1.0, inverse: false, group: 'dino_breed',
    label: '幼崽成熟速度倍率', tip: '幼崽成长为成体的速度倍率，值越大成熟越快，默认 1.0。',
  },
  {
    key: 'BabyFoodConsumptionSpeedMultiplier', file: 'game', type: 'float', default: 1.0, inverse: false, group: 'dino_breed',
    label: '幼崽食物消耗倍率', tip: '幼崽食物消耗速度的倍率，值越大需要越频繁喂食，默认 1.0。',
  },
  {
    key: 'BabyCuddleIntervalMultiplier', file: 'game', type: 'float', default: 1.0, inverse: false, group: 'dino_breed',
    label: '幼崽照顾间隔倍率', tip: '幼崽需要照顾（获得印记）的时间间隔倍率，值越小需要越频繁照顾，默认 1.0。',
  },
  {
    key: 'BabyImprintAmountMultiplier', file: 'game', type: 'float', default: 1.0, inverse: false, group: 'dino_breed',
    label: '每次照顾印记量倍率', tip: '每次照顾幼崽后获得印记百分比的倍率，值越大每次增量越多，默认 1.0。',
  },
  {
    key: 'BabyImprintingStatScaleMultiplier', file: 'game', type: 'float', default: 1.0, inverse: false, group: 'dino_breed',
    label: '印记属性加成倍率', tip: '印记质量对生物属性加成的影响倍率，设为 0 可禁用印记属性加成，默认 1.0。',
  },
  {
    key: 'BabyCuddleGracePeriodMultiplier', file: 'game', type: 'float', default: 1.0, inverse: false, group: 'dino_breed',
    label: '照顾宽限期倍率', tip: '延迟照顾幼崽后印记质量开始下降的宽限期倍率，默认 1.0。',
  },
  {
    key: 'BabyCuddleLoseImprintQualitySpeedMultiplier', file: 'game', type: 'float', default: 1.0, inverse: false, group: 'dino_breed',
    label: '印记质量流失速度倍率', tip: '宽限期过后印记质量下降速度的倍率，默认 1.0。',
  },
  // —— 生物管理 ——
  {
    key: 'DestroyTamesOverTheSoftTameLimit', file: 'gus', type: 'bool', default: false, inverse: false, group: 'tame',
    label: '消灭软上限以上的驯服生物', tip: 'True 时超过服务器软上限的驯服生物将被销毁（需配合下方两项一起设置）。',
  },
  {
    key: 'MaxTamedDinos_SoftTameLimit', file: 'gus', type: 'int', default: 5000, inverse: false, group: 'tame',
    label: '服务器驯服生物软上限', tip: '服务器全局驯服生物软上限数量，超过后被标记待销毁，默认 5000。',
  },
  {
    key: 'MaxTamedDinos_SoftTameLimit_CountdownForDeletionDuration', file: 'gus', type: 'int', default: 604800, inverse: false, group: 'tame',
    label: '软上限销毁倒计时（秒）', tip: '超限生物被标记后的存活时间（秒），默认 604800（7 天）。',
  },
  {
    key: 'DestroyTamesOverLevelClamp', file: 'game', type: 'int', default: 0, inverse: false, group: 'tame',
    label: '销毁超等级驯养（0=禁用）', tip: '服务器启动时删除驯养等级超过此值的生物，0 表示不启用。官方服务器设为 450。',
  },
  // —— 环境 / 资源消耗 ——
  {
    key: 'OxygenSwimSpeedStatMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '氧气游泳速度统计倍率', tip: '氧气属性对游泳速度加成的倍率（v256.0 后游戏内默认已降低 80%）。',
  },
  {
    key: 'UseCorpseLifeSpanMultiplier', file: 'gus', type: 'float', default: 6.0, inverse: false, group: 'env',
    label: '尸体寿命倍率', tip: '玩家/生物尸体在地图上保留时间的倍率，默认 6。',
  },
  {
    key: 'GlobalCorpseDecompositionTimeMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'env',
    label: '尸体分解时间倍率', tip: '全局尸体/物品包分解时间倍率，默认 1.0。',
  },
  {
    key: 'GlobalPoweredBatteryDurabilityDecreasePerSecond', file: 'gus', type: 'float', default: 3.0, inverse: false, group: 'env',
    label: '蓄电池耐久每秒减少量', tip: '通电蓄电池每秒损耗的耐久值，默认 3。值越小电池越耐用。',
  },
  {
    key: 'FuelConsumptionIntervalMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'env',
    label: '燃料消耗间隔倍率', tip: '机器/设备燃料消耗间隔的倍率，越大消耗越慢，默认 1.0。',
  },
  {
    key: 'ClampItemStats', file: 'gus', type: 'bool', default: false, inverse: false, group: 'env',
    label: '限制物品属性上限', tip: 'True 时将物品属性值限制在游戏设定的最大值以内（防止超高品质物品）。',
  },
  // —— 离线突袭保护（ORP）——
  {
    key: 'PreventOfflinePvP', file: 'gus', type: 'bool', default: false, inverse: false, group: 'orp',
    label: '启用离线突袭保护（ORP）', tip: 'True 时全部部落成员下线后，角色/生物/建筑进入无敌状态（生物仍会饥饿/溺亡）。',
  },
  {
    key: 'PreventOfflinePvPInterval', file: 'gus', type: 'float', default: 0.0, inverse: false, group: 'orp',
    label: 'ORP 激活等待时间（秒）', tip: '部落全员下线后经过多少秒 ORP 才生效（官服 PvE 为 10 秒）。',
  },
  {
    key: 'PreventOfflinePvPConnectionInvincibleInterval', file: 'gus', type: 'float', default: 5.0, inverse: false, group: 'orp',
    label: 'ORP 上线无敌间隔（秒）', tip: '玩家重新上线后保持无敌状态的时间（秒），默认 5。',
  },
  // —— 部落设置 ——
  {
    key: 'MaxNumberOfPlayersInTribe', file: 'gus', type: 'int', default: 0, inverse: false, group: 'tribe',
    label: '部落最大玩家数（0=无限）', tip: '限制每个部落的成员上限，0 表示不限制。',
  },
  {
    key: 'TribeNameChangeCooldown', file: 'gus', type: 'float', default: 15.0, inverse: false, group: 'tribe',
    label: '部落改名冷却（分钟）', tip: '两次部落改名之间的冷却时间（分钟），官服设为 172800（2 天）。',
  },
  {
    key: 'TribeMergeAllowed', file: 'gus', type: 'bool', default: true, inverse: false, group: 'tribe',
    label: '允许部落合并', tip: 'False 时禁止部落相互合并。',
  },
  {
    key: 'TribeMergeCooldown', file: 'gus', type: 'float', default: 0.0, inverse: false, group: 'tribe',
    label: '部落合并冷却（秒）', tip: '两次部落合并之间的冷却时间（秒）。',
  },
  {
    key: 'TribeSlotReuseCooldown', file: 'gus', type: 'float', default: 0.0, inverse: false, group: 'tribe',
    label: '部落槽位重用冷却（秒）', tip: '部落槽位被释放后重新使用的冷却时间（秒）。',
  },
  {
    key: 'PreventTribeAlliances', file: 'gus', type: 'bool', default: false, inverse: false, group: 'tribe',
    label: '禁止部落联盟', tip: 'True 时禁止部落之间创建联盟。',
  },
  {
    key: 'MaxAlliancesPerTribe', file: 'gus', type: 'int', default: 0, inverse: false, group: 'tribe',
    label: '每部落最大联盟数（0=不限）', tip: '每个部落可加入的最大联盟数量，0 表示不限制。',
  },
  {
    key: 'MaxTribesPerAlliance', file: 'gus', type: 'int', default: 0, inverse: false, group: 'tribe',
    label: '每联盟最大部落数（0=不限）', tip: '一个联盟内允许的最大部落数量，0 表示不限制。',
  },
  {
    key: 'TribeLogDestroyedEnemyStructures', file: 'gus', type: 'bool', default: false, inverse: false, group: 'tribe',
    label: '部落日志记录销毁敌方建筑', tip: 'True 时在部落日志中记录摧毁敌方建筑的事件。',
  },
  {
    key: 'MaxTribeLogs', file: 'gus', type: 'int', default: 400, inverse: false, group: 'tribe',
    label: '部落日志最大条目数', tip: '部落日志保留的最大条目数量，默认 400。',
  },
  {
    key: 'AllowHideDamageSourceFromLogs', file: 'gus', type: 'bool', default: true, inverse: false, group: 'tribe',
    label: '允许隐藏日志伤害来源', tip: 'False 时部落日志强制显示伤害来源（谁打了谁）。',
  },
  {
    key: 'PreventOutOfTribePinCodeUse', file: 'gus', type: 'bool', default: false, inverse: false, group: 'tribe',
    label: '防止部落外使用 PIN 码', tip: 'True 时非部落成员无法使用 PIN 码开锁。',
  },
  {
    key: 'LimitBunkersPerTribe', file: 'gus', type: 'bool', default: true, inverse: false, group: 'tribe',
    label: '限制每部落地堡数量', tip: 'True 时每个部落的地堡数量受「每部落最大地堡数量」限制。',
  },
  {
    key: 'LimitBunkersPerTribeNum', file: 'gus', type: 'int', default: 3, inverse: false, group: 'tribe',
    label: '每部落最大地堡数量', tip: '每个部落允许拥有的最大地堡数量，默认 3。',
  },
  {
    key: 'TribeTowerBonusMultiplier', file: 'game', type: 'float', default: 2.0, inverse: false, group: 'tribe',
    label: '部落之塔奖励倍率', tip: '部落之塔（Tribe Tower）奖励的倍率，默认 2.0。',
  },
  // —— 难度设置 ——
  {
    key: 'DifficultyOffset', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'diff',
    label: '难度偏移', tip: '控制野生生物等级分布（0~1），与 OverrideOfficialDifficulty 同时使用时建议保持 1.0。',
  },
  {
    key: 'OverrideOfficialDifficulty', file: 'gus', type: 'float', default: 0.0, inverse: false, group: 'diff',
    label: '覆盖官方难度', tip: '设为 5.0 允许野生生物刷新至最高 150 级（0 = 不生效）。启用后将自动把 DifficultyOffset 设为 1.0。',
  },
  // —— 环境配置 ——
  {
    key: 'DayCycleSpeedScale', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '昼夜循环速度倍率', tip: '整体昼夜循环的时间流速倍率，默认 1.0。值越大昼夜交替越快。',
  },
  {
    key: 'DayTimeSpeedScale', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '白天速度倍率', tip: '白天时间流逝速度的倍率，默认 1.0。大于 1 则白天更短。',
  },
  {
    key: 'NightTimeSpeedScale', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '夜晚速度倍率', tip: '夜晚时间流逝速度的倍率，默认 1.0。大于 1 则夜晚更短。',
  },
  {
    key: 'HarvestAmountMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '采集产量倍率', tip: '采集资源的数量倍率，默认 1.0。值越大每次采集获得的资源越多。',
  },
  {
    key: 'HarvestHealthMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '可采集物体生命值倍率', tip: '可采集节点（树木、矿石等）的生命值倍率，值越大节点越耐砍，默认 1.0。',
  },
  {
    key: 'PlayerDamageMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '玩家攻击伤害倍率', tip: '玩家发动攻击时的伤害倍率，默认 1.0。',
  },
  {
    key: 'PlayerResistanceMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '玩家伤害抗性倍率', tip: '玩家承受伤害的抗性倍率，值越大抗性越低（受到伤害越多），默认 1.0。',
  },
  {
    key: 'PlayerCharacterHealthRecoveryMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '玩家生命恢复倍率', tip: '玩家生命值自然恢复速度的倍率，默认 1.0。',
  },
  {
    key: 'PlayerCharacterStaminaDrainMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '玩家体力消耗倍率', tip: '玩家体力消耗速度的倍率，默认 1.0。',
  },
  {
    key: 'PlayerCharacterWaterDrainMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '玩家水分消耗倍率', tip: '玩家水分消耗速度的倍率，默认 1.0。',
  },
  {
    key: 'PlayerCharacterFoodDrainMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '玩家食物消耗倍率', tip: '玩家食物消耗速度的倍率，默认 1.0。',
  },
  {
    key: 'ResourcesRespawnPeriodMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '资源重生周期倍率', tip: '资源节点重生时间的倍率，值越大重生越慢，默认 1.0。',
  },
  {
    key: 'StructureResistanceMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '建筑伤害抗性倍率', tip: '建筑物承受伤害的抗性倍率，值越大抗性越低，默认 1.0。',
  },
  {
    key: 'StructurePreventResourceRadiusMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '建筑禁资源区域半径倍率', tip: '建筑周围阻止资源重生区域范围的倍率，默认 1.0。',
  },
  {
    key: 'StructurePickupHoldDuration', file: 'gus', type: 'float', default: 0.5, inverse: false, group: 'world',
    label: '快速拾取按住时长（秒）', tip: '快速拾取建筑所需按住的时间（秒），默认 0.5。',
  },
  {
    key: 'StructurePickupTimeAfterPlacement', file: 'gus', type: 'float', default: 30.0, inverse: false, group: 'world',
    label: '放置后可拾取时间（秒）', tip: '建筑放置后允许快速拾取的时间窗口（秒），默认 30。开启「始终允许拾取」后此项失效。',
  },
  {
    key: 'TheMaxStructuresInRange', file: 'gus', type: 'int', default: 10500, inverse: false, group: 'world',
    label: '范围内最大建筑数量', tip: '特定半径内允许放置的最大建筑数量，默认 10500。',
  },
  {
    key: 'PerPlatformMaxStructuresMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '平台鞍建筑数量倍率', tip: '平台鞍/木筏上允许放置的最大建筑数量的倍率，默认 1.0。',
  },
  {
    key: 'PlatformSaddleBuildAreaBoundsMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'world',
    label: '平台鞍建造区域倍率', tip: '平台鞍可建造区域范围的倍率，默认 1.0。',
  },
  {
    key: 'AlwaysAllowStructurePickup', file: 'gus', type: 'bool', default: false, inverse: false, group: 'world',
    label: '始终允许快速拾取建筑', tip: 'True 时禁用快速拾取的时间限制，StructurePickupTimeAfterPlacement 参数将失效。',
  },
  {
    key: 'OnlyAutoDestroyCoreStructures', file: 'gus', type: 'bool', default: false, inverse: false, group: 'world',
    label: '仅自动销毁核心建筑', tip: 'True 时建筑自动销毁机制仅作用于核心建筑（地基等），不影响其他建筑。',
  },
  {
    key: 'OnlyDecayUnsnappedCoreStructures', file: 'gus', type: 'bool', default: false, inverse: false, group: 'world',
    label: '仅衰减未连接的核心建筑', tip: 'True 时建筑衰减机制仅作用于未与其他建筑连接的核心建筑。',
  },
  // —— 物品堆叠 / 腐烂（全局） ——
  {
    key: 'ItemStackSizeMultiplier', file: 'gus', type: 'float', default: 1.0, inverse: false, group: 'item_global',
    label: '物品堆叠大小倍率', tip: '全局物品堆叠上限倍率。值为 2.0 时所有可堆叠物品上限翻倍；对堆叠上限为 1 的物品无效。ConfigOverrideItemMaxQuantity 设置了 bIgnoreMultiplier=False 的条目也受此影响。',
  },
  {
    key: 'ClampItemSpoilingTimes', file: 'gus', type: 'bool', default: false, inverse: false, group: 'item_global',
    label: '限制物品腐烂时间', tip: '启用后所有物品的腐烂时间不超过该类物品的最大腐烂时间，防止堆叠增大导致腐烂时间无限延长。',
  },
]

export const SETTING_GROUPS = [
  { key: 'diff', label: '难度设置', panel: 'world' },
  { key: 'world', label: '环境配置', panel: 'world' },
  { key: 'pvp', label: 'PvP / PvE 规则', panel: 'basic' },
  { key: 'build', label: '建造限制', panel: 'basic' },
  { key: 'loot', label: '补给箱 / 钓鱼', panel: 'basic' },
  { key: 'cryo', label: '低温舱（Cryopod）', panel: 'basic' },
  { key: 'cross', label: '跨服传输（Cross-ARK）', panel: 'basic' },
  { key: 'tame', label: '生物管理', panel: 'basic' },
  { key: 'env', label: '环境（其他）', panel: 'basic' },
  { key: 'dino_num', label: '数量上限', panel: 'dino' },
  { key: 'dino_mult', label: '倍率设置', panel: 'dino' },
  { key: 'dino_breed', label: '繁殖与幼崽', panel: 'dino' },
  { key: 'dino_rule', label: '行为规则', panel: 'dino' },
  { key: 'orp', label: '离线突袭保护（ORP）', panel: 'tribe' },
  { key: 'tribe', label: '部落设置', panel: 'tribe' },
  { key: 'item_global', label: '全局设置', panel: 'maxqty' },
]

const BY_KEY = Object.fromEntries(SETTINGS_REGISTRY.map((r) => [r.key, r]))
export const gameKeys = new Set(SETTINGS_REGISTRY.filter((r) => r.file === 'game').map((r) => r.key))
export const gusKeys = new Set(SETTINGS_REGISTRY.filter((r) => r.file === 'gus').map((r) => r.key))

// —— 基础转换 ——
const toBool = (s) => /^true$/i.test(String(s).trim())
const toNum = (s) => {
  const n = Number(String(s).trim())
  return Number.isFinite(n) ? n : 0
}
const fmtBool = (b) => (b ? 'True' : 'False')
const fmtNum = (n) => (n === '' || n == null || !Number.isFinite(Number(n)) ? '0' : String(Number(n)))
const fmtInt = (n) => String(Math.round(Number(n) || 0))

/** INI 原生值（stored）→ UI 值（应用 inverse） */
function toUiValue(reg, raw) {
  if (reg.type === 'bool') {
    const stored = toBool(raw)
    return reg.inverse ? !stored : stored
  }
  if (reg.type === 'int') return Math.round(toNum(raw))
  return toNum(raw)
}

/** 该项的默认 UI 值 */
export function defaultUiValue(reg) {
  if (reg.type === 'bool') return reg.inverse ? !reg.default : reg.default
  return reg.default
}

/** UI 值 → INI 原生值字符串（应用 inverse） */
function storedFromUi(reg, ui) {
  if (reg.type === 'bool') {
    const stored = reg.inverse ? !ui : ui
    return fmtBool(stored)
  }
  if (reg.type === 'int') return fmtInt(ui)
  return fmtNum(ui)
}

/** 全部项取默认 UI 值 */
export function createEmptyUiModel() {
  const model = {}
  for (const reg of SETTINGS_REGISTRY) model[reg.key] = defaultUiValue(reg)
  return model
}

// ---------------------------------------------------------------------------
// 解析：在指定节内收集受管标量键
// ---------------------------------------------------------------------------

/**
 * @returns { values:{canonicalKey:rawString}, present:Set, meta:{lines,eol,absorbed,target} }
 */
export function parseScalarSection(text, sectionLower, keySet) {
  const src = text || ''
  const eol = src.includes('\r\n') ? '\r\n' : '\n'
  const lines = src.split(/\r?\n/)

  const lowerToCanonical = {}
  for (const k of keySet) lowerToCanonical[k.toLowerCase()] = k

  const values = {}
  const present = new Set()
  const absorbed = new Set()
  let currentSection = ''
  let target = null

  for (let i = 0; i < lines.length; i++) {
    const trimmed = lines[i].trim()
    const sec = trimmed.match(/^\[(.+)\]$/)
    if (sec) {
      if (target && target.endLine == null) target.endLine = i
      currentSection = sec[1].trim().toLowerCase()
      if (currentSection === sectionLower && !target) target = { headerIndex: i, endLine: null }
      continue
    }
    if (currentSection !== sectionLower) continue
    if (!trimmed || trimmed.startsWith(';') || trimmed.startsWith('#')) continue
    const eq = trimmed.indexOf('=')
    if (eq < 0) continue
    const canonical = lowerToCanonical[trimmed.slice(0, eq).trim().toLowerCase()]
    if (!canonical) continue
    values[canonical] = trimmed.slice(eq + 1).trim()
    present.add(canonical)
    absorbed.add(i)
  }

  if (target && target.endLine == null) target.endLine = lines.length
  return { values, present, meta: { lines, eol, absorbed, target } }
}

/** 解析两个文件，返回可编辑的 UI 模型（key→bool/number） */
export function parseSettings(gameText, gusText) {
  const model = createEmptyUiModel()
  const g = parseScalarSection(gameText, GAME_SECTION, gameKeys)
  const u = parseScalarSection(gusText, GUS_SECTION, gusKeys)
  for (const [k, v] of Object.entries(g.values)) model[k] = toUiValue(BY_KEY[k], v)
  for (const [k, v] of Object.entries(u.values)) model[k] = toUiValue(BY_KEY[k], v)
  return { model, gameMeta: g.meta, gusMeta: u.meta, presentGame: g.present, presentGus: u.present }
}

// ---------------------------------------------------------------------------
// 序列化 + 合并
// ---------------------------------------------------------------------------

/** 生成某文件受管键的 INI 行：仅写「非默认 或 原文件已存在」的键 */
export function serializeScalars(file, model, present) {
  const out = []
  for (const reg of SETTINGS_REGISTRY) {
    if (reg.file !== file) continue
    let ui = model[reg.key]
    if (reg.type !== 'bool' && (ui === '' || ui == null)) ui = defaultUiValue(reg)
    const isDefault = ui === defaultUiValue(reg)
    if (isDefault && !present.has(reg.key)) continue
    out.push(`${reg.key}=${storedFromUi(reg, ui)}`)
  }
  return out
}

/** 合并：删除被吸收的旧行，序列化块插回；其余逐字保留（同 arkGameIni 语义） */
export function mergeScalarSection(meta, serialized, sectionHeader) {
  const { lines, eol, absorbed, target } = meta

  if (absorbed.size > 0) {
    const first = Math.min(...absorbed)
    const out = []
    for (let i = 0; i < lines.length; i++) {
      if (absorbed.has(i)) {
        if (i === first) out.push(...serialized)
        continue
      }
      out.push(lines[i])
    }
    return out.join(eol)
  }

  if (serialized.length === 0) return lines.join(eol)

  if (target) {
    const out = []
    for (let i = 0; i < lines.length; i++) {
      out.push(lines[i])
      if (i === target.endLine - 1) out.push(...serialized)
    }
    return out.join(eol)
  }

  const base = lines.length === 1 && lines[0].trim() === '' ? [] : lines
  const out = [...base]
  if (out.length && out[out.length - 1].trim() !== '') out.push('')
  out.push(sectionHeader)
  out.push(...serialized)
  return out.join(eol)
}

/**
 * 高层封装：把 UI 模型应用回两个文件文本。
 * gameText 应为「数组合并后」的 Game.ini（标量键与数组键不相交，可顺序合并）。
 */
export function applySettings(gameText, gusText, model) {
  const g = parseScalarSection(gameText, GAME_SECTION, gameKeys)
  const gameIni = mergeScalarSection(g.meta, serializeScalars('game', model, g.present), GAME_SECTION_HEADER)
  const u = parseScalarSection(gusText, GUS_SECTION, gusKeys)
  const gameUserSettings = mergeScalarSection(u.meta, serializeScalars('gus', model, u.present), GUS_SECTION_HEADER)
  return { gameIni, gameUserSettings }
}
