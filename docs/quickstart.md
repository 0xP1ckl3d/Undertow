# Quickstart

## What are you trying to do?

| Goal | Roles | Start with |
| --- | --- | --- |
| Reach an internal network from the server | Server + agent | `server --tun` |
| Reach an internal network from a separate laptop, keeping normal Internet | Server + agent + client | `client --internal` |
| Route a laptop's IPv4 Internet traffic through Undertow | Server + client | `client --vpn` |
| Use Undertow Internet egress and reach an internal network | Server + agent + client | `client --vpn --internal` |
| Reach one internal TCP service | Server + agent | `server --forward` |
| Expose a service running on a client at an agent | Server + agent + client | Client console `forward add` |
| Reach a deeper network through an agent | Server + parent agent + child agent + optional client | Parent console `relay start` |

The three client modes do different jobs:

- **`--vpn`:** send the client's IPv4 Internet traffic through the server. No agent is needed. With the DNS carrier, this is the **DNS VPN** workflow for an outbound path that permits direct UDP DNS, including some restrictive or captive portal networks.
- **`--internal`:** send only selected internal networks through agents. The client's normal Internet and default routes remain in place.
- **`--vpn --internal`:** combine server Internet egress with selected agent networks.

The commands below use Linux. The server starts DNS UDP/53, WebSocket TCP/443 and QUIC UDP/443 together; agents and clients in the first examples choose DNS. `undertow` means `./bin/undertow` if you built from source (`mkdir -p bin && go build -buildvcs=false -o bin/undertow ./cmd/undertow`, Go 1.25+). On Windows use `.\bin\undertow.exe` and an Administrator PowerShell for the client or a server with `--tun`; the agent needs no elevation. Open the server firewall for the carriers you intend to use. Use `--transport dns` on the server when only UDP/53 should listen. Run these examples in terminals so `server` and `client` open their consoles automatically.

Enter `transports` or `status` in the server console, or run `sudo undertow status` on the server host, to see every active listener and its address, TLS mode and session count. Each agent and client can choose a different active carrier; `status` shows the carrier used by each peer.

Prepare enrollment once on **SERVER**:

```sh
undertow init
```

Record the printed `FINGERPRINT`. Securely copy **only** `token.key` to **AGENT** and **CLIENT** hosts; keep `identity.key` on **SERVER**. Replace `SERVER_IP`, `FINGERPRINT`, `AGENT_IP`, and the example internal addresses with yours. Run each command from the directory containing that host's key files, or use absolute paths. `undertow doctor server|agent|client` checks local prerequisites before startup.

In the server console, `background`, `quit`, or `exit` detaches without stopping the worker. Return with `undertow server attach`; type `stop` in the console to shut the server down gracefully. `logs` shows recent server logs and `logs follow` streams them until Enter. In the client console, `background` detaches, `undertow client attach` returns, and `quit` stops the client and removes its owned routes. Stop a foreground agent with Ctrl+C.

## Reach an internal network from the server

**Goal:** run tools on **SERVER** against **INTERNAL TARGET** `10.20.0.50` through **AGENT**, which can already reach `10.20.0.0/16`. **SERVER** needs root for its TUN and usually for UDP/53. **AGENT** is unprivileged.

**Start — each command runs on the named machine:**

```sh
# SERVER, terminal 1
sudo undertow server --tun --identity identity.key --token-file token.key

# AGENT, terminal 2
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

**Use the SERVER console** that opened with `server`:

```text
agents
use 1
show
routes
route add 10.20.0.0/16
```

**Verify on SERVER:** `curl http://10.20.0.50/` (or use a reachable target and port). **Background/stop:** type `background` in the server console to leave it running; return with `sudo undertow server attach`. To cleanly disconnect, type `route del 10.20.0.0/16` in the selected agent menu, then `stop` in the server console and Ctrl+C on **AGENT**.

## Reach an internal network from a separate client

**Goal:** reach **INTERNAL TARGET** `10.20.0.50` from **CLIENT** while normal Internet access stays on the client's existing connection. **CLIENT** needs root for its TUN; **AGENT** is unprivileged; **SERVER** usually needs root for UDP/53. The server needs no TUN.

**Start:**

