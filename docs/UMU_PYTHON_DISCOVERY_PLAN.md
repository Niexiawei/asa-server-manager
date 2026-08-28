# UMU Python 解释器多版本探测与固定 — 开发计划

> 状态：**已实现**（PR1–PR4 一次性落地）。二期待办：§4 风险 #5 的降权 deep-probe 真跑解释器检查。
> 关联：`docs/LINUX_COMPATIBILITY_PLAN.md` §4.2（运行时依赖自检）、`docs/UMU_RUNTIME_USER_PLAN.md` §9 风险 10（降权运行）
> 影响范围：仅 Linux；Windows 无 Wine/Proton 运行时概念，本计划全部为 `//go:build linux`
>
> 落地文件：`internal/runner/python_linux.go`（新增，发现逻辑 + 缓存 + `runtimePython`）、
> `python_linux_test.go`（新增，13 个用例）、`preflight_linux.go`（`checkPython3` 改为调
> `pythonProblem`）、`runner_linux.go` `umuCommandLine`（`<python> <umu-run> <exe>`）、
> `umu_linux.go` `warmPrefix`、`runner_windows.go`（`runtimePython` 桩）、`runner.go`
> （`Config.PythonBin` + `RuntimePythonInfo`/`RuntimePython`）、`appconfig`
> （`LinuxConfig.UmuPythonBin` + 默认 + 校验 + template）、`main.go` / `internal/actions/setup.go`
> 装配、`webapi/systemapi` 响应加 `umuPython`。

---

## 1. 背景与问题

`internal/runner/preflight_linux.go` 的 `checkPython3()` 目前只做两件事：

```go
path, err := exec.LookPath("python3")          // 只找 PATH 里的 "python3"
out, _ := exec.Command(path, "-c",
    "import sys; print(sys.version_info >= (3, 10))").Output()
```

umu-launcher 的 zipapp 要求 **Python >= 3.10**。当前实现有两个兼容性缺陷：

1. **系统自带 `python3` 版本过低会直接卡死初始化。**
   RHEL 8 / CentOS 8（3.6）、Ubuntu 20.04（3.8）、Debian 11（3.9）等长期支持发行版，
   系统 `python3` 就是低于 3.10 的。此时 `checkPython3()` 返回 `python3-version`
   Problem，`asa-server` 启动自检不通过，实例无法初始化。

2. **强行升级系统 `python3` 有破坏系统的风险。**
   很多发行版的包管理器、`firewalld`、`dnf`/`apt` 辅助脚本等都绑定在系统自带的
   `python3` 上。替换 `/usr/bin/python3` 指向的版本可能导致系统组件崩溃。

社区（含 umu / Proton 相关项目）的通行做法是：**并行安装一个带版本号的解释器**
（如 `python3.14`），通过 `python3.14 xxx` 这种**显式版本名**调用，不动系统默认的
`python3`。用户反馈其车上服务器已用此方式装了 `python3.14`。

### 目标

- 依赖探测把范围从单一 `python3` 扩大到**一组带版本号的候选**：
  `python3`、`python3.10`、`python3.11` … `python3.14`（并对未来版本留冗余）。
- 机器上存在**多个**满足 `>= 3.10` 的解释器时，**选最高版本**。
- 探测到的解释器要被**固定下来**：之后所有 umu-run 调用（游戏启动、prefix 预热、
  安装校验）都用**同一个**解释器，而不是再走 zipapp 的 `#!/usr/bin/env python3`
  shebang（那会退回系统默认的低版本）。
- 提供配置项让用户**显式指定**解释器：
  - 一个系统解释器名字 / 路径（对应用户「指定 python3.14」的诉求）；
  - **也包括 venv / pyenv 的解释器路径**（如 `/opt/asa-venv/bin/python`、
    `~/.pyenv/versions/3.14.0/bin/python`）。
  - 一旦配置了这个项，就**完全跳过自动探测**，只认这一个。

### 非目标

