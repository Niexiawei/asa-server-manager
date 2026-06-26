/**
 * 物品图标下载服务器
 *
 * 职责：管理本地文件系统（查询已存在文件、接收写入）
 * 图标来源与分类解析由浏览器脚本 download_item_icons_wiki.js 负责
 *
 * 异步启动（必须后台运行）：
 *   PowerShell: Start-Process -NoNewWindow -FilePath node -ArgumentList "scripts/item_icon_download_server.mjs"
 *   Bash:       node scripts/item_icon_download_server.mjs &
 */

import http from 'http';
import fs   from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT     = path.resolve(__dirname, '..');
const ICON_DIR = path.join(ROOT, 'icon', 'items');
const PORT     = 19195;

if (!fs.existsSync(ICON_DIR)) fs.mkdirSync(ICON_DIR, { recursive: true });

const state = { done: 0, failed: 0 };

const server = http.createServer((req, res) => {
  res.setHeader('Access-Control-Allow-Origin', '*');
  res.setHeader('Access-Control-Allow-Methods', 'GET,POST,OPTIONS');
  res.setHeader('Access-Control-Allow-Headers', 'Content-Type,Content-Length');

  if (req.method === 'OPTIONS') { res.writeHead(204); res.end(); return; }

  // GET /existing → 返回已存在的文件名集合，供浏览器脚本过滤
  if (req.method === 'GET' && req.url === '/existing') {
    const files = fs.readdirSync(ICON_DIR);
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(files));
    return;
  }

  // GET /status → 进度统计
  if (req.method === 'GET' && req.url === '/status') {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      existing: fs.readdirSync(ICON_DIR).length,
      done    : state.done,
      failed  : state.failed,
    }));
    return;
  }

  // POST /save/:filename → 写入图片文件
  if (req.method === 'POST' && req.url.startsWith('/save/')) {
    const filename = decodeURIComponent(req.url.slice(6));
    if (!filename.endsWith('.png') || filename.includes('/') || filename.includes('..')) {
      res.writeHead(400); res.end('bad filename'); return;
    }
    const chunks = [];
    req.on('data', c => chunks.push(c));
    req.on('end', () => {
      try {
        fs.writeFileSync(path.join(ICON_DIR, filename), Buffer.concat(chunks));
        state.done++;
        res.writeHead(200); res.end('ok');
      } catch (e) {
        console.error(`[server] 写入失败: ${filename}`, e.message);
        res.writeHead(500); res.end(e.message);
      }
    });
    return;
  }

  // POST /done → 完成通知，5 秒后退出
  if (req.method === 'POST' && req.url === '/done') {
    const chunks = [];
    req.on('data', c => chunks.push(c));
    req.on('end', () => {
      const result = JSON.parse(Buffer.concat(chunks).toString());
      state.failed = result.failed ?? 0;
      console.log(`\n[server] ════ 全部完成 ════`);
      console.log(`[server] 成功: ${result.done}  失败: ${result.failed}  未匹配: ${result.noMatch ?? 0}`);
      console.log(`[server] icon/items/ 现有文件: ${fs.readdirSync(ICON_DIR).length}`);
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
  console.log(`[server] icon/items/ 现有文件: ${fs.readdirSync(ICON_DIR).length}`);
  console.log(`[server] 在 wiki Item_IDs 页面注入 scripts/download_item_icons_wiki.js`);
});

server.on('error', e => {
  if (e.code === 'EADDRINUSE') console.error(`[server] 端口 ${PORT} 已被占用`);
  else console.error('[server]', e.message);
  process.exit(1);
});
