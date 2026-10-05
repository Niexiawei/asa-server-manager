# 管理器互控（gRPC + 协调节点）可行性评估与实施计划

> 状态：§11 全部决策已定（2026-10-04）。**P1 代码已完成（2026-10-05，分支 `feat/remote-mesh-p1`，现已改名 `feat/remote-mesh`），
> 单测两个平台通过，真机验收待做**——见 §12「P1 实施记录」。**P2～P4 已细化（2026-10-05），待确认后开工**，三期都在 `feat/remote-mesh` 上做。
> D4（打洞）本期不做，技术方案已定在 §5.6；**例外：协调节点的 STUN 端点本期先实现**（2026-10-05，细化见 §12「P1-8」）。
> D6（多网络）选 B「数据模型预留」，见 §8.4。
>
> **与 simple-file-sync 的关系（已定）**：那是一个独立的同步库，本功能**不复用它的任何功能、不 import 它的任何包**，
> 协调节点也与它的协调端毫无关系。唯一参考的是两个**交互形态**：「一行 join blob 接入」与「配对」，
> 实现全部在本仓库里自己写（§3.1）。
>
> 目标：让一台机器上的 asa-server（下称**管理器**）能控制另一台机器上的管理器。
> 管理器之间走 **gRPC**；大多数管理器在内网（NAT 后），所以增加一个单独部署在公网的
> **协调节点（Coordinator）** 负责牵线与中转；两台管理器**在同一内网**或**都能被公网直接访问**时直接通信，
> 不经过协调节点中转。

---

## 1. 结论先行

| 问题 | 结论 |
|---|---|
| **能不能做？** | **能**，且没有技术上的硬阻断。所需的每一块（gRPC 跑在自定义 `net.Conn` 上、经中转的字节流、`httputil.ReverseProxy` 配自定义 `RoundTripper`）都是 Go 生态里成熟的做法。`google.golang.org/grpc` 由本功能**作为自己的直接依赖**声明（版本与依赖图里现有的一致，不依赖它经谁间接引入），新增依赖面很小。 |
| **推荐形态** | 协调节点只做**三件事：在线登记、交换地址、字节中转**，不理解业务。管理器之间在直连或中转的字节流之上**再跑一层端到端 mTLS**，协调节点看不到内容、也冒充不了任何一台管理器。 |
| **业务载荷** | 先做 **「HTTP over gRPC」隧道**：把对远端的请求原样转给远端现成的 Gin 路由，前端切换「当前机器」即可复用全部页面（含 SSE/WS）。**类型化的 gRPC 业务接口**（跨机汇总、跨机批量）放到后期按需加（§11-D1）。 |
| **信任模型** | **每台管理器自己生成密钥，节点 ID = 公钥指纹**（Syncthing 设备 ID 的思路），配对时互相钉住公钥。协调节点**不当 CA**，被攻破也不能远程控制任何游戏服（§6）。 |
| **最大代价** | ①又多一个要自己运维的公网服务；②**这是一个远程控制入口**——RCON、上传 ArkApi 插件（DLL）约等于远程执行代码，安全模型是本功能的主体工作量，不是附属品；③两台管理器版本不一致时，A 的前端调 B 的 API 可能对不上（§9）。 |
| **不做的事（首期）** | NAT 打洞（技术方案已定：UDP 打洞 + QUIC，**不是 WireGuard**，§5.6；其中协调节点的 STUN 端点首期先做，管理器侧的打洞不做）、多跳控制（A→B→C）、协调节点 Web 界面。 |
| **工作量粗估** | P1～P4（能在页面上远程控制）约 **3～4 周**；P5/P6 视需要。 |

---

## 2. 现状：今天怎么「远程管理」另一台机器

| 方式 | 问题 |
|---|---|
| 用 frp 把另一台的 19193 映射到公网，浏览器直接打开 | 每台机器要单独配 frp 规则；Web 面板暴露在公网，安全全靠 `auth`；多台机器之间是**多个浏览器标签页**，没有统一视图 |
| 内网直接访问 `https://<内网IP>:19193` | 只在同一内网可用；证书是各机器自签 CA，要逐台信任 |
| 远程桌面 | 不是「管理器控制管理器」 |

**新功能的价值**不是「多一种访问方式」，而是让「多台机器」成为本程序里的**一等概念**：
一个页面里切换机器、以后能做跨机汇总视图和跨机批量操作——这些在「每台各开一个网页」的形态下做不到。

---

## 3. 为什么不复用现有组件

### 3.1 simple-file-sync —— 不复用任何功能（已定，2026-10-04）

simple-file-sync 是一个**独立的通用同步库**，与本功能没有任何代码或运行时上的关系：

- `internal/mesh`、`internal/meshcoord`、`cmd/asa-coordinator` 及其依赖的 `pkg/*` **一律不 import**
  `github.com/Niexiawei/simple-file-sync/...`（包括 `pkg/joinblob`、`pkg/client`），P1 加一个依赖守卫测试锁住这一点。
- 协调节点是**全新的独立服务**，不是同步库协调端的扩展，也不与它共享端口、数据库、证书或进程。
- 以后即使本仓库移除 filesync 集成，mesh 也不受任何影响。

（顺带一提，同步库自己也把「P2P 发现、NAT 穿透、节点之间的中转」列为永久不做，它的协调端还是签发节点证书的 CA——
两方面都不适合远程控制，§6.1。）

**只参考两个交互形态，实现自己写**：

| 形态 | 参考的是什么 | 本功能里的落点 |
|---|---|---|
| 一行 join blob 接入 | 「地址 + 对端证书指纹 + 接入凭据」打包成带版本前缀的一行字符串，粘贴即接入；生成它的命令不在启动日志里打印凭据 | `asa-mesh-join:v1:` 格式，编解码在 `pkg/meshjoin`（§8.2） |
| 配对 | 「对端生成一次性凭据，这边粘贴后双方建立信任」的用户体验 | `asa-mesh-invite:v1:` 邀请码与申请-批准（§6.2） |

### 3.2 frp 的 stcp / xtcp —— 不采用

本程序已经进程内嵌了 frp 客户端（`frpmanage`），frp 也有「经 frps 中转的 stcp」与「打洞的 xtcp」：

- frp 解决的是「把一个端口搬到另一处」，没有**节点身份**与**在线登记**：谁在线、谁能连谁、配对关系，全都要自己在外面再做一层。
- 每对机器要写一条访问者/提供者规则，N 台机器是 N² 条；xtcp 依赖 UDP 打洞，对称 NAT 下失败，失败后的回退要自己做。
- frps 的 token 是整个服务端共享的，不是每节点身份；frp 也不承诺 Go 包 API 稳定（`docs/FRP_FORM_CONFIG_PLAN.md`）。

### 3.3 Tailscale `tsnet` + 自建 headscale —— 作为备选，不首选

这是**最强的备选**：`tsnet` 可以进程内嵌一个 WireGuard 节点（用户态网络栈，Windows 服务模式也能跑），
自带直连、打洞、DERP 中转，gRPC 直接跑在它上面即可，连通性这一层几乎不用写代码。不首选的原因：

| 维度 | tsnet + headscale | 自建（本方案） |
|---|---|---|
| 连通性 | 直连 + 打洞 + DERP 中转，**更强** | 直连 + 中转，首期不打洞 |
| 依赖体量 | `tailscale.com` 模块很大，二进制预计多十几 MB，版本更迭快 | 净新增≈0 |
| 公网要运维的东西 | headscale + DERP（两个组件，各有配置） | 一个二进制 |
| 信任根 | headscale 是控制面，**被攻破可改 ACL / 加节点** | 协调节点被攻破也冒充不了管理器（§6.1） |
| 与本程序的贴合度 | 节点身份、配对、授权仍要自己在上面做一层 | 一体设计 |

本功能的流量是**控制面**（API 请求、日志、2 秒一次的状态推送），量小，不需要打洞带来的带宽收益。
**如果以后要做「管理器之间大文件互传」或中转成本成了问题，再评估切到 tsnet**——只要第 5 章的「连接层」接口抽象得当，
上层（配对、授权、HTTP 隧道、前端）不用动。

---

## 4. 总体架构

```
                      ┌──────────────────────────────────┐
                      │   协调节点 Coordinator（公网）    │
                      │  ① 在线登记  ② 交换候选地址       │
                      │  ③ 字节中转（看不到内容）          │
                      └───────▲───────────────▲──────────┘
             gRPC/TLS 长连接   │               │  gRPC/TLS 长连接
              （主动外联）      │               │   （主动外联）
          ┌───────────────────┴──┐         ┌──┴───────────────────┐
          │ 管理器 A（控制方）    │         │ 管理器 B（被控方）    │
          │  内网 / NAT 后        │         │  内网 / NAT 后        │
          │  浏览器 ──► A 的 Web  │◄═══════►│  B 的 Gin 路由        │
          └──────────────────────┘ 端到端   └──────────────────────┘
                                   mTLS（直连或经中转）
                                   之上跑 gRPC Peer 服务
```

三种路径，按优先级尝试：

| 路径 | 什么时候成立 | 协调节点的角色 |
|---|---|---|
| **直连-内网** | A、B 在同一内网（协调节点看到的出口 IP 相同，或用户手填了内网地址） | 只牵线（交换地址），不过流量 |
| **直连-公网** | B 的 Peer 端口能被公网访问（公网 IP、端口映射、DDNS，或用户手填的地址） | 只牵线 |
| **中转** | 以上都不通 | 双方各开一条到协调节点的流，协调节点把两条流拼起来 |

另外支持**无协调节点模式**：纯内网或都在公网时，手工填对方地址 + 节点 ID 也能直接配对使用，协调节点不是必需品。

---

## 5. 连接层设计

### 5.1 身份

- 每台管理器首次启用时生成一把 **Ed25519** 密钥，存 `{BaseDir}/mesh/node.key`（0600）。
- **节点 ID = SHA-256(公钥 SPKI DER) 的 base32**，按 7 位分组显示（形如 `MFZWI3D-BONSGYC-...`）。
  绑定的是**公钥**而不是证书：自签证书到期重签不改变 ID。
- 自签证书只是 TLS 握手的载体，**校验一律按 SPKI 指纹钉住**，不走任何 CA 链。
- 「重置本机身份」= 删钥重生成，对端需要重新配对；用于换机、私钥泄露。

### 5.2 管理器 ↔ 协调节点

```proto
service Coordinator {
  // 一条长连接：登记在线、上报候选地址、接收信令（有人要连你 / 中转会话已建好）
  rpc Session(stream NodeMessage) returns (stream CoordMessage);
  // 查询某个节点：是否在线、它的候选地址、协调节点看到的双方出口 IP
  rpc Resolve(ResolveRequest) returns (ResolveResponse);
  // 申请一个中转会话：协调节点通过 Session 通知对端，返回本端的会话凭据
  rpc OpenRelay(OpenRelayRequest) returns (OpenRelayResponse);
  // 中转字节流：首帧 Join{session_id, token}，之后全是不透明字节
  rpc Relay(stream RelayFrame) returns (stream RelayFrame);
}
```

- 管理器**主动外联**协调节点（内网也能出去），TLS 1.3，客户端证书 = 本机自签证书 ⇒ 协调节点按 SPKI 认出节点 ID。
- **谁能登记**：首次登记要出示**网络密钥**（随 join blob 分发，§8.2），协调节点记下该节点 ID 属于这个网络；
  之后只凭证书。协调节点管理员可以踢出/拉黑节点。
- **协调节点自身的证书**：首启自签，指纹写进 join blob 由管理器钉住；也支持配置正式证书（前面挂了域名时）。
- 「网络」是什么、首期做到哪一步，见 §8.4（D6）。
- gRPC keepalive 要显式配置（家用路由器 NAT 表的空闲超时常见 60～300 秒）。

### 5.3 路径选择（A 要连 B）

1. `Resolve(B)` ⇒ B 是否在线、B 上报的候选地址（各网卡的内网 IP:Peer 端口、用户配置的公网地址）、
   协调节点看到的 A 与 B 的出口 IP。
2. **并发拨号**所有候选（Happy Eyeballs：错开 250ms 依次发起，整体 2 秒上限），**第一个完成端到端 mTLS 且 SPKI 等于 B 的** 胜出。
   出口 IP 相同时优先内网候选。
   - 钉公钥让「试错」是安全的：拨到别人家恰好也叫 `192.168.1.10` 的机器，握手阶段就失败，不会把请求发错对象。
3. 全部失败 ⇒ `OpenRelay(B)` 走中转。
4. **路径升级**：中转期间后台按 1 分钟起、翻倍到 10 分钟的间隔重试直连；成功后**新请求走直连**，
   中转连接上的在途流（例如一条日志 SSE）自然结束后再关闭。

**反向直连**（A 在公网、B 在内网时，让 B 主动拨 A）能省掉一部分中转流量，但需要「TCP 发起方当 gRPC 服务端」的角色翻转。
放到 P6，首期不做。**打洞**（双方都在 NAT 后）同样放到 P6，技术方案见 §5.6。

**为后续路径预留的接缝（P1 就要做对）**：选路器内部把每种路径实现成同一个接口，路径之间只比优先级，
上层（端到端 TLS、gRPC、隧道）只拿到一个 `net.Conn`，不知道它从哪来：

```go
// internal/mesh 内部，不导出
type pathProvider interface {
    Kind() PathKind                                   // lan / public / punched / relay，决定优先级与页面显示
    Dial(ctx context.Context, peer PeerInfo) (net.Conn, error)
}
```

以后加打洞、反向直连，都是新增一个 `pathProvider`，不改上层。

### 5.4 中转（协调节点视角）

```
A ──OpenRelay(B)──► Coord ──Session: IncomingRelay{S, from=A, tokenB}──► B
A ──Relay{Join S, tokenA}──► Coord ◄──Relay{Join S, tokenB}── B
                         Coord 把两条流的数据帧对拷
A（TLS 客户端） ════ 端到端 mTLS ════ B（TLS 服务端），之上跑 gRPC Peer 服务
```

