# 单测环境耦合排查与修复计划

> 日期：2026-10-02　分支：`fix/audit-batch3`（基线 `f44c13d`）
> 来源：`docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md` §11.8.3。那里修了三个「依赖开发环境才通过 / 才失败」的用例，本文把同类问题在**全部测试**里排查一遍。
> 状态：**只出方案，未改代码。**

---

## 1. 排查范围与方法

范围：仓库里全部 149 个 `*_test.go`（`internal/` 34 个包、`pkg/` 26 个包、根包 1 个）。

「环境耦合」的判定：用例的结果取决于**跑测试的这台机器**，而不是被测代码本身。分六类：

| 类 | 含义 | §11.8.3 里的例子 |
| --- | --- | --- |
| A | 依赖开发机上的数据、配置或环境变量，换一台机器结果就变 | `Test_SetMessageOfTheDay`（实例 `ces99`） |
| B | 写开发机的真实环境（数据目录、源码树、共享临时目录），且不清理 | `Test_SetMessageOfTheDay` 改真实配置 |
| C | 依赖外网或特定域名 | — |
| D | 没有断言的「调试程序」，结果只靠肉眼看输出 | `Test_tail` |
| E | 按宿主条件跳过或改变断言：不会失败，但在某些环境里**什么都没测** | — |
| F | 已排查，不受环境影响 | — |

方法：

1. **静态扫描**：在全部测试文件里搜绝对路径、作者本机的名字（`ces99` / `asa_server_data` / `nicoi`）、`os.Getenv`、`init()` / `TestMain`、对全局目录变量的读写、网络调用、`t.Skip` 条件、`fmt.Println` 型输出，再逐个读命中的用例。
2. **实测一：Windows 去掉 `ASA_BASEDIR`**。作者本机的**系统级**环境变量是 `ASA_BASEDIR=E:\asa_server_data`，CI 与别人的机器上没有它。清掉后跑全量 `go test -count=1 -json ./internal/... ./pkg/... .`：51 个包通过、0 失败，跳过 `Test_SetMessageOfTheDay` 和 `TestProfileScriptIsSourceable`（Windows 上没有 sh，属预期）。但源码树里多出了目录，见 T2。
3. **实测二：时区**。`internal/schedule`、`internal/auth`、`pkg/serverinfo`、`internal/state`、`internal/appconfig`、`internal/arkapimanage`、`pkg/arkcache` 在 `TZ=UTC / America/New_York / Australia/Lord_Howe（半小时夏令时）/ Pacific/Kiritimati（UTC+14）` 下各跑一遍，全部通过。**没有时区耦合。**
4. **实测三：开发机多导出几个 `ASA_*` 变量**。设上 `ASA_SERVER_PORT=1`、`ASA_AUTH_ENABLED=true`、`ASA_SERVER_TLS=false`、`ASA_LOG_LEVEL=debug`、`ASA_AUTH_LAN_BYPASS=true` 后跑读配置的几个包：**3 个包、至少 13 个用例失败**，见 T1。
5. **WSL 本轮没有实测**：在 WSL 里跑全量的那条命令被本机的权限策略拦下，没有执行。WSL 的基线用的是 §11.8.4 的结果（全量通过、`Test_SetMessageOfTheDay` 跳过）；Linux 上的跳过条件由静态扫描得出（§3 T10）。实施前需要补一次 WSL 实测，并且同时在 root 与普通用户下各跑一遍。

---

## 2. 结论速览

