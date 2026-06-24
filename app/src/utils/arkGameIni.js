// ARK Game.ini 高级配置的解析 / 序列化 / 合并工具。
//
// 设计原则：只接管下列「受管键」，文件的其余内容（注释、未识别键、其它节）逐字保留。
// 受管键全部位于 [/script/shootergame.shootergamemode] 节。
//
// 用法：
//   const { model, meta } = parseGameIni(text)   // 解析现有文件，得到可编辑 model
//   ...编辑 model...
//   const newText = mergeGameIni(meta, model)     // 合并回整文件文本（保留未受管内容）

const TARGET_SECTION = '/script/shootergame.shootergamemode'

// 属性索引表（0~11），用于 Stats / PerLevelStatsMultiplier UI。
export const STAT_INDICES = [
    {index: 0, cn: '生命', en: 'Health'},
    {index: 1, cn: '耐力', en: 'Stamina'},
    {index: 2, cn: '眩晕', en: 'Torpidity'},
    {index: 3, cn: '氧气', en: 'Oxygen'},
    {index: 4, cn: '食物', en: 'Food'},
    {index: 5, cn: '水分', en: 'Water'},
    {index: 6, cn: '温度', en: 'Temperature'},
    {index: 7, cn: '负重', en: 'Weight'},
    {index: 8, cn: '近战伤害', en: 'Melee Damage'},
    {index: 9, cn: '移动速度', en: 'Movement Speed'},
    {index: 10, cn: '坚毅', en: 'Fortitude'},
    {index: 11, cn: '制作速度', en: 'Crafting Speed'},
]

// 属性倍率分组。prefix 为 INI 键前缀，type 决定 UI 输入框精度。
export const STAT_GROUPS = [
    {key: 'Player', label: '玩家', prefix: 'PerLevelStatsMultiplier_Player', type: 'float'},
    {key: 'DinoWild', label: '野生生物', prefix: 'PerLevelStatsMultiplier_DinoWild', type: 'float'},
    {key: 'DinoTamed', label: '驯养生物', prefix: 'PerLevelStatsMultiplier_DinoTamed', type: 'float'},
    {key: 'DinoTamed_Add', label: '驯服加成', prefix: 'PerLevelStatsMultiplier_DinoTamed_Add', type: 'float'},
    {
        key: 'DinoTamed_Affinity',
        label: '驯服加成 (繁殖)',
        prefix: 'PerLevelStatsMultiplier_DinoTamed_Affinity',
        type: 'float'
    },
    {key: 'MutagenBoost', label: '诱变剂（驯服）', prefix: 'MutagenLevelBoost', type: 'int'},
    {key: 'MutagenBoostBred', label: '诱变剂（繁殖）', prefix: 'MutagenLevelBoost_Bred', type: 'int'},
]

// 生物伤害/抗性/速度/耐力倍率（每行一个 (ClassName=,Multiplier=) 结构）。
export const CLASS_MULTIPLIER_KEYS = [
    {key: 'DinoClassDamageMultipliers', label: '伤害', tamed: false, group: 'damage'},
    {key: 'TamedDinoClassDamageMultipliers', label: '伤害', tamed: true, group: 'damage'},
    {key: 'DinoClassResistanceMultipliers', label: '抗性', tamed: false, group: 'resistance'},
    {key: 'TamedDinoClassResistanceMultipliers', label: '抗性', tamed: true, group: 'resistance'},
    {key: 'TamedDinoClassSpeedMultipliers', label: '速度', tamed: true, group: 'speed'},
    {key: 'TamedDinoClassStaminaMultipliers', label: '耐力', tamed: true, group: 'stamina'},
]

const CLASS_MULTIPLIER_KEY_SET = new Set(CLASS_MULTIPLIER_KEYS.map((k) => k.key))

export function createEmptyModel() {
    const classMultipliers = {}
    for (const {key} of CLASS_MULTIPLIER_KEYS) classMultipliers[key] = []
    const stats = {}
    for (const {key} of STAT_GROUPS) stats[key] = {}
    return {
        classMultipliers,
        engrams: [],
        autoUnlocks: [],
        craftingCosts: [],
        maxQuantity: [],
        levels: {player: [], dino: []},
        engramPoints: [],
        stats,
    }
}

// ---------------------------------------------------------------------------
// 低层字符串工具：括号配平、引号感知
// ---------------------------------------------------------------------------

