# 目录规范化迁移方案：领域包收敛到 `internal/`

> 目标：把根目录 26 个 Go 包目录收进 `internal/`，让「哪些目录是 Go 包、哪些是脚本/文档/资源」**一眼可辨**。
> 前置阅读：`docs/PACKAGE_RESTRUCTURE_PLAN.md`（上一轮神包拆分，本文不改变那次拆分的分层结论）。
> 本文只改**位置与导入路径**，不改任何包的职责、依赖方向和分层。

---

## 1. 背景与问题

当前根目录（`ls` 结果）：

```
actions/ app/ appconfig/ auth/ backup/ batchmanage/ certmgr/ config/ countdown/
frpmanage/ gui/ icon/ installer/ instance/ logger/ mirror/ parseserver/ pkg/
process/ rconx/ realtime/ schedule/ state/ syncthingmanage/ updatemanage/
webapi/ winservice/                          ← 26 个 Go 包目录
ASA-Translation/ docs/ scripts/ tools/       ← 非 Go：游戏数据、文档、Node 脚本、Python 脚本
database_file/ asa-server.exe openapi.json   ← 运行时/构建产物（已 gitignore 或应 gitignore）
main.go go.mod go.sum README.md ...
```

三个具体问题：

1. **无法一眼区分**。`tools/` 里是一个 Python 脚本，但在 Go 仓库里 `tools/` 强烈暗示 Go 工具链（`tools.go` 惯例）；`icon/` 看着像资源目录，其实是含 `iconembed.go` 的 Go 包；`app/` 看着像 Go 应用包，其实是整个 Vue 前端。**目录名本身不携带「是不是 Go 包」的信息。**
2. **没有封装边界**。所有包对外可见。仓库一旦公开发布，`asa-server/auth`、`asa-server/state` 这类内部包会变成事实上的公共 API，重构就要考虑兼容性。
3. **根目录噪音**。26 个目录平铺，新人打开仓库第一眼看不到入口结构。

`internal/` 恰好同时解决 1 和 2：Go 工具链强制 `internal/` 只能被其父目录为根的代码导入，且规范里它就是「本模块私有代码」的公认位置。

### 为什么现在做代价最低

| 因素 | 现状 | 影响 |
|---|---|---|
| module 路径 | `asa-server`（无域名） | 本就不可能被外部 `go get`，迁移不会破坏任何下游 |
| 二进制数量 | 1 个（根 `main.go`） | 不需要引入 `cmd/`，改动面更小 |
| CI | 无 `.github/`、无 Makefile | 没有流水线路径要改 |
| 跨包引用 | 全部形如 `"asa-server/xxx"` | 一条 `sed` 可完成全部改写 |
| 测试 | 31 个 `_test.go`，全部包内测试 | 随包移动，无外部测试路径依赖 |

---

## 2. 目标目录结构

