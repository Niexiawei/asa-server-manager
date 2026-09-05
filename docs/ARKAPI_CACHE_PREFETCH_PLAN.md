# ArkApi offsets cache 预取方案（在 ArkApi 之前把缓存备好）

> 目标：在启动 `AsaApiLoader.exe` **之前**，由 asa-server 自己把 ArkApi 需要的 offsets
> cache 下好、解压好、按 ArkApi 认得的格式提交好（断点续传、多 CDN、可走
> `download.http_proxy`），让 ArkApi 启动时直接采用本地缓存。
>
> **成立条件比「把缓存做对」更严一点**，这是通读 `ArkBaseApi.cpp` 之后的结论（§2.3）：
> 自动下载默认开启，此时即使本地缓存完全有效，ArkApi 仍会向 CDN 发一次 HEAD，
> 拿 `Last-Modified` 与 `cached_key.cache` 里的 `last_modified` **逐字比对**——
> 相等才跳过下载。所以本方案有两个承重件：
>
> 1. 缓存本身做对（generation + 两个 `.cache` 通过 `validateSerializedMap` + 哈希匹配）；
> 2. `last_modified` 写的是**CDN 真实返回值**，且来自 ArkApi 会优先查询的那个 CDN。
>
> 另有一个可选的第三件：把 `settings.AutomaticCacheDownload.Enable` 写成 `false`
> （该开关确实存在，见 `ArkBaseApi.cpp:247/314`，默认 `true`）。它是唯一能让 ArkApi
> **完全不联网**的路径，但用错会让实例挂满 3 分钟——默认关闭，见 §4.4。
>
> 硬约束（与 `docs/STEAMRT_PREFETCH_PLAN.md` 同一条）：**这是加速，不是新的失败点。**
> 预取的任何一步失败，都必须无声降级回今天的行为（ArkApi 自己去下），绝不能让一台
> 原本能启动的机器启动不了。
>
> 依据：ArkApi 的 `ArkBaseApi.cpp`（启动决策流、ZIP 规则、generation 规则）与
> `Cache.h` / `Cache.cpp`（序列化格式）。行号均指这两份源码。

## 0. 实现状态

**阶段一 ~ 四已落地**（阶段五未做）。代码见 `pkg/arkcache`、`pkg/jsonx`、
`internal/instance/arkcache.go`、`internal/actions/arkapicache.go`，以及
`internal/mirror/mirror.go` 收窄后的守卫。

**§17 第 5 项——本方案成败的唯一判据——已在真机通过**（Windows，2026-09-05，
实例 `meijue`）：

```
[API][info] A verified local cache matches the current executable
[API][info] Checking …zip for an updated cache archive (attempt 1/3)
[API][info] The verified local cache is current      ← 目标状态
[API][info] Reading cached offsets / Reading cached bitfields
[API][info] API was successfully loaded
```

`Downloading cache archive` **没有出现**：缓存格式、generation 命名、
`last_modified` 的逐字比对全部成立。完整实测数据见 §21。

**③（`disable_loader_download`）已整条移除**，它建立在一个错误的读码推断上——
真机根本没有 `ArkApi/config.json`，那个开关我们够不着。原委与证据见 §22，
**§2.3 下半、§4.4 后半、§13 最后两行、§14 的 `disable_loader_download`、§15、
§17 第 5c 项、§18 阶段三全部作废**，阅读时请以 §22 为准。

实现相对本文原稿的其余偏差见 §20（其中 **§20.12 是一处实质补正**：快路径只比
exe 哈希不够，还要重新校验 `Last-Modified`）。

---

## 1. 问题

启用 ArkApi 的实例，启动链是 `AsaApiLoader.exe` → 下 offsets cache → 创建游戏进程。
那次下载由 ArkApi 的 C++ 代码发起，特征与 `STEAMRT_PREFETCH_PLAN` §1 描述的 umu 内部
下载完全同构：

1. **不归我们管**。走 ArkApi 自己的 HTTP 实现，`config.yaml` 的 `download.http_proxy` /
   `download.retries` 一个都不生效。
2. **失败即从头再来，而且有 10 分钟死线**。`Requests::DownloadFile` 用
   `std::ios::trunc` 全量重写（`Requests.cpp:1123`），没有任何续传；整个下载还压着一个
   10 分钟的总死线（`Requests.cpp:1071`）。也就是说**平均速率低到 10 分钟下不完的链路，
   ArkApi 永远下不完这个包** —— 重试多少次都一样。我们的 `.part` 续传没有这个天花板，
   这是本方案相对「让 ArkApi 自己下」的**质变**，而不只是省时间。
3. **失败形态在本项目里格外贵**。`internal/instance/common.go:128` 那条注释已经记下了 ——
   `gamePIDWaitTimeoutArkApi` 之所以是 3 分钟而不是 30 秒，就是因为加载器要先下 cache
   才会创建游戏进程；CDN 慢一点，`waitForGamePID` 就会超时，然后 `server.go` 的失败分支
   `KillTree` 掉整条启动链，用户看到的是「启动失败」，原因埋在 `arkAsaApi.log` 里。
4. **N 个实例下 N 遍**。`ShooterGame/Binaries/Win64` 在镜像里是**整棵真实复制**的
   （`internal/mirror/mirror.go:24` 的 `win64RelPath`），`ArkApi/Cache` 落在它下面，
   于是每个实例都有一份私有 Cache，也就各自向 CDN 下了一遍同样的 ZIP。
5. **（Linux 上的一个待证假设）证书链一断就永远下不成**。ArkApi 的 TLS 上下文用
   `VERIFY_STRICT` + `loadDefaultCAs=false`，根证书全部现从 **Windows 的 ROOT 存储**
   里读（`Requests.cpp:58-101`）；一张都读不到就抛 `std::runtime_error`。该异常不是
   `Poco::Exception`，会被 `:1027` 的 catch 吞成 `return false`，于是表现为「下载失败」
   而不是「证书问题」，然后进 30~60 秒的无限重试。**Wine prefix 里那个 ROOT 存储若没被
   填充，ArkApi 就永远拿不到 cache，此时预取是唯一出路。** GE-Proton 的新 prefix 通常
   会由 Wine 的 crypt32 导入宿主 CA，所以多半没事 —— 这条是读码推断，未实测，
   列在这里是因为一旦命中，现象（无限重试、日志只说下载失败）极难指向真正的原因。

---

## 2. 取证：C++ 侧的硬约束

### 2.1 目录与元数据（`ArkBaseApi.cpp`）

以下每条都是 Go 侧的**格式义务**，不满足则 `InspectLocalCache` 判定本地缓存不可用。

| # | 约束 | 出处 |
|---|---|---|
| 1 | 缓存根 = `<游戏 exe 目录>/ArkApi/Cache/`，metadata = 其下 `cached_key.cache` | `:272-276` |
| 2 | ZIP URL = `<CDN>/cache/<SHA256(ArkAscendedServer.exe)>.zip` | `:281,346` |
| 3 | metadata 是 JSON，`version` 必须**等于 1**；`executable_hash` 必须是 64 位十六进制（大小写不敏感，比对前转小写）；`cache_directory` 为空或必须通过 `IsSafeGenerationDirectory` | `ParseCacheMetadata :115-146` |
| 4 | `cache_directory` 的父目录必须**恰好是 `generations`**；目录名里第一个 `-` 必须**正好在下标 64**，总共 3 个 `-`，其后三段必须是**非空纯数字** | `IsSafeGenerationDirectory :81-113` |
| 5 | generation 内必须有 `cached_offsets.cache` 与 `cached_bitfields.cache`，且两者都要通过 `validateSerializedMap`（`intptr_t` / `BitField`） | `InspectLocalCache :172-176` |
| 6 | ZIP 条目数 2~4；只认 4 个白名单名字，其余整包拒绝；同名重复拒绝；单条目 `uncompressed_size` 必须 > 0、≤ 512 MiB，累计 ≤ 768 MiB；下载体 ≤ 768 MiB | `DownloadCacheFiles :641-717,586` |
| 7 | 提交顺序：generation 完全就绪 → 加载成功 → **最后**才原子写 metadata（`.tmp` + `FlushFileBuffers` + `MoveFileEx(REPLACE_EXISTING)`） | `:536-556`、`Cache.cpp:69` |

