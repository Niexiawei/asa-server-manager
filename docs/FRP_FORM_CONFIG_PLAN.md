# FRP 表单化配置改造计划

> 状态：**已实施**（2026-09-08，S1–S8 全部落地；真机验收未做，见 §12）
> 落地中新发现的问题与订正记录在 §16。
> 关联：`docs/LINUX_COMPATIBILITY_PLAN.md` §5.10（frp 改库内调用，已完成）、`docs/API_REFERENCE.md` §FRP 管理

---

## 1. 背景与目标

frp 已于 §5.10 改为**库内调用**（`github.com/fatedier/frp/client`，v0.71.0），仓库里不再有 `frpc.exe`。
但配置这一侧仍停留在「内嵌二进制」时代：后端在 `{BaseDir}/frp/frpc.toml` 落一份文本文件，
前端用 Monaco 编辑器让用户**手写 TOML 全量覆盖**。

这套做法有三个问题：

1. **用户要懂 frp 的 TOML schema**，还得会写 `{{- range parseNumberRangePair ... }}` 这种 Go 模板
   （现网 `E:\asa_server_data\frp\frpc.toml` 就是这么写的），门槛与本项目「面板化管理」的定位不符。
2. **接口是「文本覆盖」语义**：`PUT /api/frp/config` 收一整个字符串直接 `os.WriteFile`，
   后端对内容一无所知 —— 无法校验、无法回显结构、写坏了只能等 `Start()` 报错。
3. **文件是多余的一层**：`client.NewService` 接受的是**结构体**（`v1.ClientCommonConfig` +
   `[]v1.ProxyConfigurer`），现在的链路是「结构体 → 我们写成 TOML → frp 再解析回结构体」，
   中间那趟文本往返除了给用户一个可写坏的入口，没有任何作用。

**本次目标**：

- **参数入参**：`PUT /api/frp/config` 收**结构化参数**，不再收配置文件文本。
- **参数收敛到三项**：远程服务器地址、验证密钥 token、端口映射规则（端口范围 + `tcp`/`udp`/`tcp+udp`）。
- **去掉 `frpc.toml`**：配置真相改为 `{BaseDir}/frp/frpc.json`，运行时直接构造 frp 的配置结构体，
  **不再有 TOML 生成与解析**。
- **前端改表单**：Monaco 编辑器换成 TDesign 表单 + 端口规则表格。
- **清掉内嵌二进制时代的残留代码**（见 §3）。

---

## 2. 现状盘点

### 2.1 后端 `internal/frpmanage`（3 个文件）

| 文件 | 现在做什么 |
|---|---|
| `manager.go` | `Initialize`（建目录 + 接管 frp 包级 logger）、`Start/Stop/Restart/IsRunning`、`buildService`（读 toml → `client.NewService`）、`LogWriter`、`createDefaultFRPConfig` |
| `api.go` | 7 个 HTTP handler + `RegisterFRPRoutes`；`GetFRPConfig`/`UpdateFRPConfig` 直接读写 `frpc.toml` 文本 |
| `manager_test.go` | F3 验收：连续 50 次 Start/Stop 无 goroutine 泄漏（**要保留**，只需把用例改成喂结构体配置） |

### 2.2 接线点

| 位置 | 行为 |
|---|---|
| `internal/webapi/actions.go:404` | `InitializationBasicComponents()` → `frpmanage.Initialize(BaseDir)` |
| `internal/webapi/actions.go:137` | `APIServer.Start()` → `frpcMgr.Start()` |
| `internal/webapi/actions.go:233` | `APIServer.Stop()` → `frpcMgr.Stop()` |
| `internal/webapi/actions.go:367` | `RegisterFRPRoutes(s.engine)` |
| `internal/svcmgr/service.go:58` | **服务模式下又 `frp.Start()` 了一次** —— 见 §3 R1 |

### 2.3 前端

- `app/src/views/FRPManager.vue`：左半屏 Monaco（`language: 'toml'`）+ 右半屏日志面板（过滤 `[frpc]`）。
- `app/src/apis/api.js`：`getFRPConfig/updateFRPConfig/getFRPStatus/startFRP/stopFRP/restartFRP`。
- `app/src/apis/sseApi.js`：`streamFRPStatus`。

### 2.4 现网配置文件（参考样本）

```toml
serverAddr = "47.97.22.91"
auth.token = "9d4d40ad-…"

{{- range $_, $v := parseNumberRangePair "9310-9319" "9310-9319" }}
[[proxies]]
name = "udp-asaserver-{{ $v.First }}"
type = "udp"
localPort = {{ $v.First }}
remotePort = {{ $v.Second }}
{{- end }}
```

**信息量只有四条**：服务器地址、token、端口范围 `9310-9319`、协议 `udp`（且 `localPort == remotePort`）。
这正好就是本次要保留的参数集合 —— 说明表单化不丢能力。

---

## 3. 多余代码排查结果

> 以下每条都已在本仓库 / frp v0.71.0 / golib v0.8.2 源码里核对过，不是猜测。