- 不由 `asa-server` 去安装 Python（仍然是用户用发行版包管理器装）。
- **不自动发现 venv / pyenv 环境**。自动探测只扫系统级的 `python3` / `python3.10`…`python3.N`；
  venv / pyenv 必须由用户通过 `linux.umu_python_bin` **显式指定路径**才会被使用。
- 不改 Windows 任何行为。

---

## 2. 现状梳理（改造前）

### 2.1 umu-run 当前如何拿到 Python

`umu-run` 是一个 **zipapp**：文件头是 `#!/usr/bin/env python3` 的 shebang，后面跟 zip 数据。

- `internal/runner/umu_linux.go:151` `ensureUmu()` 只负责解压 + `chmod 0755`，不涉及 Python。
- `internal/runner/runner_linux.go:42` `run()` → `exec.CommandContext(ctx, bin, launchArgs...)`，
  `bin` 直接就是 `umu-run` 的绝对路径。**内核读 shebang → `/usr/bin/env python3`**，
  于是永远用系统默认 `python3`。
- `internal/runner/runner_linux.go:82` `runPTY()` 同理。
- `internal/runner/umu_linux.go:256` `warmPrefix()`：`exec.CommandContext(ctx, bin, "wineboot", "--init")` 同理。
- `internal/installer` 的 `VerifyServerInstallation` 走 `runner.Run()`，被上面覆盖。
- `internal/runner/runner_linux.go:101` `checkRuntime()`：纯文件系统存在性检查，**不 exec**，无需改。

要强制指定解释器，把调用形式从

```
exec.Command("/path/umu-launcher/umu-run", args...)
```

改成

```
exec.Command("/usr/bin/python3.14", append([]string{"/path/umu-launcher/umu-run"}, args...)...)
```

Python 的 zipapp 加载器会正常执行 zip 内 `__main__.py`；且 umu 内部 fork 子进程用的是
`sys.executable`，会**自动继承**我们选定的解释器，无需额外处理。

### 2.2 配置链路

- `internal/appconfig/config.go:145` `LinuxConfig`，`config.go:511` 默认值，
  `config.go:571` `v.SetDefault("linux.*", …)`，`internal/appconfig/validate.go:159` `(*LinuxConfig).validate()`。
- `internal/appconfig/template.go:147` 生成 `config.yaml` 的 `linux:` 段注释。
- `internal/runner/runner.go:166` `runner.Config`；`runner.go:245` `getConfig()` 补默认值。
- 装配点：`main.go:282` `runner.Configure(...)`（权威，含全部 linux 字段）；
  `internal/actions/setup.go:79`（**部分**字段，历史遗留）。
  `internal/gui/gui.go:435` 也有一处，但 **GUI 仅 Windows**（`CLAUDE.md`：Linux 无 GUI），
  且该文件是 `//go:build windows`，Linux 专属字段在那里没有意义 —— **不改 gui.go**。

### 2.3 自检对外暴露

- `internal/runner/runner.go:105` `Preflight() []Problem` → `preflight_linux.go:18` `preflight()`。
- `internal/webapi/systemapi/systemapi.go:32` 组装 `/api/system/preflight` 响应。

---

## 3. 设计

### 3.1 新文件 `internal/runner/python_linux.go`（`//go:build linux`）

集中解释器发现逻辑，供 `preflight_linux.go` 与 `runner_linux.go` / `umu_linux.go` 共用。

```go
package runner

// pythonInfo 是一个已解析、已验证 (>=3.10) 的 Python 解释器。
type pythonInfo struct {
    Path  string // 绝对路径（LookPath 结果，argv[0] 用它，与子进程 PATH 无关）
    Major int
    Minor int
}

func (p pythonInfo) Version() string { return fmt.Sprintf("%d.%d", p.Major, p.Minor) }
```

#### 候选名单（按优先级/版本从高到低扫描，实际选择仍按探测到的真实版本号排序）

```
python3.20 … python3.10        // 反向遍历 minor: 20 → 10（上界留足冗余）
python3                        // 发行版自带（可能是软链）
python                         // Arch 等把 python 指向 python3
```

