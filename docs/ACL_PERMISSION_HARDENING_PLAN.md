# Linux 权限方案加固执行文档（ACL 告警 / setup 提示 / 上传后自愈）

> 状态：**待实施**。派生自 `docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md` §3.7 / §7.5.2。
> 前置背景：该文档确立了"方案 B（组 + setgid + 默认 ACL）+ 方案 A（chown）兜底"的
> 权限模型，并已在真机验证方案 B 生效（那份文档 §7.5.2.2）。
> 本文处理三件收尾：**ACL 缺失的可见性**、**setup 阶段的引导**、
> **上传文件后如何自愈**。

---

## 0. 结论速览

| # | 诉求 | 结论 |
|---|---|---|
| **P0** | （新发现）ACL 缺失会**阻断 `asa-server setup`** | **必须修**，这是上一轮改动引入的回归 |
| 1 | 用 fsnotify 监听上传、变更后重建 ACL | **建议不做**（§3.1）。审查这条时做了全量审计（§3.2）：同类路径共 10 处，**全部是 asa-server 自己以 root 创建的**，没有一处属于"外部改动"，所以没有任何一处需要监听。但审计查出 3 处（`Save/<MapName>`、`Logs/ShooterGame.log`）目前排在 prepare **之后**执行 —— 无 acl 的机器上游戏连自己的日志都写不了。正解是把 prepare 挪到 `runner.Run` 正前方（**T2a**，§3.3），一次覆盖全部 |
| 2 | Linux 下 acl 不存在时给出警告 | **做**，但要先有"警告"这个概念 —— 现在 `Problem` 只有一种严重级别，任何一条都会阻断 setup（§2） |
| 3 | setup 阶段给出安装提示 | **做**，§2 的严重级别落地后自然得到（§4） |

---

## 1. P0：ACL 缺失现在会阻断 setup（上一轮引入的回归）

### 1.1 问题

上一轮把 `checkACLSupport()` 接进了 `preflight()`：

```go
// internal/runner/preflight_linux.go
if p := checkACLSupport(); p != nil {
    problems = append(problems, *p)
}
```

而 `preflight()` 的两个消费方都把"有任何一条"等同于"环境不可用"：

```go
// internal/actions/setup.go:115
func runLinuxPreflight(ignore bool) error {
    problems := runner.Preflight()
    if len(problems) == 0 { ... return nil }
    fmt.Println("宿主运行时依赖不满足，setup 无法继续。...")
    ...
    return fmt.Errorf("宿主依赖缺失，已中止；...")   // ← 直接中止
}
```

```go
// internal/webapi/systemapi/systemapi.go:44
"healthy": len(problems) == 0,                      // ← 前端据此判定环境是否就绪
```

**后果**：一台没装 `acl` 包的 Linux 机器上，`asa-server setup` 会**直接失败**，
提示"宿主依赖缺失"。但缺 ACL 根本不是缺失依赖 —— 代码会降级到方案 A，
一切照常工作，只是少了增量保护。

这和 `glibc32` / `python3` 是两类东西：缺后者 setup 继续跑只会白下载几百 MB
（`setup.go:110-114` 的原始注释说得很清楚），缺 ACL 则完全不影响 setup 成功。

### 1.2 根因：`Problem` 没有严重级别

```go
type Problem struct {
	Name   string
	Detail string
	Fix    string
}
```

所有检查项被一视同仁。要表达"这条是建议不是阻断"，就得先有这个维度。

---

## 2. T1：给 `Problem` 加严重级别（P0，其余任务的前置）

### 2.1 结构变更

```go
// internal/runner/runner.go
type Problem struct {
	Name   string
	Detail string
	Fix    string
	// Warning 为 true 表示这是**建议**而非阻断项：功能仍然可用，
	// 只是降级或有更好的做法。消费方必须区分对待 —— setup 不因它中止，
	// preflight API 不因它判定 unhealthy。
	Warning bool `json:"warning"`
}
```

JSON 是**新增字段**，对现有前端向后兼容（旧代码忽略即可）。

### 2.2 消费方改造

