# AGENTS.md - ASA Server Manager

## Project Overview

ASA Server Manager is a **Windows-only** management tool for ARK: Survival Ascended (ASA) dedicated game servers. Written in Go with a Vue.js frontend, it provides a GUI (Fyne), HTTP API (Gin), CLI, and Windows service integration for managing ARK server instances.

**Module path**: `asa-server`
**Minimum Go version**: 1.25.4
**Platform**: Windows 10/11 (64-bit) only

## Build & Run

```bash
# Backend build
go build -o asa-server.exe

# Frontend build (requires Node.js 16+)
cd app && npm install && npm run build

# Run GUI (default if no args)
./asa-server.exe

# Run API server
./asa-server.exe api [--port 19193]

# Install as Windows service
./asa-server.exe service install
```

## Project Structure

```
asa-server/
├── main.go                  # Entry point: CLI, GUI, Windows service detection
├── asaserver/               # Core: instance lifecycle, config, RCON, installer, state
│   ├── config.go            # Directory layout, InstanceConfig, INI read/write
│   ├── server.go            # Start/Stop/Restart server, RCON commands
│   ├── common.go            # Log tailing (fsnotify), file utils, mod extraction
│   ├── installer.go         # SteamCMD download, ARK server update
│   └── state_manager.go     # BadgerDB-backed instance state persistence
├── webapi/                  # HTTP API + WebSocket + SSE
│   ├── actions.go           # APIServer struct, route registration, Start/Stop
│   ├── api.go               # All HTTP handlers (~1850 lines)
│   ├── broadcast.go         # TaskBroadcaster pub/sub for SSE streaming
│   ├── task.go              # Background task runners (update, batch ops)
│   └── ws.go                # WebSocket event broadcast + RCON handlers
├── gui/                     # Fyne desktop GUI (system tray, service mgmt, log viewer)
├── winservice/              # Windows service integration (kardianos/service)
├── actions/                 # CLI command handlers (update)
├── backup/                  # tar+zstd backup/restore with functional options
├── frpmanage/               # FRP reverse proxy management (embedded frpc.exe)
├── syncthingmanage/         # Syncthing file sync management (embedded syncthing.exe)
├── processjob/              # Windows Job Object process tree management
├── serverinfo/              # CPU/memory/process metrics (gopsutil)
├── win32api/                # Windows API interop (user32/kernel32, process checks)
├── common/                  # Shared utilities (DNS resolution, WMI queries)
├── githubreleases/          # GitHub Releases API client with download progress
├── logger/                  # Zap + lumberjack structured logging with rotation
├── app/                     # Embedded Vue.js frontend (//go:embed dist)
│   ├── appembed.go          # Embeds dist/ for Gin static serving
│   └── src/                 # Vue.js source (TDesign components)
└── docs/                    # Documentation
```

## Key Packages

### `asaserver` - Core Logic
- `InstanceConfig`: ServerName, ServerPassword, ServerAdminPassword, MaxPlayers, MapName, RCONPort, QueryPort, Port, ModIDs, SaveDir, ClusterID, CustomStartParameters, EnableAsaPlugin, BindDomain, MessageOfTheDay
- `StartServer()`: Builds command line, creates NTFS junctions for per-instance config, supports both direct `ArkAscendedServer.exe` and `AsaApiLoader.exe` startup
- `StopServer()`: Sends RCON `saveworld`, then `DoExit` or `taskkill`
- `SendRCONCommand()`: Uses `gorcon/rcon` with 3 retries
- `SetupInstanceConfig()`: Creates NTFS junction from base server config to instance's Config dir

### `webapi` - HTTP API
- Uses Gin with CORS, static file serving for embedded SPA
- Long-running operations (start/stop/restart/update) stream progress via SSE using `TaskBroadcaster`
- WebSocket endpoints for global server events and interactive RCON
- Default port: **19193**

### `backup` - Backup/Restore
- Format: `.zstd` (tar + zstd compression, world save only — instance/game config is synced via the separate `/api/config/sync*` feature, not backed up here)
- Naming: `{instanceName}_{timestamp}.zstd`

### `frpmanage` / `syncthingmanage` - Embedded Binaries
- Both embed `.exe` files via `//go:embed`
- Extracted to `{baseDir}/frp/` and `{baseDir}/syncthing/` at runtime
- MD5 check to avoid re-extraction
- Managed as child processes with lifecycle control

### `processjob` - Windows Job Objects
- `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` ensures child process tree cleanup
- Used by Syncthing manager for reliable process tree termination

### `state_manager` - Persistent State
- BadgerDB stored at `{baseDir}/database_file/state_db`
- Instance statuses: start_initialization, start_initialization_successful, starting, started, stopping, stopped, start_failed, stop_failed, restart_failed, restart
- Max 500 records per instance with automatic cleanup

## Key Data Flows

```
webapi --> asaserver (all instance lifecycle operations)
asaserver --> win32api (process management, port checks)
asaserver --> common (WMI queries, DNS resolution)
frpmanage/syncthingmanage --> processjob (process tree management)
backup --> asaserver (config loading, directory paths)
gui --> asaserver, serverinfo, winservice
logger --> all packages (structured logging)
app --> webapi (embedded SPA served by Gin)
```

## Directory Layout at Runtime

