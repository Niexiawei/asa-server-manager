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
  prefix_mode: shared         # shared（默认，全部实例共用一个 Wine prefix）| per-instance（更隔离更占盘）
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
| `asa-server cert install` 报错「需要 root 权限」 | 系统信任存储需要 root 才能写 | `sudo ./asa-server cert install`；Linux 上没有 Windows 的 UAC 自动提权 |
| `cert install` 成功但浏览器仍报证书警告 | Linux 系统信任库不影响 Firefox/Chrome 的 NSS 证书库 | 需要额外手动把 CA（`{BaseDir}/certs/ca.crt`）导入浏览器自己的证书管理界面 |
| UE 报内存分配失败 / mmap 相关崩溃 | `vm.max_map_count` 太低 | `sysctl -w vm.max_map_count=262144` |
| 多实例共享 prefix 时偶发互相影响 | 共享 Wine prefix 下并发首次初始化竞争 | 已有互斥锁串行化首次初始化；持续出现可将 `linux.prefix_mode` 改成 `per-instance` 换取更强隔离（更占盘） |
| ArkApi 插件的数据（如 Permissions 库）没有按实例隔离，感觉「每次重启被重置」 | `pluginsRelPath` 硬编码大小写 `ShooterGame/Binaries/Win64/ArkApi/Plugins`，若磁盘上实际大小写不同会静默失效 | 看启动日志里有没有「检测到 ArkApi 插件目录大小写与预期不符」的告警（P6 新增的诊断）；有的话按日志里给出的实际路径重命名，或反馈上游调整 |
| 想确认 Wine/依赖是否齐全，不想等启动失败才知道 | — | `GET /api/system/preflight` 直接列出所有缺失项，无需翻日志 |
| `asa-server` 启动即以退出码 `78` 退出 / systemd 服务停在 `failed` 且不重启 | 降权运行时用户 `asa-umu-runtime` 建不出来或对相关目录没权限（`useradd` 缺失、SELinux、只读挂载、NFS root_squash） | 看日志里 `[umu-runtime-*]` 开头的错误按提示修；或 `config.yaml` 设 `linux.umu_run_as_root: true` 明确以 root 运行游戏。见 4.1 |
| 游戏进程仍以 root 运行（`ps -o user`） | `linux.umu_run_as_root: true` 已设，或 `asa-server` 本身不是 root 启动 | 按需求取舍：非 root 启动 `asa-server` 时子进程本就以当前用户跑，无需降权 |
| 存档 / prefix 目录属主是 root，实例起不来报 `umu-runtime-owner-drift` | 手工动过 `{BaseDir}` 属主，或跨机迁移后 uid 变了 | 重启 `asa-server` 会自动 `chown` 修复；修不回来看是不是只读挂载 / SELinux。迁移场景用 `umu_runtime_uid` 固定 uid |

更完整的风险清单（含已知不会在 Linux 上发生的坑，供交叉核对）见
`docs/LINUX_COMPATIBILITY_PLAN.md` §6。
