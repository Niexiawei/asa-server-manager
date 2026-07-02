# 实例进程管理守护进程 — 详细执行文档

---

## 一、背景与目标

### 问题描述

当前 `asa-server.exe` 主进程直接持有游戏服务器进程的句柄，导致：

1. **PTY 路径**（`EnableAsaPlugin=true`）：PTY 文件描述符归主进程所有，主进程退出 → PTY 关闭 → `AsaApiLoader.exe` 随之退出 → 游戏服务器下线
2. **直接 exec 路径**：Windows 进程会话继承机制也可能导致子进程跟随父进程退出
3. **更新困境**：每次更新/重启主应用，玩家都会掉线

### 解决方案

引入独立守护进程 `instance-mgr`，作为 `asa-server.exe` 的一个子命令运行：

- 以 **管理员权限 + 无窗口** 模式运行（`CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP | CREATE_BREAKAWAY_FROM_JOB`）
- **独占持有** 所有游戏服务器进程句柄（PTY / exec.Cmd）和 BadgerDB **写权限**
- 主进程以 **只读模式** 打开同一 BadgerDB，用于读取实例状态历史
- 通过 `github.com/smallnest/rpcx` TCP RPC（`:19194`）通信
- 状态变更通过 **rpcx 双向通信主动推送** 给主进程，主进程广播到 WebSocket
- **所有对外 HTTP API 仅由主进程 `:19193` 提供**，守护进程无独立 HTTP 服务

### 职责边界与 CAS 分层

| 职责 | 主进程 (webapi / batchmanage) | 守护进程 |
|---|---|---|
| **CAS 状态判断** | ✓ 通过 `CompareAndSwapState` RPC，立即响应前端 | 提供 RPC 接口并执行写入 |
| **写状态**（非 CAS） | ✓ 通过 `WriteState` RPC | 提供 RPC 接口并执行写入 |
| **进程启动** | `runStartServerTask` 内部调 `StartProcess` RPC（无 CAS） | ✓ 执行 asaserver.StartServer(WithStatePreset) |
| **进程停止** | `runStopServerTask` 内部调 `StopProcess` RPC（无 CAS） | ✓ 执行 asaserver.StopServer(WithStatePreset) |
| **进程重启** | `runRestartServerTask` 内部调 `RestartProcess` RPC（无 CAS） | ✓ 执行 asaserver.RestartServer(WithStatePreset) |
| **BadgerDB 读**（历史/当前状态） | ✓ 直接只读 | ✓ 读写 |
| **批量操作编排** | ✓ batchmanage 层调 CAS RPC + 进程 RPC | ✗ |
| **对外 HTTP API** | ✓ 唯一出口 | ✗ |
| **状态变更推送** | 接收推送 → WebSocket 广播 | ✓ 主动 SendMessage |
| **守护进程生命周期** | ✓ 通过 `ShutdownDaemon` RPC 停止；通过 `ensureDaemonRunning` 启动 | 提供 `ShutdownDaemon` RPC |

**关键设计原则**：
- CAS 在 `webapi/api.go` 和 `batchmanage/manager.go` 层发起，调用 `CompareAndSwapState` RPC 完成后**立即向前端返回响应**
- 进程控制 RPC（`StartProcess`/`StopProcess`/`RestartProcess`）**不做 CAS**，沿用 `WithStatePreset` 模式
- `webapi/api.go` 和 `webapi/task.go` 的调用结构**不变**，task runner 函数保留，内部从调用 `asaserver.*` 改为调用 RPC

### 架构图

```
Frontend (WebSocket)
       |
Main Process :19193
  webapi/api.go         → CAS RPC → 立即返回 409 or 200
                        → go runXxxServerTask (结构不变)
  webapi/task.go        → runStartServerTask: StartProcess RPC (替换 asaserver.StartServer)
                        → runStopServerTask:  StopProcess RPC
                        → runRestartServerTask: RestartProcess RPC
  batchmanage/manager.go → CAS RPC → 替换 batchDoCAS 中的本地 CAS
                         → 进程 RPC → 替换 executeInstance 中的 asaserver 调用
       |
       | rpcx TCP :19194 (双向)
       | ←→ RPC 调用
       | ←  状态推送（守护进程 SendMessage → 主进程）
       |
Daemon (instance-mgr) — 纯后台进程，无 HTTP 服务
  - CompareAndSwapState / WriteState（state RPC）
  - StartProcess / StopProcess / RestartProcess / ForceStopProcess（process RPC）
  - RegisterListener（注册双向推送）
  - 状态变更时 SendMessage 推送给主进程
  - BadgerDB 独占写，持有所有 PTY/exec 句柄
```

---

## 二、依赖变更

```bash
go get github.com/smallnest/rpcx@latest
go mod tidy
```

---

## 三、新增文件详解

### 3.1 `instancemgr/protocol.go` — 共享 RPC 协议类型