两条容易踩的细节：

- `ParseCacheMetadata` 还接受**裸 64 位哈希**作为整个文件内容（历史格式，`:141-143`），
  此时 `cache_directory` 为空、缓存文件直接落在 Cache 根。我们不产生这种形态，但
  `Inspect` 要认得它，否则会把一份 ArkApi 自己留下的合法缓存误判为无效。
- `CleanupOldCacheGenerations`（`:210-232`）会把 `generations/` 下**所有非当选**的
  目录整棵删掉，启动时（`:359`）和提交后（`:556`）各跑一次。所以镜像里留几代根本
  由不得我们 —— `keep_generations` 只对源目录有意义（§14）。

### 2.2 `.cache` 文件的二进制格式（来自 `Cache.h`）

`validateSerializedMap<T>` / `deserializeMap<T>` 读的是一个**无头、无对齐**的记录流，
一直读到文件尾：

```
repeat until EOF:
    keySize : size_t   (Win64 → 8 字节小端)
    key     : keySize 字节（不含结尾 NUL）
    value   : sizeof(T) 字节（原样 memcpy）
```

判定规则逐条抄下来（`Cache.h:19-71`），Go 侧必须**逐条等价**：

| 规则 | 值 |
|---|---|
| 文件必须是常规文件且 `size > 0` | — |
| `keySize == 0` → 非法 | — |
| `keySize > maxKeySize` → 非法 | `1024*1024` |
| `keySize > 剩余字节` → 非法 | — |
| 读完 key 后剩余 `< sizeof(T)` → 非法 | — |
| **key 重复 → 非法** | `unordered_set::emplace(...).second` |
| 条目数 > `maxEntryCount` → 非法 | `5'000'000` |
| 必须**恰好**读完（`bytesRemaining` 归零）且 `entryCount > 0` | — |

`T` 的宽度：`cached_offsets.cache` 是 `intptr_t` → **8 字节**（Win64）。
`cached_bitfields.cache` 是 `BitField`，其定义不在这两个文件里 —— 见 §12.2 的处理办法。

顺带确认两件事，省得以后再猜：

- `cached_offsets.txt` 是 `saveToFilePlain`（`Cache.cpp:155`）产出的**排序后的键名清单**，
  纯人读调试用。ArkApi 解压时若 ZIP 里有它会一并落到 generation 里（`:696-700`），
  但加载路径从不读它，`:766` 的成功判据也只看另外两个 → **我们不提取**。
- `saveToFile`（`Cache.cpp:69`）本身就是 `.tmp` + `FlushFileBuffers` +
  `MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH)`，即 C++ 侧写 metadata 用的就是原子替换。
  Go 侧用 `.tmp` + `os.Rename`（Windows 上映射为 `MoveFileEx(REPLACE_EXISTING)`）语义一致。

### 2.3 ArkApi 的启动决策流（`ArkBaseApi::Init`）

这是全文最关键的一节 —— 它决定了「缓存做对」到底够不够。

```
读 <ArkApi 目录>/config.json                                        :246-247, GetConfig :563
enable := config.settings.AutomaticCacheDownload.Enable  (默认 true)  :314
urls   := [DownloadCacheURL] + DownloadCacheURLs           :315-329
          默认 = pelayori / shadowhunter / shadowhunter-systems

┌─ enable == true（出厂默认）─────────────────────────────────── :344-485
│  InspectLocalCache + CleanupOldCacheGenerations                :357-359
│  ├─ 本地缓存无效 → 直接进下载分支
│  └─ 本地缓存有效 → **仍然发一次 HEAD** 拿 Last-Modified         :371-390
│        ├─ 拿到 && == metadata.last_modified
│        │      → "The verified local cache is current" → 采用，不下载   ✅ 目标状态
│        ├─ 拿到 && != metadata.last_modified
│        │      → shouldDownload = true → **整包重下**                   ❌ 预取白做
│        └─ 拿不到（断网 / 被墙 / CDN 挂）
│               → 不下载，但 failuresWithUsableCache++
│                 Sleep(30~60s) 重来，满 3 次才肯用本地缓存         :460-483
│                 ⇒ 启动被硬拖 **60~120 秒**（两次 sleep）
└─ enable == false ──────────────────────────────────────────── :486-501
   只做 InspectLocalCache，**完全不联网**
   ├─ 有效 → 立即采用                                            ✅ 最优
   └─ 无效 → critical 日志 + Sleep(30~60s) **无限循环**等本地缓存出现
             ⇒ 游戏进程永不创建，只能靠我们的 gamePIDWaitTimeoutArkApi 兜住
```

三个直接后果，全部写进了后面的设计：

1. **`last_modified` 是承重件**，不是可选项。写错 = 每次启动都被 ArkApi 整包重下，
   我们的预取完全失效（还多占一份磁盘）。§4.5 给出写法。
2. **完全断网的机器，光有有效缓存也会被拖 60~120 秒**。这个数字要和
   `gamePIDWaitTimeoutArkApi = 3min`（`internal/instance/common.go:133`）放在一起看 ——
   余量只剩一半左右。这类机器才是 `Enable=false` 的适用对象。
3. **`Enable=false` 是双刃的**：缓存有效时最优（零联网、零等待），缓存无效时是
   无限循环。所以它只能在「我们刚刚确认缓存有效」的前提下写，且必须能写回 `true`（§4.4）。

---

## 3. 方案总览

两层必做 + 一层可选：

```
① 在源目录里把缓存做对（下载 + 校验解压 + 造 generation + 原子提交 metadata），一次
   {ServerFilesDir}/ShooterGame/Binaries/Win64/ArkApi/Cache/
        ├── cached_key.cache
        └── generations/<hash>-<pid>-<UnixMilli>-0/{cached_offsets.cache,cached_bitfields.cache}
                    │
                    │  ② 走既有的镜像增量同步分发（不新写复制逻辑）
                    ▼
   <镜像>/ShooterGame/Binaries/Win64/ArkApi/Cache/   ← 各实例一份，内容逐字节相同
                    │
                    │  ③（可选，默认关）把镜像里 ArkApi/config.json 的
                    │     settings.AutomaticCacheDownload.Enable 写成 false
                    ▼
              runner.Run(AsaApiLoader.exe)
                    │
                    ├─ ③ 未启用 → HEAD 一次，last_modified 相等则采用（§2.3 上半）
                    └─ ③ 已启用 → 零联网，直接采用（§2.3 下半）
```

关键在于**②不是新代码**：`internal/mirror` 已经在每次启动前对镜像与源做 diff，
源有镜像缺就复制过去。缓存做在源里，分发就是免费的。需要动的只是
`mirror.go` 里那两处为「ArkApi 自己写缓存」而设的守卫（§7）。

③ 之所以是可选而不是默认：它把「缓存无效」的失败形态从「ArkApi 自己下一遍（可能成功）」
变成「无限等待直到我们超时杀掉」。默认关闭时方案仍然成立（HEAD 一次、比对通过、不下载），
只是断网机器要多等 60~120 秒；把那 60~120 秒也省掉的人再显式打开它。

---

## 4. 关键设计决策

### 4.1 缓存做在源目录（`server-files`），靠镜像同步分发

| 方案 | 结论 |
|---|---|
| A. 每实例各自下载到自己的镜像 | ❌ 镜像重建即丢、N 实例 N 次下载，只解决了「续传」 |
| B. `{BaseDir}` 下另建全局 store，再逐实例 materialize 进镜像 | ❌ 自己重写一遍「从源复制到镜像」，而那正是 `mirror` 的既有职责 |
| C. 把 `ArkApi/Cache` 改成共享 junction 指回源 | ❌ 见下 |
| **D. 做在源目录，镜像同步分发（本方案）** | ✅ |

**C 被否决**（诱人但危险）：改成 junction 能把磁盘压到 1 份，但 generation 的清理
会变成跨实例操作 —— A 实例正在启动、B 实例把旧代删了，失败形态是启动中途读不到文件，
而这条路径没有任何现成互斥；而且降级路径下多个 ArkApi 进程会并发往同一个 Cache 目录写，
C++ 侧对此没有任何保证。`mirror.go` 里 `arkApiCache` 那两处守卫
（`mirror.go:659/689`）本来就是按「Cache 是每实例私有」写的，改共享等于同时推翻它们。