| 编号 | 位置 | 性质 | 判定依据 | 处置 |
|---|---|---|---|---|
| **R1** | `internal/svcmgr/service.go:58-62` | **重复启动**：服务模式下 `program.Start()` 先 `frp.Start()`，紧接着 goroutine 里 `apiServer.Start()` 又 `frp.Start()` 一次 | 第二次必然撞上 `m.running` 返回 `"frpc is already running"`，被 `logger.Errorf` 记成错误日志 | **删掉 svcmgr 里那一段**，启动点统一收在 `webapi.APIServer.Start()`（与 `Stop()` 对称） |
| **R2** | `manager.go:211` `Cleanup()` | **死代码**，全仓库零调用方 | `grep -rn "frpmanage\." internal/ main*.go` 只出现 `GetGlobalManager/Initialize/RegisterFRPRoutes` | 删除。（它还会 `os.RemoveAll(runDir)` —— 一旦有人接上去就会连用户配置一起删掉，属于危险的死代码） |
| **R3** | `manager.go:204` `CheckStatus()` | **与 `IsRunning()` 完全同实现**的重复方法 | 两者都是 `lock; return m.running` | 删除 `CheckStatus`，`api.go` 三处改用 `IsRunning` |
| **R4** | `manager.go:238` `ansiRegex` + `LogWriter` 里的 ANSI 剥离 | **失效逻辑**：日志路径上不可能出现 ANSI 序列 | golib 的颜色只在 `ConsoleWriter.WriteLog` 里加；`Logger.write` 仅当 writer 实现了 `WriteLog([]byte,Level,time.Time)` 才走那条路。我们的 `LogWriter` 只实现 `io.Writer` → 走 `l.out.Write` 明文分支 | 删除 `ansiRegex` 与剥离调用；顺带能去掉 `regexp` import |
| **R5** | `manager.go:266` `createDefaultFRPConfig` 里的 `protocol = "tcp"` / `connPoolCount = 1` / `metaData.version = "0.57.0"` | **三个键在 v1 schema 下不存在，被静默丢弃** | v1 对应键是 `transport.protocol` / `transport.poolCount` / `metadatas`；文件不是 ini 格式 → `DetectLegacyINIFormatFromFile` 为假 → 走 v1 解析，`strict=false` 忽略未知键 | 随 `createDefaultFRPConfig` 整体删除 |
| **R6** | 同上，`log.to/level/maxDays` | **惰性配置**：写了也不生效 | frp 只在 `cmd/frpc/sub/root.go` 调 `log.InitLogger`；我们走库内调用、且在 `Initialize` 里直接替换了 `frplog.Logger`，Common 里的 `Log` 字段无人读 | 随 `createDefaultFRPConfig` 整体删除；**新配置模型不要再引入日志字段** |
| **R7** | `api.go:143-147` `StreamFRPStatus` 的 `GetStartErr` 分支 | **恒等分支**：`if err != nil { "stopped" } else if running { "running" }` —— 出错分支与默认值一样，等于没写 | 代码可见 | 重写为「带 `message` 的状态推送」（§6.3），把 `startErr` 真正暴露出去 |
| **R8** | `api.go:14` `StatusResponse` | 与 `internal/webapi/apiresp.StatusResponse` **重复定义** | 两者字段一致 | 改用 `apiresp.StatusResponse`（`frpmanage` → `webapi/apiresp` 无环：apiresp 是纯响应壳） |
| **R9** | `manager.go:184` `Restart()` 里的 `time.Sleep(500ms)` | **进程时代残留**：等旧 `frpc.exe` 退出腾出端口 | 库内调用下 `GracefulClose(2s)` 返回后已无端口占用；`svr` 字段也已被新实例替换（`asyncRun` 有 `m.svr != svr` 守卫） | 删除 sleep；`Restart` 改为「Stop（忽略 not-running）→ Start」 |
| **R10** | `manager.go:43` `frpConfigDir` 包级变量 | 只被 `api.go` 用来拼 `frpc.toml` 路径 | — | 随文件路径改造收进 `FrpcManager` 字段（`m.runDir` 已经有了，本就重复） |
| **R11** | `manager.go` 的 `bufio`/`strings`/`io` import | `LogWriter` 保留，故 **`bufio`/`strings` 仍需要**；`io` 仅为 `var _ io.Writer` 断言 | — | 保留（不是多余，此处备注避免误删） |

> **不算多余、必须保留的**：`Initialize` 里替换 `frplog.Logger` 那段（注释已说明：调 `frplog.InitLogger`
> 会按配置抢 stdout 或另开轮转文件）、`asyncRun` 的 `m.svr != svr` 守卫、`manager_test.go` 的泄漏回归。

---

## 4. 目标配置模型

### 4.1 存储

- 路径：`{BaseDir}/frp/frpc.json`（`0600`，含 token —— 与 `auth/secret.key` 同属机密文件）
  - **为什么是文本文件而不是 BadgerDB**：见 §11 决策 **D6**。一句话版本：本仓库的分工是
    「Badger = 机器产生的实例状态，SQLite = 仅鉴权，**用户填的配置一律是可读文本**」，
    `schedules.json` 就是同类先例；而且隧道配置是「服务器进不去时用来救命」的东西，
    必须能备份、能手工放回、能肉眼核对
- 旧文件：启动时若存在 `frpc.toml`，做一次**尽力迁移**（§9），随后重命名为 `frpc.toml.migrated` 保留备份，**运行时不再读取**。
- **不再生成 TOML**。产出的是内存里的 frp 结构体，不落地中间文本 —— 否则又给了「手改文件」的入口，
  而它会在下一次表单保存时被无声覆盖。

### 4.2 结构体

```go
package frpmanage

// Config 是 FRP 配置的唯一真相，落在 {BaseDir}/frp/frpc.json。
// 字段刻意只有三组：地址、鉴权、端口映射 —— 其余 frp 能力（http/stun/
// visitor/带宽限制…）本项目用不到，不开放也不落盘。
type Config struct {
    ServerAddr string     `json:"server_addr"`           // frps 地址，纯 host 或 host:port
    ServerPort int        `json:"server_port,omitempty"` // 省略/0 → 7000
    Token      string     `json:"token"`                 // auth.token，可为空（frps 未开鉴权）
    Rules      []PortRule `json:"rules"`
}

// PortRule 一条端口映射规则。remotePort 恒等于 localPort ——
// 现网配置一直如此，ARK 客户端也只认识同号端口。
type PortRule struct {
    Start    int      `json:"start"`              // 起始端口（含）
    End      int      `json:"end"`                // 结束端口（含）；单端口时与 Start 相同
    Protocol Protocol `json:"protocol"`           // tcp | udp | tcp+udp
    Remark   string   `json:"remark,omitempty"`   // 备注，仅供 UI 显示
}

type Protocol string

const (
    ProtocolTCP    Protocol = "tcp"
    ProtocolUDP    Protocol = "udp"
    ProtocolBoth   Protocol = "tcp+udp"
)
```

`frpc.json` 示例（等价于 §2.4 的现网 TOML）：

```json
{
  "server_addr": "47.97.22.91",
  "server_port": 7000,
  "token": "9d4d40ad-0ee5-4414-b38d-4e9e787830a1",
  "rules": [
    { "start": 9310, "end": 9319, "protocol": "udp", "remark": "游戏端口" }
  ]
}
```

### 4.3 展开为 frp 配置 —— **走 frp 自己的解码器，不手搓 struct**

> ⚠️ 这一节是读完 `LINUX_COMPATIBILITY_PLAN.md` §5.10.4 **坑 #5** 后的修正，
> 与本文档初稿的做法不同，理由见 §11 决策 **D5**。

**不要**手工构造 `v1.TCPProxyConfig` / `v1.ProxyBaseConfig` 这些结构体。改为在**内存里**
生成 frp 的 v1 配置字节，交给 frp 自己的解码器：