| 文件 | 改动 |
|---|---|
| `internal/runner/sharedaccess_linux.go` | `checkACLSupport()` 返回的 Problem 置 `Warning: true` |
| `internal/actions/setup.go` | `runLinuxPreflight` 拆成阻断项与建议项两组分别打印；**只有阻断项非空才中止** |
| `internal/webapi/systemapi/systemapi.go` | `healthy` 改为"无阻断项"；`problems` 原样返回（含 `warning` 字段） |
| `internal/webapi/actions.go:398` | 启动日志按级别分流：阻断项 `Warnf`，建议项也 `Warnf` 但文案区分（"建议"而非"问题"） |

### 2.3 setup 的新输出形态

全绿：

```
宿主依赖自检：通过
```

只有建议：

```
宿主依赖自检：通过（1 项建议）
  - [posix-acl] /opt/asa-server/basedir 不支持 POSIX ACL（setfacl not found in PATH）。
    asa-server 会退回到「把 server-files/instances 整体 chown 给运行时用户」的兜底方案：
    当前能用，但之后以 root 上传的 ArkApi 插件、mod 文件，以及 SteamCMD 更新产生的新文件，
    游戏进程都会写不了，直到下次重启 asa-server 或重跑更新
      建议：apt install acl（Debian/Ubuntu）/ dnf install acl（Fedora）/ pacman -S acl（Arch），
            并确认所在文件系统挂载时启用了 acl
```

有阻断项时维持现有行为（打印后中止，`--ignore-preflight` 仍是逃生舱），
建议项一并列出但不参与中止判定。

### 2.4 验收

- 无 `acl` 的机器上 `asa-server setup` **能跑完**，且输出里出现 `[posix-acl]` 建议
- `GET /api/system/preflight` 在同样机器上返回 `healthy: true`，
  且 `problems[]` 里那条带 `"warning": true`
- 装上 `acl` 后重跑，该条消失
- 缺 `glibc32` 时 setup 仍然中止（不因这次改动被放行）

---

## 3. T2：诉求一 —— 不做 fsnotify

### 3.1 为什么不做 fsnotify

**理由一：方案 B 下它是冗余的。**

默认 ACL 的作用点就是**内核的文件创建路径**。`ShooterGame` 上挂着
`default:group:asa-umu-runtime:rwx` 之后，root 通过 SFTP 传进来的每个文件，
在 `open(O_CREAT)` 返回的那一刻就已经是属组正确、组可写的 —— 不需要任何用户态
进程知道这件事发生过。再加一层 fsnotify 去"发现并修复"，修的是一个不存在的问题。

**理由二：方案 A 下它是个坏解法。**

- inotify **不递归**。要覆盖 `server-files` 得给每个目录单独下 watch，
  这棵树有几千个目录；`fs.inotify.max_user_watches` 在不少发行版上仍是 8192。
- 新建目录需要动态补 watch，而"补 watch"与"目录里已经开始写文件"之间存在
  天然竞态 —— 恰恰是上传大量文件时最容易发生的场景。
- SFTP / rsync 的写法是「写临时文件 → rename」，事件量大且需要去重。
- 它是个**常驻机制**，为的是弥补一个 `apt install acl` 就能根治的缺失。

**理由三：真实暴露面比看上去小得多 —— 但其中一处根本不该由用户负责。**

ArkApi 插件位于 `ShooterGame/Binaries/Win64/ArkApi`，落在镜像的**完整复制区**
（`mirror.go:21-24` 的 `win64RelPath`）。实例启动时它被真实拷贝进
`server-files-tmp-<instance>`，随后 `ChownMirrorForRuntime` 整棵 chown ——
**即使在方案 A 降级模式下，上传的插件对实例也是可用的**。

方案 A 下真正暴露的是这两处：

| 路径 | 谁创建的 | 现状 |
|---|---|---|
| `Mods` / `ModsUserData`（镜像里 junction 回 server-files） | **asa-server 自己，以 root** | ❌ 缺口 —— 见下方 §3.1.1，**这不是 `perms fix` 该管的事** |
| `verify` 直接跑 server-files | — | ✅ `VerifyServerInstallation` 已**无条件**调 `PrepareSharedTree(ServerFilesDir)` |

#### 3.1.1 修正：`Mods`/`ModsUserData` 是程序自己造的，必须程序自己修