**D 的代价**：磁盘上是 1 份源 + N 份镜像副本（与今天完全一样，今天还多了 N 次下载）。
换来的是：下载只发生一次、镜像重建后自动补齐、旧 generation 由同步的删除分支自动清掉
（§7）、Go 侧不需要写任何「复制到实例」的代码。

### 4.2 下载物不落在源目录里

`.part`、`.zip` 与旁车元数据放 `{BaseDir}/arkapi-cache/`，**不放**
`server-files/.../ArkApi/Cache/` 之下 —— 否则同步会把一个下了一半的 `.part`
当成源里的新文件复制进每个镜像。源目录里只出现**最终成品**：`generations/<gen>/` 与
`cached_key.cache`。

### 4.3 什么时候下

- **主路径：启动时同步阻塞**，位置在镜像同步**之前**（§6）。
- **辅路径：更新后预取**。`asa-server update` / `POST /api/server/update` 之后
  `server-files` 的 exe 哈希必然变化，此时预取新哈希的 ZIP。这条路径本来就有 SSE 进度
  通道（`serverapi` 的 `updateBroadcaster`），用户此刻也本来就在等 → 阶段三，非必需。
- **不做**：后台定时轮询 CDN。哈希只在更新时变，没有别的触发源。

### 4.4 失败时回到今天的行为，以及 `Enable` 开关的不变式

默认配置（③ 关闭）下降级是**天然**的：源里没有有效缓存 → 镜像里也就没有 →
ArkApi 照今天那样自己去下。`Prepare` 永远不向上抛致命错误，只把原因写进日志：

| 情况 | 结果 |
|---|---|
| 源里已有当前哈希的有效缓存 | 立即返回（`From=existing`），零 I/O 之外的开销 |
| 下载 / 解压 / 结构校验 / 写盘失败 | 清掉半成品，**不动**已有的 `cached_key.cache`，Warn 一条，启动继续 |
| `arkapi_cache.enabled: false` | 完全不介入 |
| ctx 取消（用户中止启动） | 立即返回，`.part` 保留供下次续传 |

「**不动已有的 `cached_key.cache`**」是硬规则：宁可让 ArkApi 用一份旧的（它自己会判
哈希不匹配再去下），也不能出现「metadata 指向的 generation 不存在」—— 那是 C++ 侧最难
诊断的形态。

开启 ③（`arkapi_cache.disable_loader_download: true`）时，多一条**必须闭合的不变式**：

```
本次启动 Prepare 返回 Ready=true   → 把镜像里的 Enable 写成 false
本次启动 Prepare 返回 Ready=false  → 把镜像里的 Enable 写回 true   ← 不能"保持不动"
```

反例（不写回的后果，实打实会发生）：上次启动成功写了 `false`；之后 ARK 更新换了 exe
哈希，这次又没网 → ArkApi 既没有匹配的本地缓存、又被禁止下载 → 进 `:488-500` 的无限
循环 → 游戏进程永不出现 → 3 分钟后被我们 `KillTree`。写回 `true` 就退化成今天的行为。

好在镜像里的 `ArkApi/config.json` 每次同步都会被源版本按 MD5 覆盖回去
（`mirror.go:982` 的 reconcile），我们的修改天然不会长期漂移：每次启动在同步之后
重新施加一次即可，不需要备份/还原逻辑。**绝不改 `server-files` 里的那份。**

另有一条防御：`GetConfig()`（`:563`）在 `config.json` 不存在时返回的是 JSON `false`，
紧接着 `:247` 对它调 `.value("settings", ...)` 会抛 `type_error` —— 而那两行在
`try` 块**之外**（`try` 从 `:265` 才开始）。也就是说**config.json 缺失或非 JSON 对象
会让 ArkApi 在任何日志之前就崩**。我们的写入路径因此只做「读出来 → 改一个叶子 →
原子写回」，文件读不到或不是 JSON 对象时**什么都不做**，绝不代为创建。

### 4.5 `last_modified` 必须写对（承重件）

§2.3 已经说明：③ 关闭时，ArkApi 会拿 metadata 里的 `last_modified` 和 CDN 的 HEAD
结果逐字比对，不等就整包重下。规则：

1. **写 CDN 返回的原始 `Last-Modified` 头**，不做任何格式化、时区换算或重排。
   已逐行确认：`GetFileLastModified` 是 `lastModified = response.get("Last-Modified")`
   （`Requests.cpp:1019`），`DownloadFile` 同样直取原始头（`Requests.cpp:1111-1113,1186`）。
   两边都不解释语义，**逐字节相等**即可。
1b. **HEAD 必须跟随重定向，并取最终响应的头**。ArkApi 的 HEAD 最多跟 5 跳、
   拒绝 HTTPS→HTTP 降级（`Requests.cpp:996-1014`），值来自终点。Go 的
   `http.Client` 默认就跟随重定向，只要不去关它即可；CDN 哪天加一层 302，
   我们和 ArkApi 仍然看到同一个值。
2. **值要取自 ArkApi 会优先查询的那个 CDN**，实践中几乎总是列表第一个：
   ArkApi 的 CDN 循环（`ArkBaseApi.cpp:378-390`）**只在抛 Poco 异常（连不上/超时）时
   才换下一个**；HEAD 返回 false（404、或 200 但没有 `Last-Modified` 头）会直接 break。
   所以如果我们因为主 CDN 挂了而从第二个源下载，就要在下载完成后**补一次对主 CDN 的
   HEAD**：拿得到就用主 CDN 的值，拿不到再退用实际下载源的值。
3. 因此 `arkapi_cache.urls` 的**默认顺序必须与 ArkApi 的默认顺序一致**
   （pelayori → shadowhunter → shadowhunter-systems），用户覆盖时也应保持第一个相同。
   这一条要写进配置注释，否则改配置的人会在毫无提示的情况下让预取失效。
4. 拿不到任何 Last-Modified 时写空串。此时 ArkApi 的 HEAD 若成功则必然不等 → 重下；
   若也失败则走「拖 60~120 秒后用本地缓存」。两种都不比今天差，可接受。

验证手段是现成的：ArkApi 命中时会打印 `The verified local cache is current`
（`:394`），未命中则打印 `Downloading cache archive ...`（`:407`）。这两行就是
§17 验收项 5 的判据。

### 4.6 哈希算谁的 exe

ArkApi 算的是**它自己所在目录**的 `ArkAscendedServer.exe`（镜像里那份）。镜像里的 exe
是源文件的字节副本（同步靠 MD5 保证），两者哈希必然相同，所以我们**只算源的那份**，
一台机器一次，全实例共用。

SHA256 一个几百 MB 的文件在 SSD 上是亚秒级，但每次启动都算仍然浪费。按 `modTime + size`
做内存缓存，**缓存逻辑写在公开函数内部**，不另包一层 `xxxCached` 包装 —— 与
`internal/instance/common.go:615` `GetInstanceAsaVersion` 的现成写法一致。读 exe 一律
`os.Open` 只读打开（那个文件常常正被运行中的服务器占用）。

---

## 5. 落点：`pkg/arkcache`（轻量注入，零领域依赖）

`internal/` 已经偏臃肿，且本功能的逻辑全部是「给定一个 exe 和一个 Cache 目录，把缓存
备好」—— 不认识实例、镜像、BaseDir 中的任何一个。三条准入标准
（`docs/INTERNAL_LAYOUT_MIGRATION.md` §9：不认识领域概念 / 零领域依赖 / 无全局状态）
全中，**放 `pkg/`**。

```
pkg/arkcache/
├── arkcache.go     # 包文档 + Request/Result + Prepare / Inspect / GC
├── hash.go         # ExeHash(path)：SHA256 + 内建 modTime/size 缓存（§4.5）
├── fetch.go        # 多 CDN 候选、HEAD 旁车、pkg/download 续传、大小上限
├── zip.go          # 白名单解压（条目数 / 单条目 / 总量 / 路径穿越）
├── serialized.go   # validateSerializedMap 的 Go 复刻（§2.2）+ BitField 宽度推断
├── generation.go   # generation 命名、cached_key.cache 原子提交、旧代清理
├── loaderconfig.go # ArkApi config.json 里 AutomaticCacheDownload.Enable 的保序改写（③）
└── *_test.go
```