```go
// frpSchema 是 frp v1 客户端配置的**子集**，字段名严格照 frp 文档化的
// 配置 schema（不是 Go struct 字段名）。我们只依赖 schema，不依赖 v1 包的内部形状。
type frpSchema struct {
    ServerAddr string          `json:"serverAddr"`
    ServerPort int             `json:"serverPort"`
    Auth       frpAuthSchema   `json:"auth"`
    Proxies    []frpProxySchema `json:"proxies"`
}
type frpAuthSchema struct {
    Token string `json:"token,omitempty"`
}
type frpProxySchema struct {
    Name       string `json:"name"`
    Type       string `json:"type"`       // tcp | udp
    LocalPort  int    `json:"localPort"`
    RemotePort int    `json:"remotePort"`
}

// build 把面板参数展开成 frp 的 Common + Proxies。
//
// 走 config.LoadConfigure 而不是直接拼 v1 结构体：frp 不承诺 client / config/v1
// 这些 Go 包的 API 稳定（LINUX_COMPATIBILITY_PLAN §5.10.4 坑 #5，已有过
// ConfigSourceAggregator 变必填这样的破坏性变更），但**配置 schema 是它文档化并
// 保证兼容的**。多一次 marshal/unmarshal（每次启动一次）换来的是：frp 改 struct
// 内部结构时我们不受影响，且白拿它自己的默认值填充与 schema 校验。
//
// 这不是"回到写配置文件"：这段字节只存在于内存，磁盘上仍然只有我们自己的
// frpc.json，用户没有可手改的入口。
func (c *Config) build() (*v1.ClientCommonConfig, []v1.ProxyConfigurer, error) {
    s := frpSchema{
        ServerAddr: c.host(),   // 拆掉 host:port 里的 port
        ServerPort: c.port(),   // 0 → 7000
        Auth:       frpAuthSchema{Token: c.Token},
    }
    for _, r := range c.Rules {
        for p := r.Start; p <= r.End; p++ {
            for _, proto := range r.Protocol.expand() { // tcp+udp → ["tcp","udp"]
                s.Proxies = append(s.Proxies, frpProxySchema{
                    Name:       fmt.Sprintf("%s-asaserver-%d", proto, p),
                    Type:       proto,
                    LocalPort:  p,
                    RemotePort: p, // 恒等，见 §11 决策 D3
                })
            }
        }
    }

    b, err := json.Marshal(&s)
    if err != nil {
        return nil, nil, err
    }

    // LoadConfigure 识别 JSON buffer（首个非空白字符是 '{'）后走 JSON 解码分支。
    // strict=false：与 frpc 命令行默认一致，也让 frp 将来加字段时我们不会炸。
    var all v1.ClientConfig
    if err := config.LoadConfigure(b, &all, false); err != nil {
        return nil, nil, fmt.Errorf("build frp config: %w", err)
    }

    common := &all.ClientCommonConfig
    proxies := make([]v1.ProxyConfigurer, 0, len(all.Proxies))
    for _, p := range all.Proxies {
        proxies = append(proxies, p.ProxyConfigurer)
    }
    return common, proxies, nil
}
```

**代理名保持 `{proto}-asaserver-{port}`**，与现网 TOML 生成的名字逐字一致 —— 换名没有收益，
而 frps 侧是按名字登记代理的，保持一致可以让老用户切过来时 frps 上看到的东西不变。
这个名字还是 §6.3 里查询单条代理状态的键，**不能带随机成分**。

### 4.4 `buildService` 改造后

```go
// 注意：src 要**存进 FrpcManager**，热更新（§7.2）要用它。
func buildService(cfg *Config) (*client.Service, *source.ConfigSource, error) {
    common, proxyCfgs, err := cfg.build()
    if err != nil {
        return nil, nil, err
    }

    src := source.NewConfigSource()
    if err := src.ReplaceAll(proxyCfgs, nil); err != nil {
        return nil, nil, fmt.Errorf("load proxies: %w", err)
    }
    aggregator := source.NewAggregator(src)

    proxyCfgs, visitorCfgs, err := aggregator.Load()
    if err != nil {
        return nil, nil, fmt.Errorf("aggregate config: %w", err)
    }
    proxyCfgs, visitorCfgs = config.FilterClientConfigurers(common, proxyCfgs, visitorCfgs)
    proxyCfgs = config.CompleteProxyConfigurers(proxyCfgs)
    visitorCfgs = config.CompleteVisitorConfigurers(visitorCfgs)

    if warn, err := validation.ValidateAllClientConfig(common, proxyCfgs, visitorCfgs, nil); err != nil {
        return nil, nil, fmt.Errorf("validate config: %w", err)
    } else if warn != nil {
        logger.Warnf("[frpc] %v", warn)
    }

    svr, err := client.NewService(client.ServiceOptions{
        Common:                 common,
        ConfigSourceAggregator: aggregator, // 必填，为空 NewService 直接报错
        // ConfigFilePath 留空：它只被 client/config_manager.go 的 admin 热重载用，
        // 我们没开 webServer，也不再有配置文件
    })
    return svr, src, err
}
```

与现有实现的差异只有开头三行（不再 `config.LoadClientConfigResult`），
`Filter/Complete/Validate` 那一串**照旧保留** —— 它们负责填默认值和挡住非法组合，不能省。

---

## 5. 校验规则

在 `PUT /api/frp/config` 与 `Start()` **两处**都跑同一个 `Config.Validate()`（保存时给用户看，启动时防手改 JSON）：

| 规则 | 报错文案（示例） |
|---|---|
| `ServerAddr` 非空 | `远程服务器地址不能为空` |
| `ServerAddr` 解析：允许 `host`、`host:port`、IPv6 `[::1]:7000`；`host:port` 与 `ServerPort` 同时给出且冲突 → 报错 | `地址里已带端口 7001，与端口字段 7000 冲突` |
| `ServerPort ∈ [1,65535]`（0 视为未填 → 7000） | `frps 端口需在 1-65535 之间` |
| `Rules` 至少一条 | `至少需要一条端口映射规则` |
| `1 ≤ Start ≤ End ≤ 65535` | `第 2 条规则：起始端口不能大于结束端口` |
| `Protocol ∈ {tcp,udp,tcp+udp}` | `第 1 条规则：协议只能是 tcp / udp / tcp+udp` |
| 单条规则端口数 ≤ **64** | `第 1 条规则跨越 200 个端口，单条上限 64` |
| 展开后代理总数 ≤ **128** | `端口映射共展开 300 条代理，上限 128` |
| 同协议端口区间**不得重叠**（含 `tcp+udp` 与 `tcp` 的交叉） | `第 3 条规则的 udp 9315 与第 1 条重复` |
| `Token` 为空时不报错，但 `Validate` 返回一条 **warning** 供前端提示 | `未设置 token，仅在 frps 未开启鉴权时可用` |

> 两条数量上限不是保守洁癖：每个端口都是一条**独立的 frp 代理注册**，
> 一个手滑写成 `1-65535` 的范围会向 frps 发起 6 万多次代理注册，
> 既打垮对端也让本进程的日志彻底不可读。ARK 一台实例实际只需要
> 游戏端口 + 查询端口 + RCON，128 条足够十几个实例。

**重叠必须挡住**的原因：展开后会生成两个同名代理（如两条 `udp-asaserver-9315`），
frps 侧按名字登记，第二个会被拒绝并连带报错，但报错发生在**启动后的异步登录流程**里，
用户在面板上只看到「启动了又停了」。在保存时同步挡掉才有可读的错误。

---

## 6. 接口收敛

### 6.1 前后对照

