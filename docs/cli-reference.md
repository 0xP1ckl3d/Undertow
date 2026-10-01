# CLI reference

The interactive console supports `run-native [--background] [--data LOCAL_FILE] MODULE_FILE [ARGS]` for Windows amd64 native `.module` files. At the main menu, put `AGENT_ID` immediately after `run-native`; after `use NUMBER`, omit it. Native runs use the agent's independent `native` capability and share the normal `jobs`, `job output`, and `job stop` commands. See [native modules](native-modules.md) for the ABI, MSVC build, imports and compatibility limits.

Run `undertow help` or `undertow help COMMAND` for terminal help. Linux binary: `./bin/undertow`; Windows binary: `.\bin\undertow.exe`. This page describes the current command line. Paths are relative to the process working directory unless absolute.

Run `undertow examples` (or `undertow help examples`) for short commands by host covering server pivots, internal-only clients, VPN egress, combined routing, and a single TCP forward. For verification and cleanup, see the [quickstart](quickstart.md).

Run `undertow doctor server|agent|client [FLAGS]` before starting a role to check local credentials, addresses, bind ports, background state, privilege, TUN/Wintun availability, and route overlaps. Server doctor checks **all selected listeners** independently, including TCP/443 and UDP/443 together by default, and reports the automatic self-signed TLS mode. Pass the relevant startup options, such as `--transport dns,quic`, per-transport listen flags, `--server`, `--tun`, `--tunnel-address`, `--route CIDR`, or `--forward LOCAL=REMOTE`. Doctor reports `PASS`, `WARN`, or `FAIL` with a remedy and changes no configuration. `FAIL` exits nonzero. For example:

```sh
# SERVER
undertow doctor server

# AGENT
undertow doctor agent --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify

# CLIENT
undertow doctor client --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify --internal --route 10.20.0.0/16
```

## Enrollment and identity shared by connection modes

| Flag | Modes | Meaning |
| --- | --- | --- |
| `--auth token\|password\|none` | server, agent, client | Enrollment policy; default `token`. `none` lets any reachable peer enroll. |
| `--token-file PATH` | server, agent, client | Enrollment token file for token mode; default `token.key`. |
| `--token HEX` | server, agent, client | Pass the token value directly instead of a file. Cannot combine with `--token-file`; visible in process listings and shell history. |
| `--password TEXT` | server, agent, client | Password mode only, at least 12 bytes; visible to process listings. |
| `--password-file PATH` | server, agent, client | Password mode only; reads a password file. Do not combine with `--password`. |
| `--domain NAME` | server, agent, client | DNS carrier only: synthetic question name; default `t.undertow.invalid`, must match. |

## Transport selection

The **server** defaults to all three listeners: QUIC UDP/443, WebSocket TCP/443 and DNS UDP/53. Agents and clients default to DNS in the CLI, so specify `--transport quic` or `--transport websocket` to use either of those paths. They can use different carriers at the same time; identity, enrollment, mux, routing, console commands, jobs and files share one server control plane. On the server, `--transport dns,quic` selects a subset and `--transport quic` keeps the single-carrier form.

| Carrier | Server endpoint | Use when |
| --- | --- | --- |
| `dns` | UDP/53 | Direct UDP DNS is the available outbound path, including some restrictive or captive portal networks. |
| `websocket` | HTTPS WebSocket on TCP/443 | Ordinary enterprise HTTPS egress or an HTTP CONNECT proxy is available. The client reads standard proxy environment variables. |
| `quic` | QUIC on UDP/443 | UDP/443 is allowed and lower transport overhead is desired. |

WebSocket and QUIC server listeners use ephemeral self-signed TLS by default. Supply `--tls-cert PATH --tls-key PATH` to use certificate files; `--tls-self-signed` remains an explicit spelling. Agents and clients verify TLS certificates by default. Use `--tls-server-name NAME` when connecting to a numeric IP whose certificate has a DNS name; for a private or self-signed certificate, `--tls-insecure-skip-verify` permits the TLS connection while the separate Undertow `--fingerprint` pin still verifies the server identity. Obtain that fingerprint from a trusted server operator; do not use first-use discovery on an untrusted network. A WebSocket VPN client keeps its carrier peer outside the VPN routes. When its HTTP CONNECT proxy runs on the same client machine, use a numeric IPv4 `--server` address so Undertow can also preserve the proxy's upstream route. `--websocket-path` defaults to `/undertow` and must match at both endpoints. DNS `--domain` and `--payload-profile` do not apply to WebSocket or QUIC.

