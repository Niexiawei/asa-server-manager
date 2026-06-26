/**
 * 下载完成后执行：将 asa-creatureids.md 中的远程图标 URL
 * 替换为本地相对路径 ../icon/creature/Filename.png
 *
 * 用法：node scripts/update_md_icon_paths.mjs
 */

import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, '..');
const mdPath = path.join(root, 'docs', 'asa-creatureids.md');
const iconDir = path.join(root, 'icon', 'creature');

// ── 文件名规范化（与 icon_download_server.mjs 保持一致）──────────────────────
function normalizeFilename(rawFilename) {
  const decoded = decodeURIComponent(rawFilename);
  return decoded
    .replace(/[^a-zA-Z0-9_.\-]/g, '_') // 特殊字符替换为 _
    .replace(/_+/g, '_');               // 合并连续下划线
}

// ── 步骤 1：重命名目录中不符合规范的文件 ────────────────────────────────────
let renamed = 0;
for (const name of fs.readdirSync(iconDir)) {
  const normalized = normalizeFilename(name);
  if (normalized !== name) {
    fs.renameSync(path.join(iconDir, name), path.join(iconDir, normalized));
    console.log(`重命名: ${name} → ${normalized}`);
    renamed++;
  }
}
if (renamed > 0) console.log(`重命名文件 ${renamed} 个`);

// ── 步骤 2：读取目录（重命名后）────────────────────────────────────────────
const downloaded = new Set(fs.readdirSync(iconDir));
console.log(`图标目录已有 ${downloaded.size} 个文件`);

let content = fs.readFileSync(mdPath, 'utf8');
let replaced = 0;
let skipped = 0;

// ── 步骤 3：将远程 URL 替换为本地路径 ───────────────────────────────────────
content = content.replace(
  /!\[([^\]]*)\]\(https:\/\/ark\.wiki\.gg\/images\/thumb\/[^)]+\/30px-([^)?]+\.png)[^)]*\)/g,
  (match, alt, rawFilename) => {
    const filename = normalizeFilename(rawFilename);
    if (downloaded.has(filename)) {
      replaced++;
      return `![${alt}](../icon/creature/${filename})`;
    } else {
      skipped++;
      return match;
    }
  }
);

// ── 步骤 4：修复已有的不规范本地路径 ────────────────────────────────────────
let fixed = 0;
content = content.replace(
  /\(\.\.\/icon\/creature\/([^)]+\.png)\)/g,
  (match, rawFilename) => {
    const filename = normalizeFilename(rawFilename);
    if (filename !== rawFilename) {
      fixed++;
      return `(../icon/creature/${filename})`;
    }
    return match;
  }
);

fs.writeFileSync(mdPath, content, 'utf8');
console.log(`替换完成：${replaced} 处已更新，${skipped} 处未下载保留原 URL`);
if (fixed > 0) console.log(`修复已有错误路径：${fixed} 处`);
