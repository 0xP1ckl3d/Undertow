# Undertow feature catalogue

Undertow runs on **Linux and Windows**. The commands below use `undertow` as the executable name; substitute `./bin/undertow` on Linux or `.\bin\undertow.exe` in Windows PowerShell. Replace `SERVER_IP`, `FINGERPRINT`, `AGENT_ID`, and example network addresses with values from your deployment. A line labeled **Server** runs on the Undertow server host; **Agent** runs on a host inside the target network; **VPN client** runs on the host whose applications will use the tunnel; **Other internal host** is a machine reached through the agent. In a terminal, `undertow server` and `undertow client` open their consoles automatically.

The fastest internal-only setup needs no server route configuration: start the server and agent, connect a client with `--internal`, then use `agents`, `use 1`, `routes`, and `route accept CIDR` in the **VPN client** console. Use `route add CIDR` there for a known network that was not reported.

## Roles and identity

### Server role

The server accepts DNS, HTTPS/WebSocket, or QUIC sessions, authenticates agents and clients, owns the local operator API, and routes traffic to server sockets or selected agents. Its proxy TUN is optional.

```text
Server: undertow init
Server: sudo undertow server --listen 0.0.0.0:53 --identity identity.key --token-file token.key
Server: sudo undertow status
```

### Agent role

An agent joins from an internal host, advertises reachable networks, and opens ordinary TCP, UDP, and ICMP sockets on behalf of the server. It does not install a TUN or change its own routes.

```text
Agent: undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
Server: sudo undertow agent list
```

### Client role

A privileged client creates its own TUN/Wintun, connects independently of agents, and routes traffic from its own applications. Choose `--vpn`, `--internal`, or both; at least one is required.

```text
VPN client: sudo undertow client --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
VPN client console: agents
```

### Transport choices and encrypted multiplexing

