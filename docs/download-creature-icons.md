# 生物图标下载方案

## 目标

从 `asa-creatureids.md` 中提取所有生物图标 URL，下载 256px 版本到本地，并将 markdown 中的图标路径更新为本地路径。

## 数据分析

- 总图标引用数: 529
- 去重后唯一图标: 287（Alpha/Beta/Gamma/Tek/Aberrant 等变体共用基础生物图标）
- 256px URL 规则: `https://ark.wiki.gg/images/thumb/{name}.png/256px-{name}.png`

## 文件名规范化

Wiki URL 中的文件名可能包含特殊字符（如括号 `%28` `%29`），保存到本地时统一规范化：

1. URL decode：`%28` → `(`，`%29` → `)`
2. 非安全字符替换为 `_`（只保留字母、数字、`-`、`_`、`.`）
3. 合并连续下划线：`__` → `_`

示例：`Dinopithecus_King_%28Gamma%29.png` → `Dinopithecus_King_Gamma_.png`

`icon_download_server.mjs` 和 `update_md_icon_paths.mjs` 使用完全相同的 `normalizeFilename` 函数，确保文件名一致。

## 方案架构

```
scripts/
  icon_download_server.mjs   - 本地 HTTP 服务器（端口 19194）
  download_icons_wiki.js     - 注入 wiki 页面执行的下载脚本
  update_md_icon_paths.mjs   - 下载完成后将 md 中的远程 URL 替换为本地路径
```

## 执行步骤

### 步骤 1：异步启动本地服务器

服务器是长驻进程，**必须以异步方式启动**，否则调用方（MCP 工具）会一直阻塞等待其退出。

```powershell
# PowerShell（Windows）
Start-Process -NoNewWindow -FilePath node -ArgumentList "scripts/icon_download_server.mjs"
```

```bash
# Bash
node scripts/icon_download_server.mjs &
```

启动后等待约 1 秒确认端口就绪，再继续后续步骤。

服务器启动后自动解析 `asa-creatureids.md`，提取唯一图标列表。兼容两种格式：

- **远程 URL**（未替换）：`![alt](https://ark.wiki.gg/images/thumb/Name.png/30px-Name.png)`
- **本地路径**（已替换）：`![alt](../icon/creature/Name.png)`，按 256px 规则重新生成下载 URL

提供以下接口：
- `GET /icons` — 返回待下载列表（已存在文件自动跳过）
- `POST /save/:filename` — 接收图片二进制数据并写入 `icon/creature/`
- `GET /status` — 当前进度统计
- `POST /done` — 接收完成通知后 **5 秒自动退出**

### 步骤 2：打开 wiki 页面并通过人机验证

通过 MCP Chrome DevTools 打开 `https://ark.wiki.gg/wiki/Creature_IDs`，手动通过人机验证。

> wiki 页面需要人机验证，需人工操作一次。验证通过后后续请求不受限制。

### 步骤 3：注入下载脚本

通过 MCP `evaluate_script` 在 wiki 页面中注入 `scripts/download_icons_wiki.js`。

脚本执行流程：
1. `GET localhost:19194/icons` — 获取待下载列表
2. 在 wiki 页面 fetch 每张图片（同源，无 CORS 问题）
3. `POST localhost:19194/save/:filename` — 回传服务器写入磁盘
4. 并发 5 个，每批间隔 250ms，失败自动重试 3 次
5. 全部完成后向服务器 `POST /done`，服务器打印汇总日志后 **5 秒自动退出**

### 步骤 4：更新 markdown 路径

```bash
node scripts/update_md_icon_paths.mjs
```

执行内容：
1. 重命名 `icon/creature/` 中不符合规范的文件名（修复历史遗留）
2. 将远程 URL 替换为本地路径，文件名经过规范化处理
3. 修复 md 中已有的不规范本地路径

替换示例：
```
# 替换前
![Achatina](https://ark.wiki.gg/images/thumb/Achatina.png/30px-Achatina.png?7e6b96)

# 替换后
![Achatina](../icon/creature/Achatina.png)
```

未下载成功的条目保留原 URL 不动。

## 断点续传

重新执行步骤 1–3 即可。服务器通过 `/icons` 接口动态过滤已存在文件，只下载缺失部分；`update_md_icon_paths.mjs` 也会跳过已正确替换的条目。

## 文件结构

```
D:\golang\asa-server\
├── docs/
│   ├── asa-creatureids.md              # 生物 ID 文档（步骤 4 后更新为本地路径）
│   └── download-creature-icons.md      # 本文档
├── icon/
│   └── creature/                       # 下载的 256px 图标（文件名已规范化）
│       ├── Achatina.png
│       ├── Dinopithecus_King_Gamma_.png
│       └── ...
└── scripts/
    ├── icon_download_server.mjs        # 本地 HTTP 服务器
    ├── download_icons_wiki.js          # wiki 页面注入脚本
    └── update_md_icon_paths.mjs        # 更新 md 本地路径
```
