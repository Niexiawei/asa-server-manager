# 更新日志

本文件记录 ASA Server Manager 每个版本面向使用者的变化。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。
设计与取舍的细节在 `docs/` 下对应的计划文档里，这里只写结论；条目后括注的文档名即出处。

## [Unreleased]

### 升级须知

- **Linux，`linux.prefix_mode: overlay`**：升级后每个实例第一次启动时，它的 Wine 可写层会**重建一次**（几秒到几十秒），
  之后恢复秒起。原因是底层前缀的指纹格式变了（见下方「修复」）；可写层里没有用户数据（存档在 `instances/<实例>/Save`），
  不会丢任何东西。`shared` / `per-instance` 模式不受影响。
- `GET /api/system/preflight` 的响应里，`display` 字段换成了 `runtimePlugins`（每个 Linux 运行时组件——图形显示、
  VC++ 运行时——的状态列表）。自带的前端没有用到它；自己写了脚本读这个字段的需要改读新字段。
- **`config.yaml` 不再在任何命令启动时自动生成**：`--help`、`config` 子命令完全不碰磁盘；`setup` 与 Windows GUI 在没有配置时
  由自己引导生成；**`api` 与服务模式缺配置时拒绝启动**（退出码 78），全新部署先运行 `asa-server config init`（或 `setup`），
  靠首次启动生成配置的部署脚本要补上这一行。已有 `config.yaml` 的部署不受影响
  （`docs/SETUP_FLOW_OPTIMIZATION_PLAN.md` Part 2、`docs/APPCONFIG_BASEDIR_PLAN.md` Part 2）。
- **配置文件校验不通过时不再以默认配置启动**：以前只记一条错误日志，然后带着默认配置跑起来（下载代理、端口、TLS、Linux 运行时设置
  与数据目录一起被静默丢掉）；现在 `api`、服务、`setup`、GUI 与其余命令都直接退出（退出码 78），并说明哪个文件、哪里错了。
  服务模式的原因写进平时的日志（Windows 另写一条事件日志），Linux 的 systemd 单元会进入 `failed` 而不是反复重启。
  `config` 子命令与 `service stop|start|remove`、`cert uninstall` 不受影响。**改完配置先 `asa-server config validate` 再重启服务。**
- **移除 `ASA_BASEDIR` 环境变量**：数据目录只由 `config.yaml` 的 `basedir` 字段决定，留空 = 配置文件所在目录。
  受影响的只有「`basedir` 留空、靠 `ASA_BASEDIR` 指定数据目录」的部署：升级后数据目录会变成配置文件所在目录，看起来像实例全没了。
  程序检测到这个变量仍设着时会在启动日志和 `config path` / `config validate` 里提示，照提示在 `config.yaml` 里写上
  `basedir: "<原来的目录>"` 即可，然后删掉这个环境变量。这条提示将在下个版本删除（`docs/APPCONFIG_BASEDIR_PLAN.md` Part 2）。
- **Linux，systemd 服务**：unit 里的环境变量改为带引号的 `Environment="K=V"`。旧 unit 在路径不含空格时照常工作；
  配置 / 数据目录路径里有空格的，`service remove` 后重新 `service install` 一次。
- **Linux，新配置项 `linux.launch_gate_timeout`**（默认 `20m`，不得小于 `1m`）：`prefix_mode: shared` 下一台实例初始化超过这么久，
  后面的实例就不再排队等它。老的 `config.yaml` 里没有这一项时取默认值，无需修改。
- **实例停止后会删除它的 PID 文件**（`instances/<实例>/pid`、`launcher_pid`、`asa_api_pid`）。以前它们一直留着；
  自己写脚本读这些文件判断实例是否在运行的，需要把「文件不存在」当作「已停止」。
- **Linux，`linux.umu_runtime_user` 不能再是 `root`**（或任何 uid 为 0 的账号）：那等于没有降权，以前却会被当作「已降权」静默接受。
  确实要以 root 运行游戏进程，请改设 `linux.umu_run_as_root: true`。
- **Linux，`asa-server setup` 的自检**：ACL 探测因「不支持 ACL」以外的原因失败时（数据目录不可写、`setfacl` 被 SELinux 拦截等）
  现在是阻断项。以前自检不报、服务启动时才失败。
- **新建与重命名实例时，名字必须在 Windows 与 Linux 上都是合法的目录名**：不能是 `CON`、`NUL`、`COM1` 之类的保留名
  （带扩展名也不行），不能以点或空格结尾、以空格开头，不能含 `<>:"|?*` 与控制字符，不超过 100 个字符；`.` 也不再被接受。
  **已有的实例不受影响**、照常可以管理，只是 `GET /api/instances` 会给这类名字附带 `name_warning` 建议改名。
  上传插件包时插件名按同一套规则校验。