Choose `--transport dns` (default) when direct UDP/53 is available, including some restrictive or captive portal networks; `--transport websocket` for HTTPS compatibility over TCP/443 and HTTP CONNECT proxies; or `--transport quic` for UDP/443. The server and connecting agent/client select the same carrier. WebSocket and QUIC need server `--tls-cert` and `--tls-key`; peers verify TLS and independently pin the Undertow Ed25519 fingerprint. See [Quickstart transport examples](docs/quickstart.md#choose-a-transport) and the [CLI reference](docs/cli-reference.md#transport-selection).

The same authenticated session, mux, routing, jobs, file transfer, and console operations run over all three carriers. WebSocket uses a persistent TLS connection; QUIC uses a bidirectional stream over UDP/443. DNS keeps its own adaptive wire behavior.

### Direct DNS details

The carrier is direct DNS over UDP to the configured numeric server address and domain. It does not depend on a recursive resolver or iodine. An authenticated encrypted session carries mux streams for network flows, commands, files, and control traffic. DNS polling, outstanding queries, fragment size, send window, and receive window adapt to path conditions.

```text
Server: sudo undertow server --listen 0.0.0.0:53 --domain t.example.invalid --identity identity.key --token-file token.key
Agent: undertow agent --server SERVER_IP:53 --domain t.example.invalid --fingerprint FINGERPRINT --token-file token.key --payload-profile auto
```

`--payload-profile auto` discovers and adjusts a 128–800 byte fragment size. `large` and `small` explicitly select legacy 800 and 320 byte profiles for compatibility. `agent show` exposes transport windows, RTT, retransmits, duplicates, and payload adjustments.

### Authentication and enrollment

Token enrollment is the default: `init` creates a random `token.key` for distribution to agents and VPN clients. Password enrollment uses `--auth password` with a password file. Open enrollment uses `--auth none` and permits anyone who can reach the listener to join, subject to each agent's capabilities.

```text
Server: undertow init --identity identity.key --token-file token.key
Server: sudo undertow server --auth token --token-file token.key
Agent: undertow agent --server SERVER_IP:53 --auth token --token-file token.key --fingerprint FINGERPRINT
VPN client: sudo undertow client --internal --server SERVER_IP:53 --auth token --token-file token.key --fingerprint FINGERPRINT
```

Alternative enrollment sequence:

```text
Server: sudo undertow server --auth password --password-file enrollment-password.txt
Agent: undertow agent --server SERVER_IP:53 --auth password --password-file enrollment-password.txt --fingerprint FINGERPRINT
```

For an intentionally open test listener:

```text
Server: undertow server --listen 127.0.0.1:5353 --auth none
Agent: undertow agent --server 127.0.0.1:5353 --auth none --trust-on-first-use
```

### Agent and client keys, fingerprints, and first-use trust

The server has a persistent Ed25519 identity; each agent and client has its own persistent key. A peer pins the server fingerprint explicitly, reads a saved pin, or opts into trust on first use. The first-use pin is saved only after an authenticated connection. Keep the server identity key on the server.

```text
Server: undertow init --identity identity.key
Agent: undertow agent --server SERVER_IP:53 --agent-key agent-west.key --fingerprint FINGERPRINT --token-file token.key
VPN client: sudo undertow client --internal --server SERVER_IP:53 --client-key operator-client.key --trust-on-first-use --fingerprint-file server.fingerprint --token-file token.key
```

## Routing and network traffic

### Full IPv4 VPN mode

`--vpn` installs `0.0.0.0/1` and `128.0.0.0/1` on the client, pins the physical server route, and verifies public egress by default. The server uses its own sockets for Internet traffic. IPv6 is outside this mode.

```text
VPN client: sudo undertow client --vpn --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
VPN client: curl -4 https://api.ipify.org
```

### Internal-only routing mode

`--internal` creates the client TUN/Wintun and pins the carrier server route without installing Internet `/1` routes or requiring a public egress check. The client's default Internet route remains in place. Accept a reported route in the client console; no server route command is needed.

```text
VPN client: sudo undertow client --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
VPN client console: agents
VPN client console: use 1
VPN client console: routes
VPN client console: route accept 10.20.0.0/16
VPN client: curl http://10.20.1.25/
```

### Combined VPN and internal mode

Combine both flags to use server Internet egress and accepted or server-configured internal paths at once.

```text
VPN client: sudo undertow client --vpn --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
VPN client console: use 1
VPN client console: route accept 10.20.0.0/16
VPN client: curl -4 https://api.ipify.org
VPN client: curl http://10.20.1.25/
```

### Server-side TUN and server-host pivots

`server --tun` creates an optional proxy TUN/Wintun so applications on the **Server** host can use a configured route through an agent. A global server route is appropriate here; it is optional for a separate VPN client's own accepted routes.

```text
Server: sudo undertow server --tun --tunnel-address 172.16.254.1/24 --identity identity.key --token-file token.key
Server: sudo undertow route add 10.20.0.0/16 --via AGENT_ID
Server: curl http://10.20.1.25/
```

### Client TUN/Wintun and route ownership

Linux clients use TUN; Windows clients use Wintun. Undertow checks for local tunnel-network collisions, installs only the routes owned by the chosen mode, and removes owned routes on graceful exit.

```text
VPN client (Linux): sudo undertow client --internal --tun-name undertow-vpn --tunnel-address 172.16.253.1/24 --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
VPN client console: quit
```

### Agent pivoting over TCP, UDP, and ICMP

The agent opens target sockets from its own network. TCP supports ordinary application flows; UDP uses datagram bridging; ICMP supports echo. Host privileges and network firewalls still apply.

```text
VPN client console: use 1
VPN client console: route accept 10.20.0.0/16
VPN client: curl http://10.20.1.25:8080/
VPN client: dig @10.20.1.53 example.internal
VPN client: ping 10.20.1.25
Other internal host: listen on 10.20.1.25:8080 for the HTTP example
```

### Multi-agent routing

Several agents can be connected at once. The client selects the target agent per accepted route; the server's global route table also binds each prefix to one agent. `agents` and `show` distinguish their identities, networks, and state.

```text
Agent on network A: undertow agent --server SERVER_IP:53 --agent-key east.key --fingerprint FINGERPRINT --token-file token.key
Agent on network B: undertow agent --server SERVER_IP:53 --agent-key west.key --fingerprint FINGERPRINT --token-file token.key
VPN client console: agents
VPN client console: use 1
VPN client console: route accept 10.20.0.0/16
VPN client console: use 2
VPN client console: route accept 10.30.0.0/16
```

### Route advertisements and structured discovery

An agent automatically offers up IPv4 interface networks and can explicitly advertise more prefixes. Its inventory also reports discovered IPv4 routes with destination, gateway, interface, type/source, and direct-versus-routed state; its default route is separate. The client console presents candidates but never installs all discovered routes automatically.

```text
Agent: undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key --advertise-route 10.40.0.0/16
VPN client console: use 1
VPN client console: routes
VPN client console: show
VPN client console: route accept 10.40.0.0/16
```

### Manual and client-specific accepted routes

`route accept CIDR` chooses an advertised or discovered candidate. `route add CIDR` manually chooses a network the agent can reach. These choices are local to one VPN client, persist in `client-routes.json`, and return after reconnect. The client checks local-network and DNS-server collisions. Remove one with `route del`.

```text
VPN client console: use 1
VPN client console: route add 10.50.0.0/16
VPN client console: routes
VPN client console: route del 10.50.0.0/16
```

### Global server routes

The server operator can configure a prefix for server-host pivots and for clients using the global internal mode. A route becomes inactive when its agent disconnects. Client-owned accepted routes remain separate.

```text
Server: sudo undertow route add 10.20.0.0/16 --via AGENT_ID
Server: sudo undertow route list
Server: sudo undertow route del 10.20.0.0/16
VPN client console: internal on
```

### Server local TCP forwards

`server --forward` listens on a local TCP address and sends each connection to a target reached through the chosen agent. It can be repeated for several listeners.

```text
Server: sudo undertow server --forward 127.0.0.1:8080=10.20.1.25:80 --via-agent AGENT_ID --identity identity.key --token-file token.key
Server: curl http://127.0.0.1:8080/
Other internal host: serve HTTP on 10.20.1.25:80
```

### Agent-side TCP listener forwards back to client services

A VPN client can ask one selected agent to listen on a TCP address and relay connections to a loopback service on that client. Multiple forwards and agents are supported. `--deny=listeners` on the agent rejects new listeners. A forward survives client console detach, and its listener closes when the owning client or agent session disconnects.

```text
VPN client: python3 -m http.server 8080 --bind 127.0.0.1
VPN client console: use 1
VPN client console: forward add 0.0.0.0:8080 127.0.0.1:8080
VPN client console: forward list
Other internal host: curl http://AGENT_IP:8080/
VPN client console: forward del 0.0.0.0:8080
```

## Agent operations

### One-shot arbitrary execution

`exec` starts the named program directly, without an implicit shell. It has a 30-second limit and bounded stdout/stderr. The `exec` capability controls it independently of other agent operations.

```text
VPN client console: use 1
VPN client console: exec /usr/bin/id
VPN client console: exec powershell.exe -NoProfile -Command whoami
```

### Built-in host operations

The built-ins are `pwd`, `ls`, `stat`, `mkdir`, `rm`, `whoami`, `ps`, `privileges`, `env`, `interfaces`, `dns`, and `route-table`. They use the independent `hostops` capability; denying `exec` does not deny them.

```text
Agent: undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key --deny=exec
VPN client console: use 1
VPN client console: whoami
VPN client console: interfaces
VPN client console: route-table
```

### Long-lived interactive sessions

`shell` opens one bidirectional mux stream to the selected agent. Linux uses a PTY with size updates; Windows uses interactive process pipes. Choose another program after `shell`; Ctrl-] closes that shell and returns to the Undertow menu without affecting other streams. The separate `interactive` capability controls this operation.

