# appconfig.Load / BaseDir 解析重新设计

> 独立成篇，不并进 `docs/LINUX_COMPATIBILITY_PLAN.md` §10.3/§10.5——那份文档定的是
> "两级查找 + basedir 字段"这个大方向，方向本身没变；本文档管的是这个方向下
> `appconfig.Load` 的**具体 API 形状与优先级细节**，纠正第一版实现里跑偏的部分。
> 第一版的问题与本次修正的关系，见下面 §1。

## 0. 状态

> 📌 **2026-10-02 后续**：本文 §2 第 3 条第 2 步里「字段为空 → `ASA_BASEDIR`」这一档将被**移除**，
> `basedir` 字段成为数据目录的唯一显式来源。方案见文末 **Part 2**；Part 1（本节至 §7）保持原文作历史记录。

✅ 已实施。`internal/appconfig/config.go` 的 `Load()`/`EnsureDirectories(baseDir string)`
按本文档定案的算法重写；`main.go`、`internal/webapi/authapi/middleware_test.go`、
`internal/appconfig/{config_test.go,basedir_test.go}`、`internal/config/config_test.go`、
`internal/instance/common_test.go` 均已同步调整调用方式。`go build`/`GOOS=linux go build`/
`go vet`（两平台）/相关包 `go test` 全部通过，另外用真实编译产物验证过 §6 第 5 条
列的四个场景（文件 basedir 字段赢 ASA_BASEDIR、字段留空时 ASA_BASEDIR 生效、
exe 同级完整覆盖系统固定目录、ASA_CFG 完整覆盖两者），均符合预期。

唯一未完全落地的是 §6 第 4 条里"断言警告日志文本"这一半：`pkg/logger` 的
`WithConsole()` 在 `init()` 时就已经把 `os.Stdout` 这个 `*os.File` 对象捕获进
zapcore 的 sink，测试里事后重新赋值 `os.Stdout` 变量捕获不到它的输出，要严格断言
需要给 `pkg/logger` 补一个可注入的测试 sink，属于那个包的改动，本次不越界去碰。
`TestLoad_DefensiveFallbackWhenLocateFails` 只验证了兜底值本身非空且返回了错误，
警告确实打印出来了（跑测试时能在终端看到那行 WARN），但没有自动化断言其文本。

## 1. 上一版实现偏离了什么

第一版把"BaseDir 到底听谁的"做成了两条并行的逻辑：

1. `Load` 保留了一个 `explicitDir` 目录参数，非空时**整个跳过两级查找**，直接把
   `explicitDir` 当 config.yaml 所在目录；`main.go` 把 `os.Getenv("ASA_BASEDIR")`
   喂给这个参数。
2. 一个独立的 `resolveConfigDir(explicitDir string)` 函数专门处理"给了 explicitDir
   就直接用，没给就走两级查找"这层判断，和 `Load` 本身的职责重叠。

这两点合起来的效果是：只要设置了 `ASA_BASEDIR`，两级查找与 `basedir` 字段整个被
短路，`ASA_BASEDIR` 变成了事实上的最高优先级——这正好是本次改造要**推翻**的旧行为
（"数据目录到底在哪"的权威应该在配置文件里，环境变量只是没写字段时的兜底），
不是要保留的兼容路径。`Load` 也因此背了一个不该有的目录参数：调用方本不需要告诉
`Load` "去哪儿找"，两级查找规则已经内置在这个函数应该做的事情里。

**本轮追加的两条不是纠偏，是新要求**（§2 第 3、4 条）：新增 `ASA_CFG` 环境变量
专门承担"指定配置文件所在目录"这件事——它和 §1 里被推翻的、曾经拿 `ASA_BASEDIR`
兼职做这件事的旧设计不是一回事：`ASA_CFG` 只回答"去哪儿找文件"，不回答"文件没写
字段时数据放哪儿"（那仍然是 `ASA_BASEDIR` 单独的职责），两个变量各管一件事，不再
像上一版那样一个变量身兼两职、互相绕过。另外 `cfgpkg.EnsureDirectories` 也要去掉
自己那份独立的 BaseDir 兜底解析，改成强制接收调用方传入的 `baseDir`。

## 2. 设计目标（硬性要求）

1. **`appconfig.Load()` 不接收任何目录参数。** 查找规则是这个函数自己的职责，不是
   调用方传进来的外部输入。生产代码里唯一的调用点（`main.go`）只需要
   `baseDir, err := appconfig.Load()`。
2. **不存在名为 `resolveConfigDir` 的函数，也不存在任何"给一个目录、跳过查找"的
   旁路（`ASA_CFG` 环境变量本身是查找的最高一级，不是绕开查找的旁路——见第 3 条）。**
   查找与取值的每一步都直接是 `Load` 内部的步骤，不拆成一个接收"外部给的目录参数"
   的独立函数。
