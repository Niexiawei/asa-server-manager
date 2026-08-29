# 生产环境 WebSocket / SSE 无法连接：定位与修复方案

> **状态**：待实施
> **现象**：发布到正式环境后，`/api/ws/events`、`/api/ws/rcon` 与所有 SSE 端点
> （日志流、资源监控、批量进度、FRP/Syncthing 状态）连不上；本地开发环境经 Vite 代理一切正常。
> **部署形态**：前端 `//go:embed` 进 Go 二进制，**内网访问不经过 nginx，浏览器直连
> `https://<内网地址>:19193`**。
> **前置阅读**：[HTTP2_CONNECTION_OPTIMIZATION.md](HTTP2_CONNECTION_OPTIMIZATION.md)
> （这次故障正是那次「默认开启 HTTPS + HTTP/2 + 本地 CA」改造落到内网直连形态后暴露出来的）

---

## 1. 一句话结论

**HTTP/2 改造把默认协议从 `http://` 变成了 `https://`，用的是「只在服务器本机被信任」的本地 CA。
从其它内网机器访问时证书不被信任 —— HTML 页面还能靠「仍要继续」点进去，但
`wss://` 和 `EventSource` 对不受信任的证书没有「继续」这个选项，直接连接失败。**

普通 REST 看起来正常，是因为在证书告警页点了「继续」之后，浏览器对该源的 fetch/XHR 放行了；
而 WebSocket 与（多数浏览器版本下的）SSE 不吃这个豁免。

---

## 2. 关键线索与推理

| 事实 | 推论 |
|------|------|
| REST 能用，只有 WS + SSE 连不上 | 不是网络 / DNS / 端口 / 鉴权问题，而是「长连接 / 流式请求」这一类的特殊前提没满足 |
| 改造后才出现 | 变量就是这次引入的：`EnableTLS=true` 默认开、`https://` 直连、本地 CA |
| 服务器本机浏览器打开正常，其它内网机器不行 | CA 只写进了**服务器那台机器**的信任存储（`asa-server cert install` / 启动时 `TrustCA()`），
其它客户端机器从没见过这个 CA |
| 开发环境（Vite 代理）正常 | Vite 的 `proxy` 配了 `secure: false`（不校验后端自签证书）+ `ws: true`，页面本身在明文
`http://localhost:3000`，浏览器对后端证书**根本不做校验** |

### 2.1 为什么 REST 活着、WS/SSE 死了

浏览器打开 `https://192.168.x.x:19193`，本地 CA 未在这台客户端机器上被信任 →
`NET::ERR_CERT_AUTHORITY_INVALID`。用户点「高级 → 继续前往（不安全）」：

| 请求类型 | 证书未受信任 + 已点「继续」后的行为 |
|----------|------------------------------------|
| 导航 / 静态资源 / `fetch` / `XHR` | ✅ 浏览器按「会话内针对该源的一次性例外」放行 —— 所以 REST 看着是好的 |
| **`WebSocket` (`wss://`)** | ❌ Chromium 长期不把该例外应用到 WS 握手，且**没有任何交互入口可以放行**，直接 fail |
| **`EventSource` (SSE)** | ❌ 多数版本同样不继承该例外；`onerror` 静默触发后无限重连，表现就是「连不上」 |

管理面板不该依赖「用户每次点继续」这种脆弱前提，所以**正确的修法是让证书真正被每台客户端信任，
或者内网干脆不上 TLS**（§4）。

### 2.2 顺带排除掉的两条

- **`CheckOrigin`（`internal/realtime/hub.go:36`）不是直连场景的原因。**
  直连时浏览器 `Origin = https://192.168.x.x:19193`，Go 侧 `r.Host` 也正是
  `192.168.x.x:19193`，`strings.Contains(origin, host)` 成立，握手能过。
  （这个函数在**加了 nginx** 之后才会出问题，见 §6；而且它本身写得脆弱，建议一并修。）
- **HTTP/2 不影响 WebSocket。** 直连 h2 下，浏览器会另开一条 HTTP/1.1 over TLS 完成 WS 升级，
  Go 的 `protocolsH1H2()` 保留了 HTTP/1.1，这条路是通的 —— 前提仍然是**证书受信任**。

---

## 3. 如何 5 分钟内确认