- 上界取 `3.20`：写死一个够用的常量 `pythonMaxMinorProbe = 20`，避免无限扫描。
  新版本发布只需调这个常量（或用户走 3.3 的显式配置）。

#### 解析算法 `resolvePython() (pythonInfo, error)`

1. **显式配置优先**：`cfg := getConfig()`，若 `cfg.PythonBin != ""` —— **只认这一个，
   不做任何自动探测回退**：
   - 取值形态（都允许）：
     - 绝对路径：`/opt/asa-venv/bin/python`、`~/.pyenv/versions/3.14.0/bin/python`
       （`~` 先 `os.UserHomeDir()` 展开）；
     - 裸名字：`python3.14`（走 `exec.LookPath` 过 `PATH`）。
   - **不要求文件名长得像 `python3.x`** —— venv 里通常就叫 `python` / `python3`。
   - `exec.LookPath` 解析（绝对路径也过它，顺带校验存在且可执行）。
   - 跑版本探测脚本（下同），`>= 3.10` → 返回 `pythonInfo{Path: <解析出的绝对路径>}`；
     否则 **硬错误**。
   - 找不到 / 不可执行 → 硬错误，消息带上原始配置值。
   - **pyenv shim 注意**：`~/.pyenv/shims/python` 是个依赖 `PYENV_ROOT`/`PATH` 的
     分发脚本，直接 exec 行为不稳。文案里建议用户填 `versions/<x>/bin/python` 真实路径，
     而不是 shim。
   - **venv 说明**：venv 只是 `pyvenv.cfg` + 指回基础解释器的软链，用绝对路径直接
     调用即可正常工作；zipapp 在 venv 解释器下运行会带上 venv 的 site-packages，
     对 umu 无害。
   - **权限提醒**（写入 §4 风险表）：venv/pyenv 常在某个用户 HOME 下，降权运行时
     那个非 root 用户可能读/执行不到。
2. **自动探测**（仅当 `cfg.PythonBin == ""`）：对候选名单逐个 `exec.LookPath`：
   - 命中就跑 `exec.Command(path, "-c", pythonProbeScript)`，
     `pythonProbeScript = "import sys;print('%d %d'%sys.version_info[:2])"`。
   - 解析出 `(major, minor)`；`major==3 && minor>=10`（或 `major>3`）才是合格候选。
   - 用**解析后的真实路径 + 版本**去重（`python3` 软链到 `python3.11` 会命中两次）。
3. 合格候选按 `(major, minor)` 降序排序，取第一个：
   - 版本相同则**优先带版本号的名字**（`python3.14` 比裸 `python3` 稳定、可读）。
4. 一个合格的都没有 → 返回 `error`，消息里**列出实际探测到的名字→版本**，
   例如：`found python3 (3.9), python3.9 (3.9); need python3.10+`。

#### 缓存

- 进程内用 `sync.Mutex` 保护一个小缓存：`{overrideKey string, info pythonInfo, ok bool}`。
- **只缓存成功结果**，键为 `cfg.PythonBin`（override 变了就重解析，兼容 GUI 多次 `Configure`）。
- 失败不缓存：`/api/system/preflight` 的语义就是「用户装完 Python 点重试」，
  每次未解析时都重新扫描；一旦成功即冻结（setup 阶段扫 ~12 个 `LookPath` +
  少量 `python -c` 的开销可接受，成功后零开销）。

#### 对外辅助

```go
// pythonProblem 把 resolvePython 的错误转成 preflight 的 *Problem；成功则 nil。
func pythonProblem() *Problem

// umuInterpreter 返回用于执行 umu-run 的解释器；解析失败时返回 error，
// 调用方（umuCommandLine / warmPrefix）据此 fail-fast，不做 shebang 回退。
func umuInterpreter() (pythonInfo, error)
```

### 3.2 `internal/runner/preflight_linux.go`

- `checkPython3()` 整体替换为调用 `pythonProblem()`：

```go
func checkPython3() *Problem { return pythonProblem() }
```