- **Linux，ArkApi 缓存**：以前的版本在缓存解压到一半时同步镜像，可能把不完整的缓存带进实例镜像，且之后不会被修复。
  升级后不会再发生；若某个实例的 ArkApi 每次启动都重新下载 offsets，停止它、删除它的镜像目录（`server-files-tmp-<实例>`）后重新启动即可。

### 新增

- **`asa-server config init | path | validate`**：先生成配置、改好下载代理等，再运行 `setup` 下载几百 MB / 几十 GB。
  `config init` 只写 `config.yaml`（不建目录、不下载），已存在时需 `--force`（先备份）；`config path` 显示三级查找各自的状态、
  当前使用哪一份以及数据目录的来源；`config validate` 只校验不修改（`docs/SETUP_FLOW_OPTIMIZATION_PLAN.md` Part 2）。
- **`setup` 在没有配置时会停下来等你改**：生成配置后打印需要检查的项，回车后重新加载（含下载代理）再开始下载；改坏了可以修正后重试。
- **Windows GUI 首次设置向导**：配置文件位置（程序目录 / 系统目录 / 自定义目录）→ 数据目录 → 检查配置（记事本打开、重新校验）→
  初始化环境。「浏览…」用系统原生的选择文件夹对话框。
- **`config init --set-env`**：把 `ASA_CFG` 持久化为配置目录——Windows 写当前用户环境变量（GUI 选自定义目录时同此），
  Linux 以 root 写 `/etc/profile.d/asa-server.sh`。`service install` 会把安装时的 `ASA_CFG` 一并写进服务配置。

### 修复

- **Linux，降权运行**：只要数据目录里有一个实例镜像（`server-files-tmp-*`）不归运行时用户所有——比如从降权功能之前的版本升级、
  或手工 `chown -R root` 过——asa-server 就会以「降权运行时环境自检未通过」拒绝启动，重启也没用，`perms fix` 也修不好。
  现在启动自检只检查启动时会自动修复的目录；实例镜像在该实例启动时检查（它在那之前会被自动修复），其余的只作为自检建议项显示；
  `asa-server perms fix` 也会修复全部实例镜像（`docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md` §2.2 P0）。
- **实例名互为前缀时会认错进程**：`srv` 与 `srv2` 同时存在时（存档目录名默认就是实例名），启动 `srv` 可能把 `srv2` 的游戏进程
  当成自己的，之后停止 `srv` 会杀掉 `srv2`。现在按带边界的存档目录名精确匹配（审计报告 §6.2）。
- **Linux，停止实例可能带走其他实例**：停止时按游戏端口找进程，而 Wine 的 wineserver 也持有这个端口，可能被选中；
  `prefix_mode: shared` 下它是所有实例共用的，停止超时后的强杀会让同一 Wine 会话里的其他实例一起退出。现在停止只针对真正的游戏进程
  （审计报告 §6.2）。
- **强制停止可能误杀无关进程**：强制停止会杀 PID 文件里记录的进程，而这些文件从不清理，号码可能早已被系统分给别的程序。
  现在只对仍属于该实例的进程动手，停止后删除 PID 文件；启动器已退出时，改为按实例标记清理残留的启动链（审计报告 §6.2）。
- **Linux，`prefix_mode: shared` 下两个 ArkApi 实例先后启动时，第二个仍会进入同一个 Wine 会话并挂起**：冲突检查只认「端口已监听」，
  而前一台放行启动闸门时端口通常还没绑定。现在正在启动中的实例也算在内，且检查在启动闸门内再做一次（审计报告 §3.2）。
- **Linux，`prefix_mode: shared` 下一台卡在初始化之前的实例会让之后的所有启动永远排队**。现在超过 `linux.launch_gate_timeout`
  就放行后面的实例；卡住的那台不会被停止。等待完整启动的调用方（批量操作等）在实例中途退出时也不再永久挂起（审计报告 §6.2）。
- **Linux，ArkApi 插件日志开服 5 分钟后不再更新**：「等日志文件出现」的 5 分钟超时同时掐断了之后的转抄。现在转抄一直持续到实例停止，
  停止时在日志末尾写一行说明（审计报告 §6.2）。
- **程序启动时的插件目录迁移可能读到正在写入的插件数据库**：判断实例是否在运行只看端口，漏掉了正在启动、端口尚未绑定的实例。
  现在进程存活或状态处于启动 / 运行中都会推迟迁移（审计报告 §7.2）。
