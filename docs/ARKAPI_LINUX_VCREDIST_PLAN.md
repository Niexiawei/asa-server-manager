# ArkApi on Linux —— 把 VC++ 运行时装进 Wine prefix

> 目标：让 `EnableAsaPlugin=true` 的实例在 Linux 上真的能被 `AsaApiLoader.exe` 拉起来。
> 缺的那一块是 **Microsoft Visual C++ Redistributable**：ArkApi 官方要求它，而 Wine 与
> GE-Proton 的 prefix 里都没有原生版本。
>
> 定位：这是 `docs/LINUX_COMPATIBILITY_PLAN.md` §1 目标 5 / §5.12 / §6 风险 11 那条
> 「ArkApi 在 Linux 上不再是非目标，但**不保证能用**」的**继续推进**——把「用户自己
> 试」往前推到「至少依赖是齐的」。**仍然不承诺 ArkApi 在 Wine 下能稳定工作**，见 §6。

---

## 0. 实现状态

> **2026-08-30 追加：ArkApi 端到端已跑通，但缺的不止 VC++ —— 还有一个图形显示。**
> 装好 VC++ 之后 `AsaApiLoader.exe` 仍然「起不来且零输出」，根因是 Wine 连不上 X
> 服务时 `CreateWindow` 失败。补一个显示后 ArkApi 完整加载（V2.03，offsets cache
> 下载成功，Permissions 插件加载，`AShooterGameMode::InitGame` 被调用）。
> **`xvfb` 因此成为 preflight 的阻断级依赖**，详见 §9。

**Level 1 已实现并在真机验证**（2026-08-30，WSL2 Ubuntu + GE-Proton10-34 + umu 1.4.4）。
真机数据推翻了本文原稿的三条假设，代码与设计都已按实测结果改过：

| # | 原稿的说法 | 真机实测 | 结果 |
|---|---|---|---|
| 1 | 注册表键 `Runtimes\x64` 的 `Installed=1` 是**主判据** | GE-Proton 在**全新 prefix** 里就预置了 `Installed=1`、`Version="14.42.34433.0"` | ❌ 判据作废。用它会「永远认为已装好、于是永远不装」。改为只看 PE 头的 Wine 标记（§2.3） |
| 2 | 安装器会跳过 `msvcp140` / `msvcp140_2`（winehq #57518），需要 Level 2 的 CAB 抽取 | 装完 **11/11 全部换成原生**，含 msvcp140、msvcp140_2 | ✅ **Level 2 不需要做**（§2.5） |
| 3 | （未预见） | **微软安装器在 Wine 下必须有一个真实可连的 X 显示**，否则一律 203 退出 | ⚠️ 无头服务器装不上。改为「override 无条件写入 + 安装器条件执行」（§2.6） |

外加一条改变主次判断的发现：**ARK 服务端自己就在 exe 同目录带了 11 个 DLL 里的 9 个
原生版**。Windows 的搜索顺序里应用目录优先于 system32，所以真正承重的是
**DLL override**（让 Wine 别用自己的内建实现），安装器只是补齐游戏没带的
`vcamp140`/`vcomp140` 以及不走应用目录的加载路径。

Level 2（CAB 抽取）**不做**，理由从「等数据」变成了「数据表明不需要」。

落地文件：

- `internal/runner/vcredist.go`（无 build tag）+ `vcredist_linux.go` + `vcredist_windows.go`
- `internal/runner/vcredist_test.go`：13 组用例全绿，含「override 值名必须带 `*` 前缀」
  与「模块清单与 winetricks 逐项一致」两条防漂移测试
- `internal/runner/umu_linux.go`：`ensureRuntime` 在 `warmPrefix` 之后接线（失败只告警）；
  顺带把 steamrt 的进度节流抽成共用的 `downloadProgress`
- `internal/runner/runner_linux.go`：`WineDLLOverrides` 非空时追加 `WINEDLLOVERRIDES`
- `internal/instance/server.go`：ArkApi 实例启动前校验 + 告警，不阻断
- `config.yaml` 新增 `linux.install_vcredist`(true) / `vcredist_url` / `vcredist_sha256` /
  `wine_dll_overrides`

- `internal/actions/verify_arkapi.go` + `internal/installer/verify_arkapi.go`：
  新增 CLI `asa-server verify-arkapi`（别名 `verify_arkapi`），
  `--check-only` 只诊断、`--install-vcredist` 顺带补装

`go build ./...`（Windows）、`CGO_ENABLED=0 GOOS=linux go vet ./...`、
`internal/runner` + `internal/installer` + `internal/appconfig` 单测均通过。

**真机验证结果**（§7.2 全部执行完毕）：

- 有 X 显示时：override 11/11 写入 → 安装器退出 0 → **system32 里 11/11 全部原生**；
- 无 X 显示时：override 11/11 写入 → 安装器**跳过**并打印原因，不浪费一次注定失败的调用；
- 幂等：再跑一次直接短路；
- **回归**：改了共享 prefix 的 DLL 加载顺序之后，普通（非 ArkApi）服务端
  `asa-server verify` 仍在 42 秒内启动并开始监听，无回归；
- **未验证**：ArkApi 本身的端到端启动 —— 该环境没有安装 ArkApi
  （`AsaApiLoader.exe` 不存在），`verify-arkapi` 如实报告「未安装」并拒绝进入启动阶段。

---

## 0.1 现状：已经就绪的部分与缺的部分

启动链路本身**早就通了**，P2/P4 阶段已经做完：

| 环节 | 现状 |
|---|---|
| 选 exe | `internal/instance/server.go:375-381`：`EnableAsaPlugin` 且镜像里有 `AsaApiLoader.exe` → `arkExe` 换成它，`arkAsaApiRunning = true` |
| 启动 | `runner.Run(ctx, arkExe, args, Options{Dir, PTY: arkAsaApiRunning})`，`runner` 对两个 exe 一视同仁（`internal/runner` 包注释） |
| PTY | Linux 侧 `runPTY` 已实现（`runner_linux.go:67`），并已处理降权时 slave pts 的属主 |
| 日志 | `console.CleanScreenOutput(handle.PTY, apiLogFile)` 两平台共用 |
| PID | `SaveAsaServerApiPID` + `waitForGamePID`（按 `AltSaveDirectoryName=` 匹配 cmdline） |
| 插件数据 | `internal/plugindata` 已跨平台，大小写问题 P6 已处理（`casecheck_linux.go`） |

**缺的就一件事**：prefix 里没有 VC++ 运行时。ArkApi 的注入/hook 代码是用 MSVC 编译的，
链接 `vcruntime140.dll` / `msvcp140.dll` 这一族；GE-Proton 的 prefix 里只有 Wine 自己的
**builtin 同名 DLL**，不是微软原生版本（这一条对本方案的成功判据有决定性影响，见 §2.3）。

---

## 1. 取证

### 1.1 ArkApi 官方要求

`ServersHub/Framework-ArkServerApi` 的前置条件只有两条：

- Windows 7 / Windows Server 2008 或更新
- **Microsoft Visual C++ 2019 Redistributable**

没有 .NET 要求，也完全没有提 Linux/Wine/Proton —— 它就是个纯 Windows 项目。

> 注：VC++ 2015 / 2017 / 2019 / 2022 是**同一个** 14.x 运行时家族，向后兼容、共用同一个
> 安装包。装 `vc_redist.x64.exe`（VS 17 通道，当前为 14.44.35211.0）即满足「2019」要求。
> ASA 服务端与 `AsaApiLoader.exe` 都是 x64，**只需要 x64，不需要 x86**。

### 1.2 下载地址与校验 —— 一个能白捡的 SHA256

用户给的地址 `https://aka.ms/vs/17/release/vc_redist.x64.exe` 是个短链。实测跟随重定向：

```
FINAL:  https://download.visualstudio.microsoft.com/download/pr/
        9d270333-8b7b-4f96-9458-6fcdb2ec0b25/
        CC0FF0EB1DC3F5188AE6300FAEF32BF5BEEBA4BDD6E8E445A9184072096B713B/
        VC_redist.x64.exe
LEN:    25,635,768        (24.4 MiB)
LASTMOD: Sat, 01 Aug 2026
```

实测下载后核对：

```
SHA256:  CC0FF0EB1DC3F5188AE6300FAEF32BF5BEEBA4BDD6E8E445A9184072096B713B
URL 段:  CC0FF0EB1DC3F5188AE6300FAEF32BF5BEEBA4BDD6E8E445A9184072096B713B   ← 完全一致
版本:    14.44.35211.0
```

**微软下载 URL 的倒数第二段就是文件的 SHA-256**（大写十六进制）。这意味着：

- 不需要在代码里钉死一个会过期的哈希（`Last-Modified` 是 2026-08，说明它随 VS 服务化更新
  一直在变，钉死等于每次微软更新就把 setup 弄挂）；
- 也不需要「下载不校验」——先解析短链拿到最终 URL，从路径里抠出 64 位十六进制段，
  直接喂给 `download.Fetch` 的 `Checksum`。**自校验，零维护。**

这条正好绕开 `docs/LINUX_COMPATIBILITY_PLAN.md` §4.3 的两难：既没有「解析 latest」的
限流/漂移问题（aka.ms 是 302，不是 API），又拿到了 §6 风险 17 要求的校验值。

**第三方独立佐证**：winetricks 的 `load_vcrun2022` 里维护着一份手工更新的哈希表，
最新一行是

```sh
# 2025/07/14: cc0ff0eb1dc3f5188ae6300faef32bf5beeba4bdd6e8e445a9184072096b713b
w_download https://aka.ms/vs/17/release/vc_redist.x64.exe cc0ff0eb...713b
```

与我们从 URL 里抠出、并实际下载核对过的值**逐字符相同**。这同时也说明：winetricks
选择的是「人工维护哈希表」这条路（历史行里能看到 2022 年至今更新了十几次），
而我们从重定向 URL 自取的做法免掉了这份维护负担。

### 1.3 umu 侧可用的两条路

`umu_run.py` 对参数的处理分两种：