```text
VPN client console: use 1
VPN client console: shell /bin/bash
VPN client shell: printf 'ready\n'
VPN client keyboard: Ctrl-]
VPN client console: shell powershell.exe -NoProfile
```

### Long-running jobs

Jobs run while the operator uses or detaches the console. They have unique IDs, agent association, start/end times, running/completed/failed/cancelled state, exit status, and up to 256 KiB of retained output. A VPN client sees its own jobs; the server operator can see all. Jobs use the `interactive` agent capability.

```text
VPN client console: use 1
VPN client console: job start /usr/bin/find /srv -type f
VPN client console: jobs
VPN client console: job show JOB_ID
VPN client console: job output JOB_ID
VPN client console: job cancel JOB_ID
```

### Memory-backed script execution

`run-script` reads a local file on the VPN client or server operator machine, sends its source through the encrypted Undertow session, and streams it into Bash or PowerShell stdin on the selected agent. The agent does not create a script file. Stdout and stderr stream separately for foreground runs; background runs use the same bounded job store as command jobs. Source is limited to 1 MiB and runtime to 10 minutes. The independent `scripts` capability controls both forms.

```text
VPN client: printf 'id\n' > check.sh
VPN client console: use 1
VPN client console: run-script bash ./check.sh
VPN client console: run-script --background bash ./check.sh
VPN client console: job output JOB_ID
Server: printf 'whoami\n' > audit.ps1
Server console: use AGENT_ID
Server console: run-script powershell ./audit.ps1
```

### In-process WebAssembly execution

`run-wasm` sends a WASI module through Undertow and instantiates it directly from memory in the agent's pure-Go runtime. It needs no temporary module file. The console can supply arguments and optional stdin; stdout and stderr stream separately. Background WASM runs use the same job manager, including bounded retained output and cancellation. The independent `wasm` capability controls this operation. Agent limits are a 4 MiB module, 64 KiB stdin, 16 MiB guest linear memory, 4 MiB combined output, a two-minute runtime and two simultaneous runs. No filesystem is preopened and no host network socket is supplied to the guest.