/** 按 sep 拆分字符串，但忽略括号内与引号内的分隔符。 */
function splitTopLevel(str, sep) {
    const out = []
    let depth = 0
    let inQuote = false
    let cur = ''
    for (let i = 0; i < str.length; i++) {
        const ch = str[i]
        if (inQuote) {
            cur += ch
            if (ch === '"') inQuote = false
            continue
        }
        if (ch === '"') {
            inQuote = true
            cur += ch
            continue
        }
        if (ch === '(') depth++
        else if (ch === ')') depth--
        else if (ch === sep && depth === 0) {
            out.push(cur)
            cur = ''
            continue
        }
        cur += ch
    }
    out.push(cur)
    return out
}

/** 若整个字符串恰好被一对最外层括号包裹，则去掉这对括号。 */
function stripOuterParens(s) {
    const t = s.trim()
    if (!(t.startsWith('(') && t.endsWith(')'))) return t
    let depth = 0
    let inQuote = false
    for (let i = 0; i < t.length; i++) {
        const ch = t[i]
        if (inQuote) {
            if (ch === '"') inQuote = false
            continue
        }
        if (ch === '"') inQuote = true
        else if (ch === '(') depth++
        else if (ch === ')') {
            depth--
            if (depth === 0 && i !== t.length - 1) return t // 第一对括号提前闭合，说明不是整体包裹
        }
    }
    return t.slice(1, -1)
}

/** 解析 `Key=Value,Key=Value` 为对象（值保留原样，含引号/括号）。 */
function parseKVs(inner) {
    const obj = {}
    for (const part of splitTopLevel(inner, ',')) {
        const p = part.trim()
        if (!p) continue
        const eq = p.indexOf('=')
        if (eq < 0) continue
        obj[p.slice(0, eq).trim()] = p.slice(eq + 1).trim()
    }
    return obj
}

const unquote = (s) => (s == null ? '' : String(s).trim().replace(/^"(.*)"$/s, '$1'))
const toNum = (s) => {
    const n = Number(String(s).trim())
    return Number.isFinite(n) ? n : 0
}
const toBool = (s) => /^true$/i.test(String(s).trim())

// 数字格式化：整数去小数，其余保留原值。
const fmtNum = (n) => (n === '' || n == null || !Number.isFinite(Number(n)) ? '0' : String(Number(n)))
const fmtBool = (b) => (b ? 'True' : 'False')

// ---------------------------------------------------------------------------
// 单行解析器（按受管键分派）
// ---------------------------------------------------------------------------

function parseClassMultiplier(value) {
    const kv = parseKVs(stripOuterParens(value))
    return {className: unquote(kv.ClassName), multiplier: toNum(kv.Multiplier)}
}

function parseEngram(key, value) {
    const kv = parseKVs(stripOuterParens(value))
    const named = key === 'OverrideNamedEngramEntries'
    return {
        kind: named ? 'named' : 'index',
        engramClassName: named ? unquote(kv.EngramClassName) : '',
        engramIndex: named ? '' : kv.EngramIndex != null ? toNum(kv.EngramIndex) : '',
        engramHidden: toBool(kv.EngramHidden),
        engramPointsCost: kv.EngramPointsCost != null ? toNum(kv.EngramPointsCost) : '',
        engramLevelRequirement: kv.EngramLevelRequirement != null ? toNum(kv.EngramLevelRequirement) : '',
        removeEngramPreReq: toBool(kv.RemoveEngramPreReq),
    }
}

function parseAutoUnlock(value) {
    const kv = parseKVs(stripOuterParens(value))
    return {
        engramClassName: unquote(kv.EngramClassName),
        levelToAutoUnlock: kv.LevelToAutoUnlock != null ? toNum(kv.LevelToAutoUnlock) : 0,
    }
}

function parseCraftingCost(value) {
    const kv = parseKVs(stripOuterParens(value))
    const arrStr = stripOuterParens(kv.BaseCraftingResourceRequirements || '')
    const resources = arrStr
        ? splitTopLevel(arrStr, ',')
            .map((g) => g.trim())
            .filter(Boolean)
            .map((g) => {
                const r = parseKVs(stripOuterParens(g))
                return {
                    resourceItemTypeString: unquote(r.ResourceItemTypeString),
                    baseResourceRequirement: toNum(r.BaseResourceRequirement),
                    requireExactType: toBool(r.bCraftingRequireExactResourceType),
                }
            })
        : []
    return {itemClassString: unquote(kv.ItemClassString), resources}
}

function parseMaxQuantity(value) {
    const kv = parseKVs(stripOuterParens(value))
    const q = parseKVs(stripOuterParens(kv.Quantity || ''))
    return {
        itemClassString: unquote(kv.ItemClassString),
        maxItemQuantity: toNum(q.MaxItemQuantity),
        ignoreMultiplier: toBool(q.bIgnoreMultiplier),
    }
}