- Problem 文案改进（在 `python_linux.go` 里构造）：
  - `Name`: 未找到任何 python → `"python3"`；找到但都 < 3.10 → `"python3-version"`；
    显式配置无效 → `"python3-config"`。
  - `Detail`: 带上探测到的清单，明确「系统自带的 `python3` **不用动**」。
  - `Fix`:
    `Debian/Ubuntu: sudo add-apt-repository ppa:deadsnakes/ppa && sudo apt install python3.12  |  `
    `RHEL/Alma/Rocky: sudo dnf install python3.12  |  Arch: sudo pacman -S python  |  `
    `装好后可与系统 python3 共存，无需替换；也可在 config.yaml 里 linux.umu_python_bin 显式指定`

### 3.3 `internal/runner/runner_linux.go` + `umu_linux.go`

在 `umuCommandLine()`（`umu_linux.go:123`）里把解释器包进去：

```go
func umuCommandLine(exePath string, args []string, opt Options) (bin string, launchArgs []string, env []string, err error) {
    if err := checkRuntime(); err != nil { return "", nil, nil, err }

    py, err := umuInterpreter()          // 新增：解析并固定解释器
    if err != nil {
        return "", nil, nil, err          // 硬错误，文案面向终端用户
    }
    cfg := getConfig()

    umuRun := umuRunPath(cfg)
    bin = py.Path
    launchArgs = append([]string{umuRun, exePath}, args...)
    // …env 组装不变…
}
```

- `run()` / `runPTY()` 本身不用改（它们已经用 `umuCommandLine` 的返回值）。
- **`Handle.LauncherPID` 语义不变**：现在它是 `python <umu-run>` 的 PID，
  仍然是 `Setsid` 后的 pgid；`launcherIsDirect()` 依旧 `false`；
  `procx.QueryProcess` 按 cmdline 找真实游戏进程的逻辑不受影响
  （`isExpectedProcess` 的 Linux 分支查 cmdline，见 `CLAUDE.md` process 包说明）。
- `warmPrefix()`（`umu_linux.go:256`）：

```go
py, err := umuInterpreter()
if err != nil { return fmt.Errorf("failed to resolve a Python interpreter for umu-run: %w", err) }
cmd := exec.CommandContext(ctx, py.Path, umuRunPath(cfg), "wineboot", "--init")
```

- 降权执行：系统解释器（`/usr/bin/python3.*`，`0755`）降权用户可执行，无影响。
  但用户若把 `linux.umu_python_bin` 指到某个 HOME 下的 venv/pyenv，降权用户可能读/执行不到 ——
  见 §4 风险表，`docs/UMU_RUNTIME_USER_PLAN.md` §9 也补一条。

### 3.4 配置项 `linux.umu_python_bin`

| 文件 | 改动 |
|---|---|
| `internal/appconfig/config.go` | `LinuxConfig` 加 `UmuPythonBin string \`mapstructure:"umu_python_bin"\``；默认段留空 `""` |
| `internal/appconfig/config.go` | `v.SetDefault("linux.umu_python_bin", "")` |
| `internal/appconfig/validate.go` | `(*LinuxConfig).validate()` 里 `l.UmuPythonBin = strings.TrimSpace(l.UmuPythonBin)`；**不 stat / 不展开 `~`**（validate 跨平台跑），真实校验（存在、可执行、`~` 展开、版本 >=3.10）留给 runner 在 Linux 上做 |
| `internal/appconfig/template.go` | `linux:` 段加注释（见下方样例） |
| `internal/runner/runner.go` | `runner.Config` 加 `PythonBin string`；注释说明：留空=自动探测系统 `python3.x`；非空=只用这一个，支持绝对路径 / 裸名字 / venv / pyenv 的解释器路径 |
| `main.go:282` | `runner.Configure` 增加 `PythonBin: cfg.Linux.UmuPythonBin` |
| `internal/actions/setup.go:79` | 同步加 `PythonBin`（顺带对齐，其它 Runtime* 字段缺失是既有问题，不在本计划范围） |
| ~~`internal/gui/gui.go:435`~~ | **不改** —— GUI 仅 Windows（`//go:build windows`），Linux 专属字段在那里无意义 |