```sh
# SERVER (TCP/443)
sudo undertow server --transport websocket --tls-cert server.crt --tls-key server.key
# AGENT (CLIENT uses the same transport flags plus --vpn or --internal)
undertow agent --transport websocket --server vpn.example.com:443 --fingerprint FINGERPRINT --token-file token.key

# SERVER (UDP/443)
sudo undertow server --transport quic --tls-cert server.crt --tls-key server.key
# AGENT
undertow agent --transport quic --server vpn.example.com:443 --fingerprint FINGERPRINT --token-file token.key
```

Agents and VPN clients must provide `--server HOST:PORT`; DNS requires a numeric IPv4 address, while WebSocket and QUIC also accept hostnames. They pin the server through `--fingerprint HEX`, a previously saved `--fingerprint-file PATH` (default `server.fingerprint`), or an explicit `--trust-on-first-use` first connection. The explicit fingerprint takes precedence. A fingerprint is saved only after a successful session. Trust on first use cannot authenticate an intercepted first contact. See [getting started](getting-started.md) for enrollment examples.

## `init`

`undertow init [--identity PATH] [--token-file PATH]`

Creates or reuses an Ed25519 server key at `identity.key`, creates or reuses a random token at `token.key`, and prints the server fingerprint. Keep the identity key only on the server; distribute the token only for token enrollment.

## `server`

`undertow server [FLAGS]`

| Flag | Default | Meaning |
| --- | --- | --- |
| `--transport LIST` | `dns,websocket,quic` | Comma-separated enabled listeners; a single name remains valid. |
| `--listen IP:PORT` | Carrier default | Generic listener address only when one transport is explicitly selected. |
| `--dns-listen IP:PORT` | `0.0.0.0:53` | DNS UDP listener address in multi-carrier mode. |
| `--websocket-listen IP:PORT` | `0.0.0.0:443` | WebSocket TCP listener address in multi-carrier mode. |
| `--quic-listen IP:PORT` | `0.0.0.0:443` | QUIC UDP listener address in multi-carrier mode. |
| `--tls-cert PATH`, `--tls-key PATH` | None | TLS PEM pair shared by enabled WebSocket/QUIC listeners. |
| `--tls-self-signed` | Automatic without TLS files | Explicitly request an ephemeral self-signed TLS certificate; mutually exclusive with TLS files. |
| `--websocket-path PATH` | `/undertow` | HTTPS upgrade path for WebSocket. |
| `--identity PATH` | `identity.key` | Server Ed25519 identity key. |
| `--domain NAME` | `t.undertow.invalid` | DNS question domain. |
| `--auth`, `--token`, `--token-file`, `--password`, `--password-file` | See above | Enrollment. |
| `--tun` | Off | Create proxy TUN/Wintun for routed internal pivots from the server host. |
| `--tun-name NAME` | `undertow0` | Proxy adapter name. |
| `--tunnel-address CIDR` | `172.16.254.1/24` | Proxy interface IPv4 address and network. |
| `--forward LOCAL=REMOTE` | None | Local TCP listener to agent reachable TCP target; may repeat. |
| `--via-agent ID` | Auto with one agent | Agent used by TCP forwards. |
| `--control-listen IP:PORT` | `127.0.0.1:47889` | Loopback operator API. |
| `--control-token-file PATH` | `control.key` | API credential, distinct from enrollment token. |
| `--probe-echo` | Off | Echo diagnostic probes; normal streams are disabled. |

The server may require privilege to bind UDP/53 or create a proxy interface. TCP/443 and UDP/443 coexist. `--listen` with several selected transports is ambiguous and rejected; use the per-transport flags. If any requested listener cannot start, the server exits instead of silently omitting it. A second positional `server` is an error. For real deployments, use token or password enrollment. `--auth none` lets any reachable peer enroll, access network paths, and execute programs on agents that allow it; the server prints a warning at startup.