function parseLevelRamp(value) {
    const kv = parseKVs(stripOuterParens(value))
    const arr = []
    for (const [k, v] of Object.entries(kv)) {
        const m = k.match(/ExperiencePointsForLevel\[(\d+)\]/)
        if (m) arr[Number(m[1])] = toNum(v)
    }
    for (let i = 0; i < arr.length; i++) if (arr[i] == null) arr[i] = 0
    return arr
}

const STAT_PREFIX_MAP = Object.fromEntries(STAT_GROUPS.map((g) => [g.prefix, g.key]))

// ---------------------------------------------------------------------------
// 顶层解析
// ---------------------------------------------------------------------------

/**
 * 解析整文件文本。返回 { model, meta }：
 *   model: 可编辑的结构化数据
 *   meta: 合并所需的原始信息（原始行、行尾、被吸收行下标、目标节范围）
 */
export function parseGameIni(text) {
    const src = text || ''
    const eol = src.includes('\r\n') ? '\r\n' : '\n'
    const lines = src.split(/\r?\n/)

    const model = createEmptyModel()
    const absorbed = new Set()
    const levelRamps = [] // 顺序：第 0 个=玩家，第 1 个=驯养

    let currentSection = '' // 小写
    let target = null // { headerIndex, endLine }

    for (let i = 0; i < lines.length; i++) {
        const raw = lines[i]
        const trimmed = raw.trim()

        const sectionMatch = trimmed.match(/^\[(.+)\]$/)
        if (sectionMatch) {
            if (target && target.endLine == null) target.endLine = i
            currentSection = sectionMatch[1].trim().toLowerCase()
            if (currentSection === TARGET_SECTION && !target) target = {headerIndex: i, endLine: null}
            continue
        }

        if (currentSection !== TARGET_SECTION) continue
        if (!trimmed || trimmed.startsWith(';') || trimmed.startsWith('#')) continue

        const eq = trimmed.indexOf('=')
        if (eq < 0) continue
        const key = trimmed.slice(0, eq).trim()
        const value = trimmed.slice(eq + 1).trim()

        try {
            if (CLASS_MULTIPLIER_KEY_SET.has(key)) {
                model.classMultipliers[key].push(parseClassMultiplier(value))
                absorbed.add(i)
            } else if (key === 'OverrideEngramEntries' || key === 'OverrideNamedEngramEntries') {
                model.engrams.push(parseEngram(key, value))
                absorbed.add(i)
            } else if (key === 'EngramEntryAutoUnlocks') {
                model.autoUnlocks.push(parseAutoUnlock(value))
                absorbed.add(i)
            } else if (key === 'ConfigOverrideItemCraftingCosts') {
                model.craftingCosts.push(parseCraftingCost(value))
                absorbed.add(i)
            } else if (key === 'ConfigOverrideItemMaxQuantity') {
                model.maxQuantity.push(parseMaxQuantity(value))
                absorbed.add(i)
            } else if (key === 'LevelExperienceRampOverrides') {
                levelRamps.push(parseLevelRamp(value))
                absorbed.add(i)
            } else if (key === 'OverridePlayerLevelEngramPoints') {
                model.engramPoints.push(toNum(value))
                absorbed.add(i)
            } else {
                const bi = key.lastIndexOf('[')
                if (bi > 0 && key.endsWith(']')) {
                    const prefix = key.slice(0, bi)
                    const idx = Number(key.slice(bi + 1, -1))
                    const groupKey = STAT_PREFIX_MAP[prefix]
                    if (groupKey && model.stats[groupKey] && !Number.isNaN(idx)) {
                        model.stats[groupKey][idx] = toNum(value)
                        absorbed.add(i)
                    }
                }
            }
        } catch {
            // 解析失败：不吸收，原行保留（不纳入结构化）
        }
    }

    if (target && target.endLine == null) target.endLine = lines.length

    model.levels.player = levelRamps[0] || []
    model.levels.dino = levelRamps[1] || []

    return {model, meta: {lines, eol, absorbed, target}}
}

// ---------------------------------------------------------------------------
// 序列化
// ---------------------------------------------------------------------------