初稿把这一处归给了 `perms fix`，这是错的。看 `mirror.go:114-120`：

```go
// 指向源的 exception（Mods / ModsUserData）必须先在源里存在：
// createInstanceMirror 靠 Walk(ServerFilesDir) 发现条目，源目录缺失就永远走不到这个分支
if err := os.MkdirAll(
    filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64SharedRelPath)), 0755,
); err != nil {
```

**每次建/修镜像时，asa-server 都会以 root 在 `server-files` 下创建这个目录**，
随后游戏以 `asa-umu-runtime` 的身份往里写 `ModsUserData` ——
正是 `LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md` §3.6 那个 CFCore 报错的形状。

这件事有确定的时机（实例启动），程序完全知道它发生了，
**要求用户手动跑一条命令去修程序自己刚造出来的目录，是不合理的设计。**
`perms fix` 的定位应该只是"带外变更（SFTP 上传）的补救"，不能拿来兜程序自身的行为。

于是拆出 **T2a**（§3.3），它才是这一处的正解；`perms fix` 降为 T2b。
而且顺着这条线做了一次全量审计（§3.2），发现同类情况**还有更严重的**。

同时这也说明：即便装了 `acl`（方案 B），这一处仍值得显式修 ——
方案 B 的继承依赖"`server-files` 在更早某个时刻被 prepare 过"，
而 T2a 不依赖任何前置状态。

综上，留给 fsnotify 的地盘只剩"管理员用 SFTP 传了 mod 包"这一种带外场景，
为它引入几千个 inotify watch 的常驻监听，性价比不成立。

### 3.2 全量审计：还有哪些「程序以 root 造、游戏要写」的路径

把仓库里所有 `MkdirAll` / `Mkdir` / `WriteFile` / `Create` 过了一遍，
筛出落在「游戏进程需要写」的树里的：

| # | 位置 | 造出什么 | 时机 | 覆盖情况 |
|---|---|---|---|---|
| 1 | `mirror.go:116` | `server-files/.../Win64/ShooterGame`（Mods/ModsUserData 的 junction 目标） | 建/修镜像 | T2a |
| 2 | `mirror.go:230`、`:688` | 同上（同步路径补建源目录） | 镜像同步 | T2a |
| 3 | `mirror.go:290` | junction 目标（migrate 路径） | 镜像迁移 | T2a |
| 4 | **`common.go:316`** | **`instances/<name>/Save/<MapName>`** | **拼启动参数时** | ⚠️ **在现有 prepare 之后执行** |
| 5 | **`server.go:373` `GetGameLogFilePath`** | **`instances/<name>/Logs` + `ShooterGame.log`（0644）** | **启动流程中** | ⚠️ **同上** |
| 6 | **`server.go:386`** | **写空 `ShooterGame.log`** | **启动流程中** | ⚠️ **同上** |
| 7 | `server.go:837`、`config.go:186/389/407/577` | `instances/<name>/Config` + 两个 ini | 建实例 / 改配置 / 同步配置 | 下次启动前的 prepare |
| 8 | `backup.go:175/225/231` | `instances/<name>/{Config,Save}` 的恢复内容 | 恢复备份 | 下次启动前的 prepare（且 `backup.go:162` **运行中直接拒绝**恢复） |
| 9 | `installer.go:274/431` | `server-files`、`ShooterGame/Saved` | update / verify | ✅ 已无条件 `PrepareSharedTree` |
| 10 | `fixups_linux.go:66` | `~/.steam/sdk{32,64}` | fixups | ✅ 紧随其后 `ChownTreeForRuntime` |

#### 3.2.1 最要命的是 4/5/6：它们在 prepare **之后**执行

当前已落地的代码是这个顺序：

```
server.go:259  ChownMirrorForRuntime(mirrorDir)
server.go:269  PrepareSharedTree(instances/<name>)      ← 现有的 prepare 在这里
       ...
common.go:316  MkdirAll(instances/<name>/Save/<MapName>)   以 root 创建   ← 之后
server.go:373  MkdirAll(instances/<name>/Logs)             以 root 创建   ← 之后
server.go:386  WriteFile(ShooterGame.log, "", 0644)        以 root 创建   ← 之后
server.go:422  runner.Run(...)                              游戏启动
```

