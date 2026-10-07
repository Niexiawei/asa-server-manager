# 管理器互控（gRPC + 协调节点）可行性评估与实施计划

> 状态：§11 全部决策已定（2026-10-04）。**P1 代码已完成（2026-10-05，分支 `feat/remote-mesh-p1`，现已改名 `feat/remote-mesh`），
> 单测两个平台通过，真机验收待做**——见 §12「P1 实施记录」。**P2～P4 代码已完成（2026-10-05，同一分支），自动化测试两个平台通过、
> 真机二进制冒烟通过**——见 §12「P2～P4 实施记录」。
> **P5（跨机编排）已废弃（2026-10-07）**：对现有页面与逻辑改动太大，细化内容仅存档，见 §12「P5」。
> **2026-10-07 页面调整**：协调节点改为单纯的配置项（保存 / 替换 + 解析预览），mesh 启停改由「远程管理器」页顶部的按钮控制，见 §12「P4 后续调整」。
> **P6 打洞代码已完成（2026-10-07，同一分支，未提交）**：信令走中转路径上的 Peer gRPC，协调节点零改动；自动化测试两个平台通过、真实二进制回环冒烟通过，
> 真机（家宽 NAT）验收待做（§14.6）——见 §12「P6 实施记录」。
> 协调节点 Web 界面不做（只保留 CLI）；中转流量统计移到 **P7**（规划待批准）。
> **所有需要人工验证的项目集中在 §14**（环境搭建、逐项步骤与判据、验证记录表）。
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
| **工作量粗估** | P1～P4（能在页面上远程控制）约 **3～4 周**；P5 已废弃（§12），P6 视需要。 |

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
>
> **2026-10-07 P6 实施时的两处修正**（细节见 §12「P6-0」）：第 3 步的信令改走**中转路径上的 Peer gRPC**（`Peer.Punch`），
> 不经协调节点的 `Session`——协调节点因此零改动，会话密钥它也看不到；第 5 步改为**发起方 Dial、应答方 Listen**，不按节点 ID 字典序。
> 下文保留原方案作为档案。

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

结论：**连接层与 `Peer` 服务一开始就是 gRPC**，业务先借隧道复用现成路由；等到要做跨机编排，再为**少数编排所需的操作**加类型化方法，而不是给所有 API 补 proto（P5 跨机编排已于 2026-10-07 废弃，见 §12）。

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

#### P1 细化（2026-10-04；2026-10-05 并入 STUN 端点；已实施）

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
- [ ] 真机验收：**§14.2 V1-1～V1-9**（双向经中转 Hello、协调节点停机恢复、错误密钥被拒、拉黑、同一身份两处、第三方 STUN 客户端、
      `stun probe` 与 NAT 实测、`stun_addrs` 下发、协调节点作为 Linux 服务）。原先列在这里的几条已并入 §14，步骤与判据以那里为准。

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
- **验收**：P2-7 的自动化用例 + **§14.3 V2-1～V2-7** 的人工验证（同一内网走直连、断开落到中转、恢复后升级、拨到别的机器不串线、无协调节点模式、公网直连、端口被占、防火墙提示）。

#### P2 细化（2026-10-05；已实施，偏差见「P2～P4 实施记录」）

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
- **验收**：P3-10 的自动化用例（安全用例单独成组，作为回归守卫）+ **§14.4 V3-1～V3-9** 的人工验证
  （用 curl 经 A 的 `/api/peers/<B>/fwd/...` 完成 B 实例的启动/停止、日志 SSE、WS 事件、RCON；B 关着鉴权时 `operator` 授权的 A
  仍然调不了 `RequireAdmin` 路由；远程访问 `/api/users`、`/api/peers` 一律 403；撤销立即生效等）。

#### P3 细化（2026-10-05；已实施，偏差见「P2～P4 实施记录」）

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

> 2026-10-07 起，配置与启停相关的几行已按「P4 后续调整」更新（协调节点与启停解耦）。

| 方法 | 路径 | 内容 |
|---|---|---|
| GET | `/api/mesh/status` | 已有；加 P2-6 的字段与 `control_role`；`coordinator_id`（协调节点证书指纹，2026-10-07） |
| PUT | `/api/mesh/config` | 备注名、Peer 端口、是否监听、公网地址、`control_role`；**只改请求体里出现的字段**；**mesh 正在运行时**才热应用（`Manager.Reload` = Stop + Start），停止时只保存；响应带 `applied` |
| POST | `/api/mesh/join` | 保存 / 替换协调节点（`mesh.SetCoordinator`），**不改启用开关**；同上，运行中才热应用、响应带 `applied`。（CLI `mesh join` 仍是「写协调节点 + 启用」） |
| POST | `/api/mesh/join/preview` | 只解析 join blob：`{addr, coordinator_id, network_id, has_secret}`，不写盘、**不回传网络密钥** |
| POST | `/api/mesh/enable` / `/api/mesh/disable` | 启动 / 停止（持久化启用开关）。页面顶部的「启动」「停止」 |
| POST | `/api/mesh/restart` | 重启（`Reload`）；未启动时 400 |
| POST | `/api/mesh/leave` | 清协调节点并停用（同 CLI `mesh leave`）。页面上不再提供，只给 CLI / 脚本用 |
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
| **安全（回归守卫，单独成组）** | 未授权身份调 `HTTP` ⇒ `PermissionDenied`；B 关鉴权 + 授予 `operator` ⇒ `RequireAdmin` 路由 403；`/api/users`、`/api/auth/audit`、`/api/mesh/status`、`/api/peers/x/fwd/...` 经隧道一律 403；A 伪造 `Cookie` / `X-Forwarded-For` / `Authorization` 在 B 侧不生效（B 看到的头里没有它们）；`RemoteAddr` 不是回环（`IsLoopbackRequest` 为 false）；撤销后在途 SSE 立即结束；A 侧 B 回 401 被改写为 403；A 侧 WS 跨源 `Origin` 被拒；`Set-Cookie` 不透传 |
| `authapi` | 隧道请求在 `auth.enabled=false` 与 `true` 两种配置下的放行表；`ActorName` = `peer:…` |

### P4 — 前端
- [ ] 「远程管理器」页、顶栏机器选择器、API 前缀集中切换、版本不一致提示、远程上下文隐藏禁区页面。
- **验收**：**§14.5 V4-1～V4-4** 的浏览器人工验收（远程管理器页、切换机器、所有现有页面在远程上下文逐页走查、异常与提示）。

#### P4 细化（2026-10-05；已实施，偏差见「P2～P4 实施记录」）

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

照 `FRPManager.vue` 的表单形态。**页面顶部**：状态标签（只看 `running`：运行中 / 已停止 / 启动失败）与「启动 / 停止 / 重启 / 刷新」按钮
（2026-10-07 调整，见「P4 后续调整」——协调节点不是 mesh 是否启动的条件）。下面分五块：
1. **本机**：节点 ID（可复制）、Peer 端口监听状态、直连地址、版本；备注名 / 端口 / 公网地址 / `control_role` 表单（运行中保存即热应用，
   停止时提示「已保存，启动后生效」）。Windows 首次监听会弹防火墙提示，这里写明；A 关着鉴权时显示警告（§6.3）。
2. **协调节点**：已保存的地址、证书指纹、网络 ID、连接状态 / 出口地址 / STUN 地址（mesh 未启动时只显示「管理器互控未启动」）；未设置时
   「只按对端的手填地址直连」。粘贴 join blob 后防抖预览解析结果（已设置时逐项「当前 → 新」），按钮只有「保存」（未设置）/「替换」（已设置，确认框），
   **没有「断开接入」**，保存不会启动 mesh。
3. **我能控制的机器**：表格（备注名、节点 ID 短格式、在线、路径、RTT、对方版本、对方授予我的角色）、「切换过去」、改备注 / 手填地址、删除；
   「添加」= 粘贴邀请码，或输入对方节点 ID 发起申请（之后显示「等待对方批准」，可手动刷新）。
4. **能控制本机的机器**：已授权的对端（授予的角色可改、撤销）；待批准申请（选角色批准 / 拒绝）。
5. **邀请码**：生成（角色、有效期、是否附带本机直连地址）——生成后的整串只显示这一次；未用邀请列表与作废。

`App.vue` 三处联动：菜单项、`watch(route.path)` 高亮、`handleMenuClick` 分支；路由 `meta` 标记 `localOnly`（P4-3 的守卫据此判断）。

##### P4-5 人工验收

见 **§14.5**（远程管理器页、切换机器、逐页走查表、异常与提示）。

#### P2～P4 实施记录（2026-10-05，分支 `feat/remote-mesh`）

**状态：代码与自动化测试全部完成（Windows `-race` + WSL `-race` 均通过），真机二进制冒烟通过；§14 的人工验证待做。**

落地的文件：

| 位置 | 内容 |
|---|---|
| `api/asamesh/v1/peer.proto` | `Pair`、`HTTP` 两个方法与 `HTTPFrame` 信封（`request` / `response` / `body` / `end` / `raw`）、`PairStatus`；生成代码已更新 |
| `pkg/meshjoin/invite.go` | 邀请码 `asa-mesh-invite:v1:` 编解码，与 join blob 同构、同一套错误，密钥打码 |
| `internal/mesh/config.go` | `PeerPort` / `NoListen` / `PublicAddrs` / `ControlRole`；无协调节点模式；`SetEnabled`、`UpdateConfig`（`ConfigPatch`，只改出现的字段） |
| `internal/mesh/candidates.go` | 网卡枚举与过滤、`buildCandidates`、`isLANAddr` |
| `internal/mesh/creds.go` | `handshakenCreds`：dialer 已完成 mTLS，gRPC 只取 TLS 状态（P2-1） |
| `internal/mesh/path.go` | `planDirect`（排序）、`raceDial`（Happy Eyeballs）、`directProvider`、`relayProvider`（都返回已认证的连接） |
| `internal/mesh/peerconn.go` | `peerHandle`（引用计数、退役）、`acquire`、升级循环、`swapHandle`；重连退避封顶 5 秒 |
| `internal/mesh/peers.go` | `PeerStore`：`peers.json` 的读写（文件锁 + mtime 重载 + 授权变化回调）、邀请 / 申请 / 授权 / 撤销 |
| `internal/mesh/peerserver.go` | 拦截器按授权表放行、`Pair`（含限流）、`HTTP`、入站连接按身份登记（`connRegistry`） |
| `internal/mesh/tunnel_server.go` / `tunnel_client.go` | 隧道两端（B 侧 `ResponseWriter`：Flusher / Hijacker / CloseNotifier；A 侧 `RoundTripper`：普通响应与 101 升级） |
| `internal/mesh/identity_ctx.go` | `PeerIdentity` 与 context 存取 |
| `internal/mesh/manager.go` | Peer 端口监听、选路组装、`Reload`、`Hello` / `PairWithInvite` / `RequestPair` / `CreateInvite` / `Peers`、`SetHTTPHandler` |
| `internal/webapi/authapi` | `handlePeer`（禁区 → 合成用户 → 审计）、`RequireAdmin` / `IsAuthenticated` / `/api/auth/state` 的隧道分支；`internal/auth` 新增事件 `peer_request` |
| `internal/webapi/meshapi` | `/api/mesh/*` 全套 + `forward.go`（`/api/peers/:id/fwd/*path`） |
| `internal/webapi/actions.go` | `meshMgr.SetHTTPHandler(s.engine)` 在 `Start` 之前注入 |
| `internal/actions/mesh.go` | `enable` / `disable` / `peers` / `invite` / `revoke` / `approve`，`status` 补 Peer 端口等 |
| `app/src/utils/peerContext.js`、`apis/meshApi.js`、`components/MachineSwitcher.vue`、`components/RemoteBanner.vue`、`views/MeshManager.vue` | P4 前端；`http.js` / `utils.js` 接入 `peerPath`，`router` 的 `localOnly` 守卫，`App.vue` 三处联动 |

测试：`internal/mesh/direct_test.go`（P2-7 全部）、`internal/mesh/tunnel_test.go`（配对、隧道往返含 8 MiB、SSE 不攒批、WebSocket、
A 断开取消 B 的 handler、撤销切断在途流、未授权被拒）、`internal/webapi/authapi/peer_test.go`（禁区表、两种 `auth.enabled`、
`lan_bypass` 开到最宽也无效、审计、伪造请求头无效）、`internal/webapi/meshapi/meshapi_test.go`（A 侧真实转发入口：头清洗、
`Set-Cookie`、401 改写、WS 同源 / 跨源、撤销后 `peer_not_paired`、`control_role` 闸门）。`GOOS=linux CGO_ENABLED=0 go build ./...`
通过，协调节点的依赖守卫通过。

与上文细化的偏差（都是实现时发现的，按「为什么」记下）：

1. **授权变化只在「撤销或降级」时切断，且分两步**（P3-5 写的是「任何变化立即断开连接」）：立即取消该对端的全部隧道流（SSE / WS 当场结束，
   这是安全上要的那部分），**1 秒后**才关它的入站连接。原因是测试抓到的一个真问题：已配对的对端用新邀请把角色从 operator 升到 admin 时，
   授权变化回调立刻关掉了连接——**正在返回的那次 `Pair` 响应**也随之丢失，A 重试时邀请已被消费，于是配对实际成功却报「邀请码无效」。
   升级与新授权不需要断开任何东西；新的 RPC 一律由拦截器按新授权判断，不依赖断开。
2. **只读的三个接口（`status` / `peers` / `peers/:id/hello`）按 `control_role` 放行，不是一律 `RequireAdmin`**（P3-8 写的是全部管理员）：
   否则 `control_role: operator` 形同虚设——操作员连「能切到哪些机器」都看不到。改配置、配对、邀请、批准仍然只有管理员。
3. **`peers.json` 不存 `last_seen` / `last_addr`**：每条连接都写一次文件不值得，在线状态与路径取运行时的最近一次 Hello（`GET /api/mesh/peers`
   的 `last_hello` / `last_error`），重启后要重新检测一次。
4. **对端连接的重连退避封顶 5 秒**（新增）：gRPC 默认退避会涨到 120 秒，对端恢复后远程面板要干等两分钟。
5. **回环地址归为 `lan`**：上报的候选里没有回环，但手填的 `127.0.0.1:port`（同机两个管理器）应显示为内网直连。
6. **B 侧 `ResponseWriter` 实现了 `http.CloseNotifier`**（细化里没提）：Gin 的 `c.Stream` 会调它，不实现直接 panic——
   本项目所有日志 / 状态 SSE 都用 `c.Stream`。
7. **A 侧转发额外删掉 `Referer`**；`Origin` 按细化在校验后删除。WS 的同源判断复用 `realtime.WSUpgrader.CheckOrigin`（沿用它既有的规则，
   本次不改它）。
8. **`Status` 增加 `version`、`enabled`、`label`、`peer_port`、`no_listen`、`public_addrs`、`control_role`**：页面与提示条要用；
   版本用于远程上下文的「版本不同」提示。
9. **`mesh.SetGlobalManagerForTest`**：`meshapi` 的端到端测试要把包级单例指向测试里的 A，测试辅助只能导出（跨包）。
10. **前端**：选择器只在「本机 mesh 在运行且至少有一台对方授权了本机」时出现（远程上下文里始终出现）；「远程管理」菜单只对本机管理员
    （或本机关鉴权时）显示；菜单栏宽度 610px → 700px；提示条用 flex 布局，`content-wrapper` 从固定 `calc(100% - 58px)` 改为占满剩余高度。
11. 排除的虚拟网卡多了 `vEthernet (Default Switch`（Hyper-V 默认交换机，同属 NAT 内网）。

