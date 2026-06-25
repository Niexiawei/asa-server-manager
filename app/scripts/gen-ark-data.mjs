// 从 docs/ 的 Markdown 表格生成前端可视化配置所需的数据集。
// 产物（提交入库，组件用动态 import 懒加载）：
//   app/src/data/ark-items.json     [{ name, className, category, stack }]
//   app/src/data/ark-creatures.json [{ name, className, category, nameTag }]
//   app/src/data/ark-engrams.json   [{ name, className, index }]
//
// 运行: cd app && npm run gen:data
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

const scriptDir = dirname(fileURLToPath(import.meta.url))
const docsDir = resolve(scriptDir, '../../docs')
const outDir = resolve(scriptDir, '../src/data')

/** 拆分一行 Markdown 表格为单元格数组（去掉首尾竖线） */
function splitRow(line) {
  const trimmed = line.trim().replace(/^\|/, '').replace(/\|$/, '')
  return trimmed.split('|').map((c) => c.trim())
}

const isSeparatorRow = (cells) =>
  cells.length > 0 && cells.every((c) => c === '' || /^:?-+:?$/.test(c))

const stripBackticks = (s) => (s || '').replace(/`/g, '').trim()
const stripMarkdownLink = (s) => (s || '').replace(/\[([^\]]+)\]\([^)]+\)/g, '$1').trim()

/**
 * 遍历所有 Markdown 表格的数据行，回调 (cells, colIdx)。
 * colIdx 为该表格自己的表头列名 -> 列下标映射，保证多表头互不干扰。
 */
function eachTableRow(md, cb) {
  const lines = md.split(/\r?\n/)
  let colIdx = null
  for (const line of lines) {
    if (!line.trim().startsWith('|')) {
      colIdx = null
      continue
    }
    const cells = splitRow(line)
    if (isSeparatorRow(cells)) continue
    if (!colIdx) {
      colIdx = {}
      cells.forEach((h, i) => {
        colIdx[h] = i
      })
      continue
    }
    cb(cells, colIdx)
  }
}

/** 通用提取：classCol 为 ClassName 列名，nameCol 为英文名列名，zhCol 为中文名列名（可选） */
function extract(md, classCol, extraCols, nameCol = '名称', zhCol = null) {
  const out = []
  const seen = new Set()
  eachTableRow(md, (cells, colIdx) => {
    const ni = colIdx[nameCol]
    const ci = colIdx[classCol]
    if (ni == null || ci == null) return
    const rawName = stripMarkdownLink(cells[ni])
    const className = stripBackticks(cells[ci])
    if (!rawName || !className || className === '-') return
    if (seen.has(className)) return
    seen.add(className)
    const zh = zhCol != null && colIdx[zhCol] != null ? cells[colIdx[zhCol]]?.trim() : ''
    const name = zh && zh !== '-' ? `${zh}(${rawName})` : rawName
    const row = { name, className, category: colIdx['分类'] != null ? cells[colIdx['分类']] : '' }
    for (const [key, col] of Object.entries(extraCols || {})) {
      row[key] = colIdx[col] != null ? cells[colIdx[col]] : ''
    }
    out.push(row)
  })
  return out
}

function extractEngrams(md) {
  // 新格式：单表 | Item | 名称（中文） | Engram Class | Index |
  const out = []
  const seen = new Set()
  eachTableRow(md, (cells, colIdx) => {
    const ei = colIdx['Item']
    const zi = colIdx['名称（中文）']
    const ci = colIdx['Engram Class']
    const ii = colIdx['Index']
    if (ei == null || ci == null) return
    const item = cells[ei]?.trim()
    const className = stripBackticks(cells[ci])
    if (!item || !className || className === '-') return
    if (seen.has(className)) return
    seen.add(className)
    const zh = zi != null ? cells[zi]?.trim() : ''
    const name = zh && zh !== '-' ? `${zh}(${item})` : item
    out.push({ name, className, index: ii != null ? (parseInt(cells[ii], 10) || 0) : 0 })
  })
  return out
}

function main() {
  mkdirSync(outDir, { recursive: true })

  const itemsMd = readFileSync(resolve(docsDir, 'asa-itemsids.md'), 'utf8')
  const creaturesMd = readFileSync(resolve(docsDir, 'asa-creatureids.md'), 'utf8')
  const engramsMd = readFileSync(resolve(docsDir, 'asa-engrams.md'), 'utf8')

  const items = extract(itemsMd, 'Class Name', { stack: 'Stack Size' }, 'Name', 'Chinese Name')
  const creatures = extract(creaturesMd, 'Entity ID', { nameTag: 'Name Tag' }, '名称', '名称（中文）')
  const engrams = extractEngrams(engramsMd)

  writeFileSync(resolve(outDir, 'ark-items.json'), JSON.stringify(items), 'utf8')
  writeFileSync(resolve(outDir, 'ark-creatures.json'), JSON.stringify(creatures), 'utf8')
  writeFileSync(resolve(outDir, 'ark-engrams.json'), JSON.stringify(engrams), 'utf8')

  console.log(`ark-items.json: ${items.length} 条`)
  console.log(`ark-creatures.json: ${creatures.length} 条`)
  console.log(`ark-engrams.json: ${engrams.length} 条`)
}

main()