```go
package instancemgr

import "time"

const (
    DaemonRPCPort = 19194
    ServiceName   = "InstanceService"

    PushServicePath   = "InstanceService"
    PushServiceMethod = "StateChange"
)

type InstanceStatus string

const (
    StatusStartStartInitialization           InstanceStatus = "start_initialization"
    StatusStartStartInitializationSuccessful InstanceStatus = "start_initialization_successful"
    StatusStarting                           InstanceStatus = "starting"
    StatusStarted                            InstanceStatus = "started"
    StatusStopping                           InstanceStatus = "stopping"
    StatusStopped                            InstanceStatus = "stopped"
    StatusStartFailed                        InstanceStatus = "start_failed"
    StatusStopFailed                         InstanceStatus = "stop_failed"
    StatusRestartFailed                      InstanceStatus = "restart_failed"
    StatusRestarting                         InstanceStatus = "restarting"
    StatusRestarted                          InstanceStatus = "restarted"
)

// ========== 状态控制 RPC（写入 BadgerDB，由 api/batchmanage 层调用）==========

// CompareAndSwapState：CAS 写入，调用方根据返回值立即向前端响应
type CASArgs struct {
    Name          string           `msgpack:"name"`
    AllowedStatus []InstanceStatus `msgpack:"allowed_status"`
    TargetStatus  InstanceStatus   `msgpack:"target_status"`
}
type CASReply struct {
    OK    bool   `msgpack:"ok"`    // false = 当前状态不在 AllowedStatus 中（→ 前端 409）
    Error string `msgpack:"error"` // 内部错误（→ 前端 500）
}

// WriteState：直接写入状态，不做 CAS（用于 restarting 等编排性状态）
type WriteStateArgs struct {
    Name         string         `msgpack:"name"`
    Status       InstanceStatus `msgpack:"status"`
    ErrorMessage string         `msgpack:"error_message"`
}
type WriteStateReply struct {
    Error string `msgpack:"error"`
}

// ========== 进程控制 RPC（不做 CAS，调用方已完成 CAS 后调用）==========

// StartProcess：启动进程。
// WaitStartup=true  → WithWaitServerCompleted，阻塞到服务器完全可用（task.go 使用）
// WaitStartup=false → 阻塞到初始化阶段成功（StatusStarting），之后立即返回（batchmanage 使用）
// 两种模式均使用 WithStatePreset（CAS 已由调用方完成）。
type StartProcessArgs struct {
    Name        string `msgpack:"name"`
    WaitStartup bool   `msgpack:"wait_startup"`
}
type StartProcessReply struct{ Error string `msgpack:"error"` }

// StopProcess：停止进程（阻塞到进程完全退出）。等同于 StopServer(WithStatePreset)。
type StopProcessArgs  struct{ Name string `msgpack:"name"` }
type StopProcessReply struct{ Error string `msgpack:"error"` }

// RestartProcess：重启进程（阻塞到重启完成）。
// 等同于 RestartServer(WithStatePreset, WithRestartStartupCompletion(noop))。
type RestartProcessArgs  struct{ Name string `msgpack:"name"` }
type RestartProcessReply struct{ Error string `msgpack:"error"` }

// ForceStopProcess：强制杀进程（同步）
type ForceStopProcessArgs  struct{ Name string `msgpack:"name"` }
type ForceStopProcessReply struct{ Error string `msgpack:"error"` }

// ========== 双向推送注册 ==========

type RegisterListenerArgs  struct{}
type RegisterListenerReply struct{ OK bool `msgpack:"ok"` }

// InstanceStateNotify 守护进程推送给主进程的状态快照（msgpack 编码为 SendMessage.Payload）
type InstanceStateNotify struct {
    InstanceName  string         `msgpack:"instance_name"`
    OperationTime time.Time      `msgpack:"operation_time"`
    Status        InstanceStatus `msgpack:"status"`
    ErrorMessage  string         `msgpack:"error_message"`
}

// ========== 守护进程生命周期控制 ==========

// ShutdownDaemon 主进程调用后，守护进程优雅退出（等待所有进程 RPC 完成后关闭）
// 主进程通过 ensureDaemonRunning 可重新拉起守护进程
type ShutdownDaemonArgs  struct{}
type ShutdownDaemonReply struct{ OK bool `msgpack:"ok"` }

// ========== 健康检查 ==========

type HealthArgs  struct{}
type HealthReply struct {
    OK  bool `msgpack:"ok"`
    PID int  `msgpack:"pid"`
}
```

---

### 3.2 `instancemgr/service.go` — RPC 服务实现

