/**
 * 生物图标下载服务器
 *
 * 用途：供 Claude MCP 自动调用，无需人工干预
 *
 * 流程：
 *   1. 解析 docs/asa-creatureids.md，提取 287 个唯一图标 256px URL
 *   2. 过滤已存在文件
 *   3. 启动 HTTP 服务器 (localhost:19194)
 *   4. Chrome 访问 http://localhost:19194，页面自动批量下载
 *   5. 每张图下载完毕后 POST 回服务器，服务器写入 icon/creature/
 *   6. 全部完成后服务器自动退出
 *
 * 手动启动：node scripts/icon_download_server.mjs
 */

import http from 'http';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, '..', '..');
const ICON_DIR = path.join(ROOT, 'icon', 'creature');
const MD_PATH  = path.join(ROOT, 'docs', 'asa-creatureids.md');
const PORT     = 19194;

// ── 文件名规范化：decode URL 编码后对 stem 部分规范化，保留扩展名 ────────────
function normalizeFilename(rawFilename) {
  const decoded = decodeURIComponent(rawFilename);
  const dotIdx  = decoded.lastIndexOf('.');
  const stem    = dotIdx >= 0 ? decoded.slice(0, dotIdx) : decoded;
  const ext     = dotIdx >= 0 ? decoded.slice(dotIdx)    : '';
  const normalizedStem = stem
    .replace(/[^a-zA-Z0-9]/g, '_') // 特殊字符（含 - .）全替换为 _
    .replace(/_+/g, '_')            // 合并连续下划线
    .replace(/^_+|_+$/g, '');       // 去除头尾下划线
  return normalizedStem + ext;
}

// ── 解析 Markdown ────────────────────────────────────────────────────────────
// 兼容两种格式：
//   远程 URL：![alt](https://ark.wiki.gg/images/thumb/Name.png/30px-Name.png?hash)
//   本地路径：![alt](../icon/creature/Name.png)   ← 已替换过的条目
function parseIcons(mdText) {
  const seen = new Set();
  const icons = [];
  let m;

  // 1. 解析还未替换的远程 URL
  const remoteRe = /!\[.*?\]\((https:\/\/ark\.wiki\.gg\/images\/thumb\/[^)]+\/30px-([^)?]+\.png))[^)]*\)/g;
  while ((m = remoteRe.exec(mdText)) !== null) {
    const rawFilename = m[2];
    const filename = normalizeFilename(rawFilename);
    if (!seen.has(filename)) {
      seen.add(filename);
      // 用原始编码名构造 256px URL，确保 wiki 路径有效
      const url256 = `https://ark.wiki.gg/images/thumb/${rawFilename}/256px-${rawFilename}`;
      icons.push({ filename, url256 });
    }
  }

  // 2. 解析已替换为本地路径的条目，按规则重新生成 256px URL
  const localRe = /!\[.*?\]\(\.\.\/icon\/creature\/([^)]+\.png)\)/g;
  while ((m = localRe.exec(mdText)) !== null) {
    const filename = m[1]; // 已规范化的本地文件名
    if (!seen.has(filename)) {
      seen.add(filename);
      const url256 = `https://ark.wiki.gg/images/thumb/${filename}/256px-${filename}`;
      icons.push({ filename, url256 });
    }
  }

  return icons;
}

// ── 初始化 ───────────────────────────────────────────────────────────────────
if (!fs.existsSync(ICON_DIR)) fs.mkdirSync(ICON_DIR, { recursive: true });

const mdText = fs.readFileSync(MD_PATH, 'utf8');
const allIcons = parseIcons(mdText);
console.log(`[server] 从 Markdown 解析到 ${allIcons.length} 个唯一图标`);

const state = { done: 0, failed: 0, saved: new Set() };

function getMissingIcons() {
  const existing = new Set(fs.readdirSync(ICON_DIR));
  return allIcons.filter(i => !existing.has(i.filename) && !state.saved.has(i.filename));
}