- 协调节点只搬**不透明字节**，看不到也改不了内容；它知道的只有元数据（谁连谁、多少字节、多久）。
- 会话凭据一次性、30 秒内未配对作废；双方必须属于同一网络。
- **限额**：每节点并发中转会话数、每会话/每节点带宽、空闲超时（SSE 有心跳，不会被误杀）。
  中转是协调节点唯一的成本项，必须一开始就有闸门。
- gRPC 流本身有流控，两侧背压可以自然传导，不需要协调节点额外缓冲。

### 5.5 管理器之间：gRPC 跑在什么上面

两种路径最后都归结为一个 `net.Conn`（直连是 TCP，中转是「gRPC 流包装成的 `net.Conn`」，下称 `streamconn`），然后：

- **客户端**：`grpc.NewClient("passthrough:///<节点ID>", grpc.WithContextDialer(按 5.3 选路), grpc.WithTransportCredentials(钉 SPKI 的 TLS))`。
  gRPC 断线重连时会再次调用 dialer，于是「重新选路」自动发生。
- **服务端**：一个 `grpc.Server`，同时 `Serve` 两个 Listener——Peer 端口的 TCP Listener，和一个由中转会话喂连接的内存 Listener。
- 中转路径上是 **TLS 套 TLS**（外层到协调节点、内层端到端），控制面流量下开销可以忽略。

```proto
service Peer {
  rpc Hello(HelloRequest) returns (HelloResponse);       // 版本、能力列表、对方授予我的角色
  rpc Pair(PairRequest) returns (PairResponse);          // 未配对的身份只能调 Hello 与 Pair
  rpc HTTP(stream HTTPFrame) returns (stream HTTPFrame); // 一个 HTTP 请求 = 一条流（§7）
}
```

- **未配对的身份**能完成 TLS 握手（否则没法配对），但拦截器只放行 `Hello` 与 `Pair`；其他方法一律 `PermissionDenied`。
  对未配对的身份，`Hello` 只回版本、能力列表与「授予你的角色 = 无」，不回备注名等任何本机信息
  （配对页面需要在配对前显示对方版本，这点信息量可以接受）。
- `streamconn`、节点 ID 与钉公钥的 TLS 配置都**不认识领域概念**，符合 `pkg/` 准入标准（§10.1）。

### 5.6 NAT 打洞技术方案（D4：本期不做，方案先定）

> **2026-10-05 调整**：本节的公网部分——协调节点的 **STUN 端点**（§5.6.3 开头那段）——**本期先实现**，并入 P1（§12「P1-8」）。
> 管理器侧的一切（长期 UDP socket、`quic.Transport`、候选收集、信令、探测、`punched` 路径）仍然 P6。
> 先做的理由：它无状态、与其余部分零耦合、协调节点又是「部署一次很少升级」的东西——现在把端口与协议定下来、
> 部署文档里就放行好 UDP 端口，P6 只升级管理器即可，不用再让每个协调节点运营者改防火墙、换二进制。

#### 5.6.1 先澄清：打洞不是 WireGuard

- **打洞**是一种 NAT 穿透**技术**：两台都在 NAT 后的机器，借助第三方交换「各自在公网上看起来的地址」，
  然后同时向对方发 UDP 包，在各自的 NAT 上开出映射，让对方的包能进来。它与打通之后跑什么协议无关。
- **WireGuard** 是一个加密的三层（IP 层）隧道**协议**，它自己**不会打洞**。Tailscale 能穿透 NAT，靠的是它自己的
  disco 协议 + STUN + DERP 中转，WireGuard 只是打通之后的加密载体。
- 所以要定的是两件事：①怎么打洞；②打通之后，那条 UDP 路径上跑什么。

#### 5.6.2 打通之后跑什么：QUIC，不用 WireGuard

| 载体 | 评估 | 结论 |
|---|---|---|
| **QUIC**（`quic-go`） | UDP 上的可靠多路流 + 拥塞控制 + 内置 TLS 1.3，一层就是我们要的全部。关键能力：`quic.Transport` 包住**一个** UDP socket，同一个 socket 上既能 `Dial` 又能 `Listen`，还能用 `WriteTo` / `ReadNonQUICPacket` 收发非 QUIC 包（打洞探测、STUN）——**打洞用的 socket 与之后跑 QUIC 的 socket 必须是同一个**，否则 NAT 映射对不上，这正是它满足的。已在依赖图里（经 frp，v0.62.0），以后改为直接依赖 | ✅ **采用** |
| WireGuard（`wireguard-go` + gVisor netstack） | 打通后得到的是一条 IP 隧道：不想要管理员权限与 TUN 设备，就得带上 gVisor 用户态网络栈，在上面再跑 TCP、再跑 TLS + gRPC——三层叠出 QUIC 一层就有的东西，体积与复杂度都大。WireGuard 密钥是 Curve25519，还要和本机 Ed25519 身份另做映射。它的价值在「整机 IP 互通」（远程桌面、任意端口），本功能不需要 | ❌ 如果以后真要 IP 级互通，直接上 tsnet（§3.3），不自己拼 WireGuard |
| KCP（`kcp-go`，frp 在用）+ smux | 可靠 UDP，但没有内置加密，拥塞控制激进，对家宽和同机的游戏流量不友好 | ❌ |

#### 5.6.3 流程

**新增的公网部分**：协调节点开一个 **STUN 端点**（标准 STUN Binding，RFC 5389 格式，**两个 UDP 端口**），
无状态，只回答「你的包是从哪个 IP:端口来的」。用标准格式是为了能直接拿现成的 STUN 工具排障。
**这一段本期就实现**，具体规格（端口、属性、限流、STUN 地址怎么告诉管理器）见 §12「P1-8」。

**管理器侧**：一个长期存在的 UDP socket（`udp_port`，默认与 Peer 端口同号 19194/udp），包成 `quic.Transport`，
所有打洞探测、STUN、QUIC 连接都走它。

1. **地址发现**：向协调节点的两个 STUN 端口各问一次，得到「服务器反射地址」。两次的公网端口相同 ⇒ NAT 的映射与目的端口无关（可打洞）；
   不同 ⇒ 对称型映射（俗称 NAT4），本机标记为「难打洞」。只有一个 IP 时分辨不出「只随目的 IP 变」的 NAT，会误判成易打洞——
   代价只是多试一次失败，可以接受。结果随 `Session` 上报，每 5 分钟及网卡变化时刷新。
2. **候选地址**：本机各网卡的 IPv4/IPv6 + UDP 端口（host）、服务器反射地址（srflx）、
   可选的 UPnP / NAT-PMP / PCP 端口映射结果（mapped，路由器支持时能把「难打洞」变成「可直连」）。
3. **信令**（都经已有的 `Session` 长连接）：A 的 TCP 直连失败后，**开中转的同时**发 `PunchOffer{会话ID, A 的候选}`；
   协调节点转给 B，B 回 `PunchAnswer{B 的候选}`；协调节点给双方同一个开始时刻（服务器时间 + 300ms）和一把会话密钥。
4. **探测**：双方在开始时刻起，每 50ms 向对方的每个候选发一个小探测包，最多 5 秒。
   探测包首字节不满足 QUIC 的固定位，`quic-go` 会把它交给 `ReadNonQUICPacket`；内容 = 会话 ID + 序号 + HMAC(会话密钥)。
   会话密钥只防「别人伪造探测包把选路引到错误地址」；**身份认证仍然靠之后 QUIC 握手钉 SPKI**，所以协调节点知道这把密钥无妨。
5. **建连**：收到对方的有效探测 ⇒ 这个对端地址可达。节点 ID 字典序小的一方 `Transport.Dial`，另一方 `Listen` 接受（避免双方同时发起握手）。
   QUIC 的 TLS 用钉 SPKI 的配置，ALPN `asa-mesh/1`。
6. **接入上层**：在 QUIC 连接上开一条双向流，包成 `net.Conn`，作为 `punched` 类型的 `pathProvider`（§5.3）交出去，
   之上照旧是端到端 TLS + gRPC——与中转路径一样「TLS 套 TLS」，上层零差别。（以后可以优化为信任 QUIC 层的 TLS、内层不再加密。）
7. **保活与回退**：QUIC `KeepAlivePeriod` 15 秒（NAT 的 UDP 映射空闲超时常见 30 秒起）。断了就回到中转，按 §5.3 的路径升级节奏重打。

**用户感知**：打洞与中转**并行**，用户立刻拿到中转连接，打通后无感升级；从不「等打洞」。
路径优先级：直连-内网 > 直连-公网 > 打洞 > 中转。

#### 5.6.4 预期效果与不做的事

| 场景（双方 NAT 类型） | 结果 |
|---|---|
| 任一方有**公网 IPv6**（防火墙放行出站 UDP 的状态跟踪） | 基本能通——双方同时发包即可打开有状态防火墙。**国内家宽 IPv6 普及，这是打洞最大的收益来源**，所以 host 候选必须包含 IPv6 |
| 双方都是 NAT1～NAT3 | 能通 |
| 一方 NAT4，另一方 NAT1/NAT2 | 通常能通 |
| 一方 NAT4，另一方 NAT3/NAT4 | 不做端口预测就打不通 ⇒ 中转 |
| 运营商级 NAT（CGNAT）、移动网络 | 多为 NAT4 ⇒ 多数中转 |
| 网络封了 UDP | 中转 |

**不做**：端口预测 / 「生日攻击」式大量端口探测（Tailscale 的做法）——实现复杂、容易被运营商当成扫描、提升有限。

#### 5.6.5 P1 就要预留的东西（P6 不改协议、不让新旧版本混跑出错）

- proto：`NodeMessage` / `CoordMessage` 的 `oneof` 里预留 `PunchOffer` / `PunchAnswer` / `PunchStart` 的字段号；
  `Candidate` 消息从一开始就带 `transport`（tcp / udp）与 `kind`（host / srflx / mapped / configured）。
- 能力协商：登记与 `Peer.Hello` 都带能力列表，只有双方都声明 `punch.v1` 时协调节点才转发 `PunchOffer`。
- ~~协调节点配置预留 `stun.listen`（默认为空 = 关闭）~~ → **2026-10-05 改为 P1 直接实现**（P1-8）：`stun.listen` 是真配置，
  `config init` 生成的模板默认开 `:3478`/`:3479`；`Registered` 消息把协调节点的 STUN 地址下发给管理器（`stun_addrs`），
  P6 的管理器直接用，不需要改 join blob。VPS 防火墙要多放行两个 UDP 端口，部署文档里写明。
- `pathProvider` 接缝（§5.3）。

---

## 6. 安全模型（本功能的主体工作量）

### 6.1 威胁与对策

| 威胁 | 后果（若不防） | 对策 |
|---|---|---|
| **协调节点被攻破** | 若协调节点是 CA：签证书冒充任意管理器 ⇒ 控制所有游戏服 | 协调节点**不签发任何身份**；管理器只认配对时钉住的公钥。被攻破的协调节点能做的只有：拒绝服务、看元数据、喂假候选地址（握手会失败） |
| A 的私钥被偷 | 攻击者以 A 的授权控制 B | B 的管理员撤销 A；A 重置身份。审计日志记录每次远程操作的来源节点 |
| 一个被授权的节点作恶 | 越权、横向移动 | 角色上限 + 远程禁区（§6.3）+ **禁止多跳**（经隧道进来的请求不能再访问 `/api/peers/*`） |
| 配对凭据被截获重放 | 陌生节点混进来 | 邀请码一次性、10 分钟有效，**在端到端 mTLS 里提交**，协调节点看不到 |
| Peer 端口暴露在局域网/公网 | 被扫描、被暴力尝试 | 握手要求客户端证书；未配对身份只能调 `Pair`，`Pair` 有失败计数限流 |
| 中转被滥用（拿协调节点当免费代理） | 带宽成本 | 只有本网络的节点能登记；中转限额（§5.4） |

### 6.2 配对与授权

- 授权是**有方向的**：「B 允许 A 以 `operator` 身份控制 B」。双向互控 = 两边各授权一次。
- 两种配对方式（首期都做，UI 二选一）：
  1. **邀请码（推荐）**：B 的管理员生成一行邀请码 `asa-mesh-invite:v1:...` = `B 的节点 ID + 一次性密钥 + 授予的角色 + 可选的 B 直连地址`，复制给 A
     （带直连地址时，无协调节点也能配对）；
     A 粘贴后连 B，在端到端 mTLS 里出示密钥，B 校验通过即钉住 A 的公钥。全程不需要 B 的管理员再点任何东西。
  2. **申请-批准**：A 输入 B 的节点 ID 发起申请，B 的页面出现「待批准」，管理员选角色后批准。
- 授权表存 `{BaseDir}/mesh/peers.json`（0600）：`节点 ID、备注名、授予的角色、配对时间、最后在线、来源地址`。
  **不放进 `auth.db`**：`auth.enabled=false` 时 `auth.db` 根本不打开，而远程授权在任何情况下都必须生效。

### 6.3 有效权限

```
远程操作的有效权限 = min( A 上发起者的角色 , B 授予 A 的角色 ) − 远程禁区
```

- **A 侧**：默认只有 A 的管理员能使用远程控制（§11-D5）；A 关着鉴权时任何能打开 A 页面的人都能控制 B——
  页面上要明确提示，这是「信任 A 就是信任 A 的安全状况」的直接后果，B 无法替 A 把关。
- **B 侧角色**：沿用现有的 `admin` / `operator` 两档，路由的 `RequireAdmin()` 照常生效。
- **远程禁区**（无论授予什么角色，经隧道一律 403）：`/api/users/*`、`/api/auth/*`（除 `state`）、`/api/mesh/*`、`/api/peers/*`。
  即：远程不能改 B 的账号、不能改 B 的配对关系、不能借 B 再跳到 C。
- ArkApi 主程序/插件上传（`/api/arkapi/*` 写操作）本质是往 B 上放可执行 DLL——现在它已经要求 `admin`，远程同样需要 B **授予 `admin`** 才能用。

### 6.4 与现有鉴权中间件的接缝