3. **完整解析算法**（这是唯一权威描述，不要再拆成"先定位文件"和"再取值"两条
   分开理解——`ASA_CFG` 和 `ASA_BASEDIR` 是两个语义不同的变量，但解析过程是
   一条连贯的算法，下面这一条就是最终定案，用来回答"BaseDir 到底等于什么"）：

   ```
   1. 确定要读的 config.yaml 是哪一份，"完整覆盖"——三档里只有一档会被真正读取，
      其余的存在与否对结果没有任何影响，不做任何跨档的字段级合并：
        环境变量 ASA_CFG 非空           → {ASA_CFG}/config.yaml（不存在就在这里生成默认模板）
        否则，可执行文件同级目录有 config.yaml → 用这一份
        否则，系统固定目录有 config.yaml       → 用这一份
        否则                                   → 在可执行文件同级目录生成默认模板，用它

   2. 读到这一份 config.yaml 后，取它的 basedir 字段决定 BaseDir：
        字段非空         → BaseDir = 字段值
        字段为空         → 环境变量 ASA_BASEDIR 非空 → BaseDir = ASA_BASEDIR
                          → 否则                    → BaseDir = 这份 config.yaml 所在的目录

   3. 兜底：走完上面两步后 BaseDir 仍是空字符串（正常输入下不会发生——第 2 步的
      最后一档"这份 config.yaml 所在的目录"本身恒不为空；这一步纯粹是防御性的
      最后一道闸，防的是 `os.Executable()` 出错之类的异常路径），则：
        BaseDir = 可执行文件同级目录；连这个都拿不到（`os.Executable()` 报错）
                  时才退到当前工作目录（`.`）
      并在**启动时**给出明显的警告提示（见下方"警告提示"）。
   ```

   第 2 步**只看第 1 步选中的那一份文件**，不会因为它的 `basedir` 字段是空的就
   回头去看其他档位的文件——那样等于变相在做字段级合并，违反第 1 步的"完整覆盖"。
   `ASA_BASEDIR` 因此只在"第 1 步选中的那份文件恰好没写 basedir 字段"时才生效，
   不是脱离第 1 步单独比较的第四档。

   **警告提示**：第 3 步一触发就立刻打警告，不用等调用方后续用上这个 BaseDir 才
   提示——`Load` 自己在兜底那一刻就已经知道最终选中的目录是什么，没有理由拖到别处
   才说。警告文案是硬性要求的一部分，**必须把实际选中的那个目录路径写进去**，不能
   只是一句"BaseDir 解析异常"就完事——用户看到警告，得立刻知道数据到底落在哪个
   目录，不用再去翻代码或者猜：
   `logger.WithConsole().Warnf("BaseDir 未能从 config.yaml/环境变量解析出来，"+
   "已回落到 %s，数据将存放在这个目录，请检查 config.yaml 的 basedir 字段或 "+
   "ASA_BASEDIR 环境变量是否配置正确", fallbackDir)`。
   选直接在 `Load` 里打日志，而不是让它返回一个额外的"是否兜底"标志、交给 `main.go` 决定怎么提示，
   是因为 `pkg/logger` 本来就是零依赖、全项目唯一日志入口，`init()` 里已经准备了
   一个"`InitLoggerWithBaseDir` 调用之前也能安全用"的纯控制台兜底 logger（见
   `pkg/logger/logger.go` 包注释）——这正是这个场景：警告发生在 BaseDir 还没解析
   出来、文件日志系统根本没法初始化的最早期，`WithConsole()` 保证它不看 `SetLevel`
   阈值、一定能在控制台露出来，不会被静默吞掉。这会让 `appconfig` 从"只用标准库
   和 viper"的叶子包变成额外依赖 `pkg/logger`——可以接受，`pkg/logger` 本身也是
   零 `internal/` 依赖的叶子包，不引入环；如果之后觉得连这点依赖也不该加，退路是
   把"是否触发了第 3 步兜底"作为 `Load` 的第三个返回值交给 `main.go` 自己打日志，
   但这会让每个调用方都要记得处理这个新返回值，本文档默认选前一种。
4. **`cfgpkg.EnsureDirectories` 不再自行解析 BaseDir，只接受调用方已经解析好的
   `baseDir` 参数。** 去掉它内部"BaseDir 为空时读 `ASA_BASEDIR`/退回 exe 同级目录"
   的兜底逻辑——BaseDir 的解析已经完全是 `appconfig.Load()` 的职责（上面第 3 条
   那一整套算法），`EnsureDirectories` 不应该再有第二套、逻辑更简陋的解析规则
   同时存在，那是重复权威、容易和 `Load()` 的结果对不上。

## 3. API 设计

```go
package appconfig

// Load 定位并加载 config.yaml，返回解析出的 BaseDir，完整算法见 §2 第 3 条：
//
// 第一步，确定读哪一份 config.yaml（完整覆盖，不合并）：
//  1. 环境变量 ASA_CFG 指定的目录
//  2. 可执行文件同级目录
//  3. 系统固定目录（Windows %ProgramData%\ASAServerManager，Linux /etc/asa-server）
//  4. 都没有 → 在可执行文件同级目录生成一份默认模板
//
// 第二步，只看第一步选中的那一份文件的 basedir 字段：
//  1. 字段非空 → 就是 BaseDir
//  2. 字段为空 → 环境变量 ASA_BASEDIR 非空则用它，否则用这份 config.yaml 所在的目录
//
// 第三步，纯防御性兜底（正常输入下不会触发）：上面两步走完 BaseDir 仍为空，
// 回落到可执行文件同级目录（拿不到时再退到当前工作目录），并用
// logger.WithConsole().Warnf 打一条启动警告。
func Load() (baseDir string, err error)

// EnsureDirectories 建 BaseDir 下的标准子目录（instances/server-files/steamcmd/
// backups）。baseDir 必须是调用方已经解析好的值（通常是 appconfig.Load() 的返回
// 值），这个函数自己不再做任何解析或兜底——BaseDir 权威只有一处。
func EnsureDirectories(baseDir string) error
```

不再有 `resolveConfigDir`。三级查找的三步直接写成 `Load` 内部的私有步骤（可以是
`Load` 内联的代码，也可以拆成几个不接收目录参数、不对外导出的小函数，比如
`locateExeDir() / locateSystemDirIfHasConfig()`——只要它们不接受"外部给一个目录"
这种输入即可，怎么拆纯粹是实现细节，不是本文档要锁定的接口）。`ASA_CFG` 本身是
直接 `os.Getenv("ASA_CFG")` 读取，不经过 viper，不会有下面这段提到的环境变量
命名碰撞问题。

`basedir` 字段依然要单独用一个不开 `AutomaticEnv` 的 viper 实例重读（上一版已经
踩过这个坑：字段名 `basedir` 加上 viper `AutomaticEnv` 的前缀规则，正好拼成
`ASA_BASEDIR`，会被同名环境变量污染，见 `docs/LINUX_COMPATIBILITY_PLAN.md` §10.5
那条"落地时发现一个真 bug"的记录）。这部分逻辑保留，不受本次改动影响。

## 4. 测试怎么控制三级查找

`ASA_CFG` 这一级不需要额外的测试钩子——它本来就是一个环境变量，测试直接
`t.Setenv("ASA_CFG", tmpDir)` 就能精确指向临时目录，`t.Setenv` 还自带用完自动
还原，比任何自建的 override 机制都省事。`internal/webapi/authapi/middleware_test.go`
这类外部包的测试，以前靠 `Load(dir)` 的目录参数达到的效果，现在改用
`t.Setenv("ASA_CFG", dir)` + `Load()` 就能等价拿到，不需要再引入别的钩子。

真正还需要钩子的只剩 exe 同级 / 系统固定目录这两级——`os.Executable()` 在
`go test` 下返回的是测试二进制自己的路径，没法把 `config.yaml` 摆在那儿；系统
固定目录同理不该在跑测试的机器上真的读写 `%ProgramData%`/`/etc/asa-server`。
上一版用的是包内私有函数变量（`executableDirFn` / `systemConfigDirFn`），只有
`appconfig` 包自己的测试能用，跨包测试用不了。

新增一个明确标注"仅供测试使用"的导出函数：

```go
// OverrideSearchDirsForTest 仅供测试使用：临时把两级查找指向给定目录，
// 返回一个还原函数。生产代码不会、也不应该调用它。
func OverrideSearchDirsForTest(t testing.TB, exeDir, systemDir string)
```

（用 `testing.TB` 而不是手工返回 restore 函数，让它能直接调用 `t.Cleanup` 自动
还原，调用方不需要自己记得清理；也让"仅测试可调"这件事在类型签名上就体现出来。）