```sh
# SERVER, terminal 1
sudo undertow server --identity identity.key --token-file token.key

# AGENT, terminal 2
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key

# CLIENT, terminal 3
sudo undertow client --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

**Use the CLIENT console:**

```text
agents
use 1
show
routes
route accept 10.20.0.0/16
```

If the agent can reach the network but has not advertised it, use `route add 10.20.0.0/16` instead of `route accept`. **Verify on CLIENT:** `curl http://10.20.0.50/`; ordinary Internet traffic should still use the client's normal connection. **Background/stop:** `background` keeps the client running, and `sudo undertow client attach` returns to its console. Type `route del 10.20.0.0/16` if you do not want the client to remember this route, then `quit` to remove its routes and stop. Type `stop` on **SERVER** and Ctrl+C on **AGENT**.

## Route client Internet traffic through Undertow

**Goal:** use the **DNS VPN** from **CLIENT** through **SERVER** for IPv4 Internet egress. No internal agent or server TUN is needed. This is useful when direct UDP DNS is the available outbound path. **CLIENT** needs root; **SERVER** usually needs root for UDP/53.

**Start:**

```sh
# SERVER, terminal 1
sudo undertow server --identity identity.key --token-file token.key

# CLIENT, terminal 2
sudo undertow client --vpn --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

**Use the consoles:** the **CLIENT** console opens after the VPN health check; type `status` to see the connection. On **SERVER**, type `status` to see the VPN client. No `agents` or route selection is needed.

**Verify on CLIENT:** `curl -4 https://api.ipify.org` and compare it with the server's public IPv4 address. **Background/stop:** type `background` on **CLIENT** to keep the VPN running, and `sudo undertow client attach` to return. Type `quit` to remove the VPN routes and stop the client; type `stop` on **SERVER**. This mode routes IPv4, not IPv6.

## Route client Internet traffic and an internal network

**Goal:** send the client's IPv4 Internet traffic through **SERVER** and `10.20.0.0/16` through **AGENT** to **INTERNAL TARGET**. **CLIENT** needs root; **AGENT** is unprivileged; **SERVER** usually needs root for UDP/53. The server needs no TUN.

**Start:**

```sh
# SERVER, terminal 1
sudo undertow server --identity identity.key --token-file token.key

# AGENT, terminal 2
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key

# CLIENT, terminal 3
sudo undertow client --vpn --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

**Use the CLIENT console:** `agents`, `use 1`, `show`, `routes`, then `route accept 10.20.0.0/16`. Use `route add 10.20.0.0/16` for a reachable network that is not advertised.

**Verify on CLIENT:** run `curl http://10.20.0.50/` and `curl -4 https://api.ipify.org` in another terminal. **Background/stop:** `background` detaches the client console and `sudo undertow client attach` returns. Remove an unwanted saved route with `route del 10.20.0.0/16`, then `quit` stops the client and removes VPN routes. Type `stop` on **SERVER** and Ctrl+C on **AGENT**.

## Reach one internal TCP service

**Goal:** expose **INTERNAL TARGET** `10.20.0.50:80` at `127.0.0.1:18080` on **SERVER** through **AGENT**. This needs no TUN, OS route, or client. **AGENT** is unprivileged; **SERVER** usually needs root for UDP/53.

**Start:**

```sh
# SERVER, terminal 1; this example has one agent
sudo undertow server --identity identity.key --token-file token.key --forward 127.0.0.1:18080=10.20.0.50:80

# AGENT, terminal 2
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

**Use the SERVER console:** type `agents`, `use 1`, and `show` to confirm which agent is connected. With several agents, add `--via-agent AGENT_ID` to the server startup command; the [CLI reference](cli-reference.md) covers scripted selection.

**Verify on SERVER:** `curl http://127.0.0.1:18080/`. **Background/stop:** `background` keeps the forward running; `sudo undertow server attach` returns. Type `stop` to close the forward and server, then Ctrl+C on **AGENT**.

## Expose a client service at an agent

**Goal:** **CLIENT** serves HTTP on `127.0.0.1:8080`; **AGENT** listens on TCP/8080 so an **INTERNAL TARGET** can reach it. **CLIENT** needs root for its TUN; **AGENT** is unprivileged; **SERVER** usually needs root for UDP/53. Allow TCP/8080 through the agent host's firewall.

**Start:**