| 方法 | 路径 | 现在 | 改造后 |
|---|---|---|---|
| GET | `/api/frp/config` | 返回 `frpc.toml` **原文字符串** | 返回 **`Config` JSON** |
| PUT | `/api/frp/config` | 收 `{config: "<整个 toml>"}`，`os.WriteFile` 覆盖 | 收 **`Config` JSON**，校验 → 存 `frpc.json` → 运行中则热重启 |
| GET | `/api/frp/status` | `{status: "running"|"stopped"}` | **`FRPStatus`**（见 6.3） |
| GET | `/api/frp/status/stream` | 每秒无脑推一次，`startErr` 分支恒等（R7） | 推 **同一个 `FRPStatus`**，**仅在变化时推**（外加首帧与 25s 心跳注释帧） |
| POST | `/api/frp/start` | 不变 | 不变（内部：配置缺失/非法时返回可读错误，不再自动造默认配置） |
| POST | `/api/frp/stop` | 不变 | 不变 |
| POST | `/api/frp/restart` | 不变 | 不变 |

**收敛点在于**：`/status` 与 `/status/stream` 从「两个各自拼字符串的地方」变成**同一个
`buildStatus(m)` 构造出的同一个结构体**；`/config` 从「不透明文本」变成「有 schema 的参数」。
端点个数不变 —— 7 个里没有一个是真多余的（一次性查询给脚本/CLI 用，推送流负责异步登录失败）。

### 6.2 `Config` payload

即 §4.2 的结构体，GET/PUT 同形。**token 明文返回**（接口本身在鉴权之后，且表单需要回显可编辑）；
前端用密码框 + 「显示」切换来避免肩窥。

### 6.3 `FRPStatus` payload —— 用 `svr.StatusExporter()` 给出**每条代理**的状态

```go
type FRPStatus struct {
    Running    bool         `json:"running"`
    Configured bool         `json:"configured"`         // frpc.json 存在且通过 Validate
    Message    string       `json:"message,omitempty"`  // 最近一次 startErr（登录失败/校验失败）
    ProxyCount int          `json:"proxy_count"`        // 当前配置展开后的代理条数
    Proxies    []ProxyState `json:"proxies,omitempty"`  // 运行中才有；未运行时省略
}

// ProxyState 是 client/proxy.WorkingStatus 的投影 —— 只取 UI 需要的四个字段，
// 不把 frp 的类型直接塞进 HTTP 响应（那等于把 v1.ProxyConfigurer 的内部形状
// 变成我们的对外 API，见 §11 决策 D5 的同一条理由）。
type ProxyState struct {
    Name       string `json:"name"`        // udp-asaserver-9310
    Type       string `json:"type"`        // tcp | udp
    LocalPort  int    `json:"local_port"`  // 从 Name 反查，UI 按端口排序用
    Phase      string `json:"phase"`       // WorkingStatus.Phase：running / wait start / start error …
    Err        string `json:"err,omitempty"`
    RemoteAddr string `json:"remote_addr,omitempty"` // frps 侧实际监听地址，直接展示给用户
}
```

数据来源（已核对 frp v0.71.0 `client/service.go:472`）：

```go
exporter := svr.StatusExporter()               // StatusExporter{ GetProxyStatus(name) (*proxy.WorkingStatus, bool) }
for _, name := range cfg.proxyNames() {        // 名字由 §4.3 确定性生成，所以查得到
    if ws, ok := exporter.GetProxyStatus(name); ok { … }
}
```

**为什么这条对表单化尤其重要**（`LINUX_COMPATIBILITY_PLAN` §5.10.3 把它列为库内调用的收益之一，
本文档初稿漏了）：端口**范围**映射最常见的故障是「范围里某一个端口在 frps 侧已被别的客户端占用」。
二值的 running/stopped 对此完全失语 —— 整体连上了，就只有 9315 那条静默失败，
用户看到的是「面板显示运行中，但一个特定端口的实例连不上」。有了 `Phase` + `Err`，
这条在 UI 上直接是一行红字。`RemoteAddr` 则顺带解决了「我该让玩家连哪个地址」。

`Message` 是 R7 修掉的另一条信息通路：`loginFailExit` 默认 `true`，token 错或 frps 不可达时
`svr.Run` 会返回错误、`asyncRun` 把它记进 `startErr` —— 现在这个错误只进日志文件，
面板上只能看到状态灯变红。改造后它出现在状态推送里，前端直接弹出来。

两者分工：`Message` = **连不上 frps**（整体失败）；`Proxies[].Err` = **连上了但这条代理没起来**（局部失败）。

---

## 7. 后端改造清单

### 7.1 新增 `internal/frpmanage/config.go`

- `Config` / `PortRule` / `Protocol` 类型（§4.2）
- `Validate() (warnings []string, err error)`（§5）
- `build() (*v1.ClientCommonConfig, []v1.ProxyConfigurer, error)`（§4.3）
- `ProxyCount() int`
- `LoadConfig(dir) (*Config, error)` / `SaveConfig(dir, *Config) error`（JSON，`0600`，先写临时文件再 `os.Rename` 原子替换）
- `migrateFromTOML(dir) (*Config, bool, error)`（§9）

### 7.2 改造 `internal/frpmanage/manager.go`

- 删除：`createDefaultFRPConfig`、`frpcConfigFileName`、`frpConfigDir`、`Cleanup`、`CheckStatus`、
  `ansiRegex` 及其调用、`Restart` 里的 `time.Sleep`（R2/R3/R4/R5/R6/R9/R10）
- `buildService(configPath string)` → `buildService(cfg *Config)`（§4.4），返回值多带一个
  `*source.ConfigSource`，存进 `FrpcManager.src` 供热更新用
- `Start()`：`LoadConfig` → `Validate` → `buildService` → `Run`；
  **配置缺失时返回哨兵错误 `ErrNotConfigured`**，不再造默认配置
- 新增 `Config() *Config` / `SetConfig(*Config) error`（见 7.2.1 的热更新分流）
- 新增 `Status() FRPStatus`（§6.3，运行中时遍历 `svr.StatusExporter()`）

#### 7.2.1 `SetConfig` 的热更新分流

`LINUX_COMPATIBILITY_PLAN` §5.10.3 把 `UpdateAllConfigurer` 标为「可选，非必须」——
**在 TOML 时代那是对的**（改配置 = 重写文件 = 反正要重启）。表单化之后它变成**应该做**：
用户加一条端口规则，不该把已经在跑的隧道全断一次 —— 正在游戏里的玩家会掉线。

```go
func (m *FrpcManager) SetConfig(next *Config) error {
    if _, err := next.Validate(); err != nil {
        return err
    }
    if err := SaveConfig(m.runDir, next); err != nil {
        return err
    }

    m.mu.Lock()
    defer m.mu.Unlock()
    if !m.running || m.svr == nil {
        m.cfg = next
        return nil // 没在跑，下次 Start 自然生效
    }

    // frps 地址 / 端口 / token 变了 → 得重新登录，只能重启
    if next.commonChanged(m.cfg) {
        m.cfg = next
        return m.restartLocked()
    }

    // 只有代理清单变了 → 热更新，已建立的连接不受影响
    common, proxyCfgs, err := next.build()
    if err != nil {
        return err
    }
    if err := m.svr.UpdateConfigSource(common, proxyCfgs, nil); err != nil {
        return err
    }
    m.cfg = next
    return nil
}
```

