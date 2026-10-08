# Networking modes and transports

Use this guide when choosing what traffic the **client** should send through Undertow and which carrier can reach the server. For a complete first installation, follow [Getting started](getting-started.md). To build and deliver a headless agent, use [Payload deployment](agent-distribution.md).

Use **QUIC on UDP/443** for these examples. The server also listens on WebSocket TCP/443 and DNS UDP/53 by default. Open UDP/443 in the server firewall. Run commands from each host's Undertow directory; `./bin/undertow` is the built Linux binary. On Windows, use `.\bin\undertow.exe`. Replace `SERVER_IP`, `FINGERPRINT`, and the example network with your values.

## Choose a client mode

| Mode | What changes on the client machine |
| --- | --- |
| `--operator-only` | GUI and console agent operations without a TUN device, route installation, or routing elevation |
| `--internal` | Creates a tunnel for agent networks; normal Internet routing stays in place |
| `--vpn` | Routes IPv4 Internet traffic through the server; agent routes can also be explicitly accepted |
| `--vpn --internal` | Combines Internet egress, accepted agent routes, and global server internal routes |

The GUI is enabled by default for all modes. Host commands, live shells, files, screenshots, modules, Jobs, payload management, and Jump do not require an accepted route: they travel over the authenticated control connection. Use a routing client for local application access and client-service forwards. Every client needs an operator account as well as transport enrollment.

## One-time setup

On **SERVER**, build the operator binary and agent templates, then generate an identity and enrollment token:

```sh
sh tools/build-release.sh bin
./bin/undertow init
./bin/undertow operators bootstrap
```

Record the printed `FINGERPRINT`. Bootstrap prompts for the first Team Leader ID and password; it runs only once against an empty account database. Securely copy `token.key` to each **CLIENT** and **AGENT** and keep `identity.key` on **SERVER**. The examples use default key filenames. Client terminal starts prompt for operator credentials; background starts need explicit `--operator ID --operator-password-file PATH` or environment equivalents. See [Operator authentication](operator-authentication.md).

The server generates temporary self-signed TLS for QUIC/WebSocket. For these certificates, peers use `--tls-insecure-skip-verify` while checking the separately pinned Undertow `--fingerprint`.

## Accept and manage routes in the GUI

Open the client's printed GUI URL, then **Routes**. Select an agent under **Routes accepted by this client**, choose **Advertised** and its actual CIDR, then **Accept and install route**. For a reachable but unadvertised subnet, use **Custom CIDR**. Overview also offers acceptance.

Check **Saved local routes** for **Installed**, then test a real host/service from an application on **that client machine**. **Disable** withdraws the route while saving the choice, **Enable** restores it, and **Remove** deletes it. Routes on other clients do not install on yours. Operator-only clients can inspect but cannot install routes.

**Settings → Client** changes Internet egress and global internal routing on a client with a tunnel. VPN off removes Internet routes but keeps explicit accepted agent routes. Internal off changes global server routing for new flows; it does not disable explicit accepted routes. Existing flows keep their path. Settings persist over carrier reconnects within the worker; restart uses startup flags.