| 编号 | 级别 | 类 | 位置 | 一句话 |
| --- | --- | --- | --- | --- |
| T1 | P1 | A | `internal/appconfig`、`internal/actions`、`internal/webapi/authapi` | 测试只隔离了 `ASA_CFG` / `ASA_BASEDIR`，开发机上任何其他 `ASA_*` 变量都会经 viper `AutomaticEnv` 改写配置，实测 13+ 用例失败 |
| T2 | P1 | A+B | `internal/config/config_test.go` 的 `init()` | 包级 `init` 用 `ASA_BASEDIR` 建目录：没设时在**源码目录** `internal/config/` 下建 5 个运行时目录（已实测），设了时整包的全局目录指向**生产数据目录**。**→ 并入 `APPCONFIG_BASEDIR_PLAN.md` Part 2** |
| T3 | 不改 | A+B+D | `internal/config` `Test_SetMessageOfTheDay` | §11.8.3 只做了「没有就跳过」。在作者本机上仍然往真实实例 `ces99` 写入公告。**2026-10-02 确认：保持现状** |
| T4 | P2 | C+D | `pkg/netutil` `TestResolveDomainToIPv4` | 解析作者的域名 `asa.nicoi.cn`，只打印不断言；断网、DNS 受限或域名过期时失败 |
| T5 | P2 | A+D | `pkg/procx` `Test_QueryProcess` | 查作者本机 `Port=9310` 的 `ArkAscendedServer.exe`，只打印不断言——查到查不到都通过 |
| T6 | P2 | B | `certmgr` / `frpmanage` / `webapi` 的 `TestMain`，`instance` 的 `withTempStateManager` | 日志写进系统临时目录的 `logs/asaServer.log`，从不清理（本机已 1.9 MB），多个测试二进制并发写同一个 lumberjack 文件 |
| T7 | P2 | E | `internal/runner/runner_linux_test.go`、`pkg/umuruntime/host_linux_test.go` | `PROTON_VERB=run` 等**承重回归用例**在没有可用 Python 的机器上静默跳过 |
| T8 | P3 | A（潜在） | `batchmanage`、`installer/status_test.go` 等 | 依赖全局目录变量「恰好为空」：判活读的是相对**当前目录**的 `instances/<名字>/`，只把部分目录变量指向临时目录 |
| T9 | P3 | — | `internal/countdown` `TestWaitCancelOneInstanceContinuesOthers` | 注释写「默认跳过」，实际只在 `-short` 下跳过，每次全量都要多等 30 秒 |
| T10 | 记录 | E | Linux 侧十余处 | root / 非 root、`ASA_TEST_*` 开关、Xvfb、ACL、overlayfs、显示等条件跳过：不改代码，但要有一张「哪个环境能测到什么」的验证矩阵 |
| T11 | 不改 | B | `pkg/userenv` `TestUserRoundTrip` | 真写 HKCU，但变量名一次性、`t.Cleanup` 删除，可以接受 |

---

## 3. 逐条说明

### T1 [P1] 其他 `ASA_*` 环境变量会改写测试读到的配置

**现象**（实测三）：

- `internal/appconfig`：`TestLoad_ASACFGWinsOverExeAndSystemDir`、`TestLoad_TwoLevelSearch_FallsBackToSystemDir`、`TestLoadCreatesTemplateWhenMissing`、`TestGeneratedTemplateIsLoadable`、`TestLoadReadsUserValues`、`TestInvalidAuthConfigIsFatal`、`TestLoadAcceptsBOMAndCRLF`、`TestLoadWithoutAutoGenerate`、`TestCheckFile`
- `internal/actions`：`TestConfigValidate`、`TestSetupResolve_BrokenEditCanBeAbandoned`
- `internal/webapi/authapi`：`TestAuthDisabledPassesEverything`（还违反了「鉴权关闭时不建 auth.db」）、`TestLANBypassAllowsLocalDirect`

（输出截到前 40 行，实际失败可能更多。）

**原因**：`appconfig.Load` 开着 `SetEnvPrefix("ASA") + AutomaticEnv()`（`internal/appconfig/config.go:442-444`），这是产品的设计（flag > 环境变量 > 文件）。测试一侧只有 `clearASABaseDir`、`newConfigEnv` 等几处清了 `ASA_CFG` / `ASA_BASEDIR`，其余 `ASA_*` 都从开发机的环境里直接透进来。现在作者本机只设了 `ASA_BASEDIR`，所以没有暴露。但只要有人为了调试导出过 `ASA_SERVER_PORT`，或在装过服务的机器上跑测试，就会出现「代码没改、测试却挂了」。

**修复**：

1. 在 `internal/appconfig` 加一个**导出的**测试辅助函数（同包已有 `OverrideSearchDirsForTest(t, ...)`，沿用同一命名），例如 `IsolateEnvForTest(t)`：遍历 `os.Environ()`，对每个 `ASA_` 前缀的变量先 `t.Setenv(k, "")`（登记还原），再 `os.Unsetenv(k)`（viper 判断的是「有没有设」，设成空串和没设不等价，实施时要用用例核实这一点）。
2. 调用点：`appconfig` 包内的 `loadFrom`、`clearASABaseDir`、`writeConfig` 这一类入口，`actions` 的 `newConfigEnv`，`authapi` 的 `setupEnv`。各包也可以在 `TestMain` 里统一清一次，但 `t.Setenv` 版本更稳：单个用例自己 `t.Setenv("ASA_SERVER_PORT", "9999")`（`config_test.go:202`）的写法不受影响。
3. 回归：在 CI 或验证脚本里加一轮「带脏 `ASA_*` 环境」的运行（§5）。