```python
# 动词形式：umu-run winetricks <verb>
is_winetricks: bool = is_cmd and cmd == "winetricks"
exe: Path = Path(protonpath, "protonfixes", "winetricks")
env["EXE"] = str(exe.resolve(strict=True)) if exe.is_file() else "winetricks"
...
if env["EXE"].endswith("winetricks") and is_installed_verb(opts, Path(env["WINEPREFIX"])):
    sys.exit(1)          # 已装过的动词直接退 1

# 普通 exe：umu-run /path/to/foo.exe <args>
exe: Path = Path(cmd).expanduser().absolute()
_ = exe.resolve(strict=True)
env["EXE"] = str(exe)
```

两点要注意：

1. `umu-run <exe>` 收的是**宿主路径**（`Path(cmd).resolve(strict=True)` 解析的是 Linux
   文件系统），**不是** `Z:\` 形式。这与我们现在 `runner.Run(ctx, arkExe, ...)` 传宿主路径、
   只对**参数**用 `runner.GamePath()` 的做法一致，无需特殊处理。
2. winetricks 那条路要求宿主或 `$PROTONPATH/protonfixes/` 下有 winetricks 脚本，
   且 winetricks 自己会**再去微软下载一次** vc_redist（用它自己的 wget/curl，不受我们
   控制）——正是我们刚在 `docs/STEAMRT_PREFETCH_PLAN.md` 里花力气消灭的那类下载。

### 1.4 prefix 的实际布局

`docs/UMU_PREFIX_INIT_TROUBLESHOOTING.md` 的现场目录列表里有一行：

```
lrwxrwxrwx  pfx -> .         Aug 29 00:04
```

umu 在 prefix 目录里建了一个指向自身的 `pfx` 软链，所以 `<prefix>/pfx/...` 与
`<prefix>/...` 是同一处。既有代码检查 `{prefix}/system.reg` 与
`{prefix}/drive_c/windows/system32` 是对的，本方案沿用同一套路径。

### 1.5 参考实现：不是 `ark_instance_manager.sh`，是 winetricks

umu / GE-Proton / steamrt 那几步都能逐行对照本仓库的 `scripts/ark_instance_manager.sh`。
**这次不行**——grep 该脚本，`ArkApi` / `vcrun` / `vc_redist` / `winetricks` **零命中**，
它压根不涉及 ArkApi。

但另有一个**更好的**参考实现：**winetricks 的 `vcrun2022` 动词**。它是这十几年里
「在 Wine 里装微软运行时」被踩得最平的一条路，且踩过的坑都以注释形式留在源码里。
本方案的 §2.3 / §2.4 / §2.5 全部逐条对照
`https://github.com/Winetricks/winetricks` 的 `src/winetricks`（本次阅读的是 master，
17,239 行），行号在各节标出。

**这把「靠真机试错」换成了「照抄已验证实现」**，并且直接推翻了原稿的一个错误假设
（§2.5）。剩下真正需要真机定论的只有 §7 那几条。

---

## 2. 方案

### 2.1 装法：直接跑官方安装包（用户提的方案），不走 winetricks

| | **直接跑 `vc_redist.x64.exe`（选它）** | winetricks `vcrun2022` |
|---|---|---|
| 宿主依赖 | 无 | winetricks（宿主或 GE-Proton 自带）+ 部分动词还要 cabextract |
| 谁下载 | `pkg/download`：重试 / 断点续传 / `http_proxy` / 校验 | winetricks 自己的 wget/curl，不受控 |
| 校验 | ✅ 从重定向 URL 白捡 SHA256（§1.2） | winetricks 自带 sha256 表，但版本由它决定 |
| Wine 适配 | 无（原样跑微软的 Burn 引导器） | ✅ winetricks 顺带设 DLL override 等 Wine 侧配置 |
| 幂等 | 我们自己记标记 + 校验后置条件 | `is_installed_verb` 读 `winetricks.log`，已装过 `sys.exit(1)` |
| 可复现 | 高（就一个 exe + 三个开关） | 中（winetricks 版本不同行为不同） |

选直接跑，理由与整个项目的取向一致：**下载归我们管、依赖不外扩**。
winetricks 唯一真正的优势是它会顺手配 DLL override，这一项在 §2.4 单独处理。

命令形态就是用户给的那条：

```bash
WINEPREFIX=<prefixDir> GAMEID=<gameid> PROTONPATH=<GE-Proton> \
  umu-run <BaseDir>/vcredist/vc_redist.x64.exe /install /quiet /norestart
```

### 2.2 装在哪、什么时候装

**装进共享 prefix，时机在 `EnsureRuntime` 里、`warmPrefix` 之后。**

- prefix 必须已经初始化（有 `system.reg`）才能往里装东西，所以只能在 `warmPrefix` 之后。
- 放在 `EnsureRuntime` 而不是实例启动路径：`setup` 本来就是「下载一堆东西」的地方，
  已经有进度流、失败了当场就能看见并重试；把一次 24 MB 下载 + 一次 MSI 安装塞进
  `startServerInternal`，等于让一次定时重启可能多花几分钟，还要和启动超时打架。
- ~~**`prefix_mode: per-instance` 暂不覆盖**：核对代码后确认，`server.go` 调
  `runner.Run` 时并没有传 `Options.PrefixKey`，所以今天**所有实例都跑在共享 prefix 上**，
  per-instance 模式实际从未被走到（这是既有缺口，见 `LINUX_COMPATIBILITY_PLAN.md`
  §6 风险 6，不在本方案范围内）。API 设计成按 prefixKey 取，将来补上 PrefixKey 时
  不用再改签名。~~

  > **2026-09-01 更新：这条备注已过期，缺口已补。** per-instance 早已接线
  > （`server.go` 现在传 `runner.PrefixKeyFor(instanceName)`），于是上面那句
  > 「实际从未被走到」不再成立，而**两条路只补上了一条**：
  >
  > - `ensurePrefix` **新建** per-instance prefix 时会调 `ensureVCRedist`（已有）；
  > - 但**先于那段代码创建的 prefix** 走的是快路径，永远补不上 —— 表现为切到
  >   per-instance 后闸门放行、实例起来、ArkApi 加载不了，每次启动只有一条
  >   「没检测到 VC++ 运行时」的告警。
  >
  > 现在快路径上加了一次 `prefixHasVCRedistOverrides(prefix)` 判断（只读 `user.reg`
  > 一个文件），缺了就补跑 `ensureVCRedist`，失败只记录。
  >
  > **判据特意不用 `prefixHasVCRedist`**：那个看的是 system32 里有没有微软原生 DLL，
  > 也就是「安装器跑成功过」——而安装器在没有图形显示的机器上**永远**装不上
  > （退出码 203）。拿它当「要不要再试一次」的判据，会让无头机每次启动都重跑一遍
  > `ensureVCRedist`，里面有一次 regedit 容器启动，好几秒。override 才是承重的、
  > 也是无头可用的那一环，所以由它当判据：写过一次之后就是一次文件读。

实例启动侧**只校验、不安装、不阻断**（§3.6）：ArkApi 起不来是用户自己要承担的实验，
程序的责任是把「缺 VC++ 运行时」这条线索明确打出来，而不是替他决定。

### 2.3 成功判据（**已按真机实测重写**）

原稿把「注册表里 `Runtimes\x64` 的 `Installed=dword:00000001`」当主判据、DLL 体积当
兜底。真机把两条都判了死刑：

**① 注册表键不能用 —— GE-Proton 预置了它。** 全新 prefix 的 `system.reg` 里：

```
[Software\\Microsoft\\VisualStudio\\14.0\\VC\\Runtimes\\x64] 1774238072
"Bld"=dword:00008681
"Installed"=dword:00000001
"Major"=dword:0000000e
"Version"="14.42.34433.0"
```

Wine/Proton 主动伪造这个键，好让游戏自带的安装器别去装 VC++。拿它当判据的结果是
**永远认为已装好、于是永远不装** —— 这个 bug 在真机第一条命令就被抓出来了。
代码里现在只留 `vcRuntimeRegistryVersion()` 读版本号供人看，判据函数**根本不接受
注册表输入**，这本身就是一道防线（`TestFreshProtonPrefixIsNotConsideredInstalled`）。

**② 文件体积不能用。** 实测 system32 里 Wine 内建的 `msvcp140.dll` 是 **1,843,959 字节**，
比微软原生的 553,552 字节还大得多；`concrt140` 两边都在 320KB 上下。PE 化之后的 Wine
内建 DLL 是真代码，任何阈值都划不出界。

**现行判据（唯一一条）：PE 的 DOS stub 里有没有 Wine 的明文标记。** 实测全新 prefix 里
`vcruntime140.dll` / `msvcp140.dll` / `concrt140.dll` 头部 1 KiB 全部命中
`Wine builtin DLL`（另一个可能的值是 `Wine placeholder DLL`，代码两个都认）。
探针文件是 `vcruntime140.dll`。

### 2.3.1 （原稿）成功判据的推导过程

这是本方案最容易做错的一处。

Wine 的 prefix 里 **`system32\vcruntime140.dll` / `msvcp140.dll` 本来就存在** ——
那是 Wine 为自己的 builtin 实现放的占位 PE（几 KB），不是微软原生运行时（几十到几百 KB）。
所以「文件存在 → 已安装」是个**恒为真**的判据，等于没判。

判据按可靠性排序：

1. **注册表键（主判据）**：VC++ 2015-2022 x64 redist 会写
   `HKLM\SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x64`，
   带 `Installed=1` / `Major` / `Minor` / `Bld` / `Version` ——
   这正是 Windows 世界检测该运行时是否安装的标准键。Wine 的 `system.reg` 是纯文本，
   直接找 `VisualStudio\\14.0\\VC\\Runtimes\\x64` 这一节即可。
2. **DOS stub 里的 Wine 标记（兜底判据）**：Wine 会在自己生成的 PE 的 DOS stub 区域
   写一个明文标记 —— 占位 DLL 是 `Wine placeholder DLL`，内建模块是 `Wine builtin DLL`。
   读 `system32\vcruntime140.dll` 头部 1 KiB，**命中任一标记 = 还是 Wine 的，没命中 = 原生已就位**。

   > 为什么不用文件体积：现代 Wine（4.x 起 PE 化之后）的内建 DLL 是**真的 PE、带真代码**，
   > 不再是当年那种几 KB 的空壳，体积和原生处于同一量级，阈值划不出可靠的界。
   > 本机量到的原生 x64 尺寸供参考：`vcruntime140.dll` 123,472 B、
   > `msvcp140.dll` 553,552 B、`concrt140.dll` 321,696 B（14.50.35719.0）。
   >
   > 查 `vcruntime140.dll` 而**不是** `msvcp140.dll`：后者根本装不进去，见 §2.5。

   这两个标记字符串必须在 §7 真机验证时实测确认（`head -c 1024 ... | strings`），
   确认后把真实输出贴回本节。
