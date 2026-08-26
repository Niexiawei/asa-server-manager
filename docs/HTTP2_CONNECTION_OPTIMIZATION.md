# HTTP/2 连接数优化方案

> **状态**：**已实施**（单 TLS 监听器方案，见 [§10 实施记录](#10-实施记录)）
> **背景问题**：常驻 SSE 连接挤占浏览器每源 6 条 HTTP/1.1 连接的额度，后续功能没有连接可用

---

## 1. 现状盘点

### 1.1 前端常驻连接清单

| 类型 | 端点 | 建立位置 | 生命周期 |
|------|------|----------|----------|
| WS | `/api/ws/events` | `workers/wsWorker.js` | **全程常驻**（Web Worker 内，防标签页休眠） |
| WS | `/api/ws/rcon` | `store/rconStore.js` | RCON 终端打开时 |
| SSE | `/api/server/all-info` | `workers/sharedResourceWorker.js` | SharedWorker，**跨标签页共享一条** |
| SSE | `/api/server/info` | `workers/serverResourceWorker.js` | 全局资源监控组件挂载时 |
| SSE | `/api/logs/:name` | `components/LogViewer.vue` | 实例日志面板（game 通道） |
| SSE | `/api/logs/:name/asaapi` | `components/LogViewer.vue` | 实例日志面板（asaapi 通道，与上一条**并存**） |
| SSE | `/api/logs` | `SystemLogs.vue` / `FRPManager.vue` / `SyncthingManager.vue` | 三个页面各自开一条 |
| SSE | `/api/frp/status/stream` | `FRPManager.vue` | FRP 页面 |
| SSE | `/api/syncthing/status/stream` | `SyncthingManager.vue` | Syncthing 页面 |
| SSE | `/api/server/batch/logs` | `components/BatchOperationDialog.vue` | 批量操作弹窗打开时 |
| SSE | `/api/server/update` | 更新流程 | 更新期间 |

后端 SSE 端点分布在 `webapi/serverapi`（3）、`webapi/logapi`（3）、`batchmanage`（1）、
`frpmanage`（1）、`syncthingmanage`（1），共 9 个。

### 1.2 先纠正一处：WebSocket 不占那 6 条额度

浏览器的「每源 6 条」限制作用于 **HTTP/1.1 连接池**，WebSocket 走的是独立配额：

- Chrome：每 host 约 255 条 WS
- Firefox：`network.websocket.max-connections` 默认 200

所以两条 WS（events + rcon）**不消耗** HTTP 额度。真正吃掉 6 条额度的只有 **SSE 和普通
REST 请求**——它们共用同一个连接池。

这个区别很重要，因为它改变了故障的形态。

### 1.3 真正的失败模式：REST 被 SSE 饿死

SSE 是永不结束的响应，一条 SSE 会**长期占住**一条 HTTP/1.1 连接。于是：

```
6 条额度 - N 条常驻 SSE = 留给所有 axios 请求的并发数
```

一个真实的重场景（单标签页，Chrome）：

| 场景 | 常驻 SSE | 剩余额度 |
|------|---------|---------|
| 首页（资源监控） | 1 | 5 |
| + 实例详情（game + asaapi 日志） | 3 | 3 |
| + 批量操作弹窗 | 4 | 2 |
| + 服务器更新中 | 5 | **1** |
| + FRP 页面（状态流 + 系统日志） | 7 | **0，REST 全部排队** |

额度耗尽时的表现**不是报错**，而是所有 axios 请求静静排队直到某条 SSE 断开——
界面看起来「卡住了」，F12 网络面板显示请求长时间 `Stalled`。这类问题极难归因，
因为它不产生任何错误日志。

雪上加霜的两点：

1. **`<KeepAlive>` 让流不随路由释放**。`App.vue` 缓存了 `ServerManager` 和 `SystemLogs`，
   切走只触发 `onDeactivated` 而非 `onBeforeUnmount`。组件若只在 `onBeforeUnmount` 里关流，
   连接会在后台一直挂着（批量日志 SSE 曾经就是这个毛病，见
   [BATCH_OPERATION.md](BATCH_OPERATION.md) §6.2）。
2. **多标签页线性放大**。除了 `sharedResourceWorker` 用 SharedWorker 做了跨页共享，
   其余每条流都是每标签页一条。开两个标签页基本必然打满。

### 1.4 为什么开发时察觉不到

`utils/utils.js` 的 `buildEventSourceUrl` 在 DEV 下**绕过 Vite 代理**直连
`http://localhost:19193`，而页面本身在 `:3000`。两者是不同的源，各有独立的 6 条额度，
所以开发环境的 SSE 和 REST 不互相挤占。**这个问题只在生产（同源内嵌）出现。**

---

## 2. 目标

1. 消除「常驻流挤占 REST 并发」这一类问题，且**不必为每个新功能精打细算连接数**
2. 不牺牲现有的实时性（日志、资源监控、批量进度）
3. 保留纯 IP 访问、FRP 内网穿透等现有部署形态的可用性
4. 开发工作流不能变复杂

---

## 3. 方案总览

| 阶段 | 手段 | 额度变化 | 结果 |
|------|------|---------|------|
| 一 | 启用 HTTP/2（TLS + 本地 CA） | 6 条连接 → **250 条并发流** | ✅ 已实施 |
| 二 | SSE 生命周期核查 | **维持现状** | ✅ 已核查，结论见 §5 |
| 三 | 兼容反向代理访问 | 同阶段一 | ✅ 已实施，单监听器，见 §6.2 |

**部署形态是硬需求，不是可选项**：本机直接访问 WebUI 与外部经 Caddy/nginx 访问
**必须同时可用**。好消息是这两者不冲突——Caddy 与 nginx 都能反代到 HTTPS 上游，
所以**一个 TLS 监听器就能同时服务两种访问方式**（[§6.2](#62-首选方案单-tls-监听器代理直接反代-https-端口)），
不需要拆双监听器。

**SSE 全部保留，不并入 WS**：它们需要「连上先回放历史、再转实时」的语义，
这是 SSE 的天然模型而非 WS 的（[§5.1](#51-sse-全部保留不并入-ws)）。

---

## 4. 阶段一：启用 HTTP/2（主方案）

### 4.1 收益

HTTP/2 把同一源的所有请求**多路复用到一条 TCP 连接**上，并发上限从「6 条连接」变成
`SETTINGS_MAX_CONCURRENT_STREAMS`（Go 默认 **250**）。9 条 SSE 全开也只占 9 个流，
REST 请求随到随走。连接数问题从此不再是设计约束。

附带收益：头部压缩（HPACK）对高频轮询接口有实际带宽收益；单连接省去多次 TCP + TLS 握手。

### 4.2 硬前提：浏览器只在 TLS 上跑 HTTP/2

**没有任何主流浏览器支持 h2c（明文 HTTP/2）。** 浏览器通过 TLS 的 ALPN 扩展协商 `h2`，
因此启用 HTTP/2 **必须**先启用 HTTPS。这是本方案唯一的实质性成本，也是决策的核心。

> `curl --http2-prior-knowledge` 能明文跑 h2，容易造成「我本地测通了」的错觉。
> 判断标准只有一个：浏览器 DevTools 网络面板的 Protocol 列显示 `h2`。

### 4.3 Go 侧改动（Go 1.26 原生支持）

项目已是 Go 1.26，`net/http` 自 1.24 起原生提供 `Protocols` 与 `HTTP2Config`，
**不需要引入 `golang.org/x/net/http2`**。

改动点在 `webapi/actions.go` 现有的 `srv := &http.Server{...}` 处：

```go
srv := &http.Server{
    Addr:    addr,
    Handler: s.engine,

    // 同时支持 HTTP/1.1 与 HTTP/2：
    // - 浏览器通过 ALPN 协商到 h2，REST 与 SSE 全部多路复用到一条连接
    // - WebSocket 握手是 HTTP/1.1 Upgrade，需要保留 HTTP1（见 4.5）
    Protocols: func() *http.Protocols {
        p := new(http.Protocols)
        p.SetHTTP1(true)
        p.SetHTTP2(true)
        return p
    }(),

    HTTP2: &http.HTTP2Config{
        // 9 条 SSE + REST 远用不到 250，留足余量即可
        MaxConcurrentStreams: 250,
        // SSE 长时间零流量，靠 PING 探活，别让中间设备把连接判死
        SendPingTimeout: 15 * time.Second,
        PingTimeout:     30 * time.Second,
    },

    TLSConfig: tlsConfig, // 见 4.4
}

// 注意是 ListenAndServeTLS：证书/私钥已在 TLSConfig 里时传空串即可
if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
    ...
}
```

> 严格说，只要用了 `ListenAndServeTLS` 且未自定义 `TLSNextProto`，Go 会**自动**启用 h2。
> 显式写 `Protocols` 的价值是意图清晰，且能防止将来有人动 `TLSConfig` 时意外关掉 h2。

### 4.4 证书策略（三选一，建议按部署形态自动降级）

本工具常见部署是「VPS 上按 IP 访问」或「FRP 穿透后按域名访问」，两者需求不同：

**A. 本地 CA + 自动写入系统受信任存储（默认，推荐）**

因为前端被打包进二进制、绝大多数场景是**本机访问 WebUI**，可以做到零警告：
程序自己签发证书，并把根证书写进 Windows 受信任根存储，浏览器直接认。
这正是 mkcert 的做法。详见 [§4.4.1](#441-自动写入系统受信任存储windows)。

代价是需要一次提权（或一次系统确认弹窗），换来的是此后完全无警告。

**A-（降级）自签证书 + 首次手动信任**

写入存储失败（无权限且用户拒绝提权）时退回这里：用户首次访问点「继续前往（不安全）」。
信任一次后同源的 SSE / WS / fetch 全部正常——因为 SPA 与 API 同源，只需信任一次。

**B. 用户自备证书（有域名时推荐）**

配置项指定 `cert_file` / `key_file` 路径，存在即优先使用。用户可以用 Let's Encrypt
签发的证书，或从别处拷贝。没有浏览器警告。

**C. 完全不启用 TLS（回退）**

保留 `--tls=false` 开关，行为与今天完全一致（HTTP/1.1 + 6 条额度）。
必须保留这条路径：局域网内网、或用户已有外层反向代理时，再套一层 TLS 是多余的。

### 4.4.1 自动写入系统受信任存储（Windows）

**结论：可行。** Windows 提供了受信任根存储的编程接口，Chrome / Edge 直接使用它，
写进去之后浏览器打开 `https://localhost:19193` 不会有任何警告。

#### 证书结构：签发一个本地 CA，而不是直接信任叶子证书

不要把服务器证书本身塞进 Root 存储——那样每次证书轮换（IP 变化、过期）都要重新写存储，
且 Chrome 对「自签叶子证书直接放 Root」的处理在各版本间并不一致。正确做法是两级：

```
ASA Server Manager Local CA   （自签，CA:TRUE，写入 Root 存储，10 年）
        └── localhost 叶子证书 （由上面的 CA 签发，1 年，随 IP 变化自动重签）
```

只有 CA 需要进存储，**且只进一次**。叶子证书随便换，浏览器都认。

叶子证书的 SAN 必须包含：

- `DNSNames`：`localhost` + 用户配置的域名
- `IPAddresses`：`127.0.0.1`、`::1` + 本机所有网卡 IP（VPS/局域网访问场景）

现代浏览器**完全忽略 CN**，只看 SAN。启动时比对当前网卡 IP 与叶子证书的 SAN，
不匹配就自动重签——这样换网络环境不需要用户干预。

私钥统一用 ECDSA P-256：比 RSA 快，证书体积也小。

#### 写入实现

两条路，建议优先 syscall（无子进程、无窗口闪烁、可精确判错）：

**syscall（推荐）**——项目已有 `windows.NewLazySystemDLL` 的绑定模式
（`pkg/winproc/win32api.go` 里的 user32/kernel32/shell32），照着加 `crypt32.dll` 即可：

```go
crypt32 := windows.NewLazySystemDLL("crypt32.dll")
procCertOpenStore                  = crypt32.NewProc("CertOpenStore")
procCertAddEncodedCertificateToStore = crypt32.NewProc("CertAddEncodedCertificateToStore")
procCertCloseStore                 = crypt32.NewProc("CertCloseStore")
```

打开 `"ROOT"` 存储，`CERT_STORE_ADD_REPLACE_EXISTING` 写入 DER 编码的 CA 证书。
存储位置按权限二选一：

| 存储 | 常量 | 需要提权 | 弹窗 | 适用 |
|------|------|---------|------|------|
| `LocalMachine\Root` | `CERT_SYSTEM_STORE_LOCAL_MACHINE` | 是 | **无** | 服务模式（LocalSystem）、已提权的 GUI |
| `CurrentUser\Root` | `CERT_SYSTEM_STORE_CURRENT_USER` | 否 | 可能一次系统确认框 | 普通用户运行 GUI |

**certutil 兜底**——`certutil -addstore -f Root <cert.crt>`（LocalMachine，需管理员）
或 `-user Root`（当前用户）。实现简单但会拉起子进程，需注意隐藏窗口
（项目里已有 `cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}` 的先例）。

#### 权限：项目现有能力刚好够用

| 运行形态 | 权限 | 策略 |
|---------|------|------|
| Windows 服务 | kardianos/service 未指定 `UserName`，默认 **LocalSystem** | 直接写 LocalMachine，静默成功 |
| GUI 已提权 | `mirror.IsElevated()` 可判断（`mirror/mirror.go:95`） | 同上 |
| GUI 未提权 | — | 先试 CurrentUser；用户需要静默安装时用 `winproc.RunAsAdmin()`（`pkg/winproc/win32api.go:219`）重启提权 |

`main.go:181` 已经有 `mirror.IsElevated()` 的调用点，判定逻辑可直接复用。

#### 幂等性

每次启动都写一遍是错的。正确做法是**按指纹（SHA-1 Thumbprint）检查**：
存储里已存在同指纹的证书就跳过。CA 被用户手动删除时，下次启动自动补装。

#### 配套 CLI

```
asa-server cert status      # 显示 CA 指纹、有效期、是否已在受信任存储中
asa-server cert install     # 手动安装（未提权时自动请求提权）
asa-server cert uninstall   # 从存储中移除 CA —— 卸载流程必须调用
```

`cert uninstall` 不是可选项：往用户系统里装根证书却不提供干净的移除手段，是不负责任的。
程序的卸载/服务移除流程也应当调用它。

#### 浏览器覆盖情况

| 浏览器 | 是否读 Windows 存储 | 说明 |
|--------|-------------------|------|
| Chrome / Edge | ✅ | 直接使用 Windows 根存储，装完即生效 |
| Firefox | ⚠️ 基本可用 | 自带 NSS 存储，但 `security.enterprise_roots.enabled` 自 FF 68 起在 Windows 上**默认为 true**，会读取系统根证书。装到 **LocalMachine** 最可靠 |

Firefox 若仍报警告，指引用户确认该 pref，或提供 CA 文件路径供手动导入。

#### 安全边界（必须遵守）

往系统信任根里装 CA 是**高权限操作**——持有该 CA 私钥的人可以对该用户伪造**任意**
HTTPS 站点。因此有几条红线：

1. **CA 私钥必须在用户本机生成，绝不能打包进二进制。** 一旦随发行版分发同一把私钥，
   任何拿到二进制的人都能 MITM 所有用户。这是此类方案唯一不可原谅的错误。
2. **私钥文件设置严格 ACL**，仅 SYSTEM + Administrators 可读，存放于 `{BaseDir}/certs/`。
3. **CA 名称必须自解释**：`ASA Server Manager Local CA`，让用户在证书管理器里看到时
   知道它是什么、从哪来。
4. **CA 只用于签发本机 localhost/内网 IP 的叶子证书**，不签发任何其它用途。
5. **默认行为要让用户知情**：首次安装时在 GUI/日志中明确告知「已向系统受信任存储写入
   本地 CA，可用 `asa-server cert uninstall` 移除」，并提供关闭开关（`--tls-trust=false`）。

#### 有效期

Chrome 对 398 天有效期的限制只针对**公开受信任**的证书，本地手动安装的根证书**不受此限**。
但为降低风险仍建议：CA 10 年，叶子 1 年并在启动时自动续签（反正续签零成本）。

### 4.5 WebSocket 怎么办

**结论：不受影响，无需改动。**

RFC 8441（WebSocket over HTTP/2 的 Extended CONNECT）目前 Go 的 `net/http`
服务端**不支持**，`gorilla/websocket` 也不支持。因此浏览器发起 WS 时会另开一条
HTTP/1.1 over TLS 连接完成 Upgrade 握手。

这没有任何问题，因为：

1. WS 本来就不占那 6 条 HTTP 额度（§1.2）
2. `Protocols` 里保留了 `SetHTTP1(true)`，服务端照常接受 HTTP/1.1 升级
3. 浏览器只有在服务端主动发 `SETTINGS_ENABLE_CONNECT_PROTOCOL` 时才会尝试 WS-over-h2，
   我们不发，它就老老实实走 HTTP/1.1

需要改的只有前端 URL 构建：`ws://` → `wss://`。`utils/utils.js` 里已经按
`window.location.protocol` 推导，确认这条逻辑覆盖 https 即可。

#### ⚠️ `SetHTTP1(true)` 不是可选项

WebSocket 升级依赖 `http.Hijacker` 劫持底层 TCP 连接，而 **HTTP/2 的 `ResponseWriter`
不实现 `Hijacker`**——h2 的流没有独占的 TCP 连接可劫持。因此一旦把 `Protocols`
配成「仅 HTTP/2」，所有 WebSocket 会直接失败：

```
websocket: response does not implement http.Hijacker
```

§4.3 的配置里 `SetHTTP1(true)` 与 `SetHTTP2(true)` **必须同时开**：
h2 服务 REST 与 SSE，HTTP/1.1 留给 WebSocket 升级。这条要写进代码注释，
否则后人「优化」掉 HTTP/1.1 时会踩爆。

#### 两个监听器上的 WebSocket 能互相广播吗

**能，且无需任何额外处理。**

`realtime` 的客户端表是**进程级全局单例**：`globalHub`（`realtime/hub.go:251`）
持有 `clients map[*websocket.Conn]*sync.Mutex`，升级成功后连接被注册进这张表
（`hub.go:97`），广播时遍历全表（`sendEventToAll`，`hub.go:122-130`）。

`http.Server` 只是**完成 accept 与 upgrade 的传输层**：握手一结束连接就被 Hijack 出来，
此后它的生命周期与哪个 Server 接的已经毫无关系。两个 Server 又共用同一个
`gin.Engine`，跑的是同一个 `/api/ws/events` handler，注册进的是同一张表。

所以：本机直连（TLS 监听）的客户端与经反代进来（明文监听）的客户端，
**在同一个广播域内，事件互通**。批量操作进度、实例状态变更等都会同时推给两边。

同理，`batchmanage` 的 `LogBroadcaster`、`updatemanage` 的广播器也都是进程级单例，
不受监听器拆分影响。

### 4.6 SSE handler 需要复核的几点

现有 SSE 写法在 h2 下基本可直接工作，但有三处要确认：

1. **`Flush()` 必须显式调用**——h2 的 `ResponseWriter` 实现了 `http.Flusher`，
   现有代码每写一帧就 `c.Writer.Flush()`，符合要求。
2. **`X-Accel-Buffering: no` 是 nginx 专用头**，h2 下无害但也无用；若将来上 Caddy 需另配。
3. **keepalive 仍然必要**。h2 有连接级 PING（已在 `HTTP2Config` 配置），但流级别的
   空闲仍可能被中间设备干掉。`batchmanage` 的 30 秒注释帧继续保留，
   其它长时间零流量的流（FRP/Syncthing 状态）建议补上同样的机制。

### 4.7 对开发工作流的影响（必须一起改）

这是最容易被忽略、却一定会绊住人的地方。`EventSource` **没有任何**忽略证书错误的手段，
所以 dev 直连 https 后端的前提是**本地 CA 已进系统受信任存储**——这正是 §4.4.1 的默认行为，
装上之后 dev 直连就和生产一样自然，不必改成走 Vite 代理（那还要动 `rewrite` 与
`VITE_API_ROOT`，牵扯更大）。

实际改动三处：

1. **`.env.development` 的 `VITE_PROXY_TARGET` 改为 `https://localhost:19193`**。
   关掉后端 TLS 时（`--tls=false`）改回 http 即可，文件里已写明。
2. **`utils.js` 的 WS 协议推导必须跟着后端走，不能跟着页面走**。原来是
   `location.protocol === 'https:' ? 'wss:' : 'ws:'` —— dev 页面是
   `http://localhost:3000`，据此推导会得到 `ws://` 去敲一个 wss 端口，必然连不上。
   改为从 `VITE_PROXY_TARGET` 的协议推导：

   ```js
   const wsTarget = proxyTarget.replace(/^https:/, 'wss:').replace(/^http:/, 'ws:')
   ```
3. **`vite.config.js` 的 `/api` 代理**已有 `secure: false`（Node 侧不校验自签证书），
   补上 `ws: true`。

生产（同源内嵌）无需任何前端改动：`buildEventSourceUrl` / `buildWebSocketUrl`
都从 `window.location` 推导协议，天然跟随 https/wss。

---

## 5. 阶段二：SSE 生命周期核查（结论：维持现状）

原计划是「离开页面即断流」，核查后**结论是不做任何改动**。两个子问题分开看：
要不要把 SSE 并入 WS（§5.1，不并），以及哪些流该随路由释放（§5.2，都不该）。

### 5.1 SSE 全部保留，不并入 WS

曾考虑把低频的 `/api/frp/status/stream` 与 `/api/syncthing/status/stream` 收编进
`/api/ws/events`，**结论是不做**。理由：

1. **它们需要回溯历史**。进入页面时要能看到之前的状态/日志，而不是从订阅那一刻才开始。
   SSE 的「连上先回放历史、再转实时」是天然契合的模型（`batchmanage` 的日志流就是这么做的，
   见 [BATCH_OPERATION.md](BATCH_OPERATION.md) §6.2）。WS 是纯广播中枢，
   要支持回溯就得在事件协议里另造一套「请求历史 / 回放响应」的往返，
   把一个单向广播通道改造成请求-响应通道，得不偿失。
2. **WS 中枢是全局广播**，所有客户端都会收到。FRP 状态只有 FRP 页面关心，
   广播给所有人属于无谓的扇出。
3. 上了 HTTP/2 之后连接数不再稀缺（§4.1），合并的收益本来就所剩无几。

因此**所有 SSE 端点维持现状**。

### 5.2 各组件的清理策略核查

`<KeepAlive>` 只缓存 `['SystemLogs', 'ServerManager']`（`App.vue:46-49`），
是否随路由释放取决于组件是否被缓存：

| 组件 | 被 KeepAlive 缓存 | 清理钩子 | 结论 |
|------|-----------------|---------|------|
| `FRPManager.vue` | ❌ 否 | `onBeforeUnmount` 关状态流 + 日志流 | ✅ 离开即断 |
| `SyncthingManager.vue` | ❌ 否 | `onBeforeUnmount` 关状态流 + 日志流 | ✅ 离开即断 |
| `BatchOperationDialog.vue` | ✅ 是（在 ServerManager 内） | `onDeactivated` + `onBeforeUnmount` | ✅ 跟随弹窗开关 |
| `SystemLogs.vue` | ✅ 是 | 只有 `onBeforeUnmount` | ✅ **有意常驻**，见下 |

FRP 与 Syncthing 页面不在 KeepAlive 名单里，路由切走就真的销毁组件，
`onBeforeUnmount`（`FRPManager.vue:330`、`SyncthingManager.vue:322`）会关掉两条流。
它们**需要**这样：状态流是页面专属的，人不在页面上就没有推送的意义。

### 5.3 为什么 `SystemLogs.vue` 的常驻流是对的

`SystemLogs.vue` 被缓存且只有 `onBeforeUnmount`，那条 `/api/logs` SSE 会跨路由一直挂着。
一度把它当成泄漏，但**这是有意为之，不要「修」**：

`/api/logs` 每次连上都会回放最近 **2000 条**系统日志。给它补 `onDeactivated`
意味着每次切回页面都要重新拉一遍两千条并重建虚拟列表 —— 后端多做一次全量回放，
前端多一次大批量渲染，用户侧的观感就是「每次进系统日志都要卡一下」。
用一条常驻连接换掉这种反复回放，是明显划算的交易。

这也正是阶段一的意义所在：**连接数不再稀缺之后，「多挂一条流」不再需要论证**，
可以纯粹按体验来选。若没有 HTTP/2，这条常驻流就得和 REST 抢那 6 条额度，
才会被迫在「体验」与「连接预算」之间做取舍。

**规矩**：凡是开长连接的组件，若自身或任一祖先在 KeepAlive 名单里，
默认应实现 `onDeactivated` 释放连接；**除非**该流重连代价高昂（大批量历史回放），
此时刻意常驻，并像本节这样写明理由——否则后人会把它当泄漏顺手「修掉」。
`BatchOperationDialog.vue` 是前者的参照实现。

---

## 6. 同时兼容本机访问与反向代理

### 6.1 先厘清一件事：代理模式下 Go 侧不需要 TLS，也不需要 HTTP/2

浏览器的 6 连接限制**只作用于「浏览器 ↔ 第一跳」**。经反向代理访问时，这一跳是
浏览器 ↔ Caddy/nginx，由代理提供 h2 即可解决；而「代理 ↔ Go」那一跳是服务端之间的
连接，代理自己管理连接池，不受 6 条限制约束，用 HTTP/1.1 明文完全够用。

**所以不要去折腾 h2c 上游。** 给上游配 HTTP/2 在本场景零收益，只会徒增复杂度
（Caddy 需 `transport http { versions h2c }`，nginx 需 `grpc_pass` 之类的绕法）。

### 6.2 首选方案：单 TLS 监听器，代理直接反代 HTTPS 端口

**Caddy 与 nginx 都能反代到 HTTPS 上游并跳过证书校验**，因此
「本机直连 + 外部反代」**可以只用一个监听器**满足，不必拆两个：

```
浏览器（本机）  ──── https://localhost:19193 ────┐
                                                 ├──►  Go 单 TLS 监听 :19193 (h2)
浏览器（外部）── https://asa.example.com ─► Caddy ┘   （代理跳过证书校验或信任本地 CA）
```

这是**推荐做法**，优点很实在：

- 只有一个监听端口，不多占端口、不需要额外防火墙说明
- 不存在「明文监听误绑 `0.0.0.0`」这个高危陷阱（见 §6.3）
- 证书、`Protocols`、超时等只有一套配置，不会两边漂移

代价只有回环上多一层 TLS 加解密，现代 CPU 上完全可忽略。

**跳过校验 vs 信任本地 CA**：同机回环（`127.0.0.1`）用 `tls_insecure_skip_verify`
足够安全——流量不出网卡，没有中间人的位置。**代理与 Go 跨机器时应当改为信任本地 CA**，
叶子证书的 SAN 已包含本机所有网卡 IP（§4.4.1），校验能通过，不必降级成跳过校验。

### 6.3 可选方案：双监听器

以下情况才值得拆两个监听器：

- 不想在代理与 Go 之间承担 TLS 开销（极高频场景，本项目基本不会遇到）
- 完全不打算启用本地 CA（纯代理部署，本机也从不直连）
- 代理侧无法配置 HTTPS 上游（某些老旧或受限的反代）

| 模式 | TLS 监听 | 明文监听 | 浏览器侧 h2 由谁提供 | 本地 CA |
|------|---------|---------|-------------------|--------|
| `local`（默认） | `:19193` HTTPS+h2 | — | Go 自己 | 需要，自动安装 |
| `proxy` | — | `127.0.0.1:19193` | Caddy/nginx | **不需要** |
| `dual` | `:19193` HTTPS+h2 | `127.0.0.1:19194` | 各自 | 需要（仅为本机访问） |

`--tls-trust`（是否往系统装本地 CA）应当**跟随模式**：`proxy` 模式下默认关闭——
既然没人会直连 Go 的 TLS 端口，就不该往用户系统里装根证书。

> 建议实现顺序：先只做 `local`（单 TLS 监听），把 §6.5 的反代配置写进文档即可覆盖两种访问方式。
> `proxy` / `dual` 模式等到确有用户需要再加。

### 6.4 Go 侧实现：多个 Server 共用同一个 engine（**未实施**，仅供将来参考）

> 实际落地的是 §6.2 的单监听器：`webapi/actions.go` 仍然只有一个 `srv`，
> 只是加上了 `Protocols` / `HTTP2` / `TLSConfig` 并改走 `ListenAndServeTLS`。
> 本节保留下来，是为了万一将来真要拆双监听器时不必重新推导。

拆分时按模式起 1–2 个 Server：

```go
// 两个监听器共用同一个 gin engine，路由、中间件、状态完全一致
var servers []*http.Server

if mode == ModeLocal || mode == ModeDual {
    servers = append(servers, &http.Server{
        Addr:      fmt.Sprintf(":%d", s.port),   // 对外可达
        Handler:   s.engine,
        Protocols: protocolsH1H2(),              // 见 §4.3
        HTTP2:     http2Config(),
        TLSConfig: tlsConfig,
    })
}

if mode == ModeProxy || mode == ModeDual {
    servers = append(servers, &http.Server{
        // 只绑回环！绑 :port 等于把未加密的管理接口直接暴露到公网
        Addr:      fmt.Sprintf("127.0.0.1:%d", s.upstreamPort),
        Handler:   s.engine,
        Protocols: protocolsH1Only(),  // 上游不需要 h2，见 §6.1
    })
}
```

`Stop()` 里需要遍历关闭全部监听器，现有的 30 秒 `srv.Shutdown` 逻辑改成并发等待所有 server。

**明文监听必须绑 `127.0.0.1`**。当前代码是 `addr := fmt.Sprintf(":%d", s.port)`
（`webapi/actions.go:114`），绑的是 `0.0.0.0`。明文监听照抄这一行会导致
**未加密的管理接口直接暴露到公网** —— 这是本方案里最危险的一处，务必显式写回环地址。

### 6.5 必须设置 gin 的可信代理（已实施）

改造前代码**没有调用 `SetTrustedProxies`**，而 gin 的默认行为是**信任所有代理**，
即无条件采信 `X-Forwarded-For`。一旦前面挂了反代，任何客户端都能伪造自己的来源 IP。
现在 `NewAPIServer()` 里：

```go
// 只信任本机回环上的反向代理；直连场景下 X-Forwarded-For 一律忽略
if err := engine.SetTrustedProxies(parseTrustedProxies(TrustedProxies)); err != nil {
    logger.GetLogger().Warnf("设置可信代理失败，将忽略 X-Forwarded-For: %v", err)
    _ = engine.SetTrustedProxies(nil)
}
```

默认 `127.0.0.1,::1`，代理在另一台机器时用 `--trusted-proxies` 指定它的实际 IP，
留空表示谁都不信任。解析失败时**收紧**（谁都不信）而不是放开——这类开关出错时
必须往安全的方向倒。

### 6.6 反向代理配置

以下是 **§6.2 首选方案**的配置：代理直连 Go 的 **HTTPS** 端口。
若采用双监听器（§6.3），把上游改成 `http://127.0.0.1:19194` 并去掉 TLS 相关行即可。

**Caddy**（自动申请对外证书、默认对浏览器启用 h2）：

```caddyfile
asa.example.com {
    reverse_proxy https://127.0.0.1:19193 {
        transport http {
            # 同机回环：跳过校验足够安全，流量不出网卡，没有中间人的位置
            tls_insecure_skip_verify

            # 跨机器时删掉上面一行，改为信任本地 CA（叶子证书 SAN 已含各网卡 IP）：
            # tls_trusted_ca_certs /path/to/asa-local-ca.crt
        }

        # 关键：禁用响应缓冲，否则 SSE 会被攒着不发，
        # 表现为「日志一批一批地跳」而不是实时滚动
        flush_interval -1
    }
}
```
WebSocket 无需额外配置，Caddy 自动处理 Upgrade。

**nginx**：

```nginx
server {
    listen 443 ssl;
    http2 on;                        # 对浏览器启用 h2 —— 本方案的收益来源
    server_name asa.example.com;

    ssl_certificate     /etc/ssl/asa.crt;
    ssl_certificate_key /etc/ssl/asa.key;

    location / {
        proxy_pass https://127.0.0.1:19193;
        proxy_http_version 1.1;      # 缺这行 WebSocket 无法升级

        # nginx 的 proxy_ssl_verify 默认就是 off，此处显式写出以表明意图。
        # 跨机器时改为：
        #   proxy_ssl_verify on;
        #   proxy_ssl_trusted_certificate /path/to/asa-local-ca.crt;
        proxy_ssl_verify      off;
        proxy_ssl_server_name on;    # 发送 SNI，让 Go 选对证书

        # WebSocket 升级
        proxy_set_header Upgrade    $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        # SSE：关缓冲 + 放宽读超时，否则 60 秒默认值会掐断空闲流
        proxy_buffering    off;
        proxy_cache        off;
        proxy_read_timeout 3600s;

        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}

map $http_upgrade $connection_upgrade {   # 放在 http {} 块内
    default upgrade;
    ''      close;
}
```

两点说明：

- **上游协商到的是 HTTP/1.1，这是对的**。nginx/Caddy 在 ALPN 里只报
  `http/1.1`，Go 会照此协商——上游本来就不需要 h2（§6.1），而 WebSocket 升级
  还**必须**走 HTTP/1.1（§4.5）。这也是 `Protocols` 必须保留 `SetHTTP1(true)` 的又一个理由。
- `proxy_read_timeout` 即便配了，§4.6 的 30 秒 keepalive 帧仍要保留——
  它是应用层的兜底，不依赖任何一家代理配得对。

### 6.7 前端无需改动（已验证）

`app/src/utils/utils.js` 的 URL 构建已经天然兼容反代：

- `buildEventSourceUrl` / `buildWebSocketUrl` 都从 `window.location` 推导，
  协议按 `location.protocol === 'https:'` 选择 `https/wss`
- 两者都拼入了 `window.location.pathname`，**子路径部署**（如 `https://host/asa/`）也已支持

唯一要改的是 §4.7 里 DEV 分支的协议推导，与本节无关。

---

## 7. 兼容性与回退

命令行开关（全部为全局标志，`asa-server api` 与 GUI 都适用）：

| 标志 | 默认 | 作用 |
|------|------|------|
| `--tls` | `true` | 关掉即退回明文 HTTP/1.1，与改造前完全一致 |
| `--tls-trust` | `true` | 是否把本地 CA 写入系统受信任存储 |
| `--cert-file` / `--key-file` | 空 | 自备证书；给出后不生成本地 CA，也不碰系统存储 |
| `--tls-domains` | 空 | 追加进证书 SAN 的域名，逗号分隔 |
| `--trusted-proxies` | `127.0.0.1,::1` | 允许设置 `X-Forwarded-For` 的来源；留空表示谁都不信 |

- **随时可退回**：`--tls=false` 等价于改造前的行为，是遇到任何 TLS 相关意外时的第一手段
- **自动降级**：证书准备失败时程序不会启动失败，而是记 ERROR 后退回明文 HTTP
- **纯代理部署**：本机从不直连时用 `--tls-trust=false`，既然没人访问 Go 的 TLS 端口，
  就不该往用户系统里装根证书
- **FRP 穿透**：TCP 模式是字节透传，TLS 端到端加密，不受影响；
  若用户配的是 FRP 的 HTTP 模式，需改为 TCP，或用 `--tls=false` 把 TLS 交给 FRP 侧
- **Windows 服务模式**：证书在 `{BaseDir}/certs/`，服务以 LocalSystem 运行，
  CA 会装进 `LocalMachine\Root`（对所有用户与 Firefox 都最可靠）
- **GUI**：跳转链接由 `webapi.Scheme()` 生成，跟随 `--tls`

---

## 8. 验证方法

**协议是否真的升级：**

```
浏览器 DevTools → Network → 右键表头启用 Protocol 列
```
所有 REST 与 SSE 请求应显示 `h2`；WebSocket 显示 `http/1.1`（符合预期，见 §4.5）。

```bash
# 命令行确认 ALPN 协商结果
curl -vk --http2 https://localhost:19193/health 2>&1 | grep -i "ALPN\|HTTP/2"
```

**连接数是否真的收敛：**

```
chrome://net-export/  →  抓取一段会话  →  用 netlog-viewer 看 socket 数量
```
升级前：同源 6 条 HTTP/1.1 socket 打满；升级后：1 条 h2 socket + 1 条 WS socket。

**饿死问题是否消失（核心验收）：**

同时打开 首页 + 实例日志（game & asaapi）+ 批量弹窗 + FRP 页面（共 ≥7 条流），
然后触发若干 REST 操作（切换实例、读配置）。升级前请求会 `Stalled` 数秒到无限；
升级后应当即时返回。

**受信任存储是否生效：**

```powershell
# 确认 CA 已在存储中（LocalMachine 或 CurrentUser）
Get-ChildItem Cert:\LocalMachine\Root | Where-Object { $_.Subject -like "*ASA Server Manager*" }
Get-ChildItem Cert:\CurrentUser\Root  | Where-Object { $_.Subject -like "*ASA Server Manager*" }
```

浏览器打开 `https://localhost:19193` 应**直接进入页面**，地址栏是正常的锁形图标，
没有任何插页警告。Chrome / Edge / Firefox 各测一次。

再验幂等与可逆：连续重启程序三次，存储里应始终只有**一份** CA（按指纹去重）；
执行 `asa-server cert uninstall` 后上面的 PowerShell 查询应返回空，浏览器恢复警告。

**本机直连与反代访问互不干扰（核心验收）：**

浏览器两条路径同时打开：本机 `https://localhost:19193`（无警告、Protocol 显示 h2），
外部 `https://asa.example.com`（经 Caddy/nginx，Protocol 同样是 h2）。
两边各开一个实例日志页，确认 SSE 都实时滚动、互不影响。

**跨监听器的 WebSocket 广播（若采用双监听器方案）：**

两条路径各开一个标签页，在其中一边发起批量停服。
另一边应**同步**看到实例状态变化与批量进度——两者共用同一个 `realtime` 全局中枢（§4.5），
事件互通。收不到就说明 engine 或 hub 被意外拆成了两份。

**上游 TLS 反代是否正常：**

```bash
# 代理侧模拟：跳过校验访问 Go 的 TLS 端口
curl -vk https://127.0.0.1:19193/health

# WebSocket 经代理仍走 HTTP/1.1（预期，见 §4.5）
# 浏览器 DevTools 里 /api/ws/events 的 Protocol 应为 http/1.1 且连接成功
```

**来源 IP 未被伪造：**

```bash
curl -H "X-Forwarded-For: 1.2.3.4" https://localhost:19193/health
```
直连时该头应被忽略（日志里记录的是真实来源），经代理时才采信。

**SSE 行为未退化：**

逐一确认日志实时滚动、资源监控 2 秒刷新、批量日志推送、断线重连、
以及 §4.6 的 keepalive 在空闲 5 分钟后连接仍存活。

---

## 9. 风险与取舍

| 风险 | 影响 | 缓解 |
|------|------|------|
| **CA 私钥泄露 = 可 MITM 该用户所有 HTTPS** | 严重 | 私钥仅本机生成、绝不打包进二进制、严格 ACL（§4.4.1 红线 1–2） |
| 用户不接受「程序往系统装根证书」 | 抵触、被杀软拦截 | 明确告知 + 提供 `cert uninstall` 与 `--tls-trust=false` 开关 |
| 写入存储失败（无权限且拒绝提权） | 回退到浏览器警告 | 降级到方案 A-，功能不受影响，仅体验退化 |
| Firefox 仍报警告 | 少数用户 | 装到 LocalMachine；必要时指引 `security.enterprise_roots.enabled` |
| **明文监听误绑 `0.0.0.0`** | **未加密管理接口暴露公网** | ✅ 单监听器方案里根本没有明文监听器，这个陷阱不存在——这也是选 §6.2 的主要理由 |
| 未设 `SetTrustedProxies` | 客户端可伪造来源 IP | ✅ 已修（§6.5），默认只信任回环 |
| 证书准备失败导致服务起不来 | 管理面板完全进不去 | ✅ 已降级为「记 ERROR + 退回明文 HTTP」，功能不受影响 |
| 代理未关缓冲 | SSE 变成批量跳动，疑似「卡住」 | 配置模板里标注为必填项；应用层 30 秒 keepalive 兜底 |
| 用户在 FRP HTTP 模式下开 TLS | 双重 TLS 导致无法访问 | 目前需用户自行改用 TCP 模式或 `--tls=false`；**尚未做启动检测** |
| 单连接故障域集中 | 一条 h2 连接断开，所有流同时中断 | 现有各流已有独立重连逻辑；h2 层面靠 PING 提前发现 |
| 队头阻塞（TCP 层） | 丢包时所有流一起卡 | 局域网/VPS 场景影响可忽略；彻底解决需 HTTP/3，不在本方案范围 |
| 开发环境改动遗漏 | 本地跑不起来 | ✅ §4.7 的三处改动已同批落地 |

**明确不做的：**

- 不引入 `golang.org/x/net/http2`——标准库已足够
- 不实现 RFC 8441（WS over h2）——收益为零（WS 不占额度），成本是自己实现 Extended CONNECT
- 不上 HTTP/3——队头阻塞在本场景不是瓶颈，QUIC 的运维复杂度不划算

---

## 10. 实施记录

采用 **§6.2 单 TLS 监听器**方案。落地的代码：

| 位置 | 内容 |
|------|------|
| `certmgr/certmgr.go` | `EnsureTLSConfig()`：自备证书优先，否则本地 CA + 叶子证书，可选写入受信任存储 |
| `certmgr/ca.go` | ECDSA P-256 的 CA（10 年）与叶子（1 年）签发；SAN 变化或临期自动重签 |
| `certmgr/store.go` | 受信任根存储读写（`golang.org/x/sys/windows` 的 `Cert*` 绑定），按 SHA-1 指纹幂等 |
| `certmgr/cli.go` | `asa-server cert status / install / uninstall` |
| `webapi/transport.go` | `protocolsH1H2()`、`http2Config()`、`parseTrustedProxies()`、`Scheme()` |
| `webapi/actions.go` | `ListenAndServeTLS`、TLS 开关与降级、`SetTrustedProxies` |
| `main.go` | `--tls` / `--tls-trust` / `--cert-file` / `--key-file` / `--tls-domains` / `--trusted-proxies` |
| `svcmgr/service.go`（原 `winservice/service.go`） | `RemoveService()` 联动 `UntrustCAOnCleanup()` |
| `gui/gui.go` | WebUI 链接按 `webapi.Scheme()` 生成 |
| 前端 | §4.7 的三处 dev 改动 |

几个与原方案不同的决定：

- **默认开启 TLS 与受信任存储写入**（`--tls=true` / `--tls-trust=true`）。原计划分三步、
  先默认关闭，但本地 CA 方案让「开着」不带来任何用户可感知的成本，没有理由默认退化。
- **证书失败不阻断启动**。`EnsureTLSConfig` 出错时记 ERROR 并**退回明文 HTTP**，
  管理面板照常可用，只是失去多路复用。管理工具的首要属性是「进得去」。
- **`cert install` 会顺手生成 CA**，不再要求用户先起一次 API 服务器。
- **`SystemLogs.vue` 不动**（§5.3）。

**双监听器（§6.3）没有做**，也不建议做——单 TLS 监听已同时满足本机直连与反代访问。

### 10.1 已验证 / 待人工验证

自动化覆盖（`go test ./certmgr/... ./webapi/`）：

- `TestALPNNegotiatesHTTP2` —— 用与生产完全相同的 `Protocols` / `HTTP2Config` / 本地 CA
  起监听器，客户端实测协商到 `HTTP/2.0`。这是本方案的核心验收，不能只靠人工点浏览器
- `TestHTTP1RemainsAvailableForWebSocket` —— HTTP/1.1 仍可用且 `ResponseWriter`
  实现 `http.Hijacker`，守住 §4.5 那条红线
- `TestEnsureLeafIsSignedByCAAndCoversLoopback` / `TestLeafIsReusedUntilSANsChange`
  —— 签发链路与「SAN 不变则复用、变了才重签」
- `TestParseTrustedProxies` —— 空配置解析为 nil（谁都不信任）

CLI 实测：`cert install` → 存储中出现指纹 `29DC…63BC` → 重复 install 仍只有一份 →
`cert uninstall` → 存储清空。

**仍需人工过一遍**（自动化测不了浏览器）：§8 的 DevTools Protocol 列、
无警告直达页面、以及反代访问。

---

## 11. 参考

- RFC 7540 §5.1.2 —— HTTP/2 并发流限制
- RFC 8441 —— Bootstrapping WebSockets with HTTP/2（本方案不实现）
- Go 1.24 release notes —— `http.Server.Protocols`、`http.HTTP2Config`
- [BATCH_OPERATION.md](BATCH_OPERATION.md) §6.2 —— SSE 长连接契约与 keepalive 实现
- [ARCHITECTURE.md](ARCHITECTURE.md) —— 系统架构