`appconfig` 包内部的测试可以继续直接换 `executableDirFn`/`systemConfigDirFn`，
也可以统一改用这个导出函数——为减少两套机制并存的心智负担，本次落地时统一改用
`OverrideSearchDirsForTest`，包内 `withDirs` 测试 helper 直接调它。

## 5. 受影响的调用方

| 位置 | 现状（上一版） | 改成 |
|---|---|---|
| `main.go` `loadAppConfig()` | `appconfig.Load(os.Getenv("ASA_BASEDIR"))`，且专门写注释解释"为什么不传" | `baseDir, err := appconfig.Load()`；删掉那段解释性注释（不再有可传可不传的选择，无需解释） |
| `main.go`（调 `EnsureDirectories` 的地方） | `cfgpkg.EnsureDirectories()`（内部自行读 `cfgpkg.BaseDir` 包级变量） | `cfgpkg.EnsureDirectories(baseDir)`，用上面 `Load()` 返回的值；`main.go` 自己再把 `cfgpkg.BaseDir = baseDir` 赋值一次给其余读这个包级变量的调用方用 |
| `internal/appconfig/config.go` | `resolveConfigDir` 独立函数 + `Load(explicitDir string)`；包注释宣称"只用标准库和 viper" | 内联三级查找逻辑进 `Load`，签名改为 `Load()`；新增 `ASA_CFG` 这一级；新增 `import "asa-server/pkg/logger"`，第三步兜底触发时打警告；同步更新包顶部注释，"只用标准库和 viper"改成"只用标准库、viper 和 `pkg/logger`" |
| `internal/config/config.go` `EnsureDirectories()` | 无参，内部读 `ASA_BASEDIR`/退回 exe 同级目录做兜底解析 | `EnsureDirectories(baseDir string) error`，删掉内部兜底解析，`baseDir` 是必须的入参 |
| `internal/config/config_test.go`（`init()`） | `EnsureDirectories()` 无参调用，靠自解析 | 显式传一个 baseDir（沿用测试原先依赖的环境变量值，比如 `os.Getenv("ASA_BASEDIR")`，这类测试本来就是环境耦合的，见 `CLAUDE.md` 的既有说明，这里只做签名层面的适配，不改它的环境耦合性质） |
| `internal/instance/common_test.go` | `cfgpkg.EnsureDirectories()` 无参调用 | 同上，显式传 baseDir |
| `internal/appconfig/config_test.go` | 用 `Load(dir)` 把 `dir`（`t.TempDir()`）当 config 目录 | 改用 `t.Setenv("ASA_CFG", dir)` 后调 `Load()` |
| `internal/appconfig/basedir_test.go` | 部分用 `withDirs`（包内私有变量），部分用 `Load(explicit)` | exe 同级/系统固定目录相关用例改用 `OverrideSearchDirsForTest`；原来测"explicitDir 绕过查找"的用例改写成测 `ASA_CFG`（`t.Setenv`）优先级最高、且不绕过 basedir 字段的取值优先级——`ASA_CFG` 只管定位文件，不改变文件里 `basedir` 字段依然是 BaseDir 取值最高权威这件事 |
| `internal/webapi/authapi/middleware_test.go` | `appconfig.Load(dir)` | `t.Setenv("ASA_CFG", dir)` 后 `appconfig.Load()` |

## 6. 验收判据

1. `grep -rn "resolveConfigDir\|func Load(\|func EnsureDirectories(" internal/appconfig
   internal/config` 只能找到 `func Load() (string, error)` 和
   `func EnsureDirectories(baseDir string) error`，找不到 `resolveConfigDir`，
   也找不到 `EnsureDirectories()`（零参版本）。
2. 单元测试证明 BaseDir 取值优先级：文件 `basedir` 字段 > `ASA_BASEDIR` 环境变量 >
   config.yaml 所在目录——三档都要有测试覆盖到"赢了"和"没设置时轮到下一档"两种
   情况（这部分沿用上一版已经写好的用例，行为不受本次 `ASA_CFG`/`EnsureDirectories`
   改动影响，只需要跟着签名改动同步适配调用方式）。
3. 单元测试证明"定位 config.yaml"的三级查找 + 完整覆盖：
   - `ASA_CFG` 非空时优先于 exe 同级与系统固定目录，即使后两者也存在
     `config.yaml` 且内容不同。
   - 没设 `ASA_CFG` 时，exe 同级与系统固定目录**同时**存在 `config.yaml` 且内容
     不同（比如端口不一样）时，最终生效的是 exe 同级那份的**全部**字段，系统
     固定目录那份没有任何字段渗透进来。
   - `ASA_CFG` 只决定"去哪儿找文件"，不改变找到文件后 `basedir` 字段仍是 BaseDir
     取值第一优先级这件事——用一个 `ASA_CFG` 指向的目录、文件里写了 `basedir`
     字段的用例验证这一点。
4. 单元测试证明第 3 步防御性兜底：用 `OverrideSearchDirsForTest` 之类的钩子让
   "exe 同级目录"这一档解析本身失败（模拟 `os.Executable()` 报错），确认最终
   BaseDir 落到可执行文件同级目录（或再退一步的当前工作目录），且能观察到一条
   通过 `logger.WithConsole()` 打出的 Warn 级别日志（可以用 zap 的 observer core
   或类似机制断言日志内容）。断言不能只测最终 BaseDir 值、漏了"有没有警告"这半条
   判据；还要断言日志文本里**包含最终选中的那个 BaseDir 路径**，不是只测"确实打了
   一条 Warn"——警告的核心价值就是让用户不用猜数据落在哪，光有警告没有路径不达标。
5. 真实编译产物端到端验证，且要吸取上一次验证的教训（见 §7）：
   - exe 同级放一份 `basedir` 指向目录 A 的 config，同时设置
     `ASA_BASEDIR` 指向目录 B → 数据落在 A，B 保持空。
   - exe 同级放一份 `basedir` 留空的 config，设置 `ASA_BASEDIR` 指向目录 B →
     数据落在 B。
   - exe 同级与系统固定目录都放 config（端口不同）→ 只有 exe 同级那份端口生效。
   - 设置 `ASA_CFG` 指向目录 C（C 下没有 config.yaml），exe 同级也放了一份 →
     实际生效、被生成默认模板的是 C，exe 同级那份原封不动、不被读取。
6. `go build ./...`、`GOOS=linux go build ./...`、`go vet ./...`、
   `go test ./internal/appconfig/... ./internal/webapi/authapi/... ./internal/config/...`
   `./internal/instance/...`（后两个只验证编译通过，运行结果本就环境耦合，见 §5）
   全部通过。