依赖只有 `pkg/download` / `pkg/fsutil` / `pkg/logger`，都是同级叶子包。

**注入面**（调用方给什么，包就用什么，不去猜路径）：

```go
type Request struct {
    ExePath   string   // ArkAscendedServer.exe（源目录里那份）
    CacheRoot string   // <exe 目录>/ArkApi/Cache
    WorkDir   string   // 下载中转：{BaseDir}/arkapi-cache
    URLs      []string // CDN 前缀列表，按序回退；空 = 用包内默认
    MaxSize   int64    // 0 = 768 MiB
    Keep      int      // 除当前哈希外额外保留几代，默认 0
    Progress  func(done, total int64)
}

type Result struct {
    Ready      bool   // CacheRoot 下现在是一份对当前 exe 有效的缓存
    Hash       string // exe 的 SHA256
    Generation string // "generations/<gen>"
    From       string // "existing" | "download"
    Reason     string // Ready=false 时的人话原因
}

// Prepare 幂等：已经有效就直接返回，不联网、不写盘。
func Prepare(ctx context.Context, req Request) Result

// Inspect 只读：判断某个 Cache 根对某个 exe 是否有效（供 mirror 决策，见 §7）。
func Inspect(cacheRoot, exeHash string) (Result, error)

// GC 删除非当前哈希的 generation 与陈旧的 .part。dryRun=true 只报不删。
func GC(req Request, dryRun bool) ([]string, error)

// SetAutomaticDownload 改写 <arkApiDir>/config.json 的
// settings.AutomaticCacheDownload.Enable（③）。文件不存在、读不出、不是 JSON 对象时
// **什么都不做并返回 nil** —— 见 §4.4 末尾：代为创建会让 ArkApi 在任何日志之前崩掉。
// 保序改写，只动这一个叶子；缺失的中间层级按需补出。
func SetAutomaticDownload(arkApiDir string, enable bool) error
```

`loaderconfig.go` 的保序 JSON 读写与 `internal/plugindata/configmerge.go` 的
`orderedObject` 是同一件事（用户手改过的配置被 `map[string]any` 重排后没法看）。
**先把那套 token 流保序读写下沉到 `pkg/`**（`pkg/jsonx` 或本包内导出），
`plugindata` 改为引用，不要复制第二份 —— 两份实现迟早漂移。

调用侧的适配器放 `internal/instance/arkcache.go`，约 40 行：从 `cfgpkg.ServerFilesDir`
/ `cfgpkg.BaseDir` / `appconfig.Get().ArkApiCache` 拼出 `Request`，调 `Prepare`，
把进度接到 `logger`。这就是「轻量注入」的全部含义 —— 领域知识留在 `internal`，
算法留在 `pkg`。

---

## 6. 接线点（`internal/instance/server.go`）

### 6.1 位置

```go
// server.go:231 —— 已有的更新中检查（保持不动，必须在我们之前）
if installer.IsUpdatingServerFiles() { ... }

// server.go:245 —— 已有的 LoadInstanceConfig

【新增 A】if config.EnableAsaPlugin && arkApiInstalledInSource() {
              ready = prepareArkApiCache(ctx)   // §5 的适配器，内部永不抛致命错误
          }

// server.go:254 —— 已有的 mirror.SyncInstanceMirror（把缓存分发进本实例镜像）
// server.go:262 —— 已有的 VerifyAndRepairInstanceMirror
// server.go:271 —— 已有的 plugindata.Rescue / Inject

【新增 B】if ③ 已启用 && config.EnableAsaPlugin {
              arkcache.SetAutomaticDownload(<镜像>/…/ArkApi, !ready)   // 见 §4.4 的不变式
          }

// server.go:278 —— 已有的 runner.ChownMirrorForRuntime（必须在 B 之后）
```

A 与 B 必须**分处同步的两侧**：A 写的是源目录，要在同步前才能被分发；B 写的是镜像里的
`config.json`，而同步会按 MD5 把它覆盖回源版本，放在同步前等于白改。B 与
`plugindata.Inject` 是同一类操作（往刚同步好的镜像里注入本实例特有的东西），
所以并列放在一起、并且都在 `ChownMirrorForRuntime` 之前 —— 那之后再写文件，
Linux 上写出来的就是 root 所有、降权游戏进程读不到。

A 的三条不能挪的理由：

1. **必须在 `SyncInstanceMirror` 之前**。整个方案靠同步来分发，放在它之后就要等下一次
   启动才生效。
2. **必须在 `IsUpdatingServerFiles()` 检查之后**。更新期间 `server-files` 正在被增删，
   此时往里写缓存、算 exe 哈希都是对着一个中间态在做。
3. **必须在 `acquireLaunchGate`（server.go:520）之前**。共享 prefix 下闸门是全局串行点，
   把一次可能几分钟的下载放进去，所有实例都会排在它后面。

顺带的好处：**几百 MB 的缓存文件全部由同步创建**，随后被
`runner.ChownMirrorForRuntime`（server.go:278）整棵接管，Linux 降权的属主问题自动解决。
B 那个几 KB 的 `config.json` 是唯一由我们直接写进镜像的东西，它遵守既有的
「chown 之前造完」不变式即可。

### 6.2 `gamePIDWaitTimeoutArkApi` 不动

3 分钟那档保持不变。缓存就绪后加载器不再下载，游戏进程会快很多；但把超时收紧属于
「顺手改一个当前没坏的东西」，而降级路径（§4.4）下那 3 分钟仍然是必需的。
只更新 `common.go:128` 的注释，说明多数情况下不再走下载。

---

## 7. `internal/mirror` 的守卫要收窄

今天 `syncMirrorEntries` 对 `ArkApi/Cache` 之下的一切一律放行（`mirror.go:659/689`）：
镜像多出来的不删、两边都有的不比对。那是为「ArkApi 自己往镜像里写运行期缓存」写的，
现在源目录成了权威，这条守卫会造成两个静默故障：

- ARK 更新后源里换了新 `cached_key.cache`，镜像里那份**永远不会被更新**（Match 分支被
  跳过）→ 镜像的 metadata 还指向旧哈希的 generation → ArkApi 判定失效 → 照样去下载，
  预取白做；
- 旧 generation 在镜像里**永远不会被删**（Insert 分支被跳过）→ 每次 ARK 更新每个实例
  多留几百 MB。

改法（`mirror.go` 内，约 20 行）：

```
守卫是否生效 = arkApiInstalled() && !sourceCacheManaged()

sourceCacheManaged():  源里的 ArkApi/Cache 通过 arkcache.Inspect(源CacheRoot, 当前exe哈希)
                       判定有效 —— 即「这份缓存是我们备的」

守卫生效（今天的行为）：Cache 下一切不删不比对，保护 ArkApi 自己的下载物
守卫失效（我们接管后）：
    · generations/ 之下的文件 → 仍跳过 Match 的 MD5 比对
      （generation 目录名里带哈希，名字相同即内容相同，没必要每次启动 MD5 几百 MB）
    · cached_key.cache      → 正常比对与回写（文件很小，代价可忽略）
    · Insert 分支（镜像有、源无）→ 正常删除，旧 generation 由此自动清掉
```

「名字相同即内容相同」这条要在代码注释里写明理由：generation 目录名的第一段就是
exe 的 SHA256，内容由 CDN 上同一个 ZIP 决定，是**内容寻址**的；而 `cached_key.cache`
是可变的指针文件，必须逐次对账。

---

## 8. 磁盘布局

```
{BaseDir}/arkapi-cache/                      # 只放中转物，不进镜像
├── <hash>.zip.part                          # 断点续传中（失败**必须保留**）
└── <hash>.zip.meta.json                     # 旁车：source_url / etag / content_length / last_modified

{ServerFilesDir}/ShooterGame/Binaries/Win64/ArkApi/Cache/     # 成品，权威
├── cached_key.cache
└── generations/
    └── <hash>-<pid>-<UnixMilli>-0/
        ├── cached_offsets.cache
        └── cached_bitfields.cache

{BaseDir}/server-files-tmp-<实例名>/.../ArkApi/Cache/          # 同步出来的副本，N 份
```