In a terminal, `server` starts a separate worker and opens its operator console. `transports` lists active listeners; `start transport NAME [self-signed|tls-cert FILE tls-key FILE] [listen ADDR]` adds one, and `stop transport NAME` removes one only when it has no sessions. `stop transport NAME force` disconnects that carrier's sessions. `background`, `quit`, and `exit` detach without stopping the worker. `undertow server attach` returns; `logs` shows recent worker logs, `logs follow` streams them until Enter, and bare `stop` shuts the worker down gracefully. `server --background` skips the console. `server --foreground` runs the worker directly for service managers or debugging. If startup used custom `--pid-file`, `--control-listen`, `--control-token-file`, or `--log-file`, provide the matching `--pid-file`, `--control`, `--control-token-file`, or `--log-file` on `server attach`.

## `agent`

`undertow agent --server HOST:PORT [--fingerprint HEX | --trust-on-first-use] [FLAGS]`

| Flag | Default | Meaning |
| --- | --- | --- |
| `--server HOST:PORT` | Required | Server host and carrier port; DNS needs numeric IPv4. |
| `--transport dns\|websocket\|quic\|relay` | `dns` | Choose one active server listener, or an explicitly enabled parent-agent relay. |
| `--websocket-path PATH` | `/undertow` | Match the server WebSocket path. |
| `--tls-server-name NAME` | Server IP | Name checked against the TLS certificate. |
| `--tls-insecure-skip-verify` | Off | Allow private/self-signed TLS certificate; keep the Undertow identity pin. |
| `--fingerprint HEX` | None | Explicit server identity pin. |
| `--fingerprint-file PATH` | `server.fingerprint` | Read saved pin or save a trust-on-first-use pin. |
| `--trust-on-first-use` | Off | Discover the server pin for first connection. |
| `--agent-key PATH` | `agent.key` | Stable agent Ed25519 identity. |
| `--deny LIST` | None | Disable agent capabilities independently: `pivot`, `exec`, `hostops`, `interactive`, `scripts`, `wasm`, `upload`, `download`, `listeners`, `relay`. All are allowed by default. `listeners` controls client-service TCP forwards; `relay` controls child-agent relay listeners. Neither opens a listener automatically. |
| `--advertise-route CIDR` | None | Offer an additional IPv4 subnet to VPN clients; repeatable. Up IPv4 interface subnets are offered automatically. |
| `--domain NAME` | `t.undertow.invalid` | DNS only; must match the server. |
| `--auth`, `--token`, `--token-file`, `--password`, `--password-file` | See above | Enrollment. |
| `--payload-profile auto\|large\|small` | `auto` | DNS only. `auto` probes for a 128–800 byte fragment size; `large` and `small` force legacy 800 and 320 byte profiles. |
| `--probe` | Off | Run encrypted echo probes against a `--probe-echo` server instead of socket operations. |
| `--probe-count N` | `0` | Number of probes; zero runs continuously. |
| `--probe-size BYTES` | `64` | Echo payload size, 16 through 65536 bytes. |
| `--probe-interval DURATION` | `1s` | Time between probes; zero sends as fast as the window allows. |

The agent makes no interface or host route changes. Its local key must differ for each independently managed agent.

For a child agent, explicitly start a relay listener on its selected parent in the **server** console with `relay start INTERNAL_IP:PORT`, then run `undertow agent --transport relay --server INTERNAL_IP:PORT --fingerprint FINGERPRINT --token-file token.key --agent-key child.key`. The fingerprint is the original server's; the child authenticates to that server, not to the parent. Relay requires an explicit trusted pin or existing fingerprint file. See [topology and relays](topology-and-relays.md).

## `client`

`undertow client (--vpn | --internal | --vpn --internal) --server HOST:PORT [FLAGS]`

Run on the elevated **VPN client** host. Choose at least one of `--vpn` and `--internal`:

