# Steam Linux Runtime 预下载方案（Linux setup 加速）

> 目标：把 umu 初始化时那次 150~190 MB 的 Steam Linux Runtime 下载，从「umu 内部的
> Python/urllib3」搬到「我们自己的 `pkg/download`」，用可控的重试 / 断点续传 / 代理
> 把首次 `asa-server setup` 的失败率和耗时压下来。
>
> 硬约束：**这是加速，不是新的失败点**。预取的任何一步失败，都必须无声降级回今天
> 的行为（umu 自己去下），绝不能让一台原本能装成的机器装不成。

## 0. 实现状态

**方案 A′ 已实现**（2026-08-29）。落地文件：

- `internal/runner/steamrt.go`（无 build tag）+ `internal/runner/steamrt_linux.go`
- `internal/runner/steamrt_test.go`：变体映射 / 归档名 / SHA256SUMS 解析 / token 校验，9 条全绿
- `internal/runner/umu_linux.go`：`ensureRuntime` 接线；`steamLinuxRuntimeReady` 改用
  `steamrtForProton`（与预取共用同一份映射）；`warmPrefix` 增 `prefetched` 参数
- `config.yaml` 新增 `linux.steamrt_prefetch`（默认 `true`）

`go build ./...`（Windows）与 `CGO_ENABLED=0 GOOS=linux go vet ./...` 均通过。
**§8.2 / §8.3 的真机验证尚未执行** —— 本机是 Windows，Linux 侧行为仍待实测。

实现相对本文原稿的偏差记录在 §10。

---

## 1. 问题

`runner.EnsureRuntime()` → `warmPrefix()` 里那一次 `umu-run wineboot --init`
（`internal/runner/umu_linux.go:258`）是整个 Linux 初始化中唯一允许联网抓运行时的调用，
注释写得很清楚：

```go
// Deliberately no UMU_RUNTIME_UPDATE=0 here: this is the one
// invocation that must be allowed to fetch a missing runtime.
```

这一步内部会由 umu 从 `repo.steampowered.com` 拉 Steam Linux Runtime 镜像：

| 变体 | 归档 | 实测大小 |
|---|---|---|
| steamrt3（sniper） | `SteamLinuxRuntime_sniper.tar.xz` | 193,105,964 B（184.2 MiB） |
| steamrt4 | `SteamLinuxRuntime_4.tar.xz` | 157.3 MiB |

这个下载有三个问题：

1. **不归我们管**。它走 umu 自带的 urllib3，`config.yaml` 的 `download.github_proxy` /
   `download.http_proxy` / `download.retries` 一个都不生效。唯一能透进去的只有
   `*_PROXY` 环境变量（`launchEnvAllowed` 放行了它们，`internal/runner/runner_linux.go:211`）。
2. **失败代价大**。超时后 `_install_umu` 抛异常 → umu-run 非零退出 → `warmPrefix` 的
   后置校验发现前缀里没有 `system.reg` → 整个 `EnsureRuntime` 报错，用户从头再来一遍
   （包括已经下好的 GE-Proton 判断，虽然那部分是幂等的）。
3. **没有进度**。用户看到的只有 umu 打的一行 `Downloading steamrt3 (…), please wait...`，
   之后可能是十几分钟的静默。

`pkg/download` 已经把这些都解决了（重试 + backoff、`.part` 断点续传、`Checksum` 校验、
`http_proxy`、`Progress` 回调）。缺的只是「怎么把下好的东西交给 umu」。

---

## 2. 取证：umu 1.4.4 到底怎么装运行时

以下全部来自 `umu-launcher` tag `1.4.4`（就是 `defaultUmuVersion` 钉住的版本，
`internal/runner/runner.go:339`）的 `umu/umu_runtime.py` / `umu/umu_util.py` /
`umu/umu_consts.py`，以及对 `repo.steampowered.com` 的实测。

### 2.1 路径常量（`umu_consts.py`）

```python
XDG_CACHE_HOME = Path(os.environ["XDG_CACHE_HOME"]) if ... else Path.home() / ".cache"
XDG_DATA_HOME  = Path(os.environ["XDG_DATA_HOME"])  if ... else Path.home() / ".local" / "share"
UMU_CACHE = XDG_CACHE_HOME / "umu"          # 下载中转
UMU_LOCAL = XDG_DATA_HOME  / "umu"          # 运行时安装位置（UMU_FOLDERS_PATH 可覆盖）
```

对本项目而言，`runtimeEnv()` 会把 `XDG_*` 全部剥掉、把 `HOME` 改写成 runtime 用户的家目录
（`internal/runner/runtimeuser_linux.go:579`），`inheritedEnv()` 的白名单里也根本没有
`XDG_*`。所以在我们的进程树里恒等于：

```
UMU_CACHE = {runtimeHomeDir(cfg)}/.cache/umu
UMU_LOCAL = {runtimeHomeDir(cfg)}/.local/share/umu
```

`runtimeHomeDir()` 已经把「降权 / 不降权」两种情况统一了，直接复用即可。