`template.go` 注释样例：

```yaml
  # umu-run（zipapp）用哪个 Python 解释器执行。
  #   留空  : 自动探测系统解释器，扫 python3 / python3.10 … python3.20，多个则取最高版本
  #           （不会动系统默认的 python3，也不会自动发现 venv/pyenv）
  #   非留空: 只用这一个，不再自动探测。可填：
  #           - 裸名字，如  python3.14        （走 PATH）
  #           - 绝对路径，如 /usr/bin/python3.14
  #           - venv 解释器，如 /opt/asa-venv/bin/python
  #           - pyenv 版本解释器，如 ~/.pyenv/versions/3.14.0/bin/python
  #             （用 versions/<x>/bin/python 真实路径，不要用 ~/.pyenv/shims/python）
  #   要求 Python >= 3.10；降权运行时该解释器需能被降权用户读取/执行。
  umu_python_bin: ""

### 3.5 `/api/system/preflight` 暴露已解析解释器（可选，建议做）

方便前端在「环境就绪」页显示实际用的是哪个 Python。

- `internal/runner/runner.go` 加：

```go
type RuntimePythonInfo struct {
    Resolved bool   `json:"resolved"`
    Path     string `json:"path"`
    Version  string `json:"version"`
    Source   string `json:"source"` // "config" | "auto" | ""
}
func RuntimePython() RuntimePythonInfo { return runtimePython() }
```

- `python_linux.go` 实现 `runtimePython()`；`runner_windows.go` 加桩 `func runtimePython() RuntimePythonInfo { return RuntimePythonInfo{Resolved: true} }`。
- `internal/webapi/systemapi/systemapi.go` 响应 `Data` 里加 `"umuPython": runner.RuntimePython()`。

---

## 4. 边界与风险

| # | 场景 | 处理 |
|---|---|---|
| 1 | `python3` 软链到 `python3.11`，同时 `python3.11` 也在 PATH | 按真实路径+版本去重，只保留一个 |
| 2 | 存在 `python3.13` 但它是坏的（import 失败 / 非 CPython） | 版本探测脚本执行失败 → 跳过该候选，不计入 |
| 3 | 只有 `python3` = 3.9，没有任何 3.10+ | 硬错误，Problem 文案列出「探测到 3.9」并给安装带版本号解释器的命令 |
| 4 | 用户 `linux.umu_python_bin` 填了个不存在/过低的 | 硬错误，不回退自动探测（尊重显式意图），信息带原值 |
| 4b | `linux.umu_python_bin` 指向 pyenv **shim**（`~/.pyenv/shims/python`） | 能跑但依赖 `PYENV_ROOT`/`PATH`，不稳。文案建议填 `versions/<x>/bin/python` 真实路径；不强制拦 |
| 5 | 降权用户无法执行选定的解释器：venv/pyenv 在某用户 HOME 下（`~/.pyenv`、`/home/foo/venv`），或非标准位置 + 权限 700 | preflight 探测以 root 跑会「通过」，实际降权启动失败。缓解：`verifyRuntimeAccess` 的 deep-probe 里，若已解析解释器，则以降权用户身份 `os.Stat` + 尝试 `python -c 'pass'`，失败给明确告警（点名「解释器在 HOME 下，降权用户读不到，请放到 `/opt` 或系统路径」）。二期可做 |
| 6 | umu 未来改用非 zipapp 形态 / 不再走 python | `umuInterpreter()` 是单一改动点；真出现再改。当前 umu 1.4.x 就是 zipapp |
| 7 | 之前靠 shebang 能跑的机器，现在因 `resolvePython` 判断偏差被硬拦 | 用 `linux.umu_python_bin: /usr/bin/python3` 显式回退；文案里提示这个逃生口 |
| 8 | `warmPrefix` 与首次 `run` 用了不同解释器 | 不会：都走同一个 `umuInterpreter()` + 同键缓存 |

---

## 5. 测试计划

### 5.1 单元测试 `internal/runner/python_linux_test.go`（`//go:build linux`）