```sh
sudo undertow client --vpn --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
sudo undertow client --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
sudo undertow client --vpn --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--vpn` | Off | Install two IPv4 `/1` routes for Internet egress through server sockets. |
| `--internal` | Off | Use internal agent routes. Accept or add routes from the client console; global server routes are optional. |
| `--server HOST:PORT` | Required | Server host and carrier port; DNS needs numeric IPv4. |
| `--transport dns\|websocket\|quic\|relay` | `dns` | Choose one active server listener; relay is available through an explicitly enabled agent listener. |
| `--websocket-path PATH` | `/undertow` | Match the server WebSocket path. |
| `--tls-server-name NAME` | Server IP | Name checked against the TLS certificate. |
| `--tls-insecure-skip-verify` | Off | Allow private/self-signed TLS certificate; keep the Undertow identity pin. |
| `--fingerprint HEX` | None | Explicit server identity pin. |
| `--fingerprint-file PATH` | `server.fingerprint` | Saved pin path. |
| `--trust-on-first-use` | Off | Discover and save a first-use pin after connection. |
| `--client-key PATH` | `client.key` | Client Ed25519 identity. |
| `--domain NAME` | `t.undertow.invalid` | DNS only; must match the server. |
| `--auth`, `--token`, `--token-file`, `--password`, `--password-file` | See above | Enrollment. |
| `--tun-name NAME` | `undertow-vpn` | Client TUN/Wintun name. |
| `--tunnel-address CIDR` | `172.16.253.1/24` | Client interface IPv4 address/network. |
| `--payload-profile auto\|large\|small` | `auto` | DNS only: automatic 128–800 byte path discovery, or a forced legacy profile. |
| `--verify-url URL` | `https://api.ipify.org` | Public IPv4 check after `--vpn` routes are installed. Ignored in internal-only mode; empty disables it for `--vpn`. |
| `--interactive` | Automatic in a terminal | Force an attached console when input is redirected. |
| `--routes-file PATH` | `client-routes.json` | Persist this client's accepted and manual agent routes. The file is created locally, not copied from the server. |

Every mode creates the client TUN/Wintun and pins a physical route to the carrier server. `--vpn` installs two IPv4 `/1` routes and verifies public egress by default. `--internal` alone does not change the Internet/default route or check public egress. After connecting, use `agents`, `use 1`, and `routes` in the client console, then `route accept CIDR` for an advertised subnet or `route add CIDR` for a manual per-client route. No server route command is required. If global server routes exist, internal-only mode also mirrors active ones. `--vpn --internal` provides both behaviours. Owned routes are withdrawn on graceful exit or a failed check. IPv6 is not routed. The `.1` client adapter address is local; the public egress address is reported separately in VPN mode.

In a terminal, any client mode opens the interactive console by default. The tunnel runs in a separate local worker so `background` detaches the console without dropping its routes or session. Run `undertow client attach` to return; use `--pid-file PATH` if the worker uses a custom state file. `quit` stops the client and removes its routes. Ctrl+C asks before stopping. Up/Down recall commands; Tab completes command names, help topics, and local paths for scripts, WASM modules, and transfers. `help` shows the full menu for the current level, `help TOPIC` gives command details, and `clear` or `cls` clears the screen. `client --background` starts without the console. A nonterminal foreground client can also be attached from another terminal. A connected client can view status, accept agent advertised routes, add its own manual route through an agent, toggle Internet egress with `vpn on|off`, toggle internal routing with `internal on|off`, and execute programs on agents that allow it. `vpn on` verifies public egress and rolls its two `/1` routes back on failure; `vpn off` removes those routes without stopping the client or removing accepted agent routes. Both mode settings survive carrier reconnects while the worker runs; restarting uses the startup flags. Client accepted routes affect only that client and persist across reconnects. Global server route management and agent selection remain on the server host. No extra credential or server setting is needed for client commands. The server's `control.key` is only for its loopback API and local operator commands; do not copy it to clients. `internal on` and `internal off` change how **new** flows use global server routes; the client's explicitly accepted routes remain active in either setting. Existing connections keep their current path. Routine logs are written to `--log-file` (default `undertow-client.log`); the console shows connection and agent changes.

In the attached client console, `vpn status` shows the active carrier and last verified public IPv4 egress address. `internal status` lists installed routes as `CIDR via HOSTNAME (AGENT_ID)`. The `routes` and server route views use the same agent label.

## Foreground and background lifecycle