3. **我们自己的标记** `{prefix}/.asa-vcredist`：记录装的是哪个 sha256/版本、什么时候装的。
   放在 prefix 内部是刻意的 —— `reconcilePrefixVersion` 换代时会把整个 prefix 移走，
   标记自动失效，新 prefix 会重装，不需要额外的失效逻辑。

**安装进程的退出码只作参考，不作判据。** 注意一个容易写错的地方：Linux 下
`wait(2)` 只能拿到退出码的**低 8 位**，Windows 那套 `1638`（已装更新版本）、
`3010`（需重启）在这里**永远不会原样出现**。winetricks 的 `w_try_ms_installer`
用的是被截断后的实际观测值（`winetricks` 第 769-780 行）：

```sh
if [ -n "${_w_ms_installer}" ]; then
    case ${status} in
        # Nonfatal
        0) ;;
        105) echo "exit status ${status} - normal, user selected 'restart now'" ;;
        194) echo "exit status ${status} - normal, user selected 'restart later'" ;;
        236) echo "exit status ${status} - newer version detected" ;;
        # Fatal
        5) w_die "exit status ${status} - user selected 'Cancel'" ;;
        *) w_die "..." ;;
    esac
```

（`194 == 3010 & 0xFF`，对得上。）我们照抄这张表**只用于生成人类可读的注脚**，
判决仍然由 §2.3 的后置条件下 —— 与 `warmPrefix` 对 wineboot 的处理同一个模式
（`umu_linux.go` 的 `exitNote`）。

### 2.4 Wine 侧配置：照抄 winetricks，不自己试

原稿把「Wine 会不会真的去用刚装的原生 DLL」列为悬案、准备靠真机试错定论。
读完 winetricks 源码后**这条悬案可以关掉**——它已经把答案写在代码里了，直接抄。

#### 2.4.1 DLL override 怎么写

`w_override_dlls` 的实现（`winetricks` 第 2007-2037 行 + `w_common_override_dll`
第 1961-2006 行）就是生成一个 `.reg` 再导入：

```sh
cat > "${W_TMP}"/override-dll.reg <<_EOF_
REGEDIT4

[HKEY_CURRENT_USER\\Software\\Wine\\DllOverrides]
_EOF_
# 每个 module 一行：
#   # Note: if you want to override even DLLs loaded with an absolute
#   # path, you need to add an asterisk:
#   echo "\"*${module}\"=\"${_W_mode}\""
w_try_regedit "${W_TMP_WIN}"\\override-dll.reg
```

要点：

- 键是 `HKCU\Software\Wine\DllOverrides`，值名带 `*` 前缀 —— 注释写明了原因：
  **不带 `*` 时用绝对路径加载的 DLL 不受 override 影响**。ArkApi 的注入代码很可能
  正是用绝对路径 `LoadLibrary`，所以这个星号不是可选项。
- 值是 `native,builtin` 而不是 `native`：原生优先、内建兜底。某个 DLL 没装上时
  还能回落，比硬 native 安全。
- 导入方式 `w_try_regedit` → `regedit /S <file.reg>`，且注释说
  「If on wow64, run under both wine and wine64 (otherwise they only go in the
  32-bit registry afaict)」。我们只关心 x64，走 umu-run 默认就是 64 位那一遍。
- `w_common_override_dll` 开头那段删 winsxs manifest 的 `case`
  **只覆盖 comctl32 / vcrun2005 / vcrun2008**，vcrun2019/2022 不在其列 ——
  这一步我们不需要做。

#### 2.4.2 要 override 哪些 DLL

`load_vcrun2022`（第 13867 行起）分两次声明，x64 部分单独补一个：

```sh
w_override_dlls native,builtin concrt140 msvcp140 msvcp140_1 msvcp140_2 \
    msvcp140_atomic_wait msvcp140_codecvt_ids vcamp140 vccorlib140 vcomp140 vcruntime140
...
case "${W_ARCH}" in
    win64)
        # vcruntime140_1 is only shipped on x64:
        w_override_dlls native,builtin vcruntime140_1
```

我们装的是 `aka.ms/vs/17`（2022 通道，实测 14.44.35211.0），所以照抄 **vcrun2022**
这一份，共 11 个：

```
concrt140  msvcp140  msvcp140_1  msvcp140_2  msvcp140_atomic_wait
msvcp140_codecvt_ids  vcamp140  vccorlib140  vcomp140  vcruntime140  vcruntime140_1
```

> 顺带一提：`load_vcrun2019` 的列表更长，多了 `atl140` 与一串
> `api-ms-win-crt-*`。ArkApi 官方写的是「2019」，但 2015–2022 是同一个 14.x 家族、
> 同一个安装包，装 vs/17 通道即覆盖。若真机上出现 `api-ms-win-crt-*` 相关的加载失败，
> 再按 vcrun2019 的长列表补 —— 这是有据可依的下一步，不是猜测。

#### 2.4.3 由此带来的方案变更

原稿「默认不设 `WINEDLLOVERRIDES`，等真机定论」的保守做法**作废**：

- 改为**安装后立即写入上面 11 个 override 到 prefix 注册表**，与 winetricks 完全一致。
  它是安装动作的组成部分，不是可选调优 —— winetricks 里 `w_override_dlls` 就在
  `w_download`/安装之前第一条执行。
- `linux.wine_dll_overrides` 配置项**保留**，但语义从「你自己填」变成
  「额外追加到启动环境的 `WINEDLLOVERRIDES`」，供排障时临时改写单个 DLL 的行为。
- 写进注册表（持久化在 prefix 里）而不是每次启动塞环境变量，同样是照抄 winetricks：
  一次装好，之后每次启动都生效，也不用担心哪条启动路径漏传环境变量。

### 2.5 ⚠️ 安装器**不会**替换 `msvcp140.dll` / `msvcp140_2.dll`

这条直接推翻了原稿「跑一遍官方安装器就完事」的假设，是本次调研最重要的发现。
`load_vcrun2022` 里明明白白：

```sh
# Setup will refuse to install msvcp140 & msvcp140_2 because the builtin's
# version number is higher, so manually replace them
# See https://bugs.winehq.org/show_bug.cgi?id=57518
w_try_cabextract --directory="${W_TMP}/win64" "${W_CACHE}"/"${W_PACKAGE}"/vc_redist.x64.exe -F 'a12'
w_try_cabextract --directory="${W_TMP}/win64" "${W_TMP}/win64/a12"
w_try mv -f "${W_TMP}/win64/msvcp140.dll_amd64"   "${W_SYSTEM64_DLLS}/msvcp140.dll"
w_try mv -f "${W_TMP}/win64/msvcp140_2.dll_amd64" "${W_SYSTEM64_DLLS}/msvcp140_2.dll"
```

（`W_SYSTEM64_DLLS` = `drive_c/windows/system32`，winetricks 第 4751 行；win64 prefix 下
`W_SYSTEM32_DLLS` 反而是 `syswow64`——别记反了。）

即：**Wine 内建 `msvcp140` 的版本号比微软发的还高，安装器据此判定"已有更新版本"直接跳过。**
而 `msvcp140` 正是 C++ 标准库，一个 C++ 写的 hook 框架最依赖的就是它。只跑安装器，
ArkApi 最需要的那块恰恰不会被换掉。

**处理方式：分两级，先做 Level 1，Level 2 由真机结论触发。**

- **Level 1（本次实现）**：跑安装器 + 写 §2.4 的 override。
  这能拿到 `vcruntime140` / `vcruntime140_1` / `concrt140` / `vcomp140` 等大部分组件；
  `msvcp140` / `msvcp140_2` 保持 Wine builtin。
  由于 override 是 `native,builtin`，没装上的那两个会自动回落到 builtin，不会变成
  "找不到 DLL"——**行为上是"用 Wine 的 C++ 标准库"，不是崩溃**。
- **Level 2（若真机确认 ArkApi 因 `msvcp140` 失败才做）**：补 CAB 抽取。
  届时的注意事项，现在就记下来免得将来重新踩：

  1. `-F 'a12'` 里的 `a12` 是 **CAB 内部成员名，随 redist 版本漂移**
     （vcrun2022 x86 用 `a10`、x64 用 `a12`，vcrun2026 x86 用 `a2`）。
     照抄这个数字等于抄一个魔数，正确做法是**枚举成员、按 `*msvcp140*` 匹配**。

  2. **`cabextract` 不是 Wine 自带的**，是宿主上一个独立的 Linux 包
     （libmspack 项目，`apt install cabextract` / `dnf install cabextract`）。
     winetricks 自己的依赖行里就列着它，且 `w_try_cabextract` 找不到就直接 `w_die`
     —— **没有兜底**（对比 `w_try_7z` 找不到 `7z` 时还会退回到装进 prefix 的 Windows 版 7-Zip）：

     ```sh
     w_try_cabextract()
     {
         # Not always installed, but shouldn't be fatal unless it's being used
         if test ! -x "$(command -v cabextract 2>/dev/null)"; then
             w_die "Cannot find cabextract.  Please install it (e.g. 'sudo apt install cabextract' ...)."
         fi
         w_try cabextract -q "$@"
     }
     ```

  3. 因此 Level 2 有三条候选路径，**都未验证**，届时按成本重新评估：

     | 路径 | 宿主依赖 | 未知数 |
     |---|---|---|
     | 宿主 `cabextract`（winetricks 的做法） | ✅ 多一个包 | 最少 —— 这条被验证过十几年 |
     | **Wine 自带的 `expand.exe` / `extrac32.exe` / `cabarc.exe`** | ❌ 无 | vc_redist 是 **WiX Burn 捆绑包**（PE 里挂着 attached container），不是裸 `.cab`。cabextract 能扫 PE 里的 `MSCF` 签名把 `a12` 掏出来，Wine 这几个重实现大概率只认真正的 cabinet 文件——**能不能行必须先实测** |
     | 纯 Go 实现（扫 `MSCF` + MSZIP 解压） | ❌ 无 | 工作量最大；MSZIP 本质是分块重置字典的 deflate，可做但不轻量 |

     若走宿主 `cabextract`，`preflight_linux.go` 要加一条**建议级**检查
     （绝不能是阻断级 —— 见 `ACL_PERMISSION_HARDENING_PLAN.md` §1：
     「acl 包没装」曾经把一台完全可用的机器挡在 setup 门外）。
     Level 2 服务的是一个可选功能的子问题，更没有资格阻断安装。

