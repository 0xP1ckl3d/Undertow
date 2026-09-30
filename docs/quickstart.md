# Quickstart

## What are you trying to do?

| Goal | Run these roles |
| --- | --- |
| Reach an internal network from the Undertow server | Server + agent |
| Reach an internal network from a separate laptop, keeping its normal Internet connection | Server + agent + `client --internal` |
| Route a laptop's IPv4 Internet traffic through the server | Server + `client --vpn` |
| Use server Internet egress and reach an internal network | Server + agent + `client --vpn --internal` |
| Reach one internal TCP service | Server + agent + a server TCP forward |
| Expose a service running on the client at the agent | Server + agent + client + an agent-side TCP forward |

These examples use Linux commands and direct DNS on UDP/53. `undertow` means `./bin/undertow` if you built from this repository. Build with `mkdir -p bin && go build -buildvcs=false -o bin/undertow ./cmd/undertow` (Go 1.25+). On Windows, use `bin\undertow.exe` in an Administrator PowerShell for a client or a server with `--tun`; the agent needs no elevation. Allow inbound UDP/53 on **SERVER**, and choose tunnel and internal networks that do not overlap local networks.

First, prepare enrollment once on **SERVER**:

```sh
undertow init
```

Record the printed `FINGERPRINT`. Securely copy **only** `token.key` to every **AGENT** and **CLIENT** host. Keep `identity.key` on **SERVER**. Replace `SERVER_IP`, `FINGERPRINT`, `AGENT_ID`, `10.20.0.0/16`, and `10.20.0.50` with your values. Run each command from the directory containing that host's key files, or supply absolute paths. An agent connects outward; it does not need an inbound port or change its own routes. Use Ctrl+C to stop a foreground server or agent. If the server created `control.key` as root, run its operator commands with `sudo`.

### Reach an internal network from the server

Topology: **SERVER** routes `10.20.0.0/16` through a TUN to **AGENT**, which can already reach **INTERNAL TARGET** `10.20.0.50`. **SERVER** needs root for its TUN and usually for UDP/53; **AGENT** is unprivileged.

```sh
# SERVER — terminal 1
sudo undertow server --listen 0.0.0.0:53 --tun --identity identity.key --token-file token.key

# AGENT — terminal 2
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key

# SERVER — terminal 3; copy the full agent ID from status
sudo undertow status --json
sudo undertow route add 10.20.0.0/16 --via AGENT_ID
curl http://10.20.0.50/
```

To disconnect, run `sudo undertow route del 10.20.0.0/16` on **SERVER**, then Ctrl+C on **AGENT** and **SERVER**. Stopping the server gracefully also removes its owned OS routes.

### Reach an internal network from a separate client

Topology: **CLIENT** sends only `10.20.0.0/16` through **SERVER** and **AGENT** to **INTERNAL TARGET**. Its normal Internet route stays in place. **CLIENT** needs root for its TUN; **AGENT** is unprivileged; **SERVER** usually needs root only to bind UDP/53.

```sh
# SERVER — terminal 1; no server TUN needed
sudo undertow server --listen 0.0.0.0:53 --identity identity.key --token-file token.key

# AGENT — terminal 2
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key

# CLIENT — terminal 3
sudo undertow client --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

In the **CLIENT** console, select the agent and accept its advertised route:

```text
agents
use 1
routes
route accept 10.20.0.0/16
```

If that network is reachable from **AGENT** but is not advertised, use `route add 10.20.0.0/16` in the same console. In another **CLIENT** terminal, verify with `curl http://10.20.0.50/`. Type `quit` in the client console to disconnect and remove its routes; Ctrl+C stops the agent and server. The client remembers accepted routes in `client-routes.json`; use `route del 10.20.0.0/16` before quitting if you do not want this route restored next time.

### Route client Internet traffic through the server