**三处 root 侧创建全部发生在 prepare 之后、启动之前。** 降级模式（无 acl）下：

- `Save/<MapName>` 属主 root → **换地图后新存档目录写不进去**
- `ShooterGame.log` 是 root:root 0644 → **游戏日志根本写不了**，
  而 `waitServerStartup` 正是靠 tail 这个日志判断"启动完成"
  —— 实例会永远停在 `starting`，症状和 §2.5 那个 `waitForGamePID` 超时几乎一样，
  但原因完全不同，非常难查

也就是说：**当前已合入的代码之所以能正常工作，纯粹是因为这台机器装了 `acl`**
（方案 B 的默认 ACL 让这三处在创建瞬间就继承了正确属组与组写权限）。
一台没装 `acl` 的机器会踩进上面两条。

这条比 Mods/ModsUserData 更值得修，而且它进一步说明 fsnotify 不是答案 ——
这些路径不是"某个时刻被外部改动"，而是**程序自己在启动流程里创建的**，
时机完全确定。

#### 3.2.2 结论：一个放对位置的 prepare 能关掉全部

第 1–8 项有一个共同点：**它们全都只在实例启动之后才被游戏使用**。

- 4/5/6 就在启动流程内
- 1/2/3 是建镜像时造的，同一次启动内
- 7/8 是离线改动（备份恢复在运行中会被直接拒绝），下一次启动前生效

所以只要把 prepare 挪到**所有 root 侧创建都完成之后、`runner.Run` 之前**，
这 8 项一次全覆盖。不需要监听，也不需要用户手动介入。

唯一的残留：**实例运行中通过 Web UI 改 Game.ini / GameUserSettings.ini**
（`configapi` 没有运行中拦截）。降级模式下，root 重写过的 ini 会让游戏在关服时
写回 GameUserSettings.ini 失败。方案 B 下不存在这个问题。
这一处窄到不值得为它引入任何常驻机制，记录在案即可。

### 3.3 T2a：junction 目标在实例启动时自动准备（无需用户介入）

**原则**：`Lchown` 不跟随软链，所以**镜像里每一条 junction 的目标目录，
都必须单独做共享写处理**。这个原则一旦成立，就不该逐个路径去记 ——
镜像本来就有这份清单。

`mirror.go:246` 的 `buildExceptionTargets` 已经枚举了全部 junction 目标：

```go
targets := map[string]string{
    "ShooterGame/Saved/Config/WindowsServer": instances/<name>/Config,
    "ShooterGame/Saved/Logs":                 instances/<name>/Logs,
}
targets["ShooterGame/Saved/"+saveDir]        = instances/<name>/Save
targets[win64SharedRelPath]                  = server-files/ShooterGame/Binaries/Win64/ShooterGame
```

把它导出，实例启动时逐个 `PrepareSharedTree`：

```go
// internal/mirror/mirror.go
// ExceptionTargets 返回镜像中各 junction 指向的**真实目录**。
// 以不同用户运行游戏的调用方必须把这些目录处理成可共享写 ——
// 镜像自身的 Lchown 只改到链接，改不到目标。
func ExceptionTargets(instanceName string, cfg *cfgpkg.InstanceConfig) []string
```

调用点**必须移到 `runner.Run` 正前方**（§3.2.1 的三处 root 侧创建都在它之前完成），
而不是现在的 259/269 行：

```go
// internal/instance/server.go —— 紧邻 runner.Run 之前，位置是本设计的一部分
//
// 这里是"asa-server 以 root 造完所有东西"与"游戏以降权用户接手"之间的唯一交界。
// 放在更早的位置会漏掉启动流程自身创建的 Save/<MapName> 与 ShooterGame.log
// （见 docs/ACL_PERMISSION_HARDENING_PLAN.md §3.2.1）。
for _, target := range mirror.ExceptionTargets(instanceName, config) {
    if err := runner.PrepareSharedTree(target); err != nil {
        startErr = fmt.Errorf("为降权运行时用户准备目录 %s 失败: %w", target, err)
        return startErr
    }
}
handle, err := runner.Run(context.Background(), arkExe, args, runner.Options{...})
```