用 `UpdateConfigSource(common, proxies, visitors)` 而不是 `UpdateAllConfigurer(proxies, visitors)`：
前者会先 `configSource.ReplaceAll(...)` 再应用，**把我们持有的那个 `*source.ConfigSource` 也一起更新了**；
只调后者的话，frp 内部任何一次 `reloadConfigFromSources()` 都会从旧的 source 把配置**回滚回去**
（已核对 `client/service.go:369` 与 `:385`）。这是个不调就静默、调错就诡异回滚的地方，
**落地时必须有一个「热更新后再触发一次 reload，配置不回滚」的测试**。
- `LogWriter`：按 golib 的行首格式（`2006-01-02 15:04:05.000 [W] …`）把 `[W]`/`[E]` 分别转
  `logger.Warnf`/`logger.Errorf`，其余走 `Infof`（现在**全部记成 INFO**，前端日志面板的等级配色恒为蓝）

### 7.3 改造 `internal/frpmanage/api.go`

- `StatusResponse` → `apiresp.StatusResponse`（R8）
- `GetFRPConfig` / `UpdateFRPConfig` 改结构化
- `GetFRPStatus` / `StreamFRPStatus` 统一走 `m.Status()`；流做**变化去重** + 心跳帧
- 所有 handler 的 `manager == nil` 分支保留（`Initialize` 失败时 `log.Fatal`，理论上到不了，但便宜）

### 7.4 改造接线

- `internal/svcmgr/service.go:58-62`：删除重复的 `frp.Start()`（R1）
- `internal/webapi/actions.go:137`：`frpcMgr.Start()` 的错误里若 `errors.Is(err, ErrNotConfigured)`，
  降为 `logger.Infof("frp 未配置，跳过自动启动")`，不再刷 ERROR

### 7.5 测试

- `manager_test.go`：`leakConfig` 常量换成 `&Config{ServerAddr:"127.0.0.1", ServerPort:1, Rules:[…]}`，
  **50 次 Start/Stop 无泄漏的验收保持不变**（它是 §5.10 F3 的硬性验收，不能顺手删掉）
- 新增 `config_test.go`：
  - `Validate` 的每条规则各一个用例（尤其**重叠检测**与**上限**）
  - `host:port` 解析（含 IPv6）
  - `build()` 展开：`tcp+udp` × `9310-9311` → 4 条代理，名字与 `LocalPort/RemotePort` 逐条断言
  - 迁移：喂 §2.4 那份带 Go 模板的 TOML，断言解析出 `9310-9319 / udp`

---

## 8. 前端改造（`app/src/views/FRPManager.vue`）

### 8.1 布局

保持现有「左配置 / 右日志」两栏骨架与顶部状态灯 + 启动/停止/重启按钮，**只换左半屏内容**：

```
┌ FRP 管理 ────────────────────── ● 运行中  [启动][停止][重启] ┐
│ ┌ 连接配置 ──────────────┐ ┌ 运行日志 ────────────────┐ │
│ │ 远程服务器地址 [47.97.22.91      ] │ │ (原日志面板，不动)        │ │
│ │ 服务端口       [7000             ] │ │                          │ │
│ │ 验证密钥       [••••••••••] [显示] │ │                          │ │
│ │ ── 端口映射 ──────────  [+ 添加] │ │                          │ │
│ │ 起始   结束    协议      备注   │ │                          │ │
│ │ [9310][9319][UDP ▾][游戏端口][×]│ │                          │ │
│ │ 共 10 条代理                    │ │                          │ │
│ │              [保存并应用] [重置] │ │                          │ │
│ │ ── 代理状态（运行中才显示）──── │ │                          │ │
│ │ ● udp 9310 → 47.97.22.91:9310  │ │                          │ │
│ │ ● udp …                        │ │                          │ │
│ │ ✕ udp 9315  端口已被占用        │ │                          │ │
│ └────────────────────────┘ └──────────────────────────┘ │
└──────────────────────────────────────────────────────┘
```

「代理状态」区来自 `FRPStatus.proxies`（§6.3），按 `local_port` 排序；
`phase == "running"` 绿点 + 显示 `remote_addr`（用户直接复制给玩家），否则红叉 + `err`。
条数多时默认折叠成「10 条正常 / 1 条异常」，展开看明细 —— 128 条上限下不会撑爆面板。

组件：`t-form` + `t-input` + `t-input-number` + `t-select` + `t-table`（可编辑行）/ 手写行布局。
「共 N 条代理」由前端按 `rules` 实时算，与后端 `ProxyCount` 同口径，超过 128 时红字提示并禁用保存。

### 8.2 交互

- **进页面**：`getFRPConfig()` 填表单；未配置时给一份空规则的初始表单（不再显示默认 TOML）。
- **保存并应用**：前端先跑一遍与后端同规则的轻校验（快速反馈），再 `updateFRPConfig(config)`；
  成功后 `MessagePlugin.success`，若返回 warning（如 token 为空）用 `MessagePlugin.warning` 提示。
  **只改端口规则时后端走热更新**（§7.2.1），已建立的隧道不断线 —— 文案要区分：
  改地址/token 时提示「将重新连接 frps」，只改端口时提示「已生效，现有连接不受影响」。
- **状态**：`streamFRPStatus` 改为消费 `FRPStatus` 对象；`message` 非空时在状态灯旁显示，
  并在从 running→stopped 且带 message 时弹一次 `NotifyPlugin.error`（当前用户完全看不到失败原因）。
- **删除**：`monaco-editor` import、`editor`/`editorContainer` ref、`initEditor`/`saveFRPConfig`/
  `reloadFRPConfig`（改名 `saveConfig`/`loadConfig`）、`.editor-container` 样式。
  ⚠️ Monaco 的分包（`vite.config.js` 的 `monaco` chunk）**不要动** —— `ConfigEditor.vue` 等还在用。
- `checkFRPStatus()` 目前**定义了但从未调用**，顺手删掉（或在流断开时用作兜底轮询，二选一，别留着不用）。

### 8.3 `app/src/apis/api.js`

```js
// 更新 FRP 配置：结构化参数，不再是配置文件文本
export function updateFRPConfig(config) {
    return apiClient.put('/api/frp/config', config)   // 原来是 {config: "<toml>"}
}
```

`getFRPConfig` 路径不变，返回值语义从 string 变对象 —— 调用点只有 `FRPManager.vue` 一处。

---

## 9. 迁移与兼容

`Initialize(baseDir)` 里追加一次性迁移，**尽力而为、失败不阻塞启动**：