```
asa-server/
├── main.go                     # 唯一的根级 .go 文件（单二进制，不建 cmd/，见 §3.1）
├── go.mod / go.sum
│
├── internal/                   # ← 所有 Go 代码都在这里（除 main.go 与两个 embed shim）
│   │
│   ├── pkg/                    # 【仅基础设施】叶子工具，零领域依赖，不认识"实例/存档/RCON"
│   │   ├── console/            # ANSI/控制台输出清洗
│   │   ├── fsutil/             # FileExists / CopyDir / CopyFile / FileMD5
│   │   ├── iox/                # I/O 辅助
│   │   ├── netutil/            # DNS 解析
│   │   ├── processjob/         # Windows Job Object 进程树
│   │   ├── serverinfo/         # CPU/内存/进程指标（gopsutil）
│   │   ├── tail/               # 日志 tail（fsnotify）
│   │   └── winproc/            # 窗口/进程/端口 API + WMI
│   │
│   ├── config/                 # 目录布局 + InstanceConfig + INI 读写        (cfgpkg)
│   ├── certmgr/                # 本地 CA + 叶子证书 + Windows 根存储
│   ├── process/                # PID 文件 + IsServerRunning                (procpkg)
│   ├── rconx/                  # RCON 连接与命令执行
│   ├── realtime/               # WS 推送 + 交互式 RCON
│   ├── state/                  # BadgerDB 状态持久化                       (statepkg)
│   ├── installer/              # SteamCMD 下载 / ARK 服务器更新
│   ├── mirror/                 # 实例镜像 / NTFS junction
│   ├── instance/               # 生命周期 Start/Stop/Restart              (instancepkg)
│   ├── countdown/              # 延迟停止/重启编排
│   ├── batchmanage/            # 批量操作
│   ├── schedule/               # 定时任务
│   ├── updatemanage/           # 更新管理
│   ├── appconfig/              # 应用配置（viper + config.yaml）
│   ├── auth/                   # 鉴权：用户/会话/TOTP/WebAuthn/限流/审计
│   ├── webapi/                 # HTTP API
│   │   └── apiresp/ authapi/ backupapi/ configapi/ iconapi/
│   │       instanceapi/ logapi/ saveapi/ scheduleapi/ serverapi/
│   ├── actions/                # CLI 命令处理
│   ├── backup/                 # tar+zstd 备份/恢复
│   ├── frpmanage/              # FRP 反代（含 frpc.exe）
│   ├── syncthingmanage/        # Syncthing 同步（含 syncthing.exe）
│   ├── parseserver/            # ARK 存档解析
│   ├── gui/                    # Fyne 桌面 GUI（含 ASA_Logo_transparent.webp）
│   ├── logger/                 # Zap + lumberjack
│   └── winservice/             # Windows 服务集成
│
├── app/                        # 前端工程，【本次不动】：例外之一，见 §3.2
│   ├── appembed.go             # //go:embed dist —— 10 行 shim，原样保留
│   ├── src/ public/ dist/ scripts/ package.json vite.config.js
│
├── icon/                       # 静态图标资源，【本次不动】：例外之二，见 §3.3
│   ├── iconembed.go            # //go:embed creature items —— 6 行 shim，原样保留
│   ├── creature/ items/        # 1923 个 png（79MB），docs/*.md 以 ../icon/ 相对链接引用
│
├── docs/                       # Markdown 文档
├── scripts/                    # 非 Go 脚本（Node + Python，原 scripts/ + tools/ 合并）
│   ├── icons/                  # 原 scripts/*.mjs、*.js（图标抓取）
│   └── translate/              # 原 tools/ark_translate.py
├── data/
│   └── ark-translation/        # 原 ASA-Translation/（游戏文本数据，7.1MB）
└── （运行时/产物，gitignore）asa-server.exe  database_file/  bin/
```

### `internal/pkg/` 的准入标准（只放基础设施）

领域包**平铺在 `internal/` 下**，不进 `pkg/`。`internal/pkg/` 只收「基础能力设施」，判定标准三条全中才算：

1. **不认识本项目的领域概念**——包里搜不到 instance / 存档 / RCON / 实例状态 这类词；换个 ARK 之外的项目照样能用。
2. **零领域依赖**——只 import 标准库和第三方库，不 import 任何 `asa-server/internal/<领域包>`。这是最硬的一条，可以用 `go list -deps` 机器校验。
3. **无全局状态、无生命周期**——不持有 DB 连接、不起后台 goroutine、不做进程编排。

按此标准，现有 8 个 `pkg/` 子包全部合格；而 `config`（认识 InstanceConfig）、`process`（认识实例 PID 文件）、`certmgr`（依赖 `config` 的目录布局）虽然听着"底层"，都**不满足第 1/2 条**，所以留在 `internal/` 平铺层。

校验脚本（可选，防止后续有人往 `pkg/` 里塞领域代码）：

```bash
go list -deps ./internal/pkg/... | grep '^asa-server/internal/' | grep -v '^asa-server/internal/pkg/' \
  && echo "❌ pkg/ 混入了领域依赖" || echo "✅ pkg/ 保持纯净"
```

### 迁移后的目录判定规则（本文的核心产出）

**一句话规则：`internal/` 和 `main.go` 之外没有业务 Go 代码；`app/` 与 `icon/` 各留一个 embed shim，是仅有的两个例外，且都由 `//go:embed` 的硬约束决定（见 §3.2 / §3.3）。**