Topology: **CLIENT** sends IPv4 Internet traffic to **SERVER**, which opens Internet sockets. No agent or server TUN is needed. **CLIENT** needs root; **SERVER** usually needs root for UDP/53.

```sh
# SERVER — terminal 1
sudo undertow server --listen 0.0.0.0:53 --identity identity.key --token-file token.key

# CLIENT — terminal 2
sudo undertow client --vpn --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key

# CLIENT — another terminal; compare with the server's public IPv4 address
curl -4 https://api.ipify.org
```

The client also checks public IPv4 egress when it starts. Type `quit` in its console to disconnect and remove its VPN routes, then Ctrl+C on **SERVER**. This mode routes IPv4; it does not route IPv6.

### Route client Internet traffic and an internal network

Topology: **CLIENT** sends IPv4 Internet traffic through **SERVER** and `10.20.0.0/16` through **AGENT** to **INTERNAL TARGET**. **CLIENT** needs root; **AGENT** is unprivileged; **SERVER** usually needs root for UDP/53.

```sh
# SERVER — terminal 1; no server TUN needed
sudo undertow server --listen 0.0.0.0:53 --identity identity.key --token-file token.key

# AGENT — terminal 2
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key

# CLIENT — terminal 3
sudo undertow client --vpn --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

In the **CLIENT** console, enter `agents`, `use 1`, `routes`, then `route accept 10.20.0.0/16` (or `route add 10.20.0.0/16` if it is not advertised). In another **CLIENT** terminal, verify both paths:

```sh
curl http://10.20.0.50/
curl -4 https://api.ipify.org
```

Type `route del 10.20.0.0/16` in the **CLIENT** agent menu if you want to forget this route; then `quit` disconnects and removes VPN routes. Ctrl+C stops **AGENT** and **SERVER**.

### Reach one internal TCP service without routed access

Topology: **SERVER** listens only on its loopback port `18080` and relays to `10.20.0.50:80` through **AGENT**. No TUN, OS route, or client is needed. **AGENT** is unprivileged; **SERVER** usually needs root for UDP/53.

```sh
# SERVER — terminal 1; omit --via-agent when there is exactly one connected agent
sudo undertow server --listen 0.0.0.0:53 --identity identity.key --token-file token.key --forward 127.0.0.1:18080=10.20.0.50:80

# AGENT — terminal 2
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key

# SERVER — another terminal
curl http://127.0.0.1:18080/
```

Ctrl+C on **AGENT** and **SERVER** closes the forward. With multiple agents, add `--via-agent AGENT_ID` to the server startup command.

### Expose a client service through an agent-side TCP forward

Topology: **CLIENT** serves HTTP on `127.0.0.1:8080`; **AGENT** listens on port `8080` for **INTERNAL TARGET** and relays connections back to **CLIENT**. **CLIENT** needs root for its TUN; **AGENT** is unprivileged; **SERVER** usually needs root for UDP/53. Allow TCP/8080 through the agent host's firewall.

```sh
# SERVER — terminal 1
sudo undertow server --listen 0.0.0.0:53 --identity identity.key --token-file token.key

# AGENT — terminal 2
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key

# CLIENT — terminal 3, start your own HTTP service on port 8080
python3 -m http.server 8080 --bind 127.0.0.1

# CLIENT — terminal 4
sudo undertow client --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

In the **CLIENT** console:

```text
agents
use 1
forward add 0.0.0.0:8080 127.0.0.1:8080
forward list
```

Verify from **INTERNAL TARGET** with `curl http://AGENT_IP:8080/`. In the same **CLIENT** agent menu, `forward del 0.0.0.0:8080` closes the listener. Type `quit` to stop the client, then Ctrl+C on the HTTP service, **AGENT**, and **SERVER**. The forward also closes when either endpoint disconnects.

For background processes, alternate enrollment modes, console commands, and deeper routing detail, see [getting started](getting-started.md), [deployment scenarios](scenarios.md), and the [CLI reference](cli-reference.md).