```go
package instancemgr

import (
    "asa-server/asaserver"
    "context"
    "net"
    "os"
    "sync"

    "github.com/smallnest/rpcx/server"
    "github.com/vmihailenco/msgpack/v5"
)

type InstanceService struct {
    rpcSrv *server.Server

    connMu        sync.Mutex
    listenerConns map[net.Conn]struct{}
}

func NewInstanceService(rpcSrv *server.Server) *InstanceService {
    return &InstanceService{
        rpcSrv:        rpcSrv,
        listenerConns: make(map[net.Conn]struct{}),
    }
}

// StartStateSubscription 订阅 asaserver 状态变更，变更时向所有已注册连接推送。
func (s *InstanceService) StartStateSubscription(ctx context.Context) {
    subID, ch := asaserver.SubscribeStateChanges(128)
    go func() {
        defer asaserver.UnsubscribeStateChanges(subID)
        for {
            select {
            case state, ok := <-ch:
                if !ok {
                    return
                }
                s.pushStateChange(state)
            case <-ctx.Done():
                return
            }
        }
    }()
}

func (s *InstanceService) pushStateChange(state asaserver.InstanceState) {
    notify := InstanceStateNotify{
        InstanceName:  state.InstanceName,
        OperationTime: state.OperationTime,
        Status:        InstanceStatus(state.Status),
        ErrorMessage:  state.ErrorMessage,
    }
    payload, err := msgpack.Marshal(&notify)
    if err != nil {
        return
    }
    s.connMu.Lock()
    defer s.connMu.Unlock()
    for conn := range s.listenerConns {
        if err := s.rpcSrv.SendMessage(conn, PushServicePath, PushServiceMethod, nil, payload); err != nil {
            delete(s.listenerConns, conn) // 主进程断开，移除
        }
    }
}

// RegisterListener 主进程调用一次，守护进程记录其连接用于后续推送。
func (s *InstanceService) RegisterListener(ctx context.Context, args *RegisterListenerArgs, reply *RegisterListenerReply) error {
    conn, ok := ctx.Value(server.RemoteConnContextKey).(net.Conn)
    if !ok {
        return nil
    }
    s.connMu.Lock()
    s.listenerConns[conn] = struct{}{}
    s.connMu.Unlock()
    reply.OK = true
    return nil
}

// ========== 状态控制 RPC ==========

// CompareAndSwapState CAS 写入，供 api/batchmanage 层调用后立即响应前端。
func (s *InstanceService) CompareAndSwapState(ctx context.Context, args *CASArgs, reply *CASReply) error {
    allowed := make([]asaserver.InstanceStatus, len(args.AllowedStatus))
    for i, st := range args.AllowedStatus {
        allowed[i] = asaserver.InstanceStatus(st)
    }
    ok, err := asaserver.CompareAndSwapInstanceState(
        args.Name, allowed, asaserver.InstanceStatus(args.TargetStatus))
    if err != nil {
        reply.Error = err.Error()
        return nil
    }
    reply.OK = ok
    return nil
}

// WriteState 直接写入状态（不做 CAS，用于 restarting 等编排性写入）。
func (s *InstanceService) WriteState(ctx context.Context, args *WriteStateArgs, reply *WriteStateReply) error {
    if err := asaserver.WriteInstanceState(
        args.Name, asaserver.InstanceStatus(args.Status), args.ErrorMessage); err != nil {
        reply.Error = err.Error()
    }
    return nil
}

// ========== 进程控制 RPC（不做 CAS，调用方已完成 CAS）==========

// StartProcess 对应两种调用模式：
//
//   WaitStartup=true  ← task.go runStartServerTask 使用
//     等价于：StartServer(WithWaitServerCompleted, WithStatePreset)
//     阻塞到 StatusStarted（服务器完全可用），RPC 客户端需配置足够长的 ReadTimeout（建议 10min+）
//
//   WaitStartup=false ← batchmanage executeInstance 使用
//     等价于：StartServer(WithStatePreset)
//     阻塞到 StatusStarting（初始化阶段通过）后返回，后续状态由守护进程监控 goroutine 推送
func (s *InstanceService) StartProcess(ctx context.Context, args *StartProcessArgs, reply *StartProcessReply) error {
    opts := []asaserver.StartServerOptionsFunc{asaserver.WithStatePreset()}
    if args.WaitStartup {
        opts = append(opts, asaserver.WithWaitServerCompleted())
    }
    if err := asaserver.StartServer(args.Name, opts...); err != nil {
        reply.Error = err.Error()
    }
    return nil
}

// StopProcess 与 task.go runStopServerTask 完全一致：StopServer(WithStatePreset)。
// 阻塞到进程完全退出（RCON DoExit / taskkill + 最长 5min 超时）。
func (s *InstanceService) StopProcess(ctx context.Context, args *StopProcessArgs, reply *StopProcessReply) error {
    if err := asaserver.StopServer(args.Name, asaserver.WithStatePreset()); err != nil {
        reply.Error = err.Error()
    }
    return nil
}

// RestartProcess 与 task.go runRestartServerTask 完全一致：
// RestartServer(WithStatePreset, WithRestartStartupCompletion(noop))。
// RestartServer 内部自动追加 WithWaitServerCompleted，阻塞到重启完成。
func (s *InstanceService) RestartProcess(ctx context.Context, args *RestartProcessArgs, reply *RestartProcessReply) error {
    if err := asaserver.RestartServer(args.Name,
        asaserver.WithStatePreset(),
        asaserver.WithRestartStartupCompletion(func(string) {}),
    ); err != nil {
        reply.Error = err.Error()
    }
    return nil
}

// ForceStopProcess 强制杀进程（同步）。
func (s *InstanceService) ForceStopProcess(ctx context.Context, args *ForceStopProcessArgs, reply *ForceStopProcessReply) error {
    if err := asaserver.ForceStopServer(args.Name); err != nil {
        reply.Error = err.Error()
    }
    return nil
}

func (s *InstanceService) Health(ctx context.Context, args *HealthArgs, reply *HealthReply) error {
    reply.OK = true
    reply.PID = os.Getpid()
    return nil
}

// ShutdownDaemon 通知守护进程优雅退出，主进程可随后调用 ensureDaemonRunning 重新拉起。
func (s *InstanceService) ShutdownDaemon(ctx context.Context, args *ShutdownDaemonArgs, reply *ShutdownDaemonReply) error {
    reply.OK = true
    go func() {
        time.Sleep(200 * time.Millisecond) // 等待 RPC 回包发出后再关闭
        s.shutdownFn()                     // 调用 RunDaemon 注入的 cancel 函数
    }()
    return nil
}
```

> `shutdownFn` 字段在 `NewInstanceService` 时注入，指向 `RunDaemon` 的 `ctx cancel` 函数：
> ```go
> type InstanceService struct {
>     rpcSrv        *server.Server
>     connMu        sync.Mutex
>     listenerConns map[net.Conn]struct{}
>     shutdownFn    func() // NEW：由 RunDaemon 注入
> }
> ```

---

### 3.3 `instancemgr/client.go` — RPC 客户端

