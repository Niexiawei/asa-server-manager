# 更新日志

本文件记录 ASA Server Manager 每个版本面向使用者的变化。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。
设计与取舍的细节在 `docs/` 下对应的计划文档里，这里只写结论；条目后括注的文档名即出处。

## [Unreleased]

`v0.0.1`（2026-06-22）之后的全部变化，按领域归类。

### 升级须知

- **Linux 正式成为支持平台**（仍以 Windows 为主平台）。Linux 上没有 GUI，无参数启动即运行 API 服务。
  首次使用先跑 `asa-server setup`，它会检查宿主依赖、下载 Wine/Proton 运行时并准备游戏服务端（`docs/LINUX_COMPATIBILITY_PLAN.md`）。
- **新增应用配置文件 `{BaseDir}/config.yaml`**（端口、TLS、鉴权、下载代理、Linux 运行时等），首次运行自动生成带注释的模板。
  优先级：命令行参数 > 环境变量 `ASA_*` > 配置文件 > 默认值。Windows 服务模式下同样生效。
- **Web 界面默认改为 HTTPS + HTTP/2**：本程序在 `{BaseDir}/certs/` 自建本地 CA 并签发证书（`asa-server cert status|install|uninstall`），
  浏览器首次访问需要信任该 CA；`--tls=false` 可退回明文 HTTP。改用 HTTP/2 后不再受浏览器"同一站点最多 6 条连接"的限制。
- **登录鉴权**：`auth.enabled` 默认关闭，行为与之前一致。开启后是**密码 + TOTP 两步验证 + 恢复码**；
  早期曾提供的 WebAuthn / Passkey **已移除**（2026-08-25），用过它的账号需改用密码 + TOTP 登录。
  `auth.lan_bypass`（内网免登录）默认关闭，放在反向代理后面时尤其不要打开（`docs/AUTH_LOGIN_DESIGN.md` §4）。
- **不再以管理员身份运行**：实例镜像改用真正的 NTFS junction，普通权限即可创建，程序不再自动提权。
- **FRP 配置自动迁移**：旧的 `{BaseDir}/frp/frpc.toml` 首次启动时一次性转成结构化的 `frpc.json`，原文件改名为 `frpc.toml.migrated` 保留。
  无法用"端口映射规则"表达的代理会被丢弃并在日志里列出，迁移后请在「端口映射」页核对一遍（`docs/FRP_FORM_CONFIG_PLAN.md`）。
- **不再内嵌 frpc / syncthing 可执行文件**：frpc 改为进程内运行；Syncthing 首次使用时按固定版本从 GitHub 下载，国内网络可配置 `download.github_proxy`。
- **ArkApi 插件目录改为每实例独立**：`instances/<实例>/ArkApi/{Plugins,PluginsDisabled,...}`，旧布局在实例下次启动前自动迁移一次
  （以标记文件 `ArkApi/.plugin-layout` 判断），无需手工操作（`docs/ARKAPI_PLUGIN_INSTALL_PLAN.md`）。
- **移除实例的 QueryPort 配置项**（2026-07-20）。
- **从源码构建**：新增私有依赖 `github.com/Niexiawei/simple-file-sync`，需要 `go env -w GOPRIVATE=github.com/Niexiawei/*`，
  并让 git 用 SSH 拉取 GitHub（`url.git@github.com:.insteadOf https://github.com/`）。

### 新增

#### 集群同步（simple-file-sync，逐步替换 Syncthing）

- 新页面「集群同步」：把 `{BaseDir}/clusters/<ClusterID>` 同步到同一集群的其他机器，用于跨机器的集群角色传输。
  需要自建协调端（见同步库 `docs/coordinator-linux-deployment.md`）。与原「文件同步」（Syncthing）页并存。
- 接入方式二选一、页面上切换：**粘贴一行接入字符串**（协调端 `coordinator join-blob` 生成，保存前可预览地址与 CA 指纹），
  或**上传 `ca.crt` / `client.crt` / `client.key` 三个文件**。
- 集群从各实例配置里的 ClusterID 下拉选择；可设上下行限速；显示本机接入状态、节点证书剩余天数，支持重置本机身份。
- 状态面板：连接诊断、各集群待处理 / 传输中 / 失败数与在途进度、最近告警（证书临期、鉴权失败、身份冲突、冲突副本等），
  以及只显示 `[filesync]` 的日志。
- 同步参数按集群传输的特点固定（1 秒静默期、5 分钟兜底全量扫描），排除规则全组一致不开放配置；
  只改集群列表时在线增删，不中断其他集群的传输。配置里含引导私钥，读取接口不回传任何证书内容，写操作要求管理员
  （`docs/FILESYNC_REPLACE_SYNCTHING_PLAN.md`）。

#### Linux 支持

- 跨平台启动器：Linux 上经 umu-launcher + GE-Proton 在 Wine 中运行 `ArkAscendedServer.exe` / `AsaApiLoader.exe`，
  运行时按需下载并校验，Steam Linux Runtime 预取（续传）。
- Wine 前缀三种隔离模式（`linux.prefix_mode`）：`shared`（默认，省盘，启动自动排队）、`per-instance`（可并发启动）、
  `overlay`（共用只读底层 + 每实例可写层）。`asa-server prefix status|gc` 管理前缀。
- 降权运行：游戏以专用运行时用户运行，自动创建账户、对账目录属主；`server-files` / `instances` 用组 + setgid + 默认 ACL 共享写权限，
  缺 `setfacl` 时降级。`asa-server perms status|fix` 诊断与修复。
