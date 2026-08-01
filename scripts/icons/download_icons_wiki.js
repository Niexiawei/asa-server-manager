/**
 * 生物图标下载注入脚本
 *
 * 运行环境：ark.wiki.gg 页面（通过 MCP evaluate_script 注入）
 * 依赖：本地 icon_download_server.mjs 已在 localhost:19194 运行
 *
 * 工作流：
 *   1. GET localhost:19194/icons  → 获取待下载列表（服务器已过滤已存在文件）
 *   2. fetch 每张图片（同源 ark.wiki.gg，无 CORS 限制）
 *   3. POST localhost:19194/save/:filename → 服务器写入 icon/creature/
 *   4. POST localhost:19194/done → 通知服务器全部完成
 */

(async () => {
  const SERVER = 'http://localhost:19194';
  const CONCURRENCY = 5;   // 并发下载数
  const DELAY_MS    = 250; // 每批间隔（ms），避免 wiki 限流

  // ── 1. 获取待下载列表 ───────────────────────────────────────────────────
  let icons;
  try {
    const r = await fetch(`${SERVER}/icons`);
    if (!r.ok) throw new Error(`/icons HTTP ${r.status}`);
    icons = await r.json();
  } catch (e) {
    console.error(`[asa-icon] 无法连接本地服务器: ${e.message}`);
    console.error(`[asa-icon] 请确认已执行: node scripts/icon_download_server.mjs`);
    return { ok: false, error: e.message };
  }

  if (icons.length === 0) {
    console.log('[asa-icon] 所有图标均已存在，无需下载');
    return { ok: true, done: 0, failed: 0 };
  }

  console.log(`[asa-icon] 待下载 ${icons.length} 个图标（并发 ${CONCURRENCY}）`);

  // ── 2. 下载单张图片并回传服务器 ─────────────────────────────────────────
  const failed = [];
  let done = 0;

  async function downloadOne({ filename, url256 }, attempt = 1) {
    try {
      // 从 wiki 获取图片（同源，无 CORS 问题）
      const imgResp = await fetch(url256, { cache: 'no-store' });
      if (!imgResp.ok) throw new Error(`fetch HTTP ${imgResp.status}`);
      const buf = await imgResp.arrayBuffer();

      // POST 到本地服务器写入磁盘
      const saveResp = await fetch(`${SERVER}/save/${encodeURIComponent(filename)}`, {
        method: 'POST',
        body: buf,
        headers: { 'Content-Type': 'image/png' },
      });
      if (!saveResp.ok) throw new Error(`save HTTP ${saveResp.status}`);

      done++;
      if (done % 20 === 0 || done === icons.length) {
        console.log(`[asa-icon] 进度 ${done}/${icons.length} (${Math.round(done / icons.length * 100)}%)`);
      }
    } catch (e) {
      if (attempt < 3) {
        await new Promise(r => setTimeout(r, 800 * attempt));
        return downloadOne({ filename, url256 }, attempt + 1);
      }
      failed.push({ filename, error: e.message });
      console.warn(`[asa-icon] ✗ 失败(${attempt}次): ${filename} — ${e.message}`);
    }
  }

  // ── 3. 分批并发执行 ──────────────────────────────────────────────────────
  for (let i = 0; i < icons.length; i += CONCURRENCY) {
    await Promise.all(icons.slice(i, i + CONCURRENCY).map(item => downloadOne(item)));
    if (i + CONCURRENCY < icons.length) {
      await new Promise(r => setTimeout(r, DELAY_MS));
    }
  }

  // ── 4. 通知服务器完成 ────────────────────────────────────────────────────
  try {
    await fetch(`${SERVER}/done`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ done, failed }),
    });
  } catch (_) { /* 服务器可能已退出，忽略 */ }

  console.log(`[asa-icon] ════ 完成 ════ 成功:${done} 失败:${failed.length}`);
  if (failed.length) {
    console.warn('[asa-icon] 失败列表:', failed.map(f => f.filename).join(', '));
  }

  return { ok: true, done, failed };
})();
