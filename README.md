# Termix Agent

A lightweight agent for Termix that enables remote terminal access and file management on enrolled machines.

## Features

- **Remote Terminal**: PTY-based terminal access with full shell support
- **File Management**: Browse, upload, download, and manage files remotely
- **Secure Enrollment**: Two-tier token authentication (install token + agent token)
- **OS Keychain Storage**: Agent credentials stored securely in OS keychain
- **Cross-Platform**: Supports Linux (amd64, arm64), macOS, and Windows

## Installation

### From Source

```bash
go build -o termix-agent .
```

### Cross-Compilation

```bash
# Linux AMD64
GOOS=linux GOARCH=amd64 go build -o termix-agent-linux-amd64 .

# Linux ARM64
GOOS=linux GOARCH=arm64 go build -o termix-agent-linux-arm64 .

# Windows
GOOS=windows GOARCH=amd64 go build -o termix-agent.exe .
```

## Usage

### Enrollment

First, create an install token in the Termix UI, then enroll the agent:

```bash
./termix-agent enroll --server your-termix-server.com:30007 --token <install-token>
```

The agent will:
1. Connect to the server with the install token
2. Receive a permanent agent token
3. Store the agent token in the OS keychain

### Running the Agent

After enrollment, simply run:

```bash
./termix-agent
```

The agent will load credentials from the keychain and connect automatically.

### Unenroll

To remove the agent credentials:

```bash
./termix-agent unenroll
```

## Configuration

Command-line flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--server` | Server address (host:port) | Required for enroll |
| `--token` | Install token | Required for enroll |
| `--ssl` | Use SSL/TLS | `true` |
| `--insecure` | Skip SSL verification | `false` |
| `--heartbeat` | Heartbeat interval (seconds) | `30` |
| `--reconnect` | Auto-reconnect on disconnect | `true` |

## Architecture

The agent connects to the Termix server via WebSocket and supports:

- **Terminal sessions**: Spawns PTY processes for interactive shell access
- **File operations**: List, read, write, copy, move, delete files and directories
- **Heartbeat**: Keeps connection alive and reports agent status

## License

MIT
