# ElectionLoop

A distributed leader election system built with etcd, featuring automatic session recovery and event-driven architecture.

## Overview

ElectionLoop provides a robust solution for leader election in distributed systems using etcd's concurrency primitives. It automatically handles leader selection, failover, and session recovery with minimal dependencies.

## Features

- **Automatic Leader Election** — Distributed leader selection using etcd
- **Session Auto-Recovery** — Automatically reconnects to etcd on session failure
- **Event-Driven Architecture** — Clean separation of concerns with three independent loops
- **Graceful Leadership Transfer** — Leaders properly resign on shutdown
- **Role-Based State Management** — Seamless transition between Leader/Follower roles
- **TLS Support** — Secure connection to etcd with mutual TLS

## Architecture

### Three-Loop Design

The system uses three independent loops, each with a single responsibility:

```text
┌────────────────────────────────────────────────────────────┐
│                         Run() Loop                         │
│                   Session Management Layer                 │
│                                                            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │ Watch Loop   │  │Campaign Loop │  │ Event Loop   │      │
│  │              │  │              │  │              │      │
│  │ Observe etcd │  │ Execute      │  │ Process      │      │
│  │ events       │  │ election     │  │ events &     │      │
│  │ (PUT/DELETE) │  │ logic        │  │ switch roles │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
│         │                  │                  │            │
│         └──────────────────┴──────────────────┘            │
│                            │                               │
│                     ┌──────▼───────┐                       │
│                     │ Event Channel│                       │
│                     └──────────────┘                       │
└────────────────────────────────────────────────────────────┘
```

### Components

#### 1. Watch Loop

- **Responsibility**: Monitor leader changes in etcd
- **Implementation**: Uses `Watch()` API on election prefix
- **Events**: Detects PUT (new leader) and DELETE (leader gone)
- **Output**: Sends `ElectionEvent` to event channel

#### 2. Campaign Loop

- **Responsibility**: Execute election logic
- **Strategy**:
  - Checks current leader before campaigning
  - Uses `Observe()` during campaign to detect competing nodes
  - Periodic checks every 30 seconds as fallback
- **Cleanup**: Resigns leadership on session shutdown

#### 3. Event Loop

- **Responsibility**: Process events and manage role transitions
- **State Management**: Maintains `currentLeader` and `currentRole`
- **Actions**: Starts/stops leaderLoop or followerLoop based on events

### Session Lifecycle

```text
Run() → run() → Session Created
              ↓
   Start Three Loops (Watch, Campaign, Event)
              ↓
   Monitor Session.Done()
              ↓
   Session Closed → Stop All Loops → Return Error
              ↓
   Run() Retries → New Session → Loop Continues
```

## Project Structure

```text
electionloop/
├── cmd/
│   ├── options/
│   │   └── runner/          # CLI options for runner command
│   └── runner.go            # Runner command implementation
├── config/
│   └── runner.yaml          # Configuration file
├── internal/
│   ├── config/
│   │   └── config.go        # Config transformation
│   ├── data/
│   │   └── data.go          # Data layer (etcd client)
│   └── server/
│       └── runner.go        # Election logic & loops
├── pkg/
│   └── app/                 # Application framework
└── main.go                  # Entry point
```

## Installation

```bash
# Clone the repository
git clone https://github.com/tsukiyoz/electionloop.git
cd electionloop

# Build
go build -o electionloop

# Or run directly
go run main.go runner -c config/runner.yaml
```

## Configuration

Create a `config/runner.yaml`:

```yaml
node-id: "node-1"

etcd:
  endpoints:
    - "https://your-etcd-server:2379"
  tls-cacert: /path/to/ca.crt
  tls-cert: /path/to/client.crt
  tls-key: /path/to/client.key
```

### Configuration Options

| Option | Description | Required | Default |
| --------- | ------------- | ---------- | --------- |
| `node-id` | Unique identifier for this node | Yes | - |
| `etcd.endpoints` | etcd cluster endpoints | Yes | `["127.0.0.1:2379"]` |
| `etcd.tls-cacert` | Path to CA certificate | No* | - |
| `etcd.tls-cert` | Path to client certificate | No* | - |
| `etcd.tls-key` | Path to client key | No* | - |