> ⚠️ `UMU_FOLDERS_PATH` 以 `UMU_` 开头，被 `launchEnvAllowed` 放行。运维如果设了它，
> **`UMU_LOCAL` 会搬家但 `UMU_CACHE` 不会**（后者只跟 `XDG_CACHE_HOME`/`HOME` 走）。
> 这一点在下面选方案时是加分项。

### 2.2 变体 → 归档名映射（**一处必须纠正的前提**）

`umu_runtime.py` 的映射表与归档名推导：

```python
RUNTIME_VERSIONS.update({
    "1391110": UmuRuntime("soldier",        "steamrt2",       "1391110", "x86_64"),
    "1628350": UmuRuntime("sniper",         "steamrt3",       "1628350", "x86_64"),
    "4183110": UmuRuntime("steamrt4",       "steamrt4",       "4183110", "x86_64"),
    "4185400": UmuRuntime("steamrt4-arm64", "steamrt4-arm64", "4185400", "aarch64"),
})

if codename.removeprefix("steamrt").removesuffix("-arm64").isdigit():
    archive = f"SteamLinuxRuntime_{codename.removeprefix('steamrt')}.tar.xz"
else:
    archive = f"SteamLinuxRuntime_{codename}.tar.xz"
```

代入：

| Proton 代 | `require_tool_appid` | codename | variant（= `UMU_LOCAL` 下目录名） | 归档名 |
|---|---|---|---|---|
| GE-Proton 9 / 10 | `1628350` | `sniper` | `steamrt3` | `SteamLinuxRuntime_sniper.tar.xz` |
| GE-Proton 11 | `4183110` | `steamrt4` | `steamrt4` | **`SteamLinuxRuntime_4.tar.xz`** |

**steamrt4 的归档名是 `SteamLinuxRuntime_4.tar.xz`，不是 `_sniper`。** 实测目录列表印证：

```
/steamrt4/images/4.0.20260805.254769/
  SteamLinuxRuntime_4.tar.xz          157.3 MiB
  SteamLinuxRuntime_4-arm64.tar.xz     85.9 MiB
  SHA256SUMS  BUILD_ID.txt  VERSION.txt
/steamrt3/images/3.0.20260805.254768/
  SteamLinuxRuntime_sniper.tar.xz     184.2 MiB
  SHA256SUMS  BUILD_ID.txt  VERSION.txt
```

当前默认 `defaultProtonVersion = "GE-Proton10-34"` → 只需要 **steamrt3**。

**变体不要靠版本号字符串前缀猜**：umu 自己是读 `{PROTONPATH}/toolmanifest.vdf` 的
`require_tool_appid`，再查上面那张表。我们应该照做（见 §4.2），这样 GE-Proton 换代时
不会漂移。今天 `steamLinuxRuntimeReady()` 里的 `strings.HasPrefix(protonVersion, "GE-Proton10-")`
判断是一个只在「版本名规范」时才成立的近似，正好可以借这次一起收敛。

### 2.3 下载流程（`_install_umu`）

```python
base_url = f"https://{host}/{variant.removesuffix('-arm64')}/images/{version}/"
# 1) SHA256SUMS  → 取 archive 的 sha256
# 2) BUILD_ID.txt → buildid，用于给缓存文件命名
parts        = tmp / f"{archive}.{buildid}.parts"
cached_parts = UMU_CACHE / f"{archive}.{buildid}.parts"

if cached_parts.is_file():
    log.info("Found '%s' in cache, resuming...", cached_parts.name)
    headers = {"Range": f"bytes={cached_parts.stat().st_size}-"}
    parts = cached_parts.rename(f"{mkdtemp(dir=UMU_CACHE)}/{parts.name}")
    with parts.open("rb") as fp:
        hashsum = file_digest(fp, hashsum.name)      # 用已有内容重建 hash 进度
else:
    log.info("Downloading %s (%s), please wait...", variant, version)

resp = http_pool.request(GET, f"...{archive}", preload_content=False, headers=headers)
if resp.status not in {OK, PARTIAL_CONTENT, REQUESTED_RANGE_NOT_SATISFIABLE}:
    raise HTTPError(...)
if resp.status != REQUESTED_RANGE_NOT_SATISFIABLE:
    hashsum = write_file_chunks(parts, resp, hashsum)  # 以 "ab+" 追加写
...
if hashsum.hexdigest() != digest:
    cached_parts.unlink(missing_ok=True)
    raise ValueError("Digest mismatched: ...")
log.info("%s: SHA256 is OK", archive)
```

解压与落位：

```python
extract_tarfile(Path(tempdir, archive), Path(tempdir))
steamrt, *_ = archive.split(".tar.xz")        # "SteamLinuxRuntime_sniper"
exchange(Path(tempdir, steamrt), local)       # renameat2(RENAME_EXCHANGE)
ret = check_runtime(local, runtime_ver)       # pressure-vessel/bin/pv-verify
if not ret:
    write_install_marker(local)               # 写 <local>/.installed.ok
local.joinpath("umu").symlink_to("_v2-entry-point")
```

