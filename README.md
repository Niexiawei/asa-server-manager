# ASA Server Manager

A comprehensive ARK Server Ascended (ASA) server management tool built with Go and Vue.js, featuring a command-line interface, HTTP API, and web dashboard.

## Features

### Core Functionality
- ✅ **Instance Management**: Create, list, rename, and delete server instances
- ✅ **Server Control**: Start, stop, and restart individual or all instances
- ✅ **RCON Support**: Send RCON commands to server instances
- ✅ **Backup & Restore**: Create and restore world backups with flexible options
- ✅ **Configuration Management**: View and edit game configuration files (Game.ini, GameUserSettings.ini)
- ✅ **Logging**: Real-time server and instance logs viewing

### Advanced Features
- ✅ **Windows Service Integration**: Run as a Windows service for background operation
- ✅ **HTTP API Server**: RESTful API with WebSocket and SSE support for real-time updates
- ✅ **Web Dashboard**: Embedded Vue.js web interface for intuitive management
- ✅ **FRP Integration**: Built-in Fast Reverse Proxy management for server exposure
- ✅ **Syncthing Integration**: File synchronization capabilities for server configuration
- ✅ **Port Management**: Automatic port conflict detection and resolution

## System Requirements

- **Operating System**: Windows 10/11 (64-bit)
- **Go Version**: 1.26 or higher (for development)
- **Node.js**: 16.x or higher (for frontend development)
- **Disk Space**: Minimum 20GB for server files and instances
- **RAM**: 8GB minimum, 16GB recommended

## Installation

### Pre-built Binaries