```sh
# SERVER, terminal 1
sudo undertow server --identity identity.key --token-file token.key

# AGENT, terminal 2
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key

# CLIENT, terminal 3; start the service
python3 -m http.server 8080 --bind 127.0.0.1

# CLIENT, terminal 4
sudo undertow client --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

**Use the CLIENT console:**

```text
agents
use 1
forward add 0.0.0.0:8080 127.0.0.1:8080
forward list
```

**Verify on INTERNAL TARGET:** `curl http://AGENT_IP:8080/`. **Background/stop:** `background` keeps the forward active; `sudo undertow client attach` returns. Type `forward del 0.0.0.0:8080`, then `quit` to stop the client. Ctrl+C stops the HTTP service and **AGENT**; type `stop` on **SERVER**. The forward also closes if either endpoint disconnects.

## Choose a transport

The examples above use **DNS** (direct UDP/53). Keep it for the DNS VPN use case when direct UDP DNS is the available outbound path, including some restrictive or captive portal networks. The same server, agent, and client console commands work with these alternatives:

| Goal | Carrier | Network path |
| --- | --- | --- |
| General HTTPS compatibility, including HTTP CONNECT proxy environments | `websocket` | TCP/443 with TLS and a WebSocket upgrade |
| Higher performance where UDP/443 is open | `quic` | QUIC over UDP/443 |

The default server already listens on both alternatives, with an ephemeral self-signed TLS certificate for each. Use `--tls-cert` and `--tls-key` on the server when you have a certificate; otherwise connect to its IP using `--tls-insecure-skip-verify --fingerprint FINGERPRINT`. The Undertow identity pin remains required. The examples below use `--transport` to run only one server carrier at a time.

**WebSocket goal → SERVER + AGENT + optional CLIENT → start:**

```sh
# SERVER, terminal 1; TCP/443
sudo undertow server --transport websocket --tls-cert server.crt --tls-key server.key

# AGENT, terminal 2
undertow agent --transport websocket --server vpn.example.com:443 --fingerprint FINGERPRINT --token-file token.key

# CLIENT, terminal 3 when internal routing is wanted
sudo undertow client --transport websocket --internal --server SERVER_IP:443 --tls-server-name vpn.example.com --fingerprint FINGERPRINT --token-file token.key
```

**Use the CLIENT console:** `agents`, `use 1`, `show`, `routes`, `route accept 10.20.0.0/16` (or `route add 10.20.0.0/16`). **Verify on CLIENT:** `curl http://10.20.0.50/`. **Background/stop:** `background` detaches, `sudo undertow client attach` returns, and `quit` stops the client. On **SERVER**, `background` detaches and `stop` shuts it down; Ctrl+C stops **AGENT**. For Internet egress without an agent, use `--vpn` instead of `--internal` on **CLIENT** and verify with `curl -4 https://api.ipify.org`.

**QUIC goal → same components and consoles → start:**

```sh
# SERVER, terminal 1; UDP/443
sudo undertow server --transport quic --tls-cert server.crt --tls-key server.key

# AGENT, terminal 2
undertow agent --transport quic --server vpn.example.com:443 --fingerprint FINGERPRINT --token-file token.key

# CLIENT, terminal 3 when internal routing is wanted
sudo undertow client --transport quic --internal --server SERVER_IP:443 --tls-server-name vpn.example.com --fingerprint FINGERPRINT --token-file token.key
```

**Use and verify:** follow the same client console and `curl` steps as WebSocket. **Background/stop:** use the same `background`, `attach`, `quit`, and server `stop` commands. A private or self-signed TLS certificate needs `--tls-insecure-skip-verify` on **AGENT** and **CLIENT**; keep `--fingerprint FINGERPRINT` to verify the separate Undertow identity. The TLS certificate and the Undertow identity key are distinct. See the [CLI reference](cli-reference.md) for `--websocket-path` and transport-specific flags.

**No domain or certificate files:** on **SERVER**, run `sudo undertow server` for all three listeners, or select one with `--transport websocket` or `--transport quic`. Self-signed TLS is automatic. On **AGENT** and **CLIENT**, connect to `--server SERVER_IP:443` with the chosen `--transport`, `--tls-insecure-skip-verify`, and `--fingerprint FINGERPRINT`. Obtain the Undertow fingerprint from `undertow init` on the server through a trusted channel. The generated TLS certificate changes on each server start, while the Undertow identity remains stable.