即：tar 内顶层目录名 = `SteamLinuxRuntime_sniper/`，最终落在 `UMU_LOCAL/steamrt3/`。

### 2.4 「要不要下载」的判定（`setup_umu`）

```python
if not has_runtime_installed(local) and local.is_dir():
    ret = check_runtime(local, runtime_ver)   # 老装法没 marker → 现场 pv-verify 补写
    if not ret:
        write_install_marker(local)

version = GET f"https://{host}/{variant}/images/latest-public-beta.txt"

if not has_runtime_installed(local):          # marker 文件 .installed.ok
    _restore_umu(...)                         # → _install_umu，全量下载
    return

if os.environ.get("UMU_RUNTIME_UPDATE") == "0":
    log.info("%s updates disabled, skipping", runtime_ver[1])
    return

_update_umu(...)                              # 校验目录结构 → 比对 VERSIONS.txt 版本
```

三个关键结论：

1. **判定入口是 `.installed.ok` 这个 marker 文件**，不是目录内容。
2. 但 marker 缺失时 umu 会**自己跑 pv-verify 并补写 marker**——所以预置方案不需要知道
   marker 叫什么，只要解压出来的树能通过 `pv-verify` 即可。
3. `UMU_RUNTIME_UPDATE=0` 只在「marker 已存在」之后才起作用。日常启动
   （`umuCommandLine`，`internal/runner/runner_linux.go:164`）本来就设了它；只有
   `warmPrefix` 那一次故意没设。

实测：`latest-public-beta.txt` 与同目录 `VERSION.txt` 内容一致
（steamrt3 = `3.0.20260805.254768`，steamrt4 = `4.0.20260805.254769`），
`_update_umu_platform` 比对的就是这个值与归档内 `VERSIONS.txt` 里 `sniper` 行的版本。

### 2.5 ⚠️ 实测：CDN 对「越界 Range」返回 200 而不是 416

这是决定方案形态的一条硬事实。对 `repo.steampowered.com`（`Server: Google-Edge-Cache`）：

```
文件长度                       193,105,964
Range: bytes=193105864-   =>   206  Content-Range: bytes 193105864-193105963/193105964
Range: bytes=193105964-   =>   200  Content-Length: 193105964     ← 越界，返回全量！
Range: bytes=193105974-   =>   200  Content-Length: 193105964
```

含义：如果我们把**完整**的归档文件放进 `UMU_CACHE` 当作 `.parts`，umu 会

`Range: bytes={完整长度}-` → 拿到 **200 + 全量 body** → 因为 `status != 416` 而进入下载分支
→ `write_file_chunks` 以 `"ab+"` **追加** 193 MB → 最终文件变成两份拼接
→ sha256 不匹配 → `raise ValueError("Digest mismatched")` → **安装直接失败**。

也就是说，「把完整文件塞进缓存」这个最直觉的做法不是「不起效」，而是**会把本来能装成的
机器弄挂**。方案设计必须绕开它（见 §4.3 的截尾处理）。

---

## 3. 方案选择

| | **A′ 预置 umu 下载缓存（截尾 1 MiB）** | **B 预置解压好的运行时目录** |
|---|---|---|
| 落点 | `{runtimeHome}/.cache/umu/{archive}.{buildid}.parts` | `{runtimeHome}/.local/share/umu/{variant}/` |
| 需要 xz 解码 | ❌ 不需要 | ✅ 需要（Go 无 stdlib xz） |
| 需要复刻 umu 布局语义 | ❌ 不需要（解压/校验/marker/软链全由 umu 做） | ✅ 需要（顶层目录剥离、`umu → _v2-entry-point`、mtree 可验证） |
| 归档完整性谁负责 | umu 自己按 `SHA256SUMS` 校验（我们额外先验一次） | 我们负责；umu 只跑 pv-verify |
| 需要 chown 的对象 | 1 个目录 + 1 个文件 | 解压出来的数万个文件 |
| 受 `UMU_FOLDERS_PATH` 影响 | ❌ 否 | ✅ 是（运维设了就打空） |
| wineboot 阶段仍需联网量 | 元数据 + **1 MiB** | 仅元数据（且可用 `UMU_RUNTIME_UPDATE=0` 完全跳过） |
| 主要风险 | 依赖 umu 的 `.parts` 缓存命名与 Range 续传语义 | 依赖 pv-verify 对我们解压结果的接受度（mtime/mode） |
| 失败时的降级 | umu 全量下载（今天的行为） | umu 全量下载（今天的行为） |
| 代码量估计 | ~150 行 | ~300 行 + 新依赖 |

**结论：选 A′。**

理由：A′ 让 umu 继续做**唯一**有权安装自己运行时的一方——解压、校验、写 marker、建软链
全部不变，我们只负责「让字节提前躺在它要找的地方」。这把耦合面收窄到一个文件名规则上，
而且不引入 xz 依赖、不需要动 `pkg/archive`、不需要赌 pv-verify 能接受我们复刻出来的
目录树。B 方案的 §5 完整设计保留在附录，供将来 umu 改掉缓存机制时切换。