---

## 9. 多 CDN 与续传

`pkg/download.Fetch` 一次只认一个 URL，多 CDN 回退在 `fetch.go` 里做：

```
for 每个候选 CDN:
    HEAD <cdn>/cache/<hash>.zip
        ├── 404 / 连不上 → 下一个候选
        └── 200 → 记下 Content-Length / ETag / Last-Modified
                  与 <hash>.zip.meta.json 比对：
                      不一致 → 删掉 .part（换源了，绝不能 append）
                      一致   → 保留 .part，续传
                  download.Fetch{Resume:true}
                  最终大小 == Content-Length → 出循环
```

`.part` 跨 CDN 的安全性全靠这个旁车文件（参考文档 §16）。**没有校验和可用** ——
URL 里那个哈希是 exe 的、不是 ZIP 的 —— 所以 `download.Options.Checksum` 留空，
完整性由「大小对得上 + ZIP 能打开 + 条目合白名单 + 两个 `.cache` 通过 §2.2 的结构校验」
四条共同保证。最后那条是本方案相对参考文档的**增强**：拿到 `Cache.h` 之后，
Go 侧可以在写 `cached_key.cache` 之前就断定「ArkApi 一定读得进去」。

重试、超时、代理**复用 `download:` 段**，不新开一套。已知偏差：`pkg/download` 的
backoff 是线性的 `attempt*2s`（`download.go:69`），参考文档 §15 想要的是指数退避到 30s。
先按现状用，不为这一个调用点改公共下载器。

进度：`Progress` 回调 → `logger.Infof` 节流打印（每 3 秒或每 5% 一条）。系统日志流
`GET /api/logs` 是 SSE，前端「系统日志」面板能实时看到，不需要新开推送通道。

---

## 10. ZIP 安全

逐条对齐 `DownloadCacheFiles`（`:641-771`），比它稍严一点：

- 条目数必须在 `[2, 4]`（`:641-642`）；
- 条目名长度 ∈ `(0, 1024]`、不含 NUL（`:661,675`）；
- 名字**只允许**这 4 个，且按整串精确匹配（不是后缀、不是 base name）：
  `cached_offsets.cache` / `cached_bitfields.cache` / `cached_key.cache` /
  `cached_offsets.txt`。其余**整包拒绝**（`:701-705`）。含目录分隔符、`..`、盘符的
  名字自然落进"其余"，一并拒 —— 只做「不合规就拒」，不做「清洗后接受」；
- **同名重复即整包拒绝**（`:692,709` 的 `entrySeen`），`cached_key.cache` 也不例外；
- 每个要提取的条目 `uncompressed_size` 必须 **> 0**、≤ 512 MiB，累计 ≤ 768 MiB
  （`:709-716`）；实际写入字节数必须**恰好等于** `uncompressed_size`（`:754`）。
  Go 侧用 `io.LimitReader` 硬限并回比实际字节数，不单信 ZIP 头（zip bomb）；
- 只提取 `cached_offsets.cache` 与 `cached_bitfields.cache`。ZIP 自带的
  `cached_key.cache` **一律忽略**（ArkApi 自己也只是"允许它存在"，从不落盘，`:690-695`）；
  `cached_offsets.txt` 是调试转储（§2.2），ArkApi 会落盘但从不读，我们不提取。

**不往 `pkg/archive` 加 ZIP**：这里是「ArkApi 白名单解压」，不是通用解压，放通用库里会被
别处误用（`installer/steamcmd_windows.go` 里那个 `extractZip` 是另一套语义）。

---

## 11. 并发与互斥

1. **同机多实例并发启动、同一哈希**：包内 `map[hash]*sync.Mutex`，只允许一个 goroutine
   下同一个哈希，其余等它完成后走 `existing` 快路径。
2. **多进程**（用户手动跑 `asa-server arkapi-cache fetch` 时后台服务也在跑）：
   `{BaseDir}/arkapi-cache/<hash>.lock`，`os.OpenFile(O_CREATE|O_EXCL)` + 内写 PID 与
   时间戳做陈旧锁超时。不引入新依赖。
3. **与 `installer` 的更新互斥**：靠 §6.1 第 2 条的位置保证，不另加锁。
4. **与镜像同步互斥**：我们只在同步**之前**写源目录，而同步自身已由 `mirrorSyncMu`
   串行化，两者不重叠。

---

## 12. `validateSerializedMap` 的 Go 复刻

### 12.1 实现

`serialized.go` 里一个函数，签名 `validateSerializedMap(path string, valueSize int) error`，
规则逐条对着 §2.2 的表写，错误信息带上出错的条目序号与偏移（排障时这比 bool 有用得多）。
读法用 `bufio.Reader` 顺序扫，不整文件读进内存（可能几十 MB 起）。

### 12.2 两个 `T` 的宽度都是确定值

```go
const (
    offsetValueSize   = 8  // intptr_t，Win64
    bitfieldValueSize = 32 // sizeof(BitField)，推导见下
)
```

`BitField` 的定义：

```cpp
struct BitField {
    DWORD64   offset;        // 8 @ 0
    DWORD     bit_position;  // 4 @ 8
    /* 4 字节 padding        @ 12 */
    ULONGLONG num_bits;      // 8 @ 16
    ULONGLONG length;        // 8 @ 24   (in bytes)
};                           // alignof = 8 → sizeof = 32
```

MSVC x64 默认对齐：`ULONGLONG` 要求 8 字节对齐，`bit_position` 之后必须补 4 字节；
总长 32 已是 8 的倍数，尾部无需再补。

⚠️ 那 4 字节 padding **是真的写进文件的** —— `serializeMap`（`Cache.h:88`）按
`sizeof(T)` 把结构体整块 `write` 出去，padding 里是什么就写什么。校验器**不得**
假设它为 0（Go 侧只按宽度跳过，不解释内容，所以天然满足；写测试构造数据时也要
用非零 padding 覆盖这一点）。

两个宽度都是硬编码常量，**不做运行期推断**。真机上若发现 `cached_bitfields.cache`
解析不过（ArkApi 换了结构体定义），错误信息里会带条目序号与偏移，据此改常量即可 ——
这比推断出一个「碰巧也能读完」的错误宽度安全得多。

### 12.3 什么时候跑

- **提取后、写 `cached_key.cache` 之前**跑完整校验（一次，几十 MB 的顺序扫）；
- **启动快路径**（`From=existing`）只做「文件存在 + 非空 + metadata 字段对得上」，
  不重复做完整校验 —— 那份文件是我们自己写完校验过的，且 ArkApi 自己还会再验一遍。

---

## 13. 失败矩阵

| 失败点 | 行为 | 用户可见 |
|---|---|---|
| exe 不存在 / 读不了 | `Ready=false` | 一条 Warn；启动继续 |
| 全部 CDN 都 404 | `Ready=false` | Warn 点名「该 exe 版本 CDN 上没有」；启动继续 |
| 下载中断 | `.part` **保留**，本次 `Ready=false` | Warn；下次启动续传 |
| ZIP 结构违规 | 删 ZIP 与 `.part`，`Ready=false` | Warn 带具体违规项；启动继续 |
| `.cache` 结构校验不过 | 删掉刚建的 generation，**不动**旧 `cached_key.cache` | Warn 带条目序号与偏移；启动继续 |
| 写盘失败（磁盘满） | 清半成品，**不动**旧 metadata | Warn；启动继续 |
| ctx 取消 | 立即返回，`.part` 保留 | 无额外噪声 |

| ③ 已启用但 `Prepare` 失败 | 把镜像里的 `Enable` 写回 `true` | Warn；退化为今天的行为 |
| ③ 已启用但 `config.json` 读不出/非对象 | 不动它（**不代为创建**，§4.4 末尾） | Debug 级；启动继续 |

所有分支的共同点：**源目录要么是「上一份完整可用的缓存」，要么是「什么都没有」，
永远不会是「metadata 指向一个不存在/不完整的 generation」；而镜像里的 `Enable`
永远不会停在「false 且缓存无效」这个组合上。**

---

## 14. 配置

`config.yaml` 新增顶层段（**不放 `linux:` 下** —— ArkApi 在 Windows 上同样要下这份缓存，
而那是本项目的主力平台）：