## 7. 上一次真机验证的事故记录（验证时的操作规范）

上一轮做端到端验证时，为了清理测试残留进程，执行了
`Get-Process asa-server | Stop-Process -Force`——按**进程名**批量匹配，误杀了一个
不相关的、当时已经跑了将近一天的真实 `asa-server.exe` 实例（`D:\golang\asa-server\
asa-server.exe`，PID 592，非本次测试启动，使用用户自己的 `ASA_BASEDIR=E:\
asa_server_data`）。同时脚本里路径拼接也出过错（Git Bash 下用 `$SCRATCH` 变量拼出
的是 POSIX 风格路径 `/c/Users/...`，喂给 Windows 二进制解析成了完全不同的位置），
导致第一轮验证结果本身也不可信。

本次重新验证时的规则：

1. **绝不用进程名做批量匹配/清理**。测试进程用完整路径或 `Start-Process` 返回的
   `$p.Id` 精确匹配、精确停止；验证前后都不执行不带过滤条件的 `Get-Process
   <程序名> | Stop-Process`。
2. **路径一律用 PowerShell 原生构造**（`Join-Path` 或 `"$var\sub"` 插值），不要在
   Bash 里拼路径再传给 Windows 二进制——两边的路径风格不兼容，Bash 的 POSIX 路径
   对 Windows 程序而言不是一个合法的绝对路径，会被解析成完全出乎意料的位置。
3. 验证脚本执行前先确认目标临时目录下**没有**已经在跑的旧进程（按完整可执行文件
   路径查，不按名字），而不是执行完了才想起来要清理。

---

# Part 2：移除 `ASA_BASEDIR` 环境变量

> 日期：2026-10-02　分支：`fix/audit-batch3`（基线 `f44c13d`）
> 状态：**✅ 已实施**（2026-10-02，分支 `refactor/remove-asa-basedir`），实施记录与偏离见 P2-7。
> 2026-10-02 确认：`Test_SetMessageOfTheDay` 用例本体不动、出错回落修正要做、追加「启动前校验：配置文件必须存在且有效」
> （P2-3 第 6 条，同时推翻「api 不需要事先存在的配置」）、6.1 的例外清单。
> 起因：`docs/TEST_ENV_COUPLING_PLAN.md` 的排查发现，作者本机的系统级 `ASA_BASEDIR`
> 会让测试的行为跟着机器变（T1、T2）。`ASA_BASEDIR` 本身是遗留配置：Part 1 之后数据目录
> 的权威已经在 `config.yaml` 的 `basedir` 字段里；SETUP_FLOW Part 2 之后，`config init
> --basedir` 与 GUI 向导都直接把它写进文件。留着这个变量，只是多了一个看不见的输入。

## P2-1. 目标

1. **`ASA_BASEDIR` 不再参与任何 BaseDir 解析。** Part 1 §2 第 3 条第 2 步改为：

   ```
   2. 读到这一份 config.yaml 后，取它的 basedir 字段决定 BaseDir：
        字段非空 → BaseDir = 字段值
        字段为空 → BaseDir = 这份 config.yaml 所在的目录
   ```

   第 1 步（`ASA_CFG` > exe 同级 > 系统固定目录）与第 3 步（防御性兜底）不变。
2. **防止后门复活。** `decodeFile` 开着 viper `AutomaticEnv`，`basedir` 这个键天然对应
   `ASA_BASEDIR`。现在靠 `fileOnlyBaseDirAt` 用一个不开 `AutomaticEnv` 的 viper 重读来
   挡住它；这一步**必须保留**，注释里的理由从「两者优先级不同」改成「这个变量已经移除，
   不能经 viper 的自动映射复活」，并用单测钉住（P2-4）。
3. **升级时不能悄悄换数据目录。** 移除之后，原本「字段为空 + 设了 `ASA_BASEDIR`」的部署，
   数据目录会从环境变量指的目录变成配置文件所在目录：面板上的实例全部「消失」，看起来
   像数据丢了。所以检测到这个变量仍然设着时，要明确告诉用户：它已经不生效，现在实际
   用的是哪个目录，要沿用原来的目录该在配置文件里写哪一行（P2-3 第 2 条）。
4. **不改其余 `ASA_*`**：别的配置项照旧走「flag > 环境变量 ASA_* > 文件 > 默认值」。
   `ASA_CFG` 管的是「去哪儿找配置文件」，与本次无关，保留。
5. **（2026-10-02 追加）启动必须有一份有效的配置文件**：找不到或校验不通过就不启动，不再回落默认配置，
   `api` / 服务模式也不再自动生成配置。见 P2-3 第 6 条。

## P2-2. 现状：`ASA_BASEDIR` 出现在哪里

| 位置 | 用途 |
| --- | --- |
| `internal/appconfig/config.go:471-478` `resolveBaseDirValue` | 唯一真正读取它的解析逻辑 |
| `internal/appconfig/config.go:362` | 配置解析失败时的回落值，同样经 `resolveBaseDirValue` |
| `internal/appconfig/config.go:490-505` `fallbackBaseDir` | 警告文案「请检查 basedir 字段或 ASA_BASEDIR 环境变量」 |
| `internal/appconfig/config.go:34-36`、`:325-329`、`:457-461` | `Config.BaseDir` / `Load` / `decodeFile` 的注释 |
| `internal/actions/configcmd.go:267-273` | `config init` 没给 basedir 时：设了变量就跳过「配置目录合不合适」的提示 |
| `internal/actions/configcmd.go:352-358` | `config init` 结果摘要里的「数据目录」 |
| `internal/actions/configcmd.go:437-442` | `config path` 打印数据目录的来源 |
| `internal/actions/configcmd.go:474-481` | `config validate` 打印数据目录 |
| `main.go:250-252` | `loadAppConfig` 的注释 |
| `internal/appconfig/basedir_test.go` | `clearASABaseDir`（被调用 18 处）+ 两条测试变量优先级的用例（`:157-190`） |
| `internal/actions/configcmd_test.go:26` | `newConfigEnv` 里清空它 |
| `internal/config/config_test.go:11-21` | 包级 `init()` 用它定位数据目录，供 `Test_SetMessageOfTheDay` 找 `ces99` |

GUI 向导、配置模板（`template_{zh,en}.go`）、svcmgr（服务环境变量只注入 `ASA_CFG`）、前端都**没有**用到它。

## P2-3. 设计

### 1. `appconfig`：删掉这一档

- `resolveBaseDirValue(fileBaseDir, dir)` 去掉环境变量分支，只剩「字段非空用字段，否则用 `dir`」。
  它只剩一行判断，直接内联进 `Load` 的两个调用点即可，函数删掉。