---

## 4. 方案 A′ 详细设计

### 4.1 新增文件

```
internal/runner/steamrt.go         # 纯逻辑：变体映射、SHA256SUMS 解析、缓存文件命名
                                   #   不加 build tag —— 与 pkg/archive 同一个理由：
                                   #   这些是纯字符串处理，Windows 上也要能跑单测
internal/runner/steamrt_linux.go   # 落盘实现：HTTP 取元数据、download.Fetch、截尾、chown
```

### 4.2 变体解析：读 toolmanifest.vdf，而不是猜版本号

```go
// steamrtVariant 描述一个 Steam Linux Runtime 变体。
type steamrtVariant struct {
    Variant  string // "steamrt3" / "steamrt4"，同时是 UMU_LOCAL 下的目录名
    Codename string // "sniper" / "steamrt4"
    Archive  string // "SteamLinuxRuntime_sniper.tar.xz" / "SteamLinuxRuntime_4.tar.xz"
}

// steamrtByAppID 是 umu 1.4.4 umu_runtime.py 的 RUNTIME_VERSIONS 表。
// 只收录 x86_64：ARK 服务端没有 arm64 版本，收录 steamrt4-arm64 只会制造
// 一条永远走不到、也永远没人验证的分支。
var steamrtByAppID = map[string]steamrtVariant{
    "1628350": {"steamrt3", "sniper",   "SteamLinuxRuntime_sniper.tar.xz"},
    "4183110": {"steamrt4", "steamrt4", "SteamLinuxRuntime_4.tar.xz"},
}

// steamrtForProton 解析 GE-Proton 需要哪个运行时变体。
//
// 权威来源是 {protonPath}/toolmanifest.vdf 里的 require_tool_appid —— umu 自己
// 就是这么查的，照做才不会在 GE-Proton 换代时漂移。读不到时回落到版本名前缀
// 启发式（今天 steamLinuxRuntimeReady 用的那套），再不行返回 false 表示
// 「不认识，别猜」。
func steamrtForProton(protonDir, protonVersion string) (steamrtVariant, bool)
```

`toolmanifest.vdf` 是几行文本，形如：

```
"manifest"
{
  "version" "2"
  "commandline" "/proton %verb%"
  "require_tool_appid" "1628350"
}
```

用一个 `regexp` 抠 `require_tool_appid` 的值即可，不需要引入 vdf 解析库。

**顺带收敛**：`steamLinuxRuntimeReady()`（`umu_linux.go:391`）目前自己写了一份
`GE-Proton9-/10- → steamrt3、GE-Proton11- → steamrt4` 的 switch。改成复用
`steamrtForProton()`，保证「预取哪个」和「检查哪个已装好」永远是同一个答案。

### 4.3 预取流程

```go
// prefetchSteamRuntime 把 umu 初始化时要下的 Steam Linux Runtime 归档，提前用
// pkg/download 放进 umu 自己的下载缓存。返回 (命中变体, error)。
func prefetchSteamRuntime(ctx context.Context, cfg Config, logf func(string, ...any)) (steamrtVariant, error)
```

步骤：

1. **前置闸门**（任一不满足 → 直接返回「未预取」，无错误）：
   - `cfg.Runtime != "umu"` —— custom 运行时不归我们管
   - `!cfg.AutoDownload` —— 明确要求不联网
   - `!cfg.SteamRTPrefetch` —— 新增开关，见 §6
   - `steamrtForProton()` 认不出变体
   - `steamLinuxRuntimeReady(...)` 已为真 —— 装过了
2. `GET https://repo.steampowered.com/{variant}/images/latest-public-beta.txt` → `version`
3. `GET .../{version}/BUILD_ID.txt` → `buildid`
4. `GET .../{version}/SHA256SUMS` → 找 `archive` 对应的 sha256
5. 清理 `{umuCache}/{archive}.*.parts` 中 buildid 不匹配的陈旧文件
6. `download.Fetch{URL: .../{version}/{archive}, Dest: {umuCache}/{archive}.{buildid}.parts.full,
   Checksum: "sha256:"+digest, Resume: true, Progress: …}`
7. **截尾**：把 `.full` 重命名为最终名，并 `os.Truncate(dest, size - steamrtTailBytes)`
8. `chownPathForRuntime()` 依次作用于 `{runtimeHome}/.cache`、`{umuCache}`、`dest`

第 2~4 步用 `download.Client()`（复用 `http_proxy` 与超时配置）直接发请求，不走
`download.Fetch`——那是给大文件准备的。`SHA256SUMS` 约 280 KiB，直接读全量再逐行解析。

#### 为什么要截尾——以及为什么截尾是安全的

见 §2.5：完整文件会让 umu 发出越界 Range、拿到 200 全量、追加写成两倍长度、
digest 不匹配后**抛异常中断安装**。