**真机冒烟（2026-10-05，Windows 本机，真实二进制，无协调节点模式，两个管理器）**：服务运行中用 CLI 生成带直连地址的邀请码 → A `POST /api/mesh/pair`
成功、路径 `lan`、同一邀请码二次使用被拒；经 `/api/peers/<B>/fwd` 访问 `/api/instances` 200、`/api/auth/state` 带 `peer:true`、
`/api/users` / `/api/mesh/status` / 多跳一律 403 `peer_forbidden`、operator 调管理员接口 403；WS 握手同源 101、跨源 403；
打开一条 all-info SSE 后用 CLI `mesh revoke`，流在几秒内结束，之后 403 `peer_not_paired`、Hello 为 `ROLE_NONE`；嵌入的 SPA 含新前端代码。
浏览器走查（§14.5）未做：本机 Chrome 没有开远程调试端口。
冒烟中顺带发现两件**既有行为**（与本功能无关，已写进 §14.1 的步骤）：`api` 在缺 SteamCMD / ARK 时拒绝启动（要 `--skip-env-check`）；
`api` 启动必拉起 Syncthing，它会用 UPnP 在路由器上开端口映射——验证环境用一个不存在的 `download.github_proxy` 让下载失败即可避开。

#### P4 后续调整（2026-10-07）：协调节点与 mesh 启停解耦

原来「远程管理器」页把协调节点当成 mesh 是否启动的条件：「接入」= 写协调节点**并启用**、「断开接入」= 清协调节点**并停用**，
没有协调节点时才出现「启用（无协调节点）/ 停用」。改为：

| 项 | 现在 |
|---|---|
| 启停 | 页面**顶部**「启动 / 停止 / 重启」，状态标签只看 `running`（运行中 / 已停止 / 启动失败）。启动 = `POST /api/mesh/enable`、停止 = `POST /api/mesh/disable`（都持久化启用开关，服务重启后保持）、重启 = 新增的 `POST /api/mesh/restart`（未启动时 400） |
| 协调节点 | 独立小节，只是一项配置：未设置时「保存」、已设置时「替换」（确认框）。页面上**不再有「断开接入」**；`/api/mesh/leave` 与 CLI `mesh leave` 保留（§14.4 的步骤还在用） |
| `POST /api/mesh/join` | 改用 `mesh.SetCoordinator`：只写协调节点、**不改启用开关**；CLI `mesh join` 仍用 `mesh.Join`（写协调节点 + 启用，headless 一条命令接入） |
| 保存后何时生效 | `/join` 与 `PUT /config` 都只在 mesh **正在运行**时热应用，响应多一个 `applied`（页面提示「已保存并应用」/「已保存，启动后生效」）——改配置不再是把 mesh 拉起来的理由 |
| 解析出的信息 | 新增 `POST /api/mesh/join/preview`（管理员）：只解析、不写盘，回 `{addr, coordinator_id, network_id, has_secret}`，**不回传网络密钥**。页面粘贴后防抖 300 ms 预览，已设置时逐项显示「当前 → 新」。`GET /api/mesh/status` 增加 `coordinator_id`（协调节点证书指纹），已保存的协调节点显示地址、指纹、网络 ID、连接、出口地址、STUN 地址 |

测试：`internal/mesh/mesh_test.go` 的 `TestSetCoordinatorKeepsEnabled`；`internal/webapi/meshapi/lifecycle_test.go`（停止状态保存不启动、
运行中替换热应用、未启动 restart 400、停止后改配置不启动、预览不含密钥且不写盘、截断串 400、三个新写接口对 operator 403）。
真实二进制按上表逐步调过一遍接口（含内嵌 SPA 里的新页面代码）；浏览器走查并入 §14.5 V4-1。

### P5 — 跨机编排（❌ 已废弃，2026-10-07）

> **废弃原因**：细化后发现对现有页面与逻辑的改动面太大——拦截器要改成按方法查角色、`batchmanage` 要加操作 ID / 新来源 /
> 按 ID 取消、新增 `internal/fleet` 包与 `/api/fleet/*`、远程禁区与前端本地前缀要同步扩充、再加一个新页面与菜单联动，
> 换来的「跨机总览 / 跨机批量」并不是刚需：逐台切换机器（P4）已经能完成同样的操作。**不实施**，下面的细化只作档案保留，
> 以后若重新评估，从这里起步。P6 不依赖 P5。

- [ ] ~~类型化方法：`Peer.Overview`（流式：对方所有实例状态 + 资源摘要）→ A 的总览页显示所有机器。~~
- [ ] ~~跨机批量启停：在 `batchmanage` 之上，每台机器各自走它本地的 `countdown`。~~

#### P5 细化（2026-10-07；未批准即废弃，仅存档）

**为什么是类型化方法，而不是经隧道调 B 的 REST**（§7.1 的原则在这里第一次兑现）：总览与跨机批量要由 **A 的 Go 后端**解析对方的回答——
B 的 REST 载荷不是版本化契约（`/api/instances` 改个字段，编排就静默错了），而且量也不对：`/api/instances` 每个实例带 200 条状态历史与整份配置，
总览只要其中三四个字段。所以 P5 只加**两组**类型化方法（总览、批量），「打开那台机器操作」仍然走隧道，不给任何其他 API 补 proto。

##### P5-1 proto 增量（`api/asamesh/v1/peer.proto`）

```proto
// 只对已授权的身份开放（角色要求见 P5-2）。B 未注入后端时回 Unimplemented，也不声明 fleet.v1。
rpc Overview(OverviewRequest) returns (stream OverviewSnapshot);
rpc StartBatch(StartBatchRequest) returns (StartBatchResponse);
rpc CancelBatch(CancelBatchRequest) returns (CancelBatchResponse);

message OverviewRequest { uint32 interval_seconds = 1; }        // 夹到 [2, 30]，0 = 5
message OverviewSnapshot {
  int64 timestamp = 1;
  HostSummary host = 2;
  repeated InstanceSummary instances = 3;   // 全部实例（含已停止的），按名字排序
  BatchSummary batch = 4;                   // 没有进行中的批量 = 不填
}
message HostSummary { uint32 cpu_cores = 1; double cpu_percent = 2; uint64 mem_used = 3; uint64 mem_total = 4;
                      double net_recv_bps = 5; double net_sent_bps = 6; }
message InstanceSummary { string name = 1; string status = 2; bool running = 3; string map = 4;
                          double cpu_percent = 5;    // 占整机的百分比（= all-info 的 cpu_total_percent）
                          uint64 mem_used = 6; string asa_version = 7; }
message BatchSummary { string id = 1; string type = 2; string origin_kind = 3; string origin_label = 4;
                       uint32 done = 5; uint32 total = 6; repeated BatchInstance instances = 7; }
message BatchInstance { string name = 1; string status = 2; string error = 3; }

message StartBatchRequest {
  string type = 1;                 // start | stop | restart
  repeated string instances = 2;   // 空 = 对方的全部实例（同 batchmanage）
  uint32 delay_seconds = 3;        // 对方机器内部、实例之间的间隔
  CountdownSpec countdown = 4;     // 不填 = 不倒计时；start 时被忽略（同 batchmanage）
  string remote_user = 5;          // A 上发起者的用户名，只用于审计与来源标签
}
message CountdownSpec { uint32 seconds = 1; repeated uint32 notify_points = 2; string notify_message = 3; string notify_command = 4; }
message StartBatchResponse { string op_id = 1; uint32 total = 2; uint32 eligible = 3; repeated BatchInstance skipped = 4; }
message CancelBatchRequest { string op_id = 1; }
message CancelBatchResponse { bool cancelled = 1; }   // false = 那一轮已结束或不是它（不报错）
```

- `status` 用字符串而不是枚举：取值就是 `state.InstanceStatus` / `batchmanage.InstanceOpStatus`，**A 的后端不解释它们**（只看 `running`），
  前端本来就认识这些字符串；做成枚举等于每加一个实例状态都要改 proto。
- 错误码：已有批量在跑 / 没有可操作的实例 ⇒ `FailedPrecondition`（详情区分 `busy` / `no_instances`）；倒计时参数错 ⇒ `InvalidArgument`。
- 能力：新增 `CapFleet = "fleet.v1"`（总览与批量一起，不拆）。**只有注入了后端才声明**——A 据此在不调用的情况下就知道对方不支持。

##### P5-2 B 侧方法级角色表（拦截器的一处安全收紧）

现在的拦截器对已授权身份**放行一切方法**（P3-5），细分角色全靠 HTTP 鉴权中间件。类型化方法不经过那个中间件，所以拦截器改成按方法查最低角色：

| 方法 | 最低角色 |
|---|---|
| `Hello`、`Pair` | 无（未授权也能调，同现状） |
| `HTTP` | `operator`（细分仍由 B 的 HTTP 中间件做） |
| `Overview`、`StartBatch`、`CancelBatch` | `operator`（与 REST 的 `/api/server/batch/*` 一致——那组路由没有 `RequireAdmin`） |
| **表里没有的方法** | **`admin`**（失败即关闭：以后再加类型化方法，忘了登记也不会默认对 operator 开放） |

##### P5-3 B 侧：后端注入（`mesh` 仍不 import 任何领域包）

```go
// internal/mesh/fleet.go
type FleetBackend interface {
    Overview(ctx context.Context) (*meshpb.OverviewSnapshot, error)
    StartBatch(ctx context.Context, caller PeerIdentity, req *meshpb.StartBatchRequest) (*meshpb.StartBatchResponse, error)
    CancelBatch(ctx context.Context, caller PeerIdentity, opID string) (bool, error)
}
func (m *Manager) SetFleetBackend(b FleetBackend)   // 组合根注入，照 SetHTTPHandler
```

- `Overview` 流：按间隔取快照发出，**每帧都发**（几 KB，不做差量——差量要处理丢帧与重连补全，不值得）。
- **撤销立即生效**：把 `tunnelServer.track/cancelID` 的「按节点 ID 登记在途流」提成 `peerService` 共用的 `streamTracker`，
  `HTTP` 与 `Overview` 都登记——`onGrantsChanged` 不改，撤销或降级时总览流与隧道流一起结束。
- **审计**：`StartBatch` / `CancelBatch` 是写操作，由后端实现（P5-5 的 `fleet.Local`）记 `[mesh]` 日志，B 开着鉴权时另写一条
  `peer_request` 审计（`Actor` = `peer:<备注名>/<用户>`，`Detail` = `fleet start-batch restart 3 个实例 → op <id>`），与隧道请求的审计同形。
  `Overview` 只记 DEBUG。

##### P5-4 `batchmanage` 增量（小）

- `BatchOperation.ID`（随机 16 位十六进制）；`GET /api/server/batch/status` 增加 `id` 字段。
- **`CancelIfCurrent(id) bool`**：只有当前在跑的那一轮就是 `id` 时才取消。A 的「取消跨机批量」只能用它——
  否则 A 下发的那一轮在 B 上早已结束、B 的定时任务又开了一轮时，A 的取消会把 B 的定时重启打断。
- 新来源 `OriginFleet = "fleet"`。标签：A 本机那一轮 `跨机批量操作（<用户>）`；B 上那一轮 `来自「<A 备注名>」的跨机批量（<用户>）`——
  B 本地有人开着批量弹窗时，必须能一眼看出是谁在操作它的机器（`BatchOrigin` 存在的理由）。
- `Summary()`：把 `getBatchStatus` 里「数完成数」的逻辑提成方法，REST 与总览共用一份（`skip_requested` 不计入已完成的规则只写一处）。

##### P5-5 新包 `internal/fleet`（跨机编排）

分层：依赖 `mesh`、`batchmanage`、`countdown`、`process`、`state`、`config`、`instance`（ASA 版本）、`auth`（审计）、`pkg/serverinfo`；
被 `internal/webapi/fleetapi` 与组合根依赖。`mesh` 不认识它，它认识 `mesh`。

- **`fleet.Local`** 实现 `mesh.FleetBackend`，**同时**给 A 的总览出「本机」那一行——两边是同一个函数出的数据，不会出现「本机这么显示、远程那么显示」。
  - 快照：`procpkg.RunningInstances()` + `serverinfo.Snapshot()`（与 all-info 同源，P2 起就定下的规矩）+ `statepkg.GetInstanceStateOrDefault`
    + 地图名（`instance_config.ini`，按 mtime 缓存，不每帧读文件）+ ASA 版本（`asaversion` 已有缓存）。
  - `StartBatch` = `batchmanage.StartOperation(…, OriginFleet 的 B 侧标签)`；`CancelBatch` = `CancelIfCurrent`。
- **`fleet.Hub`**（A 侧）：
  - 对象 = 本机 + `peers.json` 里**授权了本机**的对端（`remote_role != ""`）。
  - **只在有人看的时候订阅**：第一个 SSE 订阅者到来时为每台对端开 `Overview` 流，最后一个离开 30 秒后才关（刷新页面不会让 N 条流断了重开）。
  - 每台机器的状态：`online` / `offline`（连不上，退避 1 秒起翻倍到 30 秒重连）/ `unsupported`（Hello 的能力列表里没有 `fleet.v1`，
    或调用回 `Unimplemented`——**不重试**，只在对端版本变化时重查）/ `not_permitted`（对方已撤销本机，`PermissionDenied`）。
    附带路径（`lan`/`public`/`relay`）、对方版本、最后更新时间。
  - 依赖一个窄接口（`Overview` 流、`StartBatch`、`CancelBatch`、对端列表）而不是 `*mesh.Manager`，单测用假的。
- **`fleet.Batch(ctx, req)`**：`targets: [{node_id: ""=本机 | 节点ID, instances: [...]}]` + 类型 + `delay_seconds` + 倒计时。
  - **并发下发**，每台机器各自 `StartOperation`、各自跑本地 `countdown`：倒计时时长相同，各台起跑相差一个 RTT，玩家看到的倒计时自然对齐，不需要对时。
  - 单台超时 15 秒；结果逐台给出：`ok`（`op_id`、`eligible`、`skipped`）或错误码 `busy` / `no_instances` / `unreachable` / `unsupported` / `not_permitted` / `invalid`。
  - **不做跨机原子性**（见下方 Q1）：部分成功如实报告；内存里记下最近一次跨机批量 `{fleet_id, 每台的 op_id}`，供取消与页面刷新后回显。
  - **`fleet.Cancel(fleetID)`**：对记下的每台 `op_id` 调 `CancelBatch`（本机走 `CancelIfCurrent`），逐台报告。

##### P5-6 A 侧 API `internal/webapi/fleetapi`

| 方法 | 路径 | 内容 |
|---|---|---|
| GET | `/api/fleet/overview` | SSE：Hub 的合并视图，任一台更新即推（合并节流到每秒最多一帧）；首帧立即给出（本机数据 + 各对端的当前状态） |
| POST | `/api/fleet/batch` | P5-5 的请求体 → `{fleet_id, results: [{node_id, ok, op_id, eligible, skipped, error, code}]}` |
| GET | `/api/fleet/batch` | 最近一次跨机批量及逐台结果（页面刷新后回显） |
| POST | `/api/fleet/batch/:id/cancel` | 逐台取消，返回逐台结果 |

- 全部挂 `requireControl()`（`control_role`，与 `/api/peers/:id/fwd` 同一道闸；从 `meshapi` 导出复用，不写第二份）。mesh 没在运行时总览只有本机一行。
- ⚠️ **`/api/fleet` 加进远程禁区**（`authapi.peerForbiddenPrefixes`）：否则 A 经隧道调 B 的 `/api/fleet/batch`，B 会**以 B 的授权**去操作 C——
  这就是类型化方法版的多跳。前端 `peerContext.js` 的 `LOCAL_PREFIXES` 同步加 `/api/fleet/`（总览页本身是 `localOnly`，这只是兜底）。

##### P5-7 前端

- 新页 `views/FleetOverview.vue`（路由 `/fleet`，`meta.localOnly`），菜单「机器总览」的显示条件与顶栏机器选择器相同（本机 mesh 在运行且至少一台对端授权了本机），
  再叠加本机的 `control_role`；`App.vue` 三处联动。
- 每台机器一张卡片：名字（本机 / 对端备注名）、路径徽标、对方版本（与本机不同时标黄）、CPU / 内存；实例表：名字、地图、状态标签、CPU、内存。
  `offline` / `unsupported` / `not_permitted` 的卡片置灰并写明原因；「切换到这台」按钮 = `switchToPeer`（P4 的整页重载）。
- 跨机批量：实例行与卡片标题都有复选框；工具栏「启动 / 停止 / 重启」⇒ 确认弹窗按机器分组列出目标，**离线、不支持、正有批量在跑的机器
  直接列为不可选并写明原因**（数据就是屏幕上的总览）；停止 / 重启复用 `CountdownOptions.vue`。提交后逐台显示结果，进度来自总览快照里的 `batch`；
  「取消本次跨机批量」按钮。单个实例的跳过不在这里做——切到那台机器、用它自己的批量弹窗。