| 目录 | 是 Go 包？ | 内容 | 判定依据 |
|---|---|---|---|
| `internal/**` | ✅ 全部是 | 领域包、工具包、HTTP API | 规范目录名，Go 工具链强制私有 |
| `main.go` | ✅ | 程序入口 | 根级唯一 `.go` |
| `app/` | ⚠️ 例外：仅 `appembed.go` | Vue 前端工程 | `//go:embed` 不能跨越 `..`，shim 必须与 `dist/` 同目录（§3.2） |
| `icon/` | ⚠️ 例外：仅 `iconembed.go` | 79MB 静态图标资源 | 同上，shim 必须与 `creature/` `items/` 同目录（§3.3） |
| `docs/` | ❌ | Markdown | — |
| `scripts/` | ❌ | `.mjs` / `.js` / `.py` | 目录内无 `.go`，可用 CI 断言 |
| `data/` | ❌ | `.jsonc` 游戏文本 | — |
| `database_file/` `bin/` `*.exe` | ❌ | 运行时/构建产物 | 已在 `.gitignore` |

两个例外都有同一个可识别特征：**目录里有且仅有一个 `.go` 文件，内容只有 `package` + `//go:embed` + 一个 `embed.FS` 变量**。除此之外任何目录出现 `.go` 都是违规。

可执行的断言（可选，加进构建脚本或 CI）：

```bash
# 1) 根目录除 main.go 外不得有 .go
test "$(ls *.go)" = "main.go"

# 2) internal/ 与两个 embed 例外之外不得有 .go
! find . -name '*.go' -not -path './internal/*' -not -path './app/*' \
         -not -path './icon/*' -not -name 'main.go' | grep .

# 3) 两个例外必须保持"只有一个 shim"，不许长成真包
test "$(ls app/*.go)"  = "app/appembed.go"
test "$(ls icon/*.go)" = "icon/iconembed.go"
```

---

## 3. 三个需要决策的点（非机械迁移）

### 3.1 `main.go` 留在根，还是移到 `cmd/asa-server/`？

**决定：留在根目录。**

- 本仓库只有一个二进制。Go 官方与社区共识是 `cmd/` 用于**多二进制**仓库；单二进制时 `cmd/asa-server/main.go` 只是多一层空目录。
- 保持根 `main.go` 后，README / CLAUDE.md 里的 `go build -o asa-server.exe` **一个字都不用改**；若改用 `cmd/`，所有构建命令要变成 `go build -o asa-server.exe ./cmd/asa-server`。
- 反过来，将来若真要加第二个二进制（比如独立的 `asa-agent`），那时再引入 `cmd/` 也只是一次 `git mv`，成本相同。**不要为假设的未来提前付代价。**

### 3.2 `app/`（前端）—— 本次**保持不动**

**决定：`app/` 目录名、位置、`appembed.go` 全部原样保留，导入路径 `asa-server/app` 不变。**

背景约束：`app/appembed.go` 里是 `//go:embed dist`，而 **Go 的 embed 指令禁止 `..`** —— 只要 Go 代码要嵌入前端产物，那个 `.go` 文件就必须和 `dist/` 待在同一棵子树里。所以前端目录**无论叫什么名字**都注定要留一个 Go 文件，它是 §2 判定规则里的既定例外，不因改不改名而消失。

既然例外无法消除，就没必要为它付迁移成本。评估过但**不采纳**的几种做法：

| 方案 | 做法 | 不采纳的原因 |
|---|---|---|
| 改名 `web/` | `app/` → `web/`，`appembed.go` → `web/embed.go`（`package web`） | 例外照样存在；却要动前端工程根目录，牵连所有开发者的本地路径、IDE 配置、README 与 `cd app` 类命令 |
| 构建期拷贝 | 前端产物构建前拷进 `internal/webui/dist`（gitignore） | **破坏 `go build` 直接可用**，必须先跑拷贝步骤；漏跑时报「embed 找不到文件」，极难排查 |
| 整体搬进 internal | 前端工程搬到 `internal/webui/` | `node_modules`、`vite.config.js` 塞进 `internal/` 反直觉，前端定位成本高 |

因此 §2 表格里把 `app/` 标注为「已知例外：仅 `appembed.go` 一个 Go 文件」。**一个写进文档的例外，比一个必须记住的构建步骤更可靠。**

> ⚠️ 对迁移的实际影响：`app/` 不进 `internal/`，所以阶段 2 的批量 sed 会把 `webapi/actions.go` 里的 `"asa-server/app"` 误改成 `"asa-server/internal/app"`，**必须回改**。见阶段 2 的第二条命令。

### 3.3 `icon/`（静态资源）—— 本次**保持不动**

