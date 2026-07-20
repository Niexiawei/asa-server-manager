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
- **Go Version**: 1.25.4 or higher (for development)
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

```
d:\golang\asa-server\
├── actions/             # Command-line action handlers
├── app/                 # Vue.js frontend application
├── asaserver/           # Core server management logic
├── backup/              # Backup and restore functionality
├── docs/                # Documentation files
├── frpmanage/           # FRP integration
├── logger/              # Logging system
├── processjob/          # Process management
├── serverinfo/          # Server information gathering
├── syncthingmanage/     # Syncthing integration
├── tui/                 # Text-based user interface components
├── webapi/              # HTTP API implementation
├── win32api/            # Windows API bindings
├── winservice/          # Windows service functionality
├── main.go              # Application entry point
└── README.md            # This file
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

The HTTP API provides RESTful endpoints for all server management functions. For detailed documentation, see the [API Documentation](docs/API_GUIDE.md).

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

- Go 1.25.4 or higher
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

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

MIT License

## Support

For issues and feature requests, please create a new [GitHub Issue](https://github.com/yourusername/asa-server/issues).