##### P5-8 CLI

**不加**。CLI「不连协调节点、不连对端」的规则（P3-9）不为它破例；总览与跨机批量只在服务进程里有意义。

##### P5-9 测试

| 组 | 用例 |
|---|---|
| 角色表（安全，单独成组） | 未授权调 `Overview` / `StartBatch` ⇒ `PermissionDenied`；operator 能调这三个；**表外的方法（测试里注册一个假方法）对 operator 拒绝、对 admin 放行** |
| `mesh` | 未注入后端 ⇒ `Unimplemented` 且 Hello 不含 `fleet.v1`；撤销 ⇒ 在途 `Overview` 流立即结束；`StartBatch` 的 `remote_user` 与调用者身份到达后端 |
| `batchmanage` | `ID` 唯一；`CancelIfCurrent` 用别的 ID 不取消、用对的 ID 取消；`Summary` 与 REST 的计数一致；`OriginFleet` 标签 |
| `fleet` | Hub：无订阅者时不开流、最后一个离开后延迟关闭；`unsupported` 不重试；`not_permitted` / `offline` 的状态转换与重连。Batch：并发下发、逐台结果（一台 `busy`、一台 `unreachable`、一台成功）；取消只碰自己记下的 `op_id` |
| `authapi` | `/api/fleet`、`/api/fleet/batch` 在禁区表里 |
| `fleetapi` | `control_role` 闸门；SSE 首帧含本机；进程内 A、B 两个 Manager + 假后端的端到端（A 的总览里出现 B，A 下发的批量到达 B 的后端） |

##### P5-10 验收（未写进 §14：P5 已废弃）

T1 拓扑（同机三个管理器）：V5-1 总览显示三台、数据与各自页面一致；V5-2 停掉 B 的 mesh ⇒ B 卡片数秒内变离线、恢复后自动回来；
V5-3 B 撤销 A ⇒ 卡片变 `not_permitted`、B 侧日志显示总览流结束；V5-4 跨两台的重启带 60 秒倒计时 ⇒ 两台的 RCON 公告时间点一致，
B 的批量弹窗来源显示「来自「A」的跨机批量」；V5-5 A 取消 ⇒ 两台都取消；V5-6 B 正在跑定时批量时 A 下发 ⇒ B 报 `busy`、其余照常，
A 再点取消**不影响** B 的定时批量；V5-7 用 P4 版本的二进制当 C ⇒ C 显示「对方版本不支持」；V5-8 B 开鉴权 ⇒ 审计里有 `peer_request` 的
`fleet start-batch` 记录。T2 拓扑补一项：经中转的总览流在中转断开后自动恢复。

##### 待批准时确认的三点

| # | 问题 | 推荐 |
|---|---|---|
| Q1 | 跨机批量要不要「全有或全无」 | **不要**。`batchmanage` 没有「准备」阶段，已经开始的停服也撤不回来，真正的原子性做不到；能做的只是下发前再查一次——而这一次查询与下发之间照样有竞态。改为：确认弹窗用屏幕上的总览把离线 / 忙的机器排除掉（覆盖绝大多数情况），后端如实报告部分成功，一键取消已开始的 |
| Q2 | B 授予 `operator` 就能被跨机批量 | **是**。与 B 本地 operator 能用批量启停一致；B 若不想被批量操作，就不该授予任何角色 |
| Q3 | 总览里带不带在线玩家数 | **不带**。要对每个实例每轮发 RCON `ListPlayers`，在线实例多时是实打实的负载，而且 RCON 失败会让总览抖动；等真有需要（P6）再做成低频缓存 |

### P6 — 增强（视需要）
- [x] 打洞（按 §5.6 的方案：UDP socket + `quic.Transport`、用 `Registered.stun_addrs` 做地址发现、信令与探测、`punched` 路径）。
      **代码完成 2026-10-07**，真机验收见 §14.6。
      协调节点的 STUN 端点已在 P1-8 完成，P6 不需要升级协调节点。
- [ ] 反向直连（§5.3）：**已评估（2026-10-07，见下文「反向直连评估」）**——建议先只做「本机有公网地址但 UDP 未放行」的提示，等 P7 统计与真机打洞数据再决定是否实施。
- [ ] UPnP / NAT-PMP / PCP 端口映射（`mapped` 候选）：同上，打洞实测数据回填 §5.6.4 后再定。
- [ ] ~~协调节点 Web 界面~~ ❌ **不做**（2026-10-07）：协调节点只保留 CLI 操作（`asa-coordinator node …` / `join-blob` / `stun probe`）。
- [ ] ~~中转流量统计~~ → **移到 P7**（2026-10-07，规划见下文「P7」）。
- [ ] 若要把协调节点借给别人用：运营者的网络管理 CLI 与每网络配额（§8.4.4 的 C）。
- [ ] 若要 IP 级互通再评估 tsnet（§3.3）。

#### P6 细化：打洞（2026-10-07 批准，四点均按推荐）

本轮只做上面第一项「打洞」；另外两项保持未勾选，不在本轮范围（见文末 Q3）。
**目标**：两台都在 NAT 后、TCP 直连不通的管理器，在中转连上之后几秒内自动升级为 `punched` 路径；打不通就留在中转，用户不感知、不等待。
**协调节点零改动**（不换二进制、不改配置），理由见 P6-0。

##### P6-0 对 §5.6.3 的两处修正（先读这里）

| # | §5.6.3 原方案 | 本细化 | 理由 |
|---|---|---|---|
| 1 | 第 3 步：信令经协调节点的 `Session` 转发（`PunchOffer` → `PunchAnswer` → `PunchStart`），协调节点发会话密钥、定开始时刻 | 信令走**已经连上的中转路径上的 Peer gRPC**：新增 `Peer.Punch`，A 在请求里带自己的候选与一把随机会话密钥，B 在响应里带自己的候选 | ①协调节点一行不改——它的 `handleNodeMessage` 现在丢弃 `PunchOffer`，按原方案就得升级每个协调节点，与 §5.6 开头「P6 只升级管理器」的承诺相违；②会话密钥在端到端 mTLS 里传，协调节点**根本看不到**，比原方案还少一个知情方；③打洞本来就排在中转之后（§5.6.3 的「用户感知」），信令借中转连接是零额外等待。代价：无协调节点模式不能打洞——但那种模式本来也没有 STUN，无从打洞。`coordinator.proto` 里的 `PunchOffer`/`PunchAnswer`/`PunchStart` 及其字段号**保留不用**（永不复用） |
| 2 | 第 5 步：节点 ID 字典序小的一方 `Dial` QUIC，另一方 `Listen` | **发起方（A，要控制 B 的一方）`Dial`，应答方（B）`Listen`** | 原规则是为对称的「双方都可能先发现对方」设计的；这里会话天然有方向（A 发起 `Punch`、A 是之后 gRPC 的客户端），按方向定角色同样不会双方同时握手，而且让 QUIC 客户端 = 内层 TLS 客户端 = gRPC 客户端，三层方向一致，不用额外约定谁开流 |

其余照 §5.6：同一个 UDP socket 承载 STUN、探测与 QUIC；探测包带 HMAC；QUIC 握手钉 SPKI；QUIC 上开一条流包成 `net.Conn`，
之上照旧端到端 TLS + gRPC（TLS 套 TLS，上层零差别）；打不通回中转、按路径升级节奏重打。

##### P6-1 UDP socket 与 `quic.Transport`（新文件 `internal/mesh/udp.go`）

- `quic-go`（现为经 frp 的间接依赖 v0.62.0）改为**直接依赖**，版本不动。协调节点不引入它（`meshcoord/deps_test.go` 的守卫不变）。
- `Start` 时（有协调节点且未关打洞）绑一个 UDP socket：`udp_port`，**0 = 与 Peer 端口同号**（默认 19194/udp）；双栈（`"udp"` + 空主机）。
  绑不上**不报错退出**：退到系统分配的随机端口（打洞照样能用——反射地址是 STUN 问出来的，host 候选按实际端口填），
  只在状态里记一条 `udp_error`。`no_punch: true` 时不开这个 socket。
- socket 包成**一个**进程内唯一的 `quic.Transport`，`Stop` 时 `Close`（顺带关掉其上所有 QUIC 连接）。
- **非 QUIC 包分发器**（`udpDemux`）：一个 goroutine 循环 `ReadNonQUICPacket`，按内容分给两类等待者——
  STUN 回包（魔数 `0x2112A442`）按事务 ID 交给等它的查询；探测包（首字节 `0x2A`，见 P6-4）按会话 ID 交给打洞会话；其余丢弃。
  ⚠️ `quic-go` 的非 QUIC 包队列**只有 32 个、满了就丢**（`maxQueuedNonQUICPackets`），分发器绝不能在回调里阻塞：
  投递一律 `select … default`，等待者自己带缓冲。

##### P6-2 地址发现与 NAT 判型（`internal/mesh/natprobe.go`）

- 输入：`Registered.stun_addrs`（P1-8 已下发，主机名在管理器侧解析）。拿到第一个 `Registered` 后立刻测一次，之后每 **5 分钟**、
  以及 `reportCandidates` 发现网卡地址变化时各测一次；协调节点重连后（新的 `Registered`）也测一次。
- 做法：经 `Transport.WriteTo` 向两个 STUN 端口各发 `stun.NewBindingRequest()`，回包经分发器按事务 ID 回到 `stun.ParseBindingResponse`；
  重传沿用 `stun.Query` 的 500ms / 1s / 2s 节奏（这里不能直接用 `Query`——它要独占读 socket）。
  结果交给已有的 `stun.ClassifyMapping`（local = 各网卡地址 + 实际 UDP 端口）。
- 产物（`natState`，锁保护）：`srflx`（反射地址，0～2 个，去重）、`mapping`（`stun.Mapping`）、`checked_at`、`error`。
  测不到（UDP 被封、STUN 没开）⇒ `mapping = unknown`、没有 srflx——**照样尝试打洞**（host 候选在 IPv6 下经常就够）。
- 只问 STUN 拿到的地址族：协调节点只解析出 IPv4 时只有 IPv4 的 srflx。IPv6 不需要 srflx：全局 IPv6 的 host 候选本身就是公网地址。

##### P6-3 信令：`Peer.Punch`（`api/asamesh/v1/peer.proto` 增量，`buf generate`）

```proto
service Peer {
  ...
  // 打洞信令（§12 P6）。只在中转路径上调用：A 带上自己的候选与会话密钥，B 回自己的候选并立即开始探测。
  // 要求调用者已被授权（不在未授权白名单里）。本机关了打洞时返回 FailedPrecondition。
  rpc Punch(PunchRequest) returns (PunchResponse);
}
message PunchRequest {
  bytes session_id = 1;          // 16 字节随机
  bytes key = 2;                 // 32 字节随机，探测包 HMAC 用；只在端到端 mTLS 里传
  repeated Candidate candidates = 3;  // 只有 UDP：HOST + SRFLX
  NATMapping mapping = 4;
}
message PunchResponse {
  repeated Candidate candidates = 1;
  NATMapping mapping = 2;
}
enum NATMapping { NAT_MAPPING_UNKNOWN = 0; NAT_MAPPING_NONE = 1; NAT_MAPPING_EASY = 2; NAT_MAPPING_HARD = 3; }
```

- `Candidate` 复用 `coordinator.proto` 里的（同一个 proto 包，`import`），`transport = UDP`，`kind = HOST / SRFLX`。
  每侧候选上限 **8 个**（IPv4 在前，超出截断），探测流量因此有上界。
- 能力：`Capabilities()` 增加 `CapPunch = "punch.v1"`。A 发起前看该对端最近一次 `Hello` 的能力列表：没有 `punch.v1`（旧版本）就**不调**；
  调了却得到 `Unimplemented` / `FailedPrecondition`（对方关了打洞）⇒ 这个 handle 不再尝试打洞，只留 TCP 直连的升级重试。
- 授权：`Punch` 不进 `unpairedMethods`，现有拦截器自动要求「B 已授予 A 角色」——与 `HTTP` 相同，未配对的人摸不到 B 的 UDP 端口信息。
- **双方都是 `HARD`（对称型）⇒ 不打**：A 在调用前若自己是 HARD 且 B 最近一次的 mapping 也是 HARD（B 的 mapping 从上次 `Punch` 响应缓存），
  直接跳过并记一条「双方都是对称型 NAT，只能中转」（§5.6.4 的结论，省掉注定失败的 5 秒探测）。只有一方 HARD 照样试。

##### P6-4 探测协议与打洞会话（`internal/mesh/punch.go`）

- 探测包（固定 42 字节，首字节高两位为 0，`quic-go` 会把它交给 `ReadNonQUICPacket`）：

  | 偏移 | 长度 | 内容 |
  |---|---|---|
  | 0 | 1 | `0x2A`（版本 / 魔数） |
  | 1 | 1 | 类型：`1` = probe，`2` = ack |
  | 2 | 16 | 会话 ID |
  | 18 | 8 | 序号（大端） |
  | 26 | 16 | HMAC-SHA256(key, 前 26 字节) 截断 |

  HMAC 只防「别人伪造探测把选路引到错误地址」；身份认证仍靠之后的 QUIC 握手钉 SPKI（§5.6.3 第 4 步）。
- 会话（每次 `Punch` 一个，`punchSession`）：双方各自从「拿到对方候选」那一刻起，每 **50ms** 向对方**每个**候选发一个 probe，最长 **5 秒**
  （上界：8 候选 × 20 包/秒 × 5 秒 = 800 个小包）。B 在 `Punch` 处理函数里**先**起探测 goroutine 再返回响应，A 收到响应后起探测——
  两边相差一个中转 RTT 的一半，远小于 5 秒窗口，不需要协调节点下发开始时刻。
- 收到 HMAC 有效的 probe ⇒ 回一个 ack 给**来源地址**（不是候选表里的地址——经过 NAT 时两者不同），并把来源地址记为「已验证」。
  收到有效的 ack 同样记为已验证。HMAC 无效、会话 ID 未知的包静默丢弃（不回包，不当反射器）。
- A 拿到第一个已验证地址 ⇒ 停止探测、发起 QUIC（P6-5）；失败则换下一个已验证地址，直到窗口结束。
  B 持续探测 + 回 ack，直到 A 的 QUIC 连接被接受（P6-5 的准入）或窗口结束；会话在窗口结束后再保留 **10 秒**供 QUIC 握手完成，然后作废。
- 同一对端同时至多一个打洞会话（A 侧按 peer、B 侧按调用者 ID 去重；B 收到新的 `Punch` 时作废旧会话）。

##### P6-5 QUIC 路径（`internal/mesh/quicpath.go`）

- QUIC 配置：ALPN `asa-mesh/1`；`HandshakeIdleTimeout` 5 秒；`KeepAlivePeriod` **15 秒**、`MaxIdleTimeout` 45 秒（§5.6.3 第 7 步：
  NAT 的 UDP 映射空闲超时常见 30 秒起）；每条 QUIC 连接只开**一条**流。
- **A（Dial）**：`Transport.Dial(已验证地址, meshid.ClientConfig(cert, B) + ALPN)` ⇒ 钉 B 的公钥 ⇒ `OpenStreamSync` ⇒ 包成 `net.Conn`
  ⇒ `clientTLS`（与直连 / 中转完全相同的内层握手）⇒ `pathConn{kind: PathPunched}`。
- **B（Listen）**：`Transport.Listen(meshid.AnyClientServerConfig(cert) + ALPN)`，跑在 `Start` 起的 goroutine 里。
  **准入**：握手完成后从 `ConnectionState().TLS` 取出客户端节点 ID，**必须有一个未作废的、发起方是它的打洞会话**，否则 `CloseWithError` 拒绝——
  UDP 端口对全网可见，不能让任何人都在上面建 QUIC 连接；内层 TLS + 拦截器本来也会挡，这里是提前、更省地挡。
  准入后 `AcceptStream`，包成 `net.Conn` 交给 Peer gRPC 服务——复用现在的 `relayListener.deliver`（改名为 `injectListener`，
  Addr 不再写死 "relay"），撤销授权时 `connRegistry` 照旧能按 ID 断开它。