这会**取代**现在 269 行那句 `PrepareSharedTree(instances/<name>)`：
位置更靠后（覆盖启动流程自身的创建），覆盖面更准
（三个实例子目录 + server-files 里那个共享目录），
且以后新增 junction 会自动被覆盖，不会再漏。

`ChownMirrorForRuntime(mirrorDir)`（259 行）**保持原位不动** ——
它处理的是镜像本身，而镜像在那之后不再被 root 写入。

分层不受影响：`mirror` 不需要 import `runner`，只多导出一个查询函数；
调用发生在 `instance`，而它本来就同时依赖两者。

成本：四个目录都很小（Config / Logs / Save / Mods），不是 5 万条目的 `server-files`，
每次启动跑一遍完全可接受。

#### 3.3.1 验收

在**卸载了 `acl`** 的环境下验证（这是唯一能暴露顺序问题的配置；
装了 acl 的机器上默认 ACL 会掩盖一切）：

- 删掉 `server-files/ShooterGame/Binaries/Win64/ShooterGame` 后启动实例，
  该目录被重建且属组为运行时用户、组可写
- `ShooterGame.log` 中不再出现
  `LogCFCore: Error: Unable to create a directory .../ModsUserData/83374`
- **`instances/<name>/Logs/ShooterGame.log` 属组为运行时用户且组可写**，
  且实例状态能推进到 `started`（验证第 5/6 项 —— 日志写不了时
  `waitServerStartup` 会永远停在 `starting`）
- **换一张新地图启动**，`instances/<name>/Save/<新地图名>` 组可写（验证第 4 项）
- 运行中恢复备份被拒绝（`backup.go:162` 既有行为不受影响）
- Windows 上 `PrepareSharedTree` 恒为 no-op，循环空转，行为不变

### 3.4 T2b：`asa-server perms` —— 只管带外变更

"我刚传了文件"是一个**用户知道、程序不知道**的离散事件。
与其让程序猜，不如给一条命令让用户说。

```
asa-server perms status     # 报告 server-files / instances 的权限模型现状
asa-server perms fix        # 重新施加共享写权限（方案 B，缺 acl 时降级方案 A）
```

`perms status` 的输出即用户此前手工用 `getfacl` 拼出来的那份诊断：

```
运行时用户：asa-umu-runtime (uid=997 gid=997)
ACL 支持：  可用 (setfacl: /usr/bin/setfacl)
权限模型：  方案 B（组 + setgid + 默认 ACL）

  /opt/asa-server/basedir/server-files
    属组      asa-umu-runtime          ✓
    setgid    已设                      ✓
    默认 ACL  default:group:asa-umu-runtime:rwx   ✓
  /opt/asa-server/basedir/instances
    ...
```

缺 ACL 时把"权限模型"打成 `方案 A（chown 兜底）`，并附上安装建议 ——
与 T1 的 preflight 建议同一份文案。

**实现基本是现成的**：`prepareSharedTree` / `defaultACLMissing` /
`sharedAccessNeeded` / `aclSupported` 都已存在，这个命令只是把它们组装起来对外暴露。

### 3.5 可选：`POST /api/system/perms/fix`

前端在"插件管理"类页面放一个"修复权限"按钮。
仅当 `runner.RuntimeUserStatus().Managed` 为真时展示 —— Windows 与不降权场景下无意义。

优先级低于 CLI：SFTP 上传的用户本来就在终端里。

### 3.6 什么情况下我会改主意去做 fsnotify

如果同时满足：
1. 明确不接受"要求安装 `acl` 包"这个前提，且
2. 要求带外上传的文件**无需任何命令**即刻生效

那么可以做一个**窄范围**的监听 —— 只 watch
`ShooterGame/Binaries/Win64/ShooterGame`（Mods/ModsUserData，§3.1 里那处真缺口），
而不是整棵 `server-files`。这个目录的子目录数量是十几个量级，
watch 数量可控，也不需要处理"几千个目录动态增删"的复杂度。

但即便如此，它仍然只是方案 A 的补丁。**先把方案 B 装上，性价比高得多。**

### 3.7 T2b 的验收