## Mix carriers on one server

**Goal:** reach an agent over DNS from a client using QUIC. **SERVER** runs both listeners; **AGENT** uses UDP/53; **CLIENT** uses UDP/443. The server uses one identity, route table and console for both sessions.

**Start:**

```sh
# SERVER
sudo undertow server

# AGENT
undertow agent --transport dns --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key

# CLIENT
sudo undertow client --transport quic --internal --server SERVER_IP:443 --tls-insecure-skip-verify --fingerprint FINGERPRINT --token-file token.key
```

**Use the consoles:** on **SERVER**, type `transports` and `status` to see both carriers and their sessions. On **CLIENT**, type `agents`, `use 1`, `show`, `routes`, then `route accept 10.20.0.0/16` or `route add 10.20.0.0/16`.

**Verify:** `curl http://10.20.0.50/` from **CLIENT**. **Background/stop:** `background` detaches either console. Reattach with `server attach` or `client attach`; `quit` stops the client and removes its routes, and `stop` in the server console shuts down the server. WebSocket agents and clients may join the same server at any time. `--transport dns,quic` restricts the server to those two listeners; `--dns-listen`, `--websocket-listen` and `--quic-listen` set their addresses independently.

In the **SERVER** console, `start transport dns`, `start transport quic self-signed`, and `start transport websocket self-signed` open listeners without restarting the server. `stop transport NAME` refuses when that carrier has active sessions; `stop transport NAME force` deliberately disconnects them. Other carriers keep running. Use `transports` to inspect the current state.

## Reach a deeper network through an agent relay

**Goal:** **CHILD AGENT B** reaches the original **SERVER** through **PARENT AGENT A**, because B can reach A's internal address but cannot reach the public server. **CLIENT** uses QUIC and routes to B's deeper target. A uses DNS in this example. Neither agent changes host routes or adapters.

**Start:**

```sh
# SERVER
sudo undertow server

# PARENT AGENT A, on a host reachable at AGENT_A_INTERNAL_IP
undertow agent --transport dns --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

**Use the SERVER console:** type `agents`, `use 1`, then `relay start AGENT_A_INTERNAL_IP:8443`. This is the only relay listener opened; allow TCP/8443 between B and A. `relay start` without a bind uses loopback `127.0.0.1:8443`; `0.0.0.0:8443` is used only if you explicitly request it.

```sh
# CHILD AGENT B; its --server points to A, with a separate agent.key
undertow agent --transport relay --server AGENT_A_INTERNAL_IP:8443 --fingerprint FINGERPRINT --token-file token.key

# CLIENT
sudo undertow client --transport quic --internal --server SERVER_IP:443 --tls-insecure-skip-verify --fingerprint FINGERPRINT --token-file token.key
```

**Use the consoles:** on **SERVER**, type `topology` to see B via A, then `agents`, `use 2`, `show`, and `routes` to inspect B independently. On **CLIENT**, type `agents`, select B's current number with `use NUMBER`, then `route accept DEEPER_CIDR` or `route add DEEPER_CIDR`. B has its own identity, inventory, capabilities, shell, jobs and routes. To add C, select B in the server console, explicitly run `relay start B_INTERNAL_IP:8444`, and point C's `--transport relay --server` at that address.

**Verify:** run `curl http://DEEPER_TARGET/` on **CLIENT**, and `whoami` or `shell` after selecting B. **Background/stop:** `background` detaches the client and server consoles. On **SERVER**, select A and use `relay list` then `relay stop AGENT_A_INTERNAL_IP:8443`; stop the child and parent agents, type `quit` on **CLIENT**, then `stop` on **SERVER**. A disconnect closes B and its descendants from the server's active topology. See [topology and relay guidance](topology-and-relays.md) for limits and capability controls.

## For scripts and automation

The same operations have standalone commands. For example, on **SERVER** after an agent connects, `sudo undertow status --json` gives its full ID, and `sudo undertow route add 10.20.0.0/16 --via AGENT_ID` configures a global route. `sudo undertow route del 10.20.0.0/16` removes it. Use `server --background` or `client --background` for deliberately detached startup, and `server --foreground` or `client --foreground` for a foreground worker without the interactive console. See [deployment scenarios](scenarios.md), [console commands](console.md), and the [CLI reference](cli-reference.md) for detailed options.