**决定：`icon/` 留在根目录，`iconembed.go` 原样保留，导入路径 `asa-server/icon` 不变。**

`icon/` 的本质是**静态资源目录**（1923 个 png，79MB），`iconembed.go` 只是 6 行的 embed shim：

```go
package icon

import "embed"

//go:embed creature items
var EmbeddedFS embed.FS
```

它和 §3.2 的 `app/` 是同一类东西——受 `//go:embed` 禁止 `..` 的约束，shim 必须和资源同目录，所以**无论放哪儿都会留一个 Go 文件**。而移进 `internal/` 的代价明显更高：

| 代价 | 规模 |
|---|---|
| `docs/*.md` 里的图片相对链接 `![](../icon/creature/xxx.png)` | **2162 处**（`asa-creatureids.md` 529 + `asa-itemsids.md` 1630 + 其它 3） |
| 4 个 Node 脚本的输出目录常量 `path.join(ROOT, 'icon', 'creature')` | 4 个文件 |
| Git 重命名 commit 体积 | 1923 个文件、79MB，diff 无法人工审阅 |
| 换来的收益 | 仅仅是少一条**已文档化**的例外 |

**收益远小于代价，不迁移。** 资源目录待在根目录本身也符合直觉——`icon/` 同时被 Go（embed）、Markdown 文档（相对链接）和 Node 脚本（写入）三方共享，放在 `internal/` 反而暗示它是「Go 私有」的，与事实不符。

> ⚠️ 对迁移的实际影响：与 `app/` 完全相同——阶段 2 的批量 sed 会把 `webapi/iconapi/iconapi.go` 里的 `"asa-server/icon"` 误改成 `"asa-server/internal/icon"`，**必须回改**。见阶段 2 的第二条命令。
>
> 相应地，原计划中「批量改 2162 处文档链接 + 4 个脚本常量」的阶段 4 **整个取消**。

---

## 4. 迁移步骤

> 每个阶段**独立 commit**，`go build ./...` 通过才进下一阶段。
> 命令给了 Git Bash 与 PowerShell 两版；本仓库两种 shell 都可用。

### 阶段 0：准备（务必执行）

```bash
git status                      # 工作区必须干净；当前有 5 个文件已修改，先提交或 stash
git switch -c refactor/internal-layout
git tag pre-internal-migration  # 出问题可一键回到这里
```

**⚠️ 关掉 IDE 或至少关掉 GoLand/VSCode 的自动 import 整理**——迁移中途的中间态会让 IDE 疯狂改文件，和批量 `sed` 打架。

**⚠️ 最容易踩的坑：两个被 gitignore 的嵌入二进制。**

```
frpmanage/frpc.exe          ← 被 .gitignore 第 5 行 *.exe 忽略（未跟踪）
syncthingmanage/syncthing.exe
```

它们**被 `//go:embed` 依赖，但不在 Git 里**。`git mv` 只搬运已跟踪文件，这两个 exe 会被留在原地，构建失败并报：

```
frpmanage/manager.go:21:12: pattern frpc.exe: no matching files found
```

这个报错不提任何路径问题，排查起来很费时间。所以阶段 1 必须用**普通 `mv`** 单独搬这两个文件（见下）。

顺手建议（可选）：把 `.gitignore` 的 `*.exe` 收窄，避免这类「构建必需但被忽略」的文件再次出现：

```gitignore
# 原：*.exe    ← 太宽，误伤嵌入二进制
/asa-server.exe
/app/asa-server.exe
bin/
!internal/frpmanage/frpc.exe
!internal/syncthingmanage/syncthing.exe
```

（是否把这两个 exe 纳入版本管理由你定；若因体积/许可不入库，就在 README 里写明「首次构建前需手动放置」。）

### 阶段 1：移动 Go 包目录

Git Bash：

> 注意：下面的清单里**没有 `app` 和 `icon`** —— 这两个 embed 目录本次保持不动（§3.2 / §3.3）。共移动 **25** 个目录。