function serializeManaged(model) {
    const out = []

    for (const {key} of CLASS_MULTIPLIER_KEYS) {
        for (const row of model.classMultipliers[key] || []) {
            if (!row.className) continue
            out.push(`${key}=(ClassName="${row.className}",Multiplier=${fmtNum(row.multiplier)})`)
        }
    }

    for (const e of model.engrams || []) {
        const parts = []
        if (e.kind === 'named') {
            if (!e.engramClassName) continue
            parts.push(`EngramClassName="${e.engramClassName}"`)
        } else {
            if (e.engramIndex === '' || e.engramIndex == null) continue
            parts.push(`EngramIndex=${fmtNum(e.engramIndex)}`)
        }
        parts.push(`EngramHidden=${fmtBool(e.engramHidden)}`)
        if (e.engramPointsCost !== '' && e.engramPointsCost != null)
            parts.push(`EngramPointsCost=${fmtNum(e.engramPointsCost)}`)
        if (e.engramLevelRequirement !== '' && e.engramLevelRequirement != null)
            parts.push(`EngramLevelRequirement=${fmtNum(e.engramLevelRequirement)}`)
        parts.push(`RemoveEngramPreReq=${fmtBool(e.removeEngramPreReq)}`)
        const key = e.kind === 'named' ? 'OverrideNamedEngramEntries' : 'OverrideEngramEntries'
        out.push(`${key}=(${parts.join(',')})`)
    }

    for (const u of model.autoUnlocks || []) {
        if (!u.engramClassName) continue
        out.push(`EngramEntryAutoUnlocks=(EngramClassName="${u.engramClassName}",LevelToAutoUnlock=${fmtNum(u.levelToAutoUnlock)})`)
    }

    for (const c of model.craftingCosts || []) {
        if (!c.itemClassString) continue
        const res = (c.resources || [])
            .filter((r) => r.resourceItemTypeString)
            .map(
                (r) =>
                    `(ResourceItemTypeString="${r.resourceItemTypeString}",BaseResourceRequirement=${fmtNum(
                        r.baseResourceRequirement,
                    )},bCraftingRequireExactResourceType=${fmtBool(r.requireExactType)})`,
            )
        out.push(
            `ConfigOverrideItemCraftingCosts=(ItemClassString="${c.itemClassString}",BaseCraftingResourceRequirements=(${res.join(
                ',',
            )}))`,
        )
    }

    for (const q of model.maxQuantity || []) {
        if (!q.itemClassString) continue
        out.push(
            `ConfigOverrideItemMaxQuantity=(ItemClassString="${q.itemClassString}",Quantity=(MaxItemQuantity=${fmtNum(
                q.maxItemQuantity,
            )},bIgnoreMultiplier=${fmtBool(q.ignoreMultiplier)}))`,
        )
    }

    const rampLine = (arr) =>
        `LevelExperienceRampOverrides=(${arr
            .map((xp, i) => `ExperiencePointsForLevel[${i}]=${fmtNum(xp)}`)
            .join(',')})`
    // 顺序固定：玩家行在前，驯养行在后（ARK 按出现顺序区分）。
    if ((model.levels.player || []).length) out.push(rampLine(model.levels.player))
    if ((model.levels.dino || []).length) out.push(rampLine(model.levels.dino))

    for (const p of model.engramPoints || []) out.push(`OverridePlayerLevelEngramPoints=${fmtNum(p)}`)

    for (const {key, prefix} of STAT_GROUPS) {
        const group = model.stats[key] || {}
        for (let i = 0; i < STAT_INDICES.length; i++) {
            const v = group[i]
            if (v !== '' && v != null) out.push(`${prefix}[${i}]=${fmtNum(v)}`)
        }
    }

    return out
}

// ---------------------------------------------------------------------------
// 合并：把序列化后的受管行写回，原始未受管内容逐字保留
// ---------------------------------------------------------------------------

export function mergeGameIni(meta, model) {
    const {lines, eol, absorbed, target} = meta
    const serialized = serializeManaged(model)
    const out = []

    if (absorbed.size > 0) {
        const firstAbsorbed = Math.min(...absorbed)
        for (let i = 0; i < lines.length; i++) {
            if (absorbed.has(i)) {
                if (i === firstAbsorbed) out.push(...serialized) // 块放在原首个受管行处
                continue
            }
            out.push(lines[i])
        }
        return out.join(eol)
    }

    // 文件中没有现成的受管行
    if (serialized.length === 0) return lines.join(eol)

    if (target) {
        for (let i = 0; i < lines.length; i++) {
            out.push(lines[i])
            if (i === target.endLine - 1) out.push(...serialized)
        }
        return out.join(eol)
    }

    // 没有目标节：在文件末尾新建（空文件不保留占位空行）
    const base = lines.length === 1 && lines[0].trim() === '' ? [] : lines
    out.push(...base)
    if (out.length && out[out.length - 1].trim() !== '') out.push('')
    out.push(`[${TARGET_SECTION}]`)
    out.push(...serialized)
    return out.join(eol)
}