截掉尾部 `steamrtTailBytes = 1 << 20`（1 MiB）之后：

```
umu 读到 .parts 大小 = L - 1MiB
  → Range: bytes={L-1MiB}-      → 实测 206 + Content-Range（见 §2.5 的 near-end 探测）
  → 追加最后 1 MiB               → 文件恢复成完整的 L 字节
  → sha256 == SHA256SUMS 的值    → "SHA256 is OK" → 正常解压
```

安全性依据：
- 我们在**截尾之前**已经用 `download.Fetch` 的 `Checksum` 验过完整文件的 sha256，
  所以留在盘上的每个字节都和上游一致，补回来的 1 MiB 也来自同一个 URL。
- 最终判定权仍在 umu 手里——它会重算整个文件的 sha256。我们无法制造出一个
  「校验通过但内容是错的」运行时。
- 1 MiB 而不是 1 字节：留出足够宽的区间，避免踩到任何 CDN 对极小 Range 的边界行为；
  代价是 1 MiB 的传输，可以忽略。

#### 失败绝不阻断

接线处（`ensureRuntime`，`umu_linux.go:83` 一带）：

```go
if err := ensureGEProton(ctx, cfg, logf); err != nil {
    return fmt.Errorf("failed to install %s: %w", cfg.ProtonVersion, err)
}

// Steam Linux Runtime 预取：warmPrefix 那次 wineboot 会让 umu 自己去
// repo.steampowered.com 拉 150~190MB，用的是它内置的 urllib3——我们的重试、
// 断点续传、http_proxy 一个都够不着。先用 pkg/download 拿下来塞进 umu 自己的
// 下载缓存，wineboot 起来时就只剩「续传补最后 1 MiB」。
//
// 失败只降级不阻断：最坏就是回到今天的行为（umu 自己下）。这个优化的全部价值
// 是省时间，为省时间制造一个新的安装失败点是净亏。
prefetched := false
if v, err := prefetchSteamRuntime(ctx, cfg, logf); err != nil {
    logf("Steam Linux Runtime 预下载失败（%v），改由 umu 自行下载", err)
} else if v.Variant != "" {
    prefetched = true
}

if err := warmPrefix(ctx, cfg, logf, prefetched); err != nil { ... }
```

`prefetched` 传进 `warmPrefix` 只用于一处：日志措辞（「正在解压已预下载的运行时」
vs 今天那句「downloading Steam Linux Runtime …」）。

**不要**因为预取成功就给 wineboot 设 `UMU_RUNTIME_UPDATE=0`：A′ 预置的是**下载缓存**，
不是已安装的运行时，marker 尚不存在，`setup_umu` 会走 `_restore_umu` → `_install_umu`
新装分支，`UMU_RUNTIME_UPDATE` 在那条路上根本不参与判定（§2.4 结论 3）。
设它反而会误导后来的读者。（B 方案则相反，见附录。）

### 4.4 权限

`UMU_CACHE` 下 umu 要做的事：`mkdir`、`mkdtemp`、`rename`、`unlink`、以 `"rb"` 读、
以 `"ab+"` 追加写。其中 rename/unlink/mkdtemp 只要**目录**权限，读写才要文件权限。

必须显式处理的原因和 ACL 加固那次是同一个教训（`docs/ACL_PERMISSION_HARDENING_PLAN.md`）：
`ensureRuntimeUser()` 里的 `reconcileRuntimeOwnership` 会把整个 runtime home 递归 chown，
但它跑在 `ensureRuntime` 的**最开头**，而这些目录是我们**之后**才创建的。所以：

```go
// 创建后立刻交给 runtime 用户，别指望下次启动时的 reconcile —— 那时 wineboot 早跑完了
for _, p := range []string{filepath.Join(home, ".cache"), umuCacheDir(cfg), dest} {
    _ = chownPathForRuntime(p)   // 非 root / 不降权时是 no-op
}
```

`chownPathForRuntime` 是单路径非递归的，正合适（`runtimeuser_linux.go:389`）。

另外 `.cache` 目录建议 `0o755`：umu 只需要自己可写，但父路径要能被穿过。

### 4.5 进度输出

`download.Options.Progress` → 节流到「每 2 秒或每 5% 打一行」，写进 `logf`。
`logf` 已经同时落日志和写 `progress io.Writer`，所以 `asa-server setup` 的 stdout 和
`/api/server/update` 的 SSE 都能直接看到百分比——这是今天完全没有的信息。

```
正在预下载 Steam Linux Runtime steamrt3 (3.0.20260805.254768, 184.2 MiB)...
  steamrt3: 15% (28.4/184.2 MiB)
  ...
  steamrt3: 100% (184.2/184.2 MiB)
Steam Linux Runtime 预下载完成，umu 初始化时只需补齐最后 1 MiB
```

### 4.6 幂等与并发

- 整个 `ensureRuntime` 已经在 `runtimeMu` 之下，同进程内不会并发。
- `download.Fetch(Resume: true)` 的 `.part` 机制天然可续；再跑一次 setup 时，
  第 1 步的 `steamLinuxRuntimeReady()` 闸门会直接跳过。