**在没有真机数据之前不做 Level 2**，理由不是偷懒：三条路各自都要付出代价
（宿主依赖 / 未验证的 Wine 工具 / 可观的实现量），而「Level 1 之后 ArkApi 到底还缺不缺
`msvcp140` 的原生实现」是一个**一次实测就能回答**的问题。先花 24 MB 和几十行代码把能
确定的部分做掉，再用真机数据决定要不要付这笔额外成本。

#### ✅ 真机结论：Level 2 不需要做

实测（GE-Proton10-34 + umu 1.4.4，2026-08-30）：安装器跑完之后 system32 里

```
DLL                        system32   游戏目录
concrt140.dll              原生         原生
msvcp140.dll               原生         原生     ← winetricks 说会被跳过的那个
msvcp140_1.dll             原生         原生
msvcp140_2.dll             原生         原生     ← 同上
msvcp140_atomic_wait.dll   原生         原生
msvcp140_codecvt_ids.dll   原生         原生
vcamp140.dll               原生         缺失     ← 只有安装器能补
vccorlib140.dll            原生         原生
vcomp140.dll               原生         缺失     ← 只有安装器能补
vcruntime140.dll           原生         原生
vcruntime140_1.dll         原生         原生
```

**11/11 全部换成原生，`msvcp140` 与 `msvcp140_2` 也在内。** winehq #57518 描述的
「Wine 内建版本号更高导致安装器跳过」在 GE-Proton10-34 上不成立 ——
上游 winetricks 面对的是通用 Wine，Proton 的内建版本不同。

同时这张表暴露了另一件事（见 §2.7）：**游戏自己带了 11 个里的 9 个原生 DLL**，
所以安装器的增量其实只有 `vcamp140` / `vcomp140` 两个。

### 2.6 ⚠️ 微软安装器在 Wine 下**必须有 X 显示**

原稿完全没有预见这一条，而它对本项目的主力部署形态（无头服务器）是硬约束。

同一条命令，只改环境：

| 环境 | 退出码 | DLL 是否替换 |
|---|---|---|
| `DISPLAY=:0`（WSLg 的真实 X 服务） | **0** | ✅ 11/11 换成原生 |
| 不设 `DISPLAY` | **203** | ❌ |
| `DISPLAY=:99`（有变量、无人监听） | **203** | ❌ |
| 不设 `DISPLAY` + 禁用 `winex11.drv` | **203** | ❌ |
| 不设 `DISPLAY`，改用 `/layout` 只导出不安装 | **203** | ❌（连产物都没有） |

结论：**不是环境变量的形式问题，是真的需要一个能连上的 X 服务**。
vc_redist 的 WiX Burn 引导器即使带 `/quiet` 也要初始化 UI 子系统。
（203 = `ERROR_ENVVAR_NOT_FOUND` 截断到 8 位，成因从码本身完全看不出来 ——
所以 `msInstallerExitNote` 对这个码专门写了解释，并有单测钉住。）

**处理方式**：把「写 override」和「跑安装器」拆成两级，见 §2.7。
`resolveInstallerDisplay()` 先判断：

1. `DISPLAY` 非空**且** `/tmp/.X11-unix/X<n>` 真的存在 → 直接跑；
2. 否则宿主有 `xvfb-run` → 用 `xvfb-run -a` 包一层；
3. 都没有 → **不跑**，打印原因并说明「通常不影响 ArkApi」。

第 3 条是刻意**先判断再动手**：否则每次 `EnsureRuntime` 都要白跑一次注定 203 的
umu-run（约 20 秒）。

> `DISPLAY` 只加给安装器进程，**不动** `inheritedEnv()` 的白名单 ——
> 那条白名单是给**游戏进程**定的规矩（无头服务端不该看见显示），
> 而且它是用来防 `DBUS_SESSION_BUS_ADDRESS` 那类泄漏的，不该为这件事开口子。

### 2.7 主次关系反转：override 才是承重项

真机数据里最有价值的一条：**ARK 服务端自己就在 `ShooterGame/Binaries/Win64/`
带着 9 个原生 VC++ DLL**（见 §2.5 的表）。而 Windows/Wine 解析 DLL 时
**应用目录优先于 system32**。

也就是说：ArkApi 需要的原生运行时**本来就在正确的位置上**，唯一挡路的是
Wine 默认偏好自己的内建实现。掀开这道门的是 `DllOverrides`，不是安装器。

于是实现改成：

- **DLL override：无条件执行。** 无头可用、零依赖、几秒钟跑完。这是承重项。
- **vc_redist 安装：条件执行、失败不算失败。** 它补的是 `vcamp140`/`vcomp140`
  这两个游戏没带的，以及任何不走应用目录的加载路径。

`verify-arkapi` 的「前置条件是否满足」也据此判定：看 **override 是否 11/11**，
而不是看 system32 装没装 —— 后者在无头机上本来就装不上，拿它当门槛会把一台
完全可用的机器判成不可用。

---

## 3. 详细设计

### 3.1 新增文件与 API

```
internal/runner/vcredist.go         # 纯逻辑：从 MS 下载 URL 抠 sha256、system.reg 判据解析
                                    #   不加 build tag —— 与 steamrt.go 同理由，可跨平台单测
internal/runner/vcredist_linux.go   # 下载 + umu-run 安装 + 后置校验 + 标记
internal/runner/vcredist_windows.go # 全部空实现
```

`runner` 对外新增两个函数（`runner.go`，两平台都有声明）：

```go
// EnsurePrefixVCRedist 确保指定 Wine prefix 里装了微软 VC++ 运行时（ArkApi 的
// AsaApiLoader.exe 依赖它）。Windows 上恒为 no-op：那里本来就有系统级运行时。
// prefixKey 语义与 Options.PrefixKey 一致，空字符串 = 默认共享 prefix。
// progress 收人类可读的状态行，与 EnsureRuntime 同一形状。
func EnsurePrefixVCRedist(ctx context.Context, prefixKey string, progress io.Writer) error

// PrefixHasVCRedist 只读地判断某个 prefix 里有没有原生 VC++ 运行时，不联网、不改动。
// 供实例启动时给 ArkApi 用户一条明确诊断。Windows 恒为 true。
func PrefixHasVCRedist(prefixKey string) bool
```

### 3.2 安装流程

```
EnsurePrefixVCRedist(ctx, key, progress)
 ├─ 闸门：!cfg.InstallVCRedist            → return nil
 ├─ 闸门：cfg.Runtime != "umu"            → return nil（custom 运行时的 prefix 不归我们管）
 ├─ 闸门：prefixHasVCRedist(prefix)       → return nil（幂等，纯 stat + 读文本）
 ├─ 闸门：!prefixInitialized(prefix)      → 返回错误（prefix 还没建好，调用点搞错了）
 ├─ 闸门：!cfg.AutoDownload && 本地没有安装包 → return 错误（明确不联网）
 ├─ 解析下载地址：GET cfg.VCRedistURL，跟随重定向拿最终 URL
 │    └─ 从最终 URL 路径里抠 64 位十六进制段 → sha256（§1.2）
 │       抠不到（自定义镜像）→ 用 cfg.VCRedistSHA256；仍为空 → 不校验并打告警
 ├─ download.Fetch → {BaseDir}/vcredist/vc_redist.x64.exe（带 Checksum、Resume、Progress）
 ├─ chmod 0755 目录与文件（降权用户要能读、umu 要能 resolve）
 ├─ 写 DLL override 到 prefix 注册表   （§2.4 / §3.3）
 │    └─ 顺序照抄 winetricks：override 在安装**之前**声明
 ├─ umu-run <exe> /install /quiet /norestart   （§3.3）
 ├─ waitForWineserverDrain(prefix)
 ├─ 后置校验 prefixHasVCRedist(prefix) —— 不过就返回错误，错误里带安装器输出末尾几行
 │    并按 §2.3 的表把退出码翻译成人话
 └─ 写标记 {prefix}/.asa-vcredist
```

接线（`umu_linux.go` 的 `ensureRuntime`）：

```go
if err := warmPrefix(ctx, cfg, logf, prefetched.Variant != ""); err != nil {
    return fmt.Errorf("failed to prepare Wine prefix: %w", err)
}

// ArkApi（AsaApiLoader.exe）依赖微软 VC++ 运行时，Wine/GE-Proton 的 prefix 里只有
// Wine 自己的 builtin 同名 DLL。装不上不阻断 EnsureRuntime —— 不用 ArkApi 的用户
// 占绝大多数，为一个可选功能让整个环境准备失败是不成比例的。
// 见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md。
if err := ensurePrefixVCRedist(ctx, cfg, "", logf); err != nil {
    logf("VC++ 运行时安装失败（%v）；不使用 ArkApi 可忽略，使用 ArkApi 请看上面的输出", err)
}
```

> **为什么失败不阻断**：和 steamrt 预取同一个判断标准 —— 这一步服务的是一个**可选
> 功能**，绝大多数用户不开 ArkApi。让它把 `asa-server setup` 整个弄失败，是拿多数
> 用户的可用性去换少数用户的一条错误消息。但与 steamrt 预取不同的是，**这里的失败
> 必须响亮**（不是「无声降级」）：用户如果真要用 ArkApi，他必须看见这条。

### 3.3 umu-run 调用

与 `warmPrefix` 的 wineboot 调用逐项对齐，只有三处不同（都有理由）：