```bash
mkdir -p internal
for d in actions appconfig auth backup batchmanage certmgr config countdown \
         frpmanage gui installer instance logger mirror parseserver pkg \
         process rconx realtime schedule state syncthingmanage updatemanage \
         webapi winservice; do
  git mv "$d" "internal/$d"
done

# ⚠️ 未跟踪的嵌入二进制，必须用普通 mv
mv internal/frpmanage/frpc.exe internal/frpmanage/ 2>/dev/null || \
  mv frpmanage/frpc.exe internal/frpmanage/frpc.exe
mv syncthingmanage/syncthing.exe internal/syncthingmanage/syncthing.exe
rmdir frpmanage syncthingmanage 2>/dev/null
```

PowerShell：

```powershell
New-Item -ItemType Directory -Force internal | Out-Null
$dirs = @('actions','appconfig','auth','backup','batchmanage','certmgr','config',
          'countdown','frpmanage','gui','installer','instance','logger',
          'mirror','parseserver','pkg','process','rconx','realtime','schedule',
          'state','syncthingmanage','updatemanage','webapi','winservice')
foreach ($d in $dirs) { git mv $d "internal/$d" }
Move-Item frpmanage/frpc.exe internal/frpmanage/frpc.exe
Move-Item syncthingmanage/syncthing.exe internal/syncthingmanage/syncthing.exe
```

验证嵌入资源都跟着走了：

```bash
ls internal/frpmanage/frpc.exe internal/syncthingmanage/syncthing.exe \
   internal/gui/ASA_Logo_transparent.webp
ls app/appembed.go icon/iconembed.go          # 这两个应当仍在根目录，未被移动
```

此时 `go build ./...` **必然失败**（导入路径还没改），正常。

### 阶段 2：改写导入路径

全仓库的跨包导入都形如 `"asa-server/xxx"`，包括带别名的（`cfgpkg "asa-server/config"`）——按字符串替换即可，别名不受影响。

Git Bash：

```bash
# 1) 全量加前缀
git grep -l '"asa-server/' -- '*.go' | xargs sed -i 's|"asa-server/|"asa-server/internal/|g'

# 2) ⚠️ 回改两个未移动的 embed 包（命中点：webapi/actions.go、webapi/iconapi/iconapi.go）
git grep -lE '"asa-server/internal/(app|icon)"' -- '*.go' | \
  xargs sed -i -e 's|"asa-server/internal/app"|"asa-server/app"|g' \
               -e 's|"asa-server/internal/icon"|"asa-server/icon"|g'

gofmt -l -w main.go internal/ app/ icon/
go build ./... && go vet ./...
```

PowerShell：

```powershell
Get-ChildItem -Recurse -Include *.go -Path main.go, internal, app, icon |
  ForEach-Object {
    $t = (Get-Content $_.FullName -Raw) -replace '"asa-server/', '"asa-server/internal/'
    $t = $t -replace '"asa-server/internal/app"',  '"asa-server/app"'
    $t = $t -replace '"asa-server/internal/icon"', '"asa-server/icon"'
    Set-Content $_.FullName $t -NoNewline
  }
gofmt -l -w main.go internal/ app/ icon/
go build ./... ; go vet ./...
```

注意点：

- `"asa-server/pkg/winproc"` → `"asa-server/internal/pkg/winproc"`，自动正确。
- **`"asa-server/app"` 与 `"asa-server/icon"` 必须回改**：这两个目录本次不移动（§3.2 / §3.3），全量 sed 会把它们误改成 `internal/` 路径，导致 `package asa-server/internal/app is not in std` 类报错。上面第 2 条命令负责还原；改完用 `git grep -E '"asa-server/internal/(app|icon)"'` 确认输出为空。
- 替换只影响**双引号包裹**的导入串，注释里的裸路径（如 `// 见 asa-server/config`）不会被动到；提交前扫一眼 `git diff` 里的非 import 行确认。
- module 名不变，`go.mod` **不需要任何改动**。

### 阶段 3：两个 embed 目录 `app/` `icon/` —— 不改，只做一次确认

按 §3.2 / §3.3 的决定，这两个目录的**名字、位置、shim 文件、包名、导入路径全部不动**：

- `app/`：`appembed.go`、`package app`、`asa-server/app`；前端的 `package.json`、`vite.config.js`、`cd app && npm run build` 一个字都不用改。
- `icon/`：`iconembed.go`、`package icon`、`asa-server/icon`；`docs/*.md` 里 2162 处 `../icon/` 图片链接、4 个 Node 脚本的输出路径**全部保持有效**，无需任何 sed。

本阶段唯一要做的是确认阶段 2 的回改生效：