`authapi.Middleware()` 现在的两个短路都会让隧道请求「裸奔」，必须改：

1. `!cfg.Auth.Enabled` 时直接放行 ⇒ 隧道请求要**先于**这个判断被识别。
2. `RequireAdmin()` 在鉴权关闭时也直接放行 ⇒ 同理。

做法：隧道服务端在构造 `http.Request` 时往 **context**（不是请求头，请求头可以伪造）里放 `mesh.PeerIdentity{NodeID, Label, GrantedRole, RemoteUser}`；
中间件与 `RequireAdmin` 的第一步检查它：有就按授权角色合成一个 `*auth.User{Username: "peer:<备注名>/<A 上的用户名>", Role: 授予角色}` 设进上下文、
走远程禁区检查，**与 `auth.enabled` 无关**；`lan_bypass` 对隧道请求永不生效。

审计：`ActorName()` 自然会返回 `peer:<备注名>/<用户名>`。B 开着鉴权时进 `auth` 的审计表；关着时写 `[mesh]` 日志。
⚠️ 「A 上的用户名」是 A 自述的，B 只能验证到「是 A 这台机器说的」，审计页面要这样标注。

---

## 7. 业务载荷：HTTP over gRPC 隧道

### 7.1 为什么先做隧道而不是类型化接口

| | HTTP 隧道（推荐首期） | 类型化 gRPC 接口 |
|---|---|---|
| 覆盖面 | 第一天就是**全部**现有 API，含 SSE 日志、WS 事件、交互式 RCON、文件上传下载 | 每个功能写一遍 proto + 服务端实现，与 REST 重复 |
| 前端 | 换一个 API 前缀即可复用全部页面 | 远程视图要另写一套 |
| 契约稳定性 | 跟着 REST 走，版本不一致时可能对不上（§9） | 显式、可版本化 |
| 适合 | 「打开另一台机器的面板操作」 | 「跨机汇总」「跨机批量」这类编排 |

结论：**连接层与 `Peer` 服务一开始就是 gRPC**，业务先借隧道复用现成路由；等到要做跨机编排（P5），再为**少数编排所需的操作**加类型化方法，而不是给所有 API 补 proto。

### 7.2 实现

- **B 侧（服务端）**：`Peer.HTTP` 的每条流 = 一个请求。首帧 `RequestHead{method, path, query, headers}`，之后 `Body` 帧；
  B 构造 `http.Request`（context 带 `PeerIdentity`），直接调 **Gin engine 的 `ServeHTTP`**——不经过 B 的 TCP 端口、TLS、CORS。
  自定义 `ResponseWriter`：`WriteHeader`→`ResponseHead` 帧，`Write`→`Body` 帧，实现 `http.Flusher`（SSE 要）与
  `http.Hijacker`（gorilla/websocket 升级要，返回一个由这条流包装成的 `net.Conn`）。
- **A 侧（客户端）**：路由 `/api/peers/:id/fwd/*path` 用 `httputil.ReverseProxy`，`Transport` 换成「把请求塞进 `Peer.HTTP` 流」的 `RoundTripper`。
  ReverseProxy 自带逐跳头剥离、`text/event-stream` 立即刷新、101 升级后的双向拷贝，不用自己写。
- **前缀必须在 `/api` 下**：A 的鉴权中间件只拦 `/api` 前缀（`!strings.HasPrefix(path, "/api")` 一律放行），
  写成 `/peers/...` 会让隧道入口**绕过 A 的鉴权**。
- **转发前要清洗的头**：`Cookie`、`Authorization`（A 的会话凭证绝不能带给 B）、`X-Forwarded-*`；
  `Origin` 改写为 B 的 Host——`realtime.WSUpgrader.CheckOrigin` 按 `strings.Contains(origin, host)` 判同源，原样转发会被 B 拒掉 WS。
- 大请求体（ArkApi zip、备份下载）走流式，不整块缓冲；页面上提示「当前经中转」，中转限额对它们生效。

### 7.3 前端

- 新页面「远程管理器」：本机节点 ID / 邀请码、配对列表（在线状态、路径 直连-内网/直连-公网/中转、RTT、对方版本）、待批准申请、协调节点接入（粘贴 join blob；表单形态照本仓库的 `FRPManager.vue`）。
- 顶栏「当前机器」选择器：切到 B 时，所有 API 前缀变成 `/api/peers/<B>/fwd`。要改的位置是集中的：
  `utils/http.js`（axios `baseURL`）、`utils/utils.js` 的 `buildEventSourceUrl`、`utils/wsManager.js`、`store/rconStore.js`、
  以及两个 worker（`resourceWorker.js`、`wsWorker.js` 的 URL 由主线程传入）。
- 远程上下文里隐藏「用户管理」「远程管理器」页（远程禁区，§6.3）。
- 别忘了 `App.vue` 的三处联动（菜单项、`watch(route.path)` 高亮、`handleMenuClick` 分支）。

---

## 8. 协调节点

### 8.1 形态

- **独立二进制，与本仓库同一个 Go 模块**：`cmd/asa-coordinator/`（§11-D3，已定）。
  共享 proto 与 `streamconn`，版本天然一致；`CGO_ENABLED=0`、不 import Fyne/Badger/frp，交叉编译出一个小的 Linux 二进制。
  协调节点代码就在本仓库，**集成测试可以在进程内拉起协调节点**。
- 配置：独立的 `coordinator.yaml`（监听地址、数据目录、证书、限额、STUN；查找顺序与生成方式见 §12「P1-5」），存储用 `modernc.org/sqlite`（已在依赖里）：网络、节点、拉黑表。
- 运维：`asa-coordinator service install`（`kardianos/service`，与 asa-server 同一套）、`asa-coordinator join-blob` 打印接入串、
  `node list|ban`。Web 界面首期不做。
- 端口：建议直接 443（企业/校园网对非常见端口的出站限制最少）。
  ⚠️ 要和网站共用 443、放在 Nginx 后面时，**只能用 `stream` 模块按 SNI 做 TLS 透传**（`ssl_preread`），**不能用 `grpc_pass`**：
  `grpc_pass` 会在 Nginx 终止 TLS，协调节点就拿不到管理器的客户端证书（认不出节点 ID），管理器钉的证书指纹也会变成 Nginx 的。
- **STUN 端口**（P1-8）：另放行 **UDP 3478、3479**（STUN 标准端口及其后一个）。STUN 是 UDP，Nginx 前置与否都不影响它，
  直接由协调节点监听；云厂商的安全组与本机防火墙都要放行，**这是部署文档里最容易漏的一步**。

### 8.2 接入（join blob）

`asa-mesh-join:v1:<base64>`，内容：协调节点地址、协调节点证书 SPKI 指纹、网络 ID、网络密钥。
编解码放在 `pkg/meshjoin`（只用标准库，零领域依赖），管理器与协调节点共用。
协调节点首启**不在日志里打印**它（含网络密钥，日志文件会被备份、被贴出来求助），要显式运行 `join-blob` 子命令。
邀请码 `asa-mesh-invite:v1:` 也放在这个包里，两者格式同构：版本前缀 + base64url(JSON) + 末尾校验和（粘贴截断时给出明确报错，而不是解出半截）。

### 8.3 成本估计

控制面流量很小：一个打开的远程面板 ≈ 状态 SSE（2 秒一次，KB 级）+ 日志流 + 零星 API；
大头只有经中转的备份下载与插件上传，受 §5.4 限额约束。一台 1 核 1G 的 VPS 足够服务几十台管理器。

### 8.4 网络（D6 详解）

#### 8.4.1 「网络」是什么

**网络 = 一组能互相看见、互相发起配对、互相走中转的管理器。** 打个比方：协调节点是一栋楼，网络是楼里的房间；
同一个房间里的人能看到彼此、能敲门（发起配对），不同房间之间互相看不见。一个协调节点可以有一个或多个网络。

这里有两种不同的角色，D6 的意义就在于把它们分开：

| 角色 | 是谁 | 管什么 |
|---|---|---|
| **协调节点运营者** | 租了那台 VPS 的人 | 协调节点本身：部署、升级、带宽、证书 |
| **网络所有者** | 用这个协调节点的一群服主 | 谁能加入这个网络（持有网络密钥、join blob） |

只有自己几台机器时，这两个角色是同一个人，一个网络就够了。

#### 8.4.2 ⚠️ 网络**不是**远程控制的安全边界

**能不能控制一台管理器，只由配对决定（§6.2），与是否在同一网络无关。** 同一网络里的陌生节点，没有 B 的邀请码或 B 管理员的批准，
连 `Peer.Hello` 都调不了（§5.5 的拦截器只放行 `Pair`）。

网络隔离管的是另外三件事：

1. **可见性**：同网络的节点能 `Resolve` 到你——知道你的节点 ID、是否在线、你的出口 IP 和内网候选地址（这些都是中转与直连必需的信息）。
   不同网络之间这些一概不可见。
2. **骚扰面**：同网络的节点能向你发「申请-批准」的配对申请。陌生人多了，可以刷满你的「待批准」列表。
3. **资源与成本归属**：中转带宽、并发会话数按网络计配额。一个网络里有人疯狂走中转下载备份，不应该挤占另一个网络。

#### 8.4.3 什么时候需要多个网络

| 场景 | 一个网络够吗 |
|---|---|
| 自己的几台机器 | ✅ 够 |
| 把协调节点**借给**朋友或其他服主社区用 | ❌ 你的机器和他们的会互相可见、能互相发配对申请、共用中转配额 |
| 测试机与正式服隔离（测试版本的管理器不要出现在正式网络的节点列表里） | ❌ |
| 一个网络的密钥泄露，要换密钥，又不想影响其他人 | ❌ 单网络时换密钥会影响所有新加入者 |

#### 8.4.4 三个选项

| | A. 单网络 | **B. 数据模型预留（推荐）** | C. 完整多网络 |
|---|---|---|---|
| 协议 / join blob 带 `network_id` | 不带 | **带** | 带 |
| 数据库 | 节点表无网络列 | 有 `networks` 表与网络列；首启自动建一个 `default` 网络 | 同 B |
| 协调节点上的所有查询按网络过滤 | 无 | **有**（从第一天起就按网络过滤） | 有 |
| 运营者管理网络的 CLI | 无 | 无（只有 `default`） | `network create` / `list` / `rotate-secret` / `delete`、每网络配额 |
| 网络所有者自助管理（不找运营者就能踢人、换密钥） | 无 | 无 | 需要**另一套账号体系**（网络管理员登录协调节点）——C 的主要成本在这里 |
| 首期额外工作量 | 0 | 约 1 天 | 约 1～2 周 |
| 以后升级到 C 的代价 | **协议破坏性变更**：join blob 升 v2、数据库迁移、已接入节点要重新接入 | 只加 CLI 与配额配置，**不改协议、不重新接入** | — |

**已定 B**（2026-10-04）：首期花约 1 天，把以后「借给别人用」这条路的协议代价降到零；C 的那套账号体系等真有这个需求再做。

#### 8.4.5 B 的具体做法

- **join blob** 带 `network_id` + `network_secret`（§8.2）。
- **表结构**：
  - `networks(id, name, secret_hash, created_at, quota_json)`——密钥只存哈希。
  - `nodes(network_id, node_id, label, first_seen, last_seen, banned)`，主键 `(network_id, node_id)`。
    节点 ID 由公钥决定、全局唯一，主键仍然带上网络，是为了以后「一台管理器加入多个网络」时不用改表。
- **登记**：`Session` 首帧带 `network_id`，首次接入再带 `network_secret`；成功后协调节点把这个节点记进该网络，此后只凭证书。
- **一条硬规则**：协调节点的 `Resolve` / `OpenRelay` / 配对申请转发，**一律用「这条 `Session` 登记的网络」过滤**，
  永远不读请求参数里的网络 ID——否则伪造一个参数就能跨网络查人。P1 为它写专门的安全用例。
- **换网络密钥**：只影响以后的新接入，已接入的节点凭证书照常工作；密钥泄露后混进来的陌生节点用 `node ban` 踢掉。
- **管理器侧**：一台管理器首期只接入一个协调节点、一个网络，页面上不出现「网络」这个词（只显示协调节点地址）。

---

## 9. 版本兼容

- **协调节点与业务解耦**：它只认 `Coordinator` 服务，管理器之间的 API 怎么变都**不需要升级协调节点**。
  `Coordinator` proto 自身按 `asamesh.v1` 版本化，只做向后兼容的增量。
- **管理器之间**：`Peer.Hello` 交换程序版本与能力列表。A 的前端调 B 的 REST——版本不一致时 B 可能缺接口或字段不同：
  - 首期：前端在远程上下文顶部显示「对方版本 x.y，与本机不同」的提示；能力列表里没有的页面置灰。
  - 备选（不首选）：A 把 **B 自己内嵌的前端**也代理过来，版本错位就不存在了。代价是 SPA 现在按根路径 `/` 构建、
    依赖 Cookie 会话，要改 Vite `base` 与鉴权方式，改动面大，等版本错位真成问题再说。

---

## 10. 在本仓库里的落点

### 10.1 包划分