```go
cmd := exec.CommandContext(ctx, py.Path, umuRunPath(cfg), exePath,
    "/install", "/quiet", "/norestart")
cmd.Env = append(inheritedEnv(),
    "WINEPREFIX="+prefix,
    "GAMEID="+cfg.GameID,
    "PROTONPATH="+protonPath(cfg),
    // 与 warmPrefix 不同：这里运行时早就装好了，没有任何理由让 umu 再去
    // repo.steampowered.com 查更新。与常规启动（umuCommandLine）一致。
    "UMU_RUNTIME_UPDATE=0",
)
if cred, home, err := resolveRuntimeCredential(cfg); err != nil {
    return err
} else if cred != nil {
    // 必须用与游戏进程相同的身份安装：装出来的文件属主才对，
    // 降权后的 AsaApiLoader 才读得到。同 warmPrefix。
    cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
    cmd.Env = runtimeEnv(cmd.Env, home, runtimeUserName(cfg))
}
out := &progressWriter{logf: logf}
cmd.Stdout, cmd.Stderr = out, out
runErr := cmd.Run()   // 退出码只作注脚，判据见 §2.3
```

三处不同：

1. **`UMU_RUNTIME_UPDATE=0`**（warmPrefix 故意不设，因为那一次必须能拉运行时）。
2. **硬超时**：`/quiet` 理论上不弹窗，但 Wine 下的 Burn 引导器仍可能弹出一个没人点的
   对话框，从而永久挂住 setup。外面套一层
   `context.WithTimeout(ctx, 15*time.Minute)`，超时即杀。
   （`warmPrefix` 没有这层保护是既有问题，不在本方案范围内，但值得记一笔。）
3. **exe 传宿主路径**，不过 `GamePath()`（§1.3 第 1 点）。

#### 写 DLL override

同一套调用形状，换成 prefix 里自带的 `regedit.exe` + 一个我们生成的 `.reg`：

```go
// 内容照抄 winetricks 的 w_override_dlls（§2.4.1），11 个模块名见 §2.4.2。
// 值名的 * 前缀不能省：不带它时，用绝对路径 LoadLibrary 的 DLL 不受 override 影响，
// 而 ArkApi 的注入代码很可能正是这么加载的。
regPath := filepath.Join(cfg.BaseDir, "vcredist", "dll-overrides.reg")
os.WriteFile(regPath, []byte(buildVCRedistOverrideReg()), 0o644)

// regedit.exe 在 prefix 里，宿主路径可解析（umu 对普通 exe 走 resolve(strict=True)）；
// 它的**参数**是 Windows 路径，所以过 GamePath()。
cmd := exec.CommandContext(ctx, py.Path, umuRunPath(cfg),
    filepath.Join(prefix, "drive_c", "windows", "regedit.exe"),
    "/S", GamePath(regPath))
```

`/S` 是静默导入，与 winetricks 的 `${W_OPT_UNATTENDED:+/S}` 一致。
winetricks 在 wow64 下会跑 32 位和 64 位两遍（注释说只跑一遍时只进 32 位视图），
我们经 umu-run 跑的是 GE-Proton 的 64 位 wine，正是需要的那一遍；
ArkApi 与 ASA 服务端都是纯 x64，不需要 32 位视图。

### 3.4 幂等与 prefix 重建

- `prefixHasVCRedist` 全程只读本地文件（`system.reg` 文本查找 + 两次 `Stat`），
  无网络、无副作用，可以放心在热路径上调。
- 标记 `{prefix}/.asa-vcredist` 内容示例：
  ```
  sha256=cc0ff0eb...713b
  version=14.44.35211.0
  installed_at=2026-08-29T14:03:11+08:00
  ```
  版本号从 `download` 下来的 exe 的 PE 版本资源读？——**不读**，那要引入 PE 解析。
  改为记录我们请求的 URL 与 sha256 就够了：出问题时这两个值足以复现。
- prefix 被 `reconcilePrefixVersion` 移走（换 Proton 代次）后标记随之消失，
  下次 `EnsureRuntime` 自动重装。不需要单独的失效逻辑。

### 3.5 权限

- `{BaseDir}/vcredist/` 与其中的 exe：`0755`。降权用户只需要**读+执行**，
  不需要属主，所以用 chmod 而不是 chown ——与 `ensureWorldReadExec` 对
  `proton`/`umu-launcher` 两个只读子树的处理同类。
- prefix 本身在 `warmPrefix` 里已经 `chownPathForRuntime` 过，安装进程以降权身份运行，
  写进去的文件属主天然正确。
- `{BaseDir}` 需要 `o+x` 才能被降权用户穿过 —— 这一条 `verifyRuntimeAccess` 已经在查
  （`basedir-not-traversable`），无需重复。

### 3.6 实例启动侧：只校验 + 告警

`internal/instance/server.go`，在已有的 `arkAsaApiRunning` 判定之后：

```go
if arkAsaApiRunning && !runner.PrefixHasVCRedist("") {
    logger.Warnf("实例 %s 启用了 ArkApi，但 Wine prefix 里没有检测到微软 VC++ 运行时，"+
        "AsaApiLoader.exe 很可能起不来。执行 asa-server setup 会自动安装；"+
        "或在 config.yaml 确认 linux.install_vcredist 未被关掉。", instanceName)
}
```

**不阻断启动**：`LINUX_COMPATIBILITY_PLAN.md` §1 目标 5 的既定立场是「不由程序单方面
替用户关掉 ArkApi」。检测是启发式的（§2.3 的判据在真机验证之前都不能算板上钉钉），
用一个可能误判的检查去拦住启动，正好踩中那条立场反对的事。

`PrefixHasVCRedist` 在 Windows 上恒为 `true`，这段在 Windows 上永不触发，
所以放在跨平台的 `server.go` 里不需要 build tag。

---

## 4. 配置

`config.yaml` 的 `linux:` 段新增四项：

```yaml
linux:
  # ArkApi（AsaApiLoader.exe）依赖微软 VC++ 运行时，而 Wine/GE-Proton 的 prefix 里
  # 只有 Wine 自己的同名 builtin DLL。true = setup 时自动装进共享 prefix（约 24MB 下载）。
  # 不用 ArkApi 的话关掉可以省这一步；关掉后启用 ArkApi 的实例启动时只会打一条告警。
  install_vcredist: true
  # 安装包地址。留空 = https://aka.ms/vs/17/release/vc_redist.x64.exe
  # 微软的下载 URL 路径里自带文件 SHA256，跟随重定向后会自动提取并校验，无需手工填。
  vcredist_url: ""
  # 仅当 vcredist_url 指向自建镜像、URL 里没有那段哈希时才需要手工填（小写十六进制）。
  vcredist_sha256: ""
  # 追加到游戏进程的 WINEDLLOVERRIDES，留空 = 不设。
  # 注意：VC++ 那 11 个 DLL 的 native,builtin override 已经在安装时写进 prefix 注册表了
  # （照抄 winetricks 的做法，见 §2.4），**不需要**在这里重复配置。
  # 这一项是排障用的临时逃生舱，例如想把某个 DLL 强制掰回 builtin：
  #   "msvcp140=b"
  wine_dll_overrides: ""
```

`runner.Config` 对应新增 `InstallVCRedist bool` / `VCRedistURL string` /
`VCRedistSHA256 string` / `WineDLLOverrides string`，三处 `runner.Configure` 调用点
（`main.go`、`internal/actions/setup.go`、`internal/gui/gui.go`）同步传入。

`WineDLLOverrides` 非空时由 `umuCommandLine` 追加到启动环境
（`runner_linux.go`，紧跟现有的 `UMU_RUNTIME_UPDATE=0`）。注意 `inheritedEnv` 的白名单
已经放行 `WINE*` 前缀，所以运维直接在 systemd unit 里设 `WINEDLLOVERRIDES` 同样有效 ——
配置项只是让它能进 `config.yaml`、跟着 BaseDir 走。

---

## 5. 代码骨架

`internal/runner/vcredist.go`（无 build tag，可跨平台单测）：

```go
package runner

const defaultVCRedistURL = "https://aka.ms/vs/17/release/vc_redist.x64.exe"

// vcRedistMarker 记在 prefix 内部，prefix 被换代移走时随之失效。
const vcRedistMarker = ".asa-vcredist"

// msDownloadSHA256Re 匹配微软下载 URL 里那段文件哈希：
//   https://download.visualstudio.microsoft.com/download/pr/{guid}/{SHA256}/VC_redist.x64.exe
// 实测该段与文件 sha256 完全一致（见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §1.2），
// 所以跟随一次 302 就能白捡一个校验值，不必在代码里钉死会过期的哈希。
var msDownloadSHA256Re = regexp.MustCompile(`/([0-9A-Fa-f]{64})/[^/]+$`)

func sha256FromMSDownloadURL(rawURL string) (string, bool)

// vcRedistRegistryKey 是 VC++ 2015-2022 x64 运行时在 Windows 上的标准检测键，
// Wine 把它明文写进 system.reg。见 §2.3。
const vcRedistRegistryKey = `VisualStudio\\14.0\\VC\\Runtimes\\x64`

// nativeVCRuntimePresent 判断 system.reg 文本里有没有那一节。
func nativeVCRuntimeInRegistry(systemRegText []byte) bool

// wineDLLMarkers 是 Wine 写在自己生成的 PE 的 DOS stub 里的明文标记。命中任一 =
// 这个文件还是 Wine 的，不是微软原生运行时。见 §2.3 判据 2 —— 不用文件体积，
// 因为 PE 化之后的 Wine 内建 DLL 是真代码，体积和原生同量级。
var wineDLLMarkers = [][]byte{
    []byte("Wine placeholder DLL"), // wineboot 铺进 prefix 的占位
    []byte("Wine builtin DLL"),     // 真正的内建模块
}

// nativeProbeDLL 是兜底判据实际检查的文件。
//
// ⚠️ 不能用 msvcp140.dll：安装器会因为 Wine builtin 版本号更高而**跳过**它
// （winehq bug 57518，见 §2.5），所以即便安装完全成功它也仍是 Wine 的。
const nativeProbeDLL = "vcruntime140.dll"

// wineDLLHeaderScan 是读头部多少字节去找上面那两个标记。DOS stub 在 PE 最前面。
const wineDLLHeaderScan = 1 << 10

// vcRedistOverrideDLLs 抄自 winetricks 的 load_vcrun2022（§2.4.2），
// 含 x64 独有的 vcruntime140_1。顺序无关紧要，保持与上游一致便于对拍。
var vcRedistOverrideDLLs = []string{
    "concrt140", "msvcp140", "msvcp140_1", "msvcp140_2",
    "msvcp140_atomic_wait", "msvcp140_codecvt_ids",
    "vcamp140", "vccorlib140", "vcomp140", "vcruntime140", "vcruntime140_1",
}

// buildVCRedistOverrideReg 生成 w_override_dlls 等价的 .reg 文本。
// 值名的 "*" 前缀是必需的 —— 见 §2.4.1。
func buildVCRedistOverrideReg() string

// msInstallerExitNote 把安装器退出码翻成人话。用 winetricks w_try_ms_installer 的
// 那张表：Linux 只看得到低 8 位，Windows 的 3010/1638 不会原样出现（194 == 3010&0xFF）。
func msInstallerExitNote(code int) string
```

