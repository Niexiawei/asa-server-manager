# Linux 兼容改造方案

> 目标：让 asa-server 在 Linux 上以**同一套 Go 代码库**运行，仍然启动 **Windows 版 ARK 服务端 exe**，
> 通过 [umu-launcher](https://github.com/Open-Wine-Components/umu-launcher) + GE-Proton 提供 Wine 运行时。
> 参考实现：`scripts/ark_instance_manager.sh`（社区脚本，已在 Linux 上跑通完整 ASA 多实例流程，本方案大量沿用它踩过的坑）。
>
> 状态：**设计方案，P0/P1 已实施，P2 起尚未实施**。文档给出耦合点清单、抽象层设计、分阶段实施计划与验收标准。

---

## 0. 修订记录：已合入的上游改动

本方案初稿之后，仓库里落地了两项与它有交集的改造，以及一个新的选型决定。三者都已就地合入正文，
此处只列**改变了结论**的部分，细节看各自章节。

| 上游改动 | 状态 | 对本方案的净影响 | 落在哪 |
|---|---|---|---|
| **镜像去管理员化（真 NTFS junction）**<br>`MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` 第一部分 | 已实施 | **净正**，但工作量搬了家 | §2.1（新增编译阻断行）、§2.3、**§5.6 已重写**、§5.9、§6 风险 13、§8 P0、§10.10、§11 A |
| **移除 WebAuthn**<br>同文档第二部分 | 已实施 | **无影响** —— 删掉的 `go-webauthn` / `go-tpm` / `fxamacker/cbor` 全是纯 Go，两平台一视同仁；`auth` 本就在 §2.3 的跨平台清单里 | 无需改动 |
| **ArkApi 插件数据隔离**<br>`ARKAPI_PLUGIN_DATA_PLAN.md` | 已实施 | **基本无影响**，新增 `internal/plugindata` 已核对为跨平台；但它在 Linux 上应当整体静默，有四条要显式确认 | §2.2、§2.3、**§5.12 新增**、§6 风险 11/16、§8 P6、§9.1 |
| **frp 改为库内调用**（本次新增决定） | 已实施 | **减少** Linux 工作量：frp 从「分平台内嵌二进制」直接退出工作清单 | **§5.10 已重写**、§5.9、§6 风险 14/15/16、§8 F 轨道、§9.1、§11 A |
| **ArkApi 在 Linux 上不再是非目标**（P2 阶段新增决定） | 待实施 | **推翻**原「Linux 上标记为不支持，强制忽略开关」的结论：`EnableAsaPlugin` 在 Linux 上与 Windows 走同一个开关、同一条 `runner` 启动路径（umu-run 拉起 `AsaApiLoader.exe`，与拉起 `ArkAscendedServer.exe` 无特殊区分）。**不是**「确认能在 Wine 下稳定工作」——只是不再由程序单方面替用户关掉，社区已有在 Proton 下跑 ArkApi 的先例，让用户自己试、失败了看日志，比强制拦截更符合这个项目「能不能跑起来用户自己判断」的一贯取向 | §1 非目标表、§5.12、§6 风险 11、§8 P6、§9.1/9.2、§11 A |

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

### 5.4 停止流程

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

| 环节 | Windows | Linux |
|---|---|---|
| 下载 | `steamcmd.zip` → 解压 | `steamcmd_linux.tar.gz` → 解包，`chmod +x steamcmd.sh` |
| 可执行文件 | `steamcmd/steamcmd.exe` | `steamcmd/steamcmd.sh`（原生 ELF，**不经 umu**） |
| 初始化 | `steamcmd.exe +quit`（pty） | `steamcmd.sh +quit`（pty，go-pty 在 Linux 是真 pty） |
| 安装 ARK | `+force_install_dir ... +login anonymous +app_update 2430930 validate +quit` | **完全相同** |
| 首次配置生成 | 直接跑 `ArkAscendedServer.exe TheIsland_WP?listen ...` 固定等 60s | 经 `runner.Run()` 跑同一条命令，**轮询等待 `Saved/Config/WindowsServer/` 出现**（最长 180s）而非固定 sleep —— Wine 冷启动比 Windows 慢得多 |

把 `SteamCmdURL` 从 `config` 包的常量拆成平台常量（`config/steamcmd_windows.go` / `_linux.go`），
或直接移进 `installer`（更合理，`config` 不该知道下载地址）。

**Linux 独有：安装/更新后必须执行三项 ASA-on-Wine 修复**（`runner/fixups_linux.go`，幂等）：

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

> 这三项直接决定「能不能起来」，不是优化项。建议实现为 `fixups.Apply()` 并在
> `UpdateArkServer` 成功后、以及每次 `StartServer` 前（幂等、开销可忽略）各调一次。

### 5.6 `internal/mirror` —— 补一个 `junction_linux.go`

> 本节已按「镜像去管理员化」（`MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` 第一部分，**已实施**）重写。
> 那次改造对 Linux 兼容**净收益为正**，但把工作量从「基本不用改」挪成了「必须补一个文件」。

改造前后对 Linux 的影响：

| 项 | 改造前 | 改造后 | 对 Linux 的影响 |
|---|---|---|---|
| `createJunction` | `mirror.go` 里的 `os.Symlink`，无构建约束 | `junction_windows.go` 的 `DeviceIoControl` + `FSCTL_SET_REPARSE_POINT`，`//go:build windows` | ⚠️ **变差**：`internal/mirror` 现在在 Linux 上编译不过，必须补 `junction_linux.go` |
| `isJunctionOrSymlink` | 只查 `os.ModeSymlink` | `os.Readlink` | ✅ **变好**：本来就是冲跨平台选的（该文档 §1.3 方案 A），Linux 上直接正确，省掉一处将来必踩的坑 |
| `createFileSymlink` | `os.Symlink`，失败回退 `CopyFile` | **已删除**，统一 `fsutil.CopyFile` | ➖ 中性，见下方「11 个文件」的取舍 |
| `IsElevated()` / 提权重启 | 存在 | 已删除 | ✅ **变好**：§10.10 里那条「两平台都免特权建链接」的论述现在成立 |

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

`service remove` 联动清理本地 CA（现 `winservice/service.go:112` 调 `certmgr.UntrustCAOnCleanup()`）
的行为在 Linux 上保留，走 §5.7 的 Linux 实现。

### 5.9 `internal/gui` —— Linux 排除

> 已定案（§10 D1）：Windows GUI 最终由 Wails 重写、`internal/gui` 整包连同 Fyne 依赖一并删除。
> 但那属于 §10 的 W 轨道；本节只做 Linux 兼容所必需的**构建约束隔离**，两者可以先后进行，互不阻塞。

- `internal/gui` 整包加 `//go:build windows`（W8 之后整包删除）。
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
| 1 | **`pluginsRelPath` 硬编码大小写** | 常量是 `ShooterGame/Binaries/Win64/ArkApi/Plugins`。这与 §6 风险 8（大小写敏感文件系统）是同一条：Linux 上只要 SteamCMD 落盘的大小写与常量不符，前缀匹配就静默失效，`plugindata` 会以为没有插件目录而什么都不做。**现在是真的要核对**——ArkApi 在 Linux 上是可用路径，落盘大小写需要在 P3 装完 ArkApi 后实测确认与常量一致 |
| 2 | **`override.go:85` 的 `strings.ToLower`** | 路径包含判定折叠了大小写，在大小写敏感文件系统上会把 `/a/DB` 与 `/a/db` 判为同一路径，导致 `DbPathOverride` 被误判成「指向实例目录内」而继续搬运。**现在是真的要修**：Linux 分支不折叠大小写，或统一走 `filepath.EvalSymlinks` 后按平台决定比较方式，P4 落地 ArkApi 启动路径时一并处理 |
| 3 | **`EnableAsaPlugin` 与前端插件面板** | 不再强制忽略：Linux 上 `EnableAsaPlugin` 与 Windows 走同一个开关、同一条 `runner` 启动路径。`webapi/pluginapi` 的端点与 `PluginDataPanel.vue` 面板在 Linux 上**照常可用**，不再需要「本平台不支持」的特殊回执；面板上可以加一条不显眼的提示（Wine/Proton 下稳定性未经验证），但不隐藏功能 |
| 4 | **`PluginSnapshotInterval`** | 已进 `InstanceConfig` 与 `instance_config.ini`（`0` = 默认 5 分钟、负数 = 关闭）。两平台语义一致，无需特殊处理 |

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
| 4 | **`$HOME` 未设置/错误**（systemd 场景） | 运行时反复下载或 steamclient 崩溃 | `svcmgr` 强制注入 `HOME`；启动自检校验可写 |
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
| **W 轨道（Windows，可并行）** | Wails 取代 Fyne + 安装程序 + 首次运行引导 —— 见 **§10**，步骤 W0–W9 | Fyne 依赖清空；双击安装 → 引导 → 服务注册运行 | 另计，见 §10.8 |
| **P1 进程原语** ✅ 已完成 | `pkg/winproc` → `pkg/procx`（`GetPIDByPort` 删除，改走下面的统一实现）；`procx_linux.go` 真实现（`/proc` 扫描：`IsProcessExited`/`ProcessImageName`/`QueryProcess`，`RunAsAdmin` 返回「本平台不适用」）；新增 `pkg/procx/port.go`（`PIDByPort`，gopsutil `net.Connections("all")`，TCP/UDP 一次覆盖，无构建约束两平台共用），`internal/process.IsServerRunning` 随之从「Windows netstat 文本解析 / Linux 存根」两个平台文件收敛成一份跨平台实现；`pkg/processjob` → `pkg/proctree`，Linux 实现（`Setsid` + `Close()` 时 `kill(-pgid, SIGKILL)`，含 `pgid>1` 断言）；新增 `procx.Terminate`/`Kill`/`TerminateTree`/`KillTree`，替换掉 `server.go`/`common.go`/`installer.go` 里全部 9 处 `exec.Command("taskkill", ...)`（Windows 侧行为不变，仍是同一套 taskkill 参数，只是挪进了函数） | `CGO_ENABLED=0 GOOS=linux go build ./...`、`go build`(windows，原生 cgo)、两平台 `go vet` 均通过；`grep -rn taskkill --include=*.go` 命中数归零（除注释与 procx 内部实现自身）；新增 `pkg/procx/port_test.go` 用真实 TCP/UDP 监听自证 `PIDByPort`（不依赖解析 netstat 输出），Windows 上跑通。**未验证**：`procx_linux.go` 的 `/proc` 扫描与 `proctree_linux.go` 的 `setsid`/`kill(-pgid)` 只做到跨平台编译通过，未在真实 Linux 上跑过——本机 WSL 的 go1.27.0 安装本身已损坏（`internal/abi/map.go`/`map_swiss.go` 均缺 `//go:build` 约束、重复声明，`go build` 连标准库都过不了，与本次改动无关），修好前无法做运行时验证；这两个函数目前也没有真实调用方（游戏进程要到 P2 `runner` + P4 才会在 Linux 上真正跑起来），风险可控但记在这里，P2/P4 验收时要补跑。落在 `master`，未开分支 | 2–3 天 |
| **P2 umu 运行时** ✅ 已完成 | `internal/runner` 接口（`Run`/`GamePath`/`EnsureRuntime`/`Preflight`/`Configure`）+ 两平台实现，`Run` 对 `ArkAscendedServer.exe` 与 `AsaApiLoader.exe` 一视同仁（见 §0/§1 的 ArkApi 决定）；`umu_linux.go` 下载 umu-launcher zipapp + GE-Proton（走 `pkg/download`，含 `github_proxy`）、prefix 预热（照抄 `ark_instance_manager.sh` 的 wineboot --init + steamrt 就绪检测 + wineserver drain 轮询）与 `.created-by-proton` 版本标记/迁移；`preflight_linux.go` 五项依赖自检（32 位 glibc / python3≥3.10 / libzstd.so.1 / tar / AppArmor userns，读 `/proc/sys` 而非 shell 出去跑 `sysctl`）；`internal/webapi/systemapi` 的 `GET /api/system/preflight`；`config.yaml` 新增 `linux:` 段（`appconfig`）；`EnsureRuntime` 在 `InitializationBasicComponents` 里后台异步跑，不阻塞服务启动。**执行细节对拍**：GE-Proton/umu 的下载 URL 与 tar 内部布局已用真实 GitHub Releases API 核对（非猜测），warm-up 与 fixups 的具体命令逐行对照本仓库 `scripts/ark_instance_manager.sh` 的验证过的实现，而非重新推导 | `CGO_ENABLED=0 GOOS=linux go build ./...`、`go build`（windows 原生 cgo）、两平台 `go vet` 均通过；`extractTar`（strip-prefix + zip-slip 拒绝，含嵌套 `..` 变体）与 Windows 侧 `Run`/`GamePath` 有真实执行的单测（非仅编译检查）。**已知偏差与限制**：①GE-Proton 校验走官方 `.sha512sum`（新增 `pkg/download` 对 `sha512:` 算法的支持），umu 校验走 GitHub Releases API 的 `digest` 字段（一次性的固定 tag 元数据请求，不是"解析 latest"，但确实触达 `api.github.com`，与 §4.3"从不碰 API"的原则有个可接受的例外，失败时降级为不校验而非拦截，已在代码注释说明）；②`umu_version` 从文档原稿的占位符 `1.4.0` 更新为已核实存在的 `1.4.4`；③`PROTON_VERB=run` 从设计阶段的示意代码中去掉——参考脚本的实际调用从不设它；④尚未在真实 Linux 主机上跑过 `EnsureRuntime`/`Preflight`（本机 WSL 的 go1.27.0 安装已损坏，见 P1 行），下载、解压、prefix 预热的端到端行为仍待 P3/P4 阶段用真机验证 | 3–4 天 |
| **P3 安装与更新** | `installer` 分平台（steamcmd.sh、下载 URL）；ASA-on-Wine 三项修复；首次配置生成改轮询等待 | Linux 上 `update` 走完，`server-files` 完整，`Saved/Config/WindowsServer` 生成 | 2–3 天 |
| **P4 实例生命周期** | `StartServer` 走 `runner`；`GamePath` 转换；双 PID 语义；停止/强停/重启全链路；镜像 `IsElevated` 处理 | **单实例**启动→玩家可连入→RCON 可用→优雅停止；**双实例**并发启动互不干扰 | 3–5 天 |
| **P5 服务化与证书** | `winservice` → `svcmgr` + systemd（`HOME`/`LimitNOFILE`/非 root）；`certmgr` Linux 信任实现 | `service install/start/stop/remove` 全通；HTTPS 可用 | 2 天 |
| **P6 收尾** | 定时任务/批量/倒计时/备份/存档解析在 Linux 上回归；**ArkApi 在 Linux 上的落地确认（§5.12 表格四条：大小写常量核对、`override.go` 大小写折叠修复、`pluginapi`/面板可用性、`PluginSnapshotInterval` 语义）**；构建脚本与 CI 加 linux target；文档（部署指南、依赖清单、故障排查） | 测试矩阵（§9）全绿 | 2–3 天 |

**合计约 15–22 人日**（P0–P6，不含并行的 F / W 轨道），不含 Wine 侧疑难问题的排查缓冲（建议再留 30%）。
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

## 10. Windows 侧配套：Wails GUI、安装程序与首次运行引导

> 本节不是 Linux 兼容的必要工作，但与 P0「把 GUI 拆出去」是同一块工作面，一起做可以省一次返工。
> **当前状态：已定案，尚未动代码。**

### 10.1 决策记录

| # | 决策 | 内容 | 理由 |
|---|---|---|---|
| D1 | **移除 Fyne，改用 Wails** | 删除 `internal/gui` 整包与 `fyne.io/fyne/v2`，Windows GUI 用 Wails 重写 | 复用现有 Vue SPA（不再维护第二套 UI）+ 去掉全仓库唯一的 cgo 来源 |
| D2 | **同时提供安装程序** | `wails build -nsis` 产出 NSIS 安装器，负责装文件、注册服务 | 与 D1 同一条工具链，不额外引入 Inno Setup / WiX |
| D3 | **前端走反向代理** | Wails `AssetServer.Handler` 反代 `127.0.0.1:19193`，不内嵌 dist | HttpOnly Cookie 鉴权要求同源，见 §10.4 |
| D4 | **服务管理走 Wails 绑定方法** | 不补 `/api/service/*` HTTP 端点 | 服务管理需要管理员，不该挂在可远程访问的 HTTP 面上 |
| D5 | **系统托盘：本期移除** | 不再提供托盘驻留 | Wails v2 无内置托盘；等 v3 正式版再考虑。见 §10.5 的行为后果 |
| D6 | **新增首次运行引导程序** | 页面化完成 BaseDir 选择与 SteamCMD 初始化 | 解决 §10.7 的 BaseDir 冲突，且是安装器能否注册服务的前置 |
| D7 | **Linux 只有 CLI 模式** | Linux 不编译任何 GUI，引导走 `asa-server setup` | Wails Linux 后端需 cgo，与静态编译目标冲突；见 §10.9 |
| D8 | **`asa-server api` 保持一等入口** | GUI 与引导都是**可选外壳**，不能成为运行的必经之路 | 见 §10.10，这条约束反向限制了 D6 的设计 |

D1 + D2 + D6 三者是耦合的：**安装器不能在安装阶段就注册并启动服务**，
因为那时 BaseDir 还没选，服务会在 Program Files 里建目录 —— 正是要避免的事。
正确顺序是 `安装文件 → 首次运行引导 → 引导结束时才注册服务`，详见 §10.6 与 §10.7。

### 10.2 为什么现在讨论

P0 已经要给 `internal/gui` 加 `//go:build windows`。Fyne 是**本仓库唯一的 cgo 来源** ——
实测 `go build ./...` 目前只在 `github.com/go-gl/gl`、`github.com/go-gl/glfw` 两个包上因缺 C 工具链失败，其余全部正常。
Wails v2 的 Windows 后端不需要 cgo（见 §10.3），换过去之后**两个平台都能 `CGO_ENABLED=0` 出静态二进制**，
交叉编译与 CI 都变简单。既然 P0 无论如何要动 GUI 的构建约束，这时候一并决定「拆走还是换掉」最省事。

### 10.3 Wails 可行性核对表

| 项 | 结论 | 备注 |
|---|---|---|
| Windows 是否需要 cgo | **否**（v2 用 go-webview2 的纯 Go COM 绑定） | ⚠️ 这是选型前提，**必须实测确认**（§10.8 W0），一条 `wails build` 就能验 |
| WebView2 Runtime | Win11 预装；Win10 需引导 | `wails build -webview2 embed\|download\|browser\|error` 选策略 |
| 系统托盘 | **本期不做**（D5） | v2 无内置托盘。`fyne.io/systray` 虽是独立纯 Go 模块（`go.mod:37`，Windows 下不拖回 OpenGL），但为了 D1 的「彻底移除 Fyne 依赖」目标，本期一并删掉，等 Wails v3 正式版的内置托盘 |
| v2 / v3 选型 | 按 v2 规划 | v3 长期处于 alpha，动手前核对上游当前状态；托盘是 v3 才补齐的能力之一 |
| 生成安装器 | `wails build -nsis` | 需本机装 NSIS(`makensis`)；产出可编辑的 `build/windows/installer/project.nsi` |
| Linux 后端 | 需 gtk3 + webkit2gtk（cgo） | 见 §10.9，**Linux 侧一律不参与** |

### 10.4 前端复用：反向代理（定案 D3）

**webview 不内嵌资源，反向代理到本地 API。**
用 Wails 的 `AssetServer.Handler` 挂一个反代打到 `127.0.0.1:19193`：

- `app/` 的 Vue SPA **零改动**，GUI 与浏览器看到的是同一个前端，以后也只维护一份。
- **关键理由**：本项目的会话凭证走 **HttpOnly Cookie**（`docs/AUTH_LOGIN_DESIGN.md`：`EventSource` 与浏览器
  `WebSocket` 都无法设置自定义请求头，所以 `Authorization: Bearer` 在本项目不成立）。
  若 webview 内嵌一份 dist 再跨源 fetch API，Cookie 会因跨源 / SameSite 失效，**鉴权直接坏掉**。
  反代让两者同源，整个问题绕开。
- 附带好处：webview 走进程内 http，不碰 TLS，本地 CA 是否已被信任与 GUI 无关。

被否掉的替代方案：把 `app/dist` 交给 Wails assetserver、再用绑定方法暴露后端能力 ——
前端要改、鉴权要重做，且从此有两份前端要同步。

**唯一的例外是引导页面**（§10.7）：它必须在 API server 起来之前就能显示，
所以引导页走 Wails 自己的 AssetServer，不经反代。反代只在引导完成后接管。

### 10.5 现有 GUI 功能的落点

`internal/gui/gui.go`（862 行）目前提供 7 类能力，迁移后落点不同：

| 现有能力 | 落点 |
|---|---|
| 资源监控（CPU/内存，`gui.go:102`） | SPA 已有，白拿 |
| 实例列表（`gui.go:187`） | SPA 已有，白拿 |
| 打开 WebUI（`gui.go:538`，`rundll32`） | 不再需要，GUI 本身就是 WebUI |
| **服务安装/卸载/启停**（`gui.go:384-457`） | **Wails 绑定方法**（D4）直接调 `winservice.InstallService/Remove/Start/Stop`。不补 `/api/service/*` HTTP 端点 —— 服务管理需要管理员权限，不该挂在可被远程访问的 HTTP 面上 |
| API server 内嵌启停（`gui.go:458-516`） | 保留；GUI 进程自带 API 的「单机模式」与「连到已注册服务」两种形态的取舍不变 |
| 系统托盘（`gui.go:584`） | ❌ **移除**（D5） |
| 管理员检测与提权（`gui.go:345,369`） | 保留 —— 引导程序（§10.7）与服务管理都要它 |

**托盘移除的行为后果**（要在 UI 上讲清楚，否则是个坑）：
关闭窗口 = GUI 进程退出，但**已注册的 Windows 服务仍在后台运行，游戏实例不受影响**。
用户很容易把「关掉窗口」误解成「关掉服务器」。建议关闭时给一次确认提示，
说明服务仍在运行、以及如何重新打开或停止。等 Wails v3 补上托盘后再回到驻留形态。

`gui.go:32` 内嵌的 `ASA_Logo_transparent.webp` 原本用作托盘与窗口图标，
托盘移除后仍可复用为 Wails 窗口图标与安装器图标，资源不用重做。

**移除 Fyne 后 `go.mod` 可删除的依赖**：`fyne.io/fyne/v2`、`fyne.io/systray`、
`github.com/fyne-io/{gl-js,glfw-js,image,oksvg}`、`github.com/go-gl/gl`、`github.com/go-gl/glfw/v3.4/glfw`
及其传递依赖。这也是全仓库最后的 cgo 来源。

### 10.6 安装程序（D2）

⚠️ **与直觉相反的一点：安装阶段不注册服务。**
注册服务必须知道 BaseDir（服务启动就会去建目录），而 BaseDir 由引导程序决定。
若在安装段就 `service install + start`，服务会以 LocalSystem 在 `$INSTDIR`（多半是 Program Files）
里建出 `instances/`、`server-files/` —— 正是 §10.7 要避免的事。

**安装段**只做三件事：

```nsis
; 1. 释放文件到 $INSTDIR（只放程序，不放数据）
; 2. 创建开始菜单/桌面快捷方式
; 3. 拉起首次运行引导（服务注册在引导结束时才做）
Exec '"$INSTDIR\asa-server.exe"'
```

**卸载段**必须完整走这套，顺序不能反：

```nsis
ExecWait '"$INSTDIR\asa-server.exe" service stop'
ExecWait '"$INSTDIR\asa-server.exe" service remove'
; 再删 bootstrap（见 §10.7），最后删程序文件
```

四条硬性要求：

1. **安装器必须 `RequestExecutionLevel admin`**。注册服务、写 `%ProgramData%`、装 CA 都要管理员。
2. **重复安装要幂等**：服务已存在时先 `service remove` 再装，否则 SCM 报「服务已存在」。
3. **卸载必须走 `service remove`，不能直接删文件**。`winservice/service.go:112` 在 Remove 时会调
   `certmgr.UntrustCAOnCleanup()`，清理装进 `LocalMachine\Root` 的本地 CA。
   跳过它会在用户系统里**永久留下一张受信任的根证书** —— 这是安全问题，不是清洁度问题。
   卸载时服务可能本来就没注册（用户装完没走完引导），所以这两条要容忍失败。
4. **卸载默认不删数据目录**。BaseDir 下是 25 GB+ 的服务端与存档，
   删不删必须显式问用户，且默认「保留」。程序目录与数据目录分离正是为了这个。

### 10.7 首次运行引导程序（D6）

#### 10.7.1 要解决的问题：BaseDir

`internal/config/config.go:69-80` 的 `BaseDir` = 环境变量 `ASA_BASEDIR`，否则 **exe 所在目录**。
项目当前是「绿色解压即用」形态，这条规则没问题；一旦有了安装器就成了坑：

- 装进 `C:\Program Files\...` 后，服务以 LocalSystem 跑写得进去，
  但 `instances/` + `server-files/` 是 **25 GB 起步的游戏数据**，落在系统盘 Program Files 是错的。
- 更糟的是**交互式 GUI 以普通用户身份运行**，会把 BaseDir 解析到同一个 Program Files 路径却没有写权限 ——
  服务与 GUI 看到同一路径、一个能写一个不能写，故障现象非常难懂。

引导程序让用户在页面上选定数据目录，从根上消掉这个歧义。

#### 10.7.2 先决改动：BaseDir 的解析与持久化

**⚠️ 这是引导程序的最大障碍**：`main.go:51` 目前**无条件**调用 `cfgpkg.EnsureDirectories()`，
而且在 logger 初始化和 CLI 解析**之前**。也就是说程序一跑起来，目录树就已经建在 exe 旁边了 ——
引导页还没机会显示，事情就已经做完了。

必须拆成两步：

```go
// config 包
func ResolveBaseDir() (dir string, configured bool)  // 只解析，不落盘
func EnsureDirectories() error                       // 真正建目录，引导完成后才调
```

`main.go` 的顺序改为：`ResolveBaseDir()` → 已配置则照旧（`EnsureDirectories` + logger + appconfig）；
未配置则**只启动引导**，不碰磁盘。

**BaseDir 存哪**（它必须在 BaseDir 之外，`config.yaml` 解决不了这个鸡生蛋问题）：

| 平台 | bootstrap 路径 |
|---|---|
| Windows | `%ProgramData%\ASAServerManager\bootstrap.json` |
| Linux | `/etc/asa-server/bootstrap.json` |

内容就一行：`{"base_dir": "D:\\ASAServerManager"}`。

选 ProgramData 而不是注册表或机器级环境变量，理由：
**服务以 LocalSystem 跑、GUI 以普通用户跑，两者都能读到同一份**（ProgramData 默认 Everyone 可读，
写需要管理员 —— 而引导程序本来就要提权）；不碰注册表；Linux 有天然对应物。

最终优先级（**保留 exe 目录兜底**，现有绿色部署升级后行为不变）：

```
--basedir 参数  >  ASA_BASEDIR 环境变量  >  bootstrap.json  >  exe 所在目录
```

#### 10.7.3 引导页面怎么渲染

引导必须在 API server 起来**之前**显示（没有 BaseDir 就没有 config.yaml、auth.db、日志目录、证书），
所以它**不能**走 §10.4 的反代。

- 引导页由 **Wails 自己的 AssetServer** 提供，是一个独立的小页面（不塞进 `app/` 主 SPA —— 
  主 SPA 依赖完整的鉴权与 API）。
- 页面通过 **Wails 绑定方法**调后端，与 D4 的服务管理走同一套机制。
- 引导完成 → 建目录、写 bootstrap、起 API server → webview 导航到反代的主 SPA。

#### 10.7.4 引导步骤

| 步 | 内容 | 复用的现成能力 |
|---|---|---|
| 1 | 环境自检：管理员权限、WebView2、19193 端口占用 | Linux 侧复用 `runner.Preflight()`（§4.2） |
| 2 | **选择数据目录**（原生目录选择框 `runtime.OpenDirectoryDialog`） | — |
| 3 | 服务模式：注册为 Windows 服务（开机自启）/ 仅本次会话运行 | `winservice.InstallService/Start` |
| 4 | 端口与 TLS：是否把本地 CA 装进 Root 存储 | `certmgr.TrustCA()` |
| 5 | 鉴权：是否启用 `auth.enabled`；启用则建第一个管理员账号 | `internal/auth`（CLI 已有 `user add`） |
| 6 | **SteamCMD 安装**（流式输出日志） | `installer.DownloadAndExtractSteamCmd(ctx, w)` |
| 7 | ARK 服务端本体下载（~25 GB，**可跳过**，之后从主界面做） | `installer.UpdateArkServer(ctx, w)` / `/api/server/update` |
| 8 | 完成：写 bootstrap.json + config.yaml → 注册并启动服务 → 跳主界面 | — |

**进度流式输出是白拿的**：`installer.DownloadAndExtractSteamCmd(ctx, outputCallback ...io.Writer)`
与 `UpdateArkServer` 已经接受 `io.Writer` 用于透传控制台输出（现在喂给 SSE `TaskBroadcaster`）。
引导只需把一个 writer 适配到 Wails 的 `runtime.EventsEmit`，**installer 一行不用改**。

第 2 步的目录校验必须做三件事：**可写**、**剩余空间 ≥ 30 GB**（ARK 本体约 25 GB + 存档增长）、
**已存在 `config.yaml` 时识别为「接管已有安装」而非新建**。

#### 10.7.5 两个必须处理的边界情况

**1. 用户拒绝提权 → 友好提示后直接退出。**

引导要做的每件事（写 `%ProgramData%`、注册服务、装 CA 到 `LocalMachine\Root`）都需要管理员。
拿不到就**不做半套** —— 给一句说清楚的提示然后退出，不要静默降级成一个功能残缺、
用户还以为装好了的状态。提示至少要讲清三件事：为什么需要管理员、如何以管理员重新运行、
以及「只想直接跑起来可以用 `asa-server api`」这条出路（见 §10.10）。

> 这条只约束**引导程序与 GUI**。headless 运行的非管理员出口是另一条路径，不受此限，见 §10.10。

**2. 禁止把 BaseDir 选在映射网络盘 / 网络文件系统上。**

表面的理由是权限：服务以 LocalSystem 运行，映射盘（`Z:` 之类）是**登录会话级**的，
在服务会话里根本不存在；用户目录（`C:\Users\xxx\...`）的 ACL 也可能挡住。
这个坑的表现是「GUI 里一切正常，注册成服务后启动就失败」，极难排查。

更硬的理由是：**BadgerDB 用 mmap + 文件锁**（`{BaseDir}/database_file/state_db`），
在 SMB/CIFS/NFS 上行为不可靠，可能直接损坏实例状态库。

校验规则：

| 平台 | 拒绝条件 |
|---|---|
| Windows | `GetDriveTypeW() == DRIVE_REMOTE`；UNC 路径（`\\server\share` 开头）；`subst` 出来的虚拟盘 |
| Linux | 挂载点 fstype 属于 `nfs`/`nfs4`/`cifs`/`smbfs`/`fuse.sshfs` 等（查 `/proc/mounts`） |

校验失败时明确告诉用户「换一个本地磁盘目录」，而不是笼统报错。
同一套校验 `asa-server setup` CLI 也要走 —— 规则属于 `internal/setup`，不属于 GUI。

#### 10.7.6 非 GUI 的引导路径：`asa-server setup`

引导的编排逻辑放进**平台无关的 `internal/setup` 包**，前端有两个：

| 前端 | 平台 | 说明 |
|---|---|---|
| Wails 引导页 | 仅 Windows | §10.7.3 |
| `asa-server setup` CLI | **Windows + Linux** | 可交互，也可全参数非交互跑（自动化部署/CI） |

**CLI 版在两个平台上都要有**，不只是 Linux：Windows 上也存在无桌面、
只想 headless 跑的场景（见 §10.10），不能强迫用户先过一遍 GUI。
Linux 上它是**唯一**的引导路径（D7），并且多一步 umu/GE-Proton 运行时安装（§4）。

`setup` 与现有的 `service` / `db` / `user` / `cert` 是同级 CLI 命令组，风格一致。

### 10.8 落地步骤

**W0 / W1 是整个方案的地基，不通过就不值得动 `internal/gui`。**

| 步骤 | 内容 | 通过判据 | 依赖 |
|---|---|---|---|
| **W0** | 空目录起一个 Wails v2 demo，`CGO_ENABLED=0 wails build` | 在**没装 mingw** 的机器上能出 exe → cgo 前提成立 | — |
| **W1** | demo 里用 `AssetServer.Handler` 反代 `http://127.0.0.1:19193` | 现有 SPA 能登录（HttpOnly Cookie 有效）、SSE 与 WebSocket 均正常 | W0 |
| W2 | 拆 `config.ResolveBaseDir()` / `EnsureDirectories()`，改 `main.go` 启动顺序 | 未配置 BaseDir 时启动**不产生任何目录** | — |
| W3 | bootstrap.json 读写 + 四级优先级 | 服务（LocalSystem）与 GUI（普通用户）读到同一个 BaseDir；无 bootstrap 时回落 exe 目录，绿色部署行为不变 | W2 |
| W4 | 引导页面（Wails AssetServer）+ 绑定方法，走通步骤 1–8 | 全新机器上选目录 → 装 SteamCMD → 注册服务 → 跳主界面 | W1,W3 |
| W5 | 服务管理绑定方法（D4）替换 `gui.go:384-457` | 安装/卸载/启停四个操作在 Wails 下等价可用 | W1 |
| W6 | `wails build -nsis`，按 §10.6 改模板（**安装段不注册服务**） | 双击安装 → 自动进引导 → 完成后服务已注册并运行 | W4,W5 |
| W7 | 走一遍卸载 | 服务已移除、本地 CA 已从 Root 存储清掉、**数据目录保留**、bootstrap 已删 | W6 |
| W8 | 删除 `internal/gui` 与全部 Fyne 依赖，`go mod tidy` | 全仓库 `CGO_ENABLED=0` 可构建（Windows 与 Linux 皆是） | W5 |
| W9 | `internal/setup` 抽出平台无关编排 + `asa-server setup` CLI（**两平台都要**） | Windows 与 Linux 上均能非交互跑通同一套引导 | W4 |
| W10 | **回归：`asa-server api` 独立可用**（D8 三条不变量） | 全新解压的目录里直接 `asa-server api`，不经安装器/引导也能起服，行为与改造前一致 | W3,W9 |

- W9 与 Linux 方案的 P2/P3 有重叠（运行时安装、SteamCMD 安装），建议合并实施。
- **W10 是每一步都要复查的红线**，不是做完一次就算过 —— W2/W3 动的是 BaseDir 解析，
  最容易在这里把便携模式改坏。

### 10.9 Linux：只有 CLI 模式（D7）

**Linux 不编译任何 GUI。** Wails 的 Linux 后端需要 gtk3 + webkit2gtk、**必须开 cgo**，
与本方案「Linux 无头、`CGO_ENABLED=0` 静态二进制、交叉编译无痛」直接冲突。
所以 **Wails 包与现在的 `gui` 包一样加 `//go:build windows`**。
一旦让 Wails 参与 Linux 构建，§5.9 的全部收益作废 —— 这条不能松。

Linux 上的完整入口就是 CLI：

| 命令 | 用途 |
|---|---|
| `asa-server setup` | 首次引导：依赖自检 → BaseDir → umu/GE-Proton 运行时 → SteamCMD → ARK 本体 |
| `asa-server api` | 启动服务（无参启动在 Linux 上等价于此，见 §5.9） |
| `asa-server service …` | systemd 安装/启停/移除（§5.8） |
| `asa-server cert / db / user …` | 证书、鉴权库、账号管理（均已存在） |

管理界面仍然有 —— 就是浏览器打开 `https://<host>:19193` 的那个 SPA，
只是不再有一个本地桌面外壳。这对无头服务器反而是正确形态。

两处必须共享、不能各写一份的东西：

- **`internal/setup`**（§10.7.6）：引导编排逻辑平台无关，Wails 引导页与 `asa-server setup` CLI 都是它的前端。
- **bootstrap.json**（§10.7.2）：两平台同一套解析优先级，只有落盘路径不同
  （`%ProgramData%\ASAServerManager\` vs `/etc/asa-server/`）。

### 10.10 `asa-server api` 始终是一等入口（D8）

> **这条是对 D1/D2/D6 的反向约束**：GUI、安装器、引导程序都是**可选外壳**，
> 任何时候用户都能绕开它们，直接 `asa-server api` 把服务跑起来。

三条不变量，改造过程中不允许被破坏：

1. **BaseDir 解析必须保留 exe 目录兜底。**
   §10.7.2 的四级优先级里，`bootstrap.json` 之后仍然回落到 exe 所在目录。
   所以在一台从未跑过引导的机器上解压即用、直接 `asa-server api`，
   行为与今天**完全一致** —— 绿色/便携模式不因为引入安装器而消失。
2. **引导做的每件事都必须有 CLI 或配置等价物**，且都已经存在或已在计划内：

   | 引导步骤 | 非 GUI 等价物 |
   |---|---|
   | 选 BaseDir | `--basedir` / `ASA_BASEDIR` / bootstrap.json |
   | 注册服务 | `asa-server service install` |
   | 装本地 CA | `asa-server cert install` |
   | 建管理员账号 | `asa-server user add` |
   | SteamCMD + ARK 本体 | `asa-server update` |
   | 全流程一把梭 | `asa-server setup`（非交互模式） |

3. **`api` 不得依赖 GUI 进程，也不得要求 bootstrap.json 存在。**
   两者缺失时按第 1 条回落，不报错、不弹引导。

**与 §10.7.5.1「拒绝提权即退出」的关系**（容易看成矛盾，其实不是）：
那条约束的是**引导程序**——引导要么完整做完，要么干净退出，不留半套。
而 headless 的非管理员路径现在**根本不需要出口**：提权逻辑整体没了，`asa-server api`
在普通账户下直接就能跑（Linux 上则从来不存在提权这回事）。两者并行，互不覆盖。

> ✅ **已完成，本节的历史问题已全部消解。**
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
> - §10.7.5.1「引导拒绝提权即退出」的**理由收窄**：引导仍需管理员，但只剩「注册服务」
>   与「装本地 CA 到 `LocalMachine\Root`」两项，**镜像不再是理由**。该节的提示文案要相应删掉
>   与镜像/权限相关的措辞。
> - 安装器（§10.6）对管理员的依赖同步减半。

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
| 提权 | 镜像已不需要；仅 `cert install --machine` 走 `procx.RunAsAdmin`（`certmgr/cli.go:67`） | 不需要；仅证书信任需 root（`procx.RunAsAdmin` 返回「本平台不适用」） |
| 服务 | SCM（kardianos） | systemd（kardianos） |
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