- **顺手修一处回落**（`config.go:362` / `:376`）：配置文件存在但校验失败时（比如 `auth.networks` 写错），
  `Load` 现在返回的是**配置文件所在目录**，即使文件里明明白白写着 `basedir: E:\data`。以前设了
  `ASA_BASEDIR` 的机器会被它兜住，移除之后这个保险就没了：配置一写错，api / 服务模式就会对着另一个
  空目录跑起来（`loadAppConfig` 现在出错时仍然继续运行）。第 6 条改成「配置坏了不启动」之后，这个值
  仍有两处要用：`config path` / `config validate` 要显示正确的数据目录，服务模式要把错误原因写进**这个**
  目录的日志。改成出错时用 `fileOnlyBaseDirAt(path)`：它用
  单独的 viper 只读 `basedir` 这一个键，`auth` 写错不影响它；只有 YAML 语法本身坏了才拿不到，
  那时再回落到配置目录。这一条不改变任何正常输入下的结果。
- `fallbackBaseDir` 的警告文案去掉「或 ASA_BASEDIR 环境变量」。
- 注释同步：`Config.BaseDir`、`Load` 的包文档、`decodeFile` 里 `fileOnlyBaseDirAt` 那段（理由按 P2-1 第 2 条改写）。

### 2. 检测到遗留变量时的提示

在 `appconfig` 里加一个小函数，作为「这个变量还在不在」的**唯一**判断点：

```go
// LegacyBaseDirEnv 报告已移除的 ASA_BASEDIR 是否仍被设置。它不参与任何解析，
// 只用来提示用户：这个变量已不生效，数据目录以 config.yaml 为准。
func LegacyBaseDirEnv() (value string, set bool)
```

调用方只有三处，文案都写明「已不生效 + 当前实际数据目录 + 要沿用原目录就在哪个文件里写哪一行」：

| 调用方 | 时机 | 行为 |
| --- | --- | --- |
| `main.go` `loadAppConfig` | 每次启动（含 api、服务、GUI） | `logger.WithConsole().Warnf`。变量值与实际 BaseDir **不同**时，追加「原来的数据很可能在 `<变量值>`，请在 `<配置文件路径>` 里写 `basedir: "<变量值>"`」；相同时只说「可以删掉这个环境变量」 |
| `asa-server config path` | 用户主动查看 | 在「数据目录：…（来源：…）」下方多打一行同样的提示 |
| `asa-server config validate` | 同上 | 同上 |

只提示、**不阻断**：阻断的话，环境变量就又成了能左右启动结果的输入，等于换个形式把它留下来。
`config init` 不需要提示：它总是显式写出 `basedir`（用户给了就写给的值，没给就留空 = 配置目录），
生成的文件本身就是答案。

这个提示函数与调用点打算**保留一个版本**，之后整段删掉（记进 CHANGELOG 的「后续移除」）。

### 3. `actions/configcmd.go`

- `:267-273`：去掉 `os.Getenv("ASA_BASEDIR") == ""` 这层条件，没给 basedir 时总是检查配置目录合不合适。
- `:352-358`、`:474-481`：数据目录 = 字段值，否则配置目录，删掉环境变量分支。
- `:437-442`：来源只剩「config.yaml 的 basedir 字段」/「配置文件所在目录」两种，下面接 P2-3 第 2 条的提示。

### 4. `internal/config/config_test.go` 的 `init()`

`Test_SetMessageOfTheDay` **本身不改**（2026-10-02 确认）。但它靠 `init()` 用 `ASA_BASEDIR` 找到数据目录，
变量移除后 `init()` 必须换一个来源。改成走和生产一样的配置：

```go
func init() {
	baseDir, _ := appconfig.Load(appconfig.WithoutAutoGenerate())
	SetDirectories(baseDir)
	logger.InitLoggerWithBaseDir(BaseDir)
}
```

- 不构成导入环：`appconfig` 只依赖 `pkg/fsutil`、`pkg/logger`，不依赖 `internal/config`。
- `WithoutAutoGenerate`：不在任何地方生成 `config.yaml`。第 6 条把 `Load` 改成只读后这个选项会被删掉，
  B4 里同步改成 `appconfig.Load()`。
- `SetDirectories` 而不是 `EnsureDirectories`：只设变量、不建目录。这样顺带解决了
  `TEST_ENV_COUPLING_PLAN.md` T2 在源码目录 `internal/config/` 下留空目录的问题。
- 后果：`go test` 下 exe 同级目录是测试二进制所在的临时目录，那里没有 `config.yaml`，所以
  **只有设了 `ASA_CFG` 的机器**才会找到真实数据目录，其余机器上 `Test_SetMessageOfTheDay` 照旧跳过。
  作者本机目前没设 `ASA_CFG`，要让这条用例继续跑，需要把 `ASA_CFG` 指向写了
  `basedir: "E://asa_server_data"` 的那份配置所在的目录（仓库根目录的 `config.yaml` 就是）。
  用例被跳过时，跳过信息会打出它找的路径，能看出原因。

### 5. 测试

`internal/appconfig/basedir_test.go`：

- `clearASABaseDir` 删掉，18 个调用点一并删除（变量不再有影响，无需清理）。
- `TestLoad_EnvASABaseDirFallsBackWhenFileFieldEmpty` 反转成 `TestLoad_ASABaseDirIsIgnored`：字段留空、
  `t.Setenv("ASA_BASEDIR", "/from/env")`，断言 BaseDir 是配置目录，**并且** `Get().BaseDir == ""`
  ——后者钉住 P2-1 第 2 条：viper `AutomaticEnv` 没有把它映射回来。再对 `CheckFile` 做同样的断言
  （`config validate` 走的是它）。
- `TestLoad_FileBasedirWinsOverEnvASABaseDir` 保留：字段照样赢，只改注释（不再是「优先级比较」，
  而是「设了也不影响」）。
- 新增 `TestLoad_InvalidConfigKeepsFileBasedir`：`basedir` 写了值、`auth.networks` 写坏，断言 `Load`
  返回错误，且 BaseDir 仍是文件里的值（P2-3 第 1 条的回落修正）。
- 新增 `LegacyBaseDirEnv` 的两条小用例（设了 / 没设）。

`internal/actions/configcmd_test.go`：`newConfigEnv` 去掉 `t.Setenv("ASA_BASEDIR", "")`。新增一条：
设了 `ASA_BASEDIR`，`config path` 与 `config validate` 的输出里数据目录不变，并且包含「已不生效」的提示。

### 6. 启动前校验：配置文件必须存在且有效（2026-10-02 追加）

**这一条推翻两个现有设计**，都是 2026-10-02 确认的：