`internal/runner/vcredist_linux.go`：

```go
//go:build linux

func vcRedistPath(cfg Config) string   // {BaseDir}/vcredist/vc_redist.x64.exe
func prefixHasVCRedist(prefixKey string) bool
func ensurePrefixVCRedist(ctx context.Context, cfg Config, prefixKey string, logf func(string, ...any)) error
func downloadVCRedist(ctx context.Context, cfg Config, logf func(string, ...any)) (string, error)
func runVCRedistInstaller(ctx context.Context, cfg Config, prefix, exePath string, logf func(string, ...any)) error
```

`internal/runner/vcredist_windows.go`：三个导出函数的空实现
（`ensurePrefixVCRedist` 返回 nil，`prefixHasVCRedist` 返回 true）。

---

## 6. 风险

| # | 风险 | 影响 | 缓解 |
|---|---|---|---|
| 1 | ~~Wine 仍加载 builtin 而非刚装的原生 DLL~~ **已由 winetricks 源码关闭** | — | §2.4：安装时把 11 个 `native,builtin` override（含 `*` 前缀）写进 prefix 注册表，做法与 winetricks 逐字一致。仍在 §7 用 `WINEDEBUG=+loaddll` 复核一次 |
| 1b | ~~`msvcp140` / `msvcp140_2` 装不进去（winehq #57518）~~ **真机证伪** | — | §2.5：GE-Proton10-34 上 11/11 全部换成原生，含这两个。Level 2 取消 |
| 1c | 🆕 **微软安装器在 Wine 下必须有 X 显示**，无头机上一律 203 | 无头服务器（主力部署形态）装不上 system32 那份 | §2.6/§2.7：override 无条件写入（承重项、无头可用），安装器条件执行；有 `xvfb-run` 就用它，都没有就跳过并说明。§2.7 说明为什么这通常不影响 ArkApi |
| 1d | 🆕 **注册表判据在全新 prefix 上恒真**（GE-Proton 预置 `Installed=1`） | 会「永远认为已装好、于是永远不装」 | §2.3：判据改为只看 PE 的 Wine 标记；判据函数不接受注册表输入，并有单测钉住 |
| 2 | **`AsaApiLoader.exe` 的注入机制在 Wine 下不可靠** | VC++ 装好了 ArkApi 仍然不工作 | 这是 `LINUX_COMPATIBILITY_PLAN.md` §6 风险 11 的原有结论，本方案**不解决也不承诺**。它只保证「依赖齐了」，把失败原因从「缺运行时」这一层剥掉 |
| 3 | Burn 引导器在 Wine 下弹窗/挂死 | setup 永久卡住 | §3.3 的 15 分钟硬超时；`/quiet` 已尽力 |
| 4 | 注册表判据（§2.3）在 Wine 的 `system.reg` 里转义形式与预期不同 | 后置校验永远不过 → 每次 setup 重装一遍 | 兜底判据（`vcruntime140.dll` 体积，**不是** `msvcp140`，见风险 1b）+ §7 必须实测确认，把真实的那一行贴回本文档 |
| 5 | 微软改掉下载 URL 的形状（哈希段消失） | 退化为不校验下载 | 打告警 + `vcredist_sha256` 可手工兜底；退化不阻断 |
| 6 | 24 MB 下载给不用 ArkApi 的用户 | 多花十几秒 | `install_vcredist: false` |
| 7 | `prefix_mode: per-instance` 下每个 prefix 都要装 | ~~目前走不到（无人传 `PrefixKey`）~~ —— **已走到，且已补**（2026-09-01） | `ensurePrefix` 新建时装；快路径上按 `prefixHasVCRedistOverrides` 补装历史遗留的 prefix。详见 §2.2 的更新框 |
| 8 | 只装 x64，未装 x86 | 若某个 ArkApi 插件带 32 位组件会缺 | ASA 服务端与 AsaApiLoader 都是 x64；真出现再说，不预先复杂化 |

---

## 7. 验证

### 7.1 单测（Windows 上可跑，`vcredist.go` 无 build tag）

- `sha256FromMSDownloadURL`：真实 URL 能抠出哈希；短链本身（无哈希段）返回 false；
  长度 63/65 的十六进制段不匹配；非十六进制不匹配。
  用 §1.2 实测到的那条真实 URL 作为固定用例。
- `nativeVCRuntimeInRegistry`：含该节的 `system.reg` 片段 → true；只含
  `Wow6432Node\\...\\Runtimes\\x86` 的 → false；空文本 → false。
- `buildVCRedistOverrideReg`：输出含 `REGEDIT4` 头与
  `[HKEY_CURRENT_USER\Software\Wine\DllOverrides]` 段；11 个模块**每个都带 `*` 前缀**、
  值为 `native,builtin`（星号漏了是这套东西最容易发生、又最难从现象反推的失效方式，
  所以单独钉一条）；模块清单与 §2.4.2 逐字一致。
- `msInstallerExitNote`：0/105/194/236 判为非致命，5 与其它判为致命，
  文案与 winetricks 的语义对应。
- 含 `Wine placeholder DLL` / `Wine builtin DLL` 标记的头部字节 → 判为"不是原生"。
- 空实现的 Windows 分支：`PrefixHasVCRedist` 恒 true。

### 7.2 真机（Linux）—— ✅ 已执行

**执行于 2026-08-30，WSL2 Ubuntu + GE-Proton10-34 + umu 1.4.4，`/opt/asa-server`。**
结论已回填到 §0 / §2.3 / §2.5 / §2.6 / §2.7。做过的事：

| 检查 | 结果 |
|---|---|
| 全新 prefix 的注册表基线 | ❌ `Runtimes\x64` + `Installed=1` **已存在** → 判据作废（§2.3） |
| DOS stub 标记定标 | ✅ `Wine builtin DLL`，代码里的两个候选字符串之一命中 |
| `regedit.exe` 位置 | ✅ `C:\windows\regedit.exe`（system32 下没有），主路径正确 |
| 下载 + 校验 | ✅ 25,635,768 字节，URL 自带的 sha256 校验通过 |
| DLL override 写入 | ✅ 11/11 进入 `user.reg` |
| 有 X 显示时安装 | ✅ 退出 0，**system32 里 11/11 全部原生** |
| 无 X 显示时 | ✅ 跳过并说明原因，不浪费一次注定 203 的调用（§2.6） |
| 幂等 | ✅ 再跑直接短路 |
| **回归**：普通服务端 | ✅ 改了共享 prefix 的加载顺序后，`asa-server verify` 仍在 42 秒内启动并监听 |
| ArkApi 端到端 | ✅ **已验证**（2026-08-30 二次执行，用户装好 ArkApi 之后）—— 但先失败后成功，差别只有一个 `DISPLAY`，见 §9 |

下面是当时用的命令，供换环境时重跑。

```bash
# 1) 干净 prefix 下跑 setup，观察安装是否成功
rm -rf {BaseDir}/umu-prefix {BaseDir}/vcredist
asa-server setup 2>&1 | tee /tmp/setup.log

# 2) 确认注册表主判据 —— 把真实的那一行贴回本文档 §2.3
grep -n 'Runtimes' {BaseDir}/umu-prefix/system.reg

# 3) 确认 override 真的写进去了（照抄 winetricks 的那 11 行，注意 * 前缀）
grep -A 15 'Software\\\\Wine\\\\DllOverrides' {BaseDir}/umu-prefix/user.reg

# 3b) 【Level 2 预调研，顺手做】确认 Wine 自带的 CAB 工具能不能啃 Burn 捆绑包，
#     结论回填 §2.5 的候选路径表 —— 能行的话 Level 2 就不用加宿主依赖了
umu-run "$PREFIX/drive_c/windows/system32/expand.exe" -F:* 'Z:\...\vc_redist.x64.exe' 'Z:\tmp\out'
umu-run "$PREFIX/drive_c/windows/system32/extrac32.exe" /E 'Z:\...\vc_redist.x64.exe'

# 4) 【判据 2 定标】确认 Wine 的 DOS stub 标记到底长什么样，回填 §2.3
cd {BaseDir}/umu-prefix/drive_c/windows/system32
for f in vcruntime140.dll vcruntime140_1.dll msvcp140.dll concrt140.dll; do
    printf '%s  %s  ' "$f" "$(stat -c%s "$f")"
    head -c 1024 "$f" | strings | grep -i 'wine' || echo '(无 Wine 标记 → 原生)'
done
#    预期：vcruntime140.dll 无标记（装上了）；msvcp140.dll 仍带标记（§2.5 的已知跳过）

# 5) 幂等：再跑一次 setup，应当直接跳过，不重新下载、不重新安装
asa-server setup 2>&1 | grep -i vcredist

# 6) 【风险 1 复核】ArkApi 实例启动一次，确认加载的是 native
#    在 systemd unit 或 shell 里临时加 WINEDEBUG=+loaddll，然后 grep 启动日志
grep -iE 'msvcp140|vcruntime140|concrt140' {BaseDir}/instances/<name>/server.log
```

验收判据：

- setup 日志出现 VC++ 下载进度与「安装完成」；
- **注册表主判据命中**（第 2 步），且 override 的 11 行都在（第 3 步）；
- 第 4 步能明确区分出「哪些换成了原生、哪些还是 Wine 的」，结果回填 §2.3 与 §2.5；
- 第 5 步第二次 setup 完全跳过；
- 第 6 步 `vcruntime140` 加载的是 native —— **这是风险 1 的最终定论**，
  若仍是 builtin 说明 override 写法有问题，回到 §2.4 复核 `*` 前缀与注册表视图；
