# Linux 兼容改造方案

> 目标：让 asa-server 在 Linux 上以**同一套 Go 代码库**运行，仍然启动 **Windows 版 ARK 服务端 exe**，
> 通过 [umu-launcher](https://github.com/Open-Wine-Components/umu-launcher) + GE-Proton 提供 Wine 运行时。
> 参考实现：`scripts/ark_instance_manager.sh`（社区脚本，已在 Linux 上跑通完整 ASA 多实例流程，本方案大量沿用它踩过的坑）。
>
> 状态：**设计方案，P0–P6 已实施**（真实 Linux 主机上的端到端验证仍待补，见各阶段"已知限制"）。文档给出耦合点清单、抽象层设计、分阶段实施计划与验收标准。

---

## 0. 修订记录：已合入的上游改动

本方案初稿之后，仓库里落地了两项与它有交集的改造，以及一个新的选型决定。三者都已就地合入正文，
此处只列**改变了结论**的部分，细节看各自章节。

| 上游改动 | 状态 | 对本方案的净影响 | 落在哪 |
|---|---|---|---|
| **镜像去管理员化（真 NTFS junction）**<br>`MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` 第一部分 | 已实施 | **净正**，但工作量搬了家 | §2.1（新增编译阻断行）、§2.3、**§5.6 已重写**、§5.9、§6 风险 13、§8 P0、§10.7、§11 A |
| **移除 WebAuthn**<br>同文档第二部分 | 已实施 | **无影响** —— 删掉的 `go-webauthn` / `go-tpm` / `fxamacker/cbor` 全是纯 Go，两平台一视同仁；`auth` 本就在 §2.3 的跨平台清单里 | 无需改动 |
| **ArkApi 插件数据隔离**<br>`ARKAPI_PLUGIN_DATA_PLAN.md` | 已实施 | **基本无影响**，新增 `internal/plugindata` 已核对为跨平台；但它在 Linux 上应当整体静默，有四条要显式确认 | §2.2、§2.3、**§5.12 新增**、§6 风险 11/16、§8 P6、§9.1 |
| **frp 改为库内调用**（本次新增决定） | 已实施 | **减少** Linux 工作量：frp 从「分平台内嵌二进制」直接退出工作清单 | **§5.10 已重写**、§5.9、§6 风险 14/15/16、§8 F 轨道、§9.1、§11 A |
| **ArkApi 在 Linux 上不再是非目标**（P2 阶段新增决定） | 已实施 | **推翻**原「Linux 上标记为不支持，强制忽略开关」的结论：`EnableAsaPlugin` 在 Linux 上与 Windows 走同一个开关、同一条 `runner` 启动路径（umu-run 拉起 `AsaApiLoader.exe`，与拉起 `ArkAscendedServer.exe` 无特殊区分）。**不是**「确认能在 Wine 下稳定工作」——只是不再由程序单方面替用户关掉，社区已有在 Proton 下跑 ArkApi 的先例，让用户自己试、失败了看日志，比强制拦截更符合这个项目「能不能跑起来用户自己判断」的一贯取向 | §1 非目标表、§5.12、§6 风险 11、§8 P6、§9.1/9.2、§11 A |

四个最值得记住的结论：

1. **去管理员化把 `mirror` 从「基本不用改」变成了「Linux 上编译不过」** —— 因为 `createJunction`
   进了 `junction_windows.go`。代价很小（补 8 行 `os.Symlink`），但它是 P0 的硬阻断，不能漏。
   同一次改造顺手把 `isJunctionOrSymlink` 换成了 `os.Readlink`，**那一处从此不需要拆平台**。
2. **`plugindata` 不需要为 Linux 做任何事，但要确认它真的安静** —— 它默认静默是结构性的
   （以镜像里实际存在的插件目录为准），不是碰巧。
3. **frp 是 Go 写的，没理由当二进制内嵌。** 改成库内调用之后，§5.10 从一条 Linux 兼容工作项
   变成一条与 Linux 无关的架构改进，可以先做、单独做。
4. **ArkApi 是否支持这件事，交给用户判断，程序不替用户关。** `runner` 的 `Run()`/`Options`
   对 `ArkAscendedServer.exe` 与 `AsaApiLoader.exe` 一视同仁——两者都只是「一个要通过 umu-run
   拉起的 Windows exe」，不存在只服务前者的特殊接口。`plugindata` 结构性静默（§5.12）依旧成立，
   但不再是因为「Linux 不支持 ArkApi」，而是因为镜像里确实没有插件目录时它天然什么都不用做；
   一旦用户真的把 ArkApi 跑起来了，`plugindata` 照常工作。

---

## 1. 目标与非目标

### 目标

1. `GOOS=linux go build` 能产出可用二进制，`asa-server api` 在 Linux 上提供与 Windows 完全一致的 HTTP API 与前端。
2. 实例的**创建 / 启动 / 停止 / 重启 / 强制停止 / 更新 / 备份 / 定时任务 / RCON / 日志流**在 Linux 上行为等价。
3. Windows 侧行为**零回归**——所有平台特化通过构建约束隔离，Windows 编译产物的代码路径不变。
4. Linux 侧运行时（umu-launcher zipapp、GE-Proton、Wine prefix）由程序自己下载与管理，落在 `{BaseDir}` 内，
   不依赖发行版打包，与现有 SteamCMD 的自管理方式一致。
5. 🆕 `EnableAsaPlugin`（ArkApi）在 Linux 上是与 Windows 相同的用户开关，走同一条 `runner` 启动路径
   （umu-run 拉起 `AsaApiLoader.exe`，与拉起 `ArkAscendedServer.exe` 无特殊区分）。**这不是「保证能用」**——
   Wine/Proton 下的进程注入/DLL hook 手法是否稳定完全看社区经验（`ark_instance_manager.sh` 一类脚本有
   在 Proton 下跑起来的先例），程序不替用户判断、不强制拦截，出问题时如实报错，不静默降级。

### 非目标（本期明确不做）

| 项 | 原因 |
|---|---|
| Fyne 桌面 GUI 跑在 Linux | ARK 服务器场景基本是无头机器；Fyne 需要 X11/Wayland + OpenGL + cgo，成本高收益低。Linux 构建直接排除 GUI。 |
| 原生 Linux ARK 服务端 | 不存在。ASA 官方只发 Windows 专用服务器（AppID 2430930）。 |
| macOS | 同上，且无 Proton 生态支撑。 |
| 把 Windows 侧也切到新抽象的「大一统重写」 | 除少数几处（见 §5.2 端口→PID）外，Windows 实现原样保留，降低回归面。 |

---

## 2. 现状盘点：Windows 耦合点清单

代码库当前**完全没有构建约束**——`find . -name "*_windows.go"` 与 `grep -rl "//go:build windows"` 均为空，
`main.go:39` 直接 `runtime.GOOS != "windows"` 就退出。所有平台耦合都是「裸写」的。

按耦合类型分类：

### 2.1 阻断编译（Linux 上根本编译不过）

| 位置 | 内容 | 说明 |
|---|---|---|
| `pkg/winproc/win32api.go` | `golang.org/x/sys/windows` 全套 | 窗口枚举、`OpenProcess`、`QueryFullProcessImageName`、`ShellExecute` 提权 |
| `pkg/winproc/wmi.go` | `github.com/yusufpapurcu/wmi` | `QueryProcess(name, commandLine)` 靠 WQL |
| `pkg/processjob/job.go` | Job Object + `syscall.CREATE_NEW_PROCESS_GROUP` | 进程树管理 |
| `internal/certmgr/store.go` | `windows.Cert*` 系统证书存储、`windows.Token` | 本地 CA 写入 Root 存储 |
| ~~`internal/mirror/mirror.go:97`~~ | ~~`windows.OpenProcessToken`~~ | ✅ 已移除（`IsElevated()` 随去管理员化删除）|
| **`internal/mirror/junction_windows.go`** | `//go:build windows` + `DeviceIoControl` / `FSCTL_SET_REPARSE_POINT` | 🆕 【去管理员化引入】`createJunction` 已移入这个 windows-only 文件，而 `mirror.go` 无构建约束且有 6 处调用（`mirror.go:233,300,351,376,691,887,900`）—— **`internal/mirror` 现在在 Linux 上直接编译不过**，需补 `junction_linux.go`，见 §5.6 |
| `internal/gui/gui.go` | `windows.SID` / `syscall.SysProcAttr{HideWindow}` / Fyne | 整包 |
| `internal/process/process.go:87` | `syscall.SysProcAttr{HideWindow: true}` | Linux 的 `SysProcAttr` 无此字段 |
| `pkg/tail/tail.go:276` | `syscall.Win32FileAttributeData` | 文件身份（防日志轮转误读） |
| `internal/installer/installer.go` | `SysProcAttr{HideWindow}`（经 pty 间接） | — |
| `internal/frpmanage/manager.go:21`<br>`internal/syncthingmanage/manager.go:23` | `//go:embed frpc.exe` / `syncthing.exe` | 内嵌的是 Windows PE |

### 2.2 能编译但语义错误（Linux 上会静默跑错）

| 位置 | 内容 | Linux 后果 |
|---|---|---|
| `internal/process/process.go:85` | `exec.Command("netstat", "-ano")` | `netstat` 多数发行版默认不装；即便装了 `-ano` 也不是 Linux 参数 |
| `pkg/winproc/win32api.go:190` | 同上（`GetPIDByPort`） | 同上 |
| `internal/instance/server.go:557,559,582,589,635`<br>`internal/instance/common.go:108,110`<br>`internal/installer/installer.go:499,505` | `exec.Command("taskkill", ...)` | 命令不存在，停止流程全部降级为超时 |
| `internal/instance/server.go:410` | `exec.Command(arkExe, args...)` 直接执行 PE | Linux 内核 `ENOEXEC` |
| `internal/installer/installer.go:106,262,335` | `steamcmd.exe` | Linux 版是 `steamcmd.sh` + 32 位 ELF |
| `internal/config/config.go:27` | `SteamCmdURL = ".../steamcmd.zip"` | Linux 要 `steamcmd_linux.tar.gz` |
| `internal/instance/server.go:~322` | `-ClusterDirOverride=<Linux 绝对路径>` | UE 当成相对路径解析，产生 `/home/x/home/x/...`，且不同 CWD 得到不同簇目录，**跨服传角色直接坏掉** |
| `internal/process/process.go:119` | `expectedExecutables` 按进程镜像名匹配 | Linux 上镜像名是 `wine-preloader` / proton 的 `wine64`，判定全部落空，需改成扫 cmdline |
| ~~`internal/instance/common.go:129,469`~~ | ~~`winproc.QueryProcess("ArkAscendedServer.exe", "Port=...")`~~ | ✅ P1 已解决：包改名 `procx`，Linux 侧改为扫 `/proc/*/cmdline`，签名不变（见 §5.3、§8 P1） |
| `internal/appconfig/localnet.go:59` | 虚拟网卡名过滤含 `tap-windows` | 需补 `docker0`/`veth`/`br-`/`virbr` |
| `internal/gui/gui.go:539` | `rundll32 url.dll,FileProtocolHandler` 开浏览器 | — |
| `internal/certmgr/store.go:236` | `icacls` 收紧私钥 ACL | Linux 用 `os.Chmod(0600)` |
| ~~`main.go:281`~~ | ~~`winproc.RunAsAdmin` 提权~~ | ✅ 已移除（`certmgr/cli.go:67` 装根证书仍在用 `RunAsAdmin`，那处保留）|
| `internal/plugindata/override.go:85` | 路径包含判定用 `strings.ToLower` 折叠大小写 | 🆕 【ArkApi 插件数据引入】在大小写敏感的文件系统上是**过度匹配**（`/a/DB` 与 `/a/db` 被判为同一路径）。ArkApi 在 Linux 上不再是非目标（见 §1），这是需要在 P4 修的真 bug，见 §5.12 |

### 2.3 天然跨平台（无需改动）

`internal/webapi`（含全部子包，含新增的 `pluginapi`）、`internal/auth`、`internal/appconfig`、`internal/state`(BadgerDB)、
`internal/rconx`、`internal/realtime`、`internal/countdown`、`internal/batchmanage`、`internal/schedule`、
`internal/updatemanage`、`internal/backup`(tar+zstd 纯 Go)、`internal/parseserver`、`internal/logger`、
`internal/plugindata`、`pkg/fsutil`、`pkg/netutil`、`pkg/console`、`pkg/iox`、`pkg/serverinfo`(gopsutil)、`app/`、`icon/`。

**`internal/plugindata`（ArkApi 插件数据隔离，新增）已核对为跨平台**：无任何 `golang.org/x/sys/windows`
或 `syscall` 引用；相对路径一律以 forward slash 为规范形式、落盘前过 `filepath.FromSlash`
（`plugindata.go:194-223`、`classify.go:140-199`），并特意用 `slashBase` 而非 `filepath.Base`
（`plugindata.go:323` 的注释说明了原因）；在线快照用的 `modernc.org/sqlite` 是**纯 Go** 驱动，
在 linux/amd64 上不引入 cgo —— 这一点对 §5.9「Linux 侧 `CGO_ENABLED=0`」的结论很关键，
因为 `plugindata` 现在位于 `mirror` 与 `instance` 的依赖链上，是热路径而非可选组件。
唯一的例外是 `override.go:85` 的大小写折叠，见 §2.2 与 §5.12。

`internal/mirror` 的**核心算法**仍然跨平台，但边界已经变了：

- `isJunctionOrSymlink`（`mirror.go:413`）已改用 `os.Readlink` 判定 —— **本来就是为跨平台选的方案**
  （见 `MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` §1.3 方案 A），Linux 上对 symlink 同样正确，
  这一处不需要任何改动，也不需要拆平台文件。
- `createFileSymlink` 已被删除，第 ③ 类的 11 个根目录文件统一走 `fsutil.CopyFile` —— 跨平台无差异。
- `createJunction` 反过来成了**新的编译阻断点**（见 §2.1 新增行），Linux 侧要补实现，见 §5.6。
- `IsElevated()` 已随「镜像去管理员化」一并删除，本节无需再处理。

`internal/instance/common.go` 的 `GetAsaVersion`（`common.go:487`）是纯字节扫描 PE 文件找 UTF-16 `ArkVersion`
标记，**Linux 上原样可用**，不需要任何改动。

---

## 3. 总体策略

### 3.1 三条原则

1. **构建约束隔离，不用运行时 if**。平台差异一律通过 `_windows.go` / `_linux.go` 文件分离，
   而不是 `if runtime.GOOS == ...`。理由：`golang.org/x/sys/windows` 这类包在 Linux 上根本不可 import，
   运行时判断解决不了编译问题；且分文件后 Windows 编译产物一行 Linux 代码都不含。
2. **抽象放在最小接口面上**。不引入「平台服务大接口」，而是逐能力抽象：进程、启动器、进程树、文件身份、证书信任。
   每个抽象都是几个函数，签名与现有 Windows 实现保持一致，call site 尽量零改动。
3. **Linux 侧照抄 `ark_instance_manager.sh` 已验证的方案**。那份脚本的注释里记录了大量血泪
   （GE-Proton 11 挂死、GitHub API 限流、AppArmor userns、Sentry crashpad、steam_appid.txt、
   `~/.steam/sdk{32,64}` 软链），这些不是可选项，是**能不能启动起来的前提**。逐条落进代码。

### 3.2 新增 / 调整的包

```
pkg/
├── procx/                    # 【新】跨平台进程原语，取代 pkg/winproc 的对外角色
│   ├── procx.go              #   公共类型 + 文档
│   ├── procx_windows.go      #   现 pkg/winproc 实现平移
│   ├── procx_linux.go        #   /proc 扫描 + signal
│   └── port.go               #   端口→PID（gopsutil，两平台共用，见 §5.2）
├── proctree/                 # 【改名】原 pkg/processjob
│   ├── proctree_windows.go   #   Job Object（原样）
│   └── proctree_linux.go     #   setsid 进程组 + kill(-pgid)
├── download/                 # 【新】全局下载器 + GitHub 代理，两平台共用，见 §5.13
│   ├── download.go           #   Fetch(ctx, Options) —— 重试/断点续传/校验/进度上报
│   └── proxy.go              #   Configure(Config) —— GitHub 前缀重写 + 标准 HTTP(S)_PROXY
└── tail/
    ├── filekey_windows.go    # 【拆】Win32FileAttributeData.CreationTime
    └── filekey_linux.go      # 【拆】syscall.Stat_t.Ino（inode，比 ctime 更准）

internal/
├── runner/                   # 【新】exe 启动器抽象 —— 本方案的核心
│   ├── runner.go             #   Launcher 接口 + Options + Handle
│   ├── runner_windows.go     #   直接 exec.Command / pty.Command
│   ├── runner_linux.go       #   umu-run 包装 + Z: 路径转换
│   ├── umu_linux.go          #   umu zipapp / GE-Proton 下载、prefix 预热、依赖自检
│   └── fixups_linux.go       #   ASA-on-Wine 三项修复（Sentry / steam_appid / steam sdk 软链）
├── certmgr/
│   ├── store_windows.go      # 【拆】现 store.go
│   └── store_linux.go        # 【新】ca-certificates / ca-trust anchors
├── svcmgr/                   # 【改名】原 internal/winservice（kardianos/service 已支持 systemd）
└── gui/                      # 【加约束】整包 //go:build windows
```

**分层位置**：`runner` 依赖 `config` + `logger` + `pkg/*`，被 `instance` 与 `installer` 依赖。
即插在现有依赖链的 `config → [runner] → installer/instance` 之间，不引入环。

`pkg/procx` 的 `pkg/` 准入（见 `docs/INTERNAL_LAYOUT_MIGRATION.md` §9）：不认识实例概念、零领域依赖、无全局状态 —— 三条全中。
`internal/runner` 则认识 `BaseDir` 与实例目录布局，留在 `internal/`。

> **命名取舍**：也可以不改名，直接往 `pkg/winproc` 里加 `_linux.go`。零 call-site 改动，但包名骗人。
> 本方案选择改名（约 15 处 call site，机械替换），因为 `CLAUDE.md` 的目录表把 `pkg/` 当作对外文档在维护。

---

## 4. Linux 运行时环境

### 4.1 目录布局（在现有 `{BaseDir}` 下新增）

```
{BaseDir}/
├── umu-launcher/            # 【新】umu zipapp（含 umu-run），~5 MB
├── proton/
│   └── GE-Proton10-34/      # 【新】固定版本 GE-Proton，~450 MB
├── umu-prefix/              # 【新】Wine prefix（默认所有实例共享，见 §6 风险 6）
│   └── .created-by-proton   #   记录创建它的 Proton 版本，不匹配则重建
├── steamcmd/                # 内容变为 steamcmd.sh + linux32/ + linux64/
├── server-files/            # 不变（内容仍是 Windows PE）
├── instances/               # 不变
└── ...                      # config.yaml / certs / database_file / logs / backups 均不变
```

外部依赖（不在 BaseDir）：`$HOME/.local/share/umu/`（Steam Linux Runtime，umu 自管）、
`$HOME/.steam/sdk32|sdk64/steamclient.so`（软链到 steamcmd 自带的 `.so`，lsteamclient 硬编码这两个路径）。

### 4.2 宿主机依赖自检

启动时（Linux 分支）执行一次自检 —— 沿用脚本 `check_dependencies()`/`check_userns_restriction()` 的检查项，
重实现为函数性检查（探测实际产物是否存在/内核开关的实际值），而不是按发行版查包管理器：

> 🆕 **已实施，但比本节原稿弱一档**：原稿设想「缺项直接拒绝启动」。落地时改为**启动即打日志告警，
> 不阻断启动**——这个程序管理的不只是 Wine/Proton 相关的功能，且实例启动到 P4 之前都还不经过
> `runner`，此时就因为一个 Wine 依赖缺失而让整个 API 服务起不来面太大了。`GET /api/system/preflight`
> 把同一份检查结果暴露给前端，供其在合适的地方（引导页、设置页）展示。等 P4 把实例启动接上 `runner`，
> 到那时候「缺项时具体某个实例起不来」自然会在日志里体现，届时可以重新评估要不要收紧。

| 依赖 | 用途 | 缺失症状 |
|---|---|---|
| **32 位 glibc**（`libc6:i386` / `glibc.i686` / `lib32-glibc`） | SteamCMD 是 32 位 ELF | 内核对存在的文件返回 `ENOENT` |
| **python3 ≥ 3.10** | 跑 umu zipapp | zipapp 无法执行 |
| **libzstd.so.1** | umu 的 pyzstd 链接它 | umu import 失败 |
| `tar` | 解 GE-Proton / steamcmd | — |
| `kernel.apparmor_restrict_unprivileged_userns = 0` | pressure-vessel 的 bwrap 建容器 | `bwrap: setting up uid map: Permission denied`（Ubuntu 23.10+ 默认开启） |

> AppArmor 那条尤其要**启动即检查**并给出 `sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`
> 的修复提示。它是 Ubuntu 系用户最高频的「啥日志都没有就是起不来」。

建议在 API 层暴露 `GET /api/system/preflight`，前端在 Linux 上展示自检结果，而不是只丢进日志。

### 4.3 版本固定策略（照抄脚本，不要自作聪明）

```go
const (
    umuVersion     = "1.4.4"       // 已实施：internal/runner.defaultUmuVersion，2026-08 时点的最新稳定版
    geProtonPinned = "GE-Proton10-34"
)
```

- **GE-Proton 必须钉在 10 系**。GE-Proton11-1（Wine 11 基座）与 `ArkAscendedServer.exe` 有回归：
  进程在静态导入解析阶段永久挂起（最后加载的 DLL 是 `imm32.dll`），没有崩溃、没有异常、没有任何 UE 日志。
  升 11.x 前必须做一次完整冷启动验证（成功判据：出现 `minidumps folder is set to /tmp/dumps` 后跟 UE 日志输出）。
- **自己下载 GE-Proton，不要用 umu 的 `PROTONPATH=GE-Proton` 别名**。别名会让 umu 走 `api.github.com`
  解析最新 release，未认证限流 60 次/小时/IP，在容器、CI、CGNAT、共享出口下频繁失败，
  且 umu 1.4.0 在 `PROTONPATH` 解析为空时直接 `FileNotFoundError` 崩掉。
  改为从 release **下载 URL**（不限流）拉固定版本，`PROTONPATH` 指向具体目录。
  这次下载走 §5.13 的 `pkg/download`，国内网络访问 `github.com` 慢/抖动时可配 `github_proxy` 走加速，
  与「限流」是两个独立问题，不要混为一谈——代理解决的是**下载慢**，固定版本号解决的是**限流**。
- 常规启动一律带 `UMU_RUNTIME_UPDATE=0`；只有首次 `wineboot --init` 预热那一次不带（它必须能拉运行时）。
- 版本可通过 `config.yaml` 覆盖（见 §7），但默认值就是上面这两个。

---

## 5. 分模块改造方案

### 5.1 `internal/runner` —— 启动器抽象（核心）

> ✅ 接口本身已在 P2 落地；本节描述的「接进 `instance.StartServer`」这一步已在 P4 完成
> （`internal/instance/server.go` 的 `startServerInternal`）。

```go
package runner

// Options 描述一次 exe 启动的平台无关意图。
type Options struct {
    Dir     string   // 工作目录（宿主机路径）
    Env     []string // 追加环境变量
    PTY     bool     // 是否需要伪终端（SteamCMD / AsaApiLoader）
    LogFile string   // 直接落盘的输出文件；空则由调用方接管 Stdout/Stderr
}

// Handle 是一次启动的句柄。Windows 上 LauncherPID == 游戏 PID；
// Linux 上 LauncherPID 是 umu-run，游戏 PID 需要另行解析（见 §5.3）。
type Handle struct {
    LauncherPID int
    Cmd         *exec.Cmd
    PTY         pty.Pty // 仅 PTY 模式
}

// Run 启动一个 Windows PE。
func Run(ctx context.Context, exePath string, args []string, opt Options) (*Handle, error)

// GamePath 把宿主机路径转成 exe 参数里可用的路径。
// Windows: 原样返回。Linux: /a/b -> Z:\a\b。
func GamePath(hostPath string) string

// EnsureRuntime 保证运行时就绪（Windows 为 no-op；Linux 下载 umu + GE-Proton 并预热 prefix）。
// progress 用于把下载/预热进度透传到既有的 SSE TaskBroadcaster。
func EnsureRuntime(ctx context.Context, progress io.Writer) error

// Preflight 宿主依赖自检（Windows 为 no-op）。
func Preflight() []Problem
```

**Windows 实现**（`runner_windows.go`）：几乎是现有代码的搬家 —— `exec.Command(exePath, args...)`
或 `pty.New()` + `pp.Command(...)`；`GamePath` 是 identity，`EnsureRuntime` / `Preflight` 返回 nil。

**Linux 实现**（`runner_linux.go`，✅ 已实施）关键点：

```go
cmd := exec.CommandContext(ctx, umuRunBin, append([]string{exePath}, args...)...)
cmd.Dir = opt.Dir
cmd.Env = append(os.Environ(),
    "WINEPREFIX="+prefixDir,
    "GAMEID="+gameID,            // umu-default
    "PROTONPATH="+geProtonPath,  // 具体目录，不是别名
    "UMU_RUNTIME_UPDATE=0",
)
// 关键：独立会话 + 进程组，脱离控制终端。
// 等价于脚本里的 setsid nohup ... </dev/null &，
// 保证 API 进程退出 / SSH 断开不会带走已启动的服务器。
cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
cmd.Stdin = nil
```

> 🆕 **`PROTON_VERB=run` 已从实现里去掉**：`scripts/ark_instance_manager.sh`（本方案要求照抄的验证过的参考实现）
> 的实际游戏启动调用（第 648-658 行）从未设置这个变量——umu-run 不设它时的默认行为已经是「运行游戏」，
> 加上反而是本文档原始设计阶段的多余猜测。以脚本的已验证行为为准，不是以这里的示意代码为准。

`GamePath`：

```go
// Wine 默认把 Z: 映射到 /，所以 /home/x/asa -> Z:\home\x\asa
func GamePath(p string) string {
    abs, _ := filepath.Abs(p)
    return "Z:" + strings.ReplaceAll(abs, "/", `\`)
}
```

**调用点改造**：`internal/instance/server.go` 里所有含路径的 exe 参数都要过 `runner.GamePath()`。
目前只有一处：`-ClusterDirOverride`（`server.go:~322`）。另外 `CustomStartParameters` 里用户可能自带路径参数，
Linux 下应在文档里说明需自行写 `Z:\` 形式，并在保存实例配置时对含 `-ClusterDirOverride` / `-UserDir`
之类的参数做一次校验告警。

> ⚠️ **簇目录还有一个 UE 自身的坑**（脚本注释 issue #31）：ARK 会在 `-ClusterDirOverride` 之后**自己再拼
> `clusters/<ClusterId>/`**。所以 override 必须传 **BaseDir**，而不是 `{BaseDir}/clusters` 或
> `{BaseDir}/clusters/<id>`，否则得到 `clusters/clusters/<id>`。
> 现有 Windows 代码传的是 `filepath.Join(BaseDir, "clusters", ClusterID)`，
> **需要在改造时一并核对 Windows 上的实际落盘位置**，两平台统一。

### 5.2 端口 → PID：两平台统一切到 gopsutil

现状：Windows 走 `netstat -ano` 文本解析（`process.go:85`、`win32api.go:190`）。
`github.com/shirou/gopsutil/v4` 已经是直接依赖（`pkg/serverinfo` 在用），`net.Connections("all")`
在 Windows 走 `GetExtendedTcpTable`/`GetExtendedUdpTable`，在 Linux 走 `/proc/net/*` + `/proc/*/fd` inode 匹配。

```go
// pkg/procx/port.go —— 无构建约束，两平台共用
func PIDByPort(port int) (int, error) {
    conns, err := net.Connections("all")
    // ...
    // ARK 游戏端口是 UDP、RCON 端口是 TCP —— 必须同时覆盖。
    // 现有 netstat 文本匹配是靠 ":port" 子串顺带覆盖到 UDP 的，切换时别丢。
}
```

收益：删掉两处脆弱的文本解析、去掉一次子进程 fork、Linux 免费获得实现。

风险：gopsutil 在 Windows 上枚举全部连接比 `netstat` 慢（毫秒级，可接受）；
Linux 上需要确认 Wine 进程的 socket 可见（pressure-vessel 容器**共享宿主 PID/network namespace**，正常可见）——
这是 P1 阶段必须实测确认的一条，若不可见则回退到 §5.3 的 cmdline 扫描法。

### 5.3 PID 语义：Linux 上「启动器 PID ≠ 游戏 PID」

> ✅ 本节四条已在 P4 全部落地。

Linux 下 `umu-run` 是 Python zipapp，它 exec 进 bwrap 容器再拉起 wine 再拉起 exe，
`cmd.Process.Pid` 拿到的是 umu-run，**不是** `ArkAscendedServer.exe`。

改造：

1. 实例目录下 PID 文件从 1 个变 2 个：
   - `pid` —— 游戏进程 PID（语义不变，Windows 上仍是 `cmd.Process.Pid`）
   - `launcher_pid` —— 【Linux 新增】umu-run 的 PID，同时也是**进程组 ID**（因为 `Setsid: true`）
2. `internal/process` 增加 `SaveLauncherPID` / `GetLauncherPID`（Windows 上写同一个值，保持结构一致）。
3. 启动后用与脚本 `pgrep -f "ArkAscendedServer.exe.*AltSaveDirectoryName=$SAVE_DIR"` 等价的方式解析游戏 PID：

```go
// pkg/procx/procx_linux.go
// QueryProcess 扫描 /proc/*/cmdline，匹配 exe 名 + 命令行子串。
// 这正是 winproc.QueryProcess(name, commandLine) 的 WMI 版语义，签名保持一致。
func QueryProcess(name, cmdlineSubstr string) ([]Process, error)
```

调用方 `instance/common.go:129,469` 的 `winproc.QueryProcess("ArkAscendedServer.exe", "Port=...")`
签名不变，只是底层换实现。**但匹配键建议从 `Port=` 换成 `AltSaveDirectoryName=`**：
`Port=` 会被 `-QueryPort=` / `-RCONPort=` 的数值误伤，而 SaveDir 在本项目里天然按实例唯一。
（这条对 Windows 的 WMI 实现同样成立，建议一并修正。）

4. `process.isExpectedProcess`（`process.go:119`）在 Linux 上按 cmdline 判定而非镜像名 ——
   Wine 下 `/proc/<pid>/exe` 指向 `wine-preloader` 或 proton 的 `wine64`，镜像名判定会全部落空。
   它原本的目的（防 PID 复用误判存活）在 Linux 上同样必要，只是判据换成「cmdline 含 ArkAscendedServer.exe」。

   ✅ 已实现为 `isExpectedProcessPlatform`（`process_windows.go` 镜像名 / `process_linux.go` cmdline），
   新增跨平台 `procx.ProcessCmdline(pid)`（Windows 走 WMI 按 ProcessId 查，Linux 读 `/proc/<pid>/cmdline`）。
   **落地时发现一个真 bug**：WMI 查询只 `SELECT CommandLine` 而不带 `ProcessId`/`Name` 时，
   `wmi.Query` 会报 `cannot load field "ProcessId" into a "uint32": no such struct field`——
   它按目标 struct 的**全部字段**去映射查询列，少选一列就炸。写测试時第一次跑就炸出来了，
   修法是三个字段都 `SELECT`（同 `QueryProcess` 的查询）。这处 Windows 实现目前没有实际调用方
   （`isExpectedProcessPlatform` 的 Windows 版仍用原来的 `ProcessImageName`，未改），
   但作为 `procx` 对外原语保留、有真实单测覆盖。

### 5.4 停止流程

> ✅ 已在 P1（`procx.Terminate*` 抽象本身）+ P4（`stopServerInternal`/`ForceStopServer` 接入）落地。
> `stopServerInternal` 的映射：RCON 失败后的立即兜底 = 表格「温和结束」行（`procx.Terminate`/`Kill`，
> 单 PID）；5 分钟超时后的最终强杀从 `procx.Kill` 升级为 `procx.KillTree`（表格「强杀进程树」行）——
> Windows 上这一升级近乎无感（游戏进程本没有有意义的子进程），Linux 上是必需的（否则 5 分钟超时
> 这条路径会把 umu-run/bwrap/wine 整棵树留成孤儿，见下面的风险说明）。`ForceStopServer` 新增第 4 步
> 兜底：读 `launcher_pid` 后 `killGameServer`（`TerminateTree`/`KillTree`），前三步（AsaApiLoader PID /
> cmdline 扫描 / 已存 pid）都拿不到有效 PID 时仍有一条路能杀干净。

现有语义（`instance/server.go:520+`）：RCON `saveworld` → RCON `DoExit` → 等待 → 超时强杀。
前两步跨平台无改动，只有兜底 kill 要换：

| 场景 | Windows | Linux |
|---|---|---|
| 温和结束 | `taskkill /PID <pid>` | `syscall.Kill(pid, SIGTERM)` |
| 结束进程树 | `taskkill /PID <pid> /T` | `syscall.Kill(-launcherPGID, SIGTERM)` |
| 强杀进程树 | `taskkill /F /PID <pid> /T` | `syscall.Kill(-launcherPGID, SIGKILL)` |

抽象为 `procx.Terminate(pid)` / `procx.TerminateTree(pgid)` / `procx.KillTree(pgid)`，
替换掉 `server.go:557,559,582,589,635`、`common.go:108,110`、`installer.go:499,505` 共 9 处 `taskkill`。

> Linux 上必须杀**进程组**而不是单个 PID：umu-run → bwrap → wine 是一整棵树，
> 只杀游戏 PID 会留下 bwrap/wineserver 孤儿，下次启动 prefix 被占。
> 这也是 `Setsid: true` 的另一个理由 —— 有了独立进程组，`kill(-pgid)` 才不会误伤 API 进程自己。
> kill 前务必断言 `pgid > 1 && pgid != os.Getpid()`。

### 5.5 `internal/installer` —— SteamCMD 与 ARK 安装

> ✅ 本节已实施（P3）。落地时有两处偏离本节原稿的具体安排，都记在下面对应的段落里：
> fixups 落在 `internal/installer/fixups*.go` 而不是原稿建议的 `internal/runner/fixups_linux.go`；
> 首次配置生成的轮询等待改成了「超时也报错」而不是原 Windows 代码那种「等完固定时间就无条件宣布成功」。

| 环节 | Windows | Linux |
|---|---|---|
| 下载 | `steamcmd.zip` → 解压 | `steamcmd_linux.tar.gz` → 解包，`chmod +x steamcmd.sh` |
| 可执行文件 | `steamcmd/steamcmd.exe` | `steamcmd/steamcmd.sh`（原生 ELF，**不经 umu**） |
| 初始化 | `steamcmd.exe +quit`（pty） | `steamcmd.sh +quit`（pty，go-pty 在 Linux 是真 pty） |
| 安装 ARK | `+force_install_dir ... +login anonymous +app_update 2430930 validate +quit` | **完全相同** |
| 首次配置生成 | 直接跑 `ArkAscendedServer.exe TheIsland_WP?listen ...` 固定等 60s | 经 `runner.Run()` 跑同一条命令，**轮询等待 `Saved/Config/WindowsServer/` 出现**（最长 180s）而非固定 sleep —— Wine 冷启动比 Windows 慢得多 |

✅ 已按第二个选项落地：`SteamCmdURL` 从 `config` 包删除，改为 `installer/steamcmd_windows.go` /
`steamcmd_linux.go` 里各自的 `steamCmdURL`/`steamCmdBinaryName`/`extractSteamCmdArchive`
（Windows 走原有 zip 逻辑，Linux 走 `pkg/archive.ExtractTar` 解 `tar.gz` + `chmod +x`）。

**Linux 独有：安装/更新后必须执行三项 ASA-on-Wine 修复**（✅ 已实施，落在 `internal/installer/fixups*.go`，
不在原稿建议的 `internal/runner/fixups_linux.go`——这三项认的是 `cfgpkg.ServerFilesDir`/`SteamCmdDir`
这类 installer 领域概念，`installer` 本来就已经知道这些路径，放这里比让 `runner` 反过来认识
installer 的目录布局更顺。前两项的路径逻辑（不含 `os.Symlink`）拆进了无构建约束的
`fixups.go`，配了真实单测，幂等）：

1. **禁用 Sentry crashpad 插件**：`server-files/ShooterGame/Plugins/sentry` → 重命名为 `sentry.disabled`。
   ASA 自带的 sentry-native crashpad 后端会从 Wine 的 TEB 读 `StackLimit`/`StackBase`，
   Wine 10 在那里返回巨大值，crashpad 试图 dump 数 GB 栈，引擎永远过不了 `sentry_init`。
   重命名后 `sentry_init()` 以 `invalid handler_path` 干净失败，引擎继续。
   注意 `validate` 会把插件重新下回来，所以**每次更新后都要跑**；且要先 `rm -rf sentry.disabled` 再 mv。
2. **写 `steam_appid.txt`**：在 `ShooterGame/Binaries/Win64/` 下写入 `2430930`。
   `lsteamclient.dll` 靠它在没有 Steam 客户端的情况下向 Steam SDK 表明身份。
   **要比对内容而不是只判存在** —— 旧版本可能写的是游戏 AppID `2399830`，那是错的（需要专用服务器 AppID）。
3. **软链 Steam SDK**：`$HOME/.steam/sdk32/steamclient.so` → `steamcmd/linux32/steamclient.so`，
   sdk64 同理。Wine 的 `lsteamclient.dll` 会 `dlopen()` 这两个**硬编码路径**（按运行用户的 HOME 解析，
   与 WINEPREFIX 无关）。缺失时服务器在 `FSteamServerInstanceHandler` 里崩溃。
   用 `-f` 语义覆盖，避免旧 BaseDir 留下的失效软链。

> 这三项直接决定「能不能起来」，不是优化项。✅ 已实现为 `installer.ApplyLinuxFixups()`（Windows 上是空实现），
> 目前接了两处：`DownloadAndUpdateArkServer` 成功后、`VerifyServerInstallation` 首次拉起验证前。
> **第三处——`instance.StartServer` 每次真实启动前——还没接**，那是 P4「`StartServer` 走 `runner`」
> 的范围，本次不越界去碰 `instance/server.go`。

### 5.6 `internal/mirror` —— 补一个 `junction_linux.go`

> 本节已按「镜像去管理员化」（`MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` 第一部分，**已实施**）重写。
> 那次改造对 Linux 兼容**净收益为正**，但把工作量从「基本不用改」挪成了「必须补一个文件」。

改造前后对 Linux 的影响：

| 项 | 改造前 | 改造后 | 对 Linux 的影响 |
|---|---|---|---|
| `createJunction` | `mirror.go` 里的 `os.Symlink`，无构建约束 | `junction_windows.go` 的 `DeviceIoControl` + `FSCTL_SET_REPARSE_POINT`，`//go:build windows` | ⚠️ **变差**：`internal/mirror` 现在在 Linux 上编译不过，必须补 `junction_linux.go` |
| `isJunctionOrSymlink` | 只查 `os.ModeSymlink` | `os.Readlink` | ✅ **变好**：本来就是冲跨平台选的（该文档 §1.3 方案 A），Linux 上直接正确，省掉一处将来必踩的坑 |
| `createFileSymlink` | `os.Symlink`，失败回退 `CopyFile` | **已删除**，统一 `fsutil.CopyFile` | ➖ 中性，见下方「11 个文件」的取舍 |
| `IsElevated()` / 提权重启 | 存在 | 已删除 | ✅ **变好**：§10.7 里那条「两平台都免特权建链接」的论述现在成立 |

**需要新增的文件**（P0 阶段）：

```go
//go:build linux

package mirror

// createJunction 在 Linux 上就是普通符号链接。
//
// 语义必须与 junction_windows.go 对齐：linkPath 已存在时**报错而不是覆盖**。
// os.Symlink 天然如此（返回 EEXIST），无需额外判断 —— 但不要"贴心地"加一层
// os.RemoveAll 再建：调用方（migrateExceptionJunctions / reconcileEntry）
// 全部依赖"先删再建"的显式顺序，静默覆盖会掩盖同步逻辑里的真实错误。
func createJunction(linkPath, targetPath string) error {
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for %s: %w", targetPath, err)
	}
	return os.Symlink(absTarget, linkPath)
}
```

三条实现约束，都不是可选的：

1. **必须用绝对路径做 target。** Windows 侧的 junction 存的是 NT 绝对路径（`\??\D:\...`），
   Linux 侧若存相对路径，语义就随 CWD 漂移了，两平台对不齐。
2. **`os.Lstat` 对 symlink 返回 `IsDir()==false`，`filepath.Walk` 因此不会递归进去** ——
   这与 Windows 上 junction 的行为一致（`MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` §1.3
   把这层结构性保护称作「为什么不会删到源」）。**Linux 侧继承同一层保护，不需要额外防护。**
3. **`isJunctionOrSymlink` 不拆平台。** `os.Readlink` 在 Linux 上对 symlink 成功、对普通目录返回
   `EINVAL`，判据与 Windows 侧完全同构。多写一个 `_linux.go` 只会增加两边漂移的机会。

**那 11 个根目录文件（110 MB）在 Linux 上要不要改回 symlink？**
技术上可以 —— Linux 的 symlink 免特权，`createFileSymlink` 在 Linux 上永远不会失败。
**但不要这么做。** 理由：`reconcileEntry` 里那段「source=Symlink 但 mirror=File」的特例分支
已经随改造整段删掉了，Linux 单独恢复 symlink 等于把那段特例再加回来，而且只在一个平台上存在；
省下的是每实例 110 MB（相对每实例约 1 GB 的镜像不到 12%），换来的是两个平台的同步语义分叉。
**统一走复制，两平台行为完全一致**，这与 §3.1 原则 2「call site 尽量零改动」的取向一致。

其余不变：

- `main.go` 的 `ensureAdminElevation()` / `buildElevatedArgs()` / `--no-admin` **已随去管理员化整体删除**，
  Linux 侧不再需要为它准备 no-op 分支（§5.9 的相应描述已同步更新）。
- 镜像的语义在 Linux 上原样成立且**更重要**，见本节末尾。

镜像的语义在 Linux 上原样成立且**更重要**：`Win64` 目录必须真实复制（Wine/DXVK/UE 的着色器与启动缓存
会写进该目录，多实例共享会互相踩），`Saved/Config/WindowsServer`、`Saved/Logs`、`Saved/<SaveDir>`
仍然 symlink 到实例目录。这与 `ark_instance_manager.sh` 的做法（全局单点 symlink 换来换去、
因此**无法真正并发多实例**）相比是本项目的架构优势，要保住。

### 5.7 `internal/certmgr` —— 证书信任

拆 `store_windows.go`（现状原样）/ `store_linux.go`：

| 能力 | Linux 实现 |
|---|---|
| `TrustCA()` | 写 `/usr/local/share/ca-certificates/asa-server-ca.crt` + `update-ca-certificates`（Debian 系）<br>或 `/etc/pki/ca-trust/source/anchors/` + `update-ca-trust`（RHEL 系）。需 root。 |
| `IsCATrusted()` | 检查上述路径存在且指纹匹配 |
| `UntrustCA()` | 删文件 + 重跑 update 命令 |
| `IsElevated()` | `os.Geteuid() == 0` |
| `hardenKeyFile()`（原 `icacls`） | `os.Chmod(path, 0600)` |

**默认行为差异**：Linux 上系统信任库**不影响浏览器**（Firefox/Chrome 用各自的 NSS db）。
所以 Linux 默认 `trust_local_ca: false`，只生成证书并在启动日志/前端里给出 CA 路径 + 手动导入指引，
避免制造「装了却还是红锁」的困惑。`asa-server cert install` 保留为显式操作。

CA 生成、叶子证书签发、SAN 计算、自动重签这些逻辑全部跨平台，不动。
CA 私钥只在本机生成、绝不打包进二进制这条约束在 Linux 上同样成立。

> ✅ **P5 已实现**，与上表设计一致：`detectBackend()` 按 `exec.LookPath` 探测
> `update-ca-certificates` 优先、`update-ca-trust` 兜底；`TrustCA`/`UntrustCA` 显式检查
> `IsElevated()` 提前给出「需要 sudo」的清晰错误，不依赖系统命令的 permission-denied 报错；
> `ensureCA()` 重签时调用的 `untrustFingerprint` 找不到发行版工具或指纹不匹配都直接返回
> nil（目标状态已达成，不是错误）。`cli.go` 的 `--machine` 在非 Windows 上没有意义（Linux
> 只有一个系统级信任存储），改为非 root 时直接报错提示 `sudo` 重跑，不落到
> `procx.RunAsAdmin`（那是 Windows 的 ShellExecute 自动提权，Linux 没有等价物）。
> `EnsureTLSConfig` 补了一条 `else` 分支：`trust_local_ca=false` 时也在启动日志里报出 CA
> 路径 + `cert install` 提示，而不是完全沉默——设计文本本来就要求这条，之前的代码遗漏了。
> **未验证**：`update-ca-certificates`/`update-ca-trust` 的实际调用未在真实 Linux 主机上跑过。

### 5.8 `internal/winservice` → `internal/svcmgr`

`kardianos/service` v1.3.0 原生支持 systemd，`InstallService()` 的骨架可复用。Linux 侧需要补：

```go
svcConfig.Dependencies = []string{"After=network-online.target"}
svcConfig.Option = service.KeyValue{
    "LimitNOFILE": 1048576,   // ARK + Wine 打开的 fd 极多
    "Restart":     "on-failure",
    "RestartSec":  10,
}
svcConfig.EnvVars = map[string]string{
    "HOME": userHome,          // ⚠️ 关键，见下
}
svcConfig.WorkingDirectory = cfgpkg.BaseDir
svcConfig.UserName = "asa"     // 不要用 root
```

> ⚠️ **`HOME` 必须显式设置**。systemd 服务的 `HOME` 可能为空或 `/`，而 umu 需要
> `$HOME/.local/share/umu`（Steam Linux Runtime），lsteamclient 需要 `$HOME/.steam/sdk{32,64}`。
> HOME 不对 = 每次启动都重新下载运行时，或者直接崩在 steamclient。这是 Linux 部署最隐蔽的一个坑。

> ⚠️ **不要用 root 跑**。pressure-vessel 的非特权 user namespace 路径在 root 下行为不同，
> 且 Proton 生态普遍假设非 root。建议 `service install` 时检测并建议专用用户。

另外在部署文档里给出 `vm.max_map_count` 提示（部分发行版默认值偏低会让 UE 分配失败）。

`service remove` 联动清理本地 CA（现 `svcmgr/service.go:128` 调 `certmgr.UntrustCAOnCleanup()`）
的行为在 Linux 上保留，走 §5.7 的 Linux 实现。

> ✅ **P5 已实现**，两处偏离设计文本、都记录理由：
>
> 1. **`RestartSec` 没有单独设置，沿用 kardianos 内置 systemd 模板的 120s**，而不是示意代码里的
>    10s。原因：kardianos v1.3.0 的 `Option` 表里根本没有 `RestartSec` 这个键（源码翻过，只有
>    `LimitNOFILE`/`Restart`/`ReloadSignal`/`PIDFile`/`LogDirectory`/`SystemdScript` 等），
>    模板把 `RestartSec=120` 硬编码在内置 unit 文件里。要改就得整份复制一份 `SystemdScript`
>    自定义模板——一份会随 kardianos 版本升级悄悄漂移的分叉。120s 对一个「重启前要先
>    `saveworld`」的游戏服务器也更保守，不算退步。
> 2. **没有自动创建/切换到专用用户**，`UserName` 保持空（root），只在 `service install` 时打印
>    警告 + 手动接管步骤（`useradd` + `systemctl edit` 加 `User=`）。原因：自动切换运行身份意味着
>    要处理 `BaseDir` 及其下所有实例目录的属主/权限迁移——对一次已经在跑的 root 安装做这个，
>    风险显著高于「告诉用户怎么做，让用户自己决定要不要做」。这与 §5.1 结尾对
>    `-ClusterDirOverride` 疑似 bug 的取舍是同一个原则：没有直接证据强制要求改、又会碰用户现有
>    数据/环境的操作，优先不做。
>
> `HOME` 的处理按设计文本原样实现：`service install` 时用当前进程的 `os.UserHomeDir()`
> （`sudo` 不加 `-E` 时就是 root 的 `/root`）直接写死进 unit 的 `Environment=`，而不是留给 systemd
> 运行时环境去决定——后者正是设计文本标 ⚠️ 的那个坑。**未验证**：真实 systemd 环境下
> `service install/start/stop/remove` 全流程、以及 `LimitNOFILE`/`HOME` 是否如预期生效。

### 5.9 `internal/gui` —— Linux 排除

> 2026-08-26 更新：曾定案「Windows GUI 改用 Wails 重写、删除 Fyne」已放弃（见 §10.0），
> Windows 继续用 Fyne，`internal/gui` 不会被整包删除。本节要求的只是 Linux 兼容所必需的
> **构建约束隔离**，与 GUI 用什么框架无关，结论不变。

- `internal/gui` 整包加 `//go:build windows`（长期保留，不再有整包删除的计划）。
- `main.go` 拆出 `main_windows.go` / `main_linux.go`，各自提供 `actionGUI`：
  Linux 版返回 `errors.New("GUI 仅在 Windows 上可用，请使用 asa-server api")`。
  ~~以及 `ensureAdminElevation` 的 no-op~~ —— 该函数连同 `buildElevatedArgs` / `quoteArg` / `--no-admin`
  已随「镜像去管理员化」整体删除，Linux 侧不需要为它准备任何东西。
- 删掉 `main.go:38-42` 的 `runtime.GOOS != "windows"` 硬拦截。
- 无参数启动的默认行为：Windows 仍是 GUI，Linux 改为等价于 `api`。

**副产品**：排除 Fyne 后 Linux 构建**不再需要 cgo**（modernc/sqlite、badger、gopsutil、creack/pty 都是纯 Go），
`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build` 可产出静态二进制，交叉编译无痛。
若 `os/user` 触发 cgo，加 `-tags osusergo,netgo`。

> 🆕 这个结论在「ArkApi 插件数据隔离」落地后**仍然成立，但边际变窄了**：`modernc.org/sqlite`
> 过去只被 `internal/auth` 用（`auth.enabled` 默认 false 时甚至不打开库），现在
> `internal/plugindata/snapshot.go` 也在用，而 `plugindata` 位于 `mirror` → `instance` 的必经链上。
> modernc 仍是纯 Go、linux/amd64 支持完整，所以 `CGO_ENABLED=0` 不受影响 ——
> 但从此**任何用 cgo 的 SQLite 驱动都不能再被引入**，否则整个 Linux 静态编译目标一起作废。
> 如果 §5.10 采纳「frp 改内部调用」（引入 quic-go / kcp-go / wireguard-go 等一大票依赖），
> 同样要按这条标准逐个核对 —— 已核对结论：全部纯 Go，见 §5.10。

### 5.10 `frpmanage` —— 改为**库内调用**，彻底绕开分平台二进制

> **✅ 定案：frp 改为进程内库调用（`github.com/fatedier/frp/client`），删除 `//go:embed frpc.exe`
> 与整套子进程管理。** Syncthing 走另一条路（§5.10.5），两者不要一起处理。

#### 5.10.1 被否掉的原方案：内嵌二进制分平台

```
internal/frpmanage/
├── embed_windows.go   //go:build windows  → //go:embed bin/frpc.exe
├── embed_linux.go     //go:build linux    → //go:embed bin/frpc
└── bin/{frpc.exe, frpc}
```

代价是仓库体积翻倍（frpc ~15 MB × 2 平台），而且每加一个目标架构（linux/arm64 很可能要）
就再翻一次。对 frp **这条路没有必要走**，因为 frp 本身就是 Go 写的。

#### 5.10.2 入口在哪：`client.NewService`，不是 `pkg/sdk/client`

⚠️ **先澄清一个容易走错的岔路**：`github.com/fatedier/frp/pkg/sdk/client` 名字很像入口，
但它是 **frpc 管理端 HTTP API 的客户端**（`GetProxyStatus` / `GetAllProxyStatus` / `Reload` 等，
打的是 frpc 自己的 `webServer.port`）。**它不能把 frpc 跑在进程内**，只是换一种方式跟一个
已经在跑的 frpc 说话。本项目当前也没在用管理端口，所以它对本方案没有用处。

真正的进程内入口是**顶层 `client` 包**（`github.com/fatedier/frp/client`，不在 `internal/` 下，可导入）：

```go
// 与 cmd/frpc/sub/root.go 的 runClient → runClientWithAggregator → startServiceWithAggregator
// 完全同构，照抄即可。已核对 frp v0.71.0。
result, err := config.LoadClientConfigResult(cfgPath, false)   // pkg/config

configSource := source.NewConfigSource()                        // pkg/config/source
_ = configSource.ReplaceAll(result.Proxies, result.Visitors)
aggregator := source.NewAggregator(configSource)

proxyCfgs, visitorCfgs, _ := aggregator.Load()
proxyCfgs, visitorCfgs = config.FilterClientConfigurers(result.Common, proxyCfgs, visitorCfgs)
proxyCfgs = config.CompleteProxyConfigurers(proxyCfgs)
visitorCfgs = config.CompleteVisitorConfigurers(visitorCfgs)

if warn, err := validation.ValidateAllClientConfig(
    result.Common, proxyCfgs, visitorCfgs, nil); err != nil {   // pkg/config/v1/validation
    return err
} else if warn != nil {
    logger.GetLogger().Warnf("[frpc] %v", warn)
}

svr, err := client.NewService(client.ServiceOptions{
    Common:                 result.Common,
    ConfigSourceAggregator: aggregator,     // ⚠️ 必填，为空直接报错
    ConfigFilePath:         cfgPath,
})
go func() { m.runErr = svr.Run(ctx) }()     // Run 是阻塞的
```

配置文件格式、路径、以及现有 `frpc.toml` 的读写与前端编辑**全部不变** ——
`LoadClientConfigResult` 就是 frpc 命令行读的那一份，`api.go` 的 293 行一行不用动。

#### 5.10.3 收益

| 项 | 现状（子进程） | 库内调用 |
|---|---|---|
| 跨平台 | 每平台一个 `//go:embed` 二进制 | **一份代码，`GOOS` 随便切** |
| 仓库体积 | +15 MB/平台 | **0** |
| 提取逻辑 | MD5 比对、写盘、Linux 还要 `chmod 0755` | **整段删除**（`manager.go:41-98`） |
| 启动成败判定 | `time.After(500ms)` 猜（`manager.go:165`） | `NewService` 的返回值 + `Run` 的返回值，**确定性的** |
| 运行状态 | `cmd.ProcessState` 轮询 | `svr.StatusExporter()` 直接给每条 proxy 的状态 |
| 改配置 | 杀进程重启 | `svr.UpdateAllConfigurer(...)` 热更新（可选，非必须） |
| 孤儿进程 | 管理器被强杀会留下 frpc | **不可能** —— 没有独立进程 |
| 升级 frp | 换 exe + 重新编译（`//go:embed` 必然要重编） | 改 `go.mod` + 重新编译 |

最后一行值得单独说：**升级成本是持平的，不是变差**。因为今天的 `//go:embed frpc.exe`
本来就要求重新编译才能换版本，所以「子进程 = 能单独替换二进制」这个直觉在本项目里**不成立**。

#### 5.10.4 代价与必须处理的坑

| # | 项 | 说明与处置 |
|---|---|---|
| 1 | **崩溃隔离没了** | 今天 frpc panic 只死一个子进程，管理器照常。库内调用时 frp 任意 goroutine 的 panic 会**带走整个 asa-server**（进而失去对所有 ARK 实例的管理，游戏进程本身不受影响但没人管了）。`recover()` 在调用点拦不住别的 goroutine。**这是本方案唯一的实质性退步**，接受它的前提是 frp 作为成熟项目 panic 概率低；不接受就别做 |
| 2 | **import 副作用改全局状态** | `client/service.go` 的 `init()` 会 `os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING")`、`os.Setenv("QUIC_GO_DISABLE_ECN")`，并设 `crypto.DefaultSalt = "frp"`。都无害，但要知道它们发生在**宿主进程**里，且在 `main()` 之前 |
| 3 | **日志走 frp 的包级全局 logger** | `pkg/util/log` 有个包级 `Logger` 变量。**不要调 `log.InitLogger`** —— 它会按 frpc.toml 的 `log.to` 抢 stdout 或自己开轮转文件。正确做法是自己 `log.New(log.WithOutput(w))` 赋给 `log.Logger`，`w` 直接复用现有的 `frpmanage.LogWriter`（`manager.go:275`，已经在做 ANSI 清洗 + 按行转发 zap）。**这段适配是白拿的** |
| 4 | **依赖体积与 CVE 面** | 新增直接依赖：quic-go、kcp-go、fatedier/yamux、fatedier/golib、wireguard-go + songgao/water + vishvananda/netlink（vnet 功能，`NewService` 无条件引用）、go-socks5、go-oidc、prometheus/client_golang、gorilla/mux + websocket、pelletier/go-toml、samber/lo、tidwall/gjson、以及 `pkg/config/load.go` 拖进来的 `k8s.io/apimachinery` + `client-go`。预估二进制 **+15~25 MB**，`go.sum` 显著变长。**已核对：全部纯 Go，`CGO_ENABLED=0` 不受影响**（见 §5.9） |
| 5 | **frp 未承诺 `client` 包的 API 稳定性** | 它是导出包但不是文档化的公共 API。近期就有破坏性变更：`ServiceOptions.ConfigSourceAggregator` 变成了**必填**（为 nil 时 `NewService` 直接报 `config source aggregator is required`）。**必须在 go.mod 里钉死具体版本**（本地参考版本 v0.71.0），升级当作一次小改造而不是 `go get -u` |
| 6 | **Stop 语义从「杀进程」变成「关对象」** | 今天 `Stop()` 取消 exec context，OS 保证进程没了。库内调用要走 `svr.Close()` 或 `svr.GracefulClose(d)`，**goroutine 是否真的收干净要自己验**。泄漏会随每次 `Restart()` 累积。**验收里必须有一条「连续 Restart 50 次，`runtime.NumGoroutine()` 不单调增长」** |
| 7 | **`loginFailExit = true`** | 默认配置里有这一项（`manager.go:305`）。子进程模式下它让 frpc 直接退出；库内调用时 `svr.Run(ctx)` 只是**返回一个 error**。manager 要把这个返回值映射到 `running=false` + `startErr` —— 顺带**替掉了 `asyncStart` 里那段 500ms 的猜测逻辑**，是净改善 |
| 8 | **许可证** | frp 是 **Apache-2.0**。链接进二进制需要随分发附上 Apache-2.0 全文与 NOTICE 归属。仓库目前**连 LICENSE 文件都没有**，这项要一并补上（内嵌 exe 其实也早该做，只是链接让它无从回避） |
| 9 | Go 版本 | frp `go.mod` 声明 `go 1.25.0`，本仓库 `go 1.27` —— 兼容，无需处理 |

#### 5.10.5 Syncthing：**不要照搬**，改为按需下载

同样的思路对 Syncthing **不成立**，三条理由：

1. Syncthing 的 `lib/` 虽然可导入，但上游明确不把它当作稳定的公共 API，
   且它假定自己**拥有**配置目录、数据库与整个生命周期（含自更新、GUI、设备 ID 密钥管理），
   嵌进宿主进程要拆的东西远比 frp 多。
2. 体积量级不同：Syncthing 二进制约 30 MB，其依赖树嵌进来的膨胀比 frp 更夸张。
3. Syncthing 是**用户会独立感知**的东西（有自己的 Web UI、可能想换版本），
   保持独立进程反而更符合预期。

所以 Syncthing 走原方案里的**替代路径**：删掉 `//go:embed syncthing.exe`，
改成「首次使用时按 `GOOS`/`GOARCH` 从 GitHub Release 下载 + 校验 + `chmod 0755`」，
统一走 §5.13 的 `pkg/download`（与 umu / GE-Proton 共用同一套重试/校验/进度上报/GitHub 代理代码，
不再各写一份）。仓库因此**净减 45 MB**（frpc 15 MB 走库内调用、syncthing 30 MB 走下载）。

`pkg/processjob`（Job Object，Syncthing 用它管进程树）仍然需要 §3.2 里的 `proctree` 平台拆分 ——
frp 转成库内调用**不能**免掉这一项。

#### 5.10.6 落地顺序

这项与 Linux 兼容**解耦**，可以先在 Windows 上单独做完再合流，风险更可控：

| 步 | 内容 | 判据 |
|---|---|---|
| F0 | `go get github.com/fatedier/frp@v0.71.0`，写一个最小 demo 跑通 `NewService` + `Run` | Windows 上能连上 frps 并转发一条 tcp proxy |
| F1 | 把 frp 的包级 `log.Logger` 接到 `frpmanage.LogWriter` | 日志进 `asaServer.log`，无 ANSI 残留，不抢 stdout |
| F2 | 重写 `manager.go` 的 Start/Stop/Restart/IsRunning/CheckStatus，删掉提取与 500ms 猜测 | `api.go` **零改动**、前端零改动 |
| F3 | goroutine 泄漏验收（坑 #6） | 连续 Restart 50 次后 goroutine 数稳定 |
| F4 | 删 `frpc.exe`、删 `//go:embed`，补 LICENSE / NOTICE | `git ls-files` 里没有 frpc.exe |
| F5 | `GOOS=linux CGO_ENABLED=0 go build` 核对 `internal/frpmanage` 通过 | —— 到这一步 frp 就**彻底退出 Linux 兼容的工作清单** |

### 5.11 `pkg/tail` —— 文件身份

```go
// filekey_linux.go
func fileKey(fi os.FileInfo) string {
    if st, ok := fi.Sys().(*syscall.Stat_t); ok {
        return fmt.Sprintf("ino:%d_dev:%d", st.Ino, st.Dev)
    }
    return fmt.Sprintf("size:%d_mod:%d", fi.Size(), fi.ModTime().UnixNano())
}
```

inode+dev 比 Windows 的 CreationTime 更可靠（轮转必然换 inode）。其余 fsnotify 逻辑跨平台不变。

### 5.12 `internal/plugindata` —— 编译得过；ArkApi 未安装时结构性静默，装了就正常工作

> 🆕 本节因 `ARKAPI_PLUGIN_DATA_PLAN.md`（**已实施**）新增；下表处置结论已按「ArkApi 在 Linux 上
> 不再是非目标」（见 §0 修订记录、§1）更新——之前的版本假设 Linux 上 ArkApi 永远不存在，
> 现在的前提是「用户可能真的把它跑起来」。

`plugindata` 代码本身没有平台耦合（核对结论见 §2.3），`GOOS=linux` 下编译与单测都会通过。
它是否在 Linux 上做事，完全取决于**镜像里是否真的有插件目录**，不取决于平台：

- `listMirrorPlugins`（`plugindata.go:57`）以**镜像里实际存在的插件目录**为准，
  `os.ReadDir` 失败即返回空 —— 用户没启用 `EnableAsaPlugin`（因而没有 `ArkApi/Plugins` 目录）时，
  `Inject` / `Reclaim` / `Rescue` 自然退化成空循环；用户启用了、且 `AsaApiLoader.exe` 通过
  `runner`/umu-run 真的把插件目录建了起来，`plugindata` 就照常工作，两平台同一套逻辑。
- `IsProtectedRelPath`（`plugindata.go:295`）第一行就是前缀判断，不匹配立即 `false`，
  对 `mirror` 的同步热路径零开销。
- `StartSnapshots` 遍历的是同一份插件列表，空列表 = 不起 goroutine。

所以 **P0 不需要为 `plugindata` 做任何事**。但有四条要在 P4/P6 显式确认：

| # | 项 | 处置 |
|---|---|---|
| 1 | **`pluginsRelPath` 硬编码大小写** | ✅ **P6 已确认，行为按最保守方式处理**：常量是否与真实 SteamCMD/ArkApi 落盘大小写一致，本质上取决于 ArkApi 发行包自己的目录命名——不是本项目代码生成的路径，没有真实 Linux + ArkApi 安装无法 100% 实测核实。没有把常量改成动态探测（改动一个「本来就该精确匹配」的常量去将就一个未经证实的假设，风险大于收益），而是新增 `casecheck.go`/`casecheck_linux.go`（纯逻辑 `detectPluginsCaseMismatch` 有 4 个真实单测）：`listMirrorPlugins` 精确匹配失败时，在 Linux 上额外做一次大小写不敏感扫描，命中就在日志里把磁盘实际大小写和期望大小写都打出来——把「静默什么都不做」变成「有诊断线索」，真机验证时第一时间能看到 |
| 2 | **`override.go:85` 的 `strings.ToLower`** | ✅ **P6 已修复**：拆成 `pathCompareKey`（`override.go` 里的比较逻辑改调用它），`override_windows.go` 折叠大小写（NTFS 语义不变），`override_linux.go` 不折叠（多数 Linux 文件系统大小写敏感，`/a/DB` 与 `/a/db` 是两个真实不同的路径）。各有一组真实单测（`override_windows_test.go`/`override_linux_test.go`）分别断言两种语义 |
| 3 | **`EnableAsaPlugin` 与前端插件面板** | ✅ **P6 已确认**：`webapi/pluginapi` 与 `PluginDataPanel.vue` 复查过，均无任何平台判断/门禁代码，Linux 上功能与 Windows 完全一致，不需要改动。「不显眼的稳定性提示」是文本建议的可选项（"可以加"），评估后**未添加**——要做对得先给前端一个"当前是否 Linux"的信号，为一条软性文案新增一条 API/字段的成本收益不划算，先跳过 |
| 4 | **`PluginSnapshotInterval`** | ✅ **P6 已确认**：复查 `InstanceConfig`/`instance_config.ini`/`server.go` 三处，均无平台专属分支，两平台语义一致，无需改动 |

**分层影响**：`mirror` 与 `instance` 现在都直接 import `plugindata`（`ARKAPI_PLUGIN_DATA_PLAN.md` §10.1
把原设想的回调钩子改成了编译期依赖）。这条边不成环、也不引入平台耦合，§3.2 的包规划不用调整。

### 5.13 `pkg/download` —— 全局下载器 + GitHub 代理

> 🆕 本节因「Linux 侧要下的东西越来越多」新增：umu zipapp（§4.3）、GE-Proton（§4.3）、
> Syncthing（§5.10.5，两平台都要）都要从网络拉大文件，其中 **umu / GE-Proton / Syncthing 三个都挂在
> GitHub Release 上**。国内网络访问 `github.com` / `objects.githubusercontent.com` 慢、抖动、偶发超时是
> 常态（这与 §6 风险 3「GitHub API 限流」是两个不同问题：限流是 `api.github.com` 拒绝请求，
> 本节说的是资源本身下载慢，即使请求成功也要下半天）。如果每处各写一份下载代码，代理配置、
> 重试策略、进度上报会散在三个不同的包里，改一处漏两处。**收敛成一个包，一份配置。**

**动机**：不新增功能，是把 §4.3 / §5.5 / §5.10.5 里原本就要写的下载逻辑**收敛成一处**，
同时补上一个原方案没有的能力——GitHub 资源的代理加速。

**接口**（`pkg/download`，符合 `pkg/` 准入：不认识 `BaseDir`/实例等领域概念，目标路径由调用方传入；
零领域依赖；`Configure` 只是一次性写全局 `http.Client`，与 `logger` 的初始化同性质，不是运行时状态机）：

```go
package download

// Options 描述一次下载。
type Options struct {
    URL      string    // 源地址
    Dest     string     // 落盘路径，调用方负责保证父目录存在
    Checksum string     // 可选，"sha256:<hex>"；非空时下载完立即校验，失败删除产物并报错
    Resume   bool        // 断点续传（Range 头），大文件（GE-Proton ~450MB / Syncthing ~30MB）默认开
    Progress func(done, total int64) // 可选，调用方接到既有 SSE TaskBroadcaster
}

// Fetch 执行一次下载，内置重试（默认 3 次，指数退避）。
func Fetch(ctx context.Context, opt Options) error

// Configure 设置全局代理与超时，程序启动时按 appconfig 调一次（Windows/Linux 都要调，
// 因为 Syncthing 下载在两平台都发生）。
func Configure(cfg Config)

type Config struct {
    GithubProxy string        // 形如 "https://ghproxy.example.com/"，见下方重写规则；空 = 不代理
    HTTPProxy   string        // 标准 HTTP(S)_PROXY 语义，作用于全部下载（含非 GitHub 的），走 net/http 的 Transport.Proxy
    Timeout     time.Duration // 单次请求超时，默认 30s（不含大文件传输本身，配合 Resume 分段）
    Retries     int           // 默认 3
}
```

**GitHub 代理的实现方式，以及为什么不能直接塞进 `http.Transport.Proxy`**：

市面上常见的「GitHub 加速」服务绝大多数是**前缀重写型反代**，形如
`https://<proxy-host>/https://github.com/<owner>/<repo>/releases/download/<tag>/<asset>`，
不是标准的 HTTP/HTTPS CONNECT 代理协议。两者不能混用同一套配置：

- `GithubProxy` 命中的域名是白名单式的**精确匹配**：`github.com`、`raw.githubusercontent.com`、
  `objects.githubusercontent.com`（Release 资产的实际重定向落点，**必须一并覆盖**，
  否则只代理了第一跳的 302，真正的大文件请求仍然直连）。命中后把原始 URL 整体拼到
  `GithubProxy` 后面发出去；不命中（Steam CDN、其他任意 URL）原样直连，不受影响。
- `HTTPProxy` 是正交的兜底项：给完全没有 GitHub 专用代理、只有一个通用出口代理（公司/校园网关）
  的用户用，通过标准 `Transport.Proxy` 生效，对 `GithubProxy` 命中的请求**同样叠加**（代理服务本身
  也可能需要经通用代理才能访问）。两者互不排斥。
- 用户自建的加速服务口味不一，`GithubProxy` 只做「整串 URL 拼接在前面」这一种最通用的重写规则，
  不猜测具体服务商的路径格式差异；用户如果用的是需要特殊拼接规则的服务，自行在配置里把
  `GithubProxy` 值写成能覆盖到该规则的形式，或直接留空改用 `HTTPProxy`。

**校验不是可选项**：代理链路引入了一个新的失败面——中间节点返回篡改或截断的内容却仍是 200。
`Checksum` 对三个下载对象都应该填（GE-Proton / umu 官方发布页有 `.sha256sum`，Syncthing Release
资产同样带校验文件），下载器**发现校验失败不做「将就用」，删除产物并报错**，交给上层重试或提示用户换代理。

**调用方**（三处，均改为调这一个包，删除各自原本打算写的下载代码）：

| 调用方 | 下载对象 | 备注 |
|---|---|---|
| `internal/runner/umu_linux.go`（§4.3） | umu zipapp、GE-Proton | 两者都在 GitHub Release 上，走 `GithubProxy` |
| `internal/syncthingmanage`（§5.10.5） | Syncthing 二进制 | Windows/Linux 都从这条路径下载，`GithubProxy` 两平台都生效 |
| `internal/installer`（§5.5） | SteamCMD | **不经过 `GithubProxy`**（Valve CDN，域名不在白名单里，`HTTPProxy` 仍可覆盖到） |

**不做的事**：不做通用的「镜像源列表 + 自动测速切换」，那是发行版包管理器的复杂度，本项目只有
三四个固定下载对象，一个可配置代理前缀 + 一个通用代理兜底已经够用；过度设计只会增加没人验证过的分支。

`Config` 对应的配置项在 §7 统一列出（`download:` 段，**顶层**而非 `linux:` 段下——
因为 Syncthing 下载在 Windows 上同样发生）。

---

## 6. 关键风险与已知坑

| # | 风险 | 影响 | 应对 |
|---|---|---|---|
| 1 | **GE-Proton 11.x 挂死 ASA** | 服务器永远起不来，无任何日志 | 版本硬钉 `GE-Proton10-34`；升级前必须过冷启动验收 |
| 2 | **AppArmor 限制 userns**（Ubuntu 23.10+） | `bwrap: Permission denied`，全部启动失败 | 启动自检 + 明确 sysctl 修复指引，见 §4.2 |
| 3 | **GitHub API 限流** | umu 解析 GE-Proton 别名失败 → `PROTONPATH` 空 → 崩 | 自己从 release 下载 URL 拉固定版本，从不碰 API |
| 4 | **`$HOME` 未设置/错误**（systemd 场景） | 运行时反复下载或 steamclient 崩溃 | ✅ P5 已实现：`svcmgr` 在 `service install` 时把 `HOME`（取自安装时进程自身的 `os.UserHomeDir()`，找不到兜底 `/root`）直接写进 unit 的 `Environment=`，不依赖 systemd 运行时环境。**未做**的是「启动自检校验可写」——留给真机验证时按需补 |
| 5 | **Wine prefix 与 Proton 版本不匹配** | 微妙的行为异常，极难排查 | `.created-by-proton` 标记文件，不匹配则移开重建（重建成本 ~1 分钟，prefix 里没有任何服务器数据） |
| 6 | **共享 prefix 的 wineserver 竞争** | 多实例并发首次启动可能互相踩；一个实例崩溃可能波及同 prefix 的其他实例 | 默认共享（与脚本一致，磁盘友好），但**首次初始化加互斥锁串行化**；`config.yaml` 提供 `prefix_mode: per-instance` 开关给追求隔离的用户 |
| 7 | **Linux 路径直接传给 UE** | 簇目录错乱，跨服传角色损坏 | 所有含路径的 exe 参数过 `runner.GamePath()`；`CustomStartParameters` 做保存期校验告警 |
| 8 | **大小写敏感文件系统** | Wine 内部有大小写不敏感回退，但 Go 侧构造的路径必须与磁盘完全一致 | 以 SteamCMD 下载的大小写为准；镜像同步逻辑本就按实际条目名走，风险低。加一条集成断言 `ShooterGame/Binaries/Win64` 存在 |
| 9 | **`kill(-pgid)` 误杀** | 进程组记错会杀到自己 | `Setsid: true` 保证进程组独立；kill 前断言 `pgid > 1 && pgid != os.Getpid()` |
| 10 | **gopsutil 在容器内看不到 Wine 的 socket** | 端口存活判定失效 | pressure-vessel 默认共享宿主 PID/net namespace，预期可见；P1 实测确认，失败则回退到 cmdline 扫描（`AltSaveDirectoryName` 匹配） |
| 11 | **ArkApi 在 Wine/Proton 下稳定性未知** | `EnableAsaPlugin` 在 Linux 上与 Windows 走同一开关、同一条 `runner` 路径，但 `AsaApiLoader.exe` 依赖的进程注入/DLL hook 在 Wine 下是否可靠没有官方保证 | 不强制拦截、不静默降级；启动失败或行为异常时如实记录日志，让用户自己判断要不要继续用。`webapi/pluginapi` 与 `PluginDataPanel.vue` 在 Linux 上正常可用，不特殊处理。详见 §5.12、§1 目标 5 |
| 12 | **首次安装耗时长**（GE-Proton 450 MB + SLR + prefix + ARK 本体约 25 GB） | 用户以为卡死 | 全流程走既有 SSE `TaskBroadcaster` 推进度，与现有 update 流一致 |
| 13 | 🆕 **`internal/mirror` 在 Linux 上编译不过** | `createJunction` 随去管理员化移进了 `junction_windows.go`，`mirror.go` 有 6 处调用 | P0 补 `junction_linux.go`（约 8 行 `os.Symlink`），语义必须与 Windows 侧对齐：绝对路径 target、已存在时报错不覆盖。见 §5.6 |
| 14 | 🆕 **frp 库内调用后失去崩溃隔离** | frp 任意 goroutine 的 panic 会带走整个 asa-server，所有 ARK 实例失去管理（游戏进程本身仍在跑但无人接管） | 这是 §5.10 方案唯一的实质退步，无法用 `recover()` 消除。接受它的前提是 frp 足够成熟；若不接受则回退到「分平台内嵌二进制」的原方案 |
| 15 | 🆕 **frp 库内调用的 goroutine 泄漏** | `svr.Close()` 不像杀进程那样有 OS 兜底，泄漏会随每次 `Restart()` 累积 | F3 步骤的硬性验收：连续 Restart 50 次后 `runtime.NumGoroutine()` 不单调增长。见 §5.10.6 |
| 16 | 🆕 **纯 Go 依赖约束收紧** | `plugindata` 把 `modernc.org/sqlite` 拉进了 `mirror`→`instance` 热路径；frp 库内调用又会拉进 quic-go/kcp-go/wireguard-go 等一大票 | 已核对全部纯 Go，`CGO_ENABLED=0` 目前成立。但从此**每引入一个依赖都要过一次 cgo 核对** —— 破一次，Linux 静态编译目标整体作废。见 §5.9 |
| 17 | 🆕 **GitHub 代理返回篡改/截断内容却仍是 200** | umu / GE-Proton / Syncthing 三者都可能被换成半个文件或错误内容，且下载器如果不做校验会「看起来成功」，直到运行时才炸（GE-Proton 挂死那种没有任何日志的失败，排查成本极高） | `pkg/download` 的 `Checksum` 对三个对象都必填，校验失败直接删产物报错，不做「将就用」；见 §5.13 |

---

## 7. 配置项新增

`{BaseDir}/config.yaml` 增加两段：`download`（**顶层，两平台都读**，见 §5.13）与
`linux`（Windows 下整段被忽略）。

```yaml
# 全局下载器（pkg/download，§5.13）——umu / GE-Proton / Syncthing 三个下载对象共用一份配置
download:
  github_proxy: ""          # 形如 "https://ghproxy.example.com/"；命中 github.com /
                             # raw.githubusercontent.com / objects.githubusercontent.com 时整串 URL 拼接在后面代理；空 = 直连
  http_proxy: ""             # 标准 HTTP(S)_PROXY 语义，兜底作用于全部下载（含非 GitHub 的，如 Steam CDN）；空 = 不使用
  timeout: 30s               # 单次请求超时
  retries: 3

linux:
  # 运行时来源：umu（默认，自动下载）| custom（用户自备 PROTONPATH）
  runtime: umu
  umu_version: "1.4.4"
  proton_version: "GE-Proton10-34"
  # prefix 模式：shared（默认，全实例共用一个）| per-instance（每实例独立，更隔离更占盘）
  prefix_mode: shared
  prefix_dir: ""            # 留空 = {BaseDir}/umu-prefix
  auto_download: true       # false 时缺运行时直接报错，不联网
  gameid: "umu-default"
```

沿用 `appconfig` 现有的 **flag > 环境变量 `ASA_*` > 文件 > 默认值** 优先级，无需新机制。
环境变量形如 `ASA_DOWNLOAD_GITHUB_PROXY`，与其余配置项命名规则一致。

⚠️ `applyAppConfig()` 必须把这些值也推给 `runner` 的包级变量、以及 `download.Configure()` ——
**服务模式下 `app.Run()` 不执行**，这是 `CLAUDE.md` 已经记录过的 Windows 坑，Linux systemd 模式
同样成立；`download` 段两平台都要推，遗漏它只会在「Syncthing 首次下载」时才暴露，容易被忽略。

---

## 8. 分阶段实施计划

| 阶段 | 内容 | 产出 / 验收 | 估算 |
|---|---|---|---|
| **P0 构建打通** ✅ 已完成 | 加构建约束；`gui` 整包 windows-only；`main.go` 拆平台文件；`certmgr`/`tail`/`processjob` 拆平台文件（Linux 侧先写**返回「未实现」的存根**，`tail`/`mirror` 的 linux 实现足够小，直接写了真实现而非存根）；**`mirror` 补 `junction_linux.go`（真实现，非存根）**；`pkg/download`（§5.13，Fetch + Configure + GitHub 代理重写，两平台通用，已在 `feat/global-downloader-github-proxy` 合入）；`syncthing` 内嵌改按需下载并接到 `pkg/download`（已用真实 GitHub release 端到端验证） | `CGO_ENABLED=0 GOOS=linux go build ./...` 通过；`GOOS=windows go build`、两平台 `go vet` 均无回归；`pkg/download` 单测覆盖「命中代理域名重写」「不命中直连」「校验失败删除产物」三种路径。落在 `feat/linux-p0-build-unblock` 分支，8 个提交。顺带修掉一个已存在于 master 的死代码 bug（`main.go` 残留的 `pkg/winproc` 未用导入，被 Linux 编译约束长期掩盖） | 1–2 天 |
| **F 轨道（frp 库内调用，可并行、可先行）** ✅ 已完成 | 见 **§5.10.6**，步骤 F0–F5。**与 Linux 兼容解耦**，在 Windows 上独立做完 | frp 退出 Linux 兼容工作清单；仓库不再有 `frpc.exe`（且原本就没被 git 追踪——`.gitignore` 的 `*.exe` 命中它，`go:embed` 全程依赖本地手放的文件）；`manager.go` 改走 `client.NewService` + `Run`/`GracefulClose`；50 次连续 Start/Stop 的 goroutine 泄漏回归测试；`CGO_ENABLED=0 GOOS=linux` 全量编译通过（含新拉进来的 quic-go/kcp-go/wireguard-go/k8s.io-apimachinery 依赖链）；新增 `THIRD_PARTY_NOTICES.md`（frp 是 Apache-2.0）。落在 `feat/frp-library-call` 分支 | 2–3 天 |
| **G 轨道（Windows+Linux，可并行）** | ~~Wails 取代 Fyne~~（2026-08-26 已放弃，见 **§10.0**）。改为：Fyne 保留 + 首次启动数据目录设置，BaseDir 并入 `config.yaml` 的 `basedir` 字段、`config.yaml` 固定放在 exe 同级（非环境变量/独立标记文件/系统级目录，见 §10.3） —— 见 **§10** | Fyne 首次运行能选目录、写 `config.yaml`；GUI/CLI/服务（Windows SCM / Linux systemd）从 exe 同级读到同一个 BaseDir，不受进程启动时机或运行账户影响 | 另计，见 §10.5 |
| **P1 进程原语** ✅ 已完成 | `pkg/winproc` → `pkg/procx`（`GetPIDByPort` 删除，改走下面的统一实现）；`procx_linux.go` 真实现（`/proc` 扫描：`IsProcessExited`/`ProcessImageName`/`QueryProcess`，`RunAsAdmin` 返回「本平台不适用」）；新增 `pkg/procx/port.go`（`PIDByPort`，gopsutil `net.Connections("all")`，TCP/UDP 一次覆盖，无构建约束两平台共用），`internal/process.IsServerRunning` 随之从「Windows netstat 文本解析 / Linux 存根」两个平台文件收敛成一份跨平台实现；`pkg/processjob` → `pkg/proctree`，Linux 实现（`Setsid` + `Close()` 时 `kill(-pgid, SIGKILL)`，含 `pgid>1` 断言）；新增 `procx.Terminate`/`Kill`/`TerminateTree`/`KillTree`，替换掉 `server.go`/`common.go`/`installer.go` 里全部 9 处 `exec.Command("taskkill", ...)`（Windows 侧行为不变，仍是同一套 taskkill 参数，只是挪进了函数） | `CGO_ENABLED=0 GOOS=linux go build ./...`、`go build`(windows，原生 cgo)、两平台 `go vet` 均通过；`grep -rn taskkill --include=*.go` 命中数归零（除注释与 procx 内部实现自身）；新增 `pkg/procx/port_test.go` 用真实 TCP/UDP 监听自证 `PIDByPort`（不依赖解析 netstat 输出），Windows 上跑通。**未验证**：`procx_linux.go` 的 `/proc` 扫描与 `proctree_linux.go` 的 `setsid`/`kill(-pgid)` 只做到跨平台编译通过，未在真实 Linux 上跑过——本机 WSL 的 go1.27.0 安装本身已损坏（`internal/abi/map.go`/`map_swiss.go` 均缺 `//go:build` 约束、重复声明，`go build` 连标准库都过不了，与本次改动无关），修好前无法做运行时验证；这两个函数目前也没有真实调用方（游戏进程要到 P2 `runner` + P4 才会在 Linux 上真正跑起来），风险可控但记在这里，P2/P4 验收时要补跑。落在 `master`，未开分支 | 2–3 天 |
| **P2 umu 运行时** ✅ 已完成 | `internal/runner` 接口（`Run`/`GamePath`/`EnsureRuntime`/`Preflight`/`Configure`）+ 两平台实现，`Run` 对 `ArkAscendedServer.exe` 与 `AsaApiLoader.exe` 一视同仁（见 §0/§1 的 ArkApi 决定）；`umu_linux.go` 下载 umu-launcher zipapp + GE-Proton（走 `pkg/download`，含 `github_proxy`）、prefix 预热（照抄 `ark_instance_manager.sh` 的 wineboot --init + steamrt 就绪检测 + wineserver drain 轮询）与 `.created-by-proton` 版本标记/迁移；`preflight_linux.go` 五项依赖自检（32 位 glibc / python3≥3.10 / libzstd.so.1 / tar / AppArmor userns，读 `/proc/sys` 而非 shell 出去跑 `sysctl`）；`internal/webapi/systemapi` 的 `GET /api/system/preflight`；`config.yaml` 新增 `linux:` 段（`appconfig`）；`EnsureRuntime` 在 `InitializationBasicComponents` 里后台异步跑，不阻塞服务启动。**执行细节对拍**：GE-Proton/umu 的下载 URL 与 tar 内部布局已用真实 GitHub Releases API 核对（非猜测），warm-up 与 fixups 的具体命令逐行对照本仓库 `scripts/ark_instance_manager.sh` 的验证过的实现，而非重新推导 | `CGO_ENABLED=0 GOOS=linux go build ./...`、`go build`（windows 原生 cgo）、两平台 `go vet` 均通过；`extractTar`（strip-prefix + zip-slip 拒绝，含嵌套 `..` 变体）与 Windows 侧 `Run`/`GamePath` 有真实执行的单测（非仅编译检查）。**已知偏差与限制**：①GE-Proton 校验走官方 `.sha512sum`（新增 `pkg/download` 对 `sha512:` 算法的支持），umu 校验走 GitHub Releases API 的 `digest` 字段（一次性的固定 tag 元数据请求，不是"解析 latest"，但确实触达 `api.github.com`，与 §4.3"从不碰 API"的原则有个可接受的例外，失败时降级为不校验而非拦截，已在代码注释说明）；②`umu_version` 从文档原稿的占位符 `1.4.0` 更新为已核实存在的 `1.4.4`；③`PROTON_VERB=run` 从设计阶段的示意代码中去掉——参考脚本的实际调用从不设它；④尚未在真实 Linux 主机上跑过 `EnsureRuntime`/`Preflight`（本机 WSL 的 go1.27.0 安装已损坏，见 P1 行），下载、解压、prefix 预热的端到端行为仍待 P3/P4 阶段用真机验证 | 3–4 天 |
| **P3 安装与更新** ✅ 已完成 | `SteamCmdURL` 出 `config` 包，拆进 `installer/steamcmd_{windows,linux}.go`（各自的 URL/二进制名/解压函数，Linux 走新增的 `pkg/archive.ExtractTar` 解 `tar.gz`）；`installer/fixups*.go` 三项 ASA-on-Wine 修复（Sentry 禁用/`steam_appid.txt`/Steam SDK 软链），接在 `DownloadAndUpdateArkServer` 成功后与 `VerifyServerInstallation` 验证前；`VerifyServerInstallation` 改走 `runner.Run()` 启动 `ArkAscendedServer.exe`（Windows 直接 exec，Linux 经 umu-run），固定 60s sleep 换成轮询等待 `Saved/Config/WindowsServer/`（180s 上限，超时/取消都会如实报错，不再像原 Windows 代码那样等完固定时间就无条件宣布成功）；杀验证进程从 `procx.Kill` 换成 `procx.KillTree`（Linux 上 `LauncherPID` 是 umu-run/进程组 leader，必须整树杀，见 §5.3/§5.4）。**顺带**把 `pkg/archive`（zip-slip 防护的 tar 解压）从 `internal/runner` 提出来独立成包，因为 installer 现在也要用它——避免同一段安全相关代码存在两份 | `CGO_ENABLED=0 GOOS=linux go build ./...`、`go build`（windows 原生 cgo）、两平台 `go vet` 均通过；新增 13 个真实单测（`disableSentryPluginAt`/`writeSteamAppIDAt` 的重命名/内容纠错/幂等路径，`waitForConfigDir` 的立即返回/轮询命中/超时/取消四种路径，含真实计时断言），`internal/installer` 既有测试全部保持通过；Linux 上 `update` 走完，`server-files` 完整，`Saved/Config/WindowsServer` 生成。**已知限制**：与 P2 一致——三项 fixups 里唯一没法跨平台单测的是 `symlinkSteamSDK`（`os.Symlink` 在非提权 Windows 上可能因权限失败，该函数本来就只在 `//go:build linux` 下编译，此处无跨平台测试覆盖，逻辑走查为主）；真实 Linux 主机上的端到端验证仍待 P4/P6 | 2–3 天 |
| **P4 实例生命周期** ✅ 已完成 | `internal/instance/server.go` 的 `startServerInternal` 改走 `runner.Run()`（PTY/非 PTY 分支合并成一次调用，`Options.PTY = arkAsaApiRunning`），**用 `context.Background()` 而非局部 `ctx` 发起启动**（局部 `ctx` 会在 `startServerInternal` 返回时被 `defer cancel()` 取消，若传给 `runner.Run` 会让 `exec.CommandContext` 在启动函数一返回就把刚起的服务器杀掉——这是本阶段最容易踩、也最隐蔽的一个坑）；`-ClusterDirOverride` 的目录过 `runner.GamePath()`；`internal/process` 新增 `SaveLauncherPID`/`GetLauncherPID`（双 PID 文件）；启动后解析真实游戏 PID 的判据从 `WaitArkApiRunServer`（按 `Port=`）泛化改名为 `waitForGamePID`（按 `AltSaveDirectoryName=`，Windows AsaApi 场景与 Linux 全场景共用），`findServerPIDByPort` 同理改名 `findServerPIDBySaveDir`；`process.isExpectedProcess` 按平台拆分（`isExpectedProcessPlatform`，见 §5.3 第 4 条），新增跨平台 `procx.ProcessCmdline`；`stopServerInternal` 5 分钟超时兜底从 `procx.Kill` 升级 `procx.KillTree`；`ForceStopServer` 新增第 4 步兜底读 `launcher_pid`；`runner` 新增 `LauncherIsDirect()` 供业务层判断「`Handle.LauncherPID` 是否就是游戏 PID 本身」。**镜像 `IsElevated` 处理**：核对后发现这一项在更早的「镜像去管理员化」重构里已经整体删除（`IsElevated()`/提权重启不再存在），P4 无需再处理，纯粹是本文档条目过时 | `CGO_ENABLED=0 GOOS=linux go build ./...`、`go build`（windows 原生 cgo）、两平台 `go vet` 均通过；新增 7 个真实单测（`SaveLauncherPID`/`GetLauncherPID` 的读写与「和游戏 PID 互相独立」、`procx.ProcessCmdline` 对自身进程的真实 WMI 查询——写测试当场炸出一个真 bug，见 §5.3 第 4 条）；`internal/instance` 既有测试保持通过（只有前置的、与本次改动无关的环境耦合失败）。对 Windows 现有代码路径逐句核对过等价性（非 AsaApi 分支：`handle.LauncherPID` 直接就是原来的 `cmd.Process.Pid`，零延迟零改变；AsaApi 分支：PTY 创建/Resize/Wait/Close/日志清洗的调用序列与原代码逐行对应）。**已知限制**：①未在真实 Linux 主机上跑过 `StartServer`/`StopServer`（WSL go 环境已损坏，见 P1），`AltSaveDirectoryName=` cmdline 匹配在 Wine 宿主进程下是否真的可靠、共享 prefix 下的进程组语义等，仍待真机验证；②`-ClusterDirOverride` 是否应该只传 `BaseDir` 而不是 `{BaseDir}/clusters/<id>`（§5.1 原文怀疑现有 Windows 实现可能导致 UE 自己再拼一层 `clusters/clusters/<id>`）**本次未改动**——没有直接证据证明现网真的有这个 bug，改这个会动到活跃用户的存档路径，风险回报比不划算，留给用户自己核实；③`CustomStartParameters` 里用户自带路径参数（如 `-UserDir`）在 Linux 上需要手写 `Z:\` 形式，配置保存时的校验告警未实现，留到 P6。**单实例**启动→玩家可连入→RCON 可用→优雅停止；**双实例**并发启动互不干扰（这两条验收本身仍待真机跑通） | 3–5 天 |
| **P5 服务化与证书** ✅ 已完成 | `internal/winservice` → `internal/svcmgr`（含调用方 `main.go`/`internal/gui/gui.go`），拆 `service_windows.go`（`configurePlatform`/`warnBeforeInstall` 空实现，行为不变）/ `service_linux.go`（systemd 加固：`Dependencies=After+Wants network-online.target`、`Option{LimitNOFILE:1048576, Restart:on-failure}`、`WorkingDirectory=BaseDir`、`EnvVars["HOME"]`，`warnBeforeInstall` 检测 root 并提示手动切换专用用户，不自动切换——两处对设计文本的偏离及理由见 §5.8）；`certmgr` 补 `store_linux.go` 真实现（`update-ca-certificates`/`update-ca-trust` 探测 + 读写 + `IsElevated` 前置校验，见 §5.7）；`appconfig` 的 `trust_local_ca` 默认值按平台区分（Linux `false`，写出的 `config.yaml` 模板注释同步区分）；`EnsureTLSConfig` 补 `trust_local_ca=false` 时的 CA 路径提示日志（设计文本要求但此前代码遗漏）；**顺带解除了本次改造的一个前置阻断**——`main.go` 删掉了 `runtime.GOOS != "windows"` 硬退出（P0 阶段就该有但漏了，`main_linux.go` 自己的注释也承认"今天不可达"），`main_windows.go`/`main_linux.go` 新增 `runDefaultAction`（无参数启动：Windows 走 GUI 不变，Linux 等价于 `api`，见 §5.9）；否则 P1–P4 的全部 Linux 实现虽然编译通过，实际跑起来仍会在 `main()` 第一行就退出，`service install/start/stop/remove` 在 Linux 上根本无法执行到 | `go build`/`go vet` 两平台均通过（含 `main` 包，此前从未因为运行时硬退出而被怀疑过构建，但从未被实际跑通过）；`internal/certmgr`/`internal/appconfig`/`internal/svcmgr` 既有测试保持通过。**已知限制**：①未在真实 systemd/Linux 主机上跑过 `service install/start/stop/remove`，`update-ca-certificates`/`update-ca-trust` 的实际调用、`LimitNOFILE`/`HOME` 生效与否均未验证；②`RestartSec` 沿用 kardianos 内置模板的 120s 而非设计文本示意的 10s（kardianos v1.3.0 无此 Option 键，自定义模板会分叉，见 §5.8）；③不自动创建/切换专用用户，只在 install 时警告 + 打印手动步骤（避免动现有 root 安装的目录属主）；④`vm.max_map_count` 提示留给部署文档，未在代码里做运行时自检——留给 P6 | 2 天 |
| **P6 收尾** ✅ 已完成 | 定时任务/批量/倒计时/备份/存档解析在 Linux 上回归审查：`schedule`/`batchmanage`/`backup`/`parseserver`/`countdown`/`updatemanage` 六个包逐一 grep 排查过 `exec.Command`/`.exe`/`taskkill`/`syscall.`/`windows.` 等平台风险模式，**零命中**——它们都建立在已在 P1–P5 验证过的 `instance`/`countdown`/`process`/`runner` 之上，本身不认识平台，无需改动，回归审查本身就是产出；**ArkApi 在 Linux 上的落地确认**（§5.12 表格四条，逐条见该节：①大小写常量因无法真机验证改为运行时诊断而非静默失配，新增 `casecheck.go`/`casecheck_linux.go` + 4 个真实单测，②`override.go:85` 的大小写折叠按平台拆分修复（`pathCompareKey`，`override_windows.go`/`override_linux.go` 各一组真实单测），③`pluginapi`/`PluginDataPanel.vue` 复查确认无平台门禁，无需改动，未添加软性 UI 提示，④`PluginSnapshotInterval` 复查确认两平台语义一致）；**新增 GitHub Actions CI**（`.github/workflows/ci.yml`，windows-latest + ubuntu-latest 矩阵，`go build`/`go vet` 为硬门槛，`go test` 因既有环境耦合测试作为 informational 不阻断合并；顺带发现一个此前从未暴露的问题——`app/dist` 被 `.gitignore` 排除但 `//go:embed dist` 要求它存在，CI 必须先跑 `npm run build` 否则任何一个平台的 `go build` 都会失败，历史上从未有人在真正干净的检出上试过）；**新增部署文档** `docs/LINUX_DEPLOYMENT.md`（依赖清单、安装步骤、systemd 服务化要点、故障排查表，均已链入 `docs/README.md`/`README.md`/`README_zh.md` 的文档索引——这三处此前从未收录过 Linux 相关文档，包括本设计文档自己） | `go build`/`go vet` 两平台通过；`go test`（排除已知会挂起的 `pkg/tail` 手动调试用例与已建立先例排除的 `internal/frpmanage`）仅剩此前就存在、与本次改动无关的 3 个环境耦合失败（`TestNormalizePoints`/`Test_SaveWorldSafely`/`Test_GetAllInstanceNames`/`TestGetProcessInfo`，硬编码了作者本机路径与 PID）；`internal/plugindata` 新增 10 个真实单测全部通过 | 2–3 天 |

**合计约 15–22 人日**（P0–P6，不含并行的 F / G 轨道），不含 Wine 侧疑难问题的排查缓冲（建议再留 30%）。
F 轨道另计 2–3 天，但它**减少** P0 的工作量（省掉 frp 分平台内嵌），净增很小。

**顺序上的一个取舍**：P1 的「端口→PID 切 gopsutil」会动 Windows 现有代码路径。
如果希望 Windows 零风险，可以降级为「Linux 用 gopsutil、Windows 保留 netstat」，代价是多维护一份实现。
本方案倾向统一，因为 `netstat -ano` 的文本解析（`process.go:97-111` 那段按字段位置取 PID 的逻辑）本身就脆弱，
统一是净收益 —— 但**必须配对拍测试**。

---

## 9. 测试矩阵与验收标准

### 9.1 平台矩阵

| 用例 | Win11 | Ubuntu 24.04 | Debian 12 | Arch | Fedora 41 |
|---|---|---|---|---|---|
| 构建（含 `go vet`） | ✅ | ✅ | ✅ | ✅ | ✅ |
| 依赖自检正确识别缺项 | n/a | ✅ | ✅ | ✅ | ✅ |
| AppArmor userns 提示 | n/a | ✅（默认触发） | — | — | — |
| 运行时自动安装 | n/a | ✅ | ✅ | ✅ | ✅ |
| SteamCMD + ARK 安装 | ✅ | ✅ | ✅ | ✅ | ✅ |
| 单实例冷启动至可连入 | ✅ | ✅ | ✅ | ✅ | ✅ |
| 双实例并发启动 | ✅ | ✅ | — | — | — |
| RCON / 交互式 RCON | ✅ | ✅ | — | — | — |
| 优雅停止（saveworld + DoExit） | ✅ | ✅ | — | — | — |
| 超时强杀无孤儿进程 | ✅ | ✅ | — | — | — |
| 倒计时停止/重启 + 游戏内公告 | ✅ | ✅ | — | — | — |
| 存档备份 / 恢复 | ✅ | ✅ | — | — | — |
| 服务端更新（实例先停后起） | ✅ | ✅ | — | — | — |
| 定时任务触发 | ✅ | ✅ | — | — | — |
| 日志 SSE / 轮转跟随 | ✅ | ✅ | — | — | — |
| systemd / Windows 服务 | ✅ | ✅ | ✅ | — | — |
| HTTPS + HTTP/2 + WebSocket | ✅ | ✅ | — | — | — |
| 存档解析（parseserver） | ✅ | ✅ | — | — | — |
| 🆕 FRP 库内调用：连接 + 转发 | ✅ | ✅ | — | — | — |
| 🆕 FRP 连续 Restart 50 次无 goroutine 泄漏 | ✅ | ✅ | — | — | — |
| 🆕 `EnableAsaPlugin` 开关 + `pluginapi`/面板在 Linux 上与 Windows 行为一致 | ✅ | ✅ | — | — | — |
| 🆕 `plugindata` 在无 ArkApi 目录时零开销静默 | ✅ | ✅ | — | — | — |

### 9.2 硬性验收判据

1. Linux 单实例冷启动出现 `minidumps folder is set to /tmp/dumps` 后跟正常 UE 日志输出，
   且游戏客户端能通过 Steam 服务器列表连入。
2. 停止后针对该实例的 `ArkAscendedServer.exe` / `bwrap` / `wineserver` 进程无残留。
3. 两个实例同时运行，各自的 `Saved/<SaveDir>` 与 `Config/WindowsServer` 互不污染，
   `Win64` 目录为各自独立的真实副本。
4. 配了 `ClusterID` 的两个实例，簇目录落在 `{BaseDir}/clusters/<ClusterId>/`（**不是** `clusters/clusters/`），
   且角色可在两实例间传输。
5. Windows 侧全部现有行为无回归 —— 特别是端口→PID 与停止流程这两处被改动的公共路径。
6. 🆕 Linux 上 `junction_linux.go` 建出的 symlink 与 Windows 上的 junction **行为对齐**：
   `filepath.Walk` 不递归进去、`isJunctionOrSymlink` 认得出、`CleanupInstanceMirror` 只删链接不删目标。
   回归重点仍是 `MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` §1.5 第 3 条：
   **多实例并发同步 + 更新后增量同步跑完，`server-files/` 下没有文件被误删**（改造前先做全量快照比对）。
7. 🆕 frp 库内调用：连续 `Restart()` 50 次后 `runtime.NumGoroutine()` 稳定，
   且 `frpc.exe` 不再出现在仓库与运行目录里。
8. 🆕 `EnableAsaPlugin=false`（或未装 ArkApi）时 Linux 上 `plugindata` 全程零动作：
   `{BaseDir}/instances/*/plugins/` 不被创建，无快照 goroutine；`EnableAsaPlugin=true` 且
   `AsaApiLoader.exe` 成功通过 `runner` 启动时，`plugindata`/`pluginapi`/面板与 Windows 行为一致。

---

## 10. 首次运行的数据目录设置（BaseDir）

> 本节不是 Linux 兼容的必要工作，但两个平台共享同一个 BaseDir 解析问题，一起定案可以省一次返工。
> **当前状态：2026-08-26 改过一次方向（放弃 Wails，见 §10.0），新方向已定案；
> §10.5 的 G1/G2 已实施。⚠️ `appconfig.Load` 的具体查找/取值算法后来又改了一版
> （新增 `ASA_CFG` 环境变量、`Load()` 不再接收目录参数、`EnsureDirectories` 不再
> 自解析、外加一道防御性兜底），本节与 §10.5 只保留大方向的描述，**具体实现细节
> 以 `docs/APPCONFIG_BASEDIR_PLAN.md` 为准**，那份文档已实施完毕。G3（Fyne 对话框）
> 与 G4（Linux setup CLI）尚未开始。**

### 10.0 决策变更记录：Wails 方案已放弃

本节原计划是「移除 Fyne，改用 Wails 重写 Windows GUI，复用 Vue SPA」（见下方折叠的历史决策 D1–D8）。
2026-08-26 用独立 demo（`wails-demo`，不在本仓库）跑通 W0/W1 落地验证时，发现两个 Wails v2 Windows
后端的框架级问题，方向由此推翻：

1. **SSE/WebSocket 在反代路径下完全无法连接。** Wails 的 `AssetServer.Handler` 在 Windows 上经
   `ICoreWebView2Environment.CreateWebResourceResponse` 把响应体整体缓冲成 `[]byte` 后才一次性交给
   WebView2（`pkg/assetserver/webview/responsewriter_windows.go` 的 `responseWriter.Finish()`），
   长连接/流式响应在这条链路下永远等不到数据——这是框架设计决定的硬限制，不是配置或版本问题。
   本项目的启动/停止/更新进度、日志流、系统资源监控（SSE）与交互式 RCON、全局事件（WebSocket）
   全部依赖这两种长连接，反代方案因此立不住，D3（原 §10.4）整个作废。
2. **附带发现一个真 bug（已验证可修，但不改变上面的结论）**：同一个 `Finish()` 对多值响应头一律
   `strings.Join(v, ",")` 拼成一行发给 WebView2，而 `Set-Cookie` 是 HTTP 里唯一不允许逗号折叠多值的
   响应头（RFC 6265）——两个 `Set-Cookie` 会被拼成一行，浏览器只认出第一个，第二个连同其内容一起被
   当成第一个 cookie 的垃圾属性吞掉。本项目 TOTP 两阶段登录一次响应里正好要设两个 cookie
   （清 `asa_session_pre` + 发 `asa_session`），精确命中这个 bug。

   绕开办法是：整页导航离开 Wails 的虚拟 host（`http://wails.localhost`）直连真实后端
   （`https://127.0.0.1:19193`），这样 WebView2 走的就是原生网络栈，SSE/WS/Cookie 全部正常
   （`internal/frontend/desktop/windows/frontend.go:658-664`，请求 host 不匹配 `startURL` 时会
   放行给 WebView2 默认处理）。但这样一来 D3「反代复用同一份 SPA」这个 Wails 迁移最大的卖点也没剩多少
   ——真要走这条路，不如维持现状用 Fyne。

**决定**：Windows 保留 Fyne，不引入 Wails / webview2 / NSIS 相关依赖。原 D1–D8、原 §10.3~§10.6、
原 §10.8（步骤 W0–W10）全部作废，仅保留原 §10.7.1（BaseDir 冲突问题）与原 §10.7.5（边界情况校验规则）
——这两处与用什么 GUI 框架无关，下面 §10.2/§10.4 沿用其结论。

<details>
<summary>已作废的决策记录 D1–D8（历史参考，不再执行）</summary>

| # | 决策 | 内容 | 理由 |
|---|---|---|---|
| D1 | 移除 Fyne，改用 Wails | 删除 `internal/gui` 整包与 `fyne.io/fyne/v2`，Windows GUI 用 Wails 重写 | 复用现有 Vue SPA + 去掉全仓库唯一的 cgo 来源 |
| D2 | 同时提供安装程序 | `wails build -nsis` 产出 NSIS 安装器，负责装文件、注册服务 | 与 D1 同一条工具链 |
| D3 | 前端走反向代理 | Wails `AssetServer.Handler` 反代 `127.0.0.1:19193`，不内嵌 dist | HttpOnly Cookie 鉴权要求同源——**已被 §10.0 推翻，SSE/WS 在此路径下不通** |
| D4 | 服务管理走 Wails 绑定方法 | 不补 `/api/service/*` HTTP 端点 | 服务管理需要管理员 |
| D5 | 系统托盘：本期移除 | 不再提供托盘驻留 | Wails v2 无内置托盘 |
| D6 | 新增首次运行引导程序 | 页面化完成 BaseDir 选择与 SteamCMD 初始化 | 解决 BaseDir 冲突 |
| D7 | Linux 只有 CLI 模式 | Linux 不编译任何 GUI，引导走 `asa-server setup` | Wails Linux 后端需 cgo；**这条结论不受 §10.0 影响，原样保留，见 §10.6** |
| D8 | `asa-server api` 保持一等入口 | GUI 与引导都是可选外壳 | **不受 §10.0 影响，原样保留，见 §10.7** |

</details>

### 10.1 新决策记录

> ⚠️ **编号说明（2026-08-27 合并）**：本节曾经自己用 `G1`–`G5` 编号决策，§10.5「落地步骤」
> 又独立用 `G0`–`G4` 编号可执行步骤——两套编号同时存在且含义不同（例如本节旧 `G3` 指
> 「BaseDir 并入 config.yaml」这个决策，§10.5 旧 `G3` 却指「Linux setup CLI」这个步骤），
> 引用时极易说错。现在**只有 §10.5 的 `G0`–`G5` 是全文档唯一的编号**，本节每条决策改用
> 「落地步骤」列指向它对应的那个 ID，不再自带编号。

| 决策 | 内容 | 理由 | 落地步骤 |
|---|---|---|---|
| Windows 保留 Fyne | 不删 `internal/gui`，不引入 Wails 依赖 | 见 §10.0 | G0 |
| Fyne 新增「首次启动设置数据目录」 | 检测到按 §10.3 两级顺序都没有可用 `config.yaml` 时弹目录选择对话框，校验后把 `config.yaml`（含 `basedir` 字段）写到 exe 同级目录（§10.3 第 2 级） | 解决 §10.2 的 BaseDir 冲突；范围只到「选目录」——服务安装/卸载/证书/账号在现有 Fyne GUI 与 CLI 里都已经有等价功能（`gui.go:384-457` 服务管理、`cert install`、`user add`），不需要再重复做一套引导页 | G3 |
| BaseDir 并入 `config.yaml`，`appconfig` 按两级固定目录查找，两平台统一 | 不再是 bootstrap.json 的四级优先级，也不是环境变量/独立标记文件；`config.yaml` 新增可选字段 `basedir`，`appconfig.Load` 依次查 exe 同级、系统固定目录，见 §10.3 | exe 同级与系统固定目录都是跟运行账户（LocalSystem/root/普通用户）无关的固定磁盘路径，GUI/CLI/服务天然读到同一份文件；系统固定目录额外解决开发/调试时 exe 产出路径不稳定的问题；`basedir` 留空时默认等于「`config.yaml` 自己所在目录」，现有部署零迁移成本 | G1（实现）/ G2（验证） |
| Linux 仍然只有 CLI 模式（原 D7，不变） | `asa-server setup`，不编译任何 GUI | 见 §10.6 | G4 |
| `asa-server api` 保持一等入口（原 D8，不变） | GUI 与 setup 都是可选外壳 | 见 §10.7 | G5 |

### 10.2 要解决的问题：BaseDir（原 §10.7.1，结论不变）

`internal/config/config.go:69-80` 的 `BaseDir` = 环境变量 `ASA_BASEDIR`，否则 **exe 所在目录**。
项目当前是「绿色解压即用」形态，这条规则没问题；但只要用户把程序装进 `C:\Program Files\...` 这类需要
管理员才能写的目录，就会出现问题：

- 服务以 LocalSystem 跑，能写 `Program Files`，但 `instances/` + `server-files/` 是 **25 GB 起步的游戏数据**，
  落在系统盘 Program Files 本来就不合适。
- **交互式 GUI 以普通用户身份运行**，会把 BaseDir 解析到同一个 Program Files 路径却没有写权限 ——
  服务与 GUI 看到同一路径、一个能写一个不能写，故障现象非常难懂。

首次启动时让用户显式选一个数据目录，从根上消掉这个歧义。

### 10.3 BaseDir 并入 `config.yaml`，`appconfig.Load` 按两级固定目录查找（替代 bootstrap.json 方案，也替代环境变量/独立标记文件方案）

> 2026-08-26 四次修正：第一版（写系统级环境变量）被指出覆盖不到「同一个终端里 setup 跑完接着直接手动
> 敲 `asa-server api`」这种场景（Windows `WM_SETTINGCHANGE` 只影响之后由 Explorer 派生的新进程，Linux
> `/etc/environment` 只在 PAM 新登录会话读一次）。第二版（自解析标记文件）解决了这个问题，但多了一份
> 独立于 `config.yaml` 的新文件格式。第三版简化成「`basedir` 做成 `config.yaml` 字段，固定放在 exe 同级」，
> 干掉了独立文件也干掉了系统级目录这一层。这一版把系统级目录**按查找位置**请回来（不是按持久化机制）：
> 开发/调试时可执行文件的产出路径经常变（`go run`、临时 build 目录、每次不同的调试输出位置），
> exe 同级这条路径因此并不稳定；系统固定目录不随 exe 输出路径变化，本机固定放一份，不管调试还是正式
> 环境，跑的是哪个临时二进制都能找到同一份 `config.yaml`——这是开发便利性上的真实需求，原先只保留
> exe 同级考虑得不够全面。

`config.yaml` 新增一个可选顶层字段：

```yaml
# 数据目录：留空 = 与本文件同目录（绿色部署默认行为，兼容全部现有安装，无需迁移）
basedir: ""
```

**`appconfig.Load` 按固定顺序查找 `config.yaml` 本身，两级都是与运行账户无关的固定磁盘路径**（不用
`%USERPROFILE%`/`$HOME`——原因见 §10.2：Windows 服务默认以 LocalSystem 运行，它的「用户目录」跟普通
登录用户的 Fyne GUI 不是同一个路径）：

1. `--basedir` 显式给了路径 → 直接用，跳过下面的查找（测试/CI/临时场景的逃生舱）；仍然支持
   `ASA_BASEDIR` 环境变量作为同级覆盖（沿用项目现有「flag > 环境变量 ASA_* > 文件 > 默认值」惯例，
   不是新机制，只是不再指望它来承担首次启动向导的持久化职责）。
2. **exe 同级目录**（`os.Executable()` 解析出来的路径，不是 cwd，也不是 `WorkingDirectory`）——
   兼容全部现有绿色部署，它们的 `config.yaml` 本来就在这里；首次启动向导默认也写这里（见下）。
3. **系统固定目录**：Windows `%ProgramData%\ASAServerManager\config.yaml`；
   Linux `/etc/asa-server/config.yaml`。exe 同级没有时才查这一级——主要给开发/调试用：本机放一份，
   不管当前跑的是哪次临时构建出的二进制、放在哪个目录，都能读到同一份配置；生产环境上这一级平时不会
   被用到（向导已经把 `config.yaml` 放到 exe 同级了），但留着不冲突。
4. 都找不到 → 沿用今天的行为：在 exe 所在目录自动生成一份默认 `config.yaml`（`basedir` 留空），
   `asa-server api` 不经任何向导也能直接跑起来（§10.7 的一等入口不变量不变）。

找到 `config.yaml` 后，`BaseDir` = 该文件里的 `basedir` 字段；字段为空则 `BaseDir` = 这份 `config.yaml`
自己所在的目录——这正是**现有全部部署**的隐式状态（`config.yaml` 与数据目录同处一地），所以老用户
升级后行为完全不变，不需要在他们的 `config.yaml` 里补任何字段。

**首次启动向导（Fyne / `asa-server setup`）完成后要做的事**：用户选好数据目录，向导把
`config.yaml`（`basedir` 字段填用户选的路径）**写在可执行文件同级目录**（第 2 级，不是第 3 级的系统
目录——保持与现有绿色部署一致，权限要求也和「写这个目录本身」挂钩，不强制多一层系统目录的写入）。
若 exe 所在目录本身需要管理员/root 才能写（比如用户手动把 exe 放进了 `Program Files`），向导继承
§10.4「拿不到权限就给清楚提示，不做半套」的既有规则；若 exe 所在目录普通用户就能写（典型绿色部署），
写这份文件完全不需要提权。系统固定目录（第 3 级）不是向导的写入目标，是留给开发/调试手动放一份的
逃生舱，`asa-server setup` 也可以额外支持一个 `--system`/等价 flag 显式写那里，但不是默认行为。

**不再需要**：bootstrap.json、独立的 `basedir.env`/`env` 标记文件、Windows 注册表写入与
`WM_SETTINGCHANGE` 广播、`svcmgr` 为 BaseDir 专门传 `cfg.EnvVars`——固定目录查找本身就不依赖这些机制，
问题从根上不存在了。`ASA_BASEDIR` 环境变量本身**不删**（现有文档已经在用，`appconfig` 对其余配置项
也统一走这条 `ASA_*` 规则），只是不再是首次启动向导的持久化手段，纯粹作为 flag 之下的一层显式覆盖保留。

**权限**：写系统固定目录（第 3 级，开发/调试用）需要管理员/root（`%ProgramData%` 默认普通用户不可写；
`/etc/asa-server/` 同理）；写 exe 同级（第 2 级，向导默认路径）权限要求取决于 exe 自己所在的目录。

`config.ResolveBaseDir()` / `EnsureDirectories()` 的两步拆分（原设计文本已提出，结论不变）依然要做：
`main.go` 目前无条件先建目录、后解析 CLI/日志/appconfig，这个问题不因为换持久化方式而消失——只是现在
「解析」这一步变成「按上面 4 级顺序找 `config.yaml`、读 `basedir` 字段」，首次启动对话框要在任何目录
被创建之前就有机会弹出来。

### 10.4 首次启动设置数据目录的校验规则（原 §10.7.5，规则不变）

放进平台无关的共享代码（Fyne 的对话框回调与 Linux `asa-server setup` CLI 都调它），两条硬性规则：

1. **可写 + 剩余空间 ≥ 30GB**（ARK 本体约 25GB + 存档增长）；已存在 `config.yaml` 时识别为
   「接管已有安装」而不是新建。
2. **禁止选在映射网络盘 / 网络文件系统上。** 表面理由是权限（服务的 LocalSystem/root 会话看不到
   用户登录会话挂的网络盘），更硬的理由是 **BadgerDB 用 mmap + 文件锁**
   （`{BaseDir}/database_file/state_db`），在 SMB/CIFS/NFS 上行为不可靠，可能直接损坏实例状态库。

   | 平台 | 拒绝条件 |
   |---|---|
   | Windows | `GetDriveTypeW() == DRIVE_REMOTE`；UNC 路径（`\\server\share` 开头）；`subst` 出来的虚拟盘 |
   | Linux | 挂载点 fstype 属于 `nfs`/`nfs4`/`cifs`/`smbfs`/`fuse.sshfs` 等（查 `/proc/mounts`） |

校验失败时明确告诉用户「换一个本地磁盘目录」，不要笼统报错。

**Fyne 端的触发时机**：GUI 启动时若按 §10.3 两级顺序（exe 同级 → 系统固定目录）都找不到
`config.yaml`（全新安装/绿色解压），弹目录选择对话框；如果任一级已有旧版 `config.yaml`
（老用户绿色部署升级，没有 `basedir` 字段），维持现状不弹窗——按 §10.3 规则它会被判定为「已配置」
（`basedir` 留空 = BaseDir 就是该文件所在目录，和它今天的实际行为完全一致）。拿不到写权限时给出
清楚的提示（为什么需要、如何以管理员重新运行），并保留「不设置、直接用 exe 目录跑」这条退路，不强制。

### 10.5 落地步骤

> **本表的 `G0`–`G5` 是全文档唯一的步骤编号**（§10.1 决策表已改为反向引用这里，不再自带编号）。

| 步骤 | 内容 | 通过判据 | 依赖 |
|---|---|---|---|
| G0 | 决策，非实现项：Windows 保留 Fyne，不引入 Wails 依赖（§10.1「Windows 保留 Fyne」，见 §10.0） | 不适用——已生效，`internal/gui` 未删、无 Wails 依赖 | — |
| G1 ✅ | `config.yaml` 新增 `basedir` 字段；`appconfig.Load` 依次查 exe 同级、系统固定目录 + 新增 `ResolveBaseDir` 阶段（现由 `appconfig.Load` 承担查找与解析，`cfgpkg.EnsureDirectories` 只负责建目录，两者拆开由 `main.go` 依次调用），改 `main.go` 启动顺序（先解析配置，再建目录/初始化日志） | 两级都没有 `config.yaml` 时启动**不产生任何目录**；有则按 `basedir` 字段（留空则用该文件所在目录）解析出正确 BaseDir | G0 |
| G2 ✅ | 验证两级查找顺序 + 「exe 目录已有旧版 `config.yaml`（无 `basedir` 字段）」这条兼容路径 | 老部署原地升级后 BaseDir 与升级前完全一致，无需任何手动迁移或补字段；只在系统固定目录放一份 `config.yaml`、exe 目录清空的场景下也能正确解析（验证第 2 级查找真的生效，覆盖开发/调试场景） | G1 |
| G3 | Fyne 首次启动对话框 + §10.4 校验规则：把 `config.yaml` 写到 exe 同级目录（第 2 级），`basedir` 填用户选的路径 | 全新机器上双击 exe → 选目录 → 直接进入正常 GUI，无需重启程序；用另一个全新启动的进程（不带任何 flag/env）在同一个 exe 目录下也能解析到同一个 BaseDir | G2 |
| G4 | `asa-server setup` CLI（Linux，交互 + 非交互两种模式）：把 `config.yaml` 写到二进制同级目录 | 全新 Linux 主机上非交互跑通：BaseDir → umu/GE-Proton → SteamCMD → ARK 本体；systemd 服务重启后解析到同一个 BaseDir，不依赖 unit 文件传参 | G2 |
| G5 | **回归：`asa-server api` 独立可用**（见 §10.7 三条不变量） | 全新解压的目录里直接 `asa-server api`，不经 GUI/setup 也能起服，行为与改造前一致 | G0–G4 |

**G5 是每一步都要复查的红线**——G1/G2 动的是 `config.yaml` 的查找逻辑，最容易在这里把便携模式改坏。
**G2/G3 的验收判据刻意挑「不给任何环境变量」的场景**——这正是本节要修的那个漏洞，不能只测「设了环境变量能读到」这种已经没问题的路径。

> **G1 落地时发现一个真 bug**：`basedir` 字段配合 viper 的 `AutomaticEnv()`（前缀 `ASA_`）会被自动
> 解析成环境变量名 `ASA_BASEDIR`——而这个变量名早就被 `explicitDir` 逃生舱本身占用了，两者语义完全
> 不同（前者是"两级查找里 config.yaml 的一个字段"，后者是"整个跳过两级查找"）。开发机上只要设了
> `ASA_BASEDIR`，`basedir` 字段就会被这个八竿子打不着的环境变量悄悄顶掉。修法是 `basedir` 字段单独
> 用一个不开 `AutomaticEnv` 的 viper 实例重读（`appconfig.fileOnlyBaseDir`），只反映文件内容，
> 不受任何环境变量影响。

### 10.6 Linux：只有 CLI 模式（原 D7，结论不变）

**Linux 不编译任何 GUI**（这条本来就与 Wails 无关——Linux 从来没打算给它装图形界面）。
Linux 上的完整入口就是 CLI：

| 命令 | 用途 |
|---|---|
| `asa-server setup` | 首次引导：依赖自检 → BaseDir → umu/GE-Proton 运行时 → SteamCMD → ARK 本体 |
| `asa-server api` | 启动服务（无参启动在 Linux 上等价于此，见 §5.9） |
| `asa-server service …` | systemd 安装/启停/移除（§5.8） |
| `asa-server cert / db / user …` | 证书、鉴权库、账号管理（均已存在） |

管理界面仍然有 —— 就是浏览器打开 `https://<host>:19193` 的那个 SPA，
只是不再有一个本地桌面外壳。这对无头服务器反而是正确形态。

两平台共享、不能各写一份的东西：BaseDir 校验规则（§10.4）与解析优先级（§10.3）。

### 10.7 `asa-server api` 始终是一等入口（原 D8，结论不变）

> GUI 与 `setup` 都是**可选外壳**，任何时候用户都能绕开它们，直接 `asa-server api` 把服务跑起来。

三条不变量，改造过程中不允许被破坏：

1. **BaseDir 解析必须保留 exe 目录兜底。**
   §10.3 里，exe 同级、系统固定目录都没有 `config.yaml` 时自动在 exe 同级生成一份默认的（`basedir` 留空）。
   所以在一台从未跑过首次启动向导的机器上解压即用、直接 `asa-server api`，
   行为与今天**完全一致** —— 绿色/便携模式不因为引入首次启动向导而消失。
2. **首次启动向导做的事都必须有 CLI 或配置等价物**：

   | 步骤 | 非 GUI 等价物 |
   |---|---|
   | 选 BaseDir | `--basedir` / `ASA_BASEDIR` / 手动编辑 `config.yaml` 的 `basedir` 字段 |
   | 注册服务 | `asa-server service install` |
   | 装本地 CA | `asa-server cert install` |
   | 建管理员账号 | `asa-server user add` |
   | SteamCMD + ARK 本体 | `asa-server update` |
   | 全流程一把梭（Linux） | `asa-server setup`（非交互模式） |

3. **`api` 不得依赖 GUI 进程，也不得要求 `config.yaml` 里存在 `basedir` 字段或任何环境变量。**
   缺失时按第 1 条回落，不报错、不弹窗。

> ✅ **已完成，历史遗留问题（曾记录在本节）已全部消解，与本次 Wails→Fyne 的方向调整无关，原样保留：**
>
> 原文这里记录了两个问题：(1) `ensureAdminElevation()` 打印「将以非管理员模式继续运行」
> 却紧接着 `os.Exit(1)`，文案与行为相反；(2) 警告文案里「镜像启动将使用文件复制模式」
> 只对文件成立，目录走的 `createJunction` 失败会直接让整个镜像创建回滚 ——
> 也就是非管理员且未开开发者模式时**实例根本起不来**。
>
> 根因是命名与实现不一致：`createJunction` 当时调的是 `os.Symlink`，在 Windows 上创建的是
> **目录符号链接**（需要 `SeCreateSymbolicLinkPrivilege`），而不是真正的 **NTFS junction**
> （`FSCTL_SET_REPARSE_POINT`，**普通用户即可创建**）。
>
> `MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` 第一部分**已实施**：换成真 junction 之后，
> `ensureAdminElevation()` / `buildElevatedArgs()` / `quoteArg()` / `--no-admin` / `mirror.IsElevated()`
> 全部删除，两条警告文案连同它们描述的问题一起不存在了。
>
> **对本方案的下游影响**：
> - §5.6 已重写 —— 收益兑现（两平台都免特权建链接），代价是 `mirror` 在 Linux 上多出一个编译阻断点。
> - §10.4「首次启动拒绝提权即退出」的**理由收窄**：仍需管理员，但只剩「注册服务」与
>   「装本地 CA 到 `LocalMachine\Root`」两项，**镜像不再是理由**。
> - 原安装器相关的管理员依赖描述已随 §10.0 一并作废。

---

## 11. 附录

### A. 命令 / 机制对照表

| 能力 | Windows | Linux |
|---|---|---|
| 执行 ARK exe | `exec.Command(exe, args...)` | `umu-run <exe> <args...>`（env: `WINEPREFIX`/`GAMEID`/`PROTONPATH`/`UMU_RUNTIME_UPDATE=0`，不设 `PROTON_VERB`，见 §5.1） |
| 脱离终端 | `SysProcAttr{HideWindow:true}` | `SysProcAttr{Setsid:true}` + `Stdin=nil` |
| 结束进程树 | `taskkill /T [/F] /PID` | `kill(-pgid, SIGTERM/SIGKILL)` |
| 端口→PID | `netstat -ano` | gopsutil `net.Connections("all")`（两平台统一） |
| 按名 + cmdline 查进程 | WMI `Win32_Process` | 扫 `/proc/*/cmdline` |
| 目录链接 | **真 NTFS junction**（`DeviceIoControl` + `FSCTL_SET_REPARSE_POINT`，`junction_windows.go`，**免特权**） | symlink（`os.Symlink`，免特权，`junction_linux.go`，已实施） |
| 链接识别 | `os.Readlink`（两平台共用，**不拆平台**；不能用 `ModeSymlink`，Go 1.23+ 起 junction 报 `ModeIrregular`） | 同左 |
| 文件身份 | `Win32FileAttributeData.CreationTime` | `Stat_t.Ino` + `Dev` |
| 进程树托管 | Job Object `KILL_ON_JOB_CLOSE` | setsid 进程组（可选 `Pdeathsig`） |
| CA 信任 | `windows.Cert*` → Root 存储 | `/usr/local/share/ca-certificates` + `update-ca-certificates`<br>或 `/etc/pki/ca-trust/source/anchors` + `update-ca-trust` |
| 私钥权限 | `icacls` | `os.Chmod(0600)` |
| 提权 | 镜像已不需要；仅 `cert install --machine` 走 `procx.RunAsAdmin`（`certmgr/cli.go`） | 无 ShellExecute 式自动提权；`cert install` 非 root 直接报错提示 `sudo` 重跑（`certmgr/cli.go`，P5），不落到 `procx.RunAsAdmin` |
| 服务 | SCM（kardianos）。BaseDir 靠服务进程自己按 §10.3 两级查找（exe 同级 → `%ProgramData%\ASAServerManager\`）读 `config.yaml` 的 `basedir` 字段，不依赖 SCM 传参 | systemd（kardianos），P5 加固：`LimitNOFILE=1048576`、`Restart=on-failure`、`WorkingDirectory=BaseDir`、`Environment=HOME=...`、`After=network-online.target`（`svcmgr/service_linux.go`，见 §5.8）；BaseDir 同理靠 §10.3 两级查找（二进制同级 → `/etc/asa-server/`），不依赖 unit 文件传参 |
| SteamCMD | `steamcmd.exe`（zip） | `steamcmd.sh`（tar.gz，需 32 位 glibc） |
| 打开浏览器 | `rundll32 url.dll,FileProtocolHandler` | `xdg-open`（GUI 排除后基本用不到） |
| FRP 客户端 | 库内调用 `frp/client.NewService`（**两平台同一份代码**，见 §5.10） | 同左 |
| Syncthing / umu / GE-Proton 下载 | `pkg/download`（**两平台同一份代码**，含 GitHub 代理，见 §5.13） | 同左，落盘后 `chmod 0755` |
| ArkApi 插件数据 | `plugindata` 全功能 | 同左（`EnableAsaPlugin` 未启用/未装时结构性静默，启用且装好后全功能，见 §5.12） |
| 路径传给 UE | 原样 | `Z:\` 前缀 + 反斜杠 |

### B. 参考资料

- [Open-Wine-Components/umu-launcher](https://github.com/Open-Wine-Components/umu-launcher) —— 统一运行时启动器
- [GloriousEggroll/proton-ge-custom](https://github.com/GloriousEggroll/proton-ge-custom) —— GE-Proton 发布页
- `scripts/ark_instance_manager.sh` —— 本仓库内的社区参考实现，注释即事故记录：
  `check_dependencies()`(L87)、`check_userns_restriction()`(L247)、
  `install_base_server()`(L396，含三项 Wine 修复与 prefix 预热)、
  `start_server()`(L883，含 `Z:` 簇路径与 setsid 分离)、`stop_server()`(L1073)
- 本仓库相关文档：`docs/V2_MIRROR_STARTUP_ARCHITECTURE.md`（镜像启动架构）、
  `docs/INTERNAL_LAYOUT_MIGRATION.md`（`pkg/` 准入标准）、`docs/AUTH_LOGIN_DESIGN.md`