1. `frpc.json` 已存在 → 直接返回，不看 TOML。
2. 否则若 `frpc.toml` 存在：
   - 用 **frp 自己的** `config.LoadClientConfigResult(path, false)` 解析
     （它会渲染 `{{ parseNumberRangePair }}` 这类 Go 模板 —— 手写 TOML 解析器做不到，
     现网那份配置正是模板写法）。
   - `Common.ServerAddr/ServerPort/Auth.Token` → `Config` 对应字段。
   - 遍历 `Proxies`：只认 `tcp`/`udp` 且 `RemotePort == LocalPort` 的；
     按协议分组、端口排序后**把连续段压回 `PortRule`**；同端口同时有 tcp 与 udp 的合并为 `tcp+udp`。
   - 出现无法表达的代理（http/sts/visitor/`remotePort != localPort`）→ **不迁移这一条**，
     记一条 WARN 列出被丢弃的代理名，并在 `FRPStatus.Message` 里带出来一次。
3. 写出 `frpc.json`，把 `frpc.toml` 重命名为 `frpc.toml.migrated`（**不删**，用户可回查）。
4. 任一步失败：记 WARN，当作「未配置」，用户在面板上重填三个参数即可。

> 现网那份配置走完这条路应当得到 `{addr:47.97.22.91, port:7000, token:…, rules:[{9310,9319,udp}]}` ——
> 这是 §7.5 的迁移用例断言。

---

## 10. 自动启动行为

| 场景 | 现在 | 改造后 |
|---|---|---|
| 从没配过 frp | 自动生成指向 `127.0.0.1:7000`、token `your-token-here` 的默认配置并启动 → 必然登录失败 → 日志刷 ERROR | `Start()` 返回 `ErrNotConfigured`，`APIServer.Start()` 记一条 INFO 后跳过 |
| 配置存在且合法 | 启动 | 启动（不变） |
| 配置存在但非法（手改坏 JSON） | —— | `Start()` 返回校验错误，记 WARN，`FRPStatus.Message` 带出原因 |

**不引入「开机自启」开关**：参数集合仍是用户要求的三项。语义是「配好了就自动起」，
与现在「有配置文件就自动起」一致，用户认知不变。

---

## 11. 决策点与风险

| 编号 | 决策 | 取舍 |
|---|---|---|
| **D1** | 地址与端口**拆成两个字段**，同时允许在地址里写 `host:port`（解析后回填端口框） | 用户要求「只保留远程服务器地址」，但 frps 端口并非恒为 7000。做成「端口框默认 7000、地址里带端口也认」既不丢能力，也不逼用户理解两个框的关系 |
| **D2** | `LoginFailExit` 保持 frp 默认的 `true` | `false` 会让 frpc 无限退避重试，面板显示「运行中」而实际什么都不通，比直接失败更难排查。`true` + `FRPStatus.Message` 是可读的组合。（代价：frps 短暂重启时 frpc 不会自愈，需手动点重启 —— 现有行为也是如此，不算回归） |
| **D3** | `remotePort` 恒等于 `localPort`，不开放 | 现网配置、ARK 客户端连接方式都要求同号；开放它等于要在表单里再加一列，且大多数填错的组合无法工作 |
| **D4** | 不保留「高级模式：直接编辑 TOML」逃生口 | 保留就等于保留两条真相路径，且第二条会在表单保存时被无声覆盖。真需要 frp 高级能力的用户可以另跑一个官方 frpc |
| **D5** | 配置展开走 `config.LoadConfigure(内存字节)`，**不手搓 `v1.*` 结构体**（§4.3） | `LINUX_COMPATIBILITY_PLAN` §5.10.4 坑 #5：frp 不承诺 `client` / `config/v1` 这些 Go 包的 API 稳定，且已有过破坏性变更。手搓 struct 会把耦合面从「2 个函数」扩大到「一组 Go 字段名」，正好踩在这条坑上；而**配置 schema 是 frp 文档化并保证兼容的**。代价是每次启动多一次 marshal/unmarshal（可忽略），收益是 frp 改内部结构时我们不受影响、外加白拿它的默认值填充与 schema 校验。⚠️ 这不构成「又回到写配置文件」：字节只在内存，磁盘上只有 `frpc.json`，用户没有可手改的入口 |
| **D6** | 配置存 **`{BaseDir}/frp/frpc.json` 文本文件**，不进 BadgerDB | ①本仓库已有明确分工（根 `CLAUDE.md`）：Badger = 实例状态（机器产生、可重建、滚动淘汰 500 条），SQLite = **"仅用于鉴权"**，**用户填的配置一律是可读文本**（`config.yaml` / `instance_config.ini` / `schedules.json` / `log_mapping.json`）；②`internal/schedule/store.go` 的 `schedules.json` 是现成同类先例（面板上填、后端结构化读写、标注「可手改」），frp 配置与它同类；③依赖方向：`frpmanage` 现在零领域依赖，接 Badger 要 import `internal/state`（而 state 依赖 config+process），为 4 个字段把叶子包拖进领域层不划算；④**可救性** —— 隧道配置是「服务器进不去时用来救命」的东西，Badger 损坏/被清就没了且无法用记事本恢复，文本文件可备份、可 SFTP 手工放回、可肉眼核对 token；⑤`Initialize` 阶段就要读配置，接 Badger 会引入与状态库的启动顺序耦合。Badger 唯一更强的是原子性与并发写 —— 前者用「临时文件 + `os.Rename`」已经有，后者不存在（单写者：面板保存） |
| **R-a** | **风险**：老用户的 `frpc.toml` 用了本方案表达不了的能力（http 代理、visitor） | 迁移时不静默丢弃 —— WARN + 面板提示被丢弃的代理名（§9.2）。这类用户需要按 D4 自行处理 |
| **R-b** | **风险**：改动触及 `svcmgr` 启动路径（R1） | 服务模式的启动路径没有自动化测试。落地时必须**在 Windows 上实测一次 `service install/start`**，确认 frp 仍随服务起来（见 §12） |

---

## 12. 验收清单

**后端**

- [ ] `go build ./...` + `go vet ./...` 通过；`GOOS=linux go build ./...` 通过
- [ ] `go test ./internal/frpmanage/...` 通过，**含原有的 50 次 Start/Stop 无 goroutine 泄漏**
- [ ] `Validate` 各条规则的单测（重叠、上限、协议、端口边界、`host:port` 解析）
- [ ] 迁移单测：§2.4 的模板 TOML → `{9310-9319, udp}`
- [ ] 全仓库 `grep -rn "frpc.toml"` 只剩迁移代码与本文档
- [ ] **热更新不回滚**：运行中改端口规则 → `UpdateConfigSource` → 再触发一次 reload，
      新规则仍在（这是 §7.2.1 那个「不调就静默、调错就诡异回滚」的点）
