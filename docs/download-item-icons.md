# 物品图标下载方案

## 目标

为 `asa-itemsids.md` 中新增或缺失的物品下载 256px 图标，保存到 `icon/items/`。
现有图标无需重新下载，方案只处理缺失部分，支持持续增量更新。

## 图标来源与原理

图标来自 wiki 各分类子页面的原始文本，通过 `?action=raw` 接口直接获取，无需渲染页面、无懒加载问题。

原始文本格式：
```
{{Id item|Absorbent Substrate|Resources|100|-|PrimalItemResource_SubstrateAbsorbent_C|...}}
```

字段顺序：`名称 | 分类 | 堆叠 | ItemID | ClassName | Blueprint`

图标 URL 由物品名称推导（空格替换为 `_`）：
```
https://ark.wiki.gg/images/thumb/Absorbent_Substrate.png/256px-Absorbent_Substrate.png
```

本地保存使用 **Class Name** 作为文件名：
```
icon/items/PrimalItemResource_SubstrateAbsorbent_C.png
```

**Class Name 为空时的兜底规则**：若某物品的 Class Name 为空或 `-`，则使用规范化的物品名称作为文件名（与生物图标方案一致）：空格替换为 `_`，非安全字符替换为 `_`，合并连续下划线。
```
"Some Item (Special)" → Some_Item_Special_.png
```

## 分类页面路径

脚本在注入时自动从 Item_IDs 主页面提取所有 `a[href*="/wiki/Item_IDs/"]` 链接，动态获取分类列表，无需手动维护。

以下为当前已验证的分类及其 wiki 子页面路径：

| md 分类标题 | wiki 路径 | 物品数 |
|------------|-----------|-------|
| Resources | `Item_IDs/Resources` | 83 |
| Tools | `Item_IDs/Tools` | 27 |
| Armor | `Item_IDs/Armor` | 77 |
| Saddles | `Item_IDs/Saddles` | 104 |
| Structures | `Item_IDs/Structures` | 331 |
| Vehicles | `Item_IDs/Vehicles` | 4 |
| Dye | `Item_IDs/Dye` | 26 |
| Consumables | `Item_IDs/Consumables` | 163 |
| Recipes | `Item_IDs/Recipes` | 24 |
| Eggs | `Item_IDs/Eggs` | 95 |
| Farming | `Item_IDs/Farming` | 6 |
| Seeds | `Item_IDs/Seeds` | 18 |
| Weapons and Attachments | `Item_IDs/Weapons` | 62 |
| Ammunition | `Item_IDs/Ammunition` | 29 |
| Skins | `Item_IDs/Skins` | 224 |
| Chibi Pets | `Item_IDs/Chibi_Pets` | 165 |
| Artifacts | `Item_IDs/Artifacts` | 68 |
| Trophies | `Item_IDs/Trophy` | 66 |
| Unobtainable and Event Items | `Item_IDs/Unobtainable` | 68 |

> 注意：部分 md 标题与 wiki 路径不同（如 Trophies → Trophy，Weapons and Attachments → Weapons）。

## 方案架构

```
scripts/
  item_icon_download_server.mjs   - 本地 HTTP 服务器（端口 19195）
  download_item_icons_wiki.js     - 注入 wiki 页面执行的下载脚本
  update_item_md_icon_paths.mjs   - 下载完成后将 md 中的远程 URL 替换为本地路径
```

## 执行步骤

### 步骤 1：异步启动本地服务器

服务器是长驻进程，**必须以异步方式启动**，否则调用方会一直阻塞等待其退出。

```powershell
# PowerShell（Windows）
Start-Process -NoNewWindow -FilePath node -ArgumentList "scripts/item_icon_download_server.mjs"
```

```bash
# Bash
node scripts/item_icon_download_server.mjs &
```

启动后等待约 1 秒确认端口就绪。

服务器接口：
- `GET /existing` — 返回已存在文件名列表，供浏览器脚本过滤
- `POST /save/:localFile` — 接收图片二进制数据并写入 `icon/items/`
- `GET /status` — 当前进度统计
- `POST /done` — 完成通知，**5 秒后自动退出**

### 步骤 2：打开 wiki 页面并通过人机验证

通过 MCP Chrome DevTools 打开 `https://ark.wiki.gg/wiki/Item_IDs`，手动通过人机验证。

> wiki 页面有人机验证，需人工操作一次。验证通过后 cookie 保持，后续分类请求不受限制。

### 步骤 3：注入下载脚本

通过 MCP `evaluate_script` 在 wiki 页面注入 `scripts/download_item_icons_wiki.js`。

脚本执行流程：
1. `GET localhost:19195/existing` — 获取已存在文件名集合
2. 从当前页面 DOM 中提取所有 `a[href*="/wiki/Item_IDs/"]` 链接，动态获取分类列表
3. 逐分类请求 `https://ark.wiki.gg/wiki/Item_IDs/{Category}?action=raw`，获取原始 wiki 文本
4. 解析 `{{Id item|Name|...|ClassName|...}}` 提取物品名称与 Class Name，按以下规则确定本地文件名：
   - Class Name 有值：使用 `{ClassName}.png`
   - Class Name 为空或 `-`：使用规范化的名称 `normalizeFilename(Name).png`（空格→`_`，特殊字符→`_`，合并连续`_`）
5. 过滤已存在文件，仅下载缺失图标
6. 批量下载 256px 图标（并发 5，每批间隔 250ms，失败自动重试 3 次）
7. 每张图下载后 `POST localhost:19195/save/{localFile}` 写入磁盘
8. 全部完成后通知服务器，服务器打印汇总日志后 **5 秒自动退出**

### 步骤 4：更新 markdown 路径

```bash
node scripts/update_item_md_icon_paths.mjs
```

扫描 `asa-itemsids.md` 中 Icon 列仍为远程 URL 的行，若对应 `{ClassName}.png` 已存在于 `icon/items/`，则替换为本地路径：

```
# 替换前
![Absorbent Substrate](https://ark.wiki.gg/images/thumb/Absorbent_Substrate.png/30px-...)

# 替换后
![Absorbent Substrate](../icon/items/PrimalItemResource_SubstrateAbsorbent_C.png)
```

文件名规则与步骤 3 完全一致：
- Class Name 有值：使用 `{ClassName}.png`
- Class Name 为空或 `-`：从同行 `Name` 列读取，经 `normalizeFilename` 处理后作为文件名

未下载成功的条目保留原 URL 不动。

## 断点续传 / 增量更新

直接重新执行步骤 1–3 即可：
- 服务器通过 `/existing` 接口每次动态扫描目录，已存在文件自动跳过
- `asa-itemsids.md` 新增物品后，重新执行即可下载新增图标

## 文件结构

```
D:\golang\asa-server\
├── docs/
│   ├── asa-itemsids.md                  # 物品 ID 文档
│   └── download-item-icons.md           # 本文档
├── icon/
│   └── items/                           # 下载的 256px 图标（以 ClassName 命名）
│       ├── PrimalItemResource_SubstrateAbsorbent_C.png
│       └── ...
└── scripts/
    ├── item_icon_download_server.mjs    # 本地 HTTP 服务器
    ├── download_item_icons_wiki.js      # wiki 页面注入脚本
    └── update_item_md_icon_paths.mjs    # 更新 md 本地路径
```
