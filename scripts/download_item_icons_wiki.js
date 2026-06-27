/**
 * 物品图标下载注入脚本
 *
 * 运行环境：https://ark.wiki.gg/wiki/Item_IDs 页面（MCP evaluate_script 注入）
 * 依赖：item_icon_download_server.mjs 已在 localhost:19195 运行
 *
 * 原理：
 *   1. 从当前 Item_IDs 页面动态提取所有分类子页面链接
 *   2. 逐分类请求 HTML 页面，解析表格中的真实图片 URL
 *   3. 将图片 URL 转换为 256px 版本
 *   4. 始终使用规范化的 Name 作为文件名（特殊字符→_，合并连续_，去除头尾_）
 */

(async () => {
  const SERVER      = 'http://localhost:19195';
  const CONCURRENCY = 5;
  const DELAY_MS    = 250;

  function normalizeFilename(raw) {
    return decodeURIComponent(raw)
      .replace(/[^a-zA-Z0-9]/g, '_')
      .replace(/_+/g, '_')
      .replace(/^_+|_+$/g, '');
  }

  function to256pxUrl(imgSrc) {
    if (!imgSrc || !imgSrc.includes('.png')) return null;
    // 去掉查询参数
    imgSrc = imgSrc.split('?')[0];
    if (imgSrc.includes('/thumb/')) {
      const parts = imgSrc.split('/');
      const filename = parts[parts.length - 1].replace(/^\d+px-/, '');
      return parts.slice(0, -1).join('/') + '/256px-' + filename;
    }
    const filename = imgSrc.split('/').pop();
    return imgSrc.replace('/' + filename, '/thumb/' + filename + '/256px-' + filename);
  }

  // ── 动态从当前页面提取分类列表 ──────────────────────────────────────────────
  const CATEGORIES = [...new Set(
    [...document.querySelectorAll('a[href*="/wiki/Item_IDs/"]')]
      .map(a => { const m = a.href.match(/\/wiki\/Item_IDs\/([^#?]+)/); return m ? decodeURIComponent(m[1]) : null; })
      .filter(Boolean)
  )];

  if (CATEGORIES.length === 0) {
    console.error('[asa-item] 未找到分类链接');
    return { ok: false };
  }
  console.log(`[asa-item] 发现 ${CATEGORIES.length} 个分类`);

  // ── 1. 获取已存在文件列表 ───────────────────────────────────────────────────
  let existing;
  try {
    const r = await fetch(`${SERVER}/existing`);
    existing = new Set(await r.json());
  } catch (e) {
    return { ok: false, error: e.message };
  }
  console.log(`[asa-item] 本地已有 ${existing.size} 个图标`);

  // ── 2. 逐分类获取HTML页面，解析表格 ─────────────────────────────────────
  const todo = [];
  const seen = new Set();

  for (const category of CATEGORIES) {
    const pageUrl = `https://ark.wiki.gg/wiki/Item_IDs/${category}`;
    let html;
    try {
      const r = await fetch(pageUrl);
      if (!r.ok) continue;
      html = await r.text();
    } catch (e) { continue; }

    const parser = new DOMParser();
    const doc = parser.parseFromString(html, 'text/html');
    const rows = doc.querySelectorAll('table.wikitable tbody tr');

    for (const row of rows) {
      const cells = row.querySelectorAll('td');
      if (cells.length < 2) continue;

      // 第一列包含 icon 和名称
      const iconCell = cells[0];
      const img = iconCell.querySelector('img');
      if (!img) continue;

      // 名称在第一列的文本中（去掉图片后的文本）
      const name = iconCell.textContent?.trim();
      if (!name) continue;

      const baseName = normalizeFilename(name);
      const localFile = `${baseName}.png`;

      if (existing.has(localFile) || seen.has(localFile)) continue;

      let imgSrc = img.src || img.getAttribute('data-src') || '';
      const url256 = to256pxUrl(imgSrc);
      if (!url256) continue;

      seen.add(localFile);
      todo.push({ localFile, url256, name });
    }
    console.log(`[asa-item] ${category} 完成，累计 ${todo.length}`);
  }

  console.log(`[asa-item] 共 ${todo.length} 个待下载`);
  if (todo.length === 0) {
    await fetch(`${SERVER}/done`, { method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ done: 0, failed: 0 }) });
    return { ok: true, done: 0, failed: 0 };
  }

  // ── 3. 批量下载 ───────────────────────────────────────────────────────
  let done = 0;
  const failed = [];

  async function downloadOne(item, attempt = 1) {
    try {
      const resp = await fetch(item.url256, { cache: 'no-store' });
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const buf = await resp.arrayBuffer();
      if (buf.byteLength < 100) throw new Error('too small');
      await fetch(`${SERVER}/save/${encodeURIComponent(item.localFile)}`, {
        method: 'POST', body: buf, headers: { 'Content-Type': 'image/png' }
      });
      done++;
      if (done % 50 === 0 || done === todo.length) console.log(`[asa-item] ${done}/${todo.length}`);
    } catch (e) {
      if (attempt < 3) {
        await new Promise(r => setTimeout(r, 800 * attempt));
        return downloadOne(item, attempt + 1);
      }
      failed.push({ localFile: item.localFile, error: e.message });
      console.warn(`[asa-item] ✗ ${item.localFile}`);
    }
  }

  for (let i = 0; i < todo.length; i += CONCURRENCY) {
    await Promise.all(todo.slice(i, i + CONCURRENCY).map(item => downloadOne(item)));
    if (i + CONCURRENCY < todo.length) await new Promise(r => setTimeout(r, DELAY_MS));
  }

  await fetch(`${SERVER}/done`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ done, failed: failed.length })
  });

  console.log(`[asa-item] ════ 完成 ════ 成功:${done} 失败:${failed.length}`);
  return { ok: true, done, failed: failed.length };
})();