在**一台会报错的客户端机器**上（不是服务器本机）：

1. **DevTools → Network**
   - `/api/ws/events`：状态 `(failed)`、`net::ERR_CERT_AUTHORITY_INVALID` / `ERR_CERT_*` → 实锤证书问题。
   - `/api/logs` 一类 SSE：一直 `pending` 且 body 空，Console 有 `ERR_CERT_*` 或 `net::ERR_CERT` → 同上。
2. **地址栏**：`https://<内网地址>:19193` 是不是「不安全」/ 红色感叹号？是 → 证书未受信任。
3. **命令行看 SAN 与信任链**：
   ```bash
   openssl s_client -connect 192.168.x.x:19193 -servername 192.168.x.x </dev/null 2>/dev/null \
     | openssl x509 -noout -issuer -subject -ext subjectAltName
   ```
   - `issuer` 是 `ASA Server Manager Local CA` 且该 CA 未导入本机 → 不受信任（预期内）。
   - 若你**已经**在客户端导入了 CA 却仍失败：看 `X509v3 Subject Alternative Name` 里**有没有你访问用的那个地址**
     （IP 或主机名）。没有 → SAN 不匹配（§5.3）。
4. **服务器上确认信任状态**：
   ```
   asa-server cert status
   ```
   `受信任: 是（LocalMachine\Root）` 只代表**服务器本机**信任，不代表客户端信任。

---

## 4. 修复方案（内网直连，无 nginx）

按内网场景的推荐度排序。**选一个即可**，§4.1 最省事。

### 4.1 内网就别上 TLS —— `--tls=false`（推荐，改动最小）

内网、少量并发用户、已有物理/网络访问控制的场景，TLS 带来的信任分发成本 > 收益。
关掉 TLS 后：`http://<内网地址>:19193`，前端自动用 `ws://` 和明文 SSE，**证书问题彻底消失**。

```bash
asa-server api --tls=false
# 或 service 模式：改 config.yaml
```

```yaml
# {BaseDir}/config.yaml
server:
  tls:
    enabled: false
```

代价：退回 HTTP/1.1 的「每源 6 条连接」限制（[HTTP2 文档 §1.3](HTTP2_CONNECTION_OPTIMIZATION.md) 的
「SSE 饿死 REST」问题理论上会回来）。但内网通常一两个用户、同时开的流有限，6 条一般够用；
真不够时再走 §4.2 上 TLS。

> 关掉 TLS 后确认 `.env` 不影响生产：生产前端从 `window.location` 推导协议，`http` 页面自然用 `ws://`，
> 无需重新构建。只有本地开发要把 `app/.env.development` 的 `VITE_PROXY_TARGET` 改回 `http://localhost:19193`。

### 4.2 保留 TLS，把本地 CA 分发到每台客户端

要 HTTP/2 的多路复用收益、或内网里有较多并发流时，走这条。

1. **拿到 CA 证书**：服务器上 `{BaseDir}/certs/ca.crt`（`asa-server cert status` 会打印 `证书目录`）。
2. **导入每台客户端机器的「受信任的根证书颁发机构」**：
   - Windows（推荐机器级，覆盖所有用户 + Chrome/Edge/Firefox enterprise roots）：
     ```powershell
     Import-Certificate -FilePath ca.crt -CertStoreLocation Cert:\LocalMachine\Root
     ```
     或 `certutil -addstore -f Root ca.crt`（管理员）。域环境可用 GPO 批量下发。
   - macOS：钥匙串 →「系统」→ 拖入 → 对该证书「始终信任」。
   - Firefox（不读系统库时）：设置 → 隐私与安全 → 证书 → 导入，勾「信任由此 CA 标识的网站」。
3. **确认访问地址在叶子证书 SAN 内**（见 §5.3），不在就用 `--tls-domains` 补。
4. 客户端刷新，`https://<内网地址>:19193` 应无警告，WS/SSE 恢复。

> CA 轮换：叶子证书会自动重签，但 **CA 本身 10 年有效且指纹稳定**，只需分发一次。
> 只有删库重建 `{BaseDir}/certs/` 才会换 CA。

### 4.3 用客户端已经信任的证书 —— `--cert-file` / `--key-file`

内网有自建 PKI，或有一个能签内网域名的证书（哪怕是公网 CA 签的内网 DNS 名），直接喂给 asa-server：