```yaml
arkapi_cache:
  enabled: true                    # false = 完全不介入，回到今天的行为
  # 顺序**必须**与 ArkApi 的默认顺序一致，尤其是第一个：
  # last_modified 要取自 ArkApi 会优先 HEAD 的那个 CDN，否则它会判定缓存过期并整包重下。
  # 见 docs/ARKAPI_CACHE_PREFETCH_PLAN.md §4.5。
  urls:                            # 留空 = 用内置默认列表，按顺序回退
    - "https://cdn.pelayori.com/cache/"
    - "https://cdn.shadowhunter.co.za/cache/"
    - "https://cdn.shadowhunter-systems.co.za/cache/"
  keep_generations: 0              # 源目录里除当前哈希外额外保留几代（镜像里由 ArkApi 自己清）
  max_size: 805306368              # 768 MiB，与 C++ 侧一致
  # 缓存备好后，把镜像里 ArkApi/config.json 的 AutomaticCacheDownload.Enable 写成 false。
  # 好处：ArkApi 启动时零联网，省掉断网机器上 60~120 秒的 HEAD 重试等待。
  # 代价：缓存万一失效，ArkApi 会每 30~60 秒重查本地、永不启动（由启动超时兜底）。
  # 只在「机器长期无外网 / CDN 被墙」时打开。
  disable_loader_download: false
```

`appconfig` 的四处触点（漏一处就是「配置写了但不生效」）：`config.go` 的 `Config`
结构体 + `defaultConfig()` + `setDefaults()`，以及 `template.go` 里新生成的
`config.yaml` 模板注释。`validate.go` 补一条 `ArkApiCacheConfig.validate()`
（URL 必须是 http/https 且以 `/` 结尾、`max_size > 0`、`keep_generations >= 0`）。

---

## 15. CLI

沿用 `asa-server prefix status|gc`、`asa-server cert status|install` 的形状，在
`internal/actions` 里加 `ArkApiCacheCommand()`，注册进 `main.go` 的 `commonCommands`：

```
asa-server arkapi-cache status            # 当前 exe 哈希、源缓存是否有效、各实例镜像里的 generation
asa-server arkapi-cache fetch [--force]   # 立刻为当前 server-files 的 exe 预取
asa-server arkapi-cache gc [--apply]      # 默认预演；清非当前哈希的 generation 与陈旧 .part
```

`gc` 默认预演、`--apply` 才真删 —— 与 `prefix gc` 一致。

---

## 16. 测试

单元测试（全部可在 Windows 本机跑，不依赖真机与网络）：

