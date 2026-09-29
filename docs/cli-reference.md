# CLI reference

Run `undertow help` or `undertow help COMMAND` for terminal help. Linux binary: `./bin/undertow`; Windows binary: `.\bin\undertow.exe`. This page describes the current command line. Paths are relative to the process working directory unless absolute.

## Enrollment and identity shared by connection modes

| Flag | Modes | Meaning |
| --- | --- | --- |
| `--auth token\|password\|none` | server, agent, client | Enrollment policy; default `token`. `none` lets any reachable peer enroll. |
| `--token-file PATH` | server, agent, client | Enrollment token file for token mode; default `token.key`. |
| `--token HEX` | server, agent, client | Pass the token value directly instead of a file. Cannot combine with `--token-file`; visible in process listings and shell history. |
| `--password TEXT` | server, agent, client | Password mode only, at least 12 bytes; visible to process listings. |
| `--password-file PATH` | server, agent, client | Password mode only; reads a password file. Do not combine with `--password`. |
| `--domain NAME` | server, agent, client | Synthetic direct-DNS name; default `t.undertow.invalid`, must match. |

Agents and VPN clients must provide `--server IP:PORT` with a numeric IP. They pin the server through `--fingerprint HEX`, a previously saved `--fingerprint-file PATH` (default `server.fingerprint`), or an explicit `--trust-on-first-use` first connection. The explicit fingerprint takes precedence. A fingerprint is saved only after a successful session. Trust on first use cannot authenticate an intercepted first contact. See [getting started](getting-started.md) for enrollment examples.

## `init`

`undertow init [--identity PATH] [--token-file PATH]`

Creates or reuses an Ed25519 server key at `identity.key`, creates or reuses a random token at `token.key`, and prints the server fingerprint. Keep the identity key only on the server; distribute the token only for token enrollment.

## `server`

`undertow server [FLAGS]`

| Flag | Default | Meaning |
| --- | --- | --- |
| `--listen IP:PORT` | `0.0.0.0:53` | Direct DNS UDP listener. |
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

The server may require privilege to bind UDP/53 or create a proxy interface. A second positional `server` is an error; write `undertow server --listen ...`. For real deployments, use token or password enrollment. `--auth none` lets any reachable peer enroll, access network paths, and execute programs on agents that allow it; the server prints a warning at startup.

## `agent`

`undertow agent --server IP:PORT [--fingerprint HEX | --trust-on-first-use] [FLAGS]`

| Flag | Default | Meaning |
| --- | --- | --- |
| `--server IP:PORT` | Required | Direct-DNS server. |
| `--fingerprint HEX` | None | Explicit server identity pin. |
| `--fingerprint-file PATH` | `server.fingerprint` | Read saved pin or save a trust-on-first-use pin. |
| `--trust-on-first-use` | Off | Discover the server pin for first connection. |
| `--agent-key PATH` | `agent.key` | Stable agent Ed25519 identity. |
| `--deny LIST` | None | Disable agent capabilities independently: `pivot`, `exec`, `hostops`, `interactive`, `upload`, `download`, `listeners`. For example, `--deny=exec,upload` still allows built-in host operations and interactive sessions. All are allowed by default. `listeners` controls agent-side TCP forwards. |
| `--advertise-route CIDR` | None | Offer an additional IPv4 subnet to VPN clients; repeatable. Up IPv4 interface subnets are offered automatically. |
| `--domain NAME` | `t.undertow.invalid` | Must match the server. |
| `--auth`, `--token`, `--token-file`, `--password`, `--password-file` | See above | Enrollment. |
| `--payload-profile auto\|large\|small` | `auto` | `auto` probes the path for a 128–800 byte fragment size and adjusts future fragments after loss; `large` and `small` force legacy 800 and 320 byte profiles. |
| `--probe` | Off | Run encrypted echo probes against a `--probe-echo` server instead of socket operations. |
| `--probe-count N` | `0` | Number of probes; zero runs continuously. |
| `--probe-size BYTES` | `64` | Echo payload size, 16 through 65536 bytes. |
| `--probe-interval DURATION` | `1s` | Time between probes; zero sends as fast as the window allows. |