| 包 | 内容 | 依赖 |
|---|---|---|
| `api/mesh/v1/*.proto` → `internal/mesh/meshpb` | `Coordinator`、`Peer` 两个服务的 proto 与生成代码（buf 生成，生成代码提交进仓库） | grpc、protobuf |
| `pkg/streamconn` | gRPC 双向流 ↔ `net.Conn` 适配（读缓冲、写分帧、deadline），**不认识领域概念** | grpc |
| `pkg/meshid` | 密钥生成、节点 ID 编解码、钉 SPKI 的 `tls.Config` | 标准库 |
| `pkg/meshjoin` | join blob 与邀请码的编解码（§8.2），**自己实现，不引用任何外部 join blob 实现** | 标准库 |
| `pkg/stun` | STUN Binding（RFC 5389 子集）的报文编解码、无状态服务端、最小客户端（P1-8），**不认识领域概念** | 标准库 |
| `pkg/atomicfile` | 原子写整文件（P1 实施时新增，见「P1 实施记录」偏差 3） | 标准库 |
| `internal/mesh` | 管理器侧运行时：协调节点客户端（Session 长连接、退避重连）、Peer 端口、选路与路径升级、配对与授权表、隧道两端、包级单例 Manager（照本仓库 `frpmanage` 的形态） | config、pkg/*；**不** import webapi |
| `internal/meshcoord` | 协调节点服务实现（登记、Resolve、中转拼接、限额） | meshpb、pkg/streamconn、sqlite |
| `cmd/asa-coordinator` | 协调节点入口 | meshcoord |
| `internal/webapi/meshapi` | `/api/mesh/*`（本机信息、配对、协调节点配置、状态 SSE）+ `/api/peers/:id/fwd/*` | mesh、authapi |
| `internal/webapi/authapi` | §6.4 的 `PeerIdentity` 分支 | mesh（只取 context key 与类型） |

**组合根**：`internal/webapi/actions.go`——`mesh.SetHTTPHandler(s.engine)` 把 Gin engine 注入给隧道服务端（避免 mesh → webapi 成环），
生命周期挂进 `InitializationBasicComponents` / `Start` / `Stop`，与 frp 并列。Windows 服务模式下 API 服务所在的进程就是 mesh 所在的进程。

### 10.2 运行时目录

```
{BaseDir}/mesh/
├── node.key / node.crt     # 本机身份（0600）。不进 config.json：配置可能被导出，私钥不该跟着走
├── config.json             # 启用开关、协调节点接入信息、Peer 端口、手填的对端地址（0600）
└── peers.json              # 配对与授权表（0600），§6.2
```

配置形态照本仓库的 frp：UI 表单编辑、`{BaseDir}/<功能>/config.json`，**不进 `config.yaml`**（否则要同改中英两份模板）。
未配置时 `ErrNotConfigured` 短路，零副作用。

### 10.3 CLI

`asa-server mesh id|status|peers|invite|revoke`——`status` 显示协调节点连接、每个对端的当前路径；`invite` 在无界面的 Linux 服务器上生成邀请码。
新增带取值的全局 flag 时同步 `globalValueFlags`；`mesh` 子命令是否需要 config.yaml 按 `startupModeFor` 归类。

---

## 11. 决策

| # | 问题 | 选项 | 结论 |
|---|---|---|---|
| **D0** | 与 simple-file-sync 的关系 | — | ✅ **已定（2026-10-04）**：不复用任何功能、不 import 任何包；只参考 join blob 一行接入与配对的交互形态（§3.1） |
| **D1** | 业务载荷 | A. HTTP over gRPC 隧道先行，编排再加类型化接口；B. 一开始就全部类型化 gRPC 接口 | ✅ **A**（2026-10-04，§7.1） |
| **D2** | 信任根 | A. 每节点自生成密钥 + 配对钉公钥，协调节点不当 CA；B. 协调节点自举 PKI 签发节点证书 | ✅ **A**（2026-10-04，§6.1） |
| **D3** | 协调节点代码放哪 | A. 本仓库 `cmd/asa-coordinator`；B. `asa-server coordinator` 子命令（同一个大二进制）；C. 独立仓库 | ✅ **A**（2026-10-04） |
| **D4** | 打洞 | 本期做 / 不做 | ✅ **本期不做**（2026-10-04）；技术方案已定：UDP 打洞 + QUIC，不用 WireGuard（§5.6），P1 预留协议字段与接缝（§5.6.5）。**2026-10-05 调整：协调节点的 STUN 端点本期先实现**（P1-8），管理器侧打洞仍 P6 |
| **D5** | A 上谁能用远程控制 | 只有管理员 / 管理员与操作员 | ✅ **默认只有管理员**，配置可放开（2026-10-04） |
| **D6** | 协调节点多网络 | A. 单网络 / B. 数据模型预留 / C. 完整多网络 | ✅ **B**（2026-10-04，§8.4） |
| **D7** | Peer 端口默认 | 默认监听（19194，便于内网直连）/ 默认不监听（只走中转） | ✅ **默认监听**（2026-10-04）；Windows 首次监听会弹防火墙提示，页面上说明 |

---

## 12. 实施计划

> 总原则：每个阶段结束都有一个**能演示的闭环**；未配置 mesh 时对现有功能零影响。

### P0 — 决策（本文 §11）

### P1 — 身份 + 协调节点 + 中转打通

**目标闭环**：两台管理器各自只能外联协调节点，A 经中转调到 B 的 `Peer.Hello`，拿到 B 的版本与能力列表。
P1 **没有**配对、授权表、HTTP 隧道、直连与前端页面——那些分别在 P2～P4。
另外（2026-10-05 并入）：协调节点的 **STUN 端点**可用，现成 STUN 工具能问到正确的反射地址（P1-8）；管理器侧 P1 不消费它。

#### P1 细化（2026-10-04；2026-10-05 并入 STUN 端点；待确认后开工）

##### P1-1 proto 与代码生成

文件 `api/mesh/v1/coordinator.proto`、`api/mesh/v1/peer.proto`，proto 包 `asamesh.v1`，
`go_package = "asa-server/internal/mesh/meshpb;meshpb"`。仓库根放 `buf.yaml` / `buf.gen.yaml`（v2，本地插件 `protoc-gen-go` /
`protoc-gen-go-grpc`，本机已装），生成代码**提交进仓库**，`go build` 不需要 buf。`google.golang.org/protobuf` 从 indirect 变为直接依赖。

```proto
service Coordinator {
  rpc Session(stream NodeMessage) returns (stream CoordMessage);
  rpc Resolve(ResolveRequest) returns (ResolveResponse);
  rpc OpenRelay(OpenRelayRequest) returns (OpenRelayResponse);
  rpc Relay(stream RelayFrame) returns (stream RelayFrame);
}

message NodeMessage {
  oneof msg {
    Register register = 1;                // Session 首帧，且只能是首帧
    CandidatesUpdate candidates = 2;      // P2 用
    PunchOffer punch_offer = 10;          // P6（§5.6），P1 只定义不实现
    PunchAnswer punch_answer = 11;
  }
}
message Register {
  string network_id = 1;
  string network_secret = 2;              // 只在首次接入时必需；已是成员时协调节点忽略它
  string version = 3;
  repeated string capabilities = 4;       // P1 声明 "relay.v1"；打洞是 "punch.v1"
  repeated Candidate candidates = 5;
}
message CoordMessage {
  oneof msg {
    Registered registered = 1;            // 节点 ID（由协调节点按证书算出，回显给管理器核对）、看到的出口地址、服务器时间、
                                          //   STUN 地址列表 stun_addrs（P1-8；未开 STUN 时为空）
    IncomingRelay incoming_relay = 2;     // 有人要经中转连你：{session_id, from_node_id, token}
    Kicked kicked = 3;                    // 同一节点 ID 有新会话登记，旧会话被踢（附原因）
    PunchStart punch_start = 10;          // P6
  }
}
message Candidate {
  Transport transport = 1;                // TCP / UDP
  CandidateKind kind = 2;                 // HOST / SRFLX / MAPPED / CONFIGURED
  string addr = 3;                        // host:port
}
message ResolveRequest  { string node_id = 1; }   // ⚠️ 没有 network_id 字段——网络只能来自调用者自己的 Session（§8.4.5）
message ResolveResponse {
  string version = 1; repeated string capabilities = 2; repeated Candidate candidates = 3;
  string peer_observed_ip = 4; string self_observed_ip = 5;
}
message OpenRelayRequest  { string target_node_id = 1; }
message OpenRelayResponse { string session_id = 1; bytes token = 2; }
message RelayFrame { oneof msg { RelayJoin join = 1; bytes data = 2; } }
message RelayJoin  { string session_id = 1; bytes token = 2; }
```

`Peer` 服务 P1 **只有 `Hello`**；`Pair`（P3）与 `HTTP`（P3）以后作为新方法追加，加方法不是破坏性变更。

```proto
service Peer { rpc Hello(HelloRequest) returns (HelloResponse); }
message HelloRequest  { string version = 1; repeated string capabilities = 2; }
message HelloResponse {
  string version = 1; repeated string capabilities = 2;
  Role granted_role = 3;                  // ROLE_NONE / ROLE_OPERATOR / ROLE_ADMIN；P1 恒为 NONE
  string label = 4;                       // 只对已配对的身份填写
}
```

错误语义：节点不存在、不在同一网络、不在线，`Resolve` / `OpenRelay` **一律返回同一个 `NotFound`**，不让调用者区分「别的网络有这个节点」。

##### P1-2 `pkg/meshid`（只用标准库）

- `Generate() (ed25519.PrivateKey, error)`；`SelfSignedCert(key, validity) (tls.Certificate, error)`（有效期 20 年，到期重签不改 ID）。
- `ID` 类型：SHA-256(SPKI DER) 的 base32（无填充，52 字符），`String()` 按 4 位分组用 `-` 连接，`Short()` 取前 8 位供页面显示；
  `ParseID` 接受大小写、有无分隔符。
- `FromCert(*x509.Certificate) ID`；`PinnedTLSConfig(own tls.Certificate, expect ID) *tls.Config`——客户端/服务端两种，
  `InsecureSkipVerify` + `VerifyPeerCertificate` 只比 SPKI（不验链、不验主机名，这是刻意的，注释写清楚）；
  服务端另有 `AnyClientTLSConfig`（要求客户端证书但接受任何 ID，由上层按 ID 决定权限）。最低 TLS 1.3。
- 文件存取：`LoadOrCreate(dir) (tls.Certificate, ID, error)`，`node.key` 0600，原子写（临时文件 + rename）。

##### P1-3 `pkg/meshjoin`（只用标准库）

- `JoinBlob{Addr, CoordinatorSPKI ID 的字节, NetworkID, NetworkSecret}`，`Encode` → `asa-mesh-join:v1:<base64url(JSON)>.<校验>`，
  校验 = SHA-256(base64 段) 前 4 字节的 hex。
- 错误分得清：`ErrPrefix`（不是这个格式）、`ErrVersion`（版本不认识，提示升级）、`ErrChecksum`（多半是粘贴被截断）。
- `JoinBlob` 实现 `fmt.Formatter` / `slog.LogValuer`，打印时密钥一律打码——防止哪天被顺手 `%v` 进日志。
- 邀请码 `asa-mesh-invite:v1:` 同构，P3 才加。

##### P1-4 `pkg/streamconn`

- `New(stream, recv func() ([]byte, error), send func([]byte) error, closeSend func() error) net.Conn`：
  读侧缓冲剩余字节；写侧按 32 KiB 分帧；`Close` 幂等；`Read`/`Write` 的 deadline 用定时器 + 取消实现。
  只依赖 grpc 的 `ClientStream`/`ServerStream` 这类最小接口，单测用内存管道。
- 单测：1 MiB 单次写的分帧与重组、并发读写（`-race`）、对端关闭 → `io.EOF`、deadline 到期 → `os.ErrDeadlineExceeded`、
  **在它上面跑一次 TLS 握手 + gRPC 调用**（证明它对上层是合格的 `net.Conn`）。

##### P1-5 `internal/meshcoord` + `cmd/asa-coordinator`

- **存储**：`{data_dir}/coordinator.db`（`modernc.org/sqlite`），两张表：
  `networks(id TEXT PK, name, secret_hash, created_at, quota_json)`、
  `nodes(network_id, node_id, label, version, first_seen, last_seen, banned, PRIMARY KEY(network_id, node_id))`。
  首启建 `default` 网络（32 字节随机密钥，只存 SHA-256）。
- **自身证书**：`{data_dir}/coordinator.key/.crt` 首启自签；配置了 `tls.cert_file/key_file` 时用正式证书，join blob 里仍写它的 SPKI。
- **在线表**（内存）：`node_id → {network_id, session, observed_addr, candidates, caps, version}`。同一节点 ID 第二个会话登记 ⇒
  给旧会话发 `Kicked` 后断开，并记一条 WARN（同一把私钥出现在两台机器上的信号）。
- **登记**：TLS 层 `AnyClientTLSConfig` 取出节点 ID → 首帧必须是 `Register` → 已是该网络成员则直接通过；否则校验 `network_secret`
  （常数时间比较）后入表；被 ban 或密钥错 ⇒ `PermissionDenied` 并断开。`Resolve`/`OpenRelay`/`Relay` 要求调用者**此刻有在线会话**，
  网络取自那个会话（§8.4.5 的硬规则）。
- **中转**：`OpenRelay` 生成 `session_id` + 两个一次性 token（A 的随响应返回，B 的经 `IncomingRelay` 下发），30 秒内双方都 `Join` 才拼接，
  否则作废。拼接 = 两个 goroutine 互拷 `data` 帧；任一侧结束则关闭另一侧。
- **限额**（YAML 可配，P1 先做这三项）：每节点并发中转会话（默认 8）、中转空闲超时（默认 5 分钟，以 `data` 帧为准）、
  每节点中转速率（默认不限，令牌桶，配置了才生效）。
- **keepalive**：服务端 `keepalive.EnforcementPolicy{MinTime: 10s, PermitWithoutStream: true}` 与客户端 20 秒心跳配套，
  否则客户端会被以 `too_many_pings` 踢掉。
- **配置**：协调节点**自己一份** `coordinator.yaml`，与管理器的两份配置互不相干：

  | 配置 | 所在机器 | 读取方 |
  |---|---|---|
  | asa-server 的 `config.yaml` | 管理器 | asa-server（mesh **不往里加任何字段**） |
  | `{BaseDir}/mesh/config.json` | 管理器 | `internal/mesh`（由 `mesh join` / 页面写，不手改） |
  | `coordinator.yaml` | 协调节点（通常是 VPS） | `asa-coordinator` |

  不复用 `internal/appconfig` 与 viper（依赖面要小，那边的 basedir / Web 端口 / 鉴权对协调节点也没有意义），
  直接用依赖图里已有的 `go.yaml.in/yaml/v3`（升为直接依赖）。
  - **字段**：`listen`、`public_addr`（写进 join blob，`host:port`）、`data_dir`、`tls`、`limits`、`stun`（P1-8）。
    `listen` 与 `public_addr` 必填；解码开 `KnownFields(true)`，**不认识的字段报错**——拼错的 `stun.listn` 不该被静默忽略、变成「STUN 没开」。
  - **查找顺序**：`-c <路径>` > 二进制同目录的 `coordinator.yaml` > `/etc/asa-coordinator/coordinator.yaml`（只在 Linux 上查）。
    **不支持环境变量覆盖**。全部找不到 ⇒ 报错退出（退出码 78，与 asa-server 一致），提示列出查过的路径与 `config init` 用法；
    **从不自动生成**（与 asa-server 2026-10-02 起的规则一致）。
  - **`data_dir`**：默认 `data`；相对路径**按配置文件所在目录解析**，不按当前工作目录——作为服务运行时工作目录不可控，
    相对 cwd 会把数据库写到意想不到的地方。`tls.cert_file` / `key_file` 的相对路径同理。
  - **`config init [-o <路径>] [--force]`**：生成模板。默认写到二进制同目录；目标已存在时拒绝，`--force` 先把旧文件备份成
    `coordinator.yaml.bak-<时间戳>` 再覆盖；原子写（临时文件 + rename）。
  - **模板只有一份，注释用中文**：协调节点由运营者部署，不需要像 asa-server 那样维护中英两份同构模板。UTF-8 无 BOM、LF
    （目标平台是 Linux）。模板里写全每个字段与默认值，注释说明云安全组要放行的 TCP / UDP 端口（§8.1）。
  - **哪些命令要配置**：`run`、`join-blob`、`node *`、`service install` 要；`config init`、`stun probe`、帮助与版本不读配置。
- **CLI**（urfave/cli v3）：`run -c <yaml>`（默认动作）、`config init`、`join-blob [--network default]`、`node list|ban|unban`、
  `service install|remove|start|stop`（kardianos/service）、`stun probe`（P1-8）。首启**不打印** join blob。
  `service install` 先按上面的查找顺序定位配置并**完整校验**，再把它的**绝对路径**写进服务参数（`run -c <绝对路径>`）——
  服务运行时不再走查找顺序；挪动配置文件 = 重新 `service install`。
- **依赖面**：`CGO_ENABLED=0`；不得引入 Fyne、Badger、frp、gin（守卫测试，见 P1-9）。

##### P1-6 `internal/mesh`（管理器侧）

| 文件 | 内容 |
|---|---|
| `config.go` | `Config{Enabled, Label, Coordinator *{Addr, SPKI, NetworkID, NetworkSecret}}`，落 `{BaseDir}/mesh/config.json`（0600，原子写）；未配置 ⇒ `ErrNotConfigured`。P2 再加 `PeerPort`/`StaticPeers` |
| `identity.go` | 调 `meshid.LoadOrCreate({BaseDir}/mesh)` |
| `coordclient.go` | `Session` 长连接：1s 起翻倍、上限 60s 退避重连；收到 `Registered` 核对节点 ID；收到 `IncomingRelay` ⇒ 开 `Relay` 流、`Join`、包成 `streamconn` 投给 `relayListener`；`Kicked` ⇒ 记 ERROR 并按退避重连（不立即重连，避免两台机器互踢风暴） |
| `path.go` | §5.3 的 `pathProvider` 接口 + `PathKind`；P1 只有 `relayProvider`（`OpenRelay` → `Relay` → `streamconn`） |
| `peerclient.go` | 每个对端一个 `*grpc.ClientConn`：`passthrough:///<ID>` + `WithContextDialer`（选路）+ `meshid.PinnedTLSConfig(本机, 对端ID)` |
| `peerserver.go` | 一个 `grpc.Server`（`AnyClientTLSConfig`）`Serve(relayListener)`；拦截器按节点 ID 放行（未配对只能 `Hello`/`Pair`）；未配对连接并发上限 4、空闲 2 分钟关闭 |
| `listener.go` | `relayListener`：channel 喂 `net.Conn` 的 `net.Listener` |
| `manager.go` | `New(Options) *Manager`（测试直接用）+ 包级单例 `Initialize(baseDir)` / `Get()`，`Start`/`Stop`/`Status`/`Hello(ctx, ID)`，形态照 `frpmanage` |
| `logbridge` | 不需要：直接用 `pkg/logger`，前缀 `[mesh]`；grpc 自己的日志不接（太吵），连接错误由本包记 |

**生命周期接线**（`internal/webapi/actions.go`）：`InitializationBasicComponents` 里 `mesh.Initialize(cfgpkg.BaseDir)`；
`Start` 里启动（`ErrNotConfigured` 记 INFO，与 frp 一致）；`Stop` 里关闭。未配置时不发起任何连接、不监听任何端口。

##### P1-7 API 与 CLI（最小集）

- `internal/webapi/meshapi`：`GET /api/mesh/status`（本机节点 ID、协调节点连接状态、出口地址、协调节点下发的 `stun_addrs`、最近错误）、
  `POST /api/mesh/hello/:node`（对指定节点发一次 `Hello`，返回路径类型、耗时与对方版本）。两者都挂 `RequireAdmin()`。
  这是 P1 验收与排障的入口；完整的 `/api/mesh/*` 在 P3/P4。
- CLI `asa-server mesh`：`id`（打印本机节点 ID，必要时生成身份）、`join <blob>`（校验后写入 `config.json`）、`leave`、
  `status`（**只读本地文件**：节点 ID、协调节点地址、是否启用）。CLI 进程**不连协调节点**——它与正在运行的服务共用同一个身份，
  一连就会把服务的会话踢掉（P1-5 的单会话规则）。`join`/`leave` 写完提示「重启服务后生效」；运行时热应用留给 P4 的页面。
- `startupModeFor`：`mesh` 的子命令都要从 config.yaml 取 basedir（`id` 首次运行还会生成身份），一律按默认模式（需要有效配置），不进只读清单。

##### P1-8 协调节点 STUN 端点（2026-10-05 从 P6 提前，§5.6）

**目标**：协调节点上一个标准格式的 STUN Binding 服务——任何现成的 STUN 工具都能问到自己的反射地址。
管理器侧 P1 **不消费**它（地址发现、打洞都在 P6），但端口、报文子集、下发方式从此定型，P6 只升级管理器。

**`pkg/stun`（只用标准库，零领域依赖）**

| 文件 | 内容 |
|---|---|
| `message.go` | 报文：20 字节头（类型、长度、magic cookie `0x2112A442`、12 字节事务 ID）+ TLV 属性（4 字节对齐）。`Parse(b)` 严格校验：首 2 位为 0、长度是 4 的倍数且与实际一致、cookie 正确、属性不越界；带 `FINGERPRINT` 时校验 CRC32 ⊕ `0x5354554E`，且它必须是最后一个属性。`XOR-MAPPED-ADDRESS` 编解码 IPv4/IPv6（IPv6 与 cookie‖事务 ID 异或）。⚠️ 编码前一律 `netip.Addr.Unmap()`：双栈 socket 收到的 IPv4 来源是 `::ffff:a.b.c.d`，不处理的话 IPv4 客户端会拿到一个 IPv6 族的地址 |
| `server.go` | `Serve(ctx, pc net.PacketConn, opts ServerOptions) error`：读一个包、就地回写，无会话状态。规则见下表 |
| `client.go` | 拆成两半，**为 P6 留形状**：`NewBindingRequest() (txID, []byte)` + `ParseBindingResponse(b, txID) (netip.AddrPort, error)`——P6 的读侧走 `quic.Transport.ReadNonQUICPacket`，客户端不能自己去读 socket。另给普通 socket 一个便利函数 `Query(ctx, pc, server)`，按 500ms / 1s / 2s 重传三次（RFC 5389 §7.2.1 的简化） |
| `classify.go` | `ClassifyMapping(local, viaA, viaB netip.AddrPort) Mapping`：`NoNAT`（反射地址 = 本机地址）/ `EndpointIndependent`（两个端口问到的公网端口相同，可打洞）/ `EndpointDependent`（不同，即 NAT4）。§5.6.3 第 1 步的判据落在这里，P1 只给 `stun probe` 用 |

服务端规则：

| 收到 | 行为 |
|---|---|
| 不合法报文（`Parse` 失败、不是 request 类、`FINGERPRINT` 错） | **静默丢弃**——伪造来源的垃圾包不该换来任何回包 |
| Binding request，「必须理解」区间（0x0000–0x7FFF）里没有任何属性 | Binding success：`XOR-MAPPED-ADDRESS` + `FINGERPRINT`，事务 ID 原样 |
| Binding request 带「必须理解」属性（本服务一个都不认识：RFC 5780 的 `CHANGE-REQUEST`、ICE 的 `PRIORITY`/`USERNAME`/`MESSAGE-INTEGRITY` 等） | Binding error `420 Unknown Attribute` + `UNKNOWN-ATTRIBUTES`（RFC 5389 §7.3.1 的要求）。现成的 NAT 行为探测工具据此知道本服务不支持变 IP/端口测试，而不是干等超时 |
| 其他方法（TURN 的 Allocate 等）、Indication | 静默丢弃 |
| 超出限流 | 静默丢弃，计数 |

**刻意不做**：
- 不回 `MAPPED-ADDRESS`（RFC 3489 旧客户端用）：`XOR-MAPPED-ADDRESS` 是 2008 年起的标准，现成工具都认；多一个属性就多 12 字节放大量。
- 不回 `SOFTWARE`：未认证的公网端口不报版本号。
- 不支持 `CHANGE-REQUEST` / 不回 `OTHER-ADDRESS`（RFC 5780）：需要第二个公网 IP；单 IP 下的误判代价 §5.6.3 已论证过可以接受。
- 不做认证：答案只是「你自己的地址」，对谁都不是秘密。「只回答在线节点的出口 IP」这种白名单会被双栈误伤
  （`Session` 走 IPv4、STUN 走 IPv6 很常见），也挡掉了用现成工具排障。

**放大与滥用**——UDP 可以伪造来源，公网 STUN 服务是常见的反射源，必须一开始就有闸门：
- 放大倍数：请求最小 20 字节，成功响应 20 + 12/24（`XOR-MAPPED-ADDRESS` 的 IPv4/IPv6）+ 8（`FINGERPRINT`）= 40/52 字节，
  约 2～2.6 倍，是 STUN 固有的下限——所以上面能省的属性一个都不回。
- 限流：按来源令牌桶，IPv4 按 /32、**IPv6 按 /64**（一条家宽 IPv6 前缀里换地址是零成本的），默认 5 次/秒、突发 10；
  外加全局桶 2000 次/秒。来源表上限 65536 条、1 分钟不活动淘汰，满了新来源只受全局桶约束（表不会被撑爆）。
  P6 的管理器每 5 分钟问两次，远低于限额。
- 可观测：计数器（收到 / 成功 / 420 / 丢弃-格式 / 丢弃-限流）每 10 分钟**有变化时**记一行 INFO；
  限流丢弃首次发生时与之后每分钟最多一条 WARN（带被限的来源前缀），不刷屏。

**配置**（协调节点 YAML，P1-5 的 `stun` 段）：

```yaml
stun:
  listen: [":3478", ":3479"]   # 0 个 = 关闭；1 个能用但分辨不出 NAT4（启动时 WARN）；超过 2 个拒绝启动
  advertise: []                # 下发给管理器的地址；空 = public_addr 的主机名 + 各 listen 的端口
  rate_per_source: 5
  burst_per_source: 10
  rate_global: 2000
```

- **字段缺省 = 关闭**（配置文件里没有 `stun` 段不会突然多开 UDP 端口）；`config init` 生成的模板**默认开** `:3478`/`:3479`，
  模板注释写明云安全组与本机防火墙都要放行（§8.1）。
- 两个端口必须不同；任一端口绑不上 ⇒ **启动失败**（显式配置的端口不该静默降级成一个）。
- `:3478` 在 Go 里是双栈 socket，同时服务 IPv4 与 IPv6。
- 生命周期与 gRPC 服务同进退：`run` 里启动，收到停止信号时关 socket，`Serve` 返回。

**下发**：`Registered.stun_addrs`（`repeated string`，`host:port`）填 `advertise` 的结果。主机名**不在协调节点上解析**，
交给管理器解析——管理器可能走 IPv6。P1 的管理器只把它显示在 `/api/mesh/status` 里；P6 拿它做地址发现，不需要改 join blob。

**排障命令** `asa-coordinator stun probe <host>[:port] [--alt-port 3479]`：在任意机器上运行（协调节点二进制 `CGO_ENABLED=0`，
交叉编译出 Windows 版即可），向两个端口各问一次，打印本机地址、两个反射地址与 `ClassifyMapping` 的结论。
不读配置文件、不碰数据目录。

**依赖面**：`net`、`net/netip`、`hash/crc32`、`crypto/rand`，不引入 `pion/stun`——用到的报文子集很小，
RFC 5769 有官方测试向量可以直接验证编解码。

**工作量**：约 1～1.5 天。

##### P1-9 测试

| 包 | 用例 |
|---|---|
| `pkg/meshid` | ID 编解码往返、分组/大小写解析；钉错 ID 握手失败；重签证书 ID 不变；私钥文件权限（Linux） |
| `pkg/meshjoin` | 往返；截断 → `ErrChecksum`；前缀/版本错；`%v` 与 slog 输出不含密钥 |
| `pkg/streamconn` | 见 P1-4 |
| `pkg/stun` | **RFC 5769 官方向量**：§2.1 请求能解析、`FINGERPRINT` 校验通过，交给服务端得到 `420` 且 `UNKNOWN-ATTRIBUTES` 恰为其中的 `PRIORITY`/`USERNAME`/`MESSAGE-INTEGRITY`；§2.2 / §2.3 响应解出 `192.0.2.1:32853` 与 `[2001:db8:1234:5678:11:2233:4455:6677]:32853`。回环 UDP 端到端（v4、v6 各一）：`Query` 拿到的地址 = 客户端本地地址；双栈 socket 收 IPv4 来源回的是 IPv4 族。畸形包（截断、长度不符、cookie 错、`FINGERPRINT` 错、response 类、TURN 方法）一律无回包。限流：同来源超突发后丢弃；同一 /64 内不同地址共享一个桶，/64 之外不受影响；来源表满后新来源走全局桶。`ClassifyMapping` 三种结论。`FuzzParse`（种子 = RFC 5769 向量）不 panic、不越界读 |
| `internal/meshcoord` | 首次接入有/无/错误密钥；被 ban；**跨网络 `Resolve`/`OpenRelay` 返回 `NotFound`（安全用例）**；没有在线会话调 `Resolve` 被拒；token 一次性与 30 秒过期；中转字节完整性（随机 8 MiB 双向）；并发中转上限；同 ID 第二会话踢掉第一个；STUN 配置校验（3 个 listen、端口重复均拒绝启动；1 个 WARN）；`Registered.stun_addrs` 按 `advertise` / `public_addr` 推导正确，未开 STUN 时为空；配置文件：查找顺序（`-c` 优先、同目录次之、都没有时报错且不生成文件）、`data_dir` 与证书的相对路径按配置文件目录解析、不认识的字段报错、`config init` 生成的模板能被原样加载并通过校验、已存在时不带 `--force` 拒绝覆盖 |
| `internal/mesh` | **进程内端到端**：一个协调节点 + 两个 `Manager`（各自 `t.TempDir()`），A 经中转 `Hello` B，`granted_role = NONE`；协调节点重启后双方自动重新登记、`Hello` 恢复；未配对身份调非放行方法被拒（直接测拦截器的放行表） |
| 守卫 | `go list -deps`：mesh 相关包不含 `github.com/Niexiawei/simple-file-sync`；`cmd/asa-coordinator` 不含 `fyne.io`、`badger`、`fatedier/frp`、`gin-gonic` |

全部在 Windows（PowerShell，`-race`）与 WSL（`-race`）各跑一遍；`GOOS=linux CGO_ENABLED=0 go build ./cmd/asa-coordinator` 通过。

##### P1-10 验收

- [x] P1-9 全部通过（两个平台）。2026-10-05：Windows（PowerShell `-race`）与 WSL（`-race`）均通过；
      mesh / meshcoord / streamconn 连跑 5 轮无抖动；`FuzzParse` 20 秒约 1100 万次无问题。
- [ ] 真机：协调节点跑在 WSL（或一台 VPS），Windows 上的管理器与 WSL 里的另一个管理器（不同 BaseDir、不同身份）都 `mesh join` 同一 join blob；
      `curl POST /api/mesh/hello/<对方ID>` 双向成功，路径类型为 `relay`。
- [ ] 停掉协调节点 → 两边 `status` 显示断开、日志按退避重连而不刷屏；恢复后自动重新登记。
- [ ] 把 join blob 里的网络密钥改坏后接入的第三个管理器被拒，协调节点日志记 WARN。
      （跨网络隔离要第二个网络，而 D6-B 首期没有建网络的 CLI——它由 P1-9 的单测直接在存储里建第二个网络来覆盖。）
- [ ] STUN 格式是标准的，不只是自己和自己兼容：用一个**第三方** STUN 客户端（例如 stuntman 的 `stunclient`，
      或任何支持 RFC 5389 的工具）问协调节点的两个端口，都拿到正确的反射地址。
- [ ] 在 Windows 本机用 `asa-coordinator stun probe` 问协调节点，两个端口都有回答；若协调节点在 VPS 上，
      把本机家宽的映射类型记回本文，作为 P6 的第一条实测数据。（协调节点在 WSL 时这条路径上没有真正的 NAT，只能验证连通。）
      ——2026-10-05 已在 Windows 本机回环上冒烟：两个端口都回答、结论「无 NAT」；VPS / 家宽实测仍待做。
- [ ] 两台管理器的 `/api/mesh/status` 里都能看到下发的 `stun_addrs`。（进程内端到端用例已覆盖 `Status().STUNAddrs`，真机待做。）

**预计工作量**：6～8.5 天（原 5～7 天 + P1-8 的 STUN 端点 1～1.5 天）。

#### P1 实施记录（2026-10-05，分支 `feat/remote-mesh-p1`）

**状态：代码与 P1-9 全部完成；P1-10 的真机项待做**（上面未勾的几条）。

落地的文件：

| 位置 | 内容 |
|---|---|
| `api/asamesh/v1/{coordinator,peer}.proto`、`buf.yaml`、`buf.gen.yaml` | proto 与生成配置；生成代码在 `internal/mesh/meshpb`（`buf generate`，已提交） |
| `pkg/meshid` | `Generate` / `SelfSignedCert` / `LoadOrCreate` / `LoadOrCreateFiles` / `Reset`；`ID`（`String`/`Compact`/`Short`/`ParseID`/文本编解码）；`ClientConfig` / `ServerConfig` / `AnyClientServerConfig` / `PeerID` |
| `pkg/meshjoin` | `JoinBlob` 编解码、四种哨兵错误、`Format` / `LogValue` 打码 |
| `pkg/streamconn` | 流 ↔ `net.Conn`；含「在中转流上跑钉公钥的 TLS + gRPC」与「钉错公钥必须失败」的用例 |
| `pkg/stun` | 见 P1-8 |
| `pkg/atomicfile` | **新增**：只用标准库的原子写（见下方偏差 3） |
| `internal/meshcoord` | `config.go` / `template.go`（配置与模板）、`store.go`（SQLite）、`server.go`（登记、Resolve、拉黑复查）、`relay.go`（中转与限额）、`run.go`（组装、STUN 接线、优雅退出） |
| `cmd/asa-coordinator` | `run`（默认）/ `config init` / `join-blob` / `node list|ban|unban` / `service install|remove|start|stop` / `stun probe` |
| `internal/mesh` | `config.go`（`Join` / `Leave` / `LoadConfig`）、`coordclient.go`（Session 长连接、退避、中转路径）、`peerserver.go`（Peer 服务、拦截器、未配对连接上限）、`path.go`、`listener.go`、`manager.go`（单例、`Status`、`Hello`） |
| `internal/webapi/meshapi`、`internal/actions/mesh.go` | `GET /api/mesh/status`、`POST /api/mesh/hello/:node`（都 `RequireAdmin`）；`asa-server mesh id|join|leave|status` |
| `internal/webapi/actions.go`、`main.go` | 生命周期接线（与 frp 并列）；`appVersion` 常量同时用于 `--version` 与 `mesh.AppVersion` |

与上文设计的偏差（都是实现时发现的，按「为什么」记下）：

1. **proto 目录是 `api/asamesh/v1/`，不是 `api/mesh/v1/`**：buf 的 `PACKAGE_DIRECTORY_MATCH` 要求目录与 proto 包名 `asamesh.v1` 一致。
   `buf lint` 另关了五条规则：Session / Relay 是信封式双向流，不套 `XxxRequest` 命名；服务名与 `ROLE_NONE` 沿用本文叫法（`ROLE_NONE` 是真实取值「未授权」，不是「未设置」）。
2. **网络密钥存原文，不存哈希**（§8.4.5 写的是 `secret_hash`）：只存哈希的话 `join-blob` 没法随时重新打印接入串（首启又刻意不打印）；
   而数据库与协调节点自己的私钥在同一个 `data_dir` 里，能读到库的人本来就能冒充协调节点，哈希在这里挡不住任何人。数据库文件 0600。
3. **新增 `pkg/atomicfile`**：`pkg/meshid` 与协调节点都要原子写，而 `pkg/fsutil` 会经 gopsutil 拖大协调节点的依赖面；仓库里原有的三份私有
   `writeFileAtomic` 没动。
4. **`pkg/streamconn` 只依赖标准库**（§10.1 表里写的是依赖 grpc）：收发以函数注入，gRPC 流的适配写在调用方（`internal/mesh`、测试）。
   写超时会中止整条流（阻塞中的 Send 无法单独打断，写了一半的帧已让字节流处于未定义状态——与 crypto/tls 的约定一致）；
   `Close` 立即返回，后台先「等在途写 → 半关闭 → 等对端关闭」，最多 10 秒后才强制中止，避免丢掉 TLS close_notify 这类尾巴。
5. **`Register` 多了 `label = 6`**：协调节点 `node list` 要显示备注名，P1-6 的配置里本来就有 `Label`。
6. **未配对入站连接上限做在 TLS 凭据的包装里**（`limitedCreds`）：握手之前不知道对方是谁，握手之后、交给 gRPC 之前是唯一能按身份计数的地方。
   空闲 2 分钟关闭用 gRPC 的 `MaxConnectionIdle`，P1 没有配对，所以对全部入站连接生效；P3 有了配对后再只对未配对身份生效。
7. **`node ban` 对正在运行的协调节点生效**：`node ban` 是另一个进程直接写库，协调节点每 30 秒按拉黑表复查在线会话并踢掉被拉黑的（原设计只在下次登记时拦）。
8. **协调节点日志**写 `{data_dir}/logs/coordinator.log`（复用 `pkg/logger`），启动、配置路径等关键行同时上屏。
9. **中转凭据绑定节点**：token 只能由 OpenRelay 时指定的那个节点使用（按 TLS 身份核对），别人截获了也用不了；同一侧不能 Join 两次。
10. `go.mod` 只把三个已在依赖图里的模块从 indirect 升为直接依赖（`go.yaml.in/yaml/v3`、`golang.org/x/time`、`google.golang.org/protobuf`；
    grpc 本来就是直接依赖），**没有版本变化、没有新模块**。Linux 版协调节点约 27 MB（主要是纯 Go 的 SQLite）。

> **P2～P4 一起做**（2026-10-05）：三期都在分支 `feat/remote-mesh` 上完成（P1 的 `feat/remote-mesh-p1` 已改名为它），
> 不再按阶段拆独立分支。下面的细化是开工前写的；落地偏差照 P1 的做法另起「实施记录」。

### P2 — 直连
- [ ] Peer 端口监听、候选地址上报（枚举网卡，排除回环/链路本地/docker 网桥）、手填地址。
- [ ] Happy Eyeballs 选路、出口 IP 相同优先内网、路径升级（中转 → 直连）、无协调节点模式。
- **验收**：同一内网两台走直连；断开内网连通性后自动落到中转；恢复后升级回直连；拨到「同 IP 的别的机器」握手失败而不是串线（测试里伪造）。

#### P2 细化（2026-10-05，待确认后开工）

**协调节点不用改**：P1 的 `Register.candidates`、`CandidatesUpdate`、`ResolveResponse.{candidates, peer_observed_ip, self_observed_ip}`
已经是 P2 要的全部协议。P2 只动管理器侧。

##### P2-1 端到端 TLS 挪进 dialer（对 P1 的一处结构修正）

P1 的做法是「dialer 只给出一条原始连接，gRPC 的 `TransportCredentials` 在上面做 TLS 握手」。直连之后这不成立：
**钉公钥的校验必须在选路时完成**。否则拨到「恰好也叫 `192.168.1.10` 的别人家机器」时，dialer 认为成功了，握手失败发生在
gRPC 内部 → gRPC 进入 TRANSIENT_FAILURE、退避后**再次调同一个 dialer、再拨同一个错误地址**，永远落不到中转。

改为：
- dialer 对每条候选路径（直连 TCP / 中转流）**自己完成**端到端 mTLS（`meshid.ClientConfig(本机, 对端)`，`NextProtos: ["h2"]`——
  grpc-go 服务端默认强制 ALPN），返回 `*tls.Conn`；握手失败 = 这条候选失败，继续下一条。
- 客户端 gRPC 用一个新的 `handshakenCreds`：`ClientHandshake` 不做握手，只从 `*tls.Conn` 取出 `ConnectionState` 包成
  `credentials.TLSInfo`（安全级别 PrivacyAndIntegrity），上层看到的与原来一样。服务端不变（仍是 `credentials.NewTLS` + `limitedCreds`）。
- 线上格式与 P1 完全相同（TLS 1.3 + h2），P1 的管理器之间互通不受影响。

##### P2-2 配置（`config.json` 增量）

```go
type Config struct {
    Enabled     bool
    Label       string
    Coordinator *CoordinatorConfig // 可为空 = 无协调节点模式
    PeerPort    int      `json:"peer_port,omitempty"`     // 0 = 默认 19194
    NoListen    bool     `json:"no_listen,omitempty"`     // D7：默认监听；true = 只走中转（只能出、不能进）
    PublicAddrs []string `json:"public_addrs,omitempty"`  // 手填的本机公网地址（端口映射 / DDNS），作为 CONFIGURED 候选上报
    ControlRole string   `json:"control_role,omitempty"`  // D5：A 上谁能用远程控制，"admin"（默认）或 "operator"
}
```

- **无协调节点模式**：`Enabled && Coordinator == nil` 是合法配置——不连协调节点、只监听 Peer 端口、只按对端的手填地址直连。
  `LoadConfig` 的 `ErrNotConfigured` 改为只看「文件不存在或 `!Enabled`」。
- `mesh leave` 语义不变（清协调节点 + 停用）；新增 `mesh enable` / `mesh disable`（只拨开关），无协调节点模式由 `mesh enable` 进入。
- 对端的手填地址**不在 config.json**，在 P3 的 `peers.json` 的对端条目里（`addrs`）——地址属于「那台对端」，与配对记录同生命周期。
  P2 先建好 `peers.json` 的存储（P3-1），P2 只用其中的 `addrs`。

##### P2-3 Peer 端口与候选地址

- `Start` 时在 `:PeerPort` 上 `net.Listen("tcp")`（双栈），同一个 `grpc.Server` 同时 `Serve(tcpLn)` 与 `Serve(relayLn)`。
  **绑不上不让整个 mesh 起不来**：记 WARN、`Status.ListenError` 给页面显示，退化为只走中转——端口被占不该让远程管理整体失效。
- 候选地址（`candidates.go`）：`net.Interfaces()`，取 up、非回环；地址排除回环、链路本地（`169.254/16`、`fe80::/10`）、未指定；
  按网卡名排除虚拟网桥：`docker*`、`br-*`、`veth*`、`virbr*`、`cni*`、`flannel*`、`vEthernet (WSL*)`（Hyper-V 给 WSL 的那张）。
  IPv6 只取全局单播与 ULA（`fc00::/7`）。端口 = 实际监听端口。`PublicAddrs` 以 `CONFIGURED` 上报。`NoListen` 或监听失败时不报 HOST 候选。
- 上报：`Register.candidates` 带初始值；之后每 60 秒重新枚举，**有变化才**发 `CandidatesUpdate`（换网、DHCP 续约后地址变了）。

##### P2-4 选路（`path.go` 重写 `dialPeer`）

```
候选来源 = Resolve(B).candidates（有协调节点且 B 在线时） ∪ peers.json 里 B 的 addrs（CONFIGURED）
每个候选归类：HOST 且地址是私网（RFC1918 / ULA / 100.64/10）⇒ lan；其余（公网 HOST、CONFIGURED）⇒ public
排序：出口 IP 相同（self_observed_ip == peer_observed_ip）⇒ lan 在前；否则 public 在前、lan 在后（仍然尝试：两边可能在同一个 VPN / 组网里）
```

- **Happy Eyeballs**（`happyeyeballs.go`）：按上面的顺序，每 250ms 发起下一个（TCP + 端到端 mTLS），整体 2 秒上限；
  第一个完成握手的胜出，其余取消并关闭（已握手成功但落选的连接也要关）。
- 直连全部失败（或没有候选）⇒ `relayProvider`（有协调节点时）。都不行 ⇒ 返回合并的错误。
- `Resolve` 失败（协调节点断开、`NotFound`）不阻断：仍按手填地址直连——「协调节点挂了，已知地址的直连不受影响」（§13）。
- 路径类型记在连接上（`pathConn.kind`），`lastPath` 由 dialer 回写，`Status` / 页面显示。

##### P2-5 路径升级（中转 → 直连）

每个对端的 gRPC 连接包成 `peerHandle{cc, kind, refs}`：
- 所有 RPC（`Hello`、P3 的 `Pair` / `HTTP`）都经 `acquire(peer)` / `release()` 取连接；`HTTP` 流的 release 发生在**流结束**时。
- dialer 落到中转后，为该对端启动一个升级循环：1 分钟起、翻倍到 10 分钟，每次只做「直连 Happy Eyeballs」。
  成功 ⇒ 用**这条已握手的连接**建一个新 `grpc.ClientConn`（一次性 dialer 先交出它，之后回到正常 dialer），换成当前 handle；
  旧 handle 标记退役，**引用计数归零才 `Close`**——中转上的在途流（日志 SSE、WS）自然结束后再关，新请求立即走直连。
- 降级不需要专门做：直连断开后 gRPC 重新调 dialer，按顺序落到中转，升级循环随之重启。
- 循环在 `Stop`、handle 退役或连接空闲关闭时结束。

##### P2-6 状态与 CLI

- `Status` 增加 `listen_addr`、`listen_error`、`candidates`（本机上报的那份）；`HelloResult.path` 已有。
- `mesh status` 打印 Peer 端口与是否监听（只读本地配置）；`mesh enable|disable`。

##### P2-7 测试

| 用例 | 做法 |
|---|---|
| 同一内网走直连 | 进程内协调节点 + 两个 Manager（监听 `127.0.0.1:0`），A `Hello` B ⇒ `path=lan` |
| 无协调节点模式 | 不配协调节点，A 的 `peers.json` 里写 B 的地址 ⇒ `path` 为直连 |
| 拨到同 IP 的别的机器 | B 的候选被伪造成 C 的监听地址（C 是第三个 Manager）⇒ 握手因 SPKI 不符失败、**落到中转**，`Hello` 拿到的是 B 的回答 |
| 断开 → 中转 → 恢复 → 升级 | B 的 TCP 监听包一层可开关的 listener（关掉时 Accept 后立即断）；升级间隔用 `Options` 调到毫秒级；断开后新连接是 `relay`，恢复后升级为 `lan`，**升级时一条在途的中转流不被打断** |
| Happy Eyeballs | 第一个候选是黑洞（不回 SYN 的地址用「Accept 后不握手」的 listener 模拟），第二个可用 ⇒ 250ms 后的那个胜出，总耗时 < 2s |
| 候选枚举 | 名字过滤与地址过滤的表驱动用例（不依赖本机网卡） |
| 监听失败不致命 | 端口先被占 ⇒ `Start` 成功、`Status.ListenError` 非空、中转照常 |

### P3 — 配对、授权、隧道
- [ ] 邀请码与申请-批准两种配对、`peers.json`、撤销。
- [ ] `Peer.HTTP` 两端：B 侧 `ResponseWriter`（Flusher/Hijacker）、A 侧 ReverseProxy + 自定义 RoundTripper、头清洗与 Origin 改写。
- [ ] `authapi` 的 `PeerIdentity` 分支（**先于** `auth.enabled` 短路）、远程禁区、禁止多跳、审计。
- [ ] `internal/webapi/meshapi`。
- **验收**：用 curl 经 A 的 `/api/peers/<B>/fwd/...` 完成 B 实例的启动/停止、拉日志 SSE、WS 事件、RCON；
  B 关着鉴权时 `operator` 授权的 A 仍然调不了 `RequireAdmin` 路由；远程访问 `/api/users`、`/api/peers` 一律 403。
  安全用例单独成组，作为回归守卫。

#### P3 细化（2026-10-05，待确认后开工）

##### P3-1 `peers.json`（`internal/mesh/peers.go`）

一个文件记三类东西：对端、未用的邀请码、待批准的申请。0600，原子写。

```jsonc
{
  "version": 1,
  "peers": [{
    "node_id": "…",
    "label": "机房-2",            // 本机给它起的备注名
    "granted_role": "operator",   // 入站：本机授予它的角色；"" = 未授权（只是我能控制它）
    "granted_at": "…",
    "remote_role": "admin",       // 出站：它授予我的角色（Hello 的回答缓存，显示用，不作为授权依据）
    "remote_label": "…", "remote_version": "…",
    "addrs": ["192.168.1.20:19194"], // 手填的直连地址（P2）
    "added_at": "…", "last_seen": "…", "last_addr": "…"
  }],
  "invites":  [{ "id": "…", "secret_sha256": "…", "role": "operator", "note": "…", "created_at": "…", "expires_at": "…" }],
  "requests": [{ "node_id": "…", "label": "…", "version": "…", "addr": "…", "requested_at": "…" }]
}
```

- **授权只看 `granted_role`**（入站方向）。`remote_role` 只是缓存。
- **CLI 与服务是两个进程**（`mesh invite` / `mesh revoke` 在服务运行时执行），所以：
  - 读改写一律在 `pkg/filelock` 的排他锁下（`peers.json.lock`）；
  - 服务侧按 `(mtime, size)` 发现文件被别人改过就重新加载（每次授权判断时 `stat` 一次，热路径只有一次 stat，不读文件），
    重新加载后对比新旧授权表，**授权被撤销或角色变化的对端，其在途隧道流与入站连接立刻断开**（见 P3-5）。
- 邀请码只存 SHA-256；过期的邀请与 7 天前的申请在每次写入时顺手清掉；待批准申请上限 32 条（满了拒绝新的，防刷）。

##### P3-2 邀请码（`pkg/meshjoin` 增量）

`asa-mesh-invite:v1:<base64url(JSON)>.<校验>`，与 join blob 同构、同一套错误（`ErrPrefix`/`ErrVersion`/`ErrChecksum`）：

```go
type Invite struct {
    Node    meshid.ID // B 的节点 ID（A 据此钉公钥）
    ID      string    // 邀请编号（B 据此找记录，不必拿密钥去逐条比对）
    Secret  []byte    // 32 字节一次性密钥
    Role    string    // 授予的角色（只是提示；以 B 侧记录为准）
    Addrs   []string  // 可选：B 的直连地址（带上它时，无协调节点也能配对）
    Expires time.Time
}
```

`Format` / `LogValue` 打码 `Secret`。有效期默认 10 分钟（`--ttl` 可改，上限 24 小时）。

##### P3-3 `Peer.Pair`（proto 增量）

```proto
rpc Pair(PairRequest) returns (PairResponse);   // 未配对身份可调（加进 unpairedMethods）
message PairRequest  { string invite_id = 1; bytes invite_secret = 2; string label = 3; string version = 4; }
message PairResponse { PairStatus status = 1; Role granted_role = 2; string label = 3; }
enum PairStatus { PAIR_STATUS_UNSPECIFIED = 0; PAIR_STATUS_PAIRED = 1; PAIR_STATUS_PENDING = 2; }
```

- **带邀请**：B 按 `invite_id` 找记录、常数时间比对 SHA-256、未过期 ⇒ 记下 A 的公钥与角色、**删除该邀请**（一次性）⇒ `PAIRED`。
  已配对的身份用新邀请再配一次 = 改成新邀请的角色。
- **不带邀请**（申请-批准）：B 记一条待批准申请（同一 ID 只保留最新一条）⇒ `PENDING`；B 的管理员在页面/CLI 选角色批准后，
  A 下次 `Hello` 就能看到授予的角色。B 已经授权过 A 时直接回 `PAIRED`。
- **限流**：按调用者节点 ID，邀请错误 5 次/10 分钟后 `ResourceExhausted`；全局每分钟 30 次 `Pair`。
  邀请错误统一回 `PermissionDenied`「邀请码无效或已过期」，不区分哪一项错。
- 邀请密钥只在端到端 mTLS 里出现，协调节点看不到（§6.1）。
- A 侧：配对成功（或已提交申请）后，A 在自己的 `peers.json` 里记下 B（`remote_role`、`addrs` 取自邀请码）。

##### P3-4 `Peer.HTTP` 隧道（proto 增量）

```proto
rpc HTTP(stream HTTPFrame) returns (stream HTTPFrame);   // 一个请求 = 一条流；只对已授权身份开放
message HTTPFrame {
  oneof msg {
    HTTPRequestHead  request  = 1;   // A→B 首帧
    HTTPResponseHead response = 2;   // B→A 首帧（未劫持时）
    bytes            body     = 3;   // 双向
    HTTPBodyEnd      end      = 4;   // 发送方的 body 结束（不用 CloseSend：WS 升级后还要继续发）
    bytes            raw      = 5;   // B→A：连接被劫持（WebSocket）后的原始字节，含 101 响应行
  }
}
message HTTPRequestHead { string method = 1; string path = 2; string raw_query = 3; repeated HTTPHeader headers = 4;
                          string remote_user = 5; }        // A 上发起者的用户名，B 只用于审计，不作授权依据
message HTTPResponseHead { int32 status = 1; repeated HTTPHeader headers = 2; }
message HTTPHeader { string name = 1; repeated string values = 2; }
message HTTPBodyEnd {}
```

**B 侧**（`tunnel_server.go`）：
- 构造 `http.Request`：URL = `path` + `raw_query`，`Host` 固定为 `mesh.peer`；`RemoteAddr` **固定为 `[100::1]:0`**（RFC 6666 丢弃前缀）——
  无论直连还是中转，永远不会是回环或内网地址，任何按来源 IP 判断的旧逻辑（`IsLoopbackRequest`、`lan_bypass`）对它都不成立；
  真实来源进 `PeerIdentity.Addr` 供审计。
- **B 自己再清洗一遍请求头**（A 可能是恶意的，A 侧的清洗只是礼貌）：删 `Cookie`、`Authorization`、`X-Forwarded-*`、`X-Real-IP`、`Forwarded`、`Origin`。
- context 里放 `PeerIdentity{NodeID, Label, Role, RemoteUser, Addr}`（来自授权表与 TLS 身份，**不来自任何请求头**），然后直接
  `engine.ServeHTTP`（`mesh.SetHTTPHandler(engine)` 由组合根注入，避免 mesh → webapi 成环）。
- 自定义 `ResponseWriter`：`WriteHeader` → `response` 帧；`Write` 攒到 32 KiB 发一个 `body` 帧；`Flush` 立即发（SSE）；
  handler 返回 ⇒ 发出剩余 body + `end`、结束流。实现 `http.Hijacker`：返回一个由这条流包装的 `net.Conn`
  （读 = 后续的 `body` 帧，写 = `raw` 帧）——gorilla/websocket 自己往劫持的连接里写 101 响应，B 不解析，原样转给 A。
- 请求 ctx = 流的 ctx ⇒ A 断开（浏览器关页面）时 B 的 handler 自然收到取消。

**A 侧**（`tunnel_client.go`）：`Manager.RoundTripper(peer, remoteUser) http.RoundTripper`：
- 开 `HTTP` 流、发 head、后台把 `req.Body` 抄成 `body` 帧再发 `end`；读首帧：
  - `response` ⇒ 组 `http.Response`，Body 读后续 `body` 帧直到 `end`/EOF；
  - `raw` ⇒ 把 `raw` 帧串成一个 `bufio.Reader`，`http.ReadResponse` 解析出 101，Body 是一个 `io.ReadWriteCloser`
    （读剩余 `raw`，写 = `body` 帧）——这正是 `httputil.ReverseProxy` 处理协议升级所要求的形状。
- 流的 `acquire/release`（P2-5）在 Body 关闭时 release。

##### P3-5 B 侧授权与撤销

- 拦截器：已授权（`granted_role != ""`）⇒ 放行全部方法；未授权 ⇒ 只放行 `Hello`、`Pair`。`Hello` 回 `granted_role` 与（仅对已授权）`label`。
- **撤销 / 降级立即生效**：Peer 服务端按节点 ID 登记在途 `HTTP` 流（各自的 cancel）与入站连接；授权表变化（页面操作或 P3-1 的文件重载）
  ⇒ 取消该 ID 的全部流、关闭它的连接。长连接的 SSE / WS 不会在撤销后继续活着。
- `MaxConnectionIdle`（P1 偏差 6）维持对全部入站连接 2 分钟：已授权的对端被关掉空闲连接后，客户端下次请求透明重拨，代价只是一次握手；
  而远程面板打开时 SSE 一直在流，连接不会空闲。按身份区分空闲超时要自己实现连接级计时器，收益不值得，不做。

##### P3-6 `authapi` 的 `PeerIdentity` 分支

`internal/mesh/identity_ctx.go`：`PeerIdentity` 类型 + `WithPeerIdentity` / `PeerIdentityFrom`（context key 不导出）。`authapi` import `mesh` 只为这两个函数。

- `Middleware()` **第一步**（先于 `!cfg.Auth.Enabled`）：`PeerIdentityFrom(ctx)` 有值 ⇒
  1. 远程禁区（前缀匹配）⇒ 403 `code=peer_forbidden`：`/api/users`、`/api/auth/`（`/api/auth/state` 除外）、`/api/mesh`、`/api/peers`。
     「禁止多跳」由 `/api/peers` 落在禁区里保证。
  2. 合成 `*auth.User{Username: "peer:<备注名>/<A 上的用户名>", Role: 授予角色}` 设进上下文，另设 `auth.peer` 标记；
     审计来源 = `{ClientIP: 对端真实地址, UserAgent: "asa-mesh/<对端节点 ID 前 8 位>"}`。
  3. `c.Next()`。`lan_bypass` 对隧道请求永不生效（根本走不到那一步）。
- `RequireAdmin()` 第一步：是隧道请求 ⇒ 只看授予角色是否 `admin`，**与 `auth.enabled` 无关**。
- `IsAuthenticated()`（WS 的 AuthGate）：隧道请求 ⇒ `true`（撤销由 P3-5 断流保证，不依赖 60 秒一次的复查）。
- `/api/auth/state` 对隧道请求回 `{auth_enabled: B 的值, authenticated: true, peer: true}`——前端在远程上下文不该被 B 的登录态弄糊涂。
- **审计**：B 侧每个隧道请求结束时记一行 `[mesh] peer:<备注名>/<用户> <方法> <路径> → <状态码>`（GET/HEAD 记 DEBUG，其余 INFO）；
  B 开着鉴权时，非 GET 请求另写一条 `auth` 审计（新事件 `peer_request`，`Actor` = 合成的用户名，`Detail` = 方法 + 路径 + 状态码）。
  审计页面上 `peer:` 前缀即标注「对端自述的用户名」（§6.4 的警告）。

##### P3-7 A 侧转发入口 `/api/peers/:id/fwd/*path`

- 路由在 `/api` 下（§7.2：A 的鉴权中间件只拦 `/api` 前缀），挂 `requireControl()`：A 开着鉴权时，发起者角色必须满足
  `config.control_role`（默认 `admin`，D5）；A 关着鉴权时放行（页面上提示）。
- `httputil.ReverseProxy`（`Rewrite` 形态）：目标 URL = `/<path>`；**删** `Cookie`、`Authorization`、`X-Forwarded-*`、`X-Real-IP`、`Forwarded`；
  WebSocket 升级请求**先在 A 侧按 `realtime` 的同源规则校验 `Origin`**（否则就成了一个绕过同源检查的 WS 入口），校验后删掉 `Origin` 再转发
  （B 的 `CheckOrigin` 对空 Origin 放行）——这比原设计的「改写成 B 的 Host」简单且不依赖 B 的 Host 判定细节。
- 响应：删 `Set-Cookie`（B 不许往 A 的源上种 Cookie）；B 回 401 ⇒ 改成 403 `code=peer_unauthorized`——A 的前端见到 401 会把**本机**登出。
  连不上对端 ⇒ 502 `code=peer_unreachable`；对端未授权本机（`PermissionDenied`）⇒ 403 `code=peer_not_paired`。
- `remote_user` = A 上的 `ActorName(c)`（A 关鉴权时为空，B 记作 `peer:<备注名>/-`）。
- A 侧也记一行 `[mesh] → <对端> <方法> <路径> → <状态码>`（非 GET INFO），便于两边对账。

##### P3-8 `internal/webapi/meshapi`（全部 `RequireAdmin`）

| 方法 | 路径 | 内容 |
|---|---|---|
| GET | `/api/mesh/status` | 已有；加 P2-6 的字段与 `control_role` |
| PUT | `/api/mesh/config` | 备注名、Peer 端口、是否监听、公网地址、`control_role`；保存后**热应用**（`Manager.Reload` = Stop + Start） |
| POST | `/api/mesh/join` / `/api/mesh/leave` / `/api/mesh/enable` / `/api/mesh/disable` | 同 CLI，写完热应用 |
| GET | `/api/mesh/peers` | 对端列表 + 运行时状态（当前路径、最近一次 Hello 的 RTT / 版本 / 授予我的角色、是否在线） |
| PUT / DELETE | `/api/mesh/peers/:id` | 改备注名 / 授予角色 / 手填地址；删除 = 撤销入站授权并忘掉它 |
| POST | `/api/mesh/peers/:id/hello` | 立即 Hello 一次（刷新）；原 `/api/mesh/hello/:node` 保留为别名 |
| POST | `/api/mesh/pair` | `{invite}` 或 `{node_id, addrs?}`（申请-批准） |
| GET / POST / DELETE | `/api/mesh/invites[/:id]` | 列出（不含密钥）/ 生成（返回整串，**只此一次**）/ 作废 |
| GET / POST / DELETE | `/api/mesh/requests[/:id]` | 待批准列表 / 批准（带角色）/ 拒绝 |

##### P3-9 CLI

`asa-server mesh`：`id`、`join`、`leave`、`enable`、`disable`、`status`（P1/P2）+ `peers`（列表）、`invite [--role operator|admin] [--ttl 10m] [--addr host:port]...`、
`revoke <节点ID>`、`approve <节点ID> [--role]`。仍然**不连协调节点、不连对端**（同一身份规则）；写 `peers.json` 后服务按 P3-1 自动重载，不用重启。

##### P3-10 测试

| 组 | 用例 |
|---|---|
| 配对 | 邀请码配对成功并消费（第二次用同一邀请失败）；过期邀请失败；错误次数超限 `ResourceExhausted`；申请-批准：`PENDING` → 批准后 `Hello` 返回授予的角色；申请条数上限 |
| `peers.json` | 另一个「进程」（直接调文件层 API）撤销 ⇒ 服务侧重载、在途隧道流被取消；锁下并发写不丢更新；邀请码只存哈希 |
| 隧道 | 进程内 A、B 两个 Manager + B 侧一个最小 Gin engine：普通请求往返（含 8 MiB 上传 / 下载）；SSE 逐条到达（不被攒批）；WebSocket 经 A 的 ReverseProxy 双向收发；A 断开 ⇒ B 的 handler ctx 被取消 |
| **安全（回归守卫，单独成组）** | 未授权身份调 `HTTP` ⇒ `PermissionDenied`；B 关鉴权 + 授予 `operator` ⇒ `RequireAdmin` 路由 403；`/api/users`、`/api/auth/me`、`/api/mesh/status`、`/api/peers/x/fwd/...` 经隧道一律 403；A 伪造 `Cookie` / `X-Forwarded-For` / `Authorization` 在 B 侧不生效（B 看到的头里没有它们）；`RemoteAddr` 不是回环（`IsLoopbackRequest` 为 false）；撤销后在途 SSE 立即结束；A 侧 B 回 401 被改写为 403；A 侧 WS 跨源 `Origin` 被拒；`Set-Cookie` 不透传 |
| `authapi` | 隧道请求在 `auth.enabled=false` 与 `true` 两种配置下的放行表；`ActorName` = `peer:…` |

### P4 — 前端
- [ ] 「远程管理器」页、顶栏机器选择器、API 前缀集中切换、版本不一致提示、远程上下文隐藏禁区页面。
- **验收**：浏览器人工验收清单（照 `docs/TEST_ENV_COUPLING_PLAN.md` 的人工清单格式）：所有现有页面在远程上下文各走一遍。

#### P4 细化（2026-10-05，待确认后开工）

##### P4-1 「当前机器」上下文：`utils/peerContext.js`

- 当前对端 ID 存 **`sessionStorage`**（每个标签页各管一台，互不干扰；刷新后保持）。
- `peerPath(url)`：远程上下文里把 `/api/...`、`/health` 改写为 `/api/peers/<ID>/fwd/api/...`；
  **永远留在本机**的前缀：`/api/auth/`、`/api/mesh/`、`/api/peers/`（会话、远程管理本身、防多跳）。
- 接入点全部集中（§7.3 列的那几处）：`utils/http.js` 的 axios 请求拦截器改 `config.url`；`utils/utils.js` 的
  `buildEventSourceUrl` / `buildWebSocketUrl` 先过 `peerPath`；`wsManager.js`、`rconStore.js`、两个 worker 用的都是这两个函数，自动生效。
- **切换机器 = 写 `sessionStorage` + 整页重载**。`wsManager` / `rconStore` 在模块加载时就拼好了 URL，资源 worker、SSE、各 store
  都有自己的长连接与缓存；逐个通知它们换前缀既容易漏，又会把 A 的数据与 B 的数据短暂混在一个页面里。重载是唯一不会串数据的做法。
- 远程上下文里 axios 收到 `peer_unreachable` / `peer_not_paired` / `peer_unauthorized` 时提示并提供「回到本机」，不跳登录页。

##### P4-2 顶栏机器选择器（`components/MachineSwitcher.vue`，挂在 `App.vue` 顶栏）

- 只在本机是管理员（或本机关鉴权）且 `/api/mesh/status` 显示已启用时出现；选项 = 「本机」+ `/api/mesh/peers` 里**授予了我角色**的对端
  （显示备注名、在线状态、路径 lan / public / relay）。
- 远程上下文顶部常驻一条提示：「正在管理 <备注名>（<路径>）」+「回到本机」；对方版本与本机不同时加一句「对方版本 x.y，与本机 z 不同，部分页面可能不可用」（§9）；
  经中转时提示「经中转：大文件上传/下载较慢」。

##### P4-3 远程上下文隐藏的页面

- 「用户管理」（`/user-manager`）、「远程管理器」（`/mesh`）：菜单隐藏 + 路由守卫重定向到首页（远程禁区 §6.3，B 那边本来也会 403）。
- 「个人资料」仍是**本机**的（`/api/auth/*` 留在本机），不隐藏。
- 授予我的角色是 `operator` 时，需要 `admin` 的按钮在 B 那边会 403——P4 不逐个按钮按远程角色置灰，统一由 403 提示兜底（与本机 operator 用户现在的体验一致）。

##### P4-4 「远程管理器」页（`views/MeshManager.vue`，路由 `/mesh`）

照 `FRPManager.vue` 的表单形态，分四块：
1. **本机**：节点 ID（可复制）、协调节点连接状态 / 出口地址、Peer 端口监听状态、备注名 / 端口 / 公网地址 / `control_role` 表单；
   粘贴 join blob 接入、断开接入、启用 / 停用。Windows 首次监听会弹防火墙提示，这里写明；A 关着鉴权时显示警告（§6.3）。
2. **我能控制的机器**：表格（备注名、节点 ID 短格式、在线、路径、RTT、对方版本、对方授予我的角色）、「切换过去」、改备注 / 手填地址、删除；
   「添加」= 粘贴邀请码，或输入对方节点 ID 发起申请（之后显示「等待对方批准」，可手动刷新）。
3. **能控制本机的机器**：已授权的对端（授予的角色可改、撤销）；待批准申请（选角色批准 / 拒绝）。
4. **邀请码**：生成（角色、有效期、是否附带本机直连地址）——生成后的整串只显示这一次；未用邀请列表与作废。

`App.vue` 三处联动：菜单项、`watch(route.path)` 高亮、`handleMenuClick` 分支；路由 `meta` 标记 `localOnly`（P4-3 的守卫据此判断）。

##### P4-5 人工验收清单

写进本文「P2～P4 实施记录」：两台管理器（本机 Windows + WSL，或两台机器）配对后，在远程上下文把现有每个页面走一遍
（实例列表 / 详情 / 启停 / 日志 SSE / RCON WS / 配置编辑 / 备份 / 插件上传 / 资源监控 / 定时任务 / FRP / 同步 / 系统日志），
记录每项结果；再验证切回本机后数据不串、远程上下文里用户管理与远程管理器页不可见、断开对端时的提示。

### P5 — 跨机编排（可选，按需）
- [ ] 类型化方法：`Peer.Overview`（流式：对方所有实例状态 + 资源摘要）→ A 的总览页显示所有机器。
- [ ] 跨机批量启停：在 `batchmanage` 之上，每台机器各自走它本地的 `countdown`。

### P6 — 增强（视需要）
- [ ] 打洞（按 §5.6 的方案：UDP socket + `quic.Transport`、用 `Registered.stun_addrs` 做地址发现、信令与探测、`punched` 路径）。
      协调节点的 STUN 端点已在 P1-8 完成，P6 不需要升级协调节点。
- [ ] 反向直连（§5.3）、协调节点 Web 界面、中转流量统计；若要 IP 级互通再评估 tsnet（§3.3）。
- [ ] 若要把协调节点借给别人用：运营者的网络管理 CLI 与每网络配额（§8.4.4 的 C）。

---

## 13. 风险与回滚

| 风险 | 缓解 |
|---|---|
| 远程入口被利用 | §6 全部条目；安全用例作为 P3 的验收硬条件；默认关闭，必须显式启用并配对 |
| 协调节点挂了 | 已建立的直连不受影响（直连不经过协调节点）；中转中的会话断开；管理器退避重连。本地游戏服完全不受影响 |
| 中转流量超预期 | 限额 + 页面提示「经中转」；路径升级尽量回到直连 |
| 版本错位导致远程页面报错 | `Hello` 能力列表 + 版本提示（§9）；推荐所有机器同版本升级 |
| Windows 防火墙拦 Peer 端口 | 只影响直连，自动落到中转；页面上提示放行方法 |
| 回滚 | mesh 未启用时 `ErrNotConfigured` 短路、不监听任何端口；回滚 = 关掉开关 |