*Required if using HTTPS endpoints

## Usage

### Basic Usage

```bash
# Run with default configuration
go run main.go runner -c config/runner.yaml

# Run with custom node ID
go run main.go runner -c config/runner.yaml --node-id=node-2

# Run with debug logging
go run main.go runner -c config/runner.yaml --log-level=debug

# Run with text log format
go run main.go runner -c config/runner.yaml --log-format=text
```

### Running Multiple Nodes

```bash
# Terminal 1: Node 1
go run main.go runner -c config/runner.yaml --node-id=node-1 --log-level=debug

# Terminal 2: Node 2
go run main.go runner -c config/runner.yaml --node-id=node-2 --log-level=debug

# Terminal 3: Node 3
go run main.go runner -c config/runner.yaml --node-id=node-3 --log-level=debug
```

Observe the election in action:

1. First node becomes leader
2. Other nodes become followers
3. Kill the leader → followers detect and re-elect
4. Original leader rejoins → becomes follower

## How It Works

### Leader Election Flow

1. **Startup**: All nodes start in `RoleUnknown` state
2. **Initial Check**: Each node checks if a leader exists
3. **Campaign**: If no leader, nodes begin campaigning
4. **Selection**: etcd ensures only one node wins
5. **Role Assignment**:
   - Winner → `RoleLeader` (starts leaderLoop)
   - Losers → `RoleFollower` (start followerLoop)

### Failover Flow

```text
Leader crashes (network failure/process killed)
    ↓
Session expires (15 second TTL)
    ↓
etcd deletes leader key
    ↓
WatchLoop detects DELETE event
    ↓
EventLoop receives LeaderGone event
    ↓
CampaignLoop starts new election
    ↓
New leader selected
    ↓
All nodes update roles
```

### Graceful Shutdown Flow

```text
Ctrl+C (SIGINT)
    ↓
s.ctx.Done() triggered
    ↓
All three loops receive ctx.Done() and start cleanup:
    - WatchLoop: exits immediately
    - CampaignLoop cleanup: If leader → call Resign()
    - EventLoop cleanup: Stop leaderLoop/followerLoop
    ↓
All loops exit
    ↓
WaitGroup.Wait() completes
    ↓
Session closed (defer session.Close())
    ↓
Clean exit
```

## Event Types

| Event | Description | Action |
| ------- | ------------- | -------- |
| `LeaderElected` | New leader selected | Update `currentLeader`, switch role |
| `LeaderChanged` | Leader changed | Update `currentLeader`, switch role |
| `LeaderGone` | Leader key deleted | Clear `currentLeader`, trigger campaign |

## Role Types

| Role | Description | Business Logic |
| -------- | ------------- | -------------------- |
| `RoleUnknown` | Initial state | No active role |
| `RoleLeader` | Elected leader | Execute leader-specific logic (leaderLoop) |
| `RoleFollower` | Following leader | Execute follower-specific logic (followerLoop) |

## Key Design Decisions

### Why Separate Watch and Campaign?

- **Watch**: Uses low-level `Watch()` API to detect ALL events (including DELETE)
- **Campaign**: Uses high-level `Observe()` API to check current leader
- This separation ensures immediate detection of leader resignation

### Why Event-Driven?

- Decouples components for better testability
- Enables clean state transitions
- Makes the system more maintainable

### Why Session Auto-Recovery?

- Handles transient network failures
- Automatically reconnects to etcd
- No manual intervention needed

## Adding Business Logic

Edit `leaderLoop()` or `followerLoop()` in `internal/server/runner.go`:

```go
func (s *Server) leaderLoop(ctx context.Context) {
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            // Add your leader-specific logic here
        }
    }
}
```

## License

MIT © 2026 tsukiyo

## Acknowledgments

Built with:

- [etcd](https://etcd.io/) — Distributed key-value store for leader election
- [cobra](https://github.com/spf13/cobra) — CLI framework
- [viper](https://github.com/spf13/viper) — Configuration management