- 流 → `net.Conn` 的包装（`quicConn`）：`Read/Write/SetDeadline` 直接走 `*quic.Stream`，`Local/RemoteAddr` 取连接的，
  `Close` = 关流 + `CloseWithError(0)` 关整条 QUIC 连接（一条连接只有这一条流，生命周期一致）。

##### P6-6 接入路径升级（改 `peerconn.go` 的 `upgradeLoop`）

- 优先级（§5.3、§5.6.3）：直连-内网 > 直连-公网 > **打洞** > 中转。`dialPeer` 的 provider 列表**不变**（打洞依赖中转连接做信令，
  不能作为首次拨号的路径）；打洞只从升级循环里进来。
- 走中转的 handle：升级循环的**第一次尝试立即进行**（不再先等 `UpgradeMin`），顺序是「TCP 直连 → 打洞」；之后照旧 1 分钟起、翻倍到 10 分钟。
  打洞那一步：等中转连接 READY（`WaitForReady` + 5 秒上限）→ 在**这个中转 handle 上**调 `Punch` → 探测 → QUIC → 成功就 `swapHandle`
  （旧的中转 handle 退役，在途流自然结束，P2-5 的机制原样复用）。
- 走打洞的 handle：升级循环继续，但只试 TCP 直连（打洞不比自己更好）。
- 打洞连接断了（QUIC 空闲超时、对方 NAT 映射被回收）⇒ gRPC 重连走 `dialPeer` ⇒ 回到中转 ⇒ 新的中转 handle 立即再打一次——
  **连续失败**时按升级循环的退避节奏，不会形成「打通—断—打通」的快速循环（同一对端两次打洞间隔下限 30 秒）。
- 每个对端记最近一次打洞结果（时间、成功 / 原因：对方旧版本、对方关了打洞、双方对称型、探测超时、QUIC 握手失败），进 `PeerView`。

##### P6-7 配置、状态、API 与页面

- `config.json` 增量：`udp_port`（int，0 = 同 Peer 端口）、`no_punch`（bool，默认 false = **默认开启打洞**，见 Q2）。
  `ConfigPatch` 与 `PUT /api/mesh/config` 同步增加这两个字段；校验同 `peer_port`。运行中修改照旧热应用（`Reload`）。
- `GET /api/mesh/status` 增加 `punch` 块：`{enabled, udp_addr, udp_error, mapping, srflx[], checked_at, error}`
  （`mapping` 用 `easy` / `hard` / `none` / `unknown`，文案在前端）。
- `GET /api/mesh/peers` 每行增加 `last_punch: {at, ok, reason}`（没打过为空）。`path` 的 `punched` 已在 P4 里有「打洞」文案。
- CLI：`asa-server mesh status` 打印 NAT 类型、反射地址、UDP 端口。
- 页面（`MeshManager.vue`，小改，不动结构）：
  - ②「协调节点」节（运行中且已连接时）增加两行：**NAT 类型**（`easy`=「易打洞」绿、`hard`=「对称型，难打洞」橙、`none`=「公网直达」、`unknown`=「未知」）
    与**反射地址**（srflx，mono）。
  - ③「本机设置」增加「UDP 端口」（placeholder「同 Peer 端口」）与「允许打洞」开关（附说明：「两台都在 NAT 后时尝试 UDP 打洞，打通后不再经中转」）。
  - 对端列表的路径标签旁，若最近一次打洞失败，tooltip 显示原因。
- Windows 防火墙：首次绑 UDP 端口时系统可能弹窗；不放行只影响「对方先到的探测被挡」，状态跟踪通常仍能打通。§14 里补一条验证，部署说明补一句。

##### P6-8 测试

| 层 | 用例 |
|---|---|
| 单元（`punch_test.go`） | 探测包编解码；错 key / 错会话 ID / 截断 / 首字节不对一律拒绝；ack 回到**来源地址**；候选截断到 8 个且 IPv4 在前；双方 HARD 跳过 |
| 单元（`udp_test.go`） | 分发器：STUN 回包按事务 ID 送达、探测按会话 ID 送达、未知包丢弃、等待者不读时分发器不阻塞 |
| 集成（`punch_e2e_test.go`，测试协调节点 + 两个 Manager，全回环） | ① B `no_listen`（TCP 直连必败）⇒ 先中转、**几秒内**升级为 `punched`，`Hello` 走打洞；② 两侧 UDP 套一层模拟「地址受限锥形 NAT」的 `PacketConn` 包装（只放行自己发过包的对端地址）⇒ 仍能打通，**证明探测确实在开洞**；③ B 侧包装丢弃全部入站 UDP ⇒ 留在中转、`last_punch` 记「探测超时」、不出现快速重试；④ B `no_punch` ⇒ `FailedPrecondition`、此 handle 不再打洞；⑤ 打洞路径上撤销 A 的授权 ⇒ 隧道流被取消、连接断开（P3-5 的语义在新路径上成立）；⑥ 用第三把身份直接对 B 的 UDP 端口发起 QUIC ⇒ 被准入拒绝；⑦ 打洞连接被强制关闭 ⇒ 回到中转后能再次打通（间隔受 30 秒下限约束，测试里调小） |
| 地址发现 | 对测试协调节点的 STUN（`meshcoord` 已有）问到反射地址；回环下判型为 `none` |

测试用的钩子照现有 `wrapListener` / `localCandidates` 的写法加在 `Options` 的非导出字段里：`wrapPacketConn`、`localUDPCandidates`、`punchMinInterval`。
命令：`go test -race ./internal/mesh/ ./internal/webapi/meshapi/ ./internal/meshcoord/ ./pkg/stun/`（Windows 用 PowerShell），WSL 同样跑一遍。
`quic-go` 在非 `*net.UDPConn` 的 `PacketConn` 上会关掉 GSO/ECN 等优化，只影响测试里的包装形态，不影响正确性。

##### P6-9 验收（追加到 §14，新增「14.6 P6：打洞」，原 14.6 顺延）

| # | 前置 | 操作 | 期望 |
|---|---|---|---|
| V6-1 | T1 两个管理器，B `no_listen` | A 打开 B 的远程面板 | 先显示「中转」，**10 秒内**变「打洞」；B 的日志有「准入 QUIC 连接」，A 的日志有「从中转升级为打洞」 |
| V6-2 | 两台真机分处两个家宽 NAT（TCP 直连不通） | 同上 | 「打洞」；页面 NAT 类型显示「易打洞」；连续开着日志 SSE **30 分钟**不断（保活有效） |
| V6-3 | 一方用手机 4G 热点（多为对称型） | 同上 | 另一方是易打洞时多数能通；两方都是对称型时直接留在中转，对端行的 tooltip 是「双方都是对称型 NAT」，**不出现** 5 秒探测 |
| V6-4 | 两边任一方有公网 IPv6 | 同上 | 「打洞」，A 日志里胜出的是 IPv6 地址 |
| V6-5 | V6-2 的状态 | B 侧拔网线 1 分钟再插回 | A 回到中转、恢复后自动再次打通；期间远程面板可用 |
| V6-6 | 任意 | 本机设置关掉「允许打洞」并保存（运行中） | 立即生效：UDP 端口不再监听（`netstat`），对端连本机只走 TCP 直连或中转 |
| V6-7 | Windows 首次运行 | 启动 mesh | 若弹出防火墙提示，「取消」后 V6-2 仍能打通（靠出站状态跟踪）；结论记进 §14 验证记录 |
| V6-8 | 中转 + 打洞并存的切换期 | A 正在看 B 的日志 SSE 时完成升级 | SSE 不断（在途流留在旧中转连接上直到自然结束），新请求走打洞 |

结论（各运营商 / 路由器的 NAT 类型与成败）回填 §5.6.4，作为第一批实测数据。

##### P6-10 文件清单

新增：`internal/mesh/{udp,natprobe,punch,quicpath}.go` 及对应测试；改：`api/asamesh/v1/peer.proto`（+ 生成物）、
`internal/mesh/{manager,peerconn,peerserver,listener,config,coordclient}.go`、`internal/webapi/meshapi/meshapi.go`（只透传新字段）、
`cmd` 里 `mesh status` 的输出、`app/src/views/MeshManager.vue`、`go.mod`（quic-go 转直接依赖）、本文档、根 `CLAUDE.md` 的 mesh 条目。
**不改**：`internal/meshcoord`、`cmd/asa-coordinator`、`coordinator.proto`、`pkg/stun`。

##### 批准时确认的四点（2026-10-07：全部按推荐）

| # | 问题 | 推荐 |
|---|---|---|
| Q1 | 信令走 Peer gRPC（经中转）而不是协调节点的 Session | **是**（P6-0 #1）：协调节点零改动、会话密钥协调节点不可见；无协调节点模式本来就没法打洞 |
| Q2 | 打洞默认开还是关 | **默认开**（`no_punch` 关闭）。新开的只是一个 UDP 端口：探测只回应 HMAC 正确的包，QUIC 只接受有打洞会话的已授权对端；默认关等于没人会用上 |
| Q3 | 本轮范围 | **只做打洞**。UPnP / NAT-PMP / PCP 端口映射（`mapped` 候选）、反向直连、协调节点 Web 界面、中转流量统计、运营者 CLI 都留在 P6 清单上，不在本轮 |
| Q4 | 内层是否继续套端到端 TLS（QUIC 层已经钉了 SPKI） | **继续套**：上层（`handshakenCreds`、`connRegistry`、拦截器）只认 `*tls.Conn`，零改动；控制面流量下双层加密的开销可以忽略。以后要优化再信任 QUIC 层的 TLS |

#### P6 实施记录（2026-10-07，分支 `feat/remote-mesh`，未提交）

代码完成；自动化测试两个平台通过（Windows `-race`、WSL `-race`，打洞用例连跑 5 次无抖动）；真实二进制回环冒烟通过；
**真机（两个家宽 NAT、4G 热点、IPv6）验收待做**（§14.6）。

**文件**：新增 `internal/mesh/{udp,natprobe,punch,quicpath}.go` 与 `punch_test.go`、`punch_e2e_test.go`；改 `api/asamesh/v1/peer.proto`
（`Punch`、`NATMapping`，生成物同步）、`internal/mesh/{manager,peerconn,peerserver,coordclient,listener,config}.go`
（`relayListener` 改名 `injectListener`）、`internal/actions/mesh.go`（`mesh status` 打印打洞行）、`internal/webapi/meshapi/lifecycle_test.go`
（`TestPunchConfig`）、`app/src/views/MeshManager.vue`、`app/src/apis/meshApi.js`（注释）、`go.mod`（quic-go 转直接依赖，版本不动）。
`internal/meshcoord`、`cmd/asa-coordinator`、`coordinator.proto`、`pkg/stun` 均未改。

**与细化的偏差 / 补充**：

| # | 细化 | 实际 | 原因 |
|---|---|---|---|
| 1 | P6-3 候选「IPv4 在前」 | 反射地址 → 全局 IPv6 host → 私网 host（含 ULA） | 跨 NAT 最可能通的是反射地址；全局 IPv6 本身就是公网地址；私网地址只在同一内网有用，而同一内网时 TCP 直连通常已经赢了。截断到 8 个时先丢最没用的 |
| 2 | P6-6 中转 handle 第一轮立即打洞 | 另加前提：**只对已知授权了本机的对端打**（`peers.json` 里 `remote_role` 非空）；得知授权（Hello / 配对的回答带角色）时 `wakeUpgrade` 叫醒升级循环 | 第一条中转连接几乎总是在**配对**——此时调 `Punch` 必然 `PermissionDenied`，还白占一次最小间隔，下一轮要等 `UpgradeMin`（1 分钟）。端到端用例最初正是这样全部失败的 |
| 3 | P6-6「打洞连接断了 ⇒ 新的中转 handle 立即再打」 | 重连发生在**同一个 handle** 上（gRPC 再调 dialer、落到中转）；升级循环还在跑时由 `startUpgrade` 经 `wake` 叫醒，立即打洞，仍受 30 秒下限 | handle 的生命周期比连接长，没有「新 handle」 |
| 4 | （未写） | **Stop 时先逐条 `CloseWithError` 关 QUIC 连接，再关 Transport**（`puncher.close`，连接登记在 `puncher.conns`） | `quic.Transport.Close` 直接丢弃连接、**不发 CONNECTION_CLOSE**：对方要等 45 秒空闲超时才发现，这期间它到本机的请求全部失败、也不回落中转。冒烟实测 B 重启后 A 46 秒才恢复，修正后 0 秒。回归用例 `TestPunchPeerRestartRecoversQuickly`（已确认去掉修正时它失败） |
| 5 | P6-8 ③ 期望「探测超时」 | 原因是「探测超时」或「QUIC 握手失败」 | 只挡**入站**时 B 的探测照样发得出去，A 认为地址可达、失败在 QUIC 握手——单向 UDP 的真实样子 |
| 6 | P6-7 `punch.enabled` | `punch.active`（运行中且 UDP 就绪）；配置开关回显在顶层 `no_punch` / `udp_port`（与 `no_listen` / `peer_port` 同层） | 页面的设置表单从顶层读配置，`punch` 块只放运行时状态 |
| 7 | P6-9 日志「准入 QUIC 连接」 | A：「到 X 的路径已从中转升级为打洞（地址）」；B：「接受来自 X 的打洞连接（地址）」；失败：「到 X 打洞未成功：原因，继续走中转」 | — |
| 8 | P6-7 CLI `mesh status` 打印 NAT 类型 | 只打印配置（开关、UDP 端口）；NAT 类型只有运行中的服务知道，见 `GET /api/mesh/status` | `mesh status` 本来就只读本地文件、不连服务 |

**冒烟**（真实二进制，Windows 单机回环：协调节点 + 两个 `api --tls=false`，两边 `no_listen`，UDP 19501 / 19502）：
- 两边 NAT 判型「易打洞」、反射地址 = `127.0.0.1:<UDP 端口>`；socket 是双栈（`[::]:19501`）。判不出「无 NAT」是因为真实枚举过滤掉了回环地址——只有回环上才这样。
- 配对后第一次 Hello 走中转，**1 秒内**变「打洞」，`last_punch` 记下对方地址。
- B 重启：A 立即回中转（偏差 #4 修正后），30 秒下限到后再次打通。
- B 运行中关打洞：热应用、UDP 关闭，A 立即回中转。

**测试**：`punch_test.go`（探测包编解码与拒绝、候选排序 / 截断、对方候选解析、分发器路由与不阻塞、会话 ack 回来源地址）；
`punch_e2e_test.go` 8 个（P6-8 ①～⑦ + B 重启后快速恢复）；`meshapi` 的 `TestPunchConfig`。
测试默认关打洞（`newManager` 不传 `withPunch` 时写 `no_punch`），打洞用例显式开启并在回环上起两个真 STUN 端口。

#### 反向直连评估（2026-10-07，只评估、未批准实施）

**是什么**：A 要控制 B（A 是 gRPC 客户端），但 B 在 NAT 后、A 连不进去，而 A 自己**能被 TCP 连到**（公网 IP / 端口映射 / 放行了的全局 IPv6）。
让 B 主动拨 A 的 Peer 端口，TCP 建好之后**角色翻转**：A 在这条连接上当 TLS 客户端与 gRPC 客户端，B 当服务端——
上层看到的与 A 主动直连完全一样。

##### 1. P6 之后它还能多覆盖什么

| A（控制方） | B（被控方） | P6 现状 | 加反向直连后 |
|---|---|---|---|
| 公网 IP，TCP 与 UDP 都放行 | 家宽 NAT / 4G | 打洞能通（A 无 NAT，B 的探测直接进来，A 回 ack 走 B 已开的映射） | 同样能通，**无增益** |
| 公网 IP，**只放行了 TCP**（云安全组 / 防火墙只开了 19194/tcp） | 任意 NAT | B 的探测被 A 的防火墙挡掉 ⇒ 中转 | ✅ 反向直连 |
| 家宽 + 路由器**只映射了 TCP**（`public_addrs`） | 对称型 / CGNAT | 视 A 的 NAT 类型而定，常落中转 | ✅ 反向直连 |
| 公网，UDP 放行 | B 所在网络**封 UDP / 限速 QUIC**（公司、酒店、部分校园网） | 中转 | ✅ 反向直连（这是唯一「用户自己没法通过放行端口解决」的场景） |
| NAT 后、无映射 | 任意 | 打洞或中转 | 无增益（A 连不到） |
| CGNAT / 4G | 任意 | 打洞或中转 | 无增益 |

