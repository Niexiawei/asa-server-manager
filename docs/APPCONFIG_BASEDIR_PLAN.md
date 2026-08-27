# appconfig.Load / BaseDir 解析重新设计

> 独立成篇，不并进 `docs/LINUX_COMPATIBILITY_PLAN.md` §10.3/§10.5——那份文档定的是
> "两级查找 + basedir 字段"这个大方向，方向本身没变；本文档管的是这个方向下
> `appconfig.Load` 的**具体 API 形状与优先级细节**，纠正第一版实现里跑偏的部分。
> 第一版的问题与本次修正的关系，见下面 §1。

## 0. 状态

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