1. **「配置写坏也照常启动」**。现在 `main.go` `loadAppConfig` 的规则是：加载失败时记一条 ERROR，然后用默认配置继续；
   唯一的例外是配置里写了 `auth.enabled: true`（`ErrAuthConfigInvalid` → `log.Fatalf`）。注释里的理由是：默认配置
   不开鉴权，配置写坏的最坏后果只是「没有鉴权」，不至于「谁都登不进来」。可这只考虑了鉴权：用默认配置继续，
   下载代理、端口、TLS、`linux.*` 运行时设置（降权用户、prefix 模式）、数据目录也一起被静默丢掉。用户看到的是
   「程序起来了，但行为不对」，原因只在日志里的一行 ERROR。
2. **「`asa-server api` 不需要事先存在的 config.yaml」**（`docs/LINUX_COMPATIBILITY_PLAN.md` §10.7 不变量 1、G5，
   以及 SETUP_FLOW Part 2「api / 服务模式缺失即自动生成」）。这是在 `config init` 出现之前的遗留：那时没有别的
   办法得到一份配置，只能启动时顺手生成。现在 `config init`、`setup`、Windows GUI 向导都能显式生成，api 启动时
   再悄悄在 exe 旁边写一份，只会让「到底读的是哪份配置、数据落在哪」更难说清。

**新规则：除了少数例外（下表），启动时找不到配置文件，或配置文件读不出来、校验不通过，就不启动。**

#### 6.1 哪些入口要求配置

| 入口 | 缺配置 | 配置无效 | 说明 |
| --- | --- | --- | --- |
| `api`（含 Linux 无参）、服务模式 | 拦 | 拦 | 本条的主体 |
| `service install` | 拦 | 拦 | 装的时候就发现，比装完服务起不来好 |
| `update`、`verify`、`verify-arkapi`、`arkapi-cache`、`netmon`、`cert install`、`db`、`user`、`state`、`perms`、`prefix` | 拦 | 拦 | 都要读写 BaseDir 下的数据，数据目录只能来自配置 |
| `setup`、Windows GUI | 进入各自的生成流程（不变） | 拦 | 二者本来就负责在缺配置时生成。配置已在但写坏时，`setup` 现在会「沿用当前数据目录」带着默认配置去下载几十 GB，所以要拦 |
| `service remove` / `stop` / `start`、`cert uninstall` | 不拦 | 不拦 | 恢复用的命令：配置坏了也必须能把服务停掉、卸掉。它们不生成配置、不建目录 |
| `config` 子命令、帮助、版本 | 不拦 | 不拦 | `config validate` / `config path` 就是用来报告配置问题的 |

最后两行合并为「维护模式」：`startupMode` 现有的 `readOnly` 语义（不生成、不建目录、出错不中止）正好合适，
`startupModeFor` 扩成能识别 `service remove|stop|start` 与 `cert uninstall` 这两层命令（现在只看顶层命令名）。

> ⚠️ 例外清单是本节唯一需要再确认的地方：`service start` 放进「不拦」是因为它只是叫 SCM / systemd 去启动服务，
> 服务进程自己会按服务模式再校验一次；`cert uninstall` 只按指纹删系统信任存储里的证书。

#### 6.2 `appconfig.Load` 变成只读

自动生成没有调用方了（`main.go` 是唯一用过它的地方），所以 `Load` 不再写文件：

- 删掉 `writeDefaultConfig`、`WithoutAutoGenerate` 与 `loadOptions.noAutoGenerate`。现有的 `Load(WithoutAutoGenerate())`
  调用点（`bootstrap.Reload`、`actions` / `appconfig` 的测试、本节第 4 条的 `config_test.go` `init()`）改成 `Load()`。
  生成配置的唯一入口是 `InitConfig`（本来就是，见 SETUP_FLOW Part 2）。
- `startupMode.autoGenerate` 删掉，`defaultStartup` 改为「要求配置存在」。
- 新增哨兵 `appconfig.ErrConfigInvalid`，`decodeFile` 的三种失败（读文件、解析、`Validate`）都包上它。
  `ErrAuthConfigInvalid` 被它完全覆盖，`wrapIfAuthWanted` 与这个哨兵一起删掉；`config_test.go:149`、`:172` 的断言
  改成 `ErrConfigInvalid`。「鉴权开着时配置写坏也不能无鉴权启动」由新规则自动满足。
- 定位失败（`os.Executable()` 报错）依旧走第 3 步兜底，但它意味着配置找不到，按「缺配置」处理。

判断写成纯函数 `startupConfigBlocks(mode startupMode, missing bool, err error) bool`，放在 `startup.go`，在
`startup_test.go` 里按 6.1 的表逐行单测。

#### 6.3 怎么告诉用户

缺配置时的文案要列出查找过的三个位置（与 `config path` 同源，取 `appconfig` 已有的三级目录信息），给出下一步：
`asa-server config init`（只生成配置）或 `asa-server setup`（生成并安装）。配置无效时给出文件路径、错误原文，以及
「修正后重试；可运行 `asa-server config validate` 复查」。

| 形态 | 渠道 |
| --- | --- |
| 终端里的命令 | stderr |
| 服务模式（Linux） | stderr 进 journal。配置无效、但 `basedir` 读得出来时（第 1 条的回落修正），另外初始化文件日志并写进 `{basedir}/logs/asaServer.log` |
| 服务模式（Windows） | stderr 没人看。写 Windows 事件日志：kardianos 的 `service install` 已经用服务名注册了事件源（`eventlog.InstallAsEventCreate`），这里用 `golang.org/x/sys/windows/svc/eventlog.Open(ServiceName)` 记一条 Error。文件日志同上 |
| Windows GUI（双击运行） | 没有控制台，直接退出等于闪退。配置无效时用 `golang.org/x/sys/windows.MessageBox` 弹原生错误框（缺配置时 GUI 走向导，不会到这里）。放在 `main_windows.go`，Linux 侧空实现 |

**退出码统一 78**（`EX_CONFIG`，与 `exitRuntimeUserUnsatisfied` 相同）。Linux 的 systemd unit 已经带
`RestartPreventExitStatus=78`（`internal/svcmgr/service_linux.go`），服务直接进入 `failed`，不会每隔几秒重启一次。
Windows SCM 没有配置失败恢复动作，服务停在「已停止」。

为区分 GUI 与 `setup`（现在都是 `firstRunStartup`），`startupMode` 加一个 `gui bool`，由 `startupModeFor`
在 Windows 无参与 `gui` 命令时置上。`loadAppConfig` 的文档注释整段重写，写明为什么从「回落默认值」改成「不启动」。

#### 6.4 测试的连带修改

- `internal/appconfig`：`TestLoadCreatesTemplateWhenMissing`、`TestGeneratedTemplateIsLoadable` 测的是自动生成，改成对
  `InitConfig` 的产物断言（生成的模板能被 `Load` 读、默认值正确）；`TestLoadWithoutAutoGenerate` 改名为
  「缺配置时 `Load` 不落盘、返回 `ConfigMissing`」。