- 虚拟显示：ArkApi 加载器需要 X 显示时自动启动自管 Xvfb，跨发行版可用；并为 ArkApi 在 Wine 前缀里安装 VC++ 运行时。
- 首次引导 `asa-server setup`、环境就绪检查（阻断项 / 建议项分级），`asa-server verify`（单独重跑启动验证，以端口监听为判据）、
  `asa-server verify-arkapi`（ArkApi 链路诊断）。
- systemd 服务：`asa-server service install` 生成加固过的单元（文件句柄上限、失败重启、配置错误时不反复重启）；
  本地 CA 可写入系统信任存储（ca-certificates / ca-trust）。
- GitHub Actions 双平台构建矩阵。

#### 鉴权与安全

- 登录、用户管理（管理员 / 普通用户）、审计日志；会话走 HttpOnly Cookie，令牌可按设备或全设备吊销，登录限流。
- CLI：`asa-server user list|add|passwd|unlock|totp-reset|audit`、`asa-server db status|migrate|verify|backup|vacuum`（`docs/AUTH_LOGIN_DESIGN.md`）。

#### ArkApi

- 主程序与插件的 **zip 上传安装 / 更新 / 卸载**：两段式（上传校验 → 确认应用），插件只装到显式勾选的实例；
  覆盖游戏自带文件前备份原件，卸载时还原；安装期间与 Steam 更新互斥，逐文件失败回滚。
- 插件面板按实例展示布局与插件元数据，可启用 / 禁用、编辑插件配置。
- **offsets 缓存预取**：在 ArkApi 启动前用本程序的下载器（支持代理与续传）把缓存备好，解决慢速网络下 ArkApi 自己下载超时的问题；
  失败时退回由 ArkApi 自己下载，不新增失败点。CLI：`asa-server arkapi-cache status|fetch|gc`（`docs/ARKAPI_CACHE_PREFETCH_PLAN.md`）。
- Linux 上 ArkApi 日志转抄进统一的日志文件；共享前缀下多个 ArkApi 实例的冲突在启动前就阻断并提示。

#### 运维与监控

- **定时任务**：按"每 N 小时 / 每 N 天 / 每天定点"执行**服务端更新**或**批量重启**（`schedules.json`，可手改），带执行日志；
  定时更新会先停掉全部实例，更新完把原先在运行的实例恢复运行。
- **延迟停止 / 重启**：倒计时 + 游戏内公告，可取消；批量操作支持多实例对齐倒计时与跳过。
- **资源监控**：进程内采样器（2 秒一次），30 分钟历史并分块落盘，不打开页面也持续记录；整机与各实例的 CPU、内存、磁盘、网络速率趋势图。
- **按进程网络流量**：Linux 用 eBPF、Windows 用 ETW 统计每个实例的收发字节（需要相应权限，不满足时该项为空）。诊断：`asa-server netmon ebpf|etw`。
- **端口映射（FRP）表单化**：连接配置 + 端口映射规则表，逐条代理显示运行状态；只改规则时热更新，不断开已建立的隧道。
- 更新服务端后，原先在运行的实例自动恢复运行；启动失败的原因以弹窗提示，不再只写日志。
- 内置 pprof 调试服务器。

#### 界面

- 实例详情页改为分页签的常驻编辑区；配置编辑器支持弹窗模式；资源卡片改为迷你趋势图并加入进程 I/O 速率。
- 游戏配置编辑补充生物、物品、印痕数据与下拉选择，新增翻译数据与图标资源；日志查看改用虚拟列表。

### 变更

- 代码结构整体重构：领域代码收进 `internal/`（`config`、`instance`、`process`、`state`、`mirror`、`installer`、`runner` 等），
  可复用的基础设施下沉到根目录 `pkg/`；Web API 按领域拆成子包（`docs/PACKAGE_RESTRUCTURE_PLAN.md`、`docs/INTERNAL_LAYOUT_MIGRATION.md`）。
- 日志统一为 `pkg/logger` 包级函数。
- 目录布局：`BaseDir` 按三级顺序查找，`config.yaml` 里的 `basedir` 为准；Windows 首次启动有数据目录向导。
- 依赖升级：Fyne 2.8.1、gopsutil 4.26.8、modernc.org/sqlite 1.58、cilium/ebpf 0.22 等。

### 修复

- 实例配置的部分更新不再清空服务器密码与 Mod 列表。
- 删除 / 重命名实例时一并清理镜像目录与独立的 Wine 前缀，不再留下几百 MB 的死拷贝。
- Linux：进程树终止改为遍历父子进程（进程组信号对 Wine 链路无效）；启动验证以端口真正监听为判据，失败时输出日志尾部。
- 整机磁盘吞吐在 LVM / LUKS / md 上不再虚高，在 WSL2 上不再恒为 0。
- WebSocket 偶发 panic、SSE 地址双斜杠、PTY 控制台 ArkApi 日志乱码与控制序列残留、实例状态并发更新（CAS）等问题。

## [0.0.1] - 2026-06-22

第一个打标签的版本：Windows 上的 ASA 专用服务器管理工具，提供 Fyne 桌面 GUI、HTTP API 与内嵌 Web 界面、命令行，
以及 Windows 服务注册。支持多实例的创建、启动 / 停止 / 重启、INI 配置编辑、存档备份与恢复、RCON、日志查看、服务端更新，
基于镜像目录的多实例共享服务端文件，以及 FRP 端口映射与 Syncthing 文件同步的集成。

[Unreleased]: https://github.com/Niexiawei/asa-server-manager/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/Niexiawei/asa-server-manager/releases/tag/v0.0.1
