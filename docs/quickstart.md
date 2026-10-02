# Quickstart

## Configured agent artifact

Build the release binaries with `sh tools/build-release.sh bin` (or `tools/build-release.ps1` on Windows), then start the server. Its `bin` directory should contain the thin-agent templates. In the server console:

```text
agent profile create office server=SERVER_IP:443 transport=quic
agent build office windows amd64
agent host ARTIFACT_ID
```

Use the random URL printed by `agent host` to retrieve the executable, verify the printed SHA-256, and run it on the target with **no agent arguments**. `agent deploy-script ARTIFACT_ID powershell` can print a small download-and-run helper. Confirm with `agents`, `use 1`, and `show`. `session kill` closes the session and permits reconnect; `agent shutdown` stops the configured process after acknowledgement. `agent unhost` disables download, `agent revoke` blocks future enrollment, and `agent delete` removes the server-side artifact record and file. Hosting requires the server's WebSocket HTTPS listener. See [configured thin agents](agent-distribution.md) for the full workflow and endpoint state.

Use **QUIC on UDP/443** for these examples. The server also listens on WebSocket TCP/443 and DNS UDP/53 by default. Open UDP/443 in the server firewall. Run commands from each host's Undertow directory; `./bin/undertow` is the built Linux binary. On Windows, use `.\bin\undertow.exe`. Replace `SERVER_IP`, `FINGERPRINT`, and the example network with your values.

## One-time setup

On **SERVER**, generate an identity and enrollment token:

```sh
./bin/undertow init
```

Record the printed `FINGERPRINT`. Securely copy `token.key` to each **CLIENT** and **AGENT**. Keep `identity.key` on **SERVER**. The commands below use the default key filenames, so no key flags are needed. The server generates a temporary self-signed TLS certificate for QUIC and WebSocket. For an IP address and that certificate, peers need `--tls-insecure-skip-verify`; they still verify the separate pinned Undertow `--fingerprint`.

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

## Scripts and further examples

For automation, use `server --background` or `client --background`, `status --json`, and standalone commands such as `route add 10.20.0.0/16 --via AGENT_ID`. For a foreground worker under a service manager, use explicit `--foreground`. See the [CLI reference](cli-reference.md), [console guide](console.md), [deployment scenarios](scenarios.md), and [feature catalogue](../features.md) for forwarding, relays, capabilities, and more.