- `internal/webapi/authapi/middleware_test.go` 的 `setupEnv` 已经先写配置再 `Load()`，不用改。
- `startup_test.go`：6.1 表的每一行，外加 `service remove` / `cert uninstall` 的两层命令识别。

### 7. 文档

- 本文 Part 1 不改，§0 已加指向本节的说明。
- `docs/SETUP_FLOW_OPTIMIZATION_PLAN.md` P2-3.3（`config path` 的来源列表）与 P2-9 第 8 条、
  `docs/LINUX_COMPATIBILITY_PLAN.md` §10.3 / §10.5（「`ASA_BASEDIR` 不删」）：原文不动，各加一行
  「2026-10-02 已移除，见 APPCONFIG_BASEDIR_PLAN.md Part 2」。
- 第 6 条推翻的「api 不需要事先存在的配置」：`docs/LINUX_COMPATIBILITY_PLAN.md` §10.7 不变量 1 与 G5、
  `docs/SETUP_FLOW_OPTIMIZATION_PLAN.md` Part 2（P2-3.2 的启动模式表）同样原文不动、加一行指向本节；
  `docs/LINUX_DEPLOYMENT.md` 是给用户看的操作手册，**直接改**：§2 里「无参数等价于 `asa-server api`」之后补一句
  「需要先有配置文件，见 2.1」，2.1 写明 `api` / 服务不再自动生成配置。`README.md` / `README_zh.md` 的快速开始
  若有「直接运行 api」的写法，同样改成先 `config init`。
- 项目 `CLAUDE.md`：`appconfig` 条目里「api 与服务模式缺失时仍自动生成」「`Load(WithoutAutoGenerate())`」、
  启动引导那段「api / 服务模式保持缺失即自动生成（§10.7 不变量 3）」以及运行时目录树里 `config.yaml` 的注释，按新规则改写。
- `docs/TEST_ENV_COUPLING_PLAN.md`：T2 / T3 改为引用本节。
- `CHANGELOG.md`：三条。①移除 `ASA_BASEDIR`：受影响的部署（字段为空 + 设了变量）、启动时会看到的提示、
  一行修法，以及「提示本身将在下个版本删除」；②配置文件无效时不再以默认配置启动，退出码 78；
  ③`api` / 服务模式不再自动生成配置，全新部署先 `config init` 或 `setup`。

## P2-4. 分步实施

| 步 | 内容 |
| --- | --- |
| B1 | `appconfig`：删掉环境变量这一档、修正出错时的回落、`LegacyBaseDirEnv`、注释与警告文案 |
| B2 | `main.go` 启动提示；`actions/configcmd.go` 四处 + `config path` / `config validate` 的提示 |
| B3 | 测试：`basedir_test.go`、`configcmd_test.go` 按 P2-3 第 5 条改；`internal/config/config_test.go` 的 `init()` 按第 4 条改 |
| B4 | 启动前校验（P2-3 第 6 条）：`Load` 只读化（删 `writeDefaultConfig` / `WithoutAutoGenerate` / `startupMode.autoGenerate`）；`ErrConfigInvalid` 替换 `ErrAuthConfigInvalid`；`startup.go` 的 `startupConfigBlocks`、`startupMode.gui`、两层命令识别；`loadAppConfig` 的拦截与告知渠道（stderr / 文件日志 / Windows 事件日志 / 错误框）；`main_{windows,linux}.go`；对应单测 |
| B5 | 文档、`CLAUDE.md` 与 CHANGELOG（P2-3 第 7 条） |

B1 到 B3 一个提交（移除 `ASA_BASEDIR`），B4 一个提交（启动前校验），B5 一个提交。B4 依赖 B1 的回落修正
（服务模式要把错误写进 `basedir` 指的那个目录），所以顺序不能颠倒。

## P2-5. 验收

1. `grep -rn ASA_BASEDIR --include=*.go .` 只剩 `LegacyBaseDirEnv` 的实现、它的单测和 `TestLoad_ASABaseDirIsIgnored`。
2. Windows：`go build ./...`、`go vet ./internal/... ./pkg/...`、`go test -race ./internal/... ./pkg/...`（PowerShell），
   分别在「`ASA_BASEDIR` 为空」与「`ASA_BASEDIR=E:\nonexistent`」两种环境下各跑一遍，结果必须相同。
3. WSL：`go build ./...`、`go vet ./...`、`ASA_TEST_RUNTIME_USER=1 go test -race ./...`。
4. `GOOS=linux CGO_ENABLED=0 go build ./...`。
5. 源码目录无残留：测试前后 `git status --short --ignored` 相同，`internal/config/` 下不再出现运行时目录。
6. 真实编译产物（按 Part 1 §7 的规范：只按完整路径 / PID 操作进程，路径用 PowerShell 构造）：
   - exe 同级放一份 `basedir` 留空的配置，设 `ASA_BASEDIR` 指向目录 B → 数据落在 exe 同级目录，B 保持空，
     控制台出现「已不生效 … 请写 `basedir: "B"`」；
   - 同样的配置写上 `basedir: B`，变量也设成 B → 数据落在 B，提示只说「可以删掉这个环境变量」；
   - `basedir: B` 加上写坏的 `auth.networks` → `asa-server api` 打出配置文件路径与错误、退出码 78、不监听端口；
     `asa-server config path` 显示的数据目录仍是 B；
   - `config path` / `config validate` 在第一种情形下都打出提示。
7. 启动前校验（P2-3 第 6 条）：
   - `startupConfigBlocks` 单测按 6.1 的表逐行覆盖（缺配置 / 配置无效 × 每类入口）；
   - `grep -rn 'WithoutAutoGenerate\|writeDefaultConfig\|ErrAuthConfigInvalid' --include=*.go .` 无结果；
   - 真实产物：全新目录里 `asa-server api` 打出三个查找位置与「先运行 `config init`」，退出码 78，**exe 旁边
     没有生成 config.yaml、也没有建任何目录**；配置坏时 `asa-server setup` 在下载任何东西之前就退出；
     配置坏时 `asa-server service stop` / `service remove` 照常可用；Windows 上配置坏时双击 exe 弹出错误框，
     关闭后进程退出；缺配置时双击 exe 照旧打开首次设置向导；
   - Windows 服务：装好后把配置改坏、重启服务，事件查看器「Windows 日志 → 应用程序」里有一条以服务名为来源的 Error；
   - WSL：装成 systemd 服务后把配置改坏，`systemctl restart`，确认单元进入 `failed`、`journalctl` 与
     `{basedir}/logs/asaServer.log` 里都有原因，且不会反复重启。