The agent makes no interface or host route changes. Its local key must differ for each independently managed agent.

## `client`

`undertow client (--vpn | --internal | --vpn --internal) --server IP:PORT [FLAGS]`

Run on the elevated **VPN client** host. Choose at least one of `--vpn` and `--internal`:

```sh
sudo undertow client --vpn --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
sudo undertow client --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
sudo undertow client --vpn --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--vpn` | Off | Install two IPv4 `/1` routes for Internet egress through server sockets. |
| `--internal` | Off | Use internal agent routes. Accept or add routes from the client console; global server routes are optional. |
| `--server IP:PORT` | Required | Numeric IPv4 direct-DNS server address. |
| `--fingerprint HEX` | None | Explicit server identity pin. |
| `--fingerprint-file PATH` | `server.fingerprint` | Saved pin path. |
| `--trust-on-first-use` | Off | Discover and save a first-use pin after connection. |
| `--client-key PATH` | `client.key` | Client Ed25519 identity. |
| `--domain NAME` | `t.undertow.invalid` | Must match the server. |
| `--auth`, `--token`, `--token-file`, `--password`, `--password-file` | See above | Enrollment. |
| `--tun-name NAME` | `undertow-vpn` | Client TUN/Wintun name. |
| `--tunnel-address CIDR` | `172.16.253.1/24` | Client interface IPv4 address/network. |
| `--payload-profile auto\|large\|small` | `auto` | Automatic 128–800 byte path discovery and adjustment, or a forced legacy profile. |
| `--verify-url URL` | `https://api.ipify.org` | Public IPv4 check after `--vpn` routes are installed. Ignored in internal-only mode; empty disables it for `--vpn`. |
| `--interactive` | Automatic in a terminal | Force an attached console when input is redirected. |
| `--routes-file PATH` | `client-routes.json` | Persist this client's accepted and manual agent routes. The file is created locally, not copied from the server. |

Every mode creates the client TUN/Wintun and pins a physical route to the direct-DNS server. `--vpn` installs two IPv4 `/1` routes and verifies public egress by default. `--internal` alone does not change the Internet/default route or check public egress. After connecting, use `agents`, `use 1`, and `routes` in the client console, then `route accept CIDR` for an advertised subnet or `route add CIDR` for a manual per-client route. No server route command is required. If global server routes exist, internal-only mode also mirrors active ones. `--vpn --internal` provides both behaviours. Owned routes are withdrawn on graceful exit or a failed check. IPv6 is not routed. The `.1` client adapter address is local; the public egress address is reported separately in VPN mode.

In a terminal, any client mode opens the interactive console by default. The tunnel runs in a separate local worker so `background` detaches the console without dropping its routes or session. Run `undertow client attach` to return; use `--pid-file PATH` if the worker uses a custom state file. `quit` stops the client and removes its routes. Ctrl+C asks before stopping. Up/Down recall commands and Tab completes the first command word. `client --background` starts without the console. A nonterminal foreground client can also be attached from another terminal. A connected client can view status, accept agent advertised routes, add its own manual route through an agent, change its own internal mode, and execute programs on agents that allow it. Client accepted routes affect only that client and persist across reconnects. Global server route management and agent selection remain on the server host. No extra credential or server setting is needed for client commands. The server's `control.key` is only for its loopback API and local operator commands; do not copy it to clients. `internal on` and `internal off` change how **new** flows use global server routes; the client's explicitly accepted routes remain active in either setting. Existing connections keep their current path. Routine logs are written to `--log-file` (default `undertow-client.log`); the console shows connection and agent changes.

## Foreground and background lifecycle