- [x] `THIRD_PARTY_NOTICES.md` 的 frp 条目完整（Apache-2.0 全文 + 归属）—— 已复核，分发义务已履行。
      仓库自身的 `LICENSE` 仍缺，但那是授权决定不是本次义务，见 §14.3

**接口**

- [ ] `GET /api/frp/config` 返回结构化 JSON
- [ ] `PUT` 非法参数返回 400 且**错误文案指出是第几条规则**
- [ ] `GET /api/frp/status` 与 `/status/stream` 返回**同形**的 `FRPStatus`
- [ ] 故意填错 token 启动 → 面板上能看到失败原因（`FRPStatus.Message`，不再只在日志里）
- [ ] **单条代理失败可见**：让 frps 侧占用范围内某一个端口（如 9315）→ 整体 `running=true`，
      但 `proxies[]` 里那一条 `phase != "running"` 且带 `err`（§6.3 的核心价值）

**前端**

- [ ] `npm run build` 通过，`FRPManager.vue` 不再 import monaco
- [ ] 表单能完成：填地址/token/一条 9310-9319 UDP 规则 → 保存 → 启动 → 状态转「运行中」
- [ ] 端口规则可增删、超上限时禁用保存并红字提示
- [ ] 右侧日志面板行为不变；frp 的 WARN/ERROR 现在显示为对应颜色（LogWriter 分级后）

**真机 / 部署**

- [ ] 拿现网 `E:\asa_server_data\frp\frpc.toml` 跑一次迁移，`frpc.json` 内容与 §9 预期一致，
      且**隧道实际可用**（用 ARK 客户端或 `nc -u` 打一遍 9310）
- [ ] `asa-server service install && service start`：frp 随服务启动，且日志里**没有**
      "frpc is already running" 这条（R1 的回归判据）

---

## 13. 文档联动

| 文档 | 要改什么 |
|---|---|
| `docs/API_REFERENCE.md` §FRP 管理 | 第 416 行 "内嵌 `frpc.exe`" **已是过期描述**（§5.10 之后就没有 exe 了），本次一并改成「库内调用 + 表单参数」；补 `Config`/`FRPStatus` 的 payload |
| `docs/LINUX_COMPATIBILITY_PLAN.md` §5.10 | 追加一小节：配置面从 TOML 文本改为结构化参数（PLAN 是只增不改的档案，加节不改旧文） |
| `CLAUDE.md`（根） | `frpmanage` 条目补一句：配置为 `{BaseDir}/frp/frpc.json` 的结构化参数，**不再有 `frpc.toml`**；`app/CLAUDE.md` 的 FRP 接口清单同步 |
| `docs/CHEATSHEET.md` | 若有 frp 配置示例，改为 JSON |

---

## 14. 与 `LINUX_COMPATIBILITY_PLAN.md` §5.10 的对照

§5.10 是上一次改造（子进程 → 库内调用）的定案文档。本次改造**建立在它之上**，
逐条核对结果如下 —— 有采纳、有修正、也有它留下的未办项。

### 14.1 §5.10 提到、本次**采纳**的

| §5.10 出处 | 内容 | 本次如何用 |
|---|---|---|
| §5.10.3 收益表「运行状态」行 | `svr.StatusExporter()` 能给出**每条 proxy** 的状态 | **本文档初稿漏了**，现补入 §6.3。对端口**范围**映射价值极高：范围里单个端口被 frps 侧占用是最常见故障，二值状态对此完全失语 |
| §5.10.3 收益表「改配置」行 | `svr.UpdateAllConfigurer(...)` 热更新，标注「可选，非必须」 | **在 TOML 时代那个标注是对的**（改配置=重写文件=反正要重启）；表单化后升级为**应该做**，见 §7.2.1。改用语义更完整的 `UpdateConfigSource` |
| §5.10.4 坑 #5 | frp 不承诺 `client` 包 API 稳定，已有破坏性变更，必须钉死版本 | **直接改变了 §4.3 的做法**：从「手搓 `v1.TCPProxyConfig`」改为「生成 schema 字节交给 `config.LoadConfigure`」。见决策 D5 |
| §5.10.4 坑 #1 | 崩溃隔离没了，frp 任意 goroutine 的 panic 会带走整个 asa-server | 表单让用户能一键生成上百条代理 = **配置面变宽 = 触发 frp 边缘路径的面变宽**。§5 的「单规则 ≤ 64 端口 / 总代理 ≤ 128」上限因此不只是保护 frps，**也是保护本进程** |
| §5.10.4 坑 #6 + §5.10.6 F3 | 「连续 Restart 50 次 goroutine 不单调增长」是硬性验收 | `manager_test.go` 的这条**必须保留**，只改配置构造方式。§7.5 已列 |
| §5.10.4 坑 #7 | `loginFailExit=true` 下 `svr.Run` 的返回值是确定性的成败判据，替掉了 500ms 猜测 | 这正是 R7/§6.3 `Message` 的数据来源。也是决策 D2 保持 `true` 的依据 |

### 14.2 §5.10 说过、但**已被本次改造推翻**的

| §5.10 出处 | 当时的话 | 现在 |
|---|---|---|
| §5.10.2 结尾 | 「配置文件格式、路径、以及现有 `frpc.toml` 的读写与前端编辑**全部不变** —— `api.go` 的 293 行一行不用动」 | 那是上一次改造**刻意划的边界**（只换发动机、不动方向盘），不是长期结论。本次正是来动这一层的 |
| §5.10.6 F2 判据 | 「`api.go` **零改动**、前端零改动」 | 本次反过来：**`manager.go` 的运行机制基本不动**（Start/Stop/asyncRun/`m.svr != svr` 守卫全部保留），改的是配置面与 `api.go`。两次改造的切面正交 —— 这也是 §5.10 的 F3 泄漏验收在本次**仍然有效**的原因 |
| §5.10.4 坑 #3 | 「`LogWriter` 已经在做 ANSI 清洗 + 按行转发，**这段适配是白拿的**」 | 白拿是对的，但 ANSI 清洗那半段在库内调用后**失效了**（golib 的颜色只在 `ConsoleWriter.WriteLog` 加，我们的 writer 只实现 `io.Writer` → 走明文分支）。这是**迁移时正确保留、迁移后应当回收**的东西，不是当初写错，见 R4 |

### 14.3 §5.10 留下的**未办项**，本次一并收掉

| 出处 | 要求 | 现状 | 处置 |
|---|---|---|---|
| §5.10.4 坑 #8 | frp 是 Apache-2.0，链接进二进制需随分发附上 **Apache-2.0 全文与 NOTICE 归属**；「仓库目前连 LICENSE 文件都没有，这项要一并补上」 | 落地时复核：`THIRD_PARTY_NOTICES.md` **已含 Apache-2.0 全文 + frp 归属，分发义务已履行**（初稿把这条写成「只做了一半」，不准确）。仍缺的是仓库**自身**的 `LICENSE` | **不在本次范围**：项目用什么许可证是仓库所有者的授权决定，与 frp 引入带来的义务无关，不代为选定 |
| §5.10.4 坑 #5 | 「**必须在 go.mod 里钉死具体版本**，升级当作一次小改造而不是 `go get -u`」 | `go.mod` 已是 `v0.71.0`（非 `latest`），符合 | 无需处理。但 D5 之后升级风险进一步降低（依赖 schema 而非 struct），可在 `go.mod` 旁留一行注释说明为何不随手升 |

