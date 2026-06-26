# ARK 翻译插入工具 — 技术文档

## 概述

从官方翻译文件（JSONC 格式）提取中文名称，批量插入到 ARK wiki 采集的 Markdown 文档中。支持自定义补充翻译，可重复运行更新。

---

## 目录结构

```
asa-server/
├── tools/
│   └── ark_translate.py          # 翻译插入脚本
├── ASA-Translation/
│   ├── 000-OBT.jsonc             # OBT 版本翻译（~10,694 条）
│   ├── 001-Official.jsonc        # 官方正式版翻译（~11,729 条）
│   ├── 007-飞升附加.jsonc         # 飞升 DLC 追加翻译（~94 条）
│   └── custom.json               # 自定义补充翻译（用户维护）
└── docs/
    ├── asa-creatureids.md        # 生物 ID 表（529 条）
    ├── asa-itemsids.md           # 物品 ID 表（1630 条）
    └── asa-engrams.md            # 印痕 Class 索引（731 条）
```

---

## 翻译文件格式

### JSONC 文件（`000-OBT.jsonc` / `001-Official.jsonc` / `007-飞升附加.jsonc`）

官方翻译文件，JSONC 格式（支持 `//` 行注释和 `/* */` 块注释）。每条翻译条目结构：

```jsonc
{
  "NamespaceHash": "Content::3329776360",
  "Source": "Rex",           // 英文原文
  "Trans": {
    "SC": "霸王龙",           // 简体中文
    "JA": "ティラノサウルス"
  },
  "Config": {
    "Item": false,
    "Dino": true,            // 是否为生物
    "Default_Hash": ""
  }
}
```

### custom.json（用户维护）

纯 JSON，平铺的 `英文名 → 中文名` 映射：

```json
{
  "Achatina": "蜗牛",
  "GPS": "全球定位系统",
  "Arctic Scout Mask": "北极侦察面具"
}
```

- 值为空字符串 `""` 表示待翻译，脚本会跳过空值
- 优先级最高，覆盖所有 JSONC 文件中的同名条目

---

## 加载优先级

```
007-飞升附加.jsonc  →  000-OBT.jsonc  →  001-Official.jsonc  →  custom.json
        低                                        高                  最高
```

后加载的文件覆盖前者，`custom.json` 始终最优先。

---

## 脚本实现

### 核心流程

```
1. parse_jsonc()     解析 JSONC 文件（字符级状态机，正确处理字符串内的 // 和 /* */）
2. _extract()        递归遍历解析结果，提取 Source → Trans.SC 映射
3. build_translation_map()  合并四个文件，构建 exact_map + lower_map（大小写不敏感回退）
4. process_markdown()       逐行扫描 Markdown 表格，定位列索引，填入翻译
```

### JSONC 解析

官方文件存在字符串内含 `//` 的情况，不能用简单的正则替换，使用字符级状态机：

```python
# 状态机核心逻辑（简化）
while i < n:
    if in_str:
        if c == '\\': skip next char   # 转义字符
        elif c == '"': in_str = False  # 字符串结束
    else:
        if c == '"':   in_str = True   # 字符串开始
        elif c == '/' and next == '/': skip to \n    # 行注释
        elif c == '/' and next == '*': skip to */    # 块注释
        else: emit c
```

解析后还需移除 JSON 尾逗号（`},` / `],`），再交给 `json.loads()`。

### Markdown 处理

- 通过列标题定位源列（英文名）和目标列（中文名）的索引
- 若目标列不存在则自动插入（紧跟源列之后）
- 英文名提取：`[Name](url)` → `Name`，图片列 `![](url)` 跳过
- 已有内容默认跳过（`--overwrite` 强制覆盖）

---

## 使用方法

### 基本用法

```bash
# 填充生物中文名
python tools/ark_translate.py docs/asa-creatureids.md

# 填充物品中文名（列名不同）
python tools/ark_translate.py docs/asa-itemsids.md --source-col Name --target-col "Chinese Name"

# 填充印痕中文名
python tools/ark_translate.py docs/asa-engrams.md --source-col Item --target-col "名称（中文）"
```

### 常用参数

| 参数 | 说明 |
|------|------|
| `--source-col` | 英文名列标题（默认：`名称`） |
| `--target-col` | 中文名列标题（默认：`名称（中文）`） |
| `--overwrite` | 覆盖已有中文名（更新翻译后使用） |
| `--dry-run` | 仅统计命中数，不修改文件 |
| `--show-missed` | 输出所有未匹配的英文名 |

### 更新 custom.json 后刷新所有文件

```bash
python tools/ark_translate.py docs/asa-creatureids.md --overwrite
python tools/ark_translate.py docs/asa-itemsids.md --source-col Name --target-col "Chinese Name" --overwrite
python tools/ark_translate.py docs/asa-engrams.md --source-col Item --target-col "名称（中文）" --overwrite
```

---

## 翻译覆盖率现状

| 文件 | 命中 | 总数 | 覆盖率 | 未翻译原因 |
|------|------|------|--------|-----------|
| `asa-creatureids.md` | 448 | 529 | 84.7% | 新 DLC 生物、事件生物变体 |
| `asa-itemsids.md` | 1312 | 1630 | 80.5% | 节日皮肤、活动道具 |
| `asa-engrams.md` | 722 | 731 | 98.8% | 少量 DLC 专属配方 |

未翻译条目已收集至 `ASA-Translation/custom.json`（共 399 条，值为空字符串待填写）。

---

## 维护流程

```
1. 编辑 custom.json，填写空值条目的中文名
2. 运行脚本（--overwrite）刷新对应 md 文件
3. 如需收集新的未翻译条目，重新运行收集脚本（见下方）
```

### 重新收集未翻译条目

```javascript
// 收集逻辑（Node.js）：扫描三个 md 文件中中文列为空的行，
// 去重后追加到 custom.json（不覆盖已有条目）
// 参见本次实现对话记录
```

---

## 数据来源

| 文档 | 采集来源 | 采集方式 |
|------|---------|---------|
| `asa-creatureids.md` | https://ark.wiki.gg/wiki/Creature_IDs | Chrome DevTools 浏览器注入 JS 提取 |
| `asa-itemsids.md` | https://ark.wiki.gg/wiki/Item_IDs | 同上 |
| `asa-engrams.md` | https://ark.wiki.gg/wiki/Engram_class_names | 同上 |

Wiki 页面返回 HTTP 403 拒绝直接 fetch，通过已加载的浏览器标签页用 `evaluate_script` 注入脚本提取 DOM 数据。