- 若 `.parts` 已存在且大小正好是 `L - 1MiB`，跳过下载（但仍要 chown，因为可能是
  上一次以别的属主留下的）。
- umu 侧的 `flock(UMU_LOCAL/../umu.lock)` 不受影响——我们不碰那个锁保护的路径。

---

## 5. 代码骨架

`internal/runner/steamrt.go`（无 build tag，可在 Windows 上单测）：

```go
package runner

// steamrtHost 是 Valve 官方的运行时仓库。刻意不做成配置项：见 §6。
const steamrtHost = "https://repo.steampowered.com"

// steamrtTailBytes 是预置缓存故意留空的尾部长度。见 §4.3——完整文件会让 umu
// 发出越界 Range，而 repo.steampowered.com 对越界 Range 返回 200 全量而非 416，
// 追加写会把文件搞成两倍长度并让安装以 "Digest mismatched" 失败。
const steamrtTailBytes = 1 << 20

type steamrtVariant struct{ Variant, Codename, Archive string }

var steamrtByAppID = map[string]steamrtVariant{ /* §4.2 */ }

var requireToolAppIDRe = regexp.MustCompile(`(?i)"require_tool_appid"\s+"(\d+)"`)

func steamrtForProton(protonDir, protonVersion string) (steamrtVariant, bool) { ... }

// steamrtCacheName 是 umu 的缓存文件名规则：f"{archive}.{buildid}.parts"
func steamrtCacheName(v steamrtVariant, buildID string) string {
    return v.Archive + "." + buildID + ".parts"
}

// parseSHA256Sums 从 SHA256SUMS 里取 name 对应的十六进制摘要。
// 按字段精确匹配文件名，不用 strings.HasSuffix —— 后者会让
// "SteamLinuxRuntime_4.tar.xz" 这种名字有歧义风险。
func parseSHA256Sums(data []byte, name string) (string, error) { ... }
```

`internal/runner/steamrt_linux.go`：

```go
//go:build linux

func umuCacheDir(cfg Config) string {
    return filepath.Join(runtimeHomeDir(cfg), ".cache", "umu")
}

func prefetchSteamRuntime(ctx context.Context, cfg Config, logf func(string, ...any)) (steamrtVariant, error) {
    // 闸门 → 元数据 → download.Fetch → 截尾 → chown
}

func steamrtGet(ctx context.Context, url string, limit int64) ([]byte, error) {
    // download.Client().Do + io.LimitReader，供 latest-public-beta.txt /
    // BUILD_ID.txt / SHA256SUMS 三个小文件共用
}
```

---

## 6. 配置

新增一个开关，默认开：

```go
// LinuxConfig
// SteamRTPrefetch：umu 初始化前是否用 pkg/download 预取 Steam Linux Runtime 归档。
// 默认 true。关掉就完全回到 umu 自己下载的行为，排障时用。
SteamRTPrefetch *bool `mapstructure:"steamrt_prefetch"`
```

用 `*bool` 而不是 `bool`，因为默认值是 true——零值语义反了会让「没写这一项」
被当成「显式关闭」。（`AutoDownload` 也是 true 默认，可参照它现有的处理方式统一。）

**不新增 steamrt 镜像地址配置**，理由：
- `repo.steampowered.com` 是 Valve 自己的 CDN（实测走 Google Edge Cache），
  国内多数情况可直连；真正的痛点是 umu 内部客户端没有重试和续传，而这正是本方案解决的。
- 需要走代理的用户，`download.http_proxy` 已经覆盖我们这条路径（走 `download.Client()`）。
- umu 自己那条兜底路径也不是没救：`launchEnvAllowed` 放行 `*_PROXY`，运维给 systemd
  unit 加 `Environment=HTTPS_PROXY=...` 即可透进去。这一条值得补进
  `docs/LINUX_DEPLOYMENT.md`。

---

## 7. 风险与边界

| # | 风险 | 影响 | 缓解 |
|---|---|---|---|
| 1 | 我们解析 `latest-public-beta.txt` 到 umu 解析之间上游发了新版 | buildid 变化 → 缓存文件名对不上 → umu 全量下载 | 窗口只有几分钟；只损失一次加速，不影响正确性 |
| 2 | 缓存打空后残留一个 ~190 MB 垃圾文件 | 占盘 | 每次预取前清掉 `{archive}.*.parts` 里 buildid 不匹配的；成功路径上 umu 会自己 rename 走 |
| 3 | 未来 umu 改掉 `.parts` 缓存命名或续传逻辑 | 预取失效 | 各种改法都是**优雅降级**（umu 重新下载）。唯一不优雅的方向是「续传不再发 Range」，可能性极低；升级 `defaultUmuVersion` 时应重跑 §8 的真机验证 |
| 4 | CDN 改变 Range 语义 | 若中段 Range 也返回 200，追加写会导致 digest 不匹配、安装失败 | 已实测 206（§2.5）；这也是 umu 自身续传功能依赖的同一行为，上游一起受影响 |
| 5 | 把 steamrt4 的归档名写成 `_sniper` | 404 → 预取失败降级（不致命，但优化白做） | 归档名由 `steamrtByAppID` 单点决定，配单测 |
| 6 | 磁盘 | 预取期间多占约 190 MB（在 runtime home 所在分区），直到 umu 消化 | 现有「BaseDir 预留 30 GB」的提示不变；注意 umu 解压临时目录也在 `UMU_LOCAL` 同分区 |
| 7 | arm64 | 映射表不收录 → `steamrtForProton` 返回 false → 跳过预取 | 有意为之：ARK 服务端没有 arm64 构建 |
| 8 | `Runtime == "custom"` / `AutoDownload == false` | 不做任何事 | 闸门第 1 步 |