```bash
asa-server api --cert-file /path/asa.crt --key-file /path/asa.key
```

给了自备证书后**不再生成本地 CA、不碰系统信任存储**。客户端只要本来就信任签发方即可，零分发。

### 4.4 （可选增强）加一个 CA 下载入口

现状要用户 SSH / 远程桌面到服务器去拷 `ca.crt`，不顺手。可加：

- 一个**免鉴权**的 `GET /ca.crt`（CA 是公钥证书，公开无风险），返回 `application/x-x509-ca-cert`。
- GUI / 首启日志里打印 CA 指纹 + 一句「其它机器访问需导入 `http://<地址>:19193/ca.crt`」。
  注意下载入口本身要能明文访问（否则又是先有信任才能下载的死锁）——
  可在 `--tls` 开启时**额外**监听一个明文端口只服务 `/ca.crt` 与 `/health`，或允许 `http://` 访问该单一路径。

这条是体验优化，不是必需；§4.1 / §4.2 已能解决故障。

---

## 5. 已经上了 TLS 却仍失败的三个坑

### 5.1 CA 只装在服务器本机

`asa-server cert install` 和启动时的自动 `TrustCA()` 都**只写当前这台机器**的信任存储
（`internal/certmgr/store_windows.go`：优先 `LocalMachine\Root`，未提权退到 `CurrentUser\Root`）。
其它客户端机器必须各自导入（§4.2 第 2 步）。`CurrentUser\Root` 还只对**装它的那个 Windows 账号**有效，
换个账号登录同一台机器也不认 —— 服务模式以 LocalSystem 跑时写的是 `LocalMachine`，一般没这问题。

### 5.2 未提权运行导致压根没写进存储

GUI / `asa-server api` 以普通权限运行且用户拒绝了 UAC 时，`TrustCA()` 可能两个存储都没写成。
`asa-server cert status` 显示 `受信任: 否` 就是这种情况。手动 `asa-server cert install --machine`
（会请求提权）或按 §4.2 手工导入。

### 5.3 访问地址不在叶子证书 SAN 里

叶子 SAN 覆盖：`localhost`、`os.Hostname()`、回环、**枚举到的各网卡 IP**
（`internal/certmgr/ca.go:206` `desiredSANs`）。以下访问方式会 SAN 不匹配、即使 CA 已信任也报
`ERR_CERT_COMMON_NAME_INVALID`：

- 用 mDNS 名 `xxx.local`、NetBIOS 名、或任何 ≠ `os.Hostname()` 的 DNS 名访问；
- 用 NAT / 端口映射后的地址，或证书签发后才新增的网卡 IP（会在下次启动自动重签，但要重启一次）。

**修**：`asa-server api --tls-domains=asa.intra,other-name`（逗号分隔，写进 SAN），或改用 §4.3 自备证书。
`asa-server cert status` 的 `SAN:` 行可核对当前覆盖范围。

---

## 6. 如果之后又在外网加 nginx（对内直连仍并存）

内网直连 + 外网经 nginx 反代**可以同时存在**（[HTTP2 文档 §6.2](HTTP2_CONNECTION_OPTIMIZATION.md)）。
但 nginx 这条路会额外踩两个坑，提前记下：

### 6.1 nginx 默认配置会掐断 WS / SSE

```nginx
map $http_upgrade $connection_upgrade { default upgrade; '' close; }

server {
    listen 443 ssl;
    http2 on;
    server_name asa.example.com;
    ssl_certificate /etc/ssl/asa.crt;
    ssl_certificate_key /etc/ssl/asa.key;

    location / {
        proxy_pass https://127.0.0.1:19193;

        proxy_http_version 1.1;                      # 默认 1.0，无法 Upgrade，SSE chunked 也会坏
        proxy_set_header Upgrade    $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host       $host;           # ★ 必须透传原始 Host，否则 §6.2 的 CheckOrigin 挂
        proxy_set_header X-Forwarded-Host  $host;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        proxy_buffering    off;                      # 默认 on，SSE 会被攒住 → 前端「收不到任何 data」
        proxy_read_timeout 3600s;                    # 默认 60s，空闲 SSE/WS 被断
        proxy_ssl_verify   off;                      # 上游是本地 CA 签的自签证书
        proxy_ssl_server_name on;
    }
}
```