### T2 [P1] `internal/config` 的包级 `init()` 依赖 `ASA_BASEDIR`

**代码**（`internal/config/config_test.go:15-21`）：

```go
func init() {
	if err := EnsureDirectories(os.Getenv("ASA_BASEDIR")); err != nil {
		log.Fatal(err)
	}
	logger.InitLoggerWithBaseDir(BaseDir)
}
```

**两种机器上的两种问题：**

- **没设 `ASA_BASEDIR`**（CI、WSL、别人的开发机，以及实测一）：`EnsureDirectories("")` 在包目录下建出 `internal/config/{instances,server-files,steamcmd,backups,logs}`。这些名字都在 `.gitignore` 里，所以 `git status` 看不见；本机原来就有这几个目录，是之前 WSL 跑测试时留下的；排查时确认五个都是空目录，删掉后再跑一次实测一，又被重新建了出来。
- **设了 `ASA_BASEDIR`**（作者本机）：整个 `config` 包测试期间 `BaseDir` / `InstancesDir` 等全局变量指向 `E:\asa_server_data`，日志初始化到生产日志 `E:\asa_server_data\logs\asaServer.log`。`config_plugins_test.go`、`config_update_test.go` 都自己把 `InstancesDir` 换成了临时目录，所以目前只有 T3 真的碰到了生产数据；但以后新加的用例只要忘了换，就会直接读写生产实例。
- `log.Fatal` 会让整个包的测试进程直接退出，连「哪个用例」都报不出来。

**修复**：~~删掉这个 `init()`~~（T3 保持现状，它仍需要 `init()` 定位数据目录）。**2026-10-02 改为随 `ASA_BASEDIR` 的移除一起处理**：`init()` 改走 `appconfig.Load(WithoutAutoGenerate())` + `SetDirectories`（只设变量、不建目录），见 `docs/APPCONFIG_BASEDIR_PLAN.md` Part 2 P2-3 第 4 条。

### T3 [不改] `Test_SetMessageOfTheDay` 仍在改作者本机的真实实例

> **2026-10-02 确认：这个测试不动。** 下面保留排查时的分析作记录。它依赖的 `init()` 随 T2 调整，用例本体不变。

§11.8.3 ② 按当时的要求做成了「`ces99` 的 `GameUserSettings.ini` 不存在就跳过」，并注明「仍会改写作者本机 `ces99` 的真实配置文件，是原有行为」。放到这次排查的标准下，它同时属于 A、B、D 三类：

- A：只在有 `ces99` 的那台机器上才真的测；
- B：每次跑都往一个真实服务器的 `GameUserSettings.ini` 里写入公告 `哈哈哈哈123456`、时长 30，不还原；
- D：只检查 `err`，不读回文件，「写进去的内容对不对」实际上没测。

**修复（需确认）**：改成和 `config_update_test.go` 的 `setupTempInstance` 一样，在临时 `InstancesDir` 下造一个带 `[ServerSettings]` / `[MessageOfTheDay]` 的 `GameUserSettings.ini`，调用后读回文件，断言 `Message=` / `Duration=` 的值，以及其他段落和键没有被改动。同时加两条边界用例：文件没有 `[MessageOfTheDay]` 段，以及消息里带换行或 `=`。

这会推翻 §11.8.3 ② 里「跳过」的决定，所以要先确认：

- **方案甲（推荐）**：彻底改成自包含，不再碰 `ces99`；
- **方案乙**：保留现在的「有就跑」用例，但改成先备份、`t.Cleanup` 里还原，另外再加上自包含用例。

### T4 [P2] `TestResolveDomainToIPv4` 依赖外网与作者的域名

`pkg/netutil/netutil_test.go:44-51`：解析 `asa.nicoi.cn`，成功就 `fmt.Println(ips)`，不检查结果。断网、公司 DNS 拦截或域名到期都会让它失败；解析成功时也没有验证返回的是不是 IPv4。

**修复**：

1. 默认用例换成 `localhost`，断言返回值非空、每一项 `To4() != nil`，并且不含 IPv6。
2. 再加一条字面 IP 的用例（`127.0.0.1` 原样返回），以及一个必然解析失败的名字（RFC 6761 保留的 `.invalid` 顶级域）报错。
3. 真实公网解析如果还想保留，放到 `ASA_TEST_NETWORK=1` 开关后面（沿用 `ASA_TEST_RUNTIME_USER`、`ASA_TEST_XVFB` 的做法）。