- **Linux，降权运行**：运行时用户的 passwd 家目录为空或为 `/`（如 `nobody`）时，启动自检会因检查 `/` 的属主而拒绝启动，
  游戏进程拿到的 HOME 也没人创建。现在统一使用 `{数据目录}/runtime-home` 并自动创建（审计报告 §2.2）。
- **Linux，自管 Xvfb**：Xvfb 起不来或中途退出后，为它重新挂载为可写的 `/tmp/.X11-unix` 不会被还原（WSL 上这是与 WSLg 共享的挂载）。
  现在起失败即还原、停止时总会还原；看门狗按 2s / 5s / 15s 退避重试（以前实际只试一次），remount、还原、Xvfb 意外退出都会写日志
  （审计报告 §1.2）。
- **Linux，overlay 模式**：宿主机重启后第一次启动实例时，它的 Wine 可写层会被静默清空重建；API 服务每次启动也会把所有空闲的可写层卸掉。
  现在底层没变时原样重新挂上，没事可做时不再卸载（审计报告 §3.2）。
- **Linux，overlay 模式**：修改共享底层前缀（环境准备、`verify`、补装 VC++）与实例启动同时发生时，可能卸掉正在启动的实例的可写层，
  或在实例挂载期间改动底层。现在两者互斥：修改期间启动实例会立即失败并说明是哪个操作在修改（审计报告 §3.2）。
- **Linux，`setup` 与服务同时运行**：两边会同时下载同一个文件、在同一个 Wine 前缀里跑 wineboot。现在准备运行时有跨进程锁，
  后来的一方会等待并提示；一次 `setup` 也不再重复准备运行时两遍（审计报告 §4.2）。
- **终端里的 `update` 与服务互相看不见**：「服务端文件正在更新」只记在各自进程里，两边可以同时跑 SteamCMD 改写 `server-files`，
  终端更新期间服务也会照常启动实例。现在这个状态跨进程共享：一边在更新时，另一边的更新会被拒绝并说明原因，实例启动也会被拒绝
  （审计报告 §11.6）。
- **ArkApi 缓存预取**：实例启动时的预取还在下载旧版 exe 的缓存、ARK 更新已经换掉了 exe 时，两次预取交错可能删掉对方刚提交的缓存，
  ArkApi 只好自己整包重下。现在实例处于启动流程中时拒绝更新服务端文件，提交与清理互斥，清理也不再删除当前正被指向的那一份
  （审计报告 §5.2）。
- **ArkApi 缓存预取**：缓存解压时直接写进最终目录，同时进行的镜像同步可能复制到半成品；下载超过 30 分钟时另一个进程会把锁当作陈旧锁夺走，
  两边同时往同一个文件里写；已下完的旧包只要字节数相同就会被复用；等待同一份缓存的实例启动无法取消。现在缓存解压完、校验通过后才整体出现，
  跨进程锁随持有进程退出自动释放（不再有「陈旧」判断），旧包来源不一致时重新下载，等待可以取消（审计报告 §5.2）。
- **下载**：服务器返回的内容比声明的长时会一直写到磁盘满；续传时服务器从错误的位置开始返回，数据会被拼到错误的偏移上。
  现在超出声明长度即中止并删除半成品，续传起点不符时整个重新下载。所有下载（SteamCMD、umu、GE-Proton、Steam Linux Runtime、
  Syncthing、ArkApi 缓存）都受益（审计报告 §5.2）。
- **Linux，强制停止与启动失败清场**：忽略 SIGTERM 的 Wine 进程不会被升级为强杀，留下来继续占着端口与 Wine 会话。
  现在先请求退出，15 秒内没退出的强杀（审计报告 §6.2）。
- **Linux，ArkApi 实例的 `launcher.log`**：加载器秒退时最后几行输出（恰好是排障最需要的）可能丢失；`launcher.log` 打不开时
  启动链的输出没人读，写满缓冲后可能卡住启动链。现在读完才关闭，打不开时照常读走并丢弃（审计报告 §6.2）。
- **插件更新中途程序被结束**：旧版本已挪进备份、新版本还没放上去时，插件在面板上显示为未安装，只能手工从备份恢复。
  现在下次操作该实例（含启动）时自动补完更新；新版本也丢了时回滚到更新前的版本（审计报告 §7.2）。
- **Linux，`server-files` 里 ArkApi 目录是小写（`arkapi/plugins`，手工解压的常见结果）时**：插件目录迁移找不到镜像里上一轮崩溃
  遗留的插件数据，随后镜像同步把那个目录换成链接，数据就没了。现在按盘上的实际大小写查找（审计报告 §7.2）。