结论：增益集中在 **「A 可被 TCP 连到，但 UDP 那条走不通」**。其中前两行用户自己就能解决（多放行一个 `19194/udp`），
真正只有反向直连能救的是第四行「B 的网络封 UDP」。本项目的典型部署里 A 多是管理员自己的家用电脑（多数在 NAT 后），
B 是游戏服主机——**A 可被连到的情况本身就不多**，所以预计覆盖面窄。

##### 2. 技术方案（如果做）

关键观察：不需要「TLS 套 TLS」，也不需要新的监听端口——**TCP 方向反过来，TLS 方向不变**。

1. **信令**：照 P6 的路子，在中转路径上新增 `Peer.Reverse(ReverseRequest{session_id, key, candidates})`，A 带上自己的 TCP 候选
   （网卡地址 + `public_addrs`，即现在 `buildCandidates` 的输出）。能力 `reverse.v1`。拦截器自动要求「B 授权了 A」。
2. **B 侧**：用现成的 `raceDial`（泛化成返回原始 `net.Conn`）拨 A 的候选；连上后先写一段**明文前导**
   `"AMR1" | session_id(16) | HMAC(key, …)(16)`，然后把这条**原始连接**交给本机 Peer gRPC 服务（`injectListener.deliver`）——
   服务端 TLS 由现有的 `limitedCreds` 完成，身份、拦截器、撤销登记全部照旧。B 侧几乎不用写新代码。
3. **A 侧**：Peer 端口前面加一个**分流 Listener**：读第一个字节——`0x16`（TLS ClientHello）⇒ 原样交给 gRPC（把读掉的字节回放）；
   前导魔数 ⇒ 读满前导、校验会话与 HMAC ⇒ 把连接交给正在等它的那次升级尝试 ⇒ `clientTLS(conn, cert, B)`（钉 B 的公钥）⇒
   `pathConn{kind: PathReverse}` ⇒ `swapHandle`。其余一律关闭。
4. **选路**：新增 `PathReverse`（页面「反向直连」），优先级放在「直连-公网」之后、「打洞」之前（TCP 比 UDP 更能穿过中间设备，且没有双层 TLS）。
   只在升级循环里发生（同打洞：它依赖中转连接做信令）；A 不监听或没有任何非私网候选时不试。
5. **安全**：前导之前没有任何认证，所以分流 Listener 要有首字节 / 前导读超时（5 秒）与并发上限，未知会话、HMAC 错一律断开；
   之后的 TLS 钉公钥照旧——拨错地址、被人冒充都在握手阶段失败。权限模型零变化。

##### 3. 工作量与风险

- 约 **3～4 天**（proto 与信令 0.5、分流 Listener 1、两侧接线与升级循环 1、测试与文档 1～1.5）。
- **主要风险在分流 Listener**：它挡在**所有**直连入站连接前面，写错了影响的不只是反向直连。必须做到：首字节读超时不拖慢正常 TLS、
  回放字节不丢不重、慢速连接攻击被并发上限挡住；另外要有「关掉反向直连 = 分流 Listener 不装」的开关作退路。
- 测试能在回环上做全（A 监听、B 不监听，且 B 侧挡掉 UDP ⇒ 期望走反向直连），不像 UPnP 那样依赖真路由器。

##### 4. 更便宜的替代：先给出提示（约半天）

增益表里前三行的根因都是「A 能被连到，但 UDP 没放行」。A 能自己判断出这种情况：本机 NAT 类型是 `none`（或配置了 `public_addrs`），
而作为**应答方**的打洞屡次失败、或作为发起方失败原因是探测超时 ⇒ 在页面「协调节点」一节提示
「本机看起来有公网地址，但 UDP 19194 似乎没有放行；放行后对方可以打洞直连，不再经中转」。这一条覆盖了大部分增益，零协议改动。

##### 5. 建议

1. **现在不做反向直连**，先做 §4 的提示（可以并入 P7，或作为 P6 的小尾巴）。
2. **用数据决定**：P7 的中转流量统计（按节点对）+ §14.6 的真机打洞结果出来后，看「长期留在中转、且一方可被 TCP 连到」的节点对有多少、
   占多少中转字节。只有这部分明显时再做反向直连——到时方案按上面 §2，不需要改协调节点。

### P7 — 中转流量统计（下一期；规划 2026-10-07，待批准）

**为什么要**：中转是协调节点唯一的成本项（§5.4、§8.2），现在只有「中转结束时记一行日志、总字节数」，运营者回答不了
「这个月 VPS 的流量花在谁身上」，用户也看不到「我有多少流量走了中转」。P6 打洞上线后还需要它来**量化打洞省下了多少**。

**原则**：协调节点只保留 CLI 操作（P6 已定），统计**只经 CLI 看**，不加 Web 界面、不加对外端口；管理器侧只统计**自己**的中转字节，
不向协调节点上报任何东西（协调节点本来就看得到全部中转字节，管理器再报一遍既多余又可伪造）。

#### P7-1 协调节点：按方向计数并落库（`internal/meshcoord`）

- `relay.bytes` 拆成 `bytesAB` / `bytesBA`（发起方 → 被叫方、反方向），原子计数，转发循环里各加各的。
- `coordinator.db` 迁移到 `schemaVersion = 2`，新增表：
  `relay_sessions(id TEXT PK, network_id, from_node, to_node, opened_at, paired_at, ended_at NULL, bytes_ab, bytes_ba, end_reason)`，
  索引 `(network_id, opened_at)`、`(from_node)`、`(to_node)`。
- **写入时机**：会话**配齐**时插入一行（`ended_at` 为空）；运行期间每 **60 秒**批量刷一次在途会话的字节数（一个 goroutine、一条事务）；
  结束时写最终字节数、`ended_at` 与结束原因（`peer_closed` / `idle_timeout` / `rate_limited` / `shutdown` …）。
  没配齐就作废的会话不入库（没有流量，只记日志）。
- **崩溃恢复**：启动时把 `ended_at` 为空的行收尾为 `coordinator_restart`（字节数取最后一次刷盘值，最多少算 60 秒）。
- **保留期**：`stats.retention_days`（默认 **30**，0 = 永久），每小时清理一次过期行。按 30 天、几十台管理器估算，表在 MB 级。
- `coordinator.yaml` 新增 `stats:` 段（`retention_days`、`flush_interval`）；`KnownFields(true)` 照旧，模板同步。
- 日志行改为「A → B 转发 x，B → A 转发 y」。

#### P7-2 协调节点 CLI（`cmd/asa-coordinator`，与运行中的服务是两个进程，只读库）

| 命令 | 输出 |
|---|---|
| `relay list [--active] [--node <ID>] [--since 24h] [--limit 50] [--json]` | 会话明细：时间、双方短 ID 与备注名、持续时长、两个方向字节数、结束原因；`--active` 只看在途（字节数最多滞后 60 秒） |
| `relay stats [--by node\|pair\|day] [--since 7d] [--json]` | 汇总：按节点（作为发起方 / 被叫方各多少）、按节点对、按天 |
| `node list`（增量） | 多一列「中转 7 天」（两个方向合计） |

人类可读的字节数（KiB / MiB / GiB），`--json` 给原始整数，方便运营者自己接监控。

#### P7-3 可选闸门：每节点每日中转额度

- `limits.relay_daily_bytes`（默认 0 = 不限）：某节点当天（UTC）作为任一方的中转字节数超额后，**新的** `OpenRelay` 返回
  `ResourceExhausted`「今日中转额度已用完」；**在途会话不切断**（切断正在看的面板体验太差，额度是成本闸门不是计费）。
- 当天用量从 `relay_sessions` 汇总 + 在途会话的内存计数得出，`OpenRelay` 时查一次（有索引，毫秒级）。
- 管理器侧把这个错误原样显示在对端行上（现有的 `last_error` 即可），直连 / 打洞不受影响。

#### P7-4 管理器：本机的中转字节（`internal/mesh`，只在内存）

- 中转连接（`openRelayStream` 包出来的 `streamconn`）外面套一个计数包装，按对端累计 `sent` / `received`（含入站中转，即别人经中转连本机）。
- `GET /api/mesh/peers` 每行增加 `relay_bytes: {sent, received}`；`GET /api/mesh/status` 增加本次启动以来的合计与起算时间。
- **不持久化**：mesh 重启即清零（页面写明「本次启动以来」）。要长期账单看协调节点的 `relay stats`。
- 页面：对端行在有中转流量时显示「经中转 ↑x ↓y」；②「协调节点」节显示合计。

#### P7-5 测试与验收

- 单测：迁移 v1 → v2（拿一份 v1 的库升级，原有网络 / 节点 / 拉黑表不变）；按方向计数；周期刷盘与结束写入；崩溃恢复收尾；保留期清理；
  额度超额拒绝新会话、在途不断；CLI 的汇总 SQL（用固定数据断言 `--by node|pair|day`）。
- 管理器：中转连接收发 N 字节后 `relay_bytes` 精确等于 N（套在 TLS 下面，计的是密文字节——与协调节点看到的一致）。
- 人工（追加到 §14）：T1 拓扑下经中转下载一个备份，`relay list --active` 一分钟内看到字节数增长；结束后 `relay stats --by pair` 与管理器页面的数字一致（差值 = 协议帧开销，< 1%）。

#### 待批准时确认的三点

| # | 问题 | 推荐 |
|---|---|---|
| Q1 | 做不做每日额度（P7-3） | **做，默认关**：代价小（一次索引查询），却是运营者唯一能控制带宽账单的手段 |
| Q2 | 管理器侧要不要持久化中转字节 | **不要**：账单的权威来源是协调节点；管理器只回答「现在 / 本次启动有多少走了中转」 |
| Q3 | 保留期默认值 | **30 天**，可配 |

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

---

## 14. 人工验证清单（P1～P4、P6）

自动化测试（P1-9、P2-7、P3-10）覆盖不到的项目**全部集中在本章**；§12 各阶段的「验收」只指向这里，不再各列一份。
每一项都写了**前置环境**、**操作**与**期望**——「期望」就是判据，结果不符时把那一步的完整命令输出与两边日志贴回来。
P2～P4 的条目按 §12 的细化编写（接口路径、CLI 名字以细化为准）；落地时若有改名，在「P2～P4 实施记录」里注明并同步改这里。

### 14.0 通用约定

**三条判据原则**（每一项都隐含它们，下文不再重复）：

1. **对照法**：凡是「经 A 转发到 B」的请求，把同一个请求**直接发给 B 自己的端口**再做一次，两次的状态码与响应体应当一致
   （B 授予的角色是 `admin`、B 关着鉴权时）。不一致 = 隧道有问题；一致但都失败 = B 本身的问题，与本功能无关。
2. **日志对账**：A 侧每个转发请求有一行 `[mesh] → <对端> <方法> <路径> → <状态码>`，B 侧有对应的 `[mesh] peer:<备注名>/<用户> …`。
   管理器日志在 `{BaseDir}/logs/asaServer.log`，协调节点日志在 `{data_dir}/logs/coordinator.log`。
3. **不刷屏**：断线、拒绝类的场景，日志按退避节奏出现（1s、2s、4s… 到 60s 封顶后不再每次都记），一分钟内同类 WARN 不超过个位数。

**工具**：PowerShell 7（`pwsh`，下面的引号写法依赖 7.3+ 的原生参数传递）、系统自带的 `curl.exe`（**不要**写成 `curl`，那是
`Invoke-WebRequest` 的别名）、浏览器（Chrome / Edge，开发者工具）。STUN 第三方客户端在 WSL 里装（V1-6）。
管理器默认 HTTPS + 本地自签 CA，`curl.exe` 一律带 `-k`。

**记录方式**：每项做完在本章末尾的「14.7 验证记录」表里填一行（日期、环境、结果、备注）；失败项写清卡在哪一步。

### 14.1 环境搭建

#### 拓扑

| 拓扑 | 组成 | 能验证什么 |
|---|---|---|
| **T1 单机** | 一台 Windows：协调节点 + 管理器 A + 管理器 B（+ 需要时 C），各用独立目录与端口 | P1 中转、P3 配对/隧道/安全、P4 前端的绝大部分。**同一台机器上没法真正「断开内网」**，P2 的断开/恢复要 T2 |
| **T2 真实网络** | 公网 VPS 上的协调节点；同一局域网的两台机器 A、B；（可选）另一个网络里的 C（例如连手机热点的笔记本） | P2 直连/降级/升级、真实 NAT 下的 STUN、经中转的大文件吞吐 |
| **WSL** | Windows 本机的 WSL | 第三方 STUN 客户端（V1-6）、Linux 版管理器与「无界面服务器上用 CLI 生成邀请码」（V3-9） |

验证用的程序与数据放在**独立目录**（Windows `C:\mesh-verify`、WSL `/opt/mesh-verify`），不碰 `E:\asa_server_data` 与 WSL 里现有的
`/opt/asa-server`。`config init --basedir` 会检查剩余空间（≥ 30GB），C 盘不够时换盘符。

#### T1 搭建（Windows，普通 PowerShell 7）

```powershell
cd D:\golang\asa-server
git switch feat/remote-mesh
$v = 'C:\mesh-verify'
New-Item -ItemType Directory -Force $v, "$v\coord", "$v\A", "$v\B", "$v\C" | Out-Null
go build -o "$v\asa-server.exe" .
go build -o "$v\coord\asa-coordinator.exe" ./cmd/asa-coordinator

# 三个管理器各一份 config.yaml；同一个 exe 靠 ASA_CFG 区分
foreach ($n in 'A', 'B', 'C') {
  $env:ASA_CFG = "$v\$n"
  & "$v\asa-server.exe" config init --dir "$v\$n" --basedir "$v\$n\data" --non-interactive --lang zh
  # 验证用的管理器不需要 Syncthing：api 启动时会自动下载并拉起它，而它会用 UPnP 在路由器上开端口映射。
  # 把下载代理指向一个不存在的地址：下载失败是非致命的（只记一条 ERROR），Syncthing 就不会启动。
  (Get-Content "$v\$n\config.yaml") -replace '^  github_proxy: ""', '  github_proxy: "http://127.0.0.1:9/"' |
    Set-Content "$v\$n\config.yaml" -Encoding utf8
}
$env:ASA_CFG = $null

# 便捷函数：在当前窗口里以某个管理器的身份跑 CLI
function asa { param($n) $env:ASA_CFG = "$v\$n"; & "$v\asa-server.exe" @args; $env:ASA_CFG = $null }
```

> 之后每开一个新的 PowerShell 窗口，都先执行 `$v = 'C:\mesh-verify'` 与上面的 `function asa …` 两行。

协调节点（T1 用 8443，避开别的服务）：

```powershell
& "$v\coord\asa-coordinator.exe" config init -o "$v\coord\coordinator.yaml"
(Get-Content "$v\coord\coordinator.yaml") `
  -replace '^listen: ":443"', 'listen: ":8443"' `
  -replace '^public_addr: .*', 'public_addr: "127.0.0.1:8443"' |
  Set-Content "$v\coord\coordinator.yaml" -Encoding utf8NoBOM
Start-Process "$v\coord\asa-coordinator.exe" -ArgumentList 'run', '-c', "$v\coord\coordinator.yaml"
& "$v\coord\asa-coordinator.exe" join-blob -c "$v\coord\coordinator.yaml"   # 复制输出的整串，下面记作 $blob
```

- 期望：协调节点窗口打印配置路径与监听地址（TCP 8443、UDP 3478/3479），**不**打印 join blob；Windows 弹防火墙提示时选「专用网络」允许。

管理器 A（Web 19193）与 B（Web 19293、Peer 端口 19294——同机两个管理器不能都用默认的 19194）：