```go
package instancemgr

import (
    "context"
    "fmt"
    "time"

    "github.com/smallnest/rpcx/client"
    "github.com/smallnest/rpcx/protocol"
    "github.com/vmihailenco/msgpack/v5"
)

type StateChangeHandler func(InstanceStateNotify)

type Client struct {
    c *client.Client
}

// NewClient 连接守护进程，注册双向推送通路。
// handler 在收到状态变更推送时调用（在独立 goroutine 中）。
func NewClient(ctx context.Context, handler StateChangeHandler) (*Client, error) {
    opt := client.DefaultOption
    opt.SerializeType = protocol.MsgPack

    c := client.NewClient(opt)
    if err := c.Connect("tcp", fmt.Sprintf("127.0.0.1:%d", DaemonRPCPort)); err != nil {
        return nil, fmt.Errorf("connect to instance-mgr: %w", err)
    }

    serverMsgCh := make(chan *protocol.Message, 64)
    c.RegisterServerMessageChan(serverMsgCh)

    go func() {
        for {
            select {
            case msg, ok := <-serverMsgCh:
                if !ok {
                    return
                }
                if msg.ServicePath == PushServicePath && msg.ServiceMethod == PushServiceMethod {
                    var notify InstanceStateNotify
                    if err := msgpack.Unmarshal(msg.Payload, &notify); err == nil {
                        handler(notify)
                    }
                }
            case <-ctx.Done():
                return
            }
        }
    }()

    cl := &Client{c: c}
    if err := cl.registerListener(ctx); err != nil {
        c.Close()
        return nil, fmt.Errorf("register listener: %w", err)
    }
    return cl, nil
}

func (c *Client) Close() { c.c.Close() }

func (c *Client) registerListener(ctx context.Context) error {
    return c.c.Call(ctx, ServiceName, "RegisterListener", &RegisterListenerArgs{}, &RegisterListenerReply{})
}

// ========== 状态控制 ==========

// CompareAndSwapState CAS 写入，ok=false 时调用方应向前端返回 409。
func (c *Client) CompareAndSwapState(ctx context.Context, name string,
    allowed []InstanceStatus, target InstanceStatus) (bool, error) {
    args := &CASArgs{Name: name, AllowedStatus: allowed, TargetStatus: target}
    reply := &CASReply{}
    if err := c.c.Call(ctx, ServiceName, "CompareAndSwapState", args, reply); err != nil {
        return false, err
    }
    if reply.Error != "" {
        return false, fmt.Errorf("%s", reply.Error)
    }
    return reply.OK, nil
}

// WriteState 直接写状态（不做 CAS）。
func (c *Client) WriteState(ctx context.Context, name string, status InstanceStatus, errMsg string) error {
    reply := &WriteStateReply{}
    if err := c.c.Call(ctx, ServiceName, "WriteState",
        &WriteStateArgs{Name: name, Status: status, ErrorMessage: errMsg}, reply); err != nil {
        return err
    }
    if reply.Error != "" {
        return fmt.Errorf("%s", reply.Error)
    }
    return nil
}

// ========== 进程控制 ==========

// StartProcess 启动进程。
// waitStartup=true  → 阻塞到 StatusStarted（task.go 使用，ctx 应设置 10min+ 超时）
// waitStartup=false → 阻塞到 StatusStarting（batchmanage 使用，通常几秒返回）
func (c *Client) StartProcess(ctx context.Context, name string, waitStartup bool) error {
    reply := &StartProcessReply{}
    if err := c.c.Call(ctx, ServiceName, "StartProcess",
        &StartProcessArgs{Name: name, WaitStartup: waitStartup}, reply); err != nil {
        return err
    }
    if reply.Error != "" {
        return fmt.Errorf("%s", reply.Error)
    }
    return nil
}

func (c *Client) StopProcess(ctx context.Context, name string) error {
    reply := &StopProcessReply{}
    if err := c.c.Call(ctx, ServiceName, "StopProcess", &StopProcessArgs{Name: name}, reply); err != nil {
        return err
    }
    if reply.Error != "" {
        return fmt.Errorf("%s", reply.Error)
    }
    return nil
}

func (c *Client) RestartProcess(ctx context.Context, name string) error {
    reply := &RestartProcessReply{}
    if err := c.c.Call(ctx, ServiceName, "RestartProcess", &RestartProcessArgs{Name: name}, reply); err != nil {
        return err
    }
    if reply.Error != "" {
        return fmt.Errorf("%s", reply.Error)
    }
    return nil
}

func (c *Client) ForceStopProcess(ctx context.Context, name string) error {
    reply := &ForceStopProcessReply{}
    if err := c.c.Call(ctx, ServiceName, "ForceStopProcess", &ForceStopProcessArgs{Name: name}, reply); err != nil {
        return err
    }
    if reply.Error != "" {
        return fmt.Errorf("%s", reply.Error)
    }
    return nil
}

func (c *Client) Health(ctx context.Context) error {
    return c.c.Call(ctx, ServiceName, "Health", &HealthArgs{}, &HealthReply{})
}

// ShutdownDaemon 请求守护进程优雅退出。
func (c *Client) ShutdownDaemon(ctx context.Context) error {
    reply := &ShutdownDaemonReply{}
    return c.c.Call(ctx, ServiceName, "ShutdownDaemon", &ShutdownDaemonArgs{}, reply)
}

// RestartDaemon 停止守护进程并由主进程重新拉起。
// 调用方负责关闭当前 Client 并在 ensureDaemonRunning 成功后重建 Client。
func (c *Client) RestartDaemon(ctx context.Context) error {
    if err := c.ShutdownDaemon(ctx); err != nil {
        return err
    }
    c.Close()
    // 等待端口关闭后，由调用方调用 ensureDaemonRunning() 重启
    return nil
}
```

---

### 3.4 `instancemgr/daemon.go` — 守护进程入口

```go
package instancemgr

import (
    "asa-server/asaserver"
    "asa-server/logger"
    "context"
    "fmt"
    "net"
    "os"
    "time"

    "github.com/smallnest/rpcx/server"
)

func RunDaemon(ctx context.Context) error {
    l, err := net.Listen("tcp", fmt.Sprintf(":%d", DaemonRPCPort))
    if err != nil {
        return fmt.Errorf("instance-mgr already running (port %d in use): %w", DaemonRPCPort, err)
    }
    l.Close()

    if err := asaserver.InitStateManager(asaserver.BaseDir); err != nil {
        return fmt.Errorf("state manager init failed: %w", err)
    }
    defer asaserver.CloseStateManager()

    rpcSrv := server.NewServer()
    svc := NewInstanceService(rpcSrv)
    svc.StartStateSubscription(ctx)

    if err := rpcSrv.RegisterName(ServiceName, svc, ""); err != nil {
        return fmt.Errorf("register rpc service: %w", err)
    }

    go func() {
        logger.GetLogger().Infof("instance-mgr rpcx listening on :%d (pid=%d)", DaemonRPCPort, os.Getpid())
        if err := rpcSrv.Serve("tcp", fmt.Sprintf(":%d", DaemonRPCPort)); err != nil {
            logger.GetLogger().Errorf("rpcx server exited: %v", err)
        }
    }()

    <-ctx.Done()
    logger.GetLogger().Info("instance-mgr shutting down...")
    shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    rpcSrv.Shutdown(shutCtx)
    return nil
}
```

