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

/** 通用提取：classCol 为存放 ClassName 的列名（物品=Class Name，生物=Entity ID） */
function extract(md, classCol, extraCols) {
  const out = []
  const seen = new Set()
  eachTableRow(md, (cells, colIdx) => {
    const ni = colIdx['名称']
    const ci = colIdx[classCol]
    if (ni == null || ci == null) return
    const name = cells[ni]
    const className = stripBackticks(cells[ci])
    if (!name || !className || className === '-') return
    if (seen.has(className)) return
    seen.add(className)
    const row = { name, className, category: colIdx['分类'] != null ? cells[colIdx['分类']] : '' }
    for (const [key, col] of Object.entries(extraCols || {})) {
      row[key] = colIdx[col] != null ? cells[colIdx[col]] : ''
    }
    out.push(row)
  })
  return out
}

function extractEngrams(md) {
  const out = []
  const seen = new Set()
  eachTableRow(md, (cells, colIdx) => {
    const ni = colIdx['Item']
    const ci = colIdx['Engram Class']
    const ii = colIdx['Index']
    if (ni == null || ci == null) return
    const name = cells[ni]
    const className = stripBackticks(cells[ci])
    if (!name || !className || className === '-') return
    if (seen.has(className)) return
    seen.add(className)
    out.push({ name, className, index: ii != null ? (parseInt(cells[ii], 10) || 0) : 0 })
  })
  return out
}

function main() {
  mkdirSync(outDir, { recursive: true })

  const itemsMd = readFileSync(resolve(docsDir, 'asa-item-ids.md'), 'utf8')
  const creaturesMd = readFileSync(resolve(docsDir, 'asa-creature-ids.md'), 'utf8')
  const engramsMd = readFileSync(resolve(docsDir, 'asa-engrams.md'), 'utf8')

  const items = extract(itemsMd, 'Class Name', { stack: '堆叠' })
  const creatures = extract(creaturesMd, 'Entity ID', { nameTag: 'Name Tag' })
  const engrams = extractEngrams(engramsMd)

  writeFileSync(resolve(outDir, 'ark-items.json'), JSON.stringify(items), 'utf8')
  writeFileSync(resolve(outDir, 'ark-creatures.json'), JSON.stringify(creatures), 'utf8')
  writeFileSync(resolve(outDir, 'ark-engrams.json'), JSON.stringify(engrams), 'utf8')

  console.log(`ark-items.json: ${items.length} 条`)
  console.log(`ark-creatures.json: ${creatures.length} 条`)
  console.log(`ark-engrams.json: ${engrams.length} 条`)
}

main()