```powershell
$blob = '<上一步复制的整串>'
asa A mesh join $blob
asa B mesh join $blob
# 取最后一行：本机若还设着旧的 ASA_BASEDIR，CLI 会先打印一行 WARN，别把它当成节点 ID
$idA = asa A mesh id | Select-Object -Last 1
$idB = asa B mesh id | Select-Object -Last 1
# 备注名（对方配对后看到的名字）与 B 的 Peer 端口 19294（同机两个管理器不能都监听 19194）。
# 没有改这两项的 CLI，启动前直接改 config.json；运行中则用 PUT /api/mesh/config（只改请求里出现的字段，保存后热应用）。
function setMesh { param($n, $label, $port)
  $f = "$v\$n\data\mesh\config.json"
  $j = Get-Content $f -Raw | ConvertFrom-Json
  $j | Add-Member -NotePropertyName label -NotePropertyValue $label -Force
  if ($port) { $j | Add-Member -NotePropertyName peer_port -NotePropertyValue $port -Force }
  $j | ConvertTo-Json -Depth 5 | Set-Content $f -Encoding utf8NoBOM
}
setMesh A '机A'
setMesh B '机B' 19294
# --skip-env-check：验证用的数据目录里没有 SteamCMD / ARK 本体，不加它 api 会拒绝启动（「基础环境尚未初始化」）
$env:ASA_CFG = "$v\A"; $pA = Start-Process "$v\asa-server.exe" -ArgumentList 'api', '--skip-env-check' -PassThru
$env:ASA_CFG = "$v\B"; $pB = Start-Process "$v\asa-server.exe" -ArgumentList 'api', '--port', '19293', '--skip-env-check' -PassThru
$env:ASA_CFG = $null
$A = 'https://127.0.0.1:19193'; $B = 'https://127.0.0.1:19293'
```

- 期望：两个新窗口里都出现 `[mesh] 已启动` 与 `[mesh] 已登记到协调节点 127.0.0.1:8443`；协调节点窗口出现两条「节点 … 上线」。
- 首次以 HTTPS 启动时程序会把本地 CA 装进**当前用户**的受信任根存储，Windows 可能弹出「是否安装此证书」的确认框，
  确认之前 Web 端口不会开始监听。不想动证书存储就在两条 `Start-Process` 的参数里加 `'--tls=false'`，并把
  `$A` / `$B` 改成 `http://`（2026-10-05 的冒烟就是这样跑的，见「P2～P4 实施记录」）。
- 停止：`Stop-Process $pA.Id`、`Stop-Process $pB.Id`；重启就是重新执行对应的 `Start-Process` 那一行。

#### T2 搭建

- **VPS**（Linux，root）：在开发机上 `wsl -e zsh -lc 'cd /mnt/d/golang/asa-server && GOOS=linux CGO_ENABLED=0 go build -o /opt/mesh-verify/asa-coordinator ./cmd/asa-coordinator'`，
  把 `/opt/mesh-verify/asa-coordinator` 拷到 VPS 的 `/opt/asa-coordinator/`，然后：

  ```bash
  cd /opt/asa-coordinator
  ./asa-coordinator config init -o /opt/asa-coordinator/coordinator.yaml
  sed -i 's/^public_addr: .*/public_addr: "<VPS 公网 IP>:443"/' coordinator.yaml
  ./asa-coordinator service install -c /opt/asa-coordinator/coordinator.yaml
  ./asa-coordinator service start
  ./asa-coordinator join-blob -c /opt/asa-coordinator/coordinator.yaml
  ```

  云厂商安全组 + 本机防火墙放行 **TCP 443、UDP 3478、UDP 3479**（§8.1：最容易漏的一步）。
- **A、B**：两台同一局域网的机器各装一份 asa-server（同 T1 的 `config init` + `mesh join`），都用默认端口（Web 19193、Peer 19194）。
- **C**（可选）：另一个网络里的第三台机器，同样接入。

### 14.2 P1：身份、协调节点、中转

**V1-1 双向经中转 Hello**（T1，或 T2 但把 A、B 的 Peer 端口都关掉：`PUT /api/mesh/config {"no_listen":true}`——P2 之后 T1 上默认会走直连）

```powershell
curl.exe -sk -X POST "$A/api/mesh/hello/$idB"
curl.exe -sk -X POST "$B/api/mesh/hello/$idA"
```

- 期望：两条都 `"success":true`，`data.path` 为 `"relay"`，`data.version` 是对方版本，`data.granted_role` 为 `"ROLE_NONE"`（还没配对）。
- P2 之前（当前 P1 代码）不需要关 Peer 端口，结果就是 `relay`。

**V1-2 协调节点停机与恢复**（T1）

1. 关掉协调节点窗口（或 `Stop-Process -Name asa-coordinator`）。
2. 立即与 30 秒后各执行一次 `curl.exe -sk "$A/api/mesh/status"`。
   - 期望：`connected:false`，`last_error` 非空；A、B 的窗口里重连 WARN 的间隔依次变长（1s、2s、4s…），到 60 秒封顶后**不再每次都记**。
3. 两分钟后重新 `Start-Process` 协调节点。
   - 期望：最多一个退避周期（≤ 60 秒）内 A、B 都出现 `已登记到协调节点`，`status` 回到 `connected:true`；V1-1 的 Hello 恢复成功。

**V1-3 网络密钥错误的新节点被拒**（T1）

```powershell
asa C mesh join $blob
$f = "$v\C\data\mesh\config.json"
(Get-Content $f -Raw) -replace '("network_secret":\s*")(.)', '$1x' | Set-Content $f -Encoding utf8NoBOM   # 改坏密钥的第一个字符
$env:ASA_CFG = "$v\C"; $pC = Start-Process "$v\asa-server.exe" -ArgumentList 'api', '--port', '19393', '--skip-env-check' -PassThru; $env:ASA_CFG = $null
```

> 必须用 **从没登记过** 的 C：已是网络成员的节点只凭证书登记、不再看密钥（§8.4.5）。join blob 本身带校验和，直接改串会在本地就报
> `ErrChecksum`、根本到不了协调节点，所以改的是写进 `config.json` 之后的密钥。C 的 Peer 端口（默认 19194）会与 A 冲突，
> 这一项不关心直连，忽略「监听失败」的 WARN 即可。

- 期望：协调节点日志 `拒绝 <C 的短 ID>（来自 …）：网络 "default" 的密钥不正确`（WARN）；C 的 `status` 为 `connected:false`，
  `last_error` 含「接入被拒绝」；C 按退避重试、不刷屏。
- 收尾：`Stop-Process $pC.Id`；`asa C mesh leave`。

**V1-4 拉黑在线节点**（T1）

```powershell
& "$v\coord\asa-coordinator.exe" node list -c "$v\coord\coordinator.yaml"
& "$v\coord\asa-coordinator.exe" node ban -c "$v\coord\coordinator.yaml" $idB
```

- 期望：`node list` 列出 A、B（备注名、版本、最后在线）；`ban` 之后 **30 秒内** B 的窗口出现被踢的 ERROR（「本节点已被协调节点管理员拉黑」），
  之后 B 重连被拒；A 对 B 的 Hello 失败（`NotFound` / 502）。
- 收尾：`node unban -c "$v\coord\coordinator.yaml" $idB`，B 在一个退避周期内重新登记。

**V1-5 同一身份出现在两处**（T1）

```powershell
Copy-Item "$v\B\data\mesh" "$v\C\data\mesh" -Recurse -Force   # C 拿到 B 的私钥与配置
$env:ASA_CFG = "$v\C"; $pC = Start-Process "$v\asa-server.exe" -ArgumentList 'api', '--port', '19393', '--skip-env-check' -PassThru; $env:ASA_CFG = $null
```

- 期望：协调节点 WARN「节点 … 有新会话登记…同一身份出现在两处」；被踢的一方记 ERROR，且**不会**以 1 秒的节奏互踢（退避生效）。
- 收尾：`Stop-Process $pC.Id`；`Remove-Item "$v\C\data\mesh" -Recurse -Force`。

**V1-6 第三方 STUN 客户端**（WSL 问 T1 的协调节点，或问 T2 的 VPS）

```bash
sudo apt-get install -y stuntman-client          # 提供 stunclient
WINHOST=$(ip route | awk '/default/ {print $3}')  # WSL 里看到的 Windows 主机地址；问 VPS 时换成 VPS 地址
stunclient "$WINHOST" 3478
stunclient "$WINHOST" 3479
```

（也可以用 coturn 的 `turnutils_stunclient -p 3478 <host>`。）

- 期望：两条都输出 `Binding test: success` 与 `Mapped address: <WSL 的出口 IP>:<端口>`；协调节点日志里 10 分钟内出现一行 STUN 计数（成功数增加）。
- 问 T1 时 Windows 防火墙可能拦 WSL 进来的 UDP：协调节点首启的防火墙提示要勾上「专用网络」。

**V1-7 `stun probe` 与 NAT 实测**（T2：在家宽 Windows 上问 VPS）

```powershell
& "$v\coord\asa-coordinator.exe" stun probe <VPS 公网 IP>
```

- 期望：打印本机地址、两个反射地址与映射类型结论（无 NAT / 端点无关 / 端点相关）。
- **把结论、运营商与路由器型号回填到本文 §5.6.4**，作为 P6 的第一条实测数据。T1 本机回环只能证明连通（结论恒为「无 NAT」）。

**V1-8 `stun_addrs` 下发**（T1 或 T2）

- 操作：`curl.exe -sk "$A/api/mesh/status"`，B 同理。
- 期望：`stun_addrs` 为 `["127.0.0.1:3478","127.0.0.1:3479"]`（T1）或 `["<VPS 地址>:3478", …]`（T2）。

**V1-9 协调节点作为 Linux 服务**（T2 的 VPS）

- 操作：`systemctl status asa-coordinator`（服务名以 `service install` 的输出为准）；`reboot` 后再看一次。
- 期望：开机自启；`ExecStart` 里是 `run -c /opt/asa-coordinator/coordinator.yaml`（绝对路径）；数据库在 `/opt/asa-coordinator/data/`，
  不在 `/` 或 `/root`（相对路径按配置文件目录解析，§12 P1-5）。

### 14.3 P2：直连

**V2-1 同一局域网走直连**（T2）

- 操作：在 A 上 `curl.exe -sk -X POST "https://127.0.0.1:19193/api/mesh/hello/<B 的 ID>"`；在 B 上 `GET /api/mesh/status` 看 `candidates`。
- 期望：`path` 为 `"lan"`；B 的 `candidates` 里有它的局域网 IP + `:19194`，**没有** `127.*`、`169.254.*`、`fe80::`，
  也没有 WSL / Hyper-V（`vEthernet (WSL…)`，通常是 `172.x`）与 Docker 网桥的地址。
- T1 上同样能看到 `lan`（B 上报的是本机网卡 IP），可以先在 T1 冒烟。

**V2-2 断开直连 → 中转 → 恢复后升级**（T2）

1. 在 B 上挡住 Peer 端口的入站（管理员 PowerShell）：
   `New-NetFirewallRule -DisplayName mesh-verify-block -Direction Inbound -Protocol TCP -LocalPort 19194 -Action Block`
   （B 是 Linux 时：`iptables -I INPUT -p tcp --dport 19194 -j REJECT`）。
2. 已建立的 TCP 连接不受新规则影响，所以**重启 A**（停掉再起 A 的 asa-server），再 Hello B。
   - 期望：`path` 为 `"relay"`，耗时比 V2-1 多大约 2 秒以内（Happy Eyeballs 的整体上限）；
     A 的日志有一行 `[mesh] 到 <B 的短 ID> 改走relay：直连：<B 的地址>: …`（后半段是超时 / 拒绝连接的原因）。
3. 在 A 上经中转开一条长流，**保持不关**（这条流同时让升级循环保持活跃——最近 10 分钟没人用的对端不会去试直连）：
   `curl.exe -skN "https://127.0.0.1:19193/api/peers/<B 的 ID>/fwd/api/logs"`
4. **一分钟内**删掉规则：`Remove-NetFirewallRule -DisplayName mesh-verify-block`（Linux：`iptables -D INPUT -p tcp --dport 19194 -j REJECT`）。
   升级循环 1 分钟起、翻倍到 10 分钟，越晚恢复要等得越久。
5. 两分钟后再 Hello。
   - 期望：`path` 回到 `"lan"`；A 的日志有一行 `[mesh] 到 <B 的短 ID> 的路径已从中转升级为直连（lan）`；
     **第 3 步那条 curl 仍在持续输出**（在途流不被升级打断）。
6. Ctrl+C 结束第 3 步的 curl。
   - 期望：协调节点日志（VPS 上 `{data_dir}/logs/coordinator.log`）随即出现 `[coord] 中转 <会话> 结束：<A> → <B>，持续 …，转发 … 字节`
     ——旧的中转连接在最后一个使用者离开时才关闭。

**V2-3 拨到「同一地址的别的机器」不串线**（T1：需要 A、B、C 三个管理器）

1. 让 C 接入并在 19394 上监听（V1-5 收尾时删掉了 C 的 mesh 目录，这里重新接入）：

   ```powershell
   asa C mesh join $blob
   $idC = asa C mesh id | Select-Object -Last 1
   setMesh C '机C' 19394
   $env:ASA_CFG = "$v\C"; $pC = Start-Process "$v\asa-server.exe" -ArgumentList 'api', '--port', '19393', '--skip-env-check' -PassThru; $env:ASA_CFG = $null
   ```

2. 在 A 上把 B 的手填地址改成 C 的地址，并把 B 的 Peer 端口关掉，让 B 只剩这一个错误的候选：

   ```powershell
   curl.exe -sk -X PUT "$A/api/mesh/peers/$idB" -H 'Content-Type: application/json' -d '{"addrs":["127.0.0.1:19394"]}'
   curl.exe -sk -X PUT "$B/api/mesh/config" -H 'Content-Type: application/json' -d '{"no_listen":true}'
   ```

3. 重启 A，`curl.exe -sk -X POST "$A/api/mesh/peers/$idB/hello"`。
   - 期望：Hello **成功**、`path` 为 `"relay"`、`version` 与 `label` 是 B 的（`机B`）；A 的日志有一行
     `[mesh] 到 <B 的短 ID> 改走relay：直连：127.0.0.1:19394: 对端身份不符：期望 <B 的短 ID>，实际 <C 的短 ID>`；
     C 的日志里**没有**任何 `[mesh] peer:` 开头的请求行（握手在 A 侧就失败了，C 没有处理任何请求）。
4. 收尾：`PUT $B/api/mesh/config {"no_listen":false}`；`PUT $A/api/mesh/peers/$idB {"addrs":[]}`；`Stop-Process $pC.Id`。

**V2-4 无协调节点模式**（T2；T1 也行）

1. A、B 都切到无协调节点模式。页面 / 接口是热应用的：`POST /api/mesh/leave` 再 `POST /api/mesh/enable`；
   用 CLI（`mesh leave` + `mesh enable`）则要重启服务。
   - 期望：`status` 里 `coordinator` 为空、`enabled:true`、`running:true`、`listen_addr` 形如 `[::]:19194`（T1 上 B 是 `[::]:19294`），
     日志里**没有**任何连协调节点的尝试。
2. B 上生成带直连地址的邀请码：`$inv = asa B mesh invite --role operator --addr <B 的局域网 IP>:19194 | Select-Object -Last 1`
   （T1 上用 `127.0.0.1:19294`）。
3. A 上 `POST /api/mesh/pair`，`{"invite":"<整串>"}`，然后 Hello B。
   - 期望：配对成功，`path` 为 `"lan"`；之后的 V3 隧道用例在这个模式下同样可用（抽一条 V3-3 的请求验证即可）。
4. 收尾：两边重新接入（`POST /api/mesh/join {"blob":"…"}`——mesh 此时在运行，保存即热应用；或 CLI `mesh join` + 重启）。
   2026-10-07 起 `/join` 不再顺带启用：若 mesh 已停止，要再 `POST /api/mesh/enable`（页面上是顶部的「启动」）。

**V2-5 公网直连**（T2，可选：需要 B 有公网 IP 或路由器端口映射）

- 操作：B 的路由器把公网 TCP 19194 映射到 B；`PUT /api/mesh/config {"public_addrs":["<公网 IP 或域名>:19194"]}`；C（另一个网络）Hello B。
- 期望：`path` 为 `"public"`。不做端口映射时 C → B 应为 `"relay"`（这本身也是一条有效结果，记录下来）。