---

## 四、修改文件详解

### 4.1 `win32api/win32api.go` — 新增常量和两个函数

```go
const (
    CREATE_NO_WINDOW          uint32 = 0x08000000
    CREATE_NEW_PROCESS_GROUP  uint32 = 0x00000200
    CREATE_BREAKAWAY_FROM_JOB uint32 = 0x01000000
)

// RunAsAdminHidden 通过 ShellExecuteW "runas" + SW_HIDE 以管理员无窗口启动。
// 启动的进程由 Windows AppInfo 服务创建，不是调用进程的子进程，父进程退出不影响其生命周期。
func RunAsAdminHidden(args string) error { /* 同 RunAsAdmin，nShowCmd=0 */ }

// LaunchDetached 已是管理员时，以 CREATE_BREAKAWAY_FROM_JOB 完全解耦模式启动子进程。
func LaunchDetached(exe string, args []string) error { /* exec.Command + SysProcAttr 三标志 */ }
```

---

### 4.2 `main.go` — 新增子命令和守护进程检测

**`instance-mgr` 子命令**：无需提前检查权限，运行时如果缺少权限，内部操作（进程创建、BadgerDB 写入）会自然报错。通常不会手动运行，由 `ensureDaemonRunning` 自动拉起。

```go
// 新增子命令
{
    Name:  "instance-mgr",
    Usage: "启动实例管理守护进程（通常由主程序自动拉起，无需手动运行）",
    Action: func(ctx context.Context, cmd *cli.Command) error {
        logger.SetLogMode(logger.HttpApiMode)
        return instancemgr.RunDaemon(ctx)
    },
},
```

**`ensureDaemonRunning()`**：检测 `:19194` 端口，已运行则直接返回；未运行则根据当前是否为管理员选择两个分支——`LaunchDetached`（已提权）或 `RunAsAdminHidden`（触发 UAC 提权），**两个分支都可启动守护进程**，主进程无需本身是管理员。

```go
func ensureDaemonRunning() error {
    rpcAddr := fmt.Sprintf("127.0.0.1:%d", instancemgr.DaemonRPCPort)

    // 快速路径：守护进程已在运行
    if conn, err := net.DialTimeout("tcp", rpcAddr, 500*time.Millisecond); err == nil {
        conn.Close()
        return nil
    }

    exe, err := os.Executable()
    if err != nil {
        return fmt.Errorf("get executable path: %w", err)
    }

    if asaserver.IsElevated() {
        // 已是管理员：直接以 CREATE_BREAKAWAY_FROM_JOB 方式启动（继承已有权限）
        if err := win32api.LaunchDetached(exe, []string{"instance-mgr"}); err != nil {
            return fmt.Errorf("launch instance-mgr: %w", err)
        }
    } else {
        // 非管理员：通过 ShellExecuteW "runas" + SW_HIDE 触发 UAC 提权启动
        if err := win32api.RunAsAdminHidden("instance-mgr"); err != nil {
            return fmt.Errorf("elevate instance-mgr: %w", err)
        }
    }

    // 轮询等待守护进程就绪（最长 15s，每 300ms 检测一次）
    deadline := time.Now().Add(15 * time.Second)
    for time.Now().Before(deadline) {
        time.Sleep(300 * time.Millisecond)
        if conn, err := net.DialTimeout("tcp", rpcAddr, 500*time.Millisecond); err == nil {
            conn.Close()
            return nil
        }
    }
    return fmt.Errorf("instance-mgr did not become ready within 15s")
}
```

`api` 子命令完整的 Action，展示 `ensureDaemonRunning` 的调用位置：

```go
// main.go — api 子命令 Action
Action: func(ctx context.Context, cmd *cli.Command) error {
    // ① 原有逻辑：如果不是管理员则以管理员身份重新启动自身（os.Exit(0) 退出当前进程）
    if !hasArgFlag("--no-admin") {
        ensureAdminElevation()
    }
    // ② 新增：确保守护进程已在运行（未运行则自动拉起，拉起方式见 ensureDaemonRunning）
    if err := ensureDaemonRunning(); err != nil {
        log.Fatalf("failed to start instance manager daemon: %v", err)
    }
    // ③ 启动主服务（内部 Start() 会连接守护进程 RPC）
    return webapi.ActionAPI(ctx, cmd)
},
```

**主服务完整启动时序**：

```
asa-server.exe api
  │
  ├─ ① ensureAdminElevation()          [main.go]
  │     如非管理员 → ShellExecuteW "runas" 以管理员重启 → os.Exit(0)
  │     已是管理员 → 继续
  │
  ├─ ② ensureDaemonRunning()           [main.go]
  │     检测 :19194 端口
  │     ├─ 已监听 → 直接返回（守护进程已在跑）
  │     └─ 未监听 →
  │           IsElevated? 是 → LaunchDetached(exe, ["instance-mgr"])
  │                       否 → RunAsAdminHidden("instance-mgr")
  │           轮询等待 :19194 就绪（最长 15s）→ 返回
  │
  └─ ③ webapi.ActionAPI()              [webapi/actions.go]
        NewAPIServer()
          └─ InitializationBasicComponents()   ← frp/syncthing/batchmanage 初始化
        apiServer.Start()
          ├─ asaserver.InitStateManagerReadOnly(BaseDir)  ← 只读打开 BadgerDB
          ├─ instancemgr.NewClient(ctx, stateChangeHandler)
          │     Connect("tcp", "127.0.0.1:19194")         ← 此时守护进程必定已就绪
          │     RegisterServerMessageChan(ch)             ← 注册双向推送接收通道
          │     RegisterListener RPC                      ← 守护进程记录此连接用于 SendMessage
          └─ HTTP Gin 服务开始监听 :19193
```

> **守护进程必须在 `NewClient` 之前就绪**，`ensureDaemonRunning()` 在步骤 ② 保证了这一点（最长等 15s）。若 15s 内未就绪则主进程 `log.Fatalf` 退出。