1. Download the latest release from the [Releases](https://github.com/yourusername/asa-server/releases) page
2. Extract the archive to your preferred location
3. Run `asa-manager.exe` to start the application

### Building from Source

#### Backend (Go)
```bash
go mod tidy
go build -o asa-manager.exe
```

#### Frontend (Vue.js)
```bash
cd app
npm install
npm run build
```

## Usage

### Command-Line Interface

#### Basic Commands

```bash
# List all instances
asa-manager list

# Create a new instance
asa-manager create

# Start an instance
asa-manager start <instance_name>

# Stop an instance
asa-manager stop <instance_name>

# Restart an instance
asa-manager restart <instance_name>

# Check server status
asa-manager status [instance_name]
```

#### Backup & Restore

```bash
# Create a backup
asa-manager backup <instance_name> <world_folder>

# Restore a backup
asa-manager restore <instance_name> <backup_file> [flags]
```

Available restore flags:
- `--worldfile`: Restore world file (SaveDir)
- `--instance-config`: Restore instance_config.ini
- `--game-config`: Restore game config files

#### Batch Operations

```bash
# Start all instances
asa-manager start-all

# Stop all instances
asa-manager stop-all
```

#### Configuration Management

```bash
# View Game.ini for an instance
asa-manager view-game [instance_name]

# View GameUserSettings.ini for an instance
asa-manager view-game-user-settings [instance_name]

# Synchronize game config files
asa-manager sync-config <instance_name> [instance_name2] [...]
```

#### Windows Service Management

```bash
# Install as Windows service
asa-manager service install

# Start Windows service
asa-manager service start

# Stop Windows service
asa-manager service stop

# Remove Windows service
asa-manager service remove
```

#### HTTP API Server

```bash
# Start HTTP API server
asa-manager api
```

The API server runs on port 19193 by default.

### Web Dashboard

Once the HTTP API server is running, access the web dashboard at:
```
http://localhost:19193
```

The dashboard provides a user-friendly interface for:
- Managing server instances
- Controlling server operations
- Viewing real-time logs
- Editing configurations
- Managing backups
- Monitoring server resources

## Project Structure

### Directory Structure

The former `asaserver` god-package has been split by single responsibility into the domain
packages below; pure utilities live under `pkg/`. See
[docs/PACKAGE_RESTRUCTURE_PLAN.md](docs/PACKAGE_RESTRUCTURE_PLAN.md) for the rationale.

```
asa-server/
├── main.go              # Entry point: CLI, GUI, Windows service detection
│
│  ── Domain packages (bottom-up, no cycles) ──
├── pkg/                 # Leaf utilities: fsutil, winproc, netutil, tail, console, iox,
│                        #   processjob (Windows Job Objects), serverinfo (gopsutil metrics)
├── config/              # Directory layout, InstanceConfig, INI read/write, config sync
├── process/             # PID store + IsServerRunning (breaks the state <-> instance cycle)
├── rconx/               # RCON connection & command execution (retries, sentinel errors)
├── realtime/            # WebSocket hub: server events + interactive RCON
├── state/               # BadgerDB instance state persistence (CAS state machine)
├── installer/           # SteamCMD download / ARK server update
├── mirror/              # Per-instance NTFS junction mirrors
├── instance/            # Lifecycle: Start/Stop/Restart, saves, mod extraction, ASA version
├── countdown/           # Delayed stop/restart orchestration: countdown + in-game announcements
├── batchmanage/         # Batch start/stop/restart across instances (see docs/BATCH_OPERATION.md)
├── schedule/            # Cron-like scheduled tasks (restart / update)
├── updatemanage/        # Server update task singleton
│
│  ── Interfaces ──
├── webapi/              # HTTP API, split by domain: instanceapi, serverapi, backupapi,
│                        #   configapi, saveapi, logapi, iconapi, apiresp
├── app/                 # Embedded Vue.js frontend (//go:embed dist)
├── gui/                 # Fyne desktop GUI (system tray, service mgmt, log viewer)
├── winservice/          # Windows service integration (kardianos/service)
├── actions/             # CLI command handlers
│
│  ── Supporting ──
├── backup/              # tar+zstd backup/restore with functional options
├── frpmanage/           # FRP reverse proxy management (embedded frpc.exe)
├── syncthingmanage/     # Syncthing file sync management (embedded syncthing.exe)
├── parseserver/         # ARK save file parsing
├── logger/              # Zap + lumberjack structured logging with rotation
└── docs/                # Documentation (see index below)
```

### Instance Directory Structure

```
instances/
└── <instance_name>/
    ├── instance_config.ini    # Instance-specific configuration
    ├── Config/                # Game configuration files
    │   ├── Game.ini
    │   └── GameUserSettings.ini
    └── server.log             # Server log file
```

## HTTP API

The HTTP API provides RESTful endpoints for all server management functions. For detailed documentation, see the [API Reference](docs/API_REFERENCE.md).

### Key API Endpoints

- `GET /health` - Health check
- `GET /api/instances` - List all instances
- `POST /api/instances` - Create a new instance
- `GET /api/instances/:name` - Get instance status
- `POST /api/server/:name/start` - Start an instance
- `POST /api/server/:name/stop` - Stop an instance
- `POST /api/rcon/:name/command` - Send RCON command
- `GET /api/ws/events` - WebSocket for real-time events
- `GET /api/logs/:name` - SSE stream for instance logs

## Configuration

### Instance Configuration

Each instance has an `instance_config.ini` file with the following structure:

```ini
[ServerSettings]
ServerName=ARK Server <instance_name>
ServerPassword=
ServerAdminPassword=adminpassword
MaxPlayers=70
MapName=TheIsland_WP
RCONPort=27020
Port=7777
ModIDs=
CustomStartParameters=-NoBattlEye -crossplay -NoHangDetection
SaveDir=<instance_name>
ClusterID=
```

## Integration Features

### FRP Integration

The tool includes built-in FRP (Fast Reverse Proxy) management for exposing your server to the internet.

### Syncthing Integration

Syncthing integration allows for easy synchronization of configuration files across multiple servers.

## Development

### Prerequisites

- Go 1.26 or higher
- Node.js 16.x or higher
- npm 8.x or higher

### Building the Application

1. **Build the backend**:
   ```bash
   go build -o asa-manager.exe
   ```

2. **Build the frontend**:
   ```bash
   cd app
   npm install
   npm run build
   ```

### Running the Development Server

1. **Start the API server**:
   ```bash
   go run main.go api
   ```

2. **Start the frontend development server**:
   ```bash
   cd app
   npm run dev
   ```

### Adding New Features

1. **CLI Commands**: Add new commands in `actions/actions.go` and register them in `main.go`
2. **API Endpoints**: Add new endpoints in `webapi/actions.go`
3. **Frontend Components**: Add new components in `app/src/components/`
4. **Frontend Views**: Add new views in `app/src/views/`

## Troubleshooting

### Common Issues

#### Command Not Found
Ensure `asa-manager.exe` is in your PATH or run it from the correct directory.

#### Instance Failed to Start
- Check if the instance configuration is correct
- Verify that required ports are not in use
- Check the server logs for more details

#### Backup Failed
- Ensure the instance is stopped before creating a backup
- Verify the world folder name is correct
- Ensure there is sufficient disk space

#### API Server Not Accessible
- Check if the service is running
- Verify firewall settings allow traffic on port 19193

## Documentation

All documentation lives in [`docs/`](docs/). Start with [docs/README.md](docs/README.md) for the
Chinese overview, or jump straight to a topic below.

### Architecture & Design

| Document | What it covers |
|----------|----------------|
| [ARCHITECTURE.md](docs/ARCHITECTURE.md) | System architecture and design patterns |
| [PACKAGE_RESTRUCTURE_PLAN.md](docs/PACKAGE_RESTRUCTURE_PLAN.md) | Splitting the `asaserver` god-package into domain packages |
| [STATE_CONTROL.md](docs/STATE_CONTROL.md) | Instance state machine, CAS transitions, mutual exclusion |
| [V2_MIRROR_STARTUP_ARCHITECTURE.md](docs/V2_MIRROR_STARTUP_ARCHITECTURE.md) | NTFS junction mirrors for parallel instance startup |
| [HTTP2_CONNECTION_OPTIMIZATION.md](docs/HTTP2_CONNECTION_OPTIMIZATION.md) | HTTP/2 plan to lift the browser's 6-connection-per-origin cap on SSE |
| [instance-manager-daemon.md](docs/instance-manager-daemon.md) | Instance manager daemon design |

### Features

| Document | What it covers |
|----------|----------------|
| [BATCH_OPERATION.md](docs/BATCH_OPERATION.md) | **Batch start/stop/restart** — orchestration, preflight, CAS, SSE log stream |
| [stop-restart-countdown.md](docs/stop-restart-countdown.md) | Delayed stop/restart countdown with in-game announcements |
| [COUNTDOWN_RCON_REFACTOR_PLAN.md](docs/COUNTDOWN_RCON_REFACTOR_PLAN.md) | Extracting the `countdown` and `rconx` packages |
| [SCHEDULE_RUN_LOG_DESIGN.md](docs/SCHEDULE_RUN_LOG_DESIGN.md) | Scheduled tasks and their run logs |
| [ARK_SAVE_PARSE_SOLUTION.md](docs/ARK_SAVE_PARSE_SOLUTION.md) | ARK save file parsing |
| [PARSESERVER_REDESIGN.md](docs/PARSESERVER_REDESIGN.md) | `parseserver` redesign |
| [state-change-ws-push.md](docs/state-change-ws-push.md) | Pushing state changes over WebSocket |
| [ws-state-push-refactor.md](docs/ws-state-push-refactor.md) | WebSocket state push refactor |
| [VirtualLogList.md](docs/VirtualLogList.md) | Virtualized log list (frontend) |

### Reference

| Document | What it covers |
|----------|----------------|
| [API_REFERENCE.md](docs/API_REFERENCE.md) | Complete HTTP API reference |
| [CHEATSHEET.md](docs/CHEATSHEET.md) | Commands, config, and RCON quick reference |
| [asa-server-configuration.md](docs/asa-server-configuration.md) | ARK server configuration reference |
| [asa-game-configuration-reference.md](docs/asa-game-configuration-reference.md) | Game.ini / GameUserSettings.ini reference |
| [game-ini-visual-config-guide.md](docs/game-ini-visual-config-guide.md) | Visual guide to Game.ini options |
| [asa-creatureids.md](docs/asa-creatureids.md) · [asa-itemsids.md](docs/asa-itemsids.md) · [asa-engrams.md](docs/asa-engrams.md) | Creature / item / engram ID tables |

### Migration & History

| Document | What it covers |
|----------|----------------|
| [MIGRATION.md](docs/MIGRATION.md) | Migrating from the old bash scripts |
| [V2_MIGRATION_PLAN.md](docs/V2_MIGRATION_PLAN.md) · [V2_MIGRATION_CHANGELOG.md](docs/V2_MIGRATION_CHANGELOG.md) | v2 migration plan and changelog |
| [STARTUP_FIXES.md](docs/STARTUP_FIXES.md) | Startup/shutdown fixes log |

### Tooling

| Document | What it covers |
|----------|----------------|
| [ark-translation-tool.md](docs/ark-translation-tool.md) | ARK translation tool |
| [download-creature-icons.md](docs/download-creature-icons.md) · [download-item-icons.md](docs/download-item-icons.md) | Icon download scripts |

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

MIT License

## Support

For issues and feature requests, please create a new [GitHub Issue](https://github.com/yourusername/asa-server/issues).