**V2-6 Peer 端口被占用不致命**（T1）

- 操作：先占住 A 的 19194（`python -m http.server 19194`，或任意程序），再重启 A。
- 期望：A 的 mesh 正常启动、能登记到协调节点；`status.listen_error` 非空并点名端口；A 主动发起的 Hello（走中转）照常成功。

**V2-7 Windows 防火墙提示**（T2 的 Windows 机器，首次）

- 操作：以**桌面程序**方式（双击 / 在终端里 `asa-server api`）首次启用 mesh。
- 期望：Windows 弹出防火墙提示（这是 D7「默认监听」的已知代价，页面上有说明）。拒绝时直连进不来、自动落到中转，功能不受影响；
  以 **Windows 服务**运行时不弹提示、但入站同样被默认规则挡住——记录实际表现，回填到 P4 页面的提示文案。

**V2-8 协调节点停机不影响直连**（T2；§13 的承诺）

1. A、B 走直连（V2-1）后，A 上经转发开一条长流：`curl.exe -skN "…/api/peers/<B 的 ID>/fwd/api/logs"`。
2. 停掉协调节点（VPS 上 `asa-coordinator service stop`）。
   - 期望：第 1 步的流**不断**；新的请求（`/fwd/api/instances`）照常成功。
3. 重启 A（直连连接没了，`Resolve` 也问不到候选），再 Hello B。
   - 期望：B 在 A 的 `peers.json` 里有手填地址（V2-4 或邀请码的 `--addr` 留下的）时仍走直连成功；没有时失败并提示无法连接
     ——这是「没有协调节点就只剩手填地址」的预期行为，记录下来。
4. 收尾：`asa-coordinator service start`，两边一个退避周期内重新登记。

### 14.4 P3：配对、授权、隧道

前置：T1 的 A、B 都在线；下面用 `$fwd = "$A/api/peers/$idB/fwd"`。

**V3-1 邀请码配对**

```powershell
# 服务在跑时用 CLI 生成，验证「CLI 写、服务自动重载」。邀请码打在标准输出的最后一行（提示语在 stderr）
$inv = asa B mesh invite --role operator --ttl 10m --note 'A 机' | Select-Object -Last 1
curl.exe -sk -X POST "$A/api/mesh/pair" -H 'Content-Type: application/json' -d "{`"invite`":`"$inv`"}"
curl.exe -sk -X POST "$A/api/mesh/peers/$idB/hello"
curl.exe -sk -X POST "$A/api/mesh/pair" -H 'Content-Type: application/json' -d "{`"invite`":`"$inv`"}"   # 再用一次
```

- 期望：第一次 `"status":"paired"`、`"granted_role":"operator"`、`"label":"机B"`；Hello 的 `granted_role` 为 `ROLE_OPERATOR`、`label` 为 `机B`；
  **同一个邀请码第二次失败**（403「邀请码无效或已过期」）。
- `asa B mesh peers` 列出 A（备注名 `A 机`——取自 `--note`，授予 operator）；`Get-Content "$v\B\data\mesh\peers.json"` 里
  **看不到邀请密钥原文**（只有 `secret_sha256`）。
- 期满失效：`--ttl 1m` 生成一个，等两分钟再用 → 失败。
- **已配对时用新邀请改角色不丢响应**（实施记录偏差 1 的回归）：先在 A 上开一条 `curl.exe -skN "$fwd/api/logs"` 保持不关，
  再用 `asa B mesh invite --role admin` 生成的邀请码配对一次。
  - 期望：配对**一次成功**、返回 `admin`（不会先报错、重试后才报「邀请码无效」）；升级不切断那条在途的日志流。
- 限流：拿一个随便改过末尾几个字符的邀请码（会报校验失败，到不了 B）不算；要测限流就用一个**过期**的邀请码连续配对 5 次，
  第 6 次（即使换成有效的邀请码）返回 429「配对尝试过于频繁」，10 分钟后恢复。

**V3-2 申请-批准**（用 C，或先在 B 上撤销 A：`asa B mesh revoke $idA`）

```powershell
curl.exe -sk -X POST "$A/api/mesh/pair" -H 'Content-Type: application/json' -d "{`"node_id`":`"$idB`"}"
curl.exe -sk "$B/api/mesh/requests"
curl.exe -sk -X POST "$B/api/mesh/requests/$idA" -H 'Content-Type: application/json' -d '{"role":"admin"}'
curl.exe -sk -X POST "$A/api/mesh/peers/$idB/hello"
```

- 期望：第一步 `PENDING`；B 的待批准列表里有 A（备注名、版本、来源地址）；批准后 A 的 Hello 显示 `ROLE_ADMIN`。
- 拒绝路径：再来一次，用 `DELETE "$B/api/mesh/requests/$idA"` 拒绝 → A 的 Hello 仍是 `ROLE_NONE`。

**V3-3 经隧道的日常操作**（B 授予 A `admin`，B 关鉴权；按「对照法」逐条与直连 B 比较）

| 操作 | 命令（A 侧） | 期望 |
|---|---|---|
| 实例列表 | `curl.exe -sk "$fwd/api/instances"` | 与 `curl.exe -sk "$B/api/instances"` 一致 |
| 系统日志 SSE | `curl.exe -skN "$fwd/api/logs"` | 逐条到达、不是攒一批才出来：另开一个窗口发一个会在 B 上记 INFO 日志的请求（例如 `curl.exe -sk -X POST "$fwd/api/users"`，它本身会 403，但 B 会记一行 `[mesh] peer:… POST /api/users → 403`），这一行几乎立即出现在 SSE 里 |
| 资源 SSE | `curl.exe -skN "$fwd/api/server/all-info"` | 每 2 秒一条，数值是 **B** 的机器（与 B 的任务管理器对得上） |
| 启动 / 停止实例 ⚠️ | `curl.exe -sk "$fwd/api/server/<实例>/start"`、`…/stop` | B 上实例真的启动 / 停止；B 的日志里是 `peer:` 用户发起的 |
| 实例日志 SSE ⚠️ | `curl.exe -skN "$fwd/api/logs/<实例>"` | 实时游戏日志 |
| WS 事件 | 浏览器打开 `https://127.0.0.1:19193`（A 的页面），开发者工具 Console 执行：`w = new WebSocket('wss://127.0.0.1:19193/api/peers/<B 的 ID>/fwd/api/ws/events'); w.onmessage = e => console.log(e.data)` | 连上（Network 面板里 101）；在 B 上启停实例或做配置同步时 Console 收到事件 |
| RCON（WS）⚠️ | 同上打开 `…/fwd/api/ws/rcon`，`w.send(JSON.stringify({action:'command', instance_name:'<实例>', command:'ListPlayers'}))` | 收到 `success:true` 与游戏的回答 |
| 大文件上传 | 经 A 的转发上传一个 ArkApi 插件 zip（P4 页面里做最方便；curl：`curl.exe -sk -F "file=@<zip>" "$fwd/api/arkapi/packages"`） | 与直连 B 上传的校验结果一致；A、B 进程内存不随文件大小暴涨（流式，不整块缓冲） |
| 大文件下载 | 经转发下载一个世界存档备份 | 文件哈希与 B 上的原文件一致 |

⚠️ 标记的三项需要 B 上有能启动的 ARK 实例（装了服务端、建了实例）。T1 的 B 没装服务端时，把这三项放到 T2 的真实服务器上做。

**V3-4 B 关着鉴权时，`operator` 仍然调不了管理员接口**

1. B 上把 A 改成 operator：`PUT $B/api/mesh/peers/$idA`，`{"granted_role":"operator"}`。
2. `curl.exe -sk -i -X DELETE "$fwd/api/arkapi/packages/no-such-token"`
   - 期望：`403`，`code` 为 `forbidden`，`error` 为「需要管理员权限（对方只授予了本机操作员角色）」。
     对照：直连 B（`-X DELETE "$B/api/arkapi/packages/no-such-token"`）**不是** 403——B 关鉴权时本机请求被视为管理员
     （2026-10-05 冒烟实测返回 `200 {"success":true}`：丢弃一个不存在的暂存包是幂等的）。
3. 改回 admin 再执行一次 → 不再是 403（与直连 B 一致）。

**V3-5 B 开着鉴权、甚至开着 `lan_bypass` 时，隧道仍只认授予的角色**

1. 在 B 的 `config.yaml` 里设 `auth.enabled: true`；`asa B user add admin --role admin`（按提示设密码）；
   **再**把 `auth.lan_bypass.enabled` 设为 `true`、`networks` 里加上 `0.0.0.0/0` 与 `::/0`（故意最宽）；重启 B。
2. A 仍是 operator：重复 V3-4 第 2 步 → 仍是 `403`；`curl.exe -sk "$fwd/api/instances"` → 成功（operator 能做的事照常）。
   - 这证明隧道请求**不经过** `lan_bypass`（§6.4），也不需要 B 的登录 Cookie。
3. `asa B user audit --event peer_request`：A 刚才的**非 GET** 请求（包括被 403 挡掉的那次 DELETE——尝试同样留痕）各有一条，
   用户名为 `peer:A 机/<A 上的用户名>`（A 关鉴权时用户名部分是 `-`），详情形如 `DELETE /api/arkapi/packages/no-such-token → 403`。
4. 收尾：把 B 的 `lan_bypass` 恢复成 `enabled: false`（**一定要恢复**）；`auth.enabled` 改回 `false` 并重启 B
   ——后面的用例直连 B 的 `curl` 都没带 Cookie，开着鉴权会全部 401。

**V3-6 远程禁区与禁止多跳**（B 授予 A `admin`）

| 请求（经 `$fwd`） | 期望 |
|---|---|
| `GET /api/users` | 403 `peer_forbidden` |
| `GET /api/auth/audit` | 403 `peer_forbidden` |
| `GET /api/auth/state` | 200，含 `"peer":true`、`"authenticated":true` |
| `GET /api/mesh/status` | 403 `peer_forbidden` |
| `GET /api/peers/<任意 ID>/fwd/api/instances` | 403 `peer_forbidden`（不能借 B 再跳到 C） |

**V3-7 撤销立即生效**

1. 在 A 上开一条长流并保持：`curl.exe -skN "$fwd/api/logs"`。
2. 在 B 上用 **CLI**（另一个进程，验证文件重载）撤销：`asa B mesh revoke $idA`。
   - 期望：几秒内第 1 步的 curl 结束；随后 `curl.exe -sk -i "$fwd/api/instances"` 返回 `403`，`code` 为 `peer_not_paired`；
     A 的 Hello 显示 `ROLE_NONE`。
3. 同样的流程用 B 的接口（`DELETE $B/api/mesh/peers/$idA`）再做一次，结果相同。
4. 重新配对（V3-1）以便后续用例。

**V3-8 A 侧的闸门**

1. **控制者角色**（D5）：A 开鉴权（同 V3-5 第 1 步，但**不开** `lan_bypass`），建 `admin` 与 `oper`（`--role operator`）两个用户
   （用户名至少 3 个字符），重启 A。分别登录拿 Cookie：

   ```powershell
   curl.exe -sk -c admin.jar -H 'Content-Type: application/json' -d '{"username":"admin","password":"<密码>"}' "$A/api/auth/login"
   curl.exe -sk -c oper.jar  -H 'Content-Type: application/json' -d '{"username":"oper","password":"<密码>"}'  "$A/api/auth/login"
   curl.exe -sk -b oper.jar -i "$fwd/api/instances"        # 期望 403
   curl.exe -sk -b oper.jar -i "$A/api/mesh/peers"         # 期望 403（默认 operator 连对端列表都看不到）
   curl.exe -sk -b admin.jar -X PUT "$A/api/mesh/config" -H 'Content-Type: application/json' -d '{"control_role":"operator"}'
   curl.exe -sk -b oper.jar -i "$fwd/api/instances"        # 期望 200
   curl.exe -sk -b oper.jar -i "$A/api/mesh/peers"         # 期望 200
   curl.exe -sk -b oper.jar -i -X PUT "$A/api/mesh/config" -H 'Content-Type: application/json' -d '{}'   # 期望 403：改配置仍只有管理员
   ```

   - 收尾：`control_role` 改回 `admin`；A 的 `auth.enabled` 是否保留视 §14.5 而定（保留时浏览器里登录即可，V4-4 最后一条要用到 operator 账号）。
2. **跨源 WebSocket 被 A 拒绝**（用 curl 只做握手）：

   ```powershell
   $h = @('-H','Connection: Upgrade','-H','Upgrade: websocket','-H','Sec-WebSocket-Version: 13','-H','Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==')
   curl.exe -sk -i -N --max-time 3 @h -H 'Origin: https://evil.example' "$fwd/api/ws/events"
   curl.exe -sk -i -N --max-time 3 @h -H 'Origin: https://127.0.0.1:19193' "$fwd/api/ws/events"
   ```

   - 期望：第一条 `403`；第二条 `101 Switching Protocols`。（A 开着鉴权时两条都要加 `-b <admin 的 Cookie>`。）
3. **A 的会话凭证不会带给 B**：不做人工验证。B 不记录请求头、隧道又是端到端加密，人工手段看不到；由单测守住
   （`internal/mesh/tunnel_test.go` 的 `TestTunnelRoundTrip`、`internal/webapi/meshapi/meshapi_test.go` 的 `TestForwardSecurity`：
   B 侧 handler 看到的请求头里没有 `Cookie` / `Authorization` / `X-Forwarded-For`）。

**V3-9 无界面 Linux 上用 CLI 配对**（WSL）

1. 在 WSL 里建一个管理器（root 身份，WSL 里本来就是 root）：

   ```bash
   cd /mnt/d/golang/asa-server && go build -o /opt/mesh-verify/asa-server .
   export ASA_CFG=/opt/mesh-verify/L
   mkdir -p $ASA_CFG && /opt/mesh-verify/asa-server config init --dir $ASA_CFG --basedir $ASA_CFG/data --non-interactive --lang zh
   /opt/mesh-verify/asa-server mesh join '<T1 的 join blob>'
   ```

2. **WSL 默认是 NAT 网络模式**，WSL 里的 `127.0.0.1` 不是 Windows：把接入地址改成 Windows 主机地址（钉的是协调节点公钥，改地址不影响校验）：

   ```bash
   WINHOST=$(ip route | awk '/default/ {print $3}')
   sed -i "s/\"addr\": \"127.0.0.1:8443\"/\"addr\": \"$WINHOST:8443\"/" $ASA_CFG/data/mesh/config.json
   ```

   （WSL 开了 `networkingMode=mirrored` 时不需要这一步。）协调节点首启的 Windows 防火墙提示必须允许过，否则 WSL 连不进来。
3. 同 §14.1，先让它不拉起 Syncthing：`sed -i 's/^  github_proxy: ""/  github_proxy: "http:\/\/127.0.0.1:9\/"/' $ASA_CFG/config.yaml`；
   然后启动并保持运行：`ASA_CFG=/opt/mesh-verify/L /opt/mesh-verify/asa-server api --port 19493 --skip-env-check`（日志里应出现「已登记到协调节点」）。
4. **另开一个 WSL 终端**：`ASA_CFG=/opt/mesh-verify/L /opt/mesh-verify/asa-server mesh invite --role admin | tail -1`，把输出的整串在 Windows 上交给 A：
   `curl.exe -sk -X POST "$A/api/mesh/pair" -H 'Content-Type: application/json' -d '{"invite":"<整串>"}'`。
5. `$idL = '<WSL 里 mesh id 的输出>'`；`curl.exe -sk "$A/api/peers/$idL/fwd/api/system/preflight"`。

- 期望：配对成功；WSL 管理器**不需要重启**就认得 A（CLI 写 `peers.json`，服务自动重载）；第 5 步拿到的是 **Linux** 的运行时自检结果
  （Windows 上这个接口恒为空），证明请求真的到了 Linux 那台。`POST $A/api/mesh/peers/$idL/hello` 的 `path`：WSL 的候选地址
  （WSL 里 `eth0` 的 `172.x`）Windows 能直接拨通，通常是 `lan`。
- 收尾：Ctrl+C 停掉 WSL 里的 api；`rm -rf /opt/mesh-verify`（§14.7 收尾时一起做也行）。

**V3-10 被控方以服务方式运行**（T2 的 B；Windows 服务或 Linux systemd 各做一次更好）