// ── 客户端 HTML/JS ────────────────────────────────────────────────────────────
function buildClientHTML(missing) {
  const iconsJson = JSON.stringify(missing);
  return `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="UTF-8">
<title>ASA 图标下载</title>
<style>
  body { font-family: monospace; background:#111; color:#0f0; padding:20px; }
  #log { white-space:pre-wrap; font-size:12px; }
  #bar { background:#333; height:16px; border-radius:4px; margin:8px 0; overflow:hidden; }
  #fill{ background:#0f0; height:100%; width:0; transition:width .3s; }
</style>
</head>
<body>
<h2>ASA 生物图标下载器</h2>
<div id="bar"><div id="fill"></div></div>
<div id="prog">准备中…</div>
<div id="log"></div>
<script>
const ICONS = ${iconsJson};
const CONCURRENCY = 5;
const DELAY_MS    = 250;
const BASE        = 'http://localhost:${PORT}';

const log   = s => { document.getElementById('log').textContent += s + '\\n'; };
const setBar = (n,t) => {
  document.getElementById('fill').style.width = (n/t*100)+'%';
  document.getElementById('prog').textContent = n+'/'+t+' ('+Math.round(n/t*100)+'%)';
};

(async () => {
  const total = ICONS.length;
  if (total === 0) { log('所有图标已存在，无需下载'); return; }
  log('待下载: ' + total + ' 个图标（并发 ' + CONCURRENCY + '）');

  let done = 0, failed = [];

  async function downloadOne({filename, url256}, attempt=1) {
    try {
      const r = await fetch(url256, {cache:'no-store'});
      if (!r.ok) throw new Error('HTTP '+r.status);
      const buf = await r.arrayBuffer();
      const sr = await fetch(BASE+'/save/'+encodeURIComponent(filename), {
        method:'POST', body:buf,
        headers:{'Content-Type':'image/png','Content-Length':buf.byteLength}
      });
      if (!sr.ok) throw new Error('save HTTP '+sr.status);
      done++;
      setBar(done, total);
      if (done % 20 === 0 || done === total) log('进度 '+done+'/'+total);
    } catch(e) {
      if (attempt < 3) {
        await new Promise(r=>setTimeout(r, 800*attempt));
        return downloadOne({filename, url256}, attempt+1);
      }
      failed.push({filename, error:e.message});
      log('✗ 失败: '+filename+' — '+e.message);
    }
  }

  for (let i = 0; i < ICONS.length; i += CONCURRENCY) {
    await Promise.all(ICONS.slice(i, i+CONCURRENCY).map(downloadOne));
    if (i+CONCURRENCY < ICONS.length) await new Promise(r=>setTimeout(r, DELAY_MS));
  }

  log('');
  log('════ 完成 ════');
  log('成功: '+done+'  失败: '+failed.length);

  await fetch(BASE+'/done', {
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body: JSON.stringify({done, failed})
  });
})();
</script>
</body>
</html>`;
}

// ── HTTP 服务器 ───────────────────────────────────────────────────────────────
const server = http.createServer((req, res) => {
  const cors = () => {
    res.setHeader('Access-Control-Allow-Origin', '*');
    res.setHeader('Access-Control-Allow-Methods', 'GET,POST,OPTIONS');
    res.setHeader('Access-Control-Allow-Headers', 'Content-Type,Content-Length');
  };

  cors();

  if (req.method === 'OPTIONS') { res.writeHead(204); res.end(); return; }

  // GET / → 下载页面
  if (req.method === 'GET' && req.url === '/') {
    const missing = getMissingIcons();
    console.log(`[server] GET / — 待下载 ${missing.length} 个`);
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
    res.end(buildClientHTML(missing));
    return;
  }

  // GET /status → 当前进度 JSON
  if (req.method === 'GET' && req.url === '/status') {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      total: allIcons.length,
      existing: fs.readdirSync(ICON_DIR).length,
      done: state.done,
      failed: state.failed,
    }));
    return;
  }

  // GET /icons → 返回待下载图标列表（已存在的自动跳过）
  if (req.method === 'GET' && req.url === '/icons') {
    const missing = getMissingIcons();
    console.log(`[server] GET /icons — 返回 ${missing.length} 个待下载图标`);
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(missing));
    return;
  }

  // POST /save/:filename → 写入图片文件
  if (req.method === 'POST' && req.url.startsWith('/save/')) {
    const filename = decodeURIComponent(req.url.slice(6));
    // 基本安全检查：只允许 .png 文件名，禁止路径穿越
    if (!filename.endsWith('.png') || filename.includes('/') || filename.includes('..')) {
      res.writeHead(400); res.end('bad filename'); return;
    }
    const chunks = [];
    req.on('data', c => chunks.push(c));
    req.on('end', () => {
      try {
        const buf = Buffer.concat(chunks);
        fs.writeFileSync(path.join(ICON_DIR, filename), buf);
        state.saved.add(filename);
        state.done++;
        res.writeHead(200); res.end('ok');
      } catch (e) {
        console.error(`[server] 写入失败: ${filename}`, e.message);
        res.writeHead(500); res.end(e.message);
      }
    });
    return;
  }

  // POST /done → 浏览器通知全部完成
  if (req.method === 'POST' && req.url === '/done') {
    const chunks = [];
    req.on('data', c => chunks.push(c));
    req.on('end', () => {
      const result = JSON.parse(Buffer.concat(chunks).toString());
      state.failed = result.failed?.length ?? 0;
      const total  = fs.readdirSync(ICON_DIR).length;
      console.log(`\n[server] ════ 全部完成 ════`);
      console.log(`[server] 成功写入: ${result.done}  失败: ${state.failed}`);
      console.log(`[server] icon/creature/ 现有文件: ${total}`);
      if (result.failed?.length) {
        console.log('[server] 失败列表:');
        result.failed.forEach(f => console.log(`  ${f.filename}: ${f.error}`));
      }
      res.writeHead(200); res.end('ok');
      console.log('[server] 5 秒后自动退出…');
      setTimeout(() => { server.close(); process.exit(0); }, 5000);
    });
    return;
  }

  res.writeHead(404); res.end('not found');
});

server.listen(PORT, '127.0.0.1', () => {
  console.log(`[server] 启动成功 → http://localhost:${PORT}`);
  console.log(`[server] 用 Chrome 访问该地址即可自动开始下载`);
  console.log(`[server] Ctrl+C 可随时停止`);
});

server.on('error', e => {
  if (e.code === 'EADDRINUSE') {
    console.error(`[server] 端口 ${PORT} 已被占用，请先关闭占用该端口的进程`);
  } else {
    console.error('[server] 启动错误:', e.message);
  }
  process.exit(1);
});