Caddy 对应加 `reverse_proxy` 内 `flush_interval -1`（关缓冲），Upgrade / Host 透传是默认行为。

### 6.2 顺手加固 `CheckOrigin`

`internal/realtime/hub.go:36` 现在是 `strings.Contains(origin, r.Host)`：

- nginx 不透传 Host 时 `r.Host` 变成 `127.0.0.1:19193`，与对外域名 `Origin` 不匹配 → 握手 403，
  日志：`WebSocket upgrade error: websocket: request origin not allowed by Upgrader.CheckOrigin`。
- 子串匹配还有安全问题：`Host: asa.example.com` 会放行 `Origin: https://asa.example.com.evil.com`。

建议改为「解析 Origin 的 host 做精确比较」，允许集合 = 回环 ∪ `X-Forwarded-Host`/`r.Host`
∪ 复用 `appconfig.Get().Server.CORS.AllowedOrigins`：

```go
func checkOrigin(r *http.Request) bool {
    origin := r.Header.Get("Origin")
    if origin == "" {
        return true
    }
    u, err := url.Parse(origin)
    if err != nil || u.Host == "" {
        return false
    }
    oh := strings.ToLower(u.Hostname()) // 去端口
    switch oh {
    case "localhost", "127.0.0.1", "::1":
        return true
    }
    reqHost := r.Header.Get("X-Forwarded-Host")
    if reqHost == "" {
        reqHost = r.Host
    }
    if h, _, err := net.SplitHostPort(reqHost); err == nil {
        reqHost = h
    }
    if strings.EqualFold(oh, reqHost) {
        return true
    }
    for _, a := range appconfig.Get().Server.CORS.AllowedOrigins {
        if p, err := url.Parse(a); err == nil && strings.EqualFold(p.Hostname(), oh) {
            return true
        }
    }
    return false
}
```

若不想让 `realtime` 依赖 `appconfig`，用与 `AuthGate` 相同的手法在 `webapi` 启动时把白名单注入进来。

### 6.3 顺带：统一 SSE 响应头

只有 `serverapi/serverapi.go:218` 一处 SSE 发了 `X-Accel-Buffering: no`，`logapi` / `batchmanage` /
`frpmanage` / `syncthingmanage` 都没发。抽一个公共 helper 统一设置
（`Content-Type: text/event-stream` + `Cache-Control: no-cache` + `X-Accel-Buffering: no`），
并删掉 `logapi.go:41` 手写的 `Access-Control-Allow-Origin: *`（与 `gin-contrib/cors` 冲突）。
这样即使反代忘了关缓冲，也有应用层兜底。

---

## 7. 验证清单

- [ ] 在**其它内网机器**（非服务器本机）上，`https://<内网地址>:19193` 打开无证书警告。
- [ ] DevTools Network：`/api/ws/events` 状态 **101**；`/api/logs` 立即持续吐 data。
- [ ] `asa-server cert status`：`受信任` 状态符合预期；`SAN:` 行包含实际访问用的地址。
- [ ] （若走 §4.1）`http://<内网地址>:19193` 下 WS 为 `ws://` 且连接成功。
- [ ] 页面挂机 10 分钟 WS 不掉线；掉线能自动重连。
- [ ] 一台标签页触发批量停服，另一台标签页同步看到状态变化（`realtime` 全局中枢正常）。
- [ ] （若加了 nginx）`curl -Nk https://127.0.0.1:19193/api/logs` 本机直打与经域名访问行为一致。

---

## 8. 参考

- [HTTP2_CONNECTION_OPTIMIZATION.md](HTTP2_CONNECTION_OPTIMIZATION.md) §4.4（证书策略）、§4.5（WS 走 HTTP/1.1）、§6（兼容反代）
- `internal/certmgr/ca.go` `desiredSANs` —— 叶子证书 SAN 覆盖范围
- `internal/certmgr/store_windows.go` —— 信任存储写入（LocalMachine / CurrentUser）
- `internal/certmgr/cli.go` —— `asa-server cert status / install / uninstall`
- `internal/realtime/hub.go` —— `WSUpgrader` / `CheckOrigin`
- `internal/webapi/actions.go:152-192` —— 监听、TLS 开关与失败降级