```bash
git grep -nE '"asa-server/internal/(app|icon)"' -- '*.go'  # 必须无输出
git grep -n  '"asa-server/app"'  -- '*.go'                 # 应命中 webapi/actions.go
git grep -n  '"asa-server/icon"' -- '*.go'                 # 应命中 webapi/iconapi/iconapi.go
go build ./...
```

> 若第一条有输出，说明阶段 2 的第 2 条 sed 漏跑了，补跑即可，无需回滚。

### 阶段 4：（已取消）

原计划的「批量改 2162 处文档图片链接 + 4 个 Node 脚本常量」是 `icon/` 迁移的连带工作。既然 `icon/` 不动（§3.3），本阶段**整个取消**，`docs/` 与 `scripts/` 中所有 `../icon/` 路径保持原样。

### 阶段 5：非 Go 目录归位（可选，纯整洁性）

```bash
git mv scripts scripts_tmp && mkdir scripts && git mv scripts_tmp scripts/icons
git mv tools scripts/translate                 # ark_translate.py：Python 脚本不该占用 Go 语境的 tools/
mkdir -p data && git mv ASA-Translation data/ark-translation
```

> ⚠️ **本阶段的通用陷阱：脚本目录深了一层，所有"仓库根"的计算都要跟着改。**
> Python 与 Node 脚本都用 `<脚本位置>/..` 推算仓库根，移动后 `..` 会停在 `scripts/` 而不是仓库根。
> 症状是运行时找不到文件，报错**完全不提层级问题**，很费时间。下面两处都要改。

#### 5.1 4 个 Node 脚本（`scripts/icons/*.mjs`）

`icon/` 不迁移（§3.3），所以 `path.join(ROOT, 'icon', ...)` 这一半**不用动**；但 `ROOT` 本身的层级必须改：

```js
// 改前（脚本在 scripts/）
const ROOT = path.resolve(__dirname, '..');        // → <repo>

// 改后（脚本在 scripts/icons/，深了一层）
const ROOT = path.resolve(__dirname, '..', '..');  // → <repo>
```

涉及 4 个文件（变量名两种写法都有，`ROOT` 与 `root`）：

- `scripts/icons/icon_download_server.mjs:23`（`ROOT`）
- `scripts/icons/item_icon_download_server.mjs:18`（`ROOT`）
- `scripts/icons/update_md_icon_paths.mjs:13`（`root`）
- `scripts/icons/update_item_md_icon_paths.mjs:16`（`root`）

```bash
sed -i "s|path.resolve(__dirname, '\.\.')|path.resolve(__dirname, '..', '..')|g" scripts/icons/*.mjs
```

验证：`node scripts/icons/icon_download_server.mjs` 启动后应打印出正确的 `icon/creature/ 现有文件: <非 0 数量>`；若打印 0 或报错，就是 `ROOT` 层级没改对。

#### 5.2 `scripts/translate/ark_translate.py`

必须改 **路径常量**（原 `tools/ark_translate.py:23-25`）。这里有**两处**要动，漏掉任何一处都会在运行时报 `FileNotFoundError`：

```python
# 改前（脚本在 tools/，翻译数据在 ASA-Translation/）
SCRIPT_DIR = Path(__file__).parent          # → <repo>/tools
REPO_ROOT   = SCRIPT_DIR.parent             # → <repo>
TRANS_DIR   = REPO_ROOT / "ASA-Translation"

# 改后（脚本在 scripts/translate/，深了一层；数据在 data/ark-translation/）
SCRIPT_DIR = Path(__file__).parent          # → <repo>/scripts/translate
REPO_ROOT   = SCRIPT_DIR.parents[1]         # ⚠️ 目录深了一层，.parent 不再是仓库根
TRANS_DIR   = REPO_ROOT / "data" / "ark-translation"
```

1. **`REPO_ROOT` 的层级**：脚本从 `tools/`（深度 1）挪到 `scripts/translate/`（深度 2），`SCRIPT_DIR.parent` 会解析到 `scripts/` 而不是仓库根。必须换成 `SCRIPT_DIR.parents[1]`（等价于 `.parent.parent`）。**这一处最容易漏**——目录名改对了、层级没改，报错信息只会说找不到 `.jsonc` 文件，不会提示是层级问题。
2. **`TRANS_DIR` 的目录名**：`"ASA-Translation"` → `"data" / "ark-translation"`。`CUSTOM_FILE`（第 34 行）由 `TRANS_DIR` 派生，自动跟随，无需单独改。