- `asa-server perms status` 在装了 acl 的机器上报告"方案 B"，卸载 acl 后报告"方案 A"
- 以 root 在 `server-files` 下新建文件后 `asa-server perms fix`，
  该文件变为属组 `asa-umu-runtime` 且组可写
- `perms fix` 可重复执行且幂等（第二次不改变任何 `getfacl` 输出）

---

## 4. T3：诉求三 —— setup 阶段的安装提示

T1 落地后，`asa-server setup` 会在自检阶段自动打印 `[posix-acl]` 建议
及对应的安装命令，诉求三即告完成。再补两处收尾：

### 4.1 `printPostSetupTips` 增加一行

当 `checkACLSupport()` 非空（即当前处于降级状态）时，在 setup 末尾的
"接下来可以"里追加：

```
  apt install acl && systemctl restart asa-server   # 启用权限继承，避免上传插件后需要重启
```

理由：自检的输出在长长的下载日志之前，setup 跑完几分钟后用户早就翻过去了；
末尾的提示才是他真正会看到的那一屏。

### 4.2 文档同步

| 文件 | 改动 |
|---|---|
| `docs/LINUX_DEPLOYMENT.md` | 依赖列表加入 `acl`，标注为"强烈建议"而非必需，并说明缺失时的降级行为 |
| `CLAUDE.md` | `runner` 包的说明里补一句共享写权限模型（现在只提了 prefix 与 mirror 的属主） |

注：`scripts/ark_instance_manager.sh` 的依赖数组**不改** ——
那个脚本不降权，压根不需要 ACL，加进去只会误导。

---

## 5. 实施顺序与工作量

| 任务 | 内容 | 依赖 | 规模 |
|---|---|---|---|
| **T1** | `Problem.Warning` + 三个消费方分级 | — | 小（4 个文件，~60 行） |
| **T2a** | `mirror.ExceptionTargets` + 实例启动逐个 `PrepareSharedTree` | — | 小（2 个文件，~25 行） |
| **T2b** | `asa-server perms status\|fix` | T1（复用文案） | 中（新增 1 个 actions 文件 + runner 导出 2 个查询函数） |
| **T3** | setup 末尾提示 + 文档同步 | T1 | 小 |

优先级：

1. **T1（P0）** —— 在它之前，任何一台没装 `acl` 的 Linux 机器都无法完成
   `asa-server setup`。这是上一轮改动引入的回归，应当尽快单独落地。
2. **T2a** —— 与 T1 无依赖关系，可并行。它补的是**程序自身行为**留下的缺口，
   优先级高于 T2b（后者只服务于带外场景）。
3. T3、T2b —— 依赖 T1 的文案与结构。

T1 与 T2a 都很小，建议合成一个 PR 一起验证。

---

## 6. 风险与注意事项

- **`Problem` 是跨平台共享结构**，Windows 上 `Preflight()` 恒返回 nil，
  新增字段不影响任何 Windows 路径。
- **前端兼容性**：`problems[].warning` 是新增字段。现有前端会把建议项当成
  问题项渲染（显示为红色），功能不受影响但观感不对。
  前端适配可以后置，但要在 T1 的 PR 里写明。
- **`healthy` 语义变更**：从"无任何检查项"变成"无阻断项"。
  这是有意为之，但要确认前端没有别的地方依赖旧语义（`environmentReady`
  是另一个字段，不受影响）。
- **`perms fix` 会遍历整棵 `server-files`**（约 5 万条目）。
  只改元数据，SSD 上秒级，但要在命令输出里给出进度提示，
  避免用户以为卡住了。
- **不要在 `perms fix` 里做 chown 到 root 的"归位"**：
  现网机器的属主已经是 `asa-umu-runtime`（早先手工 chown 的结果），
  改回 root 没有任何功能收益，却会在一台正在服役的机器上动几万个 inode。

---

## 7. 明确不做的事

- **fsnotify 全树监听**（§3.1 三条理由）
- **把 `acl` 变成硬依赖**：降级路径是有效的，不应把一个可用的环境判为不可用 ——
  这正是 §1 那个回归的教训
- **自动 `apt install acl`**：本项目不代替用户做包管理，
  与 `preflight` 现有的所有检查项保持一致（都是给命令、不代跑）