## P2-6. 兼容性与风险

- **受影响的部署**：只有「配置文件里 `basedir` 为空、靠 `ASA_BASEDIR` 指定数据目录」这一种。
  `config init`、GUI 向导、`setup` 生成的配置都显式写了 `basedir` 或者本来就用配置目录，不受影响。
- **作者本机**：系统级 `ASA_BASEDIR=E:\asa_server_data`。核对过两份配置：仓库根目录的 `config.yaml`
  写了 `basedir: "E://asa_server_data"`；`E:\asa_server_data\config.yaml` 没写，但它本身就在
  `E:\asa_server_data`，回落到配置目录的结果相同。所以两种启动方式的数据目录都不变，只会多一条
  「可以删掉这个环境变量」的提示。实施后可以把这个系统环境变量删掉（需要管理员权限，由用户自己操作）。
- **服务模式**：Windows 服务以 LocalSystem 运行，看得到系统级环境变量，提示会进服务日志；
  Linux 的 systemd unit 只注入 `ASA_CFG` 和 `HOME`，本来就不带 `ASA_BASEDIR`。
- **启动前校验是行为变化**：以前配置写坏还能「带着默认值跑起来」，现在起不来。对装成服务的部署，
  这意味着改坏配置后重启服务，服务就停了。这是有意的（第 6 条），但要写进 CHANGELOG，并提醒改配置后先跑
  `asa-server config validate` 再重启服务。
- **api 不再自动生成配置**：已有部署不受影响——以前每次启动都会在缺失时生成一份，所以跑过的机器上一定已经
  有 config.yaml。受影响的只有「全新解压、直接 `asa-server api`」与「部署脚本依赖首次启动生成配置」两种用法，
  前者会看到明确的提示，后者要在脚本里加一行 `asa-server config init`（CHANGELOG 写明）。
- **回滚**：B1 到 B3、B4 各是一个提交，可以分别 `git revert`；配置文件格式没有任何变化，回滚不需要迁移。

## P2-7. 实施记录（2026-10-02，分支 `refactor/remove-asa-basedir`，基于 `master` `5dda7b9`）

| 提交 | 内容 |
| --- | --- |
| `ae61535` | 计划文档（本 Part 2、`TEST_ENV_COUPLING_PLAN.md`、审计文档 §11.9 索引） |
| `07d6e3d` | B1–B3：移除 `ASA_BASEDIR` |
| `02f1c36` | B4：启动前校验配置 |
| （本提交） | B5：文档与 CHANGELOG |

### 与计划的偏离

1. **`LegacyBaseDirHint(baseDir, configPath)` 也是导出的**。计划只写了 `LegacyBaseDirEnv()`；三个调用方的文案完全一样，
   放在 `appconfig` 里写一份，免得 `main.go` 和 `actions` 各拼一遍。判断「变量还在不在」仍只有 `LegacyBaseDirEnv` 一处。
2. **`newConfigEnv` 仍然清空 `ASA_BASEDIR`**（计划说去掉）。变量已不参与解析，但设着时 `config path` / `validate` 会多一行提示，
   清掉它测试输出才不随开发机变；提示本身由新增的 `TestConfigPathAndValidate_LegacyBaseDirHint` 测。
3. **`config path` 的「数据目录来源」改为比较 `cfgpkg.BaseDir` 与配置目录**，不再看 `appconfig.Get().BaseDir`。真实产物验证时发现：
   配置无效时 `Get()` 是默认配置，来源会被误报成「配置文件所在目录」（数据目录本身是对的，来自 P2-3 第 1 条的回落修正）。
   同一处「程序会回落到默认配置运行」的提示改为「除 config 子命令与维护命令外，程序不会启动」。
4. **删除 `TestAutoGeneratedFileMatchesInitConfig`**（计划 6.4 没列）：它比较的是「`Load` 自动生成的文件」与 `InitConfig` 的产物，
   前者已不存在。BOM / CRLF 由 `renderTemplateFor` 的既有用例覆盖。
5. 多加了 `TestLoad_UnparsableConfigFallsBackToConfigDir`：YAML 本身坏掉时拿不到 `basedir`，钉住此时回落到配置目录。
6. 计划 6.1 的表里 `cert install` 归「拦」；`cert status` 没列，按默认同样拦（它读 `{BaseDir}/certs`）。
7. `startupMode` 新增的是 `gui`、`service` 两个字段与 `serviceStartup` / `guiStartup` 两个预设；`autoGenerate` 删除。
   `deferDirsIfMissing` 同时承担「缺配置时自己生成」的含义（只有 setup 与 GUI 有它），没有再加一个同义字段。

### 验证

- Windows：`go build ./...`、`GOOS=linux CGO_ENABLED=0 go build ./...`、`go vet`（改动包，两个 GOOS）、
  `go test -race ./internal/... ./pkg/... .` **全部通过**；改动包另在 `ASA_BASEDIR=E:\nonexistent` 下跑一遍，结果相同。
- WSL：`go build ./...`、`go vet`（改动包）、`ASA_TEST_RUNTIME_USER=1 go test -race ./...` **全部通过**。
- `Test_SetMessageOfTheDay`：没设 `ASA_CFG` 时跳过，跳过信息里是测试二进制临时目录下的路径；源码目录 `internal/config/`
  下不再新建运行时目录（原有的空目录是之前留下的，未删）。
- 真实编译产物（临时目录，未碰任何真实服务或数据目录）：
  - 全新目录 `asa-server api` → 打出三个查找位置与 `config init` / `setup`，退出码 78，**目录里除 exe 外什么都没生成**；
  - `basedir: B` + `server.port: 70000` → `api` 打出文件路径与错误、退出码 78；`config path` 显示数据目录 B、来源为 basedir 字段，
    并提示「程序不会启动」；`setup --non-interactive` 在下载任何东西之前退出，退出码 78；
  - `basedir` 留空 + `ASA_BASEDIR=B` → 数据目录建在配置所在目录，B 保持空，启动日志打出「已不再生效 … 请写 `basedir: "B"`」；
  - `basedir: B` + `ASA_BASEDIR=B` → 数据目录建在 B，提示只说「可以删掉这个环境变量」。

### 未验证（需要人工）

- **Windows GUI 配置无效时的错误框**：`MessageBox` 是模态阻塞的，没有在自动化里弹。
- **Windows 服务**：配置改坏后重启服务，事件查看器里出现以 `ASA-Server-Manager` 为来源的 Error、`{basedir}/logs/asaServer.log` 有原因。
- **配置无效时 `service stop` / `service remove` 照常可用**：本机装着真实服务，没有在这台机器上执行服务命令。
- **WSL systemd**：装成服务后把配置改坏，确认单元进入 `failed` 且不反复重启。
