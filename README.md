# Undertow

Undertow carries encrypted sessions over direct DNS on UDP. Run an unprivileged **agent** on a network you want to reach, a **server** to accept sessions and select routes, or a privileged **client** to route Internet traffic, internal traffic, or both. Server Internet egress uses its own sockets; the server proxy TUN is needed only for routed internal pivots.

## How the pieces connect

```mermaid
flowchart LR
  subgraph Operator["Operator / server host"]
    Tools["curl · SSH · browser"] --> Route["OS route to internal subnet"]
    Route --> ProxyTUN["Optional TUN / Wintun"]
    ProxyTUN --> Stack["Userland network stack"]
    Stack --> Selector["Agent route selector"]
    DNS["Encrypted direct-DNS server"]
    Control["Loopback status / route API"]
    Selector --> DNS
  end

  subgraph AgentHost["Internal-network host"]
    Agent["Unprivileged undertow agent<br/>No adapter or host routes"] --> Sockets["TCP · UDP · ICMP sockets"]
    Sockets --> Internal["Internal targets"]
  end

  subgraph VPNHost["Separate VPN client host"]
    Apps["Client applications"] --> VPNRoute["IPv4 routes"]
    VPNRoute --> ClientTUN["Local TUN / Wintun"]
    ClientTUN --> VPN["Privileged client --vpn and/or --internal"]
    Physical["Physical route pinned to server IP"] --> VPN
  end

  DNS <-->|"Authenticated DNS / UDP"| Agent
  VPN <-->|"Authenticated DNS / UDP"| DNS
  DNS -->|"VPN Internet egress"| ServerSockets["Server-side Internet sockets"]
  ServerSockets --> Internet["Public Internet"]
  DNS -->|"With --internal: configured pivot route"| Selector

  Control -.-> Selector
```

| Mode | Use | Privilege |
| --- | --- | --- |
| `server` | Accept sessions; optional internal route interface | Binding UDP/53 or creating TUN may require elevation |
| `agent` | Reach internal targets through ordinary sockets | None |
| `client --vpn` | Route IPv4 Internet traffic through server sockets | Root/Administrator |
| `client --internal` | Route configured/accepted internal prefixes through agents; keep the Internet route | Root/Administrator |
| `client --vpn --internal` | VPN egress plus server configured agent routes | Root/Administrator |

**Agent and client are different jobs.** Put an `agent` on a host that can reach an internal network; it lets the server open sockets from that host, but changes none of that host's routes. Put a `client` on a host whose *own* applications should use the tunnel. `--vpn` installs two IPv4 `/1` Internet routes and verifies public egress. `--internal` alone installs only configured or accepted internal routes, leaving the client's Internet route unchanged. Combine the flags for both behaviours. An agent and a route to its subnet are needed for internal access. `status` lists agents and clients separately.

On the **VPN client host**, choose one mode:

```sh
sudo undertow client --vpn --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
sudo undertow client --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
sudo undertow client --vpn --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

For interactive operation, run `undertow console` on the server or start a VPN client in a terminal; the client console opens by default. Type `agents`, then `use 1` to enter an agent and run `exec`, `upload`, `download`, or route commands without copying its ID. `help` changes with the menu; `back` returns to the main menu. The server console manages global routes. The VPN client console manages its own accepted and manual routes, which persist across reconnects. Current agent capabilities are enabled by default; use `agent --deny=exec,upload` or other listed names to restrict them independently. `status` shows supported and allowed operations. See [interactive scenarios](docs/scenarios.md#8-interactive-consoles-and-agent-commands).

For real deployments, use token or password enrollment. With `server --auth none`, anyone who can reach the listener can join, access network paths, and run commands on agents that allow execution.

## Build

Go 1.25 or newer is required to build. Compiled files belong in the ignored `bin/` directory.

Linux:

```sh
mkdir -p bin
go build -buildvcs=false -o bin/undertow ./cmd/undertow
```

Windows PowerShell:

```powershell
New-Item -ItemType Directory -Force .\bin | Out-Null
go build -buildvcs=false -o .\bin\undertow.exe .\cmd\undertow
if ($LASTEXITCODE -ne 0) { throw 'Build failed; bin\undertow.exe may be an older version' }
.\bin\undertow.exe help agent
```

## First connection

Run these on separate hosts. `init` prints the server fingerprint and creates `identity.key` and `token.key`. Copy **only** `token.key` to the agent through a secure channel; keep `identity.key` on the server. Replace `SERVER_IP` and `FINGERPRINT` with your values.

Server:

```sh
./bin/undertow init
sudo ./bin/undertow server --listen 0.0.0.0:53 --identity identity.key --token-file token.key
```

Agent:

```sh
./bin/undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

Back on the server, `sudo ./bin/undertow status` shows the connected agent when the elevated server created `control.key`. Run `./bin/undertow help`, `./bin/undertow help server`, `./bin/undertow help agent`, or `./bin/undertow help client` for built-in guidance.

## Guides

- [Setup, roles, enrollment and fingerprints](docs/getting-started.md)
- [Scenario commands: pivot, VPN, forwarding and lifecycle](docs/scenarios.md)
- [Interactive console commands and examples](docs/console.md)
- [All commands and flags](docs/cli-reference.md)
- [Protocol version 1](docs/protocol.md)
- [Performance tests and comparison method](docs/benchmarks.md)

Undertow is experimental. Linux VPN egress and Linux pivot TCP, UDP, and ICMP have been exercised on separate hosts. Windows Wintun and the controlled iodine performance comparison still need acceptance testing. This code is [GPL-3.0-only](LICENSE); bundled third-party components keep their own licences.