顺带改文档字符串里的用法示例（`ark_translate.py:5,8-13`，共 6 处 `python tools/ark_translate.py` → `python scripts/translate/ark_translate.py`，第 5 行的 `ASA-Translation/*.jsonc` → `data/ark-translation/*.jsonc`）。

验证（在仓库根执行，`--dry-run` 不写文件）：

```bash
python scripts/translate/ark_translate.py docs/asa-creatureids.md --dry-run
```

其它文档改动：

- `docs/ark-translation-tool.md`（6 处 `python tools/ark_translate.py`）；
- `docs/download-creature-icons.md` / `download-item-icons.md`（3 处 `node scripts/*.mjs` → `node scripts/icons/*.mjs`）。

> 若暂不想动 Python，可只做 `tools/` → `scripts/translate/` 的移动而保留 `ASA-Translation/` 原地不动 —— 但那样仍需改 `REPO_ROOT` 的层级（第 1 点），**层级问题跟数据目录改不改名无关**。

> 为什么改 `tools/`：在 Go 仓库里 `tools/` 是「Go 工具链依赖」的强惯例（`tools.go` pattern），放一个 Python 脚本会持续误导人。`scripts/` 则是语言中立的。

### 阶段 6：文档与元数据收尾

- **`CLAUDE.md`**：`## Project Structure` 整棵树、`## Key Packages` 里的包路径、`## Key Data Flows` 的分层图 —— 全部按新布局更新（构建命令里的 `cd app && npm run build` **不用改**）。**这是最重要的一步**，这个文件是后续所有 AI 辅助工作的地图，过期会持续产生错误改动。
- `AGENTS.md`、`README.md` / `README_zh.md`、`docs/ARCHITECTURE.md`、`docs/PACKAGE_RESTRUCTURE_PLAN.md`（在开头加一句「目录已于本次迁移收进 `internal/`，包名与分层不变」）。
- `.gitignore`：见阶段 0 的收窄建议；另外 `database_file/` 是运行时目录漏在仓库根，可考虑让运行时 BaseDir 指向 `bin/` 之类，避免污染源码树。
- **重建 CodeGraph 索引**：路径全变了，旧索引会给出全是错误路径的答案。删掉 `.codegraph/` 重新 `codegraph init -i`。
- 删掉 `.idea/workspace.xml` 里的陈旧路径记录（IDE 会自己重建）。

---

## 5. 验证清单

按顺序执行，任一项失败就停下修，别往下走：

```bash
go build ./...            # 1. 全量编译（embed 缺文件会在这一步暴露）
go vet ./...              # 2. 静态检查
go test ./...             # 3. 31 个测试文件全绿（auth/certmgr/countdown/instance 等）
gofmt -l main.go internal app icon   # 4. 输出应为空
```

再做 4 项**运行时**验证——它们覆盖了纯编译查不出的嵌入资源问题：

| 验证项 | 命令 / 操作 | 覆盖的风险 |
|---|---|---|
| 前端资源嵌入 | `go build -o asa-server.exe .` → 启动 `api` → 浏览器打开首页 | `app/appembed.go` 的导入路径是否被 sed 误改（§3.2） |
| 图标嵌入 | 访问 `/api/icon/...` 任一图标 | `icon/iconembed.go` 的导入路径是否被 sed 误改（§3.3） |
| frp / syncthing | 在 UI 里启动一次 FRP 与 Syncthing | 两个 gitignore 的 exe 是否真被嵌进去了 |
| GUI 图标 | 直接双击 `asa-server.exe` 起 GUI | `internal/gui/ASA_Logo_transparent.webp` |

补充：`asa-server cert status`、`asa-server db status` 各跑一次，确认 CLI 子命令装配未受影响。

---

## 6. 风险与回滚