**主服务退出时序（正常情况）**：

```
主服务收到 SIGINT / SIGTERM，或用户通过 UI 停止服务
  │
  ├─ apiServer.Stop()                [webapi/actions.go]
  │     ├─ batchmanage.Shutdown()    ← 等待当前批量任务结束
  │     ├─ serverCtxStop()           ← 取消 context，所有 SSE/WS handler 退出
  │     ├─ CloseAllClients()         ← 关闭 WebSocket 连接
  │     ├─ saveDataManager.Stop()
  │     ├─ s.instanceMgrClient.Close()  ← 断开 RPC TCP 连接（仅断连，不发 ShutdownDaemon）
  │     └─ asaserver.CloseStateManager()  ← 关闭只读 BadgerDB
  │
  └─ 主进程退出
       守护进程：TCP 连接断开 → listenerConns 中该连接在下次 SendMessage 时自动清理
       守护进程：继续运行，持有所有游戏服务器进程句柄，游戏服务器不受影响
```

**主服务重启后重连**：

```
asa-server.exe api（再次启动）
  │
  ├─ ensureDaemonRunning()
  │     检测 :19194 → 已监听 → 直接返回（不重复启动守护进程）
  │
  └─ webapi/actions.go Start()
        instancemgr.NewClient()
          Connect("tcp", "127.0.0.1:19194")   ← 重新建立 TCP 连接
          RegisterListener RPC                 ← 守护进程记录新连接，推送通路恢复
```

**主动停止守护进程**（仅在需要完全关停时使用，如系统维护）：

```
主服务通过 HTTP 接口或 CLI 触发 → ShutdownDaemon RPC
  │
  ├─ 守护进程 ShutdownDaemon handler 被调用
  │     reply.OK = true → RPC 回包发出
  │     200ms 后调用 shutdownFn()（context cancel）
  │
  ├─ RunDaemon 的 <-ctx.Done() 解除阻塞
  │     rpcSrv.Shutdown()   ← 等待在途 RPC 完成后关闭监听
  │     CloseStateManager() ← 关闭 BadgerDB
  │     进程退出
  │
  └─ 游戏服务器进程：守护进程退出不主动 kill 游戏进程
         PTY 句柄关闭 → 依赖 AsaApiLoader.exe 的进程会退出（同原有行为）
         直接 exec 启动的进程：因 Job Object 已随守护进程关闭而被系统回收
         ⚠ 因此 ShutdownDaemon 前应确认所有游戏服务器已手动停止
```

> **设计原则**：守护进程的生命周期**独立于主服务**。主服务崩溃、更新、重启均不影响守护进程。只有主服务**主动调用 `ShutdownDaemon` RPC** 才会停止守护进程。`webapi/actions.go` 的 `Stop()` 方法只关闭 RPC 连接，**不调用 `ShutdownDaemon`**。

---

### 4.3 `asaserver/state_manager.go` — 新增只读初始化

```go
// InitStateManagerReadOnly 以只读模式打开 BadgerDB（主进程使用）
func InitStateManagerReadOnly(baseDir string) error {
    // badger.DefaultOptions(path).WithReadOnly(true)
    // 只读模式不启动 stuck-state watcher
}
```

---

### 4.4 `webapi/actions.go` — 连接 RPC 客户端，只读 BadgerDB

```go
type APIServer struct {
    // ...
    instanceMgrClient *instancemgr.Client // NEW
}

func (s *APIServer) Start() error {
    // 只读模式初始化 BadgerDB（用于读取历史状态，守护进程独占写）
    if err := asaserver.InitStateManagerReadOnly(asaserver.BaseDir); err != nil { panic(err) }

    // 连接守护进程，注册双向推送 handler → broadcastInstanceStateChange
    cl, err := instancemgr.NewClient(s.serverCtx, func(notify instancemgr.InstanceStateNotify) {
        broadcastInstanceStateChange(asaserver.InstanceState{
            InstanceName:  notify.InstanceName,
            OperationTime: notify.OperationTime,
            Status:        asaserver.InstanceStatus(notify.Status),
            ErrorMessage:  notify.ErrorMessage,
        })
    })
    s.instanceMgrClient = cl
    // 删除 startStateChangeDispatcher 调用（推送接收已内置在 NewClient handler 中）
}

func (s *APIServer) Stop() error {
    // ...
    if s.instanceMgrClient != nil {
        // 只断开 RPC 连接，不调用 ShutdownDaemon
        // 守护进程会继续运行，持有所有游戏服务器进程句柄
        s.instanceMgrClient.Close()
    }
    // ...
}
```

---

### 4.5 `webapi/state_dispatcher.go` — 删除 `startStateChangeDispatcher`

删除整个 `startStateChangeDispatcher` 方法（推送接收内置于 `instancemgr.NewClient` handler）。

保留 `broadcastInstanceStateChange` 和 `stateToEventFields`，供 `NewClient` handler 调用。

---

### 4.6 `webapi/api.go` — CAS 由 handler 层控制，进程操作交由 task runner

**核心流程**：CAS RPC（快速，立即响应前端）→ `go runXxxServerTask(name)`（结构与原来一致）

