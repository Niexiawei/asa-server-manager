/**
 * 物品图标下载注入脚本
 *
 * 运行环境：https://ark.wiki.gg/wiki/Item_IDs 页面（MCP evaluate_script 注入）
 * 依赖：item_icon_download_server.mjs 已在 localhost:19195 运行
 *
 * 原理：
 *   1. 从当前 Item_IDs 页面动态提取所有分类子页面链接
 *   2. 逐分类请求 ?action=raw 获取原始 wiki 文本
 *   3. 解析 {{Id item|Name|...|ClassName|...}} 条目
 *   4. className 为空时使用规范化的 name 作为文件名（同生物方案）
 *   无需懒加载、无需滚动页面
 */

(async () => {
  const SERVER      = 'http://localhost:19195';
  const CONCURRENCY = 5;
  const DELAY_MS    = 250;

  // 文件名规范化：特殊字符→_，合并连续_（className 为空时用 name 兜底）
  function normalizeFilename(raw) {
    return decodeURIComponent(raw)
      .replace(/[^a-zA-Z0-9_.\-]/g, '_')
      .replace(/_+/g, '_');
  }

  // ── 动态从当前页面提取分类列表 ──────────────────────────────────────────────
  const CATEGORIES = [...new Set(
    [...document.querySelectorAll('a[href*="/wiki/Item_IDs/"]')]
      .map(a => { const m = a.href.match(/\/wiki\/Item_IDs\/([^#?]+)/); return m ? decodeURIComponent(m[1]) : null; })
      .filter(Boolean)
  )];

  if (CATEGORIES.length === 0) {
    console.error('[asa-item] 未找到分类链接，请确认当前页面为 https://ark.wiki.gg/wiki/Item_IDs');
    return { ok: false };
  }
  console.log(`[asa-item] 发现 ${CATEGORIES.length} 个分类:`, CATEGORIES.join(', '));

  // ── 1. 获取已存在文件列表 ───────────────────────────────────────────────────
  let existing;
  try {
    const r = await fetch(`${SERVER}/existing`);
    if (!r.ok) throw new Error(`/existing HTTP ${r.status}`);
    existing = new Set(await r.json());
  } catch (e) {
    console.error(`[asa-item] 无法连接本地服务器: ${e.message}`);
    console.error(`[asa-item] 请确认已执行: node scripts/item_icon_download_server.mjs`);
    return { ok: false, error: e.message };
  }

  console.log(`[asa-item] 本地已有 ${existing.size} 个图标`);

  // ── 2. 逐分类获取原始 wiki 文本，解析物品条目 ─────────────────────────────
  const todo    = []; // { localFile, url256 }
  const noMatch = [];
  const ITEM_RE = /\{\{Id item\|([^|]+)\|[^|]+\|[^|]+\|[^|]+\|([^|]+)\|[^}]+\}\}/g;

  for (const category of CATEGORIES) {
    const url = `https://ark.wiki.gg/wiki/Item_IDs/${category}?action=raw`;
    let text;
    try {
      const r = await fetch(url);
      if (!r.ok) { console.warn(`[asa-item] 分类页面失败: ${category} (HTTP ${r.status})`); continue; }
      text = await r.text();
    } catch (e) {
      console.warn(`[asa-item] 分类请求异常: ${category} — ${e.message}`);
      continue;
    }

    let m;
    ITEM_RE.lastIndex = 0;
    while ((m = ITEM_RE.exec(text)) !== null) {
      const name      = m[1].trim();
      const className = m[2].trim();

      // className 为空或 "-" 时，用规范化的 name 作为文件名（同生物方案）
      const baseName  = (className && className !== '-')
        ? className
        : normalizeFilename(name.replace(/ /g, '_'));
      const localFile = `${baseName}.png`;
      const wikiName  = name.replace(/ /g, '_');
      const url256    = `https://ark.wiki.gg/images/thumb/${wikiName}.png/256px-${wikiName}.png`;

      if (existing.has(localFile)) continue; // 已存在，跳过
      todo.push({ localFile, url256, name });
    }

    console.log(`[asa-item] 分类 ${category} 解析完成`);
  }

  if (todo.length === 0) {
    console.log('[asa-item] 所有物品图标均已存在，无需下载');
    await fetch(`${SERVER}/done`, { method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ done: 0, failed: 0, noMatch: 0 }) });
    return { ok: true, done: 0, failed: 0 };
  }

  console.log(`[asa-item] 待下载 ${todo.length} 个图标（并发 ${CONCURRENCY}）`);

  // ── 3. 批量下载并回传服务器 ───────────────────────────────────────────────
  let done = 0;
  const failed = [];

  async function downloadOne({ localFile, url256, name }, attempt = 1) {
    try {
      const imgResp = await fetch(url256, { cache: 'no-store' });
      if (!imgResp.ok) throw new Error(`fetch HTTP ${imgResp.status}`);
      const buf = await imgResp.arrayBuffer();

      const saveResp = await fetch(`${SERVER}/save/${encodeURIComponent(localFile)}`, {
        method : 'POST',
        body   : buf,
        headers: { 'Content-Type': 'image/png' },
      });
      if (!saveResp.ok) throw new Error(`save HTTP ${saveResp.status}`);

      done++;
      if (done % 20 === 0 || done === todo.length) {
        console.log(`[asa-item] 进度 ${done}/${todo.length} (${Math.round(done / todo.length * 100)}%)`);
      }
    } catch (e) {
      if (attempt < 3) {
        await new Promise(r => setTimeout(r, 800 * attempt));
        return downloadOne({ localFile, url256, name }, attempt + 1);
      }
      failed.push({ localFile, error: e.message });
      console.warn(`[asa-item] ✗ 失败: ${localFile} — ${e.message}`);
    }
  }

  for (let i = 0; i < todo.length; i += CONCURRENCY) {
    await Promise.all(todo.slice(i, i + CONCURRENCY).map(item => downloadOne(item)));
    if (i + CONCURRENCY < todo.length) await new Promise(r => setTimeout(r, DELAY_MS));
  }

  // ── 4. 通知服务器完成 ────────────────────────────────────────────────────
  try {
    await fetch(`${SERVER}/done`, {
      method : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body   : JSON.stringify({ done, failed: failed.length, noMatch: noMatch.length }),
    });
  } catch (_) {}

  console.log(`[asa-item] ════ 完成 ════ 成功:${done} 失败:${failed.length}`);
  return { ok: true, done, failed };
})();
