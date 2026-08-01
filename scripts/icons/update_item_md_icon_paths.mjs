/**
 * 下载完成后执行：将 asa-itemsids.md 中的远程图标 URL
 * 替换为本地相对路径 ../icon/items/{localFile}.png
 *
 * 文件名规则（与 download_item_icons_wiki.js 保持一致）：
 *   - 始终使用 normalizeFilename(Name).png（特殊字符→_，合并连续_，去除头尾_）
 *
 * 用法：node scripts/update_item_md_icon_paths.mjs
 */

import fs from 'fs';
import path from 'path';
import {fileURLToPath} from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, '..', '..');
const mdPath = path.join(root, 'docs', 'asa-itemsids.md');
const iconDir = path.join(root, 'icon', 'items');

function normalizeFilename(raw) {
    return decodeURIComponent(raw)
        .replace(/[^a-zA-Z0-9]/g, '_')
        .replace(/_+/g, '_')
        .replace(/^_+|_+$/g, '');
}

const downloaded = new Set(fs.readdirSync(iconDir));
console.log(`图标目录已有 ${downloaded.size} 个文件`);
const lines = fs.readFileSync(mdPath, 'utf8').split('\n');
let colName = -1;
let colIcon = -1;
let replaced = 0;
let skipped = 0;
const result = lines.map(line => {
    if (!line.trim().startsWith('|')) {
        colName = -1;
        colIcon = -1;
        return line;
    }
    const cells = line.trim().replace(/^\||\|$/g, '').split('|');
    if (cells.map(c => c.trim()).includes('Name')) {
        colName = cells.findIndex(c => c.trim() === 'Name');
        colIcon = cells.findIndex(c => c.trim() === 'Icon');
        return line;
    }
    if (cells.every(c => !c.trim() || /^:?-+:?$/.test(c.trim()))) return line;
    if (colName < 0 || colIcon < 0) return line;
    const name = cells[colName]?.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1').replace(/`/g, '').trim();
    const baseName = normalizeFilename(name ?? '');
    if (!baseName) {
        skipped++;
        return line;
    }
    const localFile = `${baseName}.png`;
    if (!downloaded.has(localFile)) {
        skipped++;
        return line;
    }
    const iconCell = cells[colIcon]?.trim() ?? '';
    const altMatch = iconCell.match(/!\[([^\]]*)\]/);
    const alt = altMatch ? altMatch[1] : (name ?? baseName);
    cells[colIcon] = ` ![${alt}](../icon/items/${localFile}) `;
    replaced++;
    return `|${cells.join('|')}|`;
});
fs.writeFileSync(mdPath, result.join('\n'), 'utf8');
console.log(`替换完成：${replaced} 处已更新，${skipped} 处未下载保留原路径`);