```go
func (s *APIServer) startServer(c *gin.Context) {
    name := c.Param("name")
    if err := asaserver.CheckForDuplicatePorts(); err != nil {
        c.JSON(http.StatusConflict, gin.H{"success": false, "error": err.Error()})
        return
    }
    // CAS RPC：立即判断状态冲突，ok=false 则立即 409
    ok, err := s.instanceMgrClient.CompareAndSwapState(c.Request.Context(), name,
        []instancemgr.InstanceStatus{
            instancemgr.StatusStopped, instancemgr.StatusStartFailed,
            instancemgr.StatusStopFailed, instancemgr.StatusRestartFailed, "",
        },
        instancemgr.StatusStartStartInitialization,
    )
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
        return
    }
    if !ok {
        c.JSON(http.StatusConflict, gin.H{"success": false,
            "error": fmt.Sprintf("Server '%s' operation not allowed in current state", name)})
        return
    }
    c.JSON(http.StatusOK, gin.H{"success": true})
    go s.runStartServerTask(name) // 结构与原来完全一致，task runner 内部调 RPC
}

func (s *APIServer) stopServer(c *gin.Context) {
    name := c.Param("name")
    ok, err := s.instanceMgrClient.CompareAndSwapState(c.Request.Context(), name,
        []instancemgr.InstanceStatus{instancemgr.StatusStarted},
        instancemgr.StatusStopping,
    )
    if err != nil { c.JSON(500, ...); return }
    if !ok { c.JSON(409, ...); return }
    c.JSON(http.StatusOK, gin.H{"success": true})
    go s.runStopServerTask(name) // 同上
}

func (s *APIServer) restartServer(c *gin.Context) {
    name := c.Param("name")
    ok, err := s.instanceMgrClient.CompareAndSwapState(c.Request.Context(), name,
        []instancemgr.InstanceStatus{instancemgr.StatusStarted},
        instancemgr.StatusRestarting,
    )
    if err != nil { c.JSON(500, ...); return }
    if !ok { c.JSON(409, ...); return }
    c.JSON(http.StatusOK, gin.H{"success": true})
    go s.runRestartServerTask(name) // 同上
}

func (s *APIServer) forceStopServer(c *gin.Context) {
    name := c.Param("name")
    if err := s.instanceMgrClient.ForceStopProcess(c.Request.Context(), name); err != nil {
        c.JSON(500, ...); return
    }
    c.JSON(http.StatusOK, gin.H{"success": true})
}
```

**状态读取 handler**（`getInstances`、`getInstance`）：**无需修改**，继续直接读只读 BadgerDB。

**`deleteInstance` / `renameInstance`**：先 CAS → stopping，再调 `s.instanceMgrClient.StopProcess(wait=true)` 同步等待停止后继续。

---

### 4.7 `batchmanage/manager.go` — CAS 和进程操作改为 RPC

**变更一：`batchDoCAS` 改用 `CompareAndSwapState` RPC**

原来：
```go
func batchDoCAS(instanceName string, opType BatchOperationType) (bool, error) {
    return asaserver.CompareAndSwapInstanceState(...)
}
```

改为（需注入 client 到 BatchManager）：
```go
func batchDoCAS(instanceName string, opType BatchOperationType, client *instancemgr.Client) (bool, error) {
    switch opType {
    case BatchStart:
        return client.CompareAndSwapState(context.Background(), instanceName,
            []instancemgr.InstanceStatus{
                instancemgr.StatusStopped, instancemgr.StatusStartFailed,
                instancemgr.StatusStopFailed, instancemgr.StatusRestartFailed, "",
            },
            instancemgr.StatusStartStartInitialization)
    case BatchStop:
        return client.CompareAndSwapState(context.Background(), instanceName,
            []instancemgr.InstanceStatus{instancemgr.StatusStarted},
            instancemgr.StatusStopping)
    case BatchRestart:
        return client.CompareAndSwapState(context.Background(), instanceName,
            []instancemgr.InstanceStatus{instancemgr.StatusStarted},
            instancemgr.StatusRestarting)
    }
}
```

**变更二：`executeInstance` 改用进程 RPC**

原来：
```go
err = asaserver.StartServer(instanceName, asaserver.WithStatePreset())
err = asaserver.StopServer(instanceName, asaserver.WithStatePreset())
err = asaserver.RestartServer(instanceName, asaserver.WithStatePreset(), asaserver.WithRestartStartupCompletion(func(string){}))
```

改为（行为与原来完全一致）：
```go
// waitStartup=false → 守护进程内部调用 StartServer(WithStatePreset)，不含 WithWaitServerCompleted
// 阻塞到 StatusStarting 后返回，与 batchmanage 原有调用行为一致
err = op.client.StartProcess(op.ctx, instanceName, false)

// StopServer(WithStatePreset) 行为一致：阻塞到进程完全退出
err = op.client.StopProcess(op.ctx, instanceName)

// RestartServer(WithStatePreset, WithRestartStartupCompletion(noop)) 行为一致
err = op.client.RestartProcess(op.ctx, instanceName)
```

**变更三：`BatchManager` 持有 client 引用**

```go
type BatchManager struct {
    current *BatchOperation
    mu      sync.Mutex
    client  *instancemgr.Client // NEW：由 InitializationBasicComponents 注入
}

// Initialize 改为接收 client 参数
func Initialize(client *instancemgr.Client) {
    globalManager = &BatchManager{client: client}
}
```

`BatchOperation` 也需要持有 client 引用，从 `BatchManager` 传入。

---

### 4.8 `webapi/task.go` — task runner 内部改调 RPC（结构不变）

`webapi/api.go` 的调用结构**完全不变**：CAS 成功后仍然 `go runXxxServerTask(name)`。
task runner 函数保留，只将内部的 `asaserver.*` 调用替换为 RPC：

```go
// runStartServerTask：结构不变，内部调用从 asaserver.StartServer 改为 StartProcess RPC。
// 原来：asaserver.StartServer(name, WithWaitServerCompleted(), WithStatePreset())
// 改为：StartProcess(ctx, name, waitStartup=true) → 守护进程内部同样使用 WithWaitServerCompleted
// 行为完全一致：阻塞到 StatusStarted，由调用方在 goroutine 中执行。
func (s *APIServer) runStartServerTask(name string) {
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute) // 与原来阻塞行为一致
    defer cancel()
    if err := s.instanceMgrClient.StartProcess(ctx, name, true); err != nil {
        logger.GetLogger().Errorf("failed to start server '%s': %v", name, err)
    }
}

// runStopServerTask：结构不变。
// 原来：asaserver.StopServer(name, WithStatePreset())
// 改为：StopProcess RPC → 守护进程内部同样使用 StopServer(WithStatePreset)，行为完全一致。
func (s *APIServer) runStopServerTask(name string) {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
    defer cancel()
    if err := s.instanceMgrClient.StopProcess(ctx, name); err != nil {
        logger.GetLogger().Errorf("failed to stop server '%s': %v", name, err)
    }
}

// runRestartServerTask：结构不变。
// 原来：asaserver.RestartServer(name, WithStatePreset(), WithRestartStartupCompletion(noop))
// 改为：RestartProcess RPC → 守护进程内部调用完全相同的 RestartServer 参数，行为完全一致。
func (s *APIServer) runRestartServerTask(name string) {
    ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
    defer cancel()
    if err := s.instanceMgrClient.RestartProcess(ctx, name); err != nil {
        logger.GetLogger().Errorf("failed to restart server '%s': %v", name, err)
    }
}
```

