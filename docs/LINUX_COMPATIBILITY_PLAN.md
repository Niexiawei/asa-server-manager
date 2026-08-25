# Linux 兼容改造方案

> 目标：让 asa-server 在 Linux 上以**同一套 Go 代码库**运行，仍然启动 **Windows 版 ARK 服务端 exe**，
> 通过 [umu-launcher](https://github.com/Open-Wine-Components/umu-launcher) + GE-Proton 提供 Wine 运行时。
> 参考实现：`scripts/ark_instance_manager.sh`（社区脚本，已在 Linux 上跑通完整 ASA 多实例流程，本方案大量沿用它踩过的坑）。
>
> 状态：**设计方案，尚未实施**。文档给出耦合点清单、抽象层设计、分阶段实施计划与验收标准。

---

## 1. 目标与非目标

### 目标

1. `GOOS=linux go build` 能产出可用二进制，`asa-server api` 在 Linux 上提供与 Windows 完全一致的 HTTP API 与前端。
2. 实例的**创建 / 启动 / 停止 / 重启 / 强制停止 / 更新 / 备份 / 定时任务 / RCON / 日志流**在 Linux 上行为等价。
3. Windows 侧行为**零回归**——所有平台特化通过构建约束隔离，Windows 编译产物的代码路径不变。
4. Linux 侧运行时（umu-launcher zipapp、GE-Proton、Wine prefix）由程序自己下载与管理，落在 `{BaseDir}` 内，
   不依赖发行版打包，与现有 SteamCMD 的自管理方式一致。

### 非目标（本期明确不做）

| 项 | 原因 |
|---|---|
| Fyne 桌面 GUI 跑在 Linux | ARK 服务器场景基本是无头机器；Fyne 需要 X11/Wayland + OpenGL + cgo，成本高收益低。Linux 构建直接排除 GUI。 |
| ArkApi / `AsaApiLoader.exe` 插件支持 | 该加载器依赖 Windows 进程注入与 DLL hook，Wine 下不可靠。Linux 上标记为**不支持**，配置开关被强制忽略并回执告警。 |
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
| `internal/mirror/mirror.go:97` | `windows.OpenProcessToken` | `IsElevated()` |
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
| `internal/instance/common.go:129,469` | `winproc.QueryProcess("ArkAscendedServer.exe", "Port=...")` | WMI 不存在 |
| `internal/appconfig/localnet.go:59` | 虚拟网卡名过滤含 `tap-windows` | 需补 `docker0`/`veth`/`br-`/`virbr` |
| `internal/gui/gui.go:539` | `rundll32 url.dll,FileProtocolHandler` 开浏览器 | — |
| `internal/certmgr/store.go:236` | `icacls` 收紧私钥 ACL | Linux 用 `os.Chmod(0600)` |
| `main.go:281` | `winproc.RunAsAdmin` 提权 | Linux 上建 symlink 不需要特权，整个提权流程应跳过 |

### 2.3 天然跨平台（无需改动）

`internal/webapi`（含全部子包）、`internal/auth`、`internal/appconfig`、`internal/state`(BadgerDB)、
`internal/rconx`、`internal/realtime`、`internal/countdown`、`internal/batchmanage`、`internal/schedule`、
`internal/updatemanage`、`internal/backup`(tar+zstd 纯 Go)、`internal/parseserver`、`internal/logger`、
`pkg/fsutil`、`pkg/netutil`、`pkg/console`、`pkg/iox`、`pkg/serverinfo`(gopsutil)、`app/`、`icon/`。

`internal/mirror` 的**核心算法**也跨平台：`createJunction` / `createFileSymlink` 底层就是 `os.Symlink`
（`mirror.go:473,495`），Linux 上是原生 symlink，甚至比 Windows 更省事（不需要管理员权限）。
只有 `IsElevated()` 需要替换。

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

启动时（Linux 分支）执行一次自检，缺项直接给出发行版对应的安装命令并拒绝启动 —— 沿用脚本 `check_dependencies()`：

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
    umuVersion     = "1.4.0"
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

**Linux 实现**（`runner_linux.go`）关键点：

```go
cmd := exec.CommandContext(ctx, umuRunBin, append([]string{exePath}, args...)...)
cmd.Dir = opt.Dir
cmd.Env = append(os.Environ(),
    "WINEPREFIX="+prefixDir,
    "GAMEID="+gameID,            // umu-default
    "PROTONPATH="+geProtonPath,  // 具体目录，不是别名
    "PROTON_VERB=run",
    "UMU_RUNTIME_UPDATE=0",
)
// 关键：独立会话 + 进程组，脱离控制终端。
// 等价于脚本里的 setsid nohup ... </dev/null &，
// 保证 API 进程退出 / SSH 断开不会带走已启动的服务器。
cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
cmd.Stdin = nil
```

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

### 5.6 `internal/mirror` —— 基本不用改

`createJunction` / `createFileSymlink` 已经是 `os.Symlink`，Linux 原生支持且**不需要特权**
（Windows 上需要管理员或开发者模式，这正是 `IsElevated()` 存在的原因）。

改动仅两处：

- `IsElevated()` 拆平台：Linux 实现对 symlink 而言恒真；或干脆改名为 `CanSymlink()` 更贴合它唯一的用途。
- `main.go` 的 `ensureAdminElevation()`（`main.go:272`）整个走 Windows 分支，Linux 上 no-op。

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
- `main.go` 拆出 `main_windows.go` / `main_linux.go`，各自提供 `actionGUI` 与 `ensureAdminElevation`：
  Linux 版 `actionGUI` 返回 `errors.New("GUI 仅在 Windows 上可用，请使用 asa-server api")`，
  `ensureAdminElevation` 为 no-op。
- 删掉 `main.go:38-42` 的 `runtime.GOOS != "windows"` 硬拦截。
- 无参数启动的默认行为：Windows 仍是 GUI，Linux 改为等价于 `api`。

**副产品**：排除 Fyne 后 Linux 构建**不再需要 cgo**（modernc/sqlite、badger、gopsutil、creack/pty 都是纯 Go），
`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build` 可产出静态二进制，交叉编译无痛。
若 `os/user` 触发 cgo，加 `-tags osusergo,netgo`。

### 5.10 `frpmanage` / `syncthingmanage` —— 内嵌二进制分平台

```
internal/frpmanage/
├── embed_windows.go   //go:build windows  → //go:embed bin/frpc.exe
├── embed_linux.go     //go:build linux    → //go:embed bin/frpc
└── bin/{frpc.exe, frpc}
```

暴露 `embeddedBinary() ([]byte, string)`（内容 + 落盘文件名），manager 里的 MD5 比对与提取逻辑不变，
只是落盘后 Linux 要 `os.Chmod(0755)`。Syncthing 同理。

代价：仓库体积翻倍（frpc ~15 MB、syncthing ~30 MB × 2 平台）。

替代方案：Linux 下改为「首次使用时从 GitHub Release 下载」，与 umu/GE-Proton 的处理一致，仓库不增重。
**推荐后者** —— Linux 用户已经要下 450 MB 的 GE-Proton，多两个小下载无感，而仓库能省 45 MB。

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
| 11 | **ArkApi 不可用** | 开了 `EnableAsaPlugin` 的实例在 Linux 上行为未定义 | Linux 下强制忽略该开关并在 API 响应与日志中告警；前端在 Linux 上禁用该选项 |
| 12 | **首次安装耗时长**（GE-Proton 450 MB + SLR + prefix + ARK 本体约 25 GB） | 用户以为卡死 | 全流程走既有 SSE `TaskBroadcaster` 推进度，与现有 update 流一致 |

---

## 7. 配置项新增

`{BaseDir}/config.yaml` 增加 `linux` 段（Windows 下整段被忽略）：

```yaml
linux:
  # 运行时来源：umu（默认，自动下载）| custom（用户自备 PROTONPATH）
  runtime: umu
  umu_version: "1.4.0"
  proton_version: "GE-Proton10-34"
  # prefix 模式：shared（默认，全实例共用一个）| per-instance（每实例独立，更隔离更占盘）
  prefix_mode: shared
  prefix_dir: ""            # 留空 = {BaseDir}/umu-prefix
  auto_download: true       # false 时缺运行时直接报错，不联网
  gameid: "umu-default"
```

沿用 `appconfig` 现有的 **flag > 环境变量 `ASA_*` > 文件 > 默认值** 优先级，无需新机制。

⚠️ `applyAppConfig()` 必须把这些值也推给 `runner` 的包级变量 —— **服务模式下 `app.Run()` 不执行**，
这是 `CLAUDE.md` 已经记录过的 Windows 坑，Linux systemd 模式同样成立。

---

## 8. 分阶段实施计划

| 阶段 | 内容 | 产出 / 验收 | 估算 |
|---|---|---|---|
| **P0 构建打通** | 加构建约束；`gui` 整包 windows-only；`main.go` 拆平台文件；`certmgr`/`mirror`/`tail`/`processjob` 拆平台文件（Linux 侧先写**返回「未实现」的存根**）；`frp`/`syncthing` 内嵌分平台 | `CGO_ENABLED=0 GOOS=linux go build` 通过；`GOOS=windows go build` 与 `go vet` 无回归 | 1–2 天 |
| **W 轨道（Windows，可并行）** | Wails 取代 Fyne + 安装程序 + 首次运行引导 —— 见 **§10**，步骤 W0–W9 | Fyne 依赖清空；双击安装 → 引导 → 服务注册运行 | 另计，见 §10.8 |
| **P1 进程原语** | `pkg/winproc` → `pkg/procx`；Linux `/proc` 实现（`QueryProcess`/`IsProcessExited`/`ProcessImageName` 等价物）；端口→PID 两平台统一切 gopsutil；`proctree` Linux 实现；`taskkill` → `procx.Terminate*` 全量替换 | Linux 上能查到任意进程；Windows 端口→PID 与旧 `netstat` 行为一致（对拍测试） | 2–3 天 |
| **P2 umu 运行时** | `internal/runner` 接口 + 两平台实现；umu zipapp / GE-Proton 下载与校验；prefix 预热与版本标记；`Preflight` 依赖自检；`GET /api/system/preflight` | 空机器上冷启动能自动装好运行时并通过自检 | 3–4 天 |
| **P3 安装与更新** | `installer` 分平台（steamcmd.sh、下载 URL）；ASA-on-Wine 三项修复；首次配置生成改轮询等待 | Linux 上 `update` 走完，`server-files` 完整，`Saved/Config/WindowsServer` 生成 | 2–3 天 |
| **P4 实例生命周期** | `StartServer` 走 `runner`；`GamePath` 转换；双 PID 语义；停止/强停/重启全链路；镜像 `IsElevated` 处理 | **单实例**启动→玩家可连入→RCON 可用→优雅停止；**双实例**并发启动互不干扰 | 3–5 天 |
| **P5 服务化与证书** | `winservice` → `svcmgr` + systemd（`HOME`/`LimitNOFILE`/非 root）；`certmgr` Linux 信任实现 | `service install/start/stop/remove` 全通；HTTPS 可用 | 2 天 |
| **P6 收尾** | 定时任务/批量/倒计时/备份/存档解析在 Linux 上回归；构建脚本与 CI 加 linux target；文档（部署指南、依赖清单、故障排查） | 测试矩阵（§9）全绿 | 2–3 天 |

**合计约 15–22 人日**，不含 Wine 侧疑难问题的排查缓冲（建议再留 30%）。

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

### 9.2 硬性验收判据

1. Linux 单实例冷启动出现 `minidumps folder is set to /tmp/dumps` 后跟正常 UE 日志输出，
   且游戏客户端能通过 Steam 服务器列表连入。
2. 停止后针对该实例的 `ArkAscendedServer.exe` / `bwrap` / `wineserver` 进程无残留。
3. 两个实例同时运行，各自的 `Saved/<SaveDir>` 与 `Config/WindowsServer` 互不污染，
   `Win64` 目录为各自独立的真实副本。
4. 配了 `ClusterID` 的两个实例，簇目录落在 `{BaseDir}/clusters/<ClusterId>/`（**不是** `clusters/clusters/`），
   且角色可在两实例间传输。
5. Windows 侧全部现有行为无回归 —— 特别是端口→PID 与停止流程这两处被改动的公共路径。

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
而 headless 的非管理员出口是**另一条路径**：`main.go:194` 目前已有的
`asa-server api --no-admin`（Linux 上则根本不存在提权这回事）。两者并行，互不覆盖。

> ✅ **已修**：`ensureAdminElevation()` 原先在提权失败时打印「将以非管理员模式继续运行」
> 却紧接着 `os.Exit(1)`，文案与行为相反。已改为 `return`，行为向文案看齐 ——
> 提权失败不再是致命错误，Web 界面、配置编辑、备份这些不需要管理员的功能照常可用。
> 这也正是 D8 所要求的：拿不到管理员权限，也不该挡住 `asa-server api` 起服。

> ⚠️ **但这条警告文案的后半句仍然不准确，且是另一个问题**：
> 「镜像启动将使用文件复制模式」只对**文件**成立 —— `mirror.createFileSymlink`（`mirror.go:495`）
> 失败时会回退到 `fsutil.CopyFile`；而**目录**走的 `createJunction`（`mirror.go:473`）失败后
> 直接返回错误，经 `processDirectory` → `filepath.Walk` 一路上抛，整个镜像创建失败并被回滚。
> 也就是说非管理员且未开 Windows 开发者模式时，**实例根本起不来，而不是「用复制模式起来」**。
>
> 根因是命名与实现不一致：`createJunction` 实际调用的是 `os.Symlink`，
> 在 Windows 上创建的是**目录符号链接**（需要 `SeCreateSymbolicLinkPrivilege`，即管理员或开发者模式），
> 而不是真正的 **NTFS junction**（`FSCTL_SET_REPARSE_POINT` / `mklink /J`，**普通用户即可创建**）。
> 若改用真 junction，整套提权逻辑在 Windows 上都不再必要。
> 这是个独立的改进项，与 Linux 兼容无关（Linux 上 symlink 本就免特权），**本方案不含此项**。
> 已单独立项：见 [`MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md`](./MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md) 第一部分
> —— 其中实测确认了真 junction 免管理员，也发现了一个必须同步处理的连带风险
> （Go 1.23 起 junction 不再报告为 `ModeSymlink`，`isJunctionOrSymlink` 会漏判）。
> 该项完成后能消掉 `mirror.IsElevated()`、`main.go` 的提权重启，以及安装器/引导对管理员的一半依赖。

---

## 11. 附录

### A. 命令 / 机制对照表

| 能力 | Windows | Linux |
|---|---|---|
| 执行 ARK exe | `exec.Command(exe, args...)` | `umu-run <exe> <args...>`（env: `WINEPREFIX`/`GAMEID`/`PROTONPATH`/`PROTON_VERB=run`/`UMU_RUNTIME_UPDATE=0`） |
| 脱离终端 | `SysProcAttr{HideWindow:true}` | `SysProcAttr{Setsid:true}` + `Stdin=nil` |
| 结束进程树 | `taskkill /T [/F] /PID` | `kill(-pgid, SIGTERM/SIGKILL)` |
| 端口→PID | `netstat -ano` | gopsutil `net.Connections("all")`（两平台统一） |
| 按名 + cmdline 查进程 | WMI `Win32_Process` | 扫 `/proc/*/cmdline` |
| 目录链接 | NTFS junction（`os.Symlink`，需管理员） | symlink（`os.Symlink`，无需特权） |
| 文件身份 | `Win32FileAttributeData.CreationTime` | `Stat_t.Ino` + `Dev` |
| 进程树托管 | Job Object `KILL_ON_JOB_CLOSE` | setsid 进程组（可选 `Pdeathsig`） |
| CA 信任 | `windows.Cert*` → Root 存储 | `/usr/local/share/ca-certificates` + `update-ca-certificates`<br>或 `/etc/pki/ca-trust/source/anchors` + `update-ca-trust` |
| 私钥权限 | `icacls` | `os.Chmod(0600)` |
| 提权 | `ShellExecute runas` | 不需要（symlink 免特权）；仅证书信任需 root |
| 服务 | SCM（kardianos） | systemd（kardianos） |
| SteamCMD | `steamcmd.exe`（zip） | `steamcmd.sh`（tar.gz，需 32 位 glibc） |
| 打开浏览器 | `rundll32 url.dll,FileProtocolHandler` | `xdg-open`（GUI 排除后基本用不到） |
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