An enabled route holds a check-in agent connected even when idle. The [GUI routing guide](gui.md#reach-a-remote-network-from-your-machine) includes the route-toggle animation and explains installed, saved, and shared state. Use **Routes → Server configured routes → Add server route** to define a shared server path separately from client acceptance. The terminal workflows below remain available.

## Internet through the server: client `--vpn`

**Goal:** route a client's IPv4 Internet traffic through **SERVER**. Run **SERVER** on a reachable host and **CLIENT** on the machine whose traffic should use the tunnel. No agent and no server `--tun` are needed.

**Start** in separate terminals:

```sh
# SERVER
sudo ./bin/undertow server

# CLIENT
sudo ./bin/undertow client --vpn --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
```

**Use the consoles:** `status` on either host shows the active QUIC session. **Verify on CLIENT:** `curl -4 https://api.ipify.org` should show the server's public IPv4 address. **Background/stop:** `background` detaches either console while its worker continues; `sudo ./bin/undertow client attach` or `sudo ./bin/undertow server attach` returns. `quit` in the client console stops the client and removes its VPN routes. `stop` in the server console shuts down the server.

## An agent's network from a client: client `--internal`

**Goal:** reach `10.20.0.0/16` from **CLIENT** through **AGENT** while the client's ordinary Internet route stays in place. **AGENT** runs on a host that can already reach that network. **SERVER** relays the sessions and needs no `--tun`.

**Start** in separate terminals:

```sh
# SERVER
sudo ./bin/undertow server

# AGENT; no root required
./bin/undertow agent --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify --advertise-route 10.20.0.0/16

# CLIENT; root is needed for its local TUN
sudo ./bin/undertow client --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
```

**Use the CLIENT console:**

```text
agents
use 1
show
routes
route accept 10.20.0.0/16
```

Use `route add 10.20.0.0/16` instead if the agent can reach the network but has not advertised it. **Verify on CLIENT:** `curl http://10.20.0.50/` (replace with a reachable target); ordinary Internet access should still use the client's normal connection. **Background/stop:** use `background` and `client attach` to leave and return to the console. `quit` stops the client and removes its active routes; Ctrl+C stops the agent; `stop` shuts down the server.

## Internet and an agent's network: client `--vpn --internal`

**Goal:** combine the two paths above on **CLIENT**. Run **SERVER** and **AGENT** as in the previous example, then start **CLIENT** with both flags:

```sh
sudo ./bin/undertow client --vpn --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
```

**Use the CLIENT console:** `agents`, `use 1`, `routes`, `route accept 10.20.0.0/16`. **Verify on CLIENT:** test both `curl http://10.20.0.50/` and `curl -4 https://api.ipify.org`. **Background/stop:** `background` detaches, `client attach` returns, and `quit` stops the client and restores its routes. Stop the agent with Ctrl+C and the server with console `stop`.

## When does SERVER need `--tun`?

Only when an application **on the server host itself** must reach an internal network through an agent. Start `sudo ./bin/undertow server --tun`, connect the agent as above, then in the **SERVER** console run `agents`, `use 1`, `routes`, `route add 10.20.0.0/16`. Verify from **SERVER** with `curl http://10.20.0.50/`. `stop` closes the server's temporary TUN and routes.

For client `--vpn`, client `--internal`, or both, the **client creates its own TUN** and the server does **not** need `--tun`. A server's Internet egress uses ordinary sockets. `--tun` does not enable extra transports.

## Choose another carrier

The same server accepts all three by default; each agent and client chooses independently:

| Peer flags | Path | When to use |
| --- | --- | --- |
| `--transport quic --server SERVER_IP:443 --tls-insecure-skip-verify` | UDP/443 | Direct QUIC path; used above. |
| `--transport websocket --server SERVER_IP:443 --tls-insecure-skip-verify` | TCP/443 | UDP/443 unavailable but HTTPS traffic works. |
| `--transport dns --server SERVER_IP:53` | UDP/53 | DNS is the available outbound path. No TLS flag. |

Keep `--fingerprint FINGERPRINT` with every carrier. A `--vpn` client using DNS is the **DNS VPN** case: it needs no agent and no server TUN, and can help where direct UDP DNS is the available path, including some restrictive or captive portal networks. If the server has a trusted TLS certificate, configure it with `--tls-cert` and `--tls-key`; clients can then omit `--tls-insecure-skip-verify` when the certificate validates for the address they use. `status` or `transports` in the server console lists every active listener and connected peer's carrier. See [transport flags](cli-reference.md#transport-selection) and [topology and relays](topology-and-relays.md) for advanced layouts.

Child agents can instead reach an explicitly started **parent agent relay**. Use `relay` for TCP or, on Windows, `relay-smb` for a named pipe. These are child-to-parent carriers; they are not server transport listeners and cannot be selected by `start transport`. The parent keeps its own DNS, WebSocket, QUIC, or upstream relay connection to the same authoritative server. A relay is opened only by `relay start BIND` in the server or client console, or by an explicit action in the GUI. See [topology and relays](topology-and-relays.md), [SMB named-pipe relays](smb-named-pipe-relays.md), and [payload deployment](agent-distribution.md#host-a-payload-through-a-connected-agent) for building and delivering child artifacts.

## Scripts and further examples

The examples above use the consoles to inspect and accept routes. For automation, use `server --background` or `client --background`, `status --json`, and standalone commands such as `route add 10.20.0.0/16 --via AGENT_ID`. For a foreground worker under a service manager, use explicit `--foreground`. See the [CLI reference](cli-reference.md) for flags and scripting, the [console guide](console.md) for daily operation, and [deployment scenarios](scenarios.md) for forwarding and multi-agent routes.

Back to [documentation home](README.md).