- 用一个临时 `bin/` 目录塞若干**假 python 脚本**（`#!/bin/sh` 打印 `3 12` 之类），
  改 `PATH` 后验证：
  - 多版本时选最高：`python3.11` + `python3.14` → 选 3.14。
  - 版本相同优先带版本号名字。
  - 全部 < 3.10 → 返回 error，消息含探测清单。
  - 坏解释器（退出码非 0）被跳过。
  - `PythonBin` 显式指定命中 / 未命中（未命中报错且不回退）。
  - `PythonBin` 填**绝对路径**（模拟 venv：临时目录里放个假 `python` 脚本）→ 直接采用，
    不要求文件名像 `python3.x`；填带 `~` 的路径 → 正确展开。
  - `PythonBin` 非空时**完全不扫**候选名单（可用假脚本数量/调用计数断言）。
  - 去重：软链场景。
- 缓存：override key 变化触发重解析。

### 5.2 `go build` / `go vet`

- `GOOS=linux go build ./...`、`GOOS=windows go build ./...` 都要过
  （新增 `runtimePython` 的 windows 桩不能漏）。

### 5.3 真机冒烟（Linux，记录到 `docs/LINUX_DEPLOYMENT.md`）

1. 系统 `python3` = 3.9 + 并装 `python3.14`：`asa-server` 自检通过，
   `/api/system/preflight` 的 `umuPython.version == "3.14"`、`source == "auto"`。
2. `config.yaml` 填 `linux.umu_python_bin: python3.14`：`source == "config"`。
3. 启动一个实例：`ps` 里能看到 `python3.14 …/umu-run …/ArkAscendedServer.exe`，
   RCON 可连、`SaveWorld` 正常、`StopServer` 能干净结束进程树。
4. `linux.umu_python_bin` 指向一个 venv（`python -m venv /opt/asa-venv`，
   `linux.umu_python_bin: /opt/asa-venv/bin/python`）：能启动实例。
5. 降权模式（root 运行）下重复 3 与 4，确认降权用户能执行选定解释器；
   把 venv 放到 `/root/asa-venv` 验证会给出「HOME 下降权用户读不到」的告警。

---

## 6. 落地顺序（建议 PR 拆分）

1. **PR1**：`python_linux.go`（发现逻辑 + 缓存）+ 单测 + `preflight_linux.go` 接线。
   自检层面先支持多版本，行为对旧机器只增不减。
2. **PR2**：`umuCommandLine` / `warmPrefix` 改用 `umuInterpreter()`，
   真机冒烟；`Handle`/进程树语义回归确认。
3. **PR3**：`linux.umu_python_bin` 配置项全链路（含 venv/pyenv 路径形态 + `~` 展开）
   + `template.go` 注释 + `main.go`/`setup.go` 装配（**不含 gui.go**）。
4. **PR4（可选）**：`/api/system/preflight` 暴露 `umuPython` + 前端展示。

---

## 7. 需要同步更新的文档

- `docs/LINUX_COMPATIBILITY_PLAN.md` §4.2：把「python3 >= 3.10」改成
  「`python3` / `python3.10`…`python3.N` 任一满足即可，多版本取最高」。
- `docs/LINUX_DEPLOYMENT.md`：新增小节 ——「低版本系统如何并装带版本号的 Python」
  + `linux.umu_python_bin` 用法（系统解释器 / venv / pyenv 三种形态各给一个例子，
  并强调降权运行时解释器不要放在某个用户 HOME 下）。
- `docs/UMU_RUNTIME_USER_PLAN.md` §9 风险表：补「降权用户需能读取/执行选定解释器；
  venv/pyenv 放 HOME 下会失败」。
- `CLAUDE.md` 的 `runner/` 描述：`preflight_linux.go` 五项自检里 python 一项
  改为「多版本探测 + 固定」，并提一句 `python_linux.go`。