### 14.4 §5.10 的结论中，本次**确认仍然成立**的

- 入口是顶层 `client` 包而**不是** `pkg/sdk/client`（后者是 frpc 管理端 HTTP API 的客户端，
  §5.10.2 开头的那个警告）—— 本次没有引入管理端口，结论不变。
- 不要调 `frplog.InitLogger`，自己 `New` 一个 Logger 塞进 `frplog.Logger`（§5.10.4 坑 #3）——
  `manager.go:56` 那段**必须原样保留**，且这也是 R6（配置里的 `log.*` 是惰性字段）的成因。
- `CGO_ENABLED=0 GOOS=linux` 编译不受影响（§5.9 / 坑 #4）——本次不新增任何依赖，
  连 `pelletier/go-toml` 都因为改走 JSON 而少用一条路径。

---

## 15. 落地顺序

| 步 | 内容 | 判据 |
|---|---|---|
| **S1** | `config.go`：类型 + `Validate` + `build`（走 `LoadConfigure`，§4.3）+ JSON 读写 + 单测 | 纯逻辑，不碰运行时；`build()` 的展开结果逐条断言 |
| **S2** | `manager.go`：`buildService` 改吃结构体并回传 `*source.ConfigSource`；清 R2/R3/R4/R5/R6/R9/R10；`LogWriter` 分级 | `manager_test.go` 的 **50 次 Start/Stop 无泄漏**（§5.10 F3）仍绿 |
| **S3** | `Status()` + `SetConfig()` 热更新分流（§6.3 / §7.2.1） | 热更新后再触发一次 reload，配置**不回滚**（`UpdateConfigSource` vs `UpdateAllConfigurer` 的坑） |
| **S4** | `api.go`：接口结构化 + `FRPStatus` 统一 + R7/R8 | `/status` 与 `/status/stream` 同形 |
| **S5** | 接线：R1（svcmgr 去重）+ `ErrNotConfigured` 日志降级 | 服务模式启动日志里没有 "frpc is already running" |
| **S6** | 迁移逻辑 + 迁移单测（§9） | 现网那份模板 TOML → `{9310-9319, udp}` |
| **S7** | 前端表单（§8） | `npm run build` 通过，不再 import monaco |
| **S8** | 补根 `LICENSE`（§14.3 的 §5.10 未办项）+ 文档联动（§13）+ 真机验收（§12） | —— |

S1–S6 每步都能独立 `go build` / `go test`；S4 之后后端接口已稳定，S7 可与 S5/S6 并行。

---

## 16. 落地记录（2026-09-08）

计划本身按 §15 顺序执行完毕。执行中发现三件计划里没有的事，都已处理：

### 16.1 🔴 修掉一个会打死整个进程的真实缺陷（计划外）

`go test -race` 报出 `client.Service.cancel` 的读写竞争（写在 `Run` 开头 service.go:229，
读在 `GracefulClose` service.go:420）。顺着查下去发现这不只是形式上的竞争：

**`svr.cancel` 是 `Run` 开头才赋值的，而我们从另一个 goroutine 调 `GracefulClose` 去读它。**
`Start()` 之后立刻 `Stop()` / `Restart()` 时，`Run` 可能还没执行到那一行 ——
`svr.cancel(nil)` 就是在调一个 nil 函数，**当场 nil 指针 panic**。而 frp 是库内调用、
没有崩溃隔离（§5.10.4 坑 #1），这一下会带走整个 asa-server 与所有实例的管理能力。

上游 `cmd/frpc/sub/root.go` 的 `handleTermSignal` 是**完全相同的写法**，所以这是 frp 的
问题，不是我们用错 API。

**处置**：`FrpcManager` 自己持有喂给 `Run` 的 ctx 并 cancel 它 —— `Run` 的收尾
`<-svr.ctx.Done(); svr.stop()` 派生自我们传进去的 ctx，走的是同一个关闭流程。
代价只有 `gracefulShutdownDuration` 归 0（即 `ctl.GracefulClose` 里那句 `time.Sleep(d)`
没了），对 tcp/udp 端口转发没有意义 —— 上游自己也只在 kcp/quic 下才在意优雅关闭。

**回归用例** `TestStopImmediatelyAfterStart`：100 次零间隔 Start/Stop。
已验证旧写法在这条用例下当场 `panic: invalid memory address or nil pointer dereference`。

### 16.2 F3 泄漏验收此前一直没测到它标称的场景（计划外）

改造时发现 `manager_test.go` 的 `leakConfig`（`loginFailExit=false`，意在压「登录失败后
退避重试」路径）被写到了 `{base}`，而 `Initialize` 的 runDir 是 `{base}/frp` ——
**那份配置从来没被读到过**，跑的一直是自动生成的默认配置（`loginFailExit` 同样是 true，
走的是立即失败路径，`GracefulClose` 根本没被调到）。也就是说 §5.10.4 坑 #6 点名的场景
从 F3 落地起就没被覆盖。

**处置**：路径修正，并新增 `TestRetryLoopCloseNoGoroutineLeak` —— 手动把 `LoginFailExit`
翻成 false，直接驱动 `newService` + `Run` + ctx cancel 50 轮，真正压住那条路径。
为此把 `buildService` 拆出了 `newService(common, proxyCfgs)` 一半。

### 16.3 §4.3 的 `Complete()` 必须自己调（实现细节订正）

`config.LoadConfigure` **只解码不补默认值**：`Common.Complete()` 是
`LoadClientConfigResult` 内部单独调的一步。走内存字节这条路必须自己补上，
否则 `NatHoleSTUNServer` / `LoginFailExit` / `UDPPacketSize` / `Auth.Method` 全是零值。
`build()` 里已加，`TestBuildExpandsProxies` 对 `Auth.Method == "token"` 与
`UDPPacketSize != 0` 做了断言 —— 它们是「Complete 被调过」的哨兵。

### 16.4 验收现状

已完成：`go build ./...`、`go vet ./internal/...`、`go test -race ./internal/frpmanage/`
（含 50 次泄漏回归 ×2、100 次零间隔 Start/Stop、校验/解析/展开/落盘/迁移全套）、
`npm run build`、全仓库 `frpc.toml` 只剩迁移代码与文档。

**未完成（需真机）**：拿现网 `E:\asa_server_data\frp\frpc.toml` 跑真实迁移并验证隧道可用；
`service install/start` 确认无 "frpc is already running"；热更新不回滚的真连接验证；
单条代理失败可见（占用范围内某个端口）。见 §12。