- ArkApi 实例能被 `AsaApiLoader.exe` 拉起（**这一条不作为本方案的成败判据** ——
  见风险 2；起不来时要能从日志分辨出「不是缺 VC++ 运行时」）。
  若失败且日志明确指向 `msvcp140`，那就是 §2.5 的 Level 2 触发条件；
- `install_vcredist: false` 时：不下载、不安装、不写注册表，ArkApi 实例启动只多一条告警，
  其余行为与打补丁前完全一致。

---

## 8. 改动清单

| 文件 | 改动 |
|---|---|
| `internal/runner/vcredist.go` | 新增：URL 哈希提取、`system.reg` 判据、Wine DOS stub 标记、`.reg` 生成、退出码文案、常量 |
| `internal/runner/vcredist_linux.go` | 新增：下载 + 写 override + umu-run 安装 + 后置校验 + 标记 |
| `internal/runner/vcredist_windows.go` | 新增：空实现 |
| `internal/runner/vcredist_test.go` | 新增：§7.1 |
| `internal/runner/runner.go` | 新增 `EnsurePrefixVCRedist` / `PrefixHasVCRedist`；`Config` 加四个字段 |
| `internal/runner/umu_linux.go` | `ensureRuntime` 在 `warmPrefix` 之后接线（失败只告警） |
| `internal/runner/runner_linux.go` | `umuCommandLine` 在 `WineDLLOverrides` 非空时追加 `WINEDLLOVERRIDES` |
| `internal/instance/server.go` | `arkAsaApiRunning` 时校验 + 告警（不阻断） |
| `internal/appconfig/config.go` / `template.go` | `linux:` 段四个新键 + 默认值 |
| `main.go` / `internal/actions/setup.go` / `internal/gui/gui.go` | `runner.Configure` 补传四个字段 |
| `docs/LINUX_COMPATIBILITY_PLAN.md` | §5.12 / §6 风险 11 交叉引用本文；§8 增一行 P7 |
| `docs/LINUX_DEPLOYMENT.md` | 故障排查表增「ArkApi 实例起不来」一行 |
| `CLAUDE.md` | `internal/runner/` 目录树补 `vcredist*.go` |

估计 ~250 行实现 + ~100 行测试。

---

## 9. 追加：ArkApi 还需要一个图形显示（2026-08-30 实测）

> 本节是把「附录 B 排查顺序」真的走了一遍的结果。**结论落在第 0 项：一个本文档
> 原稿完全没有列出的前置条件。** 前面所有关于 VC++ 的工作都是对的，但不够。

### 9.1 现象

用户按 §2 装好 VC++（override 11/11 + system32 11/11 全原生，`ArkApi/AsaApi.dll`
与 `Plugins/Permissions` 都在位）后跑 `asa-server verify-arkapi`，
`logs/verify-arkapi-launch.log` 到此为止：

```
Proton: /opt/.../ShooterGame/Binaries/Win64/AsaApiLoader.exe
Proton: Executable a unix path, launching with /unix option.
fsync: up and running.
```

**之后再无一行。** 五分钟后端口检测超时。`Win64/logs/` 目录压根没被建出来。
同一台机器上 `ArkAscendedServer.exe` 走 `asa-server verify` 42 秒就开始监听。

### 9.2 定位

手工重跑同一条 umu 命令（带 PTY），把 Wine 的诊断打出来：

```
00d4:err:winediag:nodrv_CreateWindow Application tried to create a window,
                                     but no driver could be loaded.
00d4:err:winediag:nodrv_CreateWindow L"The explorer process failed to start."
011c:err:winediag:nodrv_CreateWindow L"Make sure that your display server is
                                     running and that its variables are set."
exit: 3   （5 秒）
```

只加一个 `DISPLAY=:0`（WSLg 的真实 X 服务），**同一条命令**：

```
0174:fixme:gameux:GameExplorerImpl_VerifyAccess (... ArkAscendedServer.exe)
（跑满 70 秒被手工掐掉，exit 124）
```

`Win64/logs/ArkApi_368_2026-08-30_10-26.log`：

```
ARK:SA Api V2.03 ... Loading...
Added DLL search directory: Z:\...\Win64\ArkApi
Checking for a verified local cache for 66cc028c….zip
Downloading cache archive 66cc028c….zip
Cache files downloaded and processed successfully
Reading cached offsets / Reading cached bitfields / Initialized hooks
API was successfully loaded
UGameEngine::Init was called
Loaded plugin Ark:SA Permissions V1.1
AShooterGameMode::InitGame was called
SERVER ID: 245100821
```

**唯一变量是 `DISPLAY`。** 日志里的 `oleacc:find_class_data unhandled window class:
L"Static"` / `uiautomation:*` 也印证了：`AsaApiLoader.exe` 是带真窗口的程序，
不是纯控制台程序。

顺带关掉两条悬案：

- **风险 2**（「注入机制在 Wine 下不可靠」）：`Initialized hooks` +
  `AShooterGameMode::InitGame` 说明 `CreateRemoteThread` 那套注入在
  GE-Proton10-34 上是work的。仍不作长期承诺，但不再是拦路项。
- **附录 B 第 3 条**（「PTY + 降权组合真机未验证」）：这次就是降权到
  `asa-umu-runtime` 且带 PTY 跑通的，验证完毕。

### 9.3 为什么原稿没预见到

§2.6 已经发现「微软安装器必须有 X 显示」，但当时把它归类成**安装器**的毛病，
还专门写了一句「DISPLAY 是刻意只加给安装器的……无头服务端不该看见显示」
（`resolveInstallerDisplay` 的注释）。那个前提对 `ArkAscendedServer.exe` 成立，
对 `AsaApiLoader.exe` 不成立 —— 而两者的失败机理其实是同一个
（`winex11.drv` 连不上 → `CreateWindow` 失败），只是一个报 203、一个报 3。

**教训**：把「需要显示」当成某个具体程序的怪癖，而不是当成 Wine 的一条通用属性。

### 9.4 落地

| 位置 | 改动 |
|---|---|
| `internal/runner/display_linux.go`（新增） | 从 `vcredist_linux.go` 抽出 `displayTarget` / `resolveDisplay`，**vc_redist 安装与 ArkApi 启动共用同一份逻辑**；三级解析 + X11 握手探测 + `/tmp/.X11-unix` 可写性判断（见 §9.5）；`wrap()` 把 `DISPLAY=` 或 `xvfb-run …` 施加到一条命令上 |
| `internal/runner/preflight_linux.go` | 新增 `checkDisplay()`，**阻断级**，直接问 `resolveDisplay`。理由见下 |
| `internal/runner/runner.go` | `Options.NeedsDisplay`；`DisplayInfo` + `DisplayStatus()` |
| `internal/runner/runner_linux.go` | `NeedsDisplay` 时解析显示并包住命令；拿不到显示**直接返回错误**，不让实例假装启动成功 |
| `internal/instance/server.go` | `NeedsDisplay: arkAsaApiRunning`；启动前用 `DisplayStatus()` 做一次实例名带上下文的硬校验 |
| `internal/installer/verify_arkapi.go` | `NeedsDisplay: true`；顺带补上一直缺的 `Options.Dir`（此前沿用 asa-server 进程的 cwd，与实例启动传镜像目录不一致） |
| `internal/actions/verify_arkapi.go` | 新增 `[3] 图形显示` 一节，排在 VC++ 前面 —— 它比 VC++ 更硬 |
| `internal/webapi/systemapi` | `GET /api/system/preflight` 返回 `display` |

**为什么 `xvfb` 可以是阻断级，而 `acl` 不行**（`ACL_PERMISSION_HARDENING_PLAN.md` §1
的教训是「别把能用的机器挡在门外」）：缺 `acl` 会降级成**能用的** chown 方案；
缺显示**没有第二条路** —— ArkApi 与 VC++ 安装器都彻底跑不了。区别不在于严重程度，
在于有没有降级路径。

**自检不看「当前 shell 的 `DISPLAY` 变量」，但看「系统里有没有能连的 X 服务」**：
前者不可靠 —— `setup` 常在有桌面的会话里敲，而真正拉起实例的服务进程没有 `DISPLAY`
（真机 `/proc/<pid>/environ` 里只有 `HOME=/root`），认它会让检查恰好在会出问题的
机器上通过；后者是磁盘上的 socket + 一次握手，服务进程同样看得见，所以可以认。

**不传 `XAUTHORITY`**：它经常指向 `/run/user/0` 下的路径，而 pressure-vessel 会
去 bind 环境变量点名的每个路径 —— 这正是 `DBUS_SESSION_BUS_ADDRESS` 那次坑掉一晚上的
同一类问题（见 `inheritedEnv` 的注释）。需要 cookie 的显示请走 `xvfb-run`，
它自带 auth、自成一体（那份 `XAUTHORITY` 由 xvfb-run 自己设，路径也是我们指定的）。

**只给 `AsaApiLoader.exe` 加显示，不给 `ArkAscendedServer.exe` 加**：后者在同一台
无头机上 42 秒就开始监听，给每次启动都套一层 `xvfb-run` 是白添一个进程和一个失败点。

### 9.5 第二轮：装了 xvfb 也可能没用 —— `/tmp/.X11-unix` 只读

第一版实现把「有没有 `xvfb-run`」当成判据。**装上 xvfb 之后后台进程启动实例仍然失败**，
日志里多了这两条：

```
pressure-vessel-wrap[137270]: W: X11 socket /tmp/.X11-unix/X100 does not exist
                                 in filesystem, trying to use abstract socket instead.
...
System.PlatformNotSupportedException: Video driver  not supported
  at Xalia.Sdl.WindowingSystem.Create () ...
```

现场确认：

```
$ mount | grep X11
none on /tmp/.X11-unix type tmpfs (ro,relatime)      ← WSLg 把它挂成只读
$ touch /tmp/.X11-unix/probe
touch: cannot touch '/tmp/.X11-unix/probe': Read-only file system
```