`internal/actions` 的 `TestSelftestDNSUsesGoResolver` 在 DNS 不通时会 `Skip`，并且先检查了错误文案，已经是正确的写法，不用改。

### T5 [P2] `Test_QueryProcess` 是查作者实例的调试程序

`pkg/procx/wmi_windows_test.go`：`QueryProcess("ArkAscendedServer.exe", "Port=9310")`，打印结果，不断言。作者本机开着那个实例时会打印出来，没开时返回空切片，两种情况都通过，所以这个用例**什么也没测**。性质与 `Test_tail` 相同。

**修复**：查**测试进程自己**：用 `filepath.Base(os.Executable())` 作名字、`-test.` 作命令行片段，断言结果里有 `os.Getpid()`，并且 `CommandLine` 非空；再加一条必然查不到的组合，断言返回空切片且没有错误。`escapeWQL` 对 `'`、`%`、`_`、`\` 的转义如果还没有单测，也在这里补上：它决定了实例名里带这些字符时会不会查错进程。

### T6 [P2] 测试日志写进共享的系统临时目录，从不清理

`internal/certmgr/ca_test.go:17`、`internal/frpmanage/manager_test.go:15`、`internal/webapi/transport_test.go:20`、`internal/instance/stoppable_test.go:14` 都调用 `logger.InitLoggerWithBaseDir(os.TempDir())`，结果是 `%TEMP%\logs\asaServer.log`（Linux 上是 `/tmp/logs/asaServer.log`）。

- 不清理：本机这个文件已经 1.9 MB，每跑一次全量都会变大。
- 并发：`go test ./...` 默认几个包并行，几个测试二进制同时往同一个 lumberjack 文件里写，轮转时会互相改名、截断。目前没有用例断言这个文件的内容，所以不会失败，但文件本身已经没法读。
- Linux 上 `/tmp/logs` 是哪个用户先建的就归谁，root 和普通用户轮流跑时，后来的那个会写不进去。

**修复**：`TestMain` 里用 `os.MkdirTemp("", "asa-test-*")`，`m.Run()` 之后先关闭 logger 的文件句柄（`pkg/logger` 的单测 `initForTest` 已经处理过 Windows 上「文件句柄没关就删不掉目录」的问题，可以照抄），再 `os.RemoveAll`；`withTempStateManager` 这种在用例里初始化的，改用 `t.TempDir()` 并在 `t.Cleanup` 里关闭。

### T7 [P2] 承重回归用例在没有 Python 的机器上静默跳过

- `internal/runner/runner_linux_test.go:97-100`、`:143-146`：`umuCommandLine` 出错就 `Skip`。这两条守的是 `PROTON_VERB=run`（写错了，共享 prefix 下第二个实例会永久排队，见 `docs/UMU_PREFIX_PLAN.md`）和关闭 Xalia。
- `pkg/umuruntime/host_linux_test.go:213-216`、`:284`：`commandOrSkip` 在 `Interpreter()` 失败时 `Skip`，`Host.Command` 环境变量叠加顺序的用例都依赖它。

`umuCommandLine` 失败的常见原因是机器上没有版本足够新的 `python3`。普通的 CI 镜像或精简容器正好就是这样，于是这几条最重要的回归用例在那里一直是 SKIP，不会有人注意到。

**修复**：`pkg/pyfinder` 的显式覆盖只是执行解释器、读 `sys.version_info`（`pkg/pyfinder/pyfinder.go:36`），所以可以在 `t.TempDir()` 里放一个只回显 `3 12` 的 `#!/bin/sh` 脚本，经 `runner.Config.PythonBin` / `umu.Config.PythonBin` 指过去。这样用例不再依赖宿主的 Python，`Skip` 分支可以删掉，改成 `t.Fatal`。实施时要确认没有其他环节会真的拿这个解释器去执行 `umu-run`（这几条用例只拼命令、不 exec，按现在的代码是成立的）。

### T8 [P3] 依赖全局目录变量「恰好为空」

`internal/config` 的 `BaseDir` / `InstancesDir` / `ServerFilesDir` / `SteamCmdDir` / `BackupsDir` 是包级变量，测试各自决定换掉哪几个：