保留：`runUpdateTask`（文件更新，与进程管理无关，不涉及 RPC）。

---

## 五、执行顺序

| 步骤 | 文件 | 操作 |
|------|------|------|
| 1 | `go.mod` | `go get github.com/smallnest/rpcx@latest && go mod tidy` |
| 2 | `instancemgr/protocol.go` | 新建（含 `ShutdownDaemon` 类型） |
| 3 | `instancemgr/service.go` | 新建（含 `ShutdownDaemon` + `shutdownFn` 字段） |
| 4 | `instancemgr/client.go` | 新建（含 `ShutdownDaemon` + `RestartDaemon` 方法） |
| 5 | `instancemgr/daemon.go` | 新建（注入 `shutdownFn` 到 InstanceService） |
| 6 | `go build ./instancemgr/...` | **验证：无导入循环** |
| 7 | `asaserver/state_manager.go` | 新增 `InitStateManagerReadOnly` |
| 8 | `win32api/win32api.go` | 新增 `RunAsAdminHidden` + `LaunchDetached` + 常量 |
| 9 | `main.go` | 新增 `instance-mgr` 子命令（无权限检查）+ `ensureDaemonRunning()` |
| 10 | `webapi/actions.go` | 只读 BadgerDB + `NewClient`（含推送 handler）；`Stop()` 调 `ShutdownDaemon` |
| 11 | `webapi/state_dispatcher.go` | 删除 `startStateChangeDispatcher`；保留 `broadcastInstanceStateChange` 和 `stateToEventFields` |
| 12 | `webapi/api.go` | handler 层：CAS RPC → `go runXxxServerTask`（结构不变） |
| 13 | `webapi/task.go` | **修改**（保留函数）：内部将 `asaserver.*` 调用替换为对应 RPC |
| 14 | `batchmanage/manager.go` | `batchDoCAS` → CAS RPC；`executeInstance` → 进程 RPC；`BatchManager` 注入 client |
| 15 | `go build ./...` | **验证：整体编译通过** |

---

## 六、调用流程对比

### startServer（单实例）

```
前端
  │ GET /api/server/:name/start
  ▼
webapi/api.go startServer
  │ ① CompareAndSwapState RPC  ←→  daemon：BadgerDB CAS 写入
  │   ok=false → 立即 409
  │   ok=true  → 立即 200 ✓（前端立刻收到响应）
  │ ② go StartProcess RPC      ←→  daemon：asaserver.StartServer(WithStatePreset)
  │                                  内部监控 goroutine 跟踪启动进度
  │                                  状态变更 → SendMessage 推送
  ▼                            ←→  主进程 handler 收到推送 → WebSocket 广播
前端 WebSocket 收到 starting / started / start_failed
```

### 批量启动（batchmanage）

```
batchmanage.runBatchOperation
  for each instance:
    ① batchDoCAS → CompareAndSwapState RPC  （skip if !ok）
    ② executeInstance → StartProcess RPC
    ③ 等待 DelayBetween 后处理下一个实例
```

---

## 七、注意事项

1. **BadgerDB 只读**：主进程调用 `InitStateManagerReadOnly`，守护进程调用 `InitStateManager`，同一路径无冲突。`once.Do` 确保同一进程只能调用一个。

2. **CAS RPC 延迟**：`CompareAndSwapState` 是一次 RPC 往返（~1ms 局域内 TCP），远快于之前 `StartInstance` 内部做 CAS + 进程启动的合并等待。前端响应延迟从"进程启动时间"缩短到"一次 RPC 往返"。

3. **StartProcess 两种模式，与现有调用完全对应**：
   - `WaitStartup=true`（task.go）：守护进程内部 `StartServer(WithWaitServerCompleted, WithStatePreset)`，阻塞到 `StatusStarted`，与原 `runStartServerTask` 行为完全一致。RPC 客户端 ctx 需设置 15min+ 超时。
   - `WaitStartup=false`（batchmanage）：守护进程内部 `StartServer(WithStatePreset)`（不含 `WithWaitServerCompleted`），阻塞到 `StatusStarting` 后返回，与原 `executeInstance` 行为完全一致。

4. **StopProcess / RestartProcess 均阻塞**：`StopServer` 阻塞到进程退出（最长 5min），`RestartServer` 内部自动追加 `WithWaitServerCompleted` 阻塞到重启完成。调用方（task runner / batchmanage）均在 goroutine 或同步批量循环中调用，行为与重构前一致。

5. **BatchManager 注入 client**：`batchmanage.Initialize()` 改为接收 `*instancemgr.Client`，在 `webapi/actions.go` 的 `InitializationBasicComponents()` 中注入。

6. **连接重连**：主进程重启后重新调用 `NewClient` + `RegisterListener` 恢复推送通路。守护进程的旧 `listenerConns` 在下次 `SendMessage` 失败时自动清理。

7. **守护进程生命周期独立于主服务**：主服务正常退出（`Stop()`）只关闭 RPC 连接，不终止守护进程。守护进程在主服务崩溃、更新、重启期间持续运行，游戏服务器不掉线。只有主服务主动调用 `ShutdownDaemon` RPC 才会停止守护进程；停止前应确保所有游戏服务器已停止，否则 PTY 句柄随守护进程关闭，依赖 PTY 的游戏进程将退出。