| 文件 | 覆盖 |
|---|---|
| `serialized_test.go` | 按 §2.2 手工构造字节流：正常多条目 / `keySize==0` / 超 1 MiB / 越界 / 尾部残字节 / **重复 key** / 空文件 / 单条目边界；32 字节 value 的用例必须用**非零 padding**（§12.2） |
| `zip_test.go` | 条目数越界 / 非法名（`../`、`C:\`、子目录）/ 单条目超限 / 总量超限 / 正常 2 条目 |
| `generation_test.go` | 目录名匹配 `^[0-9a-f]{64}-\d+-\d+-\d+$`；`cached_key.cache` 的字段与 `cache_directory` 前缀；**提交顺序**（key 写入前 generation 必须已完整）；失败时旧 metadata 不被触碰；旧代清理只删非当前哈希 |
| `fetch_test.go` | `httptest`：206 续传、200 时**不 append** 而是重下、`Content-Length` 不符判失败、CDN 回退、旁车不一致时丢弃 `.part`；**§4.5 的 last_modified 取值规则**——从备用 CDN 下载后回头 HEAD 主 CDN，写进 metadata 的必须是主 CDN 那个值，主 CDN 也拿不到时才退用实际下载源的值 |
| `loaderconfig_test.go` | 保序改写只动一个叶子；缺失的 `settings` / `AutomaticCacheDownload` 层级按需补出；`config.json` 缺失 / 非 JSON 对象 / 顶层是 `false` 时**不写不建不报错**；true↔false 往返幂等 |
| `hash_test.go` | modTime/size 变化使缓存失效 |
| `mirror` 侧 | 新增用例：源缓存有效时，镜像里旧的 `cached_key.cache` 被回写、旧 generation 被删；源缓存无效时，守卫仍保护 ArkApi 自己写入的内容（既有行为不回归） |

---

## 17. 真机验收清单

Windows（主力平台）与 Linux 各跑一遍：

1. 全新环境、启用 ArkApi 的实例首次启动 → 日志出现下载进度 → 实例正常起来；
   源目录与镜像里各有一个 generation 与 `cached_key.cache`。
2. 同一实例二次启动 → 断网也能起来，`From=existing`，秒级通过。
3. 第二个实例首次启动 → 无网络请求，缓存由镜像同步复制过去。
4. 下载中途拔网 → `.part` 保留且非空；恢复后再启动 → 从断点续传
   （日志里 Range 起始偏移 == `.part` 大小）。
5. **确认 ArkApi 真的没去下载**（本方案成败的唯一判据）：抓 `arkAsaApi.log`，
   应出现 `The verified local cache is current`（`:394`），
   **不应**出现 `Downloading cache archive`（`:407`）。若出现后者，说明
   `last_modified` 没写对 → 回到 §4.5 逐条核对。
   同时记录 `waitForGamePID` 的耗时，应显著短于今天（今天实测 20 多秒，见 `common.go:129`）。
5b. **断网机器的 60~120 秒**：拔网 + 保留有效缓存 → 启动 → 日志应出现
   `Unable to check ... for updates` 三次与两次 30~60 秒等待，最终
   `Continuing with the verified local cache`。这一条是为了**实测出那个等待到底多长**，
   与 `gamePIDWaitTimeoutArkApi = 3min` 的余量对齐；数值回填 §2.3。
5c. 打开 `disable_loader_download` 重跑 5b → 应完全没有网络相关日志、无等待。
   再把源缓存删掉重启 → 应看到 `Automatic cache download is disabled and no verified
   cache matches`（`:496`），且**我们已经把 `Enable` 写回了 `true`**（检查镜像里的
   `config.json`）→ 验证 §4.4 的不变式闭合。
6. 断网 + 清空源缓存（③ 关闭）→ 启动 → 失败形态应与**今天完全一致**（ArkApi 自己去下、
   大概率失败），而不是被我们卡住的新形态。
7. ARK 更新后（exe 哈希变化）→ 启动 → 重新下载；镜像里旧 generation 被删、
   `cached_key.cache` 被回写（验证 §7 的两处静默故障都不再出现）。
8. 手工把镜像里的 `cached_key.cache` 改坏 → 启动 → 同步应把它修回来。
9. Linux 降权：`ls -l` 确认镜像里 Cache 的属主是运行时用户（由既有的
   `ChownMirrorForRuntime` 覆盖，验证 §6.1 的推论）。
10. 拿一份真实 `cached_bitfields.cache` 跑我们的校验器，确认 32 字节宽度成立
    （文件长度必须能被「8 + keySize + 32」的记录流恰好耗完）。不成立说明 ArkApi
    改了 `BitField` 定义，按错误信息里的偏移改 §12.2 的常量。
11. Linux：清空源缓存让 ArkApi 自行下载一次，确认不是 `HTTP download failed`
    ——即 §19.5 那条证书存储假设不成立。

---

## 18. 分阶段实施

| 阶段 | 内容 | 产出 |
|---|---|---|
| **一** | `pkg/arkcache` 主体（hash/fetch/zip/serialized/generation）+ 单测 | CLI `arkapi-cache fetch` 能手工把源缓存备好 |
| **二** | `internal/instance` 适配器接线（A）+ `mirror` 守卫收窄 + `appconfig` 四处触点 + CLI `status/gc` | 启动路径自动生效（方案主体，③ 不参与） |
| **三** | `loaderconfig.go` + 接线 B + `disable_loader_download` 配置项（默认 false） | 断网机器可再省 60~120 秒 |
| **四** | `installer` 更新完成后预取（复用 update 的 SSE 进度） | 更新完即就绪，首次启动不再等 |
| **五**（可选） | `instance.PrecheckStart` 加一条提示级信息「缓存未就绪，本次启动会先下载 ~xxx MB」 | 用户点「启动」时就知道要等 |

阶段一、二是一次完整交付，**先跑验收第 5 项**：如果那时 `arkAsaApi.log` 已经打出
`The verified local cache is current`，说明主路径成立，三阶段就只是锦上添花；
如果打的是 `Downloading cache archive`，先回 §4.5 / §19.4 把 `last_modified` 对齐，
不要靠上 ③ 去掩盖它 —— 那会把一个「格式没对齐」的 bug 藏成「看起来能用」。
四、五各自独立，可以不做。

---

## 19. 未决问题

1. ~~`sizeof(BitField)`~~ **已关闭**：结构体定义到手，MSVC x64 下 `sizeof = 32`
   （含 4 字节内部 padding），推导与注意事项见 §12.2。宽度改为硬编码常量，不再推断。
2. **CDN 列表的时效性**。三个域名写死在 `pkg/arkcache` 的默认值里（与 ArkApi 的
   `ArkBaseApi.cpp:318-322` 一致），允许 `arkapi_cache.urls` 覆盖。上游若换 CDN，
   用户改配置即可。⚠️ 覆盖时第一个必须与 ArkApi 实际优先查询的那个一致，理由见 §4.5。
3. ~~`InspectLocalCache()` 的完整判定条件~~ **已关闭**：`ArkBaseApi.cpp` 通读完毕，
   判定条件、Last-Modified 比对、`Enable` 开关、重试与等待时长全部落进了 §2.1 / §2.3。
4. ~~`GetFileLastModified` 的返回格式~~ **已关闭**：`Requests.cpp:1019` 直取原始
   `Last-Modified` 头，不做任何再格式化；`DownloadFile` 同样（`:1111-1113,1186`）。
   写法与两条附带约束（跟随重定向、CDN 只在抛异常时才切换）见 §4.5。
5. **Wine prefix 的 Windows ROOT 证书存储是否被填充**（§1 第 5 条）。读码推断出的
   风险，未实测。验证很便宜：Linux 上清掉源缓存、让 ArkApi 自己下一次，看
   `arkAsaApi.log` 是「HTTP download failed」还是正常进度。结论回填 §1。
   注意这条**不影响本方案是否要做** —— 无论成立与否，预取都是解法而不是受害者。

---

## 20. 实现相对本文原稿的偏差

落地记录。原稿的判断绝大部分照做，以下是实际写出来时改了的地方与理由。

### 20.1 保序 JSON 落在 `pkg/jsonx`，`plugindata` 改为引用

§5 说「先把那套 token 流保序读写下沉到 `pkg/`（`pkg/jsonx` 或本包内导出）」。
选了前者：`pkg/jsonx` 提供 `Object`/`Member`/`ParseObject`/`Encode`/`Get`/`Set`，
`internal/plugindata/configmerge.go` 里的 `orderedObject` 现在是 `jsonx.Object`
的类型别名，`parseOrderedObject`/`encodeOrdered` 退化成两行转发。**合并策略
（`mergeOrdered`）仍留在 `plugindata`** —— 那是插件配置特有的取值规则，不是通用能力。

### 20.2 阶段四提前做了，但落点不是 `installer`

§18 把「更新后预取」列为阶段四，§4.3 写的是在 `installer` 里做。实际做不到：
`installer` 在依赖上位于 `instance` **之下**（`instance` import 它），让它反过来调
适配器会成环。改为在三个**编排层**调用点各接一行
`instance.PrefetchArkApiCacheAfterUpdate(ctx, w)`：

| 调用点 | 场景 |
|---|---|
| `actions.ActionUpdate` | CLI `asa-server update` |
| `actions.InstallBaseEnvironment` | `asa-server setup` 与 Fyne 首次安装向导 |
| `updatemanage.Manager` | `POST /api/server/update`（进度抄进既有的 SSE） |

三处都在 `VerifyServerInstallation` 之后、"完成"消息之前，失败只写一行，更新照样算成功。

### 20.3 阶段五未做

`instance.PrecheckStart` 的「本次启动会先下载 ~xxx MB」提示没有实现。它需要在
precheck 阶段就发一次 HEAD 拿体积，而 precheck 今天是**同步**跑在
`/api/server/:name/start` 的 CAS 之前的（见 CLAUDE.md 里 shared prefix 那段），
往里塞一次网络往返会让这个接口的延迟依赖 CDN 可达性。原稿也标了"可选"。

### 20.4 `Result` 多了一个 `LastModified`

§5 的 `Result` 没有这个字段。加上是因为它是 §17 第 5 项的自查入口：
`asa-server arkapi-cache status` 会把它原样打出来，为空时额外警告一句
「ArkApi 的 HEAD 一旦成功就必然判定不相等并整包重下」。缺了它，用户只能去翻
`cached_key.cache` 的原文。

### 20.5 提交成功后删掉中转 ZIP

§8 的磁盘布局只列了 `.part` 与 `.meta.json`，没说成品 ZIP 怎么办。实现里在
generation 提交成功之后 `os.Remove(<hash>.zip)`：缓存已经落进源目录了，再留一份
几百 MB 的压缩包纯属占盘。`GC` 也会清它，以及缓存已就绪时那个同哈希的旁车。

失败路径按 §13 的矩阵区分：ZIP **结构**违规（`errZipRejected`）连 ZIP 带 `.part`
一起删（续传一个坏包没有意义），其余失败一律保留 `.part`。

### 20.6 进程间锁有等待上限

§11 只说了「陈旧锁超时」。实现另加了 60 秒的**等待**上限：拿不到锁就按失败降级
（回到今天的行为，ArkApi 自己去下），而不是把一次启动无限期挂在别的进程的下载上。
陈旧判据仍是锁文件里那个时间戳超过 30 分钟。

### 20.7 `Content-Length` 缺失时直接换下一个 CDN

原稿没提这一档。没有长度就没有「下完了没有」的判据 —— 而那是本方案四条完整性
保证里的第一条（§9），没有校验和可以替代它。所以 HEAD 拿不到 `Content-Length`
与拿到 404 同等对待。

### 20.8 `mirror` 守卫的实现形状

§7 的伪代码写成一个布尔。实现里是两个，因为 Match 分支要区分三种情况：

```go
arkApiManaged := arkApiInstalled() && sourceCacheManaged()  // 我们接管了
arkApiCache   := arkApiInstalled() && !arkApiManaged        // 今天的守卫
```

- `arkApiCache` → Insert / Match 两个分支都整个跳过（既有行为，未改）；
- `arkApiManaged` → 只在 Match 分支跳过 `generations/` **之下的文件**
  （`isUnderArkApiGenerations`），`cached_key.cache` 与 Insert 分支都正常走。

回归护栏是 `internal/mirror/arkapi_cache_sync_test.go` 的两个用例：接管时镜像里
过期的 `cached_key.cache` 被回写、旧 generation 被删；未接管时 ArkApi 运行期写入的
东西一个都不能少。

### 20.9 CLI `fetch --force` 的做法

§15 只写了有这个 flag。实现是「把 `cached_key.cache` 改名藏起来 → `Prepare` →
失败就原样放回去」，**不删 generation**。理由与 §4.4 那条硬规则同源：手动路径上
同样不能出现「metadata 指向一个不存在的 generation」。

### 20.10 单测比 §16 多一个文件

`pkg/arkcache/prepare_test.go`：`Prepare`/`GC` 的端到端用例（httptest 起一个假 CDN
吐真 ZIP），覆盖幂等快路径、失败时不动已有 metadata、半成品被清干净、ctx 取消不发
请求、GC 只删该删的。§16 的表按文件拆分单元，但「提交顺序」这条不变式只有端到端才
能真的钉住。

### 20.12 快路径要重新校验 Last-Modified（原稿漏了的一条）

原稿的 `Inspect`（§5、§12.3）只比 **exe 哈希**，`Prepare` 的快路径也就只比哈希。
第一版照做了，然后发现这不够 —— 它和 §2.3 承重件 ② 是矛盾的：

> ArkApi 判定本地缓存可用有**两个**条件，哈希只是第一个；第二个是 HEAD 回来的
> `Last-Modified` 与 `cached_key.cache` 里的**逐字相等**（`ArkBaseApi.cpp:371-390`）。

于是有一个原稿没覆盖的形态：**CDN 为同一个 exe 版本重发一次包**。哈希没变，
`Last-Modified` 变了。我们报「已就绪」，ArkApi 一比对判定过期，转头自己整包重下
——预取白做，而且是悄无声息地白做（日志里只有一句"已就绪"）。§4.3 说的
「哈希只在更新时变，没有别的触发源」只对哈希成立，对 `last_modified` 不成立。

补法是让快路径自己先去问一次，也就是把 ArkApi 那次比对提前到我们这边做：

```
Inspect 说 Ready
  └─ Revalidate 打开 → HEAD 主 CDN 拿 Last-Modified（上限 10s）
        ├─ 拿到 && == metadata.last_modified → 采用          From=existing
        ├─ 拿到 && != metadata.last_modified → **重新获取**   From=refresh
        └─ 拿不到（断网 / CDN 挂）           → 采用          From=existing
