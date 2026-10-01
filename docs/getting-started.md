# Getting started

## What runs where?

- **Server:** the reachable carrier listeners and local operator API. It creates the server identity and accepts agent and VPN client sessions. Use `--tun` only when applications on the server host itself need routes through an agent; VPN Internet egress uses server sockets.
- **Agent:** a connector on the internal network. It opens TCP, UDP, and ICMP operations for the server. It needs no root/Administrator privilege, virtual adapter, route changes, or inbound port.
- **Client:** a separate, elevated process that creates its own TUN/Wintun. `--vpn` changes Internet routes; `--internal` alone adds only internal routes through agents; both flags combine them.
- **Operator commands:** `status`, `agent list/show/select`, `route add/del/list`, and `session kill` run on the server host against a token protected loopback API.

The key distinction is **which machine's traffic changes**. An agent exposes destinations reachable *from the agent host* and leaves that host's normal networking alone. A client routes applications *on the client host* into the tunnel for its selected prefixes. Starting a client does not expose its local network as an agent; starting an agent does not give its own host VPN Internet egress. Internal access needs a connected agent and a route to it.

## Client routing modes

Run one of these on the elevated **VPN client** host after starting the server and, for internal access, an agent:

```sh
sudo undertow client --vpn --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
sudo undertow client --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
sudo undertow client --vpn --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
```

`--vpn` installs two IPv4 `/1` routes and verifies public egress. `--internal` alone pins the carrier server route and creates the TUN without changing the Internet/default route or requiring a public egress check. Once connected, type `agents`, `use 1`, and `routes` in the client console; use `route accept CIDR` for an advertised subnet or `route add CIDR` for another network reachable from that agent. This setup needs no server route command. Server configured routes are also supported when an operator wants global route management. Both flags provide VPN Internet egress and agent routes. At least one flag is required. See [deployment scenarios](scenarios.md) for tests.

These examples use **QUIC UDP/443** with the server's automatic self-signed TLS certificate and a separately pinned Undertow fingerprint. The server also starts HTTPS/WebSocket TCP/443 and direct DNS UDP/53 by default; peers choose any active carrier independently. See [Quickstart transport choices](quickstart.md#choose-another-carrier). Allow the chosen port through the server firewall. UDP/53 and virtual interfaces commonly need elevated privileges.

## Build and first session

Build Linux with `mkdir -p bin && go build -buildvcs=false -o bin/undertow ./cmd/undertow`. In Windows PowerShell use:

```powershell
New-Item -ItemType Directory -Force .\bin | Out-Null
go build -buildvcs=false -o .\bin\undertow.exe .\cmd\undertow
if ($LASTEXITCODE -ne 0) { throw 'Build failed; bin\undertow.exe may be an older version' }
.\bin\undertow.exe help agent
```

In the examples below, `undertow` means `./bin/undertow` on Linux or `.\bin\undertow.exe` on Windows. Run from a directory containing your credential files, or use absolute paths.

On the server:

```sh
undertow init
sudo undertow server
```

The server prints a fingerprint. Copy `token.key` securely to the agent host, then run there:

```sh
undertow agent --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
```

The terminal server command opens its operator console. Type `agents`, `use 1`, and `show` to inspect the agent, or `status` for the full view. A connected VPN client appears under **VPN clients**; it needs no agent route for Internet egress. On the server, `background` or `quit` detaches, `sudo undertow server attach` returns, and `stop` shuts the worker down gracefully. Stop the foreground agent with Ctrl+C. The server identity stays on the server. Each agent and VPN client creates its **own** Ed25519 key file (`agent.key` or `client.key`) on first run. Never copy `identity.key` to a client.

## Enrollment choices

All modes use an encrypted session and a server identity fingerprint. `--auth` controls *who may enroll*:

| Mode | Server | Agent or VPN client | What to share |
| --- | --- | --- | --- |
| Token (default) | `--auth token --token-file token.key` or `--auth token --token TOKEN_HEX` | same token through either flag | Copy the file securely or share its hex value securely |
| Password | `--auth password --password-file password.key` | same flags | Share the password securely; each host writes its own restricted file |
| Open | `--auth none` | `--auth none` | No enrollment secret; anyone who reaches the carrier listener may enroll |

You can copy `token.key` as a file, or paste the server's hex token into a PowerShell file with `echo "TOKEN_HEX" > token.key`. Undertow accepts PowerShell's UTF-16LE text as well as UTF-8. Use the exact token generated on the server; a different token will fail enrollment. Treat the token as a secret.

To avoid creating a token file on a host, pass its hex value with `--token TOKEN_HEX` on that host. Both server and connecting hosts accept this flag; each must use the same value. Do not combine `--token` with `--token-file`. A literal `--token` value is visible in process listings and may remain in shell history, so a restricted token file is safer on shared hosts.

Password mode requires at least 12 bytes. Use `--password-file` to avoid showing the password in a process command line; `--password TEXT` is available for temporary use. The password file's trailing newline is ignored. Token mode remains the default, and a `--token-file` is irrelevant to password or open mode. Open enrollment still encrypts transport and authenticates the server **when its fingerprint is pinned**; it provides no admission control. **For real deployments, use token or password enrollment.** With `--auth none`, anyone who can reach the listener can enroll, access network paths, and use operations that agents have not restricted with `--deny`.

The server can be initialized once with `undertow init` even when you choose password or open mode. `init` also creates an unused token file; the server identity and fingerprint are what those modes need.

## Server fingerprint

The safest first connection passes the fingerprint printed by `init` or server startup through a trusted channel:

```sh
undertow agent --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
```

If distributing the fingerprint is impractical, use `--trust-on-first-use` on the agent or VPN client:

```sh
undertow agent --transport quic --server SERVER_IP:443 --trust-on-first-use --tls-insecure-skip-verify
```

The fingerprint is discovered from the server, then saved to `server.fingerprint` **after the authenticated session succeeds**. Later runs load that file without `--trust-on-first-use`. Use `--fingerprint-file PATH` for a separate pin per server. The first contact can be intercepted, so use a trusted first network or verify the saved fingerprint independently before relying on it. With `--auth none`, first use has no enrollment secret to authenticate the remote endpoint; out-of-band verification is particularly important. If the server identity changes, a saved pin causes connection failure until you deliberately replace it.

## Local operator API

The server listens on `127.0.0.1:47889` for operator commands. It creates `control.key` for that API, separate from `token.key`. Keep it private. Override the listener with `--control-listen` and use matching `--control` plus `--control-token-file` on commands. The control address must be numeric loopback.

Run operator commands as an account allowed to read `control.key`. When the server first creates it under `sudo`, use `sudo` for `status`, `agent list/show/select`, `route`, and `session` commands too, or arrange restricted file access for your operator account.

See [scenarios](scenarios.md) for working topologies and [CLI reference](cli-reference.md) for every flag.