| 风险 | 触发条件 | 处置 |
|---|---|---|
| **构建报 `pattern frpc.exe: no matching files`** | 阶段 1 只用了 `git mv`，漏搬两个未跟踪 exe | 按阶段 0 的说明用普通 `mv` 补搬；这是本次迁移**最可能**踩的坑 |
| 前端/图标 404 或编译报 `is not in std` | 阶段 2 的全量 sed 把 `asa-server/app`、`asa-server/icon` 误加了 `internal/` 前缀 | 补跑阶段 2 的第 2 条回改命令；`git grep -E '"asa-server/internal/(app\|icon)"'` 应为空 |
| 脚本运行时找不到文件 | 阶段 5 把脚本挪深一层，但 `ROOT` / `REPO_ROOT` 仍按 `..` 推算 | 见 §5.1（Node `'..','..'`）与 §5.2（Python `parents[1]`）；这是阶段 5 唯一的非机械改动 |
| `sed` 误改注释/字符串 | 注释里出现 `"asa-server/xxx"` 形式 | 提交前 `git diff` 过一遍非 import 行；Go 代码里没有按包路径反射的逻辑，运行时无隐性依赖 |
| IDE 在迁移中途自动改 import | 阶段 1↔2 之间编译不通过的窗口期 | 阶段 0 先关 IDE |
| 文档大面积过期 | 只改代码不改 `CLAUDE.md` | 阶段 6 与代码同一分支一起合入，不留尾巴 |
| CodeGraph 给出旧路径 | 未重建索引 | 阶段 6 删 `.codegraph/` 重建 |

回滚：每阶段独立 commit，可 `git reset --hard HEAD~1` 逐步退；整体放弃则 `git reset --hard pre-internal-migration`。**迁移全程不改任何包的逻辑**，所以任何运行时异常都应先怀疑「文件没搬全」，而不是「代码坏了」。

---

## 7. 明确不做的事

- **不改包名、不改包职责、不改分层依赖方向**。`PACKAGE_RESTRUCTURE_PLAN.md` 定下的无环分层原样保留，本次只是整棵子树平移。
- **不引入 `cmd/`**（理由见 §3.1）。
- **不动 `app/` 与 `icon/`**（理由见 §3.2 / §3.3）：不改名、不移动、不改包名、不改 `//go:embed`。二者是判定规则里**仅有的、且已文档化**的两个例外，成因相同——`//go:embed` 禁止 `..`，shim 必须与资源同目录，换任何位置都消除不掉这个例外。
- **不改 `docs/` 里 2162 处 `../icon/` 图片链接、不改 4 个 Node 脚本的 `icon` 路径**（`icon/` 不动的直接结果）。
- **不改 module 名**。若将来要发布到 GitHub，届时再 `module github.com/<user>/asa-server`——那时 `internal/` 已经就位，正好把内部包挡在公共 API 之外，这也是本次迁移最大的长期收益。
- **不把领域包塞进 `pkg/`**。`internal/pkg/` 严格按 §2 的三条准入标准只收基础设施（当前 8 个包），26 个领域包一律平铺在 `internal/` 下。把领域包塞进 `pkg/` 会让 `pkg/` 退化成第二个「什么都往里扔」的 `utils/`，正是上一轮神包拆分要消灭的东西。
- **也不把 `internal/pkg/` 拆平**（如 `internal/winproc/`）。保留 `pkg/` 这一层是有信息量的：它标记「零领域依赖的叶子工具」，与领域包区分开；且保持为纯路径平移，不产生额外改动面。

---

## 8. 工作量估算

| 阶段 | 内容 | 机械程度 | 预估 |
|---|---|---|---|
| 0 | 分支、tag、关 IDE、确认两个 exe | 手动 | 5 min |
| 1 | **25** 个目录 `git mv` + 2 个 exe `mv` | 全机械 | 5 min |
| 2 | 导入改写（含 app/icon 回改）+ `gofmt` + build | 全机械 | 10 min |
| 3 | `app/` `icon/` 不动，仅确认导入未被误改 | 三条 grep | 2 min |
| 4 | 已取消（`icon/` 不迁移，无连带改动） | — | 0 |
| 5 | scripts / tools / data 归位（可选）+ 脚本层级修正 | 机械 + 两处路径 | 20 min |
| 6 | CLAUDE.md 等文档 + 重建索引 | 手写 | 40 min |
| 验证 | 编译 + 测试 + 4 项运行时验证 | 手动 | 20 min |

合计约 1.5 小时，其中真正需要动脑的只有阶段 5 的路径层级修正与阶段 6 的文档更新。相比初版方案，`app/` 与 `icon/` 不迁移省掉了约 2200 处连带改动和一个 79MB 的重命名 commit。