```

三条不能动的规则：

1. **HEAD 失败绝不使缓存失效**（`acceptable` 里 `wantLM == ""` 那一支）。判死的
   后果是白下一遍，下不成还会让一台本来能起的机器起不来 —— 这是加速，不是新的
   失败点。
2. **只问第一个 CDN**。判定权在 ArkApi 手里，而它实践中只问列表第一个（理由同
   §4.5 第 2 条）。`PrimaryLastModified` 因此是导出的，`status` 拿它报「新鲜度」。
3. **没有开关**。原先想按 `disable_loader_download` 决定要不要校验（那种模式下
   ArkApi 不联网，比了也白比），但 ③ 已经整条作废（§22）——ArkApi 的自动下载
   关不掉，它那次 HEAD 一定会发生，我们这次提前比对也就一定有意义。断网的机器
   本来就要被 ArkApi 自己那三轮 HEAD 拖掉 60~120 秒，我们这 10 秒是噪声。

重新获取失败时仍然**不动**已有的 `cached_key.cache`（§4.4 的硬规则原样适用）：
宁可留着一份 ArkApi 会判定过期的旧缓存（那退化成今天的行为），也不能把它弄没。

用例在 `pkg/arkcache/revalidate_test.go`，六条，包含"探测失败必须沿用本地"和
"重新获取失败不得弄坏旧的"这两条护栏。

### 20.11 尚未验证

- **§17 第 1~9、11 项**，其中第 5 项是本方案成败的唯一判据（第 10 项已由 §21 关闭）；
- **§19.5 的 Wine ROOT 证书存储假设**，仍未实测。

---

## 21. 真机实测（Windows，2026-09-05）

一次 `asa-server arkapi-cache fetch`，源目录里原有一份属于旧 exe 的
`cached_key.cache`（ArkApi 自己留下的）。

**HEAD 主 CDN**（`https://cdn.pelayori.com/cache/<hash>.zip`）：

```
HTTP/1.1 200 OK          Content-Type: application/zip
Content-Length: 16978868          Accept-Ranges: bytes
Etag: "1ee05df0145c2853429101904a1e7e53-3"
Last-Modified: Tue, 25 Aug 2026 18:20:41 GMT
```

三条设计前提就地坐实：主 CDN 上确实有当前 exe 版本的包；`Accept-Ranges: bytes`
（§9 的续传成立）；`Last-Modified` 与 `ETag` 都有（§4.5 的逐字比对与 §9 的旁车
都有可用的值）。

**包本身比预想的小得多**：16.2 MB，不是 §14 里 768 MiB 上限暗示的量级。上限照留
（那是抄 C++ 侧的），但「几百 MB 的缓存文件」这个说法（§6.1 末尾、§8）只对解压后
的 `cached_offsets.cache` 成立。

**产物**（全部符合 §2.1 / §8）：

```
Cache/cached_key.cache
  {"version":1,"executable_hash":"1bafa4aa…f404",
   "last_modified":"Tue, 25 Aug 2026 18:20:41 GMT",
   "cache_directory":"generations/1bafa4aa…f404-41704-1788537750573-0"}
Cache/generations/<gen>/cached_offsets.cache     47,706,935 B
Cache/generations/<gen>/cached_bitfields.cache      962,591 B
{BaseDir}/arkapi-cache/<hash>.zip.meta.json      （中转 ZIP 已按 §20.5 删除）
```

`last_modified` 与 CDN 的原始头**逐字节相同**；旧 exe 的那一代已被 `pruneGenerations`
删掉；`.part` / `.lock` 都没有残留。

**§19 第 1 条（`sizeof(BitField)`）由此彻底关闭**：真实的 `cached_bitfields.cache`
被 `validateSerializedMap(path, 32)` **恰好读完**（§2.2 要求 `bytesRemaining` 归零），
真实的 `cached_offsets.cache` 同样以宽度 8 恰好读完。962,591 与 47,706,935 这两个
长度能被「8 + keySize + 宽度」的记录流整除到零，换任何别的宽度都对不上 ——
硬编码常量成立，§17 第 10 项关闭。

---

## 22. ③（`disable_loader_download`）作废：ArkApi 根本没有 config.json

**2026-09-05 真机（Windows）证伪，功能已整条移除。**

### 22.1 证据

```
E:/asa_server_data/server-files/ShooterGame/Binaries/Win64/ArkApi/
    AsaApi.dll   AsaApi.pdb   Cache/   Plugins/   pdbignores.txt
```

**没有 `config.json`**，源目录与实例镜像里都没有。而同一台机器上 ArkApi
**加载成功**（§0 的日志），并且照常打出了
`Checking … for an updated cache archive (attempt 1/3)` ——
也就是说自动下载确实是开着的默认值 `true`，而我们够不到那个开关。

### 22.2 §4.4 末尾那条推断是错的

原文写的是：

> `GetConfig()`（`:563`）在 `config.json` 不存在时返回的是 JSON `false`，紧接着
> `:247` 对它调 `.value("settings", ...)` 会抛 `type_error` —— 而那两行在 `try` 块
> **之外**。也就是说 **config.json 缺失或非 JSON 对象会让 ArkApi 在任何日志之前就崩**。

真机反证：文件不存在，ArkApi 没崩，一路跑到 `API was successfully loaded`。
那段控制流的实际形状与读码推断不符（大概率 `GetConfig()` 在缺文件时返回的是
空对象而非 `false`，或者 `:247` 有别的兜底）。**这条推断不要再拿去做任何设计依据。**

### 22.3 为什么不能"那就替它建一个"

原稿自己给出了答案，而且这个理由**不因 22.2 被证伪而失效**：代为创建一份
config.json，等于凭空替上游组件生成一个我们并不掌握其 schema 的配置文件。
ArkApi 版本一换、字段语义一变，我们写出去的东西就可能让它以别的方式出错，
而现象只会是"装了这个管理器之后 ArkApi 就不对劲"。`SetAutomaticDownload` 的
「文件不存在就什么都不做」是对的，只不过在真机上它**永远**命中这一支——
于是整个功能是死代码。

### 22.4 移除清单

| 删掉 | 说明 |
|---|---|
| `pkg/arkcache/loaderconfig.go` + 测试 | `SetAutomaticDownload` 整体 |
| `internal/instance/server.go` 的【B】块 | 连同 `cacheReady` 变量与两个 import |
| `appconfig` 的 `DisableLoaderDownload` | 结构体字段 / `setDefaults` / `config.yaml` 模板 |
| `arkcache.Request.Revalidate` | 唯一会传 `false` 的调用方没了，改为恒定重新校验（§20.12） |
| `pkg/jsonx` | 抽出它就是为了给 `loaderconfig.go` 用；`plugindata` 已改回原来的内联实现 |

### 22.5 留下来的结论

**方案主体不依赖 ③，而且主体已经验收通过。** ③ 原本只用来省掉断网机器上那
60~120 秒的 HEAD 重试等待；现在那 60~120 秒是省不掉的，只能靠
`gamePIDWaitTimeoutArkApi = 3min` 兜住（余量约一半，§2.3 第 2 条的判断仍然成立，
也正是 §6.2 决定不收紧那个超时的理由）。

§17 第 5b 项（实测那个等待到底多长）因此**变得更值得做**，而不是更不值得。