本仓库的老问题是「服务模式下某些初始化不执行」（`CLAUDE.md`：Windows 服务模式下 `app.Run()` 不执行）。隧道依赖
`internal/webapi/actions.go` 在 `Start` 里把 Gin engine 注入给 mesh，服务模式必须同样走到这一步。

1. B 以服务方式运行（`asa-server service install` + `service start`；Linux 是 systemd），A 照常配对。
2. 重复 V3-3 的「实例列表」「系统日志 SSE」两行与 V3-6 的 `/api/users` 一行。
   - 期望：结果与 B 以 `api` 方式运行时完全一致；尤其**不是** 502 / `Unavailable`「本机的 HTTP 服务尚未就绪」——出现它说明服务模式下
     没有注入 Gin engine。
3. 在 B 的页面上改一次 mesh 配置（例如备注名）并保存。
   - 期望：服务不用重启即生效（mesh 在运行时保存即热应用，提示「已保存并应用」），A 的 Hello 立即看到新的 `label`。

### 14.5 P4：前端（浏览器）

前置：T1，A、B 互相配对（B 授予 A `admin`）；浏览器打开 A 的页面 `https://127.0.0.1:19193`。
如果本机 CA 没装进系统信任，先 `asa A cert install`（或在浏览器里手动接受证书警告）。
建议给 A、B 各建一个**名字不同**的测试实例（例如 A 上 `only-on-A`、B 上 `only-on-B`），串数据一眼就能看出来。

**V4-1 远程管理器页**（`/mesh`）

| 区块 | 操作 | 期望 |
|---|---|---|
| 本机 | 打开页面 | 节点 ID（可复制）、协调节点「已连接」与出口地址、Peer 端口「监听 [::]:19194」、直连地址、版本；A 关鉴权时顶部有黄色警告「本机没有开启登录鉴权：任何能打开本页面的人都能控制已配对的机器。」 |
| 本机 | 运行中改备注名并「保存」 | 提示「已保存并应用」，不重启进程即生效：本页与 `GET /api/mesh/status` 的 `label` 更新；B 对 A 发 Hello（B 的页面上「检测」A）看到新名字；协调节点 `node list` 也是新名字。**B 的「能控制本机的机器」列表里的名字不变**——那是 B 配对时自己记下的备注名，是 B 的数据 |
| 顶部 | 依次点「停止」（确认）→「启动」→「重启」 | 标签在「已停止 / 运行中」间切换；停止后 B 对 A 的 Hello 失败，启动后恢复；已停止时「停止」「重启」不可点、运行中「启动」不可点 |
| 协调节点 | 粘贴 join blob（不点按钮） | 输入框下出现「解析结果」：地址、证书指纹、网络 ID、接入密钥「已包含」；已设置过协调节点时逐项标「当前 → 新」或「（未变）」 |
| 协调节点 | 粘贴一个截断的 join blob | 解析结果处报「接入字符串校验失败，可能没有复制完整」，按钮不可点 |
| 协调节点 | **已停止**状态下「保存」/「替换」 | 提示「已保存，启动后生效」，顶部仍是「已停止」（协调节点不会把 mesh 拉起来）；小节里显示新地址与指纹、连接一栏为「管理器互控未启动」 |
| 协调节点 | **运行中**「替换」（确认） | 提示「已保存并应用」，数秒内连接变「已连接」 |
| 本机 | 关掉「监听 Peer 端口」并保存 | Peer 端口一栏变为「不监听」；B 对 A 的 Hello 变成 `relay` |
| 我能控制的机器 | 看 B 那一行，点「检测」 | 在线、路径（内网直连 / 中转）与毫秒数、版本、对方授予本机的角色 |
| 我能控制的机器 | 粘贴 B 新生成的邀请码点「配对」 | 提示「已配对：机B，对方授予本机…」并出现在列表；再粘一次同一串 → 报「邀请码无效或已过期」 |
| 我能控制的机器 | 输入对方节点 ID 点「发起申请」 | 提示「已提交申请，等待对方管理员批准」，该行显示「未授权 / 等待批准」；B 页面批准后在 A 上点「检测」，角色出现 |
| 能控制本机的机器 | 在 **B** 的页面上看 | A 出现在列表，下拉可改角色、可「撤销」；「待批准的申请」可选角色后「批准」/「拒绝」 |
| 邀请码 | 生成 | 弹窗里的整串只显示这一次；关掉后列表里只剩编号、角色、备注、到期时间；「作废」后再用失败 |

**V4-2 切换机器**

1. 顶栏选择器（显示为「本机 ▾」；只在本机 mesh 在运行、且至少有一台机器授权了本机时出现）选 B。
   - 期望：整页重载；菜单下方出现黄色提示条「正在管理远程机器：机B」+ 路径与毫秒数 + 「对方授予本机：管理员」+「回到本机」按钮，
     选择器显示 `机B`；实例列表里是 `only-on-B`，**没有** `only-on-A`。
   - 开发者工具 Network：所有业务请求都打到 `/api/peers/<B 的 ID>/fwd/...`；`/api/auth/*`、`/api/mesh/*` 仍打本机。
2. 新开一个标签页打开 A 的页面。
   - 期望：新标签页是**本机**（选择存在 `sessionStorage`，按标签页隔离）；原标签页刷新后仍是 B。
3. 「回到本机」→ 整页重载，`only-on-A` 回来，没有任何 B 的数据残留（资源监控图表、日志面板、RCON 历史都重新开始）。

**V4-3 远程上下文逐页走查**（选择器选 B 时，每一页都做，并与直接打开 B 的页面 `https://127.0.0.1:19293` 对照）

| 页面 | 要做的操作 | 期望 |
|---|---|---|
| 首页（实例列表） | 看列表、状态；（有实例时）启动 / 停止 / 重启（含倒计时与取消） | 与 B 自己的页面一致；状态变化经 WS 事件实时刷新 |
| 批量操作 | 选两个实例批量停止（有实例时） | 批量日志 SSE 正常、结果与 B 一致 |
| 服务端更新对话框 | 只打开对话框、看状态（**不要**真的点更新，会触发 B 下载） | 显示的是 B 的版本与更新状态 |
| 实例详情 · 概览 | 打开 | B 的数据；资源小图在动 |
| 实例详情 · 基本设置 / 规则 | 改一个无害字段（例如 MOTD）保存，再改回 | B 上 `instances/<实例>/…` 对应文件确实变了又变回 |
| 实例详情 · 配置文件 | 打开 Game.ini / GameUserSettings.ini，保存一次 | 同上 |
| 实例详情 · 插件配置 | 列表、启用 / 禁用一个插件（有 ArkApi 时） | B 上目录随之变化 |
| 实例详情 · 存档备份 | 建一个备份、下载它、删除它 | 下载的文件能在本机打开（哈希与 B 上一致） |
| 实例详情 · 实时日志 | 打开 | 持续滚动、无「每 3 秒重连」 |
| RCON 终端 | 连接、发 `ListPlayers` | 有回应；关掉面板后 B 日志里连接关闭 |
| ArkApi 主程序 / 插件安装对话框 | 上传一个包到暂存、看校验报告、放弃 | 报告与在 B 上传一致 |
| 资源监控页 | 打开、等 1 分钟 | 整机与各实例曲线是 B 的；历史回填（`metrics/history`）有数据 |
| 定时任务 | 新建一个禁用状态的任务、删除 | B 的 `schedules.json` 随之变化 |
| FRP / Syncthing / 文件同步 | 打开、看状态流 | 状态 SSE 在走，显示的是 B 的配置 |
| 系统日志 | 打开 | B 的 `asaServer.log` 内容在滚动 |
| 个人资料 | 打开 | 是 **A 上**的当前用户（不随切换变化） |
| 用户管理 | 菜单里找 | **不可见**；地址栏直接输入 `#/user-manager` 被重定向回首页 |
| 远程管理器 | 菜单里找 | **不可见**；直接输入 `#/mesh` 被重定向回首页 |

**V4-4 异常与提示**

| 场景 | 操作 | 期望 |
|---|---|---|
| 对端离线 | 选择器选 B 后 `Stop-Process $pB.Id`，再在页面上点任意会发请求的操作 | 右上角提示「无法连接远程机器（机B）。可在顶部点「回到本机」。」（同一批请求只提示一次）；提示条点「重新检测」显示「无法连接：…」；**不会**跳到登录页；B 恢复后刷新即可继续 |
| 被对端撤销 | 远程上下文里，在 B 上 `asa B mesh revoke $idA` | 打开中的日志 / 资源流几秒内停止；之后的操作提示「远程机器已不再授权本机」；提示条「重新检测」显示「对方已不再授权本机」；不跳登录页 |
| operator 授权 | B 把 A 改成 operator，远程上下文里点一个管理员才能用的操作（例如 ArkApi 上传） | 页面原有的错误提示里显示「需要管理员权限（对方只授予了本机操作员角色）」，页面不崩；提示条显示「对方授予本机：操作员」 |
| 版本不同 | 用一个改过 `main.go` 里 `appVersion` 的构建跑 B（或用旧版本的 B） | 提示条里出现「对方版本 x，与本机 z 不同，部分页面可能不可用」 |
| 经中转 | 按 V2-2 让路径变成 relay，刷新 | 提示条的路径标签变黄「中转 · N ms」，并显示「经中转：大文件上传 / 下载较慢」 |
| A 关鉴权 | A 的 `auth.enabled: false` | 远程管理器页顶部的警告（见 V4-1）；选择器与「远程管理」菜单照常可用 |
| A 开鉴权、本人是 operator | 用 operator 登录 A | 「远程管理」菜单不出现；`control_role` 为默认的 admin 时选择器也不出现（`/api/mesh/status` 对它 403） |
| 同上，`control_role: operator` | A 的管理员把「谁能使用远程控制」改成「管理员与操作员」，operator 刷新 | 选择器出现、能切到 B 并操作；「远程管理」菜单仍不出现（改配置、配对只属于管理员） |

### 14.6 P6：打洞

前置：T1 / T2 拓扑之上，两台管理器都升级到 P6；**协调节点不用升级**（STUN 的 UDP 3478 / 3479 在 P1 部署时已放行）。
打洞默认开启，开关在页面「本机设置 → 允许打洞」。要让 TCP 直连必败，把两边的「监听 Peer 端口」关掉。

| # | 前置 | 操作 | 期望 |
|---|---|---|---|
| V6-1 | T1 两个管理器，两边都不监听 Peer 端口，B 授权 A | A 的「我能控制的机器」里对 B 点「检测」，隔几秒再点一次 | 第一次「中转」，几秒内「打洞」；A 日志「到 B 的路径已从中转升级为打洞（…）」，B 日志「接受来自 A 的打洞连接（…）」；两边「协调节点」一节显示 NAT 类型与反射地址 |
| V6-2 | 两台真机分处两个家宽 NAT（TCP 直连不通） | 同上，然后开着 B 的日志页 30 分钟 | 「打洞」；NAT 类型「易打洞」；日志 SSE 30 分钟不断（保活有效） |
| V6-3 | 一方用手机 4G 热点 | 同上 | 另一方易打洞时多数能通；双方都是「对称型」时留在中转，对端行「打洞未成功」悬停显示「双方都是对称型 NAT，只能中转」，日志里**没有** 5 秒探测 |
| V6-4 | 任一方有公网 IPv6 | 同上 | 「打洞」，A 日志里升级时的地址是 IPv6 |
| V6-5 | V6-2 的状态 | B 侧拔网线 1 分钟再插回 | A 回到中转（45 秒 QUIC 空闲超时内察觉），恢复后自动再次打通 |
| V6-6 | V6-1 的状态 | B 页面顶部「重启」 | A 的下一次检测**立即**成功（中转），30 秒后再变「打洞」——不能出现 45 秒的不可用 |
| V6-7 | V6-1 的状态 | 运行中关掉 B 的「允许打洞」并保存 | 提示「已保存并应用」；B 的 UDP 端口不再监听（`Get-NetUDPEndpoint -LocalPort 19194` 无输出）；A 立即回中转，30 秒后的那次尝试记「对方关闭了打洞」 |
| V6-8 | Windows 首次运行 | 启动 mesh | 若弹防火墙提示，「取消」后 V6-2 仍能打通（出站状态跟踪）；结论记进验证记录 |
| V6-9 | 中转 + 打洞的切换期 | A 正在看 B 的日志 SSE 时完成升级 | SSE 不断（在途流留在旧中转连接上），新请求走打洞 |

结论（运营商、路由器型号、NAT 类型与成败）回填 §5.6.4。

### 14.7 验证记录

| 编号 | 日期 | 环境（T1 / T2 / WSL） | 结果 | 备注 |
|---|---|---|---|---|
| V1-1 | | | | |
| V1-2 | | | | |
| V1-3 | | | | |
| V1-4 | | | | |
| V1-5 | | | | |
| V1-6 | | | | |
| V1-7 | | | | （NAT 结论回填 §5.6.4） |
| V1-8 | | | | |
| V1-9 | | | | |
| V2-1 | | | | |
| V2-2 | | | | |
| V2-3 | | | | |
| V2-4 | | | | |
| V2-5 | | | | 可选 |
| V2-6 | | | | |
| V2-7 | | | | 回填 P4 提示文案 |
| V2-8 | | | | |
| V3-1 | | | | |
| V3-2 | | | | |
| V3-3 | | | | ⚠️ 三项需要可启动的实例 |
| V3-4 | | | | |
| V3-5 | | | | 记得恢复 `lan_bypass` |
| V3-6 | | | | |
| V3-7 | | | | |
| V3-8 | | | | |
| V3-9 | | | | |
| V3-10 | | | | Windows 服务 / systemd 各一次 |
| V4-1 | | | | |
| V4-2 | | | | |
| V4-3 | | | | 逐页结果可另附 |
| V4-4 | | | | |
| V6-1 | | | | |
| V6-2 | | | | 运营商 / 路由器型号 |
| V6-3 | | | | |
| V6-4 | | | | |
| V6-5 | | | | |
| V6-6 | | | | |
| V6-7 | | | | |
| V6-8 | | | | |
| V6-9 | | | | |

已有的部分结果：
- 2026-10-05 在 Windows 本机回环上对 `stun probe` 做过冒烟（两个端口都回答、结论「无 NAT」），对应 V1-7 的连通部分；VPS / 家宽实测仍待做。
- 2026-10-05 真实二进制冒烟（单机、无协调节点模式、`--tls=false`，详见「P2～P4 实施记录」）覆盖了 V2-4 的同机部分、V3-1（CLI 生成邀请码、
  一次性）、V3-6 全表、V3-7（CLI 撤销切断在途 SSE）、V3-8 第 2 步（WS 同源 101 / 跨源 403）、V3-4 第 2 步。这些项目在 T1（带协调节点、HTTPS）
  与 T2 上仍要按本章完整走一遍。
- 2026-10-05 按实现复核本章（§14 改版）：修正了与实际不符的期望（V2-2 的日志行、V2-3 的身份不符日志、V2-4 的 `listen_addr` 形式、
  V3-4 直连 B 的返回、V3-8 的用户名长度、§14.5 的界面文案与备注名语义），补了 V2-8（协调节点停机不影响直连）、V3-10（服务模式）、
  V3-1 的「改角色不丢响应」与限流两条；V3-8 第 3 步改由单测守住。为让 V2-2 / V2-3 可观察，代码补了两行日志：
  管理器侧「到 X 改走relay：<直连失败的原因>」、协调节点侧「中转 X 结束：…，持续 …，转发 … 字节」。

- 2026-10-07 真实二进制回环冒烟（详见「P6 实施记录」）覆盖了 V6-1、V6-6、V6-7 的同机部分；跨 NAT 的 V6-2～V6-5、V6-8 仍待真机。

**收尾**：全部做完后 `Stop-Process` 掉验证用的进程；T2 的 VPS 上 `asa-coordinator service remove`（若不再使用）；
删除 `C:\mesh-verify` 与 WSL 的 `/opt/mesh-verify`；确认 B 的 `lan_bypass` 已恢复关闭、`New-NetFirewallRule` 加的规则已删除
（`Get-NetFirewallRule -DisplayName mesh-verify-*` 无输出）。