```
{BaseDir}/
├── instances/
│   └── {instance_name}/
│       ├── instance_config.ini
│       ├── Config/
│       │   ├── Game.ini
│       │   └── GameUserSettings.ini
│       └── server.log
├── server-files/            # Base ARK server installation
├── steamcmd/                # SteamCMD
├── backups/                 # Backup archives (.zstd)
├── frp/                     # Extracted frpc.exe
├── syncthing/               # Extracted syncthing.exe
├── database_file/           # BadgerDB state
├── logs/                    # asaServer.log, arkApiLog.log
└── log_mapping.json         # Instance-to-logfile mappings
```

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET/POST | `/api/instances` | List / create instances |
| GET/PUT/DELETE | `/api/instances/:name` | Get / update / delete instance |
| POST | `/api/instances/:name/rename` | Rename instance |
| POST | `/api/server/:name/start` | Start instance (SSE) |
| POST | `/api/server/:name/stop` | Stop instance |
| POST | `/api/server/:name/restart` | Restart instance (SSE) |
| POST | `/api/server/start-all` | Start all instances |
| POST | `/api/server/stop-all` | Stop all instances |
| POST | `/api/server/update` | Update server (SSE) |
| GET | `/api/server/info` | System resources (SSE, 2s interval) |
| POST | `/api/rcon/:name/command` | Send RCON command |
| POST/GET | `/api/backup/:name` | Create / list backups |
| POST | `/api/backup/:name/restore` | Restore backup |
| GET | `/api/logs/:name` | Stream game logs (SSE) |
| GET | `/api/logs` | Stream system logs (SSE) |
| GET/PUT | `/api/config/:name/game-ini` | Read / write Game.ini |
| GET/PUT | `/api/config/:name/game-user-settings` | Read / write GameUserSettings.ini |
| POST | `/api/config/sync` | Sync config from base server to instances |
| POST | `/api/config/sync-instance` | Sync config between instances |
| GET | `/api/ws/events` | WebSocket server events |
| GET | `/api/ws/rcon` | WebSocket interactive RCON |
| GET/PUT/POST | `/api/frp/*` | FRP config, status, lifecycle |
| GET/PUT/POST | `/api/syncthing/*` | Syncthing config, status, lifecycle |

## Key Dependencies

- **Gin** (`github.com/gin-gonic/gin`) - HTTP framework
- **Fyne** (`fyne.io/fyne/v2`) - Desktop GUI
- **gopsutil** (`github.com/shirou/gopsutil/v4`) - System metrics
- **BadgerDB** (`github.com/dgraph-io/badger/v4`) - Persistent state store
- **gorcon/rcon** (`github.com/gorcon/rcon`) - Game RCON protocol
- **fsnotify** (`github.com/fsnotify/fsnotify`) - File system notifications (log tailing)
- **go-pty** (`github.com/aymanbagabas/go-pty`) - Pseudo-terminal for interactive processes
- **lumberjack** (`gopkg.in/natefinch/lumberjack.v2`) - Log rotation
- **kardianos/service** (`github.com/kardianos/service`) - Windows service
- **urfave/cli** (`github.com/urfave/cli/v3`) - CLI framework
- **zstd** (via tar+zstd) - Backup compression
- **imroc/req** (`github.com/imroc/req/v3`) - HTTP client (GitHub releases)
- **jinzhu/copier** (`github.com/jinzhu/copier`) - Struct copying for config updates
- **gopsutil** (`github.com/shirou/gopsutil/v4`) - System metrics + process queries

## Development Notes

- The project is Windows-only; `main.go` checks `runtime.GOOS` and exits on non-Windows
- No tests exist in the codebase (though `common_test.go` and `config_test.go` files exist in `asaserver/`)
- The frontend uses TDesign Vue components
- FRP and Syncthing executables are embedded via `//go:embed` - rebuild the binary to update them
- Instance state is persisted in BadgerDB, not in memory - survives restarts
- Server startup uses NTFS junctions to share the base server config while allowing per-instance customization
- Long-running operations stream progress via SSE, not plain HTTP responses
- The API server uses a mutex (`serverActionsLock`) to prevent concurrent start/stop operations

### GUI (Fyne) Layout Rules

- **Fixed-width panels**: Fyne's `container.NewBorder` uses `MinSize()` to allocate space for edge children (left/right/top/bottom), **not** `Resize()`. Calling `Resize()` on a child of Border layout has no effect — the layout manager overrides it.
- **Do NOT use** `container.NewGridWrap(fyne.NewSize(width, 0), ...)` — height `0` causes blank panels. `GridWrap` forces exact dimensions, which conflicts with dynamic layouts.
- **Do NOT use** `container.NewHBox` for left-right layouts — HBox distributes space based on min sizes and does not expand children to fill remaining space.
- **Left-right split layout**: Use `container.NewHSplit` with `SetOffset()` for proportional splits. Example: left 40% + right 60%.
- **Scrollable panels**: Wrap content in `container.NewVScroll()` when it may exceed available height.
- **`fyne.Do(func()`**: All UI updates from goroutines must be wrapped in `fyne.Do()` to run on the main thread.
- **Custom theme**: The GUI uses a custom theme (`myTheme`) — do not use `theme.` functions for sizing, use the custom theme's methods.
