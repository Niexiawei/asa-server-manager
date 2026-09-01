# Linux 部署指南

> **状态**：Linux 支持是一项分阶段实施的兼容性工作，设计与实现细节见
> `docs/LINUX_COMPATIBILITY_PLAN.md`（当前 P0–P5 已实施）。**本指南描述的是设计好的
> 行为，尚未在真实 Linux 主机上做过端到端验证**——本机开发环境的 WSL Go 工具链已损坏，
> 所有 Linux 侧改动截至目前只做到 `CGO_ENABLED=0 GOOS=linux go build/vet` 通过与
> 单元测试通过（CI 引入后，`go test` 在 GitHub Actions 的 `ubuntu-latest` 上是真实
> Linux 执行，见 `.github/workflows/ci.yml`；但那仍不覆盖真机 Wine/Proton/systemd）。
> 按本指南部署前请先读一遍这条免责声明，遇到问题优先对照下面的故障排查表，
> 更深的原理解释在 `docs/LINUX_COMPATIBILITY_PLAN.md` 对应章节。

## 1. 依赖清单

Wine/Proton（经 [umu-launcher](https://github.com/Open-Wine-Components/umu-launcher)）需要以下宿主依赖，
启动前可用 `GET /api/system/preflight` 或直接看启动日志做自检（`internal/runner/preflight_linux.go`）：

| 依赖 | 用途 | 典型安装（Debian/Ubuntu） |
|---|---|---|
| 32 位 glibc | Wine 是 32/64 位混合二进制 | `apt install libc6-i386` |
| Python ≥ 3.10 | umu-launcher 本身是 Python 写的 | `apt install python3`（系统自带过低见下方「低版本系统的 Python」） |
| `libzstd.so.1` | Steam Linux Runtime 依赖 | `apt install libzstd1` |
| `tar` | 解压 SteamCMD/GE-Proton/umu 归档 | 通常预装 |
| AppArmor 允许非特权 user namespace | pressure-vessel 沙箱需要；Ubuntu 23.10+ 默认限制 | `sysctl kernel.apparmor_restrict_unprivileged_userns=0`（永久生效需写 `/etc/sysctl.d/`） |
| **`Xvfb`（只有用 ArkApi 才需要）** | 虚拟 X 显示。`AsaApiLoader.exe`（ArkApi）与微软 VC++ 安装器都会创建 Win32 窗口，Wine 下没有显示就直接失败，见下方「为什么无头服务器也要装 Xvfb」。不用 ArkApi 可以不装 | Debian/Ubuntu `apt install xvfb`  \|  Fedora/RHEL `dnf install xorg-x11-server-Xvfb`  \|  Arch `pacman -S xorg-server-xvfb` |
| **`acl`（强烈建议，非必需）** | 让 root 新建的文件自动可被降权的游戏进程写入，见下方「共享写权限」 | `apt install acl` |

### 为什么无头服务器也要装 Xvfb

ARK 服务端本身**不需要**显示 —— `ArkAscendedServer.exe` 在纯无头机上照常启动。
需要显示的是另外两个程序，成因相同：Wine 的 `winex11.drv` 连不上 X 服务时
`CreateWindow` 一律失败（`err:winediag:nodrv_CreateWindow ... The explorer process
failed to start.`），任何要开窗口的 Windows 程序都会在打出第一行日志之前就死掉。

| 程序 | 没有显示时的表现 |
|---|---|
| `AsaApiLoader.exe`（ArkApi） | **退出码 3，零输出** —— 不打日志、不建自己的 `Win64/logs/` 目录、也不拉起游戏进程。实测（WSL2 + GE-Proton10-34 + umu 1.4.4，2026-08-30）只补一个可用的 `DISPLAY`，同一条命令就能加载 ArkApi、下载 offsets cache、加载插件并拉起 `ArkAscendedServer.exe` |
| `vc_redist.x64.exe` | 退出码 203，什么都不装（见 `docs/ARKAPI_LINUX_VCREDIST_PLAN.md` §2.6） |

因此「能不能拿到一个 X 显示」是 preflight 的一项检查（`x11-display`），但它是
**建议级**，与 `acl` 同级：`asa-server setup` 会把它列出来然后照常装完。理由是
显示只对**启用了 ArkApi 的实例**是硬依赖，而 ArkApi 是每实例可选的 —— 普通实例
从头到尾不碰显示。真正需要它的地方会自己把关：启用 ArkApi 的实例启动时会被**直接
拒绝**并给出原因，`asa-server verify-arkapi` 的 `[3]` 也会明确报出来。

> 它一度是阻断级，结果一台永远用不到 ArkApi 的无头机连 `setup` 都跑不完。
> 2026-08-31 已改为建议级，见 `docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` §11。

asa-server 按下面的顺序取显示，**每一条都会真的连一次 X 服务验证**（不是看变量、
也不是看文件在不在）：

一句话记法：**点名的 > 自己管的 > 捡来的 > 扫出来的**。

| # | 用什么 | 前提 |
|---|---|---|
| 1 | `config.yaml` 的 `linux.display` **点名**的显示 | 非空、socket 在、且不需要 xauth cookie 就能握手 |
| 2 | **asa-server 自己拉起的 `Xvfb`**（默认走这条） | 装了 Xvfb **且 `/tmp/.X11-unix` 写得进去**（判据是 `access(2)`，不是权限位长什么样；以 root 运行时还会在起 Xvfb 前把这个目录按 X 的约定扶正到 `1777`，只读挂载时还会尝试重新挂载为可写，见下面的 WSL 一节） |
| 3 | `DISPLAY` 环境变量**捡来**的显示 | 同第 1 条 |
| 4 | 系统里已在运行的 X 服务 | 扫 `/tmp/.X11-unix/X<n>` 逐个握手，取第一个能连的 |

**第 2 条排在环境变量前面是有意的**：自管的那个 Xvfb 是这条链上唯一由 asa-server
启动、监控、随之退出的显示。它不依赖任何桌面会话（用户注销、桌面重启、WSLg 重启
都不会把游戏带走），也不会把游戏窗口弹到你的桌面上。而 `DISPLAY` 环境变量是**捡来**
的：从桌面终端启动、`su -` 继承、WSLg 自动导出都会带上它，谁都没表达过「请用这个显示」
的意思，它的生命周期也不归 asa-server 管。

第 3、4 条保留是为了不把任何一台机器变成跑不了：没装 `Xvfb`、或 `/tmp/.X11-unix`
死活写不了的机器，仍然能用现成的 X 服务跑起 ArkApi。**若第 2 条本该成立却失败了
（例如 Xvfb 缺字体），启动不会直接失败，而是回退到后面的候选并在日志与
`verify-arkapi` 的 `[3]` 里写明「已回退：……」** —— 回退永远是可见的。

反过来，**想用宿主现成的 X 服务**（调试时想亲眼看见游戏窗口，或本机 Xvfb 用不了），
就用 `linux.display: ":0"` 点名它：那是第 1 条，赢过一切。

> **判据是 `Xvfb`，不是 `xvfb-run`。** `xvfb-run` 是 Debian 打包时自带的一个 shell
> 脚本，Fedora / RHEL / Arch **不提供**它，只给 `Xvfb` 服务端本身。asa-server 因此
> 自己管 Xvfb 的起停（挑显示号、等它真的能握手、把它的输出落到
> `{运行时用户 HOME}/xvfb.log`），不依赖任何发行版脚本 ——
> 见 `docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md`。

自管的那个 Xvfb 是**每个 asa-server 进程一个**：多个 ArkApi 实例共用它，用之前会先
握一次手，中途死了看门狗会记一条带原因的日志并补起一个。

它的生命周期**跟着 asa-server 走** —— asa-server 退出，它一起退出。正常退出时显式停止；
被 `kill -9`／OOM 时由内核的 parent-death signal 收走（所以不会留下孤儿，也不会残留
`/tmp/.X11-unix/X<n>` 和 `/tmp/.X<n>-lock`）。这不会连累任何实例：启用了 ArkApi 的实例
本来就活不过 asa-server（它们挂在 asa-server 持有的 PTY 上，master 一关整条 umu/wine 链
就收到 SIGHUP），而不带 ArkApi 的普通实例压根不用显示。

万一两层都没生效，或者同机上还有另一个 asa-server 进程（比如服务在跑、你又敲了一条
`asa-server verify-arkapi`），下一次会通过 `{数据目录}/xvfb.state` 把现成的那个认回来，
而不是再起一个 —— 认来的不归它杀。

> **WSL / WSLg 注意**：WSLg 把 `/tmp/.X11-unix` 挂成**只读** tmpfs
> （`mount | grep X11` 可见 `ro,relatime`），而该路径写死在 X 的 xtrans 里、改不了 ——
> 所以在 WSL 上 `Xvfb` 建不出 socket，第 2 条本来是走不通的。
>
> asa-server 以 **root** 运行时会为此做一件事：发现这个目录是只读挂载（`access(2)`
> 返回 `EROFS`）就把它**重新挂载为可写**，然后照常拉起自管 Xvfb。这一步
> ①只改挂载点的读写属性，**不遮挡 WSLg 自己的 `:0`**（两个显示并存）；②会在日志里
> 写明；③asa-server 退出时还原为只读。不想让它碰宿主的挂载表就设
> `linux.allow_x11_remount: false`，或者干脆用 `linux.display: ":0"` 直接点名 WSLg 的显示。
>
> 重新挂载失败（非 root、内核拒绝、被 LSM 策略拦下）时**不会导致启动失败**：
> 候选链会落到第 3/4 条，用 WSLg 的 `:0`，与此前的行为完全一致，日志里会写明原因。
>
> 重新挂载这一步**已在 WSL2 上实测成功**（2026-09-01：
> `mount -o remount,rw /tmp/.X11-unix && touch /tmp/.X11-unix/probe` → OK）。
> ⚠️ 但**整条链路还没验完**：目录可写之后 Xvfb 的 socket 能不能被 pressure-vessel
> 带进容器、ArkApi 能不能真的加载，要跑一次实例启动才知道。届时若 `launcher.log` 里
> 仍有 `X11 socket ... does not exist in filesystem`，那是容器那一侧的问题，不是这一步。
> 见 `docs/ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md` §4.5。

**不会**把 `XAUTHORITY` 传给游戏进程：它常指向 `/run/user/0` 下的路径，而
pressure-vessel 会去 bind 环境变量点名的每个路径，降权后那次 bind 会让整个容器起不来。
自管的 Xvfb 因此也**不带 `-auth`**（无认证 + `-nolisten tcp`，只经本机 unix socket
暴露）—— 「不需要 cookie 就能握手」正是上面四条路共用的那个判据。

### 共享写权限与 `acl`

以 root 运行时，游戏进程会被降到专用账号 `asa-umu-runtime`
（`docs/UMU_RUNTIME_USER_PLAN.md`），而 SteamCMD、配置写入、你用 SFTP 上传的
ArkApi 插件全都是 root 身份产生的。两边要写同一批目录，asa-server 因此对
`server-files` 与 `instances` 施加「组 + setgid + POSIX 默认 ACL」：
默认 ACL 让**任何人**新建的文件在创建瞬间就带上组可写，无需事后修补。

`setfacl` 不可用（没装 `acl`，或文件系统挂载时未启用 ACL）时会自动降级成
「整棵 chown 给运行时用户」。**降级后一切照常工作**，`asa-server setup` 也不会
因此中止，只是少了增量保护：之后以 root 新建的文件游戏写不了，要等下一次
重启 asa-server / `asa-server update` / `asa-server perms fix` 才会被接管。

排查与修复：

```bash
asa-server perms status   # 只读：运行时用户、ACL 可用性、各目录树当前状态
asa-server perms fix      # 以 root 传过 mod / 插件后手动重新施加
```

程序自己创建的目录（实例的 Config/Logs/Save、共享的 Mods/ModsUserData）
在每次实例启动时自动处理，不需要跑上面的命令 ——
`perms fix` 只为带外变更准备。详见 `docs/ACL_PERMISSION_HARDENING_PLAN.md`。

此外部署前建议检查 `vm.max_map_count`（部分发行版默认值偏低会让 UE 内存分配失败）：

```bash
sysctl vm.max_map_count
# 太低（远小于 262144）时：
sysctl -w vm.max_map_count=262144
```

### 低版本系统的 Python（RHEL 8 / Ubuntu 20.04 / Debian 11 等）

这些发行版自带的 `python3` 低于 3.10，且**不应该替换**（系统组件绑死在它上面）。做法是**并行安装**
一个带版本号的解释器，asa-server 会自动扫描 `python3` / `python3.10` … `python3.20` 并选最高版本：

| 发行版系 | 命令 |
|---|---|
| Debian/Ubuntu | `add-apt-repository ppa:deadsnakes/ppa && apt install python3.12` |
| RHEL/Alma/Rocky | `dnf install python3.12` |
| Arch | `pacman -S python`（已是最新） |

也可以在 `config.yaml` 的 `linux.umu_python_bin` 里**显式指定**一个解释器，指定后就不再自动探测：

```yaml
linux:
  umu_python_bin: "python3.14"                          # 裸名字，走 PATH
  # umu_python_bin: "/usr/bin/python3.14"               # 绝对路径
  # umu_python_bin: "/opt/asa-venv/bin/python"          # venv
  # umu_python_bin: "~/.pyenv/versions/3.14.0/bin/python"  # pyenv（用真实路径，别用 shims/）
```

> **降权运行注意**：以 root 运行、游戏进程降权到 `asa-umu-runtime` 时，选定的解释器必须能被那个非 root
> 用户读取/执行。把 venv/pyenv 放到某个用户的 HOME 下（如 `/root/.pyenv`）会导致启动失败——放到
> `/opt` 或系统路径。`GET /api/system/preflight` 的 `umuPython` 字段会显示最终解析到的解释器路径与版本。

系统信任存储写入（`cert install`）需要以下二选一，两者都没有会导致该命令报错（不影响 HTTPS 本身，只影响
浏览器是否报证书警告）：

| 发行版系 | 命令 |
|---|---|
| Debian/Ubuntu | `update-ca-certificates` |
| RHEL/Fedora/openSUSE | `update-ca-trust` |

## 2. 安装

目前没有打包好的 Linux 发行版二进制，需要自行交叉编译或在 Linux 主机上原生编译：

```bash
# 前端先构建一次；app/dist 是 //go:embed 的目标，且被 .gitignore 排除，必须先手动构建
cd app && npm install && npm run build && cd ..

# 交叉编译（在 Windows/Linux 开发机上均可）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o asa-server .

# 或直接在目标 Linux 主机上原生编译
go build -o asa-server .
```

`CGO_ENABLED=0` 之所以可行：排除 Fyne GUI 后（Linux 上没有 GUI，见
`docs/LINUX_COMPATIBILITY_PLAN.md` §5.9），其余依赖（modernc/sqlite、BadgerDB、
gopsutil、creack/pty）全是纯 Go，静态二进制交叉编译无痛。

首次运行会在 `{BaseDir}` 下生成 `config.yaml`（默认路径见 `internal/config`），也可以先跑一次
`asa-server api` 让它自动创建，再手动改。**Linux 上无参数直接运行 `asa-server` 等价于
`asa-server api`**（没有 GUI 可退回）。

## 3. 首次配置要点（`config.yaml`）

```yaml
server:
  tls:
    trust_local_ca: false   # Linux 默认值——系统信任库不影响 Firefox/Chrome 的 NSS 证书库，
                             # 装了也还是红锁；需要免警告时手动执行 `asa-server cert install`（需 root）
                             # 或把 CA 手动导入浏览器

linux:
  runtime: umu               # umu（默认，自动下载 umu-launcher + GE-Proton）| custom（自备 PROTONPATH）
  umu_version: "1.4.4"
  proton_version: "GE-Proton10-34"   # 硬钉版本：GE-Proton 11.x 已知会挂死 ASA，升级前必须先验证
  prefix_mode: shared         # shared（默认，共用一个 Wine prefix，启动自动串行）| per-instance（每实例独立，可并发启动，更占盘）
  auto_download: true         # false 时不联网，运行时缺失直接在 preflight 里报出来，不静默重试
```

首次启动会自动下载 umu-launcher + GE-Proton（约数百 MB）并预热 Wine prefix，走既有的 SSE
进度推送，和「更新服务器」是同一套体验，不是卡死。

## 4. 装成系统服务（systemd）

```bash
sudo ./asa-server service install   # 生成并启用 systemd unit，见下方说明
sudo ./asa-server service start
./asa-server service stop
sudo ./asa-server service remove    # 同时联动清理已安装的本地 CA（若曾执行过 cert install）
```

`service install` 会：

- 把当前安装用户的 `$HOME`（`sudo` 默认会保留 root 的 `/root`）直接写进 unit 的
  `Environment=`——systemd 系统服务默认 `HOME` 为空或 `/`，这个坑不解决的话 umu 会
  每次启动都重新下载运行时，或者直接崩在 steamclient
- 加 `LimitNOFILE=1048576`（ARK + Wine 打开的文件描述符数量很大）、`Restart=on-failure`、
  `After=network-online.target`
- **`asa-server` 服务进程本身仍以 root 运行**（写系统信任库、操作 systemd 都需要 root）。
- **但每个游戏实例的 umu/wine 进程树会自动降权到专用非 root 用户 `asa-umu-runtime`**——
  见下一节。

### 4.1 游戏实例以专用非 root 用户运行

`asa-server` 以 root 运行时，启动任何实例都会把 `umu-run` → `bwrap` → `wine` →
`ArkAscendedServer.exe` 整棵进程树降权到专用系统用户 `asa-umu-runtime`
（`ps -o user=` 看不到 root）。这个用户由程序**自动创建与维护**：

- `asa-server setup` / `service install` / 每次服务启动时，若 `asa-umu-runtime` 不存在就
  `useradd -r -m` 创建（家目录 `{BaseDir}/runtime-home`），并把它要读写的运行时子树
  （`umu-prefix*`、`runtime-home`、`clusters`、实例镜像目录）`chown` 给它。
- **降权环境准备失败时，`asa-server` 会拒绝启动**（退出码 `78`；systemd 下服务直接进
  `failed` 不重启）。这是有意的——不会默默把公网游戏进程跑成 root。
- 排障 / 特殊环境的逃生舱：`config.yaml` 里
  ```yaml
  linux:
    umu_run_as_root: true      # 明确接受以 root 运行游戏进程，跳过降权与全部自检
    # 或者，指向一个已存在的非 root 账号 / 固定 uid：
    umu_runtime_user: "someuser"
    umu_runtime_uid: 0          # 非 0 时固定数值 uid（BaseDir 跨机迁移时保持属主稳定）
  ```
- **迁移注意**：把 `{BaseDir}` 整体搬到另一台机器前，先 `id asa-umu-runtime` 记下 uid/gid；
  新机器上如果 `useradd -r` 分到不同的 uid，存档等目录的属主会对不上——用 `umu_runtime_uid`
  在两边固定成同一个数值可避免。
- **卸载**：`service remove` 不会 `userdel asa-umu-runtime`（它下面可能还有存档数据）。
  确定不再需要时手动 `sudo userdel asa-umu-runtime`。

详细设计见 `docs/UMU_RUNTIME_USER_PLAN.md`。`RestartSec` 沿用 kardianos 内置的 120s
（`docs/LINUX_COMPATIBILITY_PLAN.md` §5.8）。

## 5. 故障排查

| 现象 | 可能原因 | 处置 |
|---|---|---|
| 启动即报 `bwrap: Permission denied` | AppArmor 限制了非特权 user namespace（Ubuntu 23.10+ 默认） | `sysctl kernel.apparmor_restrict_unprivileged_userns=0`，永久生效写 `/etc/sysctl.d/`；`GET /api/system/preflight` 会直接报出这条 |
| 服务器完全起不来，日志戛然而止，无报错 | GE-Proton 版本不是 `GE-Proton10-34`（11.x 系列已知挂死 ASA） | 检查 `config.yaml` 的 `linux.proton_version`，不要手动升级到 11.x，除非先自行验证过 |
| 每次启动都重新下载 umu/GE-Proton，或直接崩在 steamclient | systemd 服务的 `HOME` 未正确设置 | 确认走的是 `asa-server service install`（会显式写 `Environment=HOME=...`），而不是手写的、没设 `HOME` 的 unit 文件 |
| 首次 `setup` 卡在 Steam Linux Runtime 下载 / 超时失败 | 到 `repo.steampowered.com` 的网络不稳 | 默认已由本程序用自己的下载器预取（有重试、断点续传、走 `download.http_proxy`），日志里应出现 `正在预下载 Steam Linux Runtime`。若预取本身也失败，日志会打「改由 umu 自行下载」——此时 umu 那条路只认**环境变量**，给 systemd unit 加 `Environment=HTTPS_PROXY=http://…` 后重试。排障可用 `linux.steamrt_prefetch: false` 关掉预取。见 `docs/STEAMRT_PREFETCH_PLAN.md` |
| 启用了 ArkApi 的实例起不来，日志停在 `fsync: up and running.` 之后一个字都没有 | **没有可用的图形显示**（最常见）。`AsaApiLoader.exe` 会创建 Win32 窗口，Wine 连不上 X 就以退出码 3 静默退出 | 装 Xvfb（`apt install xvfb` / `dnf install xorg-x11-server-Xvfb` / `pacman -S xorg-server-xvfb`）。装好后 asa-server 会自己拉起一个 Xvfb 给加载器用；没装时实例启动会被**直接拒绝**并给出这条提示，而不是假装启动成功。见 `docs/ARKAPI_LINUX_VCREDIST_PLAN.md` §9 与 `docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` |
| 日志里有 `System.PlatformNotSupportedException: Video driver  not supported` + `Xalia.Sdl.WindowingSystem.Create` 的栈 | **不是故障。** Xalia 是 GE-Proton 附带的无障碍/手柄 UI 覆盖层，与被启动的程序并行的另一个进程；没有 DISPLAY 时它初始化不出窗口系统就自己退出（注意 `driver` 与 `not` 之间是两个空格——驱动名是空的，即这次运行没有显示）。普通实例本来就不需要显示 | 已消音：三处 umu/Proton 命令行都加了 `PROTON_USE_XALIA=0`，升级后不再出现。判断某一步成没成功要看它自己的结论（如 `verify-arkapi` 的 `[4] DLL override: 11/11`），不是看有没有这段栈。见 `docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` §12 |
| `Xvfb` 明明装了（`which Xvfb` 有），`setup` / `verify-arkapi` 却说「本机没有可用的 X 显示」 | 已修复。`/tmp/.X11-unix` 的权限被判成不可写。旧代码要求这个目录有 `o+w`，而目录常常是**上一轮那个降权 Xvfb 自己建的**——非 root 的 X 服务端建不出 `1777`，落到 umask 022 就是 `0755`、属主是运行时用户；那个用户明明是属主写得进去，`o+w` 这条判据却判它不行。于是**第一次成功启动把后续每一次都毒死了** | 升级到含本修复的版本：判据改为 `access(2)` 的实际写入能力，且以 root 运行时会在起 Xvfb 前把 `/tmp/.X11-unix` 按 X 的约定扶正到 `1777`。旧版可手动 `chmod 1777 /tmp/.X11-unix` 绕过。`asa-server verify-arkapi --check-only` 的 `[3]` 现在会说清是「没装 Xvfb」还是「目录不可写（附实际权限）」 |
| 装了 Xvfb，实例仍起不来；日志里有 `W: X11 socket /tmp/.X11-unix/X100 does not exist in filesystem, trying to use abstract socket instead` 和 `PlatformNotSupportedException: Video driver not supported` | `/tmp/.X11-unix` 是**只读挂载**（WSLg 就是这么挂的），`Xvfb` 建不出 socket，pressure-vessel 没法把显示带进容器。（当年经 `xvfb-run` 走这条路时它在 Xvfb 起不来后**照样会执行命令**，所以退出码看不出问题）| 升级到含本修复的版本：asa-server 会先判断 `/tmp/.X11-unix` 可不可写，不可写就自动改用系统里已在运行的 X 服务（WSL 上就是 WSLg 的 `:0`）。`asa-server verify-arkapi --check-only` 的 `[3]` 会直接说明这次用的是哪一种。现在 Xvfb 由 asa-server 自己管：起不来会**当场让启动失败**并附上 `{运行时用户 HOME}/xvfb.log` 的末尾输出，不再丢 `/dev/null`、也不再带着一个坏显示往下跑 |
| 启用了 ArkApi 的实例明明起来了（游戏窗口/端口都在），30 秒后却被标记为停止 | 已修复。旧版按 `\ArkAscendedServer.exe` 找游戏进程，而 ArkApi 下游戏进程的命令行里写的是 `\AsaApiLoader.exe`，永远找不到 → 必然超时，且失败后不收拾进程树，游戏被留成孤儿 | 升级到含本修复的版本。判据改为在候选里按 `/proc/<pid>/comm == "GameThread"` 挑；ArkApi 的等待上限也从 30 秒放宽到 3 分钟（加载器要先下载 offsets cache），同时启动链一退出就立即失败而不是干等。见 `docs/ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md` §2 |
| 「插件日志」面板里全是 `INFO: umu-launcher version …` 这类内容，看不到 ArkApi 的输出 | 已修复。Linux 上 PTY 里跑的是 umu-run 整条包装链而不是加载器本体，而 ArkApi **不往控制台写**业务日志，只写文件 | 升级后 `instances/<name>/arkAsaApi.log` 由 asa-server 从镜像里的 `ShooterGame/Binaries/Win64/logs/ArkApi_*.log` 转抄，面板内容即 ArkApi 的真日志；启动链本身的输出移到同目录的 `launcher.log`（排查「加载器起不来」时看它） |
| 启用了 ArkApi 的实例起不来（显示已就绪） | ArkApi 官方要求 Microsoft VC++ Redistributable，而 Wine/GE-Proton 的 prefix 默认优先用自己的内建实现 | 跑 **`asa-server verify-arkapi`**：它会把前置条件逐条列出来（ArkApi 装没装、Wine 运行时、图形显示、VC++ DLL 的实际出处），再真拉起一次。关键项是 **DLL override 11/11**，`setup` 会自动写入。仍失败见 `docs/ARKAPI_LINUX_VCREDIST_PLAN.md` §6 与附录 B 的排查顺序 |
| `verify-arkapi` 说「system32 里的 vcruntime140.dll 仍是 Wine 自带的」 | 装的时候没有 X 显示：微软的安装器在 Wine 下**必须有一个能连上的显示**，否则一律以 203 退出（实测，连 `/layout` 都不行） | 与上一行同一个原因、同一个修法：装好 Xvfb 后跑 `asa-server verify-arkapi --install-vcredist`（或重跑 `asa-server setup`）。单看这一项其实**通常不影响 ArkApi** —— ARK 自己在 exe 同目录带了 11 个运行时 DLL 里的 9 个原生版，应用目录的搜索优先级高于 system32，配合已写入的 override 一般够用 |
| `asa-server cert install` 报错「需要 root 权限」 | 系统信任存储需要 root 才能写 | `sudo ./asa-server cert install`；Linux 上没有 Windows 的 UAC 自动提权 |
| `cert install` 成功但浏览器仍报证书警告 | Linux 系统信任库不影响 Firefox/Chrome 的 NSS 证书库 | 需要额外手动把 CA（`{BaseDir}/certs/ca.crt`）导入浏览器自己的证书管理界面 |
| UE 报内存分配失败 / mmap 相关崩溃 | `vm.max_map_count` 太低 | `sysctl -w vm.max_map_count=262144` |
| 第二个实例启动后一直不出现游戏进程，3 分钟后报「游戏进程在 3m0s 内没有出现」，`ps` 里能看到一个 `wineserver -w` 挂着 | **旧版本的缺陷**：没有设置 `PROTON_VERB=run`，umu 默认的 `waitforexitandrun` 会先等同 prefix 的上一个游戏退出——共享 prefix 下第二个实例因此永远排队 | 升级到已修复的版本即可（见 `docs/UMU_PREFIX_PER_INSTANCE_PLAN.md`）。修复后共享模式下多实例可以同时在线，只是**启动过程**仍按顺序进行 |
| 共享模式下点了启动没立刻动，日志说「正在等待实例 X 初始化完成后再启动」 | 这是**预期行为**：共享 prefix 只有一个 wineserver，启动阶段必须串行 | 等上一台到达 `start_initialization_successful` 会自动放行；不想等就把 `linux.prefix_mode` 改成 `per-instance`（每实例独立 prefix，可并发启动） |
| 启动第二个 ArkApi 实例时报「同时只能有一个 ArkApi 实例」 | **共享 prefix = 共享 Wine 会话**，第二个 `AsaApiLoader.exe` 会卡在启动加载器之前直到超时（2026-08-31 实测；2026-09-01 在全实例同一显示下复测三轮、两次对调先后顺序，结论不变。具体机制尚未定位） | 把 `linux.prefix_mode` 改成 `per-instance`——这是**同时用 ArkApi 跑多实例的唯一办法**。不用 ArkApi 的实例不受影响，共享模式下可以照常多开 |
| 多实例共享 prefix 时偶发互相影响（注册表、崩溃波及） | 同一个 prefix 意味着同一个 wineserver，实例之间在这一层无法隔离 | 把 `linux.prefix_mode` 改成 `per-instance`。每实例首次启动会多花约一分钟创建自己的 prefix，之后正常；占盘用 `asa-server prefix status` 查看 |
| 切回 `shared` 后盘上还留着一堆 `umu-prefix-<实例名>` | per-instance 时期创建的目录不会自动清理 | `asa-server prefix gc` 预演，确认后 `asa-server prefix gc --apply` |
| ArkApi 插件的数据（如 Permissions 库）没有按实例隔离，感觉「每次重启被重置」 | `pluginsRelPath` 硬编码大小写 `ShooterGame/Binaries/Win64/ArkApi/Plugins`，若磁盘上实际大小写不同会静默失效 | 看启动日志里有没有「检测到 ArkApi 插件目录大小写与预期不符」的告警（P6 新增的诊断）；有的话按日志里给出的实际路径重命名，或反馈上游调整 |
| 想确认 Wine/依赖是否齐全，不想等启动失败才知道 | — | `GET /api/system/preflight` 直接列出所有缺失项，无需翻日志 |
| `asa-server` 启动即以退出码 `78` 退出 / systemd 服务停在 `failed` 且不重启 | 降权运行时用户 `asa-umu-runtime` 建不出来或对相关目录没权限（`useradd` 缺失、SELinux、只读挂载、NFS root_squash） | 看日志里 `[umu-runtime-*]` 开头的错误按提示修；或 `config.yaml` 设 `linux.umu_run_as_root: true` 明确以 root 运行游戏。见 4.1 |
| 游戏进程仍以 root 运行（`ps -o user`） | `linux.umu_run_as_root: true` 已设，或 `asa-server` 本身不是 root 启动 | 按需求取舍：非 root 启动 `asa-server` 时子进程本就以当前用户跑，无需降权 |
| 存档 / prefix 目录属主是 root，实例起不来报 `umu-runtime-owner-drift` | 手工动过 `{BaseDir}` 属主，或跨机迁移后 uid 变了 | 重启 `asa-server` 会自动 `chown` 修复；修不回来看是不是只读挂载 / SELinux。迁移场景用 `umu_runtime_uid` 固定 uid |

更完整的风险清单（含已知不会在 Linux 上发生的坑，供交叉核对）见
`docs/LINUX_COMPATIBILITY_PLAN.md` §6。