`server`, `agent`, and `client` each accept:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--foreground` | Agent default | Run the worker in the foreground without a console for server/client; agent already runs in the foreground. Useful for service managers, containers, debugging, and automation. |
| `--background` | Off | Detach, logging to a file and creating protected PID/control state. |
| `--stop` | Off | Ask the background process to stop gracefully. |
| `--log-file PATH` | `undertow-MODE.log` | Background log path. |
| `--pid-file PATH` | `undertow-MODE.pid` | Background control state path. |

Choose only one of foreground, background, and stop. With no lifecycle flag in a terminal, server and client open their consoles; without a terminal they run in the foreground. `--stop` needs the same `--pid-file` used on startup; the other connection flags are not needed for stop. A worker runs with the privilege of the command that launched it; a privileged server or VPN client should also be stopped at the needed privilege. Graceful stop cleans owned routes; forced termination may require manual OS route inspection.

## Operator commands

These commands run on the server host and use its loopback API. All accept `--control IP:PORT` (default `127.0.0.1:47889`) and `--control-token-file PATH` (default `control.key`). `--json` is available where noted. Use an account able to read `control.key`; an elevated server may create it with root only access, requiring `sudo` for operator commands.

| Command | Meaning |
| --- | --- |
| `undertow status [--json]` | Active server carrier, TCP/UDP listen address, DNS domain or WebSocket path, TLS mode, server fingerprint, plus connected agents and VPN clients, route state, and counters. |
| `undertow console` | Interactive console attached to the running server's loopback API. |
| `undertow server attach` | Reopen the server console after detaching; accepts the worker's PID, control, token, and log paths. |
| `undertow agent list [--json]` | Alias of `status`; also shows VPN clients. |
| `undertow agent show AGENT_ID [--json]` | Detailed human view, or JSON, for one agent. |
| `undertow agent select AGENT_ID` | Set selected agent for operations that use the selection. |
| `undertow route add CIDR [--via AGENT_ID]` | Configure an internal prefix; explicit owner recommended. |
| `undertow route list [--json]` | Show configured and active routes. |
| `undertow route del CIDR` | Remove an internal route. |
| `undertow session kill AGENT_ID` | Disconnect current agent session; agent may reconnect. |
| `undertow version` | Print build version. |

Agent IDs in the human table can be shortened for display; use `status --json` for the full ID. `--control` must be a numeric loopback address. A route belongs to one agent and becomes inactive when that agent disconnects.

In `status`, **Agents** are hosts exposing reachable networks; **VPN clients** are hosts sending their own traffic through the server. A VPN client appears after its session is ready. DNS uses an idle timeout of roughly 60–75 seconds; persistent WebSocket and QUIC sessions close when their carrier connection closes. `last_seen` in JSON shows the last received packet. Each new agent reports `capabilities.supported` and `capabilities.allowed`; older agents show unknown. `Internal` shows whether the client requested configured agent routes. `RX` and `TX` are encrypted session bytes at the server. `Streams` counts open tunneled flows; `Jobs` and `Fwd` count active background tasks and TCP forwards. `Queued`, `Flight`, and `CWND` show waiting fragments, unacknowledged packets, and the current congestion window. Rising `Retrans` indicates packet loss or delayed acknowledgement; compare repeated status snapshots to see whether traffic is still making progress.

`undertow status` lists every active server listener with network, address, TLS mode and session count, plus the actual carrier for each agent and client. Use `undertow agent show AGENT_ID` for the detailed agent view, or select an agent in either console and type `show`. It includes carrier and relay parent, OS/architecture, virtual IP, connection age, last seen, RTT, encrypted bytes and current rate, retransmits and duplicates, transport windows, mux streams, jobs, forwards, advertised networks, discovered routes and gateways, and effective capabilities. `undertow agent show AGENT_ID --json` returns the same fields as JSON. Current rate is measured between status snapshots, so the first sample after connection can be zero.

For command tables, menu examples, key bindings, agent selection, route acceptance, client-to-agent TCP forwards, built-in host operations, execution, file transfers, and detach/reattach, see the [interactive console guide](console.md). Built-in host operations use `hostops`; `agent --deny=exec` still allows them, while `agent --deny=hostops` blocks them. `agent --deny=pivot` rejects agent socket traffic and stops advertising its routes. `agent --deny=listeners` blocks agent-side TCP forward listeners.

If the agent and VPN client share a host, accepting a route used by the agent's own outbound connections can create a routing loop. Keep that outbound path outside the client's accepted routes or use an agent on a separate host.

See [scenarios](scenarios.md) for complete commands and verification steps.