---

## 8. 验证

### 8.1 单测（可在 Windows 上跑，`steamrt.go` 无 build tag）

- `steamrtForProton`：`toolmanifest.vdf` 含 `1628350` → steamrt3/sniper；含 `4183110` →
  steamrt4 且归档名为 `SteamLinuxRuntime_4.tar.xz`（**这条是回归防线**）；
  文件缺失时回落到版本名前缀；未知代次返回 false。
- `parseSHA256Sums`：正常行、`*` 前缀行、`SteamLinuxRuntime_4.tar.xz` 与
  `SteamLinuxRuntime_4-arm64.tar.xz` 共存时不串行、缺失时报错。
- `steamrtCacheName`：`SteamLinuxRuntime_sniper.tar.xz.20260805.254768.parts`。

### 8.2 真机（Linux，必须做，且升级 `defaultUmuVersion` 时重做）

```bash
# 干净起点
rm -rf ~asa-umu-runtime/.local/share/umu ~asa-umu-runtime/.cache/umu
rm -rf {BaseDir}/umu-prefix
asa-server setup 2>&1 | tee /tmp/setup.log
```

判定：
- 日志出现我们的百分比进度行；
- 随后 umu 打出 `Found 'SteamLinuxRuntime_sniper.tar.xz.<buildid>.parts' in cache, resuming...`；
- **不**出现 `Downloading steamrt3 (…), please wait...` 后的长时间静默；
- 出现 `SteamLinuxRuntime_sniper.tar.xz: SHA256 is OK`；
- 出现 `sniper_platform_*: mtree is OK`（pv-verify 通过）；
- `{runtimeHome}/.local/share/umu/steamrt3/.installed.ok` 存在；
- `warmPrefix` 的后置校验通过（prefix 里有 `system.reg`）。

### 8.3 降级路径（同样必须验）

```bash
# 断开对 repo.steampowered.com 的访问后再跑，验证「预取失败不阻断」
```
判定：日志出现「Steam Linux Runtime 预下载失败（…），改由 umu 自行下载」，
且 setup 的成败与打这个补丁之前**完全一致**（网络通就成功，不通就失败在 umu 那一步）。

---

## 9. 改动清单

| 文件 | 改动 |
|---|---|
| `internal/runner/steamrt.go` | 新增：变体映射、`toolmanifest.vdf` 解析、`SHA256SUMS` 解析、缓存命名 |
| `internal/runner/steamrt_linux.go` | 新增：元数据抓取、`download.Fetch`、截尾、chown、进度 |
| `internal/runner/steamrt_test.go` | 新增：§8.1 |
| `internal/runner/umu_linux.go` | `ensureRuntime` 接线；`steamLinuxRuntimeReady` 改用 `steamrtForProton`；`warmPrefix` 增加 `prefetched` 参数（仅影响日志措辞） |
| `internal/runner/runner.go` | `Config` 增加 `SteamRTPrefetch` |
| `internal/appconfig/config.go` | `LinuxConfig.SteamRTPrefetch` + 默认值 |
| `internal/actions/setup.go` / `internal/webapi/actions.go` / `internal/installer/installer.go` | 只需在各自的 `runner.Configure(...)` 处补传新字段 |
| `docs/LINUX_COMPATIBILITY_PLAN.md` | 交叉引用本文一行 |
| `docs/LINUX_DEPLOYMENT.md` | 补一段：给 systemd unit 加 `HTTPS_PROXY` 可救 umu 自己的下载路径 |
| `CLAUDE.md` | `internal/runner/` 目录树补 `steamrt*.go` 一行 |

估计 ~150 行实现 + ~120 行测试。

---

## 10. 实现偏差（相对本文原稿）

1. **`SteamRTPrefetch` 用普通 `bool`，不是 §6 说的 `*bool`。**
   viper 的 `SetDefault("linux.steamrt_prefetch", true)` 已经把「配置里没写 = true」
   解决掉了，与既有的 `linux.auto_download` 完全同构。为同一个问题引入第二种约定
   不划算。代价与 `AutoDownload` 一样：直接构造 `runner.Config{}` 而不填这个字段会
   得到 false（预取关闭），三处 `runner.Configure` 调用点都已显式传入。