- `internal/batchmanage` 的 `TestCountdownPhaseMarksNotRunningRatherThanCancelled` 等用例，依赖「测试实例名不存在，判活必然为假」。真实判活（`procpkg.IsInstanceProcessAlive`）先读 `cfgpkg.InstancesDir` 下的 `inst-a/instance_config.ini`，再按里面的端口查本机监听，最后看 PID 文件。`InstancesDir` 是空串，所以实际读的是**相对当前目录**的 `instances/inst-a/`。`internal/countdown` 在 `TestMain` 里把 `isAlive` 换成了桩，不受这一条影响。
- `internal/installer/status_test.go` 只换了 `SteamCmdDir` 和 `ServerFilesDir`，`BaseDir` 仍为空；如果被测代码以后用上了 `{BaseDir}/.server-files-update.lock`，锁文件就会落到包目录里。

现在这些都能通过，是因为没有任何东西给这些变量赋过值。一旦哪个包也像 T2 那样在 `init` 里赋了值，或者开发机的当前目录下恰好有同名目录，结果就会变。

**修复**：在 `internal/config` 提供一个测试辅助函数 `UseTempDirsForTest(t) string`：调用 `SetDirectories(t.TempDir())`，并用 `t.Cleanup` 恢复**全部五个**变量。上面列的用例以及 `plugindata` / `arkapimanage` / `pluginapi` / `mirror` 里各自手写的「保存 → 替换 → 还原」都改用它。这一条不急，可以和 T2 一起做。

### T9 [P3] 30 秒的倒计时用例注释与行为不符

`internal/countdown/run_test.go:348-353` 的注释说这条「必然要跑满 30s，默认跳过」，代码却只在 `testing.Short()` 时跳过，而全量验证命令从来不带 `-short`。实测一里它独占了 30 秒，`countdown` 包总共 34 秒，是全仓最慢的包。

**修复（二选一）**：把注释改成「`-short` 时跳过」，接受这 30 秒；或者改成 `ASA_TEST_SLOW=1` 才跑。倾向前者：这是唯一一条端到端覆盖「多实例对齐倒计时、取消其中一台」的用例，默认不跑就等于没有。

### T10 [记录] 按宿主条件跳过的用例：要有一张验证矩阵

这些跳过都是**有意的**，跳过的原因也写清楚了，不需要改代码。问题在于：在某一个环境里全绿，不代表这些用例都跑过。

| 条件 | 用例（举例） | 在哪里能跑到 |
| --- | --- | --- |
| 必须是 root | `pkg/sysuser` 的 Managed 分支、`pkg/wineprefix` overlay 挂载守卫、`pkg/xvfb` `ensureSocketDir`、`xdisplay` 的 root Xvfb 自检 | WSL（默认就是 root） |
| 必须**不是** root | `internal/runner/runtimeuser_linux_test.go:22`、`pkg/sysuser` not-managed 分支、`pkg/xvfb:445` | WSL 下切成普通用户跑。**目前没有人跑过** |
| `ASA_TEST_RUNTIME_USER=1` + root + useradd | `internal/runner` 真实建、删系统用户 | WSL root，手动打开开关（§11.6、§11.7 的验证用过） |
| `ASA_TEST_XVFB=1` + root + 装了 Xvfb | `pkg/xvfb` 真实拉起 Xvfb | 手动打开开关 |
| 装了 `setfacl` 且文件系统支持 ACL | `pkg/shareacl` | WSL ext4 上的 `/tmp` |
| 能拿到 / 拿不到显示 | `xdisplay` 的 `TestAcquireUnavailableIsTyped`（能拿到显示就跳过） | WSLg 会设 `DISPLAY=:0`，所以在 WSLg 上这条**永远跳过**；要在无头环境才能测到 |
| 能建 ETW 会话、且没有同名会话在跑 | `pkg/winnetetw` `TestControlCodeSemantics` | 没有运行 asa-server 服务的 Windows |
| STA 可初始化 | `pkg/folderpicker` | 普通 Windows 桌面 |
| 有 POSIX sh | `pkg/userenv` `TestProfileScriptIsSourceable` | Linux |
| `runner.CheckRuntime()` 通过 | `internal/actions` `TestVerifyEnvironmentReady_NilWhenEverythingPresent` | Windows 恒通过；Linux 上要有已装好的运行时，否则跳过 |

**要做的**：把这张表的「怎么跑」写成 §5 验证步骤的一部分，至少补上「WSL 普通用户跑一遍」。`TestVerifyEnvironmentReady_NilWhenEverythingPresent` 在 Linux 上可以像它对 SteamCMD 的做法一样，把运行时也伪造进临时 BaseDir，不再依赖宿主（可选，做 T7 时顺手）。

### T11 [不改] `pkg/userenv` 真写注册表

