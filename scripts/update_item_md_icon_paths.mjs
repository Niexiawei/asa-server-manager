/**
 * 下载完成后执行：将 asa-itemsids.md 中的远程图标 URL
 * 替换为本地相对路径 ../icon/items/{localFile}.png
 *
 * 文件名规则（与 download_item_icons_wiki.js 保持一致）：
 *   - Class Name 有值：使用 ClassName.png
 *   - Class Name 为空或"-"：使用 normalizeFilename(Name).png
 *
 * 用法：node scripts/update_item_md_icon_paths.mjs
 */

import fs   from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root    = path.resolve(__dirname, '..');
const mdPath  = path.join(root, 'docs', 'asa-itemsids.md');
const iconDir = path.join(root, 'icon', 'items');

// ── 文件名规范化（与 download_item_icons_wiki.js 保持一致）──────────────────
function normalizeFilename(raw) {
  return decodeURIComponent(raw)
    .replace(/[^a-zA-Z0-9_.\-]/g, '_')
    .replace(/_+/g, '_');
}

// ── 读取已下载的图标文件名 ───────────────────────────────────────────────────
const downloaded = new Set(fs.readdirSync(iconDir));
console.log(`图标目录已有 ${downloaded.size} 个文件`);

// ── 逐行处理表格 ─────────────────────────────────────────────────────────────
const lines  = fs.readFileSync(mdPath, 'utf8').split('\n');
let colName  = -1;
let colClass = -1;
let colIcon  = -1;
let replaced = 0;
let skipped  = 0;

const result = lines.map(line => {
  if (!line.trim().startsWith('|')) {
    colName = -1; colClass = -1; colIcon = -1;
    return line;
  }

  const cells = line.trim().replace(/^\||\|$/g, '').split('|');

  // 检测表头行
  if (cells.map(c => c.trim()).includes('Class Name')) {
    colName  = cells.findIndex(c => c.trim() === 'Name');
    colClass = cells.findIndex(c => c.trim() === 'Class Name');
    colIcon  = cells.findIndex(c => c.trim() === 'Icon');
    return line;
  }

  // 跳过分隔行
  if (cells.every(c => !c.trim() || /^:?-+:?$/.test(c.trim()))) return line;

  if (colClass < 0 || colIcon < 0) return line;

  const iconCell  = cells[colIcon]?.trim() ?? '';
  if (!iconCell.includes('https://')) return line; // 只处理远程 URL

  const className = cells[colClass]?.replace(/`/g, '').trim();
  const name      = cells[colName]?.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1').replace(/`/g, '').trim();

  // className 有值用 className，否则用规范化的 name 兜底
  const baseName  = (className && className !== '-')
    ? className
    : normalizeFilename((name ?? '').replace(/ /g, '_'));

  if (!baseName) { skipped++; return line; }

  const localFile = `${baseName}.png`;
  if (!downloaded.has(localFile)) {
    skipped++;
    return line;
  }

  const altMatch = iconCell.match(/!\[([^\]]*)\]/);
  const alt      = altMatch ? altMatch[1] : (name ?? baseName);
  cells[colIcon] = ` ![${alt}](../icon/items/${localFile}) `;
  replaced++;
  return `|${cells.join('|')}|`;
});

fs.writeFileSync(mdPath, result.join('\n'), 'utf8');
console.log(`替换完成：${replaced} 处已更新，${skipped} 处未下载保留原 URL`);