```text
VPN client console: use 1
VPN client console: run-wasm ./tool.wasm audit
VPN client console: run-wasm --stdin ./input.txt ./tool.wasm
VPN client console: run-wasm --background ./long-task.wasm
VPN client console: job output JOB_ID
Agent: undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key --deny=wasm
```

### Upload and download

File transfer streams through the server to the selected agent, verifies SHA-256, and refuses to overwrite an existing destination. The client console shows bytes, total, percentage, and current rate; Ctrl-] cancels and removes partial temporary files. Upload and download permissions are separate capabilities.

```text
VPN client console: use 1
VPN client console: upload ./notes.txt /tmp/notes.txt
VPN client console: download /tmp/result.txt ./result.txt
VPN client keyboard during a transfer: Ctrl-] to cancel
```

### Granular agent capabilities

All capabilities are enabled by default: `pivot`, `exec`, `hostops`, `interactive`, `scripts`, `wasm`, `upload`, `download`, and `listeners`. Deny any combination at agent startup; the server and agent enforce the operation at the relevant stream.

```text
Agent: undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key --deny=exec,upload
Server: sudo undertow agent show AGENT_ID
```

With this example, built-in host operations and interactive sessions still work. Add `hostops` or `interactive` to the deny list to block those separately.

## Console, lifecycle, and diagnostics

### Server and client consoles

The server console opens automatically in a terminal and uses the authenticated local loopback API. The client console also opens automatically and manages its own route acceptance, forwards, commands, jobs, and files. `agents`, `use NUMBER`, `help`, and `back` provide an agent-focused workflow.

```text
Server: sudo undertow server --listen 0.0.0.0:53
Server console: agents
Server console: use 1
Server console: show
VPN client: sudo undertow client --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
VPN client console: agents
VPN client console: use 1
VPN client console: help
```

### Background, detach, and attach

Server, agent, and client support `--foreground`, `--background`, `--stop`, `--log-file`, and `--pid-file`. In a terminal, server and client open consoles by default; `--foreground` runs their workers without a console. Either console can detach with `background` while its worker continues, then attach again. On the server, `quit` detaches and `stop` shuts down; on the client, `quit` stops the client and removes owned routes.

```text
VPN client console: background
VPN client: sudo undertow client attach --pid-file undertow-client.pid
VPN client console: jobs
VPN client console: quit
Server: sudo undertow server --stop --pid-file undertow-server.pid
Agent: undertow agent --stop --pid-file undertow-agent.pid
```

To start detached from the outset:

```text
VPN client: sudo undertow client --background --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
VPN client: sudo undertow client attach
```

### Status and detailed telemetry

`status` is a compact view of agents, VPN clients, and routes. `agent show` adds hostname, OS/architecture, virtual IP, connection age, last seen, RTT, encrypted bytes and rates, retransmits, duplicates, congestion and receive windows, queued/in-flight packets, mux streams, jobs, TCP forwards, advertised networks, discovered routes/gateways, and allowed capabilities.

```text
Server: sudo undertow status
Server: sudo undertow agent show AGENT_ID
VPN client console: use 1
VPN client console: show
```

### Probe and diagnostic modes

`server --probe-echo` runs a controlled encrypted echo endpoint instead of ordinary socket service. An agent can run `--probe` with count, size, and interval for latency and transport checks. The on-demand `route-table`, `interfaces`, and `dns` built-ins inspect an agent host.

```text
Server: undertow server --listen 0.0.0.0:5353 --probe-echo --identity identity.key --token-file token.key
Agent: undertow agent --server SERVER_IP:5353 --fingerprint FINGERPRINT --token-file token.key --probe --probe-count 25 --probe-size 512 --probe-interval 1s
VPN client console: use 1
VPN client console: route-table
```

### JSON output and local control API

The server operator can request structured JSON for status, route lists, and one detailed agent. `--control` and `--control-token-file` target the loopback API if it was configured differently. The control token is distinct from the enrollment token and stays on the server.

```text
Server: sudo undertow status --json
Server: sudo undertow route list --json
Server: sudo undertow agent show AGENT_ID --json
```

### Cleanup, disconnect, and reconnect

On graceful client stop, Undertow removes the routes it installed. Agent disconnect deactivates its global routes and closes its TCP listeners; client disconnect closes that client's forwards. Agents and clients reconnect using their stable keys. Client route choices saved in `client-routes.json` are restored after reconnect, subject to current collision and agent checks. `session kill` disconnects one agent session for diagnostics.

```text
Server: sudo undertow session kill AGENT_ID
Server: sudo undertow status
VPN client console: background
VPN client: sudo undertow client attach
VPN client console: routes
VPN client console: quit
```

For flag details, see [CLI reference](docs/cli-reference.md); for the wire format, see [protocol](docs/protocol.md); for tested performance, see [benchmarks](docs/benchmarks.md).