`server`, `agent`, and `client` each accept:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--foreground` | Active if no mode selected | Server/agent run in the terminal. VPN client opens its console when terminal input is present. |
| `--background` | Off | Detach, logging to a file and creating protected PID/control state. |
| `--stop` | Off | Ask the background process to stop gracefully. |
| `--log-file PATH` | `undertow-MODE.log` | Background log path. |
| `--pid-file PATH` | `undertow-MODE.pid` | Background control state path. |

Choose only one of foreground, background, and stop. `--stop` needs the same `--pid-file` used on startup; the other connection flags are not needed for stop. A background process runs with the privilege of the command that launched it; a privileged server or VPN client should also be stopped at the needed privilege. The interactive VPN's worker retains the launch privilege when its console detaches. Graceful stop cleans owned routes; forced termination may require manual OS route inspection.

## Operator commands

These commands run on the server host and use its loopback API. All accept `--control IP:PORT` (default `127.0.0.1:47889`) and `--control-token-file PATH` (default `control.key`). `--json` is available where noted. Use an account able to read `control.key`; an elevated server may create it with root only access, requiring `sudo` for operator commands.

| Command | Meaning |
| --- | --- |
| `undertow status [--json]` | Connected agents and VPN clients separately, their hostnames, selected agent, route state, counters. |
| `undertow console` | Interactive console attached to the running server's loopback API. |
| `undertow agent list [--json]` | Alias of `status`; also shows VPN clients. |
| `undertow agent show AGENT_ID` | Detailed JSON for one agent. |
| `undertow agent select AGENT_ID` | Set selected agent for operations that use the selection. |
| `undertow route add CIDR [--via AGENT_ID]` | Configure an internal prefix; explicit owner recommended. |
| `undertow route list [--json]` | Show configured and active routes. |
| `undertow route del CIDR` | Remove an internal route. |
| `undertow session kill AGENT_ID` | Disconnect current agent session; agent may reconnect. |
| `undertow version` | Print build version. |

Agent IDs in the human table can be shortened for display; use `status --json` for the full ID. `--control` must be a numeric loopback address. A route belongs to one agent and becomes inactive when that agent disconnects.

In `status`, **Agents** are hosts exposing reachable networks; **VPN clients** are hosts sending their own traffic through the server. A VPN client appears after its session is ready. Because UDP has no disconnect signal, the server removes an idle client after roughly 60–75 seconds. `last_seen` in JSON shows the last received packet. Each new agent reports `capabilities.supported` and `capabilities.allowed`; older agents show unknown. `Internal` shows whether the client requested configured agent routes. `RX` and `TX` are encrypted session bytes at the server. `Streams` counts open tunneled flows; `Jobs` and `Fwd` count active background tasks and TCP forwards. `Queued`, `Flight`, and `CWND` show waiting fragments, unacknowledged packets, and the current congestion window. Rising `Retrans` indicates packet loss or delayed acknowledgement; compare repeated status snapshots to see whether traffic is still making progress.

Use `undertow agent show AGENT_ID` for the detailed agent view, or select an agent in either console and type `show`. It includes OS/architecture, virtual IP, connection age, last seen, RTT, encrypted bytes and current rate, retransmits and duplicates, transport windows, mux streams, jobs, forwards, advertised networks, discovered routes and gateways, and effective capabilities. `undertow agent show AGENT_ID --json` returns the same fields as JSON. Current rate is measured between status snapshots, so the first sample after connection can be zero.

For command tables, menu examples, key bindings, agent selection, route acceptance, client-to-agent TCP forwards, built-in host operations, execution, file transfers, and detach/reattach, see the [interactive console guide](console.md). Built-in host operations use `hostops`; `agent --deny=exec` still allows them, while `agent --deny=hostops` blocks them. `agent --deny=pivot` rejects agent socket traffic and stops advertising its routes. `agent --deny=listeners` blocks agent-side TCP forward listeners.

If the agent and VPN client share a host, accepting a route used by the agent's own outbound connections can create a routing loop. Keep that outbound path outside the client's accepted routes or use an agent on a separate host.

See [scenarios](scenarios.md) for complete commands and verification steps.