`/tmp/.X11-unix` 的路径**写死在 xtrans 里**，不受任何环境变量影响。目录只读 ⇒
`Xvfb :100` 建不出文件 socket，只剩一个抽象 socket ⇒ pressure-vessel 没法把它 bind
进容器 ⇒ 容器里的程序连不上显示，回到那个「零输出」的失败模式。

放大伤害的是 **`xvfb-run` 在 Xvfb 启动失败时仍然会照常执行命令**，而且它默认把
Xvfb 的输出丢进 `/dev/null`（`-e` 的默认值）—— 所以从退出码和日志都看不出发生了什么。

> 为什么第一轮的真机验证（§9.6 表里 44 秒那条）在同一台机器上过了：那次
> pressure-vessel 退到抽象 socket 之后**碰巧连上了**（抽象 socket 按网络命名空间
> 而非路径寻址，容器没有 unshare net）。也就是说这条路不是不能用，而是**不可靠** ——
> 这正是不该赌的理由。

**修正后的解析顺序**（`resolveDisplay`），三条路，每条都验证过而不是猜的：

> ⚠️ 这张表已两次被取代：第 2 条的 `xvfb-run` 换成了自管 `Xvfb`
> （`docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md`），顺序又改成了「点名的 > 自己管的 >
> 捡来的 > 扫出来的」四档并返回候选链
> （`docs/ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md`，2026-09-01）。以那两份为准。

| # | 路径 | 前提 |
|---|---|---|
| 1 | 显式 `DISPLAY` | 变量非空、socket 文件在、**X11 握手能过** |
| 2 | `xvfb-run` | 装了 **且 `/tmp/.X11-unix` 可写**（无头服务器的正路：自带显示与 auth，不依赖桌面会话，进程树归我们） |
| 3 | 系统里已在运行的 X 显示 | 扫 `/tmp/.X11-unix/X<n>`，**逐个握手**，取第一个能过的。这条是 ① 的补丁：服务进程通常**没有** `DISPLAY`（真机 `/proc/<pid>/environ` 里只有 `HOME=/root`），但机器上确实有能用的 X 服务 —— WSLg 的 `:0` 就是 |

两处关键改进：

- **握手代替猜测。** ①③ 都真的连一次 X 服务并走一遍**无认证**的连接建立
  （12 字节 setup 请求，看回包第一个字节 `1=Success`）。必须做到这一步，因为本项目
  刻意不传 `XAUTHORITY`（理由同 `inheritedEnv`：它常指向 `/run/user/0` 下的路径，
  pressure-vessel 会去 bind 它，降权后整个容器就起不来）—— 一个需要 cookie 的显示
  对我们就是不可用的，而它的 socket 文件明明在。拿文件存在当判据会挑中一个连不上的
  显示，然后又变成那个谜题。代价是几微秒，无需任何 X 库。
- **`/tmp/.X11-unix` 可写性用 `access(2)` 判**：root 会绕过权限位，但**绕不过只读挂载**
  （返回 `EROFS`），而只读挂载正是这次的坑；再叠一条 `o+w`，因为跑 Xvfb 的是降权用户，
  root 能写不代表它能写。

顺带修掉 `xvfb-run` 两个有害的默认值（都不是调优而是纠错）：

- `-e`：改为写运行时 HOME 下的 `xvfb.log`，不再丢 `/dev/null` —— Xvfb 起不来时至少有第一手证据；
- `-f`：改为写运行时 HOME 下的 `.Xauthority-xvfb`，默认值是 `./.Xauthority`，
  也就是**游戏工作目录**（实例镜像的 Win64）。

preflight 也从「找 `xvfb-run` 这个文件」改成**直接问 `resolveDisplay`** ——
两者分家过一次，代价就是自检通过、启动照样死（`TestCheckDisplayAgreesWithResolve`
钉住这一条）。检查名也随之从 `xvfb` 改成 `x11-display`。

### 9.6 真机验证（2026-08-30，WSL2 + WSLg，`/tmp/.X11-unix` 只读）

| 场景 | 结果 |
|---|---|
| `--check-only`，无 DISPLAY、未装 xvfb | `[3] ✘ 本机没有可用的 X 显示，也没有 xvfb-run` + 安装提示 |
| `--check-only`，`DISPLAY=:0` | `[3] ✔ 宿主的 X 显示 :0` |
| `--check-only`，装了 xvfb、`env -u DISPLAY`（**第一版**） | `[3] ✔ xvfb-run（虚拟显示）` —— 判断本身就是错的，见 §9.5 |
| `--check-only`，装了 xvfb、`env -u DISPLAY`（**修正后**） | `[3] ✔ 系统里已在运行的 X 显示 :0（本机 /tmp/.X11-unix 只读，起不了 Xvfb）` |
| **完整启动，`env -u DISPLAY`**（= 后台服务进程的真实环境） | ✅ **52 秒开始监听**；`ArkApi_372_2026-08-30_11-48.log`：`API was successfully loaded` → `Loaded plugin Ark:SA Permissions V1.1` → `AShooterGameMode::InitGame was called` → `SERVER ID: 102008681` |
| 结束后残留进程 | 无 |

第一版（走 xvfb-run 抽象 socket 那次）也跑通过一遍 44 秒的完整启动，
证明 `xvfb-run` 分支本身是通的 —— 但它在同一台机器上时灵时不灵，所以现在只在
`/tmp/.X11-unix` 可写时才走。整个验证都是在降权到 `asa-umu-runtime` + PTY 的组合下做的，
顺带关闭了附录 B 第 3 条那个「真机未验证」的风险。

### 9.7 第三轮：`xvfb-run` 不是每个发行版都有 → `docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md`

§9.5 那张三级解析表的第 2 条把「有没有 `xvfb-run`」当成了「能不能开虚拟显示」的判据。
`xvfb-run` 是 **Debian 打包时自带的一个 shell 脚本**，不是 X.Org 的组件：
Fedora / RHEL / Arch 只给 `Xvfb` 服务端二进制，于是那些机器**明明能开虚拟显示，
自检却判它不能**，`asa-server setup` 直接被挡住；而 `Xvfb`（服务端，前台常驻）与
`xvfb-run`（包装器，接受「要跑的命令」）命令形态完全不同，`displayTarget.Wrapper`
那套命令前缀抽象对它不成立。

修法是**由 asa-server 自己拉起并托管 Xvfb**（进程内单例、用前握手健康检查、
`-displayfd` 挑号），并把 `resolveDisplay` 拆成只读的 `planDisplay`（preflight 用）
与会真的起进程的 `acquire`（启动路径用）。**方案与验证矩阵见
`docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md`**，本节此后只保留结论。

同一类错误的第二次：判据落在「某个发行版给不给某个脚本」上，而不是落在能力本身。

---

## 附录 A：winetricks 路线（备选，未采用）

**我们已经把 winetricks 的核心做法抄进来了**（§2.4 的 override、§2.3 的退出码表、
§2.5 的 msvcp140 已知问题），所以这条备选路线现在只剩「让 winetricks 自己跑一遍」
这一个差别：

```bash
WINEPREFIX=<prefix> GAMEID=<gameid> PROTONPATH=<GE-Proton> umu-run winetricks -q vcrun2022
```

要点：

- umu 会优先用 `$PROTONPATH/protonfixes/winetricks`，找不到才退回宿主 `PATH` 上的
  winetricks（`umu_run.py`，见 §1.3）—— 所以宿主不一定要装 winetricks，但**不能假定
  GE-Proton 一定带**，需要先 `ls $PROTONPATH/protonfixes/winetricks` 确认。
- 已装过的动词 umu 会 **`sys.exit(1)`**，所以退出码 1 不能当失败处理。
- winetricks 自己会去微软下载安装包（走它的 wget/curl，我们的 `download.http_proxy`
  够不着），且它用的是**人工维护的哈希表**（§1.2），版本落后于 aka.ms 上的当前版。
  对国内网络这是明确的退步，也是没选它的主要原因。
- 剩下的唯一实质好处：`vcrun2022` 里那段 **CAB 抽取替换 msvcp140**（§2.5 的 Level 2）
  它已经写好了，还额外依赖 `cabextract`。如果真机验证证明 Level 2 是必须的，
  「直接调 winetricks」与「自己实现 CAB 抽取」这笔账要重新算一次 ——
  前者多一个宿主依赖但零维护，后者多几十行代码但下载仍归我们管。
  **这个决定等真机数据，不要现在拍。**

## 附录 B：ArkApi 在 Wine 下还可能缺什么

本方案只解决 VC++ 运行时这一项。真机验证时若仍失败，按下面的顺序排查，
**结论应回填本文档**：

0. **图形显示** —— ✅ **实测就是这一条**，见 §9。现象是「起不来且零输出、
   `Win64/logs/` 都不建、退出码 3」。`asa-server verify-arkapi` 的 `[3]` 直接给结论；
   手工确认用 `WINEDEBUG=` 默认级别跑一次，看有没有 `nodrv_CreateWindow`。
1. **DLL 加载顺序**（风险 1）→ `WINEDEBUG=+loaddll`，见 §2.4。
   若日志显示 `msvcp140` 走的是 builtin 且 ArkApi 正因此失败，那是 §2.5 的
   Level 2 触发条件，不是 override 写错了。
2. **注入/hook 机制**：`AsaApiLoader.exe` 走的是典型的
   `CreateProcess(CREATE_SUSPENDED)` + `WriteProcessMemory` + `CreateRemoteThread`
   远程注入。Wine 对这套 API 有实现但历史上有边界情况，
   `WINEDEBUG=+process,+thread` 能看出注入是否成功。
3. ~~**PTY 与降权的组合**~~ ✅ **已验证**（§9.2）：降权到 `asa-umu-runtime` 且带 PTY
   的那次跑通了完整的 ArkApi 加载，`docs/UMU_RUNTIME_USER_PLAN.md` §9 风险 1 可关闭。
4. **插件目录大小写**：`internal/plugindata/casecheck_linux.go` 已经会在日志里把磁盘
   实际大小写打出来，先看有没有那条告警。
5. **共享写权限**：以 root 上传的插件文件降权进程写不了 —— `asa-server perms status`，
   见 `docs/ACL_PERMISSION_HARDENING_PLAN.md`。