2. **新增标记文件 `<dest>.asa-prefetch`，内容 `"<sha256> <截尾后字节数>"`。**
   原稿只说「已存在就跳过」，但那不够：一个大小对不上的残留会让 umu 从错误的偏移
   续传，最后以 `Digest mismatched` **中止安装** —— 那正是这套机制唯一能造成的实质
   伤害。所以「缓存已就绪」的判据必须是「摘要对得上 **且** 截尾后大小对得上」，
   见 `steamrtCachePrepared`。

3. **先下到 `<dest>.full`，校验通过后再 rename + truncate。**
   截尾之后的文件本身是残缺的，不能让它在中途被下一次运行误认成成品；
   `download.Fetch` 的 `.part` 续传作用在 `.full` 上，中断重跑不白下。
   截断失败时删掉 `dest`，理由同上。

4. **`version` / `BUILD_ID` 加了白名单校验（`steamrtSafeToken`）。**
   两者都来自远端并被拼进 URL，`BUILD_ID` 还会成为缓存文件名的一部分；不校验就等于
   让上游（或一个配错的镜像、或中间人）决定我们往哪个路径写文件。

5. **没有给 wineboot 设 `UMU_RUNTIME_UPDATE=0`**，与 §4.3 的论证一致：A′ 预置的是
   下载缓存而非已安装的运行时，marker 尚不存在，`setup_umu` 走的是新装分支，
   该变量在那条路上根本不参与判定。`prefetched` 只用于切换一行日志措辞。

6. **`steamLinuxRuntimeReady` 的签名从 `(protonVersion string)` 改成 `(cfg Config)`**，
   并去掉了内部的 `getConfig()`。它现在和预取共用 `steamrtForProton` 一份映射——
   「预取哪个」和「哪个算已装好」不能有两个答案。认不出代次时仍回落到原来的
   `steamrt*` 宽松 glob。

---

## 附录：方案 B（预置解压好的运行时目录）

保留设计，供 umu 改掉缓存机制、A′ 失效时切换。

**落点**：`{runtimeHome}/.local/share/umu/{variant}/`，内容为 `SteamLinuxRuntime_sniper.tar.xz`
剥掉顶层 `SteamLinuxRuntime_sniper/` 之后的全部条目。

**步骤**：
1. 同 §4.3 第 2~4 步取 version / digest。
2. `download.Fetch` 到 `{BaseDir}/steamrt/{archive}`（临时区，不放 `UMU_CACHE`）。
3. xz 解压 —— Go 无 stdlib 支持，两条路：
   - 引入 `github.com/ulikunitz/xz`（纯 Go，无宿主依赖，但解压速度约为 C 版的 1/3；
     一次性 setup 可接受）；
   - 或调用宿主 `tar -xJf`（快，但要求装了 `xz-utils`，需要给
     `preflight_linux.go` 加一条**建议级**检查，不能是阻断级）。
   推荐前者：`pkg/archive.ExtractTar` 已经有 zip-slip 防护和 strip-prefix，
   套一层 `xz.NewReader` 即可复用。
4. 解压到临时目录后 `os.Rename` 就位（避免半成品被 umu 看见）。
5. 建 `umu -> _v2-entry-point` 软链（可选：`_unwrapped_cmd` 里只有
   `Path(tool_path).joinpath("umu").is_file()` 时才替换，缺了会回落到
   `_v2-entry-point`，两者都在归档里）。
6. **不写** `.installed.ok`：让 umu 自己跑 `pv-verify` 后补写（§2.4 结论 2）——
   这样「我们解压得对不对」由 umu 判定，而不是我们自己宣布。
7. `chownTreeForRuntime()` 整棵交给 runtime 用户。
8. 预置成功时给 `warmPrefix` 的 wineboot 加 `UMU_RUNTIME_UPDATE=0`，
   跳过 `_update_umu` 的 `VERSIONS.txt` 版本比对（与 `umuCommandLine` 的既有做法一致）。
   预置失败则**不加**——那一次仍然必须允许 umu 自己去取。

**B 独有的风险**：
- `pkg/archive.ExtractTar` 目前不保留 mtime。若 `pv-verify` 校验 mtree 时比对时间戳，
  会导致 `check_runtime` 失败 → marker 不写 → umu 全量重下（优雅降级，但优化白做）。
  切到 B 之前必须先给 `ExtractTar` 补 `os.Chtimes(target, hdr.ModTime, hdr.ModTime)`，
  并在真机上确认日志出现 `mtree is OK`。
- `ExtractTar` 用 `hdr.Mode & 0777`，会丢掉 setuid/setgid 位。需确认
  `pressure-vessel/bin/` 下没有依赖这些位的二进制。
- 运维设了 `UMU_FOLDERS_PATH` 时 `UMU_LOCAL` 搬家，预置直接打空。
- 需要 chown 数万个文件，`asa-server setup` 会多出可观的一段 I/O。