- **Linux，`asa-server prefix gc --apply`**：回收某个实例上一个模式留下的独立前缀时，会连带删掉它当前在用的 overlay 可写层；
  实例名以 `bak-` 开头时，它的前缀会被当成版本备份、跳过「是否仍被 wineserver 占用」的确认直接删除。现在每一行只删它自己那种形态，
  删除前都会再确认一次占用（审计报告 §3.2）。
- **Linux，overlay 模式**：每次实例启动前的写入自检会在共享底层前缀里建删一个文件，而那个目录正被其他运行中的实例挂载为底层
  （overlayfs 的未定义行为）。现在自检写在这次启动自己用的前缀上（审计报告 §2.2）。
- **Linux**：`PATH` 上某个 Python 候选（坏掉的包装脚本等）挂住时，`setup` 的自检与所有实例启动会一起永久卡住。现在每个候选最多探测 5 秒，
  超时按失败处理（审计报告 §2.2）。
- **日志跟随**：刚开始跟随一个日志文件的瞬间写进来的行可能被跳过，或在回放历史时重复出现。受影响的不只是日志面板刚打开时
  少几行：等待游戏启动的流程也靠它盯日志，关键的那一行恰好落在这个窗口里就会一直等下去。现在跟随的起点在开始时就确定
  （审计报告 §11.8）。
- **Linux，`linux.umu_run_as_root: true`**：自管 Xvfb 以 root 运行且没有访问认证（本机任何账号都能连上），以前没有任何提示。
  现在拉起时记一条警告，`setup` 自检给出建议项（审计报告 §1.2）。
- **Linux，overlay 模式**：共享底层前缀后来才补装上 VC++ 运行时（典型：先在无头机上建好实例，后来装了 Xvfb 再跑
  `asa-server setup`）时，已有实例的可写层不会察觉，早先复制上来的注册表与 system32 会一直挡住新装的运行时，
  ArkApi 起不来且没有任何提示。现在底层装了新组件，已有的可写层会在下次启动时自动重建（`docs/UMU_PREFIX_PLAN.md` §8.1）。
- **Linux，overlay 模式**：`asa-server verify-arkapi --install-vcredist` 在有实例运行时会直接改写被它们挂载着的共享底层前缀
  （overlayfs 明确的未定义行为，症状落在正在运行的实例上）。现在它与 `setup` 走同一道保护：有实例的可写层挂在上面时拒绝并说明原因
  （`docs/UMU_PREFIX_PLAN.md` P0）。
- `setup` 与 GUI 选定数据目录后重新加载配置时，漏了重新应用下载器配置：配置里改的下载代理要重启程序才生效。
- `asa-server --help` 会在程序旁边生成 `config.yaml` 并建 5 个空的数据目录。
- **Linux，systemd 服务**：路径含空格的环境变量（`ASA_CFG`、`HOME`）被 systemd 按空白切开，服务读到的是另一个目录。
- 设置了 `ASA_CFG` 时，首次引导写入数据目录会去改程序目录下一份并不存在的配置文件。
- Windows GUI 的首次启动对话框可能被随后显示的主窗口压在下面。

### 变更

- **`config.yaml` 的编码**：Windows 上写成带 BOM 的 UTF-8 + CRLF 换行，Windows Server 自带记事本打开不再乱码、不再挤成一行。
  Linux 上交互式 `config init` / `setup` 会先问一句「能否正常显示中文」来选注释语言；非交互时终端 locale 不是 UTF-8 则生成纯 ASCII
  的英文注释版（`--lang zh|en` 可指定）。`cat` / vim 里中文都是乱码时，通常是 SSH 客户端按 GBK 解码，把客户端改成 UTF-8 即可
  （`docs/LINUX_DEPLOYMENT.md` §2.2）。
- `setup` 询问数据目录时直接回车，表示「与配置文件同目录」（以前要求必须填写）。
- **Linux 运行时内部重构**（`docs/UMU_RUNTIME_PLUGIN_PLAN.md`）：umu / Proton 运行时的编排独立成 `pkg/umuruntime`，
  图形显示（自管 Xvfb）与 VC++ 运行时改为声明依赖的插件，实例启动只声明「需要什么能力」。对使用者的行为、日志与报错文字保持不变，
  只有一处例外：ArkApi 实例启动时「没有检测到 VC++ 运行时」那条告警的措辞略有调整。

## [0.1.0] - 2026-09-25

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
  （以标记文件 `ArkApi/.plugin-layout` 判断），无需手工操作（`docs/ARKAPI_PLUGIN_PLAN.md`）。
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

[0.1.0]: https://github.com/Niexiawei/asa-server-manager/compare/v0.0.1...v0.1.0
[0.0.1]: https://github.com/Niexiawei/asa-server-manager/releases/tag/v0.0.1