`TestUserRoundTrip` 会写 `HKCU\Environment`，并广播 `WM_SETTINGCHANGE`。变量名带 PID 和纳秒时间戳，`t.Cleanup` 里删除，测的正是「真写注册表」这个能力本身，用假实现替换就没有意义了。保留。`TestGetMachineReadable` 只读系统级 `Path`，任何 Windows 上都有。

---

## 4. 已排查、确认没有问题的（F 类）

- **固定路径字符串**：`configcmd_test`、`appconfig`、`mirror_test`、`procmatch`、`umu_linux_test`、`wineprefix_test`、`console_test` 等里出现的 `C:\…`、`D:\…`、`/opt/asa/…`、`E:\asa_server_data\…` 都只是**纯字符串夹具**，没有任何用例拿它们去访问磁盘。`precheck_test`、`wineprefix_test` 里的 `jibian-pve` 同样只是字符串。
- **网络**：`download`、`arkcache`、`steamrt`、`vcredist` 的下载用例全部走 `httptest`；`procx`、`filesyncmanage`、`webapi/transport_test` 监听的都是 `127.0.0.1:0`；`frpmanage` 指向 `127.0.0.1:1`，连接必然被拒绝，这正是它要的。没有固定端口。
- **系统信任存储与证书**：`certmgr` 与 `webapi` 用 `Trust: false` 加临时 BaseDir，不写系统根证书存储。
- **状态库**：`state`、`installer/launching_test`、`instance/stoppable_test` 的 BadgerDB 都在 `t.TempDir()`。`TestMetricsStoreNilWithoutManager` 在单例已初始化时跳过，同一个包里不存在会先初始化单例的用例，实际上总会跑到。
- **持久化环境变量**：`internal/actions` 用 `stubPersist` 替换了注册表与 `/etc/profile.d` 的写入。
- **服务管理器**：`svcmgr` 只生成配置和 systemd 脚本文本，不调用 SCM / systemctl。
- **时区与夏令时**：实测二全部通过。
- **ETW**：已有同名会话就跳过，不会抢走正在运行的服务的会话。
- **源码树**：实测一前后对比，除了 T2 的那几个目录，全量测试不往仓库里写任何文件。

---

## 5. 实施排期与验证

**零批（先做）**：移除 `ASA_BASEDIR`（`docs/APPCONFIG_BASEDIR_PLAN.md` Part 2），T2 随之解决。**✅ 2026-10-02 已完成**（`07d6e3d`），
同一分支还做了启动前配置校验（`02f1c36`），T1 的「其余 `ASA_*` 变量污染测试」**尚未处理**。

**一批（P1）**：T1。T3 不改。

**二批（P2）**：T4、T5、T6、T7。

**三批（P3 与记录）**：T8、T9，以及把 T10 的矩阵写进验证步骤。

**每批的验证**（新的回归基线，比 §11.8.4 多三轮）：

1. Windows 常规：`go build ./...`、`go vet ./internal/... ./pkg/...`、`go test -race ./internal/... ./pkg/...`（PowerShell）。
2. **Windows 干净环境**：同一条测试命令，但先把 `ASA_BASEDIR` 设为空串。
3. **Windows 脏环境**：额外设上 `ASA_SERVER_PORT=1`、`ASA_AUTH_ENABLED=true`、`ASA_SERVER_TLS=false`、`ASA_AUTH_LAN_BYPASS=true`，结果必须与第 1 轮相同。这一轮是 T1 的回归。
4. WSL root：`ASA_TEST_RUNTIME_USER=1 go test -race ./...`。
5. **WSL 普通用户**：同一条命令（不带 `ASA_TEST_RUNTIME_USER`），覆盖 T10 表里「必须不是 root」的那几条。
6. **源码树无残留**：测试前后对比 `git status --short --ignored`，不能多出任何条目。

做完一、二批之后，`go test -json` 输出里的 SKIP 只应剩下 T10 表里列出的那些。新出现的 SKIP 要么补进表里，要么就说明又有用例开始依赖环境了。

---

## 6. 本次排查留下的、需要人工处理的事

- `internal/config/{instances,server-files,steamcmd,backups,logs}` 是 T2 建出来的空目录，被 gitignore，不影响提交。做完 T2 之后可以手动删掉。
- `%TEMP%\logs\asaServer.log`（1.9 MB）是 T6 的产物，做完 T6 之后可以手动删掉。
- 第 1 节第 5 条提到的 WSL 实测，需要在允许执行 WSL 命令后补跑一次。
